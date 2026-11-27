package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// secretStore is the minimal credential seam for API pulls; the billing
// package never sees plaintext credentials beyond the adapter call.
type secretStore interface {
	Decrypt(cipherText string) (string, error)
}

// Service imports vendor bills and records the run outcome (F-0521~F-0534).
type Service struct {
	db         *gorm.DB
	secrets    secretStore
	adapterFor func(storage.Provider, string) (cloudprov.Adapter, error)
}

// New builds the billing service. A nil adapter factory uses the registered
// cloudprov factories.
func New(db *gorm.DB, secrets secretStore, adapterFor func(storage.Provider, string) (cloudprov.Adapter, error)) *Service {
	if adapterFor == nil {
		adapterFor = func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
			return cloudprov.NewAdapter(cloudprov.Account{
				ProviderID: p.ID, Name: p.Name, Type: p.Type, AccessKey: plain, Region: p.Region,
			})
		}
	}
	instrumentCostFreshness(db)
	return &Service{db: db, secrets: secrets, adapterFor: adapterFor}
}

// ImportRequest is one API pull (F-0524~F-0528).
type ImportRequest struct {
	// CostProviderID identifies the billing source.
	CostProviderID uint
	// Window is the requested billing period; a zero To means "up to now".
	Window cloudprov.BillingWindow
	// Account / Region narrow the pull (F-0526); empty = whole source.
	Account string
	Region  string
	// Trigger is manual/scheduled/backfill; backfill ignores the incremental
	// watermark (F-0528).
	Trigger string
	// PageSize, Page implement pagination over the returned records (F-0527).
	// Zero PageSize means one page with all records.
	PageSize int
	Page     int
	// Incremental skips records whose bill id was already imported unless the
	// vendor supplied a newer revision (F-0527/F-0529).
	Incremental bool
}

// FileRecord is one row of a manually uploaded bill file (F-0523/F-0534).
type FileRecord struct {
	BillID      string    `json:"bill_id"`
	Revision    string    `json:"revision,omitempty"`
	ItemID      string    `json:"item_id,omitempty"`
	ResourceID  string    `json:"resource_id,omitempty"`
	Region      string    `json:"region,omitempty"`
	Category    string    `json:"category,omitempty"`
	Description string    `json:"description,omitempty"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency,omitempty"`
	Usage       int64     `json:"usage,omitempty"`
	Unit        string    `json:"unit,omitempty"`
	IncurredAt  time.Time `json:"incurred_at,omitempty"`
	PeriodStart time.Time `json:"period_start,omitempty"`
	PeriodEnd   time.Time `json:"period_end,omitempty"`
	TaxCents    int64     `json:"tax_cents,omitempty"`
	PreTaxCents int64     `json:"pre_tax_cents,omitempty"`
	// FX conversion (F-0595/F-0596): an importer-supplied rate converts the
	// bill into the report currency; source and date are kept with the record.
	FXRate       float64 `json:"fx_rate,omitempty"`
	FXRateSource string  `json:"fx_rate_source,omitempty"`
	FXRateDate   string  `json:"fx_rate_date,omitempty"` // RFC3339 or YYYY-MM-DD
}

// ImportFromAPI pulls the vendor billing API, validates the declared
// capability (F-0522, fail closed) and stores deduped records. A vendor or
// capability failure is written to the run log with the reason and retry
// state (F-0532) and returned to the caller.
func (s *Service) ImportFromAPI(ctx context.Context, req ImportRequest, actor string, now time.Time) (*storage.BillImport, error) {
	if req.Trigger == "" {
		req.Trigger = TriggerManual
	}
	if !ValidTrigger(req.Trigger) {
		return nil, fmt.Errorf("invalid trigger %q", req.Trigger)
	}
	src, prov, err := s.source(req.CostProviderID)
	if err != nil {
		return nil, err
	}
	run := &storage.BillImport{
		CostProviderID: src.ID, Source: SourceAPI, Trigger: req.Trigger,
		PeriodStart: req.Window.From, PeriodEnd: req.Window.To,
		Account: req.Account, Region: req.Region,
		State: RunRunning, StartedAt: now,
	}
	if err := s.db.Create(run).Error; err != nil {
		return nil, err
	}
	finish := func(state string, err error) (*storage.BillImport, error) {
		run.State = state
		if err != nil {
			run.Error = truncate(err.Error(), 512)
		}
		done := time.Now()
		run.FinishedAt = &done
		recordSync(run.Source, state, done.Sub(run.StartedAt))
		if state == RunFailed {
			alertSyncFailed(s.db, run, done)
		}
		if saveErr := s.db.Save(run).Error; saveErr != nil {
			return run, saveErr
		}
		return run, err
	}
	if prov == nil {
		return finish(RunFailed, errors.New("cost source has no linked cloud provider"))
	}
	plain, err := s.secrets.Decrypt(prov.AccessKey)
	if err != nil {
		return finish(RunFailed, fmt.Errorf("credential unreadable: %w", err))
	}
	adapter, err := s.adapterFor(*prov, plain)
	if err != nil {
		return finish(RunFailed, err)
	}
	acct := cloudprov.Account{ProviderID: prov.ID, Name: prov.Name, Type: prov.Type}
	caps, err := adapter.Capabilities(ctx)
	if err != nil {
		// A credentials rejection rides the auth alert (F-1828); other
		// failures just fail the run.
		cloudprov.ReportAuthFailure(acct, cloudprov.OpCapabilities, "", err)
		return finish(RunFailed, err)
	}
	// F-0522: the API capability is declared by the adapter; an unsupported
	// source fails closed with a manual-flow hint (F-0534).
	if !caps.Billing {
		return finish(RunFailed, errors.New("provider does not declare a billing API; use manual file import"))
	}
	recs, err := adapter.ImportBilling(ctx, req.Window)
	if err != nil {
		cloudprov.ReportAuthFailure(acct, cloudprov.OpImportBilling, "", err)
		return finish(RunFailed, err)
	}
	run.Fetched = len(recs)
	page, total := paginate(recs, req.Page, req.PageSize)
	if total < 0 {
		return finish(RunFailed, errors.New("invalid pagination"))
	}
	if req.PageSize > 0 && req.Page > 0 && page == nil {
		return finish(RunFailed, fmt.Errorf("page %d is past the last page (%d records)", req.Page, total))
	}
	converted := make([]FileRecord, 0, len(page))
	for _, r := range page {
		if NormalizeBillID(r.ResourceID) == "" {
			run.Skipped++
			continue
		}
		// cloudprov carries the vendor-side record id as ResourceID; key the
		// bill row on it so a repeated pull dedupes instead of double-billing.
		converted = append(converted, FileRecord{
			BillID: r.ResourceID, ResourceID: r.ResourceID,
			AmountCents: r.CostCents, Currency: r.Currency,
			PeriodStart: r.From, PeriodEnd: r.To, IncurredAt: r.To,
		})
	}
	added, updated, skipped, err := s.storeRows(src, SourceAPI, converted, actor, now)
	run.Added, run.Updated, run.Skipped = added, updated, skipped+run.Skipped
	if err != nil {
		return finish(RunPartial, err)
	}
	src.LastSyncAt = &now
	src.LastError = ""
	if err := s.db.Save(&src).Error; err != nil {
		return finish(RunPartial, err)
	}
	return finish(RunSucceeded, nil)
}

// ImportFile stores a manually uploaded bill file (F-0523/F-0534). The raw
// rows are snapshotted on each record, so later parser changes never rewrite
// history.
func (s *Service) ImportFile(srcID uint, raw []byte, format, actor string, now time.Time) (*storage.BillImport, error) {
	src, _, err := s.source(srcID)
	if err != nil {
		return nil, err
	}
	run := &storage.BillImport{
		CostProviderID: src.ID, Source: SourceFile, Trigger: TriggerManual,
		State: RunRunning, StartedAt: now, RawRef: fmt.Sprintf("file:%s:%d", format, len(raw)),
	}
	if err := s.db.Create(run).Error; err != nil {
		return nil, err
	}
	finish := func(state string, err error) (*storage.BillImport, error) {
		run.State = state
		if err != nil {
			run.Error = truncate(err.Error(), 512)
		}
		done := time.Now()
		run.FinishedAt = &done
		recordSync(run.Source, state, done.Sub(run.StartedAt))
		if state == RunFailed {
			alertSyncFailed(s.db, run, done)
		}
		if saveErr := s.db.Save(run).Error; saveErr != nil {
			return run, saveErr
		}
		return run, err
	}
	var rows []FileRecord
	if format == "json" {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return finish(RunFailed, fmt.Errorf("parse json bill file: %w", err))
		}
	} else {
		return finish(RunFailed, fmt.Errorf("unsupported bill file format %q", format))
	}
	run.Fetched = len(rows)
	added, updated, skipped, err := s.storeRows(src, SourceFile, rows, actor, now)
	run.Added, run.Updated, run.Skipped = added, updated, skipped
	if err != nil {
		return finish(RunPartial, err)
	}
	src.LastSyncAt = &now
	src.LastError = ""
	if err := s.db.Save(&src).Error; err != nil {
		return finish(RunPartial, err)
	}
	return finish(RunSucceeded, nil)
}

func (s *Service) source(id uint) (storage.CostProvider, *storage.Provider, error) {
	var src storage.CostProvider
	if err := s.db.First(&src, id).Error; err != nil {
		return src, nil, err
	}
	if src.ProviderID == nil {
		return src, nil, nil
	}
	var prov storage.Provider
	if err := s.db.First(&prov, *src.ProviderID).Error; err != nil {
		return src, nil, err
	}
	return src, &prov, nil
}

// storeRows persists parsed rows under the dedupe key (F-0512), attributing
// each item to its resource/node where the resource is bound (F-0562). Rows
// sharing a bill id + revision are line items of ONE record; a second import
// of the same bill dedupes instead of double-billing (F-0529).
func (s *Service) storeRows(src storage.CostProvider, source string, rows []FileRecord, actor string, now time.Time) (added, updated, skipped int, err error) {
	type billGroup struct {
		key      string
		rec      storage.CostRecord
		items    []FileRecord
		itemIdxs []int
	}
	groups := map[string]*billGroup{}
	var order []string
	for i, row := range rows {
		billID := NormalizeBillID(row.BillID)
		if billID == "" {
			// A row without its original id is untraceable and undedupable.
			skipped++
			continue
		}
		key := RecordKey(source, src.ID, billID, row.Revision)
		g, ok := groups[key]
		if !ok {
			currency := row.Currency
			if currency == "" {
				currency = src.Currency
			}
			periodStart, periodEnd := row.PeriodStart, row.PeriodEnd
			if periodStart.IsZero() {
				periodStart = startOfMonth(row.IncurredAt)
			}
			if periodEnd.IsZero() {
				periodEnd = endOfMonth(periodStart)
			}
			incurred := row.IncurredAt
			if incurred.IsZero() {
				incurred = periodEnd
			}
			g = &billGroup{key: key, rec: storage.CostRecord{
				CostProviderID: src.ID, Source: source, BillID: billID, Revision: row.Revision,
				Account: src.Account, Region: row.Region, Currency: currency,
				IncurredAt: incurred, PeriodStart: periodStart, PeriodEnd: periodEnd,
				ImportedAt: now, ImportedBy: actor, Status: StatusSettled,
				DedupeKey: key,
			}}
			groups[key] = g
			order = append(order, key)
		}
		// The record total is the sum of its line items; the tax split comes
		// from the first row that supplies one (F-0510), the FX conversion
		// from the first row that carries a rate (F-0595/F-0596).
		g.rec.AmountCents += row.AmountCents
		if g.rec.TaxCents == 0 && g.rec.PreTaxCents == 0 {
			g.rec.TaxCents, g.rec.PreTaxCents = row.TaxCents, row.PreTaxCents
		}
		if g.rec.FXRate == 0 && row.FXRate > 0 {
			g.rec.FXRate = row.FXRate
			g.rec.FXRateSource = row.FXRateSource
			if at, perr := parseFXDate(row.FXRateDate); perr == nil && !at.IsZero() {
				g.rec.FXRateDate = &at
			}
			g.rec.ReportAmountCents = int64(float64(row.AmountCents) * row.FXRate)
		}
		if row.Region != "" && g.rec.Region == "" {
			g.rec.Region = row.Region
		}
		g.items = append(g.items, row)
		g.itemIdxs = append(g.itemIdxs, i)
	}
	for _, key := range order {
		g := groups[key]
		g.rec.RawSnapshot = RawJSON(g.items)
		res := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&g.rec)
		if res.Error != nil {
			return added, updated, skipped, res.Error
		}
		if res.RowsAffected == 0 {
			// Same bill + revision already stored: deduped (F-0529), counted as
			// skipped so the run log shows the re-import was a no-op.
			skipped += len(g.items)
			continue
		}
		added++
		for j, row := range g.items {
			itemID := row.ItemID
			if itemID == "" {
				itemID = fmt.Sprintf("%s-%d", g.rec.BillID, g.itemIdxs[j])
			}
			nodeID := s.nodeForResource(src, row.ResourceID)
			item := storage.CostItem{
				CostRecordID: g.rec.ID, ResourceID: row.ResourceID, NodeID: nodeID,
				Region: regionFallback(src, row.Region), Category: normalizeCategory(row.Category),
				AmountCents: row.AmountCents, Usage: row.Usage, Unit: row.Unit,
				Description: row.Description, DedupeKey: ItemKey(key, itemID),
			}
			if err := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&item).Error; err != nil {
				return added, updated, skipped, err
			}
		}
	}
	return added, updated, skipped, nil
}

// nodeForResource resolves the Ferry Node bound to a provider-side resource
// (F-0562): direct attribution is a lookup, never a guess.
func (s *Service) nodeForResource(src storage.CostProvider, resourceID string) *uint {
	if src.ProviderID == nil || resourceID == "" {
		return nil
	}
	var res storage.CloudResource
	if err := s.db.Where("provider_id = ? AND external_id = ?", *src.ProviderID, resourceID).
		First(&res).Error; err != nil {
		return nil
	}
	return res.NodeID
}

// regionFallback resolves the item region: the vendor row's region wins, the
// source's configured scope fills a blank (F-0526).
func regionFallback(src storage.CostProvider, region string) string {
	if region != "" {
		return region
	}
	return src.Region
}

func normalizeCategory(c string) string {
	switch c {
	case CategoryInstance, CategoryTraffic, CategoryBandwidth, CategoryIP,
		CategoryDisk, CategorySnapshot, CategoryBackup, CategoryDiscount:
		return c
	}
	return CategoryOther
}

func paginate[T any](rows []T, page, size int) ([]T, int) {
	if size <= 0 {
		return rows, len(rows)
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * size
	if start >= len(rows) {
		return nil, len(rows)
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], len(rows)
}

func startOfMonth(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

func endOfMonth(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	y, m, _ := t.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, t.Location()).Add(-time.Nanosecond)
}

// parseFXDate accepts the rate date as RFC3339 or a plain calendar day.
func parseFXDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

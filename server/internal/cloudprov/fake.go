package cloudprov

import (
	"context"
	"sort"
	"sync"
)

// Fake is the reference Adapter implementation (F-0302~F-0318): an in-memory
// resource set with a full capability declaration. It doubles as the test
// double for the contract suite and as the shape future real vendor adapters
// and handler-layer tests code against.
type Fake struct {
	mu   sync.Mutex
	caps Capabilities
	res  map[string]*Resource
	// failHook, when set, fails the named op with the returned error before
	// any work -- used to exercise retry and mapping behavior.
	failHook func(op, resourceID string) *Error

	// opLog records every invoked op in order (calls that fail via the hook
	// are logged too; capability-gated rejections are not).
	opLog []string
}

// NewFake builds a fake with power control and billing import, an empty
// resource set, and API version 2.1.
func NewFake() *Fake {
	return &Fake{
		caps: Capabilities{
			APIVersion: "2.1",
			Power:      true,
			ForceStop:  true,
			Rebuild:    true,
			Billing:    true,
			Regions:    []string{"fra1", "nyc1"},
		},
		res: map[string]*Resource{},
	}
}

func (f *Fake) log(op string) {
	f.mu.Lock()
	f.opLog = append(f.opLog, op)
	f.mu.Unlock()
}

func (f *Fake) fail(op, resourceID string) *Error {
	f.mu.Lock()
	hook := f.failHook
	f.mu.Unlock()
	if hook != nil {
		return hook(op, resourceID)
	}
	return nil
}

// Seed adds or replaces a resource and returns it (State defaults to
// stopped when unset).
func (f *Fake) Seed(r Resource) Resource {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.State == "" {
		r.State = StateStopped
	}
	f.res[r.ResourceID] = &r
	return r
}

// Unseed drops a resource, simulating a provider-side deletion for the sync
// reconciliation tests (F-0350/F-0351); it reports whether the id existed.
func (f *Fake) Unseed(resourceID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.res[resourceID]; !ok {
		return false
	}
	delete(f.res, resourceID)
	return true
}

// SetCaps replaces the declared capability set (e.g. to test a no-power
// vendor).
func (f *Fake) SetCaps(caps Capabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caps = caps
}

// Calls returns the ordered op log (a copy).
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opLog...)
}

func (f *Fake) Capabilities(ctx context.Context) (Capabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.caps, nil
}

func (f *Fake) ListResources(ctx context.Context, flt Filter) ([]Resource, error) {
	f.log(OpListResources)
	if e := f.fail(OpListResources, ""); e != nil {
		return nil, e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Resource, 0, len(f.res))
	for _, r := range f.res {
		if flt.Region != "" && r.Region != flt.Region {
			continue
		}
		if len(flt.Tags) > 0 {
			ok := true
			for k, v := range flt.Tags {
				if r.Tags[k] != v {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out, nil
}

func (f *Fake) DescribeResource(ctx context.Context, resourceID string) (*Resource, error) {
	f.log(OpDescribe)
	if e := f.fail(OpDescribe, resourceID); e != nil {
		return nil, e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.res[resourceID]
	if !ok {
		return nil, NewError(ErrNotFound, OpDescribe, resourceID, "resource_not_found", "no such resource")
	}
	out := *r
	return &out, nil
}

func (f *Fake) RefreshStatus(ctx context.Context, resourceID string) (string, error) {
	f.log(OpRefreshStatus)
	if e := f.fail(OpRefreshStatus, resourceID); e != nil {
		return "", e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.res[resourceID]
	if !ok {
		return "", NewError(ErrNotFound, OpRefreshStatus, resourceID, "resource_not_found", "no such resource")
	}
	return r.State, nil
}

// transition runs one state change through the capability gate (fail closed,
// F-0303) and the existence check. gate nil = always allowed.
func (f *Fake) transition(ctx context.Context, op, resourceID, to string, gate func(Capabilities) bool) error {
	f.log(op)
	if e := f.fail(op, resourceID); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if gate != nil && !gate(f.caps) {
		return ErrUnsupportedOp(op, resourceID)
	}
	r, ok := f.res[resourceID]
	if !ok {
		return NewError(ErrNotFound, op, resourceID, "resource_not_found", "no such resource")
	}
	r.State = to
	return nil
}

func (f *Fake) PowerOn(ctx context.Context, resourceID string) error {
	return f.transition(ctx, OpPowerOn, resourceID, StateStarting,
		func(c Capabilities) bool { return c.Power })
}

func (f *Fake) PowerOff(ctx context.Context, resourceID string) error {
	return f.transition(ctx, OpPowerOff, resourceID, StateStopping,
		func(c Capabilities) bool { return c.Power })
}

func (f *Fake) Reboot(ctx context.Context, resourceID string) error {
	return f.transition(ctx, OpReboot, resourceID, StateStarting,
		func(c Capabilities) bool { return c.Power })
}

func (f *Fake) ForceStop(ctx context.Context, resourceID string) error {
	return f.transition(ctx, OpForceStop, resourceID, StateStopped,
		func(c Capabilities) bool { return c.ForceStop })
}

func (f *Fake) Rebuild(ctx context.Context, resourceID, image string) error {
	return f.transition(ctx, OpRebuild, resourceID, StateRebuilding,
		func(c Capabilities) bool { return c.Rebuild })
}

func (f *Fake) ImportBilling(ctx context.Context, w BillingWindow) ([]BillingRecord, error) {
	f.log(OpImportBilling)
	if e := f.fail(OpImportBilling, ""); e != nil {
		return nil, e
	}
	f.mu.Lock()
	caps := f.caps
	f.mu.Unlock()
	if !caps.Billing {
		return nil, ErrUnsupportedOp(OpImportBilling, "")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	recs := make([]BillingRecord, 0)
	for _, r := range f.res {
		// F-0313 zero bandwidth = unknown: no billing datum for it.
		if r.BwMbps <= 0 {
			continue
		}
		recs = append(recs, BillingRecord{
			ResourceID: r.ResourceID,
			CostCents:  int64(r.BwMbps) * 3, // fake per-Mbps price for the window
			Currency:   "USD",
			From:       w.From,
			To:         w.To,
		})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ResourceID < recs[j].ResourceID })
	return recs, nil
}

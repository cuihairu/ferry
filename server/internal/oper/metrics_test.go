package oper

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// histOpCount reads a labelled histogram child's _count from its exposition.
func histOpCount(t *testing.T, op string) float64 {
	t.Helper()
	out, err := testutil.CollectAndFormat(opDuration, expfmt.TypeTextPlain,
		"ferry_cloud_op_seconds")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^` +
		regexp.QuoteMeta(`ferry_cloud_op_seconds_count{op="`+op+`"}`) + ` ([0-9.eE+-]+)$`)
	m := re.FindStringSubmatch(bytes.NewBuffer(out).String())
	if m == nil {
		return 0 // series created on first observation
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("op %s count %q: %v", op, m[1], err)
	}
	return v
}

// TestOperMetrics covers F-1812: task settlements count per op and result,
// executed settlements observe their wall time, and a reconciled Unknown
// counts both the unknown settlement and the reconciled success.
func TestOperMetrics(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-ops", Name: "one", Region: "fra1",
		State: cloudprov.StateStopped})
	p, res := operTestAccount(t, db, f)
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")
	ctx := context.Background()

	okChild := opOutcome.WithLabelValues(cloudprov.OpPowerOn, StateSucceeded)
	unknownChild := opOutcome.WithLabelValues(cloudprov.OpPowerOn, StateUnknown)
	cancelChild := opOutcome.WithLabelValues(cloudprov.OpPowerOn, StateCancelled)
	okBefore, unknownBefore, cancelBefore :=
		testutil.ToFloat64(okChild), testutil.ToFloat64(unknownChild), testutil.ToFloat64(cancelChild)
	countBefore := histOpCount(t, cloudprov.OpPowerOn)

	// A successful boot: counted succeeded with one duration observation.
	if _, _, err := s.Create(ctx, Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
	}, "tester", "admin", time.Now()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Run(ctx, time.Now()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := testutil.ToFloat64(okChild) - okBefore; got != 1 {
		t.Fatalf("succeeded delta = %v, want 1", got)
	}
	if got := histOpCount(t, cloudprov.OpPowerOn) - countBefore; got != 1 {
		t.Fatalf("histogram count delta = %v, want 1", got)
	}

	// Timeout -> Unknown, then reconcile settles it succeeded: both events count.
	f.SetFailHook(cloudprov.OpPowerOn, func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrUnavailable, cloudprov.OpPowerOn, "vm-ops", "", "")
	})
	if _, _, err := s.Create(ctx, Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
		IdempotencyKey: "k-unknown",
	}, "tester", "admin", time.Now()); err != nil {
		t.Fatalf("create unknown: %v", err)
	}
	if err := s.Run(ctx, time.Now()); err != nil {
		t.Fatalf("run unknown: %v", err)
	}
	if got := testutil.ToFloat64(unknownChild) - unknownBefore; got != 1 {
		t.Fatalf("unknown delta = %v, want 1", got)
	}
	f.SetFailHook("", nil)
	f.Seed(cloudprov.Resource{ResourceID: "vm-ops", State: cloudprov.StateRunning})
	if err := s.Reconcile(ctx, time.Now()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := testutil.ToFloat64(okChild) - okBefore; got != 2 {
		t.Fatalf("reconciled success must count: delta %v, want 2", got)
	}

	// Cancel a fresh pending row: counted cancelled, no duration observation.
	_, pending, err := s.Create(ctx, Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
		IdempotencyKey: "k-cancel",
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if err := s.Cancel(ctx, pending[0].ID, time.Now()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := testutil.ToFloat64(cancelChild) - cancelBefore; got != 1 {
		t.Fatalf("cancelled delta = %v, want 1", got)
	}
}

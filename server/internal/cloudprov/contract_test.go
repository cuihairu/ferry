package cloudprov

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ContractSuite is the executable Adapter contract (F-0318): every adapter
// implementation must pass it unchanged. The factory must return a
// fully-capable adapter seeded with two resources:
//
//	id "r-1": region "fra1", StateStopped, PublicIP "203.0.113.10",
//	          BwMbps 100, Spec "s-2vcpu-4gb", tag role=entry
//	id "r-2": region "nyc1", StateRunning, no IP, BwMbps 0, tag role=landing
func ContractSuite(t *testing.T, newAdapter func(t *testing.T) Adapter) {
	t.Helper()

	ctx := context.Background()

	t.Run("capabilities", func(t *testing.T) {
		a := newAdapter(t)
		caps, err := a.Capabilities(ctx)
		if err != nil {
			t.Fatalf("capabilities: %v", err)
		}
		if !caps.Power || !caps.ForceStop || !caps.Rebuild || !caps.Billing {
			t.Fatalf("full-capability adapter flags = %+v", caps)
		}
		if !VersionAtLeast(caps.APIVersion, "1.0") {
			t.Fatalf("api version %q not parseable/at least 1.0", caps.APIVersion)
		}
	})

	t.Run("list and filter", func(t *testing.T) {
		a := newAdapter(t)
		all, err := a.ListResources(ctx, Filter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("list = %d resources, want 2", len(all))
		}
		fra, err := a.ListResources(ctx, Filter{Region: "fra1"})
		if err != nil || len(fra) != 1 || fra[0].ResourceID != "r-1" {
			t.Fatalf("region filter = %+v err=%v", fra, err)
		}
		tagged, err := a.ListResources(ctx, Filter{Tags: map[string]string{"role": "landing"}})
		if err != nil || len(tagged) != 1 || tagged[0].ResourceID != "r-2" {
			t.Fatalf("tag filter = %+v err=%v", tagged, err)
		}
	})

	t.Run("describe facts", func(t *testing.T) {
		a := newAdapter(t)
		r, err := a.DescribeResource(ctx, "r-1")
		if err != nil {
			t.Fatalf("describe: %v", err)
		}
		// F-0305 full view, F-0312 public IP, F-0313 bw+spec.
		if r.State != StateStopped || r.Region != "fra1" || r.Spec != "s-2vcpu-4gb" ||
			r.BwMbps != 100 || r.PublicIP != "203.0.113.10" || r.Tags["role"] != "entry" {
			t.Fatalf("describe r-1 = %+v", r)
		}
		// Unknown id: NotFound typed error (F-0315), never a nil resource.
		_, err = a.DescribeResource(ctx, "missing")
		if AsError(err).Type != ErrNotFound {
			t.Fatalf("describe missing = %v", err)
		}
	})

	t.Run("refresh status", func(t *testing.T) {
		a := newAdapter(t)
		st, err := a.RefreshStatus(ctx, "r-2")
		if err != nil || !ValidState(st) {
			t.Fatalf("refresh r-2 = %q err=%v", st, err)
		}
		if _, err = a.RefreshStatus(ctx, "missing"); AsError(err).Type != ErrNotFound {
			t.Fatalf("refresh missing = %v", err)
		}
	})

	t.Run("power lifecycle", func(t *testing.T) {
		a := newAdapter(t)
		// F-0307~F-0309, each transition visible through F-0306 refresh.
		if err := a.PowerOn(ctx, "r-1"); err != nil {
			t.Fatalf("power on: %v", err)
		}
		if st, _ := a.RefreshStatus(ctx, "r-1"); st != StateStarting {
			t.Fatalf("after power on = %q", st)
		}
		if err := a.PowerOff(ctx, "r-1"); err != nil {
			t.Fatalf("power off: %v", err)
		}
		if st, _ := a.RefreshStatus(ctx, "r-1"); st != StateStopping {
			t.Fatalf("after power off = %q", st)
		}
		if err := a.Reboot(ctx, "r-1"); err != nil {
			t.Fatalf("reboot: %v", err)
		}
		// F-0310 high-risk force stop lands on stopped.
		if err := a.ForceStop(ctx, "r-1"); err != nil {
			t.Fatalf("force stop: %v", err)
		}
		if st, _ := a.RefreshStatus(ctx, "r-1"); st != StateStopped {
			t.Fatalf("after force stop = %q", st)
		}
		// F-0311 rebuild declared-capable vendors accept it.
		if err := a.Rebuild(ctx, "r-1", "debian-12"); err != nil {
			t.Fatalf("rebuild: %v", err)
		}
		if st, _ := a.RefreshStatus(ctx, "r-1"); st != StateRebuilding {
			t.Fatalf("after rebuild = %q", st)
		}
		// Every failure on these ops is a typed *Error with the op set.
		if err := a.PowerOn(ctx, "missing"); AsError(err).Type != ErrNotFound || AsError(err).Op != OpPowerOn {
			t.Fatalf("power on missing = %v", err)
		}
	})

	t.Run("billing import", func(t *testing.T) {
		a := newAdapter(t)
		w := BillingWindow{From: time.Now().AddDate(0, -1, 0)}
		recs, err := a.ImportBilling(ctx, w)
		if err != nil {
			t.Fatalf("import billing: %v", err)
		}
		// F-0313 zero bandwidth = unknown: only r-1 has a billing datum.
		if len(recs) != 1 || recs[0].ResourceID != "r-1" || recs[0].CostCents <= 0 || recs[0].Currency == "" {
			t.Fatalf("billing = %+v", recs)
		}
	})
}

// TestFakeContract runs the shared contract (F-0318) against the reference
// Fake implementation.
func TestFakeContract(t *testing.T) {
	ContractSuite(t, func(t *testing.T) Adapter {
		f := NewFake()
		f.Seed(Resource{ResourceID: "r-1", Name: "entry-1", Region: "fra1",
			State: StateStopped, PublicIP: "203.0.113.10", BwMbps: 100,
			Spec: "s-2vcpu-4gb", Tags: map[string]string{"role": "entry"}})
		f.Seed(Resource{ResourceID: "r-2", Name: "landing-1", Region: "nyc1",
			State: StateRunning, Tags: map[string]string{"role": "landing"}})
		return f
	})
}

// TestUnsupportedGate covers F-0303's fail-closed half: operations an adapter
// does not declare fail with ErrUnsupported and leave state untouched.
func TestUnsupportedGate(t *testing.T) {
	f := NewFake()
	f.SetCaps(Capabilities{APIVersion: "2.1"})
	r := f.Seed(Resource{ResourceID: "r-1", Region: "fra1"})
	ctx := context.Background()

	ops := []struct {
		name string
		call func() error
	}{
		{"power_on", func() error { return f.PowerOn(ctx, "r-1") }},
		{"power_off", func() error { return f.PowerOff(ctx, "r-1") }},
		{"reboot", func() error { return f.Reboot(ctx, "r-1") }},
		{"force_stop", func() error { return f.ForceStop(ctx, "r-1") }},
		{"rebuild", func() error { return f.Rebuild(ctx, "r-1", "img") }},
		{"import_billing", func() error {
			_, err := f.ImportBilling(ctx, BillingWindow{})
			return err
		}},
	}
	for _, op := range ops {
		err := op.call()
		e := AsError(err)
		if e.Type != ErrUnsupported || e.Op != op.name {
			t.Fatalf("%s = %v, want unsupported typed error", op.name, err)
		}
	}
	// High-risk note (F-0310): the gate key matches the exported constant.
	if HighRiskForceStop != "force_stop" {
		t.Fatalf("high-risk op key = %q", HighRiskForceStop)
	}
	// State untouched by rejected ops.
	got, _ := f.RefreshStatus(ctx, "r-1")
	if got != r.State {
		t.Fatalf("rejected ops changed state: %q -> %q", r.State, got)
	}
}

// TestValidState pins the canonical state set (F-0306).
func TestValidState(t *testing.T) {
	for _, s := range []string{StateRunning, StateStopped, StateStarting, StateStopping,
		StateRebuilding, StateProvisioning, StateError} {
		if !ValidState(s) {
			t.Fatalf("canonical state %q invalid", s)
		}
	}
	if ValidState("galactic") {
		t.Fatal("non-canonical state accepted")
	}
}

// TestErrorShape covers F-0315's Error type: message formatting, typed
// extraction, retryability classes.
func TestErrorShape(t *testing.T) {
	e := NewError(ErrRateLimited, OpListResources, "", "429", "slow down")
	if e.Error() != "provider list_resources: rate_limited (429): slow down" {
		t.Fatalf("message = %q", e.Error())
	}
	if !AsError(e).Type.Retryable() {
		t.Fatal("rate_limited must be retryable")
	}
	if AsError(errors.New("boom")).Type != ErrInternal {
		t.Fatal("untyped error must map to internal")
	}
	if AsError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	for _, et := range []ErrorType{ErrAuth, ErrNotFound, ErrUnsupported, ErrBadRequest, ErrCompat, ErrInternal} {
		if et.Retryable() {
			t.Fatalf("%s must not be retryable", et)
		}
	}
	if ErrRateLimited.Retryable() == false || ErrUnavailable.Retryable() == false {
		t.Fatal("retryable class broken")
	}
}

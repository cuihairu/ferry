package alloc

import (
	"io"
	"log"
	"regexp"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// histValue reads a bare (unlabelled) histogram sample from its exposition.
func histValue(t *testing.T, name string) float64 {
	t.Helper()
	out, err := testutil.CollectAndFormat(sweepSeconds, expfmt.TypeTextPlain,
		"ferry_alloc_sweep_seconds")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` ([0-9.eE+-]+)$`)
	m := re.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("%s missing from exposition:\n%s", name, out)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("%s value %q: %v", name, m[1], err)
	}
	return v
}

// TestSweepMetrics covers F-1810: every scheduler round observes its
// decision-pipeline duration and reports the outcome (ok/error); announce and
// hook delivery time is not part of the observation.
func TestSweepMetrics(t *testing.T) {
	db := newTestDB(t)
	okChild := sweepTotal.WithLabelValues("ok")
	errChild := sweepTotal.WithLabelValues("error")
	okBefore := testutil.ToFloat64(okChild)
	errBefore := testutil.ToFloat64(errChild)
	countBefore := histValue(t, "ferry_alloc_sweep_seconds_count")

	// A round over an empty fleet succeeds and observes the duration once.
	if err := sweepOnce(db, log.New(io.Discard, "", 0), nil); err != nil {
		t.Fatalf("empty sweep: %v", err)
	}
	if got := testutil.ToFloat64(okChild) - okBefore; got != 1 {
		t.Fatalf("ok counter delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(errChild) - errBefore; got != 0 {
		t.Fatalf("error counter delta = %v, want 0", got)
	}
	if got := histValue(t, "ferry_alloc_sweep_seconds_count") - countBefore; got != 1 {
		t.Fatalf("histogram count delta = %v, want 1", got)
	}
	// A round that only ran two DB reads still takes measurable time.
	if sum := histValue(t, "ferry_alloc_sweep_seconds_sum"); sum <= 0 {
		t.Fatalf("histogram sum = %v, want > 0", sum)
	}

	// A broken DB turns the round into a counted error.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sweepOnce(db, log.New(io.Discard, "", 0), nil); err == nil {
		t.Fatal("closed db must fail the sweep")
	}
	if got := testutil.ToFloat64(errChild) - errBefore; got != 1 {
		t.Fatalf("error counter delta = %v, want 1", got)
	}
}

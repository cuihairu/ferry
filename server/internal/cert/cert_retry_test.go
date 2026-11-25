package cert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSweepRetryCapStopsRetrying covers the attempt ceiling: a failed task that already
// reached MaxAttempts is left untouched by the sweep (no more automatic retries; recovery
// from this state is a manual re-issue, which resets the counter on success).
func TestSweepRetryCapStopsRetrying(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	provID := dnsFix(t, db)
	old := now.Add(-FailBackoff - time.Minute)
	task := mkTask(t, db, func(tk *storage.CertTask) {
		tk.State, tk.LastAttempt, tk.Attempts, tk.DNSProviderID = StateFailed, &old, MaxAttempts, provID
	})

	rr := &recRunner{}
	m := newMgr(rr, now)
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now.Add(FailBackoff+2*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 0 {
		t.Fatalf("attempt ceiling must stop retry, calls = %v", rr.calls)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Attempts != MaxAttempts {
		t.Fatalf("capped task mutated: %+v", got)
	}
}

// TestSweepFailureTracksAttempts covers the attempt counter: a failed issue bumps it, a
// success resets it to zero.
func TestSweepFailureTracksAttempts(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	provID := dnsFix(t, db)
	old := now.Add(-FailBackoff - time.Minute)
	task := mkTask(t, db, func(tk *storage.CertTask) {
		tk.State, tk.LastAttempt, tk.Attempts, tk.DNSProviderID = StateFailed, &old, 3, provID
	})

	rr := &recRunner{results: []error{errors.New("acme fail")}}
	if err := Sweep(context.Background(), db, newMgr(rr, now), secret.NewStore("cert-master"), now.Add(FailBackoff+2*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Attempts != 4 {
		t.Fatalf("after failed retry = %+v", got)
	}

	rr.results = nil
	if err := Sweep(context.Background(), db, newMgr(rr, now), secret.NewStore("cert-master"), got.LastAttempt.Add(FailBackoff+time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateOK || got.Attempts != 0 {
		t.Fatalf("after success = %+v", got)
	}
}

// TestSweepRenewFailureTracksAttempts covers the renewal path counter: one failed renew
// bumps attempts; past the ceiling the failed-state backoff gate stops further retries.
func TestSweepRenewFailureTracksAttempts(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	expired := now.Add(-24 * time.Hour)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.State, tk.NotAfter, tk.Attempts = StateOK, &expired, MaxAttempts-1 })

	rr := &recRunner{results: []error{errors.New("acme fail")}}
	m := newMgr(rr, now)
	// The failure flips to failed with attempts = MaxAttempts.
	if err := Sweep(context.Background(), db, m, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Attempts != MaxAttempts {
		t.Fatalf("after failed renew = %+v", got)
	}

	// A later sweep must not retry: the ceiling is reached.
	rr.results = nil
	calls := len(rr.calls)
	if err := Sweep(context.Background(), db, m, nil, now.Add(FailBackoff+time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != calls {
		t.Fatalf("capped task was retried, calls = %v", rr.calls)
	}
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed {
		t.Fatalf("capped task state = %s", got.State)
	}
}

// TestIssueAttemptTimeout covers the per-attempt bound: a runner that hangs until its
// context dies turns the task failed with the counter bumped, instead of blocking the
// serial sweep forever.
func TestIssueAttemptTimeout(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	provID := dnsFix(t, db)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = provID })

	saved := IssueTimeout
	IssueTimeout = 50 * time.Millisecond
	defer func() { IssueTimeout = saved }()

	m := &Manager{Home: "/tmp/acme"}
	m.Run = func(ctx context.Context, _ []string, _ map[string]string) (string, error) {
		<-ctx.Done()
		return "hung run: " + ctx.Err().Error(), ctx.Err()
	}
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now, nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || got.Attempts != 1 || got.LastAttempt == nil {
		t.Fatalf("timed-out issue = %+v", got)
	}
	if !strings.Contains(got.LastError, "context deadline exceeded") {
		t.Fatalf("last error = %q", got.LastError)
	}
}

// TestIssueNowDetachedContext covers the manual-issue context fix: the background run
// must survive the caller's (request) context being canceled right after IssueNow returns.
func TestIssueNowDetachedContext(t *testing.T) {
	db := newDB(t)
	provID := dnsFix(t, db)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = provID })
	m := newMgr(&recRunner{}, time.Now().Add(90*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := IssueNow(ctx, db, m, secret.NewStore("cert-master"), task.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got storage.CertTask
		if err := db.First(&got, task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.State == StateOK && got.NotAfter != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached issue never finished: state=%s", got.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

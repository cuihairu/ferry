package recovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// deadlineAction records that Run started and reports the error it ended with; it blocks
// until its context dies, exposing whether the engine really cancels the action goroutine.
type deadlineAction struct {
	started chan struct{}
	done    chan error
}

func (a *deadlineAction) Name() string { return "deadline_probe" }

func (a *deadlineAction) Run(ctx context.Context, _ storage.Node) error {
	close(a.started)
	<-ctx.Done()
	err := ctx.Err()
	a.done <- err
	return err
}

// TestActionCancelledAtLevelDeadline covers the runAction context bound: the level
// deadline cancels the action itself (Run observes DeadlineExceeded and the write-back
// lands as failed), instead of the goroutine running on after the engine gives up waiting.
func TestActionCancelledAtLevelDeadline(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "n-cancel", "探测不可达", now.Add(-2*DefaultSustainedAfter))
	act := &deadlineAction{started: make(chan struct{}), done: make(chan error, 1)}

	// First sweep opens the verdict; the second drives L1 and starts the action.
	for i := 0; i < 2; i++ {
		if err := Sweep(db, now, Options{SustainedAfter: time.Minute, LevelTimeout: 100 * time.Millisecond},
			Registry{1: act}, nil); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-act.started:
	case <-time.After(2 * time.Second):
		t.Fatal("action never started")
	}
	select {
	case err := <-act.done:
		if err != context.DeadlineExceeded {
			t.Fatalf("action ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("action never cancelled at the level deadline")
	}

	rec := loadRecovery(t, db, n.ID)
	if rec.ActionState != ActionFailed || !strings.Contains(rec.LastErr, "deadline") {
		t.Fatalf("after cancel = %+v", rec)
	}
}

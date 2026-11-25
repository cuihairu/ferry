package announce

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 22 error-path coverage: announce error return semantics——split tables to break the first/second query, gorm callbacks
// to break intermediate statements (inserting the Nth row and updates).

// failUpdates makes all subsequent Updates fail (gorm update callback error injection).
func failUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:fail_update", func(tx *gorm.DB) {
		tx.AddError(errors.New("db down"))
	})
}

// isNotification reports whether the statement targets the notification table (both the single anchor row and the batch user rows use it).
func isNotification(v any) bool {
	switch v.(type) {
	case *storage.Notification, []storage.Notification, *[]storage.Notification:
		return true
	}
	return false
}

// seedAnchor creates an already fanned-out announcement anchor row (prerequisite for the resend endpoint).
func seedAnchor(t *testing.T, db *gorm.DB) storage.Notification {
	t.Helper()
	a := storage.Notification{
		Type: storage.NotifAnnouncement, Title: "公告", Body: "正文",
		UserID: 0, Audience: "all", CreatedAt: time.Now(),
	}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("anchor: %v", err)
	}
	return a
}

// TestFanoutAnchorCreateError scheduled-anchor and immediate-anchor persistence failures surface errors.
func TestFanoutAnchorCreateError(t *testing.T) {
	future := time.Now().Add(time.Hour)

	// Scheduled publish: only the anchor row is written, and that write fails.
	db := testDB(t)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := Fanout(db, Input{Title: "t", StartsAt: &future}); err == nil {
		t.Fatal("want scheduled anchor create error")
	}

	// Immediate fan-out: the create path fails to write the anchor row.
	db = testDB(t)
	mkUser(db, 1, true, nil)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := Fanout(db, Input{Title: "t"}); err == nil {
		t.Fatal("want immediate anchor create error")
	}
}

// TestFanoutWriteFailures batch user-row persistence failure, batch event persistence failure, and fan-out time
// write-back failure——each of the three write faults has its own error return.
func TestFanoutWriteFailures(t *testing.T) {
	// Batch user-row insert failure: the anchor is already written, and the second insert statement fails.
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		var inserts int
		db.Callback().Create().Before("gorm:create").Register("test:fail_create", func(tx *gorm.DB) {
			if isNotification(tx.Statement.Model) || isNotification(tx.Statement.Dest) {
				inserts++
				if inserts >= 2 {
					tx.AddError(errors.New("db down"))
				}
			}
		})
		if _, err := Fanout(db, Input{Title: "t"}); err == nil {
			t.Fatal("want notification rows insert error")
		}
	}

	// Batch event insert failure: the in-site message rows are written, and the batch event write fails.
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		if err := db.Migrator().DropTable(&storage.Event{}); err != nil {
			t.Fatalf("drop events: %v", err)
		}
		if _, err := Fanout(db, Input{Title: "t"}); err == nil {
			t.Fatal("want events insert error")
		}
	}

	// Fan-out time write-back failure: the in-site message and the events are already written.
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		failUpdates(t, db)
		if _, err := Fanout(db, Input{Title: "t"}); err == nil {
			t.Fatal("want fanout_at update error")
		}
	}
}

// TestRunDueErrors scheduled-anchor query failure, and fan-out failure of a due anchor.
func TestRunDueErrors(t *testing.T) {
	// The notification table is missing: the anchor query fails outright.
	db := testDB(t)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := RunDue(db, time.Now()); err == nil {
		t.Fatal("want due query error")
	}

	// A due anchor exists, but the audience roster query fails (split the user table).
	db = testDB(t)
	past := time.Now().Add(-time.Hour)
	anchor := storage.Notification{
		Type: storage.NotifAnnouncement, Title: "定时", Body: "正文",
		UserID: 0, Audience: "all", StartsAt: &past, CreatedAt: past,
	}
	if err := db.Create(&anchor).Error; err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if err := db.Migrator().DropTable(&storage.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	if _, err := RunDue(db, time.Now()); err == nil {
		t.Fatal("want fanout audience error")
	}
}

// TestResendWriteFailures resend batch event persistence failure and resend time write-back failure.
func TestResendWriteFailures(t *testing.T) {
	// Batch event insert failure: the roster is obtained, and the event write fails.
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		a := seedAnchor(t, db)
		if err := db.Migrator().DropTable(&storage.Event{}); err != nil {
			t.Fatalf("drop events: %v", err)
		}
		if _, err := Resend(db, a.ID, time.Now()); err == nil {
			t.Fatal("want events insert error")
		}
	}

	// Resend time write-back failure: the events are already written.
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		a := seedAnchor(t, db)
		failUpdates(t, db)
		if _, err := Resend(db, a.ID, time.Now()); err == nil {
			t.Fatal("want resend_at update error")
		}
	}
}

package announce

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十二批补口：announce 各错误返回口径——拆表定向破坏首查/次查，gorm 回调
// 注入破坏中间语句（插入第 N 条与更新）。

// failUpdates 让后续所有 Update 失败（gorm 更新回调注错）。
func failUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:fail_update", func(tx *gorm.DB) {
		tx.AddError(errors.New("db down"))
	})
}

// isNotification 判断语句目标是否为通知表（锚点单行与用户行批量都是它）。
func isNotification(v any) bool {
	switch v.(type) {
	case *storage.Notification, []storage.Notification, *[]storage.Notification:
		return true
	}
	return false
}

// seedAnchor 造一条已扇出的公告锚点行（重发端点的前置）。
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

// TestFanoutAnchorCreateError 定时锚点与立即锚点落库失败即冒错。
func TestFanoutAnchorCreateError(t *testing.T) {
	future := time.Now().Add(time.Hour)

	// 定时发布：只落锚点行，落库失败。
	db := testDB(t)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := Fanout(db, Input{Title: "t", StartsAt: &future}); err == nil {
		t.Fatal("want scheduled anchor create error")
	}

	// 立即扇出：新建路径落锚点行失败。
	db = testDB(t)
	mkUser(db, 1, true, nil)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := Fanout(db, Input{Title: "t"}); err == nil {
		t.Fatal("want immediate anchor create error")
	}
}

// TestFanoutWriteFailures 用户行批量落库失败、事件批量落库失败、扇出时间
// 回写失败——三种写入故障各自的错误返回。
func TestFanoutWriteFailures(t *testing.T) {
	// 用户行批量插入失败：锚点已落，第二条插入语句失败。
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

	// 事件批量插入失败：站内信行已落，事件批量写失败。
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

	// 扇出时间回写失败：站内信与事件已落。
	{
		db := testDB(t)
		mkUser(db, 1, true, nil)
		failUpdates(t, db)
		if _, err := Fanout(db, Input{Title: "t"}); err == nil {
			t.Fatal("want fanout_at update error")
		}
	}
}

// TestRunDueErrors 定时锚点查询失败、以及到点锚点扇出失败。
func TestRunDueErrors(t *testing.T) {
	// 通知表缺失：锚点查询即失败。
	db := testDB(t)
	if err := db.Migrator().DropTable(&storage.Notification{}); err != nil {
		t.Fatalf("drop notifications: %v", err)
	}
	if _, err := RunDue(db, time.Now()); err == nil {
		t.Fatal("want due query error")
	}

	// 到点锚点存在，但受众名单查询失败（拆用户表）。
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

// TestResendWriteFailures 重发的事件批量落库失败与重发时间回写失败。
func TestResendWriteFailures(t *testing.T) {
	// 事件批量插入失败：名单已取到，事件写失败。
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

	// 重发时间回写失败：事件已落。
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

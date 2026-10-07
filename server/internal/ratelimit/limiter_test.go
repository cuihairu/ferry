package ratelimit

import (
	"testing"
	"time"
)

func TestSlidingWindow(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 3, FailLimit: 100, Lockout: time.Minute})
	l.now = func() time.Time { return base }

	for i := 0; i < 3; i++ {
		if !l.Allow("ip") {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	if l.Allow("ip") {
		t.Fatal("4th attempt in window should block")
	}
	// 窗口滑过后放行
	l.now = func() time.Time { return base.Add(61 * time.Second) }
	if !l.Allow("ip") {
		t.Fatal("after window should pass")
	}
	// 不同 key 互不影响
	l.now = func() time.Time { return base }
	if !l.Allow("other") {
		t.Fatal("other key should pass")
	}
}

func TestFailLockout(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 100, FailLimit: 3, Lockout: 15 * time.Minute})
	l.now = func() time.Time { return base }

	if !l.Allow("ip") {
		t.Fatal("should pass initially")
	}
	l.RecordFailure("ip")
	l.RecordFailure("ip")
	if !l.Allow("ip") {
		t.Fatal("below fail threshold should still pass")
	}
	l.RecordFailure("ip") // 第 3 次失败 → 锁定
	if l.Allow("ip") {
		t.Fatal("locked key should block")
	}
	// 锁定期间尝试不改变结局；锁定期满恢复
	l.now = func() time.Time { return base.Add(14 * time.Minute) }
	if l.Allow("ip") {
		t.Fatal("within lockout should block")
	}
	l.now = func() time.Time { return base.Add(16 * time.Minute) }
	if !l.Allow("ip") {
		t.Fatal("after lockout should pass")
	}
}

func TestReset(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 1, FailLimit: 1, Lockout: time.Hour})
	l.now = func() time.Time { return base }

	if !l.Allow("ip") {
		t.Fatal("first should pass")
	}
	if l.Allow("ip") {
		t.Fatal("window exhausted")
	}
	l.RecordFailure("ip") // 触发锁定
	if l.Allow("ip") {
		t.Fatal("locked")
	}
	l.Reset("ip")
	if !l.Allow("ip") {
		t.Fatal("reset should clear window and lock")
	}
}

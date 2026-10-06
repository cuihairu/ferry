package app

import (
	"testing"
	"time"
)

func TestLoadWatch(t *testing.T) {
	base := time.Now()
	var w loadWatch

	// 阈值以下不报
	if w.observe(10, base.Add(1*time.Minute)) {
		t.Fatal("low cpu should not fire")
	}
	// 第一次超阈值只是累计，第二次才报（此时 last 为零值，立即生效）
	if w.observe(95, base.Add(2*time.Minute)) {
		t.Fatal("single spike should not fire")
	}
	if !w.observe(95, base.Add(3*time.Minute)) {
		t.Fatal("sustained load should fire")
	}
	// 冷却期内持续高负载不重报（streak 持续累计）
	if w.observe(95, base.Add(4*time.Minute)) || w.observe(95, base.Add(10*time.Minute)) {
		t.Fatal("cooldown should suppress")
	}
	// 冷却期（30m）过后再报：上一次为 +3m，+34m 已过
	if !w.observe(95, base.Add(34*time.Minute)) {
		t.Fatal("after cooldown should fire again")
	}
	// 回落清零：再升需重新累计两次，且新冷却期未过不报
	if w.observe(10, base.Add(35*time.Minute)) {
		t.Fatal("recovered cpu should reset streak")
	}
	if w.observe(99, base.Add(36*time.Minute)) {
		t.Fatal("fresh streak needs two samples")
	}
	if w.observe(99, base.Add(37*time.Minute)) {
		t.Fatal("fresh streak still in cooldown")
	}
	// 上一次报于 +34m，+65m 越过冷却期后可再报
	if !w.observe(99, base.Add(65*time.Minute)) {
		t.Fatal("third fire after cooldown")
	}
}

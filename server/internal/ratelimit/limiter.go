// Package ratelimit 是内存滑动窗口限流器（PAY-5）：
// 按 key（如客户端 IP）做窗口内次数上限 + 失败锁定，零外部依赖适配单机小内存。
package ratelimit

import (
	"sync"
	"time"
)

// Options 是限流参数。
type Options struct {
	// Window 是滑动窗口宽度。
	Window time.Duration
	// MaxAttempts 是窗口内允许的尝试次数。
	MaxAttempts int
	// FailLimit 是窗口内失败次数达到该值即进入锁定。
	FailLimit int
	// Lockout 是锁定时长。
	Lockout time.Duration
}

type state struct {
	attempts    []time.Time
	failures    []time.Time
	lockedUntil time.Time
}

// Limiter 按 key 限流。所有方法并发安全。
type Limiter struct {
	mu   sync.Mutex
	opts Options
	now  func() time.Time
	keys map[string]*state
}

// New 创建限流器。
func New(o Options) *Limiter {
	return &Limiter{opts: o, now: time.Now, keys: map[string]*state{}}
}

// Allow 判定一次尝试：窗口未满且未锁定则占一个额度并放行。
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.keys[key]
	if st == nil {
		st = &state{}
		l.keys[key] = st
	}
	if now.Before(st.lockedUntil) {
		return false
	}
	st.attempts = prune(st.attempts, now.Add(-l.opts.Window))
	if len(st.attempts) >= l.opts.MaxAttempts {
		return false
	}
	st.attempts = append(st.attempts, now)
	// 键数量膨胀时顺带清一遍（兑换入口流量低，懒清理足够）。
	if len(l.keys) > 4096 {
		l.sweepLocked(now)
	}
	return true
}

// RecordFailure 记一次失败；窗口内失败达到 FailLimit 即锁定。
func (l *Limiter) RecordFailure(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.keys[key]
	if st == nil {
		st = &state{}
		l.keys[key] = st
	}
	st.failures = prune(st.failures, now.Add(-l.opts.Window))
	st.failures = append(st.failures, now)
	if len(st.failures) >= l.opts.FailLimit {
		st.lockedUntil = now.Add(l.opts.Lockout)
		st.failures = nil
	}
}

// Reset 清空 key 的全部状态（成功兑换后调用）。
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

func (l *Limiter) sweepLocked(now time.Time) {
	for k, st := range l.keys {
		st.attempts = prune(st.attempts, now.Add(-l.opts.Window))
		st.failures = prune(st.failures, now.Add(-l.opts.Window))
		if len(st.attempts) == 0 && len(st.failures) == 0 && now.After(st.lockedUntil) {
			delete(l.keys, k)
		}
	}
}

// prune 丢弃窗口外的旧时间点（输入按时间有序）。
func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	if i == 0 {
		return ts
	}
	return append(ts[:0], ts[i:]...) // 原地压缩，容量复用
}

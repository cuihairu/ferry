package relay

import (
	"io"
	"sync"
	"time"
)

// rateReader is a token-bucket read limiter (F-0810): it caps byte throughput
// without adding a dependency. Tokens accrue at rate bytes/sec up to a burst
// ceiling so short idle periods do not stall the next write forever.
type rateReader struct {
	src    io.Reader
	mu     sync.Mutex
	rate   float64 // bytes per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newRateReader(src io.Reader, kbps int, now func() time.Time) io.Reader {
	if kbps <= 0 {
		return src
	}
	rate := float64(kbps) * 1000 / 8 // kilobits/s -> bytes/s
	r := &rateReader{
		src: src, rate: rate, burst: rate, tokens: rate,
		last: now(), now: now,
	}
	return r
}

func (r *rateReader) readAllowed(n int) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if elapsed := now.Sub(r.last); elapsed > 0 {
		r.tokens += elapsed.Seconds() * r.rate
		if r.tokens > r.burst {
			r.tokens = r.burst
		}
		r.last = now
	}
	if r.tokens >= float64(n) {
		r.tokens -= float64(n)
		return 0
	}
	wait := time.Duration((float64(n) - r.tokens) / r.rate * float64(time.Second))
	r.tokens = 0
	return wait
}

func (r *rateReader) Read(p []byte) (int, error) {
	// Cap each read to the bucket's burst size so the wait is bounded; io.Copy
	// calls Read again for the remainder of a larger buffer.
	if len(p) > int(r.burst) && r.burst >= 1 {
		p = p[:int(r.burst)]
	}
	if wait := r.readAllowed(len(p)); wait > 0 {
		time.Sleep(wait)
	}
	return r.src.Read(p)
}

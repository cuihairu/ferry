package quality

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestScoreGame pins the F-0945 rule: the score reads RTT, jitter, loss and
// availability, all four move it, perfect inputs score 100 and a dead target
// scores 0.
func TestScoreGame(t *testing.T) {
	perfect := storage.ProbeReport{RttMs: 30, JitterMs: 2, LossPct: 0, Reachable: true}
	if got, ok := Score(Game, perfect); !ok || got != 100 {
		t.Fatalf("perfect game = %d (%v); want 100", got, ok)
	}
	dead := storage.ProbeReport{RttMs: 0, JitterMs: 0, LossPct: 100, Reachable: false}
	if got, _ := Score(Game, dead); got != 0 {
		t.Fatalf("dead game = %d; want 0", got)
	}

	// Each of the four inputs moves the score on its own.
	base := perfect
	if s1, _ := Score(Game, base); true {
		base.RttMs = 400
		if s2, _ := Score(Game, base); s2 >= s1 {
			t.Fatalf("rtt 400 must score below rtt 30: %d vs %d", s2, s1)
		}
	}
	base = perfect
	if s1, _ := Score(Game, base); true {
		base.JitterMs = 80
		if s2, _ := Score(Game, base); s2 >= s1 {
			t.Fatalf("jitter 80 must score below jitter 2: %d vs %d", s2, s1)
		}
	}
	base = perfect
	if s1, _ := Score(Game, base); true {
		base.LossPct = 5
		if s2, _ := Score(Game, base); s2 >= s1 {
			t.Fatalf("loss 5 must score below loss 0: %d vs %d", s2, s1)
		}
	}
	base = perfect
	if s1, _ := Score(Game, base); true {
		base.Reachable = false
		base.LossPct = 100
		if s2, _ := Score(Game, base); s2 >= s1 {
			t.Fatalf("unreachable must score below reachable: %d vs %d", s2, s1)
		}
	}
}

// TestScoreWeb pins the F-0946 rule: connect, TLS, TTFB and failure rate
// drive the score; a plaintext probe (tls 0) keeps its score -- no TLS is a
// missing datum, not a slow handshake.
func TestScoreWeb(t *testing.T) {
	fast := storage.ProbeReport{ConnectMs: 10, TLSMs: 25, TTFBMs: 80, LossPct: 0, Reachable: true}
	if got, ok := Score(Web, fast); !ok || got != 100 {
		t.Fatalf("fast web = %d (%v); want 100", got, ok)
	}

	// Slow TLS hurts an https probe.
	slowTLS := fast
	slowTLS.TLSMs = 500
	if s1, _ := Score(Web, fast); true {
		if s2, _ := Score(Web, slowTLS); s2 >= s1 {
			t.Fatalf("tls 500 must score below tls 25: %d vs %d", s2, s1)
		}
	}

	// The same numbers on a plaintext probe lose the TLS component instead of
	// being punished: tls 0 with otherwise identical phases must not score
	// below the fast https probe.
	plain := fast
	plain.TLSMs = 0
	if s1, _ := Score(Web, fast); true {
		if s2, _ := Score(Web, plain); s2 != s1 {
			t.Fatalf("plaintext tls must be a dropped datum: %d vs %d", s2, s1)
		}
	}

	// TTFB at its zero-score edge with everything else perfect: the weighted
	// mean lands at exactly 65 -- the rule's math, pinned here so weight
	// changes fail loudly instead of drifting silently.
	slowTTFB := fast
	slowTTFB.TTFBMs = 1500
	if s2, _ := Score(Web, slowTTFB); s2 != 65 {
		t.Fatalf("ttfb 1500 = %d; want exactly 65 per the rule weights", s2)
	}

	// The failure rate moves the score too.
	lossy := fast
	lossy.LossPct = 50
	if s2, _ := Score(Web, lossy); s2 > 80 {
		t.Fatalf("loss 50 = %d; want a clearly degraded score", s2)
	}
}

// TestScoreVideo pins the F-0947 rule: throughput, loss and buffer risk; a
// probe without the download opt-in still scores from loss and risk (the
// weights renormalize), and absence of throughput is not zero throughput.
func TestScoreVideo(t *testing.T) {
	hd := storage.ProbeReport{ThroughputKbps: 10000, LossPct: 0, JitterMs: 0, Reachable: true}
	if got, ok := Score(Video, hd); !ok || got != 100 {
		t.Fatalf("hd video = %d (%v); want 100", got, ok)
	}

	// Starved bitrate is a bad video experience even with zero loss.
	starved := hd
	starved.ThroughputKbps = 1000
	if s2, _ := Score(Video, starved); s2 > 70 {
		t.Fatalf("throughput 1000 = %d; want a low score", s2)
	}

	// No download opt-in: the datum drops out, loss and risk carry the score.
	noDL := storage.ProbeReport{ThroughputKbps: 0, LossPct: 0, JitterMs: 2, Reachable: true}
	got, ok := Score(Video, noDL)
	if !ok {
		t.Fatal("video without throughput must still score from loss and risk")
	}
	if got != 99 { // loss 0 -> 100, risk 2 -> 98; renormalized .25/.35
		t.Fatalf("video without throughput = %d; want 99", got)
	}

	// Loss and jitter wreck playback.
	stalling := hd
	stalling.LossPct = 10
	stalling.JitterMs = 80
	if s2, _ := Score(Video, stalling); s2 > 60 {
		t.Fatalf("loss 10 jitter 80 = %d; want a stalling score", s2)
	}

	dead := storage.ProbeReport{ThroughputKbps: 0, LossPct: 100, Reachable: false}
	if s2, _ := Score(Video, dead); s2 != 0 {
		t.Fatalf("dead video = %d; want 0", s2)
	}
}

// TestScoreIndependentRules pins F-0948: the same report scores differently
// per business -- a transport that is fine for a video stream is terrible
// for a game, and no default rule rescues an unknown class.
func TestScoreIndependentRules(t *testing.T) {
	// High latency but clean phases and throughput: playable web, awful game.
	r := storage.ProbeReport{RttMs: 350, JitterMs: 60, LossPct: 0, Reachable: true,
		ConnectMs: 15, TLSMs: 30, TTFBMs: 90, ThroughputKbps: 9000}
	game, _ := Score(Game, r)
	web, _ := Score(Web, r)
	if web <= game {
		t.Fatalf("same report must score per class: web %d, game %d", web, game)
	}

	// Game ignores the web phases entirely: they cannot lift a bad game.
	webOnlyGood := storage.ProbeReport{RttMs: 400, JitterMs: 90, LossPct: 10, Reachable: true,
		ConnectMs: 5, TLSMs: 10, TTFBMs: 50, ThroughputKbps: 10000}
	if s, _ := Score(Game, webOnlyGood); s > 45 {
		t.Fatalf("web phases must not rescue a game score: %d", s)
	}

	if got, ok := Score("vending-machine", r); ok || got != 0 {
		t.Fatalf("unknown business = (%d, %v); want unscored", got, ok)
	}
}

package quality

import "github.com/cuihairu/ferry/server/internal/storage"

// Scoring rules (F-0945..F-0948): each business class scores 0-100 by its
// own rule over its own metric set -- never one universal formula. A metric
// without a datum (plaintext web probes carry no TLS; video probes without
// the download_kb opt-in carry no throughput) drops out of the weighted sum
// and the remaining weights renormalize, so absence of evidence neither
// punishes nor rewards. A report where no metric has a datum is unscored.

// component is one weighted score input: a 0-100 value and whether the
// report actually carries the datum.
type component struct {
	weight float64
	value  func(storage.ProbeReport) (float64, bool)
}

// lin maps v linearly onto 0-100: v=lo scores 0, v=hi scores 100, values
// outside clamp. Lower-better metrics pass (bad, good); higher-better ones
// pass (floor, target).
func lin(v, lo, hi float64) float64 {
	if hi == lo {
		return 100
	}
	s := 100 * (v - lo) / (hi - lo)
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// gameRule is the F-0945 scoring rule: RTT, jitter, loss and availability.
// Games are latency-bound, so the round trip carries the largest weight;
// loss and availability overlap by construction (avail = 100 - loss for a
// reachable report) and together carry 40%, because a player experiences
// both the drops and the remaining good rounds.
func gameRule() []component {
	return []component{
		{0.40, func(r storage.ProbeReport) (float64, bool) {
			// an unreachable report carried no successful round: zero RTT is
			// the absence of a datum, not a sub-millisecond link
			if !r.Reachable {
				return 0, false
			}
			return lin(float64(r.RttMs), 400, 40), true
		}},
		{0.20, func(r storage.ProbeReport) (float64, bool) {
			if !r.Reachable {
				return 0, false
			}
			return lin(float64(r.JitterMs), 80, 5), true
		}},
		{0.20, func(r storage.ProbeReport) (float64, bool) {
			return lin(float64(r.LossPct), 5, 0), true
		}},
		{0.20, func(r storage.ProbeReport) (float64, bool) {
			return float64(availPct(r)), true
		}},
	}
}

// webRule is the F-0946 scoring rule: connect, TLS, TTFB and the failure
// rate. The first byte carries the largest weight -- that is when a user
// gives up. TLS only participates on https probes (tls > 0); a plaintext
// probe neither gains nor loses from it.
func webRule() []component {
	return []component{
		{0.20, func(r storage.ProbeReport) (float64, bool) {
			if !r.Reachable {
				return 0, false // no successful round: no phase datum
			}
			return lin(float64(r.ConnectMs), 300, 20), true
		}},
		{0.20, func(r storage.ProbeReport) (float64, bool) {
			if r.TLSMs <= 0 {
				return 0, false // plaintext probe: no TLS datum
			}
			return lin(float64(r.TLSMs), 500, 30), true
		}},
		{0.35, func(r storage.ProbeReport) (float64, bool) {
			if !r.Reachable {
				return 0, false
			}
			return lin(float64(r.TTFBMs), 1500, 100), true
		}},
		{0.25, func(r storage.ProbeReport) (float64, bool) {
			return lin(float64(r.LossPct), 50, 0), true
		}},
	}
}

// videoRule is the F-0947 scoring rule: throughput, loss and the buffer-risk
// proxy. Throughput needs the download_kb opt-in -- without it there is no
// rate datum, and loss plus risk carry the score instead (renormalized).
func videoRule() []component {
	return []component{
		{0.40, func(r storage.ProbeReport) (float64, bool) {
			if r.ThroughputKbps <= 0 || !r.Reachable {
				return 0, false // no download measured: no datum, not zero
			}
			return lin(float64(r.ThroughputKbps), 1000, 10000), true
		}},
		{0.25, func(r storage.ProbeReport) (float64, bool) {
			return lin(float64(r.LossPct), 10, 0), true
		}},
		{0.35, func(r storage.ProbeReport) (float64, bool) {
			return float64(100 - bufferRisk(r)), true
		}},
	}
}

// rule returns the scoring rule for a business class. An unknown class has
// none -- the caller reports it unscored rather than inventing a default.
func rule(business string) []component {
	switch business {
	case Game:
		return gameRule()
	case Web:
		return webRule()
	case Video:
		return videoRule()
	}
	return nil
}

// Score turns one probe report into a business score 0-100 (F-0945..F-0948):
// the weighted mean of the participating components, weights renormalized
// over the metrics that have a datum. scored=false when the business has no
// rule (unknown class) or no component carries a datum -- never a guessed
// number.
func Score(business string, r storage.ProbeReport) (score int, scored bool) {
	comps := rule(business)
	if comps == nil {
		return 0, false
	}
	sum, weight := 0.0, 0.0
	for _, c := range comps {
		v, ok := c.value(r)
		if !ok {
			continue
		}
		sum += c.weight * v
		weight += c.weight
	}
	if weight == 0 {
		return 0, false
	}
	return int(sum/weight + 0.5), true
}

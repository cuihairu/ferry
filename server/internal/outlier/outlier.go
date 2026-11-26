// Package outlier trims anomalous probe RTT samples before they drive
// averages (F-0916): a single multi-second spike -- a momentarily congested
// hop, a GC pause on the prober -- must not drag a report's mean latency.
// The rule is the robust median/MAD test, not a fixed threshold, so it adapts
// to each path's normal latency instead of hardcoding "slow".
package outlier

import "sort"

// DefaultFactor is how many scaled MADs a sample may sit from the median
// before it is an outlier (Iglewicz-Hoaglin's 3.5 expressed as |x-med| >
// factor*MAD with factor = 3.5/0.6745 ~= 5).
const DefaultFactor = 5

// Trim returns the samples that pass the median/MAD outlier test (F-0916).
// Fewer than four samples are returned unchanged: the test needs a
// distribution to be robust, and inventing one would just be censoring.
// A degenerate zero MAD (most samples identical) keeps only the median
// value itself -- e.g. all-20ms-plus-one-900ms trims to the 20ms majority.
func Trim(samples []int, factor float64) []int {
	if len(samples) < 4 {
		return samples
	}
	if factor <= 0 {
		factor = DefaultFactor
	}
	med := median(samples)
	devs := make([]int, len(samples))
	for i, s := range samples {
		d := s - med
		if d < 0 {
			d = -d
		}
		devs[i] = d
	}
	mad := median(devs)
	out := make([]int, 0, len(samples))
	for _, s := range samples {
		d := s - med
		if d < 0 {
			d = -d
		}
		if mad == 0 {
			if s == med {
				out = append(out, s)
			}
			continue
		}
		if float64(d) <= factor*float64(mad) {
			out = append(out, s)
		}
	}
	return out
}

// median of a copy of the samples (the caller's slice is left untouched).
func median(samples []int) int {
	c := make([]int, len(samples))
	copy(c, samples)
	sort.Ints(c)
	return c[len(c)/2]
}

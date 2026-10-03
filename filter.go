package ntp

import (
	"math"
	"time"
)

// A FilterSample is a single time measurement obtained from one query of a
// time source. It retains the values needed by the clock filter algorithm:
// the clock offset estimate, the round-trip delay, the server's dispersion,
// and the time the response was received.
type FilterSample struct {
	// Offset is the estimated offset of the local clock relative to the
	// server's clock.
	Offset time.Duration

	// RTT is the measured round-trip-time delay between the client and the
	// server.
	RTT time.Duration

	// Dispersion is the server's estimated maximum measurement error
	// relative to the stratum 1 server (the response's RootDispersion).
	Dispersion time.Duration

	// Time is the moment the response was received, expressed on the
	// caller's clock.
	Time time.Time
}

// A ClockFilter is a fixed-capacity filter that collects samples from
// consecutive queries of the same time source and selects the most
// representative sample, as described by the clock filter algorithm in
// RFC 5905. It mirrors the behavior of an NTP peer's clock filter: samples
// age out of the filter, and the sample with the lowest round-trip delay is
// considered the most accurate.
//
// The filter never reads the system clock. The caller advances time by
// passing the current time to Add and Advance, which makes the filter fully
// deterministic and testable.
//
// The zero value is not usable; construct a filter with NewClockFilter.
type ClockFilter struct {
	samples  []FilterSample // accepted samples, in receive-time order
	capacity int            // maximum number of samples retained
	maxAge   time.Duration  // maximum sample age before expiration
	lastTime time.Time      // receive time of the most recently accepted sample
	best     int            // index of the selected sample, -1 when empty
	jitter   time.Duration  // jitter of the current sample window
}

// NewClockFilter returns a ClockFilter that retains at most capacity
// samples and expires any sample whose age exceeds maxAge. A capacity less
// than 1 is treated as 1.
func NewClockFilter(capacity int, maxAge time.Duration) *ClockFilter {
	if capacity < 1 {
		capacity = 1
	}
	return &ClockFilter{
		capacity: capacity,
		maxAge:   maxAge,
		best:     -1,
	}
}

// Add attempts to insert a sample derived from the response r received at
// time t into the filter. The time t is expressed on the caller's clock and
// must be later than the receive time of every previously accepted sample.
//
// Add returns false, leaving the filter unchanged, when:
//   - r fails response validation (r.Validate),
//   - r reports a negative round-trip time,
//   - t is equal to or earlier than the receive time of the most recently
//     accepted sample (duplicate or out-of-order reception).
//
// Before inserting, Add expires samples that are too old relative to t. If
// the filter is full, the oldest sample is evicted to make room.
func (f *ClockFilter) Add(r *Response, t time.Time) bool {
	if r == nil || r.Validate() != nil {
		return false
	}
	if r.RTT < 0 {
		return false
	}
	if !f.lastTime.IsZero() && !t.After(f.lastTime) {
		return false
	}

	f.expire(t)
	if len(f.samples) >= f.capacity {
		copy(f.samples, f.samples[1:])
		f.samples = f.samples[:len(f.samples)-1]
	}
	f.samples = append(f.samples, FilterSample{
		Offset:     r.ClockOffset,
		RTT:        r.RTT,
		Dispersion: r.RootDispersion,
		Time:       t,
	})
	f.lastTime = t
	f.update()
	return true
}

// Advance moves the caller's clock to now, expiring every sample whose age
// exceeds the filter's maximum age. A sample received at time T expires
// once now-T is strictly greater than the maximum age. The selected sample
// and jitter are recomputed from the surviving samples.
func (f *ClockFilter) Advance(now time.Time) {
	f.expire(now)
	f.update()
}

// Selected returns the sample currently selected as most representative of
// the time source. The second return value is false when the filter holds
// no samples.
//
// Selection follows a single explicit rule: the sample with the lowest
// round-trip delay wins. Ties are broken stably in favor of the sample
// received earliest.
func (f *ClockFilter) Selected() (FilterSample, bool) {
	if f.best < 0 {
		return FilterSample{}, false
	}
	return f.samples[f.best], true
}

// Jitter returns the estimated error of the current sample window. It is
// the root-mean-square of the differences between each sample's offset and
// the selected sample's offset, normalized by n-1 where n is the number of
// samples in the window:
//
//	jitter = sqrt( sum((offset_i - offset_best)^2) / (n-1) )
//
// Jitter is 0 when the window holds fewer than two samples.
func (f *ClockFilter) Jitter() time.Duration {
	return f.jitter
}

// Len returns the number of samples currently held by the filter.
func (f *ClockFilter) Len() int {
	return len(f.samples)
}

// Reset removes all samples and statistics from the filter, returning it to
// its initial state. No receive times, selections, or jitter values from
// before the reset are retained.
func (f *ClockFilter) Reset() {
	for i := range f.samples {
		f.samples[i] = FilterSample{}
	}
	f.samples = f.samples[:0]
	f.lastTime = time.Time{}
	f.best = -1
	f.jitter = 0
}

// expire removes all samples whose age relative to now exceeds the filter's
// maximum age.
func (f *ClockFilter) expire(now time.Time) {
	kept := f.samples[:0]
	for _, s := range f.samples {
		if now.Sub(s.Time) <= f.maxAge {
			kept = append(kept, s)
		}
	}
	for i := len(kept); i < len(f.samples); i++ {
		f.samples[i] = FilterSample{}
	}
	f.samples = kept
}

// update recomputes the selected sample and the jitter from the current
// sample window.
func (f *ClockFilter) update() {
	f.best = -1
	f.jitter = 0
	for i := range f.samples {
		if f.best < 0 || f.samples[i].RTT < f.samples[f.best].RTT {
			f.best = i
		}
	}
	if f.best < 0 {
		return
	}
	if n := len(f.samples); n > 1 {
		best := f.samples[f.best].Offset
		var sumSquares float64
		for _, s := range f.samples {
			d := float64(s.Offset - best)
			sumSquares += d * d
		}
		f.jitter = time.Duration(math.Sqrt(sumSquares / float64(n-1)))
	}
}

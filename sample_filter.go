package ntp

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

var (
	// ErrInvalidSample is returned when a sample cannot be stored in a
	// SampleFilter.
	ErrInvalidSample = errors.New("invalid sample")

	// ErrNegativeRTT is returned when a sample has a negative RTT.
	ErrNegativeRTT = errors.New("negative RTT")

	// ErrExpiredSample is returned when a sample's receive time is older
	// than the filter's maximum age.
	ErrExpiredSample = errors.New("expired sample")

	// ErrDuplicateReceiveTime is returned when a sample uses a receive time
	// already present in the filter.
	ErrDuplicateReceiveTime = errors.New("duplicate receive time")

	// ErrOutOfOrderReceiveTime is returned when a sample's receive time is
	// older than the newest receive time in the filter.
	ErrOutOfOrderReceiveTime = errors.New("out-of-order receive time")
)

// A Sample contains the timing information retained for one response from a
// single time source.
type Sample struct {
	// Offset is the clock offset measured by the response.
	Offset time.Duration

	// RTT is the round-trip time measured by the response.
	RTT time.Duration

	// Dispersion is the response's root synchronization dispersion.
	Dispersion time.Duration

	// ReceivedAt is the local time at which the response was received.
	ReceivedAt time.Time
}

// SampleFromResponse converts a Response into a Sample. The response is not
// validated; call AddResponse instead to validate it before adding it to a
// SampleFilter.
func SampleFromResponse(r *Response) (Sample, error) {
	if r == nil {
		return Sample{}, ErrInvalidSample
	}
	if r.Timestamps.ClientRecv.IsZero() {
		return Sample{}, fmt.Errorf("%w: zero receive time", ErrInvalidSample)
	}
	return Sample{
		Offset:     r.ClockOffset,
		RTT:        r.RTT,
		Dispersion: r.RootDispersion,
		ReceivedAt: r.Timestamps.ClientRecv,
	}, nil
}

// FilterStats contains the representative sample and statistics calculated
// from the unexpired samples in a SampleFilter.
type FilterStats struct {
	// Sample is the representative sample.
	Sample Sample

	// Jitter is the root-mean-square difference between the representative
	// sample's offset and the offsets of the other samples in the filter.
	// The RMS is calculated in nanoseconds and rounded down to the nearest
	// nanosecond.
	Jitter time.Duration

	// ErrorEstimate is the representative sample's synchronization error
	// estimate. It is calculated as RTT/2 + Dispersion + Jitter.
	ErrorEstimate time.Duration

	// Count is the number of unexpired samples used to calculate the
	// statistics.
	Count int
}

// A SampleFilter is a fixed-capacity window of recent samples from one time
// source. It does not read the system clock; the caller supplies the current
// time when adding samples or advancing the filter.
type SampleFilter struct {
	capacity int
	maxAge   time.Duration
	samples  []Sample
}

// NewSampleFilter creates a SampleFilter with the specified fixed capacity and
// maximum sample age. Both values must be positive.
func NewSampleFilter(capacity int, maxAge time.Duration) *SampleFilter {
	if capacity <= 0 {
		panic("ntp: sample filter capacity must be positive")
	}
	if maxAge <= 0 {
		panic("ntp: sample filter maximum age must be positive")
	}
	return &SampleFilter{
		capacity: capacity,
		maxAge:   maxAge,
		samples:  make([]Sample, 0, capacity),
	}
}

// Capacity returns the fixed capacity of the filter.
func (f *SampleFilter) Capacity() int {
	return f.capacity
}

// MaxAge returns the maximum age permitted for a sample in the filter.
func (f *SampleFilter) MaxAge() time.Duration {
	return f.maxAge
}

// Len returns the number of unexpired samples currently in the filter.
func (f *SampleFilter) Len() int {
	return len(f.samples)
}

// AddResponse validates a response, converts it into a Sample, and adds it to
// the filter. Invalid responses are not stored.
func (f *SampleFilter) AddResponse(now time.Time, r *Response) error {
	f.Advance(now)

	if r == nil {
		return ErrInvalidSample
	}
	if err := r.Validate(); err != nil {
		return err
	}
	sample, err := SampleFromResponse(r)
	if err != nil {
		return err
	}
	return f.Add(now, sample)
}

// Add removes expired samples, validates the new sample, and adds it to the
// filter. Receive times must be strictly increasing. When the filter is at
// capacity, the oldest sample is evicted before the new sample is stored.
func (f *SampleFilter) Add(now time.Time, sample Sample) error {
	f.Advance(now)

	if sample.ReceivedAt.IsZero() {
		return fmt.Errorf("%w: zero receive time", ErrInvalidSample)
	}
	if sample.RTT < 0 {
		return ErrNegativeRTT
	}
	if sample.Dispersion < 0 {
		return fmt.Errorf("%w: negative dispersion", ErrInvalidSample)
	}

	if age := now.Sub(sample.ReceivedAt); age >= f.maxAge {
		return ErrExpiredSample
	}
	if len(f.samples) > 0 {
		last := f.samples[len(f.samples)-1]
		switch {
		case sample.ReceivedAt.Equal(last.ReceivedAt):
			return ErrDuplicateReceiveTime
		case sample.ReceivedAt.Before(last.ReceivedAt):
			return ErrOutOfOrderReceiveTime
		}
	}

	if len(f.samples) == f.capacity {
		f.samples = f.samples[1:]
	}
	f.samples = append(f.samples, sample)
	return nil
}

// Advance removes samples whose age is at least the filter's maximum age. The
// supplied time must represent the caller's current clock value.
func (f *SampleFilter) Advance(now time.Time) {
	kept := f.samples[:0]
	for _, sample := range f.samples {
		if now.Before(sample.ReceivedAt) || now.Sub(sample.ReceivedAt) < f.maxAge {
			kept = append(kept, sample)
		}
	}
	f.samples = kept
}

// Samples returns a copy of the unexpired samples in receive-time order, from
// oldest to newest.
func (f *SampleFilter) Samples() []Sample {
	samples := make([]Sample, len(f.samples))
	copy(samples, f.samples)
	return samples
}

// Reset removes all samples and statistics from the filter. Configuration is
// retained.
func (f *SampleFilter) Reset() {
	f.samples = make([]Sample, 0, f.capacity)
}

// Stats selects the representative sample and calculates jitter and its error
// estimate from the unexpired samples. The representative sample is the one
// with the smallest RTT. If multiple samples have the same RTT, the oldest one
// is selected by the stable tie rule.
//
// Jitter is the RMS offset difference relative to the representative sample,
// using the other n-1 samples as the RMS population. For one sample, jitter is
// zero. ErrorEstimate is RTT/2 + Dispersion + Jitter.
func (f *SampleFilter) Stats() (FilterStats, bool) {
	if len(f.samples) == 0 {
		return FilterStats{}, false
	}

	candidates := make([]Sample, len(f.samples))
	copy(candidates, f.samples)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].RTT < candidates[j].RTT
	})
	selected := candidates[0]

	var squaredOffsetDifferences float64
	for _, sample := range f.samples {
		difference := sample.Offset - selected.Offset
		squaredOffsetDifferences += math.Pow(float64(difference.Nanoseconds()), 2)
	}
	jitter := time.Duration(0)
	if len(f.samples) > 1 {
		mean := squaredOffsetDifferences / float64(len(f.samples)-1)
		jitter = time.Duration(math.Sqrt(mean))
	}

	return FilterStats{
		Sample:        selected,
		Jitter:        jitter,
		ErrorEstimate: selected.RTT/2 + selected.Dispersion + jitter,
		Count:         len(f.samples),
	}, true
}

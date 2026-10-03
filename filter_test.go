package ntp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// filterResponse builds a response that passes validation and carries the
// given offset, RTT, and root dispersion.
func filterResponse(offset, rtt, dispersion time.Duration) *Response {
	return &Response{
		Stratum:        1,
		ClockOffset:    offset,
		RTT:            rtt,
		RootDispersion: dispersion,
	}
}

// TestClockFilterStepByStep walks a hand-computable scenario, inserting
// samples and advancing the caller's clock one step at a time. After every
// step the selected sample and the jitter are checked against values
// computed by hand.
//
// Filter parameters: capacity 4, maximum sample age 10s.
//
// Samples (receive times relative to t0):
//
//	s1: offset=10ms rtt=40ms disp=1ms at t0+0s
//	s2: offset=14ms rtt=20ms disp=2ms at t0+1s
//	s3: offset=18ms rtt=20ms disp=3ms at t0+2s
//	s4: offset=10ms rtt=60ms disp=4ms at t0+3s
//	s5: offset=10ms rtt=30ms disp=5ms at t0+4s
//
// Selection rule: lowest RTT wins; ties go to the earliest-received sample.
// Jitter rule: sqrt(sum((offset_i - offset_best)^2) / (n-1)).
func TestClockFilterStepByStep(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := NewClockFilter(4, 10*time.Second)

	assertSelected := func(offset, rtt, dispersion time.Duration, recv time.Time) {
		s, ok := f.Selected()
		if assert.True(t, ok, "expected a selected sample") {
			assert.Equal(t, offset, s.Offset)
			assert.Equal(t, rtt, s.RTT)
			assert.Equal(t, dispersion, s.Dispersion)
			assert.Equal(t, recv, s.Time)
		}
	}

	// Step 1: insert s1. Window: [s1]. Only one sample, so it is selected
	// and jitter is 0.
	assert.True(t, f.Add(filterResponse(10*time.Millisecond, 40*time.Millisecond, 1*time.Millisecond), t0))
	assert.Equal(t, 1, f.Len())
	assertSelected(10*time.Millisecond, 40*time.Millisecond, 1*time.Millisecond, t0)
	assert.Equal(t, time.Duration(0), f.Jitter())

	// Step 2: insert s2. Window: [s1 s2]. s2 has the lower RTT (20 < 40).
	// jitter = sqrt(((10-14)^2 + 0^2) / 1) = 4ms.
	assert.True(t, f.Add(filterResponse(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond), t0.Add(1*time.Second)))
	assert.Equal(t, 2, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 3: insert s3. Window: [s1 s2 s3]. s2 and s3 tie on RTT (20ms);
	// the tie breaks stably toward s2, the earlier arrival.
	// jitter = sqrt((16 + 0 + 16) / 2) = sqrt(16) = 4ms.
	assert.True(t, f.Add(filterResponse(18*time.Millisecond, 20*time.Millisecond, 3*time.Millisecond), t0.Add(2*time.Second)))
	assert.Equal(t, 3, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 4: rejected insertions. None of these may enter the window or
	// disturb the selection.
	// Duplicate receive time (equal to the last accepted sample's time).
	assert.False(t, f.Add(filterResponse(99*time.Millisecond, 1*time.Millisecond, 0), t0.Add(2*time.Second)))
	// Out-of-order receive time (earlier than the last accepted sample).
	assert.False(t, f.Add(filterResponse(99*time.Millisecond, 1*time.Millisecond, 0), t0.Add(1*time.Second)))
	// Negative RTT.
	assert.False(t, f.Add(filterResponse(99*time.Millisecond, -1*time.Millisecond, 0), t0.Add(3*time.Second)))
	// Response failing validation (stratum 0 is a kiss of death).
	assert.False(t, f.Add(&Response{Stratum: 0}, t0.Add(3*time.Second)))
	// Nil response.
	assert.False(t, f.Add(nil, t0.Add(3*time.Second)))
	assert.Equal(t, 3, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 5: insert s4 at t0+3s. This also proves the rejected samples
	// above did not update the last accepted receive time. The window is
	// now full: [s1 s2 s3 s4]. s2 still has the lowest RTT.
	// jitter = sqrt((16 + 0 + 16 + 16) / 3) = sqrt(16) = 4ms.
	assert.True(t, f.Add(filterResponse(10*time.Millisecond, 60*time.Millisecond, 4*time.Millisecond), t0.Add(3*time.Second)))
	assert.Equal(t, 4, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 6: insert s5 at t0+4s. The window is full, so the oldest sample
	// (s1) is evicted. Window: [s2 s3 s4 s5]. s2 still selected.
	// jitter = sqrt((0 + 16 + 16 + 16) / 3) = sqrt(16) = 4ms.
	assert.True(t, f.Add(filterResponse(10*time.Millisecond, 30*time.Millisecond, 5*time.Millisecond), t0.Add(4*time.Second)))
	assert.Equal(t, 4, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 7: advance to t0+11s. s2's age is exactly 10s, which does not
	// exceed the maximum age, so nothing expires.
	f.Advance(t0.Add(11 * time.Second))
	assert.Equal(t, 4, f.Len())
	assertSelected(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond, t0.Add(1*time.Second))
	assert.Equal(t, 4*time.Millisecond, f.Jitter())

	// Step 8: advance to t0+12s. s2 (age 11s) expires.
	// Window: [s3 s4 s5]. s3 now has the lowest RTT (20ms).
	// jitter = sqrt((0 + (10-18)^2 + (10-18)^2) / 2) = sqrt(64) = 8ms.
	f.Advance(t0.Add(12 * time.Second))
	assert.Equal(t, 3, f.Len())
	assertSelected(18*time.Millisecond, 20*time.Millisecond, 3*time.Millisecond, t0.Add(2*time.Second))
	assert.Equal(t, 8*time.Millisecond, f.Jitter())

	// Step 9: advance to t0+13s. s3 (age 11s) expires; s4's age is exactly
	// 10s, which does not exceed the maximum age. Window: [s4 s5].
	// s5 has the lower RTT (30 < 60). Both offsets are 10ms.
	// jitter = sqrt((0 + 0) / 1) = 0.
	f.Advance(t0.Add(13 * time.Second))
	assert.Equal(t, 2, f.Len())
	assertSelected(10*time.Millisecond, 30*time.Millisecond, 5*time.Millisecond, t0.Add(4*time.Second))
	assert.Equal(t, time.Duration(0), f.Jitter())

	// Step 10: advance to t0+14s. s4 (age 11s) expires. Window: [s5].
	f.Advance(t0.Add(14 * time.Second))
	assert.Equal(t, 1, f.Len())
	assertSelected(10*time.Millisecond, 30*time.Millisecond, 5*time.Millisecond, t0.Add(4*time.Second))
	assert.Equal(t, time.Duration(0), f.Jitter())

	// Step 11: advance far into the future. All samples expire and the
	// filter reports no selection.
	f.Advance(t0.Add(100 * time.Second))
	assert.Equal(t, 0, f.Len())
	_, ok := f.Selected()
	assert.False(t, ok)
	assert.Equal(t, time.Duration(0), f.Jitter())
}

// TestClockFilterReset verifies that Reset leaves no stale statistics
// behind: the window, the last accepted receive time, the selection, and
// the jitter are all cleared.
func TestClockFilterReset(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := NewClockFilter(4, 10*time.Second)

	assert.True(t, f.Add(filterResponse(10*time.Millisecond, 40*time.Millisecond, 1*time.Millisecond), t0))
	assert.True(t, f.Add(filterResponse(14*time.Millisecond, 20*time.Millisecond, 2*time.Millisecond), t0.Add(1*time.Second)))
	assert.Equal(t, 2, f.Len())

	f.Reset()
	assert.Equal(t, 0, f.Len())
	_, ok := f.Selected()
	assert.False(t, ok)
	assert.Equal(t, time.Duration(0), f.Jitter())

	// A sample arriving at the same receive time as a pre-reset sample must
	// be accepted, proving the last receive time was cleared.
	assert.True(t, f.Add(filterResponse(20*time.Millisecond, 30*time.Millisecond, 3*time.Millisecond), t0))
	assert.Equal(t, 1, f.Len())
	s, ok := f.Selected()
	if assert.True(t, ok) {
		assert.Equal(t, 20*time.Millisecond, s.Offset)
		assert.Equal(t, 30*time.Millisecond, s.RTT)
		assert.Equal(t, 3*time.Millisecond, s.Dispersion)
		assert.Equal(t, t0, s.Time)
	}
	assert.Equal(t, time.Duration(0), f.Jitter())
}

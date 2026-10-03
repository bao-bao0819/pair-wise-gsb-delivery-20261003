package ksuid

import (
	"testing"
	"time"
)

func TestTimeBoundsRoundingByNanosecond(t *testing.T) {
	const second = int64(1600000000)
	boundary := time.Unix(second, 0)

	tests := []struct {
		name         string
		start        time.Time
		end          time.Time
		includeStart bool
		includeEnd   bool
		empty        bool
		first        int64
		last         int64
	}{
		{name: "inclusive start one nanosecond before", start: boundary.Add(-time.Nanosecond), end: time.Unix(second+2, 0), includeStart: true, includeEnd: false, first: second, last: second + 1},
		{name: "inclusive start on boundary", start: boundary, end: time.Unix(second+2, 0), includeStart: true, includeEnd: false, first: second, last: second + 1},
		{name: "inclusive start one nanosecond after", start: boundary.Add(time.Nanosecond), end: time.Unix(second+2, 0), includeStart: true, includeEnd: false, first: second + 1, last: second + 1},
		{name: "exclusive start one nanosecond before", start: boundary.Add(-time.Nanosecond), end: time.Unix(second+2, 0), includeStart: false, includeEnd: false, first: second, last: second + 1},
		{name: "exclusive start on boundary", start: boundary, end: time.Unix(second+2, 0), includeStart: false, includeEnd: false, first: second + 1, last: second + 1},
		{name: "exclusive start one nanosecond after", start: boundary.Add(time.Nanosecond), end: time.Unix(second+2, 0), includeStart: false, includeEnd: false, first: second + 1, last: second + 1},
		{name: "inclusive end one nanosecond before", start: time.Unix(second, 0), end: boundary.Add(-time.Nanosecond), includeStart: true, includeEnd: true, empty: true},
		{name: "inclusive end on boundary", start: time.Unix(second, 0), end: boundary, includeStart: true, includeEnd: true, first: second, last: second},
		{name: "inclusive end one nanosecond after", start: time.Unix(second, 0), end: boundary.Add(time.Nanosecond), includeStart: true, includeEnd: true, first: second, last: second},
		{name: "exclusive end one nanosecond before", start: time.Unix(second-2, 0), end: boundary.Add(-time.Nanosecond), includeStart: true, includeEnd: false, first: second - 2, last: second - 1},
		{name: "exclusive end on boundary", start: time.Unix(second-2, 0), end: boundary, includeStart: true, includeEnd: false, first: second - 2, last: second - 1},
		{name: "exclusive end one nanosecond after", start: time.Unix(second-2, 0), end: boundary.Add(time.Nanosecond), includeStart: true, includeEnd: false, first: second - 2, last: second},
		{name: "closed zero width on boundary", start: boundary, end: boundary, includeStart: true, includeEnd: true, first: second, last: second},
		{name: "closed zero width one nanosecond before", start: boundary.Add(-time.Nanosecond), end: boundary.Add(-time.Nanosecond), includeStart: true, includeEnd: true, empty: true},
		{name: "half open zero width", start: boundary, end: boundary, includeStart: true, includeEnd: false, empty: true},
		{name: "open zero width", start: boundary, end: boundary, includeStart: false, includeEnd: false, empty: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bounds := NewTimeBounds(test.start, test.end, test.includeStart, test.includeEnd)
			if bounds.Empty != test.empty {
				t.Fatalf("Empty = %v, want %v", bounds.Empty, test.empty)
			}
			if test.empty {
				return
			}
			if got := int64(bounds.Lower.Timestamp()) + minUnixSecond; got != test.first {
				t.Fatalf("first second = %d, want %d", got, test.first)
			}
			if got := int64(bounds.Upper.Timestamp()) + minUnixSecond - 1; got != test.last {
				t.Fatalf("last second = %d, want %d", got, test.last)
			}
			if bounds.Lower.Payload()[0] != 0 || !isZeroPayload(bounds.Lower) {
				t.Fatal("lower bound must use the minimum payload")
			}
			if !isZeroPayload(bounds.Upper) {
				t.Fatal("upper bound must use the minimum payload")
			}
		})
	}
}

func TestTimeBoundsRepresentableEdges(t *testing.T) {
	minTime := time.Unix(minUnixSecond, 0)
	maxTime := time.Unix(maxUnixSecond, 0)

	t.Run("before epoch", func(t *testing.T) {
		bounds := NewTimeBounds(minTime.Add(-time.Nanosecond), minTime, true, true)
		if bounds.Empty || bounds.HasLower || !bounds.HasUpper {
			t.Fatalf("bad bounds: %+v", bounds)
		}
		if got := int64(bounds.Upper.Timestamp()) + minUnixSecond; got != minUnixSecond+1 {
			t.Fatalf("upper second = %d", got)
		}

		bounds = NewTimeBounds(time.Unix(minUnixSecond-2, 0), minTime.Add(-time.Nanosecond), true, true)
		if !bounds.Empty {
			t.Fatalf("expected empty bounds, got %+v", bounds)
		}
	})

	t.Run("after maximum second", func(t *testing.T) {
		bounds := NewTimeBounds(maxTime, maxTime.Add(time.Nanosecond), true, true)
		if bounds.Empty || !bounds.HasLower || bounds.HasUpper {
			t.Fatalf("bad bounds: %+v", bounds)
		}
		if got := int64(bounds.Lower.Timestamp()) + minUnixSecond; got != maxUnixSecond {
			t.Fatalf("lower second = %d", got)
		}

		bounds = NewTimeBounds(maxTime.Add(time.Nanosecond), maxTime.Add(time.Second), true, false)
		if !bounds.Empty {
			t.Fatalf("expected empty bounds, got %+v", bounds)
		}
	})

	t.Run("full encodable range", func(t *testing.T) {
		bounds := NewTimeBounds(time.Unix(minUnixSecond-1, 0), time.Unix(maxUnixSecond+1, 0), true, false)
		if bounds.Empty || bounds.HasLower || bounds.HasUpper {
			t.Fatalf("expected unbounded bounds, got %+v", bounds)
		}
		if !boundsContainsKSUID(bounds, Nil) || !boundsContainsKSUID(bounds, Max) {
			t.Fatal("full range must contain Nil and Max")
		}
	})
}

func TestTimeBoundsPredicates(t *testing.T) {
	anchors := []int64{
		minUnixSecond,
		1500000000,
		maxUnixSecond,
	}
	locations := []*time.Location{
		time.UTC,
		time.FixedZone("ahead", 5*3600+30*60),
		time.FixedZone("behind", -8*3600),
	}
	payloads := [][payloadLengthInBytes]byte{
		{},
		{15: 1},
		{15: 255},
		{0: 255},
		alternatingPayload(),
	}
	secondOffsets := []int64{-2, -1, 0, 1, 2}
	nanoOffsets := []time.Duration{-time.Nanosecond, 0, time.Nanosecond}
	endpointCases := []struct {
		includeStart bool
		includeEnd   bool
	}{
		{false, false},
		{false, true},
		{true, false},
		{true, true},
	}

	for _, anchor := range anchors {
		for _, location := range locations {
			for _, startOffset := range secondOffsets {
				for _, startNano := range nanoOffsets {
					for _, endOffset := range secondOffsets {
						for _, endNano := range nanoOffsets {
							for _, endpoints := range endpointCases {
								start := time.Unix(anchor+startOffset, 0).Add(startNano).In(location)
								end := time.Unix(anchor+endOffset, 0).Add(endNano).In(location)
								bounds := NewTimeBounds(start, end, endpoints.includeStart, endpoints.includeEnd)

								var expectedFirst, expectedLast int64
								matchesRepresentableSecond := false
								for second := anchor - 2; second <= anchor+2; second++ {
									if second < minUnixSecond || second > maxUnixSecond {
										continue
									}
									if timeInInterval(time.Unix(second, 0), start, end, endpoints.includeStart, endpoints.includeEnd) {
										if !matchesRepresentableSecond {
											expectedFirst = second
										}
										expectedLast = second
										matchesRepresentableSecond = true
									}
								}

								if bounds.Empty != !matchesRepresentableSecond {
									t.Fatalf("Empty = %v for %v to %v (%v,%v)", bounds.Empty, start, end, endpoints.includeStart, endpoints.includeEnd)
								}
								if bounds.Empty {
									continue
								}

								if expectedFirst == minUnixSecond {
									if bounds.HasLower {
										t.Fatalf("expected no lower bound, got %v", bounds.Lower)
									}
								} else {
									if !bounds.HasLower || int64(bounds.Lower.Timestamp())+minUnixSecond != expectedFirst || !isZeroPayload(bounds.Lower) {
										t.Fatalf("bad lower bound: %+v, first %d", bounds, expectedFirst)
									}
								}
								if expectedLast == maxUnixSecond {
									if bounds.HasUpper {
										t.Fatalf("expected no upper bound, got %v", bounds.Upper)
									}
								} else {
									upperSecond := int64(bounds.Upper.Timestamp()) + minUnixSecond
									if !bounds.HasUpper || upperSecond != expectedLast+1 || !isZeroPayload(bounds.Upper) {
										t.Fatalf("bad upper bound: %+v, last %d", bounds, expectedLast)
									}
									if Compare(bounds.Upper, Max) >= 0 {
										t.Fatal("upper bound must not be Max or a fabricated value beyond Max")
									}
								}

								for second := anchor - 2; second <= anchor+2; second++ {
									for _, candidateNano := range []int64{0, 1, int64(time.Second - 1)} {
										if second < minUnixSecond || second > maxUnixSecond {
											continue
										}
										candidateTime := time.Unix(second, candidateNano)
										encodedTime := time.Unix(second, 0)
										want := timeInInterval(encodedTime, start, end, endpoints.includeStart, endpoints.includeEnd)
										if got := boundsContainsTime(bounds, candidateTime); got != want {
											t.Fatalf("ContainsTime(%v) = %v, want %v for [%v,%v]", candidateTime, got, want, start, end)
										}
										for _, payload := range payloads {
											id := idFromUnixSecond(second, payload)
											if got := boundsContainsKSUID(bounds, id); got != want {
												t.Fatalf("ContainsKSUID(%v) = %v, want %v for [%v,%v]", id, got, want, start, end)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func timeInInterval(t, start, end time.Time, includeStart, includeEnd bool) bool {
	afterStart := t.After(start) || includeStart && t.Equal(start)
	beforeEnd := t.Before(end) || includeEnd && t.Equal(end)
	return afterStart && beforeEnd
}

func boundsContainsTime(bounds TimeBounds, t time.Time) bool {
	if bounds.Empty || t.Unix() < minUnixSecond || t.Unix() > maxUnixSecond {
		return false
	}
	if bounds.HasLower && t.Unix() < int64(bounds.Lower.Timestamp())+minUnixSecond {
		return false
	}
	if bounds.HasUpper && t.Unix() >= int64(bounds.Upper.Timestamp())+minUnixSecond {
		return false
	}
	return true
}

func boundsContainsKSUID(bounds TimeBounds, id KSUID) bool {
	if bounds.Empty {
		return false
	}
	if bounds.HasLower && Compare(id, bounds.Lower) < 0 {
		return false
	}
	if bounds.HasUpper && Compare(id, bounds.Upper) >= 0 {
		return false
	}
	return true
}

func isZeroPayload(id KSUID) bool {
	for _, value := range id.Payload() {
		if value != 0 {
			return false
		}
	}
	return true
}

func alternatingPayload() (payload [payloadLengthInBytes]byte) {
	for i := range payload {
		if i%2 == 0 {
			payload[i] = 0xaa
		} else {
			payload[i] = 0x55
		}
	}
	return payload
}

func idFromUnixSecond(second int64, payload [payloadLengthInBytes]byte) KSUID {
	id := ksuidFromUnixSecond(second)
	copy(id[timestampLengthInBytes:], payload[:])
	return id
}

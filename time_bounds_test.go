package ksuid

import (
	"testing"
	"time"
)

func TestTimeBoundsLowerEndpointRounding(t *testing.T) {
	second := time.Unix(1500000000, 0).UTC()
	end := time.Unix(maxUnixSecond, 0).UTC()

	tests := []struct {
		time      time.Time
		inclusive bool
		want      int64
	}{
		{second.Add(-time.Nanosecond), true, 1500000000},
		{second.Add(-time.Nanosecond), false, 1500000000},
		{second, true, 1500000000},
		{second, false, 1500000001},
		{second.Add(time.Nanosecond), true, 1500000001},
		{second.Add(time.Nanosecond), false, 1500000001},
	}

	for _, test := range tests {
		bounds := TimeBounds(test.time, end, test.inclusive, true)
		requireBounds(t, bounds, test.want, 0, false, true)
	}
}

func TestTimeBoundsUpperEndpointRounding(t *testing.T) {
	second := time.Unix(1500000000, 0).UTC()
	start := time.Unix(minUnixSecond, 0).UTC().Add(-time.Nanosecond)

	tests := []struct {
		time      time.Time
		inclusive bool
		last      int64
		upper     int64
	}{
		{second.Add(-time.Nanosecond), true, 1499999999, 1500000000},
		{second.Add(-time.Nanosecond), false, 1499999999, 1500000000},
		{second, true, 1500000000, 1500000001},
		{second, false, 1499999999, 1500000000},
		{second.Add(time.Nanosecond), true, 1500000000, 1500000001},
		{second.Add(time.Nanosecond), false, 1500000000, 1500000001},
	}

	for _, test := range tests {
		bounds := TimeBounds(start, test.time, true, test.inclusive)
		requireBounds(t, bounds, minUnixSecond, test.upper, true, true)

		idAtLast := testID(t, test.last, maxPayload())
		if Compare(idAtLast, bounds.Upper) >= 0 {
			t.Fatalf("ID at last second %d was not below upper %s", test.last, bounds.Upper)
		}
	}
}

func TestTimeBoundsEpochAndMaximumSeconds(t *testing.T) {
	minSecond := time.Unix(minUnixSecond, 0).UTC()
	maxSecond := time.Unix(maxUnixSecond, 0).UTC()

	tests := []struct {
		name           string
		start          time.Time
		end            time.Time
		startInclusive bool
		endInclusive   bool
		lower          int64
		upper          int64
		hasUpper       bool
		nonEmpty       bool
	}{
		{
			name:           "entirely before epoch",
			start:          minSecond.Add(-2 * time.Second),
			end:            minSecond.Add(-time.Nanosecond),
			startInclusive: true,
			endInclusive:   true,
			nonEmpty:       false,
		},
		{
			name:           "exact epoch endpoint excluded",
			start:          minSecond.Add(-time.Second),
			end:            minSecond,
			startInclusive: true,
			endInclusive:   false,
			nonEmpty:       false,
		},
		{
			name:           "one nanosecond ending at epoch includes epoch",
			start:          minSecond.Add(-time.Nanosecond),
			end:            minSecond,
			startInclusive: true,
			endInclusive:   true,
			lower:          minUnixSecond,
			upper:          minUnixSecond + 1,
			hasUpper:       true,
			nonEmpty:       true,
		},
		{
			name:           "epoch zero width inclusive",
			start:          minSecond,
			end:            minSecond,
			startInclusive: true,
			endInclusive:   true,
			lower:          minUnixSecond,
			upper:          minUnixSecond + 1,
			hasUpper:       true,
			nonEmpty:       true,
		},
		{
			name:           "exact maximum start included",
			start:          maxSecond,
			end:            maxSecond.Add(2 * time.Second),
			startInclusive: true,
			endInclusive:   false,
			lower:          maxUnixSecond,
			hasUpper:       false,
			nonEmpty:       true,
		},
		{
			name:           "exact maximum start excluded",
			start:          maxSecond,
			end:            maxSecond.Add(2 * time.Second),
			startInclusive: false,
			endInclusive:   false,
			nonEmpty:       false,
		},
		{
			name:           "one nanosecond after maximum start",
			start:          maxSecond.Add(time.Nanosecond),
			end:            maxSecond.Add(2 * time.Second),
			startInclusive: true,
			endInclusive:   false,
			nonEmpty:       false,
		},
		{
			name:           "one nanosecond before maximum through maximum",
			start:          maxSecond.Add(-time.Nanosecond),
			end:            maxSecond,
			startInclusive: true,
			endInclusive:   true,
			lower:          maxUnixSecond,
			hasUpper:       false,
			nonEmpty:       true,
		},
		{
			name:           "exact maximum end excluded",
			start:          maxSecond.Add(-time.Second),
			end:            maxSecond,
			startInclusive: true,
			endInclusive:   false,
			lower:          maxUnixSecond - 1,
			upper:          maxUnixSecond,
			hasUpper:       true,
			nonEmpty:       true,
		},
		{
			name:           "fractional maximum end excluded still includes maximum",
			start:          maxSecond.Add(-time.Nanosecond),
			end:            maxSecond.Add(time.Nanosecond),
			startInclusive: true,
			endInclusive:   false,
			lower:          maxUnixSecond,
			hasUpper:       false,
			nonEmpty:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bounds := TimeBounds(test.start, test.end, test.startInclusive, test.endInclusive)
			requireBounds(t, bounds, test.lower, test.upper, test.hasUpper, test.nonEmpty)
			if bounds.NonEmpty && !bounds.HasUpper {
				if Compare(Max, bounds.Lower) < 0 {
					t.Fatalf("maximum KSUID %s was excluded by lower bound %s", Max, bounds.Lower)
				}
			}
		})
	}
}

func TestTimeBoundsZeroWidthWindows(t *testing.T) {
	exact := time.Unix(1500000000, 0).UTC()
	fractional := exact.Add(time.Nanosecond)

	for _, includeStart := range []bool{true, false} {
		for _, includeEnd := range []bool{true, false} {
			bounds := TimeBounds(exact, exact, includeStart, includeEnd)
			requireBounds(t, bounds, 1500000000, 1500000001, true, includeStart && includeEnd)

			bounds = TimeBounds(fractional, fractional, includeStart, includeEnd)
			requireBounds(t, bounds, 0, 0, false, false)
		}
	}
}

func TestTimeBoundsNonUTCTimeZones(t *testing.T) {
	plus530 := time.FixedZone("+05:30", 5*60*60+30*60)
	minus8 := time.FixedZone("-08:00", -8*60*60)

	start := time.Unix(1499999999, int64(500*time.Millisecond)).In(plus530)
	end := time.Unix(1500000008, int64(500*time.Millisecond)).In(minus8)

	bounds := TimeBounds(start, end, true, true)
	requireBounds(t, bounds, 1500000000, 1500000009, true, true)
}

func TestTimeBoundsMatchesTimestampPredicate(t *testing.T) {
	const baseSecond = int64(1500000000)

	zones := []*time.Location{
		time.UTC,
		time.FixedZone("+05:30", 5*60*60+30*60),
		time.FixedZone("-08:00", -8*60*60),
	}
	payloads := [][]byte{
		make([]byte, payloadLengthInBytes),
		patternPayload(0x01),
		patternPayload(0x7f),
		patternPayload(0x80),
		patternPayload(0xff),
	}
	nanoseconds := []int64{-1, 0, 1}

	for _, zone := range zones {
		for startOffset := int64(-1); startOffset <= 3; startOffset++ {
			for _, startNano := range nanoseconds {
				for endOffset := int64(-1); endOffset <= 4; endOffset++ {
					for _, endNano := range nanoseconds {
						start := time.Unix(baseSecond+startOffset, startNano).In(zone)
						end := time.Unix(baseSecond+endOffset, endNano).In(zone)

						for _, includeStart := range []bool{true, false} {
							for _, includeEnd := range []bool{true, false} {
								bounds := TimeBounds(start, end, includeStart, includeEnd)

								for second := baseSecond - 2; second <= baseSecond+5; second++ {
									for _, payload := range payloads {
										id := testID(t, second, payload)
										want := timestampMatches(id.Time(), start, end, includeStart, includeEnd)

										got := false
										if bounds.NonEmpty {
											got = Compare(id, bounds.Lower) >= 0
											if bounds.HasUpper {
												got = got && Compare(id, bounds.Upper) < 0
											}
										}

										if got != want {
											t.Fatalf(
												"scan=%v timestamp=%v for id=%s in [%s,%s] start=%v end=%v bounds=%+v",
												got, want, id, start, end, includeStart, includeEnd, bounds,
											)
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

func requireBounds(t *testing.T, bounds Bounds, lower, upper int64, hasUpper, nonEmpty bool) {
	t.Helper()

	if bounds.NonEmpty != nonEmpty {
		t.Fatalf("NonEmpty=%v, want %v", bounds.NonEmpty, nonEmpty)
	}
	if !nonEmpty {
		if bounds != (Bounds{}) {
			t.Fatalf("empty bounds=%+v, want zero value", bounds)
		}
		return
	}

	if bounds.Lower != testID(t, lower, make([]byte, payloadLengthInBytes)) {
		t.Fatalf("Lower=%s, want second %d", bounds.Lower, lower)
	}
	if bounds.HasUpper != hasUpper {
		t.Fatalf("HasUpper=%v, want %v", bounds.HasUpper, hasUpper)
	}
	if hasUpper {
		if bounds.Upper != testID(t, upper, make([]byte, payloadLengthInBytes)) {
			t.Fatalf("Upper=%s, want second %d", bounds.Upper, upper)
		}
	} else if bounds.Upper != Nil {
		t.Fatalf("Upper=%s, want Nil when HasUpper is false", bounds.Upper)
	}
}

func timestampMatches(idTime, start, end time.Time, includeStart, includeEnd bool) bool {
	if includeStart {
		if idTime.Before(start) {
			return false
		}
	} else if !idTime.After(start) {
		return false
	}

	if includeEnd {
		if idTime.After(end) {
			return false
		}
	} else if !idTime.Before(end) {
		return false
	}

	return true
}

func testID(t *testing.T, second int64, payload []byte) KSUID {
	t.Helper()
	id, err := FromParts(time.Unix(second, 0).UTC(), payload)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func patternPayload(value byte) []byte {
	payload := make([]byte, payloadLengthInBytes)
	for i := range payload {
		payload[i] = value
	}
	return payload
}

func maxPayload() []byte {
	return patternPayload(0xff)
}

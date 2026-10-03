package ksuid

import (
	"encoding/binary"
	"math"
	"time"
)

const (
	minUnixSecond = epochStamp
	maxUnixSecond = epochStamp + (1<<32 - 1)
)

// Bounds is an ordered range that can be applied directly to KSUIDs.
//
// The range selects IDs whose encoded timestamp, interpreted as an exact
// UTC second, belongs to the requested time interval. KSUID does not retain
// sub-second generation time, so these bounds cannot reconstruct or query the
// original nanosecond.
type Bounds struct {
	// Lower is an inclusive KSUID with the first selected timestamp and a
	// minimum payload.
	Lower KSUID

	// Upper is an exclusive KSUID when HasUpper is true. It has one more
	// than the last selected timestamp and a minimum payload.
	Upper KSUID

	// HasUpper reports whether Upper is present. It is false when the range
	// extends through the maximum encodable KSUID timestamp.
	HasUpper bool

	// NonEmpty reports whether at least one KSUID timestamp is selected.
	NonEmpty bool
}

// TimeBounds returns exact lexicographic scan bounds for KSUIDs whose
// whole-second UTC timestamps belong to [start, end]. Set startInclusive and
// endInclusive to false to use an open endpoint.
//
// Times may be supplied in any time zone. A KSUID represents an exact UTC
// second, so non-integral endpoints select seconds by endpoint openness:
// the lower endpoint moves to floor(start)+1 and the upper endpoint selects
// floor(end). The exclusive scan boundary remains the beginning of the
// following second. An interval can therefore be empty even when its clock
// duration is positive, and a zero-width interval at an exact second selects
// that second.
func TimeBounds(start, end time.Time, startInclusive, endInclusive bool) Bounds {
	startSecond := start.Unix()
	endSecond := end.Unix()

	first := startSecond
	if !startInclusive || start.Nanosecond() != 0 {
		if first == math.MaxInt64 {
			return Bounds{}
		}
		first++
	}

	last := endSecond
	if !endInclusive && end.Nanosecond() == 0 {
		if last == math.MinInt64 {
			return Bounds{}
		}
		last--
	}

	if first < minUnixSecond {
		first = minUnixSecond
	}
	if last > maxUnixSecond {
		last = maxUnixSecond
	}

	if first > maxUnixSecond || last < minUnixSecond || first > last {
		return Bounds{}
	}

	bounds := Bounds{
		Lower:    ksuidWithUnixSecond(first),
		HasUpper: last < maxUnixSecond,
		NonEmpty: true,
	}

	if bounds.HasUpper {
		bounds.Upper = ksuidWithUnixSecond(last + 1)
	}

	return bounds
}

func ksuidWithUnixSecond(unixSecond int64) KSUID {
	var id KSUID
	binary.BigEndian.PutUint32(id[:timestampLengthInBytes], uint32(unixSecond-epochStamp))
	return id
}

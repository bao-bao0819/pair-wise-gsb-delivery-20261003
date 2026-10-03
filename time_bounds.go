package ksuid

import (
	"encoding/binary"
	"time"
)

const (
	minUnixSecond = epochStamp
	maxUnixSecond = int64(maxEncodedTimestamp) + epochStamp
)

const maxEncodedTimestamp uint32 = 1<<32 - 1

// TimeBounds describes lexicographic KSUID scan boundaries for a time
// interval. KSUIDs store time only at one-second resolution, so the bounds
// select IDs whose encoded whole second is in the interval. The nanosecond
// portion of the original generation time cannot be recovered from an ID.
//
// Lower is an inclusive bound with a zero payload. Upper, when HasUpper is
// true, is an exclusive bound with a zero payload. When HasUpper is false the
// interval extends through the maximum encodable KSUID timestamp.
type TimeBounds struct {
	// Lower is the smallest KSUID in the scan when HasLower is true.
	Lower    KSUID
	HasLower bool
	// Upper is the smallest KSUID after the scan when HasUpper is true.
	Upper    KSUID
	HasUpper bool
	// Empty reports that no encoded second intersects the requested interval.
	Empty bool
}

// NewTimeBounds returns scan boundaries for the interval selected by
// includeStart and includeEnd. Times in any location are accepted because the
// comparison uses their absolute UTC instant. A reversed interval and an
// interval that contains no encoded whole second produce Empty bounds.
//
// The start maps to the first whole second that can intersect the interval;
// the end maps to the last such second. A non-integer inclusive end includes
// its encoded second, while an exclusive end exactly on a second excludes it.
func NewTimeBounds(start, end time.Time, includeStart, includeEnd bool) TimeBounds {
	first := start.Unix()
	if !includeStart || !isWholeSecond(start) {
		first++
	}

	last := end.Unix()
	if !includeEnd && isWholeSecond(end) {
		last--
	}

	if first > last || last < minUnixSecond || first > maxUnixSecond {
		return TimeBounds{Empty: true}
	}

	if first < minUnixSecond {
		first = minUnixSecond
	}
	if last > maxUnixSecond {
		last = maxUnixSecond
	}

	var bounds TimeBounds
	if first > minUnixSecond {
		bounds.Lower = ksuidFromUnixSecond(first)
		bounds.HasLower = true
	}
	if last < maxUnixSecond {
		bounds.Upper = ksuidFromUnixSecond(last + 1)
		bounds.HasUpper = true
	}
	return bounds
}

func isWholeSecond(t time.Time) bool {
	return t.Nanosecond() == 0
}

func ksuidFromUnixSecond(second int64) KSUID {
	var id KSUID
	binary.BigEndian.PutUint32(id[:timestampLengthInBytes], uint32(second-minUnixSecond))
	return id
}

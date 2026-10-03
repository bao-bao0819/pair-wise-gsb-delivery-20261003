package ntp

import (
	"errors"
	"testing"
	"time"
)

func TestOfflineSampleFilterManual(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ms := func(value int64) time.Duration { return time.Duration(value) * time.Millisecond }
	at := func(second int64) time.Time { return base.Add(time.Duration(second) * time.Second) }

	filter := NewSampleFilter(4, 10*time.Second)

	samples := []Sample{
		{Offset: ms(2), RTT: ms(4), Dispersion: ms(1), ReceivedAt: at(1)},
		{Offset: ms(0), RTT: ms(2), Dispersion: ms(2), ReceivedAt: at(2)},
		{Offset: ms(2), RTT: ms(2), Dispersion: ms(1), ReceivedAt: at(3)},
		{Offset: ms(2), RTT: ms(6), Dispersion: ms(1), ReceivedAt: at(4)},
		{Offset: ms(2), RTT: ms(2), Dispersion: ms(1), ReceivedAt: at(5)},
	}

	expectedAfterInsert := []FilterStats{
		{Sample: samples[0], Jitter: ms(0), ErrorEstimate: ms(3), Count: 1},
		{Sample: samples[1], Jitter: ms(2), ErrorEstimate: ms(5), Count: 2},
		{Sample: samples[1], Jitter: ms(2), ErrorEstimate: ms(5), Count: 3},
		{Sample: samples[1], Jitter: ms(2), ErrorEstimate: ms(5), Count: 4},
		{Sample: samples[1], Jitter: ms(2), ErrorEstimate: ms(5), Count: 4},
	}

	for i, sample := range samples {
		if err := filter.Add(at(int64(i+1)), sample); err != nil {
			t.Fatalf("Add sample %d failed: %v", i, err)
		}
		assertFilterStats(t, filter, expectedAfterInsert[i])
	}

	if got := filter.Samples(); len(got) != 4 ||
		!got[0].ReceivedAt.Equal(samples[1].ReceivedAt) ||
		!got[3].ReceivedAt.Equal(samples[4].ReceivedAt) {
		t.Fatalf("capacity eviction failed, samples = %+v", got)
	}

	expectedAfterAdvance := []struct {
		now    time.Time
		stats  FilterStats
		exists bool
	}{
		{at(11), FilterStats{Sample: samples[1], Jitter: ms(2), ErrorEstimate: ms(5), Count: 4}, true},
		{at(12), FilterStats{Sample: samples[2], Jitter: ms(0), ErrorEstimate: ms(2), Count: 3}, true},
		{at(13), FilterStats{Sample: samples[4], Jitter: ms(0), ErrorEstimate: ms(2), Count: 2}, true},
		{at(14), FilterStats{Sample: samples[4], Jitter: ms(0), ErrorEstimate: ms(2), Count: 1}, true},
		{at(15), FilterStats{}, false},
	}

	for _, expected := range expectedAfterAdvance {
		filter.Advance(expected.now)
		_, exists := filter.Stats()
		if exists != expected.exists {
			t.Fatalf("Stats exists at %s = %v; want %v", expected.now, exists, expected.exists)
		}
		if exists {
			assertFilterStats(t, filter, expected.stats)
		}
	}
}

func TestOfflineSampleFilterRejectsInvalidSamples(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filter := NewSampleFilter(3, time.Minute)

	valid := Sample{
		Offset:     time.Millisecond,
		RTT:        2 * time.Millisecond,
		Dispersion: time.Millisecond,
		ReceivedAt: base,
	}
	if err := filter.Add(base, valid); err != nil {
		t.Fatalf("Add valid sample failed: %v", err)
	}

	cases := []struct {
		name   string
		sample Sample
		err    error
	}{
		{
			name: "negative RTT",
			sample: Sample{
				RTT:        -time.Millisecond,
				Dispersion: time.Millisecond,
				ReceivedAt: base.Add(time.Second),
			},
			err: ErrNegativeRTT,
		},
		{
			name: "negative dispersion",
			sample: Sample{
				RTT:        time.Millisecond,
				Dispersion: -time.Millisecond,
				ReceivedAt: base.Add(time.Second),
			},
			err: ErrInvalidSample,
		},
		{
			name: "zero receive time",
			sample: Sample{
				RTT:        time.Millisecond,
				Dispersion: time.Millisecond,
			},
			err: ErrInvalidSample,
		},
		{
			name: "expired",
			sample: Sample{
				RTT:        time.Millisecond,
				Dispersion: time.Millisecond,
				ReceivedAt: base.Add(-31 * time.Second),
			},
			err: ErrExpiredSample,
		},
		{
			name: "duplicate receive time",
			sample: Sample{
				RTT:        time.Millisecond,
				Dispersion: time.Millisecond,
				ReceivedAt: base,
			},
			err: ErrDuplicateReceiveTime,
		},
		{
			name: "out-of-order receive time",
			sample: Sample{
				RTT:        time.Millisecond,
				Dispersion: time.Millisecond,
				ReceivedAt: base.Add(-time.Second),
			},
			err: ErrOutOfOrderReceiveTime,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			caseFilter := NewSampleFilter(3, time.Minute)
			if err := caseFilter.Add(base, valid); err != nil {
				t.Fatalf("Add valid sample failed: %v", err)
			}

			now := base.Add(30 * time.Second)
			if c.name == "duplicate receive time" || c.name == "out-of-order receive time" {
				now = base.Add(2 * time.Second)
			}
			if err := caseFilter.Add(now, c.sample); !errors.Is(err, c.err) {
				t.Fatalf("Add error = %v; want %v", err, c.err)
			}
			if caseFilter.Len() != 1 {
				t.Fatalf("Len after rejected sample = %d; want 1", caseFilter.Len())
			}
		})
	}

	if got := filter.Len(); got != 1 {
		t.Fatalf("Len after rejected samples = %d; want 1", got)
	}
}

func TestOfflineSampleFilterRejectsInvalidResponse(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filter := NewSampleFilter(3, time.Minute)

	invalid := &Response{
		Version: 4,
		Stratum: maxStratum,
		Timestamps: ProtocolTimestamps{
			ClientRecv: base,
		},
	}
	if err := filter.AddResponse(base, invalid); err != ErrInvalidStratum {
		t.Fatalf("AddResponse error = %v; want %v", err, ErrInvalidStratum)
	}
	if filter.Len() != 0 {
		t.Fatalf("Len after invalid response = %d; want 0", filter.Len())
	}

	valid := &Response{
		ClockOffset:    2 * time.Millisecond,
		RTT:            4 * time.Millisecond,
		RootDispersion: time.Millisecond,
		Version:        4,
		Stratum:        1,
		Timestamps: ProtocolTimestamps{
			ClientRecv: base,
		},
	}
	if err := filter.AddResponse(base, valid); err != nil {
		t.Fatalf("AddResponse valid response failed: %v", err)
	}
}

func TestOfflineSampleFilterReset(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filter := NewSampleFilter(2, time.Minute)

	old := Sample{Offset: time.Millisecond, RTT: time.Millisecond, Dispersion: time.Millisecond, ReceivedAt: base}
	if err := filter.Add(base, old); err != nil {
		t.Fatalf("Add old sample failed: %v", err)
	}
	filter.Reset()

	if filter.Len() != 0 {
		t.Fatalf("Len after Reset = %d; want 0", filter.Len())
	}
	if _, exists := filter.Stats(); exists {
		t.Fatal("Stats returned old statistics after Reset")
	}

	fresh := Sample{Offset: 2 * time.Millisecond, RTT: 2 * time.Millisecond, Dispersion: 2 * time.Millisecond, ReceivedAt: base.Add(time.Second)}
	if err := filter.Add(base.Add(time.Second), fresh); err != nil {
		t.Fatalf("Add fresh sample failed: %v", err)
	}
	_, exists := filter.Stats()
	if !exists {
		t.Fatal("Stats missing after Reset and new sample")
	}
	assertFilterStats(t, filter, FilterStats{
		Sample:        fresh,
		Jitter:        0,
		ErrorEstimate: 3 * time.Millisecond,
		Count:         1,
	})
}

func assertFilterStats(t *testing.T, filter *SampleFilter, want FilterStats) {
	t.Helper()

	got, exists := filter.Stats()
	if !exists {
		t.Fatal("Stats missing")
	}
	if got.Count != want.Count {
		t.Fatalf("Count = %d; want %d", got.Count, want.Count)
	}
	if got.Jitter != want.Jitter {
		t.Fatalf("Jitter = %s; want %s", got.Jitter, want.Jitter)
	}
	if got.ErrorEstimate != want.ErrorEstimate {
		t.Fatalf("ErrorEstimate = %s; want %s", got.ErrorEstimate, want.ErrorEstimate)
	}
	if got.Sample != want.Sample {
		t.Fatalf("Sample = %+v; want %+v", got.Sample, want.Sample)
	}
}

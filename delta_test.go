package hyperloglog

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireSameRegisters(t *testing.T, want, got *Sketch) {
	t.Helper()
	w, g := want.Clone(), got.Clone()
	if w.sparse() {
		w.toNormal()
	}
	if g.sparse() {
		g.toNormal()
	}
	require.Equal(t, w.regs, g.regs)
}

func TestDeltaExchangeMatchesMerge(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	a, b := NewNoSparse(), NewNoSparse()
	baseA, baseB := a.Clone(), b.Clone()

	// Each side inserts hashes the other never sees.
	for i := 0; i < 2000; i++ {
		a.InsertHash(rng.Uint64())
	}
	for i := 0; i < 2000; i++ {
		b.InsertHash(rng.Uint64())
	}

	want := a.Clone()
	require.NoError(t, want.Merge(b))

	// Round 1: full snapshots relative to the empty baselines.
	da1, err := a.Delta(baseA)
	require.NoError(t, err)
	db1, err := b.Delta(baseB)
	require.NoError(t, err)
	require.NotZero(t, da1.Len())
	require.NotZero(t, db1.Len())

	// More insertions, then a second round of deltas relative to what the
	// peer has already been sent.
	sentA, sentB := a.Clone(), b.Clone()
	for i := 0; i < 500; i++ {
		a.InsertHash(rng.Uint64())
	}
	for i := 0; i < 500; i++ {
		b.InsertHash(rng.Uint64())
	}
	da2, err := a.Delta(sentA)
	require.NoError(t, err)
	db2, err := b.Delta(sentB)
	require.NoError(t, err)

	want = a.Clone()
	require.NoError(t, want.Merge(b))

	// Apply repeatedly and out of order: convergence must be idempotent.
	require.NoError(t, b.ApplyDelta(da2))
	require.NoError(t, b.ApplyDelta(da1))
	require.NoError(t, b.ApplyDelta(da2))
	require.NoError(t, b.ApplyDelta(da1))

	require.NoError(t, a.ApplyDelta(db2))
	require.NoError(t, a.ApplyDelta(db1))
	require.NoError(t, a.ApplyDelta(db1))
	require.NoError(t, a.ApplyDelta(db2))

	requireSameRegisters(t, want, a)
	requireSameRegisters(t, want, b)
	require.Equal(t, want.Estimate(), a.Estimate())
	require.Equal(t, want.Estimate(), b.Estimate())
}

func TestDeltaResetStartsNewGeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	a, b := NewNoSparse(), NewNoSparse()
	for i := 0; i < 1000; i++ {
		a.InsertHash(rng.Uint64())
		b.InsertHash(rng.Uint64())
	}

	db, err := b.Delta(nil)
	require.NoError(t, err)

	a.Reset()
	requireSameRegisters(t, NewNoSparse(), a)

	// The stale delta belongs to the previous generation and must not
	// revive the discarded data.
	require.NoError(t, a.ApplyDelta(db))
	requireSameRegisters(t, NewNoSparse(), a)

	// Deltas exported after the reset carry the new generation and apply.
	for i := 0; i < 100; i++ {
		a.InsertHash(rng.Uint64())
	}
	da, err := a.Delta(nil)
	require.NoError(t, err)
	snapA := a.Clone()
	a.Reset()
	require.NoError(t, a.ApplyDelta(da))
	requireSameRegisters(t, NewNoSparse(), a)

	// A peer at the same generation (one reset each) still accepts it.
	b.Reset()
	require.NoError(t, b.ApplyDelta(da))
	requireSameRegisters(t, snapA, b)
}

func TestApplyDeltaValidation(t *testing.T) {
	sk := NewNoSparse()
	sk.InsertHash(0x00010fffffffffff)
	before := sk.Clone()

	m := uint32(1) << 14
	for name, d := range map[string]*Delta{
		"index out of range": {Precision: 14, Index: []uint32{m}, Value: []uint8{1}},
		"rho too large":      {Precision: 14, Index: []uint32{0}, Value: []uint8{maxRho(14) + 1}},
		"length mismatch":    {Precision: 14, Index: []uint32{1, 2}, Value: []uint8{1}},
		"bad precision":      {Precision: 3, Index: []uint32{0}, Value: []uint8{1}},
	} {
		t.Run(name, func(t *testing.T) {
			err := sk.ApplyDelta(d)
			require.Error(t, err)
			requireSameRegisters(t, before, sk)
		})
	}

	// Precision mismatch with an initialized sketch.
	err := sk.ApplyDelta(&Delta{Precision: 16, Index: []uint32{0}, Value: []uint8{1}})
	require.ErrorIs(t, err, ErrorPrecisionMismatch)
	requireSameRegisters(t, before, sk)

	// A delta from another generation is ignored, not applied.
	err = sk.ApplyDelta(&Delta{Precision: 14, Generation: sk.generation + 1, Index: []uint32{0}, Value: []uint8{10}})
	require.NoError(t, err)
	requireSameRegisters(t, before, sk)

	// A nil delta is a no-op.
	require.NoError(t, sk.ApplyDelta(nil))
	requireSameRegisters(t, before, sk)
}

func TestDeltaSparseConversion(t *testing.T) {
	rng := rand.New(rand.NewSource(11))

	a, b := New(), New() // both sparse
	for i := 0; i < 100; i++ {
		a.InsertHash(rng.Uint64())
	}
	for i := 0; i < 100; i++ {
		b.InsertHash(rng.Uint64())
	}
	require.True(t, a.sparse())
	require.True(t, b.sparse())

	want := a.Clone()
	require.NoError(t, want.Merge(b))

	// Exporting from a sparse sketch converts it to dense.
	da, err := a.Delta(nil)
	require.NoError(t, err)
	require.False(t, a.sparse())

	// Applying to a sparse sketch converts it too; no encoding is mixed.
	require.NoError(t, b.ApplyDelta(da))
	require.False(t, b.sparse())
	requireSameRegisters(t, want, b)

	// A sparse baseline is converted on a copy and left untouched.
	sparseBase := New()
	for i := 0; i < 50; i++ {
		sparseBase.InsertHash(rng.Uint64())
	}
	dense := sparseBase.Clone()
	dense.toNormal()
	extra := dense.Clone()
	extra.InsertHash(rng.Uint64())

	d, err := extra.Delta(sparseBase)
	require.NoError(t, err)
	require.True(t, sparseBase.sparse(), "baseline must not be mutated")
	require.Equal(t, 1, d.Len())

	got := sparseBase.Clone()
	require.NoError(t, got.ApplyDelta(d))
	requireSameRegisters(t, extra, got)
}

func TestApplyDeltaZeroValueAdopts(t *testing.T) {
	src := New16NoSparse()
	for i := uint64(0); i < 500; i++ {
		src.InsertHash(i * 0x9e3779b97f4a7c15)
	}
	d, err := src.Delta(nil)
	require.NoError(t, err)

	var sk Sketch
	require.NoError(t, sk.ApplyDelta(d))
	requireSameRegisters(t, src, &sk)

	// A delta from a different generation no longer applies after adoption.
	d2 := &Delta{Precision: 16, Generation: d.Generation + 1, Index: []uint32{0}, Value: []uint8{maxRho(16)}}
	require.NoError(t, sk.ApplyDelta(d2))
	requireSameRegisters(t, src, &sk)
}

func TestDeltaPrecisionMismatch(t *testing.T) {
	a, b := NewNoSparse(), New16NoSparse()
	a.InsertHash(1)
	b.InsertHash(1)

	_, err := a.Delta(b)
	require.ErrorIs(t, err, ErrorPrecisionMismatch)

	_, err = a.Delta(nil)
	require.NoError(t, err)

	var zero Sketch
	_, err = zero.Delta(nil)
	require.ErrorIs(t, err, ErrorInvalidPrecision)
}

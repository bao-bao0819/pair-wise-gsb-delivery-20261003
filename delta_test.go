package hyperloglog

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func newDense(t *testing.T, p uint8) *Sketch {
	t.Helper()
	sk, err := NewSketch(p, false)
	require.NoError(t, err)
	return sk
}

func insertHashes(sk *Sketch, hashes []uint64) {
	for _, h := range hashes {
		sk.InsertHash(h)
	}
}

func partitionHashes(seed, count int) []uint64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	hashes := make([]uint64, count)
	for i := range hashes {
		hashes[i] = rng.Uint64()
	}
	return hashes
}

func requireSameRegs(t *testing.T, want, got *Sketch) {
	t.Helper()
	require.Equal(t, want.regs, got.regs)
}

func TestDelta_GenerationLifecycle(t *testing.T) {
	sk := newDense(t, 14)
	require.EqualValues(t, 1, sk.Generation())

	insertHashes(sk, partitionHashes(1, 100))
	sk.Reset()
	require.EqualValues(t, 2, sk.Generation())

	sk.Reset()
	require.EqualValues(t, 3, sk.Generation())

	var zero Sketch
	require.Zero(t, zero.Generation())

	clone := sk.Clone()
	require.Equal(t, sk.Generation(), clone.Generation())

	// Generation is not persisted by the binary encoding; decoding restores
	// the initial generation.
	data, err := sk.MarshalBinary()
	require.NoError(t, err)
	decoded := newDense(t, 14)
	require.NoError(t, decoded.UnmarshalBinary(data))
	require.EqualValues(t, 1, decoded.Generation())
}

func TestDeltaSince_OnlyImprovedRegistersAndSorted(t *testing.T) {
	const p = uint8(12)
	sk := newDense(t, p)
	baseline := newDense(t, p)

	insertHashes(sk, partitionHashes(7, 500))

	delta, err := sk.DeltaSince(baseline)
	require.NoError(t, err)
	require.Equal(t, p, delta.Precision)
	require.EqualValues(t, 1, delta.Generation)

	// Exactly the non-zero registers are exported against the zero baseline,
	// each once, in index order.
	var want []RegisterChange
	for i, v := range sk.regs {
		if v > 0 {
			want = append(want, RegisterChange{uint32(i), v})
		}
	}
	require.Equal(t, want, delta.Changes)
	require.True(t, slices.IsSortedFunc(delta.Changes, func(a, b RegisterChange) int {
		return int(a.Index) - int(b.Index)
	}))

	// Applying the delta to the baseline reproduces the registers.
	require.NoError(t, baseline.ApplyDelta(delta))
	requireSameRegs(t, sk, baseline)

	// A second export relative to the synchronized baseline is empty.
	again, err := sk.DeltaSince(baseline)
	require.NoError(t, err)
	require.Empty(t, again.Changes)

	// Raising one register exports only that register.
	sk.InsertHash(hash([]byte("a fresh and unique value")))
	inc, err := sk.DeltaSince(baseline)
	require.NoError(t, err)
	require.Len(t, inc.Changes, 1)
}

func TestDelta_ExchangeMatchesMerge(t *testing.T) {
	const p = uint8(14)

	for _, rounds := range []int{1, 3, 6} {
		t.Run(fmt.Sprintf("rounds=%d", rounds), func(t *testing.T) {
			a, b := newDense(t, p), newDense(t, p)
			baseA, baseB := newDense(t, p), newDense(t, p)

			// Reference sketches only ever receive the local insertions;
			// they are merged directly once at the end.
			localA, localB := newDense(t, p), newDense(t, p)

			for round := range rounds {
				ha := partitionHashes(100+round, 400)
				hb := partitionHashes(900+round, 400)

				insertHashes(a, ha)
				insertHashes(b, hb)
				insertHashes(localA, ha)
				insertHashes(localB, hb)

				// Export both deltas from the pre-exchange state before
				// applying anything, so each side sends only its local
				// changes relative to its last synchronized snapshot.
				da, err := a.DeltaSince(baseA)
				require.NoError(t, err)
				db, err := b.DeltaSince(baseB)
				require.NoError(t, err)

				require.NoError(t, b.ApplyDelta(da))
				require.NoError(t, a.ApplyDelta(db))

				// Both sides now hold the union; snapshot it as the new
				// exchange baseline.
				requireSameRegs(t, a, b)
				copy(baseA.regs, a.regs)
				copy(baseB.regs, b.regs)
			}

			merged := newDense(t, p)
			require.NoError(t, merged.Merge(localA))
			require.NoError(t, merged.Merge(localB))

			requireSameRegs(t, merged, a)
			requireSameRegs(t, merged, b)
			require.Equal(t, merged.Estimate(), a.Estimate())
			require.Equal(t, merged.Estimate(), b.Estimate())
		})
	}
}

func TestDelta_IdempotentAndOutOfOrder(t *testing.T) {
	const p = uint8(14)
	sk := newDense(t, p)
	insertHashes(sk, partitionHashes(42, 300))

	delta, err := sk.DeltaSince(newDense(t, p))
	require.NoError(t, err)
	require.NotEmpty(t, delta.Changes)

	target := newDense(t, p)
	require.NoError(t, target.ApplyDelta(delta))
	regsAfterFirst := slices.Clone(target.regs)

	// Applying the same delta again must be a no-op.
	require.NoError(t, target.ApplyDelta(delta))
	require.Equal(t, regsAfterFirst, target.regs)

	// A shuffled copy, padded with duplicate entries, converges too.
	shuffled := Delta{
		Precision:  p,
		Generation: 1,
		Changes:    append(slices.Clone(delta.Changes), delta.Changes[0], delta.Changes[len(delta.Changes)/2]),
	}
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(shuffled.Changes), func(i, j int) {
		shuffled.Changes[i], shuffled.Changes[j] = shuffled.Changes[j], shuffled.Changes[i]
	})

	outOfOrder := newDense(t, p)
	require.NoError(t, outOfOrder.ApplyDelta(shuffled))
	require.Equal(t, regsAfterFirst, outOfOrder.regs)

	// An empty delta is a valid no-op.
	require.NoError(t, target.ApplyDelta(Delta{Precision: p, Generation: 1}))
	require.Equal(t, regsAfterFirst, target.regs)
}

func TestDelta_ResetRejectsStaleDelta(t *testing.T) {
	const p = uint8(14)
	sk := newDense(t, p)
	insertHashes(sk, partitionHashes(5, 200))

	oldDelta, err := sk.DeltaSince(newDense(t, p))
	require.NoError(t, err)
	require.EqualValues(t, 1, oldDelta.Generation)

	sk.Reset()
	require.Zero(t, sk.Estimate())
	require.EqualValues(t, 2, sk.Generation())

	// The stale delta is refused and clears nothing back in.
	err = sk.ApplyDelta(oldDelta)
	require.ErrorIs(t, err, ErrorGenerationMismatch)
	for i, v := range sk.regs {
		require.Zero(t, v, "register %d revived by stale delta", i)
	}

	// Fresh inserts in the new generation exchange fine, and a forged
	// generation-1 delta is still rejected.
	insertHashes(sk, partitionHashes(6, 50))
	fresh, err := sk.DeltaSince(newDense(t, p))
	require.NoError(t, err)
	require.EqualValues(t, 2, fresh.Generation)
	target := newDense(t, p)
	target.Reset() // advance the receiver to generation 2
	require.NoError(t, target.ApplyDelta(fresh))

	stale := slices.Clone(fresh.Changes)
	err = sk.ApplyDelta(Delta{Precision: p, Generation: 1, Changes: stale})
	require.ErrorIs(t, err, ErrorGenerationMismatch)
}

func TestDelta_ApplyRejectsInvalid(t *testing.T) {
	const p = uint8(14)
	sk := newDense(t, p)

	good := Delta{Precision: p, Generation: 1, Changes: []RegisterChange{{Index: 3, Rho: 7}}}

	t.Run("precision mismatch", func(t *testing.T) {
		d := good
		d.Precision = 16
		err := sk.ApplyDelta(d)
		require.ErrorIs(t, err, ErrorPrecisionMismatch)
	})

	t.Run("zero-value sketch", func(t *testing.T) {
		var zero Sketch
		err := zero.ApplyDelta(good)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrorSparseUnsupported)
	})

	t.Run("index out of range", func(t *testing.T) {
		d := good
		d.Changes = []RegisterChange{{Index: sk.m, Rho: 1}}
		err := sk.ApplyDelta(d)
		require.ErrorIs(t, err, ErrorInvalidData)
	})

	t.Run("rho out of range", func(t *testing.T) {
		d := good
		d.Changes = []RegisterChange{{Index: 3, Rho: maxRho(p) + 1}}
		err := sk.ApplyDelta(d)
		require.ErrorIs(t, err, ErrorInvalidData)
	})

	t.Run("bad entry after a good one leaves registers untouched", func(t *testing.T) {
		d := good
		d.Changes = []RegisterChange{{Index: 1, Rho: 9}, {Index: sk.m + 10, Rho: 1}}
		err := sk.ApplyDelta(d)
		require.ErrorIs(t, err, ErrorInvalidData)
		require.Zero(t, sk.regs[1])
	})
}

func TestDeltaSince_RejectsInvalid(t *testing.T) {
	sk := newDense(t, 14)

	_, err := sk.DeltaSince(nil)
	require.Error(t, err)

	_, err = sk.DeltaSince(newDense(t, 16))
	require.ErrorIs(t, err, ErrorPrecisionMismatch)

	var zero Sketch
	_, err = sk.DeltaSince(&zero)
	require.Error(t, err)

	_, err = zero.DeltaSince(sk)
	require.Error(t, err)
}

func TestDelta_SparseRejectedOrConverted(t *testing.T) {
	const p = uint8(14)

	// Dense deltas must never touch sparse sketches or their encoding.
	dense := newDense(t, p)
	insertHashes(dense, partitionHashes(8, 10))
	delta, err := dense.DeltaSince(newDense(t, p))
	require.NoError(t, err)

	sparse, err := NewSketch(p, true)
	require.NoError(t, err)
	insertHashes(sparse, partitionHashes(9, 10))

	_, err = sparse.DeltaSince(newDense(t, p))
	require.ErrorIs(t, err, ErrorSparseUnsupported)
	_, err = newDense(t, p).DeltaSince(sparse)
	require.ErrorIs(t, err, ErrorSparseUnsupported)
	err = newDense(t, p).ApplyDelta(delta)
	require.NoError(t, err) // sanity: the delta itself is valid for dense
	err = sparse.ApplyDelta(delta)
	require.ErrorIs(t, err, ErrorSparseUnsupported)

	// Explicit conversion works and keeps the registers; afterwards deltas
	// flow and match a direct Merge of the same data.
	genBefore := sparse.Generation()
	require.NoError(t, sparse.ToDense())
	require.False(t, sparse.sparse())
	require.Equal(t, genBefore, sparse.Generation())

	converted := newDense(t, p)
	d, err := sparse.DeltaSince(newDense(t, p))
	require.NoError(t, err)
	require.NoError(t, converted.ApplyDelta(d))
	requireSameRegs(t, sparse, converted)

	merged := newDense(t, p)
	require.NoError(t, merged.Merge(sparse))
	requireSameRegs(t, merged, converted)

	// ToDense on an already dense sketch is a no-op; the zero value refuses.
	require.NoError(t, dense.ToDense())
	var zero Sketch
	require.Error(t, zero.ToDense())
}

package hyperloglog

import (
	"errors"
	"fmt"
)

// RegisterChange is a single register that a Delta raises. Rho is the absolute
// register value of the dense representation, never a sparse hash encoding.
type RegisterChange struct {
	Index uint32
	Rho   uint8
}

// Delta is the dense-Sketch increment exchanged relative to a baseline: the
// precision and generation of the exporting Sketch, and the registers whose
// values were raised above the caller's baseline, each with its absolute rho.
//
// Delta is an in-memory value type, not a serialized format. Its entries are
// returned sorted and unique by DeltaSince, but ApplyDelta accepts them in any
// order, including duplicates. Since every entry carries an absolute rho and
// application is an element-wise maximum, applying the same Delta twice, or a
// set of Deltas in any order, converges to the same result: application is
// idempotent and commutative.
//
// A Delta is tied to its generation. ApplyDelta rejects a Delta whose
// generation is not the receiver's current generation, so after a Reset an old
// Delta can never revive the registers the Reset cleared.
type Delta struct {
	Precision  uint8
	Generation uint64
	Changes    []RegisterChange
}

// ErrorGenerationMismatch is wrapped by ApplyDelta when the Delta's generation
// does not match the receiving Sketch's current generation, in particular
// after the receiving Sketch was Reset.
var ErrorGenerationMismatch = errors.New("delta generation does not match sketch generation")

// ErrorSparseUnsupported is wrapped by DeltaSince and ApplyDelta when either
// Sketch involved uses the sparse representation. Dense deltas carry dense
// register values and must never be mixed with the internal sparse hash
// encoding. Convert a sparse Sketch explicitly with ToDense before exchanging
// deltas.
var ErrorSparseUnsupported = errors.New("dense delta exchange does not support sparse sketches")

// Generation returns sk's current generation. A Sketch created with NewSketch
// or decoded with UnmarshalBinary starts at generation 1, and every successful
// Reset moves to a fresh generation. The generation is not part of the binary
// encoding and is only meaningful for delta exchange: the zero-value Sketch,
// which has no precision yet, reports 0.
func (sk *Sketch) Generation() uint64 {
	return sk.generation
}

// DeltaSince exports the dense registers of sk whose rho is strictly greater
// than the rho at the same index in the dense baseline, together with sk's
// precision and generation. baseline is a snapshot of the registers the
// receiving side already holds, typically a Clone of the exporting Sketch made
// at the time of the previous exchange; a fresh dense Sketch of the same
// precision serves as an all-zero baseline.
//
// The returned Changes are sorted by Index with no duplicates. baseline's
// generation is intentionally not consulted: it is only a register snapshot,
// and may predate a Reset of the baseline holder.
//
// Both sk and baseline must be initialized dense Sketches of the same
// precision. A sparse Sketch on either side is rejected with
// ErrorSparseUnsupported; convert it explicitly with ToDense. No registers
// change when an error is returned.
func (sk *Sketch) DeltaSince(baseline *Sketch) (Delta, error) {
	if baseline == nil {
		return Delta{}, errors.New("hyperloglog: delta baseline is nil")
	}
	if err := requireDenseForDelta(sk); err != nil {
		return Delta{}, err
	}
	if err := requireDenseForDelta(baseline); err != nil {
		return Delta{}, fmt.Errorf("hyperloglog: baseline %w", err)
	}
	if sk.p != baseline.p {
		return Delta{}, fmt.Errorf("hyperloglog: cannot export delta relative to precision %d from precision %d: %w", baseline.p, sk.p, ErrorPrecisionMismatch)
	}

	delta := Delta{
		Precision:  sk.p,
		Generation: sk.generation,
	}
	for i, v := range sk.regs {
		if v > baseline.regs[i] {
			delta.Changes = append(delta.Changes, RegisterChange{Index: uint32(i), Rho: v})
		}
	}
	return delta, nil
}

// ApplyDelta raises the registers of sk according to d: for every entry the
// register at Index becomes the maximum of its current value and Rho. Because
// this is a maximum, applying a Delta more than once is a no-op after the
// first application, and Deltas may be applied in any order.
//
// d and sk are fully validated before any register is touched, so an error
// leaves sk unchanged. sk must be an initialized dense Sketch: the zero-value
// Sketch and sparse Sketches are rejected (a sparse Sketch has to be converted
// explicitly with ToDense; dense register values and sparse hash encodings
// must never be mixed). d.Precision must equal sk's precision, d.Generation
// must equal sk's current generation (see Generation; a Delta that predates a
// Reset is rejected), and every Index must be within 2^d.Precision and every
// Rho within 64-d.Precision+1. Empty and duplicate entries are allowed.
func (sk *Sketch) ApplyDelta(d Delta) error {
	if err := requireDenseForDelta(sk); err != nil {
		return err
	}
	if d.Precision != sk.p {
		return fmt.Errorf("hyperloglog: cannot apply precision %d delta to precision %d sketch: %w", d.Precision, sk.p, ErrorPrecisionMismatch)
	}
	if d.Generation != sk.generation {
		return fmt.Errorf("hyperloglog: delta generation %d, sketch generation %d: %w", d.Generation, sk.generation, ErrorGenerationMismatch)
	}

	maxRho := maxRho(sk.p)
	for _, change := range d.Changes {
		if change.Index >= sk.m {
			return fmt.Errorf("hyperloglog: delta register index %d, max %d: %w", change.Index, sk.m-1, ErrorInvalidData)
		}
		if change.Rho > maxRho {
			return fmt.Errorf("hyperloglog: delta register %d rho %d, max %d: %w", change.Index, change.Rho, maxRho, ErrorInvalidData)
		}
	}

	for _, change := range d.Changes {
		if change.Rho > sk.regs[change.Index] {
			sk.regs[change.Index] = change.Rho
		}
	}
	return nil
}

// ToDense converts sk from the sparse representation to the dense register
// representation in place. Register values are preserved; only the encoding
// changes. It is the explicit conversion required before DeltaSince and
// ApplyDelta, which refuse to mix dense deltas with the sparse encoding. It is
// a no-op on a Sketch that is already dense. The zero-value Sketch cannot be
// converted and is rejected.
func (sk *Sketch) ToDense() error {
	if sk.p == 0 {
		return errors.New("hyperloglog: cannot convert zero-value sketch to dense")
	}
	if sk.sparse() {
		sk.toNormal()
	}
	return nil
}

// requireDenseForDelta rejects sketches that cannot take part in dense delta
// exchange: the zero-value sketch, which has no precision to check against,
// and the sparse sketch, whose internal hash encoding differs from dense
// register values.
func requireDenseForDelta(sk *Sketch) error {
	if sk == nil {
		return errors.New("hyperloglog: sketch is nil")
	}
	if sk.p == 0 {
		return errors.New("hyperloglog: sketch is not initialized")
	}
	if sk.sparse() {
		return ErrorSparseUnsupported
	}
	return nil
}

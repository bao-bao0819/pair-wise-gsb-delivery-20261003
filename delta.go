package hyperloglog

import "fmt"

// Delta is an incremental update to a dense Sketch: the registers that
// increased relative to a baseline, exported by Sketch.Delta and applied to
// another compatible sketch with Sketch.ApplyDelta.
//
// A Delta carries the precision and the generation of the sketch it was
// exported from, alongside the register indexes and values. Index and Value
// are parallel slices of equal length: Value[k] is the value of register
// Index[k].
//
// Applying a delta is idempotent and order independent: registers only ever
// take the maximum, so reapplying the same delta or applying several deltas
// out of order converges to the same state as merging the sketches the
// deltas were exported from.
type Delta struct {
	Precision  uint8
	Generation uint64
	Index      []uint32
	Value      []uint8
}

// Len returns the number of register updates in d.
func (d *Delta) Len() int { return len(d.Index) }

// Delta returns the registers of sk that are strictly greater than the
// corresponding registers of base, as a Delta tagged with sk's precision and
// generation. A nil or zero-value base is treated as empty, so Delta(nil)
// exports every non-zero register.
//
// The delta exchange protocol works on dense registers. If sk is sparse it
// is converted to the dense representation, the same way Merge converts it.
// A sparse base is not mixed into the dense encoding either: it is converted
// on a copy, leaving base itself untouched.
//
// Delta returns an error wrapping ErrorInvalidPrecision when sk is
// uninitialized, and an error wrapping ErrorPrecisionMismatch when base has
// a different precision.
func (sk *Sketch) Delta(base *Sketch) (*Delta, error) {
	if sk.p == 0 {
		return nil, fmt.Errorf("hyperloglog: precision %d: %w", sk.p, ErrorInvalidPrecision)
	}
	if base != nil && base.p != 0 && base.p != sk.p {
		return nil, fmt.Errorf("hyperloglog: cannot diff precision %d against precision %d: %w", sk.p, base.p, ErrorPrecisionMismatch)
	}

	if sk.sparse() {
		sk.toNormal()
	}

	var baseRegs []uint8
	if base != nil && base.p != 0 {
		b := base
		if b.sparse() {
			b = b.Clone()
			b.toNormal()
		}
		baseRegs = b.regs
	}

	d := &Delta{Precision: sk.p, Generation: sk.generation}
	for i, v := range sk.regs {
		var bv uint8
		if baseRegs != nil {
			bv = baseRegs[i]
		}
		if v > bv {
			d.Index = append(d.Index, uint32(i))
			d.Value = append(d.Value, v)
		}
	}
	return d, nil
}

// ApplyDelta applies d to sk by raising every register d names to d's value
// for it. Because registers only take the maximum, applying the same delta
// twice or applying several deltas of one generation out of order has the
// same effect as applying them once in order.
//
// A delta only applies to the generation it was exported from: if d's
// generation differs from sk's, ApplyDelta is a no-op. In particular a delta
// exported before sk was Reset belongs to the previous generation and cannot
// revive the data the reset discarded.
//
// A zero-value sk adopts the delta's precision, generation and dense
// representation, the same way Merge adopts another sketch's configuration.
// A sparse sk is converted to the dense representation before applying;
// sparse encodings are never mixed with delta registers.
//
// The delta is fully validated before sk is modified in any way: a precision
// mismatch returns an error wrapping ErrorPrecisionMismatch, and mismatched
// Index and Value lengths, an index >= 2^precision or a value above the
// maximum rho return an error wrapping ErrorInvalidData. When an error is
// returned sk is left unchanged.
func (sk *Sketch) ApplyDelta(d *Delta) error {
	if d == nil {
		return nil
	}
	if err := checkPrecision(d.Precision); err != nil {
		return fmt.Errorf("hyperloglog: delta precision %d: %w", d.Precision, err)
	}
	if len(d.Index) != len(d.Value) {
		return fmt.Errorf("hyperloglog: delta has %d indexes but %d values: %w", len(d.Index), len(d.Value), ErrorInvalidData)
	}
	if sk.p != 0 && sk.p != d.Precision {
		return fmt.Errorf("hyperloglog: cannot apply precision %d delta to precision %d sketch: %w", d.Precision, sk.p, ErrorPrecisionMismatch)
	}

	m := uint32(1) << d.Precision
	maxRho := maxRho(d.Precision)
	for k, idx := range d.Index {
		if idx >= m {
			return fmt.Errorf("hyperloglog: delta index %d = %d, register count %d: %w", k, idx, m, ErrorInvalidData)
		}
		if v := d.Value[k]; v > maxRho {
			return fmt.Errorf("hyperloglog: delta value %d = %d, max rho %d: %w", k, v, maxRho, ErrorInvalidData)
		}
	}

	if sk.p == 0 {
		*sk = *newSketchNoError(d.Precision, false)
		sk.generation = d.Generation
	}
	if d.Generation != sk.generation {
		return nil
	}
	if sk.sparse() {
		sk.toNormal()
	}
	for k, idx := range d.Index {
		if v := d.Value[k]; v > sk.regs[idx] {
			sk.regs[idx] = v
		}
	}
	return nil
}

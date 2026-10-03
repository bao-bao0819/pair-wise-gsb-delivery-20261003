package ber

import (
	"bytes"
	"fmt"
	"testing"
)

func assertBitStringValue(t *testing.T, p *Packet, wantBytes []byte, wantBits int) {
	t.Helper()

	value, ok := p.Value.(BitStringValue)
	if !ok {
		t.Fatalf("expected BitStringValue, got %T (%v)", p.Value, p.Value)
	}
	if value.BitLength != wantBits {
		t.Errorf("BitLength: want %d, got %d", wantBits, value.BitLength)
	}
	if !bytes.Equal(value.Bytes, wantBytes) {
		t.Errorf("Bytes: want %x, got %x", wantBytes, value.Bytes)
	}
	if wantBits == 0 && len(value.Bytes) != 0 {
		t.Errorf("empty bit string should have no bytes, got %x", value.Bytes)
	}
	if value.UnusedBits() != (8-wantBits%8)%8 {
		t.Errorf("UnusedBits: want %d, got %d", (8-wantBits%8)%8, value.UnusedBits())
	}
}

func TestDecodeBitStringPrimitive(t *testing.T) {
	// unused=4, data 6e 40 -> 12 significant bits
	p, err := DecodePacketErr([]byte{0x03, 0x03, 0x04, 0x6e, 0x40})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, []byte{0x6e, 0x40}, 12)
}

func TestDecodeBitStringDirtyTailBitsCleared(t *testing.T) {
	// BER does not require the unused bits to be zero; the structured value
	// clears them, while the raw content (and thus the re-encoding) is kept.
	p, err := DecodePacketErr([]byte{0x03, 0x02, 0x04, 0x0f})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, []byte{0x00}, 4)

	if got := p.Bytes(); !bytes.Equal(got, []byte{0x03, 0x02, 0x04, 0x0f}) {
		t.Errorf("re-encoding changed: got %x", got)
	}
}

func TestDecodeBitStringEmpty(t *testing.T) {
	// An empty bit string is a single zero unused-bits octet.
	p, err := DecodePacketErr([]byte{0x03, 0x01, 0x00})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, nil, 0)
}

func TestDecodeBitStringErrors(t *testing.T) {
	for name, data := range map[string][]byte{
		// Zero-length content is a missing unused-bits byte, not an empty
		// bit string.
		"missing unused-bits byte": {0x03, 0x00},
		// The unused-bits count must be in the range 0-7.
		"unused bits out of range": {0x03, 0x02, 0x08, 0x00},
		// Unused bits require at least one data byte.
		"unused bits without data": {0x03, 0x01, 0x05},
	} {
		if p, err := DecodePacketErr(data); err == nil {
			t.Errorf("%s: expected error, got packet %v", name, p)
		}
	}
}

func TestDecodeBitStringConstructedDefinite(t *testing.T) {
	// tests/tc37.ber: three definite-length segments.
	p, err := DecodePacketErr([]byte{
		0x23, 0x0c,
		0x03, 0x02, 0x00, 0x01,
		0x03, 0x02, 0x00, 0x01,
		0x03, 0x02, 0x04, 0x0f,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 8 + 8 + 4 bits; the dirty tail bits of the final segment are cleared.
	assertBitStringValue(t, p, []byte{0x01, 0x01, 0x00}, 20)
}

func TestDecodeBitStringConstructedIndefinite(t *testing.T) {
	// tests/tc38.ber: indefinite-length constructed form.
	p, err := DecodePacketErr([]byte{
		0x23, 0x80,
		0x03, 0x03, 0x00, 0x0a, 0x3b,
		0x03, 0x05, 0x04, 0x5f, 0x29, 0x1c, 0xd0,
		0x00, 0x00,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, []byte{0x0a, 0x3b, 0x5f, 0x29, 0x1c, 0xd0}, 44)
}

func TestDecodeBitStringConstructedNested(t *testing.T) {
	// Nested constructed segments are flattened in encoding order; both
	// definite and indefinite lengths may be mixed.
	p, err := DecodePacketErr([]byte{
		0x23, 0x80,
		0x23, 0x08,
		0x03, 0x02, 0x00, 0x01,
		0x03, 0x02, 0x00, 0x02,
		0x23, 0x80,
		0x03, 0x02, 0x00, 0x03,
		0x00, 0x00,
		0x03, 0x02, 0x04, 0x40,
		0x00, 0x00,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, []byte{0x01, 0x02, 0x03, 0x40}, 28)
}

func TestDecodeBitStringConstructedEmpty(t *testing.T) {
	// A constructed bit string with zero segments is the empty bit string.
	p, err := DecodePacketErr([]byte{0x23, 0x00})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, nil, 0)
}

func TestDecodeBitStringConstructedErrors(t *testing.T) {
	for name, data := range map[string][]byte{
		// Segments must be BIT STRINGs (tests/tc35.ber).
		"non-bit-string segment": {
			0x23, 0x80,
			0x04, 0x03, 0x00, 0x0a, 0x3b,
			0x00, 0x00,
		},
		// Intermediate segments must be byte-aligned (tests/tc36.ber).
		"non-final segment not byte-aligned": {
			0x23, 0x80,
			0x23, 0x80,
			0x03, 0x02, 0x00, 0x01,
			0x03, 0x02, 0x01, 0x02,
			0x00, 0x00,
			0x03, 0x02, 0x04, 0x0f,
			0x00, 0x00,
		},
		// The final segment's unused-bits count must be in range
		// (tests/tc48.ber).
		"final segment unused bits out of range": {
			0x23, 0x80,
			0x03, 0x02, 0x00, 0x01,
			0x03, 0x02, 0x0f, 0x0f,
			0x00, 0x00,
		},
		// A segment without its unused-bits byte is not an empty segment.
		"segment missing unused-bits byte": {
			0x23, 0x80,
			0x03, 0x00,
			0x00, 0x00,
		},
	} {
		if p, err := DecodePacketErr(data); err == nil {
			t.Errorf("%s: expected error, got packet %v", name, p)
		}
	}
}

// bitStringSegment encodes one primitive BIT STRING segment.
func bitStringSegment(data []byte, unused int) []byte {
	out := []byte{0x03, byte(1 + len(data)), byte(unused)}
	return append(out, data...)
}

// wrapConstructed wraps inner in a constructed BIT STRING, definite or
// indefinite length.
func wrapConstructed(inner []byte, indefinite bool) []byte {
	if indefinite {
		out := append([]byte{0x23, 0x80}, inner...)
		return append(out, 0x00, 0x00)
	}
	out := []byte{0x23, byte(len(inner))}
	return append(out, inner...)
}

// TestDecodeBitStringSplitPositions splits the same logical bit string at
// every byte boundary and checks that all segmentations (primitive,
// constructed, nested, definite and indefinite) decode to the same
// structured value.
func TestDecodeBitStringSplitPositions(t *testing.T) {
	data := []byte{0xaa, 0xbb, 0xc0}
	const bitLength = 20 // final byte carries 4 significant bits
	const unused = 4

	want := BitStringValue{Bytes: data, BitLength: bitLength}

	primitive := append([]byte{0x03, 0x04, byte(unused)}, data...)

	// All compositions of the 3 data bytes into segments; intermediate
	// segments are byte-aligned, the final segment carries the unused bits.
	compositions := [][]int{{3}, {1, 2}, {2, 1}, {1, 1, 1}}

	encodings := map[string][]byte{"primitive": primitive}
	for _, comp := range compositions {
		var inner []byte
		offset := 0
		for i, n := range comp {
			segUnused := 0
			if i == len(comp)-1 {
				segUnused = unused
			}
			inner = append(inner, bitStringSegment(data[offset:offset+n], segUnused)...)
			offset += n
		}
		encodings[fmt.Sprintf("definite %v", comp)] = wrapConstructed(inner, false)
		encodings[fmt.Sprintf("indefinite %v", comp)] = wrapConstructed(inner, true)
	}
	// A nested variant: [[aa] [bb]] [c0].
	nestedInner := append(bitStringSegment(data[:1], 0), bitStringSegment(data[1:2], 0)...)
	nestedInner = wrapConstructed(nestedInner, false)
	nestedInner = append(nestedInner, bitStringSegment(data[2:], unused)...)
	encodings["nested"] = wrapConstructed(nestedInner, true)

	for name, encoding := range encodings {
		p, err := DecodePacketErr(encoding)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		value, ok := p.Value.(BitStringValue)
		if !ok {
			t.Errorf("%s: expected BitStringValue, got %T", name, p.Value)
			continue
		}
		if value.BitLength != want.BitLength || !bytes.Equal(value.Bytes, want.Bytes) {
			t.Errorf("%s: want %+v, got %+v", name, want, value)
		}
	}
}

func TestNewBitString(t *testing.T) {
	p, err := NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, []byte{0x6e, 0x40}, 12, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, []byte{0x6e, 0x40}, 12)

	want := []byte{0x03, 0x03, 0x04, 0x6e, 0x40}
	if got := p.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("encoding: want %x, got %x", want, got)
	}

	// Round-trip: decoding the encoding yields the same logical bit string.
	decoded, err := DecodePacketErr(p.Bytes())
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	assertBitStringValue(t, decoded, []byte{0x6e, 0x40}, 12)
}

func TestNewBitStringEmpty(t *testing.T) {
	p, err := NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, nil, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertBitStringValue(t, p, nil, 0)

	want := []byte{0x03, 0x01, 0x00}
	if got := p.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("encoding: want %x, got %x", want, got)
	}
}

func TestNewBitStringErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		data      []byte
		bitLength int
	}{
		"negative bit length":   {[]byte{0x00}, -1},
		"bit length too large":  {[]byte{0x00, 0x00}, 17},
		"dirty tail bit":        {[]byte{0x6e, 0x41}, 12},
		"dirty byte beyond end": {[]byte{0x6e, 0x40, 0x01}, 12},
	} {
		if _, err := NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, tc.data, tc.bitLength, ""); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestNewBitStringPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for dirty tail bits")
		}
	}()
	NewBitString(ClassUniversal, TypePrimitive, TagBitString, []byte{0xff}, 4, "")
}

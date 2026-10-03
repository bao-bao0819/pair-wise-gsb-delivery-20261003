package ber

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParseBitString(t *testing.T) {
	valid := []struct {
		content   []byte
		bytes     []byte
		bitLength int
	}{
		{[]byte{0x00}, []byte{}, 0},
		{[]byte{0x00, 0xab}, []byte{0xab}, 8},
		{[]byte{0x04, 0x5f, 0x29, 0x1c, 0xd0}, []byte{0x5f, 0x29, 0x1c, 0xd0}, 28},
		{[]byte{0x07, 0x80}, []byte{0x80}, 1},
	}
	for _, tc := range valid {
		bv, err := ParseBitString(tc.content)
		if err != nil {
			t.Errorf("ParseBitString(% x): unexpected error: %v", tc.content, err)
			continue
		}
		if bv.BitLength != tc.bitLength || !bytes.Equal(bv.Bytes, tc.bytes) {
			t.Errorf("ParseBitString(% x) = {% x, %d}, want {% x, %d}",
				tc.content, bv.Bytes, bv.BitLength, tc.bytes, tc.bitLength)
		}
	}

	invalid := [][]byte{
		{},                             // missing unused-bits byte (not an empty bit string)
		{0x08, 0xab},                   // unused-bits count out of range
		{0x0f, 0x0f},                   // unused-bits count out of range
		{0x01},                         // no data bytes but unused-bits count non-zero
		{0x04, 0x5f, 0x29, 0x1c, 0xd1}, // dirty trailing bits
		{0x07, 0x81},                   // dirty trailing bit
	}
	for _, content := range invalid {
		if bv, err := ParseBitString(content); err == nil {
			t.Errorf("ParseBitString(% x) = %v, expected error", content, bv)
		}
	}
}

func TestDecodePrimitiveBitString(t *testing.T) {
	p, err := DecodePacketErr([]byte{0x03, 0x05, 0x04, 0x5f, 0x29, 0x1c, 0xd0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bv, ok := p.Value.(BitStringValue)
	if !ok {
		t.Fatalf("expected BitStringValue, got %T", p.Value)
	}
	if bv.BitLength != 28 || !bytes.Equal(bv.Bytes, []byte{0x5f, 0x29, 0x1c, 0xd0}) {
		t.Fatalf("unexpected value: %+v", bv)
	}
	if bv.UnusedBits() != 4 {
		t.Fatalf("expected 4 unused bits, got %d", bv.UnusedBits())
	}

	// An empty bit string is a single zero unused-bits byte...
	p, err = DecodePacketErr([]byte{0x03, 0x01, 0x00})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bv := p.Value.(BitStringValue); bv.BitLength != 0 || len(bv.Bytes) != 0 {
		t.Fatalf("expected empty bit string, got %+v", bv)
	}

	// ...which is not the same as a missing unused-bits byte.
	if _, err = DecodePacketErr([]byte{0x03, 0x00}); err == nil {
		t.Fatal("expected error for missing unused-bits byte")
	}
}

// primBitString encodes a primitive BIT STRING around raw content octets.
func primBitString(content ...byte) []byte {
	return append([]byte{0x03, byte(len(content))}, content...)
}

// consBitString wraps already-encoded children in a constructed BIT STRING,
// using indefinite length when requested.
func consBitString(indefinite bool, children ...[]byte) []byte {
	var out []byte
	if indefinite {
		out = []byte{0x23, 0x80}
	} else {
		var content []byte
		for _, c := range children {
			content = append(content, c...)
		}
		out = []byte{0x23, byte(len(content))}
	}
	for _, c := range children {
		out = append(out, c...)
	}
	if indefinite {
		out = append(out, 0x00, 0x00)
	}
	return out
}

func decodeBitStringValue(t *testing.T, data []byte) BitStringValue {
	t.Helper()
	p, err := DecodePacketErr(data)
	if err != nil {
		t.Fatalf("decode % x: unexpected error: %v", data, err)
	}
	bv, ok := p.Value.(BitStringValue)
	if !ok {
		t.Fatalf("decode % x: expected BitStringValue, got %T", data, p.Value)
	}
	return bv
}

// TestConstructedBitStringSplitInvariance verifies that the same logical bit
// string decodes to the same value regardless of how the segments are split,
// for definite and indefinite lengths and nested constructed segments.
func TestConstructedBitStringSplitInvariance(t *testing.T) {
	// 20 bits: 0xDE 0xAD 0xB0 with 4 unused bits in the final byte.
	want := BitStringValue{Bytes: []byte{0xde, 0xad, 0xb0}, BitLength: 20}

	encodings := map[string][]byte{
		"primitive":             {0x03, 0x04, 0x04, 0xde, 0xad, 0xb0},
		"split 1/1/1":           consBitString(false, primBitString(0x00, 0xde), primBitString(0x00, 0xad), primBitString(0x04, 0xb0)),
		"split 2/1":             consBitString(false, primBitString(0x00, 0xde, 0xad), primBitString(0x04, 0xb0)),
		"split 1/2":             consBitString(false, primBitString(0x00, 0xde), primBitString(0x04, 0xad, 0xb0)),
		"leading empty segment": consBitString(false, primBitString(0x00), primBitString(0x04, 0xde, 0xad, 0xb0)),
		"indefinite length":     consBitString(true, primBitString(0x00, 0xde), primBitString(0x00, 0xad), primBitString(0x04, 0xb0)),
		"nested constructed": consBitString(false,
			consBitString(false, primBitString(0x00, 0xde)),
			consBitString(true, primBitString(0x00, 0xad), primBitString(0x04, 0xb0))),
	}
	for name, data := range encodings {
		if bv := decodeBitStringValue(t, data); !reflect.DeepEqual(bv, want) {
			t.Errorf("%s: got %+v, want %+v", name, bv, want)
		}
	}
}

func TestDecodeConstructedBitStringErrors(t *testing.T) {
	cases := map[string][]byte{
		// Intermediate segment carries unused bits.
		"intermediate not byte-aligned": consBitString(false, primBitString(0x01, 0xde), primBitString(0x04, 0xb0)),
		// Final segment's unused-bits count exceeds 7.
		"final unused out of range": consBitString(false, primBitString(0x00, 0xde), primBitString(0x0f, 0x0f)),
		// Final segment has dirty trailing bits.
		"final dirty trailing bits": consBitString(false, primBitString(0x00, 0xde), primBitString(0x04, 0xb1)),
		// A segment without its unused-bits byte.
		"segment missing unused-bits byte": consBitString(false, primBitString(0x00, 0xde), primBitString()),
		// A non-BIT-STRING child.
		"non-bitstring child": consBitString(false, primBitString(0x00, 0xde), []byte{0x04, 0x01, 0x00}),
	}
	for name, data := range cases {
		if p, err := DecodePacketErr(data); err == nil {
			t.Errorf("%s: expected error, got packet %v", name, p)
		}
	}

	// An empty constructed bit string is a valid empty bit string.
	if bv := decodeBitStringValue(t, []byte{0x23, 0x00}); bv.BitLength != 0 || len(bv.Bytes) != 0 {
		t.Errorf("empty constructed bit string: got %+v", bv)
	}
}

func TestNewBitStringErr(t *testing.T) {
	p, err := NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, []byte{0xde, 0xad, 0xb0}, 20, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x03, 0x04, 0x04, 0xde, 0xad, 0xb0}
	if got := p.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("encoding = % x, want % x", got, want)
	}
	if bv := decodeBitStringValue(t, p.Bytes()); !reflect.DeepEqual(bv, BitStringValue{Bytes: []byte{0xde, 0xad, 0xb0}, BitLength: 20}) {
		t.Fatalf("round-trip mismatch: %+v", bv)
	}

	// Empty bit string.
	p, err = NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, nil, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.Bytes(); !bytes.Equal(got, []byte{0x03, 0x01, 0x00}) {
		t.Fatalf("empty encoding = % x, want 03 01 00", got)
	}

	invalid := []struct {
		data      []byte
		bitLength int
	}{
		{[]byte{0xde}, -1},             // negative bit length
		{[]byte{0xde}, 9},              // bit length beyond data
		{[]byte{0xde, 0xad}, 8},        // more data bytes than the bit length needs
		{nil, 1},                       // bit length without data
		{[]byte{0xde, 0xad, 0xb1}, 20}, // dirty trailing bits
	}
	for _, tc := range invalid {
		if _, err := NewBitStringErr(ClassUniversal, TypePrimitive, TagBitString, tc.data, tc.bitLength, ""); err == nil {
			t.Errorf("NewBitStringErr(% x, %d): expected error", tc.data, tc.bitLength)
		}
	}
}

func TestNewBitStringPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for dirty trailing bits")
		}
	}()
	NewBitString(ClassUniversal, TypePrimitive, TagBitString, []byte{0xb1}, 4, "")
}

package ber

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDERValid(t *testing.T) {
	// SEQUENCE { INTEGER 5, BOOLEAN TRUE, NULL, OID 1.2.840, UTF8String "hi" }
	valid := []byte{
		0x30, 0x11,
		0x02, 0x01, 0x05,
		0x01, 0x01, 0xFF,
		0x05, 0x00,
		0x06, 0x03, 0x2A, 0x86, 0x48,
		0x0C, 0x02, 'h', 'i',
	}
	if err := ValidateDER(valid); err != nil {
		t.Fatalf("ValidateDER rejected valid DER: %v", err)
	}

	packet, err := DecodePacketDER(valid)
	if err != nil {
		t.Fatalf("DecodePacketDER rejected valid DER: %v", err)
	}
	if len(packet.Children) != 5 {
		t.Fatalf("expected 5 children, got %d", len(packet.Children))
	}
	if v, ok := packet.Children[0].Value.(int64); !ok || v != 5 {
		t.Errorf("INTEGER child: got %v", packet.Children[0].Value)
	}
	if v, ok := packet.Children[1].Value.(bool); !ok || !v {
		t.Errorf("BOOLEAN child: got %v", packet.Children[1].Value)
	}
	if v, ok := packet.Children[3].Value.(string); !ok || v != "1.2.840" {
		t.Errorf("OID child: got %v", packet.Children[3].Value)
	}
	if v, ok := packet.Children[4].Value.(string); !ok || v != "hi" {
		t.Errorf("UTF8String child: got %v", packet.Children[4].Value)
	}

	// Long-form length is legal DER when the length is >= 128.
	longForm := []byte{0x04, 0x81, 0x80}
	for i := 0; i < 128; i++ {
		longForm = append(longForm, byte(i))
	}
	if err := ValidateDER(longForm); err != nil {
		t.Errorf("ValidateDER rejected valid long-form length: %v", err)
	}

	// High-tag form is legal DER for tags >= 31, but a context-specific
	// class is outside the supported strict set.
	highTag := []byte{0x9F, 0x1F, 0x00}
	if err := ValidateDER(highTag); !errors.Is(err, ErrUnsupportedStrictType) {
		t.Errorf("context-specific high tag: expected unsupported strict type, got %v", err)
	}
}

func TestValidateDERViolations(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		wantOffset int
		wantPath   string
		wantReason string
	}{
		{
			name:       "non-minimal length",
			data:       []byte{0x04, 0x81, 0x01, 'a'},
			wantOffset: 1,
			wantPath:   "Octet String",
			wantReason: "short-form",
		},
		{
			name:       "long-form leading zero",
			data:       []byte{0x04, 0x82, 0x00, 0x80},
			wantOffset: 2,
			wantPath:   "Octet String",
			wantReason: "leading zero",
		},
		{
			name:       "indefinite length",
			data:       []byte{0x30, 0x80, 0x00, 0x00},
			wantOffset: 1,
			wantPath:   "SEQUENCE",
			wantReason: "indefinite length",
		},
		{
			name:       "integer redundant leading zero",
			data:       []byte{0x02, 0x02, 0x00, 0x7F},
			wantOffset: 2,
			wantPath:   "INTEGER",
			wantReason: "redundant leading 0x00",
		},
		{
			name:       "integer redundant leading 0xff",
			data:       []byte{0x02, 0x02, 0xFF, 0x80},
			wantOffset: 2,
			wantPath:   "INTEGER",
			wantReason: "redundant leading 0xFF",
		},
		{
			name:       "integer empty",
			data:       []byte{0x02, 0x00},
			wantOffset: 1,
			wantPath:   "INTEGER",
			wantReason: "at least one content octet",
		},
		{
			name:       "boolean non-canonical",
			data:       []byte{0x01, 0x01, 0x01},
			wantOffset: 2,
			wantPath:   "BOOLEAN",
			wantReason: "non-canonical BOOLEAN",
		},
		{
			name:       "boolean wrong length",
			data:       []byte{0x01, 0x02, 0x00, 0xFF},
			wantOffset: 1,
			wantPath:   "BOOLEAN",
			wantReason: "length 1",
		},
		{
			name:       "null with content",
			data:       []byte{0x05, 0x01, 0x00},
			wantOffset: 1,
			wantPath:   "NULL",
			wantReason: "length 0",
		},
		{
			name:       "oid non-minimal arc",
			data:       []byte{0x06, 0x02, 0x80, 0x01},
			wantOffset: 2,
			wantPath:   "OBJECT IDENTIFIER",
			wantReason: "not minimally encoded",
		},
		{
			name:       "oid truncated arc",
			data:       []byte{0x06, 0x02, 0x2A, 0x81},
			wantOffset: 3,
			wantPath:   "OBJECT IDENTIFIER",
			wantReason: "truncated base-128 arc",
		},
		{
			name:       "high-tag form for tag below 31",
			data:       []byte{0x1F, 0x02, 0x01, 0x05},
			wantOffset: 0,
			wantPath:   "",
			wantReason: "not minimally encoded",
		},
		{
			name:       "constructed string",
			data:       []byte{0x24, 0x03, 0x04, 0x01, 'a'},
			wantOffset: 0,
			wantPath:   "Octet String",
			wantReason: "primitive encoding",
		},
		{
			name:       "primitive sequence",
			data:       []byte{0x10, 0x00},
			wantOffset: 0,
			wantPath:   "SEQUENCE",
			wantReason: "constructed encoding",
		},
		{
			name:       "nested violation path",
			data:       []byte{0x30, 0x07, 0x30, 0x05, 0x02, 0x03, 0x00, 0x00, 0x7F},
			wantOffset: 6,
			wantPath:   "SEQUENCE[0].SEQUENCE[0].INTEGER",
			wantReason: "redundant leading 0x00",
		},
		{
			name:       "trailing data",
			data:       []byte{0x05, 0x00, 0x00},
			wantOffset: 2,
			wantPath:   "",
			wantReason: "trailing data",
		},
		{
			name:       "truncated content",
			data:       []byte{0x04, 0x02, 'a'},
			wantOffset: 1,
			wantPath:   "Octet String",
			wantReason: "extends past end of data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDER(tc.data)
			if err == nil {
				t.Fatalf("expected error, got none")
			}
			var derErr *DERError
			if !errors.As(err, &derErr) {
				t.Fatalf("expected *DERError, got %T: %v", err, err)
			}
			if derErr.Offset != tc.wantOffset {
				t.Errorf("offset: want %d, got %d", tc.wantOffset, derErr.Offset)
			}
			if derErr.Path != tc.wantPath {
				t.Errorf("path: want %q, got %q", tc.wantPath, derErr.Path)
			}
			if !strings.Contains(derErr.Reason, tc.wantReason) {
				t.Errorf("reason: want substring %q, got %q", tc.wantReason, derErr.Reason)
			}
			if errors.Is(err, ErrUnsupportedStrictType) {
				t.Errorf("unexpected unsupported-strict-type error: %v", err)
			}
			// DecodePacketDER must surface the same validation error.
			if _, decErr := DecodePacketDER(tc.data); decErr == nil {
				t.Errorf("DecodePacketDER: expected error, got none")
			}
		})
	}
}

func TestValidateDERUnsupportedTypes(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"set", []byte{0x31, 0x00}},
		{"set of", []byte{0x31, 0x03, 0x02, 0x01, 0x01}},
		{"nested set", []byte{0x30, 0x04, 0x31, 0x02, 0x05, 0x00}},
		{"enumerated", []byte{0x0A, 0x01, 0x01}},
		{"real", []byte{0x09, 0x00}},
		{"context-specific", []byte{0x80, 0x01, 0x00}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDER(tc.data)
			if err == nil {
				t.Fatalf("expected error, got none")
			}
			if !errors.Is(err, ErrUnsupportedStrictType) {
				t.Fatalf("expected ErrUnsupportedStrictType, got %v", err)
			}
			if !strings.Contains(err.Error(), "unsupported strict type") {
				t.Errorf("error should mention unsupported strict type: %v", err)
			}
		})
	}
}

// TestDERModeLeavesBERAlone ensures the strict checks did not leak into the
// regular BER decoder: non-DER-but-valid-BER inputs must still decode.
func TestDERModeLeavesBERAlone(t *testing.T) {
	berOnly := [][]byte{
		{0x04, 0x81, 0x01, 'a'},              // non-minimal length
		{0x02, 0x02, 0x00, 0x7F},             // non-minimal integer
		{0x01, 0x01, 0x01},                   // non-canonical boolean
		{0x30, 0x80, 0x05, 0x00, 0x00, 0x00}, // indefinite-length sequence
		{0x24, 0x03, 0x04, 0x01, 'a'},        // constructed octet string
		{0x31, 0x03, 0x02, 0x01, 0x01},       // SET
	}
	for _, data := range berOnly {
		if _, err := DecodePacketErr(data); err != nil {
			t.Errorf("BER decode of %#v failed: %v", data, err)
		}
		if err := ValidateDER(data); err == nil {
			t.Errorf("ValidateDER unexpectedly accepted %#v", data)
		}
	}
}

// TestDecodePacketDERMatchesBER ensures valid DER decodes to the same Packet
// values as the regular decoder.
func TestDecodePacketDERMatchesBER(t *testing.T) {
	inner := NewSequence("inner")
	inner.AppendChild(NewInteger(ClassUniversal, TypePrimitive, TagInteger, int64(-129), "neg"))
	inner.AppendChild(NewLDAPBoolean(ClassUniversal, TypePrimitive, TagBoolean, true, "bool"))
	inner.AppendChild(Encode(ClassUniversal, TypePrimitive, TagNULL, nil, "null"))
	oid, err := NewOIDErr(ClassUniversal, TypePrimitive, TagObjectIdentifier, "1.2.840.113549", "oid")
	if err != nil {
		t.Fatal(err)
	}
	inner.AppendChild(oid)
	inner.AppendChild(NewString(ClassUniversal, TypePrimitive, TagUTF8String, "hello", "str"))

	data := inner.Bytes()
	if err := ValidateDER(data); err != nil {
		t.Fatalf("constructor output is not valid DER: %v", err)
	}

	derPacket, err := DecodePacketDER(data)
	if err != nil {
		t.Fatalf("DecodePacketDER: %v", err)
	}
	berPacket, err := DecodePacketErr(data)
	if err != nil {
		t.Fatalf("DecodePacketErr: %v", err)
	}
	if len(derPacket.Children) != len(berPacket.Children) {
		t.Fatalf("child count mismatch: %d vs %d", len(derPacket.Children), len(berPacket.Children))
	}
	for i := range derPacket.Children {
		got, want := derPacket.Children[i].Value, berPacket.Children[i].Value
		if got != want {
			t.Errorf("child %d: DER value %v != BER value %v", i, got, want)
		}
	}
}

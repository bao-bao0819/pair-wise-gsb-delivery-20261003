package ber_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
)

func expectDERError(t *testing.T, data []byte, offset int, path, contains string) {
	t.Helper()
	err := ber.CheckDER(data)
	if err == nil {
		t.Fatalf("CheckDER(% x): expected error, got nil", data)
	}
	var derr *ber.DERError
	if !errors.As(err, &derr) {
		t.Fatalf("CheckDER(% x): expected *ber.DERError, got %T (%v)", data, err, err)
	}
	if derr.Offset != offset {
		t.Errorf("CheckDER(% x): offset = %d, want %d (err: %v)", data, derr.Offset, offset, err)
	}
	if derr.Path != path {
		t.Errorf("CheckDER(% x): path = %q, want %q (err: %v)", data, derr.Path, path, err)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Errorf("CheckDER(% x): error %q does not contain %q", data, err.Error(), contains)
	}
}

func assertDEROK(t *testing.T, data []byte) {
	t.Helper()
	if err := ber.CheckDER(data); err != nil {
		t.Fatalf("CheckDER(% x): unexpected error: %v", data, err)
	}
}

// assertSamePacket compares the packets produced by the DER and BER decoders.
func assertSamePacket(t *testing.T, data []byte) {
	t.Helper()
	derPacket, err := ber.DecodePacketDER(data)
	if err != nil {
		t.Fatalf("DecodePacketDER(% x): %v", data, err)
	}
	berPacket, err := ber.DecodePacketErr(data)
	if err != nil {
		t.Fatalf("DecodePacketErr(% x): %v", data, err)
	}
	if !packetsEqual(derPacket, berPacket) {
		t.Errorf("DecodePacketDER(% x): packet differs from DecodePacketErr", data)
	}
}

func packetsEqual(a, b *ber.Packet) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Identifier != b.Identifier || a.Value != b.Value || len(a.Children) != len(b.Children) {
		return false
	}
	if !bytes.Equal(a.ByteValue, b.ByteValue) {
		return false
	}
	for i := range a.Children {
		if !packetsEqual(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

func TestDERValidSamples(t *testing.T) {
	seq := ber.NewSequence("root")
	seq.AppendChild(ber.NewLDAPBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, true, "bool-true"))
	seq.AppendChild(ber.NewBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, false, "bool-false"))
	seq.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, int64(128), "int-128"))
	seq.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, int64(-1), "int-neg1"))
	seq.AppendChild(ber.Encode(ber.ClassUniversal, ber.TypePrimitive, ber.TagNULL, nil, "null"))
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagObjectIdentifier, "1.2.840.113549", "oid"))
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "hello", "octet"))
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagUTF8String, "héllo", "utf8"))
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagPrintableString, "AB 12", "printable"))
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagIA5String, "user@example.com", "ia5"))

	data := seq.Bytes()
	assertDEROK(t, data)
	assertSamePacket(t, data)
}

func TestDERValidBoundaryValues(t *testing.T) {
	for _, data := range [][]byte{
		{0x01, 0x01, 0x00},             // FALSE
		{0x01, 0x01, 0xFF},             // TRUE (canonical, RFC 4511 too)
		{0x02, 0x01, 0x00},             // INTEGER 0
		{0x02, 0x01, 0x7F},             // INTEGER 127
		{0x02, 0x01, 0xFF},             // INTEGER -1, single octet
		{0x02, 0x02, 0x00, 0x80},       // INTEGER 128
		{0x02, 0x02, 0xFF, 0x7F},       // INTEGER -129
		{0x05, 0x00},                   // NULL
		{0x06, 0x03, 0x2A, 0x86, 0x48}, // OID 1.2.840
		{0x30, 0x00},                   // empty SEQUENCE
		{0x30, 0x03, 0x02, 0x01, 0x01}, // SEQUENCE { INTEGER 1 }
		// Context-specific primitive and constructed wrappers.
		{0x80, 0x01, 0x42},
		{0xA0, 0x03, 0x02, 0x01, 0x01},
		// Long-form length used only where required.
		append([]byte{0x04, 0x81, 0x80}, make([]byte, 128)...),
	} {
		assertDEROK(t, data)
	}
}

func TestDERBooleanViolations(t *testing.T) {
	// Non-canonical TRUE: single content octet other than 0x00/0xFF.
	expectDERError(t, []byte{0x01, 0x01, 0x01}, 2, "", "BOOLEAN content must be")
	// Over-long BOOLEAN.
	expectDERError(t, []byte{0x01, 0x03, 0x00, 0x00, 0x00}, 2, "", "exactly one content octet")
	// Empty BOOLEAN.
	expectDERError(t, []byte{0x01, 0x00}, 2, "", "exactly one content octet")
	// Constructed BOOLEAN is a form violation at the identifier octet.
	expectDERError(t, []byte{0x21, 0x01, 0xFF}, 0, "", "must be primitive")

	// BER mode must still accept these non-DER encodings.
	for _, data := range [][]byte{
		{0x01, 0x01, 0x01},
		{0x01, 0x03, 0x00, 0x00, 0x00},
	} {
		if p, err := ber.DecodePacketErr(data); err != nil || p == nil {
			t.Errorf("BER decode of % x should still succeed, got packet=%v err=%v", data, p, err)
		}
	}
}

func TestDERIntegerSignExtension(t *testing.T) {
	// Positive integer with redundant leading 0x00.
	expectDERError(t, []byte{0x02, 0x02, 0x00, 0x01}, 2, "", "redundant leading 0x00")
	// Zero encoded with a leading 0x00.
	expectDERError(t, []byte{0x02, 0x02, 0x00, 0x00}, 2, "", "redundant leading 0x00")
	// Negative integer with redundant leading 0xFF.
	expectDERError(t, []byte{0x02, 0x02, 0xFF, 0xFF}, 2, "", "redundant leading 0xFF")
	expectDERError(t, []byte{0x02, 0x03, 0xFF, 0xFF, 0x7E}, 2, "", "redundant leading 0xFF")
	// Empty INTEGER.
	expectDERError(t, []byte{0x02, 0x00}, 2, "", "INTEGER content must not be empty")
}

func TestDERNullViolations(t *testing.T) {
	expectDERError(t, []byte{0x05, 0x01, 0x00}, 2, "", "NULL content must be empty")
	expectDERError(t, []byte{0x25, 0x00}, 0, "", "must be primitive")
}

func TestDERLengthViolations(t *testing.T) {
	// Indefinite length on a constructed SEQUENCE.
	expectDERError(t, []byte{0x30, 0x80, 0x00, 0x00}, 1, "", "indefinite length")
	// Indefinite length on a primitive octet string.
	expectDERError(t, []byte{0x04, 0x80, 0x00, 0x00}, 1, "", "indefinite length")
	// Non-minimal long-form length: 0 in one length octet.
	expectDERError(t, []byte{0x02, 0x81, 0x00}, 1, "", "non-minimal long-form")
	// Non-minimal long-form length: 1 should be short form.
	expectDERError(t, []byte{0x04, 0x81, 0x01, 0x41}, 1, "", "non-minimal long-form")
	// 0x80 itself would fit in the short form? It does not (high bit set),
	// but the leading length octet's high bit must then be set; 0x81 0x80 is
	// minimal and accepted.
	// (It declares 128 content octets, which must actually be present.)
	assertDEROK(t, append([]byte{0x04, 0x81, 0x80}, make([]byte, 128)...))
	// Reserved length byte 0xFF.
	expectDERError(t, []byte{0x04, 0xFF}, 1, "", "invalid length byte 0xff")
}

func TestDERIdentifierViolations(t *testing.T) {
	// Non-minimal high-tag-number form for tag 0.
	expectDERError(t, []byte{0x1F, 0x00}, 1, "", "non-minimal high-tag-number")
	// Tag 30 must use short form (0x1E), not high form 0x1F 0x1E.
	expectDERError(t, []byte{0x1F, 0x1E, 0x00}, 0, "", "non-minimal tag encoding")
	// Truncated high-tag-number form.
	expectDERError(t, []byte{0x1F, 0x81}, 2, "", "truncated high-tag-number")
	// A genuine high tag (31 = 0x1F) is accepted when primitive, universal.
	assertDEROK(t, []byte{0x1F, 0x1F, 0x00})
}

func TestDERSetIsUnsupported(t *testing.T) {
	for _, data := range [][]byte{
		{0x31, 0x00},                   // SET
		{0x31, 0x03, 0x02, 0x01, 0x01}, // SET { INTEGER 1 }
	} {
		err := ber.CheckDER(data)
		var derr *ber.DERError
		if !errors.As(err, &derr) {
			t.Fatalf("CheckDER(% x): expected *DERError, got %v", data, err)
		}
		if !errors.Is(err, ber.ErrUnsupportedStrictType) {
			t.Fatalf("CheckDER(% x): expected ErrUnsupportedStrictType, got %v", data, err)
		}
		if derr.Offset != 0 || derr.Path != "" {
			t.Fatalf("CheckDER(% x): offset=%d path=%q, want 0/\"\"", data, derr.Offset, derr.Path)
		}
	}

	// Ordinary BER decoding of a SET is unaffected.
	data := []byte{0x31, 0x03, 0x02, 0x01, 0x01}
	if p, err := ber.DecodePacketErr(data); err != nil || p == nil {
		t.Fatalf("BER decode of SET should still succeed, got %v", err)
	}
}

func TestDERSequenceMustBeConstructed(t *testing.T) {
	expectDERError(t, []byte{0x10, 0x00}, 0, "", "must be constructed")
}

func TestDERStringsMustBePrimitive(t *testing.T) {
	// Constructed OCTET STRING, empty.
	expectDERError(t, []byte{0x24, 0x00}, 0, "", "must be primitive")
	// Constructed OCTET STRING wrapping a primitive OCTET STRING.
	expectDERError(t, []byte{0x24, 0x05, 0x04, 0x03, 0x61, 0x62, 0x63}, 0, "", "must be primitive")
	// Constructed UTF8String.
	expectDERError(t, []byte{0x2C, 0x00}, 0, "", "must be primitive")

	// Invalid contents are located at the content start.
	expectDERError(t, []byte{0x0C, 0x01, 0xFF}, 2, "", "invalid UTF-8")
	expectDERError(t, []byte{0x16, 0x01, 0x80}, 2, "", "invalid character for IA5String")
	expectDERError(t, []byte{0x13, 0x01, 0x40}, 2, "", "invalid character for PrintableString")
}

func TestDEROIDViolations(t *testing.T) {
	// Empty OID.
	expectDERError(t, []byte{0x06, 0x00}, 2, "", "Object Identifier content must not be empty")
	// Non-minimal arc (leading 0x80 continuation) from the conformance suite.
	expectDERError(t, []byte{0x06, 0x06, 0x80, 0x80, 0x51, 0x80, 0x80, 0x01}, 2, "", "not minimally encoded")
	// Truncated final arc.
	expectDERError(t, []byte{0x06, 0x02, 0x2A, 0x86}, 3, "", "truncated base-128 OID arc")
}

func TestDERNestedPathAndOffset(t *testing.T) {
	// SEQUENCE { INTEGER 1 ; BOOLEAN 0x42 }
	//   30 06
	//   [0] 02 01 01
	//   [1] 01 01 42          <- non-canonical BOOLEAN at offset 6
	data := []byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x01, 0x01, 0x42}
	expectDERError(t, data, 7, "$[1]", "BOOLEAN content must be")

	// SEQUENCE { SEQUENCE { INTEGER 00 00 (non-minimal zero) } }
	//   30 06 30 04 02 02 00 00 ; bad content octet at offset 6
	nested := []byte{0x30, 0x06, 0x30, 0x04, 0x02, 0x02, 0x00, 0x00}
	expectDERError(t, nested, 6, "$[0][0]", "redundant leading 0x00")

	// Indefinite length inside an outer definite-length SEQUENCE: the outer
	// header is fine, the inner length octet at offset 4 is the violation.
	indef := []byte{
		0x30, 0x04, // outer SEQUENCE len 4
		0x30, 0x80, // inner SEQUENCE indefinite at offset 2/3
		0x00, 0x00, // EOC
	}
	expectDERError(t, indef, 3, "$[0]", "indefinite length")

	// SET nested inside an otherwise valid SEQUENCE: error path identifies it.
	setNested := []byte{0x30, 0x05, 0x02, 0x01, 0x01, 0x31, 0x00}
	err := ber.CheckDER(setNested)
	var derr *ber.DERError
	if !errors.As(err, &derr) {
		t.Fatalf("expected *DERError, got %v", err)
	}
	if derr.Offset != 5 || derr.Path != "$[1]" {
		t.Fatalf("offset=%d path=%q, want 5/$[1]", derr.Offset, derr.Path)
	}
	if !errors.Is(err, ber.ErrUnsupportedStrictType) {
		t.Fatalf("expected ErrUnsupportedStrictType, got %v", err)
	}
}

func TestDERTruncation(t *testing.T) {
	// Declared length exceeds available bytes: violation at first missing byte.
	expectDERError(t, []byte{0x04, 0x03, 0x41}, 3, "", "truncated content")
	// Missing length octet.
	expectDERError(t, []byte{0x04}, 1, "", "truncated length")
	// Empty input.
	expectDERError(t, []byte{}, 0, "", "truncated identifier")
	// Constructed element claims a child that runs past the content end.
	expectDERError(t, []byte{0x30, 0x02, 0x02, 0x01}, 4, "$[0]", "truncated content")
}

func TestDERTrailingBytesIgnored(t *testing.T) {
	// A valid element followed by extra bytes is fine, matching DecodePacketErr.
	data := []byte{0x05, 0x00, 0x00, 0x00}
	assertDEROK(t, data)
}

func TestDERTopLevelEOCRejected(t *testing.T) {
	expectDERError(t, []byte{0x00, 0x00}, 0, "", "End-of-Contents")
}

func TestDERReadPacket(t *testing.T) {
	// Valid input through a reader yields the same packet as BER.
	data := []byte{0x30, 0x03, 0x02, 0x01, 0x01}
	packet, err := ber.ReadPacketDER(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadPacketDER: %v", err)
	}
	if packet.Tag != ber.TagSequence || len(packet.Children) != 1 {
		t.Fatalf("unexpected packet: %+v", packet)
	}

	// Non-DER input through a reader reports offset/path too.
	bad := []byte{0x30, 0x03, 0x01, 0x01, 0x01}
	_, err = ber.ReadPacketDER(bytes.NewReader(bad))
	var derr *ber.DERError
	if !errors.As(err, &derr) {
		t.Fatalf("ReadPacketDER: expected *DERError, got %v", err)
	}
	if derr.Offset != 4 || derr.Path != "$[0]" {
		t.Fatalf("offset=%d path=%q, want 4/$[0]", derr.Offset, derr.Path)
	}
}

func TestDERDecodeReturnsOrdinaryPacket(t *testing.T) {
	// NewBoolean(false) encodes 0x00, the DER canonical FALSE.
	data := ber.NewBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, false, "").Bytes()
	assertDEROK(t, data)
	assertSamePacket(t, data)

	// LDAP boolean TRUE is 0xFF, which is also the DER canonical TRUE; FALSE
	// is 0x00. Both must round-trip to the same Packet as the BER decoder.
	data = ber.NewLDAPBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, true, "").Bytes()
	assertDEROK(t, data)
	assertSamePacket(t, data)
	data = ber.NewLDAPBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, false, "").Bytes()
	assertDEROK(t, data)
	assertSamePacket(t, data)

	// NewBoolean(true) emits 0x01 (BER but not DER), which DER must reject.
	nonDER := ber.NewBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, true, "").Bytes()
	if err := ber.CheckDER(nonDER); err == nil {
		t.Fatalf("0x01 BOOLEAN must not be accepted as DER")
	}
}

func TestDEROutOfScopeTypesGetGenericChecks(t *testing.T) {
	// A 256 value encoded with the minimum two length octets is canonical.
	assertDEROK(t, append([]byte{0x04, 0x82, 0x01, 0x00}, make([]byte, 256)...))

	// A 9-byte INTEGER is a BER decoder limitation, but the bytes themselves
	// are valid DER; the type-scoped checker must not flag the size.
	bigInt := []byte{0x02, 0x09, 0x00, 0x80, 0, 0, 0, 0, 0, 0, 0}
	assertDEROK(t, bigInt)

	// The leading 0x00 is redundant sign padding even for a large integer.
	badBigInt := append([]byte{0x02, 0x09, 0x00, 0x00}, make([]byte, 7)...)
	expectDERError(t, badBigInt, 2, "", "redundant leading 0x00")

	// BIT STRING and REAL are outside the supported DER scope: their contents
	// are not canonical-checked, but header DER rules still apply.
	assertDEROK(t, []byte{0x03, 0x01, 0x00}) // BIT STRING, zero unused bits
	assertDEROK(t, []byte{0x09, 0x00})       // REAL +0 as zero-length value
	// Indefinite length is rejected regardless of type.
	expectDERError(t, []byte{0x23, 0x80, 0x00, 0x00}, 1, "", "indefinite length")

	// RELATIVE-OID (tag 0x0D) shares the minimal arc encoding of OID.
	assertDEROK(t, []byte{0x0D, 0x02, 0x88, 0x37}) // arcs 135, 55
	expectDERError(t, []byte{0x0D, 0x02, 0x80, 0x01}, 2, "", "not minimally encoded")
}

func TestDERNestingLimit(t *testing.T) {
	// Build a chain of empty SEQUENCE wrappers one deeper than the limit.
	limit := ber.MaxNestingDepth
	var data []byte
	encodeLen := func(n int) []byte {
		if n <= 127 {
			return []byte{byte(n)}
		}
		var b []byte
		for v := n; v > 0; v >>= 8 {
			b = append([]byte{byte(v)}, b...)
		}
		return append([]byte{0x80 | byte(len(b))}, b...)
	}
	for i := 0; i < limit+1; i++ {
		// Each wrapper contains exactly one child (next wrapper or the NULL).
		data = append([]byte{0x30}, append(encodeLen(len(data)), data...)...)
	}
	data = append(data, 0x05, 0x00)

	err := ber.CheckDER(data)
	var derr *ber.DERError
	if !errors.As(err, &derr) {
		t.Fatalf("expected *DERError for deep nesting, got %v", err)
	}
	if !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected nesting depth error, got %v", err)
	}
}

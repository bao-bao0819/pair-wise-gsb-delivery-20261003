package ber

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// ErrUnsupportedStrictType wraps the [DERError] reported by a DER validation
// function when the input uses a type whose canonical encoding cannot be
// checked without ASN.1 type information the library does not have. In
// particular, DER requires SET/SET OF members to be canonically ordered, which
// is type-dependent; encountering a SET therefore yields this error rather
// than a possibly-wrong "valid DER" verdict.
var ErrUnsupportedStrictType = errors.New("unsupported strict type")

// DERError describes the first DER conformance violation found while walking
// the original, undecoded bytes.
//
// Offset is the index within the input of the offending octet (the start of
// the identifier or length for header violations, or the start of the content
// for content violations). Path locates the element within the tree: it is
// empty for the outermost element and otherwise lists the 0-based child index
// of each ancestor, e.g. "$[1][0]" for the first child of the second child of
// the outermost element.
//
// Use [errors.Is] with [ErrUnsupportedStrictType] to detect an unsupported
// type rather than a malformed encoding.
type DERError struct {
	Offset int
	Path   string
	Msg    string
	Err    error
}

func (e *DERError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("der: offset %d: %s", e.Offset, e.Msg)
	}
	return fmt.Sprintf("der: offset %d at %s: %s", e.Offset, e.Path, e.Msg)
}

func (e *DERError) Unwrap() error {
	return e.Err
}

// CheckDER walks data in its original encoded form and verifies conformance
// with DER (the canonical subset of BER, ITU-T X.690 section 10). It reports
// the first violation encountered as a [*DERError], giving both the byte
// offset and the tree path; a nil result means the bytes are DER for the types
// checked.
//
// Coverage is deliberately limited to the types the library supports:
// BOOLEAN, INTEGER, NULL, OBJECT IDENTIFIER (and RELATIVE-OID), the restricted
// character strings plus OCTET STRING, and SEQUENCE. For these it enforces,
// directly on the raw bytes:
//
//   - minimal identifier encoding (tags 0..30 in short form);
//   - definite, minimal length encoding (indefinite length is rejected);
//   - INTEGER contents that are minimally encoded, with no redundant leading
//     0x00 sign octet or leading 0xFF octet;
//   - BOOLEAN contents of exactly one canonical octet, 0x00 or 0xFF;
//   - NULL with empty content;
//   - OID arcs that are complete and minimally base-128 encoded;
//   - strings in primitive form with valid contents where the library checks
//     them (UTF8String, PrintableString, IA5String);
//   - SEQUENCE in constructed form, with children checked recursively.
//
// SET and SET OF return a [*DERError] wrapping [ErrUnsupportedStrictType]:
// their canonical element ordering requires type information this checker does
// not have, so they are never silently accepted. Other tags (context-specific
// IMPLICIT wrappers and the remaining universal types such as BIT STRING or
// REAL) are checked only for minimal definite-length encoding, not for
// type-specific canonicality. This check is opt-in; ordinary BER decoding is
// not made stricter. Trailing bytes after the outermost element are ignored,
// matching [DecodePacketErr].
func CheckDER(data []byte) error {
	if _, _, err := derValidate(data, "", 0); err != nil {
		return err
	}
	return nil
}

// DecodePacketDER decodes a single DER-encoded packet. It first verifies the
// raw bytes with [CheckDER], reporting the first DER violation's offset and
// path, and only then decodes with the ordinary BER decoder, so a successfully
// returned [Packet] has exactly the values [DecodePacketErr] would produce.
func DecodePacketDER(data []byte) (*Packet, error) {
	if err := CheckDER(data); err != nil {
		return nil, err
	}
	return DecodePacketErr(data)
}

// ReadPacketDER reads and validates a single DER-encoded packet from reader,
// mirroring [ReadPacket]. The bytes actually consumed are captured during the
// BER read and then checked with [CheckDER]; the returned error therefore
// carries the offset and path within the packet.
func ReadPacketDER(reader io.Reader) (*Packet, error) {
	var captured bytes.Buffer
	packet, err := ReadPacket(io.TeeReader(reader, &captured))
	if err != nil {
		return nil, err
	}
	if err := CheckDER(captured.Bytes()); err != nil {
		return nil, err
	}
	return packet, nil
}

// derValidate checks one DER TLV starting at data[0]. path is the ancestor
// prefix (empty for the outermost element). depth tracks nesting against
// MaxNestingDepth. It returns the number of bytes the element occupies, the
// absolute offset where its content begins, and the first violation.
func derValidate(data []byte, path string, depth int) (consumed, contentOffset int, derr *DERError) {
	fail := func(offset int, msg string) (int, int, *DERError) {
		return 0, 0, &DERError{Offset: offset, Path: path, Msg: msg}
	}

	if MaxNestingDepth > 0 && depth >= MaxNestingDepth {
		return fail(0, fmt.Sprintf("nesting depth %d exceeds maximum %d", depth, MaxNestingDepth))
	}

	if len(data) == 0 {
		return fail(0, "truncated identifier: no identifier octet")
	}

	identifier, idLen, err := derParseIdentifier(data)
	if err != nil {
		return fail(err.Offset, err.Msg)
	}

	name := derNodeName(identifier)

	// Identifier/encoding-form requirements are properties of the tag octet,
	// so they are reported before the length is examined.
	if identifier.ClassType == ClassUniversal {
		switch identifier.Tag {
		case TagEOC:
			return fail(0, "End-of-Contents octet is not allowed in DER")
		case TagSet:
			return 0, 0, &DERError{
				Offset: 0,
				Path:   path,
				Msg:    name + " canonical ordering requires type information",
				Err:    ErrUnsupportedStrictType,
			}
		case TagSequence, TagBoolean, TagInteger, TagNULL,
			TagObjectIdentifier, TagRelativeOID:
			want := TypeConstructed
			wantName := "constructed"
			if identifier.Tag != TagSequence {
				want = TypePrimitive
				wantName = "primitive"
			}
			if identifier.TagType != want {
				gotName := "constructed"
				if identifier.TagType != TypeConstructed {
					gotName = "primitive"
				}
				return fail(0, fmt.Sprintf("%s must be %s in DER, got %s", name, wantName, gotName))
			}
		}
		if identifier.Tag == TagOctetString || derStringTag(identifier.Tag) {
			if identifier.TagType != TypePrimitive {
				return fail(0, fmt.Sprintf("%s must be primitive in DER, got constructed", name))
			}
		}
	}

	if idLen >= len(data) {
		return fail(idLen, "truncated length: no length octet")
	}
	length, lengthLen, err := derParseLength(data[idLen:])
	if err != nil {
		return fail(idLen+err.Offset, err.Msg)
	}

	if length != LengthIndefinite && MaxPacketLengthBytes > 0 && int64(length) > MaxPacketLengthBytes {
		return fail(idLen, fmt.Sprintf("length %d greater than maximum %d", length, MaxPacketLengthBytes))
	}

	contentStart := idLen + lengthLen
	contentEnd := contentStart + length
	if contentEnd > len(data) {
		return fail(len(data), fmt.Sprintf("truncated content: length declares %d bytes but only %d remain",
			length, len(data)-contentStart))
	}

	content := data[contentStart:contentEnd]

	if identifier.TagType == TypeConstructed {
		childIndex := 0
		pos := 0
		for len(content) > 0 {
			childPath := derChildPath(path, childIndex)
			n, _, err := derValidate(content, childPath, depth+1)
			if err != nil {
				err.Offset += contentStart + pos
				return 0, 0, err
			}
			content = content[n:]
			pos += n
			childIndex++
		}
		return contentEnd, contentStart, nil
	}

	if identifier.ClassType == ClassUniversal {
		if dErr := derValidatePrimitive(identifier.Tag, content, contentStart, path, name); dErr != nil {
			return 0, 0, dErr
		}
	}

	return contentEnd, contentStart, nil
}

// derValidatePrimitive applies type-specific DER content rules for universal
// primitive tags. contentOffset is the absolute offset of content[0].
func derValidatePrimitive(tag Tag, content []byte, contentOffset int, path, name string) *DERError {
	fail := func(rel int, msg string) *DERError {
		return &DERError{Offset: contentOffset + rel, Path: path, Msg: msg}
	}

	switch tag {
	case TagBoolean:
		if len(content) != 1 {
			return fail(0, fmt.Sprintf("BOOLEAN must have exactly one content octet, got %d", len(content)))
		}
		if content[0] != 0x00 && content[0] != 0xFF {
			return fail(0, fmt.Sprintf("BOOLEAN content must be 0x00 or 0xFF, got 0x%02x", content[0]))
		}

	case TagInteger:
		if len(content) == 0 {
			return fail(0, "INTEGER content must not be empty")
		}
		if len(content) >= 2 {
			if content[0] == 0x00 && content[1]&0x80 == 0 {
				return fail(0, "INTEGER has redundant leading 0x00 octet")
			}
			if content[0] == 0xFF && content[1]&0x80 != 0 {
				return fail(0, "INTEGER has redundant leading 0xFF octet")
			}
		}

	case TagNULL:
		if len(content) != 0 {
			return fail(0, fmt.Sprintf("NULL content must be empty, got %d bytes", len(content)))
		}

	case TagObjectIdentifier, TagRelativeOID:
		if len(content) == 0 {
			return fail(0, name+" content must not be empty")
		}
		if dErr := derCheckOIDArcs(content, contentOffset, path); dErr != nil {
			return dErr
		}

	case TagUTF8String:
		if !utf8.Valid(content) {
			return fail(0, "invalid UTF-8 string")
		}

	case TagIA5String:
		for i, c := range string(content) {
			if c > 0x7F {
				return fail(i, fmt.Sprintf("invalid character for IA5String at pos %d", i))
			}
		}

	case TagPrintableString:
		for i, c := range string(content) {
			if !derIsPrintable(c) {
				return fail(i, fmt.Sprintf("invalid character for PrintableString at pos %d", i))
			}
		}
	}

	return nil
}

// derIsPrintable reports whether c belongs to the PrintableString alphabet
// (the same set [isPrintableString] enforces).
func derIsPrintable(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '\'', '(', ')', '+', ',', '-', '.', '=', '/', ':', '?', ' ':
		return true
	}
	return false
}

// derParseIdentifier parses one identifier from data, returning it and the
// number of octets consumed. Offsets in an error are relative to data.
func derParseIdentifier(data []byte) (Identifier, int, *DERError) {
	identifier := Identifier{
		ClassType: Class(data[0]) & ClassBitmask,
		TagType:   Type(data[0]) & TypeBitmask,
	}

	if tag := Tag(data[0]) & TagBitmask; tag != HighTag {
		identifier.Tag = tag
		return identifier, 1, nil
	}

	// High-tag-number form: the tag must be at least 31, so short-form tags
	// must not be encoded here.
	var tag Tag
	for i := 1; ; i++ {
		if i >= len(data) {
			return Identifier{}, i, &DERError{Offset: i, Msg: "truncated high-tag-number identifier"}
		}
		b := data[i]
		if i == 1 && b&byte(HighTagValueBitmask) == 0 {
			return Identifier{}, i, &DERError{Offset: i, Msg: "non-minimal high-tag-number identifier"}
		}
		if i > 9 {
			return Identifier{}, i, &DERError{Offset: i, Msg: "high-tag-number tag overflow"}
		}
		tag <<= 7
		tag |= Tag(b) & HighTagValueBitmask
		if b&byte(HighTagContinueBitmask) == 0 {
			if tag < HighTag {
				return Identifier{}, i, &DERError{Offset: 0, Msg: "non-minimal tag encoding: tag 0..30 must use short form"}
			}
			identifier.Tag = tag
			return identifier, i + 1, nil
		}
	}
}

// derParseLength parses one definite length from data, returning it and the
// number of octets consumed. Indefinite and non-minimal encodings are rejected.
// Offsets in an error are relative to data.
func derParseLength(data []byte) (int, int, *DERError) {
	b := data[0]
	if b == 0xFF {
		return 0, 1, &DERError{Offset: 0, Msg: "invalid length byte 0xff"}
	}
	if b == LengthLongFormBitmask {
		return 0, 1, &DERError{Offset: 0, Msg: "indefinite length is not allowed in DER"}
	}
	if b&LengthLongFormBitmask == 0 {
		return int(b), 1, nil
	}

	n := int(b & LengthValueBitmask)
	if n > 8 {
		return 0, 1, &DERError{Offset: 0, Msg: "long-form length overflow"}
	}
	if 1+n > len(data) {
		return 0, len(data), &DERError{Offset: len(data), Msg: "truncated long-form length"}
	}

	var length64 uint64
	for i := 0; i < n; i++ {
		length64 <<= 8
		length64 |= uint64(data[1+i])
	}

	length := int(length64)
	if length < 0 || uint64(length) != length64 {
		return 0, 1, &DERError{Offset: 0, Msg: "long-form length overflow"}
	}

	// DER requires the minimum number of length octets (X.690 10.1): values
	// 0..127 use the short form; a larger value must not start with a zero
	// octet (e.g. 0x82 0x00 0x80 encodes 128 with a redundant leading octet).
	if length64 < 0x80 || data[1] == 0 {
		return 0, 1, &DERError{Offset: 0, Msg: "non-minimal long-form length encoding"}
	}

	return length, n + 1, nil
}

// derCheckOIDArcs validates that content is a complete, minimally base-128
// encoded sequence of OID arcs (OBJECT IDENTIFIER and RELATIVE-OID share the
// arc encoding). It mirrors the accepted values of [parseBase128Ints] while
// reporting the offending raw offset.
func derCheckOIDArcs(content []byte, contentOffset int, path string) *DERError {
	offset := 0
	for offset < len(content) {
		var value uint64
		start := offset
		shifted := 0
		for {
			if offset >= len(content) {
				return &DERError{Offset: contentOffset + start, Path: path, Msg: "truncated base-128 OID arc"}
			}
			b := content[offset]
			if shifted == 0 && b == 0x80 {
				return &DERError{Offset: contentOffset + offset, Path: path, Msg: "OID arc is not minimally encoded"}
			}
			if shifted == 5 {
				return &DERError{Offset: contentOffset + offset, Path: path, Msg: "base-128 OID arc too large"}
			}
			value <<= 7
			value |= uint64(b & 0x7f)
			offset++
			if b&0x80 == 0 {
				if value > math.MaxInt32 {
					return &DERError{Offset: contentOffset + start, Path: path, Msg: "base-128 OID arc too large"}
				}
				break
			}
			shifted++
		}
	}
	return nil
}

// derStringTag reports whether tag is one of the restricted character string
// types the library recognizes. OCTET STRING is handled separately.
func derStringTag(tag Tag) bool {
	switch tag {
	case TagUTF8String,
		TagNumericString,
		TagPrintableString,
		TagT61String,
		TagVideotexString,
		TagIA5String,
		TagGraphicString,
		TagVisibleString,
		TagGeneralString,
		TagUniversalString,
		TagCharacterString,
		TagBMPString:
		return true
	}
	return false
}

// derNodeName returns a human-readable element name used in paths and error
// messages.
func derNodeName(identifier Identifier) string {
	if identifier.ClassType == ClassUniversal {
		if label, ok := tagMap[identifier.Tag]; ok {
			return label
		}
	}
	return fmt.Sprintf("%s tag 0x%x", ClassMap[identifier.ClassType], identifier.Tag)
}

// derChildPath builds the path of childIndex-th child of an element at path.
// The outermost element is the "$" root; its first child is "$[0]".
func derChildPath(path string, childIndex int) string {
	if path == "" {
		path = "$"
	}
	return fmt.Sprintf("%s[%d]", path, childIndex)
}

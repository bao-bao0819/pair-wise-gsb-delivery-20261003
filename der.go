package ber

import (
	"errors"
	"fmt"
	"math"
)

// ErrUnsupportedStrictType is returned (wrapped in a *DERError) when DER
// validation encounters a type outside the supported strict set. SET and
// SET OF always report this: their DER ordering rules require ASN.1 type
// information this package does not have, so no complete-DER claim is made
// for them.
var ErrUnsupportedStrictType = errors.New("unsupported strict type")

// DERError describes the first DER violation found while validating raw
// encoded bytes. Offset is the byte offset in the input where the violation
// occurs and Path identifies the nested value containing it (for example
// "SEQUENCE[1].INTEGER").
type DERError struct {
	Offset int
	Path   string
	Reason string
	// Err is set to ErrUnsupportedStrictType when the violation is an
	// out-of-scope type, so callers can use errors.Is.
	Err error
}

func (e *DERError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("der: offset %d: %s", e.Offset, e.Reason)
	}
	return fmt.Sprintf("der: %s at offset %d (%s)", e.Reason, e.Offset, e.Path)
}

func (e *DERError) Unwrap() error { return e.Err }

// derStringTags are the restricted character string types (plus OCTET STRING)
// supported by strict DER validation. DER requires their primitive form.
var derStringTags = map[Tag]bool{
	TagOctetString:     true,
	TagUTF8String:      true,
	TagNumericString:   true,
	TagPrintableString: true,
	TagT61String:       true,
	TagVideotexString:  true,
	TagIA5String:       true,
	TagGraphicString:   true,
	TagVisibleString:   true,
	TagGeneralString:   true,
	TagUniversalString: true,
	TagCharacterString: true,
	TagBMPString:       true,
}

// derTagNames provides short path names for the universal tags DER
// validation knows about; anything else falls back to tagMap or a
// class(tag) description.
var derTagNames = map[Tag]string{
	TagBoolean:          "BOOLEAN",
	TagInteger:          "INTEGER",
	TagNULL:             "NULL",
	TagObjectIdentifier: "OBJECT IDENTIFIER",
	TagSequence:         "SEQUENCE",
	TagSet:              "SET",
}

// ValidateDER strictly validates data as a single DER-encoded value whose
// type is within the supported set (BOOLEAN, INTEGER, NULL, OBJECT
// IDENTIFIER, the string types, and SEQUENCE). It inspects the raw bytes
// directly -- it does not decode and re-encode -- and reports the first
// violation as a *DERError carrying the byte offset and value path.
//
// Checks include shortest-form tag and length encodings, INTEGER sign
// extension, canonical BOOLEAN values, and the DER prohibition on
// indefinite lengths. SET/SET OF and any type outside the supported set
// yield an error wrapping ErrUnsupportedStrictType.
func ValidateDER(data []byte) error {
	next, err := validateDERTLV(data, 0, "", 0)
	if err != nil {
		return err
	}
	if next != len(data) {
		return &DERError{
			Offset: next,
			Reason: fmt.Sprintf("trailing data: %d byte(s) after top-level value", len(data)-next),
		}
	}
	return nil
}

// DecodePacketDER validates data with ValidateDER and, if it is strict DER
// for a supported type, decodes it with the regular BER decoder. The
// returned Packet therefore carries the same values DecodePacketErr would
// produce for the (valid) input.
func DecodePacketDER(data []byte) (*Packet, error) {
	if err := ValidateDER(data); err != nil {
		return nil, err
	}
	return DecodePacketErr(data)
}

// validateDERTLV validates one TLV starting at offset, returning the offset
// one past its end. pathPrefix is the dotted path of the enclosing value
// ("" at the top level, "SEQUENCE[2]." for a child).
func validateDERTLV(data []byte, offset int, pathPrefix string, depth int) (int, error) {
	if MaxNestingDepth > 0 && depth >= MaxNestingDepth {
		return 0, &DERError{
			Offset: offset,
			Path:   pathPrefix,
			Reason: fmt.Sprintf("nesting depth %d exceeds maximum %d", depth, MaxNestingDepth),
		}
	}

	identifier, idLen, err := derReadIdentifier(data, offset, pathPrefix)
	if err != nil {
		return 0, err
	}
	path := pathPrefix + derTypeName(identifier)

	length, lenLen, err := derReadLength(data, offset+idLen, path)
	if err != nil {
		return 0, err
	}

	contentStart := offset + idLen + lenLen
	contentEnd := contentStart + length
	if contentEnd > len(data) {
		return 0, &DERError{
			Offset: offset + idLen,
			Path:   path,
			Reason: fmt.Sprintf("content of %d byte(s) extends past end of data", length),
		}
	}

	if identifier.ClassType != ClassUniversal {
		return 0, &DERError{
			Offset: offset,
			Path:   path,
			Reason: fmt.Sprintf("%s class tag %d: %s", ClassMap[identifier.ClassType], identifier.Tag, ErrUnsupportedStrictType),
			Err:    ErrUnsupportedStrictType,
		}
	}

	switch identifier.Tag {
	case TagBoolean:
		if err := derRequirePrimitive(identifier, offset, path); err != nil {
			return 0, err
		}
		if length != 1 {
			return 0, &DERError{Offset: offset + idLen, Path: path, Reason: fmt.Sprintf("BOOLEAN must have length 1, got %d", length)}
		}
		if v := data[contentStart]; v != 0x00 && v != 0xFF {
			return 0, &DERError{Offset: contentStart, Path: path, Reason: fmt.Sprintf("non-canonical BOOLEAN value 0x%02X (must be 0x00 or 0xFF)", v)}
		}

	case TagInteger:
		if err := derRequirePrimitive(identifier, offset, path); err != nil {
			return 0, err
		}
		if length == 0 {
			return 0, &DERError{Offset: offset + idLen, Path: path, Reason: "INTEGER must have at least one content octet"}
		}
		if length > 1 {
			first, second := data[contentStart], data[contentStart+1]
			if first == 0x00 && second&0x80 == 0 {
				return 0, &DERError{Offset: contentStart, Path: path, Reason: "INTEGER not minimally encoded (redundant leading 0x00)"}
			}
			if first == 0xFF && second&0x80 != 0 {
				return 0, &DERError{Offset: contentStart, Path: path, Reason: "INTEGER not minimally encoded (redundant leading 0xFF)"}
			}
		}

	case TagNULL:
		if err := derRequirePrimitive(identifier, offset, path); err != nil {
			return 0, err
		}
		if length != 0 {
			return 0, &DERError{Offset: offset + idLen, Path: path, Reason: fmt.Sprintf("NULL must have length 0, got %d", length)}
		}

	case TagObjectIdentifier:
		if err := derRequirePrimitive(identifier, offset, path); err != nil {
			return 0, err
		}
		if length == 0 {
			return 0, &DERError{Offset: offset + idLen, Path: path, Reason: "OBJECT IDENTIFIER must have at least one content octet"}
		}
		if err := derCheckOIDContent(data, contentStart, contentEnd, path); err != nil {
			return 0, err
		}

	case TagSequence:
		if identifier.TagType != TypeConstructed {
			return 0, &DERError{Offset: offset, Path: path, Reason: "SEQUENCE must use constructed encoding"}
		}
		for pos, i := contentStart, 0; pos < contentEnd; i++ {
			next, err := validateDERTLV(data, pos, fmt.Sprintf("%s[%d].", path, i), depth+1)
			if err != nil {
				return 0, err
			}
			pos = next
		}

	case TagSet:
		return 0, &DERError{
			Offset: offset,
			Path:   path,
			Reason: "SET/SET OF: " + ErrUnsupportedStrictType.Error() + " (DER ordering requires ASN.1 type information)",
			Err:    ErrUnsupportedStrictType,
		}

	default:
		if !derStringTags[identifier.Tag] {
			return 0, &DERError{
				Offset: offset,
				Path:   path,
				Reason: fmt.Sprintf("universal tag %d: %s", identifier.Tag, ErrUnsupportedStrictType),
				Err:    ErrUnsupportedStrictType,
			}
		}
		if err := derRequirePrimitive(identifier, offset, path); err != nil {
			return 0, err
		}
	}

	return contentEnd, nil
}

// derRequirePrimitive reports an error if the identifier is constructed.
func derRequirePrimitive(identifier Identifier, offset int, path string) error {
	if identifier.TagType != TypePrimitive {
		return &DERError{Offset: offset, Path: path, Reason: derTypeName(identifier) + " must use primitive encoding in DER"}
	}
	return nil
}

// derCheckOIDContent verifies that every subidentifier arc in an OBJECT
// IDENTIFIER value is a minimally encoded, properly terminated base-128
// integer.
func derCheckOIDContent(data []byte, start, end int, path string) error {
	for pos := start; pos < end; {
		arcStart := pos
		// A leading 0x80 octet is a non-minimal (padded) arc encoding.
		if data[pos] == 0x80 {
			return &DERError{Offset: pos, Path: path, Reason: "OBJECT IDENTIFIER arc not minimally encoded"}
		}
		for {
			b := data[pos]
			pos++
			if b&0x80 == 0 {
				break
			}
			if pos == end {
				return &DERError{Offset: arcStart, Path: path, Reason: "truncated base-128 arc in OBJECT IDENTIFIER"}
			}
		}
	}
	return nil
}

// derReadIdentifier parses identifier octets from data starting at offset,
// enforcing the DER shortest-form rules (X.690 10.1): tags below 31 must
// use the short form.
func derReadIdentifier(data []byte, offset int, pathPrefix string) (Identifier, int, error) {
	if offset >= len(data) {
		return Identifier{}, 0, &DERError{Offset: offset, Path: pathPrefix, Reason: "unexpected end of data reading identifier"}
	}

	b := data[offset]
	identifier := Identifier{
		ClassType: Class(b) & ClassBitmask,
		TagType:   Type(b) & TypeBitmask,
	}
	read := 1

	if tag := Tag(b) & TagBitmask; tag != HighTag {
		identifier.Tag = tag
		return identifier, read, nil
	}

	tagBytes := 0
	for {
		if offset+read >= len(data) {
			return Identifier{}, read, &DERError{Offset: offset + read, Path: pathPrefix, Reason: "unexpected end of data in high-tag-number identifier"}
		}
		hb := data[offset+read]
		read++
		tagBytes++

		identifier.Tag = identifier.Tag<<7 | Tag(hb)&HighTagValueBitmask

		if tagBytes == 1 && identifier.Tag == 0 {
			return Identifier{}, read, &DERError{Offset: offset + read - 1, Path: pathPrefix, Reason: "invalid first high-tag-number tag byte"}
		}
		if tagBytes > 9 {
			return Identifier{}, read, &DERError{Offset: offset + read - 1, Path: pathPrefix, Reason: "high-tag-number tag overflow"}
		}
		if Tag(hb)&HighTagContinueBitmask == 0 {
			break
		}
	}

	if identifier.Tag < HighTag {
		return Identifier{}, read, &DERError{
			Offset: offset,
			Path:   pathPrefix,
			Reason: fmt.Sprintf("tag %d not minimally encoded (high-tag form used for tag < 31)", identifier.Tag),
		}
	}
	return identifier, read, nil
}

// derReadLength parses the length octets from data starting at offset,
// enforcing the DER definite shortest-form rules (X.690 10.1): indefinite
// form is forbidden, lengths below 128 use the short form, and long-form
// lengths have no leading zero octets.
func derReadLength(data []byte, offset int, path string) (int, int, error) {
	if offset >= len(data) {
		return 0, 0, &DERError{Offset: offset, Path: path, Reason: "unexpected end of data reading length"}
	}

	b := data[offset]
	read := 1

	switch {
	case b == 0xFF:
		return 0, read, &DERError{Offset: offset, Path: path, Reason: "invalid length byte 0xff"}
	case b == LengthLongFormBitmask:
		return 0, read, &DERError{Offset: offset, Path: path, Reason: "indefinite length not allowed in DER"}
	case b&LengthLongFormBitmask == 0:
		return int(b) & LengthValueBitmask, read, nil
	}

	lengthBytes := int(b) & LengthValueBitmask
	if lengthBytes > 8 {
		return 0, read, &DERError{Offset: offset, Path: path, Reason: "long-form length overflow"}
	}
	if offset+read+lengthBytes > len(data) {
		return 0, read, &DERError{Offset: offset + read, Path: path, Reason: "unexpected end of data in long-form length"}
	}
	if data[offset+read] == 0x00 {
		return 0, read, &DERError{Offset: offset + read, Path: path, Reason: "long-form length has leading zero octet (not shortest form)"}
	}

	var length64 uint64
	for i := 0; i < lengthBytes; i++ {
		length64 = length64<<8 | uint64(data[offset+read])
		read++
	}
	if length64 > math.MaxInt64 {
		return 0, read, &DERError{Offset: offset, Path: path, Reason: "long-form length overflow"}
	}
	length := int(length64)
	if length < 0 || uint64(length) != length64 {
		return 0, read, &DERError{Offset: offset, Path: path, Reason: "long-form length overflow"}
	}

	if length64 < 128 {
		return 0, read, &DERError{Offset: offset, Path: path, Reason: fmt.Sprintf("length %d must use short-form encoding", length64)}
	}
	return length, read, nil
}

// derTypeName returns a short human-readable name for an identifier, used
// to build DERError paths.
func derTypeName(identifier Identifier) string {
	if identifier.ClassType == ClassUniversal {
		if name, ok := derTagNames[identifier.Tag]; ok {
			return name
		}
		if name, ok := tagMap[identifier.Tag]; ok {
			return name
		}
	}
	return fmt.Sprintf("%s(%d)", ClassMap[identifier.ClassType], identifier.Tag)
}

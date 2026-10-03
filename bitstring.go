package ber

import (
	"errors"
	"fmt"
)

// BitStringValue is the structured value of an ASN.1 BIT STRING: the
// significant content octets together with the number of meaningful bits.
// Bits are packed from the most significant bit of the first byte, and the
// unused tail bits of the final byte are always cleared to zero.
type BitStringValue struct {
	// Bytes holds the significant content octets (without the leading
	// unused-bits count octet of the BER encoding).
	Bytes []byte
	// BitLength is the number of significant bits. It satisfies
	// 8*(len(Bytes)-1) < BitLength <= 8*len(Bytes), and is 0 for an empty
	// bit string.
	BitLength int
}

// UnusedBits returns the number of padding bits in the final byte of Bytes.
func (v BitStringValue) UnusedBits() int {
	return (8 - v.BitLength%8) % 8
}

// ParseBitString parses the content octets of a primitive BIT STRING (the
// leading unused-bits count octet followed by the data octets) into a
// BitStringValue. An empty bit string is encoded as a single zero octet; a
// zero-length content is an error, not an empty bit string.
func ParseBitString(content []byte) (BitStringValue, error) {
	return parseBitStringSegments([][]byte{content})
}

// parseBitStringSegments flattens BER bit string segments (each still
// carrying its leading unused-bits count octet) into a single BitStringValue.
// Every segment except the last must be byte-aligned (unused bits == 0); the
// final segment's unused-bits count is range-checked and the corresponding
// tail bits are cleared in the returned value.
func parseBitStringSegments(segments [][]byte) (BitStringValue, error) {
	var value BitStringValue
	var out []byte

	for i, segment := range segments {
		if len(segment) == 0 {
			return value, errors.New("bit string segment missing unused-bits byte")
		}
		unused := int(segment[0])
		if unused > 7 {
			return value, fmt.Errorf("bit string unused bits %d out of range (0-7)", unused)
		}
		data := segment[1:]
		if i < len(segments)-1 && unused != 0 {
			return value, errors.New("non-final bit string segment is not byte-aligned")
		}
		if len(data) == 0 {
			if unused != 0 {
				return value, fmt.Errorf("bit string segment has %d unused bits but no data bytes", unused)
			}
			continue
		}
		out = append(out, data...)
		value.BitLength += len(data)*8 - unused
	}

	// Clear the padding bits of the final byte so the structured value
	// carries no dirty tail bits. The input segments are not mutated.
	if unused := len(out)*8 - value.BitLength; unused > 0 {
		out[len(out)-1] &= 0xff << uint(unused)
	}
	value.Bytes = out

	return value, nil
}

// parseConstructedBitString flattens the segments of a constructed BIT STRING
// (nested constructed segments included) in encoding order and parses them
// into a single BitStringValue.
func parseConstructedBitString(p *Packet) (BitStringValue, error) {
	var segments [][]byte
	if err := collectBitStringSegments(p, &segments); err != nil {
		return BitStringValue{}, err
	}
	return parseBitStringSegments(segments)
}

// collectBitStringSegments appends the content octets of every primitive BIT
// STRING segment of p to segments, recursing into nested constructed BIT
// STRINGs in order. Any segment that is not a universal BIT STRING is an
// error.
func collectBitStringSegments(p *Packet, segments *[][]byte) error {
	for _, child := range p.Children {
		if child.ClassType != ClassUniversal || child.Tag != TagBitString {
			return errors.New("constructed bit string contains non-bit-string segment")
		}
		if child.TagType == TypeConstructed {
			if err := collectBitStringSegments(child, segments); err != nil {
				return err
			}
			continue
		}
		*segments = append(*segments, child.Data.Bytes())
	}
	return nil
}

// NewBitStringErr builds a BIT STRING packet from data, using the first
// bitLength bits. bitLength must be in the range [0, 8*len(data)] and every
// bit of data beyond bitLength must be zero; a dirty tail bit is rejected as
// an error. The packet is encoded in primitive form with the leading
// unused-bits count octet.
func NewBitStringErr(classType Class, tagType Type, tag Tag, data []byte, bitLength int, description string) (*Packet, error) {
	if bitLength < 0 {
		return nil, fmt.Errorf("invalid bit length %d", bitLength)
	}
	if bitLength > len(data)*8 {
		return nil, fmt.Errorf("bit length %d exceeds data length of %d bytes", bitLength, len(data))
	}
	for bit := bitLength; bit < len(data)*8; bit++ {
		if data[bit/8]&(0x80>>uint(bit%8)) != 0 {
			return nil, fmt.Errorf("bit %d beyond bit length %d is set", bit, bitLength)
		}
	}

	used := (bitLength + 7) / 8
	unused := used*8 - bitLength

	p := Encode(classType, tagType, tag, nil, description)
	p.Data.WriteByte(byte(unused))
	p.Data.Write(data[:used])
	p.Value = BitStringValue{
		Bytes:     append([]byte(nil), data[:used]...),
		BitLength: bitLength,
	}

	return p, nil
}

// NewBitString builds a BIT STRING packet from data, using the first
// bitLength bits. It panics if bitLength is out of range or any bit beyond
// bitLength is set; use [NewBitStringErr] to receive an error instead.
func NewBitString(classType Class, tagType Type, tag Tag, data []byte, bitLength int, description string) *Packet {
	p, err := NewBitStringErr(classType, tagType, tag, data, bitLength, description)
	if err != nil {
		panic(err.Error())
	}
	return p
}

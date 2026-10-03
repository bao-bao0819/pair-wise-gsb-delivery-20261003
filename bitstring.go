package ber

import (
	"errors"
	"fmt"
)

// BitStringValue is the structured value of an ASN.1 BIT STRING: the
// significant bits of Bytes, MSB-first, exactly BitLength of them. The final
// byte is left-aligned; its 8*len(Bytes)-BitLength trailing bits are unused
// and always zero.
type BitStringValue struct {
	Bytes     []byte
	BitLength int
}

// UnusedBits returns the number of padding bits in the final byte.
func (b BitStringValue) UnusedBits() int {
	return len(b.Bytes)*8 - b.BitLength
}

// ParseBitString parses the content octets of a primitive BIT STRING: a
// leading unused-bits count byte followed by the data bytes. An empty bit
// string is encoded as a single zero byte; zero content octets mean the
// unused-bits byte is missing, which is an error.
func ParseBitString(content []byte) (BitStringValue, error) {
	if len(content) == 0 {
		return BitStringValue{}, errors.New("bit string content missing unused-bits byte")
	}
	unused := int(content[0])
	if unused > 7 {
		return BitStringValue{}, fmt.Errorf("bit string unused-bits count %d out of range 0-7", unused)
	}
	data := content[1:]
	if len(data) == 0 {
		if unused != 0 {
			return BitStringValue{}, fmt.Errorf("empty bit string must have 0 unused bits, got %d", unused)
		}
		return BitStringValue{Bytes: []byte{}, BitLength: 0}, nil
	}
	if mask := byte(0xff >> (8 - unused)); data[len(data)-1]&mask != 0 {
		return BitStringValue{}, fmt.Errorf("bit string has non-zero bits in the %d unused trailing bits", unused)
	}
	return BitStringValue{Bytes: data, BitLength: len(data)*8 - unused}, nil
}

// flattenBitString collapses a constructed BIT STRING into a single logical
// bit string. Every child must be a universal BIT STRING; nested constructed
// children are flattened in order. All segments except the last must be
// byte-aligned (zero unused bits).
func flattenBitString(p *Packet) (BitStringValue, error) {
	var segments [][]byte
	if err := collectBitStringSegments(p, &segments); err != nil {
		return BitStringValue{}, err
	}

	data := []byte{}
	bitLength := 0
	for i, segment := range segments {
		bv, err := ParseBitString(segment)
		if err != nil {
			return BitStringValue{}, fmt.Errorf("bit string segment %d: %w", i, err)
		}
		if i < len(segments)-1 && bv.UnusedBits() != 0 {
			return BitStringValue{}, fmt.Errorf("bit string segment %d: intermediate segment not byte-aligned (%d unused bits)", i, bv.UnusedBits())
		}
		data = append(data, bv.Bytes...)
		bitLength += bv.BitLength
	}
	return BitStringValue{Bytes: data, BitLength: bitLength}, nil
}

func collectBitStringSegments(p *Packet, segments *[][]byte) error {
	for _, child := range p.Children {
		if child.ClassType != ClassUniversal || child.Tag != TagBitString {
			return fmt.Errorf("constructed bit string child is not a universal bit string (class %v, tag %v)", child.ClassType, child.Tag)
		}
		if child.TagType == TypeConstructed {
			if err := collectBitStringSegments(child, segments); err != nil {
				return err
			}
			continue
		}
		*segments = append(*segments, child.ByteValue)
	}
	return nil
}

// NewBitStringErr builds a primitive BIT STRING packet from data and an exact
// bit length. len(data) must be the minimal number of bytes holding bitLength
// bits, and any trailing bits beyond bitLength must be zero; use
// [NewBitString] for a panicking variant.
func NewBitStringErr(classType Class, tagType Type, tag Tag, data []byte, bitLength int, description string) (*Packet, error) {
	if bitLength < 0 || (bitLength+7)/8 != len(data) {
		return nil, fmt.Errorf("bit length %d out of range for %d data bytes", bitLength, len(data))
	}
	unused := len(data)*8 - bitLength
	if unused > 0 && data[len(data)-1]&byte(0xff>>(8-unused)) != 0 {
		return nil, fmt.Errorf("bit string has non-zero bits in the %d unused trailing bits", unused)
	}

	p := Encode(classType, tagType, tag, nil, description)
	p.Value = BitStringValue{Bytes: append([]byte{}, data...), BitLength: bitLength}
	p.Data.WriteByte(byte(unused))
	p.Data.Write(data)
	return p, nil
}

// NewBitString builds a BIT STRING packet. It panics if bitLength does not
// match data or the trailing bits are dirty; use [NewBitStringErr] to receive
// an error instead.
func NewBitString(classType Class, tagType Type, tag Tag, data []byte, bitLength int, description string) *Packet {
	p, err := NewBitStringErr(classType, tagType, tag, data, bitLength, description)
	if err != nil {
		panic(err.Error())
	}
	return p
}

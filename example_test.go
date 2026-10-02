package ber_test

import (
	"errors"
	"fmt"

	ber "github.com/go-asn1-ber/asn1-ber"
)

// ExampleDecodePacketErr encodes a SEQUENCE containing a single OCTET STRING,
// serializes it to BER bytes, and decodes it back into a packet tree.
func ExampleDecodePacketErr() {
	seq := ber.NewSequence("greeting")
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "Hello, world", ""))

	packet, err := ber.DecodePacketErr(seq.Bytes())
	if err != nil {
		panic(err)
	}

	fmt.Println(packet.Children[0].Value)
	// Output: Hello, world
}

// ExampleCheckDER validates DER bytes directly, locating the first violation
// by byte offset and tree path instead of decoding and re-encoding.
func ExampleCheckDER() {
	// SEQUENCE { BOOLEAN 0x01 }: 0x01 is a legal BER TRUE but not the canonical
	// DER value (must be 0xFF).
	data := []byte{0x30, 0x03, 0x01, 0x01, 0x01}

	err := ber.CheckDER(data)
	var derr *ber.DERError
	if errors.As(err, &derr) {
		fmt.Printf("violation at offset %d, path %s\n", derr.Offset, derr.Path)
	}
	// Output: violation at offset 4, path $[0]
}

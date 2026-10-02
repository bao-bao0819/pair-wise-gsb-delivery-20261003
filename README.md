# asn1-ber

[![Go Reference](https://pkg.go.dev/badge/github.com/go-asn1-ber/asn1-ber.svg)](https://pkg.go.dev/github.com/go-asn1-ber/asn1-ber)
[![PR](https://github.com/go-asn1-ber/asn1-ber/actions/workflows/pr.yml/badge.svg)](https://github.com/go-asn1-ber/asn1-ber/actions/workflows/pr.yml)
[![Lint](https://github.com/go-asn1-ber/asn1-ber/actions/workflows/lint.yml/badge.svg)](https://github.com/go-asn1-ber/asn1-ber/actions/workflows/lint.yml)

ASN.1 BER encoding and decoding for Go, with no external dependencies. This is
the BER layer used by [go-ldap](https://github.com/go-ldap/ldap); it implements
the subset of BER/DER needed for LDAP (RFC 4511), including integers, booleans,
strings, object identifiers, REAL numbers, and generalized time.

## Install

```sh
go get github.com/go-asn1-ber/asn1-ber
```

Requires Go 1.22 or newer.

## Usage

```go
package main

import (
	"fmt"

	ber "github.com/go-asn1-ber/asn1-ber"
)

func main() {
	// Encode a SEQUENCE containing a single OCTET STRING.
	seq := ber.NewSequence("greeting")
	seq.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "Hello, world", ""))

	// Serialize, then decode back into a packet tree.
	packet, err := ber.DecodePacketErr(seq.Bytes())
	if err != nil {
		panic(err)
	}

	fmt.Println(packet.Children[0].Value) // Hello, world
}
```

`DecodePacketErr` (and `ReadPacket`, which reads from an `io.Reader`) return an
error on malformed input. `DecodePacket` is an older variant that returns `nil`
instead; prefer the `Err` form. When decoding untrusted data, the
`MaxPacketLengthBytes` and `MaxNestingDepth` package variables bound memory and
recursion.

See the [reference documentation](https://pkg.go.dev/github.com/go-asn1-ber/asn1-ber)
for the full API.

## DER validation

BER tolerates several non-canonical encodings. To require DER (the canonical
subset of BER) without changing how the ordinary decoder behaves, use the
explicit DER functions:

```go
// Validate without decoding; the *ber.DERError pinpoints the first violation.
if err := ber.CheckDER(data); err != nil {
    var derr *ber.DERError
    if errors.As(err, &derr) {
        log.Fatalf("not DER at byte %d (%s): %v", derr.Offset, derr.Path, derr)
    }
}

// Or validate and decode in one call; a valid input yields the same Packet as
// DecodePacketErr.
packet, err := ber.DecodePacketDER(data)
```

Checks are scoped to the types the library supports — BOOLEAN, INTEGER, NULL,
OBJECT IDENTIFIER, the character strings, OCTET STRING, and SEQUENCE — and
cover minimal tag/length encoding, definite length, integer sign padding, and
the canonical boolean value. SET/SET OF return an error wrapping
`ber.ErrUnsupportedStrictType`, since their ordering needs type information;
this is deliberately not presented as complete DER.

## Development

```sh
go test -race ./...                       # unit tests + conformance suite
go test -run='^$' -fuzz=FuzzDecodePacket  # fuzz the decoder
golangci-lint run                         # lint
```

The conformance suite in `tests/` is built from the
[Strozhevsky ASN.1 test suite](http://www.strozhevsky.com/free_docs/); see
[tests/README.md](tests/README.md).

## License

MIT. See [LICENSE](LICENSE).

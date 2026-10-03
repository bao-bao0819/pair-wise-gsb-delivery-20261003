# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `BitStringValue`, the structured value of a BIT STRING (`Bytes` plus
  `BitLength`), now populated on `Packet.Value` for both primitive and
  constructed BIT STRINGs. Constructed encodings (definite and indefinite
  length, including nesting) are flattened in segment order; intermediate
  segments must be byte-aligned and the final segment's unused-bits count is
  range-checked, with the unused tail bits cleared in the structured value.
- `ParseBitString` to parse the content octets of a primitive BIT STRING.
- `NewBitString` and `NewBitStringErr`: constructors that encode a bit
  string by bit length and reject out-of-range lengths and dirty tail bits.
- `NewIntegerErr`, `NewRealErr`, `NewOIDErr`, and `NewRelativeOIDErr`:
  error-returning variants of the existing constructors that report invalid
  input instead of panicking or returning `nil`.

### Changed

- Require Go 1.22 or newer.
- Constructed packets no longer populate the exported `Data` field after
  decoding or `AppendChild`. Content is now serialized lazily by `Bytes()`;
  read a constructed packet's content via `Bytes()` or by walking `Children`.
  This removes an O(depth x subtree) memory amplification.
- `NewOID` and `NewRelativeOID` now return `nil` instead of panicking on a
  structurally invalid OID string (matching how they already handled other
  invalid strings), and are deprecated in favor of the `*Err` variants.

### Deprecated

- `NewOID` and `NewRelativeOID` — use `NewOIDErr` / `NewRelativeOIDErr`.

### Fixed

- Invalid BIT STRING encodings were silently accepted: an out-of-range
  unused-bits count, a zero-length content (missing the unused-bits byte,
  which is not an empty bit string), non-BIT STRING segments inside a
  constructed BIT STRING, and non-byte-aligned intermediate segments are now
  rejected.
- OID and Relative-OID parse errors were silently swallowed, so
  `DecodePacketErr` reported success on a malformed OID; they now propagate.
- Over-long `INTEGER`/`BOOLEAN`/`ENUMERATED` values (more than 8 bytes)
  silently decoded to `0`; they are now rejected.
- `IA5String` incorrectly rejected the valid `0x7F` (DEL) character.
- An 8-byte long-form length with the high bit set decoded to a negative
  length and was mistaken for indefinite length; it is now rejected as
  overflow.
- `DecodePacket` returned a partially-populated packet on a decode error
  despite documenting that it returns `nil`.
- Memory amplification in `AppendChild`, which retained a serialized copy of
  every subtree at each level.

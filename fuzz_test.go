//go:build go1.18
// +build go1.18

// Fuzzing test(s) are gated behind build tags in order to avoid generating errors
// when running on versions of Go that do not support fuzzing. This was added in
// Go 1.18.

package ber

import (
	"errors"
	"os"
	"testing"
	"time"
)

func FuzzDecodePacket(f *testing.F) {
	// Seed the fuzz corpus with the test cases defined in suite_test.go
	for _, tc := range testCases {
		file := tc.File

		dataIn, err := os.ReadFile(file)
		if err != nil {
			f.Fatalf("failed to load file %s into fuzz corpus: %v", file, err)
			continue
		}
		f.Add(dataIn)
	}

	// Seed the fuzz corpus with data known to cause panics in the past
	f.Add([]byte{0x09, 0x02, 0x85, 0x30})
	f.Add([]byte{0x09, 0x01, 0xcf})

	f.Fuzz(func(t *testing.T, data []byte) {
		stime := time.Now()
		p, err := DecodePacketErr(data)

		if e := time.Since(stime); e > (time.Millisecond * 500) {
			t.Fatalf("DecodePacketErr took too long: %s", e)
		}

		if p == nil && err == nil {
			t.Fatalf("DecodePacketErr returned a nil packet and no error")
		}
	})
}

func FuzzCheckDER(f *testing.F) {
	for _, tc := range testCases {
		dataIn, err := os.ReadFile(tc.File)
		if err != nil {
			f.Fatalf("failed to load %s: %v", tc.File, err)
		}
		f.Add(dataIn)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CheckDER panicked on % x: %v", data, r)
			}
		}()

		err := CheckDER(data)
		if err == nil {
			// DecodePacketDER and CheckDER must agree. Note that an input can
			// pass the structure-level DER checks yet fail to decode when its
			// type is outside the supported scope (e.g. a malformed REAL), so
			// acceptance does not imply a successful BER decode.
			if _, derErr := DecodePacketDER(data); derErr != nil {
				if _, berErr := DecodePacketErr(data); berErr == nil {
					t.Fatalf("DecodePacketDER(% x) failed %v but DecodePacketErr succeeded", data, derErr)
				}
			}
			return
		}

		var derr *DERError
		if !errors.As(err, &derr) {
			t.Fatalf("CheckDER(% x): non-DERError error %T", data, err)
		}
		if derr.Offset < 0 || derr.Offset > len(data) {
			t.Fatalf("CheckDER(% x): out-of-range offset %d", data, derr.Offset)
		}
	})
}

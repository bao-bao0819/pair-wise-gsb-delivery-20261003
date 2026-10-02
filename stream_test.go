package ws

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

type decodedFrame struct {
	header  Header
	payload []byte
}

// decodeStream feeds chunks into a StreamDecoder and collects completed
// frames. It returns the frames and the result of Finish().
func decodeStream(t *testing.T, state State, chunks ...[]byte) ([]decodedFrame, error) {
	t.Helper()
	var (
		frames []decodedFrame
		cur    []byte
	)
	d := NewStreamDecoder(state, func(h Header, chunk []byte) {
		cur = append(cur, chunk...)
	})
	for _, c := range chunks {
		for len(c) > 0 {
			n, st := d.Feed(c)
			c = c[n:]
			switch st {
			case DecodeFrame:
				frames = append(frames, decodedFrame{d.Header(), cur})
				cur = nil
			case DecodeError:
				return frames, d.Err()
			case DecodeNeedMore:
				if n == 0 {
					t.Fatalf("decoder made no progress on %d bytes", len(c))
				}
			}
		}
	}
	return frames, d.Finish()
}

func streamTestSample() ([]byte, []Frame) {
	big := make([]byte, 300)
	for i := range big {
		big[i] = byte(i)
	}
	frames := []Frame{
		NewTextFrame([]byte("hello")),
		MaskFrameWith(NewBinaryFrame(big), [4]byte{1, 2, 3, 4}),
		NewPingFrame([]byte("hi")),
		MaskFrameWith(NewBinaryFrame(nil), [4]byte{5, 6, 7, 8}),
		NewFrame(OpText, false, []byte("foo")),
		MaskFrameWith(NewFrame(OpContinuation, true, []byte("bar")), [4]byte{9, 8, 7, 6}),
		NewCloseFrame(nil),
	}
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(MustCompileFrame(f))
	}
	return buf.Bytes(), frames
}

func wantFrames(frames []Frame) []decodedFrame {
	var want []decodedFrame
	for _, f := range frames {
		p := f.Payload
		if f.Header.Masked {
			p = UnmaskFrame(f).Payload
		}
		if len(p) == 0 {
			p = nil
		}
		want = append(want, decodedFrame{f.Header, p})
	}
	return want
}

func TestStreamDecoderEquivalence(t *testing.T) {
	sample, frames := streamTestSample()
	want := wantFrames(frames)
	check := func(label string, chunks ...[]byte) {
		t.Helper()
		got, err := decodeStream(t, 0, chunks...)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", label, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\ngot:\n\t%#v\nwant:\n\t%#v", label, got, want)
		}
	}
	check("whole", sample)
	// All single split points.
	for i := 0; i <= len(sample); i++ {
		check("single split", sample[:i], sample[i:])
	}
	// All pairs of split points.
	for i := 0; i <= len(sample); i++ {
		for j := i; j <= len(sample); j++ {
			check("double split", sample[:i], sample[i:j], sample[j:])
		}
	}
	// Byte by byte.
	chunks := make([][]byte, len(sample))
	for i := range sample {
		chunks[i] = sample[i : i+1]
	}
	check("bytewise", chunks...)
}

func TestStreamDecoderMultipleFramesSingleInput(t *testing.T) {
	sample, frames := streamTestSample()
	want := wantFrames(frames)
	var got []decodedFrame
	d := NewStreamDecoder(0, nil)
	p := sample
	for len(p) > 0 {
		n, st := d.Feed(p)
		if st != DecodeFrame {
			t.Fatalf("Feed() status = %v, want DecodeFrame", st)
		}
		if n == 0 || n > len(p) {
			t.Fatalf("Feed() consumed %d of %d bytes", n, len(p))
		}
		got = append(got, decodedFrame{d.Header(), nil})
		p = p[n:]
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].header != want[i].header {
			t.Errorf("frame #%d header = %+v, want %+v", i, got[i].header, want[i].header)
		}
	}
	if err := d.Finish(); err != nil {
		t.Errorf("Finish() = %v, want nil", err)
	}
}

func TestStreamDecoderMaskOffsetAcrossChunks(t *testing.T) {
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	mask := [4]byte{0xde, 0xad, 0xbe, 0xef}
	wire := MustCompileFrame(MaskFrameWith(NewBinaryFrame(payload), mask))
	// Feed with chunk sizes that are not multiples of 4 to rotate the
	// mask cipher offset across fragment boundaries.
	sizes := []int{1, 2, 3, 5, 7, 11, 13, 17, 4, 8, 64, 1}
	var got []byte
	d := NewStreamDecoder(0, func(h Header, chunk []byte) {
		got = append(got, chunk...)
	})
	off := 0
	for off < len(wire) {
		for _, s := range sizes {
			if off >= len(wire) {
				break
			}
			end := off + s
			if end > len(wire) {
				end = len(wire)
			}
			n, st := d.Feed(wire[off:end])
			off += n
			if st == DecodeError {
				t.Fatalf("unexpected error: %v", d.Err())
			}
		}
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("reassembled payload mismatch")
	}
	if err := d.Finish(); err != nil {
		t.Errorf("Finish() = %v, want nil", err)
	}
}

func TestStreamDecoderLargeFrame(t *testing.T) {
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i >> 8)
	}
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	wire := MustCompileFrame(MaskFrameWith(NewBinaryFrame(payload), mask))
	var (
		got      []byte
		maxChunk int
	)
	d := NewStreamDecoder(0, func(h Header, chunk []byte) {
		if len(chunk) > maxChunk {
			maxChunk = len(chunk)
		}
		got = append(got, chunk...)
	})
	const step = 8191 // odd size to rotate the mask offset
	for off := 0; off < len(wire); {
		end := off + step
		if end > len(wire) {
			end = len(wire)
		}
		n, st := d.Feed(wire[off:end])
		off += n
		if st == DecodeError {
			t.Fatalf("unexpected error: %v", d.Err())
		}
	}
	if h := d.Header(); h.Length != int64(len(payload)) {
		t.Errorf("header length = %d, want %d", h.Length, len(payload))
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("reassembled payload mismatch")
	}
	if maxChunk > streamScratchSize {
		t.Errorf("masked chunk size = %d, exceeds scratch size %d", maxChunk, streamScratchSize)
	}
	if err := d.Finish(); err != nil {
		t.Errorf("Finish() = %v, want nil", err)
	}
}

func TestStreamDecoderFinish(t *testing.T) {
	wire := MustCompileFrame(MaskFrameWith(NewTextFrame([]byte("hello")), [4]byte{1, 2, 3, 4}))
	for _, test := range []struct {
		name string
		n    int
		err  error
	}{
		{"complete", len(wire), nil},
		{"empty", 0, nil},
		{"mid header first byte", 1, io.ErrUnexpectedEOF},
		{"mid header extended", 3, io.ErrUnexpectedEOF},
		{"mid payload", len(wire) - 1, io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := NewStreamDecoder(0, nil)
			n, st := d.Feed(wire[:test.n])
			if st == DecodeError {
				t.Fatalf("unexpected error: %v", d.Err())
			}
			if n != test.n && st != DecodeFrame {
				t.Fatalf("consumed %d of %d", n, test.n)
			}
			if err := d.Finish(); err != test.err {
				t.Errorf("Finish() = %v, want %v", err, test.err)
			}
		})
	}
}

func TestStreamDecoderProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		state State
		wire  []byte
		err   error
	}{
		{
			name:  "unmasked client frame",
			state: StateServerSide,
			wire:  MustCompileFrame(NewTextFrame([]byte("hello"))),
			err:   ErrProtocolMaskRequired,
		},
		{
			name:  "masked server frame",
			state: StateClientSide,
			wire:  MustCompileFrame(MaskFrame(NewTextFrame([]byte("hello")))),
			err:   ErrProtocolMaskUnexpected,
		},
		{
			name:  "reserved opcode",
			state: 0,
			wire:  MustCompileFrame(NewFrame(OpCode(0x3), true, nil)),
			err:   ErrProtocolOpCodeReserved,
		},
		{
			name:  "unexpected continuation",
			state: 0,
			wire:  MustCompileFrame(NewFrame(OpContinuation, true, nil)),
			err:   ErrProtocolContinuationUnexpected,
		},
		{
			name:  "length msb set",
			state: 0,
			wire:  []byte{0x82, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			err:   ErrHeaderLengthMSB,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := NewStreamDecoder(test.state, nil)
			_, st := d.Feed(test.wire)
			if st != DecodeError {
				t.Fatalf("Feed() status = %v, want DecodeError", st)
			}
			if d.Err() != test.err {
				t.Fatalf("Err() = %v, want %v", d.Err(), test.err)
			}
			// The error is latched.
			if n, st := d.Feed(test.wire); n != 0 || st != DecodeError {
				t.Errorf("after error Feed() = (%d, %v), want (0, DecodeError)", n, st)
			}
			if err := d.Finish(); err != test.err {
				t.Errorf("Finish() = %v, want %v", err, test.err)
			}
			d.Reset()
			if err := d.Finish(); err != nil {
				t.Errorf("after Reset() Finish() = %v, want nil", err)
			}
		})
	}
}

func TestStreamDecoderFragmentation(t *testing.T) {
	wire := bytes.Join([][]byte{
		MustCompileFrame(NewFrame(OpText, false, []byte("foo"))),
		MustCompileFrame(NewFrame(OpText, false, []byte("bar"))),
	}, nil)
	d := NewStreamDecoder(0, nil)
	var st DecodeStatus
	for len(wire) > 0 {
		var n int
		n, st = d.Feed(wire)
		if st == DecodeError {
			break
		}
		wire = wire[n:]
	}
	if st != DecodeError {
		t.Fatalf("Feed() status = %v, want DecodeError", st)
	}
	if d.Err() != ErrProtocolContinuationExpected {
		t.Errorf("Err() = %v, want %v", d.Err(), ErrProtocolContinuationExpected)
	}
}

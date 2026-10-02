package ws

import (
	"bytes"
	"io"
	"testing"
)

// decodeFrames drives dec over the given chunks and collects completed
// frames with their reassembled payloads.
func decodeFrames(t *testing.T, dec *Decoder, chunks ...[]byte) []Frame {
	t.Helper()

	var (
		frames  []Frame
		payload []byte
	)
	dec.OnPayload = func(p []byte) {
		payload = append(payload, p...)
	}
	for _, c := range chunks {
		for len(c) > 0 {
			n, status, err := dec.Feed(c)
			if err != nil {
				t.Fatalf("Feed(%d bytes) error: %v", len(c), err)
			}
			if status == DecodeNeedMore && n != len(c) {
				t.Fatalf("Feed returned %v but consumed %d of %d bytes", status, n, len(c))
			}
			c = c[n:]
			if status == DecodeFrame {
				frames = append(frames, Frame{
					Header:  dec.Header(),
					Payload: payload,
				})
				payload = nil
			}
		}
	}
	return frames
}

// readFramesReference decodes wire with the blocking ReadFrame as a
// reference implementation.
func readFramesReference(t *testing.T, wire []byte) []Frame {
	t.Helper()

	var frames []Frame
	r := bytes.NewReader(wire)
	for r.Len() > 0 {
		f, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("reference ReadFrame error: %v", err)
		}
		if f.Header.Masked {
			f = UnmaskFrameInPlace(f)
		}
		frames = append(frames, f)
	}
	return frames
}

func assertFramesEqual(t *testing.T, act, exp []Frame) {
	t.Helper()

	if len(act) != len(exp) {
		t.Fatalf("decoded %d frames, want %d", len(act), len(exp))
	}
	for i := range exp {
		ah, eh := act[i].Header, exp[i].Header
		if ah.Fin != eh.Fin || ah.Rsv != eh.Rsv || ah.OpCode != eh.OpCode || ah.Length != eh.Length {
			t.Errorf("frame %d header = {fin:%v rsv:%v op:%v len:%d}, want {fin:%v rsv:%v op:%v len:%d}",
				i, ah.Fin, ah.Rsv, ah.OpCode, ah.Length,
				eh.Fin, eh.Rsv, eh.OpCode, eh.Length)
		}
		if !bytes.Equal(act[i].Payload, exp[i].Payload) {
			t.Errorf("frame %d payload = %x, want %x", i, act[i].Payload, exp[i].Payload)
		}
	}
}

// testWireSample builds a wire sample that exercises short and extended
// payload lengths, fragmentation, control frames and zero-length payloads.
func testWireSample() []byte {
	masks := [][4]byte{
		{0x01, 0x02, 0x03, 0x04},
		{0xaa, 0xbb, 0xcc, 0xdd},
		{0x00, 0xff, 0x00, 0xff},
		{0xde, 0xad, 0xbe, 0xef},
	}
	masked := func(i int, f Frame) Frame {
		return MaskFrameInPlaceWith(f, masks[i%len(masks)])
	}

	bin300 := make([]byte, 300) // forces 16-bit extended length
	for i := range bin300 {
		bin300[i] = byte(i)
	}

	frames := []Frame{
		masked(0, NewTextFrame([]byte("hello"))),
		masked(1, NewFrame(OpText, false, []byte("frag-"))),
		masked(2, NewFrame(OpContinuation, true, []byte("ment"))),
		masked(3, NewPingFrame([]byte("pingdata!"))),
		masked(0, NewBinaryFrame(bin300)),
		masked(1, NewTextFrame(nil)), // zero-length payload
		masked(2, NewCloseFrame(NewCloseFrameBody(StatusNormalClosure, "bye"))),
	}

	var wire []byte
	for _, f := range frames {
		wire = append(wire, MustCompileFrame(f)...)
	}
	return wire
}

func TestDecoderAllSplitPoints(t *testing.T) {
	wire := testWireSample()
	exp := readFramesReference(t, wire)

	for split := 0; split <= len(wire); split++ {
		dec := NewDecoder(StateServerSide)
		act := decodeFrames(t, dec, wire[:split], wire[split:])
		if err := dec.Finish(); err != nil {
			t.Fatalf("split %d: Finish() = %v, want nil", split, err)
		}
		assertFramesEqual(t, act, exp)
	}
}

func TestDecoderByteByByte(t *testing.T) {
	wire := testWireSample()
	exp := readFramesReference(t, wire)

	dec := NewDecoder(StateServerSide)
	chunks := make([][]byte, len(wire))
	for i := range wire {
		chunks[i] = wire[i : i+1]
	}
	act := decodeFrames(t, dec, chunks...)
	if err := dec.Finish(); err != nil {
		t.Fatalf("Finish() = %v, want nil", err)
	}
	assertFramesEqual(t, act, exp)
}

func TestDecoderMultiFrameSingleInput(t *testing.T) {
	wire := testWireSample()
	exp := readFramesReference(t, wire)

	// The whole sample in a single input chunk: Feed must stop at every
	// frame boundary and never swallow bytes of the next frame.
	dec := NewDecoder(StateServerSide)
	act := decodeFrames(t, dec, wire)
	if err := dec.Finish(); err != nil {
		t.Fatalf("Finish() = %v, want nil", err)
	}
	assertFramesEqual(t, act, exp)

	// A single Feed call must not consume past the first frame boundary.
	dec = NewDecoder(StateServerSide)
	dec.OnPayload = func([]byte) {}
	n, status, err := dec.Feed(wire)
	if err != nil {
		t.Fatalf("Feed() error: %v", err)
	}
	if status != DecodeFrame {
		t.Fatalf("Feed() status = %v, want %v", status, DecodeFrame)
	}
	if n <= 0 || n >= len(wire) {
		t.Fatalf("Feed() consumed %d of %d bytes, want a strict prefix", n, len(wire))
	}
}

func TestDecoderFinish(t *testing.T) {
	wire := testWireSample()

	// Complete boundary.
	dec := NewDecoder(StateServerSide)
	decodeFrames(t, dec, wire)
	if err := dec.Finish(); err != nil {
		t.Fatalf("Finish() = %v, want nil", err)
	}

	// Truncated payload.
	dec = NewDecoder(StateServerSide)
	decodeFrames(t, dec, wire[:len(wire)-1])
	if err := dec.Finish(); err != io.ErrUnexpectedEOF {
		t.Fatalf("Finish() = %v, want %v", err, io.ErrUnexpectedEOF)
	}

	// Truncated header.
	dec = NewDecoder(StateServerSide)
	decodeFrames(t, dec, wire[:1])
	if err := dec.Finish(); err != io.ErrUnexpectedEOF {
		t.Fatalf("Finish() = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestDecoderProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		state State
		wire  []byte
		err   error
	}{
		{
			name:  "unmasked client frame",
			state: StateServerSide,
			wire:  MustCompileFrame(NewTextFrame([]byte("hi"))),
			err:   ErrProtocolMaskRequired,
		},
		{
			name:  "masked server frame",
			state: StateClientSide,
			wire:  MustCompileFrame(MaskFrameInPlace(NewTextFrame([]byte("hi")))),
			err:   ErrProtocolMaskUnexpected,
		},
		{
			name:  "reserved opcode",
			state: StateServerSide,
			wire: MustCompileFrame(MaskFrameInPlaceWith(
				NewFrame(OpCode(0x3), true, nil), [4]byte{1, 2, 3, 4},
			)),
			err: ErrProtocolOpCodeReserved,
		},
		{
			name:  "non-final control frame",
			state: StateServerSide,
			wire: MustCompileFrame(MaskFrameInPlaceWith(
				NewFrame(OpPing, false, nil), [4]byte{1, 2, 3, 4},
			)),
			err: ErrProtocolControlNotFinal,
		},
		{
			name:  "length msb set",
			state: 0,
			wire:  []byte{0x82, 0x7f, 0x80, 0, 0, 0, 0, 0, 0, 0},
			err:   ErrHeaderLengthMSB,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Exercise error reporting at every split point as well.
			for split := 0; split <= len(test.wire); split++ {
				dec := NewDecoder(test.state)
				dec.OnPayload = func([]byte) {}
				_, status, err := dec.Feed(test.wire[:split])
				if err == nil {
					if status != DecodeNeedMore {
						t.Fatalf("split %d: Feed(prefix) = %v, nil; want %v, nil", split, status, DecodeNeedMore)
					}
					_, status, err = dec.Feed(test.wire[split:])
				}
				if status != DecodeError || err != test.err {
					t.Fatalf("split %d: Feed() = %v, %v; want %v, %v", split, status, err, DecodeError, test.err)
				}
				// The error is sticky.
				if _, status, err := dec.Feed(test.wire); status != DecodeError || err != test.err {
					t.Fatalf("split %d: second Feed() = %v, %v; want %v, %v", split, status, err, DecodeError, test.err)
				}
				if err := dec.Finish(); err != test.err {
					t.Fatalf("split %d: Finish() = %v, want %v", split, err, test.err)
				}
			}
		})
	}
}

func TestDecoderLargeFrame(t *testing.T) {
	payload := make([]byte, 1<<20) // forces 64-bit extended length
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	// MaskFrameInPlaceWith ciphers payload in place, so keep a copy of the
	// original bytes as the expectation.
	want := append([]byte(nil), payload...)
	wire := MustCompileFrame(MaskFrameInPlaceWith(NewBinaryFrame(payload), mask))

	// Split at every header byte boundary, then feed the payload in odd
	// sized chunks so that the mask offset crosses chunk boundaries.
	header := wire[:MaxHeaderSize]
	for split := 0; split <= len(header); split++ {
		dec := NewDecoder(StateServerSide)
		var got []byte
		maxChunk := 0
		dec.OnPayload = func(p []byte) {
			got = append(got, p...)
			if len(p) > maxChunk {
				maxChunk = len(p)
			}
		}
		chunks := [][]byte{header[:split], header[split:]}
		rest := wire[MaxHeaderSize:]
		for len(rest) > 0 {
			n := 8191 // odd on purpose: not a multiple of the mask period
			if n > len(rest) {
				n = len(rest)
			}
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
		}
		for _, c := range chunks {
			for len(c) > 0 {
				n, _, err := dec.Feed(c)
				if err != nil {
					t.Fatalf("split %d: Feed() error: %v", split, err)
				}
				c = c[n:]
			}
		}
		if err := dec.Finish(); err != nil {
			t.Fatalf("split %d: Finish() = %v, want nil", split, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("split %d: payload mismatch", split)
		}
		if maxChunk > decoderScratchSize {
			t.Fatalf("split %d: callback chunk of %d bytes exceeds scratch size %d",
				split, maxChunk, decoderScratchSize)
		}
	}
}

func TestDecoderFeedAllocs(t *testing.T) {
	payload := make([]byte, 1<<20)
	wire := MustCompileFrame(MaskFrameInPlaceWith(NewBinaryFrame(payload), [4]byte{1, 2, 3, 4}))

	dec := NewDecoder(StateServerSide)
	var sink int
	dec.OnPayload = func(p []byte) { sink += len(p) }

	// Warm up: parse the header and allocate the scratch buffer.
	chunk := wire[:decoderScratchSize]
	for !dec.inPayload {
		n, _, err := dec.Feed(chunk)
		if err != nil {
			t.Fatalf("Feed() error: %v", err)
		}
		chunk = chunk[n:]
	}
	if _, _, err := dec.Feed(chunk); err != nil {
		t.Fatalf("Feed() error: %v", err)
	}

	// Steady state: streaming payload chunks must not allocate, no matter
	// how large the frame is.
	allocs := testing.AllocsPerRun(100, func() {
		if _, _, err := dec.Feed(chunk); err != nil {
			t.Fatalf("Feed() error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Feed() allocated %v times per run, want 0", allocs)
	}
}

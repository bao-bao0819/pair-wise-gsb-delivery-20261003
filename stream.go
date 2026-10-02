package ws

import (
	"encoding/binary"
	"io"
)

// DecodeStatus represents the outcome of a single StreamDecoder.Feed call.
type DecodeStatus int

const (
	// DecodeNeedMore means that the decoder consumed all given bytes and
	// needs more data to complete the current frame.
	DecodeNeedMore DecodeStatus = iota
	// DecodeFrame means that a complete frame was decoded. The number of
	// consumed bytes may be less than the length of the given input; the
	// caller should feed the remaining bytes again to decode the next
	// frame. The decoded header is available via Header().
	DecodeFrame
	// DecodeError means that a header parsing or protocol error occurred.
	// The decoder stays in the failed state and Err() returns the cause.
	DecodeError
)

// streamScratchSize bounds the internal buffer used to unmask payload
// chunks. It keeps memory usage constant regardless of the frame size.
const streamScratchSize = 32 << 10

// PayloadHandler receives payload chunks of the frame being decoded.
//
// The chunk slice is valid only during the call: it either aliases the
// buffer passed to Feed or an internal scratch buffer that is reused by
// subsequent Feed calls. Copy the bytes to retain them.
type PayloadHandler func(header Header, chunk []byte)

// StreamDecoder is an incremental websocket frame decoder. Unlike ReadFrame
// it does not perform any blocking I/O: the caller feeds byte fragments as
// they arrive from the transport and gets back the number of consumed bytes
// along with a DecodeStatus.
//
// Both header and payload may be split at any byte boundary. For masked
// frames the cipher offset is tracked across fragments, so chunks are
// unmasked correctly no matter where the splits happen.
//
// Payload is reported in chunks via PayloadHandler, thus the decoder never
// allocates a buffer proportional to the frame length.
type StreamDecoder struct {
	state State
	cb    PayloadHandler

	header Header
	frag   State // StateFragmented or 0

	hbuf  [MaxHeaderSize]byte
	held  int  // number of header bytes buffered in hbuf
	want  int  // total header size; zero until first two bytes are read
	ready bool // header fully parsed, payload expected
	left  int64

	offset  int // number of payload bytes consumed; mask cipher offset
	scratch []byte
	err     error
}

// NewStreamDecoder returns a StreamDecoder that checks headers against the
// given state (use zero state to disable endpoint-specific checks) and
// reports payload chunks to cb. The cb may be nil, in which case payload
// bytes are decoded and discarded.
func NewStreamDecoder(state State, cb PayloadHandler) *StreamDecoder {
	return &StreamDecoder{state: state, cb: cb}
}

// Feed consumes a fragment of the byte stream and reports the decoding
// progress. It returns the number of consumed bytes and the status:
//
//   - DecodeNeedMore: all bytes were consumed, more data is needed;
//   - DecodeFrame: a frame is complete; remaining bytes (if any) belong to
//     the next frame and must be fed again;
//   - DecodeError: the stream is broken; see Err().
func (d *StreamDecoder) Feed(p []byte) (int, DecodeStatus) {
	if d.err != nil {
		return 0, DecodeError
	}
	n := 0
	if !d.ready {
		k, done, st := d.readHeader(p)
		n += k
		if !done {
			return n, st
		}
		p = p[k:]
	}
	if d.left > 0 {
		n += d.readPayload(p)
	}
	if d.left == 0 {
		d.ready = false
		d.held, d.want, d.offset = 0, 0, 0
		return n, DecodeFrame
	}
	return n, DecodeNeedMore
}

// Header returns the header of the most recently completed frame. It is
// meaningful only after Feed has returned DecodeFrame.
func (d *StreamDecoder) Header() Header {
	return d.header
}

// Err returns the error that put the decoder into the failed state, if any.
func (d *StreamDecoder) Err() error {
	return d.err
}

// Finish reports whether the stream ends at a clean frame boundary. It
// returns io.ErrUnexpectedEOF if the input was truncated in the middle of a
// header or a payload, and the decoding error itself if the decoder is in
// the failed state.
func (d *StreamDecoder) Finish() error {
	switch {
	case d.err != nil:
		return d.err
	case d.ready || d.held != 0:
		return io.ErrUnexpectedEOF
	default:
		return nil
	}
}

// Reset resets the decoder to its initial state, keeping the state flags
// and the payload handler given to NewStreamDecoder.
func (d *StreamDecoder) Reset() {
	d.header = Header{}
	d.frag = 0
	d.held, d.want, d.offset = 0, 0, 0
	d.ready = false
	d.left = 0
	d.err = nil
}

// readHeader accumulates header bytes and parses the header once complete.
// It reports done=true when the header has been parsed successfully.
func (d *StreamDecoder) readHeader(p []byte) (n int, done bool, st DecodeStatus) {
	for {
		want := d.want
		if want == 0 {
			want = MinHeaderSize
		}
		need := want - d.held
		if len(p) < need {
			copy(d.hbuf[d.held:], p)
			d.held += len(p)
			return n + len(p), false, DecodeNeedMore
		}
		copy(d.hbuf[d.held:], p[:need])
		d.held += need
		n += need
		p = p[need:]

		if d.want == 0 {
			d.want = headerSize(d.hbuf[:MinHeaderSize])
			if d.held < d.want {
				continue
			}
		}
		if err := d.parseHeader(); err != nil {
			d.err = err
			return n, false, DecodeError
		}
		return n, true, DecodeNeedMore
	}
}

// parseHeader parses the buffered header bytes and checks them against the
// decoder state.
func (d *StreamDecoder) parseHeader() error {
	bts := d.hbuf[:d.want]

	h := Header{
		Fin:    bts[0]&bit0 != 0,
		Rsv:    (bts[0] & 0x70) >> 4,
		OpCode: OpCode(bts[0] & 0x0f),
	}
	length := bts[1] & 0x7f
	bts = bts[MinHeaderSize:]

	switch length {
	case 126:
		h.Length = int64(binary.BigEndian.Uint16(bts[:2]))
		bts = bts[2:]
	case 127:
		if bts[0]&bit0 != 0 {
			return ErrHeaderLengthMSB
		}
		h.Length = int64(binary.BigEndian.Uint64(bts[:8]))
		bts = bts[8:]
	default:
		h.Length = int64(length)
	}

	if d.hbuf[1]&bit0 != 0 {
		h.Masked = true
		copy(h.Mask[:], bts)
	}

	if err := CheckHeader(h, d.state|d.frag); err != nil {
		return err
	}

	// Track fragmentation state to check continuation frames of the
	// subsequent headers.
	if h.OpCode.IsData() {
		switch {
		case h.OpCode == OpContinuation && h.Fin:
			d.frag = 0
		case h.OpCode != OpContinuation && !h.Fin:
			d.frag = StateFragmented
		}
	}

	d.header = h
	d.left = h.Length
	d.ready = true
	return nil
}

// readPayload consumes payload bytes from p, reporting unmasked chunks to
// the payload handler. It returns the number of bytes consumed from p.
func (d *StreamDecoder) readPayload(p []byte) (n int) {
	for d.left > 0 && len(p) > 0 {
		m := len(p)
		if left := d.left; int64(m) > left {
			m = int(left)
		}

		chunk := p[:m]
		if d.header.Masked {
			if m > len(d.scratch) {
				if d.scratch == nil {
					d.scratch = make([]byte, streamScratchSize)
				}
				m = min(m, len(d.scratch))
			}
			// Unmask a copy to leave the caller's buffer untouched.
			copy(d.scratch, p[:m])
			Cipher(d.scratch[:m], d.header.Mask, d.offset)
			chunk = d.scratch[:m]
		}

		if d.cb != nil {
			d.cb(d.header, chunk)
		}

		d.offset += m
		d.left -= int64(m)
		n += m
		p = p[m:]
	}
	return n
}

// headerSize returns the total size of a frame header given its first two
// bytes.
func headerSize(bts []byte) int {
	n := MinHeaderSize
	if bts[1]&bit0 != 0 {
		n += 4
	}
	switch bts[1] & 0x7f {
	case 126:
		n += 2
	case 127:
		n += 8
	}
	return n
}

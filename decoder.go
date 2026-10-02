package ws

import (
	"encoding/binary"
	"io"
)

// DecodeStatus describes the result of a single Decoder.Feed call.
type DecodeStatus int

const (
	// DecodeNeedMore means that the decoder consumed the whole input chunk
	// but the current frame is not complete yet. The caller should feed
	// more bytes.
	DecodeNeedMore DecodeStatus = iota
	// DecodeFrame means that a frame has been completed. The consumed
	// length returned by Feed may be less than the input chunk length if
	// bytes of the next frame are already present; the caller should feed
	// the remaining bytes again. The completed frame header is available
	// via Decoder.Header.
	DecodeFrame
	// DecodeError means that a protocol or header parsing error occurred.
	// The decoder is broken and must not be used anymore.
	DecodeError
)

// String implements fmt.Stringer.
func (s DecodeStatus) String() string {
	switch s {
	case DecodeNeedMore:
		return "need more data"
	case DecodeFrame:
		return "frame complete"
	case DecodeError:
		return "decode error"
	default:
		return "unknown status"
	}
}

// decoderScratchSize bounds the amount of memory the Decoder allocates for
// unmasking payload chunks, regardless of the frame payload length.
const decoderScratchSize = 4096

// Decoder is an incremental, non-blocking websocket frame decoder.
//
// Unlike ReadFrame it does not depend on a blocking io.Reader. Instead the
// caller repeatedly feeds byte chunks as they arrive from the transport and
// inspects the returned DecodeStatus. Both the header and the payload may be
// split at any byte boundary across chunks. The client mask offset is
// continued across chunk boundaries, and a single input chunk containing
// multiple frames is never over-consumed: Feed stops right after a completed
// frame and reports the number of consumed bytes.
//
// The payload is streamed to the OnPayload callback in pieces, so the
// Decoder never allocates a buffer proportional to the frame length.
type Decoder struct {
	// OnPayload is called zero or more times per frame with the next piece
	// of the (unmasked) payload.
	//
	// The slice passed to the callback is valid only during the call: for
	// masked frames it aliases an internal scratch buffer that is reused by
	// subsequent Feed calls, and for unmasked frames it aliases the caller
	// input chunk. Copy the data if it must outlive the callback.
	OnPayload func(p []byte)

	state State

	// err is sticky: once set, every Feed reports DecodeError.
	err error

	// Header assembly state.
	hdr   [MaxHeaderSize]byte
	hlen  int // number of header bytes buffered so far
	hneed int // expected full header size, 0 until first 2 bytes are known

	// Payload streaming state.
	inPayload bool
	header    Header
	remain    int64 // payload bytes remaining for the current frame
	maskOff   int   // number of payload bytes already unmasked (cipher offset)

	scratch []byte // lazily allocated unmasking buffer, capped in size
}

// NewDecoder returns a Decoder that checks frames against the given State
// (e.g. StateServerSide to enforce client masking). The fragmentation part
// of the state is tracked by the Decoder itself across frames.
func NewDecoder(s State) *Decoder {
	return &Decoder{state: s.Clear(StateFragmented)}
}

// Header returns the header of the most recently completed frame. It is
// valid only after Feed has returned DecodeFrame and until the next frame
// completes.
func (d *Decoder) Header() Header {
	return d.header
}

// Feed consumes as much of p as needed to advance the decoding of the
// current frame and reports the number of consumed bytes along with the
// resulting status.
//
// When DecodeFrame is returned the caller should check n against len(p):
// if n < len(p), the rest of p belongs to subsequent frames and must be fed
// again.
func (d *Decoder) Feed(p []byte) (n int, status DecodeStatus, err error) {
	if d.err != nil {
		return 0, DecodeError, d.err
	}
	if !d.inPayload {
		m, s, err := d.feedHeader(p)
		n += m
		if err != nil || s == DecodeNeedMore {
			return n, s, err
		}
		if !d.inPayload {
			// Zero-length payload frame completed on the header alone.
			return n, DecodeFrame, nil
		}
	}
	m, s := d.feedPayload(p[n:])
	n += m
	return n, s, nil
}

// feedHeader assembles the frame header byte by byte across chunk
// boundaries. It returns DecodeFrame once a full header has been parsed and
// checked; if the header announces a non-empty payload the decoder switches
// to the payload phase and the caller must continue with feedPayload.
func (d *Decoder) feedHeader(p []byte) (n int, status DecodeStatus, err error) {
	for {
		want := MinHeaderSize
		if d.hneed != 0 {
			want = d.hneed
		}
		if d.hlen < want {
			take := min(want-d.hlen, len(p)-n)
			copy(d.hdr[d.hlen:], p[n:n+take])
			d.hlen += take
			n += take
			if d.hlen < want {
				return n, DecodeNeedMore, nil
			}
		}
		if d.hneed == 0 {
			// The first two bytes are enough to learn the full
			// header size.
			d.hneed = MinHeaderSize + headerExtraSize(d.hdr[1])
			continue
		}
		break
	}

	h, err := parseHeader(d.hdr[:d.hneed])
	if err == nil {
		err = CheckHeader(h, d.state)
	}
	if err != nil {
		d.err = err
		return n, DecodeError, err
	}

	d.header = h
	d.hlen = 0
	d.hneed = 0
	d.remain = h.Length
	d.maskOff = 0

	if d.remain > 0 {
		d.inPayload = true
		return n, DecodeFrame, nil
	}
	d.completeFrame()
	return n, DecodeFrame, nil
}

// feedPayload streams the frame payload to OnPayload in bounded-size
// pieces. It returns DecodeFrame when the last payload byte of the current
// frame has been consumed.
func (d *Decoder) feedPayload(p []byte) (n int, status DecodeStatus) {
	for d.remain > 0 {
		if len(p) == n {
			return n, DecodeNeedMore
		}
		take := int64(len(p) - n)
		if take > d.remain {
			take = d.remain
		}
		chunk := p[n : n+int(take)]
		if d.header.Masked {
			if d.scratch == nil {
				d.scratch = make([]byte, decoderScratchSize)
			}
			if take > int64(len(d.scratch)) {
				take = int64(len(d.scratch))
				chunk = chunk[:take]
			}
			copy(d.scratch, chunk)
			chunk = d.scratch[:take]
			// The offset continues across chunks so that the mask
			// stays aligned no matter where the input was split.
			Cipher(chunk, d.header.Mask, d.maskOff)
		}
		if d.OnPayload != nil {
			d.OnPayload(chunk)
		}
		d.maskOff += int(take)
		d.remain -= take
		n += int(take)
	}
	d.completeFrame()
	return n, DecodeFrame
}

// completeFrame resets the decoder for the next frame and advances the
// fragmentation part of the protocol state.
func (d *Decoder) completeFrame() {
	d.inPayload = false
	switch op := d.header.OpCode; {
	case op.IsControl():
		// Control frames do not affect fragmentation state.
	case op == OpContinuation:
		if d.header.Fin {
			d.state = d.state.Clear(StateFragmented)
		}
	case !d.header.Fin:
		d.state = d.state.Set(StateFragmented)
	}
}

// Finish reports whether the input stream ended on a frame boundary. It
// returns nil if every fed byte belongs to some fully decoded frame, and
// io.ErrUnexpectedEOF if the stream was truncated in the middle of a header
// or a payload.
func (d *Decoder) Finish() error {
	if d.err != nil {
		return d.err
	}
	if d.hlen != 0 || d.inPayload {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// headerExtraSize returns the number of additional header bytes announced by
// the second header byte: the extended payload length and the masking key.
func headerExtraSize(b byte) (extra int) {
	if b&bit0 != 0 {
		extra += 4
	}
	switch b & 0x7f {
	case 126:
		extra += 2
	case 127:
		extra += 8
	}
	return extra
}

// parseHeader parses a complete frame header from bts, which must be exactly
// MinHeaderSize + headerExtraSize(bts[1]) bytes long.
func parseHeader(bts []byte) (h Header, err error) {
	h.Fin = bts[0]&bit0 != 0
	h.Rsv = (bts[0] & 0x70) >> 4
	h.OpCode = OpCode(bts[0] & 0x0f)

	if bts[1]&bit0 != 0 {
		h.Masked = true
	}

	length := bts[1] & 0x7f
	bts = bts[2:]
	switch {
	case length < 126:
		h.Length = int64(length)

	case length == 126:
		h.Length = int64(binary.BigEndian.Uint16(bts[:2]))
		bts = bts[2:]

	case length == 127:
		if bts[0]&0x80 != 0 {
			return h, ErrHeaderLengthMSB
		}
		h.Length = int64(binary.BigEndian.Uint64(bts[:8]))
		bts = bts[8:]

	default:
		return h, ErrHeaderLengthUnexpected
	}

	if h.Masked {
		copy(h.Mask[:], bts)
	}

	return h, nil
}

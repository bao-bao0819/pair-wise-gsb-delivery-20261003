package mbox

import (
	"bytes"
	"errors"
	"io"
	"time"
)

// ErrMessageClosed is returned by Write and Close when a message has already
// ended: it was explicitly closed, another message was started, the stream
// was closed, or an earlier write to the underlying writer failed.
var ErrMessageClosed = errors.New("mbox: message already closed")

type messageWriter struct {
	parent *Writer
	w      io.Writer
	buf    bytes.Buffer

	closed bool
	err    error
}

// fail records the first error hit while talking to the underlying writer.
// The error is sticky: it ends the message and is reported by every later
// Write and Close, so no separator can ever be appended after a partially
// failed message.
func (mw *messageWriter) fail(err error) {
	if mw.err == nil {
		mw.err = err
	}
	mw.closed = true
	if mw.parent != nil {
		if mw.parent.err == nil {
			mw.parent.err = mw.err
		}
		if mw.parent.last == mw {
			mw.parent.last = nil
		}
	}
}

// writeAll writes p to the underlying writer, retrying transparent short
// writes. It only returns once every byte has been written or the first real
// error occurs.
func (mw *messageWriter) writeAll(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n, err := mw.w.Write(p)
		if n > 0 {
			written += n
			p = p[n:]
		}
		if err != nil {
			mw.fail(err)
			return written, err
		}
		if n <= 0 {
			mw.fail(io.ErrShortWrite)
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

// writeLine emits one complete (or trailing partial) line, escaping a leading
// "From " and collapsing CRLF to LF. It reports how many caller input bytes
// are represented by whatever the underlying writer accepted, so the return
// value of Write keeps counting the caller's bytes on a short write.
func (mw *messageWriter) writeLine(l []byte, crlf bool) (int, error) {
	var out bytes.Buffer
	escape := bytes.HasPrefix(l, header)
	if escape {
		out.WriteByte('>')
	}
	if crlf {
		out.Write(l[:len(l)-2])
		out.WriteByte('\n')
	} else {
		out.Write(l)
	}

	produced, err := mw.writeAll(out.Bytes())

	// Map physically written output bytes back to input bytes. The leading
	// escape byte represents no input; the collapsed CR of a fully written
	// CRLF line is an input byte that produces no output.
	in := produced
	if escape {
		in--
	}
	if in < 0 {
		in = 0
	}
	if crlf {
		want := out.Len()
		if escape {
			want--
		}
		if in > want {
			in = want
		}
		if err == nil && produced == out.Len() {
			in = len(l)
		}
	}
	if in > len(l) {
		in = len(l)
	}
	return in, err
}

func (mw *messageWriter) Write(p []byte) (int, error) {
	if mw.closed {
		if mw.err != nil {
			return 0, mw.err
		}
		return 0, ErrMessageClosed
	}

	// We return the number of bytes *from p* that were accepted. Since we
	// also scan bytes already buffered before this write, remember the
	// initial buffer length and subtract it at the end.
	initBufLen := mw.buf.Len()

	mw.buf.Write(p)
	b := mw.buf.Bytes()

	accepted := 0
	var err error
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}

		l := b[:i+1]
		b = b[i+1:]

		crlf := len(l) > 1 && l[len(l)-2] == '\r' // Replace CRLF with LF.
		in, werr := mw.writeLine(l, crlf)
		accepted += in
		if werr != nil {
			err = werr
			break
		}
	}

	// Keep the trailing partial line buffered for the next Write/Close. On
	// failure stop retaining data: the handle is dead anyway.
	mw.buf.Reset()
	if err == nil {
		mw.buf.Write(b)
	}

	n := accepted + mw.buf.Len() - initBufLen
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
	}
	return n, err
}

// Close ends the message: it flushes any trailing partial line and appends
// the blank-line separator. After Close, starting another message, closing
// the stream, or a write failure, the handle is unusable and further writes
// are rejected. Closing an already ended message reports the sticky error if
// there was one, or ErrMessageClosed otherwise.
func (mw *messageWriter) Close() error {
	if mw.closed {
		if mw.err != nil {
			return mw.err
		}
		return ErrMessageClosed
	}
	mw.closed = true

	if b := mw.buf.Bytes(); len(b) > 0 {
		if _, err := mw.writeLine(b, false); err != nil {
			return err
		}
	}
	mw.buf.Reset()

	if _, err := mw.writeAll([]byte("\n\n")); err != nil {
		return err
	}

	if mw.parent != nil && mw.parent.last == mw {
		mw.parent.last = nil
	}
	return nil
}

// Writer writes messages to a mbox stream. The Close method must be called to
// end the stream.
type Writer struct {
	w      io.Writer
	last   *messageWriter
	closed bool
	err    error
}

// NewWriter creates a new Writer that writes messages to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// CreateMessage appends a message to the mbox stream. The message text
// (including both the header and the body) should be written to the returned
// writer. The returned io.WriteCloser must be closed to end the message; it
// is also closed automatically when the next message is created or the
// stream itself is closed. Once a message has ended, its writer rejects any
// further writes.
func (w *Writer) CreateMessage(from string, t time.Time) (io.WriteCloser, error) {
	if w.closed {
		return nil, errors.New("mbox: Writer.CreateMessage called after Close")
	}
	if w.err != nil {
		return nil, w.err
	}
	if w.last != nil {
		if err := w.last.Close(); err != nil {
			return nil, err
		}
		w.last = nil
	}

	if from == "" {
		from = "???@???"
	}

	if t.IsZero() {
		t = time.Now()
	}
	date := t.UTC().Format(time.ANSIC)

	line := "From " + from + " " + date + "\n"
	if _, err := io.WriteString(w.w, line); err != nil {
		w.err = err
		return nil, err
	}

	mw := &messageWriter{parent: w, w: w.w}
	w.last = mw
	return mw, nil
}

func (w *Writer) Close() error {
	if w.closed {
		return errors.New("mbox: Writer already closed")
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	if w.last != nil {
		return w.last.Close()
	}
	return nil
}

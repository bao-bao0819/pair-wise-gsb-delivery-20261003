package mbox

import (
	"bytes"
	"errors"
	"io"
	"time"
)

// ErrMessageClosed is returned by Write calls on a message handle after the
// message has ended. A message ends when it has been closed explicitly, when
// the next message is started with CreateMessage, or when the archive Writer
// is closed.
var ErrMessageClosed = errors.New("mbox: message has already been closed")

type messageWriter struct {
	parent *Writer
	buf    bytes.Buffer
	ended  bool
}

// encodeLine turns a single line (including its trailing line feed) into the
// representation stored in the archive: a leading "From " is escaped and
// CRLF is normalized to LF.
func encodeLine(line []byte) []byte {
	escape := bytes.HasPrefix(line, header)
	crlf := len(line) >= 2 && line[len(line)-2] == '\r'

	out := make([]byte, 0, len(line)+1)
	if escape {
		out = append(out, '>')
	}
	if crlf {
		out = append(out, line[:len(line)-2]...)
		out = append(out, '\n')
	} else {
		out = append(out, line...)
	}
	return out
}

// write sends p to the underlying writer and records the first failure as a
// sticky error. A short write without an error is reported as
// io.ErrShortWrite.
func (w *Writer) write(p []byte) error {
	if w.err != nil {
		return w.err
	}

	n, err := w.w.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return err
}

func (mw *messageWriter) Write(p []byte) (int, error) {
	if mw.parent.err != nil {
		return 0, mw.parent.err
	}
	if mw.ended || mw.parent.closed || mw.parent.last != mw {
		return 0, ErrMessageClosed
	}

	// Bytes carried over from previous Write calls are not part of p, so the
	// returned count is derived from the caller's input rather than from the
	// transformed bytes emitted to the underlying writer.
	prevLen := mw.buf.Len()
	mw.buf.Write(p)
	b := mw.buf.Bytes()
	mw.buf.Reset()

	consumed := 0
	for {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			mw.buf.Write(b)
			consumed += len(b)
			break
		}

		line := b[:i+1]
		b = b[i+1:]

		if err := mw.parent.write(encodeLine(line)); err != nil {
			break
		}
		consumed += i + 1
	}

	n := consumed - prevLen
	if n < 0 {
		n = 0
	}
	if n > len(p) {
		n = len(p)
	}
	if mw.parent.err != nil {
		return n, mw.parent.err
	}
	return n, nil
}

// Close ends the current message: the pending partial line is flushed and the
// message separator is written. It is safe to call more than once; closing a
// message that has already ended (including one ended automatically by the
// next CreateMessage or by Writer.Close) is a no-op.
func (mw *messageWriter) Close() error {
	if mw.parent.err != nil {
		return mw.parent.err
	}
	if mw.ended {
		return nil
	}

	mw.ended = true
	if mw.parent.last == mw {
		mw.parent.last = nil
	}

	b := mw.buf.Bytes()
	mw.buf.Reset()
	if err := mw.parent.write(encodeLine(b)); err != nil {
		return err
	}
	if err := mw.parent.write([]byte("\n\n")); err != nil {
		return err
	}
	return nil
}

// Writer writes messages to a mbox stream. The Close method must be called to
// end the stream.
type Writer struct {
	w      io.Writer
	last   *messageWriter
	closed bool

	// err is sticky: once the underlying writer fails, all further writes
	// and closes return this error.
	err error
}

// NewWriter creates a new Writer that writes messages to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// CreateMessage appends a message to the mbox stream. The message text
// (including both the header and the body) should be written to the returned
// io.WriteCloser.
//
// Calling Close on the returned io.WriteCloser ends the current message
// explicitly; this is optional, as CreateMessage and Writer.Close end the
// previous message automatically. After a message has ended its handle
// rejects further writes with ErrMessageClosed.
func (w *Writer) CreateMessage(from string, t time.Time) (io.WriteCloser, error) {
	if w.err != nil {
		return nil, w.err
	}
	if w.closed {
		return nil, errors.New("mbox: Writer.CreateMessage called after Close")
	}
	if w.last != nil {
		if err := w.last.Close(); err != nil {
			return nil, err
		}
	}

	if from == "" {
		from = "???@???"
	}

	if t.IsZero() {
		t = time.Now()
	}
	date := t.UTC().Format(time.ANSIC)

	line := "From " + from + " " + date + "\n"
	if err := w.write([]byte(line)); err != nil {
		return nil, err
	}

	w.last = &messageWriter{parent: w}
	return w.last, nil
}

// Close ends the stream, closing the current message if there is one.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return errors.New("mbox: Writer already closed")
	}
	w.closed = true
	if w.last != nil {
		return w.last.Close()
	}
	return nil
}

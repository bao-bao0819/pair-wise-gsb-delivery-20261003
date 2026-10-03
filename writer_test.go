package mbox

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type testMessage struct {
	date string
	text string
}

func testWriter(t *testing.T, messages []testMessage) string {
	var b bytes.Buffer
	wc := NewWriter(&b)

	for _, m := range messages {
		r := strings.NewReader(m.text)
		date, _ := time.Parse(time.RFC1123Z, m.date)

		mw, err := wc.CreateMessage("", date)
		if err != nil {
			t.Fatal(err)
		}

		_, err = io.Copy(mw, r)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}

	return b.String()
}

func TestWriter(t *testing.T) {
	messages := []testMessage{
		{
			"Thu, 01 Jan 2015 00:00:01 +0100",
			`Date: Thu, 01 Jan 2015 00:00:01 +0100

This is a simple test.

And, by the way, this is how a "From" line is escaped in mboxo format:

From Herp Derp with love.

Bye.`,
		},
		{
			"Thu, 02 Jan 2015 00:00:01 +0100",
			`Date: Thu, 02 Jan 2015 00:00:01 +0100` + "\r" + `
` + "\r" + `
This is another simple test.` + "\r" + `
` + "\r" + `
Another line.` + "\r" + `
` + "\r" + `
Bye.`,
		},
	}

	expected := `From ???@??? Wed Dec 31 23:00:01 2014
Date: Thu, 01 Jan 2015 00:00:01 +0100

This is a simple test.

And, by the way, this is how a "From" line is escaped in mboxo format:

>From Herp Derp with love.

Bye.

From ???@??? Thu Jan  1 23:00:01 2015
Date: Thu, 02 Jan 2015 00:00:01 +0100

This is another simple test.

Another line.

Bye.

`

	s := testWriter(t, messages)
	if s != expected {
		t.Error("Invalid mbox output:", s)
	}
}

// Test for the fix for https://github.com/emersion/go-mbox/issues/21
func TestWriter_multipleWritesCount(t *testing.T) {
	var buffer bytes.Buffer
	mboxWriter := NewWriter(&buffer)
	w, err := mboxWriter.CreateMessage("-", time.Time{})
	if err != nil {
		panic(err)
	}
	b := []byte("some text   ")
	n, err := w.Write(b)
	if n != len(b) {
		t.Errorf("unexpected return value for write: %d (expected %d)", n, len(b))
	}
	b = []byte("end of line\n")
	n, err = w.Write(b)
	if n != len(b) {
		t.Errorf("unexpected return value for write: %d (expected %d)", n, len(b))
	}
}

func TestWriter_crlfCount(t *testing.T) {
	var buffer bytes.Buffer
	mboxWriter := NewWriter(&buffer)
	w, err := mboxWriter.CreateMessage("-", time.Time{})
	if err != nil {
		panic(err)
	}
	// CRLF gets turned into LF, so one less byte than the input gets written
	b := []byte("hello\r\nworld!")
	n, err := w.Write(b)
	if n != len(b) {
		t.Errorf("Write() = %v, want %v", n, len(b))
	}
}

func mustCreateMessage(t *testing.T, mw *Writer, from string) io.WriteCloser {
	t.Helper()
	w, err := mw.CreateMessage(from, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CreateMessage(%q) = %v", from, err)
	}
	return w
}

// A handle saved before starting the next message must reject later writes,
// so stale data can never land in a later (or the separator of a later)
// message.
func TestWriter_interleavedHandles(t *testing.T) {
	var b bytes.Buffer
	mw := NewWriter(&b)

	first := mustCreateMessage(t, mw, "a@example.com")
	if _, err := first.Write([]byte("first body\n")); err != nil {
		t.Fatalf("first Write() = %v", err)
	}

	// Creating the second message ends the first one automatically.
	second := mustCreateMessage(t, mw, "b@example.com")

	if _, err := first.Write([]byte("stale\n")); err != ErrMessageClosed {
		t.Errorf("stale first Write() error = %v, want %v", err, ErrMessageClosed)
	}
	if err := first.Close(); err != ErrMessageClosed {
		t.Errorf("stale first Close() error = %v, want %v", err, ErrMessageClosed)
	}

	if _, err := second.Write([]byte("second body\n")); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Writer.Close() = %v", err)
	}

	// Both handles are dead after the stream is closed.
	if _, err := second.Write([]byte("late\n")); err != ErrMessageClosed {
		t.Errorf("late second Write() error = %v, want %v", err, ErrMessageClosed)
	}

	want := "From a@example.com Thu Jan  1 00:00:00 1970\n" +
		"first body\n" +
		"\n\n" +
		"From b@example.com Thu Jan  1 00:00:00 1970\n" +
		"second body\n" +
		"\n\n"
	if b.String() != want {
		t.Errorf("archive mismatch:\n got %q\nwant %q", b.String(), want)
	}
}

// Repeatedly ending a message is harmless: it reports ErrMessageClosed rather
// than appending a second separator.
func TestWriter_repeatedClose(t *testing.T) {
	var b bytes.Buffer
	mw := NewWriter(&b)
	w := mustCreateMessage(t, mw, "a@example.com")

	if _, err := w.Write([]byte("body")); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close() = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := w.Close(); err != ErrMessageClosed {
			t.Errorf("Close() #%d error = %v, want %v", i+2, err, ErrMessageClosed)
		}
	}
	if _, err := w.Write([]byte("late")); err != ErrMessageClosed {
		t.Errorf("Write() after Close error = %v, want %v", err, ErrMessageClosed)
	}

	want := "From a@example.com Thu Jan  1 00:00:00 1970\nbody\n\n"
	if b.String() != want {
		t.Errorf("archive mismatch:\n got %q\nwant %q", b.String(), want)
	}
}

// A CRLF sequence split across two Write calls must still be collapsed to a
// single LF and both writes must report their full input length.
func TestWriter_crlfChunked(t *testing.T) {
	var b bytes.Buffer
	mw := NewWriter(&b)
	w := mustCreateMessage(t, mw, "a@example.com")

	chunks := [][]byte{
		[]byte("hello\r"),
		[]byte("\nwor"),
		[]byte("ld\r\n"),
		[]byte("From split\r"),
		[]byte("\nend"),
	}
	for i, c := range chunks {
		n, err := w.Write(c)
		if err != nil {
			t.Fatalf("Write(%q) #%d = %v", c, i, err)
		}
		if n != len(c) {
			t.Errorf("Write(%q) #%d n = %d, want %d", c, i, n, len(c))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	want := "From a@example.com Thu Jan  1 00:00:00 1970\n" +
		"hello\nworld\n>From split\nend\n\n"
	if b.String() != want {
		t.Errorf("archive mismatch:\n got %q\nwant %q", b.String(), want)
	}
}

var errSink = errors.New("sink failure")

// failingWriter accepts at most limit bytes total, then fails. Bytes it
// accepted are still captured.
type failingWriter struct {
	buf   bytes.Buffer
	limit int
}

func (fw *failingWriter) Write(p []byte) (int, error) {
	if fw.limit <= 0 {
		return 0, errSink
	}
	if len(p) > fw.limit {
		p = p[:fw.limit]
	}
	n, _ := fw.buf.Write(p)
	fw.limit -= n
	return n, nil
}

// Once the underlying writer fails the error becomes sticky: no separator is
// ever appended, a new message cannot be started, and later writes do not
// leak data into the archive.
func TestWriter_writeFailure(t *testing.T) {
	sink := &failingWriter{limit: len("From a@example.com Thu Jan  1 00:00:00 1970\n") + 2}
	mw := NewWriter(sink)
	w := mustCreateMessage(t, mw, "a@example.com")

	p := []byte("some body bytes\n")
	n, err := w.Write(p)
	if err != errSink {
		t.Fatalf("Write() error = %v, want %v", err, errSink)
	}
	if n != 2 {
		t.Errorf("Write() n = %d, want %d (only the accepted input bytes)", n, 2)
	}

	// The same error sticks on every later operation.
	if _, err := w.Write([]byte("more\n")); err != errSink {
		t.Errorf("second Write() error = %v, want %v", err, errSink)
	}
	if err := w.Close(); err != errSink {
		t.Errorf("Close() error = %v, want %v", err, errSink)
	}
	if _, err := mw.CreateMessage("b@example.com", time.Unix(0, 0).UTC()); err != errSink {
		t.Errorf("CreateMessage() after failure error = %v, want %v", err, errSink)
	}
	if err := mw.Close(); err != errSink {
		t.Errorf("Writer.Close() error = %v, want %v", err, errSink)
	}

	out := sink.buf.String()
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("separator was appended after failure: %q", out)
	}
	if strings.Contains(out, "more") || strings.Contains(out, "some body bytes"[2:]) {
		t.Errorf("rejected data leaked into archive: %q", out)
	}
}

// A writer that returns a short count with a nil error is reported as
// io.ErrShortWrite and the returned byte count still follows the input.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func TestWriter_shortWrite(t *testing.T) {
	mw := NewWriter(shortWriter{})
	w := mustCreateMessage(t, mw, "a@example.com")

	p := []byte("body\n")
	n, err := w.Write(p)
	if err != io.ErrShortWrite {
		t.Fatalf("Write() error = %v, want %v", err, io.ErrShortWrite)
	}
	if n < 0 || n > len(p) {
		t.Errorf("Write() n = %d, want value within [0,%d]", n, len(p))
	}
	if err := w.Close(); err != io.ErrShortWrite {
		t.Errorf("Close() error = %v, want %v", err, io.ErrShortWrite)
	}
}

package mbox

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

var errTestWrite = errors.New("mbox-test: underlying write failure")

// failingWriter returns errTestWrite on every Write after allow successful
// bytes have been emitted. Everything that would have been written after the
// failure is instead recorded in afterErr, so tests can prove no stale data or
// separator is appended later.
type failingWriter struct {
	buf      bytes.Buffer
	afterErr bytes.Buffer
	allow    int
}

func (fw *failingWriter) Write(p []byte) (int, error) {
	if fw.allow > 0 {
		n := len(p)
		if n > fw.allow {
			n = fw.allow
		}
		fw.allow -= n
		fw.buf.Write(p[:n])
		if n < len(p) {
			fw.afterErr.Write(p[n:])
			return n, errTestWrite
		}
		return n, nil
	}
	fw.afterErr.Write(p)
	return 0, errTestWrite
}

// shortWriter successfully swallows the mbox separator of the first message
// but reports every subsequent write as a short write without an error.
type shortWriter struct {
	buf   bytes.Buffer
	wrote int
}

func (sw *shortWriter) Write(p []byte) (int, error) {
	sw.wrote++
	if sw.wrote == 1 {
		sw.buf.Write(p)
		return len(p), nil
	}
	if len(p) > 1 {
		p = p[:len(p)-1]
	}
	sw.buf.Write(p)
	return len(p), nil
}

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

// Two handles must not interleave: starting a new message retires the old
// handle, and writes to the retired handle must not leak into the archive.
func TestWriter_handleLifecycle(t *testing.T) {
	var buf bytes.Buffer
	mw := NewWriter(&buf)
	ts := time.Unix(0, 0).UTC()

	first, err := mw.CreateMessage("one@example.com", ts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Write([]byte("first body")); err != nil {
		t.Fatalf("write before next message: %v", err)
	}

	second, err := mw.CreateMessage("two@example.com", ts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write([]byte("second body")); err != nil {
		t.Fatalf("write current message: %v", err)
	}

	// The retired handle rejects further writes instead of corrupting the
	// second message.
	if _, err := first.Write([]byte("STALE\n")); err != ErrMessageClosed {
		t.Fatalf("stale handle Write() error = %v, want %v", err, ErrMessageClosed)
	}

	// Repeated explicit close of the retired message is harmless.
	if err := first.(io.Closer).Close(); err != nil {
		t.Fatalf("closing retired message: %v", err)
	}
	if err := first.(io.Closer).Close(); err != nil {
		t.Fatalf("closing retired message twice: %v", err)
	}

	if err := second.(io.Closer).Close(); err != nil {
		t.Fatalf("explicit close: %v", err)
	}
	if err := second.(io.Closer).Close(); err != nil {
		t.Fatalf("duplicate explicit close: %v", err)
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("archive close: %v", err)
	}

	// Writer.Close must not be callable twice.
	if err := mw.Close(); err == nil {
		t.Fatal("second Writer.Close() succeeded")
	}

	// Handles stay invalid after the archive ends.
	if _, err := second.Write([]byte("POSTMORTEM\n")); err != ErrMessageClosed {
		t.Fatalf("post-close Write() error = %v, want %v", err, ErrMessageClosed)
	}

	got := buf.String()
	if strings.Contains(got, "STALE") || strings.Contains(got, "POSTMORTEM") {
		t.Fatalf("archive contains data from expired handles:\n%s", got)
	}

	want := "From one@example.com Thu Jan  1 00:00:00 1970\n" +
		"first body\n\n" +
		"From two@example.com Thu Jan  1 00:00:00 1970\n" +
		"second body\n\n"
	if got != want {
		t.Fatalf("archive mismatch:\nwant %q\n got %q", want, got)
	}

	// No new messages can be appended once the archive is closed.
	if _, err := mw.CreateMessage("three@example.com", ts); err == nil {
		t.Fatal("CreateMessage after Writer.Close() succeeded")
	}
}

// A CRLF split across two Write calls must be normalized to a single LF, and
// every Write must report exactly the number of bytes consumed from its own
// input.
func TestWriter_splitCRLF(t *testing.T) {
	var buf bytes.Buffer
	mw := NewWriter(&buf)

	msg, err := mw.CreateMessage("-", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	chunks := [][]byte{
		[]byte("ab\r"),
		[]byte("\ncd"),
		[]byte("\r\ne"),
	}
	wantN := []int{3, 3, 3}

	var prev int
	for i, chunk := range chunks {
		n, err := msg.Write(chunk)
		if err != nil {
			t.Fatalf("Write(%q) error: %v", chunk, err)
		}
		if n != wantN[i] {
			t.Fatalf("Write(%q) = %d, want %d", chunk, n, wantN[i])
		}
		prev += n
	}
	if prev != 9 {
		t.Fatalf("total consumed = %d, want 9", prev)
	}

	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	want := "From - Thu Jan  1 00:00:00 1970\nab\ncd\ne\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("archive mismatch:\nwant %q\n got %q", want, got)
	}
}

// The first underlying error is sticky: it is returned by every subsequent
// Write and Close, and no fresh separator bytes are appended.
func TestWriter_midWriteFailure(t *testing.T) {
	fw := &failingWriter{allow: 100}
	mw := NewWriter(fw)

	msg, err := mw.CreateMessage("a@example.com", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := msg.Write([]byte("line one\n")); err != nil {
		t.Fatal(err)
	}

	// Drain the remaining allowance so the next write fails mid-message.
	if _, err := msg.Write([]byte(strings.Repeat("x", 40))); err != nil {
		t.Fatalf("unexpected early failure: %v", err)
	}

	if _, err := msg.Write([]byte("line two\n")); !errors.Is(err, errTestWrite) {
		t.Fatalf("failing Write() error = %v, want %v", err, errTestWrite)
	}

	// Once failed, the message and the archive are dead.
	if _, err := msg.Write([]byte("more\n")); !errors.Is(err, errTestWrite) {
		t.Fatalf("write after failure error = %v, want %v", err, errTestWrite)
	}
	if err := msg.Close(); !errors.Is(err, errTestWrite) {
		t.Fatalf("message close after failure error = %v, want %v", err, errTestWrite)
	}
	if err := mw.Close(); !errors.Is(err, errTestWrite) {
		t.Fatalf("archive close after failure error = %v, want %v", err, errTestWrite)
	}

	// The error must not change on repeated calls.
	if err := mw.Close(); !errors.Is(err, errTestWrite) {
		t.Fatalf("second archive close error = %v, want %v", err, errTestWrite)
	}

	// Nothing must reach the underlying writer after the first failure: no
	// separator, no stale bytes, no "From " header of a new message.
	after := fw.afterErr.String()
	if strings.Contains(after, "\n\n") {
		t.Fatalf("separator appended after failure: %q", after)
	}
	if strings.Contains(after, "From ") || strings.Contains(after, "more") {
		t.Fatalf("stale data appended after failure: %q", after)
	}

	if _, err := mw.CreateMessage("b@example.com", time.Unix(0, 0).UTC()); !errors.Is(err, errTestWrite) {
		t.Fatalf("CreateMessage after failure error = %v, want %v", err, errTestWrite)
	}
}

// A short write reported without an error becomes io.ErrShortWrite, while the
// reported count never exceeds the caller's input length.
func TestWriter_shortWrite(t *testing.T) {
	sw := &shortWriter{}
	mw := NewWriter(sw)

	msg, err := mw.CreateMessage("-", time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	p := []byte("hello world\n")
	n, err := msg.Write(p)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short Write() error = %v, want %v", err, io.ErrShortWrite)
	}
	if n < 0 || n > len(p) {
		t.Fatalf("short Write() n = %d, want 0 <= n <= %d", n, len(p))
	}
}

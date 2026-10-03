package httphead

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"strings"
	"testing"
)

// shortReader reads at most max bytes per Read call.
type shortReader struct {
	data []byte
	max  int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.max
	if n > len(r.data) {
		n = len(r.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func equalHeaders(a, b []Header) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Name, b[i].Name) || !bytes.Equal(a[i].Value, b[i].Value) {
			return false
		}
	}
	return true
}

func TestReadHeader(t *testing.T) {
	// The second line is crafted so that with a 16-bytes bufio buffer its
	// '\r' is the last byte that fits into the buffer and '\n' arrives with
	// the next fill. The third line is longer than the buffer and crosses
	// its boundary multiple times.
	block := "Host: example.com\r\n" +
		"X-AB: 123456789\r\n" +
		"X-Long-Header-Name: some rather long header value here\r\n" +
		"X-Dup: 1\r\n" +
		"X-Dup: 2\r\n" +
		"\r\n"
	exp := []Header{
		{Name: []byte("Host"), Value: []byte("example.com")},
		{Name: []byte("X-AB"), Value: []byte("123456789")},
		{Name: []byte("X-Long-Header-Name"), Value: []byte("some rather long header value here")},
		// Duplicate fields are preserved in order and are not merged.
		{Name: []byte("X-Dup"), Value: []byte("1")},
		{Name: []byte("X-Dup"), Value: []byte("2")},
	}
	for _, bufSize := range []int{16, 17, 24, 32, 4096} {
		for _, chunk := range []int{1, 2, 3, 7, 4096} {
			br := bufio.NewReaderSize(&shortReader{data: []byte(block), max: chunk}, bufSize)
			fields, n, err := ReadHeader(br, HeaderLimits{})
			if err != nil {
				t.Fatalf("bufSize=%d chunk=%d: unexpected error: %v", bufSize, chunk, err)
			}
			if n != len(block) {
				t.Errorf("bufSize=%d chunk=%d: consumed %d bytes; want %d", bufSize, chunk, n, len(block))
			}
			if !equalHeaders(fields, exp) {
				t.Errorf("bufSize=%d chunk=%d: unexpected fields: %q", bufSize, chunk, fields)
			}
		}
	}
}

func TestReadHeaderSequence(t *testing.T) {
	first := "A: 1\r\n\r\n"
	second := "B: 2\r\nC: 3\r\n\r\n"
	body := "BODY"
	br := bufio.NewReaderSize(&shortReader{data: []byte(first + second + body), max: 3}, 16)

	f1, n1, err := ReadHeader(br, HeaderLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n1 != len(first) {
		t.Errorf("first block consumed %d bytes; want %d", n1, len(first))
	}
	if exp := []Header{{Name: []byte("A"), Value: []byte("1")}}; !equalHeaders(f1, exp) {
		t.Errorf("unexpected first block fields: %q", f1)
	}

	f2, n2, err := ReadHeader(br, HeaderLimits{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n2 != len(second) {
		t.Errorf("second block consumed %d bytes; want %d", n2, len(second))
	}
	if exp := []Header{
		{Name: []byte("B"), Value: []byte("2")},
		{Name: []byte("C"), Value: []byte("3")},
	}; !equalHeaders(f2, exp) {
		t.Errorf("unexpected second block fields: %q", f2)
	}

	rest, err := ioutil.ReadAll(br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(rest) != body {
		t.Errorf("unexpected body: %q; want %q", rest, body)
	}
}

func TestReadHeaderLimits(t *testing.T) {
	// next imitates bytes of a subsequent request that must not be consumed
	// when an error occurs.
	next := "GET /next HTTP/1.1\r\n\r\n"
	for _, test := range []struct {
		name  string
		block string
		lim   HeaderLimits
		err   error
		// consumed, if >= 0, is the exact number of bytes expected to be
		// consumed; otherwise only the not-consumed suffix is checked.
		consumed int
	}{
		{
			name:  "line too long",
			block: "A: 1\r\nLong: " + strings.Repeat("x", 100) + "\r\n\r\n",
			lim:   HeaderLimits{MaxLineLength: 16},
			err:   ErrHeaderLineTooLong,
			// The reader must stop in the middle of the long line.
			consumed: -1,
		},
		{
			name:     "too many fields",
			block:    "A: 1\r\nB: 2\r\n\r\n",
			lim:      HeaderLimits{MaxFields: 1},
			err:      ErrTooManyHeaders,
			consumed: len("A: 1\r\nB: 2\r\n"),
		},
		{
			name:     "block too large",
			block:    "A: 1\r\nB: 2\r\n\r\n",
			lim:      HeaderLimits{MaxBytes: 10},
			err:      ErrHeaderTooLarge,
			consumed: len("A: 1\r\nB: 2\r\n"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := test.block + next
			br := bufio.NewReaderSize(&shortReader{data: []byte(data), max: 3}, 16)
			_, n, err := ReadHeader(br, test.lim)
			if !errors.Is(err, test.err) {
				t.Fatalf("unexpected error: %v; want %v", err, test.err)
			}
			if test.consumed >= 0 && n != test.consumed {
				t.Errorf("consumed %d bytes; want %d", n, test.consumed)
			}
			// The rest of the stream, including the terminating empty
			// line and the subsequent request, must be left untouched.
			rest, rerr := ioutil.ReadAll(br)
			if rerr != nil {
				t.Fatalf("unexpected error: %v", rerr)
			}
			if act, exp := string(rest), data[n:]; act != exp {
				t.Errorf("unexpected rest of stream: %q; want %q", act, exp)
			}
			if !strings.HasSuffix(string(rest), next) {
				t.Errorf("subsequent request was consumed; rest of stream: %q", rest)
			}
		})
	}
}

func TestReadHeaderStrict(t *testing.T) {
	for _, test := range []struct {
		name  string
		block string
		err   error
	}{
		{
			name:  "bare LF",
			block: "A: 1\n\r\n",
			err:   ErrBareLineFeed,
		},
		{
			name:  "bare LF terminator",
			block: "A: 1\r\n\n",
			err:   ErrBareLineFeed,
		},
		{
			name:  "obs-fold space",
			block: "A: 1\r\n folded\r\n\r\n",
			err:   ErrObsoleteFolding,
		},
		{
			name:  "obs-fold tab",
			block: "A: 1\r\n\tfolded\r\n\r\n",
			err:   ErrObsoleteFolding,
		},
		{
			name:  "malformed line",
			block: "A: 1\r\nno colon here\r\n\r\n",
			err:   ErrMalformedHeader,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			br := bufio.NewReaderSize(&shortReader{data: []byte(test.block), max: 2}, 16)
			_, _, err := ReadHeader(br, HeaderLimits{})
			if !errors.Is(err, test.err) {
				t.Fatalf("unexpected error: %v; want %v", err, test.err)
			}
		})
	}
}

func TestReadHeaderTruncated(t *testing.T) {
	for _, test := range []struct {
		name     string
		block    string
		consumed int
	}{
		{
			name:     "empty",
			block:    "",
			consumed: 0,
		},
		{
			name:     "truncated line",
			block:    "A: 1\r\nB: 2",
			consumed: len("A: 1\r\nB: 2"),
		},
		{
			name:     "truncated CR",
			block:    "A: 1\r\nB: 2\r",
			consumed: len("A: 1\r\nB: 2\r"),
		},
		{
			name:     "no terminator",
			block:    "A: 1\r\n",
			consumed: len("A: 1\r\n"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			br := bufio.NewReaderSize(&shortReader{data: []byte(test.block), max: 3}, 16)
			_, n, err := ReadHeader(br, HeaderLimits{})
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("unexpected error: %v; want %v", err, io.ErrUnexpectedEOF)
			}
			if n != test.consumed {
				t.Errorf("consumed %d bytes; want %d", n, test.consumed)
			}
		})
	}
}

func TestParseRequestLine(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
		exp  RequestLine
		fail bool
	}{
		{
			line: "",
			fail: true,
		},
		{
			line: "GET",
			fail: true,
		},
		{
			line: "GET ",
			fail: true,
		},
		{
			line: "GET  ",
			fail: true,
		},
		{
			line: "GET   ",
			fail: true,
		},
		{
			line: "GET / HTTP/1.1",
			exp: RequestLine{
				Method:  []byte("GET"),
				URI:     []byte("/"),
				Version: Version{1, 1},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, ok := ParseRequestLine([]byte(test.line))
			if test.fail && ok {
				t.Fatalf("unexpected successful parsing")
			}
			if !test.fail && !ok {
				t.Fatalf("unexpected parse error")
			}
			if test.fail {
				return
			}
			if act, exp := r.Method, test.exp.Method; !bytes.Equal(act, exp) {
				t.Errorf("unexpected parsed method: %q; want %q", act, exp)
			}
			if act, exp := r.URI, test.exp.URI; !bytes.Equal(act, exp) {
				t.Errorf("unexpected parsed uri: %q; want %q", act, exp)
			}
			if act, exp := r.Version, test.exp.Version; act != exp {
				t.Errorf("unexpected parsed version: %+v; want %+v", act, exp)
			}
		})
	}
}

func TestParseResponseLine(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
		exp  ResponseLine
		fail bool
	}{
		{
			line: "",
			fail: true,
		},
		{
			line: "HTTP/1.1",
			fail: true,
		},
		{
			line: "HTTP/1.1 ",
			fail: true,
		},
		{
			line: "HTTP/1.1  ",
			fail: true,
		},
		{
			line: "HTTP/1.1   ",
			fail: true,
		},
		{
			line: "HTTP/1.1 200 OK",
			exp: ResponseLine{
				Version: Version{1, 1},
				Status:  200,
				Reason:  []byte("OK"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, ok := ParseResponseLine([]byte(test.line))
			if test.fail && ok {
				t.Fatalf("unexpected successful parsing")
			}
			if !test.fail && !ok {
				t.Fatalf("unexpected parse error")
			}
			if test.fail {
				return
			}
			if act, exp := r.Version, test.exp.Version; act != exp {
				t.Errorf("unexpected parsed version: %+v; want %+v", act, exp)
			}
			if act, exp := r.Status, test.exp.Status; act != exp {
				t.Errorf("unexpected parsed status: %d; want %d", act, exp)
			}
			if act, exp := r.Reason, test.exp.Reason; !bytes.Equal(act, exp) {
				t.Errorf("unexpected parsed reason: %q; want %q", act, exp)
			}
		})
	}
}

var versionCases = []struct {
	in    []byte
	major int
	minor int
	ok    bool
}{
	{[]byte("HTTP/1.1"), 1, 1, true},
	{[]byte("HTTP/1.0"), 1, 0, true},
	{[]byte("HTTP/1.2"), 1, 2, true},
	{[]byte("HTTP/42.1092"), 42, 1092, true},
}

func TestParseHttpVersion(t *testing.T) {
	for _, c := range versionCases {
		t.Run(string(c.in), func(t *testing.T) {
			major, minor, ok := ParseVersion(c.in)
			if major != c.major || minor != c.minor || ok != c.ok {
				t.Errorf(
					"parseHttpVersion([]byte(%q)) = %v, %v, %v; want %v, %v, %v",
					string(c.in), major, minor, ok, c.major, c.minor, c.ok,
				)
			}
		})
	}
}

func BenchmarkParseHttpVersion(b *testing.B) {
	for _, c := range versionCases {
		b.Run(string(c.in), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _, _ = ParseVersion(c.in)
			}
		})
	}
}

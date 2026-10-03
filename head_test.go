package httphead

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"strings"
	"testing"
)

func headerFieldsEqual(fields []HeaderField, exp [][2]string) bool {
	if len(fields) != len(exp) {
		return false
	}
	for i, f := range fields {
		if string(f.Name) != exp[i][0] || string(f.Value) != exp[i][1] {
			return false
		}
	}
	return true
}

func TestReadHeader(t *testing.T) {
	for _, test := range []struct {
		name   string
		in     string
		opts   ReadHeaderOptions
		exp    [][2]string
		err    error
		consum int
	}{
		{
			name: "simple",
			in:   "Host: example.com\r\nAccept: text/html\r\n\r\n",
			exp: [][2]string{
				{"Host", "example.com"},
				{"Accept", "text/html"},
			},
		},
		{
			name: "duplicates preserved in order",
			in:   "X-A: 1\r\nX-B: 2\r\nX-A: 3\r\n\r\n",
			exp: [][2]string{
				{"X-A", "1"},
				{"X-B", "2"},
				{"X-A", "3"},
			},
		},
		{
			name: "empty block",
			in:   "\r\n",
			exp:  nil,
		},
		{
			name:   "bare lf strict",
			in:     "Host: example.com\n\n",
			err:    ErrBareLineFeed,
			consum: len("Host: example.com\n"),
		},
		{
			name: "bare lf allowed",
			in:   "Host: example.com\nAccept: x\n\n",
			opts: ReadHeaderOptions{AllowBareLF: true},
			exp: [][2]string{
				{"Host", "example.com"},
				{"Accept", "x"},
			},
		},
		{
			name:   "bare lf empty line strict",
			in:     "Host: example.com\r\n\n",
			err:    ErrBareLineFeed,
			consum: len("Host: example.com\r\n\n"),
		},
		{
			name:   "obs fold strict",
			in:     "X-A: 1\r\n\t2\r\n\r\n",
			err:    ErrObsFold,
			consum: len("X-A: 1\r\n\t2\r\n"),
		},
		{
			name: "obs fold allowed",
			in:   "X-A: 1\r\n\t2\r\n  3\r\n\r\n",
			opts: ReadHeaderOptions{AllowObsFold: true},
			exp: [][2]string{
				{"X-A", "1 2 3"},
			},
		},
		{
			name:   "obs fold without field",
			in:     " folded\r\n\r\n",
			opts:   ReadHeaderOptions{AllowObsFold: true},
			err:    ErrMalformedHeaderLine,
			consum: len(" folded\r\n"),
		},
		{
			name:   "malformed line",
			in:     "NoColonHere\r\n\r\n",
			err:    ErrMalformedHeaderLine,
			consum: len("NoColonHere\r\n"),
		},
		{
			name:   "truncated",
			in:     "Host: example.com\r\nAccept: text/ht",
			err:    io.EOF,
			consum: len("Host: example.com\r\nAccept: text/ht"),
		},
		{
			name:   "line too long",
			in:     "X-Long: " + strings.Repeat("a", 64) + "\r\n\r\n",
			opts:   ReadHeaderOptions{MaxLineLength: 32},
			err:    ErrHeaderLineTooLong,
			consum: 48, // stops at the first 16-byte chunk past the limit.
		},
		{
			name:   "too many fields",
			in:     "A: 1\r\nB: 2\r\nC: 3\r\n\r\n",
			opts:   ReadHeaderOptions{MaxFields: 2},
			err:    ErrTooManyHeaderFields,
			consum: len("A: 1\r\nB: 2\r\nC: 3\r\n"),
		},
		{
			name:   "block too large",
			in:     "A: 1\r\nB: 2\r\n\r\n",
			opts:   ReadHeaderOptions{MaxBytes: 10},
			err:    ErrHeaderTooLarge,
			consum: 12,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			br := bufio.NewReaderSize(strings.NewReader(test.in), 16)
			fields, n, err := ReadHeader(br, test.opts)
			if test.err != nil {
				if err != test.err {
					t.Fatalf("ReadHeader() error = %v; want %v", err, test.err)
				}
			} else if err != nil {
				t.Fatalf("ReadHeader() unexpected error: %v", err)
			}
			if test.err == nil && !headerFieldsEqual(fields, test.exp) {
				t.Errorf("ReadHeader() fields = %q; want %q", fields, test.exp)
			}
			expConsum := test.consum
			if expConsum == 0 && test.err == nil {
				expConsum = len(test.in)
			}
			if n != expConsum {
				t.Errorf("ReadHeader() consumed = %d; want %d", n, expConsum)
			}
			// Whatever happened, the reader must not have consumed
			// more than reported.
			rest, rerr := ioutil.ReadAll(br)
			if rerr != nil {
				t.Fatalf("unexpected read error: %v", rerr)
			}
			if string(rest) != test.in[n:] {
				t.Errorf("unread remainder = %q; want %q", rest, test.in[n:])
			}
		})
	}
}

func TestReadHeaderBodyStart(t *testing.T) {
	in := "Host: example.com\r\n\r\nGET / HTTP/1.1\r\n\r\n"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	_, n, err := ReadHeader(br, ReadHeaderOptions{})
	if err != nil {
		t.Fatalf("ReadHeader() unexpected error: %v", err)
	}
	body, err := ioutil.ReadAll(br)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(body) != in[n:] {
		t.Errorf("body = %q; want %q", body, in[n:])
	}
	if act, exp := string(body), "GET / HTTP/1.1\r\n\r\n"; act != exp {
		t.Errorf("body = %q; want %q", act, exp)
	}
}

func TestReadHeaderTwoBlocks(t *testing.T) {
	in := "A: 1\r\n\r\nB: 2\r\nC: 3\r\n\r\n"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)

	f1, n1, err := ReadHeader(br, ReadHeaderOptions{})
	if err != nil {
		t.Fatalf("first ReadHeader() unexpected error: %v", err)
	}
	if !headerFieldsEqual(f1, [][2]string{{"A", "1"}}) {
		t.Errorf("first block fields = %q; want [A:1]", f1)
	}

	f2, n2, err := ReadHeader(br, ReadHeaderOptions{})
	if err != nil {
		t.Fatalf("second ReadHeader() unexpected error: %v", err)
	}
	if !headerFieldsEqual(f2, [][2]string{{"B", "2"}, {"C", "3"}}) {
		t.Errorf("second block fields = %q; want [B:2 C:3]", f2)
	}

	if n1+n2 != len(in) {
		t.Errorf("consumed = %d + %d; want %d", n1, n2, len(in))
	}
}

func TestReadHeaderTinyBuffer(t *testing.T) {
	// Lines are much longer than the 16-byte minimal bufio buffer, so
	// every line is assembled across multiple buffer fills.
	in := "X-Long-Header: some-quite-long-value-here\r\n" +
		"Second: another-pretty-long-header-value\r\n" +
		"\r\n" +
		"BODY"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	fields, n, err := ReadHeader(br, ReadHeaderOptions{})
	if err != nil {
		t.Fatalf("ReadHeader() unexpected error: %v", err)
	}
	exp := [][2]string{
		{"X-Long-Header", "some-quite-long-value-here"},
		{"Second", "another-pretty-long-header-value"},
	}
	if !headerFieldsEqual(fields, exp) {
		t.Errorf("ReadHeader() fields = %q; want %q", fields, exp)
	}
	if act := in[n:]; act != "BODY" {
		t.Errorf("body starts at %d: %q; want %q", n, act, "BODY")
	}
}

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

func TestReadHeaderShortReads(t *testing.T) {
	in := "Host: example.com\r\nAccept: text/html\r\n\r\n"
	// With max=1 every CRLF is split across two reads.
	for _, step := range []int{1, 2, 3, 5, 7} {
		t.Run(strings.Repeat("x", step), func(t *testing.T) {
			br := bufio.NewReaderSize(&shortReader{data: []byte(in), max: step}, 16)
			fields, n, err := ReadHeader(br, ReadHeaderOptions{})
			if err != nil {
				t.Fatalf("ReadHeader() unexpected error: %v", err)
			}
			exp := [][2]string{
				{"Host", "example.com"},
				{"Accept", "text/html"},
			}
			if !headerFieldsEqual(fields, exp) {
				t.Errorf("ReadHeader() fields = %q; want %q", fields, exp)
			}
			if n != len(in) {
				t.Errorf("ReadHeader() consumed = %d; want %d", n, len(in))
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

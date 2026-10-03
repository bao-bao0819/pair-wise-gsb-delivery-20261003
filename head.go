package httphead

import (
	"bufio"
	"bytes"
	"errors"
)

// HeaderField represents a single HTTP header field as a name-value pair.
type HeaderField struct {
	Name  []byte
	Value []byte
}

// ReadHeaderOptions configures ReadHeader behavior.
//
// The zero value means no limits and strict line ending policy.
type ReadHeaderOptions struct {
	// MaxLineLength limits the length of a single header line in bytes,
	// including the terminating CRLF. Zero means no limit.
	MaxLineLength int

	// MaxFields limits the number of header fields. Zero means no limit.
	MaxFields int

	// MaxBytes limits the cumulative number of bytes read for the whole
	// header block, including the terminating empty line. Zero means no
	// limit.
	MaxBytes int

	// AllowBareLF allows header lines to be terminated by a bare '\n'
	// without a preceding '\r'. By default such lines are rejected with
	// ErrBareLineFeed.
	AllowBareLF bool

	// AllowObsFold allows obsolete line folding: a line starting with a
	// space or horizontal tab is treated as a continuation of the previous
	// field value. By default such lines are rejected with ErrObsFold.
	AllowObsFold bool
}

var (
	// ErrHeaderLineTooLong is returned by ReadHeader when a single header
	// line exceeds ReadHeaderOptions.MaxLineLength.
	ErrHeaderLineTooLong = errors.New("httphead: header line exceeds length limit")

	// ErrTooManyHeaderFields is returned by ReadHeader when the number of
	// header fields exceeds ReadHeaderOptions.MaxFields.
	ErrTooManyHeaderFields = errors.New("httphead: header fields count exceeds limit")

	// ErrHeaderTooLarge is returned by ReadHeader when the cumulative size
	// of the header block exceeds ReadHeaderOptions.MaxBytes.
	ErrHeaderTooLarge = errors.New("httphead: header block exceeds size limit")

	// ErrBareLineFeed is returned by ReadHeader when a header line is
	// terminated by a bare '\n' and ReadHeaderOptions.AllowBareLF is not
	// set.
	ErrBareLineFeed = errors.New("httphead: bare line feed in header")

	// ErrObsFold is returned by ReadHeader when a line starts with a space
	// or horizontal tab (obsolete line folding) and
	// ReadHeaderOptions.AllowObsFold is not set.
	ErrObsFold = errors.New("httphead: obsolete line folding in header")

	// ErrMalformedHeaderLine is returned by ReadHeader when a header line
	// can not be parsed as a key-value pair.
	ErrMalformedHeaderLine = errors.New("httphead: malformed header line")
)

// ReadHeader reads a complete HTTP header block from br.
//
// The header block is a sequence of header field lines terminated by a
// single empty line, which is the only normal end of the block. It returns
// the parsed fields in the order they appear on the wire and the number of
// bytes consumed from br. The consumed count is the offset at which the
// message body starts.
//
// Duplicate fields are preserved in their original order; no comma-based
// merging of values is performed at this layer.
//
// Lines must be terminated by CRLF. Bare line feeds and obsolete line
// folding are rejected unless explicitly allowed by the corresponding
// ReadHeaderOptions flags.
//
// If a limit is exceeded or the stream is truncated before the terminating
// empty line, ReadHeader reports the error along with the number of bytes
// consumed so far and stops reading immediately. That is, it never keeps
// reading bytes that may belong to a subsequent request. On error the
// returned fields contain the fields parsed so far.
//
// The returned fields are copied out of the reader's buffer, so they stay
// valid after subsequent reads from br.
func ReadHeader(br *bufio.Reader, opts ReadHeaderOptions) (fields []HeaderField, n int, err error) {
	for {
		var maxTotal int
		if opts.MaxBytes > 0 {
			maxTotal = opts.MaxBytes - n
			if maxTotal <= 0 {
				return fields, n, ErrHeaderTooLarge
			}
		}

		line, crlf, m, lerr := readHeaderLine(br, opts.MaxLineLength, maxTotal)
		n += m
		if lerr != nil {
			return fields, n, lerr
		}
		if !crlf && !opts.AllowBareLF {
			return fields, n, ErrBareLineFeed
		}
		if len(line) == 0 {
			// Empty line is the only normal end of the header block.
			return fields, n, nil
		}
		if c := line[0]; c == ' ' || c == '\t' {
			if !opts.AllowObsFold {
				return fields, n, ErrObsFold
			}
			if len(fields) == 0 {
				return fields, n, ErrMalformedHeaderLine
			}
			// Unfold by joining the continuation with the previous
			// value using a single space. Copy into a fresh slice to
			// not write into the bufio.Reader internal buffer.
			last := &fields[len(fields)-1]
			cont := trim(line)
			v := make([]byte, 0, len(last.Value)+1+len(cont))
			v = append(v, last.Value...)
			if len(last.Value) > 0 && len(cont) > 0 {
				v = append(v, ' ')
			}
			last.Value = append(v, cont...)
			continue
		}

		k, v, ok := ParseHeaderLine(line)
		if !ok {
			return fields, n, ErrMalformedHeaderLine
		}
		// Copy the field out of the reader's buffer: it must stay
		// valid after further reads from br.
		fields = append(fields, HeaderField{
			Name:  append([]byte(nil), k...),
			Value: append([]byte(nil), v...),
		})
		if opts.MaxFields > 0 && len(fields) > opts.MaxFields {
			return fields, n, ErrTooManyHeaderFields
		}
	}
}

// readHeaderLine reads a single line from br much like ReadLine, but
// additionally enforces the given limits and reports the line ending style.
//
// It returns the line without the trailing CRLF or LF, whether the line was
// terminated by CRLF, and the number of bytes consumed from br, including
// the terminator.
//
// maxLine limits the number of bytes consumed by this call; maxTotal limits
// it as well, but violations are reported as ErrHeaderTooLarge instead of
// ErrHeaderLineTooLong. Zero means no limit. When a limit is exceeded the
// function returns immediately without reading the rest of the line.
func readHeaderLine(br *bufio.Reader, maxLine, maxTotal int) (line []byte, crlf bool, n int, err error) {
	for {
		bts, rerr := br.ReadSlice('\n')
		n += len(bts)
		if maxLine > 0 && n > maxLine {
			return nil, false, n, ErrHeaderLineTooLong
		}
		if maxTotal > 0 && n > maxTotal {
			return nil, false, n, ErrHeaderTooLarge
		}
		if rerr == bufio.ErrBufferFull {
			// Copy bytes because next read will discard them.
			line = append(line, bts...)
			continue
		}
		// Avoid copy of single read.
		if line == nil {
			line = bts
		} else {
			line = append(line, bts...)
		}
		if rerr != nil {
			// Truncated stream: report consumed bytes and stop.
			return nil, false, n, rerr
		}
		// Size of line is at least 1.
		// In other case bufio.ReadSlice() returns error.
		m := len(line)
		if m > 1 && line[m-2] == '\r' {
			return line[:m-2], true, n, nil
		}
		return line[:m-1], false, n, nil
	}
}

// Version contains protocol major and minor version.
type Version struct {
	Major int
	Minor int
}

// RequestLine contains parameters parsed from the first request line.
type RequestLine struct {
	Method  []byte
	URI     []byte
	Version Version
}

// ResponseLine contains parameters parsed from the first response line.
type ResponseLine struct {
	Version Version
	Status  int
	Reason  []byte
}

// SplitRequestLine splits given slice of bytes into three chunks without
// parsing.
func SplitRequestLine(line []byte) (method, uri, version []byte) {
	return split3(line, ' ')
}

// ParseRequestLine parses http request line like "GET / HTTP/1.0".
func ParseRequestLine(line []byte) (r RequestLine, ok bool) {
	var i int
	for i = 0; i < len(line); i++ {
		c := line[i]
		if !OctetTypes[c].IsToken() {
			if i > 0 && c == ' ' {
				break
			}
			return
		}
	}
	if i == len(line) {
		return
	}

	var proto []byte
	r.Method = line[:i]
	r.URI, proto = split2(line[i+1:], ' ')
	if len(r.URI) == 0 {
		return
	}
	if major, minor, ok := ParseVersion(proto); ok {
		r.Version.Major = major
		r.Version.Minor = minor
		return r, true
	}

	return r, false
}

// SplitResponseLine splits given slice of bytes into three chunks without
// parsing.
func SplitResponseLine(line []byte) (version, status, reason []byte) {
	return split3(line, ' ')
}

// ParseResponseLine parses first response line into ResponseLine struct.
func ParseResponseLine(line []byte) (r ResponseLine, ok bool) {
	var (
		proto  []byte
		status []byte
	)
	proto, status, r.Reason = split3(line, ' ')
	if major, minor, ok := ParseVersion(proto); ok {
		r.Version.Major = major
		r.Version.Minor = minor
	} else {
		return r, false
	}
	if n, ok := IntFromASCII(status); ok {
		r.Status = n
	} else {
		return r, false
	}
	// TODO(gobwas): parse here r.Reason fot TEXT rule:
	//   TEXT = <any OCTET except CTLs,
	//           but including LWS>
	return r, true
}

var (
	httpVersion10     = []byte("HTTP/1.0")
	httpVersion11     = []byte("HTTP/1.1")
	httpVersionPrefix = []byte("HTTP/")
)

// ParseVersion parses major and minor version of HTTP protocol.
// It returns parsed values and true if parse is ok.
func ParseVersion(bts []byte) (major, minor int, ok bool) {
	switch {
	case bytes.Equal(bts, httpVersion11):
		return 1, 1, true
	case bytes.Equal(bts, httpVersion10):
		return 1, 0, true
	case len(bts) < 8:
		return
	case !bytes.Equal(bts[:5], httpVersionPrefix):
		return
	}

	bts = bts[5:]

	dot := bytes.IndexByte(bts, '.')
	if dot == -1 {
		return
	}
	major, ok = IntFromASCII(bts[:dot])
	if !ok {
		return
	}
	minor, ok = IntFromASCII(bts[dot+1:])
	if !ok {
		return
	}

	return major, minor, true
}

// ReadLine reads line from br. It reads until '\n' and returns bytes without
// '\n' or '\r\n' at the end.
// It returns err if and only if line does not end in '\n'. Note that read
// bytes returned in any case of error.
//
// It is much like the textproto/Reader.ReadLine() except the thing that it
// returns raw bytes, instead of string. That is, it avoids copying bytes read
// from br.
//
// textproto/Reader.ReadLineBytes() is also makes copy of resulting bytes to be
// safe with future I/O operations on br.
//
// We could control I/O operations on br and do not need to make additional
// copy for safety.
func ReadLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		bts, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// Copy bytes because next read will discard them.
			line = append(line, bts...)
			continue
		}
		// Avoid copy of single read.
		if line == nil {
			line = bts
		} else {
			line = append(line, bts...)
		}
		if err != nil {
			return line, err
		}
		// Size of line is at least 1.
		// In other case bufio.ReadSlice() returns error.
		n := len(line)
		// Cut '\n' or '\r\n'.
		if n > 1 && line[n-2] == '\r' {
			line = line[:n-2]
		} else {
			line = line[:n-1]
		}
		return line, nil
	}
}

// ParseHeaderLine parses HTTP header as key-value pair. It returns parsed
// values and true if parse is ok.
func ParseHeaderLine(line []byte) (k, v []byte, ok bool) {
	colon := bytes.IndexByte(line, ':')
	if colon == -1 {
		return
	}
	k = trim(line[:colon])
	for _, c := range k {
		if !OctetTypes[c].IsToken() {
			return nil, nil, false
		}
	}
	v = trim(line[colon+1:])
	return k, v, true
}

// IntFromASCII converts ascii encoded decimal numeric value from HTTP entities
// to an integer.
func IntFromASCII(bts []byte) (ret int, ok bool) {
	// ASCII numbers all start with the high-order bits 0011.
	// If you see that, and the next bits are 0-9 (0000 - 1001) you can grab those
	// bits and interpret them directly as an integer.
	var n int
	if n = len(bts); n < 1 {
		return 0, false
	}
	for i := 0; i < n; i++ {
		if bts[i]&0xf0 != 0x30 {
			return 0, false
		}
		ret += int(bts[i]&0xf) * pow(10, n-i-1)
	}
	return ret, true
}

const (
	toLower = 'a' - 'A'      // for use with OR.
	toUpper = ^byte(toLower) // for use with AND.
)

// CanonicalizeHeaderKey is like standard textproto/CanonicalMIMEHeaderKey,
// except that it operates with slice of bytes and modifies it inplace without
// copying.
func CanonicalizeHeaderKey(k []byte) {
	upper := true
	for i, c := range k {
		if upper && 'a' <= c && c <= 'z' {
			k[i] &= toUpper
		} else if !upper && 'A' <= c && c <= 'Z' {
			k[i] |= toLower
		}
		upper = c == '-'
	}
}

// pow for integers implementation.
// See Donald Knuth, The Art of Computer Programming, Volume 2, Section 4.6.3
func pow(a, b int) int {
	p := 1
	for b > 0 {
		if b&1 != 0 {
			p *= a
		}
		b >>= 1
		a *= a
	}
	return p
}

func split3(p []byte, sep byte) (p1, p2, p3 []byte) {
	a := bytes.IndexByte(p, sep)
	b := bytes.IndexByte(p[a+1:], sep)
	if a == -1 || b == -1 {
		return p, nil, nil
	}
	b += a + 1
	return p[:a], p[a+1 : b], p[b+1:]
}

func split2(p []byte, sep byte) (p1, p2 []byte) {
	i := bytes.IndexByte(p, sep)
	if i == -1 {
		return p, nil
	}
	return p[:i], p[i+1:]
}

func trim(p []byte) []byte {
	var i, j int
	for i = 0; i < len(p) && (p[i] == ' ' || p[i] == '\t'); {
		i++
	}
	for j = len(p); j > i && (p[j-1] == ' ' || p[j-1] == '\t'); {
		j--
	}
	return p[i:j]
}

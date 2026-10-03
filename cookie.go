package httphead

import (
	"bytes"
	"errors"
)

// ScanCookie scans cookie pairs from data using DefaultCookieScanner.Scan()
// method.
func ScanCookie(data []byte, it func(key, value []byte) bool) bool {
	return DefaultCookieScanner.Scan(data, it)
}

// ScanCookieItems scans cookie pairs from data using
// DefaultCookieScanner.ScanItems() method.
func ScanCookieItems(data []byte, it func(CookieItem) bool) CookieScanStatus {
	return DefaultCookieScanner.ScanItems(data, it)
}

// DefaultCookieScanner is a CookieScanner which is used by ScanCookie().
// Note that it is intended to have the same behavior as http.Request.Cookies()
// has.
var DefaultCookieScanner = CookieScanner{}

// Reasons why a cookie pair could be dropped during scanning.
var (
	// ErrCookieEmptyPair reports an empty pair, that is, nothing (or only
	// whitespace) between two semicolons.
	ErrCookieEmptyPair = errors.New("httphead: empty cookie pair")

	// ErrCookieNoValue reports a pair without "=" sign. Such pairs are
	// dropped only in strict mode.
	ErrCookieNoValue = errors.New("httphead: cookie pair without value")

	// ErrCookieInvalidName reports a pair whose name is not a valid
	// RFC2616 token.
	ErrCookieInvalidName = errors.New("httphead: invalid cookie name")

	// ErrCookieInvalidValue reports a pair whose value contains bytes that
	// are not valid RFC6265 "cookie-octet" characters.
	ErrCookieInvalidValue = errors.New("httphead: invalid cookie value")

	// ErrCookieUnclosedQuote reports a pair whose value opens a quote that
	// is never closed within the pair.
	ErrCookieUnclosedQuote = errors.New("httphead: unclosed quote in cookie value")

	// ErrCookieInvalidSeparator reports that pairs are not separated by
	// ";" and space as RFC6265 requires. Such error is reported only in
	// strict mode.
	ErrCookieInvalidSeparator = errors.New("httphead: invalid cookie pair separator")
)

// CookieScanStatus describes how cookie scanning has terminated.
type CookieScanStatus int

const (
	// CookieScanComplete reports that the whole input was consumed. Note
	// that dropped pairs (reported via CookieItem.Err) do not prevent
	// scanning from completing.
	CookieScanComplete CookieScanStatus = iota

	// CookieScanError reports that scanning stopped on the first error,
	// that is, when CookieScanner.BreakOnPairError is set and a pair failed
	// validation, or when a strict mode separator rule was violated.
	CookieScanError

	// CookieScanStopped reports that scanning stopped because the items
	// callback returned false.
	CookieScanStopped
)

// String implements fmt.Stringer.
func (s CookieScanStatus) String() string {
	switch s {
	case CookieScanComplete:
		return "complete"
	case CookieScanError:
		return "error"
	case CookieScanStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// CookieItem represents a single scanned item of a Cookie header value.
type CookieItem struct {
	// Name and Value contain the parsed pair. They are set only when Err
	// is nil.
	Name  []byte
	Value []byte

	// Raw contains the exact bytes of the item within the scanned data,
	// and Offset is the index at which they start. That is,
	// Raw == data[Offset:Offset+len(Raw)]. Raw is set for both valid and
	// dropped items; reported ranges never overlap.
	Raw    []byte
	Offset int

	// Err describes why the item was dropped. It is nil for valid pairs.
	Err error
}

// CookieScanner contains options for scanning cookie pairs.
// See https://tools.ietf.org/html/rfc6265#section-4.1.1
type CookieScanner struct {
	// DisableNameValidation disables name validation of a cookie. If false,
	// only RFC2616 "tokens" are accepted.
	DisableNameValidation bool

	// DisableValueValidation disables value validation of a cookie. If false,
	// only RFC6265 "cookie-octet" characters are accepted.
	//
	// Note that Strict option also affects validation of a value.
	//
	// If Strict is false, then scanner begins to allow space and comma
	// characters inside the value for better compatibility with non standard
	// cookies implementations.
	DisableValueValidation bool

	// BreakOnPairError sets scanner to immediately return after first pair syntax
	// validation error.
	// If false, scanner will try to skip invalid pair bytes and go ahead.
	BreakOnPairError bool

	// Strict enables strict RFC6265 mode scanning. It affects name and value
	// validation, as also some other rules.
	// If false, it is intended to bring the same behavior as
	// http.Request.Cookies().
	Strict bool
}

// Scan maps data to name and value pairs. Usually data represents value of the
// Cookie header.
//
// It reports only valid pairs and returns false on the first pair syntax
// error when BreakOnPairError is set (or a strict mode separator rule is
// violated). Use ScanItems() to also observe dropped pairs and their
// locations.
func (c CookieScanner) Scan(data []byte, it func(name, value []byte) bool) bool {
	return c.ScanItems(data, func(item CookieItem) bool {
		if item.Err != nil {
			return true
		}
		return it(item.Name, item.Value)
	}) != CookieScanError
}

// ScanItems maps data to cookie items, calling it for every pair found,
// including dropped ones. Usually data represents value of the Cookie header.
//
// Pairs are delimited by ";" (followed by a single optional space). A pair
// that fails validation is dropped as a whole: its Raw range extends up to
// the next ";" (or to the end of data), so an "=" inside a bad pair never
// causes the rest of that pair to be scanned as a new cookie. In particular,
// an unclosed quote invalidates only its own pair, and scanning resumes
// after the next ";".
//
// Every item is reported exactly once and reported Raw ranges never overlap.
// If it returns false, scanning stops and CookieScanStopped is returned. If
// BreakOnPairError is set (or a strict mode separator rule is violated),
// scanning stops right after reporting the failed item and CookieScanError
// is returned. Otherwise scanning continues and CookieScanComplete is
// returned once the whole input is consumed.
func (c CookieScanner) ScanItems(data []byte, it func(CookieItem) bool) CookieScanStatus {
	lexer := &Scanner{data: data}

	const (
		statePair = iota
		stateBefore
	)

	state := statePair

	for lexer.Buffered() > 0 {
		switch state {
		case stateBefore:
			// Pairs separated by ";" and space, according to the RFC6265:
			//   cookie-pair *( ";" SP cookie-pair )
			//
			// Cookie pairs MUST be separated by (";" SP). So our only option
			// here is to fail as syntax error.
			a, b := lexer.Peek2()
			if a != ';' {
				return CookieScanError
			}

			state = statePair

			advance := 1
			if b == ' ' {
				advance++
			} else if c.Strict {
				// Report the pair that follows the invalid separator.
				start := lexer.pos + 1
				end := len(data)
				if i := bytes.IndexByte(data[start:], ';'); i != -1 {
					end = start + i
				}
				if !it(CookieItem{
					Raw:    data[start:end],
					Offset: start,
					Err:    ErrCookieInvalidSeparator,
				}) {
					return CookieScanStopped
				}
				return CookieScanError
			}

			lexer.Advance(advance)

		case statePair:
			start := lexer.pos
			if !lexer.FetchUntil(';') {
				return CookieScanError
			}

			item := c.pair(lexer.Bytes())
			item.Offset = start
			if !it(item) {
				return CookieScanStopped
			}
			if item.Err != nil && c.BreakOnPairError {
				return CookieScanError
			}

			state = stateBefore
		}
	}

	return CookieScanComplete
}

// pair classifies a single raw cookie pair bytes (without the trailing ";"
// separator) and returns the corresponding CookieItem.
func (c CookieScanner) pair(raw []byte) CookieItem {
	item := CookieItem{Raw: raw}

	var (
		name  []byte
		value []byte
	)
	hasValue := false
	if i := bytes.IndexByte(raw, '='); i != -1 {
		name, value, hasValue = raw[:i], raw[i+1:], true
	} else {
		name = raw
	}

	if !c.Strict {
		name = trimLeft(name)
	}

	switch {
	case !hasValue && len(name) == 0:
		// Nothing (or only whitespace) between two semicolons.
		item.Err = ErrCookieEmptyPair
		return item
	case !hasValue && c.Strict:
		item.Err = ErrCookieNoValue
		return item
	}

	if !c.DisableNameValidation && !ValidCookieName(name) {
		item.Err = ErrCookieInvalidName
		return item
	}

	if !c.Strict {
		value = trimRight(value)
	}
	value = stripQuotes(value)
	if !c.DisableValueValidation && !ValidCookieValue(value, c.Strict) {
		item.Err = cookieValueError(value)
		return item
	}

	item.Name = name
	item.Value = value
	return item
}

// cookieValueError classifies why given value failed ValidCookieValue()
// check. Note that value must already be unquoted with stripQuotes().
func cookieValueError(value []byte) error {
	if len(value) > 0 && value[0] == '"' {
		return ErrCookieUnclosedQuote
	}
	return ErrCookieInvalidValue
}

// ValidCookieValue reports whether given value is a valid RFC6265
// "cookie-octet" bytes.
//
// cookie-octet = %x21 / %x23-2B / %x2D-3A / %x3C-5B / %x5D-7E
//                ; US-ASCII characters excluding CTLs,
//                ; whitespace DQUOTE, comma, semicolon,
//                ; and backslash
//
// Note that the false strict parameter disables errors on space 0x20 and comma
// 0x2c. This could be useful to bring some compatibility with non-compliant
// clients/servers in the real world.
// It acts the same as standard library cookie parser if strict is false.
func ValidCookieValue(value []byte, strict bool) bool {
	if len(value) == 0 {
		return true
	}
	for _, c := range value {
		switch c {
		case '"', ';', '\\':
			return false
		case ',', ' ':
			if strict {
				return false
			}
		default:
			if c <= 0x20 {
				return false
			}
			if c >= 0x7f {
				return false
			}
		}
	}
	return true
}

// ValidCookieName reports wheter given bytes is a valid RFC2616 "token" bytes.
func ValidCookieName(name []byte) bool {
	for _, c := range name {
		if !OctetTypes[c].IsToken() {
			return false
		}
	}
	return true
}

func stripQuotes(bts []byte) []byte {
	if last := len(bts) - 1; last > 0 && bts[0] == '"' && bts[last] == '"' {
		return bts[1:last]
	}
	return bts
}

func trimLeft(p []byte) []byte {
	var i int
	for i < len(p) && OctetTypes[p[i]].IsSpace() {
		i++
	}
	return p[i:]
}

func trimRight(p []byte) []byte {
	j := len(p)
	for j > 0 && OctetTypes[p[j-1]].IsSpace() {
		j--
	}
	return p[:j]
}

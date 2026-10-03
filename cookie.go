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

// ScanCookieItems scans cookie items from data using
// DefaultCookieScanner.ScanItems() method.
func ScanCookieItems(data []byte, it func(CookieItem) bool) CookieScanStatus {
	return DefaultCookieScanner.ScanItems(data, it)
}

// DefaultCookieScanner is a CookieScanner which is used by ScanCookie().
// Note that it is intended to have the same behavior as http.Request.Cookies()
// has.
var DefaultCookieScanner = CookieScanner{}

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

// CookieScanStatus describes the way cookie scanning has finished.
type CookieScanStatus int

const (
	// CookieScanOK reports that the whole input has been scanned.
	CookieScanOK CookieScanStatus = iota

	// CookieScanError reports that scanning has stopped due to a pair
	// syntax or validation error. It could happen only when
	// CookieScanner.BreakOnPairError is set or when CookieScanner.Strict
	// is set and the input violates the RFC6265 pairs separator rule.
	CookieScanError

	// CookieScanBreak reports that scanning has stopped because the items
	// callback has returned false.
	CookieScanBreak
)

var (
	// ErrCookieNoValue reports that a cookie pair has no "=" and thus no
	// value. It is reported only in strict mode; otherwise such pair is
	// treated as a pair with empty value.
	ErrCookieNoValue = errors.New("httphead: cookie pair has no value")

	// ErrCookieInvalidName reports that a cookie pair has invalid name.
	ErrCookieInvalidName = errors.New("httphead: invalid cookie name")

	// ErrCookieInvalidValue reports that a cookie pair has invalid value.
	ErrCookieInvalidValue = errors.New("httphead: invalid cookie value")

	// ErrCookieUnclosedQuote reports that a cookie value has unclosed
	// quote.
	ErrCookieUnclosedQuote = errors.New("httphead: unclosed cookie value quote")

	// ErrCookieSeparator reports that cookie pairs are not separated
	// according to the RFC6265 ";" SP rule. It is reported only in strict
	// mode.
	ErrCookieSeparator = errors.New("httphead: invalid cookie pairs separator")
)

// CookieItem represents a single scanned cookie item — either a valid
// name-value pair or a dropped fragment of the input.
type CookieItem struct {
	// Offset is the offset of the item within the scanned input.
	Offset int

	// Raw contains raw bytes of the item as they appeared in the input,
	// excluding the ";" separator.
	Raw []byte

	// Name and Value contain parsed cookie pair bytes. They are nil when
	// Err is non-nil.
	Name  []byte
	Value []byte

	// Err describes the reason why the item was dropped. It is nil for
	// valid cookie pairs.
	Err error
}

// ScanItems maps data to cookie items, calling it for every scanned item —
// both valid pairs and dropped fragments. Usually data represents value of
// the Cookie header.
//
// The input is split into items by ";" separators, so a dropped item always
// covers the whole fragment between two separators, even if it contains "="
// bytes. Every item is reported to the callback exactly once and the
// reported Raw byte ranges never overlap.
//
// If an item can not be parsed or validated, it is reported with non-nil Err
// and scanning goes on, unless BreakOnPairError option is set — in that case
// scanning stops and CookieScanError is returned. In strict mode a separator
// that does not follow the RFC6265 ";" SP rule also stops scanning with
// CookieScanError.
//
// If the callback returns false, scanning stops and CookieScanBreak is
// returned.
func (c CookieScanner) ScanItems(data []byte, it func(CookieItem) bool) CookieScanStatus {
	for pos := 0; pos < len(data); {
		end := len(data)
		if i := bytes.IndexByte(data[pos:], ';'); i != -1 {
			end = pos + i
		}

		item := c.scanPair(data[pos:end])
		item.Offset = pos
		item.Raw = data[pos:end]
		if !it(item) {
			return CookieScanBreak
		}
		if item.Err != nil && c.BreakOnPairError {
			return CookieScanError
		}

		sep := end
		pos = end + 1 // Skip ';'.
		if pos < len(data) && data[pos] == ' ' {
			pos++
		} else if c.Strict && sep < len(data) {
			// RFC6265 requires cookie pairs to be separated by ";" SP.
			// Thus ";" followed by any other byte or by the end of data
			// is a syntax error.
			if !it(CookieItem{
				Offset: sep,
				Raw:    data[sep:],
				Err:    ErrCookieSeparator,
			}) {
				return CookieScanBreak
			}
			return CookieScanError
		}
	}
	return CookieScanOK
}

// Scan maps data to name and value pairs. Usually data represents value of the
// Cookie header.
//
// It reports only valid cookie pairs, silently skipping dropped items. To
// inspect dropped items use ScanItems().
func (c CookieScanner) Scan(data []byte, it func(name, value []byte) bool) bool {
	return c.ScanItems(data, func(item CookieItem) bool {
		if item.Err != nil {
			return true
		}
		return it(item.Name, item.Value)
	}) != CookieScanError
}

func (c CookieScanner) scanPair(pair []byte) (item CookieItem) {
	name := pair
	var value []byte
	if i := bytes.IndexByte(pair, '='); i != -1 {
		name, value = pair[:i], pair[i+1:]
	} else if c.Strict {
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
		item.Err = ErrCookieInvalidValue
		if n := len(value); n > 0 && value[0] == '"' && (n == 1 || value[n-1] != '"') {
			item.Err = ErrCookieUnclosedQuote
		}
		return item
	}

	item.Name = name
	item.Value = value
	return item
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

func trimRight(p []byte) []byte {
	j := len(p)
	for j > 0 && OctetTypes[p[j-1]].IsSpace() {
		j--
	}
	return p[:j]
}

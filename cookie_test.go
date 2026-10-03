package httphead

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
)

type cookieTuple struct {
	name, value []byte
}

var cookieCases = []struct {
	label string
	in    []byte
	ok    bool
	exp   []cookieTuple

	c CookieScanner
}{
	{
		label: "simple",
		in:    []byte(`foo=bar`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
	},
	{
		label: "simple",
		in:    []byte(`foo=bar; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
			{[]byte(`bar`), []byte(`baz`)},
		},
	},
	{
		label: "duplicate",
		in:    []byte(`foo=bar; bar=baz; foo=bar`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
			{[]byte(`bar`), []byte(`baz`)},
			{[]byte(`foo`), []byte(`bar`)},
		},
	},
	{
		label: "quoted",
		in:    []byte(`foo="bar"`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
	},
	{
		label: "empty value",
		in:    []byte(`foo=`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte{}},
		},
	},
	{
		label: "empty value",
		in:    []byte(`foo=; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte{}},
			{[]byte(`bar`), []byte(`baz`)},
		},
	},
	{
		label: "quote as value",
		in:    []byte(`foo="; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte{'"'}},
			{[]byte(`bar`), []byte(`baz`)},
		},
		c: CookieScanner{
			DisableValueValidation: true,
		},
	},
	{
		label: "quote as value",
		in:    []byte(`foo="; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`bar`), []byte(`baz`)},
		},
	},
	{
		label: "skip invalid key",
		in:    []byte(`foo@example.com=1; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte("bar"), []byte("baz")},
		},
	},
	{
		label: "skip invalid value",
		in:    []byte(`foo="1; bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte("bar"), []byte("baz")},
		},
	},
	{
		label: "trailing semicolon",
		in:    []byte(`foo=bar;`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
	},
	{
		label: "trailing semicolon strict",
		in:    []byte(`foo=bar;`),
		ok:    false,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label: "want space between",
		in:    []byte(`foo=bar;bar=baz`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
			{[]byte(`bar`), []byte(`baz`)},
		},
	},
	{
		label: "want space between strict",
		in:    []byte(`foo=bar;bar=baz`),
		ok:    false,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label: "value single dquote",
		in:    []byte(`foo="bar`),
		ok:    true,
	},
	{
		label: "value single dquote",
		in:    []byte(`foo=bar"`),
		ok:    true,
	},
	{
		label: "value single dquote",
		in:    []byte(`foo="bar`),
		ok:    false,
		c: CookieScanner{
			BreakOnPairError: true,
		},
	},
	{
		label: "value single dquote",
		in:    []byte(`foo=bar"`),
		ok:    false,
		c: CookieScanner{
			BreakOnPairError: true,
		},
	},
	{
		label: "value whitespace",
		in:    []byte(`foo=bar `),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`bar`)},
		},
	},
	{
		label: "value whitespace strict",
		in:    []byte(`foo=bar `),
		ok:    false,
		c: CookieScanner{
			Strict:           true,
			BreakOnPairError: true,
		},
	},
	{
		label: "value whitespace",
		in:    []byte(`foo=b ar`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`b ar`)},
		},
	},
	{
		label: "value whitespace strict",
		in:    []byte(`foo=b ar`),
		ok:    false,
		c: CookieScanner{
			Strict:           true,
			BreakOnPairError: true,
		},
	},
	{
		label: "value whitespace strict",
		in:    []byte(`foo= bar`),
		ok:    false,
		c: CookieScanner{
			Strict:           true,
			BreakOnPairError: true,
		},
	},
	{
		label: "value quoted whitespace",
		in:    []byte(`foo="b ar"`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(`b ar`)},
		},
	},
	{
		label: "value quoted whitespace strict",
		in:    []byte(`foo="b ar"`),
		c: CookieScanner{
			Strict:           true,
			BreakOnPairError: true,
		},
	},
	{
		label: "parse ok without values",
		in:    []byte(`foo;bar;baz=10`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(``)},
			{[]byte(`bar`), []byte(``)},
			{[]byte(`baz`), []byte(`10`)},
		},
		c: CookieScanner{
			Strict: false,
		},
	},
	{
		label: "strict parse ok without values",
		in:    []byte(`foo; bar; baz=10`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`baz`), []byte(`10`)},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label: "parse ok without values",
		in:    []byte(`foo;`),
		ok:    true,
		exp: []cookieTuple{
			{[]byte(`foo`), []byte(``)},
		},
		c: CookieScanner{
			Strict: false,
		},
	},
	{
		label: "strict parse err without values",
		in:    []byte(`foo;`),
		ok:    false,
		exp:   []cookieTuple{},
		c: CookieScanner{
			Strict: true,
		},
	},
}

func TestScanCookie(t *testing.T) {
	for _, test := range cookieCases {
		t.Run(test.label, func(t *testing.T) {
			var act []cookieTuple

			ok := test.c.Scan(test.in, func(k, v []byte) bool {
				act = append(act, cookieTuple{k, v})
				return true
			})
			if ok != test.ok {
				t.Errorf("unexpected result: %v; want %v", ok, test.ok)
			}
			if an, en := len(act), len(test.exp); an != en {
				t.Errorf("unexpected length of result: %d; want %d", an, en)
			} else {
				for i, ev := range test.exp {
					if av := act[i]; !bytes.Equal(av.name, ev.name) || !bytes.Equal(av.value, ev.value) {
						t.Errorf(
							"unexpected %d-th tuple: %#q=%#q; want %#q=%#q", i,
							string(av.name), string(av.value),
							string(ev.name), string(ev.value),
						)
					}
				}
			}

			if test.c != DefaultCookieScanner {
				return
			}

			// Compare with standard library.
			req := http.Request{
				Header: http.Header{
					"Cookie": []string{string(test.in)},
				},
			}
			std := req.Cookies()
			if an, sn := len(act), len(std); an != sn {
				t.Errorf("length of result: %d; standard lib returns %d; details:\n%s", an, sn, dumpActStd(act, std))
			} else {
				for i := 0; i < an; i++ {
					if a, s := act[i], std[i]; string(a.name) != s.Name || string(a.value) != s.Value {
						t.Errorf("%d-th cookie not equal:\n%s", i, dumpActStd(act, std))
						break
					}
				}
			}
		})
	}
}

var cookieItemCases = []struct {
	label  string
	in     []byte
	status CookieScanStatus
	items  []CookieItem
	brk    bool

	c CookieScanner
}{
	{
		label:  "dropped invalid name",
		in:     []byte(`foo@example.com=1; bar=baz`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo@example.com=1`), Err: ErrCookieInvalidName},
			{Offset: 19, Raw: []byte(`bar=baz`), Name: []byte(`bar`), Value: []byte(`baz`)},
		},
	},
	{
		label:  "dropped unclosed quote",
		in:     []byte(`foo="bar; baz=1`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo="bar`), Err: ErrCookieUnclosedQuote},
			{Offset: 10, Raw: []byte(`baz=1`), Name: []byte(`baz`), Value: []byte(`1`)},
		},
	},
	{
		label:  "dropped invalid value",
		in:     []byte(`foo=bar"; ok=1`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo=bar"`), Err: ErrCookieInvalidValue},
			{Offset: 10, Raw: []byte(`ok=1`), Name: []byte(`ok`), Value: []byte(`1`)},
		},
	},
	{
		label:  "equals inside dropped pair",
		in:     []byte(`bad name=x=y; good=1`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`bad name=x=y`), Err: ErrCookieInvalidName},
			{Offset: 14, Raw: []byte(`good=1`), Name: []byte(`good`), Value: []byte(`1`)},
		},
	},
	{
		label:  "break on pair error",
		in:     []byte(`foo="1; bar=baz`),
		status: CookieScanError,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo="1`), Err: ErrCookieUnclosedQuote},
		},
		c: CookieScanner{
			BreakOnPairError: true,
		},
	},
	{
		label:  "strict missing value",
		in:     []byte(`foo; bar=baz`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo`), Err: ErrCookieNoValue},
			{Offset: 5, Raw: []byte(`bar=baz`), Name: []byte(`bar`), Value: []byte(`baz`)},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label:  "strict separator error",
		in:     []byte(`foo=bar;bar=baz`),
		status: CookieScanError,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo=bar`), Name: []byte(`foo`), Value: []byte(`bar`)},
			{Offset: 7, Raw: []byte(`;bar=baz`), Err: ErrCookieSeparator},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label:  "lenient pairs without values",
		in:     []byte(`foo;bar`),
		status: CookieScanOK,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`foo`), Name: []byte(`foo`)},
			{Offset: 4, Raw: []byte(`bar`), Name: []byte(`bar`)},
		},
	},
	{
		label:  "callback break",
		in:     []byte(`a=1; b=2`),
		status: CookieScanBreak,
		items: []CookieItem{
			{Offset: 0, Raw: []byte(`a=1`), Name: []byte(`a`), Value: []byte(`1`)},
		},
		brk: true,
	},
}

func TestScanCookieItems(t *testing.T) {
	for _, test := range cookieItemCases {
		t.Run(test.label, func(t *testing.T) {
			var act []CookieItem

			status := test.c.ScanItems(test.in, func(item CookieItem) bool {
				act = append(act, item)
				return !test.brk
			})
			if status != test.status {
				t.Errorf("unexpected status: %v; want %v", status, test.status)
			}
			if an, en := len(act), len(test.items); an != en {
				t.Fatalf("unexpected number of items: %d; want %d", an, en)
			}
			for i, exp := range test.items {
				av := act[i]
				if av.Offset != exp.Offset ||
					!bytes.Equal(av.Raw, exp.Raw) ||
					!bytes.Equal(av.Name, exp.Name) ||
					!bytes.Equal(av.Value, exp.Value) ||
					av.Err != exp.Err {
					t.Errorf(
						"unexpected %d-th item: %+v; want %+v", i,
						av, exp,
					)
				}
			}
			for i := 1; i < len(act); i++ {
				prev, cur := act[i-1], act[i]
				if prev.Offset+len(prev.Raw) > cur.Offset {
					t.Errorf(
						"items %d and %d report overlapping byte ranges: %+v and %+v",
						i-1, i, prev, cur,
					)
				}
			}
		})
	}
}

func BenchmarkScanCookie(b *testing.B) {
	for _, test := range cookieCases {
		b.Run(test.label, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				test.c.Scan(test.in, func(_, _ []byte) bool {
					return true
				})
			}
		})
		if test.c == DefaultCookieScanner {
			b.Run(test.label+"_std", func(b *testing.B) {
				r := http.Request{
					Header: http.Header{
						"Cookie": []string{string(test.in)},
					},
				}
				for i := 0; i < b.N; i++ {
					_ = r.Cookies()
				}
			})
		}
	}
}

func dumpActStd(act []cookieTuple, std []*http.Cookie) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "actual:\n")
	for i, p := range act {
		fmt.Fprintf(&buf, "\t#%d: %#q=%#q\n", i, p.name, p.value)
	}
	fmt.Fprintf(&buf, "standard:\n")
	for i, c := range std {
		fmt.Fprintf(&buf, "\t#%d: %#q=%#q\n", i, c.Name, c.Value)
	}
	return buf.String()
}

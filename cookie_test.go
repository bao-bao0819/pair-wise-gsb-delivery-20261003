package httphead

import (
	"bytes"
	"errors"
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

type cookieItemTuple struct {
	name, value []byte
	raw         []byte
	offset      int
	err         error
}

var cookieItemCases = []struct {
	label  string
	in     []byte
	status CookieScanStatus
	exp    []cookieItemTuple

	c CookieScanner
}{
	{
		label:  "lenient recovery",
		in:     []byte(`foo=bar;bad@=x;;qux="oops;last=1`),
		status: CookieScanComplete,
		exp: []cookieItemTuple{
			{name: []byte(`foo`), value: []byte(`bar`), raw: []byte(`foo=bar`), offset: 0},
			{raw: []byte(`bad@=x`), offset: 8, err: ErrCookieInvalidName},
			{raw: []byte(``), offset: 15, err: ErrCookieEmptyPair},
			{raw: []byte(`qux="oops`), offset: 16, err: ErrCookieUnclosedQuote},
			{name: []byte(`last`), value: []byte(`1`), raw: []byte(`last=1`), offset: 26},
		},
	},
	{
		label:  "bad pair equal sign is not a new cookie",
		in:     []byte(`bad name=x=y; good=1`),
		status: CookieScanComplete,
		exp: []cookieItemTuple{
			{raw: []byte(`bad name=x=y`), offset: 0, err: ErrCookieInvalidName},
			{name: []byte(`good`), value: []byte(`1`), raw: []byte(`good=1`), offset: 14},
		},
	},
	{
		label:  "break on pair error",
		in:     []byte(`foo=bar;bad name=1;baz=2`),
		status: CookieScanError,
		exp: []cookieItemTuple{
			{name: []byte(`foo`), value: []byte(`bar`), raw: []byte(`foo=bar`), offset: 0},
			{raw: []byte(`bad name=1`), offset: 8, err: ErrCookieInvalidName},
		},
		c: CookieScanner{
			BreakOnPairError: true,
		},
	},
	{
		label:  "strict separator without space",
		in:     []byte(`foo=bar;bar=baz`),
		status: CookieScanError,
		exp: []cookieItemTuple{
			{name: []byte(`foo`), value: []byte(`bar`), raw: []byte(`foo=bar`), offset: 0},
			{raw: []byte(`bar=baz`), offset: 8, err: ErrCookieInvalidSeparator},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label:  "strict trailing semicolon",
		in:     []byte(`foo=bar;`),
		status: CookieScanError,
		exp: []cookieItemTuple{
			{name: []byte(`foo`), value: []byte(`bar`), raw: []byte(`foo=bar`), offset: 0},
			{raw: []byte(``), offset: 8, err: ErrCookieInvalidSeparator},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label:  "strict pairs without values",
		in:     []byte(`foo; bar; baz=10`),
		status: CookieScanComplete,
		exp: []cookieItemTuple{
			{raw: []byte(`foo`), offset: 0, err: ErrCookieNoValue},
			{raw: []byte(`bar`), offset: 5, err: ErrCookieNoValue},
			{name: []byte(`baz`), value: []byte(`10`), raw: []byte(`baz=10`), offset: 10},
		},
		c: CookieScanner{
			Strict: true,
		},
	},
	{
		label:  "stray quote",
		in:     []byte(`foo=bar"; ok=1`),
		status: CookieScanComplete,
		exp: []cookieItemTuple{
			{raw: []byte(`foo=bar"`), offset: 0, err: ErrCookieInvalidValue},
			{name: []byte(`ok`), value: []byte(`1`), raw: []byte(`ok=1`), offset: 10},
		},
	},
}

func TestScanCookieItems(t *testing.T) {
	for _, test := range cookieItemCases {
		t.Run(test.label, func(t *testing.T) {
			var act []cookieItemTuple

			status := test.c.ScanItems(test.in, func(item CookieItem) bool {
				act = append(act, cookieItemTuple{
					name:   item.Name,
					value:  item.Value,
					raw:    item.Raw,
					offset: item.Offset,
					err:    item.Err,
				})
				return true
			})
			if status != test.status {
				t.Errorf("unexpected status: %v; want %v", status, test.status)
			}
			if an, en := len(act), len(test.exp); an != en {
				t.Fatalf("unexpected number of items: %d; want %d", an, en)
			}
			for i, ev := range test.exp {
				av := act[i]
				switch {
				case !bytes.Equal(av.name, ev.name):
					t.Errorf("unexpected %d-th item name: %#q; want %#q", i, av.name, ev.name)
				case !bytes.Equal(av.value, ev.value):
					t.Errorf("unexpected %d-th item value: %#q; want %#q", i, av.value, ev.value)
				case !bytes.Equal(av.raw, ev.raw):
					t.Errorf("unexpected %d-th item raw: %#q; want %#q", i, av.raw, ev.raw)
				case av.offset != ev.offset:
					t.Errorf("unexpected %d-th item offset: %d; want %d", i, av.offset, ev.offset)
				case !errors.Is(av.err, ev.err):
					t.Errorf("unexpected %d-th item error: %v; want %v", i, av.err, ev.err)
				}
				if end := av.offset + len(av.raw); end > len(test.in) || av.offset < 0 {
					t.Errorf("%d-th item range [%d:%d] is out of bounds", i, av.offset, end)
				} else if !bytes.Equal(test.in[av.offset:end], av.raw) {
					t.Errorf("%d-th item raw range mismatch", i)
				}
				if i > 0 {
					if prev := act[i-1].offset + len(act[i-1].raw); av.offset < prev {
						t.Errorf("%d-th item range overlaps with previous one", i)
					}
				}
			}
		})
	}
}

func TestScanCookieItemsStop(t *testing.T) {
	in := []byte(`a=1;b=2;c=3`)

	var n int
	status := ScanCookieItems(in, func(item CookieItem) bool {
		n++
		return false
	})
	if status != CookieScanStopped {
		t.Errorf("unexpected status: %v; want %v", status, CookieScanStopped)
	}
	if n != 1 {
		t.Errorf("unexpected number of items: %d; want 1", n)
	}
}

func TestScanCookieItemsConsistentWithScan(t *testing.T) {
	for _, test := range cookieCases {
		t.Run(test.label, func(t *testing.T) {
			var pairs []cookieTuple
			ok := test.c.Scan(test.in, func(k, v []byte) bool {
				pairs = append(pairs, cookieTuple{k, v})
				return true
			})

			var valid []cookieTuple
			status := test.c.ScanItems(test.in, func(item CookieItem) bool {
				if item.Err == nil {
					valid = append(valid, cookieTuple{item.Name, item.Value})
				}
				return true
			})

			if got, want := status != CookieScanError, ok; got != want {
				t.Errorf("unexpected result: %v; want %v", got, want)
			}
			if an, en := len(valid), len(pairs); an != en {
				t.Fatalf("unexpected number of valid items: %d; want %d", an, en)
			}
			for i, ev := range pairs {
				if av := valid[i]; !bytes.Equal(av.name, ev.name) || !bytes.Equal(av.value, ev.value) {
					t.Errorf(
						"unexpected %d-th tuple: %#q=%#q; want %#q=%#q", i,
						string(av.name), string(av.value),
						string(ev.name), string(ev.value),
					)
				}
			}
		})
	}
}

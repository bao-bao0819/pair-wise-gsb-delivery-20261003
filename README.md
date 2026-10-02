# httphead.[go](https://golang.org)

[![GoDoc][godoc-image]][godoc-url] 

> Tiny HTTP header value parsing library in go.

## Overview

This library contains low-level functions for scanning HTTP RFC2616 compatible header value grammars.

## Install

```shell
    go get github.com/gobwas/httphead
```

## Example

The example below shows how multiple-choise HTTP header value could be parsed with this library:

```go
	options, ok := httphead.ParseOptions([]byte(`foo;bar=1,baz`), nil)
	fmt.Println(options, ok)
	// Output: [{foo map[bar:1]} {baz map[]}] true
```

The low-level example below shows how to optimize keys skipping and selection
of some key:

```go
	// The right part of full header line like:
	// X-My-Header: key;foo=bar;baz,key;baz
	header := []byte(`foo;a=0,foo;a=1,foo;a=2,foo;a=3`)

	// We want to search key "foo" with an "a" parameter that equal to "2".
	var (
		foo = []byte(`foo`)
		a   = []byte(`a`)
		v   = []byte(`2`)
	)
	var found bool
	httphead.ScanOptions(header, func(i int, key, param, value []byte) Control {
		if !bytes.Equal(key, foo) {
			return ControlSkip
		}
		if !bytes.Equal(param, a) {
			if bytes.Equal(value, v) {
				// Found it!
				found = true
				return ControlBreak
			}
			return ControlSkip
		}
		return ControlContinue
	})
```

For more usage examples please see [docs][godoc-url] or package tests.

## Order preserving parameters view

`ParseOptions` (and `Parameters.Set`) keeps every parameter occurrence, but
does not distinguish a parameter without a value from an empty quoted one.
Use `ParseOptionViews` when an upper layer protocol needs to inspect repeated
parameters and their exact textual form:

```go
	options, ok := httphead.ParseOptionViews(
		[]byte(`foo;a;b="";c="x;y";c=2`), nil,
	)
	// Each Parameter carries a ValueKind:
	// httphead.ValueNone   – "a" has no value;
	// httphead.ValueQuoted – "b" is an empty quoted string,
	//                        "c" is a quoted string containing a semicolon;
	// httphead.ValueToken  – "2" is an ordinary token.
	// Occurrences are preserved in source order, so repeated "c" is visible.

	// Explicit single value projection.
	v, found, conflict := options[0].Parameters.Project("c", httphead.ProjectOptions{
		Duplicates: httphead.DuplicateReject, // or DuplicateFirst / DuplicateLast.
		FoldNames:  true,                     // case insensitive name comparison.
	})
```

`WriteOptionViews` writes the view back preserving quoted values (a semicolon
inside quotes never becomes a separator), and `OptionView.Clone` returns a deep
copy that does not retain the input buffer. The legacy `ParseOptions` and
`Parameters.Get` remain unchanged.

[godoc-image]: https://godoc.org/github.com/gobwas/httphead?status.svg
[godoc-url]: https://godoc.org/github.com/gobwas/httphead
[travis-image]: https://travis-ci.org/gobwas/httphead.svg?branch=master
[travis-url]: https://travis-ci.org/gobwas/httphead

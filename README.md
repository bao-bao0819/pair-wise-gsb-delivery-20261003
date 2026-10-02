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

The ordered parameter view keeps every parameter occurrence (it does not fold
same names) and distinguishes a bare parameter, an empty quoted value and a
regular token value. It also offers explicit single-value projections:

```go
	options, ok := httphead.ParseViewOptions(
		[]byte(`foo;a=1;a=2;b="";c`),
		httphead.ParseConfig{},
	)
	// Detect conflicting duplicate parameters.
	single, err := options[0].Params.Project(httphead.ProjectOptions{
		Duplicate: httphead.DuplicateReject, // or DuplicateFirst / DuplicateLast
		FoldName:  true,
	})
	// options[0].Params.All() reports ParamBare, ParamEmpty or ParamToken for
	// each occurrence in parse order.
	_ = ok
	_ = single
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

[godoc-image]: https://godoc.org/github.com/gobwas/httphead?status.svg
[godoc-url]: https://godoc.org/github.com/gobwas/httphead
[travis-image]: https://travis-ci.org/gobwas/httphead.svg?branch=master
[travis-url]: https://travis-ci.org/gobwas/httphead

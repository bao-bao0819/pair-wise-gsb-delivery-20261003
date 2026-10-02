Gzip Handler
============

This is a tiny Go package which wraps HTTP handlers to transparently gzip the
response body, for clients which support it. Although it's usually simpler to
leave that to a reverse proxy (like nginx or Varnish), this package is useful
when that's undesirable.

## Install
```bash
go get -u github.com/NYTimes/gziphandler
```

## Usage

Call `GzipHandler` with any handler (an object which implements the
`http.Handler` interface), and it'll return a new handler which gzips the
response. For example:

```go
package main

import (
	"io"
	"net/http"
	"github.com/NYTimes/gziphandler"
)

func main() {
	withoutGz := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "Hello, World")
	})

	withGz := gziphandler.GzipHandler(withoutGz)

http.Handle("/", withGz)
http.ListenAndServe("0.0.0.0:8000", nil)
}
```

### ETag handling

Compression changes the response bytes, so a strong `ETag` validator set by
the wrapped handler no longer matches the encoded representation. Use
`GzipHandlerWithOpts` to choose how the `ETag` header is rewritten when a
response is actually compressed:

- `RemoveETag()` deletes the `ETag` header.
- `WeakenETag()` rewrites a strong tag to its weak form (`W/"..."`). Tags
  that are already weak are left untouched; headers with multiple values or
  invalid entity-tags are removed.
- `ETagFunc(fn)` calls `fn` with the original tag and the content coding
  (e.g. `"gzip"`) so the caller can compute a new validator; returning an
  empty string removes the header.

Responses passed through uncompressed always keep their original `ETag`
header. By default the header is left as-is.


## Documentation

The docs can be found at [godoc.org][docs], as usual.


## License

[Apache 2.0][license].




[docs]:     https://godoc.org/github.com/NYTimes/gziphandler
[license]:  https://github.com/NYTimes/gziphandler/blob/master/LICENSE

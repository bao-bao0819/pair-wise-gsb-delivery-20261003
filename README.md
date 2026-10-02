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

## ETags

Gzip changes the response representation, so a strong ETag cannot remain
unchanged when compression is applied. Configure an ETag policy with
`GzipHandlerWithOpts`:

```go
// Remove strong ETags, but preserve weak ETags.
wrapper, _ := gziphandler.GzipHandlerWithOpts(gziphandler.ETagPolicy(gziphandler.DeleteStrongETag))

// Convert a strong ETag such as "v1" into W/"v1".
wrapper, _ := gziphandler.GzipHandlerWithOpts(gziphandler.ETagPolicy(gziphandler.WeakenStrongETag))

// Generate a representation-specific ETag. The encoding is "gzip".
wrapper, _ := gziphandler.GzipHandlerWithOpts(gziphandler.ETagPolicy(func(tag, encoding string) string {
    return `"` + encoding + `-version"`
}))
```

The policy runs only when a response is actually compressed. HEAD responses,
204 and 304 responses, already-encoded responses, and responses below the
minimum size remain unchanged. Multiple or malformed ETag values are removed
before compressed headers are committed.


## Documentation

The docs can be found at [godoc.org][docs], as usual.


## License

[Apache 2.0][license].




[docs]:     https://godoc.org/github.com/NYTimes/gziphandler
[license]:  https://github.com/NYTimes/gziphandler/blob/master/LICENSE

package secure

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

// headerSnapshot captures the state of the response headers at the moment a
// final WriteHeader call reaches the underlying writer.
type headerSnapshot struct {
	statusCode int
	header     http.Header
}

// orderedResponseWriter is an http.ResponseWriter that records the order of
// WriteHeader/Write calls and snapshots the headers at each WriteHeader.
type orderedResponseWriter struct {
	header    http.Header
	events    []string
	snapshots []headerSnapshot
}

func newOrderedResponseWriter() *orderedResponseWriter {
	return &orderedResponseWriter{header: make(http.Header)}
}

func (w *orderedResponseWriter) Header() http.Header {
	return w.header
}

func (w *orderedResponseWriter) WriteHeader(statusCode int) {
	w.events = append(w.events, fmt.Sprintf("WriteHeader(%d)", statusCode))
	w.snapshots = append(w.snapshots, headerSnapshot{statusCode: statusCode, header: w.header.Clone()})
}

func (w *orderedResponseWriter) Write(b []byte) (int, error) {
	w.events = append(w.events, fmt.Sprintf("Write(%d)", len(b)))

	return len(b), nil
}

// orderedFlushWriter adds http.Flusher support.
type orderedFlushWriter struct {
	*orderedResponseWriter
}

func (w *orderedFlushWriter) Flush() {
	w.events = append(w.events, "Flush")
}

// orderedHijackWriter adds http.Hijacker support.
type orderedHijackWriter struct {
	*orderedResponseWriter
}

func (w *orderedHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.events = append(w.events, "Hijack")

	return nil, nil, nil
}

// orderedFlushHijackWriter adds both http.Flusher and http.Hijacker support.
type orderedFlushHijackWriter struct {
	*orderedResponseWriter
}

func (w *orderedFlushHijackWriter) Flush() {
	w.events = append(w.events, "Flush")
}

func (w *orderedFlushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.events = append(w.events, "Hijack")

	return nil, nil, nil
}

func commitRequest() *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/foo", nil)
	req.Host = exampleHost

	return req
}

func commitOptions() Options {
	return Options{
		FrameDeny:          true,
		ContentTypeNosniff: true,
		HeadersCommitMode:  true,
	}
}

func TestCommitModeImplicit200(t *testing.T) {
	s := New(commitOptions())

	res := newOrderedResponseWriter()
	s.Handler(myHandler).ServeHTTP(res, commitRequest())

	expect(t, strings.Join(res.events, ","), "WriteHeader(200),Write(3)")
	expect(t, len(res.snapshots), 1)
	// The security headers must be in place when the implicit 200 is committed.
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
	expect(t, res.snapshots[0].header.Get(contentTypeHeader), contentTypeValue)
}

func TestCommitModeOverridesDownstreamAtCommit(t *testing.T) {
	s := New(commitOptions())

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The downstream handler sets its own value before committing.
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		w.WriteHeader(http.StatusCreated)
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, strings.Join(res.events, ","), "WriteHeader(201)")
	expect(t, len(res.snapshots), 1)
	// The default policy overrides the downstream value at commit time.
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
	expect(t, res.Header().Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeKeepPolicy(t *testing.T) {
	opt := commitOptions()
	opt.HeaderConflictPolicies = map[string]HeaderConflictPolicy{
		"x-frame-options": HeaderConflictKeep, // canonicalization is exercised here
	}
	s := New(opt)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		_, _ = w.Write([]byte("bar"))
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, res.Header().Get(frameOptionsHeader), "SAMEORIGIN")
	// Headers without a downstream value still get the middleware value.
	expect(t, res.Header().Get(contentTypeHeader), contentTypeValue)
}

func TestCommitModeErrorPolicy(t *testing.T) {
	type conflict struct {
		header, downstream, middleware string
	}

	var conflicts []conflict

	opt := commitOptions()
	opt.HeaderConflictPolicies = map[string]HeaderConflictPolicy{
		frameOptionsHeader: HeaderConflictError,
	}
	opt.HeaderConflictHandler = func(_ *http.Request, header, downstreamValue, middlewareValue string) {
		conflicts = append(conflicts, conflict{header, downstreamValue, middlewareValue})
	}
	s := New(opt)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		_, _ = w.Write([]byte("bar"))
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	// The conflict is reported and the downstream value is kept.
	expect(t, len(conflicts), 1)
	expect(t, conflicts[0].header, frameOptionsHeader)
	expect(t, conflicts[0].downstream, "SAMEORIGIN")
	expect(t, conflicts[0].middleware, frameOptionsValue)
	expect(t, res.Header().Get(frameOptionsHeader), "SAMEORIGIN")
}

func TestCommitModeErrorPolicyNoConflict(t *testing.T) {
	called := false

	opt := commitOptions()
	opt.HeaderConflictPolicies = map[string]HeaderConflictPolicy{
		frameOptionsHeader: HeaderConflictError,
	}
	opt.HeaderConflictHandler = func(_ *http.Request, _, _, _ string) {
		called = true
	}
	s := New(opt)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Same value as the middleware: no conflict.
		w.Header().Set(frameOptionsHeader, frameOptionsValue)
		_, _ = w.Write([]byte("bar"))
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, called, false)
	expect(t, res.Header().Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeMultipleWriteHeader(t *testing.T) {
	s := New(commitOptions())

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Mutating the headers after the commit must not be reprocessed.
		w.Header().Set(frameOptionsHeader, "AFTER-COMMIT")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("bar"))
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, strings.Join(res.events, ","), "WriteHeader(200),WriteHeader(500),Write(3)")
	expect(t, len(res.snapshots), 2)
	// Headers are applied exactly once, at the first final WriteHeader.
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
	// The second WriteHeader is passed through without reapplying headers.
	expect(t, res.snapshots[1].header.Get(frameOptionsHeader), "AFTER-COMMIT")
	expect(t, res.Header().Get(frameOptionsHeader), "AFTER-COMMIT")
}

func TestCommitModeInformationalStatus(t *testing.T) {
	s := New(commitOptions())

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload; as=style")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, strings.Join(res.events, ","), "WriteHeader(103),WriteHeader(200)")
	expect(t, len(res.snapshots), 2)
	// Informational responses do not carry the security headers.
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), "")
	// The final commit applies them.
	expect(t, res.snapshots[1].header.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeSwitchingProtocolsCommits(t *testing.T) {
	s := New(commitOptions())

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		w.WriteHeader(http.StatusOK)
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, len(res.snapshots), 2)
	// 101 finalizes the response, so the headers are committed with it.
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeFlush(t *testing.T) {
	s := New(commitOptions())

	var isFlusher bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		isFlusher = ok
		if ok {
			flusher.Flush()
		}
	})

	res := &orderedFlushWriter{newOrderedResponseWriter()}
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, isFlusher, true)
	// Flush commits an implicit 200 (with the security headers) before flushing.
	expect(t, strings.Join(res.events, ","), "WriteHeader(200),Flush")
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeFlushNotExposed(t *testing.T) {
	s := New(commitOptions())

	var isFlusher bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, isFlusher = w.(http.Flusher)
		_, _ = w.Write([]byte("bar"))
	})

	// The underlying writer does not support Flush, so neither must the wrapper.
	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, isFlusher, false)
	expect(t, strings.Join(res.events, ","), "WriteHeader(200),Write(3)")
}

func TestCommitModeHijack(t *testing.T) {
	s := New(commitOptions())

	var isHijacker bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		isHijacker = ok
		if ok {
			_, _, _ = hijacker.Hijack()
		}
	})

	res := &orderedFlushHijackWriter{newOrderedResponseWriter()}
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, isHijacker, true)
	// Hijack is passed through; no headers are forced onto a hijacked connection.
	expect(t, strings.Join(res.events, ","), "Hijack")
	expect(t, res.Header().Get(frameOptionsHeader), "")
}

func TestCommitModeHijackNotExposed(t *testing.T) {
	s := New(commitOptions())

	var isHijacker bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, isHijacker = w.(http.Hijacker)
		_, _ = w.Write([]byte("bar"))
	})

	res := &orderedFlushWriter{newOrderedResponseWriter()}
	s.Handler(handler).ServeHTTP(res, commitRequest())

	expect(t, isHijacker, false)
}

func TestCommitModeHandlerWritesNothing(t *testing.T) {
	s := New(commitOptions())

	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	// The handler never committed; the headers are applied when it returns so
	// the server's implicit 200 still carries them.
	expect(t, len(res.events), 0)
	expect(t, res.Header().Get(frameOptionsHeader), frameOptionsValue)
	expect(t, res.Header().Get(contentTypeHeader), contentTypeValue)
}

func TestCommitModeBadHost(t *testing.T) {
	opt := commitOptions()
	opt.AllowedHosts = []string{"other.example.com"}
	s := New(opt)

	res := newOrderedResponseWriter()
	s.Handler(myHandler).ServeHTTP(res, commitRequest())

	// The error response flows through the wrapper without security headers.
	expect(t, strings.Join(res.events, ","), "WriteHeader(500),Write(9)")
	expect(t, res.Header().Get(frameOptionsHeader), "")
}

func TestCommitModeHandlerFuncWithNext(t *testing.T) {
	s := New(commitOptions())

	res := newOrderedResponseWriter()
	s.HandlerFuncWithNext(res, commitRequest(), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		_, _ = w.Write([]byte("bar"))
	})

	expect(t, strings.Join(res.events, ","), "WriteHeader(200),Write(3)")
	expect(t, res.snapshots[0].header.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeDefaultPolicyIsOverride(t *testing.T) {
	opt := commitOptions()
	opt.HeaderConflictPolicies = map[string]HeaderConflictPolicy{
		contentTypeHeader: HeaderConflictKeep,
	}
	s := New(opt)

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		w.Header().Set(contentTypeHeader, "downstream")
		_, _ = w.Write([]byte("bar"))
	})

	res := newOrderedResponseWriter()
	s.Handler(handler).ServeHTTP(res, commitRequest())

	// X-Frame-Options falls back to the default (override) policy.
	expect(t, res.Header().Get(frameOptionsHeader), frameOptionsValue)
	// X-Content-Type-Options uses the configured keep policy.
	expect(t, res.Header().Get(contentTypeHeader), "downstream")
}

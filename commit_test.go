package secure

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

// recordedCall captures a single call made to a recordingWriter, along with a
// snapshot of the response headers at the moment of the call.
type recordedCall struct {
	name    string
	detail  string
	headers http.Header
}

// recordingWriter is an http.ResponseWriter that records the order of the
// calls it receives and the state of the headers at each call.
type recordingWriter struct {
	header http.Header
	calls  []recordedCall
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: make(http.Header)}
}

func (rw *recordingWriter) Header() http.Header {
	return rw.header
}

func (rw *recordingWriter) snapshot() http.Header {
	snap := make(http.Header, len(rw.header))
	for key, values := range rw.header {
		snap[key] = append([]string(nil), values...)
	}

	return snap
}

func (rw *recordingWriter) WriteHeader(statusCode int) {
	rw.calls = append(rw.calls, recordedCall{name: "WriteHeader", detail: fmt.Sprint(statusCode), headers: rw.snapshot()})
}

func (rw *recordingWriter) Write(p []byte) (int, error) {
	rw.calls = append(rw.calls, recordedCall{name: "Write", detail: string(p), headers: rw.snapshot()})

	return len(p), nil
}

type recordingFlusher struct{ *recordingWriter }

func (rw recordingFlusher) Flush() {
	rw.calls = append(rw.calls, recordedCall{name: "Flush", headers: rw.snapshot()})
}

var errHijackSentinel = errors.New("hijacked")

type recordingHijacker struct{ *recordingWriter }

func (rw recordingHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw.calls = append(rw.calls, recordedCall{name: "Hijack", headers: rw.snapshot()})

	return nil, nil, errHijackSentinel
}

type recordingFlusherHijacker struct{ *recordingWriter }

func (rw recordingFlusherHijacker) Flush() {
	rw.calls = append(rw.calls, recordedCall{name: "Flush", headers: rw.snapshot()})
}

func (rw recordingFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw.calls = append(rw.calls, recordedCall{name: "Hijack", headers: rw.snapshot()})

	return nil, nil, errHijackSentinel
}

func commitRequest() *http.Request {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/foo", nil)
	req.Host = exampleHost

	return req
}

func TestCommitModeHeadersAppliedOnImplicit200(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := newRecordingWriter()

	var sawHeaderDownstream bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The security headers must not be visible while the downstream handler runs.
		sawHeaderDownstream = w.Header().Get(frameOptionsHeader) != ""
		w.Header().Set("X-Downstream", "yes")
		_, _ = w.Write([]byte("hello"))
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, sawHeaderDownstream, false)
	expect(t, len(rw.calls), 1)
	expect(t, rw.calls[0].name, "Write")
	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), frameOptionsValue)
	expect(t, rw.calls[0].headers.Get("X-Downstream"), "yes")
	expect(t, rw.Header().Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeHeadersAppliedOnExplicitWriteHeader(t *testing.T) {
	s := New(Options{
		ContentTypeNosniff: true,
		HeadersCommitMode:  true,
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, len(rw.calls), 1)
	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].detail, "201")
	expect(t, rw.calls[0].headers.Get(contentTypeHeader), contentTypeValue)
}

func TestCommitModeInformationalStatusDoesNotCommit(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, len(rw.calls), 2)
	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].detail, "103")
	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), "")
	expect(t, rw.calls[1].name, "WriteHeader")
	expect(t, rw.calls[1].detail, "200")
	expect(t, rw.calls[1].headers.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeHeadersNotReprocessedAfterCommit(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := recordingFlusher{newRecordingWriter()}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Mutating the headers after the commit must not trigger a re-application.
		w.Header().Set(frameOptionsHeader, "AFTER-COMMIT")
		w.WriteHeader(http.StatusInternalServerError)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("late"))
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, len(rw.calls), 4)
	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), frameOptionsValue)
	// The post-commit downstream value is never overwritten again.
	expect(t, rw.Header().Get(frameOptionsHeader), "AFTER-COMMIT")
	expect(t, rw.calls[1].headers.Get(frameOptionsHeader), "AFTER-COMMIT")
	expect(t, rw.calls[2].headers.Get(frameOptionsHeader), "AFTER-COMMIT")
	expect(t, rw.calls[3].headers.Get(frameOptionsHeader), "AFTER-COMMIT")
}

func TestCommitModeKeepBehavior(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
		HeaderConflictBehaviors: map[string]HeaderConflictBehavior{
			frameOptionsHeader: HeaderConflictKeep,
		},
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), "SAMEORIGIN")
}

func TestCommitModeOverrideBehavior(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
		HeaderConflictBehaviors: map[string]HeaderConflictBehavior{
			frameOptionsHeader: HeaderConflictOverride,
		},
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeErrorBehaviorDefaultHandler(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
		HeaderConflictBehaviors: map[string]HeaderConflictBehavior{
			frameOptionsHeader: HeaderConflictError,
		},
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	// The downstream 200 is replaced by the default 500 error response.
	expect(t, len(rw.calls), 2)
	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].detail, "500")
	expect(t, rw.calls[1].name, "Write")
	// Neither the downstream value nor the middleware value is committed.
	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), "SAMEORIGIN")
}

func TestCommitModeErrorBehaviorCustomHandler(t *testing.T) {
	var handlerErr error

	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
		HeaderConflictBehaviors: map[string]HeaderConflictBehavior{
			frameOptionsHeader: HeaderConflictError,
		},
		CommitErrorHandler: func(w http.ResponseWriter, err error) {
			handlerErr = err
			http.Error(w, "conflict", http.StatusConflict)
		},
	})

	rw := newRecordingWriter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(frameOptionsHeader, "SAMEORIGIN")
		_, _ = w.Write([]byte("hello"))
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	conflictErr, ok := handlerErr.(*CommitConflictError)
	expect(t, ok, true)
	expect(t, len(conflictErr.Headers), 1)
	expect(t, conflictErr.Headers[0], frameOptionsHeader)
	expect(t, strings.Contains(conflictErr.Error(), frameOptionsHeader), true)
	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].detail, "409")
}

func TestCommitModeFlushCommitsHeaders(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := recordingFlusher{newRecordingWriter()}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("after-flush"))
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, len(rw.calls), 2)
	expect(t, rw.calls[0].name, "Flush")
	expect(t, rw.calls[0].headers.Get(frameOptionsHeader), frameOptionsValue)
	expect(t, rw.calls[1].name, "Write")
	expect(t, rw.calls[1].headers.Get(frameOptionsHeader), frameOptionsValue)
}

func TestCommitModeHijackIsDelegated(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := recordingHijacker{newRecordingWriter()}

	var hijackErr error

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		expect(t, ok, true)
		_, _, hijackErr = hijacker.Hijack()
	})

	s.Handler(handler).ServeHTTP(rw, commitRequest())

	expect(t, errors.Is(hijackErr, errHijackSentinel), true)
	expect(t, len(rw.calls), 1)
	expect(t, rw.calls[0].name, "Hijack")
	// Hijacking takes over the connection; no headers are committed.
	expect(t, rw.Header().Get(frameOptionsHeader), "")
}

func TestCommitModeDoesNotFakeCapabilities(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	t.Run("plain writer gains nothing", func(t *testing.T) {
		rw := newRecordingWriter()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, isFlusher := w.(http.Flusher)
			_, isHijacker := w.(http.Hijacker)
			_, isPusher := w.(http.Pusher)
			expect(t, isFlusher, false)
			expect(t, isHijacker, false)
			expect(t, isPusher, false)
			_, _ = w.Write([]byte("ok"))
		})

		s.Handler(handler).ServeHTTP(rw, commitRequest())
	})

	t.Run("flusher and hijacker are preserved", func(t *testing.T) {
		rw := recordingFlusherHijacker{newRecordingWriter()}

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, isFlusher := w.(http.Flusher)
			_, isHijacker := w.(http.Hijacker)
			_, isPusher := w.(http.Pusher)
			expect(t, isFlusher, true)
			expect(t, isHijacker, true)
			expect(t, isPusher, false)
			_, _ = w.Write([]byte("ok"))
		})

		s.Handler(handler).ServeHTTP(rw, commitRequest())
	})
}

func TestCommitModeRequestErrorWritesNoSecurityHeaders(t *testing.T) {
	s := New(Options{
		AllowedHosts:      []string{"sub.example.com"},
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := newRecordingWriter()

	s.Handler(myHandler).ServeHTTP(rw, commitRequest())

	expect(t, rw.calls[0].name, "WriteHeader")
	expect(t, rw.calls[0].detail, "500")
	expect(t, rw.Header().Get(frameOptionsHeader), "")
}

func TestCommitModeRequestOnlyHandlerUnchanged(t *testing.T) {
	s := New(Options{
		FrameDeny:         true,
		HeadersCommitMode: true,
	})

	rw := newRecordingWriter()

	var ctxHeader http.Header

	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxHeader, _ = r.Context().Value(ctxDefaultSecureHeaderKey).(http.Header)
	})

	s.HandlerForRequestOnly(handler).ServeHTTP(rw, commitRequest())

	// Request-only mode never writes headers, commit mode does not change that.
	expect(t, len(rw.calls), 0)
	expect(t, rw.Header().Get(frameOptionsHeader), "")
	expect(t, ctxHeader.Get(frameOptionsHeader), frameOptionsValue)
}

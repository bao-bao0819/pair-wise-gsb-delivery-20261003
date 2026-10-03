package secure

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// HeaderConflictBehavior defines how the middleware resolves a conflict when,
// in commit mode, a security header it wants to set has already been set by a
// downstream handler.
type HeaderConflictBehavior int

const (
	// HeaderConflictOverride makes the middleware value replace any value set downstream. This is the default.
	HeaderConflictOverride HeaderConflictBehavior = iota
	// HeaderConflictKeep keeps the value set downstream and discards the middleware value.
	HeaderConflictKeep
	// HeaderConflictError reports the conflict as an error instead of writing either value.
	HeaderConflictError
)

// CommitConflictError is passed to the CommitErrorHandler when one or more
// security headers configured with HeaderConflictError conflict with values
// set by a downstream handler.
type CommitConflictError struct {
	// Headers holds the canonical names of the conflicting headers.
	Headers []string
}

// Error implements the error interface.
func (e *CommitConflictError) Error() string {
	return fmt.Sprintf("secure: response header conflict on: %s", strings.Join(e.Headers, ", "))
}

// applyCommitHeaders merges the security headers into the live response header
// map, honoring the configured conflict behavior for each header.
func (s *Secure) applyCommitHeaders(responseHeader http.Header, w http.ResponseWriter) error {
	downstream := w.Header()

	var conflicts []string

	for key, values := range responseHeader {
		behavior, ok := s.opt.HeaderConflictBehaviors[http.CanonicalHeaderKey(key)]
		if !ok {
			behavior = s.opt.DefaultHeaderConflictBehavior
		}

		for _, value := range values {
			if downstream.Get(key) == "" {
				downstream.Set(key, value)

				continue
			}

			switch behavior {
			case HeaderConflictKeep:
				// Keep the downstream value untouched.
			case HeaderConflictError:
				conflicts = append(conflicts, key)
			default:
				downstream.Set(key, value)
			}
		}
	}

	if len(conflicts) > 0 {
		return &CommitConflictError{Headers: conflicts}
	}

	return nil
}

// commitWriter wraps an http.ResponseWriter and applies the security headers
// just before the final response headers are committed to the client.
type commitWriter struct {
	http.ResponseWriter
	s              *Secure
	responseHeader http.Header
	committed      bool
	commitErr      error
}

// commit applies the security headers exactly once. Headers set downstream
// after this point are never re-processed.
func (cw *commitWriter) commit() {
	if cw.committed {
		return
	}

	cw.committed = true
	cw.commitErr = cw.s.applyCommitHeaders(cw.responseHeader, cw.ResponseWriter)
}

// commitFailed reports whether committing the security headers failed. On the
// first failure it invokes the configured CommitErrorHandler (or a default
// 500 response) instead of letting the downstream response through.
func (cw *commitWriter) commitFailed() bool {
	if cw.commitErr == nil {
		return false
	}

	if handler := cw.s.opt.CommitErrorHandler; handler != nil {
		handler(cw.ResponseWriter, cw.commitErr)
	} else {
		http.Error(cw.ResponseWriter, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}

	return true
}

// WriteHeader implements http.ResponseWriter. Informational 1xx statuses are
// passed through without committing the security headers; the first final
// status triggers the commit. Later calls are forwarded untouched.
func (cw *commitWriter) WriteHeader(statusCode int) {
	if cw.committed {
		cw.ResponseWriter.WriteHeader(statusCode)

		return
	}

	if statusCode >= 100 && statusCode <= 199 && statusCode != http.StatusSwitchingProtocols {
		cw.ResponseWriter.WriteHeader(statusCode)

		return
	}

	cw.commit()

	if cw.commitFailed() {
		return
	}

	cw.ResponseWriter.WriteHeader(statusCode)
}

// Write implements http.ResponseWriter, committing the security headers first
// so an implicit 200 response still carries them.
func (cw *commitWriter) Write(p []byte) (int, error) {
	if !cw.committed {
		cw.commit()

		if cw.commitFailed() {
			return 0, cw.commitErr
		}
	}

	return cw.ResponseWriter.Write(p)
}

// Unwrap returns the underlying http.ResponseWriter, e.g. for use with
// http.ResponseController.
func (cw *commitWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

// flush commits the security headers before flushing, mirroring the net/http
// behavior where Flush commits the response headers.
func (cw *commitWriter) flush() {
	if !cw.committed {
		cw.commit()

		if cw.commitFailed() {
			return
		}
	}

	cw.ResponseWriter.(http.Flusher).Flush()
}

func (cw *commitWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	return cw.ResponseWriter.(http.Hijacker).Hijack()
}

func (cw *commitWriter) push(target string, opts *http.PushOptions) error {
	return cw.ResponseWriter.(http.Pusher).Push(target, opts)
}

// The wrappers below expose the optional http.Flusher, http.Hijacker, and
// http.Pusher interfaces only in the combinations supported by the underlying
// ResponseWriter, so capabilities are never faked.

type commitFlusher struct{ *commitWriter }

func (cw commitFlusher) Flush() { cw.commitWriter.flush() }

type commitHijacker struct{ *commitWriter }

func (cw commitHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return cw.hijack() }

type commitPusher struct{ *commitWriter }

func (cw commitPusher) Push(target string, opts *http.PushOptions) error {
	return cw.push(target, opts)
}

type commitFlusherHijacker struct{ *commitWriter }

func (cw commitFlusherHijacker) Flush() { cw.commitWriter.flush() }

func (cw commitFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return cw.hijack() }

type commitFlusherPusher struct{ *commitWriter }

func (cw commitFlusherPusher) Flush() { cw.commitWriter.flush() }

func (cw commitFlusherPusher) Push(target string, opts *http.PushOptions) error {
	return cw.push(target, opts)
}

type commitHijackerPusher struct{ *commitWriter }

func (cw commitHijackerPusher) Hijack() (net.Conn, *bufio.ReadWriter, error) { return cw.hijack() }

func (cw commitHijackerPusher) Push(target string, opts *http.PushOptions) error {
	return cw.push(target, opts)
}

type commitFlusherHijackerPusher struct{ *commitWriter }

func (cw commitFlusherHijackerPusher) Flush() { cw.commitWriter.flush() }

func (cw commitFlusherHijackerPusher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return cw.hijack()
}

func (cw commitFlusherHijackerPusher) Push(target string, opts *http.PushOptions) error {
	return cw.push(target, opts)
}

// wrapCommitResponseWriter wraps w so the security headers are applied when
// the response headers are committed, preserving the optional interfaces of
// the underlying writer.
func (s *Secure) wrapCommitResponseWriter(w http.ResponseWriter, responseHeader http.Header) http.ResponseWriter {
	cw := &commitWriter{ResponseWriter: w, s: s, responseHeader: responseHeader}

	_, flusher := w.(http.Flusher)
	_, hijacker := w.(http.Hijacker)
	_, pusher := w.(http.Pusher)

	switch {
	case flusher && hijacker && pusher:
		return commitFlusherHijackerPusher{cw}
	case flusher && hijacker:
		return commitFlusherHijacker{cw}
	case flusher && pusher:
		return commitFlusherPusher{cw}
	case hijacker && pusher:
		return commitHijackerPusher{cw}
	case flusher:
		return commitFlusher{cw}
	case hijacker:
		return commitHijacker{cw}
	case pusher:
		return commitPusher{cw}
	default:
		return cw
	}
}

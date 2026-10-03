package secure

import (
	"bufio"
	"net"
	"net/http"
)

// HeaderConflictPolicy defines how the middleware resolves a conflict between a
// computed security header and a value set by a downstream handler when
// Options.HeadersCommitMode is enabled.
type HeaderConflictPolicy int

const (
	// HeaderConflictOverride replaces any downstream value with the middleware
	// value when the final response headers are committed. This is the default.
	HeaderConflictOverride HeaderConflictPolicy = iota
	// HeaderConflictKeep preserves the downstream value when one is set;
	// otherwise the middleware value is applied.
	HeaderConflictKeep
	// HeaderConflictError preserves the downstream value and reports the
	// conflict through Options.HeaderConflictHandler.
	HeaderConflictError
)

// HeaderConflictHandler is called when the final response headers are committed
// for each header configured with the HeaderConflictError policy whose
// downstream value differs from the middleware-computed value.
type HeaderConflictHandler func(r *http.Request, header, downstreamValue, middlewareValue string)

// commitResponseWriter wraps an http.ResponseWriter and applies the security
// headers right before the final response headers are committed: on the first
// non-1xx WriteHeader, on the implicit 200 triggered by Write or Flush, or
// when the handler returns without having written anything.
type commitResponseWriter struct {
	w              http.ResponseWriter
	r              *http.Request
	s              *Secure
	responseHeader http.Header
	committed      bool
}

// newCommitResponseWriter wraps w so that the security headers are applied at
// commit time. The returned http.ResponseWriter only exposes the Flusher and
// Hijacker capabilities that the underlying writer actually supports.
func newCommitResponseWriter(w http.ResponseWriter, r *http.Request, s *Secure) (*commitResponseWriter, http.ResponseWriter) {
	cw := &commitResponseWriter{w: w, r: r, s: s}

	_, flusher := w.(http.Flusher)
	_, hijacker := w.(http.Hijacker)

	switch {
	case flusher && hijacker:
		return cw, &commitFlushHijackWriter{cw}
	case flusher:
		return cw, &commitFlushWriter{cw}
	case hijacker:
		return cw, &commitHijackWriter{cw}
	default:
		return cw, cw
	}
}

// setResponseHeaders stores the middleware-computed headers to apply at commit time.
func (cw *commitResponseWriter) setResponseHeaders(responseHeader http.Header) {
	cw.responseHeader = responseHeader
}

// Header implements http.ResponseWriter.
func (cw *commitResponseWriter) Header() http.Header {
	return cw.w.Header()
}

// Unwrap returns the underlying ResponseWriter, for use with http.ResponseController.
func (cw *commitResponseWriter) Unwrap() http.ResponseWriter {
	return cw.w
}

// WriteHeader implements http.ResponseWriter. Informational (1xx) status codes
// are passed through without applying the security headers; the first final
// status code commits the headers. Subsequent calls are passed through
// untouched, mirroring the underlying writer's behavior.
func (cw *commitResponseWriter) WriteHeader(statusCode int) {
	if cw.committed || isInformationalStatus(statusCode) {
		cw.w.WriteHeader(statusCode)

		return
	}

	cw.committed = true
	cw.applyHeaders()
	cw.w.WriteHeader(statusCode)
}

// Write implements http.ResponseWriter, committing an implicit 200 first if
// the final headers have not been written yet.
func (cw *commitResponseWriter) Write(b []byte) (int, error) {
	if !cw.committed {
		cw.WriteHeader(http.StatusOK)
	}

	return cw.w.Write(b)
}

// finalize applies the security headers when the handler returned without
// committing the response, so the implicit 200 written by the server still
// carries them.
func (cw *commitResponseWriter) finalize() {
	if cw.committed {
		return
	}

	cw.committed = true
	cw.applyHeaders()
}

// isInformationalStatus reports whether the status code is an informational
// (1xx) response that does not commit the final response headers. 101
// Switching Protocols is excluded: it finalizes the response.
func isInformationalStatus(statusCode int) bool {
	return statusCode >= 100 && statusCode < 200 && statusCode != http.StatusSwitchingProtocols
}

// applyHeaders applies the middleware-computed headers to the response,
// resolving conflicts with downstream values per the configured policies.
// It runs exactly once, when the final response headers are committed.
func (cw *commitResponseWriter) applyHeaders() {
	for name, values := range cw.responseHeader {
		for _, value := range values {
			cw.applyHeader(name, value)
		}
	}
}

func (cw *commitResponseWriter) applyHeader(name, middlewareValue string) {
	policy := cw.s.opt.DefaultHeaderConflictPolicy
	if p, ok := cw.s.opt.HeaderConflictPolicies[http.CanonicalHeaderKey(name)]; ok {
		policy = p
	}

	downstreamValues := cw.w.Header().Values(name)
	downstreamSet := len(downstreamValues) > 0

	switch policy {
	case HeaderConflictKeep:
		if downstreamSet {
			return
		}
	case HeaderConflictError:
		if downstreamSet {
			if downstreamValues[0] != middlewareValue && cw.s.opt.HeaderConflictHandler != nil {
				cw.s.opt.HeaderConflictHandler(cw.r, name, downstreamValues[0], middlewareValue)
			}

			return
		}
	case HeaderConflictOverride:
	}

	cw.w.Header().Set(name, middlewareValue)
}

// flush commits an implicit 200 if needed and flushes the underlying writer.
func (cw *commitResponseWriter) flush() {
	if !cw.committed {
		cw.WriteHeader(http.StatusOK)
	}

	cw.w.(http.Flusher).Flush()
}

// hijack hands the connection over to the downstream handler. The response can
// no longer be written through the ResponseWriter, so no headers are applied.
func (cw *commitResponseWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	cw.committed = true

	return cw.w.(http.Hijacker).Hijack()
}

// commitFlushWriter exposes Flush only when the underlying writer is an http.Flusher.
type commitFlushWriter struct {
	*commitResponseWriter
}

// Flush implements http.Flusher.
func (w *commitFlushWriter) Flush() {
	w.flush()
}

// commitHijackWriter exposes Hijack only when the underlying writer is an http.Hijacker.
type commitHijackWriter struct {
	*commitResponseWriter
}

// Hijack implements http.Hijacker.
func (w *commitHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.hijack()
}

// commitFlushHijackWriter exposes Flush and Hijack only when the underlying
// writer supports both.
type commitFlushHijackWriter struct {
	*commitResponseWriter
}

// Flush implements http.Flusher.
func (w *commitFlushHijackWriter) Flush() {
	w.flush()
}

// Hijack implements http.Hijacker.
func (w *commitFlushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.hijack()
}

package secure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type secureCtxKey string

const (
	stsHeader                    = "Strict-Transport-Security"
	stsSubdomainString           = "; includeSubDomains"
	stsPreloadString             = "; preload"
	frameOptionsHeader           = "X-Frame-Options"
	frameOptionsValue            = "DENY"
	contentTypeHeader            = "X-Content-Type-Options"
	contentTypeValue             = "nosniff"
	xssProtectionHeader          = "X-XSS-Protection"
	xssProtectionValue           = "1; mode=block"
	cspHeader                    = "Content-Security-Policy"
	cspReportOnlyHeader          = "Content-Security-Policy-Report-Only"
	hpkpHeader                   = "Public-Key-Pins"
	referrerPolicyHeader         = "Referrer-Policy"
	featurePolicyHeader          = "Feature-Policy"
	permissionsPolicyHeader      = "Permissions-Policy"
	coopHeader                   = "Cross-Origin-Opener-Policy"
	coepHeader                   = "Cross-Origin-Embedder-Policy"
	corpHeader                   = "Cross-Origin-Resource-Policy"
	dnsPreFetchControlHeader     = "X-DNS-Prefetch-Control"
	permittedCrossDomainPolicies = "X-Permitted-Cross-Domain-Policies"
	ctxDefaultSecureHeaderKey    = secureCtxKey("SecureResponseHeader")
	cspNonceSize                 = 16
)

// Action describes what the middleware decided should happen to a request.
type Action int

const (
	// ActionAllow means the request may proceed to the next handler.
	ActionAllow Action = iota
	// ActionDeny means the request was rejected and the bad host or bad request handler must be called.
	ActionDeny
	// ActionRedirect means the request must be redirected (typically to HTTPS).
	ActionRedirect
)

// Decision is the pure evaluation result for a request. It does not depend on a
// http.ResponseWriter and never writes anything itself. Use ApplyDecision to
// realize the decision on an actual ResponseWriter.
type Decision struct {
	// Action is the decision taken for the request.
	Action Action

	// Request is the (possibly modified) request that should be passed down the
	// handler chain. It carries the CSP nonce in its context when one is
	// required, but otherwise leaves the incoming request untouched.
	Request *http.Request

	// Header holds the headers that must be set on the response. It is only
	// populated for ActionAllow.
	Header http.Header

	// Nonce holds the generated CSP nonce for the request, if one is required.
	// It is generated once during evaluation and reused by ApplyDecision.
	Nonce string

	// RedirectURL is the target of the redirect. Only set for ActionRedirect.
	RedirectURL string

	// RedirectStatus is the status code of the redirect. Only set for ActionRedirect.
	RedirectStatus int

	// Reason explains why the request was denied or redirected. It is empty
	// for ActionAllow and is also returned as the error by ApplyDecision.
	Reason string

	// denyHandler is the handler that must serve the response for ActionDeny.
	denyHandler http.Handler
}

// SSLHostFunc is a custom function type that can be used to dynamically set the SSL host of a request.
type SSLHostFunc func(host string) (newHost string)

// AllowRequestFunc is a custom function type that can be used to dynamically determine if a request should proceed or not.
type AllowRequestFunc func(r *http.Request) bool

func defaultBadHostHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "Bad Host", http.StatusInternalServerError)
}

func defaultBadRequestHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "Bad Request", http.StatusBadRequest)
}

// Options is a struct for specifying configuration options for the secure.Secure middleware.
type Options struct {
	// If BrowserXssFilter is true, adds the X-XSS-Protection header with the value `1; mode=block`. Default is false.
	BrowserXssFilter bool //nolint:stylecheck,revive
	// If ContentTypeNosniff is true, adds the X-Content-Type-Options header with the value `nosniff`. Default is false.
	ContentTypeNosniff bool
	// If ForceSTSHeader is set to true, the STS header will be added even when the connection is HTTP. Default is false.
	ForceSTSHeader bool
	// If FrameDeny is set to true, adds the X-Frame-Options header with the value of `DENY`. Default is false.
	FrameDeny bool
	// When developing, the AllowedHosts, SSL, and STS options can cause some unwanted effects. Usually testing happens on http, not https, and on localhost, not your production domain... so set this to true for dev environment.
	// If you would like your development environment to mimic production with complete Host blocking, SSL redirects, and STS headers, leave this as false. Default if false.
	IsDevelopment bool
	// nonceEnabled is used internally for dynamic nouces.
	nonceEnabled bool
	// If SSLRedirect is set to true, then only allow https requests. Default is false.
	SSLRedirect bool
	// If SSLForceHost is true and SSLHost is set, requests will be forced to use SSLHost even the ones that are already using SSL. Default is false.
	SSLForceHost bool
	// If SSLTemporaryRedirect is true, then a 307 will be used while redirecting. Default is false (301).
	SSLTemporaryRedirect bool
	// If STSIncludeSubdomains is set to true, the `includeSubdomains` will be appended to the Strict-Transport-Security header. Default is false.
	STSIncludeSubdomains bool
	// If STSPreload is set to true, the `preload` flag will be appended to the Strict-Transport-Security header. Default is false.
	STSPreload bool
	// ContentSecurityPolicy allows the Content-Security-Policy header value to be set with a custom value. Default is "".
	ContentSecurityPolicy string
	// ContentSecurityPolicyReportOnly allows the Content-Security-Policy-Report-Only header value to be set with a custom value. Default is "".
	ContentSecurityPolicyReportOnly string
	// CustomBrowserXssValue allows the X-XSS-Protection header value to be set with a custom value. This overrides the BrowserXssFilter option. Default is "".
	CustomBrowserXssValue string //nolint:stylecheck,revive
	// Passing a template string will replace `$NONCE` with a dynamic nonce value of 16 bytes for each request which can be later retrieved using the Nonce function.
	// Eg: script-src $NONCE -> script-src 'nonce-a2ZobGFoZg=='
	// CustomFrameOptionsValue allows the X-Frame-Options header value to be set with a custom value. This overrides the FrameDeny option. Default is "".
	CustomFrameOptionsValue string
	// ReferrerPolicy allows sites to control when browsers will pass the Referer header to other sites. Default is "".
	ReferrerPolicy string
	// FeaturePolicy allows to selectively enable and disable use of various browser features and APIs. Default is "".
	// Deprecated: This header has been renamed to Permissions-Policy.
	FeaturePolicy string
	// PermissionsPolicy allows to selectively enable and disable use of various browser features and APIs. Default is "".
	PermissionsPolicy string
	// CrossOriginOpenerPolicy allows you to ensure a top-level document does not share a browsing context group with cross-origin documents. Default is "".
	// Reference: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cross-Origin-Opener-Policy
	CrossOriginOpenerPolicy string
	// CrossOriginResourcePolicy header blocks others from loading your resources cross-origin in some cases.
	// Reference https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cross-Origin-Opener-Policy
	CrossOriginResourcePolicy string
	// CrossOriginEmbedderPolicy header helps control what resources can be loaded cross-origin.
	// Reference https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Cross-Origin-Embedder-Policy
	CrossOriginEmbedderPolicy string
	// XDNSPrefetchControl header helps control DNS prefetching, which can improve user privacy at the expense of performance.
	// Reference: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-DNS-Prefetch-Control
	XDNSPrefetchControl string
	// XPermittedCrossDomainPolicies header tells some clients (mostly Adobe products) your domain's policy for loading cross-domain content.
	// Reference: https://owasp.org/www-project-secure-headers/
	XPermittedCrossDomainPolicies string
	// SSLHost is the host name that is used to redirect http requests to https. Default is "", which indicates to use the same host.
	SSLHost string
	// AllowedHosts is a slice of fully qualified domain names that are allowed. Default is an empty slice, which allows any and all host names.
	AllowedHosts []string
	// AllowedHostsAreRegex determines, if the provided `AllowedHosts` slice contains valid regular expressions. If this flag is set to true, every request's host will be checked against these expressions. Default is false.
	AllowedHostsAreRegex bool
	// AllowRequestFunc is a custom function that allows you to determine if the request should proceed or not based on your own custom logic. Default is nil.
	AllowRequestFunc AllowRequestFunc `json:"-" toml:"-" yaml:"-"`
	// HostsProxyHeaders is a set of header keys that may hold a proxied hostname value for the request.
	HostsProxyHeaders []string
	// SSLHostFunc is a function pointer, the return value of the function is the host name that has same functionality as `SSHost`. Default is nil.
	// If SSLHostFunc is nil, the `SSLHost` option will be used.
	SSLHostFunc *SSLHostFunc `json:"-" toml:"-" yaml:"-"`
	// SSLProxyHeaders is set of header keys with associated values that would indicate a valid https request. Useful when using Nginx: `map[string]string{"X-Forwarded-Proto": "https"}`. Default is blank map.
	SSLProxyHeaders map[string]string
	// STSSeconds is the max-age of the Strict-Transport-Security header. Default is 0, which would NOT include the header.
	STSSeconds int64
	// SecureContextKey allows a custom key to be specified for context storage.
	SecureContextKey string
}

// Secure is a middleware that helps setup a few basic security features. A single secure.Options struct can be
// provided to configure which features should be enabled, and the ability to override a few of the default values.
type Secure struct {
	// Customize Secure with an Options struct.
	opt Options

	// badHostHandler is the handler used when an incorrect host is passed in.
	badHostHandler http.Handler

	// badRequestHandler is the handler used when the AllowRequestFunc rejects a request.
	badRequestHandler http.Handler

	// cRegexAllowedHosts saves the compiled regular expressions of the AllowedHosts
	// option for subsequent use in processRequest
	cRegexAllowedHosts []*regexp.Regexp

	// ctxSecureHeaderKey is the key used for context storage for request modification.
	ctxSecureHeaderKey secureCtxKey
}

// New constructs a new Secure instance with the supplied options.
func New(options ...Options) *Secure {
	var o Options
	if len(options) == 0 {
		o = Options{}
	} else {
		o = options[0]
	}

	o.ContentSecurityPolicy = strings.ReplaceAll(o.ContentSecurityPolicy, "$NONCE", "'nonce-%[1]s'")
	o.ContentSecurityPolicyReportOnly = strings.ReplaceAll(o.ContentSecurityPolicyReportOnly, "$NONCE", "'nonce-%[1]s'")

	o.nonceEnabled = strings.Contains(o.ContentSecurityPolicy, "%[1]s") || strings.Contains(o.ContentSecurityPolicyReportOnly, "%[1]s")

	s := &Secure{
		opt:               o,
		badHostHandler:    http.HandlerFunc(defaultBadHostHandler),
		badRequestHandler: http.HandlerFunc(defaultBadRequestHandler),
	}

	if s.opt.AllowedHostsAreRegex {
		// Test for invalid regular expressions in AllowedHosts
		for _, allowedHost := range o.AllowedHosts {
			regex, err := regexp.Compile(fmt.Sprintf("^%s$", allowedHost))
			if err != nil {
				panic(fmt.Sprintf("Error parsing AllowedHost: %s", err))
			}

			s.cRegexAllowedHosts = append(s.cRegexAllowedHosts, regex)
		}
	}

	s.ctxSecureHeaderKey = ctxDefaultSecureHeaderKey
	if len(s.opt.SecureContextKey) > 0 {
		s.ctxSecureHeaderKey = secureCtxKey(s.opt.SecureContextKey)
	}

	return s
}

// SetBadHostHandler sets the handler to call when secure rejects the host name.
func (s *Secure) SetBadHostHandler(handler http.Handler) {
	s.badHostHandler = handler
}

// SetBadRequestHandler sets the handler to call when the AllowRequestFunc rejects a request.
func (s *Secure) SetBadRequestHandler(handler http.Handler) {
	s.badRequestHandler = handler
}

// Handler implements the http.HandlerFunc for integration with the standard net/http lib.
func (s *Secure) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let secure evaluate the request. If the decision is not allowed,
		// do not continue.
		decision := s.EvaluateRequest(r)
		if s.applyDecision(w, decision) != nil {
			return
		}

		h.ServeHTTP(w, decision.Request)
	})
}

// HandlerForRequestOnly implements the http.HandlerFunc for integration with the standard net/http lib.
// Note that this is for requests only and will not write any headers.
func (s *Secure) HandlerForRequestOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let secure evaluate the request. If the decision is not allowed,
		// that indicates the request should not continue.
		decision := s.EvaluateRequest(r)
		if decision.Action != ActionAllow {
			return
		}

		// Save response headers in the request context.
		ctx := context.WithValue(decision.Request.Context(), s.ctxSecureHeaderKey, decision.Header)

		// No headers will be written to the ResponseWriter.
		h.ServeHTTP(w, decision.Request.WithContext(ctx))
	})
}

// HandlerFuncWithNext is a special implementation for Negroni, but could be used elsewhere.
func (s *Secure) HandlerFuncWithNext(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// Let secure evaluate the request. If the decision is not allowed,
	// do not call next.
	decision := s.EvaluateRequest(r)
	err := s.applyDecision(w, decision)

	if err == nil && next != nil {
		next(w, decision.Request)
	}
}

// HandlerFuncWithNextForRequestOnly is a special implementation for Negroni, but could be used elsewhere.
// Note that this is for requests only and will not write any headers.
func (s *Secure) HandlerFuncWithNextForRequestOnly(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// Let secure evaluate the request. If the decision is not allowed,
	// that indicates the request should not continue.
	decision := s.EvaluateRequest(r)

	// If the request is not allowed, do not call next.
	if decision.Action == ActionAllow && next != nil {
		// Save response headers in the request context
		ctx := context.WithValue(decision.Request.Context(), s.ctxSecureHeaderKey, decision.Header)

		// No headers will be written to the ResponseWriter.
		next(w, decision.Request.WithContext(ctx))
	}
}

// addResponseHeaders Adds the headers from 'responseHeader' to the response.
func addResponseHeaders(responseHeader http.Header, w http.ResponseWriter) {
	for key, values := range responseHeader {
		for _, value := range values {
			w.Header().Set(key, value)
		}
	}
}

// Process runs the actual checks and writes the headers in the ResponseWriter.
func (s *Secure) Process(w http.ResponseWriter, r *http.Request) error {
	return s.applyDecision(w, s.EvaluateRequest(r))
}

// ProcessAndReturnNonce runs the actual checks and writes the headers in the ResponseWriter.
// In addition, the generated nonce for the request is returned as well as the error value.
func (s *Secure) ProcessAndReturnNonce(w http.ResponseWriter, r *http.Request) (string, error) {
	decision := s.EvaluateRequest(r)
	if err := s.applyDecision(w, decision); err != nil {
		return "", err
	}

	return decision.Nonce, nil
}

// ProcessNoModifyRequest runs the actual checks but does not write the headers in the ResponseWriter.
func (s *Secure) ProcessNoModifyRequest(w http.ResponseWriter, r *http.Request) (http.Header, *http.Request, error) {
	decision := s.EvaluateRequest(r)

	if decision.Action != ActionAllow {
		return nil, nil, s.applyDecision(w, decision)
	}

	return decision.Header, decision.Request, nil
}

// EvaluateRequest runs the actual checks on the request without touching any
// http.ResponseWriter. It never calls the bad host or bad request handlers and
// never modifies the incoming request (in particular, it never mutates
// r.URL); the result of the evaluation, including the headers to add, the
// redirect target and the reason for a denial, is returned as a Decision.
//
// When a CSP nonce is required, it is generated once and stored in the
// Decision. Applying the same Decision reuses that nonce instead of
// generating a new one.
func (s *Secure) EvaluateRequest(r *http.Request) *Decision {
	// Setup nonce if required.
	if s.opt.nonceEnabled {
		r = withCSPNonce(r, cspRandNonce())
	}

	// Resolve the host for the request, using proxy headers if present.
	host := r.Host

	for _, header := range s.opt.HostsProxyHeaders {
		if h := r.Header.Get(header); h != "" {
			host = h

			break
		}
	}

	// Allowed hosts check.
	if len(s.opt.AllowedHosts) > 0 && !s.opt.IsDevelopment {
		isGoodHost := false

		if s.opt.AllowedHostsAreRegex {
			for _, allowedHost := range s.cRegexAllowedHosts {
				if match := allowedHost.MatchString(host); match {
					isGoodHost = true

					break
				}
			}
		} else {
			for _, allowedHost := range s.opt.AllowedHosts {
				if strings.EqualFold(allowedHost, host) {
					isGoodHost = true

					break
				}
			}
		}

		if !isGoodHost {
			return &Decision{
				Action:      ActionDeny,
				Request:     r,
				Reason:      fmt.Sprintf("bad host name: %s", host),
				denyHandler: s.badHostHandler,
			}
		}
	}

	// Determine if we are on HTTPS.
	ssl := s.isSSL(r)

	// SSL check.
	if s.opt.SSLRedirect && !ssl && !s.opt.IsDevelopment {
		url := *r.URL
		url.Scheme = "https"
		url.Host = host

		if s.opt.SSLHostFunc != nil {
			if h := (*s.opt.SSLHostFunc)(host); len(h) > 0 {
				url.Host = h
			}
		} else if len(s.opt.SSLHost) > 0 {
			url.Host = s.opt.SSLHost
		}

		status := http.StatusMovedPermanently
		if s.opt.SSLTemporaryRedirect {
			status = http.StatusTemporaryRedirect
		}

		return &Decision{
			Action:         ActionRedirect,
			Request:        r,
			RedirectURL:    url.String(),
			RedirectStatus: status,
			Reason:         "redirecting to HTTPS",
		}
	}

	if s.opt.SSLForceHost {
		tempSSLHost := host

		if s.opt.SSLHostFunc != nil {
			if h := (*s.opt.SSLHostFunc)(host); len(h) > 0 {
				tempSSLHost = h
			}
		} else if len(s.opt.SSLHost) > 0 {
			tempSSLHost = s.opt.SSLHost
		}

		if tempSSLHost != host {
			url := *r.URL
			url.Scheme = "https"
			url.Host = tempSSLHost

			status := http.StatusMovedPermanently
			if s.opt.SSLTemporaryRedirect {
				status = http.StatusTemporaryRedirect
			}

			return &Decision{
				Action:         ActionRedirect,
				Request:        r,
				RedirectURL:    url.String(),
				RedirectStatus: status,
				Reason:         "redirecting to HTTPS",
			}
		}
	}

	// If the AllowRequestFunc is set, call it and exit early if needed.
	if s.opt.AllowRequestFunc != nil && !s.opt.AllowRequestFunc(r) {
		return &Decision{
			Action:      ActionDeny,
			Request:     r,
			Reason:      "request not allowed",
			denyHandler: s.badRequestHandler,
		}
	}

	// Create our header container.
	responseHeader := make(http.Header)

	// Strict Transport Security header. Only add header when we know it's an SSL connection.
	// See https://tools.ietf.org/html/rfc6797#section-7.2 for details.
	if s.opt.STSSeconds != 0 && (ssl || s.opt.ForceSTSHeader) && !s.opt.IsDevelopment {
		stsSub := ""
		if s.opt.STSIncludeSubdomains {
			stsSub = stsSubdomainString
		}

		if s.opt.STSPreload {
			stsSub += stsPreloadString
		}

		responseHeader.Set(stsHeader, fmt.Sprintf("max-age=%d%s", s.opt.STSSeconds, stsSub))
	}

	// Frame Options header.
	if len(s.opt.CustomFrameOptionsValue) > 0 {
		responseHeader.Set(frameOptionsHeader, s.opt.CustomFrameOptionsValue)
	} else if s.opt.FrameDeny {
		responseHeader.Set(frameOptionsHeader, frameOptionsValue)
	}

	// Content Type Options header.
	if s.opt.ContentTypeNosniff {
		responseHeader.Set(contentTypeHeader, contentTypeValue)
	}

	// XSS Protection header.
	if len(s.opt.CustomBrowserXssValue) > 0 {
		responseHeader.Set(xssProtectionHeader, s.opt.CustomBrowserXssValue)
	} else if s.opt.BrowserXssFilter {
		responseHeader.Set(xssProtectionHeader, xssProtectionValue)
	}

	// Content Security Policy header.
	if len(s.opt.ContentSecurityPolicy) > 0 {
		if s.opt.nonceEnabled {
			responseHeader.Set(cspHeader, fmt.Sprintf(s.opt.ContentSecurityPolicy, CSPNonce(r.Context())))
		} else {
			responseHeader.Set(cspHeader, s.opt.ContentSecurityPolicy)
		}
	}

	// Content Security Policy Report Only header.
	if len(s.opt.ContentSecurityPolicyReportOnly) > 0 {
		if s.opt.nonceEnabled {
			responseHeader.Set(cspReportOnlyHeader, fmt.Sprintf(s.opt.ContentSecurityPolicyReportOnly, CSPNonce(r.Context())))
		} else {
			responseHeader.Set(cspReportOnlyHeader, s.opt.ContentSecurityPolicyReportOnly)
		}
	}

	// Referrer Policy header.
	if len(s.opt.ReferrerPolicy) > 0 {
		responseHeader.Set(referrerPolicyHeader, s.opt.ReferrerPolicy)
	}

	// Feature Policy header.
	if len(s.opt.FeaturePolicy) > 0 {
		responseHeader.Set(featurePolicyHeader, s.opt.FeaturePolicy)
	}

	// Permissions Policy header.
	if len(s.opt.PermissionsPolicy) > 0 {
		responseHeader.Set(permissionsPolicyHeader, s.opt.PermissionsPolicy)
	}

	// Cross Origin Opener Policy header.
	if len(s.opt.CrossOriginOpenerPolicy) > 0 {
		responseHeader.Set(coopHeader, s.opt.CrossOriginOpenerPolicy)
	}

	// Cross Origin Resource Policy header.
	if len(s.opt.CrossOriginResourcePolicy) > 0 {
		responseHeader.Set(corpHeader, s.opt.CrossOriginResourcePolicy)
	}

	// Cross-Origin-Embedder-Policy header.
	if len(s.opt.CrossOriginEmbedderPolicy) > 0 {
		responseHeader.Set(coepHeader, s.opt.CrossOriginEmbedderPolicy)
	}

	// X-DNS-Prefetch-Control header.
	switch strings.ToLower(s.opt.XDNSPrefetchControl) {
	case "on":
		responseHeader.Set(dnsPreFetchControlHeader, "on")
	case "off":
		responseHeader.Set(dnsPreFetchControlHeader, "off")
	}

	// X-Permitted-Cross-Domain-Policies header.
	if len(s.opt.XPermittedCrossDomainPolicies) > 0 {
		responseHeader.Set(permittedCrossDomainPolicies, s.opt.XPermittedCrossDomainPolicies)
	}

	return &Decision{
		Action:  ActionAllow,
		Request: r,
		Header:  responseHeader,
		Nonce:   CSPNonce(r.Context()),
	}
}

// applyDecision realizes a Decision returned by EvaluateRequest on the
// ResponseWriter. A denied request is served by the decision's deny handler,
// a redirecting request triggers the configured redirect and an allowed
// request gets the decision's headers added. The returned error is nil for
// ActionAllow and explains the outcome otherwise.
func (s *Secure) applyDecision(w http.ResponseWriter, decision *Decision) error {
	switch decision.Action {
	case ActionDeny:
		decision.denyHandler.ServeHTTP(w, decision.Request)

		return errors.New(decision.Reason)
	case ActionRedirect:
		http.Redirect(w, decision.Request, decision.RedirectURL, decision.RedirectStatus)

		return errors.New(decision.Reason)
	default:
		addResponseHeaders(decision.Header, w)

		return nil
	}
}

// ApplyDecision realizes a Decision previously returned by EvaluateRequest on
// the given ResponseWriter. It calls the bad host or bad request handler for a
// denied request, performs the redirect for a redirecting request and adds the
// prepared headers for an allowed request. It returns nil when the request is
// allowed and an error explaining the decision otherwise.
func (s *Secure) ApplyDecision(w http.ResponseWriter, decision *Decision) error {
	return s.applyDecision(w, decision)
}

// isSSL determine if we are on HTTPS.
func (s *Secure) isSSL(r *http.Request) bool {
	ssl := strings.EqualFold(r.URL.Scheme, "https") || r.TLS != nil
	if !ssl {
		for k, v := range s.opt.SSLProxyHeaders {
			if r.Header.Get(k) == v {
				ssl = true

				break
			}
		}
	}

	return ssl
}

// ModifyResponseHeaders modifies the Response.
// Used by http.ReverseProxy.
func (s *Secure) ModifyResponseHeaders(res *http.Response) error {
	if res != nil && res.Request != nil {
		// Fix Location response header http to https:
		// When SSL is enabled,
		// And SSLHost is defined,
		// And the response location header includes the SSLHost as the domain with a trailing slash,
		// Or an exact match to the SSLHost.
		location := res.Header.Get("Location")
		if s.isSSL(res.Request) &&
			len(s.opt.SSLHost) > 0 &&
			(strings.HasPrefix(location, fmt.Sprintf("http://%s/", s.opt.SSLHost)) || location == fmt.Sprintf("http://%s", s.opt.SSLHost)) {
			location = strings.Replace(location, "http:", "https:", 1)
			res.Header.Set("Location", location)
		}

		responseHeader := res.Request.Context().Value(s.ctxSecureHeaderKey)
		if responseHeader != nil {
			headers, _ := responseHeader.(http.Header)
			for header, values := range headers {
				if len(values) > 0 {
					res.Header.Set(header, strings.Join(values, ","))
				}
			}
		}
	}

	return nil
}

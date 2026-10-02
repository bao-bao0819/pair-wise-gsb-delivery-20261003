package secure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"
)

func TestEvaluateRequestAllowPreparesHeaders(t *testing.T) {
	s := New(Options{
		FrameDeny:          true,
		ContentTypeNosniff: true,
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://www.example.com/foo", nil)
	req.Host = exampleHost

	decision := s.EvaluateRequest(req)

	expect(t, decision.Action, ActionAllow)
	expect(t, decision.Header.Get(frameOptionsHeader), frameOptionsValue)
	expect(t, decision.Header.Get(contentTypeHeader), contentTypeValue)
	expect(t, decision.RedirectURL, "")
	expect(t, decision.Reason, "")
	if decision.Request == nil {
		t.Errorf("expected decision request to be set")
	}
}

func TestEvaluateRequestDenyDoesNotInvokeHandlerOrMutateRequest(t *testing.T) {
	s := New(Options{
		AllowedHosts: []string{"sub.example.com"},
	})

	badHostCalled := false
	s.SetBadHostHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		badHostCalled = true
	}))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://www.example.com/foo", nil)
	req.Host = exampleHost
	originalURL := req.URL.String()

	decision := s.EvaluateRequest(req)

	expect(t, decision.Action, ActionDeny)
	expect(t, decision.Reason, "bad host name: "+exampleHost)
	expect(t, badHostCalled, false)
	expect(t, req.URL.String(), originalURL)
	expect(t, len(decision.Header), 0)

	// Applying the decision invokes the handler.
	res := httptest.NewRecorder()
	err := s.ApplyDecision(res, decision)

	expect(t, badHostCalled, true)
	if err == nil || err.Error() != decision.Reason {
		t.Errorf("Expected error %q, got %v", decision.Reason, err)
	}
}

func TestEvaluateRequestDenyAllowRequestFunc(t *testing.T) {
	s := New(Options{
		AllowRequestFunc: func(_ *http.Request) bool { return false },
	})

	badRequestCalled := false
	s.SetBadRequestHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		badRequestCalled = true
	}))

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://www.example.com/foo", nil)
	req.Host = "www.deny-request.com"

	decision := s.EvaluateRequest(req)

	expect(t, decision.Action, ActionDeny)
	expect(t, decision.Reason, "request not allowed")
	expect(t, badRequestCalled, false)

	res := httptest.NewRecorder()
	if err := s.ApplyDecision(res, decision); err == nil {
		t.Errorf("Expected an error when applying a deny decision")
	}

	expect(t, badRequestCalled, true)
}

func TestEvaluateRequestRedirectDoesNotMutateRequestURL(t *testing.T) {
	s := New(Options{
		SSLRedirect:          true,
		SSLTemporaryRedirect: true,
		SSLHost:              "secure.example.com",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://www.example.com/foo", nil)
	req.Host = exampleHost

	decision := s.EvaluateRequest(req)

	expect(t, decision.Action, ActionRedirect)
	expect(t, decision.RedirectStatus, http.StatusTemporaryRedirect)
	expect(t, decision.RedirectURL, "https://secure.example.com/foo")
	expect(t, decision.Reason, "redirecting to HTTPS")

	// The original request URL must be left untouched.
	expect(t, req.URL.Scheme, httpSchema)
	expect(t, req.URL.Host, exampleHost)

	res := httptest.NewRecorder()
	if err := s.ApplyDecision(res, decision); err == nil {
		t.Errorf("Expected an error when applying a redirect decision")
	}

	expect(t, res.Code, http.StatusTemporaryRedirect)
	expect(t, res.Header().Get("Location"), "https://secure.example.com/foo")
}

func TestEvaluateRequestNonceIsGeneratedOnceAndReusedOnApply(t *testing.T) {
	s := New(Options{
		ContentSecurityPolicy: "default-src 'self' $NONCE",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://www.example.com/foo", nil)

	decision := s.EvaluateRequest(req)

	if decision.Nonce == "" {
		t.Fatalf("expected a nonce to be generated in the decision")
	}

	expectedHeader := "default-src 'self' 'nonce-" + decision.Nonce + "'"
	expect(t, decision.Header.Get(cspHeader), expectedHeader)
	// The nonce is not leaked onto the original request.
	expect(t, CSPNonce(req.Context()), "")

	// Applying the same decision must reuse the generated nonce instead of
	// generating a new one, even when applied multiple times.
	res := httptest.NewRecorder()
	if err := s.ApplyDecision(res, decision); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expect(t, res.Header().Get(cspHeader), expectedHeader)
	expect(t, CSPNonce(decision.Request.Context()), decision.Nonce)

	res = httptest.NewRecorder()
	if err := s.ApplyDecision(res, decision); err != nil {
		t.Fatalf("unexpected error on second apply: %v", err)
	}

	expect(t, res.Header().Get(cspHeader), expectedHeader)
}

func TestDecisionParityWithRealHandler(t *testing.T) {
	cspTemplate := "default-src 'self' $NONCE"
	noncePattern := regexp.MustCompile(`'nonce-[^']*'`)

	cases := []struct {
		name    string
		options Options
		url     string
		host    string
		headers map[string]string
	}{
		{
			name:    "allow with headers",
			options: Options{FrameDeny: true, ContentTypeNosniff: true},
			url:     "http://www.example.com/foo",
			host:    exampleHost,
		},
		{
			name:    "bad host",
			options: Options{AllowedHosts: []string{"sub.example.com"}},
			url:     "http://www.example.com/foo",
			host:    exampleHost,
		},
		{
			name:    "ssl redirect",
			options: Options{SSLRedirect: true},
			url:     "http://www.example.com/foo",
			host:    exampleHost,
		},
		{
			name:    "ssl redirect with host and temp status",
			options: Options{SSLRedirect: true, SSLTemporaryRedirect: true, SSLHost: "secure.example.com"},
			url:     "http://www.example.com/foo",
			host:    exampleHost,
		},
		{
			name: "allow request func deny",
			options: Options{
				AllowRequestFunc: func(_ *http.Request) bool { return false },
			},
			url:  "http://www.example.com/foo",
			host: "www.deny-request.com",
		},
		{
			name:    "allow with nonce",
			options: Options{ContentSecurityPolicy: cspTemplate},
			url:     "http://www.example.com/foo",
			host:    exampleHost,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newRequest := func() *http.Request {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.url, nil)
				req.Host = c.host

				for key, value := range c.headers {
					req.Header.Set(key, value)
				}

				return req
			}

			// Real response.
			s1 := New(c.options)
			realRes := httptest.NewRecorder()
			s1.Handler(myHandler).ServeHTTP(realRes, newRequest())

			// Pure evaluation followed by application.
			s2 := New(c.options)
			req := newRequest()
			beforeURL := req.URL.String()
			decision := s2.EvaluateRequest(req)
			expect(t, req.URL.String(), beforeURL)

			pureRes := httptest.NewRecorder()
			err := s2.ApplyDecision(pureRes, decision)
			if decision.Action == ActionAllow {
				expect(t, err, nil)
				myHandler.ServeHTTP(pureRes, decision.Request)
			} else if err == nil {
				t.Errorf("expected an error for action %d", decision.Action)
			}

			expect(t, pureRes.Code, realRes.Code)
			expect(t, pureRes.Body.String(), realRes.Body.String())

			realHeaders := realRes.Header().Clone()
			pureHeaders := pureRes.Header().Clone()
			realHeaders.Del("Date")
			pureHeaders.Del("Date")

			// Each evaluation generates its own random nonce, so normalize the
			// nonce values before comparing.
			realHeaders.Set(cspHeader, noncePattern.ReplaceAllString(realHeaders.Get(cspHeader), "'nonce-X'"))
			pureHeaders.Set(cspHeader, noncePattern.ReplaceAllString(pureHeaders.Get(cspHeader), "'nonce-X'"))

			if !reflect.DeepEqual(pureHeaders, realHeaders) {
				t.Errorf("header mismatch:\npure: %v\nreal: %v", pureHeaders, realHeaders)
			}
		})
	}
}

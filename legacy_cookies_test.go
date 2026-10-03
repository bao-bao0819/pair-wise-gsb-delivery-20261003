package nosurf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var legacyBaseCookie = http.Cookie{
	Name:   "old_csrf_token",
	Path:   "/app",
	Domain: "example.com",
}

// makeTokenPair generates a real token and returns it along with
// its cookie value and a masked "sent" token value.
func makeTokenPair() (realToken []byte, cookieValue, sentToken string) {
	realToken = generateToken()
	cookieValue = b64encode(realToken)
	sentToken = b64encode(maskToken(realToken))
	return
}

// legacyPostReq builds a POST request that passes the origin checks
// and carries the given sent token and cookies.
func legacyPostReq(sentToken string, cookies ...*http.Cookie) *http.Request {
	vals := [][]string{
		{"name", "Jolene"},
		{FormFieldName, sentToken},
	}
	req, err := http.NewRequest("POST", "http://example.com/", formBodyR(vals))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://example.com/")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func TestLegacyCookieMigrates(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(legacyBaseCookie)

	_, cookieValue, _ := makeTokenPair()

	req := dummyGet()
	req.AddCookie(&http.Cookie{Name: legacyBaseCookie.Name, Value: cookieValue})

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != 200 {
		t.Fatalf("Expected status 200, got %d", writer.Code)
	}

	resp := writer.Result()

	// The token must be re-issued under the current name.
	current := getRespCookie(resp, CookieName)
	if current == nil {
		t.Fatal("The current-name cookie was not set during migration.")
	}
	if current.Value != cookieValue {
		t.Errorf("The migrated token changed: expected %q, got %q",
			cookieValue, current.Value)
	}

	// The legacy cookie must be deleted within its own scope.
	legacy := getRespCookie(resp, legacyBaseCookie.Name)
	if legacy == nil {
		t.Fatal("The legacy cookie was not deleted during migration.")
	}
	if legacy.MaxAge >= 0 {
		t.Errorf("The legacy deletion cookie should have a negative Max-Age, got %d",
			legacy.MaxAge)
	}
	if legacy.Path != legacyBaseCookie.Path || legacy.Domain != legacyBaseCookie.Domain {
		t.Errorf("The deletion cookie must keep the legacy scope: got Path=%q, Domain=%q",
			legacy.Path, legacy.Domain)
	}
}

func TestLegacyCookieValidatesPost(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(legacyBaseCookie)
	hand.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("The request should have succeeded, but failed. Reason: %v", Reason(r))
	}))

	_, cookieValue, sentToken := makeTokenPair()

	req := legacyPostReq(sentToken, &http.Cookie{Name: legacyBaseCookie.Name, Value: cookieValue})

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != 200 {
		t.Fatalf("Expected status 200, got %d", writer.Code)
	}
}

func TestCorruptCurrentCookieIgnoresLegacy(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(legacyBaseCookie)
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	_, cookieValue, sentToken := makeTokenPair()

	// The current-name cookie is corrupt, the legacy one is valid
	// and matches the sent token. The request must still fail:
	// the current name is authoritative and never falls back.
	req := legacyPostReq(sentToken,
		&http.Cookie{Name: CookieName, Value: "corrupt"},
		&http.Cookie{Name: legacyBaseCookie.Name, Value: cookieValue},
	)

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("A corrupt current cookie must not fall back to a legacy one."+
			" Expected status %d, got %d", FailureCode, writer.Code)
	}

	// The legacy cookie was not consulted, so it must not be deleted either.
	if c := getRespCookie(writer.Result(), legacyBaseCookie.Name); c != nil {
		t.Error("The legacy cookie must not be touched when the current name is present.")
	}
}

func TestConflictingLegacyCookiesFail(t *testing.T) {
	otherLegacy := http.Cookie{Name: "older_csrf_token", Path: "/", Domain: "example.com"}

	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(legacyBaseCookie, otherLegacy)
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	_, value1, sentToken := makeTokenPair()
	_, value2, _ := makeTokenPair()

	// Two legacy cookies disagree. Even though the sent token
	// matches one of them, the request must not pass.
	req := legacyPostReq(sentToken,
		&http.Cookie{Name: legacyBaseCookie.Name, Value: value1},
		&http.Cookie{Name: otherLegacy.Name, Value: value2},
	)

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Conflicting legacy cookies must not validate."+
			" Expected status %d, got %d", FailureCode, writer.Code)
	}

	// Both stale legacy cookies get cleaned up, each in its own scope.
	resp := writer.Result()
	for _, base := range []http.Cookie{legacyBaseCookie, otherLegacy} {
		c := getRespCookie(resp, base.Name)
		if c == nil || c.MaxAge >= 0 {
			t.Errorf("The conflicting legacy cookie %q was not deleted.", base.Name)
			continue
		}
		if c.Path != base.Path || c.Domain != base.Domain {
			t.Errorf("The deletion cookie for %q has wrong scope: Path=%q, Domain=%q",
				base.Name, c.Path, c.Domain)
		}
	}
}

func TestDuplicateConflictingCookiesFail(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	_, value1, sentToken := makeTokenPair()
	_, value2, _ := makeTokenPair()

	// Two cookies share the current name but disagree.
	req := legacyPostReq(sentToken,
		&http.Cookie{Name: CookieName, Value: value1},
		&http.Cookie{Name: CookieName, Value: value2},
	)

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Duplicate conflicting cookies must not validate."+
			" Expected status %d, got %d", FailureCode, writer.Code)
	}
}

func TestDuplicateIdenticalCookiesPass(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("The request should have succeeded, but failed. Reason: %v", Reason(r))
	}))

	_, cookieValue, sentToken := makeTokenPair()

	req := legacyPostReq(sentToken,
		&http.Cookie{Name: CookieName, Value: cookieValue},
		&http.Cookie{Name: CookieName, Value: cookieValue},
	)

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != 200 {
		t.Fatalf("Identical duplicate cookies should validate."+
			" Expected status 200, got %d", writer.Code)
	}
}

func TestLegacyIgnoredAfterCompatPeriod(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetFailureHandler(correctReason(t, ErrBadToken))
	// No SetLegacyCookies call: the compatibility period is over.

	_, cookieValue, sentToken := makeTokenPair()

	// A safe request must not migrate or delete anything.
	getReq := dummyGet()
	getReq.AddCookie(&http.Cookie{Name: legacyBaseCookie.Name, Value: cookieValue})

	getWriter := httptest.NewRecorder()
	hand.ServeHTTP(getWriter, getReq)

	if c := getRespCookie(getWriter.Result(), legacyBaseCookie.Name); c != nil {
		t.Error("Legacy cookies must not be touched after the compatibility period.")
	}

	// A POST carrying only a legacy cookie must not validate.
	req := legacyPostReq(sentToken, &http.Cookie{Name: legacyBaseCookie.Name, Value: cookieValue})

	writer := httptest.NewRecorder()
	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Legacy cookies must not validate after the compatibility period."+
			" Expected status %d, got %d", FailureCode, writer.Code)
	}
}

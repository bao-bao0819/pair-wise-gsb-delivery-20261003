package nosurf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const legacyCookieName = "csrf_token_legacy"

// Builds a POST request that carries the given cookies and,
// if realToken is non-nil, a matching (masked) form token.
func legacyRequest(t *testing.T, realToken []byte, cookies ...*http.Cookie) *http.Request {
	t.Helper()

	vals := [][]string{{"name", "Jolene"}}
	if realToken != nil {
		vals = append(vals, []string{FormFieldName, b64encode(maskToken(realToken))})
	}

	req, err := http.NewRequest("POST", "/", formBodyR(vals))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.com"
	req.Header.Add("Referer", "https://example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	return req
}

// Returns every cookie with the given name set on the response.
func respCookies(resp *http.Response, name string) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func legacyHandler() *CSRFHandler {
	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(http.Cookie{
		Name:   legacyCookieName,
		Path:   "/legacy-path",
		Domain: "legacy.example.com",
	})
	return hand
}

func TestLegacyCookieMigrates(t *testing.T) {
	hand := legacyHandler()

	token := generateToken()
	req := legacyRequest(t, token, &http.Cookie{
		Name:  legacyCookieName,
		Value: b64encode(token),
	})
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != 200 {
		t.Fatalf("A valid legacy cookie should pass the check, got code %d", writer.Code)
	}

	resp := writer.Result()

	// The token is re-issued under the primary name only.
	primary := respCookies(resp, CookieName)
	if len(primary) != 1 {
		t.Fatalf("Expected exactly one %s cookie in the response, got %d", CookieName, len(primary))
	}
	if primary[0].Value != b64encode(token) {
		t.Errorf("The migrated cookie should carry the legacy token")
	}

	// The legacy cookie is deleted with its configured Path and Domain.
	legacy := respCookies(resp, legacyCookieName)
	if len(legacy) != 1 {
		t.Fatalf("Expected exactly one deletion cookie for %s, got %d", legacyCookieName, len(legacy))
	}
	if legacy[0].MaxAge >= 0 {
		t.Errorf("The legacy deletion cookie should have a negative Max-Age, got %d", legacy[0].MaxAge)
	}
	if legacy[0].Path != "/legacy-path" || legacy[0].Domain != "legacy.example.com" {
		t.Errorf("The deletion cookie should reuse the legacy Path/Domain, got Path=%q Domain=%q",
			legacy[0].Path, legacy[0].Domain)
	}
}

func TestLegacyCookieConflictingFail(t *testing.T) {
	hand := legacyHandler()
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	tokenA := generateToken()
	tokenB := generateToken()

	// The sent token matches one of the two contradictory cookies:
	// the request must not pass by picking it.
	req := legacyRequest(t, tokenA,
		&http.Cookie{Name: legacyCookieName, Value: b64encode(tokenA)},
		&http.Cookie{Name: legacyCookieName, Value: b64encode(tokenB)},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Conflicting legacy cookies should fail the check, got code %d", writer.Code)
	}

	// The contradictory cookies are cleaned up.
	legacy := respCookies(writer.Result(), legacyCookieName)
	if len(legacy) != 1 || legacy[0].MaxAge >= 0 {
		t.Errorf("Conflicting legacy cookies should be deleted, got %+v", legacy)
	}
}

func TestLegacyMultipleNamesConflictingFail(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetFailureHandler(correctReason(t, ErrBadToken))
	hand.SetLegacyCookies(
		http.Cookie{Name: "legacy_one"},
		http.Cookie{Name: "legacy_two"},
	)

	tokenA := generateToken()
	tokenB := generateToken()

	req := legacyRequest(t, tokenA,
		&http.Cookie{Name: "legacy_one", Value: b64encode(tokenA)},
		&http.Cookie{Name: "legacy_two", Value: b64encode(tokenB)},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Contradictory legacy names should fail the check, got code %d", writer.Code)
	}
}

func TestLegacyMultipleNamesConsistentPass(t *testing.T) {
	hand := New(http.HandlerFunc(succHand))
	hand.SetLegacyCookies(
		http.Cookie{Name: "legacy_one"},
		http.Cookie{Name: "legacy_two"},
	)

	token := generateToken()
	req := legacyRequest(t, token,
		&http.Cookie{Name: "legacy_one", Value: b64encode(token)},
		&http.Cookie{Name: "legacy_two", Value: b64encode(token)},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != 200 {
		t.Fatalf("Consistent legacy cookies should pass the check, got code %d", writer.Code)
	}

	resp := writer.Result()
	for _, name := range []string{"legacy_one", "legacy_two"} {
		legacy := respCookies(resp, name)
		if len(legacy) != 1 || legacy[0].MaxAge >= 0 {
			t.Errorf("Expected a deletion cookie for %s, got %+v", name, legacy)
		}
	}
}

func TestPrimaryCookiePresentIgnoresLegacy(t *testing.T) {
	hand := legacyHandler()
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	legacyToken := generateToken()

	// The primary cookie is present but corrupt: the valid legacy
	// cookie must not be used to pass the check.
	req := legacyRequest(t, legacyToken,
		&http.Cookie{Name: CookieName, Value: "corrupt"},
		&http.Cookie{Name: legacyCookieName, Value: b64encode(legacyToken)},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("A corrupt primary cookie should fail even with a valid legacy cookie, got code %d", writer.Code)
	}

	// The primary cookie is regenerated; the legacy cookie is left alone.
	primary := respCookies(writer.Result(), CookieName)
	if len(primary) != 1 {
		t.Errorf("Expected the primary cookie to be regenerated, got %+v", primary)
	}
	if legacy := respCookies(writer.Result(), legacyCookieName); len(legacy) != 0 {
		t.Errorf("The legacy cookie should not be touched when the primary is present, got %+v", legacy)
	}
}

func TestPrimaryCookieDuplicateConflict(t *testing.T) {
	hand := legacyHandler()
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	tokenA := generateToken()
	tokenB := generateToken()

	req := legacyRequest(t, tokenA,
		&http.Cookie{Name: CookieName, Value: b64encode(tokenA)},
		&http.Cookie{Name: CookieName, Value: b64encode(tokenB)},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Conflicting duplicate primary cookies should fail the check, got code %d", writer.Code)
	}
}

func TestLegacyCookieCorruptOnlyFails(t *testing.T) {
	hand := legacyHandler()
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	req := legacyRequest(t, generateToken(),
		&http.Cookie{Name: legacyCookieName, Value: "corrupt"},
	)
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("A corrupt legacy cookie should fail the check, got code %d", writer.Code)
	}

	legacy := respCookies(writer.Result(), legacyCookieName)
	if len(legacy) != 1 || legacy[0].MaxAge >= 0 {
		t.Errorf("The corrupt legacy cookie should be deleted, got %+v", legacy)
	}
}

func TestLegacyCookieAfterMigrationPeriod(t *testing.T) {
	hand := legacyHandler()
	hand.SetFailureHandler(correctReason(t, ErrBadToken))

	// End the compatibility period.
	hand.SetLegacyCookies()

	token := generateToken()
	req := legacyRequest(t, token, &http.Cookie{
		Name:  legacyCookieName,
		Value: b64encode(token),
	})
	writer := httptest.NewRecorder()

	hand.ServeHTTP(writer, req)

	if writer.Code != FailureCode {
		t.Fatalf("Legacy cookies must not validate after the migration period, got code %d", writer.Code)
	}

	if legacy := respCookies(writer.Result(), legacyCookieName); len(legacy) != 0 {
		t.Errorf("No deletion cookie should be issued after the migration period, got %+v", legacy)
	}
}

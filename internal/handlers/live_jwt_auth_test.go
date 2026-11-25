package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// liveJWTTestKey is the test signing key; the Router under test must carry it
// in JWTSigningKey so the minted tokens validate.
const liveJWTTestKey = "test-global-jwt-signing-key"

// jwtRouter builds a test Router with the test signing key configured.
func jwtRouter() *Router {
	r := NewRouterWithLogger(testLogger())
	r.JWTSigningKey = liveJWTTestKey
	return r
}

// mintLiveJWT mirrors global issueSessionJWT: HS256, Subject = account_id,
// issuer "risegen-global".
func mintLiveJWT(t *testing.T, subject string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:  subject,
		Issuer:   "risegen-global",
		IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	s, err := tok.SignedString([]byte(liveJWTTestKey))
	if err != nil {
		t.Fatalf("mint JWT: %v", err)
	}
	return s
}

// TestLiveJWTAuth is the US6 counter: the live list/attach endpoints resolve
// the caller's account from the global session JWT Subject (HS256), not from
// a ':' split of the bearer. Counters: matching account lists 1 session,
// foreign account lists 0, non-JWT bearer and expired JWT get 401.
func TestLiveJWTAuth(t *testing.T) {
	r := jwtRouter()

	daemon := &liveDaemon{machineID: "machine-a", account: "acct-123"}
	r.trackLiveSession(daemon, "sess-1")

	mux := NewRouter(r)
	valid := mintLiveJWT(t, "acct-123", time.Now().Add(time.Hour))
	foreign := mintLiveJWT(t, "other-acct", time.Now().Add(time.Hour))
	expired := mintLiveJWT(t, "acct-123", time.Now().Add(-time.Hour))

	list := func(t *testing.T, bearer string) (int, *httptest.ResponseRecorder) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/live/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		n := 0
		if rec.Code == http.StatusOK {
			var body struct {
				Sessions []struct {
					SessionID string `json:"session_id"`
				} `json:"sessions"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode list: %v (body %s)", err, rec.Body.String())
			}
			n = len(body.Sessions)
		}
		return n, rec
	}

	// Counter 1: matching Subject lists exactly 1 session.
	n, rec := list(t, valid)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching JWT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if n != 1 {
		t.Errorf("matching JWT: sessions = %d, want 1 (body %s)", n, rec.Body.String())
	}

	// Counter 2: foreign Subject lists 0 sessions.
	n, rec = list(t, foreign)
	if rec.Code != http.StatusOK {
		t.Fatalf("foreign JWT: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if n != 0 {
		t.Errorf("foreign JWT: sessions = %d, want 0 (body %s)", n, rec.Body.String())
	}

	// Counter 3: a non-JWT bearer with no ':' is 401, never an account.
	// (Old split code returned "garbage" as the account.)
	_, rec = list(t, "garbage")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("non-JWT bearer: status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	// Counter 4: expired JWT is 401.
	_, rec = list(t, expired)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired JWT: status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	// Counter 5: foreign Subject attach is 403 session_not_yours.
	req := httptest.NewRequest(http.MethodGet, "/v1/live/sessions/sess-1", nil)
	req.Header.Set("Authorization", "Bearer "+foreign)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign attach: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("attach response not JSON: %v", err)
	}
	if body["error"] != "session_not_yours" {
		t.Errorf("foreign attach: error = %q, want session_not_yours", body["error"])
	}
}

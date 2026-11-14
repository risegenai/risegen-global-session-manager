package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newNilCassandraRouter builds the real mux with a nil Cassandra session, the
// state main.go passes at boot when Cassandra was unreachable.
func newNilCassandraRouter() http.Handler {
	return NewRouter(&Router{
		Cassandra: nil,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// TestRequireCassandra_Returns503WhenSessionNil asserts every wrapped session,
// scope, aggregation and publication route answers 503 with a JSON
// cassandra_unavailable body when the service started without persistence,
// instead of nil-panicking inside the handler.
func TestRequireCassandra_Returns503WhenSessionNil(t *testing.T) {
	h := newNilCassandraRouter()

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/global/sessions"},
		{http.MethodGet, "/v1/global/sessions/recent"},
		{http.MethodGet, "/v1/global/sessions/00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/v1/global/sessions/00000000-0000-0000-0000-000000000000/events"},
		{http.MethodGet, "/v1/global/sessions/00000000-0000-0000-0000-000000000000/events"},
		{http.MethodPost, "/v1/scopes"},
		{http.MethodGet, "/v1/scopes/some-scope"},
		{http.MethodPut, "/v1/scopes/some-scope/heartbeat"},
		{http.MethodGet, "/v1/aggregated/sessions"},
		{http.MethodGet, "/v1/aggregated/sessions/some-scope/00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/v1/publication/session"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}

			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body["error"] != "cassandra_unavailable" {
				t.Fatalf("error = %q, want cassandra_unavailable", body["error"])
			}
		})
	}
}

// TestHandleHealthz_DisconnectedWhenSessionNil asserts the healthcheck reports
// HTTP 503 and ok:false with cassandra:disconnected when the session is nil,
// instead of dereferencing a nil *gocql.Session.
func TestHandleHealthz_DisconnectedWhenSessionNil(t *testing.T) {
	h := newNilCassandraRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["cassandra"] != "disconnected" {
		t.Fatalf("cassandra = %v, want disconnected", body["cassandra"])
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Fatalf("ok = %v, want false", body["ok"])
	}
}

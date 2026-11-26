package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestLiveFrameForwardedToAttachedClient dials a daemon and a client onto the
// same live session and asserts that a frame the daemon sends for that
// session reaches the attached client.
func TestLiveFrameForwardedToAttachedClient(t *testing.T) {
	r := jwtRouter()
	srv := httptest.NewServer(NewRouter(r))
	defer srv.Close()

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	daemonURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/live/daemon"
	daemonConn, _, err := dialer.Dial(daemonURL, nil)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer daemonConn.Close()

	reg := map[string]string{
		"machine_id":    "machine-a",
		"account":       "acct-a",
		"canonical_url": "https://gsm.example.com",
	}
	if err := daemonConn.WriteJSON(reg); err != nil {
		t.Fatalf("send registration: %v", err)
	}

	clientURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/live/sessions/sess-1"
	clientConn, resp, err := dialer.Dial(clientURL, http.Header{
		"Authorization": []string{"Bearer " + mintLiveJWT(t, "acct-a", time.Now().Add(time.Hour))},
	})
	if err != nil {
		t.Fatalf("dial client: %v (status %v)", err, resp)
	}
	defer clientConn.Close()

	frame := liveFrame{
		SessionID: "sess-1",
		Frame:     json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"sess-1"}}`),
	}
	if err := daemonConn.WriteJSON(frame); err != nil {
		t.Fatalf("send frame: %v", err)
	}

	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !strings.Contains(string(msg), "session/update") {
		t.Fatalf("client message missing frame: %s", msg)
	}
}

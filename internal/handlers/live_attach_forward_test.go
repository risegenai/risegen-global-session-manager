package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestAttachClientFrameForwardedToDaemon dials a daemon and a client onto the
// same live session and asserts that a frame the client sends for that
// session reaches the daemon socket as the same raw envelope (ADR 0083 §3:
// GSM relays frames unchanged both directions).
func TestAttachClientFrameForwardedToDaemon(t *testing.T) {
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

	envelope := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","method":"session/prompt","params":{"session_id":"sess-1","prompt":[{"type":"text","text":"hi"}]}}}`
	if err := clientConn.WriteMessage(websocket.TextMessage, []byte(envelope)); err != nil {
		t.Fatalf("send client frame: %v", err)
	}

	daemonConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := daemonConn.ReadMessage()
	if err != nil {
		t.Fatalf("daemon read: %v", err)
	}
	if string(msg) != envelope {
		t.Fatalf("daemon got %q, want the same raw envelope %q", msg, envelope)
	}
}

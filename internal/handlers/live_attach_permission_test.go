package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestAttachPermissionDecisionFirstWins asserts that a client response frame
// carrying result.outcome records a permission decision keyed by
// (session_id, request_id): the first decision is forwarded raw to the
// daemon and stored; a second decision for the same request is NOT
// forwarded, the client gets an already_decided error frame, and the stored
// decision is unchanged.
func TestAttachPermissionDecisionFirstWins(t *testing.T) {
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

	first := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","id":7,"result":{"outcome":{"outcome":"selected","optionId":"allow"}}}}`
	if err := clientConn.WriteMessage(websocket.TextMessage, []byte(first)); err != nil {
		t.Fatalf("send first frame: %v", err)
	}

	daemonConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := daemonConn.ReadMessage()
	if err != nil {
		t.Fatalf("daemon read: %v", err)
	}
	if string(msg) != first {
		t.Fatalf("daemon got %q, want the raw frame %q", msg, first)
	}
	if d, ok := r.permissionDecision("sess-1", "7"); !ok || d != "allow" {
		t.Fatalf("permissionDecision = (%q, %v), want (allow, true)", d, ok)
	}

	second := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","id":7,"result":{"outcome":{"outcome":"selected","optionId":"deny"}}}}`
	if err := clientConn.WriteMessage(websocket.TextMessage, []byte(second)); err != nil {
		t.Fatalf("send second frame: %v", err)
	}

	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, errFrame, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !strings.Contains(string(errFrame), "already_decided") {
		t.Fatalf("client got %q, want an error frame containing already_decided", errFrame)
	}

	daemonConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := daemonConn.ReadMessage(); err == nil {
		t.Fatal("daemon received the second frame, want no forward")
	}
	if d, ok := r.permissionDecision("sess-1", "7"); !ok || d != "allow" {
		t.Fatalf("permissionDecision after second frame = (%q, %v), want (allow, true)", d, ok)
	}
}

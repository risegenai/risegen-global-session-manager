package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// attachHistoryEnvelope is the shape of a replayed history frame: the live
// envelope plus the history marker, seq, event_type and ts.
type attachHistoryEnvelope struct {
	SessionID string          `json:"session_id"`
	Frame     json.RawMessage `json:"frame"`
	History   bool            `json:"history"`
	Seq       int64           `json:"seq"`
	EventType string          `json:"event_type"`
	TS        time.Time       `json:"ts"`
}

// dialAttachHistory wires a daemon (machine-a / acct-a) and a client onto
// session sess-1, mirroring the dial pattern of live_attach_forward_test.go.
func dialAttachHistory(t *testing.T, r *Router) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	srv := httptest.NewServer(NewRouter(r))
	t.Cleanup(srv.Close)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	daemonURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/live/daemon"
	daemonConn, _, err := dialer.Dial(daemonURL, nil)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	t.Cleanup(func() { daemonConn.Close() })

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
	t.Cleanup(func() { clientConn.Close() })

	return daemonConn, clientConn
}

// TestAttachReplaysHistoryBeforeLive asserts that an attach first receives
// the session's durable history (seq ASC, history:true) and only then the
// live frames. Counter: history-frames = 3.
func TestAttachReplaysHistoryBeforeLive(t *testing.T) {
	r := jwtRouter()

	events := []historyEvent{
		{Seq: 1, EventID: "ev-1", EventType: "session/update", TurnID: "turn-1",
			Payload: `{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}`,
			TS: time.Date(2026, 11, 24, 10, 0, 1, 0, time.UTC)},
		{Seq: 2, EventID: "ev-2", EventType: "session/update", TurnID: "turn-1",
			Payload: `{"jsonrpc":"2.0","method":"session/update","params":{"n":2}}`,
			TS: time.Date(2026, 11, 24, 10, 0, 2, 0, time.UTC)},
		{Seq: 3, EventID: "ev-3", EventType: "session/update", TurnID: "turn-1",
			Payload: `{"jsonrpc":"2.0","method":"session/update","params":{"n":3}}`,
			TS: time.Date(2026, 11, 24, 10, 0, 3, 0, time.UTC)},
	}
	historyReaderForTest = func(*Router, string) ([]historyEvent, error) { return events, nil }
	t.Cleanup(func() { historyReaderForTest = nil })

	daemonConn, clientConn := dialAttachHistory(t, r)

	liveEnvelope := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","method":"session/update","params":{"n":4}}}`
	if err := daemonConn.WriteMessage(websocket.TextMessage, []byte(liveEnvelope)); err != nil {
		t.Fatalf("send live frame: %v", err)
	}

	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	historyFrames := 0
	for i := 0; i < 4; i++ {
		_, msg, err := clientConn.ReadMessage()
		if err != nil {
			t.Fatalf("client read %d: %v", i+1, err)
		}
		if i < 3 {
			var env attachHistoryEnvelope
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("message %d is not a history envelope: %v (%s)", i+1, err, msg)
			}
			if !env.History {
				t.Fatalf("message %d: history flag = false, want true (%s)", i+1, msg)
			}
			if env.SessionID != "sess-1" {
				t.Fatalf("message %d: session_id = %q, want sess-1", i+1, env.SessionID)
			}
			if env.Seq != int64(i+1) {
				t.Fatalf("message %d: seq = %d, want %d (history must replay in seq ASC order)", i+1, env.Seq, i+1)
			}
			if env.EventType != "session/update" {
				t.Fatalf("message %d: event_type = %q, want session/update", i+1, env.EventType)
			}
			var frame struct {
				Params struct {
					N int `json:"n"`
				} `json:"params"`
			}
			if err := json.Unmarshal(env.Frame, &frame); err != nil {
				t.Fatalf("message %d: frame is not the event payload: %v", i+1, err)
			}
			if frame.Params.N != i+1 {
				t.Fatalf("message %d: frame params.n = %d, want %d", i+1, frame.Params.N, i+1)
			}
			historyFrames++
		} else {
			if string(msg) != liveEnvelope {
				t.Fatalf("message 4 = %q, want the live envelope unchanged %q", msg, liveEnvelope)
			}
		}
	}
	if historyFrames != 3 {
		t.Fatalf("history-frames = %d, want 3", historyFrames)
	}
}

// TestAttachLiveDuringReplayIsNotLost asserts that a live frame sent while
// the history fetch is still in flight is not lost and is delivered after
// the replay, in order. Counter: live-frames-delivered = 1.
func TestAttachLiveDuringReplayIsNotLost(t *testing.T) {
	r := jwtRouter()

	release := make(chan struct{})
	historyReaderForTest = func(*Router, string) ([]historyEvent, error) {
		<-release
		return []historyEvent{
			{Seq: 1, EventID: "ev-1", EventType: "session/update", TurnID: "turn-1",
				Payload: `{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}`,
				TS: time.Date(2026, 11, 24, 10, 0, 1, 0, time.UTC)},
		}, nil
	}
	t.Cleanup(func() { historyReaderForTest = nil })

	daemonConn, clientConn := dialAttachHistory(t, r)

	// The attach handler is now blocked inside the history fetch; send the
	// live frame while replay is pending.
	liveEnvelope := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","method":"session/update","params":{"n":2}}}`
	if err := daemonConn.WriteMessage(websocket.TextMessage, []byte(liveEnvelope)); err != nil {
		t.Fatalf("send live frame: %v", err)
	}
	close(release)

	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, first, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read 1: %v", err)
	}
	var env attachHistoryEnvelope
	if err := json.Unmarshal(first, &env); err != nil || !env.History || env.Seq != 1 {
		t.Fatalf("message 1 = %q, want the history envelope (history:true, seq 1)", first)
	}
	_, second, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read 2: %v", err)
	}
	liveFramesDelivered := 0
	if string(second) == liveEnvelope {
		liveFramesDelivered++
	}
	if liveFramesDelivered != 1 {
		t.Fatalf("live-frames-delivered = %d, want 1 (message 2 = %q)", liveFramesDelivered, second)
	}
}

// TestAttachForeignAccountReadsNoHistory asserts that a refused attach
// (403 session_not_yours) never reads history: auth happens before the
// upgrade and the history fetch happens after it. Counter: reader calls = 0.
func TestAttachForeignAccountReadsNoHistory(t *testing.T) {
	r := jwtRouter()

	var readerCalls int32
	historyReaderForTest = func(*Router, string) ([]historyEvent, error) {
		atomic.AddInt32(&readerCalls, 1)
		return nil, nil
	}
	t.Cleanup(func() { historyReaderForTest = nil })

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
	_, resp, err := dialer.Dial(clientURL, http.Header{
		"Authorization": []string{"Bearer " + mintLiveJWT(t, "acct-b", time.Now().Add(time.Hour))},
	})
	if err == nil {
		t.Fatalf("dial with foreign account succeeded, want 403 session_not_yours")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dial status = %v, want 403", resp)
	}
	if got := atomic.LoadInt32(&readerCalls); got != 0 {
		t.Fatalf("history reader calls = %d, want 0 (refused attach must not read history)", got)
	}
}

// TestAttachWithoutHistorySourceIsLiveOnly guards the existing behavior:
// with no history reader and a nil Cassandra session, an attach is live-only
// and live frames still flow.
func TestAttachWithoutHistorySourceIsLiveOnly(t *testing.T) {
	r := jwtRouter()
	if r.Cassandra != nil {
		t.Fatalf("test requires a nil Cassandra session, got %v", r.Cassandra)
	}
	if historyReaderForTest != nil {
		t.Fatalf("test requires no history reader override")
	}

	daemonConn, clientConn := dialAttachHistory(t, r)

	liveEnvelope := `{"session_id":"sess-1","frame":{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}}`
	if err := daemonConn.WriteMessage(websocket.TextMessage, []byte(liveEnvelope)); err != nil {
		t.Fatalf("send live frame: %v", err)
	}

	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(msg) != liveEnvelope {
		t.Fatalf("client got %q, want the live envelope unchanged %q", msg, liveEnvelope)
	}
}

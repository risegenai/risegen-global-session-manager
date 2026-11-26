package cassandra

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/gocql/gocql"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestConnectRetryAttemptsUntilSuccess: connect fails twice, then succeeds.
// Counter must be EXACTLY 3 and the returned error must be nil.
func TestConnectRetryAttemptsUntilSuccess(t *testing.T) {
	calls := 0
	connect := func(hosts []string, keyspace string, logger *slog.Logger) (*gocql.Session, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("cassandra not ready")
		}
		return nil, nil
	}

	sess, err := ConnectWithRetry([]string{"127.0.0.1:9042"}, "global_sessions", discardLogger(), 5, 0, connect)
	if err != nil {
		t.Fatalf("expected nil error after success, got %v", err)
	}
	if sess != nil {
		t.Fatalf("expected nil session from fake connect, got %v", sess)
	}
	if calls != 3 {
		t.Fatalf("expected exactly 3 connect attempts, got %d", calls)
	}
}

// TestConnectRetryGivesUpAfterBudget: connect always fails, attempts=4.
// Counter must be EXACTLY 4 and the returned error must be non-nil
// (fail-soft preserved).
func TestConnectRetryGivesUpAfterBudget(t *testing.T) {
	calls := 0
	connect := func(hosts []string, keyspace string, logger *slog.Logger) (*gocql.Session, error) {
		calls++
		return nil, errors.New("cassandra refused")
	}

	sess, err := ConnectWithRetry([]string{"127.0.0.1:9042"}, "global_sessions", discardLogger(), 4, 0, connect)
	if err == nil {
		t.Fatal("expected non-nil error after budget exhausted, got nil")
	}
	if sess != nil {
		t.Fatalf("expected nil session after budget exhausted, got %v", sess)
	}
	if calls != 4 {
		t.Fatalf("expected exactly 4 connect attempts, got %d", calls)
	}
}

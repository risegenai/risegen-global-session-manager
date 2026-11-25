package cassandra

import (
	"log/slog"
	"time"

	"github.com/gocql/gocql"
)

// ConnectWithRetry calls connect up to attempts times, sleeping delay
// between attempts, so a boot race with Cassandra self-heals. Fail-soft
// is preserved: after the budget is exhausted the last error is returned.
func ConnectWithRetry(hosts []string, keyspace string, logger *slog.Logger,
	attempts int, delay time.Duration,
	connect func([]string, string, *slog.Logger) (*gocql.Session, error),
) (*gocql.Session, error) {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		sess, err := connect(hosts, keyspace, logger)
		if err == nil {
			return sess, nil
		}
		lastErr = err
		logger.Warn("cassandra: connect retry", "attempt", i, "error", err)
		if i < attempts && delay > 0 {
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}

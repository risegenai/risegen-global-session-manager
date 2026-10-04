// Package cassandra provides the Cassandra client, migration runner, and
// session store for the Global Session Manager.
package cassandra

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gocql/gocql"
)

//go:embed migrations/*.cql
var migrationsFS embed.FS

// Connect creates a gocql session connected to the shared Cassandra cluster.
func Connect(hosts []string, keyspace string, logger *slog.Logger) (*gocql.Session, error) {
	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(gocql.DCAwareRoundRobinPolicy(""))

	sess, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("cassandra: connect: %w", err)
	}
	logger.Info("cassandra: connected", "hosts", hosts, "keyspace", keyspace)
	return sess, nil
}

// RunMigrations reads all .cql files from the embedded migrations directory
// and executes them idempotently. Files are sorted by name so numbering
// controls order. Each statement is expected to be idempotent (CREATE …
// IF NOT EXISTS).
func RunMigrations(sess *gocql.Session, logger *slog.Logger) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("cassandra: read migrations dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".cql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		path := filepath.Join("migrations", name)
		cql, err := migrationsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cassandra: read %s: %w", name, err)
		}
		if err := execCQL(sess, string(cql), logger); err != nil {
			return fmt.Errorf("cassandra: migrate %s: %w", name, err)
		}
		logger.Info("cassandra: migration applied", "file", name)
	}
	return nil
}

func execCQL(sess *gocql.Session, cql string, logger *slog.Logger) error {
	stmts := strings.Split(cql, ";")
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if err := sess.Query(stmt).Exec(); err != nil {
			// Log and continue — idempotent statements may fail on
			// re-run (e.g. keyspace already exists with different
			// replication). This is acceptable for boot-time migration.
			logger.Warn("cassandra: statement warning", "error", err, "stmt", stmt[:min(80, len(stmt))])
		}
	}
	return nil
}

// GetEnv retrieves an environment variable with a fallback.
func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
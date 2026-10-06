package cassandra

import (
	"strings"
	"testing"
)

func TestMigrationStatementsIgnoreComments(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/0001_global_sessions_keyspace.cql")
	if err != nil {
		t.Fatal(err)
	}
	stmts := cqlStatements(string(raw))
	if len(stmts) != 5 {
		t.Fatalf("statements = %d, want 5 (keyspace + 4 tables)", len(stmts))
	}
	for _, stmt := range stmts {
		if !strings.HasPrefix(stmt, "CREATE ") {
			t.Fatalf("comment leaked into statement: %q", stmt[:min(80, len(stmt))])
		}
	}
}

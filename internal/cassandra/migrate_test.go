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

// TestMigrationsCreateKeyspace locks the invariant that the global_sessions
// keyspace is created by the embedded migrations, not by Connect(). Binding
// the keyspace in Connect() made CreateSession fail on a fresh cluster (the
// keyspace does not exist yet), which left the GSM with a nil session and
// never ran the migration that creates it — a permanent deadlock. The keyspace
// therefore must exist on its own in the migrations, ahead of the tables.
func TestMigrationsCreateKeyspace(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}

	var keyspaceFile, keyspaceBody string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cql") {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToUpper(string(raw)), "CREATE KEYSPACE") {
			keyspaceFile = e.Name()
			keyspaceBody = string(raw)
			break
		}
	}

	if keyspaceFile == "" {
		t.Fatal("no migration creates a keyspace; Connect must not bind one")
	}

	upper := strings.ToUpper(keyspaceBody)
	if !strings.Contains(upper, "GLOBAL_SESSIONS") {
		t.Fatalf("%s does not create the global_sessions keyspace", keyspaceFile)
	}

	ksIdx := strings.Index(upper, "CREATE KEYSPACE")
	tblIdx := strings.Index(upper, "CREATE TABLE")
	if tblIdx >= 0 && ksIdx > tblIdx {
		t.Fatalf("%s creates a table before the keyspace it belongs to", keyspaceFile)
	}
}

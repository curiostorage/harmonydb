package harmonyquery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/curiostorage/harmonyquery/pglite"
)

func TestHarmonyqueryPgliteSerialInserts(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	db, err := NewFromConfig(Config{
		Pglite:           pglite.UseInternalDB(dataDir),
		Schema:            "curio",
		SqlEmbedFS:        pgliteTestMigrations,
		DowngradeEmbedFS:  pgliteTestMigrations,
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for i, name := range names {
		count, err := db.Exec(ctx, "INSERT INTO pglite_hq_test (name) VALUES ($1)", name)
		if err != nil {
			t.Fatalf("serial insert #%d (%q): %v", i+1, name, err)
		}
		if count != 1 {
			t.Fatalf("insert %q: expected 1 row affected, got %d", name, count)
		}
	}

	var all []struct {
		Name string
	}
	if err := db.Select(ctx, &all, "SELECT name FROM pglite_hq_test ORDER BY name"); err != nil {
		t.Fatalf("Select all: %v", err)
	}
	if len(all) != len(names) {
		t.Fatalf("expected %d rows, got %+v", len(names), all)
	}
}

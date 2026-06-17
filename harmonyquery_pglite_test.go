package harmonyquery

import (
	"context"
	"embed"
	"io/fs"
	"path/filepath"
	"testing"
)

//go:embed testdata
var pgliteTestData embed.FS

var pgliteTestMigrations fs.FS

func init() {
	sub, err := fs.Sub(pgliteTestData, "testdata")
	if err != nil {
		panic(err)
	}
	pgliteTestMigrations = sub
}

func TestHarmonyqueryPglite(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	db, err := NewFromConfig(Config{
		PgliteStoragePath: dataDir,
		Schema:            "curio",
		SqlEmbedFS:        pgliteTestMigrations,
		DowngradeEmbedFS:  pgliteTestMigrations,
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	var searchPath string
	if err := db.QueryRow(ctx, "SHOW search_path").Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	if searchPath != "curio" {
		t.Fatalf("search_path: got %q, want curio", searchPath)
	}

	var schemaName string
	if err := db.QueryRow(ctx,
		"SELECT schemaname FROM pg_catalog.pg_tables WHERE tablename = 'pglite_hq_test'").
		Scan(&schemaName); err != nil {
		t.Fatalf("pg_tables lookup: %v", err)
	}
	if schemaName != "curio" {
		t.Fatalf("migration table schema: got %q, want curio", schemaName)
	}

	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, name := range names {
		count, err := db.Exec(ctx, "INSERT INTO pglite_hq_test (name) VALUES ($1)", name)
		if err != nil {
			t.Fatalf("Exec insert %q: %v", name, err)
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

	wantSubset := []string{"bravo", "delta", "echo"}
	var subset []struct {
		Name string
	}
	if err := db.Select(ctx, &subset,
		"SELECT name FROM pglite_hq_test WHERE name = ANY($1) ORDER BY name",
		wantSubset,
	); err != nil {
		t.Fatalf("Select subset: %v", err)
	}
	if len(subset) != len(wantSubset) {
		t.Fatalf("expected subset %v, got %+v", wantSubset, subset)
	}
	for i, want := range wantSubset {
		if subset[i].Name != want {
			t.Fatalf("subset[%d]: got %q, want %q", i, subset[i].Name, want)
		}
	}
}

func TestHarmonyqueryPgliteITest(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "pgbase")
	db, err := NewFromConfig(Config{
		PgliteStoragePath: baseDir,
		Schema:            "public",
		SqlEmbedFS:        pgliteTestMigrations,
		DowngradeEmbedFS:  pgliteTestMigrations,
		ITestID:           ITestNewID(),
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { db.ITestDeleteAll() })

	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO pglite_hq_test (name) VALUES ($1)", "itest"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
}

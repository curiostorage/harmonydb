package pglite_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/curiostorage/harmonyquery/pglite"
	"github.com/yugabyte/pgx/v5"
	"github.com/yugabyte/pgx/v5/pgxpool"
	_ "github.com/yugabyte/pgx/v5/stdlib"
)

const testSchema = "curio"

func TestPGliteRequiresDataDir(t *testing.T) {
	_, _, err := pglite.Start(context.Background(), pglite.Config{})
	if err == nil {
		t.Fatal("expected error for empty DataDir")
	}
}

func TestPGliteConfigDefaults(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Start with defaults: %v", err)
	}
	defer cleanup()

	pool := openPool(t, harmonyqueryConnString(t, socketDir, testSchema, "harmonyquery.test"), testSchema)
	defer pool.Close()

	var userName string
	if err := pool.QueryRow(context.Background(), `SELECT current_user`).Scan(&userName); err != nil {
		t.Fatalf("current_user: %v", err)
	}
	if userName != "postgres" {
		t.Fatalf("User default: got %q, want postgres", userName)
	}
}

func TestPGliteConfigStdoutStderr(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	stdoutPath := filepath.Join(t.TempDir(), "pg.stdout")
	stderrPath := filepath.Join(t.TempDir(), "pg.stderr")

	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{
		DataDir:    dataDir,
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
	})
	if err != nil {
		if b, readErr := os.ReadFile(stderrPath); readErr == nil && len(b) > 0 {
			t.Logf("pglite stderr:\n%s", b)
		}
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	pool := openPool(t, "host="+socketDir+" dbname=postgres user=postgres sslmode=disable", "")
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}

	for _, path := range []string{stdoutPath, stderrPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestPGliteConfigDatabaseUser(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{
		DataDir:  dataDir,
		Database: "postgres",
		User:     "postgres",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	pool := openPool(t, "host="+socketDir+" dbname=postgres user=postgres sslmode=disable", "")
	defer pool.Close()

	var one int
	if err := pool.QueryRow(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
}

func TestPGliteCleanup(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	socketPath := filepath.Join(socketDir, ".s.PGSQL.5432")
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket missing before cleanup: %v", err)
	}

	cleanup()
	if _, err := os.Stat(socketDir); !os.IsNotExist(err) {
		t.Fatalf("socket dir should be removed after cleanup, stat err=%v", err)
	}

	cleanup() // must be safe to call twice
}

func TestPGliteSecondStartSameDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")

	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	pool := openPool(t, "host="+socketDir+" dbname=postgres user=postgres sslmode=disable", "")
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS init_check (n int)`); err != nil {
		t.Fatalf("init table: %v", err)
	}
	pool.Close()
	cleanup()

	socketDir, cleanup, err = pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("second Start on existing data dir: %v", err)
	}
	defer cleanup()

	conn, err := pgx.Connect(context.Background(), "host="+socketDir+" dbname=postgres user=postgres sslmode=disable")
	if err != nil {
		t.Fatalf("connect after second Start: %v", err)
	}
	defer conn.Close(context.Background())

	var one int
	if err := conn.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("query after second Start: %v", err)
	}
	if one != 1 {
		t.Fatalf("got %d, want 1", one)
	}
}

func TestPGliteSearchPath(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	connStr := harmonyqueryConnString(t, socketDir, testSchema, "harmonyquery.test")
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams["search_path"] != testSchema {
		t.Fatalf("conn string search_path param: got %q, want %q",
			cfg.ConnConfig.RuntimeParams["search_path"], testSchema)
	}

	pool := openPool(t, connStr, testSchema)
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+testSchema); err != nil {
		t.Fatalf("CREATE SCHEMA: %v", err)
	}

	var searchPath string
	if err := pool.QueryRow(ctx, `SHOW search_path`).Scan(&searchPath); err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}
	if searchPath != testSchema {
		t.Fatalf("search_path: got %q, want %q", searchPath, testSchema)
	}

	var currentSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if currentSchema != testSchema {
		t.Fatalf("current_schema: got %q, want %q", currentSchema, testSchema)
	}

	const table = "pglite_search_path_test"
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (id serial primary key, name text NOT NULL)`, table)); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	if _, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s (name) VALUES ($1)`, table), "sp-check"); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var schemaName string
	if err := pool.QueryRow(ctx,
		`SELECT schemaname FROM pg_catalog.pg_tables WHERE tablename = $1`, table).
		Scan(&schemaName); err != nil {
		t.Fatalf("pg_tables lookup: %v", err)
	}
	if schemaName != testSchema {
		t.Fatalf("table schema: got %q, want %q", schemaName, testSchema)
	}

	var name string
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT name FROM %s WHERE id = 1`, table)).Scan(&name); err != nil {
		t.Fatalf("SELECT unqualified: %v", err)
	}
	if name != "sp-check" {
		t.Fatalf("got %q, want sp-check", name)
	}
}

func TestPGliteSerialInserts(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	connStr := harmonyqueryConnString(t, socketDir, testSchema, "harmonyquery.test")
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinConns = 1
	cfg.AfterConnect = searchPathAfterConnect(testSchema)

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+testSchema); err != nil {
		t.Fatalf("CREATE SCHEMA: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS pglite_serial_test (id serial primary key, name text)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	names := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for i, name := range names {
		if _, err := pool.Exec(ctx, `INSERT INTO pglite_serial_test (name) VALUES ($1)`, name); err != nil {
			t.Fatalf("insert #%d (%q): %v", i+1, name, err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pglite_serial_test`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != len(names) {
		t.Fatalf("expected %d rows, got %d", len(names), count)
	}
}

func TestPGliteDatabaseSQL(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	connStr := "host=" + socketDir + " dbname=postgres user=postgres sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS pglite_sql_test (id serial primary key, name text)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO pglite_sql_test (name) VALUES ('via-sql')`); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM pglite_sql_test WHERE id = 1`).Scan(&name); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if name != "via-sql" {
		t.Fatalf("got %q, want via-sql", name)
	}
}

func TestPGliteSmoke(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "pgdata")
	stderrPath := filepath.Join(t.TempDir(), "pg.stderr")
	socketDir, cleanup, err := pglite.Start(context.Background(), pglite.Config{
		DataDir:    dataDir,
		StderrFile: stderrPath,
	})
	if err != nil {
		if b, readErr := os.ReadFile(stderrPath); readErr == nil && len(b) > 0 {
			t.Logf("pglite stderr:\n%s", b)
		}
		t.Fatalf("Start: %v", err)
	}
	defer cleanup()

	pool := openPool(t, "host="+socketDir+" dbname=postgres user=postgres sslmode=disable", "")
	defer pool.Close()

	ctx := context.Background()
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("expected 1, got %d", one)
	}

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS pglite_smoke (id serial primary key, name text)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pglite_smoke (name) VALUES ('test')`); err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM pglite_smoke WHERE id = 1`).Scan(&name); err != nil {
		t.Fatalf("SELECT row: %v", err)
	}
	if name != "test" {
		t.Fatalf("expected test, got %q", name)
	}
}

// harmonyqueryConnString mirrors harmonyquery.newFromConfigPglite connection URLs.
func harmonyqueryConnString(t *testing.T, socketDir, schema, appName string) string {
	t.Helper()
	host := url.QueryEscape(socketDir)
	return fmt.Sprintf(
		"postgresql://%s@/%s?host=%s&sslmode=disable&application_name=%s&search_path=%s",
		url.QueryEscape("postgres"),
		url.PathEscape("postgres"),
		host,
		url.QueryEscape(appName),
		url.QueryEscape(schema),
	)
}

func openPool(t *testing.T, connStr, schema string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinConns = 1
	if schema != "" {
		cfg.AfterConnect = searchPathAfterConnect(schema)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	return pool
}

// searchPathAfterConnect mirrors harmonyquery pglite seasoning: PGlite ignores
// search_path as a startup parameter, so it must be set per connection.
func searchPathAfterConnect(schema string) func(context.Context, *pgx.Conn) error {
	return func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET search_path TO "+schema)
		return err
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

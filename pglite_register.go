package harmonyquery

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/yugabyte/pgx/v5/pgconn"
	"github.com/yugabyte/pgx/v5/pgxpool"
	"golang.org/x/xerrors"
)

func newFromConfigPglite(options Config) (*DB, error) {
	if options.Schema == "" {
		options.Schema = DefaultSchema
	}
	if options.ApplicationName == "" {
		options.ApplicationName = filepath.Base(os.Args[0])
	}
	if options.Database == "" {
		options.Database = "postgres"
	}
	if options.Username == "" {
		options.Username = "postgres"
	}

	pg := options.Pglite
	pgliteDataDir := ""
	itest := string(options.ITestID)
	if itest != "" {
		pg = pg.Subdir("itest_" + itest)
		pgliteDataDir = pg.DataDir()
		options.Schema = "public"
	}

	if options.PoolConfig == nil {
		options.PoolConfig = &PoolConfig{MaxConnections: 1, MinConnections: 1}
	} else {
		options.PoolConfig.MaxConnections = 1
		options.PoolConfig.MinConnections = 1
	}

	logger.Infof("PGlite connection config: storagePath=%s", pg.DataDir())

	socketDir, cleanup, err := pg.Start(
		context.Background(),
		options.Database,
		options.Username,
	)
	if err != nil {
		return nil, xerrors.Errorf("start pglite: %w", err)
	}

	host := url.QueryEscape(socketDir)
	connString := fmt.Sprintf(
		"postgresql://%s@/%s?host=%s&sslmode=disable&application_name=%s&search_path=%s",
		url.QueryEscape(options.Username),
		url.PathEscape(options.Database),
		host,
		url.QueryEscape(options.ApplicationName),
		url.QueryEscape(options.Schema),
	)

	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		cleanup()
		return nil, err
	}
	cfg.MaxConns = 1
	cfg.MinConns = 1
	if options.PoolConfig != nil {
		if options.PoolConfig.MaxConnectionLifetime > 0 {
			cfg.MaxConnLifetime = options.PoolConfig.MaxConnectionLifetime
		}
		if options.PoolConfig.MaxIdleTime > 0 {
			cfg.MaxConnIdleTime = options.PoolConfig.MaxIdleTime
		}
	}

	db := DB{
		cfg:              cfg,
		schema:           options.Schema,
		pgliteCleanup:    cleanup,
		pgliteDataDir:    pgliteDataDir,
		pgliteMode:       true,
		hostnames:        []string{"pglite"},
		sqlEmbedFS:       options.SqlEmbedFS,
		downgradeEmbedFS: options.DowngradeEmbedFS,
	}
	if err := db.addStatsAndConnect(); err != nil {
		cleanup()
		return nil, err
	}

	if itest == "" {
		if err := ensureSchemaExistsPglite(&db, options.Schema); err != nil {
			db.Close()
			return nil, err
		}
	}

	if err := db.upgrade(); err != nil {
		db.Close()
		return nil, err
	}

	return &db, db.setBTFP()
}

func ensureSchemaExistsPglite(db *DB, schema string) error {
	if len(schema) < 5 || !schemaRE.MatchString(schema) {
		return xerrors.New("schema must be of the form " + schemaREString + "\n Got: " + schema)
	}
	_, err := backoffForSerializationError(func() (pgconn.CommandTag, error) {
		return db.pgx.Exec(context.Background(), "CREATE SCHEMA IF NOT EXISTS "+schema)
	})
	return err
}

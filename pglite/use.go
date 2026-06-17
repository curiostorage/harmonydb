package pglite

import (
	"context"
	"path/filepath"

	"github.com/curiostorage/harmonyquery/pgliteinterface"
)

type internalDB struct {
	dataPath string
}

// UseInternalDB returns an embedded PostgreSQL backend at dataPath for harmonyquery.Config.Pglite.
func UseInternalDB(dataPath string) pgliteinterface.Backend {
	return internalDB{dataPath: dataPath}
}

func (d internalDB) DataDir() string { return d.dataPath }

func (d internalDB) Subdir(name string) pgliteinterface.Backend {
	return internalDB{dataPath: filepath.Join(d.dataPath, name)}
}

func (d internalDB) Start(ctx context.Context, database, user string) (socketDir string, cleanup func(), err error) {
	return Start(ctx, Config{
		DataDir:  d.dataPath,
		Database: database,
		User:     user,
	})
}

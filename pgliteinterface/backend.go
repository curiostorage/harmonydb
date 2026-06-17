package pgliteinterface

import "context"

// Backend is an embedded PostgreSQL instance (implemented by pglite.UseInternalDB).
type Backend interface {
	DataDir() string
	Subdir(name string) Backend
	Start(ctx context.Context, database, user string) (socketDir string, cleanup func(), err error)
}

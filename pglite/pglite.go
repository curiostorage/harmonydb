// Package pglite embeds PostgreSQL 17 via the PGlite WASI build and exposes it
// through a Unix-socket wire-protocol bridge compatible with pgx.
//
// Use with harmonyquery:
//
//	db, err := harmonyquery.NewFromConfig(harmonyquery.Config{
//	    Pglite: pglite.UseInternalDB("/var/lib/myapp/pglite"),
//	    ...
//	})
//
// The runtime uses wasmtime-go because this PGlite WASI artifact requires
// overlapping WASI directory preopens that are not handled correctly by wazero.
package pglite

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bytecodealliance/wasmtime-go/v37"
)

const shmemExitInprogressAddr = 4895117

// Instance is an embedded PostgreSQL instance running in a WASI sandbox.
type Instance struct {
	wasmMu sync.Mutex
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	engine   *wasmtime.Engine
	store    *wasmtime.Store
	instance *wasmtime.Instance

	dataDir string

	socketDir  string
	socketPath string
	listener   net.Listener
	wg         sync.WaitGroup

	fnInteractiveOne   *wasmtime.Func
	fnInteractiveWrite *wasmtime.Func
	fnUseWire          *wasmtime.Func
	fnClearError       *wasmtime.Func
	memory             *wasmtime.Memory
}

// Start creates and initializes an embedded PGlite instance.
// It returns the Unix socket directory pgx should use as host= and a cleanup function.
func Start(ctx context.Context, cfg Config) (socketDir string, cleanup func(), err error) {
	cfg = cfg.withDefaults()
	if cfg.DataDir == "" {
		return "", nil, fmt.Errorf("pglite DataDir is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("creating data dir: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	pg := &Instance{
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		dataDir: cfg.DataDir,
	}

	wasmBinary, err := setupEnvironment(pg.dataDir)
	if err != nil {
		cancel()
		return "", nil, err
	}

	pg.engine = wasmtime.NewEngine()
	pg.store = wasmtime.NewStore(pg.engine)

	wasiCfg := wasmtime.NewWasiConfig()
	wasiCfg.SetArgv([]string{"/tmp/pglite/bin/postgres", "--single", cfg.Database})
	wasiCfg.SetEnv(
		[]string{"ENVIRONMENT", "PREFIX", "PGDATA", "PGSYSCONFDIR", "PGUSER", "PGDATABASE", "MODE", "REPL", "TZ", "PGTZ", "PATH"},
		[]string{"wasm32_wasi_preview1", "/tmp/pglite", "/tmp/pglite/base", "/tmp/pglite", cfg.User, cfg.Database, "REACT", "N", "UTC", "UTC", "/tmp/pglite/bin"},
	)

	pgdataDir := filepath.Join(pg.dataDir, "pglite", "base")
	devDir := filepath.Join(pg.dataDir, "dev")
	if err := os.MkdirAll(pgdataDir, 0o755); err != nil {
		cancel()
		return "", nil, fmt.Errorf("creating pgdata dir: %w", err)
	}
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		cancel()
		return "", nil, fmt.Errorf("creating dev dir: %w", err)
	}

	allDirPerms := wasmtime.DIR_READ | wasmtime.DIR_WRITE
	allFilePerms := wasmtime.FILE_READ | wasmtime.FILE_WRITE
	if err := wasiCfg.PreopenDir(pg.dataDir, "/tmp", allDirPerms, allFilePerms); err != nil {
		cancel()
		return "", nil, fmt.Errorf("preopen /tmp: %w", err)
	}
	if err := wasiCfg.PreopenDir(pgdataDir, "/tmp/pglite/base", allDirPerms, allFilePerms); err != nil {
		cancel()
		return "", nil, fmt.Errorf("preopen pgdata: %w", err)
	}
	if err := wasiCfg.PreopenDir(devDir, "/dev", allDirPerms, allFilePerms); err != nil {
		cancel()
		return "", nil, fmt.Errorf("preopen /dev: %w", err)
	}

	if cfg.StdoutFile != "" {
		if err := wasiCfg.SetStdoutFile(cfg.StdoutFile); err != nil {
			cancel()
			return "", nil, fmt.Errorf("set stdout: %w", err)
		}
	} else {
		wasiCfg.InheritStdout()
	}
	if cfg.StderrFile != "" {
		if err := wasiCfg.SetStderrFile(cfg.StderrFile); err != nil {
			cancel()
			return "", nil, fmt.Errorf("set stderr: %w", err)
		}
	} else {
		wasiCfg.InheritStderr()
	}
	pg.store.SetWasi(wasiCfg)

	linker := wasmtime.NewLinker(pg.engine)
	if err := linker.DefineWasi(); err != nil {
		cancel()
		return "", nil, fmt.Errorf("define wasi: %w", err)
	}

	module, err := wasmtime.NewModule(pg.engine, wasmBinary)
	if err != nil {
		cancel()
		return "", nil, fmt.Errorf("compile module: %w", err)
	}

	instance, err := linker.Instantiate(pg.store, module)
	if err != nil {
		cancel()
		return "", nil, fmt.Errorf("instantiate module: %w", err)
	}
	pg.instance = instance

	if fn := instance.GetFunc(pg.store, "_start"); fn != nil {
		if _, err := fn.Call(pg.store); err != nil && !isWasiExit(err) {
			cancel()
			return "", nil, fmt.Errorf("_start: %w", err)
		}
	}
	if fn := instance.GetFunc(pg.store, "pgl_initdb"); fn != nil {
		if _, err := fn.Call(pg.store); err != nil {
			cancel()
			return "", nil, fmt.Errorf("pgl_initdb: %w", err)
		}
	} else if fn := instance.GetFunc(pg.store, "pg_initdb"); fn != nil {
		if _, err := fn.Call(pg.store); err != nil {
			cancel()
			return "", nil, fmt.Errorf("pg_initdb: %w", err)
		}
	}
	_ = os.Remove(filepath.Join(pg.dataDir, "pglite", "base", "postmaster.pid"))
	if fn := instance.GetFunc(pg.store, "pgl_backend"); fn != nil {
		if _, err := fn.Call(pg.store); err != nil {
			cancel()
			return "", nil, fmt.Errorf("pgl_backend: %w", err)
		}
	}

	pg.fnInteractiveOne = instance.GetFunc(pg.store, "interactive_one")
	if pg.fnInteractiveOne == nil {
		cancel()
		return "", nil, fmt.Errorf("module missing interactive_one export")
	}
	pg.fnInteractiveWrite = instance.GetFunc(pg.store, "interactive_write")
	pg.fnUseWire = instance.GetFunc(pg.store, "use_wire")
	pg.fnClearError = instance.GetFunc(pg.store, "clear_error")
	if exp := instance.GetExport(pg.store, "memory"); exp != nil {
		pg.memory = exp.Memory()
	}

	if err := pg.startBridge(); err != nil {
		cancel()
		return "", nil, fmt.Errorf("starting socket bridge: %w", err)
	}

	return pg.socketDir, pg.Close, nil
}

// Close shuts down the PGlite instance.
func (pg *Instance) Close() {
	pg.cancel()
	if pg.listener != nil {
		pg.listener.Close()
	}
	pg.wg.Wait()
	_ = os.Remove(filepath.Join(pg.dataDir, "pglite", "base", "postmaster.pid"))
	if pg.socketDir != "" {
		os.RemoveAll(pg.socketDir)
	}
}

func isWasiExit(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "exit status")
}

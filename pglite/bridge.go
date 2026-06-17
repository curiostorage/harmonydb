package pglite

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (pg *Instance) startBridge() error {
	sockDir, err := os.MkdirTemp("", "pglite-sock-*")
	if err != nil {
		return err
	}
	pg.socketDir = sockDir
	pg.socketPath = filepath.Join(sockDir, ".s.PGSQL.5432")

	ln, err := net.Listen("unix", pg.socketPath)
	if err != nil {
		return err
	}
	pg.listener = ln

	ioBase := filepath.Join(pg.dataDir, "pglite", "base", ".s.PGSQL.5432")
	for _, suffix := range []string{".in", ".out", ".lock.in", ".lock.out"} {
		os.Remove(ioBase + suffix)
	}

	pg.wg.Add(1)
	go func() {
		defer pg.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			pg.handleConn(conn, ioBase)
		}
	}()
	return nil
}

func (pg *Instance) handleConn(conn net.Conn, ioBase string) {
	defer conn.Close()

	inFile := ioBase + ".in"
	lockIn := ioBase + ".lock.in"
	outFile := ioBase + ".out"

	for {
		select {
		case <-pg.ctx.Done():
			return
		default:
		}

		pg.wasmMu.Lock()
		if data, err := os.ReadFile(outFile); err == nil && len(data) > 0 {
			os.Remove(outFile)
			pg.wasmMu.Unlock()
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, werr := conn.Write(data); werr != nil {
				return
			}
			continue
		}
		pg.wasmMu.Unlock()

		conn.SetReadDeadline(time.Now().Add(16 * time.Millisecond))
		buf := make([]byte, 65536)
		n, readErr := conn.Read(buf)

		if n > 0 {
			if err := os.WriteFile(lockIn, buf[:n], 0o644); err != nil {
				return
			}
			if err := os.Rename(lockIn, inFile); err != nil {
				return
			}

			pg.wasmMu.Lock()
			replies, trapErr := pg.forwardWire(outFile)
			pg.wasmMu.Unlock()

			if !pg.sendReplies(conn, replies) {
				return
			}
			if trapErr != nil {
				return
			}
		}

		if readErr != nil {
			if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}
	}
}

func (pg *Instance) forwardWire(outFile string) ([][]byte, error) {
	const maxTicks = 256

	if pg.fnUseWire != nil {
		_, _ = pg.fnUseWire.Call(pg.store, int32(1))
	}

	var replies [][]byte
	for range maxTicks {
		producedBefore := pg.collectReply(outFile, &replies)

		_, err := pg.fnInteractiveOne.Call(pg.store)
		if err != nil {
			pg.collectReply(outFile, &replies)

			if pg.memory != nil {
				data := pg.memory.UnsafeData(pg.store)
				if int(shmemExitInprogressAddr) < len(data) {
					data[shmemExitInprogressAddr] = 1
				}
			}
			if pg.fnClearError != nil {
				_, _ = pg.fnClearError.Call(pg.store)
			}
			if pg.memory != nil {
				data := pg.memory.UnsafeData(pg.store)
				if int(shmemExitInprogressAddr) < len(data) {
					data[shmemExitInprogressAddr] = 0
				}
			}
			inFile := strings.TrimSuffix(outFile, ".out") + ".in"
			os.Remove(inFile)
			if pg.fnInteractiveWrite != nil {
				_, _ = pg.fnInteractiveWrite.Call(pg.store, int32(-1))
			}
			if pg.fnUseWire != nil {
				_, _ = pg.fnUseWire.Call(pg.store, int32(1))
			}
			_, _ = pg.fnInteractiveOne.Call(pg.store)
			pg.collectReply(outFile, &replies)
			return replies, err
		}

		producedAfter := pg.collectReply(outFile, &replies)
		if !producedBefore && !producedAfter {
			break
		}
	}
	return replies, nil
}

func (pg *Instance) collectReply(outFile string, replies *[][]byte) bool {
	data, err := os.ReadFile(outFile)
	if err != nil || len(data) == 0 {
		return false
	}
	os.Remove(outFile)
	*replies = append(*replies, data)
	return true
}

func (pg *Instance) sendReplies(conn net.Conn, replies [][]byte) bool {
	for _, data := range replies {
		if len(data) == 0 {
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(data); err != nil {
			return false
		}
	}
	return true
}

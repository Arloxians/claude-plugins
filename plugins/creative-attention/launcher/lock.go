package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	lockStaleAfter = 10 * time.Minute
	lockWaitFor    = 12 * time.Minute
)

var errLockTimeout = errors.New("another setup has been running for too long")

func readLockPID(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// lockIsStale: the holder process is gone, or the lock is older than staleAfter.
func lockIsStale(dir string, staleAfter time.Duration) bool {
	if pid := readLockPID(dir); pid > 0 && !pidAlive(pid) {
		return true
	}
	st, err := os.Stat(dir)
	return err == nil && time.Since(st.ModTime()) > staleAfter
}

// acquireLock creates dir atomically and writes our PID into it. onWait is called once if we have to wait.
func acquireLock(dir string, staleAfter, waitFor, poll time.Duration, onWait func()) error {
	deadline := time.Now().Add(waitFor)
	waited := false
	for {
		if err := os.Mkdir(dir, 0o755); err == nil {
			return os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o644)
		} else if !os.IsExist(err) {
			if _, serr := os.Stat(dir); serr != nil {
				return err
			}
		}
		if lockIsStale(dir, staleAfter) {
			os.RemoveAll(dir)
			continue
		}
		if !waited {
			waited = true
			if onWait != nil {
				onWait()
			}
		}
		if time.Now().After(deadline) {
			return errLockTimeout
		}
		time.Sleep(poll)
	}
}

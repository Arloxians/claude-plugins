package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSHAMatches(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("hello"), 0o644)
	got, err := fileSHA256(p)
	if err != nil || got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("bad digest %q %v", got, err)
	}
	if !shaMatches(got, got) || !shaMatches(got, " "+got+"\n") {
		t.Fatal("equal digests must match")
	}
	altered := "3" + got[1:]
	if shaMatches(got, altered) {
		t.Fatal("altered digest must not match")
	}
	if shaMatches(got, "") || shaMatches("", "") {
		t.Fatal("empty digest must never match")
	}
}

func TestLockAcquireAndWait(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "install.lock")
	if err := acquireLock(lock, time.Minute, time.Second, 10*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if readLockPID(lock) != os.Getpid() {
		t.Fatal("lock must hold our pid")
	}
	// we are alive and the lock is fresh: a second attempt must time out, not steal it
	waited := false
	err := acquireLock(lock, time.Minute, 100*time.Millisecond, 10*time.Millisecond, func() { waited = true })
	if err != errLockTimeout || !waited {
		t.Fatalf("expected timeout after waiting, got %v waited=%v", err, waited)
	}
}

func TestLockDeadPIDIsStale(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "install.lock")
	os.Mkdir(lock, 0o755)
	// a pid that is not running: take a very high one
	os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(2147480000)), 0o644)
	start := time.Now()
	if err := acquireLock(lock, 10*time.Minute, 5*time.Second, 50*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second || readLockPID(lock) != os.Getpid() {
		t.Fatal("dead holder must be replaced at once")
	}
}

func TestLockOldAgeIsStale(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "install.lock")
	os.Mkdir(lock, 0o755)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(lock, old, old)
	if !lockIsStale(lock, 10*time.Minute) {
		t.Fatal("old lock must be stale")
	}
	if lockStaleAfter >= lockWaitFor {
		t.Fatal("stale age must be shorter than the wait")
	}
}

func TestPruneKeepsPrevious(t *testing.T) {
	d := t.TempDir()
	mk := func(n string, age time.Duration) string {
		p := filepath.Join(d, n)
		os.Mkdir(p, 0o755)
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
		return p
	}
	a, b, c, cur := mk("app-1", 3*time.Hour), mk("app-2", 2*time.Hour), mk("app-3", time.Hour), mk("app-4", 0)
	pruneOldApps(d, cur)
	for _, p := range []string{cur, c} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must stay", p)
		}
	}
	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s must be pruned", p)
		}
	}
}

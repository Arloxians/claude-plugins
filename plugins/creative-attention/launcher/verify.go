package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// shaMatches compares hex digests, ignoring case and surrounding space. An empty want never matches.
func shaMatches(got, want string) bool {
	want = strings.TrimSpace(want)
	return want != "" && strings.EqualFold(strings.TrimSpace(got), want)
}

// pruneOldApps removes app-* folders in data except keep and the newest other one (the previous version,
// which a running Claude window may still use). Delete failures are ignored (Windows locks files in use).
func pruneOldApps(data, keep string) {
	olds, _ := filepath.Glob(filepath.Join(data, "app-*"))
	type d struct {
		p string
		t int64
	}
	var others []d
	for _, p := range olds {
		if p == keep {
			continue
		}
		if st, err := os.Stat(p); err == nil {
			others = append(others, d{p, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].t > others[j].t })
	for i, o := range others {
		if i > 0 {
			_ = os.RemoveAll(o.p)
		}
	}
}

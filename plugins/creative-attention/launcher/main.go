// ca-launch.exe: Windows launcher for Creative Attention. Same logic as bin/ca-launch (POSIX sh):
// download pinned Node + app (sha256 verified) into the plugin data dir, then run the app
// with stdio passed through. Pins come from ../runtime.json.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type platform struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type runtimeCfg struct {
	NodeVersion string              `json:"node_version"`
	Node        map[string]platform `json:"node"`
	AppVersion  string              `json:"app_version"`
	AppURL      string              `json:"app_url"`
	AppSHA256   string              `json:"app_sha256"`
}

var lockDir string

func fail(f string, a ...any) {
	fmt.Fprintln(os.Stderr, "Creative Attention could not start: "+fmt.Sprintf(f, a...))
	fmt.Fprintln(os.Stderr, "Check your internet connection and restart Claude. If it keeps happening, tell an Arlox admin and include this message.")
	if lockDir != "" {
		os.RemoveAll(lockDir)
	}
	os.Exit(1)
}
func say(f string, a ...any) {
	fmt.Fprintln(os.Stderr, "Creative Attention setup: "+fmt.Sprintf(f, a...))
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   20 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

const (
	stallWindow   = 60 * time.Second
	stallMinBytes = 60 * 1024 // about 1 KB/s, like curl --speed-limit 1024 --speed-time 60
)

// fetch downloads url to dst and returns the sha256. It gives up when the transfer stalls.
func fetch(url, dst string, window time.Duration, minBytes int64) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	r, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", r.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	var n atomic.Int64
	var stalled atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(window)
		defer t.Stop()
		var last int64
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cur := n.Load()
				if cur-last < minBytes {
					stalled.Store(true)
					cancel()
					return
				}
				last = cur
			}
		}
	}()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		k, rerr := r.Body.Read(buf)
		if k > 0 {
			n.Add(int64(k))
			h.Write(buf[:k])
			if _, werr := out.Write(buf[:k]); werr != nil {
				return "", werr
			}
		}
		if rerr == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if rerr != nil {
			if stalled.Load() {
				return "", fmt.Errorf("the download stalled")
			}
			return "", rerr
		}
	}
}

func download(url, dst string) string {
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		got, err := fetch(url, dst, stallWindow, stallMinBytes)
		if err == nil {
			return got
		}
		lastErr = err
	}
	fail("the download from %s failed (%v)", url, lastErr)
	return ""
}

func unzip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(p, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		o, err := os.Create(p)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(o, rc)
		o.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untargz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(p, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path %s", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(p), 0o755)
			o, err := os.Create(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(o, tr)
			o.Close()
			if err != nil {
				return err
			}
		}
	}
}

func dataDir() string {
	if d := os.Getenv("CLAUDE_PLUGIN_DATA"); d != "" {
		return d
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "ca-launch")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".ca-launch")
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fail("cannot locate the launcher: %v", err)
	}
	root := filepath.Dir(filepath.Dir(exe))
	raw, err := os.ReadFile(filepath.Join(root, "runtime.json"))
	if err != nil {
		fail("the file runtime.json is missing from the plugin")
	}
	var cfg runtimeCfg
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fail("runtime.json is not valid: %v", err)
	}
	plat, ok := cfg.Node["win-x64"]
	if !ok {
		fail("no Node build is listed for win-x64")
	}
	if cfg.AppURL == "" || cfg.AppSHA256 == "" {
		fail("the plugin has no app version pinned yet")
	}
	data := dataDir()
	if err := os.MkdirAll(data, 0o755); err != nil {
		fail("cannot create the folder %s", data)
	}
	nodeDir := filepath.Join(data, "node-"+cfg.NodeVersion+"-win-x64")
	node := filepath.Join(nodeDir, "node.exe")
	short := cfg.AppSHA256
	if len(short) > 12 {
		short = short[:12]
	}
	appDir := filepath.Join(data, "app-"+cfg.AppVersion+"-"+short)
	app := filepath.Join(appDir, "dist", "server.js")

	if !exists(node) || !exists(app) {
		lock := filepath.Join(data, "install.lock")
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			<-sigs
			if lockDir != "" {
				os.RemoveAll(lockDir)
			}
			os.Exit(130)
		}()
		if err := acquireLock(lock, lockStaleAfter, lockWaitFor, time.Second, func() {
			say("another Claude window is setting things up, waiting for it...")
		}); err != nil {
			fail("%v", err)
		}
		lockDir = lock
		if !exists(node) {
			say("downloading Node %s (one time, about 35 MB)...", cfg.NodeVersion)
			tmp, err := os.MkdirTemp(data, "tmp")
			if err != nil {
				fail("cannot write to %s", data)
			}
			zp := filepath.Join(tmp, "node.zip")
			if got := download(plat.URL, zp); !shaMatches(got, plat.SHA256) {
				fail("the downloaded Node file is damaged (checksum mismatch)")
			}
			x := filepath.Join(tmp, "x")
			if err := unzip(zp, x); err != nil {
				fail("could not unpack Node: %v", err)
			}
			os.RemoveAll(nodeDir)
			if err := os.Rename(filepath.Join(x, "node-"+cfg.NodeVersion+"-win-x64"), nodeDir); err != nil {
				fail("could not install Node: %v", err)
			}
			os.RemoveAll(tmp)
		}
		if !exists(app) {
			say("downloading Creative Attention %s (one time)...", cfg.AppVersion)
			tmp, err := os.MkdirTemp(data, "tmp")
			if err != nil {
				fail("cannot write to %s", data)
			}
			tp := filepath.Join(tmp, "app.tgz")
			if got := download(cfg.AppURL, tp); !shaMatches(got, cfg.AppSHA256) {
				fail("the downloaded app file is damaged (checksum mismatch)")
			}
			x := filepath.Join(tmp, "x")
			if err := untargz(tp, x); err != nil {
				fail("could not unpack the app: %v", err)
			}
			os.RemoveAll(appDir)
			if err := os.Rename(filepath.Join(x, "app"), appDir); err != nil {
				fail("could not install the app: %v", err)
			}
			os.RemoveAll(tmp)
			pruneOldApps(data, appDir)
		}
		os.RemoveAll(lockDir)
		lockDir = ""
		signal.Reset()
		say("ready.")
	}
	// work behind corporate proxies and with the operating system's certificate store
	os.Setenv("NODE_USE_ENV_PROXY", "1")
	c := exec.Command(node, "--use-system-ca", app)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fail("could not run the app: %v", err)
	}
}

// ca-launch.exe: Windows launcher for Creative Attention. Same logic as bin/ca-launch (POSIX sh):
// download pinned Node + app (sha256 verified) into the plugin data dir, then run the app
// with stdio passed through. Pins come from ../runtime.json.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func say(f string, a ...any) { fmt.Fprintln(os.Stderr, "Creative Attention setup: "+fmt.Sprintf(f, a...)) }
func exists(p string) bool  { _, err := os.Stat(p); return err == nil }

func download(url, dst string) string {
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		r, err := http.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if r.StatusCode != 200 {
			r.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", r.StatusCode)
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			r.Body.Close()
			fail("cannot write %s: %v", dst, err)
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(out, h), r.Body)
		r.Body.Close()
		out.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return hex.EncodeToString(h.Sum(nil))
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
		lockDir = filepath.Join(data, "install.lock")
		for n := 0; ; n++ {
			if err := os.Mkdir(lockDir, 0o755); err == nil {
				break
			}
			if st, err := os.Stat(lockDir); err == nil && time.Since(st.ModTime()) > 20*time.Minute {
				os.RemoveAll(lockDir)
				continue
			}
			if n == 0 {
				say("another Claude window is setting things up, waiting for it...")
			}
			if n > 900 {
				lockDir = ""
				fail("another setup has been running for too long")
			}
			time.Sleep(time.Second)
		}
		if !exists(node) {
			say("downloading Node %s (one time, about 35 MB)...", cfg.NodeVersion)
			tmp, err := os.MkdirTemp(data, "tmp")
			if err != nil {
				fail("cannot write to %s", data)
			}
			zp := filepath.Join(tmp, "node.zip")
			if got := download(plat.URL, zp); got != plat.SHA256 {
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
			if got := download(cfg.AppURL, tp); got != cfg.AppSHA256 {
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
			if olds, _ := filepath.Glob(filepath.Join(data, "app-*")); olds != nil {
				for _, d := range olds {
					if d != appDir {
						os.RemoveAll(d)
					}
				}
			}
		}
		os.RemoveAll(lockDir)
		lockDir = ""
		say("ready.")
	}
	c := exec.Command(node, app)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fail("could not run the app: %v", err)
	}
}

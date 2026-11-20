package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary act as the Go guard, so both guards run as real subprocesses.
func TestMain(m *testing.M) {
	if os.Getenv("CA_GUARD_EXEC") == "1" {
		runGuard()
	}
	os.Exit(m.Run())
}

type guardCase struct {
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	InputRaw   string          `json:"input_raw"` // exact bytes, for inputs a JSON object cannot express
	Status     string          `json:"status"`
	Expect     string          `json:"expect"`
	RouterHits int             `json:"router_hits"`
}

type guardImpl struct {
	name string
	argv []string
}

func guardImpls(t *testing.T) []guardImpl {
	impls := []guardImpl{
		{"go", []string{os.Args[0], "-test.run=^$"}},
		{"sh", []string{"sh", "../bin/ca-guard"}},
		{"bash-posix", []string{"bash", "--posix", "../bin/ca-guard"}},
	}
	if _, err := exec.LookPath("dash"); err == nil {
		impls = append(impls, guardImpl{"dash", []string{"dash", "../bin/ca-guard"}})
	} else {
		t.Log("dash not installed: skipping dash runs")
	}
	return impls
}

// fakeRouter answers GET /auth/cli/me per the wire contract and counts hits.
type fakeRouter struct {
	srv    *httptest.Server
	hits   atomic.Int32
	badHdr atomic.Int32
}

func newFakeRouter(t *testing.T, status string) *fakeRouter {
	f := &fakeRouter{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.URL.Path != "/auth/cli/me" || r.Header.Get("Authorization") != "Bearer arx_test" {
			f.badHdr.Add(1)
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"SIGN_IN_REQUIRED"}`))
			return
		}
		w.Header().Set("cache-control", "no-store")
		switch status {
		case "on", "off":
			w.Header().Set("content-type", "application/json")
			w.Write([]byte(`{"email":"t@arlox.io","status":"active","creative_attention":` + map[string]string{"on": "true", "off": "false"}[status] + `}`))
		case "redirect_off":
			w.Header().Set("location", "/elsewhere")
			w.WriteHeader(302)
			w.Write([]byte(`{"creative_attention":false}`))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func guardEnv(t *testing.T, routerURL string, withToken bool) (env []string, data string) {
	home := t.TempDir()
	data = t.TempDir()
	if withToken {
		os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"token":"arx_test","email":"t@arlox.io","expires_at":"2099-01-01T00:00:00Z"}`), 0o600)
	}
	rules, _ := filepath.Abs("../guard/rules.txt")
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "CA_HOME", "CLAUDE_PLUGIN_DATA", "ROUTER_URL", "CA_GUARD_RULES", "CA_GUARD_TEST", "CA_GUARD_EXEC", "LOCALAPPDATA",
			"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CA_HOME="+home, "CLAUDE_PLUGIN_DATA="+data, "ROUTER_URL="+routerURL,
		"CA_GUARD_RULES="+rules, "CA_GUARD_TEST=1", "CA_GUARD_EXEC=1", "NO_PROXY=127.0.0.1,localhost")
	return env, data
}

func runGuardImpl(t *testing.T, impl guardImpl, env []string, input []byte) (string, time.Duration) {
	cmd := exec.Command(impl.argv[0], impl.argv[1:]...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(string(input))
	start := time.Now()
	out, err := cmd.Output()
	took := time.Since(start)
	if err != nil {
		t.Fatalf("%s guard exited with error: %v (%s)", impl.name, err, out)
	}
	return string(out), took
}

func decision(t *testing.T, out string) string {
	if strings.TrimSpace(out) == "" {
		return "allow"
	}
	var d struct {
		H struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.H.Event != "PreToolUse" || d.H.Decision != "deny" {
		t.Fatalf("bad deny output %q: %v", out, err)
	}
	switch d.H.Reason {
	case guardOnReason:
		return "deny_on"
	case guardOnReason + guardUnknownSuffix:
		return "deny_on_unknown"
	case guardOffReason:
		return "deny_off"
	}
	t.Fatalf("unexpected reason %q", d.H.Reason)
	return ""
}

func loadCases(t *testing.T) []guardCase {
	raw, err := os.ReadFile("../guard/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []guardCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) < 50 {
		t.Fatalf("only %d cases", len(cs))
	}
	return cs
}

func TestGuardCases(t *testing.T) {
	impls := guardImpls(t)
	for _, c := range loadCases(t) {
		for _, impl := range impls {
			r := newFakeRouter(t, c.Status)
			env, _ := guardEnv(t, r.srv.URL, c.Status != "no_token")
			in := []byte(c.InputRaw)
			if c.InputRaw == "" {
				in = c.Input
			}
			out, took := runGuardImpl(t, impl, env, in)
			if got := decision(t, out); got != c.Expect {
				t.Errorf("%s [%s]: got %s want %s", c.Name, impl.name, got, c.Expect)
			}
			if h := int(r.hits.Load()); h != c.RouterHits {
				t.Errorf("%s [%s]: router hits %d want %d", c.Name, impl.name, h, c.RouterHits)
			}
			if r.badHdr.Load() != 0 {
				t.Errorf("%s [%s]: request without Authorization: Bearer arx_test", c.Name, impl.name)
			}
			if c.Name == "bash git log allow" {
				t.Logf("non-candidate Bash case: %s guard took %v", impl.name, took)
			}
		}
	}
}

func TestGuardCacheReuse(t *testing.T) {
	in := []byte(`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`)
	for _, impl := range guardImpls(t) {
		for _, status := range []string{"on", "unknown"} {
			r := newFakeRouter(t, status)
			env, data := guardEnv(t, r.srv.URL, true)
			want := map[string]string{"on": "deny_on", "unknown": "deny_on_unknown"}[status]
			for i := 0; i < 2; i++ {
				out, _ := runGuardImpl(t, impl, env, in)
				if got := decision(t, out); got != want {
					t.Errorf("[%s] %s call %d: got %s", impl.name, status, i, got)
				}
			}
			if h := r.hits.Load(); h != 1 {
				t.Errorf("[%s] %s: router hits %d want 1 (cache reuse)", impl.name, status, h)
			}
			raw, _ := os.ReadFile(filepath.Join(data, "policy-cache"))
			if f := strings.Fields(string(raw)); len(f) != 2 || f[1] != status {
				t.Errorf("[%s] cache file %q", impl.name, raw)
			}
			// Stale cache: on/off live 120 s, unknown 20 s.
			old := time.Now().Unix() - map[string]int64{"on": 121, "unknown": 21}[status]
			os.WriteFile(filepath.Join(data, "policy-cache"), []byte(strconv.FormatInt(old, 10)+" "+status+"\n"), 0o644)
			runGuardImpl(t, impl, env, in)
			if h := r.hits.Load(); h != 2 {
				t.Errorf("[%s] %s: stale cache not refreshed (hits %d)", impl.name, status, h)
			}
		}
	}
}

// TestGuardCacheStrict: "off" is never cached or trusted from the cache (a forged cache cannot switch the
// guard off), and only lines matching ^[1-9][0-9]{0,11} (on|unknown)$ are reused.
func TestGuardCacheStrict(t *testing.T) {
	in := []byte(`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	for _, impl := range guardImpls(t) {
		for _, forged := range []string{now + " off\n", "0" + now + " on\n", " " + now + " on\n", now + "  on\n",
			now + " on extra\n", now + " on\r\n", "1234567890123 on\n", now + "\toff\n"} {
			r := newFakeRouter(t, "on")
			env, data := guardEnv(t, r.srv.URL, true)
			os.WriteFile(filepath.Join(data, "policy-cache"), []byte(forged), 0o644)
			out, _ := runGuardImpl(t, impl, env, in)
			if got := decision(t, out); got != "deny_on" || r.hits.Load() != 1 {
				t.Errorf("[%s] cache %q: got %s hits %d (want deny_on, 1 hit)", impl.name, forged, got, r.hits.Load())
			}
		}
		// A valid cached "on" is reused.
		r := newFakeRouter(t, "off")
		env, data := guardEnv(t, r.srv.URL, true)
		os.WriteFile(filepath.Join(data, "policy-cache"), []byte(now+" on\n"), 0o644)
		if out, _ := runGuardImpl(t, impl, env, in); decision(t, out) != "deny_on" || r.hits.Load() != 0 {
			t.Errorf("[%s] valid cached on not reused (hits %d)", impl.name, r.hits.Load())
		}
		// Switch off: asked every time, never written to the cache.
		r = newFakeRouter(t, "off")
		env, data = guardEnv(t, r.srv.URL, true)
		for i := 0; i < 2; i++ {
			if out, _ := runGuardImpl(t, impl, env, in); decision(t, out) != "allow" {
				t.Errorf("[%s] off call %d not allowed", impl.name, i)
			}
		}
		if h := r.hits.Load(); h != 2 {
			t.Errorf("[%s] off: router hits %d want 2 (off must not be cached)", impl.name, h)
		}
		if _, err := os.Stat(filepath.Join(data, "policy-cache")); err == nil {
			t.Errorf("[%s] off was written to the cache", impl.name)
		}
	}
}

// TestGuardLongName: a ~1 MB tool_name is a candidate at once (no word or camelCase splitting) and stays fast.
func TestGuardLongName(t *testing.T) {
	in, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"},
		"x": map[string]string{"tool_name": "mcp__x__" + strings.Repeat("aB", 500000)}})
	for _, impl := range guardImpls(t) {
		for status, want := range map[string]string{"on": "deny_on", "off": "allow", "unknown": "deny_on_unknown"} {
			r := newFakeRouter(t, status)
			env, _ := guardEnv(t, r.srv.URL, true)
			out, took := runGuardImpl(t, impl, env, in)
			t.Logf("[%s] 1 MB nested name, %s: %s in %v", impl.name, status, decision(t, out), took)
			if got := decision(t, out); got != want || took > time.Second {
				t.Errorf("[%s] 1 MB name %s: got %s in %v (want %s < 1s)", impl.name, status, got, took, want)
			}
		}
	}
}

// TestGuardAwkFailure: with a broken awk the sh guard still allows an ON user's own CA tools and fails closed
// on everything else.
func TestGuardAwkFailure(t *testing.T) {
	bin := t.TempDir()
	for _, tool := range []string{"tr", "grep", "sed", "head", "curl", "date", "dirname", "mkdir", "mv", "rm", "cat"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		os.Symlink(p, filepath.Join(bin, tool))
	}
	os.WriteFile(filepath.Join(bin, "awk"), []byte("#!/bin/sh\nexit 2\n"), 0o755)
	cases := []struct{ in, want string }{
		{`{"tool_name":"mcp__plugin_creative-attention_creative-attention__ca_run","tool_input":{}}`, "allow"},
		{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, "deny_on"},
		{`{"tool_name":"mcp__claude_ai_Slack__slack_send_message","tool_input":{}}`, "deny_on"},
	}
	for _, impl := range guardImpls(t)[1:] {
		for _, c := range cases {
			r := newFakeRouter(t, "on")
			env, _ := guardEnv(t, r.srv.URL, true)
			env = append(withoutKeys(env, "PATH"), "PATH="+bin)
			out, _ := runGuardImpl(t, impl, env, []byte(c.in))
			if got := decision(t, out); got != c.want {
				t.Errorf("[%s] awk broken, %s: got %s want %s", impl.name, c.in, got, c.want)
			}
		}
	}
}

// TestGuardLargeInputs: many nested names fail closed fast; a big single-line Write stays quick.
func TestGuardLargeInputs(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"tool_name":"Bash","tool_input":{"command":"ls"},"x":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"tool_name":"mcp__claude_ai_Slack__slack_read_file"}`)
	}
	b.WriteString(`]}`)
	many := []byte(b.String())
	big, _ := json.Marshal(map[string]any{"tool_name": "Write", "tool_input": map[string]string{"file_path": "/x/a.txt", "content": strings.Repeat("hello world, nothing to see here. ", 300000)}})
	for _, impl := range guardImpls(t) {
		r := newFakeRouter(t, "on")
		env, _ := guardEnv(t, r.srv.URL, true)
		out, took := runGuardImpl(t, impl, env, many)
		t.Logf("[%s] 2000 nested tool names: %s in %v", impl.name, decision(t, out), took)
		if decision(t, out) != "deny_on" || took > time.Second {
			t.Errorf("[%s] 2000 names: got %s in %v (want deny_on < 1s)", impl.name, decision(t, out), took)
		}
		out, took = runGuardImpl(t, impl, env, big)
		t.Logf("[%s] %d MB single-line Write: %s in %v", impl.name, len(big)>>20, decision(t, out), took)
		if decision(t, out) != "allow" || took > 3*time.Second {
			t.Errorf("[%s] big Write: got %s in %v (want allow < 3s)", impl.name, decision(t, out), took)
		}
	}
}

func TestGuardRouterURLRejected(t *testing.T) {
	in := []byte(`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`)
	for _, impl := range guardImpls(t) {
		r := newFakeRouter(t, "off")
		env, _ := guardEnv(t, "http://example.com", true)
		out, _ := runGuardImpl(t, impl, env, in)
		if got := decision(t, out); got != "deny_on_unknown" || r.hits.Load() != 0 {
			t.Errorf("[%s] plain-http router: got %s", impl.name, got)
		}
	}
}

func TestGuardRulesMissing(t *testing.T) {
	cases := []struct {
		in   string
		want string
		hits int32
	}{
		{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, "deny_on", 1},
		{`{"tool_name":"Write","tool_input":{"content":"hello"}}`, "deny_on", 1},
		{`{"tool_name":"Read","tool_input":{"file_path":"/x"}}`, "allow", 0},
		{`{"tool_name":"mcp__claude_ai_Slack__slack_send_message","tool_input":{}}`, "deny_on", 1},
		{`{"tool_name":"mcp__plugin_creative-attention_creative-attention__ca_run","tool_input":{}}`, "allow", 1},
	}
	for _, impl := range guardImpls(t) {
		for _, c := range cases {
			r := newFakeRouter(t, "on")
			env, _ := guardEnv(t, r.srv.URL, true)
			env = append(env, "CA_GUARD_RULES="+filepath.Join(t.TempDir(), "missing.txt"))
			out, _ := runGuardImpl(t, impl, env, []byte(c.in))
			if got := decision(t, out); got != c.want || r.hits.Load() != c.hits {
				t.Errorf("[%s] rules missing %s: got %s hits %d", impl.name, c.in, got, r.hits.Load())
			}
		}
	}
}

// withoutKeys drops env entries for the given names.
func withoutKeys(env []string, keys ...string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, d := range keys {
			drop = drop || k == d
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.WriteFile(dst, raw, mode); err != nil {
		t.Fatal(err)
	}
}

// TestGuardDefaultRulesAndOverrideGate: both guards find <plugin>/guard/rules.txt next to bin/, and
// CA_GUARD_RULES is ignored unless CA_GUARD_TEST=1 (so CA_GUARD_RULES=/dev/null cannot switch rules off).
func TestGuardDefaultRulesAndOverrideGate(t *testing.T) {
	root := t.TempDir()
	copyFile(t, "../guard/rules.txt", filepath.Join(root, "guard", "rules.txt"), 0o644)
	copyFile(t, "../bin/ca-guard", filepath.Join(root, "bin", "ca-guard"), 0o755)
	copyFile(t, os.Args[0], filepath.Join(root, "bin", "guardtest"), 0o755)
	impls := []guardImpl{
		{"go", []string{filepath.Join(root, "bin", "guardtest"), "-test.run=^$"}},
		{"sh", []string{"sh", filepath.Join(root, "bin", "ca-guard")}},
		{"bash-posix", []string{"bash", "--posix", filepath.Join(root, "bin", "ca-guard")}},
	}
	if _, err := exec.LookPath("dash"); err == nil {
		impls = append(impls, guardImpl{"dash", []string{"dash", filepath.Join(root, "bin", "ca-guard")}})
	}
	cases := []struct {
		in, want string
		hits     int32
	}{
		{`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`, "deny_on", 1},
		{`{"tool_name":"Bash","tool_input":{"command":"ls"}}`, "allow", 0},
	}
	for _, impl := range impls {
		for _, override := range []string{"", "/dev/null"} {
			for _, c := range cases {
				r := newFakeRouter(t, "on")
				env, _ := guardEnv(t, r.srv.URL, true)
				env = withoutKeys(env, "CA_GUARD_RULES", "CA_GUARD_TEST")
				if override != "" {
					env = append(env, "CA_GUARD_RULES="+override)
				}
				out, _ := runGuardImpl(t, impl, env, []byte(c.in))
				if got := decision(t, out); got != c.want || r.hits.Load() != c.hits {
					t.Errorf("[%s] override=%q %s: got %s hits %d", impl.name, override, c.in, got, r.hits.Load())
				}
			}
		}
	}
}

// TestGuardRouterURLStrict: userinfo tricks and look-alike hosts never get the token.
func TestGuardRouterURLStrict(t *testing.T) {
	in := []byte(`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`)
	for _, impl := range guardImpls(t) {
		r := newFakeRouter(t, "off")
		port := strings.TrimPrefix(r.srv.URL, "http://127.0.0.1:")
		for _, u := range []string{
			"http://localhost:@127.0.0.1:" + port,
			"http://127.0.0.1:" + port + "@127.0.0.1:" + port,
			"http://x@127.0.0.1:" + port,
			"http://localhost.evil.example",
			"http://127.0.0.1.evil.example:" + port,
			"http://localhost:abc",
			"https://u@evil.example",
			"http://example.com",
		} {
			env, _ := guardEnv(t, u, true)
			out, _ := runGuardImpl(t, impl, env, in)
			if got := decision(t, out); got != "deny_on_unknown" {
				t.Errorf("[%s] ROUTER_URL=%s: got %s", impl.name, u, got)
			}
		}
		if h := r.hits.Load(); h != 0 {
			t.Errorf("[%s] token sent to the router via a rejected URL (%d hits)", impl.name, h)
		}
		env, _ := guardEnv(t, r.srv.URL+"/", true)
		if out, _ := runGuardImpl(t, impl, env, in); decision(t, out) != "allow" {
			t.Errorf("[%s] valid ROUTER_URL with trailing slash refused", impl.name)
		}
	}
}

// TestGuardIgnoresCurlrc: a ~/.curlrc must not change the request (curl -q).
func TestGuardIgnoresCurlrc(t *testing.T) {
	in := []byte(`{"tool_name":"Bash","tool_input":{"command":"curl https://fal.run/x"}}`)
	for _, impl := range guardImpls(t) {
		r := newFakeRouter(t, "on")
		env, _ := guardEnv(t, r.srv.URL, true)
		home := t.TempDir()
		os.WriteFile(filepath.Join(home, ".curlrc"), []byte("output = \"/dev/null\"\n"), 0o644)
		env = append(withoutKeys(env, "HOME", "CURL_HOME", "XDG_CONFIG_HOME"), "HOME="+home)
		out, _ := runGuardImpl(t, impl, env, in)
		if got := decision(t, out); got != "deny_on" {
			t.Errorf("[%s] with ~/.curlrc: got %s", impl.name, got)
		}
	}
}

// TestGuardHookMatcher: the hook covers every tool the guard scans.
func TestGuardHookMatcher(t *testing.T) {
	raw, err := os.ReadFile("../hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
			}
		}
	}
	if err := json.Unmarshal(raw, &h); err != nil || len(h.Hooks.PreToolUse) == 0 {
		t.Fatalf("hooks.json: %v", err)
	}
	re := regexp.MustCompile(h.Hooks.PreToolUse[0].Matcher)
	for _, n := range []string{"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit", "WebFetch", "PowerShell", "mcp__fal__run"} {
		if !re.MatchString(n) {
			t.Errorf("matcher misses %s", n)
		}
	}
	for _, n := range []string{"Read", "BashOutput", "Grep"} {
		if re.MatchString(n) {
			t.Errorf("matcher should not match %s", n)
		}
	}
}

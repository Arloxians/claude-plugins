package main

// Creative Attention policy guard (`ca-launch.exe guard`). Same logic as bin/ca-guard (POSIX sh); both are
// tested against ../guard/cases.json. See bin/ca-guard for the decision steps.
//
// Rules come from <exe dir>/../guard/rules.txt (override: CA_GUARD_RULES). If that file is missing,
// unreadable or has no rules, every non-CA mcp__ tool is a candidate and the text scan is off (Bash,
// Write, Edit, WebFetch are then never candidates); CA tools still deny only when the switch is off.
// Text handling is ASCII-only and line-bound so it matches tr/sed/grep in the sh guard exactly.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	guardCAPrefix      = "mcp__plugin_creative-attention_creative-attention__"
	guardOnReason      = "Company policy: for you, image, audio and video generation only goes through Creative Attention (switched on by an Arlox admin). Use the creative-attention tools instead."
	guardUnknownSuffix = " (Your Creative Attention status could not be checked: you may not be signed in yet or the router is unreachable. Ask Claude to run ca_doctor to sign in.)"
	guardOffReason     = "Creative Attention is switched off for you. Ask an Arlox admin to switch it on."
)

// sp is [[:space:]] without newline: sed and grep in the sh guard work per line.
const sp = `[\t\v\f\r ]*`

var (
	nonAlnum     = regexp.MustCompile(`[^a-z0-9]+`)
	trailDigits  = regexp.MustCompile(`[0-9]+$`)
	anyToolName  = regexp.MustCompile(`"tool_name"` + sp + `:` + sp + `"([^"\n]*)"`)
	envelope     = regexp.MustCompile(`"(session_id|transcript_path|cwd|permission_mode|hook_event_name|tool_use_id|agent_id|agent_type|tool_name)"` + sp + `:` + sp + `"[^"\n]*"`)
	authToken    = regexp.MustCompile(`"token"` + sp + `:` + sp + `"(arx_[A-Za-z0-9_-]+)"`)
	switchOn     = regexp.MustCompile(`"creative_attention"` + sp + `:` + sp + `true`)
	switchOff    = regexp.MustCompile(`"creative_attention"` + sp + `:` + sp + `false`)
	cacheDigits  = regexp.MustCompile(`^[0-9]{1,14}$`)
	guardSuffix  = []string{"ai", "labs", "pro", "fast", "dev", "turbo", "max", "lite"}
	guardTimeout = 3 * time.Second
)

type guardRules struct {
	long, short, phrase, text []string
	n                         int
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func loadGuardRules(path string) guardRules {
	var r guardRules
	raw, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n") {
		line = strings.TrimRight(line, " \t")
		var dst *[]string
		var pat string
		if p, ok := strings.CutPrefix(line, "tool_long "); ok {
			dst, pat = &r.long, p
		} else if p, ok := strings.CutPrefix(line, "tool_short "); ok {
			dst, pat = &r.short, p
		} else if p, ok := strings.CutPrefix(line, "tool_phrase "); ok {
			dst, pat = &r.phrase, p
		} else if p, ok := strings.CutPrefix(line, "text "); ok {
			dst, pat = &r.text, p
		} else {
			continue
		}
		r.n++
		if pat != "" {
			*dst = append(*dst, pat)
		}
	}
	return r
}

func guardRulesPath() string {
	if p := os.Getenv("CA_GUARD_RULES"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Join(filepath.Dir(exe), "..", "guard", "rules.txt")
}

func stripSuffixes(t string) string {
	for prev := ""; prev != t; {
		prev = t
		t = trailDigits.ReplaceAllString(t, "")
		for _, s := range guardSuffix {
			if len(t) > len(s) && strings.HasSuffix(t, s) {
				t = strings.TrimSuffix(t, s)
			}
		}
	}
	return t
}

// toolDenied reports whether a tool name is a generation tool outside Creative Attention.
func (r guardRules) toolDenied(tool string) bool {
	if strings.HasPrefix(tool, guardCAPrefix) || !strings.HasPrefix(tool, "mcp__") {
		return false
	}
	lower := asciiLower(tool)
	for _, w := range r.long {
		if strings.Contains(lower, w) {
			return true
		}
	}
	toks := strings.Fields(nonAlnum.ReplaceAllString(lower, " "))
	for _, t := range toks {
		base := stripSuffixes(t)
		for _, w := range r.short {
			if base == w {
				return true
			}
		}
	}
	norm := " " + strings.Join(toks, " ") + " "
	for _, p := range r.phrase {
		if strings.Contains(norm, " "+p+" ") {
			return true
		}
	}
	return false
}

// guardNames returns every non-empty "tool_name" value (nested too), plus the JSON-decoded top-level one.
func guardNames(in []byte) []string {
	var names []string
	for _, m := range anyToolName.FindAllSubmatch(in, -1) {
		if len(m[1]) > 0 {
			names = append(names, string(m[1]))
		}
	}
	var top struct {
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(in, &top) == nil && top.ToolName != "" {
		names = append(names, top.ToolName)
	}
	return names
}

// guardDecide returns the deny reason, or "" to allow. lookup is called only when needed.
func guardDecide(in []byte, r guardRules, lookup func() string) string {
	names := guardNames(in)
	allCA := len(names) > 0
	for _, n := range names {
		if !strings.HasPrefix(n, guardCAPrefix) {
			allCA = false
		}
	}
	if allCA {
		if lookup() == "off" {
			return guardOffReason
		}
		return ""
	}
	cand := false
	if r.n > 0 {
		for _, n := range names {
			if r.toolDenied(n) {
				cand = true
				break
			}
		}
		if !cand && len(r.text) > 0 {
			text := envelope.ReplaceAllString(asciiLower(string(in)), "")
			for _, p := range r.text {
				if strings.Contains(text, p) {
					cand = true
					break
				}
			}
		}
	} else {
		for _, n := range names {
			if strings.HasPrefix(n, "mcp__") && !strings.HasPrefix(n, guardCAPrefix) {
				cand = true
			}
		}
	}
	if !cand {
		return ""
	}
	switch lookup() {
	case "off":
		return ""
	case "on":
		return guardOnReason
	default:
		return guardOnReason + guardUnknownSuffix
	}
}

func caHome() string {
	if h := os.Getenv("CA_HOME"); h != "" {
		return h
	}
	if l := os.Getenv("LOCALAPPDATA"); l != "" {
		p := filepath.Join(l, "creative-attention")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".creative-attention")
}

func routerAllowed(u string) bool {
	if strings.HasPrefix(u, "https://") && len(u) > len("https://") {
		return true
	}
	for _, h := range []string{"http://127.0.0.1", "http://localhost"} {
		if u == h || strings.HasPrefix(u, h+":") || strings.HasPrefix(u, h+"/") {
			return true
		}
	}
	return false
}

// fetchStatus asks the router for the switch: on | off | unknown.
func fetchStatus(home string) string {
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return "unknown"
	}
	ms := authToken.FindAllSubmatch([]byte(strings.NewReplacer("\r", "", "\n", "").Replace(string(raw))), -1)
	if len(ms) == 0 {
		return "unknown"
	}
	tok := string(ms[len(ms)-1][1]) // last match, like the greedy sed in the sh guard
	router := os.Getenv("ROUTER_URL")
	if router == "" {
		router = "https://router.arlox.io"
	}
	router = strings.TrimRight(router, "/")
	if !routerAllowed(router) {
		return "unknown"
	}
	req, err := http.NewRequest("GET", router+"/auth/cli/me", nil)
	if err != nil {
		return "unknown"
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	client := &http.Client{
		Timeout:       guardTimeout,
		Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, // curl without -L
	}
	resp, err := client.Do(req)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 { // curl -f
		return "unknown"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "unknown"
	}
	switch {
	case switchOn.Match(body):
		return "on"
	case switchOff.Match(body):
		return "off"
	}
	return "unknown"
}

// guardLookup returns the cached switch state, refreshing it from the router when stale.
// Cache file "<epoch> <state>": on/off live 120 s, unknown 20 s. Write failures are ignored.
func guardLookup() string {
	home := caHome()
	dir := os.Getenv("CLAUDE_PLUGIN_DATA")
	if dir == "" {
		dir = home
	}
	cache := filepath.Join(dir, "policy-cache")
	now := time.Now().Unix()
	if raw, err := os.ReadFile(cache); err == nil {
		line, _, _ := strings.Cut(string(raw), "\n")
		f := strings.Fields(line)
		if len(f) == 2 && cacheDigits.MatchString(f[0]) {
			ts, _ := strconv.ParseInt(f[0], 10, 64)
			max := int64(0)
			switch f[1] {
			case "on", "off":
				max = 120
			case "unknown":
				max = 20
			}
			if age := now - ts; age >= 0 && age < max {
				return f[1]
			}
		}
	}
	st := fetchStatus(home)
	if os.MkdirAll(dir, 0o755) == nil {
		if tmp, err := os.CreateTemp(dir, "policy-cache.*.tmp"); err == nil {
			_, werr := tmp.WriteString(strconv.FormatInt(now, 10) + " " + st + "\n")
			cerr := tmp.Close()
			if werr != nil || cerr != nil || os.Rename(tmp.Name(), cache) != nil {
				os.Remove(tmp.Name())
			}
		}
	}
	return st
}

// runGuard is the `guard` subcommand: hook JSON on stdin, deny JSON on stdout, always exit 0.
func runGuard() {
	defer os.Exit(0)
	defer func() { recover() }()
	in, _ := io.ReadAll(os.Stdin)
	if reason := guardDecide(in, loadGuardRules(guardRulesPath()), guardLookup); reason != "" {
		os.Stdout.WriteString(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + reason + `"}}` + "\n")
	}
}

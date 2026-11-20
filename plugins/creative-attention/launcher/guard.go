package main

// Creative Attention policy guard (`ca-launch.exe guard`). Same logic as bin/ca-guard (POSIX sh); both are
// tested against ../guard/cases.json. See bin/ca-guard for the decision steps.
//
// Rules come from <exe dir>/../guard/rules.txt; CA_GUARD_RULES overrides the path only when CA_GUARD_TEST=1.
// If the rules file is missing, unreadable or has no rules, the guard fails closed: every non-CA mcp__ tool
// and every scan-tool call (Bash, PowerShell, Write, Edit, MultiEdit, NotebookEdit, WebFetch) is a
// candidate; CA tools still deny only when the switch is off. The text scan runs only for scan tools
// (the fixed list plus MCP code runners / file writers, see scanTool). More than 200 tool_name values is a
// candidate without further analysis, and so is any tool_name over 256 bytes (never word or camelCase split). Only "on" and "unknown" are cached; "off" is asked every time.
// Text handling is ASCII-only and line-bound so it matches tr/sed/grep in the sh guard exactly. Tool names
// are scanned with CR/LF turned into spaces, as the sh guard does. Raw NUL bytes (never valid JSON) are kept
// here but may be dropped by the shell, so the two guards can disagree on input containing NUL.

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

// guardMaxNames: more tool_name values than this is a candidate without further analysis.
const guardMaxNames = 200

// guardMaxNameLen: a tool_name longer than this is a candidate without word or camelCase splitting.
const guardMaxNameLen = 256

var (
	nonAlnum     = regexp.MustCompile(`[^a-z0-9]+`)
	trailDigits  = regexp.MustCompile(`[0-9]+$`)
	anyToolName  = regexp.MustCompile(`"tool_name"` + sp + `:` + sp + `"([^"\n]+)"`)
	envelope     = regexp.MustCompile(`"(session_id|transcript_path|cwd|permission_mode|hook_event_name|tool_use_id|agent_id|agent_type|tool_name|file_path|notebook_path)"` + sp + `:` + sp + `"[^"\n]*"`)
	authToken    = regexp.MustCompile(`"token"` + sp + `:` + sp + `"(arx_[A-Za-z0-9_-]+)"`)
	switchOn     = regexp.MustCompile(`"creative_attention"` + sp + `:` + sp + `true`)
	switchOff    = regexp.MustCompile(`"creative_attention"` + sp + `:` + sp + `false`)
	cacheLine    = regexp.MustCompile(`^([1-9][0-9]{0,11}) (on|unknown)$`)
	camelSplit   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	scanSegments = []string{"execute_code", "execute_command", "run_command", "start_process", "run_code", "write_file", "edit_file", "create_file"}
	httpLocal    = regexp.MustCompile(`^http://(localhost|127\.0\.0\.1)(:[0-9]+)?(/.*)?$`)
	scanTools    = map[string]bool{"Bash": true, "PowerShell": true, "Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true, "WebFetch": true}
	guardSuffix  = []string{"ai", "labs", "pro", "fast", "dev", "turbo", "max", "lite"}
	guardTimeout = 3 * time.Second
)

type guardRules struct {
	long, short, phrase, guardedPhrase, text []string
	except, generator                        map[string]bool
	n                                        int
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
		} else if p, ok := strings.CutPrefix(line, "tool_phrase_guarded "); ok {
			dst, pat = &r.guardedPhrase, p
		} else if p, ok := strings.CutPrefix(line, "tool_except "); ok {
			if r.except == nil {
				r.except = map[string]bool{}
			}
			r.except[p] = true
			r.n++
			continue
		} else if p, ok := strings.CutPrefix(line, "tool_generator "); ok {
			if r.generator == nil {
				r.generator = map[string]bool{}
			}
			r.generator[p] = true
			r.n++
			continue
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
	if p := os.Getenv("CA_GUARD_RULES"); p != "" && os.Getenv("CA_GUARD_TEST") == "1" {
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
	c := asciiLower(camel(tool))
	return r.tokensDenied(lower) || r.tokensDenied(c) || r.guardedDenied(lower, c)
}

// guardedDenied: a tool_phrase_guarded phrase in either token form, unless a tool_except token is present
// and no tool_generator token is.
func (r guardRules) guardedDenied(lower, split string) bool {
	if len(r.guardedPhrase) == 0 {
		return false
	}
	a := strings.Fields(nonAlnum.ReplaceAllString(lower, " "))
	b := strings.Fields(nonAlnum.ReplaceAllString(split, " "))
	na, nb := " "+strings.Join(a, " ")+" ", " "+strings.Join(b, " ")+" "
	hit := false
	for _, p := range r.guardedPhrase {
		hit = hit || strings.Contains(na, " "+p+" ") || strings.Contains(nb, " "+p+" ")
	}
	if !hit {
		return false
	}
	all := append(append([]string{}, a...), b...)
	for _, t := range all {
		if r.generator[t] {
			return true
		}
	}
	for _, t := range all {
		if r.except[t] {
			return false
		}
	}
	return true
}

// camel inserts _ between a lowercase letter or digit and an uppercase letter (generateImage -> generate_Image).
func camel(s string) string { return camelSplit.ReplaceAllString(s, "${1}_${2}") }

func (r guardRules) tokensDenied(lower string) bool {
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

// scanTool: tools whose input is text-scanned. The fixed list, plus non-CA MCP tools whose last name segment
// (camelCase split, lowercased, - as _) contains a code-runner / file-writer word.
func scanTool(n string) bool {
	if scanTools[n] {
		return true
	}
	if strings.HasPrefix(n, guardCAPrefix) || !strings.HasPrefix(n, "mcp__") {
		return false
	}
	seg := n
	if i := strings.LastIndex(n, "__"); i >= 0 {
		seg = n[i+2:]
	}
	seg = strings.ReplaceAll(asciiLower(camel(seg)), "-", "_")
	for _, w := range scanSegments {
		if strings.Contains(seg, w) {
			return true
		}
	}
	return false
}

// guardNames returns every non-empty "tool_name" value (nested too), plus the JSON-decoded top-level one,
// and how many values the scan found (the sh guard sees only that scan).
func guardNames(in []byte) ([]string, int) {
	var names []string
	joined := []byte(strings.NewReplacer("\r", " ", "\n", " ").Replace(string(in)))
	for _, m := range anyToolName.FindAllSubmatch(joined, -1) {
		if len(m[1]) > 0 {
			names = append(names, string(m[1]))
		}
	}
	var top struct {
		ToolName string `json:"tool_name"`
	}
	count := len(names)
	if json.Unmarshal(in, &top) == nil && top.ToolName != "" {
		names = append(names, top.ToolName)
	}
	return names, count
}

// guardDecide returns the deny reason, or "" to allow. lookup is called only when needed.
func guardDecide(in []byte, r guardRules, lookup func() string) string {
	names, count := guardNames(in)
	if count > guardMaxNames {
		return guardVerdict(lookup())
	}
	// Only the raw-scanned names (names[:count]) are checked, as in the sh guard. A backslash there is a JSON
	// escape the raw scan cannot read: fail closed.
	for _, n := range names[:count] {
		if len(n) > guardMaxNameLen || strings.Contains(n, `\`) {
			return guardVerdict(lookup())
		}
	}
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
	scan := false
	for _, n := range names {
		scan = scan || scanTool(n)
	}
	cand := false
	if r.n > 0 {
		for _, n := range names {
			if r.toolDenied(n) {
				cand = true
				break
			}
		}
		if !cand && scan && len(r.text) > 0 {
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
		cand = cand || scan
	}
	if !cand {
		return ""
	}
	return guardVerdict(lookup())
}

func guardVerdict(status string) string {
	switch status {
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

// routerAllowed: https to any host; plain http only to localhost / 127.0.0.1 with an optional numeric port.
// No userinfo (@) and no whitespace, so http://localhost:@evil.example cannot carry the token elsewhere.
func routerAllowed(u string) bool {
	if strings.ContainsAny(u, "@ \t\n\v\f\r") {
		return false
	}
	return (strings.HasPrefix(u, "https://") && len(u) > len("https://")) || httpLocal.MatchString(u)
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
	if resp.StatusCode != http.StatusOK { // a redirect body is never trusted
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
// Cache file "<epoch> <state>": on lives 120 s, unknown 20 s; "off" is never cached, so a forged cache cannot
// switch the guard off. Only a first line matching ^[1-9][0-9]{0,11} (on|unknown)$ is used. Write failures
// are ignored.
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
		if m := cacheLine.FindStringSubmatch(line); m != nil {
			ts, _ := strconv.ParseInt(m[1], 10, 64)
			max := int64(120)
			if m[2] == "unknown" {
				max = 20
			}
			if age := now - ts; age >= 0 && age < max {
				return m[2]
			}
		}
	}
	st := fetchStatus(home)
	if st != "off" && os.MkdirAll(dir, 0o755) == nil {
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

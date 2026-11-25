package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type renderCall struct {
	event, tool, cmd string
	bg               bool
}

func runRenderCheck(t *testing.T, argv []string, data, cwd string, c renderCall) string {
	t.Helper()
	tool := c.tool
	if tool == "" {
		tool = "Bash"
	}
	event := c.event
	if event == "" {
		event = "PreToolUse"
	}
	in, err := json.Marshal(map[string]any{
		"session_id": "11111111-2222-3333", "cwd": cwd, "hook_event_name": event, "tool_name": tool,
		"tool_input": map[string]any{"command": c.cmd, "description": "x", "run_in_background": c.bg},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Claude Code does not escape & < > in hook JSON.
	s := strings.NewReplacer(`\u0026`, "&", `\u003c`, "<", `\u003e`, ">").Replace(string(in))
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_DATA="+data)
	cmd.Stdin = strings.NewReader(s)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if len(out) == 0 {
		return "none"
	}
	var r struct {
		H struct {
			D string `json:"permissionDecision"`
			R string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.H.R == "" {
		t.Fatalf("bad hook output %q: %v", out, err)
	}
	return r.H.D
}

func TestRenderCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh hook")
	}
	impls := [][]string{{"sh", "../bin/ca-render-check"}, {"bash", "--posix", "../bin/ca-render-check"}}
	if _, err := exec.LookPath("dash"); err == nil {
		impls = append(impls, []string{"dash", "../bin/ca-render-check"})
	}
	for _, argv := range impls {
		t.Run(argv[0], func(t *testing.T) {
			data, cwd := t.TempDir(), t.TempDir()
			write := func(name, text string) {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(cwd, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cwd, name), []byte(text), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write("scenes/final.sh", "#!/bin/zsh\n# render\nB=/Applications/Blender.app/Contents/MacOS/Blender\n\"$B\" -b -P v2.py -- $1\n")
			write("render.py", "import subprocess\nsubprocess.run([\"blender\", \"--background\", \"x.blend\", \"-a\"])\n")
			write("plain.sh", "# blender -b in a comment only\necho hello\n")
			for _, c := range []struct {
				name, want string
				call       renderCall
			}{
				{"plain command", "none", renderCall{cmd: "ls -la", bg: true}},
				{"foreground render", "none", renderCall{cmd: "blender -b a.blend -f 1"}},
				{"background render, no estimate", "deny", renderCall{cmd: "blender -b a.blend -a", bg: true}},
				{"mac app path", "deny", renderCall{cmd: `"/Applications/Blender.app/Contents/MacOS/Blender" --background a.blend -a`, bg: true}},
				{"PowerShell", "deny", renderCall{tool: "PowerShell", cmd: `& "C:\Blender\blender.exe" -b a.blend -a`, bg: true}},
				{"nohup", "deny", renderCall{cmd: "nohup blender -b a.blend -a > log 2>&1"}},
				{"trailing ampersand", "deny", renderCall{cmd: "blender -b a.blend -a &"}},
				{"ampersand then another command", "deny", renderCall{cmd: "blender -b a.blend -a & echo started"}},
				{"foreground with redirection and &&", "none", renderCall{cmd: "cd x && blender -b a.blend -f 1 > log 2>&1 && echo ok"}},
				{"script with a render", "deny", renderCall{cmd: "cd scenes && caffeinate -ims ./final.sh 100 96 final", bg: true}},
				{"script by absolute path", "deny", renderCall{cmd: filepath.Join(cwd, "scenes/final.sh") + " 100", bg: true}},
				{"python script with a render", "deny", renderCall{cmd: "date; python3 render.py", bg: true}},
				{"script without one", "none", renderCall{cmd: "./plain.sh", bg: true}},
				{"missing script", "none", renderCall{cmd: "./nope.sh", bg: true}},
				{"20 minutes", "none", renderCall{cmd: "CA_RENDER_ETA_MIN=20 blender -b a.blend -a", bg: true}},
				{"21 minutes", "ask", renderCall{cmd: "CA_RENDER_ETA_MIN=21 blender -b a.blend -a", bg: true}},
				{"leading zeros", "ask", renderCall{cmd: "CA_RENDER_ETA_MIN=0090 blender -b a.blend -a", bg: true}},
				{"other tool", "none", renderCall{tool: "Write", cmd: "blender -b a.blend -a", bg: true}},
				{"ampersand only inside a heredoc", "none", renderCall{cmd: "cat > v.py <<'E'\nx = a & b\nE\nblender -b a.blend -f 1"}},
				{"nohup only inside a heredoc", "none", renderCall{cmd: "cat > n.sh <<E\nnohup x\nE\nblender -b a.blend -f 1"}},
				{"heredoc, then a background render", "deny", renderCall{cmd: "cat > v.py <<'E'\nx = 1\nE\nblender -b a.blend -a &"}},
				{"backslash before a closing quote", "deny", renderCall{cmd: `echo "a\\\\" ; blender -b a.blend -a`, bg: true}},
				{"quotes and newlines", "deny", renderCall{cmd: "echo \"a \\\"b\\\"\"\nblender -b \"a b.blend\" -a\n", bg: true}},
			} {
				if got := runRenderCheck(t, argv, data, cwd, c.call); got != c.want {
					t.Errorf("%s: got %s, want %s", c.name, got, c.want)
				}
			}
			// A very large command is answered well inside the hook's 20 s timeout.
			big := "cat > f <<'E'\n" + strings.Repeat("a blender \"q\" \\ line\n", 400000) + "E"
			start := time.Now()
			runRenderCheck(t, argv, data, cwd, renderCall{cmd: big, bg: true})
			if d := time.Since(start); d > 10*time.Second {
				t.Errorf("9 MB command took %s", d)
			}
			at := func(event, min string) string {
				return runRenderCheck(t, argv, data, cwd, renderCall{event: event, cmd: "CA_RENDER_ETA_MIN=" + min + " blender -b a.blend -a", bg: true})
			}
			// Allowed once at 100 minutes: less than twice that is not asked again, twice is.
			if got := at("PostToolUse", "100"); got != "none" {
				t.Errorf("post: %s", got)
			}
			for min, want := range map[string]string{"100": "none", "199": "none", "200": "ask"} {
				if got := at("PreToolUse", min); got != want {
					t.Errorf("after 100 allowed, %s: got %s, want %s", min, got, want)
				}
			}
			// A shorter one later does not lower what was allowed.
			at("PostToolUse", "30")
			if got := at("PreToolUse", "199"); got != "none" {
				t.Errorf("after a shorter run: %s", got)
			}
		})
	}
}

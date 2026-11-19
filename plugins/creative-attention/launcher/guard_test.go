package main

import (
	"os/exec"
	"strings"
	"testing"
)

var guardCases = []struct {
	tool string
	deny bool
}{
	{"mcp__plugin_creative-attention_creative-attention__ca_run", false},
	{"mcp__eachlabs-video__generate", true},
	{"mcp__claude_ai_Higgsfield__create", true},
	{"mcp__fal__run", true},
	{"mcp__claude_ai_Slack__slack_send_message", false},
	{"mcp__claude_ai_Slack__slack_read_file", false},
	{"mcp__claude_ai_Notion__notion-fetch", false},
	{"Bash", false},
	{"mcp__lumalabs__generate", true},
	{"mcp__pikalabs__gen", true},
	{"mcp__x__veo3fast", true},
	{"mcp__x__fluxpro", true},
	{"mcp__recraftai__gen", true},
	{"mcp__x__seedream_generate", true},
	{"mcp__openai__images_generate", true},
	{"mcp__claude_ai_Gmail__search_threads", false},
	{"mcp__claude_ai_Meta_Ads__ads_get_ad_accounts", false},
	{"mcp__veo3__generate", true},
	{"mcp__sora2__generate", true},
	{"mcp__falai__generate", true},
	{"mcp__openai__gpt-image-1", true},
	{"mcp__google__imagen_generate", true},
	{"mcp__nano-banana__edit", true},
	{"mcp__comfyui__queue_prompt", true},
	{"mcp__claude_ai_Google_Drive__search", false},
}

var guardRaw = []string{
	`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"mcp__claude_ai_Higgsfield__generate","tool_input":{"prompt":"x","tool_name":"Bash"}}`,
	`{"tool_name":"mcp__claude_ai_Higgsfield__generate","tool_input":{"tool_name":"mcp__plugin_creative-attention_creative-attention__x"}}`,
}

func TestGuardGo(t *testing.T) {
	for _, c := range guardCases {
		if got := guardDenies(c.tool); got != c.deny {
			t.Errorf("%s: deny=%v want %v", c.tool, got, c.deny)
		}
	}
}

func TestGuardSh(t *testing.T) {
	for _, c := range guardCases {
		cmd := exec.Command("sh", "../bin/ca-guard")
		cmd.Stdin = strings.NewReader(`{"tool_name":"` + c.tool + `","tool_input":{}}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		got := strings.Contains(string(out), `"permissionDecision":"deny"`)
		if got != c.deny {
			t.Errorf("%s: sh deny=%v want %v (%s)", c.tool, got, c.deny, out)
		}
		if c.deny && !strings.Contains(string(out), guardReason) {
			t.Errorf("%s: wrong reason", c.tool)
		}
	}
}

func TestGuardNested(t *testing.T) {
	for _, r := range guardRaw {
		if !guardInput([]byte(r)) {
			t.Errorf("go allowed %s", r)
		}
		cmd := exec.Command("sh", "../bin/ca-guard")
		cmd.Stdin = strings.NewReader(r)
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), `"permissionDecision":"deny"`) {
			t.Errorf("sh allowed %s (%s)", r, out)
		}
	}
}

func TestGuardBashFal(t *testing.T) {
	r := `{"tool_name":"Bash","tool_input":{"command":"curl https://fal.ai/x"}}`
	if guardInput([]byte(r)) {
		t.Error("go denied Bash")
	}
	cmd := exec.Command("sh", "../bin/ca-guard")
	cmd.Stdin = strings.NewReader(r)
	out, _ := cmd.Output()
	if len(out) != 0 {
		t.Errorf("sh denied Bash: %s", out)
	}
}

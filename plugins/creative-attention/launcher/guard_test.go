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

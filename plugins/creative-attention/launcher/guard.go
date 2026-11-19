package main

import (
	"io"
	"os"
	"regexp"
	"strings"
)

const guardReason = "Company policy: image and video generation only goes through Creative Attention (eachlabs via the Arlox router). Use the creative-attention tools instead."

var guardLong = []string{"higgsfield", "replicate", "runway", "kling", "minimax", "hailuo", "ideogram", "midjourney", "leonardo", "stability", "eachlabs", "seedance", "heygen", "synthesia", "elevenlabs"}
var guardShort = []string{"fal", "luma", "pika", "veo", "sora", "krea", "flux"}
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// guardDenies reports whether a tool name is a generation tool outside Creative Attention.
func guardDenies(tool string) bool {
	if strings.HasPrefix(tool, "mcp__plugin_creative-attention_creative-attention__") || !strings.HasPrefix(tool, "mcp__") {
		return false
	}
	lower := strings.ToLower(tool)
	for _, w := range guardLong {
		if strings.Contains(lower, w) {
			return true
		}
	}
	for _, t := range nonAlnum.Split(lower, -1) {
		for _, w := range guardShort {
			if t == w {
				return true
			}
		}
	}
	return false
}

// runGuard is the `guard` subcommand: hook JSON on stdin, deny JSON on stdout.
func runGuard() {
	in, _ := io.ReadAll(os.Stdin)
	m := regexp.MustCompile(`"tool_name"\s*:\s*"([^"]*)"`).FindSubmatch(in)
	if m != nil && guardDenies(string(m[1])) {
		os.Stdout.WriteString(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + guardReason + `"}}` + "\n")
	}
	os.Exit(0)
}

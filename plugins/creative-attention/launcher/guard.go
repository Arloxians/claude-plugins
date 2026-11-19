package main

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

const guardReason = "Company policy: image and video generation only goes through Creative Attention (eachlabs via the Arlox router). Use the creative-attention tools instead."

var guardLong = []string{"higgsfield", "replicate", "runway", "kling", "minimax", "hailuo", "ideogram", "midjourney", "leonardo", "stability", "eachlabs", "seedance", "heygen", "synthesia", "elevenlabs"}
var guardShort = map[string]bool{}
var guardPhrases = []string{"gpt image", "dall e", "nano banana", "gemini image"}
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
var trailDigits = regexp.MustCompile(`[0-9]+$`)
var anyToolName = regexp.MustCompile(`"tool_name"\s*:\s*"([^"]*)"`)

func init() {
	for _, w := range strings.Fields("fal falai luma pika veo sora krea flux imagen dalle comfy comfyui wan hunyuan ltx pixverse vidu hedra recraft") {
		guardShort[w] = true
	}
}

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
	toks := strings.Fields(nonAlnum.ReplaceAllString(lower, " "))
	for _, t := range toks {
		if guardShort[trailDigits.ReplaceAllString(t, "")] {
			return true
		}
	}
	norm := " " + strings.Join(toks, " ") + " "
	for _, p := range guardPhrases {
		if strings.Contains(norm, " "+p+" ") {
			return true
		}
	}
	return false
}

// guardInput decides for a whole hook payload: top-level tool_name, plus fail closed on any nested one.
func guardInput(in []byte) bool {
	var top struct {
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(in, &top) == nil && guardDenies(top.ToolName) {
		return true
	}
	for _, m := range anyToolName.FindAllSubmatch(in, -1) {
		if guardDenies(string(m[1])) {
			return true
		}
	}
	return false
}

// runGuard is the `guard` subcommand: hook JSON on stdin, deny JSON on stdout.
func runGuard() {
	in, _ := io.ReadAll(os.Stdin)
	if guardInput(in) {
		os.Stdout.WriteString(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + guardReason + `"}}` + "\n")
	}
	os.Exit(0)
}

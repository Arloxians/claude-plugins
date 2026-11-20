# Arlox plugins for Claude Code

Marketplace `arlox` with one plugin: `creative-attention` (make and edit videos with Arlox Creative Attention).
Works in Claude Code (terminal) and the Claude Desktop Code tab on macOS, Windows and Linux. You do not need Node or git.

## 1. Install (individuals)

Paste this one sentence into Claude Code:

> Install the creative-attention plugin from the Arloxians/claude-plugins marketplace

Claude will run these two commands:

```
claude plugin marketplace add Arloxians/claude-plugins
claude plugin install creative-attention@arlox
```

Then restart Claude Code. The first video takes a few minutes while tools are downloaded (once), and a browser opens to sign in with your Arlox Google account. If it says "waiting for an admin", ask an admin to approve you.

## 2. Install for the whole team (Team Owners)

In the Claude admin console, add this to server-managed settings:

```json
{
  "extraKnownMarketplaces": {
    "arlox": {
      "source": { "source": "github", "repo": "Arloxians/claude-plugins" },
      "autoUpdate": true
    }
  },
  "enabledPlugins": {
    "creative-attention@arlox": true
  }
}
```

## 3. Updating

New versions are published here as a new plugin version. With the team settings above (`"autoUpdate": true`), Claude Code picks up a new version by itself: within about ten minutes of the first message in a session it checks this repo and installs the update, then asks you to run `/reload-plugins` (or restart). Auto-update only fires when the plugin's version number changes. Individual installs can turn it on in `/plugin` → Marketplaces → arlox, or update by hand: `claude plugin marketplace update arlox` then `claude plugin update creative-attention@arlox`, and restart Claude Code.

## Company policy: Creative Attention on or off per teammate

An Arlox admin switches Creative Attention on or off for each teammate, in the router admin (People page). New teammates start off.

- **On:** image, audio and video generation must go only through Creative Attention (eachlabs via the Arlox router). In Claude Code, the plugin's guard hook (`hooks/hooks.json`, runs `bin/ca-guard`) blocks other ways to make them. It blocks other generator connectors (by tool name). It also blocks shell commands, file writes and edits, notebook edits, web fetches, and code-runner or file-writer tools when their content calls a generation API, SDK or local generation model. Local editing (ffmpeg, ImageMagick) stays allowed.
- **Off:** anything is allowed except Creative Attention's own tools. The router refuses those too.

If the status cannot be checked (not signed in yet, offline, or the router is unreachable), the guard treats the teammate as on. Signing in through Creative Attention fixes it: ask Claude to run `ca_doctor`.

A status change reaches a PC within about 2 minutes. An "on" result is cached for up to 120 seconds. An "off" result is never cached.

Accepted limits:

- A teammate who removes or disables the plugin escapes the guard.
- On Windows, install Git for Windows. Without it, hooks run in PowerShell, and the sh guard cannot run there.
- The guard is a blocklist. A brand-new provider is allowed until someone adds it to `guard/rules.txt`.
- A server named `plugin_creative-attention_creative-attention` would share our allowed prefix, so its tools would pass the guard when the switch is on. The managed settings below are the stronger control for this.

The guard only covers machines where the plugin is installed. Team Owners can also add this optional extra lock in managed settings. It blocks the listed connectors on every machine, whatever the teammate's switch says:

```json
{
  "permissions": {
    "deny": ["mcp__claude_ai_Higgsfield__*", "mcp__claude_ai_Fal__*", "mcp__claude_ai_Replicate__*"]
  },
  "deniedMcpServers": [{ "serverName": "eachlabs" }, { "serverUrl": "https://*.eachlabs.ai/*" }]
}
```

## For maintainers

How it works: `.mcp.json` runs `bin/ca-launch` (POSIX sh) or `bin/ca-launch.exe` (Go, Windows). On first start the launcher downloads the pinned Node (from nodejs.org) and the app bundle (a release asset of this repo), checks sha256 against `runtime.json`, installs into the plugin data folder, and runs the app. Only one install runs at a time.

Release a new app version after publishing it to npm: run the `build-app` workflow with the version (for example `0.4.0`). It packs the npm package, installs production dependencies, uploads release `creative-attention-v<version>`, rebuilds `ca-launch.exe`, updates `runtime.json` and the plugin version, and commits to main. The `test` workflow then checks all five platforms with Node hidden.

### Maintaining the guard

The guard is `bin/ca-guard` (POSIX sh). The same logic is in `launcher/guard.go` (`ca-launch.exe guard`). Both read the list of generators from `guard/rules.txt`, one rule per line in the form `<kind> <pattern>`. The kinds are `tool_long`, `tool_short`, `tool_phrase` and `text`. The comment at the top of that file explains each one. Patterns are lowercase.

Test cases are in `guard/cases.json`. The Go tests run every case against the sh guard (sh and bash in POSIX mode, and dash when it is installed) and against the Go guard.

To block a newly found generator:

1. Add one line to `guard/rules.txt`.
2. Add one case to `guard/cases.json`.
3. Run `cd plugins/creative-attention/launcher && go test ./...`. It must pass before you commit.

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
      "source": { "source": "github", "repo": "Arloxians/claude-plugins" }
    }
  },
  "enabledPlugins": {
    "creative-attention@arlox": true
  }
}
```

## 3. Updating

New versions are published here as a new plugin version. To update, run `claude plugin marketplace update arlox` then `claude plugin update creative-attention@arlox` and restart Claude Code (or ask Claude to do it).

## Company policy: generation only through Creative Attention

While the plugin is installed, image and video generation must go only through Creative Attention (eachlabs via the Arlox router). The plugin ships a PreToolUse hook (`hooks/hooks.json`, runs `bin/ca-guard`) that denies tools from other generation connectors (Higgsfield, fal, Replicate, Runway, eachlabs connectors and similar). On Windows the hook runs through Git Bash/sh; `ca-launch.exe guard` has the same logic for a Windows-native hook.

The hook only covers machines where the plugin is installed. Team Owners can also enforce it in managed settings:

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

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

## For maintainers

How it works: `.mcp.json` runs `bin/ca-launch` (POSIX sh) or `bin/ca-launch.exe` (Go, Windows). On first start the launcher downloads the pinned Node (from nodejs.org) and the app bundle (a release asset of this repo), checks sha256 against `runtime.json`, installs into the plugin data folder, and runs the app. Only one install runs at a time.

Release a new app version after publishing it to npm: run the `build-app` workflow with the version (for example `0.4.0`). It packs the npm package, installs production dependencies, uploads release `creative-attention-v<version>`, rebuilds `ca-launch.exe`, updates `runtime.json` and the plugin version, and commits to main. The `test` workflow then checks all five platforms with Node hidden.

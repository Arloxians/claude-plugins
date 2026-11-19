# Creative Attention 0.6.0 release checklist

Prepared on branch feat/eachlabs-only. Done already: plugin.json version 0.6.0, guard tests extended (direct ElevenLabs, Higgsfield, Replicate, Runway denied), `bin/ca-launch.exe` rebuilt with the `guard` subcommand. `runtime.json` still pins app 0.5.0 on purpose: its URL and sha256 only exist after the release asset is built (step 3).
macOS and Linux use the POSIX sh `bin/ca-launch`; only the Windows exe is a compiled binary (README / build-app.yml).

Run from the repo root after merging the branch to main.

1. Publish to npm first (`@arloxians/creative-attention-mcp@0.6.0`) and confirm: `npm view @arloxians/creative-attention-mcp@0.6.0 version`
2. Rebuild the exe if launcher code changed since this commit:
   `cd plugins/creative-attention/launcher && go test ./... && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../bin/ca-launch.exe .`
3. Preferred: run the workflow, which does build, release and runtime update in one go:
   `gh workflow run build-app -f version=0.6.0` (it checks out main, so merge first)
   Manual equivalent:
   ```
   mkdir -p work && cd work
   npm pack @arloxians/creative-attention-mcp@0.6.0
   mkdir x && tar xzf arloxians-creative-attention-mcp-0.6.0.tgz -C x && mv x/package app
   (cd app && npm install --omit=dev --omit=optional --ignore-scripts --no-audit --no-fund)
   test -f app/dist/server.js
   tar czf ../creative-attention-app-0.6.0.tar.gz app && cd ..
   gh release create creative-attention-v0.6.0 creative-attention-app-0.6.0.tar.gz --title "Creative Attention 0.6.0" --notes "App bundle for @arloxians/creative-attention-mcp 0.6.0 (used by the plugin launcher)."
   SHA=$(shasum -a 256 creative-attention-app-0.6.0.tar.gz | cut -d' ' -f1)
   python3 scripts/update-runtime.py 0.6.0 "https://github.com/Arloxians/claude-plugins/releases/download/creative-attention-v0.6.0/creative-attention-app-0.6.0.tar.gz" "$SHA"
   git add plugins/creative-attention && git commit -m "Change: pin Creative Attention app 0.6.0" && git push origin HEAD:main
   ```
4. Wait for the `test` workflow (5 platforms) to pass.
5. Marketplace update on a client: `claude plugin marketplace update arlox && claude plugin update creative-attention@arlox`, restart Claude Code.

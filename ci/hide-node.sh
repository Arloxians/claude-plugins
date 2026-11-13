#!/usr/bin/env bash
# move every node/npm/npx found on PATH out of the way, then assert none is left
for n in node npm npx nodejs corepack; do
  while p=$(type -P $n 2>/dev/null) && [ -n "$p" ]; do
    echo "hiding $p"; sudo mv "$p" "$p.hidden" 2>/dev/null || mv "$p" "$p.hidden" || break
  done
done
if command -v node >/dev/null; then echo "node STILL on PATH: $(command -v node)"; exit 1; fi
echo "node hidden OK"

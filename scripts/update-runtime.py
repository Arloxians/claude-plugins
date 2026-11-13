#!/usr/bin/env python3
"""usage: update-runtime.py <version> <app_url> <app_sha256>
Updates plugins/creative-attention/runtime.json (line-based edit, keeps the format the sh launcher greps)
and the version in plugin.json."""
import json, re, sys
ver, url, sha = sys.argv[1:4]
base = "plugins/creative-attention/"
rt = open(base + "runtime.json").read()
for key, val in (("app_version", ver), ("app_url", url), ("app_sha256", sha)):
    rt, n = re.subn(r'("%s": )"[^"]*"' % key, lambda m: m.group(1) + json.dumps(val), rt)
    assert n == 1, key
json.loads(rt)
open(base + "runtime.json", "w").write(rt)
pj = json.load(open(base + ".claude-plugin/plugin.json"))
pj["version"] = ver
open(base + ".claude-plugin/plugin.json", "w").write(json.dumps(pj, indent=2) + "\n")

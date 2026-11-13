"""Launch an MCP stdio server like Claude Code does; initialize, tools/list, require named tools.
usage: mcp_check.py <comma-separated required tools> <command> [args...]"""
import json, subprocess, sys, threading, time
required = [t for t in sys.argv[1].split(",") if t]
cmd = sys.argv[2:]
t0 = time.time()
p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=sys.stderr, text=True)
timer = threading.Timer(600, p.kill); timer.daemon = True; timer.start()
def send(o): p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()
send({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "ci", "version": "0"}}})
ok = False
for line in p.stdout:
    m = json.loads(line)
    if m.get("id") == 1:
        send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        send({"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
    elif m.get("id") == 2:
        names = [t["name"] for t in m["result"]["tools"]]
        print("TOOLS (%d): %s" % (len(names), ", ".join(names)))
        missing = [t for t in required if t not in names]
        if missing: print("MISSING TOOLS:", missing)
        else: ok = True
        break
print("elapsed_s=%.1f" % (time.time() - t0))
p.kill()
sys.exit(0 if ok else 1)

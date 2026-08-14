# -*- coding: utf-8 -*-
"""forge_guard.py — Forge guardian: integrity monitoring + permission audit + exposure-surface scan
Usage:
  python forge_guard.py init      # first-time hash baseline
  python forge_guard.py check     # compare against baseline, report tampering (default)
  python forge_guard.py audit     # full health check: permissions/listening/firewall/secret-leak risk"""
import os, sys, hashlib, json, datetime, subprocess
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASELINE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "forge_baseline.json")
LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "forge_guard.log")

WATCH = {
    "executable": ["forge.exe"],
    "source": ["main.go", "agent.go", "forge.go", "config.go", "llm.go", "ux.go", "cache_stats.go", "go.mod", "go.sum"],
    "secrets": [".env", "memory.json"],
}

def sha256(fp):
    h = hashlib.sha256()
    try:
        with open(fp, "rb") as f:
            for chunk in iter(lambda: f.read(65536), b""):
                h.update(chunk)
        return h.hexdigest()
    except Exception as e:
        return f"ERROR:{e}"

def log(msg):
    ts = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    line = f"[{ts}] {msg}"
    print(line)
    with open(LOG, "a", encoding="utf-8") as f:
        f.write(line + "\n")

def init_baseline():
    data = {}
    for cat, files in WATCH.items():
        for f in files:
            fp = os.path.join(BASE, f)
            if os.path.exists(fp):
                data[f] = {"cat": cat, "sha256": sha256(fp), "size": os.path.getsize(fp)}
            else:
                data[f] = {"cat": cat, "sha256": None, "size": None, "missing": True}
    with open(BASELINE, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
    log(f"[INIT] baseline established: {len(data)} file(s), saved to {BASELINE}")
    for f, v in data.items():
        if v.get("missing"):
            log(f"  [WARN] missing: {f}")

def check():
    if not os.path.exists(BASELINE):
        log("[ERROR] baseline does not exist, run init first")
        return 1
    with open(BASELINE, "r", encoding="utf-8") as f:
        base = json.load(f)
    issues = 0
    log(f"[CHECK] integrity comparison started ({len(base)} files)...")
    for f, v in base.items():
        fp = os.path.join(BASE, f)
        if not os.path.exists(fp):
            log(f"  [!!] file missing: {f}")
            issues += 1
            continue
        cur = sha256(fp)
        if cur != v["sha256"]:
            log(f"  [!!] tampered/changed: {f}  (baseline {v['sha256'][:12]}... -> current {cur[:12]}...)")
            issues += 1
    if issues == 0:
        log("[CHECK] OK: all files intact, no tampering")
    else:
        log(f"[CHECK] WARNING: {issues} issue(s) found!")
    dc = deadcheck()
    # exit code: integrity issues first, then dead-code compile-level issues
    return 1 if (issues or dc) else 0


def deadcheck():
    """Routine dead-code check: static zero-reference scan (advisory only) + go vet compile-level fallback (counts as issue)
    Static scan can produce false positives (lesson: ForgeParams was once falsely flagged), so static results are only WARN, not failure;
    go vet is the compile-level last line of defense; only its failure counts as an issue."""
    log("[DEADCODE] routine dead-code check started")
    issues = 0
    # static scan (advisory only)
    try:
        dc = os.path.join(os.path.dirname(os.path.abspath(__file__)), "deadcode_scan.py")
        r = subprocess.run([sys.executable, dc, BASE], capture_output=True, text=True,
                           timeout=120, encoding="utf-8", errors="replace")
        out = (r.stdout or "") + (r.stderr or "")
        for line in out.splitlines():
            log(f"  {line}")
        if "suspected dead code" in out:
            log("  [WARN] static suspected dead code (advisory only; go build/vet is authoritative)")
    except Exception as e:
        log(f"  [ERR] static dead-code scan failed: {e}")
    # go vet compile-level fallback (counts as issue)
    try:
        r = subprocess.run(["go", "vet", "./..."], cwd=BASE, capture_output=True, text=True,
                           timeout=180, encoding="utf-8", errors="replace")
        if r.returncode == 0:
            log("  [OK] go vet passed: no unreachable/compile-level issues")
        else:
            log(f"  [!!] go vet found issues (exit={r.returncode}):")
            for line in (r.stdout or "").splitlines()[:30]:
                log(f"      {line}")
            issues += 1
    except Exception as e:
        log(f"  [ERR] go vet execution failed: {e}")
    if issues == 0:
        log("[DEADCODE] OK: dead-code check finished, no issues")
    else:
        log(f"[DEADCODE] WARNING: {issues} compile-level issue(s)")
    return 1 if issues else 0

def audit():
    log("[AUDIT] Forge full health check started")
    # 1. sensitive file permissions
    log("  -- 1. .env permissions (admin/current user only) --")
    try:
        r = subprocess.run('icacls "D:\\forge\\.env"', shell=True, capture_output=True, text=True, timeout=10)
        for line in r.stdout.splitlines():
            if "Authenticated Users" in line or "Users" in line or "Everyone" in line:
                log(f"  [!!] permissions too broad: {line.strip()}")
            elif "BUILTIN\\Administrators" in line or "SYSTEM" in line:
                log(f"  [OK] {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 2. listening ports (non-default external ports)
    log("  -- 2. external listening ports --")
    try:
        r = subprocess.run('netstat -ano | findstr LISTENING', shell=True, capture_output=True, text=True, timeout=15)
        for line in r.stdout.splitlines():
            if ("0.0.0.0:" in line or "[::]:" in line) and "127.0.0.1:" not in line and "[::1]:" not in line:
                port = line.split()[1].rsplit(":", 1)[-1]
                if port not in ("135", "445", "139", "5040", "5357", "7680", "49664", "49665", "49666", "49667", "49668", "49718", "49719", "49697"):
                    log(f"  [!!] non-standard external port: {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 3. firewall status
    log("  -- 3. firewall --")
    try:
        r = subprocess.run('netsh advfirewall show allprofiles state', shell=True, capture_output=True, text=True, timeout=15)
        for line in r.stdout.splitlines():
            if "State" in line and "ON" in line:
                log(f"  [OK] {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 4. secret-leak risk
    log("  -- 4. secret files --")
    for f in [".env", "memory.json"]:
        fp = os.path.join(BASE, f)
        if os.path.exists(fp):
            log(f"  [INFO] {f} exists (contains API keys; do not commit to public repos; .gitignore should cover it)")
    log("[AUDIT] health check finished")
    return 0

if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else "check"
    if mode == "init":
        init_baseline()
    elif mode == "check":
        sys.exit(check())
    elif mode == "audit":
        audit()
    elif mode == "deadcheck":
        sys.exit(deadcheck())
    else:
        print("Usage: forge_guard.py init|check|audit")

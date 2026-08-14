# -*- coding: utf-8 -*-
"""deadcode_scan.py — Forge Go-source dead-code scanner (top-level identifier zero-reference detection)
Usage: python deadcode_scan.py [project-dir]
Scans each .go file for top-level declarations (func/type/var/const names), counts references across
the project; references <= 1 (declaration only) => suspected dead code. Skips _test.go. Static scan only; go build is the final word."""
import os, re, sys

DECL = re.compile(
    r'^(?:func\s+([A-Za-z_]\w*)\s*\(|func\s+\([^)]*\)\s+([A-Za-z_]\w*)\s*\(|type\s+([A-Za-z_]\w*)'
    r'|var\s+([A-Za-z_]\w*)|const\s+([A-Za-z_]\w*))\b',
    re.M)

def scan(root):
    files = []
    for dp, _, fns in os.walk(root):
        if any(x in dp for x in ("_archive", ".git", ".forge-temp", "defense_system", "tcc", "deno")):
            continue
        for fn in fns:
            if fn.endswith(".go") and not fn.endswith("_test.go"):
                files.append(os.path.join(dp, fn))
    decls = {}
    for fp in files:
        try:
            txt = open(fp, encoding="utf-8", errors="replace").read()
        except Exception:
            continue
        for m in DECL.finditer(txt):
            name = next(g for g in m.groups() if g)
            decls.setdefault(name, []).append(fp)
    alltxt = "\n".join(open(fp, encoding="utf-8", errors="replace").read() for fp in files)
    dead = []
    for name, locs in sorted(decls.items()):
        if len(locs) > 1:
            continue
        cnt = len(re.findall(r'\b' + re.escape(name) + r'\b', alltxt))
        if cnt <= 1:
            dead.append((name, locs[0]))
    return dead

if __name__ == "__main__":
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    dead = scan(root)
    if not dead:
        print("OK: no dead code")
    else:
        print("WARNING: %d suspected dead code(s) (verify with go build):" % len(dead))
        for name, fp in dead:
            print(f"  {name}  <-  {os.path.relpath(fp, root)}")

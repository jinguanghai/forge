# -*- coding: utf-8 -*-
"""deadcode_scan.py — 铸剑炉 Go 源码死代码扫描（顶层标识符零引用检测）
用法: python deadcode_scan.py [项目目录]
说明: 提取每个 .go 文件的顶层声明(func/type/var/const 名), 统计全项目引用次数,
      引用<=1(仅声明处) 即疑似死代码。排除 _test.go。仅静态扫描, 以 go build 兜底。
"""
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
        print("✅ 无死代码")
    else:
        print("⚠️ 疑似死代码 %d 处（以 go build 为准）:" % len(dead))
        for name, fp in dead:
            print(f"  {name}  <-  {os.path.relpath(fp, root)}")

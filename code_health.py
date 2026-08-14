# -*- coding: utf-8 -*-
"""
code_health.py — 铸剑炉进化感知器官 v0.2 (2026-08-10)
把"气机模型"落地为代码库健康体检工具:
  精层(存量): 规模/复杂度/函数分布   趋势=精的积累
  神层(结构): 耦合/重复/注释/测试比  结构=神的主宰
用法: python code_health.py [目录] [--save] [--compare 历史json]
口径说明: Go函数提取=声明行正则+大括号深度配平(字符串内括号会有少量误差,量级可信)
"""
import os, re, sys, json, io, hashlib
from datetime import datetime

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')

GO_EXT = {'.go'}; PY_EXT = {'.py'}
SKIP_DIRS = {'_archive', '.git', '.forge', '.forge-temp', 'defense_system', 'node_modules', 'vendor', 'dist', 'build', '__pycache__', 'open_source_sync'}
FUNC_RE = re.compile(r'^\s*func\s+(?:\([^)]*\)\s*)?[A-Za-z0-9_\.]+\s*\([^)]*\)\s*(?:\([^)]*\)|[A-Za-z0-9_\.\[\]\*]+)?\s*\{')
COMPLEX_RE = re.compile(r'\b(if|for|switch|case|else\s+if|&&|\|\|)\b')
TODO_RE = re.compile(r'\b(TODO|FIXME|HACK)\b')

def complexity_approx(text):
    return len(COMPLEX_RE.findall(text)) + 1

def extract_go_funcs(src):
    funcs = []
    lines = src.split('\n')
    cur, depth, start = None, 0, 0
    for i, ln in enumerate(lines):
        d = ln.count('{') - ln.count('}')
        if cur is None:
            m = FUNC_RE.match(ln)
            if m:
                cur, start, depth = ln.strip(), i, d
        else:
            depth += d
            if depth <= 0:
                body = '\n'.join(lines[start+1:i+1])
                funcs.append((cur, len(body.split('\n')), complexity_approx(body)))
                cur = None
    return funcs

def extract_py_funcs(src):
    funcs = []
    try:
        import ast
        tree = ast.parse(src)
        for node in ast.walk(tree):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                c = 1
                for n in ast.walk(node):
                    if isinstance(n, (ast.If, ast.For, ast.AsyncFor, ast.While, ast.BoolOp, ast.IfExp)):
                        c += 1
                funcs.append((node.name, node.end_lineno - node.lineno + 1, c))
    except SyntaxError:
        pass
    return funcs

def scan_dir(root):
    files, funcs, total_lines, total_cc, todos, comment_lines = [], [], 0, 0, 0, 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            ext = os.path.splitext(fn)[1]
            if ext not in GO_EXT | PY_EXT: continue
            p = os.path.join(dirpath, fn)
            try: src = open(p, encoding='utf-8', errors='replace').read()
            except Exception: continue
            rel = os.path.relpath(p, root).replace('\\', '/')
            n = src.count('\n') + 1
            total_lines += n
            todos += len(TODO_RE.findall(src))
            cl = len([l for l in src.split('\n') if re.match(r'^\s*(//|#|/\*)', l)])
            comment_lines += cl
            fs = extract_go_funcs(src) if ext == '.go' else extract_py_funcs(src)
            for name, ln, cc in fs:
                funcs.append({'file': rel, 'name': name, 'lines': ln, 'cc': cc})
                total_cc += cc
            files.append({'file': rel, 'lines': n, 'comments': cl})
    return files, funcs, total_lines, total_cc, todos, comment_lines

def main():
    args = [a for a in sys.argv[1:] if not a.startswith('--')]
    root = args[0] if args else r'D:\forge'
    save = '--save' in sys.argv
    files, funcs, total_lines, total_cc, todos, comment_lines = scan_dir(root)
    n_files, n_funcs = len(files), len(funcs)
    avg_cc = round(total_cc / n_funcs, 1) if n_funcs else 0
    comment_rate = round(comment_lines / total_lines * 100, 1) if total_lines else 0
    test_lines = sum(f['lines'] for f in files if '_test' in f['file'] or 'test_' in f['file'])
    test_ratio = round(test_lines / max(1, total_lines - test_lines), 2)
    top_long = sorted(funcs, key=lambda x: -x['lines'])[:5]
    top_cc = sorted(funcs, key=lambda x: -x['cc'])[:5]
    top_files = sorted(files, key=lambda x: -x['lines'])[:5]

    print('=' * 66)
    print(f'  ⚕  铸剑炉体检报告  —  {datetime.now().strftime("%Y-%m-%d %H:%M")}')
    print(f'  对象: {root}')
    print('=' * 66)
    print(f'\n【精层·存量】文件{n_files} 函数{n_funcs} 源码{total_lines}行 总复杂度{total_cc}')
    print(f'  平均复杂度 {avg_cc} | TODO/FIXME {todos}处 | 测试/源码比 {test_ratio}')
    print(f'\n  ⚠ 超长函数TOP5 (>200行=警戒):')
    for f in top_long:
        print(f'    {f["file"]}:{f["name"]} = {f["lines"]}行 / 复杂度{f["cc"]}' + (' 🔥高危' if f['lines'] > 200 else ''))
    print(f'\n  ⚠ 高复杂度TOP5 (>30=警戒):')
    for f in top_cc:
        print(f'    {f["file"]}:{f["name"]} = 复杂度{f["cc"]}' + (' 🔥高危' if f['cc'] > 30 else ''))
    print(f'\n  ⚠ 超大文件TOP5 (>1000行=警戒):')
    for f in top_files:
        print(f'    {f["file"]} = {f["lines"]}行 注释率{f["comments"]/max(1,f["lines"])*100:.1f}%' + (' 🔥' if f['lines'] > 1000 else ''))
    print(f'\n【神层·结构】')
    print(f'  总注释率 {comment_rate}%' + (' ⚠️ 核心引擎需补注释' if comment_rate < 8 else ' ✅'))
    print(f'  测试/源码比 {test_ratio}' + (' ✅ 有测试兜底，可动刀' if test_ratio >= 0.3 else ' ⚠️ 测试不足'))
    print('=' * 66)

    report = {
        'date': datetime.now().isoformat(),
        'jing': {'files': n_files, 'funcs': n_funcs, 'lines': total_lines, 'total_cc': total_cc,
                 'avg_cc': avg_cc, 'todos': todos},
        'shen': {'comment_rate': comment_rate, 'test_ratio': test_ratio},
        'top_long': [{'f': f['file'], 'n': f['name'], 'l': f['lines'], 'c': f['cc']} for f in top_long],
        'top_cc': [{'f': f['file'], 'n': f['name'], 'c': f['cc']} for f in top_cc],
    }
    if save:
        out = os.path.join(root, 'code_health_report.json')
        json.dump(report, open(out, 'w', encoding='utf-8'), ensure_ascii=False, indent=2)
        print(f'✅ 报告已存档: {out} (供下次趋势对比)')
    if '--compare' in sys.argv:
        cmp = sys.argv[sys.argv.index('--compare')+1]
        if os.path.exists(cmp):
            old = json.load(open(cmp, encoding='utf-8'))
            print(f'\n【趋势对比 vs {cmp}】')
            for k, label in [('lines','源码行数'), ('total_cc','总复杂度'), ('avg_cc','平均复杂度'),
                             ('todos','TODO遗留'), ('funcs','函数数')]:
                if k in old.get('jing', {}):
                    d = report['jing'][k] - old['jing'][k]
                    print(f'  {label}: {old["jing"][k]} → {report["jing"][k]} ({d:+d})')

if __name__ == '__main__':
    main()

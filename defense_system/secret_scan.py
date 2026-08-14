#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Secret security self-check (secret_scan.py) - Forge defense system
Usage:
  python secret_scan.py                 # scan the working area (default)
  python secret_scan.py --git-hist      # also scan git history (committed then deleted)
  python secret_scan.py --loose         # include weak-signal patterns (more false positives)
  python secret_scan.py <paths...>      # custom scan paths
  python secret_scan.py --archive       # include _archive history (slow, more false positives)
  # skipped by default: _archive (historical) / toolchain caches / third-party libs
Exit codes: 0=no real secrets  1=real secrets found  2=error
"""
import os, re, sys, subprocess, time

DEFAULT_ROOTS = [os.path.dirname(os.path.dirname(os.path.abspath(__file__)))]
SKIP_DIR_NAMES = {'.git', 'node_modules', '__pycache__', '.venv', 'venv',
                  'target', 'dist', 'build', '.cache', '.idea', '.vscode',
                  'site-packages', '.next', '.nuxt', 'vendor', '.mypy_cache',
                  '.pytest_cache', 'test_dir', 'test_secret_scan',
                  '_archive', 'deno', '.go', 'gopath', 'toolchain', 'exe_backup'}
SKIP_EXTS = {'.exe', '.dll', '.so', '.dylib', '.a', '.o', '.obj', '.class',
             '.jar', '.png', '.jpg', '.jpeg', '.gif', '.ico', '.pdf', '.zip',
             '.7z', '.rar', '.tar', '.gz', '.gob', '.pyc', '.pyd', '.woff',
             '.woff2', '.ttf', '.dat', '.db', '.sqlite', '.bin', '.pak',
             '.wasm', '.node', '.map', '.min.js', '.lock'}
MAX_FILE_SIZE = 10 * 1024 * 1024

STRONG_PATTERNS = [
    ('DeepSeek/OpenAI', r'sk-[A-Za-z0-9]{20,}'),
    ('GitHub token',      r'gh[pousr]_[A-Za-z0-9]{20,}'),
    ('GitHub fine-grained', r'github_pat_[A-Za-z0-9_]{20,}'),
    ('NVIDIA',           r'nvapi-[A-Za-z0-9_-]{20,}'),
    ('AWS access key',    r'AKIA[0-9A-Z]{16}'),
    ('Google',           r'AIza[0-9A-Za-z_-]{35}'),
    ('Slack',            r'xox[baprs]-[0-9A-Za-z-]{10,}'),
    ('Private key block', r'-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----'),
]
LOOSE_PATTERNS = [
    ('Possible credential assignment', r'(?:api[_-]?key|apikey|secret|access[_-]?token|passwd|password)\s*[:=]\s*["\']?[A-Za-z0-9_\-./+]{20,}'),
]
PLACEHOLDER_HINTS = ['your-', 'your_', 'xxxx', '<your', 'example', 'sample',
                     'placeholder', 'replace', 'dummy', 'fake', 'demo',
                     'your-key', 'yourkey', 'test-key', 'xxxxx', 'redacted']

def mask(s, head=8, tail=4):
    if len(s) <= head + tail:
        return s[:2] + '...'
    return s[:head] + '...' + s[-tail:]

def is_binary(content):
    return b'\x00' in content[:4096]

def _looks_fake(val):
    """Value characteristics: fake keys for testing/examples"""
    v = val.lower()
    if v.startswith(('sk-abc', 'sk-xyz', 'sk-test', 'sk-123', 'sk-000', 'sk-111',
                     'ghp_abc', 'ghp_xxx', 'github_pat_aaaa')):
        return True
    if re.search(r'(.)\1{4,}', val):      # 5+ identical chars in a row (e.g. AAAAAAA)
        return True
    if 'example' in v or 'xxxx' in v or 'test' in v or 'dummy' in v:
        return True
    return False

def is_placeholder(line, val=''):
    low = line.lower()
    if any(h in low for h in PLACEHOLDER_HINTS):
        return True
    return bool(val) and _looks_fake(val)

def scan_file(path, loose=False):
    # .env is the expected secret storage (and is protected by .gitignore), not a leak
    if os.path.basename(path).startswith('.env'):
        return []
    hits = []
    try:
        with open(path, 'rb') as f:
            head = f.read(4096)
            if is_binary(head):
                return hits
            f.seek(0)
            data = f.read(MAX_FILE_SIZE + 1)
            if len(data) > MAX_FILE_SIZE:
                return hits
        text = data.decode('utf-8', errors='replace')
    except OSError:
        return hits
    pats = STRONG_PATTERNS + (LOOSE_PATTERNS if loose else [])
    for i, line in enumerate(text.splitlines(), 1):
        for name, pat in pats:
            for m in re.finditer(pat, line):
                val = m.group(0)
                sev = 'placeholder' if is_placeholder(line, val) else 'danger'
                hits.append((sev, name, mask(val), i, line.strip()[:100]))
    return hits

def scan_root(root, loose=False):
    total = skipped = 0
    findings = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIR_NAMES]
        for fn in filenames:
            ext = os.path.splitext(fn)[1].lower()
            if ext in SKIP_EXTS:
                skipped += 1
                continue
            fp = os.path.join(dirpath, fn)
            try:
                if os.path.getsize(fp) > MAX_FILE_SIZE:
                    skipped += 1
                    continue
            except OSError:
                continue
            total += 1
            for hit in scan_file(fp, loose):
                findings.append((fp, hit))
    return total, skipped, findings

def scan_git_history(repo, loose=False):
    try:
        r = subprocess.run(['git', '-C', repo, 'rev-list', '--all', '--count'],
                           capture_output=True, text=True, timeout=60)
        n = int(r.stdout.strip() or 0)
    except Exception:
        return 0, []
    if n == 0:
        return 0, []
    try:
        r = subprocess.run(['git', '-C', repo, 'log', '--all', '-p', '--no-color',
                            '--format=COMMIT %H %s'],
                           capture_output=True, text=True, errors='replace',
                           timeout=600)
        text = r.stdout
    except Exception:
        return n, []
    hits = []
    pats = STRONG_PATTERNS + (LOOSE_PATTERNS if loose else [])
    cur = ''
    for line in text.splitlines():
        if line.startswith('COMMIT '):
            cur = line[7:19]
            continue
        for name, pat in pats:
            for m in re.finditer(pat, line):
                if is_placeholder(line):
                    continue
                hits.append((cur, name, mask(m.group(0))))
    return n, hits

def main():
    args = sys.argv[1:]
    git_hist = '--git-hist' in args
    loose = '--loose' in args
    scan_archive = '--archive' in args
    paths = [a for a in args if not a.startswith('--')]
    roots = paths if paths else DEFAULT_ROOTS

    print('=' * 56)
    print('  Secret security self-check  (secret_scan)')
    print('=' * 56)
    t0 = time.time()
    all_findings = []
    tot_files = tot_skip = 0
    saved_skip = set(SKIP_DIR_NAMES)
    if scan_archive:
        SKIP_DIR_NAMES.discard('_archive')
        # toolchain (Go toolchain ~18k files) always skipped: avoids timeout and has no user-secret value
    try:
        for root in roots:
            if not os.path.isdir(root):
                print('[error] path does not exist: %s' % root)
                return 2
        t, s, f = scan_root(root, loose)
        tot_files += t; tot_skip += s
        print('\nScanning: %s' % root)
        print('  files %d, skipped %d (binary/large/third-party dirs)' % (t, s))
        all_findings.extend(f)

    finally:
        SKIP_DIR_NAMES.clear()
        SKIP_DIR_NAMES.update(saved_skip)

    hist_hits = []
    if git_hist:
        repos = set()
        for root in roots:
            for dirpath, dirnames, _ in os.walk(root):
                dirnames[:] = [d for d in dirnames if d not in SKIP_DIR_NAMES]
                if '.git' in dirnames:
                    repos.add(dirpath)
        for repo in sorted(repos):
            n, h = scan_git_history(repo, loose)
            print('\ngit history: %s  (%d commits)' % (repo, n))
            if h:
                hist_hits.extend((repo,) + x for x in h)

    print('\n' + '-' * 56)
    danger = [x for x in all_findings if x[1][0] == 'danger']
    ph = [x for x in all_findings if x[1][0] == 'placeholder']
    print('workspace hits: danger %d, placeholder %d' % (len(danger), len(ph)))
    for fp, (sev, name, val, ln, ctx) in danger:
        print('  [DANGER] %s:%d  %s  %s' % (os.path.relpath(fp), ln, name, val))
    for fp, (sev, name, val, ln, ctx) in ph:
        print('  [PLACEHOLDER] %s:%d  %s  %s' % (os.path.relpath(fp), ln, name, val))
    if git_hist:
        print('git history hits: %d' % len(hist_hits))
        for repo, sha, name, val in hist_hits:
            print('  [HISTORY] %s %s  %s  %s' % (os.path.basename(repo), sha, name, val))
    if not danger and not hist_hits:
        print('\nresult: no real secrets found')
        rc = 0
    else:
        print('\nresult: %d real secret(s) found, handle immediately!' % (len(danger) + len(hist_hits)))
        rc = 1
    print('elapsed: %.1fs' % (time.time() - t0))
    return rc

if __name__ == '__main__':
    sys.exit(main())

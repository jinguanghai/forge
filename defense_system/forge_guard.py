# -*- coding: utf-8 -*-
"""forge_guard.py — 铸剑炉守门人: 完整性监控 + 权限体检 + 暴露面扫描
用法:
  python forge_guard.py init      # 首次建立哈希基线
  python forge_guard.py check     # 比对基线, 报告篡改 (默认模式)
  python forge_guard.py audit     # 全身体检: 权限/监听/防火墙/密钥泄露风险
"""
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
    log(f"[INIT] 基线已建立: {len(data)} 个文件, 保存到 {BASELINE}")
    for f, v in data.items():
        if v.get("missing"):
            log(f"  [WARN] 缺失: {f}")

def check():
    if not os.path.exists(BASELINE):
        log("[ERROR] 基线不存在, 先运行 init")
        return 1
    with open(BASELINE, "r", encoding="utf-8") as f:
        base = json.load(f)
    issues = 0
    log(f"[CHECK] 开始完整性比对 ({len(base)} 文件)...")
    for f, v in base.items():
        fp = os.path.join(BASE, f)
        if not os.path.exists(fp):
            log(f"  [!!] 文件丢失: {f}")
            issues += 1
            continue
        cur = sha256(fp)
        if cur != v["sha256"]:
            log(f"  [!!] 篡改/变更: {f}  (基线 {v['sha256'][:12]}... -> 当前 {cur[:12]}...)")
            issues += 1
    if issues == 0:
        log("[CHECK] ✅ 全部文件完好, 无篡改")
    else:
        log(f"[CHECK] ⚠️ 发现 {issues} 处异常!")
    dc = deadcheck()
    # 返回码: 完整性异常优先, 其次死代码编译级问题
    return 1 if (issues or dc) else 0


def deadcheck():
    """死代码例行检查: 静态零引用扫描(仅告警) + go vet 编译级兜底(算问题)
    静态扫描可能有误报(教训: ForgeParams曾被静态误判), 故静态结果只WARN不计入失败;
    go vet 是编译级最后防线, 失败才算异常。
    """
    log("[DEADCODE] 死代码例行检查开始")
    issues = 0
    # 静态扫描 (仅告警)
    try:
        dc = os.path.join(os.path.dirname(os.path.abspath(__file__)), "deadcode_scan.py")
        r = subprocess.run([sys.executable, dc, BASE], capture_output=True, text=True,
                           timeout=120, encoding="utf-8", errors="replace")
        out = (r.stdout or "") + (r.stderr or "")
        for line in out.splitlines():
            log(f"  {line}")
        if "疑似死代码" in out:
            log("  [WARN] 静态疑似死代码(仅提示, 以 go build/vet 为准)")
    except Exception as e:
        log(f"  [ERR] 静态死代码扫描失败: {e}")
    # go vet 编译级兜底 (算问题)
    try:
        r = subprocess.run(["go", "vet", "./..."], cwd=BASE, capture_output=True, text=True,
                           timeout=180, encoding="utf-8", errors="replace")
        if r.returncode == 0:
            log("  [OK] go vet 通过: 无 unreachable/编译级问题")
        else:
            log(f"  [!!] go vet 发现问题 (exit={r.returncode}):")
            for line in (r.stdout or "").splitlines()[:30]:
                log(f"      {line}")
            issues += 1
    except Exception as e:
        log(f"  [ERR] go vet 执行失败: {e}")
    if issues == 0:
        log("[DEADCODE] ✅ 死代码检查完成, 无异常")
    else:
        log(f"[DEADCODE] ⚠️ 发现 {issues} 处编译级问题")
    return 1 if issues else 0

def audit():
    log("[AUDIT] 铸剑炉全身体检开始")
    # 1. 敏感文件权限
    log("  -- 1. .env 权限 (应仅管理员/当前用户) --")
    try:
        r = subprocess.run('icacls "D:\\forge\\.env"', shell=True, capture_output=True, text=True, timeout=10)
        for line in r.stdout.splitlines():
            if "Authenticated Users" in line or "Users" in line or "Everyone" in line:
                log(f"  [!!] 权限过宽: {line.strip()}")
            elif "BUILTIN\\Administrators" in line or "SYSTEM" in line:
                log(f"  [OK] {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 2. 监听端口 (非系统默认的对外端口)
    log("  -- 2. 对外监听端口 --")
    try:
        r = subprocess.run('netstat -ano | findstr LISTENING', shell=True, capture_output=True, text=True, timeout=15)
        for line in r.stdout.splitlines():
            if ("0.0.0.0:" in line or "[::]:" in line) and "127.0.0.1:" not in line and "[::1]:" not in line:
                port = line.split()[1].rsplit(":", 1)[-1]
                if port not in ("135", "445", "139", "5040", "5357", "7680", "49664", "49665", "49666", "49667", "49668", "49718", "49719", "49697"):
                    log(f"  [!!] 非常规对外端口: {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 3. 防火墙状态
    log("  -- 3. 防火墙 --")
    try:
        r = subprocess.run('netsh advfirewall show allprofiles state', shell=True, capture_output=True, text=True, timeout=15)
        for line in r.stdout.splitlines():
            if "State" in line and "ON" in line:
                log(f"  [OK] {line.strip()}")
    except Exception as e:
        log(f"  [ERR] {e}")
    # 4. 密钥泄露风险提示
    log("  -- 4. 密钥文件 --")
    for f in [".env", "memory.json"]:
        fp = os.path.join(BASE, f)
        if os.path.exists(fp):
            log(f"  [INFO] {f} 存在 (内含API密钥, 勿提交公开仓库; .gitignore应包含)")
    log("[AUDIT] 体检完成")
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
        print("用法: forge_guard.py init|check|audit")

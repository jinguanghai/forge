# -*- coding: utf-8 -*-
"""模拟测试: 生成『模拟攻击者』流量(不发起真实网络请求), 跑通 检测->阻断->溯源->告警 全链路."""
import os, sys, json, datetime, random, shutil
sys.stdout.reconfigure(encoding='utf-8', errors='replace')
BASE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BASE)
import ids, blocker, tracer, alert

LOG = os.path.join(BASE, "logs", "honeypot.jsonl")
os.makedirs(os.path.dirname(LOG), exist_ok=True)

def gen_fake_events():
    """生成三类模拟攻击者的事件记录"""
    now = datetime.datetime.now()
    evs = []
    def add(ip, port, svc, data="", ua=""):
        evs.append({"ts": now.isoformat(timespec="seconds"), "type": f"honeypot_{svc}",
                    "src_ip": ip, "src_port": port, "data": data, "ua": ua, "service": svc})
    # 攻击者A: SSH暴力破解
    for i in range(8):
        add("203.0.113.10", 40000 + i, "ssh", f"ssh-2.0-client password=admin{i}")
    # 攻击者B: 端口扫描 + 攻击工具UA
    for i, svc in enumerate(["ssh", "http", "ssh", "http", "http"]):
        add("198.51.100.77", 50000 + i, svc, ua="Mozilla/5.0 (compatible; Nmap Scripting Engine)")
    # 攻击者C: 路径爆破 + SQL注入载荷
    for p in ["/admin", "/wp-login.php", "/.env", "/config.php", "/phpmyadmin",
              "/api/user?id=1", "/api/user?id=1 union select 1,2", "/backup.zip",
              "/db.sql", "/server-status", "/manager/html", "/cgi-bin/test.cgi"]:
        add("192.0.2.55", 60000, "http", f"GET {p} HTTP/1.1", ua="sqlmap/1.7")
    # 正常访客(不应误报)
    for i in range(3):
        add("8.8.8.8", 61000 + i, "http", f"GET /index.html HTTP/1.1", ua="Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/126.0")
    return evs

def main():
    print("=" * 60)
    print("【防守反击体系 · 本地模拟测试】(不发起任何真实网络攻击)")
    print("=" * 60)
    # 清理旧日志, 生成模拟攻击流量
    if os.path.exists(LOG):
        os.remove(LOG)
    evs = gen_fake_events()
    with open(LOG, "w", encoding="utf-8") as f:
        for e in evs:
            f.write(json.dumps(e, ensure_ascii=False) + "\n")
    print(f"[模拟] 已生成蜜罐日志 {len(evs)} 条 (含3个模拟攻击源 + 1个正常访客)")

    # 1. 检测
    events = ids.parse_logs(LOG)
    alerts = ids.detect(events)
    print(f"\n--- 检测阶段: 告警 {len(alerts)} 条 ---")
    for a in alerts:
        print(f"  [{a['sev'].upper()}] {a['ip']} 规则={a['rule']}  {a['detail']}")

    # 2. 阻断(dry_run)
    print("\n--- 阻断阶段(dry_run, 只生成命令不执行) ---")
    added = blocker.apply_alerts(alerts, dry_run=True)

    # 3. 告警
    print("\n--- 告警阶段 ---")
    n = alert.send(alerts)

    # 4. 溯源
    print("\n--- 溯源阶段 ---")
    reports, path = tracer.run(events)
    for r in reports:
        print(f"  {r['ip']}: 评分{r['risk_score']}/100 服务{r['services']} UA{r['uas'][:1]}")

    # 5. 验证: 正常访客不应被误报
    normal_ips = {e['src_ip'] for e in events if e['src_ip'] == "8.8.8.8"}
    alerted_ips = {a['ip'] for a in alerts}
    assert not (normal_ips & alerted_ips), "误报! 正常访客被标记"
    print("\n[验证] ✅ 正常访客 8.8.8.8 未被误报")

    # 6. 输出黑名单
    bl = blocker._load_banlist()
    print(f"\n[结果] 黑名单 {len(bl)} 条: {[b['ip'] for b in bl]}")
    print(f"[结果] 溯源报告: {path}")
    print("\n全链路模拟测试通过 ✅ (未发起任何真实网络请求)")

if __name__ == "__main__":
    main()

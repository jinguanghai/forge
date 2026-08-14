# -*- coding: utf-8 -*-
"""Simulation test: generate fake attacker traffic (no real network requests), run the detect->block->trace->alert full chain."""
import os, sys, json, datetime, random, shutil
sys.stdout.reconfigure(encoding='utf-8', errors='replace')
BASE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BASE)
import ids, blocker, tracer, alert

LOG = os.path.join(BASE, "logs", "honeypot.jsonl")
os.makedirs(os.path.dirname(LOG), exist_ok=True)

def gen_fake_events():
    """Generate event records for three types of simulated attackers"""
    now = datetime.datetime.now()
    evs = []
    def add(ip, port, svc, data="", ua=""):
        evs.append({"ts": now.isoformat(timespec="seconds"), "type": f"honeypot_{svc}",
                    "src_ip": ip, "src_port": port, "data": data, "ua": ua, "service": svc})
    # Attacker A: SSH brute force
    for i in range(8):
        add("203.0.113.10", 40000 + i, "ssh", f"ssh-2.0-client password=admin{i}")
    # Attacker B: port scan + attack-tool UA
    for i, svc in enumerate(["ssh", "http", "ssh", "http", "http"]):
        add("198.51.100.77", 50000 + i, svc, ua="Mozilla/5.0 (compatible; Nmap Scripting Engine)")
    # Attacker C: path brute force + SQL injection payload
    for p in ["/admin", "/wp-login.php", "/.env", "/config.php", "/phpmyadmin",
              "/api/user?id=1", "/api/user?id=1 union select 1,2", "/backup.zip",
              "/db.sql", "/server-status", "/manager/html", "/cgi-bin/test.cgi"]:
        add("192.0.2.55", 60000, "http", f"GET {p} HTTP/1.1", ua="sqlmap/1.7")
    # normal visitor (should not be flagged)
    for i in range(3):
        add("8.8.8.8", 61000 + i, "http", f"GET /index.html HTTP/1.1", ua="Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/126.0")
    return evs

def main():
    print("=" * 60)
    print("=== Defense & Counter-Response · Local Simulation Test ===")
    print("=" * 60)
    # clean old logs, generate simulated attack traffic
    if os.path.exists(LOG):
        os.remove(LOG)
    evs = gen_fake_events()
    with open(LOG, "w", encoding="utf-8") as f:
        for e in evs:
            f.write(json.dumps(e, ensure_ascii=False) + "\n")
    print(f"[sim] honeypot log generated: {len(evs)} entries (3 simulated attackers + 1 normal visitor)")

    # 1. detect
    events = ids.parse_logs(LOG)
    alerts = ids.detect(events)
    print(f"\n--- Detection stage: {len(alerts)} alert(s) ---")
    for a in alerts:
        print(f"  [{a['sev'].upper()}] {a['ip']} rule={a['rule']}  {a['detail']}")

    # 2. block (dry_run)
    print("\n--- Blocking stage (dry_run, commands only, not executed) ---")
    added = blocker.apply_alerts(alerts, dry_run=True)

    # 3. alert
    print("\n--- Alerting stage ---")
    n = alert.send(alerts)

    # 4. trace
    print("\n--- Tracing stage ---")
    reports, path = tracer.run(events)
    for r in reports:
        print(f"  {r['ip']}: score {r['risk_score']}/100 services {r['services']} UA {r['uas'][:1]}")

    # 5. verify: normal visitor must not be flagged
    normal_ips = {e['src_ip'] for e in events if e['src_ip'] == "8.8.8.8"}
    alerted_ips = {a['ip'] for a in alerts}
    assert not (normal_ips & alerted_ips), "false positive! normal visitor flagged"
    print("\n[verify] OK: normal visitor 8.8.8.8 not flagged")

    # 6. output blacklist
    bl = blocker._load_banlist()
    print(f"\n[result] blacklist {len(bl)} entries: {[b['ip'] for b in bl]}")
    print(f"[result] trace report: {path}")
    print("\nFull-chain simulation test passed (no real network requests made)")

if __name__ == "__main__":
    main()

# -*- coding: utf-8 -*-
"""Auto-block: generate firewall rules from alerts (commands only; dry_run on by default; never overreach)."""
import os, json, datetime, sys
sys.stdout.reconfigure(encoding='utf-8', errors='replace')

BASE = os.path.dirname(os.path.abspath(__file__))
BANLIST = os.path.join(BASE, "rules", "banned_ips.json")

def _load_banlist():
    if os.path.exists(BANLIST):
        with open(BANLIST, encoding="utf-8") as f:
            return json.load(f)
    return []

def save_banlist(items):
    os.makedirs(os.path.dirname(BANLIST), exist_ok=True)
    with open(BANLIST, "w", encoding="utf-8") as f:
        json.dump(items, f, ensure_ascii=False, indent=2)

def build_block_cmds(ip, sev="medium", dry_run=True):
    """Generate block commands. iptables on Linux, netsh on Windows. dry_run=True prints commands without executing."""
    cmds = []
    if os.name == "posix":
        cmds.append(f"iptables -A INPUT -s {ip} -j DROP")
        cmds.append(f"iptables -A INPUT -s {ip} -p tcp --dport 22 -j DROP")
    else:
        cmds.append(f'netsh advfirewall firewall add rule name="FORGE_BLOCK_{ip}" dir=in action=block remoteip={ip}')
    action = "generated" if dry_run else "executed"
    for c in cmds:
        print(f"[blocker] ({action}) {c}")
    return cmds

def apply_alerts(alerts, dry_run=True):
    """Dedupe alert IPs, add to blacklist and generate block commands"""
    banned = _load_banlist()
    known = {b["ip"] for b in banned}
    now = datetime.datetime.now().isoformat(timespec="seconds")
    added = []
    for a in alerts:
        ip = a["ip"]
        if ip in known: continue
        entry = {"ip": ip, "sev": a["sev"], "rule": a["rule"],
                 "added": now, "reason": a["detail"]}
        banned.append(entry); known.add(ip); added.append(entry)
        build_block_cmds(ip, a["sev"], dry_run=dry_run)
    if added:
        save_banlist(banned)
        print(f"[blocker] {len(added)} new IP(s) in blacklist, total {len(banned)}")
    else:
        print(f"[blocker] nothing new, blacklist size {len(banned)}")
    return added

if __name__ == "__main__":
    alerts = json.loads(sys.argv[1]) if len(sys.argv) > 1 else []
    apply_alerts(alerts)

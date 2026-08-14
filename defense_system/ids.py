# -*- coding: utf-8 -*-
"""Intrusion detection: parse honeypot/system logs, rule engine identifies attack behavior. Emits alert events."""
import json, os, re, datetime, collections

RULE_SSH_BRUTEFORCE  = {"name": "ssh_bruteforce",  "desc": "SSH brute force: >=5 attempts from same source within 60s",  "sev": "high",   "window": 60,  "count": 5}
RULE_PORT_SCAN       = {"name": "port_scan",       "desc": "Port scan: same source hits multiple distinct services", "sev": "medium", "window": 60,  "count": 3}
RULE_ATTACK_UA       = {"name": "attack_ua",       "desc": "Attack-tool UA signature (sqlmap/nmap/nessus...)", "sev": "high", "window": 0, "count": 1}
RULE_PATH_BRUTE      = {"name": "path_bruteforce", "desc": "Path brute force: >=10 distinct path requests from same source", "sev": "medium", "window": 120, "count": 10}

UA_ATTACK_PATTERNS = [
    r"sqlmap", r"nmap", r"nessus", r"nikto", r"masscan", r"zgrab",
    r"hydra", r"medusa", r"dirbuster", r"gobuster", r"wpscan", r"acunetix",
]

def parse_logs(logfile):
    """Read honeypot jsonl log -> event list"""
    events = []
    if not os.path.exists(logfile):
        return events
    with open(logfile, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line: continue
            try:
                ev = json.loads(line)
                events.append(ev)
            except Exception:
                continue
    return events

def _parse_ts(ts):
    try:
        return datetime.datetime.fromisoformat(ts)
    except Exception:
        return datetime.datetime.min

def detect(events):
    """Rule engine: return alert list [{ip, rule, sev, detail, evidence:[...]}]"""
    alerts = []
    by_ip = collections.defaultdict(list)
    for ev in events:
        by_ip[ev.get("src_ip")].append(ev)

    for ip, evs in by_ip.items():
        evs.sort(key=lambda e: _parse_ts(e.get("ts", "")))
        # Rule 1: SSH brute force
        ssh = [e for e in evs if e.get("service") == "ssh"]
        if len(ssh) >= RULE_SSH_BRUTEFORCE["count"]:
            alerts.append({"ip": ip, "rule": RULE_SSH_BRUTEFORCE["name"],
                           "sev": RULE_SSH_BRUTEFORCE["sev"],
                           "detail": f"{len(ssh)} SSH connection attempts in a short window",
                           "evidence": [e.get("data","") for e in ssh[:5]]})
        # Rule 2: port scan (multiple service types hit)
        svcs = {e.get("service") for e in evs}
        if len(svcs) >= RULE_PORT_SCAN["count"]:
            alerts.append({"ip": ip, "rule": RULE_PORT_SCAN["name"],
                           "sev": RULE_PORT_SCAN["sev"],
                           "detail": f"same source probed multiple services: {sorted(svcs)}",
                           "evidence": []})
        # Rule 3: attack-tool UA
        for e in evs:
            ua = (e.get("ua") or "").lower()
            hit = [p for p in UA_ATTACK_PATTERNS if re.search(p, ua)]
            if hit:
                alerts.append({"ip": ip, "rule": RULE_ATTACK_UA["name"],
                               "sev": RULE_ATTACK_UA["sev"],
                                "detail": f"UA matches attack-tool signature: {hit}",
                               "evidence": [ua]})
                break
        # Rule 4: path brute force
        paths = [e.get("request","") for e in evs if e.get("service")=="http"]
        if len(paths) >= RULE_PATH_BRUTE["count"]:
            alerts.append({"ip": ip, "rule": RULE_PATH_BRUTE["name"],
                           "sev": RULE_PATH_BRUTE["sev"],
                           "detail": f"{len(paths)} HTTP requests (possible path brute force)",
                           "evidence": paths[:5]})
    return alerts

if __name__ == "__main__":
    import sys as _s
    lp = _s.argv[1] if len(_s.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "logs", "honeypot.jsonl")
    evs = parse_logs(lp)
    for a in detect(evs):
        print(a)

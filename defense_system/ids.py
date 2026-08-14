# -*- coding: utf-8 -*-
"""入侵检测: 解析蜜罐/系统日志, 规则引擎识别攻击行为. 输出告警事件."""
import json, os, re, datetime, collections

RULE_SSH_BRUTEFORCE  = {"name": "ssh_bruteforce",  "desc": "SSH暴力破解: 同源5次尝试/60s",  "sev": "high",   "window": 60,  "count": 5}
RULE_PORT_SCAN       = {"name": "port_scan",       "desc": "端口扫描: 同源命中多个不同服务", "sev": "medium", "window": 60,  "count": 3}
RULE_ATTACK_UA       = {"name": "attack_ua",       "desc": "攻击工具UA特征(sqlmap/nmap/nessus等)", "sev": "high", "window": 0, "count": 1}
RULE_PATH_BRUTE      = {"name": "path_bruteforce", "desc": "路径爆破: 同源10次不同路径请求", "sev": "medium", "window": 120, "count": 10}

UA_ATTACK_PATTERNS = [
    r"sqlmap", r"nmap", r"nessus", r"nikto", r"masscan", r"zgrab",
    r"hydra", r"medusa", r"dirbuster", r"gobuster", r"wpscan", r"acunetix",
]

def parse_logs(logfile):
    """读取蜜罐jsonl日志 -> 事件列表"""
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
    """规则引擎: 返回告警列表 [{ip, rule, sev, detail, evidence:[...]}]"""
    alerts = []
    by_ip = collections.defaultdict(list)
    for ev in events:
        by_ip[ev.get("src_ip")].append(ev)

    for ip, evs in by_ip.items():
        evs.sort(key=lambda e: _parse_ts(e.get("ts", "")))
        # 规则1: SSH暴力破解
        ssh = [e for e in evs if e.get("service") == "ssh"]
        if len(ssh) >= RULE_SSH_BRUTEFORCE["count"]:
            alerts.append({"ip": ip, "rule": RULE_SSH_BRUTEFORCE["name"],
                           "sev": RULE_SSH_BRUTEFORCE["sev"],
                           "detail": f"短时间内{len(ssh)}次SSH连接尝试",
                           "evidence": [e.get("data","") for e in ssh[:5]]})
        # 规则2: 端口扫描(命中多个服务类型)
        svcs = {e.get("service") for e in evs}
        if len(svcs) >= RULE_PORT_SCAN["count"]:
            alerts.append({"ip": ip, "rule": RULE_PORT_SCAN["name"],
                           "sev": RULE_PORT_SCAN["sev"],
                           "detail": f"同源探测多个服务: {sorted(svcs)}",
                           "evidence": []})
        # 规则3: 攻击工具UA
        for e in evs:
            ua = (e.get("ua") or "").lower()
            hit = [p for p in UA_ATTACK_PATTERNS if re.search(p, ua)]
            if hit:
                alerts.append({"ip": ip, "rule": RULE_ATTACK_UA["name"],
                               "sev": RULE_ATTACK_UA["sev"],
                               "detail": f"UA匹配攻击工具特征: {hit}",
                               "evidence": [ua]})
                break
        # 规则4: 路径爆破
        paths = [e.get("request","") for e in evs if e.get("service")=="http"]
        if len(paths) >= RULE_PATH_BRUTE["count"]:
            alerts.append({"ip": ip, "rule": RULE_PATH_BRUTE["name"],
                           "sev": RULE_PATH_BRUTE["sev"],
                           "detail": f"{len(paths)}次HTTP请求(疑似路径爆破)",
                           "evidence": paths[:5]})
    return alerts

if __name__ == "__main__":
    import sys as _s
    lp = _s.argv[1] if len(_s.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "logs", "honeypot.jsonl")
    evs = parse_logs(lp)
    for a in detect(evs):
        print(a)

# -*- coding: utf-8 -*-
"""Tracing: aggregate all behavior of an attacker IP -> fingerprint / timeline / risk score -> Markdown report."""
import json, os, datetime, collections, sys
sys.stdout.reconfigure(encoding='utf-8', errors='replace')

BASE = os.path.dirname(os.path.abspath(__file__))

def _parse_ts(ts):
    try: return datetime.datetime.fromisoformat(ts)
    except Exception: return datetime.datetime.min

def risk_score(evs):
    score = 0
    for e in evs:
        svc = e.get("service")
        data = (e.get("data") or "").lower()
        ua = (e.get("ua") or "").lower()
        if svc == "ssh": score += 3
        if "password" in data or "login" in data: score += 2
        if any(k in ua for k in ("sqlmap","nmap","nessus","hydra","nikto")): score += 5
        if "..%2f" in data or "union select" in data or "cmd=" in data: score += 5
        if e.get("type","").endswith("_err"): score += 1
    return min(score, 100)

def trace_ip(ip, events):
    evs = [e for e in events if e.get("src_ip") == ip]
    evs.sort(key=lambda e: _parse_ts(e.get("ts", "")))
    ports = sorted({e.get("src_port") for e in evs if e.get("src_port")})
    svcs = sorted({e.get("service") for e in evs})
    uas = sorted({e.get("ua") for e in evs if e.get("ua")})
    payloads = [e.get("data","") for e in evs if e.get("data")]
    return {
        "ip": ip, "events": len(evs),
        "time_span": f"{evs[0].get('ts')} ~ {evs[-1].get('ts')}" if evs else "-",
        "services": svcs, "src_ports": ports[:20],
        "uas": uas[:10], "payloads": payloads[:10],
        "risk_score": risk_score(evs),
    }

def gen_report(reports, outfile=None):
    lines = ["# Attack Trace Report", "", f"Generated at: {datetime.datetime.now().isoformat(timespec='seconds')}", ""]
    for r in reports:
        sev = "🔴 HIGH" if r["risk_score"] >= 50 else ("🟠 MEDIUM" if r["risk_score"] >= 20 else "🟡 LOW")
        lines += [f"## Attacker {r['ip']}  [{sev}]", "",
                  f"- Risk score: **{r['risk_score']}/100**",
                  f"- Events: {r['events']}",
                  f"- Time span: {r['time_span']}",
                  f"- Services probed: {r['services']}",
                  f"- Source ports: {r['src_ports']}",
                  f"- UA fingerprints: {r['uas']}",
                  ""]
        if r["payloads"]:
            lines += ["### Attack payload samples", ""]
            for p in r["payloads"]:
                lines += [f"```", p[:300], "```", ""]
        lines += ["---", ""]
    md = "\n".join(lines)
    if outfile:
        os.makedirs(os.path.dirname(outfile), exist_ok=True)
        with open(outfile, "w", encoding="utf-8") as f:
            f.write(md)
        print("[tracer] report generated:", outfile)
    return md

def run(events, outdir=None):
    outdir = outdir or os.path.join(BASE, "reports")
    ips = sorted({e.get("src_ip") for e in events})
    reports = [trace_ip(ip, events) for ip in ips]
    path = os.path.join(outdir, f"trace_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}.md")
    gen_report(reports, path)
    return reports, path

if __name__ == "__main__":
    lp = sys.argv[1] if len(sys.argv) > 1 else os.path.join(BASE, "logs", "honeypot.jsonl")
    from ids import parse_logs
    evs = parse_logs(lp)
    reps, p = run(evs)
    for r in reps:
        print(r["ip"], r["risk_score"], r["services"])

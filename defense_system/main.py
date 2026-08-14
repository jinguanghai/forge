# -*- coding: utf-8 -*-
"""Defense & counter-response main entry: honeypot + detection + blocking + tracing + alerting full chain.
Usage: python main.py           # honeypot + monitoring (foreground)
       python main.py --once    # single detect-block-trace-alert round (cron-friendly)"""
import os, sys, time, json, threading
sys.stdout.reconfigure(encoding='utf-8', errors='replace')
BASE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BASE)
import ids, blocker, tracer, alert

LOG = os.path.join(BASE, "logs", "honeypot.jsonl")

def one_pass():
    print("=" * 50)
    print("[main] detection-response round started")
    events = ids.parse_logs(LOG)
    print(f"[main] honeypot events: {len(events)}")
    alerts = ids.detect(events)
    if alerts:
        sev_counts = {}
        for a in alerts: sev_counts[a["sev"]] = sev_counts.get(a["sev"], 0) + 1
        print(f"[main] alerts: {sev_counts}")
        for a in alerts:
            print(f"  [{a['sev'].upper()}] {a['ip']} <- {a['rule']}: {a['detail']}")
        # blocking (dry_run by default, commands only)
        blocker.apply_alerts(alerts, dry_run=True)
        # alerting
        alert.send(alerts)
        # tracing
        reports, path = tracer.run(events)
        print("[main] trace report:", path)
    else:
        print("[main] no alerts this round, system OK")
    print("[main] round finished")

def watch_loop(interval=30):
    while True:
        try:
            one_pass()
        except Exception as e:
            print("[main] error:", e)
        time.sleep(interval)

if __name__ == "__main__":
    if "--once" in sys.argv:
        one_pass()
    else:
        print("[main] monitoring mode (one round per 30s). Ctrl+C to exit")
        watch_loop()

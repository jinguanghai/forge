# -*- coding: utf-8 -*-
"""防守反击体系 主入口: 蜜罐 + 检测 + 阻断 + 溯源 + 告警 全链路.
用法: python main.py            # 蜜罐+监控(前台运行)
      python main.py --once     # 只做一次检测-阻断-溯源-告警(适合cron)
"""
import os, sys, time, json, threading
sys.stdout.reconfigure(encoding='utf-8', errors='replace')
BASE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BASE)
import ids, blocker, tracer, alert

LOG = os.path.join(BASE, "logs", "honeypot.jsonl")

def one_pass():
    print("=" * 50)
    print("[main] 检测-响应 一轮开始")
    events = ids.parse_logs(LOG)
    print(f"[main] 蜜罐事件: {len(events)} 条")
    alerts = ids.detect(events)
    if alerts:
        sev_counts = {}
        for a in alerts: sev_counts[a["sev"]] = sev_counts.get(a["sev"], 0) + 1
        print(f"[main] 告警: {sev_counts}")
        for a in alerts:
            print(f"  [{a['sev'].upper()}] {a['ip']} <- {a['rule']}: {a['detail']}")
        # 阻断(默认dry_run, 只生成命令)
        blocker.apply_alerts(alerts, dry_run=True)
        # 告警
        alert.send(alerts)
        # 溯源
        reports, path = tracer.run(events)
        print("[main] 溯源报告:", path)
    else:
        print("[main] 本轮无告警, 系统正常")
    print("[main] 一轮结束")

def watch_loop(interval=30):
    while True:
        try:
            one_pass()
        except Exception as e:
            print("[main] 异常:", e)
        time.sleep(interval)

if __name__ == "__main__":
    if "--once" in sys.argv:
        one_pass()
    else:
        print("[main] 监控模式启动(每30秒一轮). Ctrl+C退出")
        watch_loop()

# -*- coding: utf-8 -*-
"""告警: 文件日志 + 可选Webhook/邮件(需在config.json配置, 默认只写本地日志)."""
import json, os, datetime, sys
sys.stdout.reconfigure(encoding='utf-8', errors='replace')

BASE = os.path.dirname(os.path.abspath(__file__))
ALERT_LOG = os.path.join(BASE, "logs", "alerts.jsonl")
CONFIG = os.path.join(BASE, "config.json")

def load_config():
    if os.path.exists(CONFIG):
        with open(CONFIG, encoding="utf-8") as f:
            return json.load(f)
    return {"webhook_url": "", "smtp": {"enabled": False}}

def send(alerts):
    cfg = load_config()
    now = datetime.datetime.now().isoformat(timespec="seconds")
    os.makedirs(os.path.dirname(ALERT_LOG), exist_ok=True)
    with open(ALERT_LOG, "a", encoding="utf-8") as f:
        for a in alerts:
            rec = dict(a); rec["alerted_at"] = now
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    print(f"[alert] 已记录 {len(alerts)} 条告警 -> {ALERT_LOG}")
    # Webhook 示例(默认关闭)
    if cfg.get("webhook_url"):
        import urllib.request
        payload = json.dumps({"text": f"[防守反击] {len(alerts)}条告警: " + "; ".join(a['detail'] for a in alerts[:3])}).encode()
        req = urllib.request.Request(cfg["webhook_url"], data=payload, headers={"Content-Type": "application/json"})
        try:
            urllib.request.urlopen(req, timeout=5)
            print("[alert] Webhook已推送")
        except Exception as e:
            print("[alert] Webhook推送失败:", e)
    return len(alerts)

if __name__ == "__main__":
    send(json.loads(sys.argv[1]))

> 📜 **Forge Defense & Counter-Response Charter** (user principles) — see [CHARTER.md](CHARTER.md)

# Defense & Counter-Response · Security Response Framework

A purely **defensive** security framework: entrapment, detection, blocking, tracing, alerting.
**It never initiates any attack.**

## Components

| File | Purpose |
|---|---|
| honeypot.py | Honeypot: emulates SSH (2222) / HTTP (8080) to entrap attackers, records only |
| ids.py | Intrusion detection: rule engine for SSH brute-force / port scan / malicious UA / path probing |
| blocker.py | Auto-blocking: generates iptables/netsh commands, **dry_run by default (commands only)** |
| tracer.py | Tracing: attacker fingerprint + timeline + risk score → Markdown report |
| alert.py | Alerting: local log + optional Webhook/email (requires config.json) |
| main.py | Orchestration: `python main.py --once` single round / monitoring loop by default |
| test_sim.py | Local simulation test (no real traffic) |

## Deploy on a VPS (Linux)

1. Upload this directory to the server
2. `python3 main.py --once` to verify the chain
3. `nohup python3 main.py > monitor.log 2>&1 &` for background monitoring
4. When verified, set `auto_block.dry_run: false` in `config.json` to enable real blocking
5. Optional: `crontab` entry to run `python3 main.py --once` periodically

## Legal & Security Boundary

- The honeypot listens on **your own** ports and records people who **actively probe you** — fully legal
- Blocking commands act on **your own server** only; no retaliation against any third party
- Tracing reports are for **evidence / law enforcement**, handed to the cyber-security authorities

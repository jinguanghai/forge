# Forge Defense & Counter-Response Charter
> Version 1.0 · 2026-08-01 · Formal codification of the user's principles

## 1. Statement of Principles (the user's own words)

> **My core principle is simple: I am a good person, I do not harm others — but others must not harm me either. If you intend me harm, I have countermeasures ready.**

## 2. Three Iron Rules

| # | Rule | Implementation |
|---|------|----------------|
| 1 | **I am good and do no harm** | Every action in this system acts only on **our own assets**; no active scanning, no intrusion, no probing of any third-party system |
| 2 | **Others must not harm me either** | Honeypot entrapment, intrusion detection, integrity guardian — discover **and leave evidence of** actions that harm us |
| 3 | **If you intend me harm, I have countermeasures** | Escalating response: log → block → trace → evidence → legal action, all within the legal framework |

## 3. Escalating Countermeasures (matching the attacker's intent)

| Level | Attack behavior | Our response | Tools | Legality |
|-------|-----------------|--------------|-------|----------|
| L1 Harassment | Port scan / probing | Record fingerprint + source, warn in logs | honeypot.py, ids.py | ✅ Purely defensive |
| L2 Intrusion | Brute force / exploit | Auto-block (ban IP), honeypot entrapment | blocker.py, honeypot.py | ✅ Acts on our own system |
| L3 Compromised | File tampering / backdoor | Integrity guardian alert (hourly) | forge_guard.py | ✅ Self-check, self-evidence |
| L4 Adversarial | Persistent attack / data theft | Trace & collect evidence → Markdown report → **hand to cyber-security authorities / legal channel** | tracer.py | ✅ By law |

## 4. Hard Red Lines (this system never crosses)

1. ❌ No counter-attack on the attacker's machine (even with known IP)
2. ❌ No DDoS retaliation, no destruction of other systems
3. ❌ No probing of any unauthorized third-party target
4. ❌ No identity concealment for any network activity
5. ✅ All evidence goes through **reporting / legal channels** — the attacker faces a court, not our own hands

## 5. Why "Countermeasure" Stops at the Law

- Private revenge = turning from victim into offender; you lose half the case before it starts
- Attacker IPs are mostly proxies/zombies; striking back hits innocent machines
- A complete evidence chain handed to the authorities yields far heavier punishment than private vendettas — with zero risk to ourselves

## 6. System File Map

```
defense_system/
├── honeypot.py       Honeypot (entrapment, records only)
├── ids.py            Intrusion detection (rule engine)
├── blocker.py        Auto-blocking (dry_run by default)
├── tracer.py         Trace & evidence → incident report
├── alert.py          Alerting (log / Webhook / email)
├── main.py           Monitoring orchestration
├── forge_guard.py    Forge guardian (integrity monitoring)
├── forge_baseline.json   Hash baseline of 14 files
├── config.json       Thresholds and switches
└── test_sim.py       Local simulation test
```

## 7. Daily Operations

- Hourly: guardian auto integrity check (scheduled task ForgeGuardHourly)
- Under attack: `python main.py --once` single-round inspection
- For evidence: `python tracer.py <event-id>` generates a Markdown report
- On VPS: set `dry_run` to `false` in `config.json` to enable real blocking

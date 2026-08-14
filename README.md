# Forge — General-Purpose Digital Agent

> *The forge that shapes software.* One tool. Eighteen gates. Self-evolving.

**Forge** is a self-hosted AI agent engine built on a radical design: **a single tool** (`forge`) that compiles, executes, and destroys code across **18 language & logic gates**, paired with an LLM orchestration layer. Generation and execution are strictly separated — the LLM brain writes code, the forge runs it, and the verdict feeds back. No hidden tools, no magic.

## Highlights

- Single-tool architecture — every capability flows through one `forge` tool; generated code is compiled, executed, and destroyed on the spot
- 18 tool gates — python / go / sh / node / deno / rust / tcc / math / logic / system / knowledge / regex / chain / eprover / repair / self / tcm / browser
- LLM brain + code body — the agent reasons in real time while the body stays deterministic; `repair` and `self` gates let the agent improve its own source code
- Defense in depth — integrity guard, review gate, baseline audit (`defense_system/`)
- Robust memory — atomic store (tmp+rename), fold/recall engine, event log
- Streaming LLM client — 3-tier reasoning effort, smart routing, provider config via `.env`
- Cross-platform — Windows first-class; platform files isolate OS specifics

## Quick Start

```bash
git clone https://github.com/jinguanghai/forge.git
cd forge
cp .env.example .env      # set DEEPSEEK_API_KEY (DeepSeek or any OpenAI-compatible)
go build -o forge .
./forge
```

## Tool Gates

| Gate | Engine | Purpose |
|---|---|---|
| `python` | Python 3 | general scripting, data & file processing |
| `go` / `rust` / `tcc` | Go / Rust / TinyCC | compiled execution |
| `sh` | shell | system commands |
| `node` / `deno` | Node / Deno | JS/TS execution |
| `math` | CAS | symbolic computation & verification |
| `logic` | SMT solver | prove / equivalence / consistency checks |
| `system` | model checker | state machines, invariants, deadlock |
| `knowledge` | KB query | knowledge-base retrieval |
| `regex` | regex engine | validation with fullmatch semantics |
| `chain` | orchestrator | conditional multi-gate pipelines |
| `eprover` | E prover | TPTP first-order theorem proving |
| `repair` | analyzer | code repair suggestions |
| `self` | self-mod | agent self-improvement |
| `tcm` | TCM gate | herb-pair & pattern queries (data self-hosted) |
| `browser` | web | browser automation |

## Project Layout

| File | Role |
|---|---|
| `main.go` | entrypoint, agent loop, self-replacement |
| `agent.go` | orchestration, infinite loop, triple protection |
| `forge.go` | core engine: 18 gates, cache, self-mod |
| `llm.go` | streaming client (DeepSeek & OpenAI-compatible) |
| `config.go` | `.env` configuration |
| `ux.go` | terminal rendering |
| `memory_*.go` | atomic store, fold/recall, event log |
| `guard*.go` | defense: integrity check, review gate |
| `health_report.go` | self-diagnostics |
| `upgrade.go` | self-update pipeline |
| `defense_system/` | guard scripts (check / audit / init) |

## Safety & Privacy

- This repository contains **engine source code only** — no API keys, personal data, or private knowledge bases
- Private data (personal knowledge, medical records, corpus data) **never** enters this repository
- Review `.env` and runtime files before committing anything of your own

## License

[MIT](LICENSE) © 2026 jinguanghai

---

[中文版](README.zh.md)

# Forge — LLM-Powered Multi-Language Compiler Sandbox

> The forge that shapes software. One tool. Fourteen gates. Deterministic at the core.

**Forge** is a self-hosted LLM-powered execution framework built on a deliberate design: **a single tool** (`forge`) that compiles, executes, and destroys code across **14 language & logic gates**, paired with an LLM orchestration layer. Generation and execution are strictly separated — the LLM brain writes code, the forge runs it, and the verdict feeds back.

The core philosophy is **determinism by construction**: the LLM is a live, statistical system that reasons in real time and can drift; the body is a deterministic program that enforces, verifies, and bails out. Every capability flows through one tool, so there are no hidden hooks and no magic.

## Highlights

- **Single-tool architecture** — every capability flows through one `forge` tool; generated code is compiled, executed, and destroyed on the spot
- **14 tool gates** — python / go / node / math / logic / regex / knowledge / tcm / browser / chain / self / relation / media / task
- **LLM brain + code body** — the agent reasons in real time while the body stays deterministic; `chain` orchestrates multi-gate pipelines, `self` lets the agent modify its own source (with approval)
- **Defense in depth** — integrity guard, review gate, baseline checks
- **Robust memory** — atomic store (tmp + rename), fold/recall engine, event log
- **Streaming LLM client** — 3-tier reasoning effort, smart routing, provider config via `.env`
- **Cross-platform** — Windows first-class; platform files isolate OS specifics

## Quick Start

```bash
git clone https://github.com/jinguanghai/forge.git
cd forge
cp .env.example .env      # set DEEPSEEK_API_KEY (any OpenAI-compatible provider)
go build -o forge .
./forge
```

## Tool Gates

| Gate | Engine | Purpose |
|---|---|---|
| `python` | Python 3 | general scripting, data & file processing (default) |
| `go` | Go | compiled execution |
| `node` | Node | JS/TS execution |
| `math` | CAS | symbolic computation & verification |
| `logic` | SMT solver | prove / equivalence / consistency checks |
| `regex` | regex engine | validation with fullmatch semantics |
| `knowledge` | KB query | knowledge-base retrieval |
| `tcm` | domain query | traditional Chinese medicine herb-pair lookup |
| `browser` | headless | web browsing (direct, no proxy) |
| `chain` | orchestrator | conditional multi-gate pipelines |
| `relation` | skeleton check | wiring / relation assertion verification (symbol table + reference graph) |
| `self` | hot-rewrite | modify its own source (requires approval) |
| `media` | media API | image & video generation (bring your own MiniMax API key) |
| `task` | long-task channel | long jobs that outlive the 30s budget: submit / poll / wait / tail, runs detached |

## Architecture

The body is a small Go program: `agent.go` orchestrates the LLM loop, `forge.go` implements the gates, `llm.go` streams from the provider, `config.go` owns configuration, `ux.go` renders the terminal. Session stats and display helpers live in their own files (`stats.go`, `style.go`) to keep responsibilities separated.

## License

MIT

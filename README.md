# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**A multi-agent runtime that treats "expert agents" as tools**: every expert agent binds
itself to a model that suits it, while the parent agent only orchestrates and
synthesises — tokens are spent where they actually matter.

- **Expert gets expert model** — every agent declares its own `model:` in `agents/*.md`,
  so a simple retrieval does not pay for reasoning capability it does not need.
- **Tools are isolation** — an expert agent is called as a **bounded capability**: its own
  tool loop and intermediate reasoning stay in its context, and the parent agent
  receives only the summary it wrote.
- **Structure is savings** — tokens are saved by drawing boundaries, not by compressing
  prompts.

"who may do what" becomes a few hard rules:

- **Tool permissions are named, and the default is to ask** — a tool nobody names asks
  first; the exceptions are the ones a file's own `allows:` list names, and each agent
  says which of them it may use at all.
- **Delegation has a depth cap** — the entry agent is level 0, an agent at depth `d` may
  spawn children if and only if `d < max_depth`; a topology over the cap is a **startup
  error**, never a silent truncation.
- **One session can run several agents** — independent work is dispatched in parallel.
- **Configuration is files** — model profiles, prompts and tool tiers are files you
  can read and edit.
- **One binary, runs anywhere** — the frontend is embedded into a single file, compiled
  statically across platforms.

★ The boundaries, stated up front: the token saving is largest on tasks that decompose
with verbose tool output — on strongly order-dependent tasks a single agent may be faster
and more accurate; and "high performance" means useful output per token, it promises
neither zero latency nor unlimited concurrency.

> 中文版：[README_zh.md](README_zh.md)

## Documentation

★ **Everything else lives in a document, not here.** This file says what openheron is;
the documents say how it works.

| If you want to… | Read |
|---|---|
| Install it, run it, drive the interface | [`docs/user-manual.md`](docs/user-manual.md) · [`docs/user-manual_zh.md`](docs/user-manual_zh.md) |
| Work on this tree — layering, recipes, testing | [`docs/developer-manual.md`](docs/developer-manual.md) · [`docs/developer-manual_zh.md`](docs/developer-manual_zh.md) |
| Understand how the code is put together | [`docs/architecture.md`](docs/architecture.md) · [`docs/architecture_zh.md`](docs/architecture_zh.md) |
| Read the configuration system and the tool surface | [`docs/config-design.md`](docs/config-design.md) |
| Read the runtime — the run lifecycle, admission, context, logging | [`docs/runtime-design.md`](docs/runtime-design.md) |
| Read where a tool comes from, the filesystem backend, MCP, the Kit | [`docs/tools-design.md`](docs/tools-design.md) |
| Read the interface and its resources — attachments and motion | [`docs/ui-design.md`](docs/ui-design.md) |
| Read the traps this tree has hit, and what they cost | [`docs/pitfalls.md`](docs/pitfalls.md) |
| Package it as a sandbox workload | [`sandbox-kit/TUTORIAL.md`](sandbox-kit/TUTORIAL.md) · [`sandbox-kit/BUILDING.md`](sandbox-kit/BUILDING.md) |
| Follow the conventions of the people and AI working here | [`AGENTS.md`](AGENTS.md) · [`AGENTS_zh.md`](AGENTS_zh.md) |
| Read the factory configuration sample | [`.openheron-example/README.md`](.openheron-example/README.md) |

★ The first three rows are **bilingual** — the English file is the main version, the `_zh`
one the Chinese, and the two correspond one to one. The five design documents are
**Chinese only**: their Chinese content took over the plain name, so the link above is the
only entry there is. The Kit's two guides have `_zh` twins beside them.

★ The finer-grained design documents and the industry comparison live in the **upstream
repository** under `docs/` — for the mechanism breakdown and the trade-offs, see the
upstream `docs/positioning.md`.

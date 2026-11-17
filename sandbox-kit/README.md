# openheron — Docker Sandbox Kits

The openheron agent server, packaged as Docker Sandboxes **v3 Kits**.

## Start here

| | |
|---|---|
| **I want to run openheron in a sandbox** | [`TUTORIAL.md`](TUTORIAL.md) — the published image, the credential, the browser. The Chinese version is [`TUTORIAL_zh.md`](TUTORIAL_zh.md). |
| **I want to change the Kit** | [`BUILDING.md`](BUILDING.md) — build it, change the descriptor, publish a version. The Chinese version is [`BUILDING_zh.md`](BUILDING_zh.md). |
| **I want to know why it is shaped this way** | [`docs/tools-design.md`](../docs/tools-design.md) — the design, and every assumption it rests on, measured. |
| **I want to see it work** | [`examples/demo-project`](examples/demo-project) — a tiny Go program with a bug in it, to point a sandbox at. |

## What is in here

| Kit | Kind | What it carries |
|---|---|---|
| [`openheron/`](openheron/) | `workload` | the server itself: the binary, the port, the startup hook, and the egress posture ("no restriction from this Kit") |
| [`openheron-deepseek/`](openheron-deepseek/) | `mixin` | the DeepSeek credential and the network rule that goes with it |
| [`openheron-openai/`](openheron-openai/) | `mixin` | the same, for OpenAI |

★ The workload is vendor-neutral: it declares no credential at all, because
which provider, which endpoint and which variable name are the user's
business. Vendor mixins are **optional** and compose in any number.

## The rules the runtime enforces

- **One workload, one mixin per vendor.** The runtime resolves each reference
  in the set separately, so a workload without a mixin starts and then answers
  every model call with a 401.
- **A mixin carries the network rule with the credential.** Every domain the
  credential injects into must appear literally in the allow list for the same
  phase; the workload's own `*` covers every domain but does not satisfy that
  check.
- **The port is declared; the host side is not.** The workload declares the
  port it serves on, and the runtime allocates the host port.
- **Where a Kit may come from is a policy.** The runtime's allowlist decides,
  and the default holds `docker.io/` and `dhi.io/`.

★ Each of those is measured, and the measurements are in
[`docs/tools-design.md`](../docs/tools-design.md) §4.4. The
repeatable form of them is [`acceptance/run.sh`](acceptance/run.sh) — 21
checks, and it exits non-zero if any of them stops holding.

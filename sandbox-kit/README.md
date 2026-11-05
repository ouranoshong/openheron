# openheron — Docker Sandbox Kits

The openheron agent server, packaged as Docker Sandboxes **v3 Kits**.

Three of them, and the split is the point:

| Kit | Kind | What it carries |
|---|---|---|
| [`openheron/`](openheron/) | `workload` | the server itself: the binary, the startup hook, and the egress posture ("no restriction from this Kit") |
| [`openheron-deepseek/`](openheron-deepseek/) | `mixin` | the DeepSeek credential and the network rule that goes with it |
| [`openheron-openai/`](openheron-openai/) | `mixin` | the same, for OpenAI |

★ The workload is vendor-neutral: it declares no credential at all, because
which provider, which endpoint and which variable name are the user's
business. Vendor mixins are **optional** and compose in any number — compose
the ones you use, and leave the rest out.

The design behind it — and every assumption it rests on — is
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md).

★ **New to sandboxes?** [`TUTORIAL.md`](TUTORIAL.md) is the step-by-step
guide for a user: start a sandbox, open it in a browser, work on the files.
[`TUTORIAL_zh.md`](TUTORIAL_zh.md) is the Chinese version.

## Files

| File | What it is |
|---|---|
| `TUTORIAL.md` / `TUTORIAL_zh.md` | the step-by-step guide for a user; this file is the reference |
| `openheron/openheron.yaml` | the workload descriptor: the launch contract, the port, the egress posture, the startup hook |
| `openheron/openheron.dockerfile` | the content recipe: the base image and the binary |
| `openheron/entrypoint.sh` | the launch script: enter the workspace, seed the config, exec the server |
| `openheron-deepseek/openheron-deepseek.yaml` | the DeepSeek mixin: declaration only, no content, no Dockerfile |
| `openheron-openai/openheron-openai.yaml` | the OpenAI mixin: the same, for another vendor |
| `acceptance/run.sh` | the acceptance run: every claim this design makes, as a check that fails the script |

## Requirements

- `sbx` **0.42.0 or later**. Docker Sandboxes 0.42.0 fixed
  `CVE-2026-77179` and `CVE-2026-79994`, two guest-to-host escapes. The
  mitigation Docker documents — clone mode and no read-write host mounts — is
  exactly what openheron cannot use, so the version floor is the mitigation.
- A Docker account: `sbx login`.
- A hypervisor: KVM on Linux (Ubuntu 24.04+), Apple Silicon on macOS, Windows
  Hypervisor Platform on Windows.

## Build

```sh
make kit         # the workload: openheron-kit:0.0.6
make kit-mixin   # every mixin: openheron-kit-<vendor>:0.0.6
```

Both use a container-driver buildx builder, and both export into the local
docker store — the default builder drops the manifest annotations that make
an image a Kit, and without the export there is no local image to validate.

```sh
make kit-check   # kit-tck validate against the specification
```

## Run

★ **A mixin does not have to be an image.** It is a declaration, so a
directory holding one YAML file is a complete mixin — the runtime builds the
declaration-only image from it. Copy `openheron-deepseek/openheron-deepseek.yaml`
into a directory of your own and point `--kit` at that, and you never build
or publish anything. The tutorial gives the whole file.

★ **Where a Kit may come from is a policy.** The runtime's allowlist holds
`docker.io/` and `dhi.io/` by default; a Kit from anywhere else is refused
with the command that would allow it.

One vendor:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron --kit ./sandbox-kit/openheron-deepseek \
    --name openheron ~/my-project
```

★ **From a packaged image instead of the source tree**, the workload is an
OCI reference and the mixin stays a file:

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit ./openheron-deepseek \
    --name openheron ~/my-project
```

Two vendors — the mixins compose, and each one brings its own key and its own
network rule:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --kit ./sandbox-kit/openheron-openai \
    --name openheron ~/my-project
```

Without any mixin — the key goes into the sandbox:

```sh
sbx run ./sandbox-kit/openheron --name openheron \
    --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" ~/my-project
```

★ **The run above opens an agent session, and the session is what starts the
server.** The Kit's entrypoint is the agent's launch command, so a detached
run (`sbx run … --detach`) creates the sandbox and runs the startup hooks —
and leaves the server down. Let the session open, watch the server come up,
then detach from it: the Kit declares `long-running@1`, so the workload keeps
running when no session remains attached.

★ **The port is published for you.** The Kit declares
`com.docker.sandbox/port@1` for container port `8080`, so the runtime
publishes it on an ephemeral loopback port at start time. Read it with:

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32772       8080           tcp
::1         32772       8080           tcp
```

★ There is nothing to publish by hand. `sbx ports --publish` still works, but
a Kit cannot pin a host port — the runtime allocates it, because two Kits
asking for the same one would collide.

The configuration lands in `<workspace>/.openheron` — on your mount, not
inside the sandbox — so you can edit `llm/deepseek.yaml`, `agents/main.md` and
the policy files with your own editor, and openheron picks the change up.

★ `sbx rm` takes the published ports with it: after recreating a sandbox,
`sbx ports` reports "No published ports" and the forwarding does not come back
by itself.

## Credentials

A mixin declares its credential `proxyManaged`, so the container receives a
**sentinel** and the host-side proxy swaps in the real value on the way out.
Two ways to give the sandbox a working key:

### The placeholder — the key stays on the host

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ The placeholder is `proxy-managed` on purpose: that is the sentinel the
mixin's `credential@1` puts into the container, so the runtime's injection
and the host's binding agree without the user passing an environment
variable at all. The container ends up holding
`DEEPSEEK_API_KEY=proxy-managed`, and the real key never enters the sandbox.

★ `--value-stdin` keeps the key out of the process arguments and out of the
shell history. `sbx secret set deepseek` also works, but then a **binding**
has to be written into `~/.config/sbx/credentials.yaml` as well — two steps
instead of one.

### The key itself — the agent can read it

```sh
sbx run ./sandbox-kit/openheron --name openheron \
    --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" .
```

A runtime variable carries whatever it is given, so the real key ends up
inside the VM — where the agent reads it out of the environment, and where
anything it runs can send it anywhere. The model call still works: the proxy
forwards the bearer token unchanged, because it is not the placeholder.

★ So the two are not two spellings of the same thing. The first keeps the
secret on the host and leaves the agent unable to read it; the second hands
the agent the secret and asks it not to leak it. Choosing the second is
choosing to trust the agent — a decision about the **agent**, not about the
sandbox.

### Neither

With no credential at all, the sandbox still starts and the model calls fail
with a 401 — the `optional: true` degradation of the credential capability.
The sentinel still appears, so the agent always knows which variable to use;
only the real value needs a binding.

## Using another provider

Write a mixin for it — the same three declarations with the vendor's host,
service name and variable name — and compose it. Two rules the runtime
enforces:

- **The network rule and the credential travel together.** Every inject
  domain has to appear *literally* in the allow list for the same phase; the
  workload's bare `*` does not satisfy that check, so the mixin names the
  host outright. The two allow lists union per phase.
- **One credential has one owner.** Two mixins that claim the same service
  name do not compose; give each vendor its own.

★ **The provider configuration is the user's to write.** `openheron init`
maintains **one** provider configuration per configuration root — once the
root holds a usable model it reuses it and ignores `--provider` — so a second
vendor's `llm/*.yaml` is written by hand on the mount, or through the
configuration editor.

## Acceptance

The measurement is a script, not a transcript:

```sh
sandbox-kit/acceptance/run.sh                       # one vendor
sandbox-kit/acceptance/run.sh --build               # and build the Kit first
MIXINS="sandbox-kit/openheron-deepseek sandbox-kit/openheron-openai" \
  sandbox-kit/acceptance/run.sh                     # two vendors
```

It runs the whole thing — preflight, a clean sandbox, the configuration on the
mount, the credential, a real model call, the shape of the stream, the
client's departure, and the port from every vantage point — and exits
non-zero if any check fails.

★ **The transcript is yours to keep, not the tree's.** `--out` writes it
wherever you want it; the numbers that matter are in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) §6.6. The
single-vendor and two-mixin runs were both **21 checks, 21 passed**.

## What has been measured

Three things this Kit depends on were settled in a real sandbox on 2026-11-05
(`sbx` v0.51.0, Linux on WSL2, a Kit built from a clean clone of `develop`):

- **`WORKSPACE_DIR` is injected** into the workload's environment, and it is
  the host's workspace path — the same path the workspace is mounted at. The
  hook and the entrypoint can both rely on it.
- **The port forwarding does not buffer.** The SSE stream arrived as ~1000
  frames over ten seconds, the first at a few milliseconds — the model
  streams to the browser as it happens.
- ★ **The Kit has to declare the port it serves on.** It did not, and that was
  the whole of the "the port forwarding is unreliable" symptom: with no
  `port@1` the runtime had no service to forward to, and the client saw a
  connection accepted and then closed. The forwarding layer was never the
  problem.

★ Everything else that was measured — both credential routes, the two-vendor
composition, the ownership of the written files — is in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) §6, and the
operational findings that are none of the Kit's business are in §6.2.

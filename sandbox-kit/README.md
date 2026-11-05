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

## Files

| File | What it is |
|---|---|
| `openheron/openheron.yaml` | the workload descriptor: the launch contract, the egress posture, the startup hook |
| `openheron/openheron.dockerfile` | the content recipe: the base image and the binary |
| `openheron/entrypoint.sh` | the launch script: enter the workspace, seed the config, exec the server |
| `openheron-deepseek/openheron-deepseek.yaml` | the DeepSeek mixin: declaration only, no content, no Dockerfile |
| `openheron-openai/openheron-openai.yaml` | the OpenAI mixin: the same, for another vendor |

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

One vendor:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron --kit ./sandbox-kit/openheron-deepseek \
    --name openheron .
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
    --name openheron .
```

Without any mixin — the key goes into the sandbox:

```sh
sbx run ./sandbox-kit/openheron --name openheron \
    --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" .
```

Then publish the server's port and open it:

```sh
sbx ports openheron --publish 8080:8080
```

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

## What has been measured

Two things this Kit depends on were settled in a real sandbox on 2026-11-04
(`sbx` v0.51.0, Linux on WSL2):

- **`WORKSPACE_DIR` is injected** into the workload's environment, and it is
  the host's workspace path — the same path the workspace is mounted at. The
  hook and the entrypoint can both rely on it.
- **The port forwarding does not buffer.** The SSE stream arrived as 1339
  frames over 10.3 seconds, the first at +21 ms — the model streams to the
  browser as it happens.

★ Everything else that was measured — both credential routes, the two-vendor
composition, the ownership of the written files — is in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) §6, and the
operational findings that are none of the Kit's business are in §6.2.

# openheron — Docker Sandbox Kits

The openheron agent server, packaged as Docker Sandboxes **v3 Kits**.

Two of them, and the split is the point:

| Kit | Kind | What it carries |
|---|---|---|
| [`openheron/`](openheron/) | `workload` | the server itself: the binary, the startup hook, and the egress posture ("no restriction from this Kit") |
| [`openheron-credential/`](openheron-credential/) | `mixin` | the model credential and the network rule that goes with it — **vendor-specific**, and **optional** |

★ The workload is vendor-neutral: it declares no credential at all, because
which provider, which endpoint and which variable name are the user's
business. Compose the credential mixin when the key should stay on the host;
leave it out when the user injects the key themselves.

The design behind it — and every assumption it rests on — is
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md).

## Files

| File | What it is |
|---|---|
| `openheron/openheron.yaml` | the workload descriptor: the launch contract, the egress posture, the startup hook |
| `openheron/openheron.dockerfile` | the content recipe: the base image and the binary |
| `openheron/entrypoint.sh` | the launch script: enter the workspace, seed the config, exec the server |
| `openheron-credential/openheron-credential.yaml` | the credential mixin: declaration only, no content, no Dockerfile |

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
make kit-mixin   # the credential mixin: openheron-kit-credential:0.0.6
```

Both use a container-driver buildx builder, and both export into the local
docker store — the default builder drops the manifest annotations that make
an image a Kit, and without the export there is no local image to validate.

```sh
make kit-check   # kit-tck validate against the specification
```

## Run

With the credential mixin — the key stays on the host:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron --kit ./sandbox-kit/openheron-credential \
    --name openheron .
```

Without it — the key goes into the sandbox:

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

The credential mixin declares the credential `proxyManaged`, so the container
receives a **sentinel** and the host-side proxy swaps in the real value on
the way out. Two ways to give the sandbox a working key:

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

## Changing the provider

The mixin names the vendor: `DEEPSEEK_API_KEY` and `api.deepseek.com`, which
are openheron's factory defaults
(`initcfg/templates/llm/deepseek.yaml`). They have to move together with the
provider config the startup hook writes on the mount — either edit the mixin
and the config, or write a mixin for the provider you use.

## What is not verified yet

The design lists five things that need a real sandbox to settle. Two of them
shape this Kit:

- **Does the workload's environment carry `WORKSPACE_DIR`?** The Kit's hook
  reads it, and the entrypoint falls back to it. If the runtime does not
  export it to the workload, the entrypoint needs the path from the hook
  instead, and the hooks' position relative to the entrypoint has to hold.
- **Does the port forwarding buffer the SSE stream?** If it does, the model
  stops streaming and the whole interaction changes character.

Both are in `docs/sandbox-kit-design.md` §4 and §5.

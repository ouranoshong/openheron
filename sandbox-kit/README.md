# openheron — Docker Sandbox Kit

The openheron agent server packaged as a **Docker Sandboxes v3 workload Kit**.

The whole openheron process runs inside a microVM: the HTTP/SSE server, the
file tools and the shell tool share one backend inside the boundary. The host
keeps one forwarded port.

The design behind it — and every assumption it rests on — is
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md).

## Files

| File | What it is |
|---|---|
| `openheron.yaml` | the descriptor: what the Kit needs from the host |
| `openheron.dockerfile` | the content recipe: the base image and the binary |
| `entrypoint.sh` | the launch script: enter the workspace, seed the config, exec the server |

The YAML and the Dockerfile share a filename stem because the Kit specification
finds the companion recipe by that convention.

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
docker buildx create --name kit-builder --driver docker-container --bootstrap
docker buildx build --builder kit-builder . -f openheron.yaml -t openheron-kit:0.0.6
```

or, from the repository root, `make kit`.

## Check

```sh
kit-tck validate openheron-kit:0.0.6
```

A build is not proof that the Kit works. The checks that matter are the
behavioural ones in `docs/sandbox-kit-design.md` §5 — in particular the SSE
test, which no offline check can stand in for.

## Run

```sh
# from the project directory you want the agent to work in
sbx run ./openheron.yaml --name openheron .

# publish the server's port, then open it
sbx ports openheron --publish 8080:8080
```

The configuration lands in `<workspace>/.openheron` — on your mount, not inside
the sandbox — so you can edit `llm/deepseek.yaml`, `agents/main.md` and the
policy files with your own editor, and openheron picks the change up.

## Credentials

Two ways to give the sandbox a working model key. They differ in exactly one
thing: what the container ends up holding.

### The placeholder — the key stays on the host

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ The placeholder is `proxy-managed` on purpose: that is the sentinel the
Kit's `credential@1` puts into the container, so the runtime's injection and
the host's binding agree without the user passing an environment variable at
all. The container ends up holding `DEEPSEEK_API_KEY=proxy-managed`, and the
host-side proxy swaps in the real value on the way out.

★ `--value-stdin` keeps the key out of the process arguments and out of the
shell history. `sbx secret set deepseek` also works, but then a **binding**
has to be written into `~/.config/sbx/credentials.yaml` as well — two steps
instead of one.

### The key itself — the agent can read it

```sh
sbx run --detach --name openheron --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
    ./openheron.yaml .
```

A runtime variable carries whatever it is given, so the real key ends up
inside the VM — where the agent reads it out of the environment, and where
anything it runs can send it anywhere. The model call still works: the proxy
forwards the bearer token unchanged, because it is not the placeholder. The
Kit's `credential@1` entry does not get in the way — the injected value wins
over the sentinel it would have installed.

★ So the two are not two spellings of the same thing. The first keeps the
secret on the host and leaves the agent unable to read it; the second hands
the agent the secret and asks it not to leak it. Choosing the second is
choosing to trust the agent — a decision about the **agent**, not about the
sandbox.

### Neither

With no binding at all, the sandbox still starts and the model calls fail
with a 401 — which is the `optional: true` degradation of the credential
capability.

## Network

★ **The Kit does not restrict what the agent may reach.** The runtime phase
is granted `*`, because the boundary here is the microVM, not an allow list.
An agent that cannot install a dependency or reach its own MCP server cannot
do the job it was given — and a second wall inside the sandbox buys friction
rather than safety.

What the agent *actually* reaches is decided above the Kit:

- the **host's policy** — `sbx policy`, and the preset you chose at sign-in
  (Open, Balanced, Locked Down);
- your **organization's rules**, where they apply — there the Kit's and the
  local allow rules are inactive, and only the organization's rules grant;
- the **sandbox's own posture** — the private ranges, the loopback and the
  link-local addresses are unreachable, and non-HTTP protocols are refused at
  the network layer.

★ Note that "saying nothing" is not "saying open": the runtime is
deny-by-default, and a phase with no policy block grants nothing at all. The
`*` is what expresses the choice.

The one place the Kit names a host is the credential's **inject domain** —
where the secret belongs, not a limit on the agent's reach. See below.

## The provider is openheron's own

The Kit declares **no provider arguments**. Which provider, which endpoint and
what the api key environment variable is called are openheron's own rules,
decided by the factory templates — and the configuration on your mount is yours
to edit.

What the descriptor mirrors is those defaults, in three literals:

| Literal | Comes from |
|---|---|
| `api.deepseek.com` | `initcfg/templates/llm/deepseek.yaml` — the `base_url` |
| `DEEPSEEK_API_KEY` | `initcfg/initcfg.go:361-363` — `defaultEnvName` |
| `deepseek` | `initcfg/initcfg.go:365` — `defaultProvider` |

★ Those three have to be changed together with the factory defaults. A Kit
that re-exposed them as arguments would be a second place to keep them in
sync, and nothing in the Kit needs to vary per user: the reach is open, and
the configuration is the user's.

To use a different provider, edit the three literals in `openheron.yaml` and
rebuild — and edit the provider file in `llm/` on your mount to match. Or run
the plain binary, where the host's network policy is yours to set.

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

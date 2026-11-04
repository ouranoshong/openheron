# Openheron as a Docker Sandbox Kit — Design

> **Status: a proposal. Nothing in §2 or later exists in the tree.**
> §1 describes the code as it actually is; §2 onward describes what *would* be built.
> The decision this document records: openheron's **whole process** runs inside a
> microVM, and the host keeps nothing but a forwarded HTTP/SSE port.
>
> The reasons are in §2.1 and §3; the short version is that this is the only
> isolation level that also covers the **read path**, and it costs openheron
> **no code change at all** — the boundary moves outward instead of inward.

---

## 1. The code as it is

### 1.1 openheron is one process with three surfaces

openheron ships as a single static binary (`Makefile:96`, `Makefile:108`). At
runtime it is **one process** that opens three surfaces to the outside:

- **An HTTP/SSE server.** `main.go:265` builds it:
  `api.New(rt, fm, hserver.WithHostPorts(st.addr))`. The listen address comes
  from `main.go:217`: `override("LISTEN_ADDR", cfg.Server.ListenAddr)`.
  `POST /v2/run` (`api/v2_files.go:58`) is the turn endpoint; it answers with an
  SSE stream (`api/v2_run.go:146`, `stream := s.newStream(c)`) that flushes as it
  goes (`api/v2_run.go:155`, `api/v2_run.go:176`; `transport/sse.go:91`).
- **The file tools and the shell tool.** `agentkit/build/snapshot.go:56` builds
  one backend, `fsx.New(fsx.Config{Root: s.projectRoot, ExtraRoots: roots})`, and
  `agentkit/build/snapshot.go:60-61` hands the same value to two roles:
  `out.backend = local` (the file tools) and `out.streamingShell = local` (the
  `execute` tool). The shell side runs
  `exec.CommandContext(ctx, "/bin/sh", "-c", command)` with `cmd.Dir = s.root`
  (`agentkit/fsx/fsx.go:747`, `agentkit/fsx/fsx.go:750`).
- **The model API.** `llm/common/model.go:66-72` hands the provider config to
  `acl.NewClient`.

The web UI is **inside the binary**: `main.go:35` embeds `all:web/dist`, and
`main.go:337` mounts it at `/`. `main.go:333` is the warning you get when the
frontend was not built (`make web`, `Makefile:29-31`).

★ Consequence: openheron is **not** a CLI coding agent. It has no TUI, no
per-command approval prompt in the terminal, no session picker. It is a server
that a browser talks to.

### 1.2 The process is the isolation unit today

Everything above runs in one process, as one user, on one kernel:

- the same user as whoever started the server;
- the same kernel, the same filesystem, the same network namespace;
- the same process as the one holding the model credentials.

`agentkit/spec/defaults.go:11` puts `execute` in the `ask` tier:

```go
Ask:  []string{"write_file", "edit_file", "execute", "kb_index"},
```

and the factory `main` agent turns it off outright
(`initcfg/templates/agents/main.md:7`):

```
disabled_tools: [write_file, edit_file, execute]
```

★ An approval is a **policy** boundary, not a **technical** one. Once the approval
is granted, the command has the full authority of the openheron process — and so
does `read_file`, which never asks at all. `docs/fs-backend-design.md:166-171`
states the reasoning outright: reads are not confined because `execute` can
already `cat` any file the process can read, so confining `read_file` would buy
no safety.

### 1.3 The two possible seams

There are exactly two places an isolation boundary could go, and this document
picks one:

- **Inward** — keep the server on the host and move only the `execute` tool
  into an isolation unit. That is `docs/microvm-execution-design.md`; it
  replaces the `StreamingShell` value and nothing else.
- **Outward** — move the **whole process** into an isolation unit. Then the
  file tools, the shell tool, the model client and the state store are all
  inside, and the host keeps only a forwarded port.

★ The outward seam is the one this document takes, because it is the only one
that closes the read path. `read_file` is served by the same `fsx.Local`
(`agentkit/build/snapshot.go:60`); if the process is inside the VM, the read is
inside the VM too.

### 1.4 The credential model as it is

openheron already refuses a literal key. `agentkit/spec/write.go:493` is the
error a writer sees:

```
api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path so the secret stays out of the repository
```

`agentkit/spec/load.go:34-45` resolves the reference at load time:

```go
rawKey := p.APIKey
key, resolved, err := expandValue(rawKey)
...
if !resolved {
	p.APIKey = ""
	c.deferKey("llm/%s.yaml: api_key env %s is not set; calls to this endpoint will fail with 401 Unauthorized",
		id, envVarName(rawKey))
} else {
	p.APIKey = key
}
```

`agentkit/spec/expand.go:13` is the `@file:` branch; `agentkit/spec/expand.go:40`
is `envVarName`.

★ Two facts follow, and §2.8 depends on both:

1. The key is a **reference** in the config, and the resolved value lives in the
   **process memory** of whatever runs the provider.
2. **An unresolved reference is not fatal.** `p.APIKey = ""` and the config still
   loads; the failure surfaces later, as a 401.

`llm/common/model.go:56-58` only requires a base URL —

```go
if c.BaseURL == "" {
	return nil, ErrNoBaseURL
}
```

— and hands `APIKey` through to `acl.NewClient` (`llm/common/model.go:66-72`)
without requiring it to be non-empty. `llm/common/logging.go:157` redacts
`apikey` and `authorization` headers from the logs.

### 1.5 The paths that would move into the VM

The config root has exactly three levels (`AGENTS.md`): `--config-dir` →
`$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`. `main.go:184` builds the
service from the two flags parsed at `main.go:80-95`.

Inside the process:

- the **project root** — `main.go:219`, `override("PROJECT_ROOT", cfgSvc.ProjectRoot())`;
  it is the root every path in a shell command is relative to
  (`agentkit/fsx/fsx.go:750`) and the root the file tools are confined to
  (`agentkit/fsx/fsx.go:194`, `agentkit/fsx/fsx.go:219`);
- the **data dir** — `main.go:218`, `override("DATA_DIR", cfgDataDir)`; sessions
  live at `main.go:224` (`sessionsRoot := filepath.Join(st.dataDir, "sessions")`)
  and checkpoints next to them (`main.go:229-232`);
- the **skills dir**, added as a second root at `agentkit/build/snapshot.go:52-54`.

### 1.6 The release model constrains the integration

`Makefile:96` names the six shipping targets:

```
linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
```

and `Makefile:108` builds them with `CGO_ENABLED=0`. The release artifacts are
static, dependency-free binaries produced by a cross-compile on Linux — no C
toolchain is available on the runner for `darwin/*` or `windows/*`.

★ Any integration that needs cgo breaks the release model. A Kit does not: a
Kit is an image, and openheron goes into it as the same static binary.

### 1.7 The startup chain, and where each root comes from

openheron resolves three roots, at three different moments. The sandbox
touches all three, so it is worth stating where each one comes from:

| Root | Resolved by | Line |
|---|---|---|
| project root | `--project-root`, else the **working directory** | `agentkit/spec/config.go:157-164` |
| config root | `--config-dir`, else `$OPENHERON_CONFIG_DIR`, else `<projectRoot>/.openheron` | `agentkit/spec/config.go:170-177` |
| data dir | `$DATA_DIR`, else `<configRoot>/state` | `main.go:218`, `agentkit/spec/config.go:224-232` |

★ **The working directory is the default project root, and the config root
follows it.** A process started in a directory *is* a process rooted there:
`cd` into the workspace, and the configuration lands in
`<workspace>/.openheron`.

★ **`$PROJECT_ROOT` does not move the config root.** `main.go:184` builds the
service from the two **flags** — `spec.NewService(flags.configDir, flags.projectRoot)` —
and only afterwards does `main.go:219` apply the `PROJECT_ROOT` override, which
reaches the agent's workdir and the filesystem backend's root
(`agentkit/build/snapshot.go:56`) but not the config root. A container that
sets `$PROJECT_ROOT` alone ends up with two different roots: the agent works in
one directory, the configuration is read from another.

★ `openheron init` is non-interactive and idempotent, which is what makes it
usable as a startup step:

- every input is a flag (`initcmd.go:16-42`): `--dir`, `--config-dir`,
  `--model`, `--provider`, `--base-url`, `--env`, `--no-policy`, `--tools`,
  `--force`, `--dry-run`, `--json`;
- it writes only what is missing and reports the rest as kept
  (`initcmd.go:120`, `initcmd.go:147-148`);
- it writes to `<dir>/.openheron` and **never reads `$OPENHERON_CONFIG_DIR`**
  (`initcmd.go:57-65`).

★ That last point is a trap for the sandbox: `init` and the server must agree
on the config root, and the only way they agree by default is by sharing the
**directory** — `init --dir <workspace>` puts the configuration at
`<workspace>/.openheron`, which is exactly where the server looks when its
working directory is that workspace.

---

## 2. What would be built

### 2.1 Goal

Run the **whole openheron process** inside a microVM, with the project
directory bind-mounted, so that a turn that misbehaves — a malicious
instruction pulled from a web page, a dependency install script, an agent that
has been talked into "clean up the workspace" — damages the project directory
and nothing else.

The host keeps exactly one thing: a forwarded port for the HTTP/SSE server.

Non-goals: replacing the approval flow, changing the agent model, changing
the API, or making openheron faster.

★ The difference from `docs/microvm-execution-design.md` is the whole point.
That document keeps the server on the host and moves only `execute` into the
VM, which hardens the command path and leaves the read path open (§2.7 there).
Here the read path is inside too, because the process that serves it is inside.

### 2.2 The shape: a v3 workload Kit

Docker's Kit specification (v3) is the packaging format. The facts that matter,
taken from the specification itself:

- A Kit is **one OCI image**. Its manifest annotation
  `vnd.docker.sandbox.kit.descriptor` carries the descriptor; its layers carry
  the content. There is no kit-specific media type: an engine that does not
  read the annotation runs it as an ordinary image.
- There are exactly two published kinds. A **`workload`** supplies a root
  filesystem and the launch config (entrypoint, cmd, env, user, workdir); a
  **`mixin`** supplies an overlay. Exactly one workload per composition.
- The descriptor is authored in YAML; the content recipe is ordinary
  Dockerfile text. The default authoring form is the **companion pair**:
  `<stem>.yaml` beside `<stem>.dockerfile`, found by the filename-stem
  convention.
- Decoding is **strict**: any unrecognized field anywhere is an error, because
  a misspelled key silently ignored would be a policy silently absent.
- A published Kit **stages its own sources** at
  `/usr/share/sandbox/kit/<stem>/kit.yaml` and
  `/usr/share/sandbox/kit/<stem>/kit.dockerfile`.
- The runtime guarantees a platform floor: `bash`, `sh`, `curl`, `git`, a
  populated CA store, and a non-root default user named `agent`, uid `1000`,
  home `/home/agent`.

openheron is a **workload**, not a mixin: its layers are the root filesystem of
the machine the agent works in.

★ A workload's image config carries the launch contract, and the specification
is explicit that the descriptor duplicates none of it. That is what keeps §2.5
short.

### 2.3 The descriptor

The real descriptor is **`sandbox-kit/openheron.yaml`**, beside
`sandbox-kit/openheron.dockerfile` and `sandbox-kit/entrypoint.sh`. What
follows is the part of it the decisions below are about:

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: workload
provides: ["openheron@${{ kit.args.version }}"]
version: "${{ kit.args.version }}"

args:
  version:
    default: "0.0.6"
    pattern: '^[0-9]+(\.[0-9A-Za-z-]+)*$'
    buildArg: OPENHERON_VERSION

capabilities:
  - type: com.docker.sandbox/sbx@1
  - type: com.docker.sandbox/long-running@1
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - "*"
  - type: com.docker.sandbox/credential@1
    optional: true
    config:
      service: deepseek
      phase: runtime
      apiKey:
        name: DEEPSEEK_API_KEY
        proxyManaged: true
        inject:
          - {domain: api.deepseek.com, header: Authorization, format: "Bearer %s"}
  - type: com.docker.sandbox/lifecycle@1
    config:
      startup:
        - command: 'openheron init --dir "$WORKSPACE_DIR" || echo "[kit] init did not complete; the existing configuration is kept"'
          env: [WORKSPACE_DIR]
```

Points that are not decoration:

- **`com.docker.sandbox/sbx@1`** declares that the workload is prepared for the
  host to launch the agent's way, and it is the capability that carries the
  launch contract — the shells and the user account the base image has to
  provide.
- **`com.docker.sandbox/long-running@1`** keeps the workload running
  independently of attached sessions. openheron is a server, not a session: it
  has to come up, serve, and stay up.
- **`com.docker.sandbox/network-policy@1` with `allow: ["*"]`** says that the
  Kit itself places **no restriction** on what the agent may reach (§2.7).
  ★ The "everything" pattern is the portable form of that: an absent phase
  block grants nothing at all, so saying nothing is the opposite of saying
  "open".
- **`com.docker.sandbox/credential@1` with `proxyManaged: true`** is the
  mechanism the whole credential story rests on. The host keeps the real value
  in its secret store; the container only ever sees the sentinel; the proxy
  substitutes the real value on the way out to the named domain. openheron
  already speaks that protocol (§2.8).
- **`com.docker.sandbox/lifecycle@1`** is where the initialization lives (§2.5):
  a `startup` hook, so it runs on every boot as uid `1000` and may rely on
  `openheron init` being idempotent. The hook environment is deny by
  default, so the name it reads is declared.
- **The version argument is the only argument.** The provider, the model API
  host and the name of the api key environment variable are openheron's own
  rules (§2.7), and the descriptor mirrors them in three literals that have
  to move together with the factory defaults:
  `api.deepseek.com` (`initcfg/templates/llm/deepseek.yaml`),
  `DEEPSEEK_API_KEY` (`initcfg/initcfg.go:361-363`),
  `deepseek` (`initcfg/initcfg.go:365`).
- **No `agent-context@2`.** openheron's own instructions live in the config
  root (`agents/main.md`, `agents/task.md`), so the runtime has nothing to hand
  over — and a Kit that declares it has to survive a runtime that does not
  implement it.

★ The credential's `optional: true` is a statement about **degradation**: if the
host cannot provide it, the sandbox still starts and the model calls fail with
401 — which is exactly what `agentkit/spec/load.go:39-42` already tolerates.
How the user binds the key, and what the container ends up holding, is §2.8.

### 2.4 The Dockerfile

The recipe's job is small: provide the platform floor, put openheron in, and
say how to start it. The real file is **`sandbox-kit/openheron.dockerfile`**:

```dockerfile
# syntax=docker/dockerfile:1
#
# The content recipe for the openheron workload Kit.
#
# ★ The base must provide the platform floor: the `agent` user at uid 1000,
#   /home/agent, bash, git and a CA store. A workload that ignores this
#   "builds fine and fails at agent launch".

FROM docker/sandbox-templates:shell

ARG OPENHERON_VERSION
ARG TARGETARCH

USER root
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# ★ The release asset is the same static, CGO_ENABLED=0 binary the six release
#   targets ship (Makefile:96 and Makefile:108). The Kit adds no second build.
RUN curl -fsSL "https://github.com/ouranoshong/openheron/releases/download/v${OPENHERON_VERSION}/openheron_v${OPENHERON_VERSION}_linux_${TARGETARCH}.tar.gz" \
    -o /tmp/openheron.tar.gz \
    && mkdir -p /tmp/openheron \
    && tar -xzf /tmp/openheron.tar.gz -C /tmp/openheron --strip-components=1 \
    && mv /tmp/openheron/openheron /usr/local/bin/openheron \
    && rm -rf /tmp/openheron /tmp/openheron.tar.gz

# ★ The entrypoint is copied as root: the agent user could not chmod a
#   root-owned file.
COPY entrypoint.sh /usr/local/bin/openheron-entrypoint
RUN chmod 0755 /usr/local/bin/openheron-entrypoint

USER agent
WORKDIR /home/agent/workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/openheron-entrypoint"]
```

and the entrypoint it copies is **`sandbox-kit/entrypoint.sh`**:

```sh
#!/bin/sh
# ★ The workspace path is dynamic; WORKSPACE_DIR is how the runtime names it.
#   The image's WORKDIR is a fixed path, and starting there would put the
#   configuration inside the VM instead of on the user's mount.
cd "${WORKSPACE_DIR:-$PWD}" || exit 1

# ★ init is idempotent: it writes what is missing and keeps what is there.
#   The startup hook already did this; repeating it costs one no-op and makes
#   the result independent of where the hooks sit relative to the entrypoint.
#
# ★ No --provider and no --env: openheron init decides both from its own
#   rules, and the configuration on the mount is the user's to edit.
openheron init --dir "$PWD" || \
    echo "[entrypoint] init did not complete; the existing configuration is kept"

exec openheron
```

Points:

- The base **must** be a platform-floor image: the `agent` user at uid `1000`,
  `/home/agent`, `bash`, `git`, a CA store. The specification says a workload
  that ignores this "builds fine and fails at agent launch".
- The binary is the release artifact from `Makefile:96` — the same static,
  `CGO_ENABLED=0` build (`Makefile:108`) the six targets ship. The Kit does
  not introduce a second build.
- `TARGETARCH` selects `amd64` or `arm64`, matching the two Linux targets.
- The CA store matters: the model client talks TLS, and
  `llm/common/error.go:133` tells the user to check that "network and TLS are
  healthy".
- **`cd` first, then everything else follows**: the working directory is the
  project root (§1.7), and the config root is `<cwd>/.openheron`.
- **The entrypoint repeats the initialization on purpose.** `openheron init`
  is idempotent, so running it in both the startup hook and the entrypoint
  costs one extra no-op and removes the dependency on the hooks' position
  relative to the entrypoint (§2.5).
- **`EXPOSE 8080`** declares the port; publishing it is the runtime's job
  (`sbx ports --publish 8080:8080`), and it is not a capability the Kit asks
  for.

### 2.5 The launch contract

The image config carries the launch contract; the descriptor duplicates none
of it. The specification's launch modes:

| Mode | Argv |
|---|---|
| Default (headless / task) | image `Entrypoint` + image `Cmd` |
| Interactive (TTY) | image `Entrypoint` + lifecycle `interactive` |

openheron is **headless**: there is no interactive mode to declare, and the
`interactive` field is left alone.

★ **The workspace is mounted at the same absolute path inside the VM — but the
workload does not start there.** The image config owns `workdir`, and a
workload Kit owns those fields outright. The "the agent starts in the
workspace" behaviour belongs to the **built-in** agents; a Kit workload runs
where its image says it runs.

★ So the Kit has one job the Dockerfile alone cannot do: make the process
**start in the workspace**. Everything else in this section follows from that.

The environment the container gets:

- `LISTEN_ADDR` — consumed at `main.go:217`.
- `OPENHERON_CONFIG_DIR` — the config root, per the three-level rule (§1.7).
  It has to name the workspace, or the default lands in the image's workdir.
- `DATA_DIR` — consumed at `main.go:218`. Point it at a volume when the
  sessions should outlive the sandbox (§2.9).
- `OPENHERON_PORT` — the arg exported by the descriptor (§2.3). openheron does
  not read it; it is exported to the container, and the entrypoint turns it
  into `LISTEN_ADDR`.
- `HTTPS_PROXY` and `HTTP_PROXY` — the runtime's proxy. openheron honors them
  (§2.7), and they are what makes the credential swap work (§2.8).
- `WORKSPACE_DIR` — where the workspace is mounted. The specification's own
  `lifecycle@1` example passes it to a hook by that name; whether the
  **workload** sees it in its environment is the open question below.

★ `main.go:459-464` (`override`) is what makes the first three possible:
`LISTEN_ADDR`, `DATA_DIR` and `PROJECT_ROOT` are read from the environment
**before** the config file, so the container can be shaped without editing a
config file.

★ **`$PROJECT_ROOT` is deliberately absent from that list.** It does not move
the config root (§1.7), so setting it would give the container two roots
instead of one.

#### The first run: initialization

A fresh workspace has no configuration, and the server refuses to start
without one (`main.go:135-143`). So the Kit has to seed the configuration
before the server comes up — and it has to do it in the **workspace**, as
uid `1000`, because the workspace belongs to the user.

`openheron init` is non-interactive and idempotent (§1.7), so it fits. It needs
to know one thing: where the workspace is. Which provider and which env var
name to write are openheron's own rules (§2.7), so the hook asks for neither.

★ **`lifecycle@1` is where the workspace path comes from.** The capability's
hooks run with an environment that is **deny by default** — a hook sees only
the names it declares — and the specification's own example passes
`WORKSPACE_DIR` to a hook by that name. The rules that matter here:

- `install` hooks run **once per sandbox** and default to **root**;
- `startup` hooks run **on every boot** and default to the **agent user
  (uid 1000)**;
- hook authors must make `startup` hooks idempotent — every boot means every
  boot, which is exactly what `openheron init` is.

★ So the initialization belongs in a **`startup` hook**: uid `1000` by default,
every boot, idempotent by construction. An `install` hook is the wrong phase
twice over — it runs once (a recreated sandbox would find nothing to seed) and
it runs as root (the files would land on the user's mount owned by root, and
the agent could not write to them).

```yaml
  - type: com.docker.sandbox/lifecycle@1
    config:
      startup:
        - command: 'openheron init --dir "$WORKSPACE_DIR" || echo "[kit] init did not complete; the existing configuration is kept"'
          env: [WORKSPACE_DIR]
          description: Seed the project configuration in the workspace
```

★ `|| echo ...` rather than `&&`: `init` fails when an existing provider file
declares a different model (`initcfg/initcfg.go:94-99`), and that is a
configuration to keep, not a reason to refuse to serve it.

★ **The open question is how the entrypoint learns the workspace path.** The
hook sees `WORKSPACE_DIR`; the workload's own environment may or may not
carry it, and the specification does not say. Two shapes follow:

- **The workload's environment carries `WORKSPACE_DIR`** — then the entrypoint
  alone does everything: it seeds the configuration, `cd`s into the workspace,
  and exports `OPENHERON_CONFIG_DIR`. No hook, no ordering question.
- **It does not** — then a hook has to hand the path to the entrypoint (a file
  it writes), and the hooks' position relative to the entrypoint has to hold.
  The specification requires the **install** hooks to finish before the
  entrypoint first runs and describes the **startup** hooks as part of every
  boot, but it does not state the startup hooks' position relative to the
  entrypoint.

★ Either way the entrypoint stays a two-line script, and the difference
between the two shapes is one line in it. §4's first question is which shape
to build against; §5's second question is how to observe the answer.

### 2.6 Path semantics inside the VM

The workspace is mounted at the **same absolute path** it has on the host, and
the Kit makes the process start **in** it (§2.5). Then:

- `cmd.Dir = s.root` (`agentkit/fsx/fsx.go:750`) keeps its meaning: the guest's
  working directory is the same directory, spelled the same way;
- a relative path in a shell command still means the project root;
- the config root is `<workspace>/.openheron` — the same place the user edits
  on the host, and the same place `openheron init` writes (§1.7);
- error messages that quote a path still match the paths the user sees.

★ If the guest path differs from the host path, every path the model has been
told about (`docs/user-manual.md`, the agent prompts) becomes wrong in the one
place where being wrong is expensive. ★ And the configuration would stop being
user-managed: a config root outside the mount is a config root that vanishes
with the sandbox.

★ **This is the step that has no fallback.** The path is dynamic — it is
whatever the user's directory happens to be — so it cannot be baked into the
image, and the image's `workdir` is a fixed path that would put the
configuration inside the VM. The runtime has to tell the process where the
workspace is, and the mechanism it documents is `WORKSPACE_DIR` (§2.5).

### 2.7 The network surface

★ **The rule this Kit follows: the boundary is the isolation, not the allow
list.** The microVM already separates the agent from the host. A second wall
inside the sandbox mostly buys friction, not safety — an agent that cannot
install a dependency or reach its own MCP server cannot do the job it was
given. So the Kit's `network-policy@1` grants the runtime phase `*`: the Kit
imposes no egress restriction of its own.

★ **"Saying nothing" is not the same as saying "open."** The runtime is
deny-by-default, and "an absent phase block grants nothing for that phase" —
so a Kit that omitted the capability would grant the agent **no** egress at
all. The `*` is what expresses the choice.

What still decides what the agent reaches:

- the **host's policy** — `sbx policy`, and the preset chosen at sign-in
  (Open, Balanced, Locked Down). That is the user's lever, and it applies on
  top of whatever the Kit asks for.
- the **organization's rules**, where they apply — and there the Kit's and the
  local allow rules are inactive: only the organization's rules grant, while
  denies still apply.
- the **sandbox's own network posture** — the private ranges, the loopback and
  the link-local addresses are unreachable, and non-HTTP protocols are refused
  at the network layer. That is the runtime's, not the Kit's.

★ **The one thing the Kit does name is where the secret goes.** The
credential's inject domain is the model API host — that is the host's proxy
swapping the real key into the request, not a limit on the agent's reach. A
configuration the user edits to point elsewhere reaches that host fine; it
just does not carry the model key there, so the call comes back 401 until the
inject domain is changed to match.

openheron's own outbound needs are unchanged by this:

- the **model API host** — from the provider config's `base_url`. The factory
  templates make it a template variable too: `initcfg/templates/llm/deepseek.yaml`
  renders `base_url: https://api.deepseek.com` and
  `initcfg/templates/llm/openai.yaml` renders `https://api.openai.com/v1`,
  and `openheron init` asks for it (`initcfg/initcfg.go:100-106`). The `inject`
  rule matches by **domain**, so the path in `base_url` does not matter (§2.8);
- whatever the **tools** reach — the HTTP tool
  (`agentkit/spec/defaults.go:16-27` registers `http_get` / `http_post` /
  …), MCP servers, and package registries the agent installs from. ★ With
  `allow: ["*"]` these ride the same grant as everything else; they were the
  reason a short allow list would have been the wrong shape.

★ **The host's policy log is the diagnostic**, not the Kit. It shows what was
refused, and under a host policy narrower than `*` that log is where the
missing hosts show up.

★ **The proxy is the whole model.** Every packet leaves through a host-side
proxy that enforces the policy and injects the credentials — so "the network
policy" and "the proxy" are one thing, not two. openheron's client goes
through it: `llm/common/model.go:60-63` builds a plain `http.Client` with no
transport of its own, `llm/common/logging.go:36-38` falls back to
`http.DefaultTransport`, and `llm/common/logging.go:77` delegates the round
trip to it — and `http.DefaultTransport` reads `HTTPS_PROXY` and `HTTP_PROXY`
from the environment.

★ That is what makes the credential swap work (§2.8), and it is also the
failure mode to watch: a client that ignores the proxy sends the **sentinel**
straight to the API, and the API rejects it with a 401 that reads like a bad
key.

### 2.8 The credential path — two ways to run it

★ The mechanism a Kit uses is `credential@1` with `apiKey.proxyManaged: true`,
and the specification is precise about what it does:

- `apiKey.name` is the **in-container env var name**;
- with `proxyManaged: true` the real value **stays on the host**, and the
  container sees a **sentinel** — `proxy-managed`, a non-empty placeholder,
  not the secret;
- the boundary (the host-side proxy) substitutes the real credential on
  outbound requests that match the `inject` rules;
- the host's secret store is the only source — the specification puts it
  as "a Kit declares the need, the user's bindings answer it", and "host
  environment variables never auto-inject; a Kit cannot name where a secret
  lives, only what it needs."

That is exactly the shape openheron already has:

1. `agentkit/spec/load.go:34-45` resolves `api_key` through
   `agentkit/spec/expand.go:24-33`, which looks the `${...}` name up in the
   process environment, and
2. `llm/common/model.go:66-72` sends it as the bearer token.

★ **Two ways to run it.** They differ in exactly one thing — what the
container ends up holding — and that one thing decides whether the agent can
read the key. Both were measured (§6).

| | the container holds | does the real key enter the VM |
|---|---|---|
| **the placeholder** | `proxy-managed` | no |
| **the key itself** | `sk-…` | **yes** |

#### 2.8.1 The placeholder the runtime injects

The runtime puts the sentinel into the container; the host holds the real
value, and the two have to agree on the placeholder. They agree after **one
command**:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ `--placeholder proxy-managed` is the whole trick: it makes the host's
placeholder **the same string** the runtime injects, so the two agree without
the user passing an environment variable at all (§6.4). The container ends up
holding `DEEPSEEK_API_KEY=proxy-managed`, and the host-side proxy swaps in
the real value on the way out. `--value-stdin` keeps the key out of the
process arguments and out of the shell history.

★ The alternative to the custom secret is a **service binding**, and it takes
two steps instead of one: the stored secret is only the *value*, while the
*authorization* lives in a second file.

```sh
sbx secret set deepseek      # the value
```

```yaml
# ~/.config/sbx/credentials.yaml — the authorization
bindings:
  deepseek:
    apiKey:
      domains:
        - api.deepseek.com
```

Without the binding the sandbox still starts and every model call comes back
401 — the `optional: true` degradation of §2.3, arriving earlier than
expected.

#### 2.8.2 The key itself, injected as a plain variable

The other way skips the credential machinery: let the runtime carry the key.

```sh
sbx run --detach --name openheron --env DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" \
    ./openheron.yaml .
```

A runtime variable carries whatever it is given, so this puts the **real key
inside the VM** — where the agent reads it out of the environment, and where
anything it runs can send it anywhere. The model call then works without the
proxy substituting anything: the container already holds the right bearer
token, and the proxy forwards it unchanged because it is not the placeholder.

★ The Kit's `credential@1` entry does not get in the way: the injected value
wins over the sentinel the capability would have installed, so the same Kit
runs both ways.

#### 2.8.3 What the two have in common

- The Kit's `credential@1` entry stays in the descriptor either way, so the
  credential's purpose remains in the permission surface (§2.14).
- The **inject domain is not a limit on the agent**: it says where the
  **secret** belongs. A configuration pointing at another host reaches that
  host fine; it just does not carry the model key there, so that call comes
  back 401 unless the inject domain is changed to match (§2.3).
- One credential per sandbox: a credential has one owner per (service,
  phase), so two vendors cannot share a service name. A second vendor needs
  a second service name, and therefore a second Kit or a mixin.

### 2.9 State across sandbox teardown

The sessions and the checkpoints are files under the data dir (`main.go:224`,
`main.go:229-232`), and the data dir's default is `<configRoot>/state`
(`agentkit/spec/config.go:224-232`). Since the config root is
`<workspace>/.openheron` (§1.7), the default data dir is
**`<workspace>/.openheron/state`** — on the mount.

★ That is worth stating plainly: **the default already persists.** The mount
belongs to the user and outlives the sandbox, so the conversations survive a
sandbox that is stopped, recreated, or removed. The specification's
`volume@1` capability is not needed for that.

Two places to put the state, and they behave differently:

| Where the data dir lives | The sessions survive |
|---|---|
| on the mount (the default) | a stop, a recreate, **and** a removal |
| on a `volume@1` instance | a stop and a recreate, but not a removal |

★ The trade is not "persist or not" — it is **where the state lives**. On the
mount, the state sits in the user's own directory, next to their code, where
they can read it, back it up, or delete it with the rest. On a volume it is
out of the way, and out of the user's reach.

★ `DATA_DIR` (`main.go:218`) is the switch: an absolute path wins over the
default (`agentkit/spec/config.go:229-232`), so the Kit can point the data dir
at a volume without touching the configuration.

★ One consequence of the default: the state lands in the user's own tree, in
`<workspace>/.openheron`. That directory has no business being in version
control.

### 2.10 The SSE path — the risk

This is where the whole design is most likely to break, and it is worth stating
plainly rather than burying.

The turn stream is an SSE response. `api/v2_run.go:146` creates the stream,
`api/v2_run.go:155` and `api/v2_run.go:176` flush it, and
`transport/sse.go:104` keeps the connection alive with keepalives. The browser
holds that connection open for the length of a turn.

The path the browser takes is: browser → **host port** → sandbox → openheron's
hertz server. If the forwarding layer between the host and the sandbox
**buffers** the response body, the model stops streaming: the user sees
nothing until the turn ends, and then everything at once.

★ Nothing in this repository decides that. It is a property of the forwarding
layer, and it has to be **measured** (§5), not assumed. If it buffers, the
options are a forwarding configuration that flushes, or a different transport
between the browser and the sandbox.

This is the same risk the browser already lives with in `make smoke` — a real
SSE stream through a real server — and it is **not** present in the E2E fake
backend, which is why the E2E suite cannot answer this question (§5).

### 2.11 What it does not change

The outward boundary is what makes this cheap. **No Go code changes.** Not one
of these moves:

- the agent model, the policy tiers (`agentkit/spec/defaults.go:11`), the
  approval flow, the checkpoint store;
- the file tools and their confinement (`agentkit/fsx/fsx.go:194`);
- the API surface (`api/v2_files.go:34-72`);
- the frontend, which is embedded (`main.go:35`);
- the release model (`Makefile:96`, `Makefile:108`).

★ What changes is **where the process runs**, and therefore what the host can
see. That is the cheapest possible form of this change, and it is the reason
this document is worth writing before any code is written.

### 2.12 Platform matrix

| Host | Docker Sandboxes | Consequence |
|---|---|---|
| `linux/amd64`, `linux/arm64` | KVM required | works on a server or a Linux workstation with `/dev/kvm` |
| `darwin/arm64` | Apple Silicon + Hypervisor.framework | works |
| `darwin/amd64` | **not supported** | Intel Macs have no microVM path |
| `windows/amd64`, `windows/arm64` | Windows Hypervisor Platform | supported, with caveats |

★ Intel Mac is the same hole `docs/microvm-execution-design.md` §2.8 records
for the microsandbox route. Two proposals, one hole: the release matrix has
six targets and microVM isolation reaches five of them.

The difference is what fills the hole. The microsandbox route leaves the
server on the host, so on Intel Mac the **host path** still works and only the
`execute` isolation is unavailable. Here the whole process is inside the VM,
so there is nothing to fall back to: on Intel Mac, openheron-as-a-Kit does not
run at all.

★ That is a real trade, and it is the one to weigh against §2.11's "no code
changes". A user on an Intel Mac keeps the plain binary; a user in a Kit gets
the whole boundary.

### 2.13 Distribution

- The Kit is a **registry artifact**. `sbx run <image>` consumes it; the
  published descriptor is what the signature covers and what the permission
  gate judges.
- The Kit needs the `sbx` CLI on the host. That is a new install step for an
  openheron user — and, unlike microsandbox, it comes with a **Docker sign-in**
  and a license that is free for individual and commercial use but prices
  organization-wide governance separately.
- The permission surface is a **reviewable diff**: the specification's gate
  stops any widening — a new allowed domain, a new credential, a removed deny
  entry — and asks. For an agent that runs arbitrary commands, that diff is
  the artifact a security review should look at.

### 2.14 The boundary's own risk

★ Everything above describes what the boundary **buys**. This section is about
what it **costs**, because a design that only accounts for the first is not
accounting.

**The boundary has had published escapes.** Docker's own security
announcements page records two flaws in Docker Sandboxes, both fixed in the
`0.42.0` release on 2026-09-07 and published on 2026-09-15:

- `CVE-2026-77179` (CVSS 9.4, critical) — on macOS, the virtio-fs host server
  followed symlinks when reopening an unlinked file from a stored path. A
  malicious guest could replace a parent directory with a symlink, escape the
  shared workspace, and read or modify arbitrary host files as the VMM user,
  potentially reaching host code execution. Affected `0.28.0` up to but not
  including `0.42.0`.
- `CVE-2026-79994` (high) — a time-of-check to time-of-use race in the Unix
  socket relay: a guest could make the host connect to an `AF_UNIX` socket
  outside the shared workspace. Affected `0.37.0` up to but not including
  `0.42.0`.

★ The documented mitigation for both is **clone mode and no read-write host
mounts** — which is exactly what openheron cannot use: the agent has to write
into the workspace, and it has to write into the configuration the user edits.
So for this workload the mitigation is unavailable, and the answer is the
version floor: **`sbx` 0.42.0 or later**, checked before the Kit is published.

★ The boundary is a strong boundary, not an absolute one, and it moves fast.
The same announcements page also records a certificate-authority compromise that
forced the built-in kit images to be rebuilt — so a Kit's base image is a
supply-chain item that has to be refreshed, not a constant.

★ **The files the agent creates are owned by uid `1000` on the host.** The
workload runs as the `agent` user at uid `1000`, and the mount carries no uid
mapping that openheron can rely on: a file the agent writes inside the
workspace belongs to uid `1000` in the host's view, which is some unrelated
account on most Linux hosts. The user can read it; they may have to `chown` it
before editing. This one is worth verifying rather than assuming (§5).

---

## 3. The relation to the microsandbox proposal

Two proposals, one goal, different shapes:

| | `docs/microvm-execution-design.md` | this document |
|---|---|---|
| What is isolated | the `execute` tool | the whole process |
| Where the server runs | on the host | inside the microVM |
| Code change | replace `StreamingShell` | none |
| The read path | still on the host, still unbounded | inside the boundary |
| Intel Mac | the host path still works | nothing to fall back to |
| Runtime | microsandbox (Apache-2.0, self-hosted) | Docker Sandboxes (`sbx`, Docker sign-in) |
| Distribution | openheron ships or requires `msb` | a registry artifact plus `sbx` |

★ They are not mutually exclusive, and the difference is the point: the
microsandbox route is a **narrower** boundary that openheron can adopt without
moving anything. This one is a **wider** boundary that costs no code.

If both were built, the natural composition is: **the Kit is the outer boundary,
and microsandbox is not needed inside it** — the sandbox already provides the
isolation the `execute` tool was going to ask for. Two isolation layers for one
command path is one too many.

---

## 4. Decisions

### 4.1 Settled

These are decided; the Kit in `sandbox-kit/` is built on them.

1. **The initialization carrier.** A `lifecycle@1` **startup hook** plus the
   entrypoint's repeat of the same command (§2.5). Two carriers rather than
   one, because `openheron init` is idempotent and the redundancy costs a
   no-op while removing the dependency on the hooks' position relative to the
   entrypoint. **No Go change.**
2. **Where the state lives.** On the **mount** (§2.9): the data dir stays at
   its default, `<workspace>/.openheron/state`, so the conversations survive
   a sandbox that is stopped, recreated, or removed, and the user can read
   them. **No `volume@1` is declared**; `DATA_DIR` stays free for a user who
   wants the other arrangement.
3. **The provider is openheron's own; the credential is bound with one
   command.** The provider — which file `openheron init` writes — is decided by
   the factory template and is not an argument (§2.3). The credential is bound
   on the host with `sbx secret set-custom … --placeholder proxy-managed`, which
   makes the host's placeholder the same string the runtime injects: the
   container holds the sentinel, the key never enters the VM, and the user runs
   one command (§2.8). The Kit keeps the `credential@1` declaration, so the
   credential's purpose stays in the descriptor — and therefore in the
   permission surface (§2.14).
4. **Egress.** The Kit **does not restrict** what the agent may reach: the
   runtime phase is granted `*` (§2.7). The microVM is the boundary; the
   host's policy and the organization's rules are the levers, and they sit
   above the Kit in the enforcement order. A Kit that restricted egress would
   buy friction rather than safety — and the friction lands on the agent.

### 4.2 Still open

These are the owner's calls. The two questions §6 measured are settled: the
workspace path **is** injected, and the forwarding layer does **not** buffer.

1. **The published registry.** Does openheron publish the Kit to a registry it
   controls, or does each user build it from the source tree?
2. **The skills directory.** `agentkit/build/snapshot.go:52-54` adds it as a
   second root. Where does it live inside the VM — the workspace, or the
   volume?
3. **The `sbx` version floor.** `0.42.0` or later, because the documented
   mitigation for the published escapes is a mode openheron cannot use
   (§2.14). Does the Kit declare the floor, or does the installer check it?

---

## 5. How this would be verified

A boundary that is not a unit test has to be verified behaviourally.

1. **Escape test.** A turn that reads `~/.ssh` and writes outside the project
   root must fail, and the failure must be visible in the tool output.
2. **Read-path test.** ★ The one the microsandbox route cannot pass: a turn
   that asks `read_file` for a file outside the project root must fail. If it
   succeeds, the boundary is not where this document says it is.
3. **SSE test.** A turn must stream to the browser **as it happens**, not in
   one burst at the end (§2.10). This is the acceptance criterion most likely
   to fail first, and it cannot be checked against the E2E fake backend — that
   backend has no real forwarding layer in front of it.
4. **Network test.** A command that opens a socket to a host outside the allow
   list must fail.
5. **State test.** Restart the sandbox: the session must still be there.
   Remove it: with the data dir on the mount (§2.9), the session must still be
   there, and the files must be readable by the user on the host.
6. **Degradation test.** With the credential not provided, the sandbox must
   still start and the model calls must fail with a clear 401 — not a crash.
7. **Credential test.** The real key must appear nowhere inside the sandbox —
   not in the environment, not in the config root, not in a process dump.
   The container sees a sentinel, and the model calls still succeed (§2.8).
8. **Proxy test.** ★ The model call must actually traverse the sandbox
   proxy: the policy log has to show it on the proxied path, and the
   container's env var must hold the sentinel rather than a real key. A client
   that bypasses the proxy ships the sentinel to the API and gets a 401.
   openheron's client honors `HTTPS_PROXY` (`llm/common/logging.go:77`
   delegates to `http.DefaultTransport`), but that has to be observed, not
   assumed.
9. **Initialization test.** An empty workspace must come up configured: the
   initialization writes the configuration, the server starts, and the config
   root is the one the user sees on the host. A second start must keep the
   existing configuration and change nothing. ★ And the configuration must
   land on the **mount**, not inside the VM — that is the whole point of
   §2.6, and it is the one thing a running sandbox can get wrong quietly.
10. **Ownership test.** The files the agent wrote inside the workspace must be
    readable by the user on the host, and their ownership must be something
    the user can live with (§2.14).
11. **Cost test.** Ten turns in one sandbox must pay one boot, not ten.

---

## 6. Measured

★ Measured on 2026-11-04, on a real sandbox: `sbx` v0.51.0, Docker Engine
29.9.2, Linux on WSL2, the Kit built from `sandbox-kit/` with the binary baked
locally. The workload's own
environment was captured from inside the sandbox and read on the host.

| Question | Answer |
|---|---|
| Does the workload's environment carry `WORKSPACE_DIR`? | **Yes** — `/home/you/my-project`: the host's path, and the path the workspace is mounted at |
| Does the process start in the workspace? | **Yes** — `PWD` and `OLDPWD` are both the workspace |
| Does the configuration land on the mount? | **Yes** — `.openheron/{agents,llm,policy,server,state}` is on the host, and the sessions with it |
| Does the credential reach the model? | **Yes** — the container held `DEEPSEEK_API_KEY=proxy-managed`; the proxy substituted the real value and the call returned "ok" |
| Does the published recipe build? | **Yes** — `make kit` with the container-driver builder produces `openheron-kit:0.0.6`, and the binary inside reports `v0.0.6` |
| Does the published Kit run in a sandbox? | **Yes** — the startup hook writes the configuration onto the mount and the server creates its data directory there |
| Does the port forwarding buffer the SSE stream? | **No** — 1339 frames over 10.3 s, the first at +21 ms |
| Who owns the files the agent writes? | The host user — the VM's uid `1000` is the host's uid `1000` (§2.14) |

★ **Both credential routes were measured.** With the placeholder
(`--placeholder proxy-managed`) the container held `DEEPSEEK_API_KEY=proxy-managed`
and the proxy substituted the real value; with the key injected directly
(`--env DEEPSEEK_API_KEY=…`) the container held the key and the proxy
forwarded it unchanged because it was not the placeholder. Both reached the
model; only the first keeps the key out of the VM (§2.8).

### 6.1 What the measurement changed in this document

- §2.5 and §2.6 are no longer a hypothesis: `WORKSPACE_DIR` is injected, so
  the entrypoint's `cd "${WORKSPACE_DIR:-$PWD}"` lands on the mount, and the
  configuration root is `<workspace>/.openheron`.
- §2.10's risk did not materialize: the forwarding layer does not buffer.
- §2.14's ownership worry is resolved for a host whose uid `1000` is the user's
  — the agent's files are the user's files.
- ★ The recipe changed: the base image already carries a populated CA store,
  so the `apt-get` step was removed — it was redundant *and* it was the only
  thing in the build that needed a package mirror.

### 6.2 Operational findings, none of them in the Kit

- ★ **`sbx rm` takes the published ports with it.** After recreating a
  sandbox, `sbx ports` reports "No published ports" and the forwarding does
  not come back by itself; `sbx ports <name> --publish 8080:8080` has to be
  repeated. A stale published port looks exactly like a dead workload.
- ★ **The `default` buildx builder drops the Kit's manifest annotations.** The
  image builds, and it is no longer a Kit: the descriptor annotation is gone.
  A container-driver builder keeps it, so `make kit` names one.
- ★ **That builder also needs `--load`.** Without it the result stays in the
  build cache and there is no local image to validate — `make kit-check` would
  look for an image that was never exported.
- ★ **The port forwarding is unreliable on a WSL2 host.** The proxy dials the
  bridge gateway (`172.17.0.1:8080`) and gets a connection refused, while the
  workload is up: the same Kit answered 1339 SSE frames earlier in the same
  session and stopped answering later, with no change to the Kit. Re-publishing
  the port sometimes brings it back. It is a host-side symptom, and the Kit
  must not be read as the cause.
- ★ **The host needs `/dev/kvm`,** so the user has to be in the `kvm` group.
  The daemon is the process that opens it, not the CLI.
- ★ **`sbx run --name` restarts the sandbox; it does not rebuild the
  container from a changed Kit.** Kit changes need `sbx rm` and a new
  `sbx run` — a restart silently keeps the old image, which makes a probe look
  like it did not run.
- ★ **The recipe names a published release** (`v0.0.6`), so anyone can build
  the Kit. The measurement above used a locally built binary instead, because
  the release the recipe named did not exist yet at the time.

### 6.3 How `WORKSPACE_DIR` arrives

★ It is a **sandbox-level environment variable that the runtime injects** — not
the Kit's, and not the image's. Three pieces of evidence:

- The Kit's Dockerfile does not set it, and the workload container has it.
- `sbx exec` starts a **different** container in the same sandbox, and it has
  the same value — so it belongs to the sandbox, not to the workload.
- The `sbx` binary contains the spec templates it generates for the built-in
  agent kits, and they read it the same way:
  `ws="${WORKSPACE_DIR:-/}"` with `env: [WORKSPACE_DIR]` declared on the
  hook.

The runtime injects a whole set of sandbox variables, not just this one:
`SANDBOX_ID`, `SANDBOX_NAME`, `SANDBOX_VM_ID`, `SBX_AGENT`,
`MCP_GATEWAY_URL`, `MCP_SENTINEL_TOKEN_NAME`, `SBX_CRED_<SERVICE>_MODE`,
and the proxy settings (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`,
`NODE_USE_ENV_PROXY`, `JAVA_TOOL_OPTIONS`, `SSL_CERT_FILE`).

★ **There are two ways to read it, and they are not symmetric.** The workload's
environment is unrestricted, so the entrypoint reads `${WORKSPACE_DIR:-$PWD}`
directly (§2.5). A hook's environment is **deny by default**, so the hook has
to declare the name — `env: [WORKSPACE_DIR]` — and that declaration is exactly
what the built-in kits do.

★ The Kit therefore uses the same mechanism as Docker's own agent kits: a
startup hook that materialises the workspace path into the agent's own
configuration, and an entrypoint that starts in the same directory.

### 6.4 Injecting variables

★ Four channels, and they are not interchangeable:

| Channel | Scope | Where the value comes from |
|---|---|---|
| the image's `ENV` | every container of that image | the Dockerfile — static, the same for everyone |
| a Kit **argument** with `env:` | the container, at create | the installer, expanded into the effective descriptor |
| `sbx run -e` / `--env-file` | the sandbox | the host — a bare name takes the host's value |
| `sbx secret set-custom --host --env` | the sandbox | the host's secret store — the sandbox sees a placeholder |

Measured on the sandbox: `sbx exec -e INJECT_TEST=custom-value` put
`custom-value` into the exec'd process, `sbx run --env MY_RUN_VAR=hello-from-run`
put it into the sandbox — where it stayed until the sandbox was recreated —
and the runtime's own variables arrived in the workload's environment (§6.3).
★ A value injected with `--env` **overrides** what a capability would have
installed in the same variable, which is what makes §2.8.2 work with the same
Kit.

★ **A hook's `env:` list is not a fifth channel.** The hook environment is
deny by default, so declaring a name only grants *visibility* of a value that
is already there — it never introduces one. A hook that needs a custom
variable gets it from the host (`-e`) and declares the name; the Kit's
arguments are visible to a hook the same way.

★ **A sandbox-level variable and a session-level one are not the same
lifetime.** A variable baked in at create stays with the sandbox; one applied
to a session applies to that session only. Recreating the sandbox drops the
former and keeps the latter's own rules.


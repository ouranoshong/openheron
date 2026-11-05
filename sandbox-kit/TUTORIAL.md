# Running openheron in a sandbox

A guide for a user who wants to run the openheron agent **inside a Docker
sandbox**. You need a Docker account, the `sbx` CLI, and a model key. You do
not need the source tree, a build step, or a Dockerfile — the sandbox image is
already published.

★ **What you get.** The whole openheron process — the HTTP/SSE server, the
file tools and the shell tool — runs inside a microVM. Your host is outside
that boundary. The workspace you name is the only thing that crosses it, and
it crosses it as an ordinary directory on your host.

★ **What you run.** Two things:

| | What it is |
|---|---|
| **the workload** | the root filesystem the agent works in, plus the launch contract — the published image `docker.io/ouranoshong/openheron-kit:latest` |
| **a mixin** | the model credential, and the network rule that goes with it |

★ A sandbox without a mixin starts and then answers every model call with a
401: the credential is what the mixin carries. One mixin per vendor, and you
compose the ones you use.

★ **`latest` is the current release, and it moves.** The images are also
published under their version — `0.0.7`, `0.0.6`, and so on, the version
being whatever the release notes say — so a sandbox you want to be the same
sandbox next month names that one instead:

```sh
sbx run docker.io/ouranoshong/openheron-kit:X.Y.Z \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:X.Y.Z \
    --name openheron ~/my-project
```

★ `X.Y.Z` is the version the release notes name. ★ The version inside the
Kit is the release it was built from, so the two never drift apart: `latest`
and `X.Y.Z` are the same image on the day `X.Y.Z` is the latest, and
`latest` moves on while `X.Y.Z` stays.

---

## 0. Before you start

**A Docker account.** Sandboxes are a Docker feature:

```sh
sbx login
```

**A hypervisor.** On Linux the sandbox is a microVM, so it needs KVM:

```sh
ls -l /dev/kvm          # must exist
```

If it exists but you get "permission denied", your user is not in the `kvm`
group. Adding it needs a logout to take effect:

```sh
sudo usermod -aG kvm "$USER"
```

**The model key.** openheron reads the key from the environment, but the
sandbox must not hold the key itself. Bind it on the host once:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ The placeholder `proxy-managed` is deliberate: it is the sentinel the
runtime injects into the container. The real key stays in the host's secret
store, and the sandbox's proxy swaps it in on the way out. The container only
ever sees `DEEPSEEK_API_KEY=proxy-managed`.

★ `--value-stdin` keeps the key out of your shell history and out of the
process arguments.

---

## 1. One choice to make: where the mixin comes from

★ **A mixin is a declaration, not a product.** It says two things — which
credential to inject, and which host may reach it — and it carries no content
of its own. So it reaches you in one of two shapes, and **you pick either
one**; the runtime accepts both and behaves the same:

| | a published image | a file of your own |
|---|---|---|
| **What you get** | `docker.io/ouranoshong/openheron-kit-deepseek:latest` | one YAML file, 25 lines |
| **To start using it** | nothing — pull it | copy it into a directory |
| **To point it at your own endpoint** | wait for a new image | edit one line |
| **The version** | fixed, and the one that was measured | whatever you wrote |
| **You must trust** | the publisher | nobody but yourself |

★ **Take the image** if you want the version that was measured, and you have
no reason to change the endpoint.

★ **Take the file** if openheron has to talk to *your* endpoint — a proxy, a
gateway, a vendor with no published mixin. Then the change is one line, and
there is no release to wait for.

### 1.1 The image

Nothing to do. The reference is the whole thing:

```
docker.io/ouranoshong/openheron-kit-deepseek:latest
```

### 1.2 The file

This is the whole file. Save it as `openheron-deepseek.yaml`:

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: mixin
displayName: openheron credential (DeepSeek)
description: the DeepSeek model credential for an openheron workload, injected by the host proxy
licenses: [Apache-2.0]

capabilities:
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - api.deepseek.com

  - type: com.docker.sandbox/credential@1
    optional: true
    description: DeepSeek API access
    config:
      service: deepseek
      phase: runtime
      apiKey:
        name: DEEPSEEK_API_KEY
        proxyManaged: true
        inject:
          - {domain: api.deepseek.com, header: Authorization, format: "Bearer %s"}
```

★ **Put it in a directory of its own** — the directory name is the file name's
stem:

```sh
mkdir -p ~/openheron-deepseek
# save the file above as ~/openheron-deepseek/openheron-deepseek.yaml
```

★ **Three rules the runtime insists on**, all measured:

- **A directory, not the file.** `--kit ~/mixin.yaml` is refused:
  `artifact zip "./mixin.yaml": zip: not a valid zip file` — the path is
  taken as an archive.
- **The directory name is the file name's stem.** `openheron-deepseek/`
  holds `openheron-deepseek.yaml`.
- **A v3 mixin is not a ZIP.** Zipping the directory is refused with
  `artifact: spec.yaml (or spec.yml) is required` — that is the older kit
  grammar, where a kit is a `spec.yaml` and an optional `files/` directory.

★ **Why the network rule is in the same file as the credential.** The runtime
wants every domain the credential injects into to appear **literally** in the
allow list for the same phase. The workload's own rule is `*`, which covers
every domain but does not satisfy that check — so the mixin names the host
outright. The two allow lists union per phase.

★ **The `service` name is what makes a second vendor possible.** A credential
has one owner per (service, phase), so two vendors need two service names.
Two mixins claiming `deepseek` do not compose.

---

## 2. Start a sandbox

With the published mixin:

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:latest \
    --name openheron ~/my-project
```

With your own file:

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit ~/openheron-deepseek \
    --name openheron ~/my-project
```

★ **Only the `--kit` line differs.** The workload is an image in both cases —
the workload has a root filesystem, so it has to be an image.

Read the arguments apart:

| | |
|---|---|
| `docker.io/ouranoshong/openheron-kit:latest` | the workload — the published image, pulled on first use |
| `--kit …` | the mixin — an image reference or a directory, your choice |
| `~/my-project` | the workspace — a directory on your host, mounted into the sandbox |

★ **The workspace is a real directory on your host.** Point it at a project
you want the agent to work on, or at an empty directory to start from. It
appears at the same absolute path inside the sandbox, so the paths in the
conversation mean the same thing on both sides.

★ **The daemon has to be able to see that path.** The sandbox runtime runs the
daemon somewhere of its own choosing — on Linux with Docker Desktop that is a
virtual machine — and it mounts what it can reach. A path it cannot see is
refused before the sandbox starts:

```
error: cannot create sandbox: request failed: 400 Bad Request: workspace "/tmp/work" does not exist
```

★ Your home directory is the safe answer. A project under it is reachable;
`/tmp` usually is not.

What you will see — with the file form, the only difference is where the
mixin came from:

```
  ○ image — pulling…
      0a096e869e28 downloaded  509.3 MB
  ✓ image — ready
  ✓ assembled kit image (21 layers)
    setting 1 environment variable
      +DEEPSEEK_API_KEY
  ✓ http — published localhost:32772 -> 8080/tcp

  ✓ Created sandbox openheron
  ○ openheron — starting in sandbox openheron…
      workspace /home/you/my-project
```

★ **`http — published localhost:32772 -> 8080/tcp` is the runtime telling you
the port is ready.** The workload declares the port it serves on, and the
runtime publishes it on an ephemeral loopback port at start time. You never
choose that port yourself — two sandboxes asking for the same one would
collide.

★ **The terminal stays busy.** The workload's entrypoint is the server
itself, and the session is what starts it. You will see the server's log
lines here.

---

## 3. Open it

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32772       8080           tcp
::1         32772       8080           tcp
```

Open **http://127.0.0.1:32772** in a browser. That is the full openheron
interface: the chat, the agents, the settings editor. The web interface and
the API share the port, so there is nothing to configure.

★ Ask it something. The answer streams back as it is generated — you will see
the words appear, not the finished text at once.

---

## 3.1 Pin the port, if you want to

★ **The port above is temporary.** The runtime picks it, because two sandboxes
asking for the same host port would collide. That is right for a sandbox you
open once, and wrong for one you want to reach at the same address every time.

★ **Publish it on a port of your own** to get there:

```sh
sbx ports openheron --publish 8080:8080
```

```
Published 127.0.0.1:8080 -> 8080/tcp4
```

★ **The specification reads `HOST_PORT:SANDBOX_PORT`.** The left number is the
port on your host, the right one is the port inside the sandbox — and the
sandbox's is already `8080`, because that is what the workload declares.

★ **It adds a mapping; it does not move one.** The temporary port keeps
working, so both addresses serve the same sandbox:

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   8080        8080           tcp4
::1         32772       8080           tcp
127.0.0.1   32772       8080           tcp
```

★ **Without a protocol the binding is IPv4 only** — `tcp4`, on `127.0.0.1`.
Name `tcp` and it binds both families, so a client that resolves `localhost`
to `::1` reaches it too:

```sh
sbx ports openheron --publish 8080:8080/tcp
```

```
Published 127.0.0.1:8080 -> 8080/tcp
Published [::1]:8080 -> 8080/tcp
```

★ **Two things can go wrong, and only one of them complains.** Publishing the
same mapping twice is refused, plainly:

```
error: publish ports: request failed: 409 Conflict: request[0]: port 127.0.0.1:8080/tcp4 is already published to sandbox port 8080
```

★ **A host port another process already holds is not.** The publish reports
success — the forwarder binds its own socket, not yours — and then your client
reaches *that other process* instead of the sandbox:

```
Published 127.0.0.1:8081 -> 8080/tcp4
```

```
$ curl http://127.0.0.1:8081/v2/health
# no answer: something else is listening there
```

★ **Check before you pin.** `ss -ltn | grep 8080` shows whether the port is
free, and `sbx ports openheron` shows what is already published.

★ **And a published port does not survive the sandbox.** `sbx rm` takes it
with it, so a sandbox you recreate needs the publish again — step 6 covers
that.

---

## 4. Leave it running

★ **You can close the terminal and the server keeps serving.** Press
`Ctrl-\` to leave the session while the sandbox keeps running:

```
^\
```

The sandbox stays up with the port still published. You can close the
terminal window; the next time you want it, see step 6.

★ Without `long-running@1` the sandbox would stop as soon as its last client
disconnected. The workload declares it, so the server is a background
workload rather than a session.

---

## 5. The configuration openheron writes

★ **On the first start, openheron seeds its own configuration** in the
workspace — the startup hook does it, before the server comes up:

```
~/my-project/.openheron/
  agents/main.md      the entry agent
  agents/task.md      the executor agent
  llm/deepseek.yaml   the model profile
  policy/             permissions and limits
  server/             the server's own settings
  state/              the sessions
```

★ **Those are your files.** They are on your mount, in plain sight, and
openheron re-reads them as they change. The one you will edit most is the
model profile — this is what `llm/deepseek.yaml` contains:

```yaml
# ★ Factory template: DeepSeek (an OpenAI-compatible endpoint).
#
# ★ The endpoint lives in the configuration (the code hardcodes nothing) — take it
# from the vendor's docs, **path included**.
type: openai_compat
base_url: https://api.deepseek.com

# ★ api_key holds the **name of an environment variable**, rendered as ${NAME}.
api_key: ${DEEPSEEK_API_KEY}

timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072

    # Tier vocabulary: tier name → the field this endpoint sends it as.
    # An agent only writes `reasoning: high`; it does not need to know what shape
    # "high" has at this vendor.
    #
    # ★ This table is **not optional**: a tier the endpoint does not declare is
    #   **silently ignored** — an agent writes `reasoning: high` while the model
    #   has no "high", which means "no thinking at all", with no error and no
    #   message. Declaring it is what makes `high` / `low` actually take effect.
    reasoning:
      off:  { reasoning_effort: none, thinking: { type: disabled } }
      low:  { reasoning_effort: low,  thinking: { type: enabled } }
      high: { reasoning_effort: high, thinking: { type: enabled } }
      max:  { reasoning_effort: max, thinking: { type: enabled } }
    reasoning_default: off                        # used when the agent writes no reasoning
```

★ **The two lines that matter when you point it somewhere else:**

- `base_url` — the vendor's endpoint, **path included**, exactly as their
  documentation writes it.
- `api_key` — the **name** of an environment variable, written as `${NAME}`. A
  literal key is rejected: the configuration is meant to be committed.

★ **`agents/main.md` and `agents/task.md`** are the two agents: the entry one
understands the request, splits it, delegates and verifies; the executor one
does the work. They are ordinary Markdown with instructions in them.

★ **`policy/permissions.yaml`** decides which tools ask first:

```yaml
# ★ There are only two columns, ask and deny — **any tool not named is allowed**.
#   Name the tools that really have consequences here: ask (a nod every time) or
#   deny (unavailable).
ask:   [write_file, edit_file, execute, kb_index]
deny:  []

# ★ The outer deny takes precedence over each agent's own disabled_tools — that
#   is the one guarantee a sub-agent cannot relax. To narrow down **one**
#   agent, use its own disabled_tools.
```

★ **`server/config.yaml`** is the process's own settings — the listen address,
the data directory, the retry count, the agent depth cap. It is **not**
hot-reloaded: it configures the process, so a change needs a restart.

★ **The sessions live in `state/`**, on the mount too, so the conversations
survive the sandbox being stopped, recreated or removed.

---

## 6. Come back later

```sh
sbx run --name openheron
```

★ **No path, no `--kit`.** The sandbox remembers both. Passing a path would
make the CLI read it as the agent to run, and passing `--kit` is refused:

```
error: sandbox 'openheron' already exists; --kit can only be used when creating a new sandbox
  try: sbx run --name openheron
```

★ If the server is already up, this second launch cannot bind the port and
reports — in one of two ways, depending on how far it got:

```
error: agent exited with code 2
```

```
error: inspect exec: context deadline exceeded
```

★ **Neither is a failure.** The running server is the one you wanted; it is
still serving on the published port. The message is about the *second* copy
of the server, not the first.

★ **A sandbox you *recreate* comes back without its ports.** `sbx rm` takes
the published ports with it, and the runtime publishes the new one on a fresh
temporary port. Publish the one you want again — step 3.1 has the command:

```sh
sbx ports openheron --publish 8080:8080
```

★ **The `sbx run --name openheron` above is the other case.** It attaches to
the sandbox that is already there, ports and all, so nothing has to be
published again.

---

## 7. Another vendor

★ **Add one more mixin.** Either shape works, and they compose the same way —
so keep whichever you started with.

With images:

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:latest \
    --kit docker.io/ouranoshong/openheron-kit-openai:latest \
    --name openheron ~/my-project
```

With files — the second one is the same YAML as in step 1.2, with `openai`
where it says `deepseek`:

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit ~/openheron-deepseek \
    --kit ~/openheron-openai \
    --name openheron ~/my-project
```

★ **And bind that vendor's key** before you start:

```sh
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ Both keys arrive in the container as sentinels, each with its own network
rule. What the **provider configuration** does is your business:
`openheron init` maintains one provider configuration per configuration root,
so the second vendor's `llm/*.yaml` is written by you, on the mount.

---

## 8. Clean up

```sh
sbx rm openheron
```

★ The sandbox, its container and its published port all go. The workspace
stays on your host — `.openheron/` included — so the next run picks up where
this one left off.

---

## Try it on a project

★ **A project that does something is the whole demonstration.** The repository
carries one: [`examples/demo-project`](examples/demo-project) is a tiny Go
program with a bug in it — `make test` fails, and the fix is one character.

★ **Run it from the repository root**, where the paths below resolve.

From the published images:

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:latest \
    --name openheron sandbox-kit/examples/demo-project
```

★ **Or from the source tree**, where the runtime assembles both Kits from
the files on disk:

```sh
make kit    # once: it builds the binary and copies it beside the recipe

sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/examples/demo-project/openheron-deepseek \
    --name openheron sandbox-kit/examples/demo-project
```

★ That second form is the one to use when you are **changing** the Kit: the
runtime rebuilds it from the files you just edited, with no image and no
registry in between. It needs `make kit` once, because that is what copies
the binary next to the recipe where the build can reach it. The first form
is the one a user takes, and it needs nothing but `sbx`.

Ask it to fix the failing test. The agent reads the files, runs `make test`,
**asks you before it acts** — approve it — and the change lands on your host,
because the workspace is a directory on your host.

★ The project carries **no** `.openheron/`: `openheron init` writes the whole
configuration on the first start. A project that *does* ship its own agents or
its own policy keeps them — `init` writes what is missing and keeps what is
there, so the files you ship win and the ones you leave out arrive.

---

## If something goes wrong

| What you see | What it means |
|---|---|
| `error: resolve kits: kit "…": its source is not in your allowlist` | the Kit is not on `docker.io/` — an administrator has to allow its source |
| `error: … fetch manifest: … http: server gave HTTP response to HTTPS client` | the registry serves plain HTTP; it has to serve HTTPS |
| `artifact zip "./mixin.yaml": zip: not a valid zip file` | `--kit` takes a **directory** for a mixin, not the file |
| `artifact: spec.yaml (or spec.yml) is required` | a ZIP of a v3 mixin is refused — that is the older kit grammar |
| `error: sandbox '…' already exists; --kit can only be used when creating a new sandbox` | you passed `--kit` to an existing sandbox — drop it, or `sbx rm` first |
| `error: agent exited with code 2` | the server was already running; the one you want is still up |
| `error: resolve kits: kit "…": artifact: spec.yaml (or spec.yml) is required` | you put a path where the agent goes; `sbx run --name <name>` takes no path |
| the browser shows "connection refused" | the port changed — `sbx ports <name>` again, or the sandbox stopped |
| the sandbox came up but nothing answers on the port | `openheron init` wrote **nothing** — it refuses to write a configuration that does not load, so a broken agent file leaves the server with no configuration at all. The session log says which file broke it |
| the port is gone after a restart | `sbx rm` takes the published ports with it; the runtime republishes on the next start |
| the model call fails with 401 | the key was never bound — redo step 0 |
| the model call fails with a connection error | the credential's network rule is missing — the mixin carries it, so check you passed the mixin |

---

## One more thing to know

★ **The sandbox is the boundary; the allow list is not.** The workload does
not restrict what the agent may reach — the microVM is what isolates it, and
the host's own policy (`sbx policy`) is what governs egress. If you want to
narrow that, narrow it on the host, not by editing the Kit.

---

★ **Building this yourself?** The image is published, but the sources are
in the repository: [`BUILDING.md`](BUILDING.md) covers building it from
source, changing the descriptor, and publishing a new version.
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) is the design
behind it, and every assumption it rests on is measured.

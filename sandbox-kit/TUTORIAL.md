# Running openheron from a packaged sandbox Kit

A guide for a user who has an **openheron sandbox image** and wants to run it
with `sbx` — no source tree, no build step, no Dockerfile.

★ **What you need is two small things.** A **workload** Kit (the packaged
image: the openheron server and its runtime) and a **mixin** Kit (a
declarative overlay: the model credential and the network rule that goes with
it). The mixin is a few lines of YAML — you can write it yourself, and
step 2 gives you the exact text.

★ **What you get.** The whole openheron process — the HTTP/SSE server, the
file tools and the shell tool — runs inside a microVM. Your host is outside
that boundary. The workspace you name is the only thing that crosses it, and
it crosses it as an ordinary directory on your host.

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

**The model credential.** openheron reads the key from the environment, but
the sandbox must not hold the key itself. Bind it on the host once:

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

## 1. The two Kits

| | What it is | Where it comes from |
|---|---|---|
| **workload** | the root filesystem the agent works in, plus the launch contract | a packaged image, referenced by its OCI name |
| **mixin** | the credential, and the network rule that goes with it | a packaged image, **or a YAML file you write** |

★ **The workload is an image reference.** It looks like this:

```
docker.io/ouranoshong/openheron-kit:0.0.6
```

★ **That name is where it is published, not where it is today.** openheron's
Kit is not on Docker Hub yet; whoever hosts it publishes it with

```sh
make kit-push
```

and the reference is then the name it was pushed to. If your Kit came from
your own registry, that is the name to use.

★ **The mixin does not have to be an image.** It is a declaration, and a
declaration is a YAML file. If your Kit set came with mixin images, use them
the same way; if it did not, step 2 gives you the text to save.

★ **Where the images may come from.** By default `sbx` only installs Kits
from two places:

```sh
sbx settings get kit.allowedSources
```

```
["docker.io/","dhi.io/"]
```

★ A Kit from anywhere else is refused, with the command to fix it in the
message:

```
error: resolve kits: kit "…": its source is not in your allowlist;
current kit.allowedSources: docker.io/, dhi.io/
```

★ That is a deliberate line: a Kit is executable content, so where it comes
from is a decision, not a detail. If your Kit is on Docker Hub, nothing to do.
If it is on your company's registry, an administrator has to allow that
source.

★ **A registry has to serve HTTPS.** There is no plain-HTTP option.

---

## 2. Write the mixin

Save this as `openheron-deepseek.yaml` in a directory of its own:

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: mixin
displayName: openheron credential (DeepSeek)
description: the DeepSeek model credential for an openheron workload, injected by the host proxy
licenses: [Apache-2.0]

capabilities:
  # ★ The network rule and the credential travel together: the runtime checks
  #   that every inject domain appears literally in the allow list for the
  #   same phase, and the workload's bare "*" does not satisfy that check.
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - api.deepseek.com

  # ★ proxyManaged: the real value stays on the host's secret store; the
  #   container only ever sees the sentinel "proxy-managed".
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

★ **The directory name is the file name's stem** — `openheron-deepseek.yaml`
in a directory called `openheron-deepseek`. `sbx` reads the file and builds
the declaration-only image from it; there is nothing to build yourself.

★ **Why the network rule is in the same file as the credential.** The runtime
wants every domain the credential injects into to appear **literally** in the
allow list for the same phase. The workload's own rule is `*`, which covers
every domain but does not satisfy that check — so the mixin names the host
outright. The two allow lists union per phase.

★ **The `service` name is what makes a second vendor possible.** A credential
has one owner per (service, phase), so two vendors need two service names.
Two mixins claiming `deepseek` do not compose.

---

## 3. Start a sandbox

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit ./openheron-deepseek \
    --name openheron ~/my-project
```

Read the three arguments apart:

| | |
|---|---|
| `docker.io/ouranoshong/openheron-kit:0.0.6` | the workload — the packaged image, pulled on first use |
| `--kit ./openheron-deepseek` | a mixin — the directory you wrote in step 2 |
| `~/my-project` | the workspace — a directory on your host, mounted into the sandbox |

★ **The workspace is a real directory on your host.** Point it at a project
you want the agent to work on, or at an empty directory to start from. It
appears at the same absolute path inside the sandbox, so the paths in the
conversation mean the same thing on both sides.

What you will see:

```
  ○ image — pulling…
      a72c2ead8a2a downloaded
      509.3 MB
  ✓ image — ready
  ✓ assembled kit image (21 layers)
    setting 1 environment variable
      +DEEPSEEK_API_KEY
  ✓ http — published localhost:32777 -> 8080/tcp

  ✓ Created sandbox openheron
  ○ openheron — starting in sandbox openheron…
      workspace /home/you/my-project
```

★ **`http — published localhost:32777 -> 8080/tcp` is the runtime telling you
the port is ready.** The workload declares the port it serves on, and the
runtime publishes it on an ephemeral loopback port at start time. You never
choose that port yourself — two Kits asking for the same one would collide.

★ **The terminal stays busy.** The workload's entrypoint is the server
itself, and the session is what starts it. You will see the server's log
lines here.

★ **If you have the source tree instead of a published image**, the workload
can be a local directory, and then nothing is pulled:

```sh
sbx run ./sandbox-kit/openheron \
    --kit ./openheron-deepseek \
    --name openheron ~/my-project
```

---

## 4. Open it

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32777       8080           tcp
::1         32777       8080           tcp
```

Open **http://127.0.0.1:32777** in a browser. That is the full openheron
interface: the chat, the agents, the settings editor. The web interface and
the API share the port, so there is nothing to configure.

★ Ask it something. The answer streams back as it is generated — you will see
the words appear, not the finished text at once.

---

## 5. Leave it running

★ **You can close the terminal and the server keeps serving.** Press
`Ctrl-\` to leave the session while the sandbox keeps running:

```
^\
```

The sandbox stays up with the port still published. You can close the
terminal window; the next time you want it, see step 7.

★ Without `long-running@1` the sandbox would stop as soon as its last client
disconnected. The workload declares it, so the server is a background
workload rather than a session.

---

## 6. The configuration openheron writes

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

## 7. Come back later

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

---

## 8. Another vendor

Add a second mixin — the same file with the vendor's names in it:

```yaml
# syntax=docker/sandbox-kit:3
schemaVersion: "3"
kind: mixin
displayName: openheron credential (OpenAI)
description: the OpenAI model credential for an openheron workload, injected by the host proxy
licenses: [Apache-2.0]

capabilities:
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - api.openai.com

  - type: com.docker.sandbox/credential@1
    optional: true
    description: OpenAI API access
    config:
      service: openai
      phase: runtime
      apiKey:
        name: OPENAI_API_KEY
        proxyManaged: true
        inject:
          - {domain: api.openai.com, header: Authorization, format: "Bearer %s"}
```

Bind that key too, and compose the two mixins:

```sh
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run docker.io/ouranoshong/openheron-kit:0.0.6 \
    --kit ./openheron-deepseek \
    --kit ./openheron-openai \
    --name openheron ~/my-project
```

★ Both keys arrive in the container as sentinels, each with its own network
rule. What the **provider configuration** does is your business:
`openheron init` maintains one provider configuration per configuration root,
so the second vendor's `llm/*.yaml` is written by you, on the mount.

---

## 9. Clean up

```sh
sbx rm openheron
```

★ The sandbox, its container and its published port all go. The workspace
stays on your host — `.openheron/` included — so the next run picks up where
this one left off.

---

## If something goes wrong

| What you see | What it means |
|---|---|
| `error: resolve kits: kit "…": its source is not in your allowlist` | the Kit is not on `docker.io/` — an administrator has to allow its source |
| `error: … fetch manifest: … http: server gave HTTP response to HTTPS client` | the registry serves plain HTTP; it has to serve HTTPS |
| `error: sandbox '…' already exists; --kit can only be used when creating a new sandbox` | you passed `--kit` to an existing sandbox — drop it, or `sbx rm` first |
| `error: agent exited with code 2` | the server was already running; the one you want is still up |
| `error: resolve kits: kit "…": artifact: spec.yaml (or spec.yml) is required` | you put a path where the agent goes; `sbx run --name <name>` takes no path |
| the browser shows "connection refused" | the port changed — `sbx ports <name>` again, or the sandbox stopped |
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

The design behind all of this, and every assumption it rests on, is in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md). The repeatable
version of this guide is
[`sandbox-kit/acceptance/run.sh`](acceptance/run.sh) — every claim above is a
check there, and the check fails the run when it does not hold.

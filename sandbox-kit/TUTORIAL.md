# Using openheron in a Docker Sandbox

A step-by-step guide for running openheron inside a sandbox, on a machine
where you do not want its agents to have the run of your files.

★ **What this gives you.** The whole openheron process — the HTTP/SSE server,
the file tools and the shell tool — runs inside a microVM. Your host is
outside that boundary. The workspace you name is the one thing that crosses
it, and it crosses it as an ordinary directory on your host.

★ **What it does not give you.** The sandbox is not a login shell. You open
the server in a browser, exactly as you would on your own machine.

---

## 0. Before you start

You need three things.

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

## 1. Get the Kit

★ **If openheron is installed from a release, the Kit is in the source tree.**
It lives under `sandbox-kit/` in this repository:

```
sandbox-kit/
  openheron/           the workload — the server itself
  openheron-deepseek/  a mixin — the DeepSeek credential
  openheron-openai/    a mixin — the same, for OpenAI
```

You do not have to build it. `sbx run` builds the Kit itself when it needs
one, and caches it. Build it yourself only when you want the image ahead of
time:

```sh
make kit         # the workload
make kit-mixin   # every mixin
```

★ Both use a buildx builder of their own, and both export into the local
docker store. The default builder drops the manifest annotations that make an
image a Kit, so `make kit` names a container-driver builder instead.

---

## 2. Start a sandbox

```sh
sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --name openheron ~/my-project
```

Read the three arguments apart:

| | |
|---|---|
| `./sandbox-kit/openheron` | the workload — the root filesystem the agent works in |
| `--kit ./sandbox-kit/openheron-deepseek` | a mixin — the credential and the network rule that goes with it |
| `~/my-project` | the workspace — a directory on your host, mounted into the sandbox |

★ **The workspace is a real directory on your host.** Point it at a project
you want the agent to work on, or at an empty directory to start from. It
appears at the same absolute path inside the sandbox, so the paths in the
conversation mean the same thing on both sides.

What you will see:

```
  ✓ http — published localhost:32777 -> 8080/tcp
  ✓ Created sandbox openheron
  ○ openheron — starting in sandbox openheron…
      workspace /home/you/my-project
```

★ **`http — published localhost:32777 -> 8080/tcp` is the runtime telling you
the port is ready.** The Kit declares the port it serves on, and the runtime
publishes it on an ephemeral loopback port at start time. You never choose
that port yourself — two Kits asking for the same one would collide.

★ **The terminal stays busy.** The Kit's entrypoint is the server itself, and
the session is what starts it. You will see the server's log lines here.

---

## 3. Open it

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

## 4. Leave it running

★ **You can close the terminal and the server keeps serving.** Press
`Ctrl-\` to leave the session while the sandbox keeps running:

```
^\
```

The sandbox stays up with the port still published. You can close the
terminal window; the next time you want it, see step 6.

★ Without `long-running@1` the sandbox would stop as soon as its last client
disconnected. The Kit declares it, so the server is a background workload
rather than a session.

---

## 5. Work on the files

Everything the agent reads and writes is inside the sandbox — but the
workspace is your directory, so you can watch it from the host:

```sh
ls ~/my-project
```

★ On the first start, openheron seeds its own configuration there:

```
~/my-project/.openheron/
  agents/main.md      the entry agent
  agents/task.md      the executor agent
  llm/deepseek.yaml   the model profile
  policy/             permissions and limits
  server/             the server's own settings
  state/              the sessions
```

★ **Those are your files.** Edit `llm/deepseek.yaml` with your own editor,
change the model or the base URL, and openheron picks it up. The
configuration is not hidden inside the sandbox — it is on your mount, in
plain sight.

★ **The sessions survive.** `state/` is on the mount too, so the conversations
are still there after you stop and recreate the sandbox.

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

---

## 7. Another vendor

Add the mixin and bind that key:

```sh
printf '%s' "$OPENAI_API_KEY" | sbx secret set-custom \
    --host api.openai.com --env OPENAI_API_KEY \
    --placeholder proxy-managed --value-stdin

sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --kit ./sandbox-kit/openheron-openai \
    --name openheron ~/my-project
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

## If something goes wrong

| What you see | What it means |
|---|---|
| `error: sandbox '…' already exists; --kit can only be used when creating a new sandbox` | you passed `--kit` to an existing sandbox — drop it, or `sbx rm` first |
| `error: agent exited with code 2` | the server was already running; the one you want is still up |
| `error: inspect exec: context deadline exceeded` | the same thing, seen from the other end |
| `error: resolve kits: kit "…": artifact: spec.yaml (or spec.yml) is required` | you put a path where the agent goes; `sbx run --name <name>` takes no path |
| the browser shows "connection refused" | the port changed — `sbx ports <name>` again, or the sandbox stopped |
| the port is gone after a restart | `sbx rm` takes the published ports with it; the runtime republishes on the next start |
| the model call fails with 401 | the key was never bound — redo step 0 |
| the model call fails with a connection error | the credential's network rule is missing — the mixin carries it, so check you passed the mixin |

---

## One more thing to know

★ **The sandbox is the boundary; the allow list is not.** This Kit does not
restrict what the agent may reach — the microVM is what isolates it, and the
host's own policy (`sbx policy`) is what governs egress. If you want to
narrow that, narrow it on the host, not by editing the Kit.

---

The design behind all of this, and every assumption it rests on, is in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md). The repeatable
version of this guide is
[`sandbox-kit/acceptance/run.sh`](acceptance/run.sh) — every claim above is a
check there, and the check fails the run when it does not hold.

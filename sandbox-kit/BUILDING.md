# Building and publishing the openheron sandbox Kit

For the person changing the Kit rather than running it. If you want to **use**
openheron in a sandbox, read [`TUTORIAL.md`](TUTORIAL.md) instead — the Chinese version is
[`TUTORIAL_zh.md`](TUTORIAL_zh.md) — and it needs no source tree and no build.

★ **What lives here**

```
sandbox-kit/
  openheron/                    the workload
    openheron.yaml              the descriptor: args, capabilities, the startup hook
    openheron.dockerfile        the content recipe: the base image and the binary
    entrypoint.sh               the launch script
  openheron-deepseek/           a mixin: the DeepSeek credential and its network rule
    openheron-deepseek.yaml
  openheron-openai/             a mixin: the same, for OpenAI
    openheron-openai.yaml
  acceptance/run.sh             the acceptance run
```

★ **A Kit is one OCI image.** The descriptor's `capabilities` are its
declarations; the image's layers are its content. The workload carries the
root filesystem, the mixins carry declarations and no content.

---

## 1. Prerequisites

```sh
sbx --version      # 0.42.0 or later
docker --version
bun --version      # the frontend toolchain: task kit runs task build
```

★ The version floor is not cosmetic: 0.42.0 fixed `CVE-2026-77179` and
`CVE-2026-79994`, two guest-to-host escapes, and the mitigation Docker
documents is a mode openheron cannot use (§2.14 of the design).

★ **A builder of its own.** The build needs a container-driver buildx
builder, and every Kit target creates one if it is missing:

```sh
task kit-builder
```

★ **Why not the default builder.** The `docker` driver with the classic
image store drops the manifest annotations that make an image a Kit — the
image builds, and it is no longer a Kit. A container-driver builder keeps
them.

---

## 2. Build

```sh
task kit         # the workload: openheron-kit:<version>
task kit-mixin   # every mixin: openheron-kit-<vendor>:<version>
```

★ **`<version>` is `KIT_VERSION` in the Taskfile** — the one place the
version lives. The build stamps it into the descriptor and into the image
tag, so the image you get locally is the one the release publishes.

★ Both export into the local docker store. Without the export there is no
local image to validate, and `task kit-check` would look for an image that
was never exported.

★ **The binary is built on the host, not in the image.** `task kit` runs
`task build` first — the frontend, the embedded binary, the same code this
repository builds — and copies the result beside the recipe:

```dockerfile
COPY openheron /usr/local/bin/openheron
RUN chmod 0755 /usr/local/bin/openheron
```

★ **Two consequences.** The Kit carries the **current source**, not a
release: there is no release to wait for, and the binary inside reports
`git describe` rather than a release tag. And the build needs the **frontend
toolchain** — `task build` runs vite — so building a Kit takes more than
docker does.

★ **The binary is copied beside the recipe** because a build context cannot
reach outside the directory it is given: the repository's `bin/openheron` is
out of reach from the Kit's own directory, and the copy is what makes
`sbx run ./sandbox-kit/openheron` build from here too.

★ **That binary is dynamically linked**, where the release binaries are
static (`CGO_ENABLED=0`). The base image is Ubuntu, so it has the libc the
binary wants; the cost is a larger binary and a Kit bound to its base image's
libc.

---

## 3. Validate

```sh
task kit-check   # kit-tck validate against the specification
```

★ `kit-tck` is the conformance suite from the specification's repository. It
reads the image, so it needs the local export from step 2.

---

## 4. Run it from the source tree

```sh
sbx run ./sandbox-kit/openheron \
    --kit ./sandbox-kit/openheron-deepseek \
    --name openheron ~/my-project
```

★ A local directory is a valid Kit reference, and the runtime builds it on
demand — so this path needs no `task kit` at all. It is how you test a
descriptor change before publishing it.

★ **`--kit` takes a directory here, not an image.** A mixin directory holds
one YAML file and nothing else; the runtime builds the declaration-only
image from it.

★ **The session is what starts the server.** `--detach` creates the sandbox
and runs the startup hooks, and leaves the server down: the entrypoint is the
agent's launch command, and no session means nothing launches it. Open the
session, watch the server come up, then detach from it — `long-running@1`
keeps it running afterwards.

---

## 5. Publish

```sh
task kit-push kit-push-mixin
```

★ **The mixins live in their own repositories**, and they have to: a Kit is
identified by its reference, and the runtime resolves each reference in the
set separately. A workload published without its mixins leaves a user
unable to reach a model — the Kit runs, and every call comes back 401.

★ The three land on Docker Hub under `ouranoshong/`:

```
docker.io/ouranoshong/openheron-kit:latest
docker.io/ouranoshong/openheron-kit-deepseek:latest
docker.io/ouranoshong/openheron-kit-openai:latest
```

★ **The repositories have to exist first.** Docker Hub's API creates them:

```sh
curl -s -X POST "https://hub.docker.com/v2/repositories/ouranoshong/" \
  -H "Authorization: JWT $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"openheron-kit","is_private":false,"namespace":"ouranoshong"}'
```

### In CI

★ **A tag publishes the Kit.** The `kit` job in
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) builds the workload
and every mixin and pushes them, and it runs on a `v*` tag and on nothing
else:

```yaml
needs: [go, web, e2e, secrets]
if: startsWith(github.ref, 'refs/tags/v') || inputs.tag != ''
```

★ **It waits for the same checks the release waits for**, including the
secret scan: a tag on a red commit publishes no image, and a tag whose commit
holds a key publishes none either.

★ **It needs two secrets**: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`. The
token is a Docker Hub access token — a personal access token or a read-write
organization token — not the account password.

★ **The image is tagged without the `v`**: the release is `v0.1.0` and the Kit
is `0.1.0`, so the reference in the guide is the tag. The version reaches the
**published descriptor** as well, because `task kit` builds in a copy of the
Kit's directory with the version written into it — the working tree is never
left modified.

★ **The Kit is one architecture.** The binary is built on the runner, so the
image is the runner's architecture — `linux/amd64` for the GitHub runners.
A multi-platform Kit would need a cross-compiled binary, and the current one
is dynamically linked.

★ **The tag is the version, and the version is the release.** The Kit's
`version` argument is the release tag it installs, and `task kit-push` tags
the image with that same version. A Kit that names a release nobody
published is a Kit nobody can build — so publish the release first, then push
the Kit that names it.

★ **The push is slow for the workload and instant for the mixins.** The
workload is the sandbox template plus the binary; the mixins are
declarations.

---

## 6. Change the descriptor

`sandbox-kit/openheron/openheron.yaml` is the contract. The parts that
matter:

```yaml
args:
  version:
    default: "<version>"
    pattern: '^[0-9]+(\.[0-9A-Za-z-]+)*$'
    description: version label for this build of the Kit
    buildArg: OPENHERON_VERSION

provides: ["openheron@${{ kit.args.version }}"]
version: "${{ kit.args.version }}"

capabilities:
  - type: com.docker.sandbox/port@1
    config:
      name: http
      container: 8080
      transport: tcp
```

★ **The snippet is a copy, so it can drift — the file is the truth.** When the
two disagree, the descriptor wins; a copy that silently ages is worse than no
copy at all, because it reads as current.

★ **`buildArg` is how the version reaches the recipe.** The recipe reads
`${OPENHERON_VERSION}`; the descriptor's `buildArg` is what puts the
argument's value there. Building the recipe by hand with
`docker buildx build -f openheron.dockerfile` skips that injection and the
download 404s.

★ **`port@1` is not optional.** A workload that serves has to declare the
port it serves on: without the capability the runtime has no service to
publish, and a hand-made `sbx ports --publish` can only bind a host socket
with nothing behind it — which a client sees as a connection accepted and
then closed.

★ **The family is left at its default**, and that is the truth: openheron
listens on `[::]:8080`, a dual-stack socket that accepts IPv4 too, so a
client reaching the published IPv4 binding reaches the service. Declaring
`dual` says the same thing and is refused by the runtime:

```
error: resolve kits: kit "./sandbox-kit/openheron": port 8080: family "dual"
  is not supported yet; omit it
```

★ **A rejected descriptor is a sandbox that never starts**, so declare what
the runtime implements today.

★ **The mixin carries the network rule with the credential.** The runtime
wants every inject domain to appear **literally** in the allow list for the
same phase; the workload's bare `*` covers every domain but does not satisfy
that check. The two allow lists union per phase.

---

## 7. The acceptance run

```sh
sandbox-kit/acceptance/run.sh
```

★ It runs the whole thing — preflight, a clean sandbox, the configuration on
the mount, the credential, a real model call, the shape of the stream, the
client's departure, and the port from every vantage point — and exits
non-zero if any check fails. The numbers it produced are in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) §6.6.

★ **The transcript is not part of the tree.** It is a run artifact: absolute
paths, an ephemeral port, frame counts, the model's own words — and every
rerun would contradict the numbers the document quotes. `--out` writes it
wherever you want it.

---

## When the build misbehaves

| Symptom | Cause |
|---|---|
| `kit-tck` finds no image to validate | the build did not export — `--load` is required, and it is in the recipe |
| the image builds but the runtime does not recognise it as a Kit | the manifest annotations are gone — use a container-driver builder |
| the download 404s during the build | the version names a release that was never published |
| `openheron: not found` during the build | the binary was not copied in — `task kit` does that, so build the Kit with `task kit` and not with buildx directly |
| `family "dual" is not supported yet` | the runtime does not implement it; omit the field |
| `spec.yaml (or spec.yml) is required` | `sbx kit pack` is the **v1/v2** artifact grammar; a v3 Kit travels as an OCI image or a directory |
| `KVM error: Permission denied` | the daemon serving the socket is not the one with `/dev/kvm` access — see §6.2 of the design |

---

The design behind all of this, and every assumption it rests on, is in
[`docs/sandbox-kit-design.md`](../docs/sandbox-kit-design.md) — that document is Chinese
only, like every other design document in this repository.

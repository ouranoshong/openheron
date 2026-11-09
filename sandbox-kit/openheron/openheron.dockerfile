# syntax=docker/dockerfile:1
#
# The content recipe for the openheron workload Kit.
#
# ★ The binary is built on the **host**, not in here. `task kit` runs
#   `task build` first, and this recipe copies the result. Two reasons: the
#   binary is then the one this repository builds — the frontend embedded,
#   the same code — and the image carries no Go toolchain it would never
#   run.
#
# ★ That binary is **dynamically linked**, where the release binaries are
#   static (`CGO_ENABLED=0`). The base image is Ubuntu, so it has the
#   libc the binary wants; the cost is a larger binary and a Kit that is
#   bound to the base image's libc rather than to nothing at all.
#
# ★ The build context is **this directory**: the binary is copied in beside
#   the recipe by `task kit`, next to the entrypoint it copies too.

FROM docker/sandbox-templates:shell

USER root

# ★ The binary `task build` produced. It is a local build, so it carries the
#   host's architecture — a Kit built on an amd64 host is an amd64 image.
COPY openheron /usr/local/bin/openheron
RUN chmod 0755 /usr/local/bin/openheron

# ★ The entrypoint is copied as root: the agent user could not chmod a
#   root-owned file.
COPY entrypoint.sh /usr/local/bin/openheron-entrypoint
RUN chmod 0755 /usr/local/bin/openheron-entrypoint

USER agent
WORKDIR /home/agent/workspace
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/openheron-entrypoint"]

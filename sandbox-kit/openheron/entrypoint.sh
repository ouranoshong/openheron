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
#
# ★ The config root is named explicitly: the default is <openheron-root>/configs,
#   which lives in the container's own filesystem, and the configuration the
#   user edits is the one on the mount. Naming it is what makes the two the
#   same directory.
export OPENHERON_CONFIG_ROOT="$PWD/.openheron"
openheron init --config-root "$OPENHERON_CONFIG_ROOT" || \
    echo "[entrypoint] init did not complete; the configuration was left as it was, and with none at all the server will not start"

# ★ The sandbox publishes the workload's port, so the server inside has to
#   listen on every interface: the default is the loopback, and the runtime
#   outside the microVM cannot reach a loopback.
#
#   ★ "[::]" and not "0.0.0.0": it is a dual-stack socket, so it accepts the
#     IPv4 connection the runtime publishes (the descriptor declares the
#     family as ipv4). The descriptor's comment says exactly this.
export OPENHERON_LISTEN_ADDR="[::]:8080"

# ★ The state lives on the mount, not in the sandbox's own filesystem: the
#   openheron root defaults to ~/.openheron, which disappears with the
#   sandbox, and a conversation that does not survive the sandbox is a
#   conversation the user lost. The data dir is named, so it is on the mount.
export OPENHERON_DATA_DIR="$PWD/.openheron/state"

# ★ The workspace is the project: the agent works where the user's files are.
exec openheron "$PWD"

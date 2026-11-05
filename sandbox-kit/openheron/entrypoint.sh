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
    echo "[entrypoint] init did not complete; the configuration was left as it was, and with none at all the server will not start"

exec openheron

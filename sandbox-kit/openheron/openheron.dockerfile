# syntax=docker/dockerfile:1
#
# The content recipe for the openheron workload Kit.
#
# ★ The base provides the platform floor: the `agent` user at uid 1000,
#   /home/agent, bash, git and a populated CA store. Nothing is installed on
#   top of it, so apart from fetching the binary the build needs no network —
#   and an apt step here would only add a mirror that can fail.

FROM docker/sandbox-templates:shell

ARG OPENHERON_VERSION
ARG TARGETARCH

USER root

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

#!/bin/bash
# Entrypoint for the controlled-egress proxy.
#
# This replaces the one shipped in ubuntu/squid, whose final line is:
#
#     /usr/sbin/squid "$@"
#
# without exec. That leaves the shell as PID 1 with squid as a child, and a
# shell does not forward signals to its children — so `podman stop` / `docker
# stop` delivered SIGTERM to bash, squid never saw it, and every shutdown ended
# in SIGKILL after the runtime's 10s timeout:
#
#     StopSignal SIGTERM failed to stop container telos-egress-proxy in 10
#     seconds, resorting to SIGKILL
#
# Using exec makes squid itself PID 1, so it receives SIGTERM directly and
# honors shutdown_lifetime (set to 5 seconds in config/egress/squid.conf,
# comfortably inside the runtime's timeout).
set -e

# Create any cache directories the configuration asks for. The Telos policy
# sets "cache deny all" with no cache_dir, so this is a no-op today; it is kept
# so the entrypoint stays correct if caching is ever enabled.
/usr/sbin/squid -Nz

# Squid writes its logs to files; forward them to the container's stdout so
# `podman logs` works. These become children of squid once we exec, and the
# runtime tears them down with the container.
tail -F /var/log/squid/access.log 2>/dev/null &
tail -F /var/log/squid/cache.log 2>/dev/null &

# exec: squid replaces this shell as PID 1 and receives signals directly.
exec /usr/sbin/squid "$@"

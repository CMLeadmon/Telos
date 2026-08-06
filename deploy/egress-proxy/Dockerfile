# Telos controlled-egress proxy: a minimal Squid with the deny-by-default policy
# and reviewed-domain allowlist baked in. The only container that joins the
# non-internal telos-egress network.
FROM docker.io/ubuntu/squid:latest

# The policy and allowlist are provided read-only at runtime by Compose:
#   ./config/egress/squid.conf         -> /etc/squid/squid.conf
#   ./config/egress/allowed-domains.txt -> /etc/squid/allowed-domains.txt
# They are copied here as a fallback so the image is self-contained if run
# without the mounts.
COPY config/egress/squid.conf /etc/squid/squid.conf
COPY config/egress/allowed-domains.txt /etc/squid/allowed-domains.txt

EXPOSE 3128

# Replace the upstream entrypoint, which starts squid without exec and so
# leaves a shell as PID 1 that never forwards SIGTERM — making every shutdown a
# 10-second wait ending in SIGKILL. See the script for detail.
COPY deploy/egress-proxy/entrypoint.sh /telos-entrypoint.sh
RUN chmod +x /telos-entrypoint.sh
ENTRYPOINT ["/telos-entrypoint.sh"]
# CMD must be restated: declaring ENTRYPOINT resets any CMD inherited from the
# base image to empty. Without this, "$@" in the entrypoint is empty, squid
# starts in daemon mode, forks to the background, and the foreground process
# exits 0 — so the container exits and the restart policy loops forever. -N
# keeps squid in the foreground, which is what makes it a well-behaved PID 1.
CMD ["-f", "/etc/squid/squid.conf", "-NYC"]

# Validate the configuration at build time so a broken policy fails fast.
RUN squid -k parse -f /etc/squid/squid.conf

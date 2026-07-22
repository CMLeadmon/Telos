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

# Validate the configuration at build time so a broken policy fails fast.
RUN squid -k parse -f /etc/squid/squid.conf

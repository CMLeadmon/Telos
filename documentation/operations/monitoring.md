# Telos Monitoring and Metrics Architecture

Telos instruments backend services, real-time channels, database connectivity, and host operational state using Prometheus metrics.

## Internal Metrics Endpoint
- Exposed exclusively on internal port `9090` at `/metrics`.
- Never routed through public Traefik entrypoints.

## Metrics Privacy Rules
- No PII, usernames, channel names, or request parameters are permitted as metric labels.

# Telos Incident Response Runbooks

This document provides incident runbooks for operators responding to alerts fired by the Telos monitoring system.

## Incident Runbooks

### 1. Dependency or Service Down (`TelosDependencyDown`)
1. Inspect container health using `docker compose ps`.
2. Check backend logs using `docker compose logs -f telos-core`.
3. Restart failed services using `docker compose restart <service>`.

### 2. High Outbox Queue Size
1. Check Redis status and outbox worker log output.
2. Confirm database connection pool health.

# Telos Network Exposure Audit Guide

> **Status:** the audit is implemented and was rehearsed on the reference VMs (an external scan of all 65 535 TCP
> ports and 19 UDP ports found only Traefik's 80 and 443 and the VM's declared SSH). That is not certification evidence:
> the `host-exposure` gate needs a frozen candidate, a real scanning host on another network and the operator's
> declared management ports.

## What Telos exposes

| | |
|---|---|
| Public TCP | **80 and 443**, published by Traefik alone (`telos-traefik`) |
| Public UDP | **none** (no media, discovery or DNS port) |
| Everything else | PostgreSQL, Redis, MariaDB, ClamAV, Jellyfin, Grimmory, the gateway, the static client, the metrics listener and Prometheus stay on container networks; none publishes a host port |
| Host management | **not Telos's.** An SSH port or similar is declared by the operator in the policy file and is never labelled a Telos port |

`telos-backend`, `telos-db` and `telos-observability` are Podman `internal` networks (no route off the node);
`telos-ingress` and `telos-egress` are routable and only the services named in the policy join them
(`config/exposure-policy.json`, section `telos.networks`).

## The policy

`config/exposure-policy.json` states the public ports, the ports that must never be reachable (with what listens
there), the guided-setup containers that must not be left running, the network design, `operatorDeclared`
(`tcp`/`udp` lists, empty in the shipped file) and the scan scope (all TCP ports, a fixed UDP list). The operator copies
it, fills `operatorDeclared` for their host (for example `{"tcp": [22], "udp": []}`) and passes the copy to the audit.
A declared port may not be 80/443 or any Telos-internal port.

## Running it

```bash
# Static, anywhere: the policy is well formed and Compose publishes only Traefik on 80/443
# (every overlay -- setup, dev, test -- on 127.0.0.1 only).
bash scripts/audit-host-exposure.sh --source --policy config/exposure-policy.json

# Live, from a machine that is NOT the node (it needs nmap, and sudo or root for the UDP scan):
bash scripts/audit-host-exposure.sh --target-file TARGET.json --policy OPERATOR-POLICY.json \
  (--rehearsal --source-commit SHA | --candidate-lock LOCK) --result-out OUT/result.json
```

The live audit proves, in order: the node's machine identity is the target's and the scanner is not that machine;
the origin resolves to an address the node owns; only Traefik publishes, only TCP 80/443 on all addresses; no setup
relay or setup port is left behind; the internal networks are internal and only declared services join the routable
ones; every listener a machine outside the host could reach is 80/443 or declared (loopback and container-network
gateways such as container DNS are not routable); an external scan of **every TCP port** and the UDP list finds
only 80/443 and declared ports, and everything reachable from outside is a listener the node itself reports.
A missing scanner is `not_run`; an undeclared open port is a failure whoever owns it.

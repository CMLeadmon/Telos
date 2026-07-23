# Telos Network Exposure Audit Guide

This document details permitted open host ports and internal service boundaries across the Telos platform stack.

## Allowed Public Ports
- TCP 80 / 443 (HTTP/HTTPS via Traefik)
- TCP 7881 (LiveKit RTC)
- UDP 3478 / 50000-50100 (TURN and LiveKit media streams)

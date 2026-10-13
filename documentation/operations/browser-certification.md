# Telos Supported Browser and Responsive Certification Guide

Browser coverage remains cross-platform despite the Ubuntu-only server policy.
Windows/Mac browser users, desktop Safari, and mobile browsers remain in scope.
See [platform scope](../product/platform-scope.md). A Playwright WebKit run is
useful automation, but does not replace real Safari/device evidence.

Browser certification is not implemented or claimable in the current
repository. `scripts/certify-browser-matrix.sh` deliberately returns `not_run`
(exit 3) until declared browser hosts, a candidate URL, and the owning evidence
workflow are available.

## Matrix Cells
- Google Chrome (Stable)
- Mozilla Firefox (current stable)
- Microsoft Edge (Stable)
- WebKit (Playwright automation)
- Mobile Chrome / Mobile Safari Emulation
- Physical iOS Safari / Android Chrome

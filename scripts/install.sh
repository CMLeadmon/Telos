#!/usr/bin/env bash
# Superseded. The real installer lives at the repository root.
#
# This file previously printed "Installation completed successfully." without
# doing any work, which is worse than not existing: it is the first thing a new
# user runs, and it told them they were installed when they were not. It now
# redirects and exits non-zero so nothing can mistake it for success.
set -euo pipefail

cat >&2 <<'EOF'
scripts/install.sh is no longer the installer.

Use the installer at the repository root instead:

    ./install.sh            # install the telos CLI
    ./install.sh --check    # verify host prerequisites only

Then:

    telos init              # generate .env and create the storage tree
    telos start             # bring the stack up
EOF
exit 2

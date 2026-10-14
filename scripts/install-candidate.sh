#!/usr/bin/env bash
# install-candidate.sh -- install a verified candidate as an immutable generation on this node.
#
#   sudo scripts/install-candidate.sh --candidate DIR [--activate] [--release-base /srv/telos] [--node /var/lib/telos]
#
# DIR is the extracted candidate (telos-<version>-candidate.tar.zst). The release graph is verified, the OCI
# archives are loaded and proven to resolve by digest, and the generation is copied to
# <release-base>/generations/<candidate digest>. --activate only for a node with no active generation;
# moving between generations is upgrade.sh. Nothing is built, pulled or fetched.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 -I "$script_dir/lib/candidate_install.py" install "$@"

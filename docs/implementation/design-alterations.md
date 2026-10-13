# Standalone beta: design alterations

Every deliberate departure from the plans, with the reason, the files that carry it and
how to reverse it. Authorized by the project owner on 2026-10-13 ("make do with what you
have; progress is more important than strict adherence; inform me of the design alterations
after implementation"). Newest last.

| # | Alteration | Why | Where | Reverse by |
|---|---|---|---|---|
| A1 | The **verified reference release is Ubuntu 26.04 LTS**; the policy floor stays 24.04. The preflight labels every other release "not yet a verified combination". | The only Podman-capable environment available was a 26.04 VM; its archive ships Podman 5.7.0, so a Podman 5 source became *verified* there, whereas none exists for 24.04. | `cli/lib/preflight.sh` (`TELOS_UBUNTU_REFERENCE`), `scripts/lib/clean_install_drill.py`, `documentation/product/platform-scope.md` (amendment), `install.md`, `clean-install-evidence.md`, tests | Set the constant back and re-run the live checks on 24.04 once a Podman 5 source is verified |
| A2 | Podman-dependent gates run on a **user-space KVM VM** (QEMU extracted from apt `.deb` files, signed Ubuntu cloud image) instead of a bare-metal clean host. Evidence from it is a labeled VM-hosted rehearsal, never candidate evidence. | No sudo on the host; `/dev/kvm` was ACL-accessible. | `~/.local/telos-vm/` (outside the repository) | n/a — replace with a real host |

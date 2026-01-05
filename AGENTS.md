# AGENTS.md

<INSTRUCTIONS>
- Use `jj` for VCS operations; do not use `git` unless explicitly requested.
- Work with worktrees where appropriate (assume multi-worktree setups by default).
- Use `jj` from a separate workspace/worktree for any changes in this repo.
- This repo is a Clan (Nix) config repo; host configs live under `machines/<host>/configuration.nix`.
- The repo defines a Clan flake with shared modules and inventory-based services to manage multiple NixOS machines.
- `itppeach` has IPv4 `130.92.184.229` in `networking.interfaces.enp7s0.ipv4.addresses` and `clan.core.networking.targetHost`.
</INSTRUCTIONS>

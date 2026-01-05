# AGENTS.md

<INSTRUCTIONS>
- Use `jj` for VCS operations; do not use `git` unless explicitly requested.
- Always run `jj describe` before making changes.
- Work with worktrees where appropriate (assume multi-worktree setups by default).
- Use `jj` from a separate workspace/worktree for any changes in this repo.
- This repo is a Clan (Nix) config repo; host configs live under `machines/<host>/configuration.nix`.
- The repo defines a Clan flake with shared modules and inventory-based services to manage multiple NixOS machines.
- `itppeach` has IPv4 `130.92.184.229` in `networking.interfaces.enp7s0.ipv4.addresses` and `clan.core.networking.targetHost`.
- Common/shared machine settings live in `modules/shared.nix`; both `itppeach` and `itphlies` import it.
- Tailscale is managed via a custom Clan service in `service-modules/tailscale.nix` and wired through `inventory.instances.tailscale` in `flake.nix`.
- Tailscale auth uses a shared vars generator `clan.core.vars.generators.tailscale-auth-key` with `share = true` (sops-backed secret).
- Use Clan inventory roles (`inventory.instances.<name>.roles.<role>`) to assign services to machines instead of duplicating per-host config.
- Secrets/vars are stored via Clan (sops) under `sops/` and generated with `clan vars generate`.
</INSTRUCTIONS>

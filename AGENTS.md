# AGENTS.md

<INSTRUCTIONS>
- Use `jj` for VCS operations; do not use `git` unless explicitly requested.
 - To get documentation for using commands (commands using jj), look at https://jj-vcs.github.io/jj/prerelease/cli-reference/.
 - use  --no-pager with `jj log` to avoid paging in terminals that do not support it. (for diff too)
 - Check what jj change is current. If starting a new task, start a new jj change with `jj new`. unless the working copy is empty with no description. if it is not empty but still has no description, `jj diff` and `jj describe` it first.
 - Update the current CL description with `jj describe -m "<description>"`.
 - To see the changed files in a workspace `jj diff`.
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
- In `jj`, the working copy `@` is the active change; keep it and work on top of it.
- Prefer a `jj new` per logical change with `jj describe -m "<message>"`, then `jj squash` to combine later; avoid repeatedly overwriting the same `@` description.
</INSTRUCTIONS>

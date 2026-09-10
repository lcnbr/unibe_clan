# Shared Codex homes

`home.nix` is the common Home Manager source for the ten `codex*` accounts
on itphlies. The deployed shared checkout is `/common/nix/clan`, so every user
in the `users` group can edit:

```text
/common/nix/clan/home-manager/codex/home.nix
```

`nh home switch` automatically selects the caller's entry from the shared
flake and updates that account. A Clan/NixOS deployment applies the same source
to all ten accounts. The `.codex` authentication directory is intentionally
outside Home Manager and is never copied into the repository.

## Updating Codex

The CLI and code-mode host come from the `SecBear/codex-nix` input. Both the
Clan deployment and the standalone Home Manager flake pin that input, so update
the two locks together from the shared workspace:

```console
cd /common/nix/clan
nix develop path:. -c just update-codex
```

Review and build the resulting change before running
`nix develop path:. -c just update-phlies`. The dashboard deliberately keeps an
explicit tested Codex version in `service-modules/codex-usage-dashboard.nix`;
bump it only after validating the new CLI's app-server protocol.

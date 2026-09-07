# Shared Codex homes

`home.nix` is the common Home Manager source for the eight `codex*` accounts
on itphlies. The deployed shared checkout is `/common/nix/clan`, so every user
in the `users` group can edit:

```text
/common/nix/clan/home-manager/codex/home.nix
```

`nh home switch` automatically selects the caller's entry from the shared
flake and updates that account. A Clan/NixOS deployment applies the same source
to all eight accounts. The `.codex` authentication directory is intentionally
outside Home Manager and is never copied into the repository.

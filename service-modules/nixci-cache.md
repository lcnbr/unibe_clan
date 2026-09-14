# NixCI cache

The `nixci-cache` client role lets the Nix daemon download signed builds from
`https://cache.nix-ci.com`, alongside the standard NixOS cache. It also gives every
normal user on the assigned machines read access to a shared NixCI credential.
These users share the token's account and permissions. System service accounts
are excluded from that credential group.

Clan stores the netrc encrypted under `vars/shared/nixci-cache/netrc`. SOPS
installs it as `root:nixci-cache`, mode `0440`, and the login environment exposes
its path through `NIXCI_NETRC`. The daemon uses the same file through
`nix.settings.netrc-file`. Personal `~/.netrc` files are left alone.

In GammaLoop, use `just ci-checks-and-upload` after incorporating the CI changes.
Ordinary builds and other repositories do not automatically upload. This service
adds neither a daemon post-build hook nor trusted Nix users.

## Deploy

The supplied cache credential is already encrypted for the four assigned
machines and the existing `lcnbr` administrator. Review and integrate this change,
then deploy with the usual `clan machines update <machine>` workflow. Users must
start a fresh login session to receive both the group membership and environment.

After logging in, check access without displaying the credential:

```sh
id -nG
test -r "$NIXCI_NETRC"
nix config show | rg '^(substituters|trusted-public-keys|netrc-file) ='
```

The settings should include the NixCI URL, its `nix-ci:` signing key, and the
shared netrc path. Ordinary builds can then reuse matching cached outputs without
accepting per-flake cache settings. Signature verification remains enabled.

## Replace the credential

From a Clan administrator session with the existing SOPS identity, run:

```sh
CLAN_NO_COMMIT=1 clan vars set itphlies nixci-cache/netrc
jj status --no-pager
```

Enter only the netrc entry for `cache.nix-ci.com` at the hidden prompt, including
its `machine`, `login`, and `password` fields. The variable is shared, so changing
it on one machine updates the stored credential for every assigned machine.
Review the encrypted change with `jj`, then redeploy those machines. Never place
plaintext credentials in the flake or a tracked file.

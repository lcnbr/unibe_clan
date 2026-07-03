# itppeach Reachability Report

## Context

`just update-peach` originally expanded to:

```sh
nix develop -c clan machines update itppeach --flake .
```

That used the configured target host `lcnbr@130.92.184.229` from `machines/itppeach/configuration.nix`.
The public SSH path timed out, so the recipe was changed to deploy through the Tailscale address:

```sh
nix develop -c clan machines update itppeach --target-host lcnbr@100.76.100.113 --option pure-eval false --flake .
```

The `--option pure-eval false` override is needed because the config fetches GitHub SSH keys through `builtins.fetchurl` without a fixed hash.

## Deployment Observations

The Tailscale target worked for deployment. Clan reached `itppeach` over `100.76.100.113`, copied the flake source, and ran:

```sh
nixos-rebuild switch --show-trace --option keep-going true --option accept-flake-config true -L --option pure-eval false --flake ...#itppeach --fast
```

The rebuild reached activation and printed:

```text
Done. The new configuration is
```

During activation, systemd also printed warnings about destructive transactions:

```text
Failed to start getty.target: Transaction for getty.target/start is destructive
Failed to start sockets.target: Transaction for sockets.target/start is destructive
Failed to start avahi-daemon.socket: Transaction for avahi-daemon.socket/start is destructive
```

The local Clan wrapper stayed attached after activation, so it was terminated locally after the switch had reached the "Done" line.

## Current Reachability Symptoms

After the update:

```sh
ssh -vvv -o BatchMode=yes -o ConnectTimeout=10 lcnbr@130.92.184.229 true
```

returned:

```text
connect to address 130.92.184.229 port 22: Connection refused
ssh: connect to host 130.92.184.229 port 22: Connection refused
```

Earlier checks to both `130.92.184.229` and `100.76.100.113` timed out. The later public-IP result changed to `Connection refused`, which means the host or an intermediate device is reachable enough to reject TCP port 22, but SSH is not accepting connections.

`itphlies.tailb3264.ts.net` still answered over Tailscale, while `itppeach.tailb3264.ts.net` resolved to `100.76.100.113` and timed out on SSH. That suggests the issue is specific to `itppeach`, not the local Tailscale client or the whole tailnet.

## Diff Review

The deployed parent change was `feat(unibe): add common volume and mosh` and touched:

- `Justfile`
- `flake.nix`
- `modules/shared.nix`
- `modules/user-disko.nix`
- `modules/zfs-user-management.nix`

The SSH-adjacent diff in `modules/shared.nix` added:

```nix
programs.mosh.enable = true;
programs.mosh.openFirewall = true;

networking.firewall.allowedTCPPorts = [
  8080
];
```

Evaluating the effective `itppeach` config showed:

```text
services.openssh.enable = true
services.openssh.openFirewall = true
services.openssh.ports = [ 22 ]
networking.firewall.allowedTCPPorts = [ 22, 8080 ]
programs.mosh.openFirewall = true
networking.firewall.allowedUDPPortRanges = [ { from = 60000; to = 61000; } ]
```

So the static NixOS config still enables SSH and still opens TCP port 22. The firewall addition did not override SSH's firewall opening; Nix merged both ports.

## Most Likely Failure Area

The diff does not appear to disable SSH statically. The symptom points to runtime state after activation:

- `sshd` may be stopped or failed.
- `sshd` may not have restarted cleanly after the systemd transaction warnings.
- The system may be partway through boot or recovery.
- Local firewall/runtime networking may differ from the evaluated config.

One notable activation clue was removal of obsolete network files named for `itphlies` interfaces while deploying `itppeach`, such as:

```text
/etc/systemd/network/40-eno1np0.network
/etc/systemd/network/40-enp193s0f0np0.network
/etc/systemd/network/40-enp193s0f1np1.network
/etc/systemd/network/40-enp193s0f2np2.network
/etc/systemd/network/40-enp193s0f3np3.network
```

That suggests the host may previously have had stale networkd units from another machine config, or had been running a mismatched generation. This should be checked from console.

## Recovery Checks From Console or IPMI

Run these on `itppeach`:

```sh
hostname
ip addr
ip route
readlink /run/current-system
systemctl status sshd
journalctl -u sshd -b --no-pager
ss -ltnp | grep ':22'
```

If `sshd` is not running:

```sh
sudo systemctl restart sshd
sudo systemctl status sshd
```

If the current generation is bad and immediate recovery is needed:

```sh
sudo nixos-rebuild switch --rollback
```

Then re-test externally:

```sh
ssh -vvv -o BatchMode=yes -o ConnectTimeout=10 lcnbr@130.92.184.229 true
ssh -vvv -o BatchMode=yes -o ConnectTimeout=10 lcnbr@100.76.100.113 true
```

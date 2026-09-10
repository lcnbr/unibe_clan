set dotenv-load := true

default:
  @just --list

list:
  nix develop -c clan machines list --flake .

build machine="itppeach":
  nix develop -c clan machines build {{machine}} --flake .

update machine="itppeach":
  nix develop -c clan machines update {{machine}} --flake .

update-bowser:
  just update itpbowser

update-mario:
  just update itpmario

update-peach:
  just update itppeach

update-phlies:
  just update itphlies

update-codex:
  #!/usr/bin/env bash
  set -euo pipefail
  nix flake update codex-nix --flake path:.
  codex_revision="$(nix eval --impure --raw --expr '(builtins.fromJSON (builtins.readFile ./flake.lock)).nodes."codex-nix".locked.rev')"
  nixpkgs_revision="$(nix eval --impure --raw --expr 'let lock = builtins.fromJSON (builtins.readFile ./flake.lock); input = lock.nodes.root.inputs.nixpkgs; in (builtins.getAttr input lock.nodes).locked.rev')"
  nix flake update codex-nix nixpkgs --flake path:./home-manager/codex \
    --override-input codex-nix "github:SecBear/codex-nix/${codex_revision}" \
    --override-input nixpkgs "github:nixos/nixpkgs/${nixpkgs_revision}"
  root_drv="$(nix eval --raw path:.#packages.x86_64-linux.codex-cli.drvPath)"
  home_drv="$(nix eval --raw path:./home-manager/codex#packages.x86_64-linux.codex-cli.drvPath)"
  [[ "$root_drv" == "$home_drv" ]] || { echo "Codex derivation mismatch" >&2; exit 1; }

ssh machine="itppeach":
  nix develop -c clan ssh {{machine}} --flake .

select selector:
  nix develop -c clan select --flake . {{selector}}

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

ssh machine="itppeach":
  nix develop -c clan ssh {{machine}} --flake .

select selector:
  nix develop -c clan select --flake . {{selector}}

{
  description = "Unibe Clan";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    home-manager.url = "github:nix-community/home-manager";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";
    zen-browser.url = "github:0xc000022070/zen-browser-flake";

    nixos-cosmic.url = "github:lilyinstarlight/nixos-cosmic";
    # New flake-parts input
    flake-parts.url = "github:hercules-ci/flake-parts";
    flake-parts.inputs.nixpkgs-lib.follows = "nixpkgs";
    impermanence.url = "github:nix-community/impermanence";
    clan-core = {
      url = "git+https://git.clan.lol/clan/clan-core";
      inputs.nixpkgs.follows = "nixpkgs"; # Needed if your configuration uses nixpkgs unstable.
      # New
      inputs.flake-parts.follows = "flake-parts";
    };
  };

  outputs = inputs @ {
    flake-parts,
    clan-core,
    impermanence,
    home-manager,
    nixos-cosmic,
    ...
  }:
    flake-parts.lib.mkFlake {inherit inputs;} ({
      self,
      pkgs,
      ...
    }: {
      # We define our own systems below. you can still use this to add system specific outputs to your flake.
      # See: https://flake.parts/getting-started
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      # import clan-core modules
      imports = [
        clan-core.flakeModules.default
      ];
      # Define your clan
      # See: https://docs.clan.lol/reference/nix-api/buildclan/
      clan = {
        # Clan wide settings. (Required)
        meta.name = "unibe-clan"; # Ensure to choose a unique name.

        modules.tailscale = import ./service-modules/tailscale.nix;
        modules.codex-usage-dashboard = import ./service-modules/codex-usage-dashboard.nix;

        inventory.instances.tailscale = {
          module = {
            name = "tailscale";
            input = "self";
          };

          roles.peer.machines.itpbowser = {};
          roles.peer.machines.itpmario = {};
          roles.peer.machines.itppeach = {};
          roles.peer.machines.itphlies = {};
        };

        inventory.instances.codex-usage-dashboard = {
          module = {
            name = "codex-usage-dashboard";
            input = "self";
          };

          roles.server.machines.itphlies = {};
        };

        machines = {
          itpbowser = {
            imports = [
              ./machines/itpbowser/configuration.nix
              home-manager.nixosModules.home-manager
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
          };

          itpmario = {
            imports = [
              ./machines/itpmario/configuration.nix
              home-manager.nixosModules.home-manager
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
          };

          itppeach = {
            imports = [
              ./machines/itppeach/configuration.nix
              home-manager.nixosModules.home-manager
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
          };

          itphlies = {
            imports = [
              ./machines/itphlies/configuration.nix
              home-manager.nixosModules.home-manager
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
          };
        };
      };

      perSystem = {
        lib,
        pkgs,
        system,
        ...
      }: {
        packages = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          codex-usage-dashboard =
            pkgs.callPackage ./packages/codex-usage-dashboard/package.nix {};
        };

        checks = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          codex-usage-dashboard =
            pkgs.callPackage ./packages/codex-usage-dashboard/package.nix {};
          codex-usage-dashboard-module = import ./packages/codex-usage-dashboard/nix/module-test.nix {
            nixpkgs = inputs.nixpkgs;
            inherit system;
          };
        };

        devShells.default = pkgs.mkShell {
          packages = [
            clan-core.packages.${system}.clan-cli
            pkgs.nil
            pkgs.nixd
            pkgs.alejandra
            pkgs.just
          ];
          shellHook = ''
            export NIX_PATH=""
          '';
        };
      };
    });
}

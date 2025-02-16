{
  description = "Unibe Clan";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";

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
    nixpkgs,...
  }:
    flake-parts.lib.mkFlake {inherit inputs;} ({
      self,
      pkgs,
      ...
    }: {
      # We define our own systems below. you can still use this to add system specific outputs to your flake.
      # See: https://flake.parts/getting-started
      systems = ["x86_64-linux"];

      # import clan-core modules
      imports = [
        clan-core.flakeModules.default
      ];
      # Define your clan
      # See: https://docs.clan.lol/reference/nix-api/buildclan/
      clan = {
        # Clan wide settings. (Required)
        meta.name = "Unibe clan"; # Ensure to choose a unique name.

        machines = {
          itppeach = {
            imports = [
              ./machines/itppeach/configuration.nix
              ./modules/disko.nix
              ./root-passwd.nix
              impermanence.nixosModules.impermanence
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
            # clan.core.networking.targetHost = "root@130.92.184.147";

            # Set this for clan commands use ssh i.e. `clan machines update`
            # There needs to be exactly one controller per clan
            clan.core.networking.zerotier.controller.enable = true;
          };
        };
      };

      perSystem = {
        lib,
        pkgs,
        system,
        ...
      }: {
        devShells.default = pkgs.mkShell {
          packages = [
            clan-core.packages.${system}.clan-cli
            pkgs.nil
            pkgs.nixd
            pkgs.alejandra
          ];
        };
      };
    });
}

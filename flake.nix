{
  description = "Unibe Clan";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    home-manager.url = "github:nix-community/home-manager";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";

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
    ...
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
              # ./root-passwd.nix
              impermanence.nixosModules.impermanence
              home-manager.nixosModules.home-manager
              {
                home-manager.backupFileExtension = "backup";
                home-manager.useGlobalPkgs = true;
                home-manager.useUserPackages = true;
                home-manager.users.lcnbr = import ./users/lcnbr/home.nix;
                home-manager.extraSpecialArgs = {inherit inputs;};
              }
            ];
            nixpkgs.hostPlatform = "x86_64-linux";
          };

          itphlies = {
            imports = [
              ./machines/itphlies/configuration.nix
              ./modules/user-disko.nix
              # ./root-passwd.nix
              # impermanence.nixosModules.impermanence
              home-manager.nixosModules.home-manager
              {
                # home-manager.backupFileExtension = "backup";
                # home-manager.useGlobalPkgs = true;
                # home-manager.useUserPackages = true;
                home-manager.users.lcnbr = import ./users/lcnbr/home.nix;
                home-manager.extraSpecialArgs = {inherit inputs;};
              }
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

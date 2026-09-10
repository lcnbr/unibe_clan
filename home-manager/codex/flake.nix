{
  description = "Shared Home Manager configurations for the itphlies Codex accounts";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager.url = "github:nix-community/home-manager";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";
    codex-nix = {
      url = "github:SecBear/codex-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = {
    nixpkgs,
    home-manager,
    codex-nix,
    ...
  }: let
    codexUsernames = import ./users.nix;
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
    codexPackage = codex-nix.packages.x86_64-linux.default;
    mkHome = codexUsername: {
      name = codexUsername;
      value = home-manager.lib.homeManagerConfiguration {
        inherit pkgs;
        modules = [./home.nix];
        extraSpecialArgs = {
          inherit codexUsername;
          inherit codexPackage;
        };
      };
    };
  in {
    homeConfigurations = builtins.listToAttrs (map mkHome codexUsernames);
    packages.x86_64-linux.codex-cli = codexPackage;
  };
}

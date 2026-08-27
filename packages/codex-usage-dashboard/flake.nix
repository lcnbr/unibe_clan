{
  description = "Tailscale-only Codex usage dashboard";

  # This revision packages Codex CLI 0.149.0, the app-server version covered
  # by this repository's protocol compatibility tests.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/56c02bc00adcf003215cc4bd996d6efaf4cff188";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      packagesFor = system: nixpkgs.legacyPackages.${system};
      dashboardFor = system: (packagesFor system).callPackage ./package.nix { };
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = packagesFor system;
          dashboard = dashboardFor system;
        in
        {
          default = dashboard;
          codex-usage-dashboard = dashboard;
          codex-cli = pkgs.codex;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/codex-usage-dashboard";
          meta.description = "Run the Codex usage dashboard";
        };
      });

      checks = forAllSystems (
        system:
        let
          pkgs = packagesFor system;
          dashboard = dashboardFor system;
          goTests = pkgs.stdenv.mkDerivation {
            pname = "codex-usage-dashboard-go-tests";
            version = "0.1.0";
            src = nixpkgs.lib.cleanSource ./.;
            nativeBuildInputs = [ pkgs.go ];
            dontConfigure = true;
            buildPhase = ''
              runHook preBuild
              export HOME="$TMPDIR/home"
              export GOCACHE="$TMPDIR/go-cache"
              export GOPATH="$TMPDIR/go"
              mkdir -p "$HOME" "$GOCACHE" "$GOPATH"
              go test ./...
              go test -race ./...
              runHook postBuild
            '';
            installPhase = ''
              runHook preInstall
              touch "$out"
              runHook postInstall
            '';
          };
        in
        {
          package = dashboard;
          go-tests = goTests;
          nixos-module = import ./nix/module-test.nix {
            inherit nixpkgs system;
          };
        }
      );

      devShells = forAllSystems (
        system:
        let
          pkgs = packagesFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.nixfmt
            ];
          };
        }
      );

      formatter = forAllSystems (system: (packagesFor system).nixfmt);
      nixosModules.default = import ./nix/module.nix;
    };
}

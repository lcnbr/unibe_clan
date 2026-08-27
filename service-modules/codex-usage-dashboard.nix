{ ... }: {
  _class = "clan.service";
  manifest.name = "codex-usage-dashboard";
  manifest.description = "Tailnet-only dashboard for isolated local Codex account quotas";
  manifest.categories = [
    "Network"
    "System"
  ];
  manifest.readme = builtins.readFile ../packages/codex-usage-dashboard/README.md;

  roles.server = {
    description = "Host the Codex usage dashboard and its per-user collectors";

    perInstance = { ... }: {
      nixosModule = { pkgs, ... }: {
        imports = [
          ../packages/codex-usage-dashboard/nix/module.nix
        ];

        services.codexUsageDashboard = {
          enable = true;
          package = pkgs.callPackage ../packages/codex-usage-dashboard/package.nix { };
          codexPackage = pkgs.codex;
          expectedCodexVersion = "0.149.0";
          users = [
            "codex"
            "codex-1"
            "codex-2"
            "codex-3"
            "lcnbr"
            "nfink"
            "vhirschi"
            "zeno"
          ];
          listen = "127.0.0.1:8787";
          allowedHosts = [ "itphlies.tailb3264.ts.net" ];

          # Tailscale is already owned by the Clan tailscale service.
          tailscale.enable = false;
        };
      };
    };
  };
}

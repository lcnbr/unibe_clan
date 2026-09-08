{...}: {
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

    perInstance = {...}: {
      nixosModule = {
        config,
        lib,
        pkgs,
        ...
      }: {
        imports = [
          ../packages/codex-usage-dashboard/nix/module.nix
        ];

        services.codexUsageDashboard = {
          enable = true;
          package = pkgs.callPackage ../packages/codex-usage-dashboard/package.nix {};
          codexPackage = pkgs.callPackage ../home-manager/codex/package.nix {};
          expectedCodexVersion = "0.153.4";
          users = lib.attrNames (lib.filterAttrs (_: user: user.isNormalUser or false) config.users.users);
          expectedAnchors = {
            codex-dummy-0 = "localunitarity@gmail.com";
            codex-dummy-1 = "localunitarity+1@gmail.com";
            codex-dummy-2 = "localunitarity+2@gmail.com";
            codex-dummy-3 = "localunitarity+3@gmail.com";
            codex-dummy-4 = "localunitarity+4@gmail.com";
            codex-dummy-5 = "localunitarity+5@gmail.com";
          };
          # Per-user child datasets are created and mounted by this host's
          # ZFS management unit. Prepare `.codex` only after it completes.
          homePreparationRequires = ["zfs-user-datasets.service"];
          listen = "127.0.0.1:8787";
          allowedHosts = ["itphlies.tailb3264.ts.net"];

          # Tailscale is already owned by the Clan tailscale service.
          tailscale.enable = false;
        };
      };
    };
  };
}

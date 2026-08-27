{ nixpkgs, system }:

let
  inherit (nixpkgs) lib;
  pkgs = nixpkgs.legacyPackages.${system};

  usernames = [
    "codex"
    "codex-1"
    "codex-2"
    "codex-3"
    "lcnbr"
    "nfink"
    "vhirschi"
    "zeno"
  ];

  fakeDashboard = pkgs.writeShellScriptBin "codex-usage-dashboard" ''
    exit 0
  '';

  machine = lib.nixosSystem {
    inherit system;
    modules = [
      ./module.nix
      ({ ... }: {
        system.stateVersion = "26.05";
        fileSystems."/" = {
          device = "none";
          fsType = "tmpfs";
        };
        boot.loader.grub = {
          enable = true;
          device = "nodev";
        };

        services.codexUsageDashboard = {
          enable = true;
          package = fakeDashboard;
          allowedHosts = [ "itphlies.tailb3264.ts.net" ];
        };

        users.users = lib.genAttrs usernames (user: {
          isNormalUser = true;
          home = "/home/${user}";
        });
      })
    ];
  };

  cfg = machine.config;
  dashboard = cfg.systemd.services.codex-usage-dashboard;
  collector = user: cfg.systemd.services."codex-usage-collector-${user}";
  codexPackagePath = builtins.unsafeDiscardStringContext (
    toString cfg.services.codexUsageDashboard.codexPackage
  );

  generatedUnitNames = builtins.attrNames (
    lib.filterAttrs (name: _: lib.hasPrefix "codex-usage-" name) cfg.systemd.services
  );
  expectedUnitNames = [
    "codex-usage-collector-codex"
    "codex-usage-collector-codex-1"
    "codex-usage-collector-codex-2"
    "codex-usage-collector-codex-3"
    "codex-usage-collector-lcnbr"
    "codex-usage-collector-nfink"
    "codex-usage-collector-vhirschi"
    "codex-usage-collector-zeno"
    "codex-usage-dashboard"
  ];

  checks = [
    {
      assertion = builtins.all (entry: entry.assertion) cfg.assertions;
      message = "the evaluated NixOS configuration has a failed assertion";
    }
    {
      assertion = generatedUnitNames == expectedUnitNames;
      message = "the dashboard and eight explicit collector units must be generated";
    }
    {
      assertion =
        dashboard.serviceConfig.User == "codex-usage-dashboard"
        && dashboard.serviceConfig.Group == "codex-usage-dashboard"
        && cfg.users.users.codex-usage-dashboard.isSystemUser;
      message = "the dashboard must run as its dedicated system user and group";
    }
    {
      assertion = cfg.users.groups.codex-usage-dashboard.members == usernames;
      message = "all collector users must belong to the ingest group";
    }
    {
      assertion = builtins.all (
        user: builtins.elem "d /home/${user}/.codex 0700 ${user} users -" cfg.systemd.tmpfiles.rules
      ) usernames;
      message = "each collector state directory must exist before service startup";
    }
    {
      assertion =
        dashboard.serviceConfig.ProtectHome == true
        && dashboard.serviceConfig.RuntimeDirectory == "codex-usage-dashboard"
        && dashboard.serviceConfig.RuntimeDirectoryMode == "0750"
        && dashboard.serviceConfig.StateDirectory == "codex-usage-dashboard"
        && dashboard.serviceConfig.StateDirectoryMode == "0700"
        && dashboard.serviceConfig.UMask == "0077"
        && dashboard.serviceConfig.ReadWritePaths == [ "/run/codex-usage-dashboard" ]
        && dashboard.serviceConfig.IPAddressDeny == "any"
        && dashboard.serviceConfig.IPAddressAllow == [ "localhost" ];
      message = "the dashboard hardening and runtime-directory permissions changed";
    }
    {
      assertion = lib.hasInfix "serve --listen 127.0.0.1:8787 --allowed-host itphlies.tailb3264.ts.net --socket /run/codex-usage-dashboard/ingest.sock --stale-after 90s --history-file /var/lib/codex-usage-dashboard/history.json --history-retention 1344h --user codex --user codex-1 --user codex-2 --user codex-3 --user lcnbr --user nfink --user vhirschi --user zeno" dashboard.serviceConfig.ExecStart;
      message = "the dashboard command-line contract changed";
    }
    {
      assertion = builtins.all (
        user:
        let
          unit = collector user;
          home = "/home/${user}";
        in
        unit.serviceConfig.User == user
        && unit.serviceConfig.SupplementaryGroups == [ "codex-usage-dashboard" ]
        && unit.serviceConfig.UMask == "0077"
        && unit.environment.HOME == home
        && unit.environment.CODEX_HOME == "${home}/.codex"
        && unit.serviceConfig.ProtectHome == "tmpfs"
        && unit.serviceConfig.RemoveIPC == false
        && unit.serviceConfig.BindPaths == [ "${home}/.codex" ]
        && builtins.elem "AF_INET" unit.serviceConfig.RestrictAddressFamilies
        && builtins.elem "AF_INET6" unit.serviceConfig.RestrictAddressFamilies
        && builtins.elem "AF_UNIX" unit.serviceConfig.RestrictAddressFamilies
        && builtins.elem "codex-usage-dashboard.service" unit.requires
        && builtins.elem "multi-user.target" unit.wantedBy
        && lib.hasInfix "collector --username ${user}" unit.serviceConfig.ExecStart
        && lib.hasInfix "--codex-bin ${codexPackagePath}/bin/codex" unit.serviceConfig.ExecStart
        && lib.hasInfix "--socket /run/codex-usage-dashboard/ingest.sock" unit.serviceConfig.ExecStart
        && lib.hasInfix "--auth-file ${home}/.codex/auth.json" unit.serviceConfig.ExecStart
        && lib.hasInfix "--poll-interval 30s --recycle-interval 5m --stat-interval 5s" unit.serviceConfig.ExecStart
      ) usernames;
      message = "a collector's identity, isolation, network access, or command-line contract changed";
    }
    {
      assertion =
        lib.getVersion cfg.services.codexUsageDashboard.codexPackage
        == cfg.services.codexUsageDashboard.expectedCodexVersion;
      message = "the Codex app-server package is not the compatibility-tested version";
    }
    {
      assertion = cfg.services.tailscale.enable;
      message = "enabling the dashboard should enable tailscaled by default";
    }
  ];

  failures = builtins.filter (check: !check.assertion) checks;
in
assert lib.assertMsg (failures == [ ]) (
  "codex usage dashboard module test failed:\n"
  + lib.concatMapStringsSep "\n" (check: "- ${check.message}") failures
);
pkgs.runCommand "codex-usage-dashboard-module-test" { } ''
  touch "$out"
''

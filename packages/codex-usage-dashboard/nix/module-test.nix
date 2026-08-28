{ nixpkgs, system }:

let
  inherit (nixpkgs) lib;
  pkgs = nixpkgs.legacyPackages.${system};

  usernames = [
    "mercury"
    "lcnbr"
    "vhirschi"
    "zeno"
    "fraaije"
    "simone"
    "alice"
    "bobby"
    "ben"
    "kaapo"
    "nfink"
    "cedric"
    "kotarela"
    "codex"
    "codex-1"
    "codex-2"
    "codex-3"
    "codex-dummy-0"
    "codex-dummy-1"
    "codex-dummy-2"
    "codex-dummy-3"
  ];

  expectedAnchors = {
    codex-dummy-0 = "localunitarity@gmail.com";
    codex-dummy-1 = "localunitarity+1@gmail.com";
    codex-dummy-2 = "localunitarity+2@gmail.com";
    codex-dummy-3 = "localunitarity+3@gmail.com";
  };

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
          users = usernames;
          inherit expectedAnchors;
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
  homePreparation = cfg.systemd.services.codex-dashboard-home-preparation;
  collector = user: cfg.systemd.services."codex-usage-collector-${user}";
  codexPackagePath = builtins.unsafeDiscardStringContext (
    toString cfg.services.codexUsageDashboard.codexPackage
  );
  dashboardPackagePath = builtins.unsafeDiscardStringContext (toString fakeDashboard);
  requirements = cfg.environment.etc."codex/requirements.toml";

  generatedUnitNames = builtins.attrNames (
    lib.filterAttrs (name: _: lib.hasPrefix "codex-usage-" name) cfg.systemd.services
  );
  expectedUnitNames = lib.sort builtins.lessThan (
    [ "codex-usage-dashboard" ] ++ map (user: "codex-usage-collector-${user}") usernames
  );

  checks = [
    {
      assertion = builtins.all (entry: entry.assertion) cfg.assertions;
      message = "the evaluated NixOS configuration has a failed assertion";
    }
    {
      assertion = generatedUnitNames == expectedUnitNames;
      message = "the dashboard and 21 explicit collector units must be generated";
    }
    {
      assertion =
        dashboard.serviceConfig.User == "codex-usage-dashboard"
        && dashboard.serviceConfig.Group == "codex-usage-dashboard"
        && dashboard.serviceConfig.SupplementaryGroups == [ "users" ]
        && cfg.users.users.codex-usage-dashboard.isSystemUser;
      message = "the dashboard must use its dedicated identity plus the activity socket group";
    }
    {
      assertion = cfg.users.groups.codex-usage-dashboard.members == usernames;
      message = "all collector users must belong to the ingest group";
    }
    {
      assertion =
        homePreparation.before == [ "codex-usage-dashboard.service" ]
        && homePreparation.after == [ "local-fs.target" ]
        && homePreparation.requires == [ ]
        && homePreparation.serviceConfig.Type == "oneshot"
        && homePreparation.serviceConfig.RemainAfterExit
        && builtins.all (
          user:
          lib.hasInfix "systemd-tmpfiles --create '--prefix=/home/${user}/.codex'" homePreparation.script
        ) usernames
        && builtins.all (
          user: builtins.elem "d /home/${user}/.codex 0700 ${user} users -" cfg.systemd.tmpfiles.rules
        ) usernames
        && builtins.elem "codex-dashboard-home-preparation.service" dashboard.after
        && builtins.elem "codex-dashboard-home-preparation.service" dashboard.requires;
      message = "collector state directories must be prepared after user homes are mounted";
    }
    {
      assertion =
        dashboard.serviceConfig.ProtectHome == true
        && dashboard.serviceConfig.RuntimeDirectory == "codex-usage-dashboard"
        && dashboard.serviceConfig.RuntimeDirectoryMode == "0711"
        && dashboard.serviceConfig.StateDirectory == "codex-usage-dashboard"
        && dashboard.serviceConfig.StateDirectoryMode == "0700"
        && dashboard.serviceConfig.UMask == "0077"
        && dashboard.serviceConfig.ReadWritePaths == [ "/run/codex-usage-dashboard" ]
        && dashboard.serviceConfig.IPAddressDeny == "any"
        && dashboard.serviceConfig.IPAddressAllow == [ "localhost" ];
      message = "the dashboard hardening and runtime-directory permissions changed";
    }
    {
      assertion =
        lib.hasInfix "serve --listen 127.0.0.1:8787 --allowed-host itphlies.tailb3264.ts.net" dashboard.serviceConfig.ExecStart
        && lib.hasInfix "--socket /run/codex-usage-dashboard/ingest.sock" dashboard.serviceConfig.ExecStart
        && lib.hasInfix "--activity-socket /run/codex-usage-dashboard/activity.sock --activity-socket-group users --activity-lease 30m" dashboard.serviceConfig.ExecStart
        && lib.hasInfix "--stale-after 90s --history-file /var/lib/codex-usage-dashboard/account-history.json --history-retention 8784h" dashboard.serviceConfig.ExecStart
        && builtins.all (user: lib.hasInfix "--user ${user}" dashboard.serviceConfig.ExecStart) usernames
        && builtins.all (
          user:
          lib.hasInfix (lib.escapeShellArgs [
            "--anchor"
            "${user}=${expectedAnchors.${user}}"
          ]) dashboard.serviceConfig.ExecStart
        ) (builtins.attrNames expectedAnchors)
        && !(lib.hasInfix "/var/lib/codex-usage-dashboard/history.json" dashboard.serviceConfig.ExecStart)
        && !(lib.hasInfix "/var/lib/codex-usage-dashboard/history-adjustments.json" dashboard.serviceConfig.ExecStart);
      message = "the dashboard command-line contract changed";
    }
    {
      assertion =
        cfg.services.codexUsageDashboard.activitySocket == "/run/codex-usage-dashboard/activity.sock"
        && cfg.services.codexUsageDashboard.activityLease == "30m"
        && cfg.services.codexUsageDashboard.expectedAnchors == expectedAnchors;
      message = "the activity socket, lease, or expected-anchor options changed";
    }
    {
      assertion =
        requirements.mode == "0444"
        && lib.hasInfix "[features]\nhooks = true" requirements.text
        && lib.hasInfix "managed_dir = \"${dashboardPackagePath}/bin\"" requirements.text
        &&
          builtins.all
            (
              event:
              lib.hasInfix "[[hooks.${event}]]" requirements.text
              && lib.hasInfix "[[hooks.${event}.hooks]]" requirements.text
            )
            [
              "SessionStart"
              "UserPromptSubmit"
              "PreToolUse"
              "PostToolUse"
              "Stop"
              "SubagentStop"
              "SessionEnd"
            ]
        && lib.hasInfix "command = \"${dashboardPackagePath}/bin/codex-usage-dashboard hook-report --socket /run/codex-usage-dashboard/activity.sock\"" requirements.text
        && lib.hasInfix "timeout = 2" requirements.text
        && !(lib.hasInfix "allow_managed_hooks_only" requirements.text);
      message = "the system-managed Codex hook requirements changed";
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
      assertion = builtins.elem cfg.services.codexUsageDashboard.codexPackage cfg.environment.systemPackages;
      message = "the pinned Codex CLI must be installed system-wide";
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

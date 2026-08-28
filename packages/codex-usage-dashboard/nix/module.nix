{
  config,
  lib,
  pkgs,
  ...
}:

let
  inherit (lib)
    concatMap
    escapeShellArgs
    getVersion
    hasPrefix
    length
    mkEnableOption
    mkIf
    mkMerge
    mkOption
    nameValuePair
    types
    unique
    ;

  cfg = config.services.codexUsageDashboard;

  serviceUser = "codex-usage-dashboard";
  ingestGroup = "codex-usage-dashboard";
  activityGroup = "users";
  runtimeDirectory = "codex-usage-dashboard";
  runtimePath = "/run/${runtimeDirectory}";
  stateDirectory = "codex-usage-dashboard";
  historyPath = "/var/lib/${stateDirectory}/account-history.json";
  # Keep this outside the codex-usage-* unit namespace: operational checks
  # count that namespace as the dashboard plus collector fleet.
  homePreparationService = "codex-dashboard-home-preparation";

  dashboardBin = "${cfg.package}/bin/codex-usage-dashboard";
  codexBin = "${cfg.codexPackage}/bin/codex";
  anchorUsers = builtins.attrNames cfg.expectedAnchors;

  hookCommand = escapeShellArgs [
    dashboardBin
    "hook-report"
    "--socket"
    cfg.activitySocket
  ];

  managedRequirements = ''
    [features]
    hooks = true

    [hooks]
    managed_dir = ${builtins.toJSON "${cfg.package}/bin"}

    [[hooks.SessionStart]]

    [[hooks.SessionStart.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.UserPromptSubmit]]

    [[hooks.UserPromptSubmit.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.PreToolUse]]

    [[hooks.PreToolUse.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.PostToolUse]]

    [[hooks.PostToolUse.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.Stop]]

    [[hooks.Stop.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.SubagentStop]]

    [[hooks.SubagentStop.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2

    [[hooks.SessionEnd]]

    [[hooks.SessionEnd.hooks]]
    type = "command"
    command = ${builtins.toJSON hookCommand}
    timeout = 2
  '';

  userExists = user: builtins.hasAttr user config.users.users;
  userHome = user: config.users.users.${user}.home;
  userGroup = user: config.users.users.${user}.group;
  codexHome = user: "${userHome user}/.codex";

  serverCommand = escapeShellArgs (
    [
      dashboardBin
      "serve"
      "--listen"
      cfg.listen
    ]
    ++ concatMap (host: [
      "--allowed-host"
      host
    ]) cfg.allowedHosts
    ++ [
      "--socket"
      cfg.socket
      "--activity-socket"
      cfg.activitySocket
      "--activity-socket-group"
      activityGroup
      "--activity-lease"
      cfg.activityLease
      "--stale-after"
      "90s"
      "--history-file"
      historyPath
      "--history-retention"
      "8784h"
    ]
    ++ concatMap (user: [
      "--anchor"
      "${user}=${cfg.expectedAnchors.${user}}"
    ]) anchorUsers
    ++ concatMap (user: [
      "--user"
      user
    ]) cfg.users
  );

  collectorCommand =
    user:
    escapeShellArgs [
      dashboardBin
      "collector"
      "--username"
      user
      "--codex-bin"
      codexBin
      "--socket"
      cfg.socket
      "--auth-file"
      "${codexHome user}/auth.json"
      "--poll-interval"
      "30s"
      "--recycle-interval"
      "5m"
      "--stat-interval"
      "5s"
    ];

  prepareHomeCommand =
    user:
    escapeShellArgs [
      "${config.systemd.package}/bin/systemd-tmpfiles"
      "--create"
      "--prefix=${codexHome user}"
    ];

  commonHardening = {
    CapabilityBoundingSet = "";
    DevicePolicy = "closed";
    LockPersonality = true;
    NoNewPrivileges = true;
    PrivateDevices = true;
    PrivateTmp = true;
    ProtectClock = true;
    ProtectControlGroups = true;
    ProtectHostname = true;
    ProtectKernelLogs = true;
    ProtectKernelModules = true;
    ProtectKernelTunables = true;
    ProtectSystem = "strict";
    RemoveIPC = true;
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    SystemCallArchitectures = "native";
  };

  collectorService =
    user:
    nameValuePair "codex-usage-collector-${user}" {
      description = "Codex account usage collector for ${user}";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [
        "network-online.target"
        "codex-usage-dashboard.service"
      ];
      requires = [ "codex-usage-dashboard.service" ];
      partOf = [ "codex-usage-dashboard.service" ];
      startLimitIntervalSec = 0;

      environment = {
        HOME = userHome user;
        CODEX_HOME = codexHome user;
      };

      serviceConfig = commonHardening // {
        Type = "simple";
        ExecStart = collectorCommand user;
        User = user;
        SupplementaryGroups = [ ingestGroup ];

        Restart = "always";
        RestartSec = "5s";
        UMask = "0077";

        # These are real interactive login UIDs. RemoveIPC would delete IPC
        # objects owned by the user when this collector recycles.
        RemoveIPC = false;

        # Hide every home, then bind only this collector owner's Codex state
        # back into its namespace. Network access is intentionally retained
        # for app-server.
        ProtectHome = "tmpfs";
        BindPaths = [ (codexHome user) ];
        RestrictAddressFamilies = [
          "AF_UNIX"
          "AF_INET"
          "AF_INET6"
        ];
      };
    };
in
{
  options.services.codexUsageDashboard = {
    enable = mkEnableOption "the local Codex account usage dashboard";

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ../package.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ../package.nix { }";
      description = "Package providing the codex-usage-dashboard binary.";
    };

    listen = mkOption {
      type = types.str;
      default = "127.0.0.1:8787";
      description = ''
        Loopback address on which the dashboard HTTP server listens. A
        non-loopback listener is rejected so Tailscale Serve remains the only
        remote entry point.
      '';
    };

    allowedHosts = mkOption {
      type = types.listOf types.str;
      default = [ ];
      description = ''
        Exact additional HTTP Host names or IP literals accepted by the
        dashboard. The literal loopback IP from listen is always accepted.
        Entries must omit ports and wildcards; add the machine's complete
        Tailscale DNS name when using Tailscale Serve.
      '';
    };

    socket = mkOption {
      type = types.str;
      default = "${runtimePath}/ingest.sock";
      description = "Unix socket used by collectors to submit snapshots.";
    };

    activitySocket = mkOption {
      type = types.str;
      default = "${runtimePath}/activity.sock";
      description = ''
        Peer-authenticated Unix socket used by managed Codex hooks to submit
        sanitized chat lifecycle events.
      '';
    };

    activityLease = mkOption {
      type = types.str;
      default = "30m";
      description = ''
        Maximum age of a hook session without an event before its in-memory
        activity record is expired. SessionEnd removes it immediately.
      '';
    };

    users = mkOption {
      type = types.listOf types.str;
      default = [
        "codex"
        "codex-1"
        "codex-2"
        "codex-3"
        "lcnbr"
        "nfink"
        "vhirschi"
        "zeno"
      ];
      description = "Existing local users for which collectors are started.";
    };

    expectedAnchors = mkOption {
      type = types.attrsOf types.str;
      default = { };
      description = ''
        Map of collector usernames to the exact account email expected for
        anchor health checks. Values are used only in memory and public status
        responses; they are never written to history.
      '';
    };

    homePreparationRequires = mkOption {
      type = types.listOf types.str;
      default = [ ];
      description = ''
        Units that must finish before collector-owned Codex directories are
        prepared. Hosts that mount per-user homes dynamically should list the
        responsible mount or dataset-management service here.
      '';
    };

    codexPackage = mkOption {
      type = types.package;
      default = pkgs.codex;
      defaultText = lib.literalExpression "pkgs.codex";
      description = "Pinned Codex CLI package launched by each collector.";
    };

    expectedCodexVersion = mkOption {
      type = types.str;
      default = "0.149.0";
      description = ''
        Codex CLI version whose app-server protocol has been compatibility
        tested. Change this together with codexPackage only after retesting.
      '';
    };

    tailscale.enable = mkOption {
      type = types.bool;
      default = true;
      description = ''
        Enable the NixOS Tailscale daemon. This module never authenticates the
        machine and never configures Serve or Funnel.
      '';
    };
  };

  config = mkIf cfg.enable (mkMerge [
    {
      assertions = [
        {
          assertion = cfg.users != [ ];
          message = "services.codexUsageDashboard.users must not be empty";
        }
        {
          assertion = length (unique cfg.users) == length cfg.users;
          message = "services.codexUsageDashboard.users must not contain duplicates";
        }
        {
          assertion = builtins.all userExists cfg.users;
          message = ''
            Every services.codexUsageDashboard.users entry must already be
            declared in users.users
          '';
        }
        {
          assertion = !(builtins.elem serviceUser cfg.users);
          message = "The dashboard service user cannot also be a collector user";
        }
        {
          assertion = hasPrefix "127.0.0.1:" cfg.listen || hasPrefix "[::1]:" cfg.listen;
          message = "services.codexUsageDashboard.listen must be a loopback address";
        }
        {
          assertion = builtins.all (host: host != "") cfg.allowedHosts;
          message = "services.codexUsageDashboard.allowedHosts must not contain empty entries";
        }
        {
          assertion = length (unique cfg.allowedHosts) == length cfg.allowedHosts;
          message = "services.codexUsageDashboard.allowedHosts must not contain duplicates";
        }
        {
          assertion = builtins.dirOf cfg.socket == runtimePath;
          message = "services.codexUsageDashboard.socket must be inside ${runtimePath}";
        }
        {
          assertion = builtins.dirOf cfg.activitySocket == runtimePath;
          message = "services.codexUsageDashboard.activitySocket must be inside ${runtimePath}";
        }
        {
          assertion = cfg.activitySocket != cfg.socket;
          message = "services.codexUsageDashboard.activitySocket and socket must differ";
        }
        {
          assertion = cfg.activityLease != "";
          message = "services.codexUsageDashboard.activityLease must not be empty";
        }
        {
          assertion = builtins.all (user: builtins.elem user cfg.users) anchorUsers;
          message = ''
            Every services.codexUsageDashboard.expectedAnchors key must also
            be listed in services.codexUsageDashboard.users
          '';
        }
        {
          assertion = builtins.all (email: email != "") (builtins.attrValues cfg.expectedAnchors);
          message = "services.codexUsageDashboard.expectedAnchors values must not be empty";
        }
        {
          assertion =
            length (unique (builtins.attrValues cfg.expectedAnchors))
            == length (builtins.attrValues cfg.expectedAnchors);
          message = "services.codexUsageDashboard.expectedAnchors values must be unique";
        }
        {
          assertion = getVersion cfg.codexPackage == cfg.expectedCodexVersion;
          message = ''
            services.codexUsageDashboard.codexPackage must be Codex CLI
            ${cfg.expectedCodexVersion}; update expectedCodexVersion only after
            app-server compatibility tests pass
          '';
        }
      ];

      users.groups.${ingestGroup}.members = cfg.users;
      users.users.${serviceUser} = {
        isSystemUser = true;
        group = ingestGroup;
        description = "Codex usage dashboard service";
        home = "/var/empty";
        createHome = false;
      };

      # Keep the compatibility-tested Codex executable available for both the
      # collectors and interactive `sudo -iu <user> codex login` sessions.
      environment.systemPackages = [ cfg.codexPackage ];

      # Codex loads Unix-wide admin requirements from this fixed location.
      # The hooks forward their JSON event on stdin to a peer-UID-authenticated
      # socket; prompts, tool inputs, paths, and environment data are discarded
      # by the reporter and never interpolated into this command or its logs.
      environment.etc."codex/requirements.toml" = {
        text = managedRequirements;
        mode = "0444";
      };

      # The normal boot pass handles ordinary mounted homes. The ordered
      # preparation service below safely repeats these exact rules after any
      # host-specific dynamic per-user mounts have completed.
      systemd.tmpfiles.rules = map (
        user: "d ${codexHome user} 0700 ${user} ${userGroup user} -"
      ) cfg.users;

      systemd.services = {
        ${homePreparationService} = {
          description = "Prepare private Codex state directories after user homes are mounted";
          before = [ "codex-usage-dashboard.service" ];
          after = [ "local-fs.target" ] ++ cfg.homePreparationRequires;
          requires = cfg.homePreparationRequires;

          # Safely repeat the declarative tmpfiles rule after host-specific
          # per-user datasets are mounted. systemd-tmpfiles rejects unsafe
          # symlink traversal; this service never reads credential data.
          script = lib.concatMapStringsSep "\n" prepareHomeCommand cfg.users;
          serviceConfig = {
            Type = "oneshot";
            RemainAfterExit = true;
            UMask = "0077";
          };
        };

        "codex-usage-dashboard" = {
          description = "Codex account usage dashboard";
          wantedBy = [ "multi-user.target" ];
          after = [
            "network.target"
            "${homePreparationService}.service"
          ];
          requires = [ "${homePreparationService}.service" ];
          startLimitIntervalSec = 0;

          environment = {
            HOME = "/var/empty";
          };

          serviceConfig = commonHardening // {
            Type = "simple";
            ExecStart = serverCommand;
            User = serviceUser;
            Group = ingestGroup;
            SupplementaryGroups = [ activityGroup ];

            Restart = "always";
            RestartSec = "5s";
            UMask = "0077";

            RuntimeDirectory = runtimeDirectory;
            # Existing Codex processes may predate this module activation and
            # therefore lack the newly-created ingest group. Execute-only
            # traversal lets them reach activity.sock through their stable
            # `users` group without permitting directory listing; ingest.sock
            # remains protected by its dedicated group and SO_PEERCRED checks.
            RuntimeDirectoryMode = "0711";
            StateDirectory = stateDirectory;
            StateDirectoryMode = "0700";
            ProtectHome = true;
            ReadWritePaths = [ runtimePath ];
            IPAddressDeny = "any";
            IPAddressAllow = [ "localhost" ];
            RestrictAddressFamilies = [
              "AF_UNIX"
              "AF_INET"
              "AF_INET6"
            ];
          };
        };
      }
      // builtins.listToAttrs (map collectorService cfg.users);
    }

    (mkIf cfg.tailscale.enable {
      services.tailscale.enable = true;
    })
  ]);
}

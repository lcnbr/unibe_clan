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
  runtimeDirectory = "codex-usage-dashboard";
  runtimePath = "/run/${runtimeDirectory}";
  stateDirectory = "codex-usage-dashboard";
  historyPath = "/var/lib/${stateDirectory}/history.json";

  dashboardBin = "${cfg.package}/bin/codex-usage-dashboard";
  codexBin = "${cfg.codexPackage}/bin/codex";

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
      "--stale-after"
      "90s"
      "--history-file"
      historyPath
      "--history-retention"
      "1344h"
    ]
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

      # A collector must be able to start before its user has ever logged in.
      # Create only the empty state directory; credential contents remain
      # exclusively owned and read by Codex App Server under that user's UID.
      systemd.tmpfiles.rules = map (
        user: "d ${codexHome user} 0700 ${user} ${userGroup user} -"
      ) cfg.users;

      systemd.services = {
        "codex-usage-dashboard" = {
          description = "Codex account usage dashboard";
          wantedBy = [ "multi-user.target" ];
          after = [ "network.target" ];
          startLimitIntervalSec = 0;

          environment = {
            HOME = "/var/empty";
          };

          serviceConfig = commonHardening // {
            Type = "simple";
            ExecStart = serverCommand;
            User = serviceUser;
            Group = ingestGroup;

            Restart = "always";
            RestartSec = "5s";
            UMask = "0077";

            RuntimeDirectory = runtimeDirectory;
            RuntimeDirectoryMode = "0750";
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

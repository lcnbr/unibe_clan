{
  config,
  lib,
  pkgs,
  ...
}: let
  inherit (lib) mkEnableOption mkIf mkOption types;

  dashboard = config.services.codexUsageDashboard;
  cfg = dashboard.publicAccess;

  proxyService = "codex-dashboard-public-proxy";
  funnelService = "codex-dashboard-public-funnel";
  credentialName = "dashboard.htpasswd";
  credentialPath = "/run/credentials/${proxyService}.service/${credentialName}";
  credentialSource =
    if cfg.htpasswdFile == null
    then "/no-such-path"
    else cfg.htpasswdFile;
  nginxConfigPath = "/etc/codex-dashboard-public/nginx.conf";
  tailscaleBin = "${config.services.tailscale.package}/bin/tailscale";
  backendPortText =
    if lib.hasPrefix "127.0.0.1:" dashboard.listen
    then lib.removePrefix "127.0.0.1:" dashboard.listen
    else if lib.hasPrefix "[::1]:" dashboard.listen
    then lib.removePrefix "[::1]:" dashboard.listen
    else null;
  backendPortResult =
    if backendPortText == null
    then {
      success = false;
      value = null;
    }
    else builtins.tryEval (lib.toInt backendPortText);
  backendPort =
    if backendPortResult.success
    then backendPortResult.value
    else null;

  securityHeaders = ''
    add_header Cache-Control "no-store" always;
    add_header Content-Security-Policy "default-src 'none'; base-uri 'none'; connect-src 'self'; font-src 'self'; form-action 'none'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self'" always;
    add_header Cross-Origin-Opener-Policy "same-origin" always;
    add_header Cross-Origin-Resource-Policy "same-origin" always;
    add_header Permissions-Policy "camera=(), geolocation=(), microphone=()" always;
    add_header Referrer-Policy "no-referrer" always;
    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
  '';

  nginxConfig = ''
    worker_processes 1;
    daemon off;
    pid /run/${proxyService}/nginx.pid;
    # Keep configuration and process failures, but never journal request paths
    # or fixed Basic-auth usernames from failed public attempts.
    error_log stderr crit;

    events {
      worker_connections 128;
    }

    http {
      include ${pkgs.nginx}/conf/mime.types;
      default_type application/octet-stream;
      access_log off;
      server_tokens off;
      sendfile off;
      keepalive_timeout 30s;
      client_body_timeout 10s;
      client_header_timeout 10s;
      client_max_body_size 1m;

      client_body_temp_path /run/${proxyService}/client-body;
      proxy_temp_path /run/${proxyService}/proxy;

      limit_req_zone $binary_remote_addr zone=codex_dashboard_auth:1m rate=2r/s;
      limit_conn_zone $binary_remote_addr zone=codex_dashboard_connections:1m;
      limit_req_status 429;
      limit_conn_status 429;

      server {
        listen 127.0.0.1:${toString cfg.proxyPort} default_server;
        server_name _;
        ${securityHeaders}
        return 421;
      }

      server {
        listen 127.0.0.1:${toString cfg.proxyPort};
        server_name ${cfg.hostname};
        ${securityHeaders}

        location / {
          auth_basic "Codex usage dashboard";
          auth_basic_user_file ${credentialPath};

          limit_req zone=codex_dashboard_auth burst=30 nodelay;
          limit_conn codex_dashboard_connections 16;

          proxy_pass http://${dashboard.listen};
          proxy_http_version 1.1;
          proxy_buffering off;
          proxy_request_buffering off;
          proxy_cache off;
          proxy_connect_timeout 5s;
          proxy_read_timeout 1h;
          proxy_send_timeout 1h;

          # Forward only the canonical authority needed by the dashboard's
          # Host check. In particular, the shared Basic credential, cookies,
          # spoofed forwarding data, and Tailscale identity headers never
          # reach the Go service.
          proxy_pass_request_headers off;
          proxy_set_header Host ${cfg.hostname};
          proxy_set_header Connection "";
        }
      }
    }
  '';

  funnelTarget = "http://127.0.0.1:${toString cfg.proxyPort}";
  # NodeAttrFunnel is intentionally a short node-attribute key. The similarly
  # named URL capability is used for ingress grants, not Self.CapMap.
  funnelCapability = "funnel";

  funnelPreflight = pkgs.writeShellScript "codex-dashboard-funnel-preflight" ''
    set -eu

    status="$(${tailscaleBin} status --json)"
    if ! printf '%s\n' "$status" | ${pkgs.jq}/bin/jq -e \
      --arg capability ${lib.escapeShellArg funnelCapability} '
      ((.Self.CapMap // {}) | has($capability)) or
      (((.Self.Capabilities // []) | index($capability)) != null)
    ' >/dev/null; then
      echo "Tailscale Funnel is not authorized for this node; grant the funnel node attribute first" >&2
      exit 1
    fi

    serve="$(${tailscaleBin} serve status --json)"
    if ! printf '%s\n' "$serve" | ${pkgs.jq}/bin/jq -e \
      --arg port ${lib.escapeShellArg (toString cfg.funnelPort)} '
        def matching_entries($object):
          [($object // {}) | to_entries[] |
            select(.key == $port or (.key | endswith(":" + $port)))];
        def port_is_free($config):
          (($config.TCP // {})[$port] == null) and
          ((matching_entries($config.Web) | length) == 0) and
          ((matching_entries($config.AllowFunnel) | length) == 0);
        port_is_free(.) and
        all((.Foreground // {})[]; port_is_free(.))
      ' >/dev/null; then
      echo "refusing to replace an unrelated Tailscale route on port ${toString cfg.funnelPort}" >&2
      exit 1
    fi
  '';

  funnelCommand = lib.escapeShellArgs [
    tailscaleBin
    "funnel"
    "--yes"
    "--https=${toString cfg.funnelPort}"
    "--set-path=/"
    funnelTarget
  ];

in {
  options.services.codexUsageDashboard.publicAccess = {
    enable = mkEnableOption "password-protected public access through Tailscale Funnel";

    hostname = mkOption {
      type = types.str;
      description = "Exact Tailscale DNS hostname used by the public Funnel endpoint.";
    };

    proxyPort = mkOption {
      type = types.port;
      default = 8788;
      description = "Loopback port for the password-authenticated nginx proxy.";
    };

    funnelPort = mkOption {
      type = types.enum [
        443
        8443
        10000
      ];
      default = 10000;
      description = "Public HTTPS port assigned to Tailscale Funnel.";
    };

    htpasswdFile = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = ''
        Runtime path to a secret htpasswd file. The file is transferred to the
        proxy with systemd credentials and must never be placed in the Nix store.
      '';
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = dashboard.enable;
        message = "services.codexUsageDashboard.publicAccess requires services.codexUsageDashboard.enable";
      }
      {
        assertion = config.services.tailscale.enable;
        message = "services.codexUsageDashboard.publicAccess requires services.tailscale.enable";
      }
      {
        assertion = cfg.htpasswdFile != null;
        message = "services.codexUsageDashboard.publicAccess.htpasswdFile must be set";
      }
      {
        assertion =
          cfg.htpasswdFile == null
          || (lib.hasPrefix "/" cfg.htpasswdFile && !(lib.hasPrefix "${builtins.storeDir}/" cfg.htpasswdFile));
        message = "services.codexUsageDashboard.publicAccess.htpasswdFile must be an absolute runtime path outside the Nix store";
      }
      {
        assertion = builtins.match "[A-Za-z0-9][A-Za-z0-9.-]{0,251}[A-Za-z0-9]" cfg.hostname != null;
        message = "services.codexUsageDashboard.publicAccess.hostname must be a plain DNS name";
      }
      {
        assertion = builtins.elem cfg.hostname dashboard.allowedHosts;
        message = "the public hostname must also appear in services.codexUsageDashboard.allowedHosts";
      }
      {
        assertion = cfg.proxyPort != cfg.funnelPort;
        message = "the loopback proxy and public Funnel ports must differ";
      }
      {
        assertion = backendPort == null || cfg.proxyPort != backendPort;
        message = "the public proxy and dashboard backend listen ports must differ";
      }
    ];

    environment.etc."codex-dashboard-public/nginx.conf" = {
      text = nginxConfig;
      mode = "0444";
    };

    systemd.services.${proxyService} = {
      description = "Password-authenticated Codex dashboard proxy";
      wantedBy = ["multi-user.target"];
      after = ["codex-usage-dashboard.service"];
      requires = ["codex-usage-dashboard.service"];

      serviceConfig = {
        Type = "simple";
        ExecStartPre = "${pkgs.nginx}/bin/nginx -e stderr -t -c ${nginxConfigPath}";
        ExecStart = "${pkgs.nginx}/bin/nginx -e stderr -c ${nginxConfigPath}";
        Restart = "always";
        RestartSec = "5s";

        DynamicUser = true;
        LoadCredential = ["${credentialName}:${credentialSource}"];
        RuntimeDirectory = proxyService;
        RuntimeDirectoryMode = "0700";
        UMask = "0077";

        AmbientCapabilities = "";
        CapabilityBoundingSet = "";
        DevicePolicy = "closed";
        IPAddressAllow = ["localhost"];
        IPAddressDeny = "any";
        LimitNOFILE = 1024;
        LimitCORE = 0;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        MemoryMax = "256M";
        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        ProcSubset = "pid";
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        ProtectSystem = "strict";
        RemoveIPC = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_UNIX"
        ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "~@cpu-emulation"
          "~@debug"
          "~@keyring"
          "~@mount"
          "~@obsolete"
          "~@privileged"
          "~@setuid"
        ];
        TasksMax = 64;
      };
    };

    systemd.services.${funnelService} = {
      description = "Public Tailscale Funnel for the Codex dashboard";
      wantedBy = ["multi-user.target"];
      wants = [
        "network-online.target"
        "tailscaled-autoconnect.service"
      ];
      after = [
        "network-online.target"
        "tailscaled.service"
        "tailscaled-autoconnect.service"
        "${proxyService}.service"
      ];
      requires = [
        "tailscaled.service"
        "${proxyService}.service"
      ];
      partOf = ["tailscaled.service"];

      serviceConfig = {
        Type = "simple";
        ExecCondition = funnelPreflight;
        ExecStart = funnelCommand;
        KillSignal = "SIGINT";
        Restart = "always";
        RestartSec = "10s";

        AmbientCapabilities = "";
        CapabilityBoundingSet = "";
        DevicePolicy = "closed";
        LimitCORE = 0;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        NoNewPrivileges = true;
        PrivateDevices = true;
        PrivateTmp = true;
        ProtectClock = true;
        ProtectControlGroups = true;
        ProtectHome = true;
        ProtectHostname = true;
        ProtectKernelLogs = true;
        ProtectKernelModules = true;
        ProtectKernelTunables = true;
        ProtectProc = "invisible";
        ProtectSystem = "strict";
        RemoveIPC = true;
        RestrictAddressFamilies = ["AF_UNIX"];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "~@cpu-emulation"
          "~@debug"
          "~@keyring"
          "~@mount"
          "~@obsolete"
          "~@privileged"
          "~@setuid"
        ];
      };
    };
  };
}

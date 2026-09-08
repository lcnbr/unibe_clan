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
    "codex-dummy-4"
    "codex-dummy-5"
  ];

  expectedAnchors = {
    codex-dummy-0 = "localunitarity@gmail.com";
    codex-dummy-1 = "localunitarity+1@gmail.com";
    codex-dummy-2 = "localunitarity+2@gmail.com";
    codex-dummy-3 = "localunitarity+3@gmail.com";
    codex-dummy-4 = "localunitarity+4@gmail.com";
    codex-dummy-5 = "localunitarity+5@gmail.com";
  };

  fakeDashboard = pkgs.writeShellScriptBin "codex-usage-dashboard" ''
    exit 0
  '';

  fakeHistoricalCodex = pkgs.runCommand "codex-0.148.0" { } ''
    mkdir -p "$out/bin"
    touch "$out/bin/codex"
  '';
  fakeHistoricalCodexPath = builtins.unsafeDiscardStringContext (toString fakeHistoricalCodex);
  historicalVersionWithContext = builtins.appendContext "0.148.0" (
    builtins.getContext (toString fakeHistoricalCodex)
  );
  fakeTailscale = pkgs.writeShellScriptBin "tailscale" ''
    case "$1" in
      status)
        printf '%s\n' "''${TAILSCALE_STATUS_JSON:?}"
        ;;
      serve)
        test "$2" = status
        printf '%s\n' "''${TAILSCALE_SERVE_JSON:?}"
        ;;
      *)
        exit 2
        ;;
    esac
  '';
  extraCodexVersionTargets = {
    "${fakeHistoricalCodexPath}/bin/codex" = historicalVersionWithContext;
  };
  defaultCodexTarget = "${builtins.unsafeDiscardStringContext (toString pkgs.codex)}/bin/codex";

  validationMachine =
    codexVersionTargets:
    lib.nixosSystem {
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
            users = [ "validation-user" ];
            inherit codexVersionTargets;
            tailscale.enable = false;
          };

          users.users.validation-user = {
            isNormalUser = true;
            home = "/home/validation-user";
          };
        })
      ];
    };

  failedTargetAssertionMessages =
    codexVersionTargets:
    map (entry: entry.message) (
      builtins.filter (
        entry:
        !entry.assertion && lib.hasInfix "services.codexUsageDashboard.codexVersionTargets" entry.message
      ) (validationMachine codexVersionTargets).config.assertions
    );

  failedPublicAssertionMessages =
    {
      dashboardEnable ? true,
      dashboardTailscaleEnable ? true,
      htpasswdFile ? "/run/secrets/dashboard.htpasswd",
      listen ? "127.0.0.1:8787",
    }:
    let
      publicValidation = lib.nixosSystem {
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
              enable = dashboardEnable;
              package = fakeDashboard;
              users = [ "validation-user" ];
              inherit listen;
              tailscale.enable = dashboardTailscaleEnable;
              allowedHosts = [ "itphlies.tailb3264.ts.net" ];
              publicAccess = {
                enable = true;
                hostname = "itphlies.tailb3264.ts.net";
                inherit htpasswdFile;
              };
            };
            users.users.validation-user = {
              isNormalUser = true;
              home = "/home/validation-user";
            };
          })
        ];
      };
    in
    map (entry: entry.message) (
      builtins.filter (entry: !entry.assertion) publicValidation.config.assertions
    );

  invalidTargetCases = [
    {
      targets = {
        "/nix/store/0000000000000000000000000000000e-codex-0.148.0/bin/codex" = "0.148.0";
      };
      expectedMessage = "keys must be clean";
    }
    {
      targets = {
        "/nix/store/00000000000000000000000000000000-codex-0.148.0/bin/../bin/codex" = "0.148.0";
      };
      expectedMessage = "keys must be clean";
    }
    {
      targets = {
        "/nix/store/00000000000000000000000000000000-codex-0.148.0/bin/codex-helper" = "0.148.0";
      };
      expectedMessage = "keys must be clean";
    }
    {
      targets = {
        "/nix/store/00000000000000000000000000000000-codex-0.148.0/bin/codex" = "0.148.0\n";
      };
      expectedMessage = "values must be valid Codex versions";
    }
    {
      targets = {
        "/nix/store/00000000000000000000000000000000-codex-0.148.0/bin/codex" = "0.149.0";
      };
      expectedMessage = "values must match their store-path versions";
    }
    {
      targets = {
        "${defaultCodexTarget}" = "malformed version";
      };
      expectedMessage = "values must be valid Codex versions";
    }
  ];

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
          codexVersionTargets = extraCodexVersionTargets;
          allowedHosts = [ "itphlies.tailb3264.ts.net" ];
          publicAccess = {
            enable = true;
            hostname = "itphlies.tailb3264.ts.net";
            htpasswdFile = "/run/secrets/dashboard.htpasswd";
          };
        };
        services.tailscale.package = fakeTailscale;

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
  publicProxy = cfg.systemd.services.codex-dashboard-public-proxy;
  publicFunnel = cfg.systemd.services.codex-dashboard-public-funnel;
  publicFunnelPreflight = publicFunnel.serviceConfig.ExecCondition;
  publicNginx = cfg.environment.etc."codex-dashboard-public/nginx.conf".text;
  publicNginxFile = pkgs.writeText "codex-dashboard-public-nginx-test.conf" publicNginx;
  fakePublicUpstream = pkgs.writeText "codex-dashboard-public-upstream.py" ''
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def send_payload(self, code, payload, content_type="text/plain"):
            encoded = payload.encode()
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(encoded)
                self.wfile.flush()

        def do_GET(self):
            if self.path == "/api/v1/events":
                self.send_payload(200, "event: snapshot\ndata: {\"ok\":true}\n\n", "text/event-stream")
                return
            observed = "\n".join([
                "host=" + self.headers.get("Host", ""),
                "authorization=" + self.headers.get("Authorization", ""),
                "proxy-authorization=" + self.headers.get("Proxy-Authorization", ""),
                "cookie=" + self.headers.get("Cookie", ""),
                "forwarded=" + self.headers.get("X-Forwarded-For", ""),
                "tailscale=" + self.headers.get("Tailscale-User-Login", ""),
            ])
            self.send_payload(200, observed + "\n")

        def do_HEAD(self):
            self.do_GET()

        def do_POST(self):
            self.send_payload(405, "method not allowed\n")

    ThreadingHTTPServer(("127.0.0.1", 8787), Handler).serve_forever()
  '';
  collector = user: cfg.systemd.services."codex-usage-collector-${user}";
  codexPackagePath = builtins.unsafeDiscardStringContext (
    toString cfg.services.codexUsageDashboard.codexPackage
  );
  dashboardPackagePath = builtins.unsafeDiscardStringContext (toString fakeDashboard);
  expectedCodexVersionTargets = extraCodexVersionTargets // {
    "${codexPackagePath}/bin/.codex-wrapped" = "0.149.0";
    "${codexPackagePath}/bin/codex" = "0.149.0";
    "${codexPackagePath}/bin/codex-raw" = "0.149.0";
  };
  expectedHistoricalCodexStoreItem = fakeHistoricalCodexPath;
  expectedCodexVersionTargetFlags = builtins.unsafeDiscardStringContext (
    lib.concatMapStringsSep " " (
      target:
      lib.escapeShellArgs [
        "--codex-version-target"
        "${target}=${expectedCodexVersionTargets.${target}}"
      ]
    ) (lib.sort builtins.lessThan (builtins.attrNames expectedCodexVersionTargets))
  );
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
      message = "the dashboard and 23 explicit collector units must be generated";
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
        publicProxy.serviceConfig.DynamicUser
        && publicProxy.serviceConfig.LoadCredential == [
          "dashboard.htpasswd:/run/secrets/dashboard.htpasswd"
        ]
        && publicProxy.serviceConfig.RuntimeDirectory == "codex-dashboard-public-proxy"
        && publicProxy.serviceConfig.AmbientCapabilities == ""
        && publicProxy.serviceConfig.CapabilityBoundingSet == ""
        && publicProxy.serviceConfig.IPAddressDeny == "any"
        && publicProxy.serviceConfig.IPAddressAllow == [ "localhost" ]
        && publicProxy.serviceConfig.LimitCORE == 0
        && publicProxy.serviceConfig.MemoryDenyWriteExecute == true
        && publicProxy.serviceConfig.ProtectHome == true
        && publicProxy.serviceConfig.ProtectSystem == "strict"
        && builtins.elem "~@privileged" publicProxy.serviceConfig.SystemCallFilter
        && publicProxy.serviceConfig.UMask == "0077"
        && builtins.elem "codex-usage-dashboard.service" publicProxy.requires;
      message = "the password proxy credential delivery or hardening changed";
    }
    {
      assertion =
        lib.hasInfix "listen 127.0.0.1:8788 default_server" publicNginx
        && lib.hasInfix "access_log off" publicNginx
        && lib.hasInfix "error_log stderr crit" publicNginx
        && lib.hasInfix "server_name _" publicNginx
        && lib.hasInfix "return 421" publicNginx
        && lib.hasInfix "server_name itphlies.tailb3264.ts.net" publicNginx
        && lib.hasInfix "auth_basic_user_file /run/credentials/codex-dashboard-public-proxy.service/dashboard.htpasswd" publicNginx
        && lib.hasInfix "limit_req_zone $binary_remote_addr zone=codex_dashboard_auth:1m rate=2r/s" publicNginx
        && lib.hasInfix "limit_req_status 429" publicNginx
        && lib.hasInfix "proxy_pass http://127.0.0.1:8787" publicNginx
        && lib.hasInfix "proxy_buffering off" publicNginx
        && lib.hasInfix "proxy_pass_request_headers off" publicNginx
        && lib.hasInfix "proxy_set_header Host itphlies.tailb3264.ts.net" publicNginx
        && lib.hasInfix "Strict-Transport-Security" publicNginx
        && !(lib.hasInfix "/run/secrets/dashboard.htpasswd" publicNginx);
      message = "the loopback-only authenticated nginx proxy contract changed";
    }
    {
      assertion =
        publicFunnel.serviceConfig.Type == "simple"
        && lib.hasInfix "tailscale funnel --yes '--https=10000' '--set-path=/' http://127.0.0.1:8788" publicFunnel.serviceConfig.ExecStart
        && !(lib.hasInfix "--bg" publicFunnel.serviceConfig.ExecStart)
        && !(publicFunnel.serviceConfig ? ExecStop)
        && !(publicFunnel.serviceConfig ? ExecStopPost)
        && publicFunnel.serviceConfig.ExecCondition != ""
        && publicFunnel.serviceConfig.LimitCORE == 0
        && publicFunnel.serviceConfig.MemoryDenyWriteExecute == true
        && publicFunnel.serviceConfig.RestrictAddressFamilies == [ "AF_UNIX" ]
        && builtins.elem "~@privileged" publicFunnel.serviceConfig.SystemCallFilter
        && builtins.elem "codex-dashboard-public-proxy.service" publicFunnel.requires
        && builtins.elem "tailscaled.service" publicFunnel.requires
        && !(builtins.elem 8787 cfg.networking.firewall.allowedTCPPorts)
        && !(builtins.elem 8788 cfg.networking.firewall.allowedTCPPorts)
        && !(builtins.elem 10000 cfg.networking.firewall.allowedTCPPorts);
      message = "the collision-safe foreground Funnel route contract changed";
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
        && lib.hasInfix expectedCodexVersionTargetFlags unit.serviceConfig.ExecStart
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
      assertion = cfg.services.codexUsageDashboard.codexVersionTargets == extraCodexVersionTargets;
      message = "the explicit Codex version target registry changed";
    }
    {
      assertion = builtins.any (
        dependency:
        toString dependency == expectedHistoricalCodexStoreItem
        && builtins.getContext (toString dependency) == builtins.getContext (toString fakeHistoricalCodex)
      ) cfg.system.extraDependencies;
      message = "historical Codex target packages must remain rooted in the system closure";
    }
    {
      assertion = builtins.all (
        case: builtins.any (lib.hasInfix case.expectedMessage) (failedTargetAssertionMessages case.targets)
      ) invalidTargetCases;
      message = "invalid Codex version target paths or versions must fail module assertions";
    }
    {
      assertion = builtins.any
        (lib.hasInfix "publicAccess requires services.codexUsageDashboard.enable")
        (failedPublicAssertionMessages { dashboardEnable = false; });
      message = "public access must require the dashboard service";
    }
    {
      assertion = builtins.any
        (lib.hasInfix "publicAccess requires services.tailscale.enable")
        (failedPublicAssertionMessages { dashboardTailscaleEnable = false; });
      message = "public access must require the Tailscale service";
    }
    {
      assertion = builtins.any
        (lib.hasInfix "public proxy and dashboard backend listen ports must differ")
        (failedPublicAssertionMessages { listen = "127.0.0.1:8788"; });
      message = "public access must reject a proxy/backend bind collision";
    }
    {
      assertion = builtins.all
        (messages: builtins.any (lib.hasInfix "absolute runtime path outside the Nix store") messages)
        [
          (failedPublicAssertionMessages { htpasswdFile = ""; })
          (failedPublicAssertionMessages { htpasswdFile = "/nix/store/example-dashboard.htpasswd"; })
        ];
      message = "public access must reject empty or Nix-store credential paths";
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
pkgs.runCommand "codex-usage-dashboard-module-test" { nativeBuildInputs = [ pkgs.curl ]; } ''
  export TAILSCALE_SERVE_JSON='{"TCP":{},"Web":{},"AllowFunnel":{}}'
  for status_json in \
    '{"Self":{"CapMap":{"funnel":[]}}}' \
    '{"Self":{"Capabilities":["funnel"]}}'; do
    TAILSCALE_STATUS_JSON="$status_json" ${publicFunnelPreflight}
  done
  for status_json in \
    '{"Self":{"CapMap":{"https://tailscale.com/cap/funnel":[]}}}' \
    '{"Self":{"Capabilities":["https://tailscale.com/cap/funnel"]}}' \
    '{"Self":{}}'; do
    if TAILSCALE_STATUS_JSON="$status_json" ${publicFunnelPreflight}; then
      echo "Funnel preflight accepted an unauthorized capability fixture" >&2
      exit 1
    fi
  done

  export TAILSCALE_STATUS_JSON='{"Self":{"Capabilities":["funnel"]}}'
  TAILSCALE_SERVE_JSON='{"TCP":{"443":{"HTTPS":true}},"Web":{"itphlies.tailb3264.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}}},"Foreground":{"other-session":{"TCP":{"8443":{"HTTPS":true}},"Web":{"itphlies.tailb3264.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2720"}}}}}}}' \
    ${publicFunnelPreflight}
  for serve_json in \
    '{"TCP":{"10000":{"HTTPS":true}}}' \
    '{"Web":{"itphlies.tailb3264.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8788"}}}},"AllowFunnel":{"itphlies.tailb3264.ts.net:10000":true}}' \
    '{"Foreground":{"other-session":{"TCP":{"10000":{"HTTPS":true}}}}}' \
    '{"Foreground":{"other-session":{"Web":{"other.tail.example:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9999"}}}},"AllowFunnel":{"other.tail.example:10000":true}}}}'; do
    if TAILSCALE_SERVE_JSON="$serve_json" ${publicFunnelPreflight}; then
      echo "Funnel preflight accepted an occupied port-10000 fixture" >&2
      exit 1
    fi
  done

  mkdir -p \
    "$TMPDIR/credentials/codex-dashboard-public-proxy.service" \
    "$TMPDIR/run"
  substitute ${publicNginxFile} "$TMPDIR/nginx.conf" \
    --replace-fail "/run/credentials/codex-dashboard-public-proxy.service" \
      "$TMPDIR/credentials/codex-dashboard-public-proxy.service" \
    --replace-fail "/run/codex-dashboard-public-proxy" "$TMPDIR/run"
  printf '%s\n' \
    'dashboard:$2y$12$pnaQ0NAVcVGzhZT86Is5UO/SF8OZdot4xOOf9xlawZTp0nR/GiVGa' \
    > "$TMPDIR/credentials/codex-dashboard-public-proxy.service/dashboard.htpasswd"
  ${pkgs.nginx}/bin/nginx -e stderr -t -c "$TMPDIR/nginx.conf"

  ${pkgs.python3}/bin/python ${fakePublicUpstream} &
  upstream_pid=$!
  ${pkgs.nginx}/bin/nginx -e stderr -c "$TMPDIR/nginx.conf" &
  nginx_pid=$!
  cleanup() {
    kill "$nginx_pid" "$upstream_pid" 2>/dev/null || true
    wait "$nginx_pid" "$upstream_pid" 2>/dev/null || true
  }
  trap cleanup EXIT

  ready=false
  for _attempt in $(seq 1 100); do
    code="$(curl --silent --output /dev/null --write-out '%{http_code}' \
      --header 'Host: itphlies.tailb3264.ts.net:10000' \
      http://127.0.0.1:8788/healthz || true)"
    if test "$code" = 401; then
      ready=true
      break
    fi
  done
  test "$ready" = true

  curl --silent --dump-header "$TMPDIR/reject.headers" --output "$TMPDIR/reject.body" \
    --header 'Host: attacker.example' http://127.0.0.1:8788/api/v1/status
  grep -q '^HTTP/1.1 421 ' "$TMPDIR/reject.headers"
  ! grep -qi '^WWW-Authenticate:' "$TMPDIR/reject.headers"

  for path in / /assets/app.js /api/v1/status /api/v1/history /api/v1/events /healthz; do
    curl --silent --dump-header "$TMPDIR/unauthorized.headers" \
      --output "$TMPDIR/unauthorized.body" \
      --header 'Host: itphlies.tailb3264.ts.net:10000' \
      "http://127.0.0.1:8788$path"
    grep -q '^HTTP/1.1 401 ' "$TMPDIR/unauthorized.headers"
    grep -qi '^WWW-Authenticate: Basic ' "$TMPDIR/unauthorized.headers"
    ! grep -q 'host=itphlies' "$TMPDIR/unauthorized.body"
  done

  test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --user dashboard:wrong \
    --header 'Host: itphlies.tailb3264.ts.net:10000' \
    http://127.0.0.1:8788/api/v1/status)" = 401

  for path in / /assets/app.js /api/v1/status /api/v1/history /healthz; do
    curl --fail --silent --dump-header "$TMPDIR/authorized.headers" \
      --output "$TMPDIR/authorized.body" \
      --user dashboard:not-a-secret-test-value \
      --header 'Host: itphlies.tailb3264.ts.net:10000' \
      --header 'Proxy-Authorization: Basic forged' \
      --header 'Cookie: private=test' \
      --header 'X-Forwarded-For: 203.0.113.1' \
      --header 'Tailscale-User-Login: forged@example.com' \
      "http://127.0.0.1:8788$path"
    grep -q '^host=itphlies.tailb3264.ts.net$' "$TMPDIR/authorized.body"
    grep -q '^authorization=$' "$TMPDIR/authorized.body"
    grep -q '^proxy-authorization=$' "$TMPDIR/authorized.body"
    grep -q '^cookie=$' "$TMPDIR/authorized.body"
    grep -q '^forwarded=$' "$TMPDIR/authorized.body"
    grep -q '^tailscale=$' "$TMPDIR/authorized.body"
  done

  curl --fail --silent --user dashboard:not-a-secret-test-value \
    --header 'Host: itphlies.tailb3264.ts.net:10000' \
    http://127.0.0.1:8788/api/v1/events > "$TMPDIR/events"
  grep -q '^event: snapshot$' "$TMPDIR/events"

  test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --request POST --user dashboard:not-a-secret-test-value \
    --header 'Host: itphlies.tailb3264.ts.net:10000' \
    http://127.0.0.1:8788/api/v1/status)" = 405

  for headers in reject unauthorized authorized; do
    grep -qi '^Cache-Control: no-store' "$TMPDIR/$headers.headers"
    grep -qi '^Content-Security-Policy:' "$TMPDIR/$headers.headers"
    grep -qi '^Strict-Transport-Security:' "$TMPDIR/$headers.headers"
    grep -qi '^X-Content-Type-Options: nosniff' "$TMPDIR/$headers.headers"
    grep -qi '^X-Frame-Options: DENY' "$TMPDIR/$headers.headers"
  done

  saw_rate_limit=false
  for _attempt in $(seq 1 80); do
    code="$(curl --silent --dump-header "$TMPDIR/ratelimited.headers" \
      --output /dev/null --write-out '%{http_code}' \
      --header 'Host: itphlies.tailb3264.ts.net:10000' \
      http://127.0.0.1:8788/healthz || true)"
    if test "$code" = 429; then
      saw_rate_limit=true
      break
    fi
  done
  test "$saw_rate_limit" = true
  grep -qi '^Cache-Control: no-store' "$TMPDIR/ratelimited.headers"
  grep -qi '^Content-Security-Policy:' "$TMPDIR/ratelimited.headers"
  grep -qi '^X-Content-Type-Options: nosniff' "$TMPDIR/ratelimited.headers"
  grep -qi '^X-Frame-Options: DENY' "$TMPDIR/ratelimited.headers"

  trap - EXIT
  cleanup
  touch "$out"
''

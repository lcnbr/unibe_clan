# Codex usage dashboard

This source is vendored into the Unibe Clan flake. The
`codex-usage-dashboard` Clan service assigns it to `itphlies`; the
standalone flake below remains useful for focused development and tests.

A small, local-only dashboard for all eight Codex sessions on `itphlies`,
owned by `codex`, `codex-1`, `codex-2`, `codex-3`, `lcnbr`, `nfink`,
`vhirschi`, and `zeno`. Each collector asks its own `codex app-server` for
the signed-in ChatGPT account and quota windows. The web process receives a
deliberately small snapshot over a peer-authenticated Unix socket and listens
only on `127.0.0.1:8787`.

Tailscale Serve is the intended HTTPS entry point. There is no LAN listener,
Funnel configuration, cloud deployment, external JavaScript, analytics, or
third-party request.

## What the dashboard reports

The UI has two coordinated views:

- a compact one-row-per-user account overview with the full ChatGPT email,
  plan, canonical main weekly usage, remaining percentage, next reset,
  authoritative earned-reset count when OpenAI reports it, freshness, and
  state; and
- a horizontally scrollable weekly timeline with one lane per Linux user,
  anchored next resets, completed reset history, amber server-adjustment
  markers, and an honest “starts on next use” state for unused windows whose
  reset timestamp is still moving.

Both views use the same presentation-only account selection and ordering. A
default-on, case-insensitive `localunitarity*@gmail.com` filter can be disabled
to reveal every configured account. The sort button toggles alphabetical and
priority order. Priority puts active Pro windows first, then “starts on next
use”, then accounts with at least one earned reset credit; within each tier the
radio choice uses either most remaining weekly quota or the soonest anchored
weekly reset, with username as the deterministic final tie-break.

Spark and every other model-specific bucket are excluded from both views. The
collector takes main usage only from App Server's authoritative top-level
`rateLimits` value and requires an exact 10,080-minute window. The detailed
`GET /api/v1/status` response continues to contain all sanitized buckets for
diagnostics, while `GET /api/v1/history` contains only usernames and reset
window/adjustment metadata. The live earned-reset count is not persisted.

Codex App Server exposes rounded whole-number percentages, reset windows, and
sometimes an aggregate `availableCount` for earned rate-limit reset credits.
The dashboard distinguishes an authoritative zero from an unavailable count
and never retains the accompanying opaque credit rows. App Server does not
expose a reliable exact count of messages remaining, so the dashboard never
invents one. A reported 0% can include usage below 0.5%. Historical token
activity is intentionally excluded.

The protocol integration uses the documented `account/read` and
`account/rateLimits/read` methods:

- [Codex App Server documentation](https://learn.chatgpt.com/docs/app-server)
- [Codex pricing and limit behavior](https://learn.chatgpt.com/docs/pricing)

## Architecture and trust boundary

```text
codex{,-1,-2,-3} ─┐
lcnbr              │
nfink              ├─ one collector per UID ── Unix socket ── dashboard ── 127.0.0.1:8787
vhirschi           │    + private app-server      SO_PEERCRED       │
zeno              ─┘                                               └─ Tailscale Serve HTTPS
```

Collectors run as their corresponding Linux users. They start
`codex app-server` over stdio, refresh every 30 seconds, react to account and
rate-limit notifications, recycle after account-file metadata changes, and
restart the app-server at least every five minutes. The dashboard maps the
kernel-reported sender UID to a fixed username; a collector cannot claim
another account.

The dashboard keeps current account snapshots only in memory. An unavailable
refresh retains last-good data, while data older than 90 seconds is marked
stale. Restarting the dashboard discards those live snapshots.

Anchored weekly reset windows and completed reset events are retained for eight
weeks in `/var/lib/codex-usage-dashboard/history.json`. The mode-0600 file is
written atomically and contains no email, plan, credential, account ID, credit,
or model-specific data. A zero-use timestamp must stay fixed across polls
before it is anchored, preventing an unused “now + 7 days” window from
generating false resets every 30 seconds. Tracking begins after this version is
activated; history is not reconstructed retroactively. The state survives
service restarts, NixOS switches, and reboots, but it is not a backup.

Server-reported adjustments are retained separately in the mode-0600
`history-adjustments.json` sidecar. An adjustment is recorded only when an
anchored reset timestamp changes by more than two minutes and the change is
observed more than two minutes before its scheduled boundary, or when the
reported usage percentage decreases inside the same anchored window. Smaller
timestamp differences are ignored as clock/rounding jitter; any non-equivalent
reset report inside the boundary band is treated as the normal window rollover.
A single event can contain both observations. Routine upward usage changes are
not recorded. The collector reacts immediately to App Server's rate-limit
notification and also polls, so the marker records when this service first
observed the changed response; it does not claim to know why OpenAI made
the change. Keeping adjustments in a separate sidecar leaves the core
`history.json` disk schema compatible with the previous deployed generation.

### Privacy and security properties

- A collector stats its own `~/.codex/auth.json` metadata but never opens or
  parses the credential file. Codex App Server accesses its normal account
  state inside the collector user's service.
- Tokens, account IDs, opaque credit IDs, raw JSON-RPC payloads, and raw errors
  are not part of the ingest schema and are not logged.
- Reset and adjustment history contain only the fixed Linux username, reset
  timestamps, observation times, and rounded main weekly usage. The state
  directory is mode `0700` and both files are mode `0600`, so collector users
  cannot read them.
- The Unix ingest socket is inside a mode-`0750` runtime directory. Snapshot
  size, schema, allowed fields, UID, and claimed username are checked.
- The dashboard has `ProtectHome=true`, no home-directory bind, and an
  outbound network deny. Collectors see only their owner's `.codex`
  directory inside their systemd home namespace.
- HTTP is rejected unless the listener uses a literal loopback address. Every
  request must also use that loopback IP or an explicitly configured exact Host
  name; malformed and unrecognized Host values receive HTTP 421, preventing DNS
  rebinding. Responses set a restrictive CSP, defensive browser headers, and
  `Cache-Control: no-store`.
- The UI uses embedded assets and DOM `textContent`; account strings are not
  interpreted as markup.

The HTTP application has no separate login. Full emails and quota details are
therefore visible to local callers and to every tailnet member allowed to
reach this device's HTTPS port by the tailnet policy. That is an intentional
trust decision; narrow the tailnet ACL if the audience should be smaller.

## Build and test

The vendored flake is pinned to the same Nixpkgs revision as the Clan flake,
which packages Codex CLI `0.149.0`. From the `unibe_clan` repository root:

```console
nix flake check --impure -L path:.
nix flake check -L path:./packages/codex-usage-dashboard
nix build --impure -L \
  path:.#nixosConfigurations.itphlies.config.system.build.toplevel \
  --no-link
```

The focused dashboard check builds the application, runs all Go tests normally
and under the race detector, runs the dependency-free account filter/priority
policy tests in Node.js, checks both browser scripts, and evaluates the NixOS
module contract.

For an interactive development shell:

```console
cd packages/codex-usage-dashboard
nix develop
go test ./...
go test -race ./...
```

### Local demo preview

The demo uses synthetic data and an alternate port. It still resolves the
eight local Linux usernames, but it never reads their account state.

```console
cd packages/codex-usage-dashboard
nix run . -- serve \
  --demo \
  --listen 127.0.0.1:8877 \
  --socket /tmp/codex-usage-dashboard-demo.sock
```

Open <http://127.0.0.1:8877/> and stop the preview with Ctrl-C.

## Launch on `itphlies`

The root Clan flake registers the `codex-usage-dashboard` service and assigns
its `server` role only to `itphlies`. An authorized Clan administrator can
deploy it from a workstation that has the repository's SOPS identity:

```console
nix develop -c clan machines update itphlies --flake .
```

Alternatively, an administrator already logged in on `itphlies` can build the
closure as an ordinary user, dry-activate it, test it without changing the boot
default, and then persist that exact closure:

```console
set -euo pipefail
old_system="$(readlink -f /run/current-system)"
old_profile="$(readlink -f /nix/var/nix/profiles/system)"
test "$old_system" = "$old_profile"

recover_previous_system() {
  failed_status="$?"
  trap - ERR
  set +e
  current_profile="$(readlink -f /nix/var/nix/profiles/system)"
  if test "$current_profile" = "$old_profile"; then
    sudo nixos-rebuild test --no-reexec --store-path "$old_system"
  else
    sudo nixos-rebuild switch --no-reexec --store-path "$old_system"
  fi
  test "$(readlink -f /run/current-system)" = "$old_system"
  rollback_status="$?"
  test "$(readlink -f /nix/var/nix/profiles/system)" = "$old_system"
  profile_status="$?"
  if test "$rollback_status" -ne 0 || test "$profile_status" -ne 0; then
    printf 'Automatic recovery did not restore the exact prior closure.\n' >&2
    exit 1
  fi
  exit "$failed_status"
}
trap recover_previous_system ERR

system_path="$(nix build --impure --no-link --print-out-paths \
  'path:.#nixosConfigurations.itphlies.config.system.build.toplevel')"
sudo nixos-rebuild dry-activate --no-reexec --store-path "$system_path"
sudo nixos-rebuild test --no-reexec --store-path "$system_path"
test "$(readlink -f /run/current-system)" = "$system_path"
test "$(readlink -f /nix/var/nix/profiles/system)" = "$old_profile"

candidate_healthy=false
for _attempt in {1..30}; do
  if curl --fail --silent http://127.0.0.1:8787/healthz >/dev/null; then
    candidate_healthy=true
    break
  fi
  sleep 1
done
test "$candidate_healthy" = true

state_dir=/var/lib/codex-usage-dashboard
main_history="$state_dir/history.json"
adjustment_history="$state_dir/history-adjustments.json"
test "$(sudo stat -c '%a %U %G' "$state_dir")" = \
  '700 codex-usage-dashboard codex-usage-dashboard'
test "$(sudo stat -c '%a %U %G' "$main_history")" = \
  '600 codex-usage-dashboard codex-usage-dashboard'
test "$(sudo stat -c '%a %U %G' "$adjustment_history")" = \
  '600 codex-usage-dashboard codex-usage-dashboard'
sudo jq -e '.schemaVersion == 1' "$main_history" >/dev/null
sudo jq -e '.schemaVersion == 1' "$adjustment_history" >/dev/null
curl --fail --silent http://127.0.0.1:8787/api/v1/history |
  jq -e '.schemaVersion == 2 and ((.degraded // false) == false)' >/dev/null

# Prove the exact previous generation still reads v1 history and ignores the
# sidecar, then return to the candidate before making it persistent.
adjustment_checksum="$(sudo sha256sum "$adjustment_history" | cut -d' ' -f1)"
sudo nixos-rebuild test --no-reexec --store-path "$old_system"
test "$(readlink -f /run/current-system)" = "$old_system"
test "$(readlink -f /nix/var/nix/profiles/system)" = "$old_profile"
curl --fail --silent http://127.0.0.1:8787/healthz >/dev/null
sudo jq -e '.schemaVersion == 1' "$main_history" >/dev/null
test "$(sudo sha256sum "$adjustment_history" | cut -d' ' -f1)" = \
  "$adjustment_checksum"
sudo nixos-rebuild test --no-reexec --store-path "$system_path"
test "$(readlink -f /run/current-system)" = "$system_path"
test "$(readlink -f /nix/var/nix/profiles/system)" = "$old_profile"
curl --fail --silent http://127.0.0.1:8787/healthz >/dev/null

# Verify all nine services, then make this exact candidate persistent.
active_unit_count="$(
  sudo systemctl is-active \
    codex-usage-dashboard.service \
    'codex-usage-collector-*.service' |
    grep -c '^active$'
)"
test "$active_unit_count" -eq 9
sudo nixos-rebuild switch --no-reexec --store-path "$system_path"
test "$(readlink -f /run/current-system)" = "$system_path"
test "$(readlink -f /nix/var/nix/profiles/system)" = "$system_path"
trap - ERR
```

`test` and `switch` can return an error after partially activating the candidate;
they do not automatically restore the previous live system. The `ERR` trap keeps
the original paths in scope and restores that exact closure if candidate
activation or verification fails. After recovery, rerun the unit, listener, and
health checks against the restored system before attempting another activation.
The brief compatibility rehearsal cannot record adjustments that occur while
the old binary is active; existing sidecar events remain untouched and reappear
when the candidate is reactivated.

Do not substitute a generic `nixos-rebuild --rollback`; it may select a
different older generation.

Both routes create the dedicated `codex-usage-dashboard` user/group, start the
read-only web service, and start one collector for each of the eight Codex
users. The root flake deliberately leaves Tailscale ownership with the existing
Clan Tailscale service.

### Reusing the standalone module

The reusable module is exported as `nixosModules.default`. The safest host
configuration uses both the dashboard and Codex packages from this project's
locked input, so the module's `0.149.0` compatibility assertion is
reproducible.

Add the following input to the consuming flake after this change is published:

```nix
inputs.codex-usage-dashboard.url =
  "github:lcnbr/unibe_clan?dir=packages/codex-usage-dashboard";
```

Then add the module and service configuration to the machine's module list.
This example assumes the existing NixOS configuration already declares all
eight collector users:

```nix
{
  inputs,
  pkgs,
  ...
}:

{
  imports = [
    inputs.codex-usage-dashboard.nixosModules.default
  ];

  services.codexUsageDashboard = {
    enable = true;
    package =
      inputs.codex-usage-dashboard.packages.${pkgs.system}.default;
    codexPackage =
      inputs.codex-usage-dashboard.packages.${pkgs.system}.codex-cli;

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
    allowedHosts = [ "<machine>.<tailnet>.ts.net" ];
    tailscale.enable = true;
  };
}
```

Do not make this project's `nixpkgs` input follow a newer host input unless
that input still packages Codex `0.149.0`. The module intentionally refuses
to evaluate if `codexPackage` and `expectedCodexVersion` differ.

Activate the configuration, replacing the flake output name if necessary:

```console
sudo nixos-rebuild switch --flake /etc/nixos#itphlies
```

That command creates the dedicated `codex-usage-dashboard` user/group,
starts the read-only dashboard, starts eight per-user collectors, creates the
runtime directory/socket, and enables `tailscaled`. No separate process
manager or manual background command is needed for the application.

If one of the eight users still needs a ChatGPT login, perform it as that user,
never as root:

```console
sudo -iu codex codex login
sudo -iu codex-1 codex login
sudo -iu codex-2 codex login
sudo -iu codex-3 codex login
sudo -iu lcnbr codex login
sudo -iu nfink codex login
sudo -iu vhirschi codex login
sudo -iu zeno codex login
```

Collectors detect a file-backed account switch within about 30 seconds.
Keyring or other external changes are picked up by the five-minute recycle
fallback.

### Verify the local services

```console
sudo systemctl status \
  codex-usage-dashboard.service \
  codex-usage-collector-codex.service \
  codex-usage-collector-codex-1.service \
  codex-usage-collector-codex-2.service \
  codex-usage-collector-codex-3.service \
  codex-usage-collector-lcnbr.service \
  codex-usage-collector-nfink.service \
  codex-usage-collector-vhirschi.service \
  codex-usage-collector-zeno.service

curl --fail --silent http://127.0.0.1:8787/healthz
curl --fail --silent http://127.0.0.1:8787/api/v1/status
curl --no-buffer http://127.0.0.1:8787/api/v1/events

test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header 'Host: attacker.invalid' \
  http://127.0.0.1:8787/healthz)" = 421
```

`/healthz` contains no account data. The status and event endpoints do
contain full account emails.

Confirm that port `8787` is loopback-only and that the socket permissions are
restricted:

```console
ss -ltnp '( sport = :8787 )'
stat -c '%A %U %G %n' /run/codex-usage-dashboard/ingest.sock
sudo -u codex-usage-dashboard test ! -r /home/codex/.codex/auth.json
```

The listener output must show `127.0.0.1:8787`, never `0.0.0.0:8787`, a
LAN address, or `[::]:8787`. Repeat the unreadability check for the other
seven home directories if desired. Service logs should contain state changes
and safe error categories only:

```console
sudo journalctl -u 'codex-usage-*' --since boot
```

Do not paste that journal or `/api/v1/status` into an untrusted channel
without reviewing it, because the status response intentionally contains
emails.

## Tailnet-only HTTPS with Tailscale Serve

Tailscale Serve proxies the loopback service only to tailnet clients. It is
different from Funnel, which publishes to the internet. See the
[Tailscale Serve guide](https://tailscale.com/docs/features/tailscale-serve)
and [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve).

First confirm the daemon and login state:

```console
sudo systemctl enable --now tailscaled
tailscale status
```

If the machine is not yet authenticated, run `sudo tailscale up` and finish
the one-time browser login. Check for any existing Funnel permission,
configure persistent background Serve, and inspect both JSON views:

```console
tailscale funnel status --json
sudo tailscale serve --bg --https=443 --set-path=/ 8787
tailscale serve status --json
tailscale funnel status --json
```

The first Serve command can request one-time HTTPS certificate consent.
`tailscale serve status` prints the exact
`https://<machine>.<tailnet>.ts.net/` URL. Background Serve configuration is
stored by Tailscale and survives reboot. In Tailscale 1.102.2,
`tailscale funnel status --json` mirrors the shared Serve configuration even
when Funnel is disabled; verify that its `AllowFunnel` object is absent or
empty rather than expecting the whole response to be empty.

These commands use the route-specific syntax from Tailscale 1.102.2. If Funnel
is active for this dashboard's HTTPS 443 root route, first confirm that the
route is not owned by another service, then remove only that route with
`sudo tailscale funnel --https=443 --set-path=/ off` and verify its status
again.

If this dashboard's Serve root route is configured incorrectly, remove only
that route with `sudo tailscale serve --https=443 --set-path=/ off`, then run
the Serve command above again. Do not use the broad `serve reset` or
`funnel reset` commands because they also remove unrelated routes. Never run
`tailscale funnel --bg --https=443 --set-path=/ 8787` for this dashboard.

From another tailnet member:

```console
curl --fail https://itphlies.tailb3264.ts.net/healthz
```

If that is blocked, update the tailnet access policy to permit the intended
members to reach this device on TCP 443. Do not open TCP 8787. From a LAN-only
device without Tailscale, this must fail:

```console
curl --connect-timeout 3 http://<lan-ip-of-this-machine>:8787/healthz
```

After a reboot, repeat `systemctl status`, `tailscale serve status`, and the
remote HTTPS health check. All eight account rows should populate within 60 seconds;
ordinary rate-limit changes should appear within 30 seconds.

## Read-only HTTP interface

| Path | Purpose |
| --- | --- |
| `GET /` | Embedded responsive dashboard |
| `GET /api/v1/status` | Versioned snapshot for all eight users |
| `GET /api/v1/history` | Private reset-window history without account emails |
| `GET /api/v1/events` | Server-Sent Events, including initial state and updates |
| `GET /healthz` | Process health without account data |

There are no mutating HTTP routes.

## Codex App Server upgrade procedure

App Server is experimental, so changing Codex versions is an explicit
compatibility task:

1. Update the Nixpkgs input only to a revision containing the candidate Codex
   package, then verify it with
   `nix eval --raw .#packages.x86_64-linux.codex-cli.version`.
2. Generate the candidate's protocol bundle in a temporary directory:

   ```console
   schema_dir="$(mktemp -d)"
   nix shell .#codex-cli -c codex app-server generate-json-schema \
     --experimental --out "$schema_dir"
   ```

3. Compare `account/read`, `account/rateLimits/read`, their nullable fields,
   and update-notification shapes against the typed collector code. Update the
   types and compatibility fixtures deliberately; never fall back to
   forwarding raw protocol objects.
4. Run `nix flake check -L`, including normal tests, the race detector,
   sanitizer tests, and the NixOS module test.
5. Test signed-in, signed-out, API-key, multi-bucket, sparse-update, restart,
   and on-machine account-switch behavior.
6. Only after all checks pass, update
   `services.codexUsageDashboard.expectedCodexVersion`, the flake comment,
   and the lock file in the same change.

Until that procedure is complete, keep the Codex CLI pin at `0.149.0`.

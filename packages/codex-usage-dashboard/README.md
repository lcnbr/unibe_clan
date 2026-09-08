# Codex usage dashboard

This source is vendored into the Unibe Clan flake. The
`codex-usage-dashboard` Clan service assigns it to `itphlies`; the
standalone flake below remains useful for focused development and tests.

A small dashboard for every normal user on `itphlies`: 23
collectors after adding the six persistent `codex-dummy-{0..5}` anchor users.
Each collector asks its own `codex app-server` for the signed-in ChatGPT
account, quota windows, and optional lifetime usage. The dashboard groups those
snapshots into one row per OpenAI account. It receives allowlisted collector and
hook data over separate peer-authenticated Unix sockets and listens only on
`127.0.0.1:8787`.

Tailscale Serve provides the private tailnet HTTPS entry point. A separate
password-authenticated loopback proxy can be published by Tailscale Funnel on
port 10000 without changing the private route. There is no LAN listener,
external JavaScript, analytics, or third-party request from the dashboard.

## What the dashboard reports

The UI has three coordinated views:

- a compact one-row-per-account overview with the full ChatGPT email, plan,
  canonical main weekly usage, remaining percentage, next reset,
  authoritative earned-reset count when OpenAI reports it, optional all-Codex
  lifetime tokens, anchor health, consumer-user and active-chat dropdowns,
  freshness, and state;
- a compact one-row-per-Linux-user account mapping with anchor/consumer role,
  the last observed immutable Codex CLI package version, its observation time,
  current active chats or explicit collection coverage, freshness, and state;
  and
- a horizontally scrollable weekly timeline with one lane per opaque account,
  anchored next resets, completed reset history, explicitly labelled inferred
  early/new-window reset points, amber server-adjustment markers, and an honest
  “starts on next use” state for unused windows whose reset timestamp is still
  moving.

The consumer dropdown excludes dummy anchors. Active chats show only currently
running work as a bounded, sanitized `task name — username`; idle loaded
sessions remain private and are omitted. A known-complete scan with no running
work is shown as zero, while a missing or failed scan is shown as `Unknown` per
consumer. The account row also shows `Unknown` when no chat is visible and any
mapped consumer lacks coverage. Anchors remain visible through anchor health
and use `—` in the per-user chat column, as do unassigned users. A chat
disappears on `Stop` or `SessionEnd`, or after 30 minutes with no hook event.

Each collector finds the newest-started live Codex process owned by its Linux
user and reads its immutable Nix-store package metadata without executing the
discovered binary. Only full executable paths in the administrator-managed
version registry are eligible. The collector rejects its own processes,
writable or malformed targets, cross-mount inode mismatches, and
process-identity races. The newest observation is retained across account
switches and transient failures, but never persisted. Standalone, newly updated,
or otherwise unregistered installations are shown as unknown rather than
guessed.

All three views use the same presentation-only account selection and ordering. A
default-on, case-insensitive `localunitarity*@gmail.com` filter can be disabled
to reveal every configured account. The sort button toggles alphabetical and
priority order. Priority puts active Pro windows first, then “starts on next
use”, then accounts with at least one earned reset credit; within each tier the
radio choice uses either most remaining weekly quota or the soonest anchored
weekly reset, with account label and opaque key as deterministic tie-breaks.

Spark and every other model-specific bucket are excluded from both views. The
collector takes main usage only from App Server's authoritative top-level
`rateLimits` value and requires an exact 10,080-minute window. The detailed
`GET /api/v1/status` response continues to contain all sanitized buckets for
diagnostics, while `GET /api/v1/history` contains only opaque account keys and
reset window/adjustment metadata. Earned-reset counts, lifetime tokens, emails,
chat names, and user membership are not persisted.

Codex App Server exposes rounded whole-number percentages, reset windows, and
sometimes an aggregate `availableCount` for earned rate-limit reset credits.
The dashboard distinguishes an authoritative zero from an unavailable count
and never retains the accompanying opaque credit rows. App Server does not
expose a reliable exact count of messages remaining, so the dashboard never
invents one. A reported 0% can include usage below 0.5%. The optional lifetime
token total is the all-Codex account total (the API exposes no reliable
Spark-excluded lifetime total); it is display-only and no token history is
persisted.

The protocol integration uses the documented `account/read`,
`account/rateLimits/read`, and optional `account/usage/read` methods:

- [Codex App Server documentation](https://learn.chatgpt.com/docs/app-server)
- [Codex pricing and limit behavior](https://learn.chatgpt.com/docs/pricing)

## Architecture and trust boundary

```text
23 local users ── private app-servers ── ingest.sock ─┐
                                                     ├─ dashboard ── 127.0.0.1:8787
Codex clients ── system-managed hooks ─ activity.sock ┘      │
                 (SO_PEERCRED on both sockets)               ├─ Tailscale Serve :443 (tailnet)
                                                            └─ nginx :8788 ─ Funnel :10000 (public)
```

Collectors run as their corresponding Linux users. Until authentication
material exists, a collector publishes signed-out once and idles without an
app-server. Once authenticated, it starts `codex app-server` over stdio,
refreshes every 30 seconds, reacts to account and rate-limit notifications,
recycles after account-file metadata changes, and restarts the app-server at
least every five minutes. The dashboard maps the kernel-reported sender UID to
a fixed username; a collector or hook cannot claim another user.

On each refresh, the collector performs a bounded, read-only scan for live
same-user Codex processes. It accepts only an exact Codex executable in an
administrator-registered, immutable, store-owned Nix output, verifies that the
process executable and host path identify the same file, and uses the
registered, strictly validated version token. It never executes a discovered
process binary and does not open or parse authentication material. The
dashboard keeps the newest successful observation for that Linux user across
sign-out, account switching, and transient discovery failures.

The dashboard keeps current account snapshots only in memory. An unavailable
refresh retains last-good data, while data older than 90 seconds is marked
stale. Snapshots are accepted monotonically by collector observation time for
each Linux user: delayed or duplicate deliveries are acknowledged but ignored.
A newer unavailable observation retains last-good account data, while only a
newer signed-out or API-key observation clears account membership. Restarting
the dashboard discards those live snapshots.

Account-keyed anchored weekly reset windows and completed reset events are
retained for 366 days in
`/var/lib/codex-usage-dashboard/account-history.json`. The mode-0600 file is
written atomically and contains no email, plan, credential, account ID, user
membership, token count, credit, chat name, or model-specific data. A zero-use
timestamp must stay fixed across polls
before it is anchored, preventing an unused “now + 7 days” window from
generating false resets every 30 seconds. Tracking begins after this version is
activated; history is not reconstructed retroactively. The state survives
service restarts, NixOS switches, and reboots, but it is not a backup.

Server-reported adjustments are retained separately in the mode-0600
`account-adjustments.json` sidecar. An adjustment is recorded only when an
anchored reset timestamp changes by more than two minutes and the change is
observed more than two minutes before its scheduled boundary, or when the
reported usage percentage decreases inside the same anchored window. Smaller
timestamp differences are ignored as clock/rounding jitter; any non-equivalent
reset report inside the boundary band is treated as the normal window rollover.
A single event can contain both observations. Routine upward usage changes are
not recorded. The collector reacts immediately to App Server's rate-limit
notification and also polls, so the marker records when this service first
observed the changed response; it does not claim to know why OpenAI made
the change. Only the canonical account snapshot is observed, so duplicate
collectors cannot create resets or adjustments. The legacy username-keyed
`history.json` and `history-adjustments.json` files are left untouched for
rollback and audit; the service never guesses how an old username lane maps to
an account.

The public history schema derives `resetPoints` from both completed scheduled
events and the existing adjustment sidecar. An `inferred_early` point is emitted
only when the service observed both lower usage and a changed reset timestamp,
and the server-reported new seven-day window began no more than 24 hours before
the observation (with five minutes of future clock tolerance). Its timestamp is
the reported new-window start and its separate detection time is retained. The
label is deliberately an inference: App Server does not provide a definitive
"reset happened" event or identify whether a reset credit caused the change.
The v2 disk formats remain unchanged, so already-retained adjustments produce
these points immediately after upgrade and the legacy files are never migrated.

History uses a sticky source per account even though the overview continues to
show the freshest canonical snapshot. A healthy expected anchor takes priority;
otherwise one deterministic fresh consumer remains selected. Brief unavailable
polls pause history without discarding continuity, and conflicting unanchored
consumer snapshots cannot mutate it. A source handoff or process restart still
rebases ordinary collector discrepancies, while preserving a clear scheduled
rollover or plausible inferred early reset against the persisted active window.
Per-account events, adjustments, and public reset points are each bounded at
1,024 entries and time-pruned to 366 days; aggregate disk input remains bounded
at 16 MiB.

### Privacy and security properties

- A collector stats its own `~/.codex/auth.json` metadata but never opens or
  parses the credential file. Codex App Server accesses its normal account
  state inside the collector user's service.
- Credentials, account IDs, opaque credit IDs, raw JSON-RPC payloads, and raw
  errors are not part of the ingest schema and are not logged.
- Reset and adjustment history contain only opaque account keys, reset
  timestamps, observation times, and rounded main weekly usage. The state
  directory is mode `0700` and both files are mode `0600`, so collector users
  cannot read them.
- The Unix sockets are mode `0660` inside a non-listable mode-`0711` runtime
  directory. The collector ingest socket remains owned by the dedicated
  dashboard group; the activity socket uses the stable `users` group so Codex
  processes started before a deployment can still reach it. Payload size,
  schema, allowed fields, and kernel-reported peer UID are checked, so socket
  access alone never authorizes an unconfigured user.
- `/etc/codex/requirements.toml` enables managed hooks for
  `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, and `SessionEnd`.
  The reporter discards prompts, previews, tool inputs, paths, repository and
  environment metadata. It forwards only bounded opaque session/turn lookup
  keys to the peer-authenticated activity socket; those keys and sanitized chat
  names remain only in memory and are never returned, logged, or persisted.
- The dashboard has `ProtectHome=true`, no home-directory bind, and an
  outbound network deny. Collectors see only their owner's `.codex`
  directory inside their systemd home namespace.
- HTTP is rejected unless the listener uses a literal loopback address. Every
  request must also use that loopback IP or an explicitly configured exact Host
  name; malformed and unrecognized Host values receive HTTP 421, preventing DNS
  rebinding. Responses set a restrictive CSP, defensive browser headers, and
  `Cache-Control: no-store`.
- The optional public path terminates at a dedicated nginx process bound only
  to `127.0.0.1:8788`. It rejects unknown Host values before authentication,
  authenticates every path from a runtime systemd credential, rate-limits
  requests, disables buffering for SSE, and forwards no client headers other
  than the canonical Host value. The bcrypt verifier is SOPS-encrypted and
  never enters the Nix store; the generated plaintext password is retained
  only as a non-deployed encrypted Clan var.
- The UI uses embedded assets and DOM `textContent`; account strings are not
  interpreted as markup.

The Go HTTP application has no login of its own. Full emails and quota details
are visible to local callers and to every tailnet member allowed to reach the
private HTTPS port by the tailnet policy. The public Funnel route must target
the authenticated proxy on port 8788, never the application on port 8787.

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

The demo uses synthetic account data and an alternate port. It never reads
local authentication state.

```console
cd packages/codex-usage-dashboard
nix run . -- serve \
  --demo \
  --listen 127.0.0.1:8877 \
  --socket /tmp/codex-usage-dashboard-demo.sock \
  --activity-socket /tmp/codex-usage-dashboard-demo-activity.sock
```

Open <http://127.0.0.1:8877/> and stop the preview with Ctrl-C.

## Launch on `itphlies`

The root Clan flake registers the `codex-usage-dashboard` service and assigns
its `server` role only to `itphlies`. An authorized Clan administrator can
deploy it from a workstation that has the repository's SOPS identity.

The live NSS check on 2026-09-08 found no entries at UIDs 1131–1132 and found
1130 (`codex-dummy-3`) as the highest configured normal-user UID. Re-run that
check immediately before deployment. If any unrelated account now occupies the
block, stop and move both declarations to the lowest contiguous free block
above the current maximum. Before activation, evaluate both machines and
confirm that `itphlies` has 23 normal users while `itppeach` remains at 17 and
has no `codex-dummy-*` users:

```console
getent passwd 1131 1132
nix eval --impure --json \
  path:.#nixosConfigurations.itphlies.config.services.codexUsageDashboard.users
nix eval --impure --raw \
  path:.#nixosConfigurations.itphlies.config.system.build.toplevel.drvPath
nix eval --impure --raw \
  path:.#nixosConfigurations.itppeach.config.system.build.toplevel.drvPath
```

Deploy the backend and its embedded frontend in the same Clan activation; their
status and history schemas intentionally change together:

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
state_dir=/var/lib/codex-usage-dashboard
legacy_history_mtime="$(sudo stat -c %Y "$state_dir/history.json" 2>/dev/null || true)"
legacy_adjustment_mtime="$(sudo stat -c %Y "$state_dir/history-adjustments.json" 2>/dev/null || true)"

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

main_history="$state_dir/account-history.json"
adjustment_history="$state_dir/account-adjustments.json"
test "$(sudo stat -c '%a %U %G' "$state_dir")" = \
  '700 codex-usage-dashboard codex-usage-dashboard'
test "$(sudo stat -c '%a %U %G' "$main_history")" = \
  '600 codex-usage-dashboard codex-usage-dashboard'
test "$(sudo stat -c '%a %U %G' "$adjustment_history")" = \
  '600 codex-usage-dashboard codex-usage-dashboard'
sudo jq -e . "$main_history" >/dev/null
sudo jq -e . "$adjustment_history" >/dev/null
curl --fail --silent http://127.0.0.1:8787/api/v1/history |
  jq -e '.schemaVersion == 4 and ((.degraded // false) == false)' >/dev/null

# The account-keyed rollout must not rewrite the legacy username-keyed files.
test "$(sudo stat -c %Y "$state_dir/history.json" 2>/dev/null || true)" = \
  "$legacy_history_mtime"
test "$(sudo stat -c %Y "$state_dir/history-adjustments.json" 2>/dev/null || true)" = \
  "$legacy_adjustment_mtime"

# Verify the dashboard and 23 collectors, then persist this exact candidate.
active_unit_count="$(
  sudo systemctl is-active \
    codex-usage-dashboard.service \
    'codex-usage-collector-*.service' |
    grep -c '^active$'
)"
test "$active_unit_count" -eq 24
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

Do not substitute a generic `nixos-rebuild --rollback`; it may select a
different older generation.

Both routes create the dedicated `codex-usage-dashboard` user/group, start the
read-only web service, start one collector for each of the 23 normal itphlies
users, install the pinned Codex CLI system-wide, and install managed hooks at
`/etc/codex/requirements.toml`. The root flake deliberately leaves Tailscale
ownership with the existing Clan Tailscale service. A dedicated oneshot waits
for `zfs-user-datasets.service` and then safely reapplies the scoped
`systemd-tmpfiles` rules for each private `.codex` directory, so collectors
cannot bind a path hidden by a later home-dataset mount.

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
This example collects every already-declared normal user:

```nix
{
  config,
  inputs,
  lib,
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

    users = lib.attrNames (
      lib.filterAttrs (_: user: user.isNormalUser or false) config.users.users
    );
    expectedAnchors = {
      codex-dummy-0 = "localunitarity@gmail.com";
      codex-dummy-1 = "localunitarity+1@gmail.com";
      codex-dummy-2 = "localunitarity+2@gmail.com";
      codex-dummy-3 = "localunitarity+3@gmail.com";
      codex-dummy-4 = "localunitarity+4@gmail.com";
      codex-dummy-5 = "localunitarity+5@gmail.com";
    };
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
starts the read-only dashboard and per-user collectors, creates both runtime
sockets, installs the pinned CLI and system-managed hooks, and enables
`tailscaled`. No separate process manager or manual background command is
needed for the application.

After the first itphlies activation, authenticate the six anchors
interactively as their own users, never as root. Complete each device flow with
the exact expected alias before starting the next one; never copy or commit
`auth.json`:

```console
sudo -iu codex-dummy-0 codex login --device-auth
sudo -iu codex-dummy-1 codex login --device-auth
sudo -iu codex-dummy-2 codex login --device-auth
sudo -iu codex-dummy-3 codex login --device-auth
sudo -iu codex-dummy-4 codex login --device-auth
sudo -iu codex-dummy-5 codex login --device-auth
```

The expected aliases are, in order, `localunitarity@gmail.com`,
`localunitarity+1@gmail.com`, `localunitarity+2@gmail.com`,
`localunitarity+3@gmail.com`, `localunitarity+4@gmail.com`, and
`localunitarity+5@gmail.com`. Gmail dots and `+` suffixes are deliberately not
normalized away. Collectors detect file-backed account switches within about
30 seconds; keyring or other external changes are picked up by the five-minute
recycle fallback.

### Verify the local services

```console
sudo systemctl status 'codex-usage-*'
test "$(systemctl is-active \
  codex-usage-dashboard.service \
  'codex-usage-collector-*.service' | grep -c '^active$')" -eq 24

curl --fail --silent http://127.0.0.1:8787/healthz
curl --fail --silent http://127.0.0.1:8787/api/v1/status |
  jq -e '
    .schemaVersion == 2 and
    (.accounts | type == "array") and
    (.unassignedUsers | type == "array") and
    all(.accounts[]; (.lifetimeTokens == null) or
      (.lifetimeTokens | type == "number"))
  '
curl --no-buffer http://127.0.0.1:8787/api/v1/events

test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --header 'Host: attacker.invalid' \
  http://127.0.0.1:8787/healthz)" = 421
```

`/healthz` contains no account data. The status and event endpoints do
contain full account emails.

Confirm the locked dummy identities, ZFS datasets, managed policy, loopback
listener, and both socket permissions:

```console
getent passwd codex-dummy-{0..5}
for user in codex-dummy-{0..5}; do
  groups="$(id -nG "$user" | tr ' ' '\n')"
  grep -qx users <<<"$groups"
  grep -qx codex-usage-dashboard <<<"$groups"
  ! grep -Eq '^(wheel|nfs)$' <<<"$groups"
  test "$(sudo passwd -S "$user" | awk '{print $2}')" = 'L'
  test "$(stat -c '%a %U %G' "/home/$user")" = "755 $user users"
done
sudo sshd -T | grep '^denyusers ' | grep -q 'codex-dummy-5'
sudo zfs list \
  zroot/local/home/codex-dummy-0 \
  zroot/local/home/codex-dummy-1 \
  zroot/local/home/codex-dummy-2 \
  zroot/local/home/codex-dummy-3 \
  zroot/local/home/codex-dummy-4 \
  zroot/local/home/codex-dummy-5
codex --version
test -r /etc/codex/requirements.toml
grep -q '^hooks = true$' /etc/codex/requirements.toml
ss -ltnp '( sport = :8787 )'
stat -c '%A %U %G %n' /run/codex-usage-dashboard/ingest.sock
stat -c '%A %U %G %n' /run/codex-usage-dashboard/activity.sock
stat -c '%A %U %G %n' /run/codex-usage-dashboard
sudo -u codex-usage-dashboard test ! -r /home/codex/.codex/auth.json
```

The listener output must show `127.0.0.1:8787`, never `0.0.0.0:8787`, a
LAN address, or `[::]:8787`. Both sockets must be `srw-rw----`: ingest is owned
by `codex-usage-dashboard:codex-usage-dashboard`, activity by
`codex-usage-dashboard:users`, and the runtime directory is `drwx--x--x`.
Repeat the unreadability check for other home directories if desired. Service
logs should contain state changes and safe error categories only:

```console
sudo journalctl -u 'codex-usage-*' --since boot
```

Do not paste that journal or `/api/v1/status` into an untrusted channel
without reviewing it, because the status response intentionally contains
emails.

After anchor login, require one account with `anchorHealth == "ok"` for each
expected alias. The other valid health values are `stale`, `signed_out`, and
`wrong_account`:

```console
curl --fail --silent http://127.0.0.1:8787/api/v1/status |
  jq -e '
    [
      "localunitarity@gmail.com",
      "localunitarity+1@gmail.com",
      "localunitarity+2@gmail.com",
      "localunitarity+3@gmail.com",
      "localunitarity+4@gmail.com",
      "localunitarity+5@gmail.com"
    ] as $expected |
    all($expected[] as $email;
      any(.accounts[];
        .account.email == $email and .anchorHealth == "ok"))
  '
```

Start a short Codex turn as a consumer and verify its sanitized entry appears
under `.accounts[].activeChats[]`, then stops. Restarting the dashboard must
clear all active-chat memory. The UI should show anchors only through health,
consumer users in the Users dropdown, and `task name — username` in Active
chats.

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
remote HTTPS health check. Account rows should populate within 60 seconds;
ordinary rate-limit changes should appear within 30 seconds.

## Password-protected public HTTPS without a custom domain

The itphlies configuration generates a random 48-character password, stores it
encrypted in Clan vars, deploys only a bcrypt verifier through a systemd
credential, and starts the authentication proxy on `127.0.0.1:8788`. Retrieve
the shared credential locally; this command intentionally prints it:

```console
nix develop path:/common/nix/clan -c \
  clan vars get itphlies codex-dashboard-public-auth/password
```

The fixed Basic authentication username is `dashboard`. Browsers cache Basic
credentials and there is no per-person identity or revocation. If the password
is disclosed, rotate it for everyone and redeploy:

```console
nix develop path:/common/nix/clan -c \
  clan vars generate itphlies \
    --generator codex-dashboard-public-auth --regenerate
nix develop path:/common/nix/clan -c \
  clan machines update itphlies --flake /common/nix/clan
```

Because Funnel terminates the public connection before nginx, the proxy's
request and connection limits are intentionally global rather than per-client.
Sustained abusive traffic can therefore produce HTTP 429 for every public user
until the shared bucket recovers.

Funnel authorization is deliberately outside the Nix configuration. Before
starting the public unit, grant the `funnel` node attribute only to the
itphlies node (currently `100.87.156.102`) in the tailnet policy. Do not grant
the shared `tag:itppeach`, which is used by multiple machines. Then start the
preflighted foreground route:

```console
sudo systemctl start codex-dashboard-public-funnel.service
```

The preflight skips activation when the node lacks permission and refuses to
replace an unrelated port-10000 route. The resulting public URL is:

```text
https://itphlies.tailb3264.ts.net:10000/
```

Verify that every public path requires authentication, while the original
tailnet-only route remains unchanged:

```console
test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  https://itphlies.tailb3264.ts.net:10000/api/v1/status)" = 401
curl --fail --user dashboard \
  https://itphlies.tailb3264.ts.net:10000/healthz
tailscale serve status --json
tailscale funnel status --json
```

`curl --user dashboard` prompts for the password instead of placing it in the
process argument list. Stop the foreground Funnel session with:

```console
sudo systemctl stop codex-dashboard-public-funnel.service
tailscale serve status --json
```

The foreground route is owned by the running CLI session and disappears when
that service stops. The unit deliberately never runs `serve ... off`: doing so
could remove a route owned by another process after a failed preflight.

Never use `serve reset` or `funnel reset`, and never point Funnel directly at
port 8787. Tailscale Funnel is public, beta, relay-dependent, and subject to
non-configurable bandwidth limits.

## Read-only HTTP interface

| Path | Purpose |
| --- | --- |
| `GET /` | Embedded responsive dashboard |
| `GET /api/v1/status` | Versioned account-grouped snapshot, users, anchors, and active chats |
| `GET /api/v1/history` | Private account-keyed reset history without account emails |
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

3. Compare `account/read`, `account/rateLimits/read`, optional
   `account/usage/read`, their nullable fields, and update-notification shapes
   against the typed collector code. Update the types and compatibility
   fixtures deliberately; never fall back to forwarding raw protocol objects.
4. Run `nix flake check -L`, including normal tests, the race detector,
   sanitizer tests, and the NixOS module test.
5. Test signed-in, signed-out, API-key, multi-bucket, sparse-update, restart,
   and on-machine account-switch behavior.
6. Only after all checks pass, update
   `services.codexUsageDashboard.expectedCodexVersion`, the flake comment,
   and the lock file in the same change.

Until that procedure is complete, keep the Codex CLI pin at `0.149.0`.

"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const logic = require("./static/assets/account_logic.js");

function account(accountKey, email) {
  return { accountKey, account: email ? { email } : null };
}

test("localunitarity filter is default-pattern compatible and case insensitive", () => {
  assert.equal(logic.matchesLocalunitarityAccount(account("a", "localunitarity@gmail.com")), true);
  assert.equal(logic.matchesLocalunitarityAccount(account("b", "LocalUnitarity+ZENO@GMAIL.COM")), true);
  assert.equal(logic.matchesLocalunitarityAccount(account("c", "localunitarity@example.com")), false);
  assert.equal(logic.matchesLocalunitarityAccount(account("d", "prefixlocalunitarity@gmail.com")), false);
  assert.equal(logic.matchesLocalunitarityAccount(account("e")), false);
});

test("priority tiers follow active Pro, next-use, reset-credit, then remainder", () => {
  assert.equal(logic.priorityTier({ activePro: true, startsOnNextUse: true, resetCreditsAvailable: 4 }), 0);
  assert.equal(logic.priorityTier({ activePro: false, startsOnNextUse: true, resetCreditsAvailable: 4 }), 1);
  assert.equal(logic.priorityTier({ activePro: false, startsOnNextUse: false, resetCreditsAvailable: 1 }), 2);
  assert.equal(logic.priorityTier({ activePro: false, startsOnNextUse: false, resetCreditsAvailable: 0 }), 3);
  assert.equal(logic.priorityTier({ activePro: false, startsOnNextUse: false, resetCreditsAvailable: null }), 3);
});

test("selected priority metric orders within tiers with alphabetical ties", () => {
  const entries = [
    { account: account("zeno"), facts: { activePro: true, remainingPercent: 20, nextResetAt: 300 } },
    { account: account("valentin"), facts: { activePro: true, remainingPercent: 80, nextResetAt: 500 } },
    { account: account("anna"), facts: { activePro: true, remainingPercent: 80, nextResetAt: 100 } },
  ];
  assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, "remaining")).map((entry) => entry.account.accountKey), [
    "anna", "valentin", "zeno",
  ]);
  assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, "reset")).map((entry) => entry.account.accountKey), [
    "anna", "zeno", "valentin",
  ]);
});

test("column sort clicks promote new columns and toggle the current primary", () => {
  let stack = logic.nextColumnSort([], "account");
  assert.deepEqual(stack, [{ key: "account", direction: "asc" }]);

  stack = logic.nextColumnSort(stack, "remaining");
  assert.deepEqual(stack, [
    { key: "remaining", direction: "asc" },
    { key: "account", direction: "asc" },
  ]);

  stack = logic.nextColumnSort(stack, "remaining");
  assert.deepEqual(stack, [
    { key: "remaining", direction: "desc" },
    { key: "account", direction: "asc" },
  ]);

  stack = logic.nextColumnSort(stack, "account");
  assert.deepEqual(stack, [
    { key: "account", direction: "asc" },
    { key: "remaining", direction: "desc" },
  ]);
  assert.deepEqual(logic.nextColumnSort(stack, ""), stack);
});

test("column sort state discards malformed and duplicate descriptors", () => {
  const source = [
    { key: " account ", direction: "asc" },
    { key: "account", direction: "desc" },
    { key: "remaining", direction: "sideways" },
    null,
  ];
  assert.deepEqual(logic.nextColumnSort(source, "plan"), [
    { key: "plan", direction: "asc" },
    { key: "account", direction: "asc" },
  ]);
  assert.deepEqual(source[0], { key: " account ", direction: "asc" });
});

test("column indicators expose direction and lexicographic priority", () => {
  const stack = [
    { key: "remaining", direction: "desc" },
    { key: "account", direction: "asc" },
    { key: "reset", direction: "desc" },
  ];
  assert.equal(logic.columnSortIndicator(stack, "remaining"), "↓");
  assert.equal(logic.columnSortIndicator(stack, "account"), "↑2");
  assert.equal(logic.columnSortIndicator(stack, "reset"), "↓3");
  assert.equal(logic.columnSortIndicator(stack, "unknown"), "");
  assert.equal(logic.columnSortPriority(stack, "remaining"), 1);
  assert.equal(logic.columnSortPriority(stack, "account"), 2);
  assert.equal(logic.columnSortPriority(stack, "unknown"), 0);
});

test("multi-column row sorting applies remembered columns lexicographically", () => {
  const rows = [
    { id: "z", plan: "Pro", remaining: 20, account: "zulu" },
    { id: "b", plan: "Team", remaining: 80, account: "beta" },
    { id: "a", plan: "Pro", remaining: 80, account: "alpha" },
    { id: "c", plan: "Pro", remaining: 80, account: "charlie" },
  ];
  const stack = [
    { key: "plan", direction: "asc" },
    { key: "remaining", direction: "desc" },
    { key: "account", direction: "asc" },
  ];
  const sorted = logic.sortRowsByColumns(
    rows,
    stack,
    (row, key) => row[key],
    (left, right) => left.id.localeCompare(right.id),
  );
  assert.deepEqual(sorted.map((row) => row.id), ["a", "c", "z", "b"]);
  assert.deepEqual(rows.map((row) => row.id), ["z", "b", "a", "c"]);
});

test("multi-column row sorting handles natural strings, dates, booleans, and missing values", () => {
  const rows = [
    { id: "missing", version: null, observed: new Date("invalid"), stale: null },
    { id: "ten", version: "codex-10", observed: new Date("2026-09-10T00:00:00Z"), stale: true },
    { id: "two", version: "codex-2", observed: new Date("2026-09-02T00:00:00Z"), stale: false },
  ];
  assert.deepEqual(logic.sortRowsByColumns(
    rows,
    [{ key: "version", direction: "asc" }],
    (row, key) => row[key],
    (left, right) => left.id.localeCompare(right.id),
  ).map((row) => row.id), ["two", "ten", "missing"]);
  assert.deepEqual(logic.sortRowsByColumns(
    rows,
    [{ key: "observed", direction: "desc" }],
    (row, key) => row[key],
    (left, right) => left.id.localeCompare(right.id),
  ).map((row) => row.id), ["ten", "two", "missing"]);
  assert.deepEqual(logic.sortRowsByColumns(
    rows,
    [{ key: "stale", direction: "asc" }],
    (row, key) => row[key],
    (left, right) => left.id.localeCompare(right.id),
  ).map((row) => row.id), ["two", "ten", "missing"]);
});

test("row sorting uses the ascending fallback and then stable input order", () => {
  const rows = [
    { id: "b", value: 1 },
    { id: "a", value: 1 },
    { id: "a", value: 1, duplicate: true },
  ];
  const sorted = logic.sortRowsByColumns(
    rows,
    [{ key: "value", direction: "desc" }],
    (row, key) => row[key],
    (left, right) => left.id.localeCompare(right.id),
  );
  assert.deepEqual(sorted, [rows[1], rows[2], rows[0]]);
});

test("precedence tiers win even when the selected metric favors a later tier", () => {
  const entries = [
    { account: account("remainder"), facts: { remainingPercent: 100, nextResetAt: 1 } },
    { account: account("credit"), facts: { resetCreditsAvailable: 1, remainingPercent: 90, nextResetAt: 2 } },
    { account: account("next-use"), facts: { startsOnNextUse: true, resetCreditsAvailable: 2, remainingPercent: 80 } },
    { account: account("active-pro"), facts: { activePro: true, remainingPercent: 1, nextResetAt: 999 } },
  ];
  for (const basis of ["remaining", "reset"]) {
    assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, basis)).map((entry) => entry.account.accountKey), [
      "active-pro", "next-use", "credit", "remainder",
    ]);
  }
});

test("filter and sort operate on a copy and use deterministic natural usernames", () => {
  const source = [
    account("codex-10", "other@example.com"),
    account("codex-2", "localunitarity+two@gmail.com"),
    account("codex-1", "LOCALUNITARITY+one@GMAIL.COM"),
  ];
  const selected = logic.selectAccounts(source, {
    localOnly: true,
    sortMode: "alphabetical",
    priorityBasis: "remaining",
  }, () => ({}));
  assert.deepEqual(selected.map((entry) => entry.accountKey), ["codex-1", "codex-2"]);
  assert.equal(source[0].accountKey, "codex-10");
});

test("account email supports the status shape and direct preview fixtures", () => {
  assert.equal(logic.accountEmail({ accountKey: "acct-a", email: "a@example.com" }), "a@example.com");
  assert.equal(logic.accountEmail({ account: { email: "legacy@example.com" } }), "legacy@example.com");
  assert.equal(logic.accountEmail(null), "");
});

test("consumer user disclosure excludes permanent anchors and sorts naturally", () => {
  const source = {
    users: [
      { username: "codex-10", role: "consumer" },
      { username: "codex-dummy-0", role: "anchor" },
      { username: "codex-2", role: "consumer", stale: true },
    ],
  };
  assert.deepEqual(logic.consumerUsers(source).map((user) => user.username), ["codex-2", "codex-10"]);
  assert.equal(source.users.length, 3);
});

test("chat disclosure includes only currently running sessions", () => {
  const source = {
    activeChats: [
      { taskName: "Newest", username: "codex-1", running: true },
      { taskName: "Stopped", username: "codex-2", running: false },
      { taskName: "Malformed", username: "codex-3" },
      null,
    ],
  };
  const chats = logic.accountChats(source);
  assert.deepEqual(chats.map((chat) => chat.taskName), ["Newest"]);
  assert.equal(logic.chatStateSummary(chats), "1 active chat");
  assert.equal(logic.chatStateSummary([{ running: true }, { running: true }]), "2 active chats");
  assert.equal(logic.chatStateSummary([{ running: false }]), "0 active chats");
  assert.equal(logic.chatStateSummary([]), "0 active chats");
  assert.equal(source.activeChats.length, 4);
});

test("per-user chat disclosure selects only that user's running chats", () => {
  const source = {
    activeChats: [
      { taskName: "Newest", username: "codex-2", running: true },
      { taskName: "Other user", username: "codex-10", running: true },
      { taskName: "Older", username: "codex-2", running: true },
      { taskName: "Stopped", username: "codex-2", running: false },
    ],
  };
  assert.deepEqual(
    logic.userChats(source, { username: "codex-2" }).map((chat) => chat.taskName),
    ["Newest", "Older"],
  );
  assert.deepEqual(logic.userChats(source, { username: "codex" }), []);
  assert.deepEqual(logic.userChats(source, null), []);
  assert.equal(source.activeChats.length, 4);
});

test("per-user chat status distinguishes active, empty, unknown, and not applicable", () => {
  const account = {
    activeChats: [
      { taskName: "Running", username: "codex-1", running: true },
      { taskName: "Idle", username: "codex-2", running: false },
    ],
  };
  assert.deepEqual(logic.userChatStatus(account, {
    username: "codex-1", role: "consumer", activeChatsKnown: false,
  }), {
    kind: "active",
    chats: [{ taskName: "Running", username: "codex-1", running: true }],
  });
  assert.deepEqual(logic.userChatStatus(account, {
    username: "codex-2", role: "consumer", activeChatsKnown: true,
  }), { kind: "empty", chats: [] });
  assert.deepEqual(logic.userChatStatus(account, {
    username: "codex-3", role: "consumer", activeChatsKnown: false,
  }), { kind: "unknown", chats: [] });
  assert.deepEqual(logic.userChatStatus(account, {
    username: "codex-4", role: "consumer",
  }), { kind: "unknown", chats: [] });
  assert.deepEqual(logic.userChatStatus(account, {
    username: "codex-dummy-0", role: "anchor", activeChatsKnown: false,
  }), { kind: "not-applicable", chats: [] });
  assert.deepEqual(logic.userChatStatus(null, {
    username: "unassigned", role: "consumer", activeChatsKnown: false,
  }), { kind: "not-applicable", chats: [] });
});

test("account chat status reports unknown zero when any consumer lacks coverage", () => {
  assert.deepEqual(logic.accountChatStatus({
    users: [
      { username: "codex-1", role: "consumer", activeChatsKnown: true },
      { username: "codex-2", role: "consumer", activeChatsKnown: false },
      { username: "codex-dummy-0", role: "anchor", activeChatsKnown: false },
    ],
    activeChats: [
      { taskName: "Idle", username: "codex-1", running: false },
    ],
  }), { kind: "unknown", chats: [] });
  assert.deepEqual(logic.accountChatStatus({
    users: [
      { username: "codex-1", role: "consumer", activeChatsKnown: true },
      { username: "codex-2", role: "consumer", activeChatsKnown: true },
    ],
    activeChats: [],
  }), { kind: "empty", chats: [] });
  assert.deepEqual(logic.accountChatStatus({
    users: [{ username: "codex-dummy-0", role: "anchor", activeChatsKnown: false }],
    activeChats: [],
  }), { kind: "not-applicable", chats: [] });
});

test("a running account chat remains visible when another consumer lacks coverage", () => {
  const running = { taskName: "Running", username: "codex-1", running: true };
  assert.deepEqual(logic.accountChatStatus({
    users: [
      { username: "codex-1", role: "consumer", activeChatsKnown: true },
      { username: "codex-2", role: "consumer", activeChatsKnown: false },
    ],
    activeChats: [running, { taskName: "Idle", username: "codex-2", running: false }],
  }), { kind: "active", chats: [running] });
});

test("user mapping includes anchors and consumers, and optionally unassigned users", () => {
  const accounts = [
    {
      accountKey: "account-two",
      email: "localunitarity+2@gmail.com",
      users: [
        { username: "codex-10", role: "consumer", codexVersion: "0.142.0" },
        { username: "codex-dummy-2", role: "anchor", codexVersion: "0.141.0" },
        { username: "codex-2", role: "consumer", codexVersion: "0.142.0" },
      ],
    },
  ];
  const unassigned = [
    { username: "valentin", role: "consumer", codexVersion: "0.140.0" },
  ];
  assert.deepEqual(
    logic.userAccountRows(accounts, unassigned, false).map((row) => row.user.username),
    ["codex-2", "codex-10", "codex-dummy-2"],
  );
  const allRows = logic.userAccountRows(accounts, unassigned, true);
  assert.deepEqual(allRows.map((row) => [row.user.username, row.assigned]), [
    ["codex-2", true],
    ["codex-10", true],
    ["codex-dummy-2", true],
    ["valentin", false],
  ]);
  assert.equal(accounts[0].users[0].username, "codex-10");
});

test("dummy-user matching accepts only the exact codex-dummy-digits form", () => {
  assert.equal(logic.isDummyUsername("codex-dummy-0"), true);
  assert.equal(logic.isDummyUsername("codex-dummy-42"), true);
  assert.equal(logic.isDummyUsername("codex-dummy-"), false);
  assert.equal(logic.isDummyUsername("codex-dummy-2-extra"), false);
  assert.equal(logic.isDummyUsername("Codex-dummy-2"), false);
  assert.equal(logic.isDummyUsername(" codex-dummy-2"), false);
  assert.equal(logic.isDummyUsername(null), false);
});

test("observed timestamps reject Go zero times and use a valid fallback", () => {
  const fallback = "2026-09-09T12:34:56Z";
  assert.equal(
    logic.observedTimestamp("0001-01-01T00:00:00Z", fallback),
    Date.parse(fallback),
  );
  assert.equal(logic.observedTimestamp("0001-01-01T00:00:00Z"), null);
  assert.equal(logic.observedTimestamp("not-a-date", "0001-01-01T00:00:00Z"), null);

  const rows = [
    { id: "never", observed: logic.observedTimestamp("0001-01-01T00:00:00Z") },
    { id: "older", observed: logic.observedTimestamp("2026-09-01T00:00:00Z") },
    { id: "newer", observed: logic.observedTimestamp("2026-09-08T00:00:00Z") },
  ];
  for (const [direction, expected] of [
    ["asc", ["older", "newer", "never"]],
    ["desc", ["newer", "older", "never"]],
  ]) {
    assert.deepEqual(logic.sortRowsByColumns(
      rows,
      [{ key: "observed", direction }],
      (row) => row.observed,
      (left, right) => left.id.localeCompare(right.id),
    ).map((row) => row.id), expected);
  }
});

test("Codex versions are trimmed and missing versions render unavailable", () => {
  assert.equal(logic.codexVersion({ codexVersion: " 0.142.0 " }), "0.142.0");
  assert.equal(logic.codexVersion({ codexVersion: "" }), "—");
  assert.equal(logic.codexVersion({ codexVersion: null }), "—");
  assert.equal(logic.codexVersion(null), "—");
});

test("account disclosure keys remain stable across refreshed account objects", () => {
  const before = { accountKey: "opaque-account-1", users: [{ username: "codex-1" }] };
  const after = { accountKey: "opaque-account-1", users: [{ username: "codex-2" }] };
  assert.equal(logic.disclosureKey(before, "users"), "opaque-account-1:users");
  assert.equal(logic.disclosureKey(after, "users"), "opaque-account-1:users");
  assert.equal(logic.disclosureKey(after, "active-chats"), "opaque-account-1:active-chats");
  assert.notEqual(logic.disclosureKey(after, "users"), logic.disclosureKey(after, "active-chats"));
  assert.equal(logic.disclosureKey({ accountKey: "" }, "users"), "");
  assert.equal(logic.disclosureKey(after, "unknown"), "");
});

test("per-user chat disclosure keys are stable, scoped, and blank-safe", () => {
  const account = { accountKey: "opaque-account-1" };
  const refreshed = { accountKey: "opaque-account-1" };
  const key = logic.userChatDisclosureKey(account, { username: "codex-2" });
  assert.equal(key, "opaque-account-1:user:codex-2:active-chats");
  assert.equal(logic.userChatDisclosureKey(refreshed, { username: "codex-2" }), key);
  assert.notEqual(logic.userChatDisclosureKey(account, { username: "codex-10" }), key);
  assert.equal(logic.userChatDisclosureKey({ accountKey: "" }, { username: "codex-2" }), "");
  assert.equal(logic.userChatDisclosureKey(account, { username: "" }), "");
});

test("open disclosure state survives replacement with a refreshed account object", () => {
  const openKeys = new Set();
  const originalKey = logic.disclosureKey({ accountKey: "opaque-account-1" }, "users");
  logic.rememberDisclosureState(openKeys, originalKey, true);
  const refreshedKey = logic.disclosureKey({ accountKey: "opaque-account-1" }, "users");
  assert.equal(logic.disclosureIsOpen(openKeys, refreshedKey), true);
  assert.equal(logic.disclosureIsOpen(openKeys, logic.disclosureKey({ accountKey: "opaque-account-1" }, "active-chats")), false);
  logic.rememberDisclosureState(openKeys, refreshedKey, false);
  assert.equal(logic.disclosureIsOpen(openKeys, originalKey), false);
});

test("live disclosure properties are captured before an SSE table replacement", () => {
  const openKeys = new Set(["old:users", "closing:users"]);
  logic.snapshotDisclosureStates(openKeys, [
    { dataset: { disclosureKey: "opening:users" }, open: true },
    { dataset: { disclosureKey: "closing:users" }, open: false },
    { dataset: {}, open: true },
  ]);
  assert.equal(openKeys.has("opening:users"), true);
  assert.equal(openKeys.has("closing:users"), false);
  assert.equal(openKeys.has("old:users"), true);
});

test("focused disclosure summaries expose a stable restoration key", () => {
  const details = { dataset: { disclosureKey: "opaque-account-1:users" } };
  assert.equal(logic.focusedDisclosureKey({ tagName: "SUMMARY", parentElement: details }), "opaque-account-1:users");
  assert.equal(logic.focusedDisclosureKey({ tagName: "BUTTON", parentElement: details }), "");
  assert.equal(logic.focusedDisclosureKey({ tagName: "SUMMARY", parentElement: { dataset: {} } }), "");
  assert.equal(logic.focusedDisclosureKey(null), "");
});

test("quota labels omit the approximate-equals glyph", () => {
  assert.equal(logic.quotaUsageLabel(42), "42% used");
  assert.equal(logic.quotaUsageLabel(0), "0% reported");
  assert.equal(logic.quotaRemainingLabel(58), "58%");
  assert.doesNotMatch(logic.quotaUsageLabel(42), /≈/);
  assert.doesNotMatch(logic.quotaRemainingLabel(58), /≈/);
});

test("lifetime tokens use explicit compact suffixes", () => {
  assert.equal(logic.compactTokenCount(null), "—");
  assert.equal(logic.compactTokenCount(-1), "—");
  assert.equal(logic.compactTokenCount(999), "999");
  assert.equal(logic.compactTokenCount(1_250), "1.25K");
  assert.equal(logic.compactTokenCount(12_500_000), "12.5M");
  assert.equal(logic.compactTokenCount(999_999), "1M");
  assert.equal(logic.compactTokenCount(250_000_000_000), "250B");
  assert.equal(logic.compactTokenCount(1_500_000_000_000), "1.5T");
});

test("lifetime total counts every displayed account once and skips null values", () => {
  assert.equal(logic.totalLifetimeTokens([
    { accountKey: "a", lifetimeTokens: 1_250 },
    { accountKey: "b", lifetimeTokens: null },
    { accountKey: "c", lifetimeTokens: 2_750 },
  ]), 4_000);
  assert.equal(logic.totalLifetimeTokens([{ lifetimeTokens: 0 }, { lifetimeTokens: null }]), 0);
  assert.equal(logic.totalLifetimeTokens([{ lifetimeTokens: null }, {}]), null);
  assert.equal(logic.totalLifetimeTokens([]), null);
});

test("quota meter fills by remaining quota and warns as it runs out", () => {
  assert.deepEqual(logic.remainingQuotaMeter(42, 58), {
    remaining: 42,
    state: "warn",
    valueText: "approximately 42 percent remaining",
  });
  assert.equal(logic.remainingQuotaMeter(51, 49).state, "");
  assert.equal(logic.remainingQuotaMeter(50, 50).state, "warn");
  assert.equal(logic.remainingQuotaMeter(20, 80).state, "danger");
  assert.equal(logic.remainingQuotaMeter(0, 100).state, "danger");
  assert.equal(logic.remainingQuotaMeter(125, -25).remaining, 100);
  assert.equal(logic.remainingQuotaMeter(null, 58).remaining, 42);
  assert.match(logic.remainingQuotaMeter(100, 0).valueText, /below one half percent/);
});

test("history retention follows the validated server value for the scroll horizon", () => {
  assert.equal(logic.historyRetentionDays(366), 366);
  assert.equal(logic.historyRetentionDays(90), 90);
  assert.equal(logic.historyRetentionDays("365"), 365);
  assert.equal(logic.historyRetentionDays(null), 366);
  assert.equal(logic.historyRetentionDays(367), 366);
  assert.equal(logic.historyRetentionDays(6, 30), 30);
});

test("reset points validate, sort, and prefer scheduled points at duplicate timestamps", () => {
  const source = {
    resetPoints: [
      { at: 300, detectedAt: "2026-09-03T00:00:00Z", kind: "inferred_early", usedPercentBefore: 40 },
      { at: 100, detectedAt: "2026-09-01T00:00:00Z", kind: "scheduled", usedPercentBefore: 90 },
      { at: 300, detectedAt: "2026-09-03T00:01:00Z", kind: "scheduled", usedPercentBefore: 42 },
      { at: 200, kind: "unknown", usedPercentBefore: 20 },
      { at: 400, kind: "scheduled", usedPercentBefore: 101 },
      null,
    ],
  };
  const points = logic.accountResetPoints(source);
  assert.deepEqual(points.map((point) => [point.at, point.kind, point.usedPercentBefore]), [
    [100, "scheduled", 90],
    [300, "scheduled", 42],
  ]);
  assert.equal(source.resetPoints.length, 6);
});

test("explicit reset points match completed-window endpoints without coercion surprises", () => {
  const points = [
    { at: 1_700_000_000, kind: "scheduled", usedPercentBefore: 80 },
    { at: 1_700_000_100, kind: "inferred_early", usedPercentBefore: 20 },
  ];
  assert.equal(logic.resetPointMatchesWindow(points, 1_700_000_000), true);
  assert.equal(logic.resetPointMatchesWindow(points, "1700000100"), true);
  assert.equal(logic.resetPointMatchesWindow(points, 1_700_000_001), false);
  assert.equal(logic.resetPointMatchesWindow([], 1_700_000_000), false);
});

test("an inferred reset matches only the adjustment from which it was derived", () => {
  const point = {
    kind: "inferred_early",
    at: 1_800_000_000,
    detectedAt: "2026-09-03T00:00:00Z",
    previousScheduledAt: 1_800_000_000,
    nextScheduledAt: 1_800_604_800,
    usedPercentBefore: 82,
  };
  const adjustment = {
    detectedAt: "2026-09-03T00:00:00Z",
    reasons: ["reset_timestamp_changed", "used_percent_decreased"],
    before: { resetsAt: 1_800_000_000, usedPercent: 82 },
    after: { windowStartedAt: 1_800_000_000, resetsAt: 1_800_604_800, usedPercent: 0 },
  };
  assert.equal(logic.resetPointMatchesAdjustment(point, adjustment), true);
  assert.equal(logic.resetPointMatchesAdjustment({ ...point, kind: "scheduled" }, adjustment), false);
  assert.equal(logic.resetPointMatchesAdjustment(point, {
    ...adjustment,
    detectedAt: "2026-09-03T00:00:01Z",
  }), false);
  assert.equal(logic.resetPointMatchesAdjustment(point, {
    ...adjustment,
    reasons: ["reset_timestamp_changed"],
  }), false);
  assert.equal(logic.resetPointMatchesAdjustment({ ...point, at: point.at + 1 }, adjustment), false);
});

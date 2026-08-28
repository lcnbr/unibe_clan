"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const logic = require("./static/assets/account_logic.js");

function account(username, email) {
  return { username, account: email ? { email } : null };
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
  assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, "remaining")).map((entry) => entry.account.username), [
    "anna", "valentin", "zeno",
  ]);
  assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, "reset")).map((entry) => entry.account.username), [
    "anna", "zeno", "valentin",
  ]);
});

test("precedence tiers win even when the selected metric favors a later tier", () => {
  const entries = [
    { account: account("remainder"), facts: { remainingPercent: 100, nextResetAt: 1 } },
    { account: account("credit"), facts: { resetCreditsAvailable: 1, remainingPercent: 90, nextResetAt: 2 } },
    { account: account("next-use"), facts: { startsOnNextUse: true, resetCreditsAvailable: 2, remainingPercent: 80 } },
    { account: account("active-pro"), facts: { activePro: true, remainingPercent: 1, nextResetAt: 999 } },
  ];
  for (const basis of ["remaining", "reset"]) {
    assert.deepEqual(entries.slice().sort((a, b) => logic.priorityCompare(a, b, basis)).map((entry) => entry.account.username), [
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
  assert.deepEqual(selected.map((entry) => entry.username), ["codex-1", "codex-2"]);
  assert.equal(source[0].username, "codex-10");
});

test("quota meter fills by remaining quota and warns as it runs out", () => {
  assert.deepEqual(logic.remainingQuotaMeter(42, 58), {
    remaining: 42,
    state: "",
    valueText: "approximately 42 percent remaining",
  });
  assert.equal(logic.remainingQuotaMeter(20, 80).state, "warn");
  assert.equal(logic.remainingQuotaMeter(0, 100).state, "danger");
  assert.equal(logic.remainingQuotaMeter(125, -25).remaining, 100);
  assert.equal(logic.remainingQuotaMeter(null, 58).remaining, 42);
  assert.match(logic.remainingQuotaMeter(100, 0).valueText, /below one half percent/);
});

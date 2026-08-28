"use strict";

(function exposeAccountLogic(root, factory) {
  const logic = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = logic;
  } else {
    root.AccountLogic = logic;
  }
})(typeof globalThis === "object" ? globalThis : this, function accountLogicFactory() {
  const LOCALUNITARITY_EMAIL = /^localunitarity.*@gmail\.com$/i;

  function normalizedUsername(account) {
    return String(account && account.username ? account.username : "").toLocaleLowerCase();
  }

  function alphabeticalCompare(left, right) {
    const normalized = normalizedUsername(left).localeCompare(normalizedUsername(right), undefined, {
      numeric: true,
      sensitivity: "base",
    });
    if (normalized !== 0) {
      return normalized;
    }
    return String(left && left.username ? left.username : "")
      .localeCompare(String(right && right.username ? right.username : ""), undefined, { numeric: true });
  }

  function matchesLocalunitarityAccount(account) {
    const email = account && account.account && account.account.email;
    return typeof email === "string" && LOCALUNITARITY_EMAIL.test(email.trim());
  }

  function priorityTier(facts) {
    if (facts.activePro) {
      return 0;
    }
    if (facts.startsOnNextUse) {
      return 1;
    }
    if (Number.isSafeInteger(facts.resetCreditsAvailable) && facts.resetCreditsAvailable > 0) {
      return 2;
    }
    return 3;
  }

  function compareOptionalNumber(left, right, direction) {
    const leftValid = Number.isFinite(left);
    const rightValid = Number.isFinite(right);
    if (leftValid !== rightValid) {
      return leftValid ? -1 : 1;
    }
    if (!leftValid || left === right) {
      return 0;
    }
    return direction * (left < right ? -1 : 1);
  }

  function priorityCompare(left, right, basis) {
    const tierDifference = priorityTier(left.facts) - priorityTier(right.facts);
    if (tierDifference !== 0) {
      return tierDifference;
    }
    const metricDifference = basis === "reset"
      ? compareOptionalNumber(left.facts.nextResetAt, right.facts.nextResetAt, 1)
      : compareOptionalNumber(left.facts.remainingPercent, right.facts.remainingPercent, -1);
    return metricDifference || alphabeticalCompare(left.account, right.account);
  }

  function selectAccounts(accounts, options, factsFor) {
    const source = Array.isArray(accounts) ? accounts : [];
    const selected = options.localOnly
      ? source.filter(matchesLocalunitarityAccount)
      : source.slice();
    if (options.sortMode !== "priority") {
      return selected.sort(alphabeticalCompare);
    }
    return selected
      .map((account) => ({ account, facts: factsFor(account) }))
      .sort((left, right) => priorityCompare(left, right, options.priorityBasis))
      .map((entry) => entry.account);
  }

  return {
    alphabeticalCompare,
    matchesLocalunitarityAccount,
    priorityCompare,
    priorityTier,
    selectAccounts,
  };
});

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

  function accountEmail(account) {
    if (account && typeof account.email === "string") {
      return account.email;
    }
    const legacyEmail = account && account.account && account.account.email;
    return typeof legacyEmail === "string" ? legacyEmail : "";
  }

  function normalizedIdentity(account) {
    return accountEmail(account).trim().toLowerCase();
  }

  function alphabeticalCompare(left, right) {
    const normalized = normalizedIdentity(left).localeCompare(normalizedIdentity(right), undefined, {
      numeric: true,
      sensitivity: "base",
    });
    if (normalized !== 0) {
      return normalized;
    }
    return String(left && left.accountKey ? left.accountKey : "")
      .localeCompare(String(right && right.accountKey ? right.accountKey : ""), undefined, { numeric: true });
  }

  function matchesLocalunitarityAccount(account) {
    return LOCALUNITARITY_EMAIL.test(accountEmail(account).trim());
  }

  function consumerUsers(account) {
    const users = account && Array.isArray(account.users) ? account.users : [];
    return users
      .filter((user) => user && user.role !== "anchor")
      .slice()
      .sort((left, right) => String(left.username || "").localeCompare(String(right.username || ""), undefined, {
        numeric: true,
        sensitivity: "base",
      }));
  }

  function accountChats(account) {
    const chats = account && Array.isArray(account.activeChats) ? account.activeChats : [];
    return chats.filter((chat) => chat && chat.running === true);
  }

  function chatStateSummary(chats) {
    const source = Array.isArray(chats) ? chats : [];
    const running = source.filter((chat) => chat && chat.running === true).length;
    return `${running} active chat${running === 1 ? "" : "s"}`;
  }

  function compareUsername(left, right) {
    return String(left && left.username ? left.username : "")
      .localeCompare(String(right && right.username ? right.username : ""), undefined, {
        numeric: true,
        sensitivity: "base",
      });
  }

  function userAccountRows(accounts, unassignedUsers, includeUnassigned) {
    const rows = [];
    (Array.isArray(accounts) ? accounts : []).forEach((account) => {
      const users = account && Array.isArray(account.users) ? account.users.slice().sort(compareUsername) : [];
      users.forEach((user) => {
        if (user) {
          rows.push({ account, user, assigned: true });
        }
      });
    });
    if (includeUnassigned) {
      (Array.isArray(unassignedUsers) ? unassignedUsers : [])
        .filter(Boolean)
        .slice()
        .sort(compareUsername)
        .forEach((user) => rows.push({ account: null, user, assigned: false }));
    }
    return rows;
  }

  function codexVersion(user) {
    return user && typeof user.codexVersion === "string" && user.codexVersion.trim()
      ? user.codexVersion.trim()
      : "—";
  }

  function disclosureKey(account, kind) {
    const accountKey = account && typeof account.accountKey === "string"
      ? account.accountKey.trim()
      : "";
    if (!accountKey || (kind !== "users" && kind !== "active-chats")) {
      return "";
    }
    return `${accountKey}:${kind}`;
  }

  function disclosureIsOpen(openKeys, key) {
    return Boolean(key && openKeys instanceof Set && openKeys.has(key));
  }

  function rememberDisclosureState(openKeys, key, open) {
    if (!key || !(openKeys instanceof Set)) {
      return;
    }
    if (open) {
      openKeys.add(key);
    } else {
      openKeys.delete(key);
    }
  }

  function snapshotDisclosureStates(openKeys, disclosures) {
    if (!(openKeys instanceof Set) || !disclosures || typeof disclosures[Symbol.iterator] !== "function") {
      return;
    }
    for (const disclosure of disclosures) {
      const key = disclosure && disclosure.dataset && disclosure.dataset.disclosureKey;
      rememberDisclosureState(openKeys, key, Boolean(disclosure && disclosure.open));
    }
  }

  function focusedDisclosureKey(activeElement) {
    const parent = activeElement && activeElement.parentElement;
    const tagName = activeElement && typeof activeElement.tagName === "string"
      ? activeElement.tagName.toLowerCase()
      : "";
    if (tagName !== "summary" || !parent || !parent.dataset) {
      return "";
    }
    return typeof parent.dataset.disclosureKey === "string"
      ? parent.dataset.disclosureKey
      : "";
  }

  function quotaUsageLabel(value) {
    const used = Number(value);
    return used === 0 ? "0% reported" : `${used}% used`;
  }

  function quotaRemainingLabel(value) {
    return `${Number(value)}%`;
  }

  function compactTokenCount(value) {
    if (!Number.isSafeInteger(value) || value < 0) {
      return "—";
    }
    const units = [
      { threshold: 1, suffix: "" },
      { threshold: 1e3, suffix: "K" },
      { threshold: 1e6, suffix: "M" },
      { threshold: 1e9, suffix: "B" },
      { threshold: 1e12, suffix: "T" },
    ];
    let unitIndex = units.findLastIndex((candidate) => value >= candidate.threshold);
    if (unitIndex <= 0) {
      return new Intl.NumberFormat().format(value);
    }
    let unit = units[unitIndex];
    let scaled = value / unit.threshold;
    let maximumFractionDigits = scaled >= 100 ? 0 : (scaled >= 10 ? 1 : 2);
    let rounded = Number(scaled.toFixed(maximumFractionDigits));
    if (rounded >= 1000 && unitIndex < units.length - 1) {
      unit = units[++unitIndex];
      scaled = value / unit.threshold;
      maximumFractionDigits = scaled >= 100 ? 0 : (scaled >= 10 ? 1 : 2);
      rounded = Number(scaled.toFixed(maximumFractionDigits));
    }
    return `${new Intl.NumberFormat(undefined, { maximumFractionDigits }).format(rounded)}${unit.suffix}`;
  }

  function totalLifetimeTokens(accounts) {
    let found = false;
    let overflowed = false;
    let total = 0;
    (Array.isArray(accounts) ? accounts : []).forEach((account) => {
      const value = account && account.lifetimeTokens;
      if (!Number.isSafeInteger(value) || value < 0) {
        return;
      }
      const next = total + value;
      if (!Number.isSafeInteger(next)) {
        overflowed = true;
        return;
      }
      total = next;
      found = true;
    });
    return found && !overflowed ? total : null;
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

  function remainingQuotaMeter(remainingPercent, usedPercent) {
    const remainingValue = Number(remainingPercent);
    const usedValue = Number(usedPercent);
    const hasReportedRemaining = remainingPercent !== null
      && remainingPercent !== undefined
      && Number.isFinite(remainingValue);
    const remaining = hasReportedRemaining
      ? Math.max(0, Math.min(100, remainingValue))
      : Math.max(0, Math.min(100, 100 - (Number.isFinite(usedValue) ? usedValue : 0)));
    return {
      remaining,
      state: remaining <= 20 ? "danger" : (remaining <= 50 ? "warn" : ""),
      valueText: usedValue === 0
        ? `approximately ${remaining} percent remaining; reported usage may be below one half percent`
        : `approximately ${remaining} percent remaining`,
    };
  }

  function historyRetentionDays(value, fallback = 366) {
    const reported = Number(value);
    if (Number.isInteger(reported) && reported >= 7 && reported <= 366) {
      return reported;
    }
    const defaultValue = Number(fallback);
    return Number.isInteger(defaultValue) && defaultValue >= 7 && defaultValue <= 366
      ? defaultValue
      : 366;
  }

  function accountResetPoints(historyAccount) {
    const points = historyAccount && Array.isArray(historyAccount.resetPoints)
      ? historyAccount.resetPoints
      : [];
    const byTimestamp = new Map();
    points.forEach((point) => {
      if (!point || (point.kind !== "scheduled" && point.kind !== "inferred_early")) {
        return;
      }
      const at = Number(point.at);
      const usedPercentBefore = Number(point.usedPercentBefore);
      if (!Number.isSafeInteger(at) || at <= 0 || !Number.isInteger(usedPercentBefore) ||
          usedPercentBefore < 0 || usedPercentBefore > 100) {
        return;
      }
      const normalized = { ...point, at, usedPercentBefore };
      const previous = byTimestamp.get(at);
      if (!previous || (previous.kind === "inferred_early" && normalized.kind === "scheduled")) {
        byTimestamp.set(at, normalized);
      }
    });
    return Array.from(byTimestamp.values()).sort((left, right) => left.at - right.at);
  }

  function resetPointMatchesWindow(points, resetsAt) {
    const timestamp = Number(resetsAt);
    return Number.isFinite(timestamp) && (Array.isArray(points) ? points : [])
      .some((point) => point && Number(point.at) === timestamp);
  }

  function resetPointMatchesAdjustment(point, adjustment) {
    if (!point || point.kind !== "inferred_early" || !adjustment ||
        !adjustment.before || !adjustment.after) {
      return false;
    }
    const reasons = Array.isArray(adjustment.reasons) ? adjustment.reasons : [];
    if (!reasons.includes("reset_timestamp_changed") ||
        !reasons.includes("used_percent_decreased")) {
      return false;
    }
    const pointDetectedAt = Date.parse(point.detectedAt);
    const adjustmentDetectedAt = Date.parse(adjustment.detectedAt);
    return Number.isFinite(pointDetectedAt) && pointDetectedAt === adjustmentDetectedAt &&
      Number(point.at) === Number(adjustment.after.windowStartedAt) &&
      Number(point.previousScheduledAt) === Number(adjustment.before.resetsAt) &&
      Number(point.nextScheduledAt) === Number(adjustment.after.resetsAt) &&
      Number(point.usedPercentBefore) === Number(adjustment.before.usedPercent);
  }

  return {
    accountEmail,
    accountChats,
    accountResetPoints,
    alphabeticalCompare,
    chatStateSummary,
    codexVersion,
    compactTokenCount,
    consumerUsers,
    disclosureKey,
    disclosureIsOpen,
    focusedDisclosureKey,
    historyRetentionDays,
    matchesLocalunitarityAccount,
    priorityCompare,
    priorityTier,
    quotaRemainingLabel,
    quotaUsageLabel,
    rememberDisclosureState,
    snapshotDisclosureStates,
    remainingQuotaMeter,
    resetPointMatchesAdjustment,
    resetPointMatchesWindow,
    selectAccounts,
    totalLifetimeTokens,
    userAccountRows,
  };
});

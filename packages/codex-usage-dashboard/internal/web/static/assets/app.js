"use strict";

const DAY_MS = 24 * 60 * 60 * 1000;
const MAIN_WEEK_MINUTES = 10080;
const MAX_HISTORY_DAYS = 56;
const HISTORY_SCHEMA_VERSION = 2;
const RESET_TIMESTAMP_CHANGED = "reset_timestamp_changed";
const USED_PERCENT_DECREASED = "used_percent_decreased";

const accountsRoot = document.getElementById("accounts");
const timelineRoot = document.getElementById("timeline");
const trackingCopy = document.getElementById("tracking-copy");
const adjustmentDetail = document.getElementById("adjustment-detail");
const updatedAt = document.getElementById("updated-at");
const demoBanner = document.getElementById("demo-banner");
const connectionDot = document.getElementById("connection-dot");
const connectionLabel = document.getElementById("connection-label");
const screenReaderStatus = document.getElementById("screen-reader-status");
const earlierButton = document.getElementById("timeline-earlier");
const nowButton = document.getElementById("timeline-now");
const localunitarityFilter = document.getElementById("localunitarity-filter");
const sortModeButton = document.getElementById("sort-mode");
const priorityBasisGroup = document.getElementById("priority-basis");
const priorityBasisInputs = Array.from(document.querySelectorAll('input[name="priority-basis"]'));
const selectionCount = document.getElementById("selection-count");
const accountLogic = window.AccountLogic;

let currentStatus = null;
let historyStatus = null;
let streamHasOpened = false;
let timelineInitialized = false;
let timelineRange = null;
let latestStatusRevision = -1;
let latestHistoryRevision = -1;
let lastAnnouncementSignature = "";
let knownAdjustmentIDs = null;
let localunitarityOnly = localunitarityFilter.checked;
let sortMode = "alphabetical";
let priorityBasis = (priorityBasisInputs.find((input) => input.checked) || {}).value || "remaining";
const adjustmentDetailDefault = "Focus or tap an amber marker to inspect the server-reported before and after values.";

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) {
    element.className = className;
  }
  if (text !== undefined && text !== null) {
    element.textContent = String(text);
  }
  return element;
}

function timeNode(className, date, text) {
  const element = node("time", className, text);
  if (date) {
    element.dateTime = date.toISOString();
    element.title = date.toLocaleString();
  }
  return element;
}

function setConnection(kind, label) {
  connectionDot.className = "connection-dot";
  if (kind) {
    connectionDot.classList.add(kind);
  }
  connectionLabel.textContent = label;
}

function titleCase(value) {
  if (!value) {
    return "Unknown";
  }
  return String(value)
    .replaceAll("_", " ")
    .replaceAll("-", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function validDate(value, seconds = false) {
  if (value === null || value === undefined || value === "") {
    return null;
  }
  const date = new Date(seconds ? Number(value) * 1000 : value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatClock(value, seconds = true) {
  const date = validDate(value, seconds);
  if (!date) {
    return "Reset unavailable";
  }
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
}

function formatDay(date) {
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
  }).format(date);
}

function relativeTime(value) {
  const date = validDate(value);
  if (!date || date.getUTCFullYear() <= 1) {
    return "Never";
  }
  const deltaSeconds = Math.round((date.getTime() - Date.now()) / 1000);
  const absolute = Math.abs(deltaSeconds);
  if (absolute < 8) {
    return "Just now";
  }
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (absolute < 90) {
    return formatter.format(deltaSeconds, "second");
  }
  if (absolute < 5400) {
    return formatter.format(Math.round(deltaSeconds / 60), "minute");
  }
  if (absolute < 129600) {
    return formatter.format(Math.round(deltaSeconds / 3600), "hour");
  }
  return formatter.format(Math.round(deltaSeconds / 86400), "day");
}

function countdown(value) {
  const date = validDate(value, true);
  if (!date) {
    return "—";
  }
  let seconds = Math.max(0, Math.floor((date.getTime() - Date.now()) / 1000));
  if (seconds <= 0) {
    return "Reset due";
  }
  const days = Math.floor(seconds / 86400);
  seconds %= 86400;
  const hours = Math.floor(seconds / 3600);
  seconds %= 3600;
  const minutes = Math.floor(seconds / 60);
  if (days > 0) {
    return `${days}d ${hours}h remaining`;
  }
  if (hours > 0) {
    return `${hours}h ${minutes}m remaining`;
  }
  return `${minutes}m remaining`;
}

function canonicalMainUsage(account) {
  if (account && account.mainUsage && Number(account.mainUsage.windowDurationMins) === MAIN_WEEK_MINUTES) {
    return account.mainUsage;
  }
  return null;
}

function historyFor(username) {
  if (!historyStatus || !Array.isArray(historyStatus.accounts)) {
    return null;
  }
  return historyStatus.accounts.find((entry) => entry.username === username) || null;
}

function adjustmentsFor(username) {
  const history = historyFor(username);
  return history && Array.isArray(history.adjustments) ? history.adjustments : [];
}

function adjustmentID(username, adjustment) {
  const before = adjustment && adjustment.before ? adjustment.before : {};
  const after = adjustment && adjustment.after ? adjustment.after : {};
  return [username, adjustment && adjustment.detectedAt, before.resetsAt, after.resetsAt,
    before.usedPercent, after.usedPercent].join(":");
}

function adjustmentUsageLabel(value) {
  const used = Number(value) || 0;
  return used === 0 ? "0% reported" : `approximately ${used}% used`;
}

function adjustmentDescription(adjustment, compact = false) {
  if (!adjustment || !adjustment.before || !adjustment.after) {
    return "Server adjustment details unavailable";
  }
  const reasons = Array.isArray(adjustment.reasons) ? adjustment.reasons : [];
  const parts = [];
  if (reasons.includes(RESET_TIMESTAMP_CHANGED)) {
    if (compact) {
      parts.push(`Reset ${formatClock(adjustment.before.resetsAt)} → ${formatClock(adjustment.after.resetsAt)}`);
    } else {
      parts.push(`Reset adjusted from ${formatClock(adjustment.before.resetsAt)} to ${formatClock(adjustment.after.resetsAt)}`);
    }
  }
  if (reasons.includes(USED_PERCENT_DECREASED)) {
    if (compact) {
      parts.push(`${adjustmentUsageLabel(adjustment.before.usedPercent)} → ${adjustmentUsageLabel(adjustment.after.usedPercent)}`);
    } else {
      parts.push(`Usage revised from ${adjustmentUsageLabel(adjustment.before.usedPercent)} to ${adjustmentUsageLabel(adjustment.after.usedPercent)}`);
    }
  }
  const detected = validDate(adjustment.detectedAt);
  const detectedText = detected ? detected.toLocaleString() : "an unknown time";
  if (compact) {
    return `${parts.join(" · ")} · detected ${detectedText}`;
  }
  return `${parts.join(". ")}. Detected ${detectedText}.`;
}

function showAdjustmentDetail(account, adjustment) {
  adjustmentDetail.textContent = `${account.username}: ${adjustmentDescription(adjustment, true)}`;
  adjustmentDetail.classList.add("is-active");
}

function resetAdjustmentDetail() {
  adjustmentDetail.textContent = adjustmentDetailDefault;
  adjustmentDetail.classList.remove("is-active");
}

function restoreFocusedAdjustmentDetail() {
  const focused = document.activeElement;
  if (focused && focused.classList && focused.classList.contains("adjustment-marker") &&
      focused.dataset.adjustmentDetail) {
    adjustmentDetail.textContent = focused.dataset.adjustmentDetail;
    adjustmentDetail.classList.add("is-active");
    return;
  }
  resetAdjustmentDetail();
}

function isFloatingUnusedWindow(account, windowValue) {
  if (!windowValue || Number(windowValue.usedPercent) !== 0) {
    return false;
  }
  const observedAt = validDate(account.observedAt);
  const resetAt = validDate(windowValue.resetsAt, true);
  const duration = Number(windowValue.windowDurationMins);
  if (!observedAt || !resetAt || !Number.isFinite(duration) || duration <= 0) {
    return false;
  }
  const expectedReset = observedAt.getTime() + duration * 60 * 1000;
  return Math.abs(resetAt.getTime() - expectedReset) <= 2 * 60 * 1000;
}

function anchoredReset(account) {
  const usage = canonicalMainUsage(account);
  const history = historyFor(account.username);
  if (history && history.active && (
    !usage || Number(usage.resetsAt) === Number(history.active.resetsAt)
  )) {
    return history.active;
  }
  if (!usage || !usage.resetsAt || isFloatingUnusedWindow(account, usage)) {
    return null;
  }
  const resetAt = Number(usage.resetsAt);
  const duration = Number(usage.windowDurationMins) || MAIN_WEEK_MINUTES;
  return {
    windowStartedAt: resetAt - duration * 60,
    resetsAt: resetAt,
    usedPercent: Number(usage.usedPercent) || 0,
  };
}

function mainReached(account) {
  const usage = canonicalMainUsage(account);
  return Boolean(usage && Number(usage.usedPercent) >= 100);
}

function resetCreditsAvailable(account) {
  const value = account ? account.resetCreditsAvailable : null;
  return Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function remainingPercent(account) {
  const usage = canonicalMainUsage(account);
  if (!usage) {
    return null;
  }
  const reported = Number(usage.remainingPercent);
  if (usage.remainingPercent !== null && usage.remainingPercent !== undefined && Number.isFinite(reported)) {
    return Math.max(0, Math.min(100, reported));
  }
  const used = Number(usage.usedPercent);
  return Number.isFinite(used) ? Math.max(0, Math.min(100, 100 - used)) : null;
}

function priorityFacts(account) {
  const usage = canonicalMainUsage(account);
  const active = anchoredReset(account);
  const startsOnNextUse = Boolean(
    usage && !active && isFloatingUnusedWindow(account, usage),
  );
  const plan = account && account.account ? String(account.account.planType || "").toLowerCase() : "";
  return {
    activePro: Boolean(account && account.state === "ok" && plan === "pro" && active && active.resetsAt),
    startsOnNextUse,
    resetCreditsAvailable: resetCreditsAvailable(account),
    remainingPercent: remainingPercent(account),
    nextResetAt: active && Number.isFinite(Number(active.resetsAt)) ? Number(active.resetsAt) : null,
  };
}

function displayedAccounts() {
  if (!currentStatus || !Array.isArray(currentStatus.accounts)) {
    return [];
  }
  return accountLogic.selectAccounts(currentStatus.accounts, {
    localOnly: localunitarityOnly,
    sortMode,
    priorityBasis,
  }, priorityFacts);
}

function updateViewControls(accountCount) {
  const priorityActive = sortMode === "priority";
  sortModeButton.textContent = priorityActive ? "Sort alphabetically" : "Sort by priority";
  priorityBasisGroup.disabled = !priorityActive;
  const total = currentStatus && Array.isArray(currentStatus.accounts) ? currentStatus.accounts.length : 0;
  selectionCount.textContent = `Showing ${accountCount} of ${total} accounts`;
}

function statusPresentation(account) {
  if (account.stale) {
    return { label: "Stale", className: "warning" };
  }
  if (mainReached(account)) {
    return { label: "Limit reached", className: "error" };
  }
  if (account.state === "signed_out") {
    return { label: "Signed out", className: "warning" };
  }
  if (account.state === "api_key") {
    return { label: "API key", className: "warning" };
  }
  if (account.state === "unavailable") {
    return { label: "Unavailable", className: "error" };
  }
  return { label: "Live", className: "" };
}

function usageLabel(used) {
  return used === 0 ? "0% reported" : `≈${used}% used`;
}

function appendMeter(parent, used, label) {
  const meter = node("div", "meter");
  meter.setAttribute("role", "progressbar");
  meter.setAttribute("aria-label", label);
  meter.setAttribute("aria-valuemin", "0");
  meter.setAttribute("aria-valuemax", "100");
  meter.setAttribute("aria-valuenow", String(used));
  meter.setAttribute(
    "aria-valuetext",
    used === 0 ? "0 percent reported; actual usage may be below one half percent" : `approximately ${used} percent used`,
  );
  if (used >= 100) {
    meter.classList.add("danger");
  } else if (used >= 80) {
    meter.classList.add("warn");
  }
  const fill = node("span");
  fill.style.width = `${Math.max(0, Math.min(100, used))}%`;
  meter.append(fill);
  parent.append(meter);
}

function initials(username) {
  const value = String(username || "?");
  if (value.startsWith("codex")) {
    return value.replace("codex", "c").slice(0, 2);
  }
  return value.slice(0, 2);
}

function renderAccountSummary(accounts) {
  const table = node("table", "account-table");
  const caption = node("caption", "visually-hidden", "Main weekly Codex usage by Linux account");
  const head = node("thead");
  const headingRow = node("tr");
  ["Account", "Plan", "Main weekly usage", "Remaining", "Next reset", "Resets available", "State", "Observed"].forEach((label) => {
    headingRow.append(node("th", "", label));
  });
  head.append(headingRow);

  const body = node("tbody");
  accounts.forEach((account) => {
    const row = node("tr");
    if (account.stale) {
      row.classList.add("is-stale");
    }
    if (mainReached(account)) {
      row.classList.add("is-reached");
    }

    const identityCell = node("th", "account-identity");
    identityCell.scope = "row";
    const identityInner = node("span", "account-identity-inner");
    const orb = node("span", "user-orb", initials(account.username));
    orb.setAttribute("aria-hidden", "true");
    identityInner.append(orb);
    const identity = node("span", "identity-copy");
    identity.append(node("strong", "username", account.username || "unknown"));
    const email = account.account && account.account.email ? account.account.email : "No ChatGPT email";
    const emailNode = node("span", "email", email);
    emailNode.title = email;
    identity.append(emailNode);
    identityInner.append(identity);
    identityCell.append(identityInner);
    row.append(identityCell);

    const plan = account.account && account.account.planType
      ? `${titleCase(account.account.planType)}`
      : "—";
    row.append(node("td", "plan-cell", plan));

    const usage = canonicalMainUsage(account);
    const usageCell = node("td", "usage-cell");
    if (usage) {
      const used = Math.max(0, Math.min(100, Number(usage.usedPercent) || 0));
      usageCell.append(node("strong", "usage-value", usageLabel(used)));
      appendMeter(usageCell, used, `Main weekly usage for ${account.username}`);
    } else {
      usageCell.append(node("span", "unavailable-value", "—"));
    }
    row.append(usageCell);

    const remainingCell = node("td", "remaining-cell");
    const remaining = remainingPercent(account);
    if (remaining === null) {
      remainingCell.append(node("span", "unavailable-value", "—"));
    } else {
      const remainingValue = node("strong", "remaining-value", `≈${remaining}%`);
      remainingValue.title = "Approximate remaining weekly quota, derived from OpenAI's rounded usage percentage";
      remainingCell.append(remainingValue);
    }
    row.append(remainingCell);

    const resetCell = node("td", "reset-cell");
    const active = anchoredReset(account);
    if (active && active.resetsAt) {
      const resetDate = validDate(active.resetsAt, true);
      resetCell.append(
        timeNode("reset-clock", resetDate, formatClock(active.resetsAt)),
        node("span", "countdown", countdown(active.resetsAt)),
      );
      resetCell.lastElementChild.dataset.countdown = String(active.resetsAt);
    } else if (usage && isFloatingUnusedWindow(account, usage)) {
      resetCell.append(
        node("strong", "idle-window", "Starts on next use"),
        node("span", "reset-explainer", "No fixed weekly reset yet"),
      );
    } else {
      resetCell.append(node("span", "unavailable-value", "—"));
    }
    row.append(resetCell);

    const creditsCell = node("td", "reset-credits-cell");
    const availableCredits = resetCreditsAvailable(account);
    if (availableCredits === null) {
      const unavailable = node("span", "unavailable-value", "—");
      unavailable.title = "OpenAI did not report a reset-credit count";
      creditsCell.append(unavailable);
    } else {
      const value = node("strong", "reset-credits-value", availableCredits);
      value.title = `${availableCredits} earned Codex reset credit${availableCredits === 1 ? "" : "s"} available`;
      value.setAttribute("aria-label", value.title);
      creditsCell.append(value);
    }
    row.append(creditsCell);

    const presentation = statusPresentation(account);
    const stateCell = node("td", "state-cell");
    stateCell.append(node("span", `status-pill ${presentation.className}`.trim(), presentation.label));
    row.append(stateCell);

    const observedCell = node("td", "observed-cell", relativeTime(account.observedAt));
    observedCell.dataset.relativeTime = account.observedAt || "";
    row.append(observedCell);
    body.append(row);
  });

  if (accounts.length === 0) {
    const emptyRow = node("tr", "empty-row");
    const emptyCell = node("td", "empty-cell", localunitarityOnly
      ? "No matching localunitarity Gmail accounts"
      : "No accounts available");
    emptyCell.colSpan = 8;
    emptyRow.append(emptyCell);
    body.append(emptyRow);
  }

  table.append(caption, head, body);
  accountsRoot.replaceChildren(table);
  accountsRoot.setAttribute("aria-busy", "false");
}

function localDayStart(value) {
  const date = new Date(value);
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

function resetWindowsFor(account) {
  const entry = historyFor(account.username);
  const windows = [];
  if (entry && Array.isArray(entry.events)) {
    entry.events.forEach((event) => {
      if (event && event.resetsAt && event.windowStartedAt) {
        windows.push({ ...event, kind: "history" });
      }
    });
  }
  const active = anchoredReset(account);
  if (active && active.resetsAt && active.windowStartedAt) {
    const duplicate = windows.some((event) => Number(event.resetsAt) === Number(active.resetsAt));
    if (!duplicate) {
      windows.push({ ...active, kind: "active" });
    }
  }
  return windows;
}

function timelineGeometry(accounts) {
  const now = Date.now();
  const oldestAllowed = now - MAX_HISTORY_DAYS * DAY_MS;
  let earliest = now - DAY_MS;
  accounts.forEach((account) => {
    resetWindowsFor(account).forEach((windowValue) => {
      const started = Number(windowValue.windowStartedAt) * 1000;
      if (Number.isFinite(started)) {
        earliest = Math.min(earliest, Math.max(oldestAllowed, started));
      }
    });
    adjustmentsFor(account.username).forEach((adjustment) => {
      const detected = validDate(adjustment.detectedAt);
      if (detected) {
        earliest = Math.min(earliest, Math.max(oldestAllowed, detected.getTime()));
      }
    });
  });
  const start = localDayStart(earliest).getTime();
  const end = now + 7 * DAY_MS;
  const labelWidth = window.matchMedia("(max-width: 620px)").matches ? 112 : 164;
  const available = Math.max(320, timelineRoot.clientWidth - labelWidth);
  const dayWidth = Math.max(96, Math.min(156, available / 7));
  const pixelsPerMs = dayWidth / DAY_MS;
  const width = labelWidth + Math.ceil((end - start) * pixelsPerMs);
  return { start, end, labelWidth, dayWidth, pixelsPerMs, width };
}

function renderTimelineAdjustment(lane, adjustment, geometry, account) {
  const detected = validDate(adjustment && adjustment.detectedAt);
  if (!detected || detected.getTime() < geometry.start || detected.getTime() > geometry.end) {
    return;
  }
  const marker = node("button", "adjustment-marker");
  marker.type = "button";
  marker.style.left = `${timelineLeft(detected.getTime(), geometry)}px`;
  marker.dataset.adjustmentId = adjustmentID(account.username, adjustment);
  marker.dataset.adjustmentDetail = `${account.username}: ${adjustmentDescription(adjustment, true)}`;
  marker.setAttribute("aria-label", `${account.username}. ${adjustmentDescription(adjustment)}`);
  marker.setAttribute("aria-controls", "adjustment-detail");
  const diamond = node("span", "adjustment-diamond");
  diamond.setAttribute("aria-hidden", "true");
  marker.append(diamond);
  marker.addEventListener("mouseenter", () => showAdjustmentDetail(account, adjustment));
  marker.addEventListener("mouseleave", () => {
    if (document.activeElement !== marker) {
      restoreFocusedAdjustmentDetail();
    }
  });
  marker.addEventListener("focus", () => showAdjustmentDetail(account, adjustment));
  marker.addEventListener("blur", resetAdjustmentDetail);
  marker.addEventListener("click", () => showAdjustmentDetail(account, adjustment));
  marker.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      marker.blur();
    }
  });
  lane.append(marker);
}

function timelineLeft(timestamp, geometry) {
  return geometry.labelWidth + (timestamp - geometry.start) * geometry.pixelsPerMs;
}

function renderTimelineWindow(lane, windowValue, geometry, account) {
  const start = Number(windowValue.windowStartedAt) * 1000;
  const end = Number(windowValue.resetsAt) * 1000;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < geometry.start || start > geometry.end) {
    return;
  }
  const clippedStart = Math.max(start, geometry.start);
  const clippedEnd = Math.min(end, geometry.end);
  const track = node("span", `reset-window ${windowValue.kind === "active" ? "active" : "history"}`);
  if (account.stale && windowValue.kind === "active") {
    track.classList.add("stale");
  }
  track.style.left = `${timelineLeft(clippedStart, geometry)}px`;
  track.style.width = `${Math.max(2, (clippedEnd - clippedStart) * geometry.pixelsPerMs)}px`;
  lane.append(track);

  if (end >= geometry.start && end <= geometry.end) {
    const marker = node("span", `reset-marker ${windowValue.kind === "active" ? "next" : "past"}`);
    marker.style.left = `${timelineLeft(end, geometry)}px`;
    marker.title = `${windowValue.kind === "active" ? "Next reset" : "Completed reset"}: ${new Date(end).toLocaleString()}`;
    marker.setAttribute("aria-hidden", "true");
    lane.append(marker);
  }
}

function setTimelineToNow(smooth = false) {
  if (!timelineRange) {
    return;
  }
  const target = Math.max(0, timelineLeft(Date.now(), timelineRange) - timelineRange.labelWidth - 16);
  timelineRoot.scrollTo({ left: target, behavior: scrollBehavior(smooth) });
}

function scrollBehavior(smooth) {
  return smooth && !window.matchMedia("(prefers-reduced-motion: reduce)").matches
    ? "smooth"
    : "auto";
}

function renderTimeline(accounts) {
  let anchorTimestamp = null;
  const focusedAdjustmentID = document.activeElement && document.activeElement.dataset
    ? document.activeElement.dataset.adjustmentId || null
    : null;
  if (timelineInitialized && timelineRange) {
    anchorTimestamp = timelineRange.start
      + Math.max(0, timelineRoot.scrollLeft) / timelineRange.pixelsPerMs;
  }

  const geometry = timelineGeometry(accounts);
  resetAdjustmentDetail();
  const canvas = node("div", "timeline-canvas");
  canvas.style.width = `${geometry.width}px`;
  canvas.style.setProperty("--label-width", `${geometry.labelWidth}px`);
  canvas.style.setProperty("--day-width", `${geometry.dayWidth}px`);

  const axis = node("div", "timeline-axis");
  axis.style.width = `${geometry.width}px`;
  let day = localDayStart(geometry.start);
  while (day.getTime() <= geometry.end) {
    const tick = node("div", "day-tick", formatDay(day));
    tick.style.left = `${timelineLeft(day.getTime(), geometry)}px`;
    axis.append(tick);
    day = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
  }
  canvas.append(axis);

  accounts.forEach((account) => {
    const lane = node("div", "timeline-lane");
    lane.style.width = `${geometry.width}px`;
    const label = node("div", "lane-label");
    label.append(node("strong", "", account.username || "unknown"));
    const active = anchoredReset(account);
    const usage = canonicalMainUsage(account);
    let summary = "No weekly data";
    if (active && active.resetsAt) {
      summary = countdown(active.resetsAt);
    } else if (usage && isFloatingUnusedWindow(account, usage)) {
      summary = "Starts on next use";
    }
    label.append(node("span", "", summary));
    lane.append(label);

    const windows = resetWindowsFor(account);
    windows.forEach((windowValue) => renderTimelineWindow(lane, windowValue, geometry, account));
    const adjustments = adjustmentsFor(account.username);
    adjustments.forEach((adjustment) => renderTimelineAdjustment(lane, adjustment, geometry, account));

    if (windows.length === 0) {
      const empty = node("span", "lane-empty", usage ? "No fixed reset" : "No weekly data");
      empty.style.left = `${timelineLeft(Date.now(), geometry) + 22}px`;
      lane.append(empty);
    }

    const accessible = node("span", "visually-hidden");
    const completed = windows.filter((windowValue) => windowValue.kind === "history");
    const currentSummary = active && active.resetsAt
      ? `${account.username} next resets ${formatClock(active.resetsAt)}.`
      : `${account.username} has no fixed weekly reset.`;
    const historySummary = completed.length > 0
      ? ` ${completed.length} completed reset${completed.length === 1 ? "" : "s"} tracked; most recent ${formatClock(completed[completed.length - 1].resetsAt)}.`
      : " No completed resets tracked yet.";
    const recentAdjustmentAt = adjustments.length > 0
      ? validDate(adjustments[adjustments.length - 1].detectedAt)
      : null;
    const adjustmentSummary = adjustments.length > 0
      ? ` ${adjustments.length} server adjustment${adjustments.length === 1 ? "" : "s"} tracked; most recent detected ${recentAdjustmentAt ? recentAdjustmentAt.toLocaleString() : "at an unknown time"}.`
      : " No server adjustments tracked yet.";
    accessible.textContent = currentSummary + historySummary + adjustmentSummary;
    lane.append(accessible);
    canvas.append(lane);
  });

  if (accounts.length === 0) {
    canvas.append(node("div", "timeline-empty", localunitarityOnly
      ? "No matching localunitarity Gmail accounts"
      : "No accounts available"));
  }

  const nowLine = node("span", "now-line");
  nowLine.style.left = `${timelineLeft(Date.now(), geometry)}px`;
  nowLine.dataset.nowLine = "true";
  nowLine.append(node("span", "", "Now"));
  canvas.append(nowLine);

  timelineRoot.replaceChildren(canvas);
  timelineRoot.setAttribute("aria-busy", "false");
  timelineRange = geometry;

  if (!timelineInitialized) {
    timelineInitialized = true;
    requestAnimationFrame(() => setTimelineToNow(false));
  } else if (anchorTimestamp !== null) {
    const restored = Math.max(0, (anchorTimestamp - geometry.start) * geometry.pixelsPerMs);
    timelineRoot.scrollLeft = restored;
  }

  if (focusedAdjustmentID) {
    const marker = Array.from(timelineRoot.querySelectorAll("[data-adjustment-id]"))
      .find((candidate) => candidate.dataset.adjustmentId === focusedAdjustmentID);
    if (marker) {
      marker.focus({ preventScroll: true });
    }
  }

  if (historyStatus && historyStatus.degraded) {
    trackingCopy.textContent = "Live usage is available, but reset history could not be saved. The service will retry automatically.";
    trackingCopy.classList.add("warning-copy");
  } else if (historyStatus && historyStatus.trackingSince) {
    trackingCopy.classList.remove("warning-copy");
    const since = validDate(historyStatus.trackingSince);
    const adjustmentsSince = validDate(historyStatus.adjustmentsTrackingSince);
    trackingCopy.textContent = since
      ? `Reset history since ${since.toLocaleString()}; server adjustments since ${adjustmentsSince ? adjustmentsSince.toLocaleString() : since.toLocaleString()}. Retained for ${historyStatus.retentionDays || MAX_HISTORY_DAYS} days.`
      : "Scroll left to review completed weekly windows and server adjustments.";
  } else {
    trackingCopy.classList.remove("warning-copy");
    trackingCopy.textContent = "Reset history begins when the first weekly window becomes anchored.";
  }
}

function render() {
  if (!currentStatus || !Array.isArray(currentStatus.accounts)) {
    return;
  }
  const accounts = displayedAccounts();
  updateViewControls(accounts.length);
  renderAccountSummary(accounts);
  renderTimeline(accounts);
  demoBanner.hidden = !currentStatus.demo;
  updatedAt.textContent = relativeTime(currentStatus.generatedAt);
  const generated = validDate(currentStatus.generatedAt);
  if (generated) {
    updatedAt.dateTime = generated.toISOString();
    updatedAt.title = generated.toLocaleString();
  }
}

function updateClockText() {
  document.querySelectorAll("[data-countdown]").forEach((element) => {
    element.textContent = countdown(element.dataset.countdown);
  });
  document.querySelectorAll("[data-relative-time]").forEach((element) => {
    element.textContent = relativeTime(element.dataset.relativeTime);
  });
  if (currentStatus) {
    updatedAt.textContent = relativeTime(currentStatus.generatedAt);
  }
  if (timelineRange) {
    const line = timelineRoot.querySelector("[data-now-line]");
    if (line) {
      line.style.left = `${timelineLeft(Date.now(), timelineRange)}px`;
    }
  }
}

function acceptStatus(status) {
  if (!status || status.schemaVersion !== 1 || !Array.isArray(status.accounts)) {
    return;
  }
  const incomingGenerated = validDate(status.generatedAt);
  const currentGenerated = currentStatus ? validDate(currentStatus.generatedAt) : null;
  if (incomingGenerated && currentGenerated && incomingGenerated < currentGenerated) {
    return;
  }
  const revision = Number(status.revision);
  const newerEpoch = incomingGenerated && currentGenerated && incomingGenerated > currentGenerated;
  const restarted = Number.isFinite(revision) && revision < latestStatusRevision && newerEpoch;
  if (Number.isFinite(revision) && revision < latestStatusRevision && !restarted) {
    return;
  }
  latestStatusRevision = Number.isFinite(revision) ? revision : latestStatusRevision;
  currentStatus = status;
  render();
  const announcementSignature = JSON.stringify(status.accounts.map((account) => {
    const usage = canonicalMainUsage(account);
    return [
      account.username,
      account.state,
      Boolean(account.stale),
      usage ? usage.usedPercent : null,
      usage ? usage.resetsAt : null,
      resetCreditsAvailable(account),
    ];
  }));
  if (announcementSignature !== lastAnnouncementSignature) {
    screenReaderStatus.textContent = lastAnnouncementSignature
      ? "Account usage or reset state updated."
      : `Usage loaded. ${selectionCount.textContent}.`;
    lastAnnouncementSignature = announcementSignature;
  }
}

function acceptHistory(history) {
  if (!history || ![1, HISTORY_SCHEMA_VERSION].includes(history.schemaVersion) || !Array.isArray(history.accounts)) {
    return;
  }
  const incomingGenerated = validDate(history.generatedAt);
  const currentGenerated = historyStatus ? validDate(historyStatus.generatedAt) : null;
  if (incomingGenerated && currentGenerated && incomingGenerated < currentGenerated) {
    return;
  }
  const revision = Number(history.revision);
  const newerEpoch = incomingGenerated && currentGenerated && incomingGenerated > currentGenerated;
  const restarted = Number.isFinite(revision) && revision < latestHistoryRevision && newerEpoch;
  if (Number.isFinite(revision) && revision < latestHistoryRevision && !restarted) {
    return;
  }
  latestHistoryRevision = Number.isFinite(revision) ? revision : latestHistoryRevision;
  const incomingAdjustmentIDs = new Set();
  history.accounts.forEach((account) => {
    if (Array.isArray(account.adjustments)) {
      account.adjustments.forEach((adjustment) => {
        incomingAdjustmentIDs.add(adjustmentID(account.username, adjustment));
      });
    }
  });
  if (knownAdjustmentIDs !== null) {
    let added = 0;
    incomingAdjustmentIDs.forEach((id) => {
      if (!knownAdjustmentIDs.has(id)) {
        added++;
      }
    });
    if (added > 0) {
      screenReaderStatus.textContent = `${added} new server adjustment${added === 1 ? "" : "s"} recorded.`;
    }
  }
  knownAdjustmentIDs = incomingAdjustmentIDs;
  historyStatus = history;
  if (currentStatus) {
    render();
  }
}

async function fetchStatus() {
  try {
    const response = await fetch("/api/v1/status", {
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      throw new Error("status request failed");
    }
    acceptStatus(await response.json());
    if (!streamHasOpened) {
      setConnection("", "Snapshot loaded");
    }
  } catch {
    if (!currentStatus) {
      setConnection("offline", "Unavailable");
    }
  }
}

async function fetchHistory() {
  try {
    const response = await fetch("/api/v1/history", {
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      return;
    }
    acceptHistory(await response.json());
  } catch {
    // Current usage remains useful while retained history is unavailable.
  }
}

function connectEvents() {
  const stream = new EventSource("/api/v1/events");
  stream.addEventListener("open", () => {
    // Revisions are process-local for live state. A reconnect may follow a
    // service restart, so allow the new process to begin again at revision 0.
    latestStatusRevision = -1;
    latestHistoryRevision = -1;
    streamHasOpened = true;
    setConnection("live", "Live updates");
    fetchHistory();
  });
  stream.addEventListener("snapshot", (event) => {
    try {
      acceptStatus(JSON.parse(event.data));
    } catch {
      setConnection("offline", "Invalid update");
    }
  });
  stream.addEventListener("error", () => {
    streamHasOpened = false;
    setConnection("offline", "Reconnecting");
  });
}

earlierButton.addEventListener("click", () => {
  if (timelineRange) {
    timelineRoot.scrollBy({ left: -7 * timelineRange.dayWidth, behavior: scrollBehavior(true) });
  }
});

nowButton.addEventListener("click", () => setTimelineToNow(true));

localunitarityFilter.addEventListener("change", () => {
  localunitarityOnly = localunitarityFilter.checked;
  render();
  screenReaderStatus.textContent = `${selectionCount.textContent}. ${localunitarityOnly
    ? "Localunitarity Gmail filter enabled."
    : "All Codex accounts shown."}`;
});

sortModeButton.addEventListener("click", () => {
  sortMode = sortMode === "priority" ? "alphabetical" : "priority";
  render();
  screenReaderStatus.textContent = sortMode === "priority"
    ? `Priority sorting enabled by ${priorityBasis === "reset" ? "soonest weekly reset" : "most quota remaining"}.`
    : "Alphabetical sorting enabled.";
});

priorityBasisInputs.forEach((input) => {
  input.addEventListener("change", () => {
    if (!input.checked) {
      return;
    }
    priorityBasis = input.value === "reset" ? "reset" : "remaining";
    render();
    screenReaderStatus.textContent = `Priority ordering now uses ${priorityBasis === "reset"
      ? "soonest weekly reset"
      : "most quota remaining"}.`;
  });
});

timelineRoot.addEventListener("keydown", (event) => {
  if (!timelineRange) {
    return;
  }
  const movements = {
    ArrowLeft: -timelineRange.dayWidth,
    ArrowRight: timelineRange.dayWidth,
    PageUp: -7 * timelineRange.dayWidth,
    PageDown: 7 * timelineRange.dayWidth,
  };
  if (movements[event.key] !== undefined) {
    event.preventDefault();
    timelineRoot.scrollBy({ left: movements[event.key], behavior: scrollBehavior(true) });
  }
});

if ("ResizeObserver" in window) {
  let resizeTimer = null;
  new ResizeObserver(() => {
    window.clearTimeout(resizeTimer);
    resizeTimer = window.setTimeout(() => {
      if (currentStatus) {
        renderTimeline(displayedAccounts());
      }
    }, 100);
  }).observe(timelineRoot);
}

fetchStatus();
fetchHistory();
connectEvents();
window.setInterval(updateClockText, 1000);
window.setInterval(fetchStatus, 60000);
window.setInterval(fetchHistory, 60000);

"use strict";

const accountsRoot = document.getElementById("accounts");
const updatedAt = document.getElementById("updated-at");
const demoBanner = document.getElementById("demo-banner");
const connectionDot = document.getElementById("connection-dot");
const connectionLabel = document.getElementById("connection-label");

let currentStatus = null;
let streamHasOpened = false;

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

function validDate(value, seconds) {
  if (value === null || value === undefined || value === "") {
    return null;
  }
  const date = new Date(seconds ? Number(value) * 1000 : value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatClock(value, seconds) {
  const date = validDate(value, seconds);
  if (!date) {
    return "Reset time unavailable";
  }
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
}

function relativeTime(value) {
  const date = validDate(value, false);
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
    return "Countdown unavailable";
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

function windowName(windowValue, fallback) {
  const minutes = windowValue.windowDurationMins;
  if (!minutes) {
    return fallback;
  }
  if (minutes % 10080 === 0) {
    const weeks = minutes / 10080;
    return weeks === 1 ? "Weekly window" : `${weeks}-week window`;
  }
  if (minutes % 1440 === 0) {
    const days = minutes / 1440;
    return days === 1 ? "Daily window" : `${days}-day window`;
  }
  if (minutes % 60 === 0) {
    return `${minutes / 60}-hour window`;
  }
  return `${minutes}-minute window`;
}

function appendWindow(parent, windowValue, fallbackName) {
  const section = node("section", "window");
  const heading = node("div", "window-title");
  heading.append(node("strong", "", windowName(windowValue, fallbackName)));

  const reset = node("span", "reset-at", formatClock(windowValue.resetsAt, true));
  if (windowValue.resetsAt) {
    const resetDate = validDate(windowValue.resetsAt, true);
    if (resetDate) {
      reset.title = resetDate.toLocaleString();
    }
  }
  heading.append(reset);
  section.append(heading);

  const copy = node("div", "quota-copy");
  const main = node("div", "quota-main");
  const used = Number(windowValue.usedPercent);
  const remaining = Number(windowValue.remainingPercent);
  main.append(document.createTextNode(`${used}% `), node("span", "", "used"));
  copy.append(main, node("div", "quota-remaining", `${remaining}% remaining`));
  section.append(copy);

  const meter = node("div", "meter");
  meter.setAttribute("role", "progressbar");
  meter.setAttribute("aria-label", `${windowName(windowValue, fallbackName)} usage`);
  meter.setAttribute("aria-valuemin", "0");
  meter.setAttribute("aria-valuemax", "100");
  meter.setAttribute("aria-valuenow", String(used));
  if (used >= 100) {
    meter.classList.add("danger");
  } else if (used >= 80) {
    meter.classList.add("warn");
  }
  const fill = node("span");
  fill.style.width = `${Math.max(0, Math.min(100, used))}%`;
  meter.append(fill);
  section.append(meter);

  const meta = node("div", "window-meta");
  meta.append(
    node("span", "", windowValue.windowDurationMins ? `${windowValue.windowDurationMins.toLocaleString()} min allocation` : "Rolling allocation"),
    node("span", "countdown", countdown(windowValue.resetsAt)),
  );
  section.append(meta);
  parent.append(section);
}

function reached(limit) {
  return Boolean(
    limit.spendControlReached ||
    limit.reachedType ||
    (limit.primary && limit.primary.usedPercent >= 100) ||
    (limit.secondary && limit.secondary.usedPercent >= 100),
  );
}

function accountReached(account) {
  return Array.isArray(account.limits) && account.limits.some(reached);
}

function statusPresentation(account) {
  if (account.stale) {
    return { label: "Stale", className: "warning" };
  }
  if (accountReached(account)) {
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

function safeErrorMessage(category) {
  const messages = {
    awaiting_collector: "Waiting for this user’s collector to publish its first snapshot.",
    auth_unavailable: "The account session is unavailable. Sign in again from this Linux account.",
    codex_unavailable: "The local Codex App Server is temporarily unavailable.",
    protocol_error: "The installed Codex App Server returned an incompatible response.",
    rate_limit_read_failed: "Account identity is available, but quota windows could not be refreshed.",
    publish_failed: "The collector could not deliver its latest snapshot.",
  };
  return messages[category] || "Live account data is temporarily unavailable.";
}

function appendLimit(parent, limit) {
  const block = node("article", "limit-block");
  const heading = node("div", "limit-heading");
  const name = limit.name || limit.id || "Quota";
  heading.append(node("h3", "limit-name", name));
  if (limit.id && limit.id !== name) {
    heading.append(node("span", "limit-id", limit.id));
  } else if (limit.planType) {
    heading.append(node("span", "limit-id", titleCase(limit.planType)));
  }
  block.append(heading);

  let windows = 0;
  if (limit.primary) {
    appendWindow(block, limit.primary, "Primary window");
    windows += 1;
  }
  if (limit.secondary) {
    appendWindow(block, limit.secondary, "Secondary window");
    windows += 1;
  }
  if (windows === 0) {
    block.append(node("p", "state-message", "This bucket did not return a percentage window."));
  }

  if (limit.credits) {
    const credit = node("div", "credit-row");
    let value = "No balance";
    if (limit.credits.unlimited) {
      value = "Unlimited";
    } else if (limit.credits.balance !== undefined && limit.credits.balance !== null) {
      value = String(limit.credits.balance);
    } else if (limit.credits.hasCredits) {
      value = "Available";
    }
    credit.append(node("span", "", "Credits"), node("strong", "", value));
    block.append(credit);
  }

  if (limit.individualLimit) {
    const individual = node("div", "individual-row");
    individual.append(
      node("span", "", `Spend control · ${countdown(limit.individualLimit.resetsAt)}`),
      node("strong", "", `${limit.individualLimit.used} / ${limit.individualLimit.limit}`),
    );
    block.append(individual);
  }

  if (reached(limit)) {
    const reason = limit.reachedType ? ` (${titleCase(limit.reachedType)})` : "";
    block.append(node("p", "state-message", `This quota is currently reached${reason}.`));
  }
  parent.append(block);
}

function emptyState(account) {
  const empty = node("div", "empty-state");
  let title = "Usage unavailable";
  let message = safeErrorMessage(account.errorCategory);
  if (account.state === "signed_out") {
    title = "Not signed in";
    message = "Open Codex as this Linux user and complete ChatGPT sign-in to populate quota windows.";
  } else if (account.state === "api_key") {
    title = "API key session";
    message = "ChatGPT subscription quota windows are not reported for API-key authentication.";
  } else if (account.state === "ok") {
    title = "No quota windows";
    message = "The account is signed in, but App Server did not return a usage bucket.";
  }
  empty.append(node("strong", "", title), node("p", "", message));
  return empty;
}

function renderAccount(account) {
  const card = node("article", "account-card");
  if (account.stale) {
    card.classList.add("is-stale");
  }
  if (accountReached(account)) {
    card.classList.add("is-reached");
  }

  const top = node("header", "card-top");
  const user = node("div", "card-user");
  const initial = String(account.username || "?").replace("codex", "c").slice(0, 2);
  user.append(node("div", "user-orb", initial));

  const identity = node("div");
  identity.append(node("div", "username", account.username || "unknown"));
  const email = account.account && account.account.email ? account.account.email : "No ChatGPT email";
  const emailNode = node("div", "email", email);
  emailNode.title = email;
  identity.append(emailNode);
  if (account.account && account.account.planType) {
    identity.append(node("div", "plan", `${titleCase(account.account.planType)} plan`));
  }
  user.append(identity);

  const presentation = statusPresentation(account);
  top.append(user, node("span", `status-pill ${presentation.className}`.trim(), presentation.label));
  card.append(top);

  const hasLastGoodLimits = Array.isArray(account.limits) && account.limits.length > 0;
  if (account.stale) {
    card.append(node("p", "state-message", hasLastGoodLimits
      ? "Collector data is stale. Showing the last successful quota snapshot."
      : "This collector has not delivered fresh account data."));
  } else if (account.state === "unavailable" && hasLastGoodLimits) {
    card.append(node("p", "state-message", `${safeErrorMessage(account.errorCategory)} Showing last-good quota data.`));
  }

  if (hasLastGoodLimits) {
    const limits = node("div", "limit-list");
    account.limits.forEach((limit) => appendLimit(limits, limit));
    card.append(limits);
  } else {
    card.append(emptyState(account));
  }

  const footer = node("footer", "card-footer");
  footer.append(
    node("span", "", `Observed ${relativeTime(account.observedAt)}`),
    node("span", "", `Collector ${relativeTime(account.lastSeenAt)}`),
  );
  card.append(footer);
  return card;
}

function render() {
  if (!currentStatus || !Array.isArray(currentStatus.accounts)) {
    return;
  }
  const cards = currentStatus.accounts.map(renderAccount);
  accountsRoot.replaceChildren(...cards);
  accountsRoot.setAttribute("aria-busy", "false");
  demoBanner.hidden = !currentStatus.demo;
  updatedAt.textContent = relativeTime(currentStatus.generatedAt);
  const generated = validDate(currentStatus.generatedAt, false);
  if (generated) {
    updatedAt.dateTime = generated.toISOString();
    updatedAt.title = generated.toLocaleString();
  }
}

function acceptStatus(status) {
  if (!status || status.schemaVersion !== 1 || !Array.isArray(status.accounts)) {
    return;
  }
  currentStatus = status;
  render();
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

function connectEvents() {
  const stream = new EventSource("/api/v1/events");
  stream.addEventListener("open", () => {
    streamHasOpened = true;
    setConnection("live", "Live updates");
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

fetchStatus();
connectEvents();
setInterval(render, 1000);
setInterval(fetchStatus, 60000);

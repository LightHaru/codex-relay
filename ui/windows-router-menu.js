/*
 * Windows renderer bridge for Codex Relay.
 *
 * This deliberately uses DOM insertion instead of renderer-private React
 * symbols. Windows builds rename/minify those symbols independently of macOS,
 * while the profile menu continues to expose stable visible menu rows.
 */
(() => {
  "use strict";

  const API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1";
  const TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";
  const MENU_ID = "codex-mux-windows-menu";
  const STYLE_ID = "codex-mux-windows-menu-style";
  const PROFILE_ACCOUNT_KEY = "codex-mux.windows.profile-account";
  const PLUGIN_ACCOUNT_KEY = "codex-mux.windows.plugin-account";
  const RESET_ACCOUNT_KEY = "codex-mux.windows.reset-account";
  const SCOPED_PLUGIN_METHODS = new Set([
    "app/installed",
    "app/list",
    "app/read",
    "mcpServer/oauth/login",
    "mcpServerStatus/list",
  ]);
  // The native desktop profile menu renders Settings with its keyboard
  // shortcut in the same button (for example, "Settings ⌘,").  Match the
  // smallest visible text node first, then walk back to its menu row.  This is
  // deliberately also tolerant of the Vietnamese labels used by the app.
  const PROFILE_SETTINGS_LABELS = ["Settings", "Cài đặt"];
  const PROFILE_LOGOUT_LABELS = ["Log out", "Sign out", "Đăng xuất"];
  let refreshTimer = null;
  let scheduled = false;
  let activeLogin = null;
  let activeToast = null;
  let activeUpdateToast = null;
  let updateWatcherStarted = false;
  let latestAccounts = [];
  const resetSubscribers = new Set();

  function normalize(value) {
    return String(value || "").replace(/\s+/g, " ").trim();
  }

  function isVisible(element) {
    if (!(element instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(element);
    return style.display !== "none" && style.visibility !== "hidden" && element.getClientRects().length > 0;
  }

  function make(tag, className, content) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (content != null) element.textContent = String(content);
    return element;
  }

  function append(parent, ...children) {
    for (const child of children.flat()) {
      if (child != null) parent.append(child);
    }
    return parent;
  }

  async function request(path, options = {}) {
    const response = await fetch(`${API}${path}`, {
      ...options,
      headers: {
        "Content-Type": "application/json",
        "X-Codex-Mux-Token": TOKEN,
        ...(options.headers || {}),
      },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Router request failed (${response.status})`);
    return body;
  }

  function connectedAccounts(accounts) {
    return (Array.isArray(accounts) ? accounts : []).filter(
      (account) => account?.enabled && account?.connected,
    );
  }

  function readSessionValue(key, fallback = null) {
    try {
      return window.sessionStorage.getItem(key) || fallback;
    } catch {
      return fallback;
    }
  }

  function writeSessionValue(key, value) {
    try {
      if (value) window.sessionStorage.setItem(key, value);
      else window.sessionStorage.removeItem(key);
    } catch {
      // The router remains usable when the embedded renderer disables storage.
    }
  }

  function getProfileAccountId() {
    return readSessionValue(PROFILE_ACCOUNT_KEY);
  }

  function setProfileAccountId(accountId) {
    writeSessionValue(PROFILE_ACCOUNT_KEY, accountId || null);
  }

  function getPluginAccountId() {
    return readSessionValue(PLUGIN_ACCOUNT_KEY, "primary");
  }

  function setPluginAccountId(accountId) {
    writeSessionValue(PLUGIN_ACCOUNT_KEY, accountId || "primary");
  }

  function getResetAccountId() {
    return readSessionValue(RESET_ACCOUNT_KEY, "primary");
  }

  function notifyResetSubscribers() {
    for (const listener of [...resetSubscribers]) {
      try {
        listener();
      } catch {
        // A stale renderer listener must not prevent other surfaces updating.
      }
    }
  }

  function setResetAccountId(accountId) {
    writeSessionValue(RESET_ACCOUNT_KEY, accountId || "primary");
    notifyResetSubscribers();
  }

  function subscribeReset(listener) {
    resetSubscribers.add(listener);
    return () => resetSubscribers.delete(listener);
  }

  function scopePluginRequest(method, params) {
    if (!SCOPED_PLUGIN_METHODS.has(method)) return params;
    if (params != null && (typeof params !== "object" || Array.isArray(params))) return params;
    const accountId = getPluginAccountId();
    return accountId ? { ...(params || {}), codexMuxAccountId: accountId } : params;
  }

  async function profileData() {
    const accountId = getProfileAccountId();
    const query = accountId ? `?accountId=${encodeURIComponent(accountId)}` : "";
    try {
      const result = await request(`/profile/combined${query}`);
      return result.profile;
    } catch (error) {
      // A deleted/disabled account can leave a stale tab-session selection.
      // Recover to the combined view instead of making the Profile page blank.
      if (!accountId) throw error;
      setProfileAccountId(null);
      const result = await request("/profile/combined");
      return result.profile;
    }
  }

  // Settings -> Usage normally reads the Store app's browser session. Relay
  // subscriptions intentionally use isolated Codex homes instead, so ask the
  // local Router for the same native usage payload. Returning null on a local
  // failure lets the version-pinned renderer patch fall back to its normal
  // request instead of turning the entire Settings page into an error screen.
  async function nativeUsageStatus() {
    try {
      const result = await request("/usage");
      return result && typeof result.usage === "object" ? result.usage : null;
    } catch {
      return null;
    }
  }

  function rateLimitResets(accountId) {
    return request(`/accounts/${encodeURIComponent(accountId)}/rate-limit-resets`);
  }

  function consumeRateLimitReset(accountId, input) {
    return request(`/accounts/${encodeURIComponent(accountId)}/rate-limit-resets/consume`, {
      method: "POST",
      body: JSON.stringify({
        creditId: input?.creditId ?? null,
        redeemRequestId: input?.redeemRequestId,
      }),
    });
  }

  function usageWindows(rateLimits) {
    return [rateLimits?.primary, rateLimits?.secondary]
      .filter(Boolean)
      .map((window) => {
        const usedPercent = Number(window.usedPercent);
        if (!Number.isFinite(usedPercent)) return null;
        const duration = Number(window.windowDurationMins);
        return {
          usedPercent: Math.max(0, Math.min(100, usedPercent)),
          remainingPercent: Math.max(0, Math.min(100, 100 - usedPercent)),
          windowMinutes: Number.isFinite(duration) && duration > 0 ? duration : 0,
          resetsAt: window.resetsAt ?? null,
        };
      })
      .filter(Boolean);
  }

  function selectedResetUsageWindows() {
    const account = connectedAccounts(latestAccounts).find(
      (item) => item.id === getResetAccountId(),
    );
    return account ? usageWindows(account.rateLimits) : null;
  }

  function longestUsageWindow(account) {
    const windows = usageWindows(account?.rateLimits)
      .sort((left, right) => left.windowMinutes - right.windowMinutes);
    return windows.at(-1) || null;
  }

  function remainingUsage(account) {
    const usage = longestUsageWindow(account);
    return usage ? usage.remainingPercent : null;
  }

  function usageLabel(account) {
    const remaining = remainingUsage(account);
    if (remaining != null) return `${Math.round(remaining)}% left`;
    if (account?.rateLimitError) return "Quota unavailable";
    return "Updating quota…";
  }

  function formatWindowDuration(minutes) {
    const value = Number(minutes);
    if (!Number.isFinite(value) || value <= 0) return "quota";
    if (value % 10080 === 0) return `${value / 10080}w`;
    if (value % 1440 === 0) return `${value / 1440}d`;
    if (value % 60 === 0) return `${value / 60}h`;
    return `${value}m`;
  }

  function formatResetCountdown(resetsAt, now = Date.now()) {
    const seconds = Number(resetsAt);
    if (!Number.isFinite(seconds) || seconds <= 0) return null;
    const remainingMinutes = Math.ceil((seconds * 1000 - now) / 60000);
    if (remainingMinutes <= 0) return "now";
    if (remainingMinutes < 60) return `${remainingMinutes}m`;
    const hours = Math.floor(remainingMinutes / 60);
    const minutes = remainingMinutes % 60;
    if (hours < 48) return minutes ? `${hours}h ${minutes}m` : `${hours}h`;
    const days = Math.floor(hours / 24);
    const leftoverHours = hours % 24;
    return leftoverHours ? `${days}d ${leftoverHours}h` : `${days}d`;
  }

  function quotaResetSummary(account) {
    const windows = usageWindows(account?.rateLimits)
      .filter((window) => Number.isFinite(Number(window.resetsAt)) && Number(window.resetsAt) > 0)
      .sort((left, right) => Number(left.resetsAt) - Number(right.resetsAt));
    if (windows.length === 0) {
      return account?.rateLimitError ? "Reset time unavailable" : "Reset time not reported";
    }
    return `Reset ${windows.map((window) => `${formatWindowDuration(window.windowMinutes)}: ${formatResetCountdown(window.resetsAt)}`).join(" · ")}`;
  }

  function quotaResetTitle(account) {
    const windows = usageWindows(account?.rateLimits)
      .filter((window) => Number.isFinite(Number(window.resetsAt)) && Number(window.resetsAt) > 0)
      .sort((left, right) => Number(left.resetsAt) - Number(right.resetsAt));
    if (windows.length === 0) return quotaResetSummary(account);
    return windows.map((window) => {
      const date = new Date(Number(window.resetsAt) * 1000);
      return `${formatWindowDuration(window.windowMinutes)}: ${date.toLocaleString()}`;
    }).join(" · ");
  }

  function usageStatus(account) {
    const windows = usageWindows(account?.rateLimits);
    const quota = windows.length > 0
      ? `${Math.round(remainingUsage(account))}% quota left`
      : account?.rateLimitError ? "Quota data unavailable" : "Quota data is updating";
    return `${quota} · ${quotaResetSummary(account)}`;
  }

  function accountDisplayName(account) {
    return [account?.displayName, account?.username, account?.email, account?.label]
      .map((value) => String(value || "").trim())
      .find(Boolean) || "Subscription";
  }

  function accountIdentityDetail(account) {
    const displayName = accountDisplayName(account);
    return [account?.email, account?.username, account?.label]
      .map((value) => String(value || "").trim())
      .filter((value, index, values) => value && value !== displayName && values.indexOf(value) === index)
      .join(" · ");
  }

  function accountName(account) {
    const name = accountDisplayName(account);
    return account?.planLabel ? `${name} · ${account.planLabel}` : name;
  }

  function initials(label) {
    const letters = String(label || "?")
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join("");
    return letters || "?";
  }

  function avatar(account) {
    const shell = make("span", "codex-mux-win-avatar");
    shell.setAttribute("aria-hidden", "true");
    if (account.profileImageUrl) {
      const image = document.createElement("img");
      image.src = account.profileImageUrl;
      image.alt = "";
      image.referrerPolicy = "no-referrer";
      image.addEventListener("error", () => {
      image.replaceWith(document.createTextNode(initials(accountDisplayName(account))));
      });
      shell.append(image);
    } else {
      shell.textContent = initials(accountDisplayName(account));
    }
    return shell;
  }

  function row(account, onCancelPending) {
    const usage = remainingUsage(account);
    const line = make("div", "codex-mux-win-row");
    const identity = make("div", "codex-mux-win-identity");
    const labels = make("div", "codex-mux-win-labels");
    const primary = make("div", "codex-mux-win-name", accountName(account));
    const identityDetail = accountIdentityDetail(account);
    const secondary = make(
      "div",
      "codex-mux-win-subtext",
      account.connected
        ? `${account.controller ? "Primary · " : ""}${usageStatus(account)}`
        : "Waiting for sign-in",
    );
    append(labels, primary, identityDetail ? make("div", "codex-mux-win-account-id", identityDetail) : null, secondary);
    append(identity, avatar(account), labels);
    line.title = quotaResetTitle(account);
    line.append(identity);
    if (!account.connected && !account.controller && typeof onCancelPending === "function") {
      const cancel = make("button", "codex-mux-win-pending-cancel", "Cancel sign-in");
      cancel.type = "button";
      cancel.setAttribute("aria-label", `Cancel sign-in for ${account.label || "subscription"}`);
      cancel.addEventListener("click", (event) => {
        event.preventDefault();
        event.stopPropagation();
        onCancelPending(cancel);
      });
      line.append(cancel);
    } else {
      line.append(make("div", `codex-mux-win-percent${usage == null ? " codex-mux-win-percent-muted" : ""}`, usageLabel(account)));
    }
    return line;
  }

  function addStyles() {
    if (document.getElementById(STYLE_ID)) return;
    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = `
      #${MENU_ID} { box-sizing: border-box; color: var(--text-primary, var(--token-text-primary, inherit)); font-family: inherit; }
      #${MENU_ID} *, #${MENU_ID} *::before, #${MENU_ID} *::after { box-sizing: border-box; }
      .codex-mux-win-divider { height: 1px; margin: 6px 8px; background: color-mix(in srgb, currentColor 18%, transparent); opacity: .65; }
      .codex-mux-win-summary, .codex-mux-win-row, .codex-mux-win-add { display: flex; align-items: center; min-height: 42px; gap: 10px; margin: 0 3px; padding: 7px 9px; border-radius: 9px; color: inherit; }
      .codex-mux-win-summary { cursor: default; }
      .codex-mux-win-summary-icon { display: grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); }
      .codex-mux-win-summary-label { flex: 1; min-width: 0; }
      .codex-mux-win-title, .codex-mux-win-name { overflow: hidden; color: inherit; font-size: 14px; font-weight: 500; line-height: 18px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-subtext { overflow: hidden; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 16px; text-overflow: ellipsis; white-space: nowrap; opacity: .8; }
      .codex-mux-win-account-id { overflow: hidden; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; text-overflow: ellipsis; white-space: nowrap; opacity: .68; }
      .codex-mux-win-total, .codex-mux-win-percent { margin-left: auto; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 14px; font-variant-numeric: tabular-nums; opacity: .82; }
      .codex-mux-win-percent-muted { max-width: 112px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; text-align: right; white-space: normal; }
      .codex-mux-win-row { cursor: default; }
      .codex-mux-win-identity { display: flex; min-width: 0; flex: 1; align-items: center; gap: 10px; }
      .codex-mux-win-labels { min-width: 0; }
      .codex-mux-win-avatar { display: grid; width: 24px; height: 24px; flex: 0 0 24px; place-items: center; overflow: hidden; border-radius: 50%; background: color-mix(in srgb, #5b7cfa 45%, transparent); color: white; font-size: 9px; font-weight: 700; }
      .codex-mux-win-avatar img { width: 100%; height: 100%; object-fit: cover; }
      .codex-mux-win-pending-cancel { margin-left: auto; border: 0; border-radius: 7px; background: rgb(255 255 255 / .12); color: inherit; cursor: pointer; font: inherit; font-size: 12px; padding: 5px 8px; }
      .codex-mux-win-pending-cancel:hover, .codex-mux-win-pending-cancel:focus-visible { background: rgb(255 255 255 / .2); outline: none; }
      .codex-mux-win-pending-cancel:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-add { width: calc(100% - 6px); border: 0; background: transparent; cursor: pointer; font: inherit; text-align: left; }
      .codex-mux-win-add:hover, .codex-mux-win-add:focus-visible { background: color-mix(in srgb, currentColor 9%, transparent); outline: none; }
      .codex-mux-win-add:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-plus { display: inline-grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 23px; font-weight: 300; line-height: 1; }
      .codex-mux-win-settings { width: calc(100% - 6px); border: 0; background: transparent; cursor: pointer; font: inherit; text-align: left; }
      .codex-mux-win-settings:hover, .codex-mux-win-settings:focus-visible { background: color-mix(in srgb, currentColor 9%, transparent); outline: none; }
      .codex-mux-win-settings-icon { display: inline-grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 17px; }
      .codex-mux-win-modal-manager { width: min(560px, 100%); max-height: min(720px, calc(100vh - 48px)); overflow: auto; }
      .codex-mux-win-account-list { display: grid; gap: 8px; margin-top: 16px; }
      .codex-mux-win-account-card { display: flex; min-width: 0; align-items: center; gap: 10px; border: 1px solid color-mix(in srgb, currentColor 14%, transparent); border-radius: 12px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 10px; }
      .codex-mux-win-account-card .codex-mux-win-identity { min-width: 0; }
      .codex-mux-win-account-card-actions { display: flex; flex: 0 0 auto; flex-wrap: wrap; justify-content: flex-end; gap: 6px; }
      .codex-mux-win-account-action { min-height: 30px; border: 0; border-radius: 7px; background: rgb(255 255 255 / .12); color: inherit; cursor: pointer; font: inherit; font-size: 12px; padding: 6px 9px; }
      .codex-mux-win-account-action:hover, .codex-mux-win-account-action:focus-visible { background: rgb(255 255 255 / .2); outline: none; }
      .codex-mux-win-account-action-primary { background: #4f6bed; color: white; }
      .codex-mux-win-account-action-danger { background: color-mix(in srgb, #d95757 38%, transparent); }
      .codex-mux-win-account-action:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-account-badge { display: inline-flex; align-items: center; margin-left: 5px; border-radius: 999px; background: color-mix(in srgb, #6e86f7 32%, transparent); color: inherit; font-size: 10px; line-height: 16px; padding: 0 6px; vertical-align: 1px; }
      .codex-mux-win-account-meta { min-width: 0; flex: 1; }
      .codex-mux-win-account-meta .codex-mux-win-name { white-space: normal; }
      .codex-mux-win-account-hint { margin-top: 3px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; opacity: .82; }
      .codex-mux-win-close-button { margin-left: auto; }
      .codex-mux-win-error { margin: 4px 11px 8px; color: #e05a65; font-size: 12px; line-height: 16px; }
      .codex-mux-win-modal-backdrop { position: fixed; z-index: 2147483647; inset: 0; display: grid; place-items: center; padding: 24px; background: rgb(0 0 0 / .48); }
      .codex-mux-win-modal { width: min(420px, 100%); border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 16px; background: var(--main-surface-background, var(--token-main-surface-background, #292929)); box-shadow: 0 20px 60px rgb(0 0 0 / .42); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 20px; }
      .codex-mux-win-manager-header { display: flex; align-items: flex-start; gap: 8px; }
      .codex-mux-win-manager-header h2 { flex: 1; }
      .codex-mux-win-modal h2 { margin: 0; font-size: 17px; line-height: 24px; }
      .codex-mux-win-modal p { margin: 8px 0 0; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 14px; line-height: 20px; }
      .codex-mux-win-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 18px; }
      .codex-mux-win-actions button, .codex-mux-win-actions a { display: inline-flex; min-height: 34px; align-items: center; justify-content: center; border: 0; border-radius: 8px; background: rgb(255 255 255 / .13); color: inherit; cursor: pointer; font: inherit; font-size: 13px; padding: 7px 12px; text-decoration: none; }
      .codex-mux-win-actions button:hover, .codex-mux-win-actions a:hover { background: rgb(255 255 255 / .2); }
      .codex-mux-win-actions .codex-mux-win-primary { background: #4f6bed; color: white; }
      .codex-mux-win-status { min-height: 20px; margin-top: 12px; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 12px; }
      .codex-mux-win-toast { position: fixed; z-index: 2147483646; top: 20px; right: 20px; display: flex; width: min(380px, calc(100vw - 32px)); align-items: flex-start; gap: 11px; border: 1px solid color-mix(in srgb, #55c982 58%, transparent); border-radius: 13px; background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #164a2c); box-shadow: 0 16px 45px rgb(0 0 0 / .35); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 13px 14px; }
      .codex-mux-win-toast-icon { display: grid; width: 22px; height: 22px; flex: 0 0 22px; place-items: center; border-radius: 50%; background: #2da861; color: white; font-size: 14px; font-weight: 800; }
      .codex-mux-win-toast-copy { min-width: 0; flex: 1; }
      .codex-mux-win-toast-title { font-size: 14px; font-weight: 650; line-height: 19px; }
      .codex-mux-win-toast-detail { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 12px; line-height: 17px; }
      .codex-mux-win-toast-close { width: 24px; height: 24px; border: 0; border-radius: 7px; background: transparent; color: inherit; cursor: pointer; font-size: 18px; line-height: 20px; opacity: .75; }
      .codex-mux-win-toast-close:hover, .codex-mux-win-toast-close:focus-visible { background: rgb(255 255 255 / .12); opacity: 1; outline: none; }
      .codex-mux-win-toast-neutral { border-color: color-mix(in srgb, #8ea0b8 50%, transparent); background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #273445); }
      .codex-mux-win-toast-neutral .codex-mux-win-toast-icon { background: #64748b; }
      .codex-mux-win-update-toast { border-color: color-mix(in srgb, #6e86f7 70%, transparent); background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #28375f); }
      .codex-mux-win-update-toast .codex-mux-win-toast-icon { background: #5873e8; }
      .codex-mux-win-update-actions { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 9px; }
      .codex-mux-win-update-button { min-height: 30px; border: 0; border-radius: 7px; background: #5873e8; color: white; cursor: pointer; font: inherit; font-size: 12px; font-weight: 600; padding: 6px 10px; }
      .codex-mux-win-update-button:hover, .codex-mux-win-update-button:focus-visible { background: #6e86f7; outline: none; }
      .codex-mux-win-update-button:disabled { cursor: wait; opacity: .65; }
      codex-mux-profile-picker, codex-mux-plugin-picker, codex-mux-reset-picker { display: block; }
      .codex-mux-win-surface { margin: 0 0 16px; border: 1px solid color-mix(in srgb, currentColor 16%, transparent); border-radius: 14px; background: color-mix(in srgb, currentColor 3%, transparent); color: var(--text-primary, var(--token-text-primary, inherit)); padding: 12px; }
      .codex-mux-win-surface-title { font-size: 14px; font-weight: 650; line-height: 19px; }
      .codex-mux-win-surface-description { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 17px; opacity: .85; }
      .codex-mux-win-avatar-stack { display: flex; align-items: center; min-height: 48px; margin-top: 12px; padding-left: 2px; }
      .codex-mux-win-avatar-button { position: relative; display: grid; width: 46px; height: 46px; margin-left: -11px; place-items: center; overflow: hidden; border: 3px solid var(--main-surface-background, var(--token-main-surface-background, #292929)); border-radius: 50%; background: transparent; cursor: pointer; transition: transform 120ms ease, z-index 120ms ease; }
      .codex-mux-win-avatar-button:first-child { margin-left: 0; }
      .codex-mux-win-avatar-button:hover, .codex-mux-win-avatar-button:focus-visible { z-index: 5 !important; outline: 2px solid #6e86f7; outline-offset: 2px; transform: scale(1.06); }
      .codex-mux-win-avatar-button .codex-mux-win-avatar { width: 40px; height: 40px; flex-basis: 40px; font-size: 12px; }
      .codex-mux-win-picker { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 11px; }
      .codex-mux-win-picker-option { display: flex; min-width: 0; align-items: center; gap: 8px; border: 1px solid color-mix(in srgb, currentColor 14%, transparent); border-radius: 10px; background: transparent; color: inherit; cursor: pointer; font: inherit; padding: 7px 9px; text-align: left; }
      .codex-mux-win-picker-option:hover, .codex-mux-win-picker-option:focus-visible { background: color-mix(in srgb, currentColor 8%, transparent); outline: none; }
      .codex-mux-win-picker-option[aria-pressed="true"] { border-color: color-mix(in srgb, #6e86f7 72%, transparent); background: color-mix(in srgb, #6e86f7 17%, transparent); }
      .codex-mux-win-picker-option .codex-mux-win-avatar { width: 26px; height: 26px; flex-basis: 26px; font-size: 9px; }
      .codex-mux-win-picker-copy { display: flex; min-width: 0; flex-direction: column; gap: 1px; }
      .codex-mux-win-picker-name { max-width: 230px; overflow: hidden; font-size: 12px; font-weight: 600; line-height: 16px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-picker-detail { color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 14px; opacity: .82; }
      .codex-mux-win-picker-empty { margin-top: 10px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 17px; opacity: .85; }
      .codex-mux-win-reset-surface { margin-top: 10px; }
      .codex-mux-win-reset-surface .codex-mux-win-surface-title { font-size: 12px; }
    `;
    document.head.append(style);
  }

  function interactiveRow(element) {
    return element.closest("button, [role='menuitem'], [role='button'], a") || element;
  }

  function labelMatches(value, labels, allowShortcut = false) {
    const text = normalize(value);
    return labels.some((label) => {
      if (text === label) return true;
      // The current Windows build folds a keyboard shortcut into the Settings
      // button text.  Keep the fallback narrowly bounded so an unrelated
      // settings page is not mistaken for the profile menu.
      return allowShortcut && text.startsWith(label) && text.length <= label.length + 24;
    });
  }

  function rowsForProfileLabel(labels) {
    const elements = [...document.querySelectorAll("body *")].filter(isVisible);
    const exact = elements.filter((element) => labelMatches(element.textContent, labels));
    const candidates = exact.length > 0
      ? exact
      : elements.filter((element) => {
        if (!element.matches("button, [role='menuitem'], [role='button'], a")) return false;
        return labelMatches(element.textContent, labels, true);
      });
    return [...new Set(candidates.map(interactiveRow).filter(isVisible))];
  }

  function isProfileMenuContainer(element) {
    const rect = element.getBoundingClientRect();
    const controls = element.querySelectorAll("button, [role='menuitem'], [role='button'], a").length;
    return rect.width > 120 && rect.width < 720 && rect.height > 50 && rect.height < 1400 && controls < 32;
  }

  function profileMenuPlacement() {
    const settingsRows = rowsForProfileLabel(PROFILE_SETTINGS_LABELS);
    const logoutRows = rowsForProfileLabel(PROFILE_LOGOUT_LABELS);
    for (const settings of settingsRows) {
      let ancestor = settings.parentElement;
      for (let depth = 0; ancestor && depth < 10; depth += 1, ancestor = ancestor.parentElement) {
        if (!isVisible(ancestor)) continue;
        if (isProfileMenuContainer(ancestor) && logoutRows.some((logout) => ancestor.contains(logout))) {
          return { parent: settings.parentElement, before: settings };
        }
      }
    }
    return null;
  }

  async function loadAccounts() {
    const result = await request("/accounts");
    latestAccounts = Array.isArray(result.accounts) ? result.accounts : [];
    return latestAccounts;
  }

  function surface(title, description, className = "") {
    const element = make("section", `codex-mux-win-surface ${className}`.trim());
    append(
      element,
      make("div", "codex-mux-win-surface-title", title),
      make("div", "codex-mux-win-surface-description", description),
    );
    return element;
  }

  function normalizeSelectedAccount(accounts, requested, fallback = "primary") {
    if (fallback == null) {
      return accounts.some((account) => account.id === requested) ? requested : null;
    }
    if (accounts.some((account) => account.id === requested)) return requested;
    if (accounts.some((account) => account.id === fallback)) return fallback;
    return accounts[0]?.id || null;
  }

  function scheduleSurfaceReload(host, message) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", message));
    window.setTimeout(() => window.location.reload(), 140);
  }

  function profilePicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getProfileAccountId(), null);
    if (selectedId !== getProfileAccountId()) setProfileAccountId(selectedId);
    const visible = selectedId
      ? accounts.filter((account) => account.id === selectedId)
      : accounts;
    const panel = surface(
      selectedId ? "Selected subscription profile" : "Combined profile statistics",
      selectedId
        ? "Select the photo again to return to the combined view."
        : `${accounts.length} connected subscriptions. Select a photo to view one account's statistics.`,
    );
    const stack = make("div", "codex-mux-win-avatar-stack");
    visible.forEach((account, index) => {
      const button = make("button", "codex-mux-win-avatar-button");
      button.type = "button";
      button.style.zIndex = String(index + 1);
      button.title = accountName(account);
      button.setAttribute(
        "aria-label",
        selectedId ? "Show combined profile statistics" : `Show ${accountName(account)} profile statistics`,
      );
      button.append(avatar(account));
      button.addEventListener("click", () => {
        const nextId = selectedId === account.id ? null : account.id;
        setProfileAccountId(nextId);
        scheduleSurfaceReload(
          host,
          nextId ? `Opening ${account.label}'s profile statistics…` : "Returning to combined profile statistics…",
        );
      });
      stack.append(button);
    });
    panel.append(stack);
    host.replaceChildren(panel);
  }

  function pickerButton(account, selected, detail, onSelect) {
    const button = make("button", "codex-mux-win-picker-option");
    button.type = "button";
    button.setAttribute("aria-pressed", String(selected));
    append(
      button,
      avatar(account),
      append(
        make("span", "codex-mux-win-picker-copy"),
        make("span", "codex-mux-win-picker-name", accountName(account)),
        make("span", "codex-mux-win-picker-detail", detail),
      ),
    );
    button.addEventListener("click", onSelect);
    return button;
  }

  function pluginPicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getPluginAccountId());
    if (selectedId !== getPluginAccountId()) setPluginAccountId(selectedId);
    const selected = accounts.find((account) => account.id === selectedId) || null;
    const panel = surface(
      "Plugin connections",
      selected
        ? `Plugin definitions stay shared. Apps, connection status, and OAuth below use ${selected.label}.`
        : "Plugin definitions stay shared. Choose a subscription for Apps, connection status, and OAuth.",
    );
    const options = make("div", "codex-mux-win-picker");
    accounts.forEach((account) => {
      options.append(pickerButton(
        account,
        account.id === selectedId,
        account.id === selectedId ? "Selected for Apps and MCP" : "Use this subscription",
        () => {
          if (account.id === selectedId) return;
          setPluginAccountId(account.id);
          scheduleSurfaceReload(host, `Switching plugin connections to ${account.label}…`);
        },
      ));
    });
    panel.append(options);
    host.replaceChildren(panel);
  }

  async function resetPicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getResetAccountId());
    if (selectedId !== getResetAccountId()) setResetAccountId(selectedId);
    const counts = await Promise.all(accounts.map(async (account) => {
      try {
        const result = await rateLimitResets(account.id);
        return [account.id, Math.max(0, Number(result.available_count || 0))];
      } catch {
        return [account.id, null];
      }
    }));
    if (!host.isConnected) return;
    const resetCounts = new Map(counts);
    const panel = surface(
      "Subscription",
      "The displayed balance and any reset are isolated to the selected subscription.",
      "codex-mux-win-reset-surface",
    );
    const options = make("div", "codex-mux-win-picker");
    accounts.forEach((account) => {
      const count = resetCounts.get(account.id);
      const detail = count == null
        ? "Resets unavailable"
        : count === 1
          ? "1 reset available"
          : `${count} resets available`;
      options.append(pickerButton(account, account.id === selectedId, detail, () => {
        if (account.id === selectedId) return;
        setResetAccountId(account.id);
        void mountResetPicker(host);
      }));
    });
    panel.append(options);
    host.replaceChildren(panel);
  }

  async function mountProfilePicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      profilePicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  async function mountPluginPicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      pluginPicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  async function mountResetPicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      // Force the native React reset hook to pick up the fresh account snapshot
      // even when the default account remains Primary.
      setResetAccountId(normalizeSelectedAccount(accounts, getResetAccountId()));
      await resetPicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  function defineSurfaceElement(name, mount) {
    if (customElements.get(name)) return;
    customElements.define(name, class extends HTMLElement {
      connectedCallback() {
        void mount(this);
      }
    });
  }

  function registerSurfaceElements() {
    defineSurfaceElement("codex-mux-profile-picker", mountProfilePicker);
    defineSurfaceElement("codex-mux-plugin-picker", mountPluginPicker);
    defineSurfaceElement("codex-mux-reset-picker", mountResetPicker);
  }

  function setMenuStatus(menu, message) {
    let status = menu.querySelector(".codex-mux-win-error");
    if (!message) {
      status?.remove();
      return;
    }
    if (!status) {
      status = make("div", "codex-mux-win-error");
      menu.append(status);
    }
    status.textContent = message;
  }

  let activeAccountManager = null;

  function closeAccountManager(expected = null) {
    if (expected && activeAccountManager !== expected) return false;
    const current = activeAccountManager;
    current?.backdrop?.remove();
    if (!expected || activeAccountManager === expected) activeAccountManager = null;
    return Boolean(current);
  }

  function showActionToast(title, detail, neutral = false) {
    dismissToast();
    const toast = make("section", `codex-mux-win-toast${neutral ? " codex-mux-win-toast-neutral" : ""}`);
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss notification");
    append(
      toast,
      make("div", "codex-mux-win-toast-icon", neutral ? "i" : "✓"),
      append(
        make("div", "codex-mux-win-toast-copy"),
        make("div", "codex-mux-win-toast-title", title),
        make("div", "codex-mux-win-toast-detail", detail),
      ),
      close,
    );
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function dismissUpdateToast() {
    const current = activeUpdateToast;
    if (!current) return false;
    current.element?.remove();
    activeUpdateToast = null;
    return true;
  }

  function showUpdateToast(update) {
    if (update?.available !== true || update?.installing === true) return;
    const version = String(update.version || "").trim();
    if (!version) return;
    if (activeUpdateToast?.version === version) return;
    dismissUpdateToast();
    const toast = make("section", "codex-mux-win-toast codex-mux-win-update-toast");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss update notification");
    const copy = make("div", "codex-mux-win-toast-copy");
    append(
      copy,
      make("div", "codex-mux-win-toast-title", `Codex Relay ${version} is available`),
      make("div", "codex-mux-win-toast-detail", update.notes || "Update now to download, restart, and reopen the Router automatically."),
    );
    const actions = make("div", "codex-mux-win-update-actions");
    const install = make("button", "codex-mux-win-update-button", "Update now");
    install.type = "button";
    install.addEventListener("click", async () => {
      const bridge = globalThis.codexMuxUpdater;
      if (bridge == null || typeof bridge.install !== "function" || install.disabled) return;
      install.disabled = true;
      install.textContent = "Preparing update…";
      const detail = copy.querySelector?.(".codex-mux-win-toast-detail");
      if (detail) detail.textContent = "The Router will close, install the verified release, and reopen automatically.";
      try {
        const result = await bridge.install();
        if (result?.installing !== true) {
          install.disabled = false;
          install.textContent = "Try again";
          if (detail) detail.textContent = result?.error || "The update could not be started. The current Router is still running.";
        }
      } catch (error) {
        install.disabled = false;
        install.textContent = "Try again";
        if (detail) detail.textContent = `The update could not be started: ${error.message}`;
      }
    });
    append(actions, install);
    copy.append(actions);
    append(toast, make("div", "codex-mux-win-toast-icon", "↑"), copy, close);
    document.body.append(toast);
    const notice = { element: toast, version };
    activeUpdateToast = notice;
    close.addEventListener("click", dismissUpdateToast);
  }

  function startUpdateWatcher() {
    if (updateWatcherStarted) return;
    updateWatcherStarted = true;
    const bridge = globalThis.codexMuxUpdater;
    if (bridge == null) return;
    const apply = (value) => {
      if (value?.available === true && value?.installing !== true) showUpdateToast(value);
    };
    if (typeof bridge.subscribe === "function") bridge.subscribe(apply);
    if (typeof bridge.getState === "function") void bridge.getState().then(apply).catch(() => {});
  }

  function accountRemovalConfirmed(account) {
    if (typeof window.confirm !== "function") return false;
    const chats = Number(account?.threadCount || 0);
    const historyWarning = chats > 0
      ? `\n\nThis subscription owns ${chats} chat${chats === 1 ? "" : "s"}. Removing it will clear Router's assignment for those chats; their local files are not deleted.`
      : "";
    return window.confirm(
      `Remove ${accountName(account)} from Codex Relay?${historyWarning}\n\nYou can add it again later by signing in.`
    );
  }

  function accountManagerStatus(state, message = "") {
    state.status.textContent = message;
    state.status.hidden = !message;
  }

  async function setPrimaryAccount(state, account, button) {
    if (button?.disabled || account?.controller) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Switching Primary to ${accountName(account)} and restarting Router sessions…`);
    try {
      const result = await request(`/accounts/${encodeURIComponent(account.id)}/primary`, { method: "POST" });
      const selected = result.account || account;
      const restarted = Number(result.restartedChildren);
      const restartText = Number.isFinite(restarted)
        ? `Restarted ${restarted} Router Codex session${restarted === 1 ? "" : "s"}.`
        : "Router Codex sessions were restarted.";
      showActionToast("Primary account changed", `${accountName(selected)} is now the Router Primary. ${restartText}`);
      const accounts = await loadAccounts();
      state.accounts = accounts;
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not change Primary: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function removeManagedAccount(state, account, button) {
    if (button?.disabled || account?.controller || !account?.id) return;
    if (!accountRemovalConfirmed(account)) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Removing ${accountName(account)}…`);
    try {
      await request(`/accounts/${encodeURIComponent(account.id)}`, {
        method: "DELETE",
        body: JSON.stringify({ force: true }),
      });
      showActionToast("Subscription removed", `${accountName(account)} was removed from Router. The native Codex app was not changed.`);
      const accounts = await loadAccounts();
      state.accounts = accounts;
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not remove subscription: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function cancelManagedPending(state, account, button) {
    if (button?.disabled || !account?.id) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Cancelling ${account.label || "subscription"} sign-in…`);
    try {
      const result = await cancelPendingAccount(account.id);
      if (result.connected) showLoginSuccess(result.account || account);
      else showLoginCancelled(account);
      state.accounts = await loadAccounts();
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not cancel sign-in: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  function renderAccountManager(state) {
    const accounts = Array.isArray(state.accounts) ? state.accounts : [];
    state.list.replaceChildren();
    if (accounts.length === 0) {
      state.list.append(make("div", "codex-mux-win-picker-empty", "No subscriptions are configured."));
      return;
    }
    accounts.forEach((account) => {
      const card = make("div", "codex-mux-win-account-card");
      const identity = make("div", "codex-mux-win-identity");
      const meta = make("div", "codex-mux-win-account-meta");
      const name = make("div", "codex-mux-win-name", accountName(account));
      if (account.controller) name.append(make("span", "codex-mux-win-account-badge", "Primary"));
      const identityDetail = accountIdentityDetail(account);
      const status = account.connected
        ? usageStatus(account)
        : "Waiting for sign-in";
      append(meta, name, identityDetail ? make("div", "codex-mux-win-account-id", identityDetail) : null, make("div", "codex-mux-win-account-hint", status));
      append(identity, avatar(account), meta);
      const actions = make("div", "codex-mux-win-account-card-actions");
      if (account.controller) {
        const primary = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Primary");
        primary.type = "button";
        primary.disabled = true;
        primary.title = "Choose another Primary before removing this account";
        actions.append(primary);
      } else if (account.connected) {
        const select = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Set as Primary");
        select.type = "button";
        select.addEventListener("click", () => { void setPrimaryAccount(state, account, select); });
        actions.append(select);
        const remove = make("button", "codex-mux-win-account-action codex-mux-win-account-action-danger", "Remove");
        remove.type = "button";
        remove.addEventListener("click", () => { void removeManagedAccount(state, account, remove); });
        actions.append(remove);
      } else {
        const cancel = make("button", "codex-mux-win-account-action", "Cancel sign-in");
        cancel.type = "button";
        cancel.addEventListener("click", () => { void cancelManagedPending(state, account, cancel); });
        actions.append(cancel);
      }
      append(card, identity, actions);
      state.list.append(card);
    });
    accountManagerStatus(state, "");
  }

  function openAccountSettings(menu, accounts) {
    closeAccountManager();
    const backdrop = make("div", "codex-mux-win-modal-backdrop");
    const dialog = make("section", "codex-mux-win-modal codex-mux-win-modal-manager");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    const header = make("div", "codex-mux-win-manager-header");
    append(header, make("h2", "", "Account settings"));
    const close = make("button", "codex-mux-win-toast-close codex-mux-win-close-button", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Close account settings");
    close.addEventListener("click", () => closeAccountManager(state));
    header.append(close);
    const description = make("p", "", "Choose which connected subscription is Primary for Router, or remove a subscription. Changing Primary restarts Router sessions automatically and does not change the native Codex app.");
    const list = make("div", "codex-mux-win-account-list");
    const status = make("div", "codex-mux-win-status");
    status.hidden = true;
    const state = { accounts: Array.isArray(accounts) ? accounts.slice() : [], backdrop, dialog, list, menu, status };
    append(dialog, header, description, list, status);
    backdrop.append(dialog);
    backdrop.addEventListener("click", (event) => {
      if (event.target === backdrop) closeAccountManager(state);
    });
    document.body.append(backdrop);
    activeAccountManager = state;
    renderAccountManager(state);
    return state;
  }

  function renderMenu(menu, accounts) {
    const priorError = menu.querySelector(".codex-mux-win-error")?.textContent || "";
    menu.replaceChildren();
    const connected = accounts.filter((account) => account.enabled && account.connected);
    const knownUsage = connected.map(remainingUsage).filter((value) => value != null);
    const total = knownUsage.length > 0
      ? knownUsage.reduce((sum, value) => sum + value, 0)
      : null;
    const missing = Math.max(0, connected.length - knownUsage.length);

    const summary = make("div", "codex-mux-win-summary");
    const icon = make("div", "codex-mux-win-summary-icon", "◔");
    const label = make("div", "codex-mux-win-summary-label");
    append(
      label,
      make("div", "codex-mux-win-title", "Usage remaining"),
      make("div", "codex-mux-win-subtext", missing > 0
        ? `${knownUsage.length}/${connected.length} quota${knownUsage.length === 1 ? "" : "s"} available · updating ${missing}`
        : `${connected.length} connected subscription${connected.length === 1 ? "" : "s"}`),
    );
    append(summary, icon, label, make("div", `codex-mux-win-total${total == null ? " codex-mux-win-percent-muted" : ""}`, total == null ? "Updating…" : `${Math.round(total)}% left`));
    menu.append(summary);

    if (accounts.length) menu.append(make("div", "codex-mux-win-divider"));
    accounts.forEach((account) => menu.append(row(account, (button) => {
      cancelPendingSubscription(menu, account, button).catch((error) => setMenuStatus(menu, error.message));
    })));

    const add = make("button", "codex-mux-win-add");
    add.type = "button";
    append(add, make("span", "codex-mux-win-plus", "+"), make("span", "", "Add another subscription"));
    add.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      startSubscription(menu, add, accounts).catch((error) => setMenuStatus(menu, error.message));
    });
    menu.append(make("div", "codex-mux-win-divider"), add);
    const settings = make("button", "codex-mux-win-add codex-mux-win-settings");
    settings.type = "button";
    append(settings, make("span", "codex-mux-win-settings-icon", "⚙"), make("span", "", "Account settings"));
    settings.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      openAccountSettings(menu, accounts);
    });
    menu.append(settings);
    if (priorError) setMenuStatus(menu, priorError);
  }

  function readLoginValue(login, ...keys) {
    for (const key of keys) {
      const value = login?.[key];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
    return "";
  }

  function trustedVerificationURL(value) {
    try {
      const url = new URL(value);
      if (url.protocol !== "https:" || !["chatgpt.com", "auth.openai.com"].includes(url.hostname)) return "";
      return url.href;
    } catch {
      return "";
    }
  }

  function loginBridge() {
    const bridge = globalThis.codexMuxLoginWindow;
    if (
      bridge == null ||
      typeof bridge.open !== "function" ||
      typeof bridge.close !== "function"
    ) {
      return null;
    }
    return bridge;
  }

  function closeLoginBridge(id) {
    const bridge = loginBridge();
    if (!id || bridge == null) return;
    void Promise.resolve(bridge.close(id)).catch(() => {
      // The child window can already be gone when login completion races close.
    });
  }

  function closeLogin(expected = null) {
    if (expected && activeLogin !== expected) return false;
    const current = activeLogin;
    current?.timer && clearTimeout(current.timer);
    current?.unsubscribeNativeClose?.();
    closeLoginBridge(current?.nativeLoginId);
    current?.backdrop?.remove();
    if (!expected || activeLogin === expected) activeLogin = null;
    return Boolean(current);
  }

  function dismissToast(expected = null) {
    if (expected && activeToast !== expected) return false;
    const current = activeToast;
    current?.timer && clearTimeout(current.timer);
    current?.element?.remove();
    if (!expected || activeToast === expected) activeToast = null;
    return Boolean(current);
  }

  function showLoginSuccess(account) {
    dismissToast();
    const toast = make("section", "codex-mux-win-toast");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss sign-in confirmation");
    const message = append(
      make("div", "codex-mux-win-toast-copy"),
      make("div", "codex-mux-win-toast-title", `${account.label || "Subscription"} connected successfully`),
      make("div", "codex-mux-win-toast-detail", "This subscription is ready to use. Official browser sign-in is complete."),
    );
    append(toast, make("div", "codex-mux-win-toast-icon", "✓"), message, close);
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function showLoginCancelled(account) {
    dismissToast();
    const toast = make("section", "codex-mux-win-toast codex-mux-win-toast-neutral");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss cancellation confirmation");
    const message = append(
      make("div", "codex-mux-win-toast-copy"),
      make("div", "codex-mux-win-toast-title", `${account.label || "Subscription"} sign-in cancelled`),
      make("div", "codex-mux-win-toast-detail", "The unfinished subscription was removed. Your primary account was not changed."),
    );
    append(toast, make("div", "codex-mux-win-toast-icon", "×"), message, close);
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function completeLoginOnce(session, account) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    session.completed = true;
    closeLogin(session);
    showLoginSuccess(account);
    schedule();
    return true;
  }

  async function cancelPendingAccount(accountId, loginId = "") {
    return request(`/accounts/${encodeURIComponent(accountId)}/login/cancel`, {
      method: "POST",
      body: JSON.stringify({ loginId: loginId || "" }),
    });
  }

  async function cancelPendingSubscription(menu, account, button = null) {
    if (!account?.id || button?.disabled) return;
    if (button) button.disabled = true;
    try {
      const result = await cancelPendingAccount(account.id);
      if (result.connected) {
        showLoginSuccess(result.account || account);
      } else {
        showLoginCancelled(account);
      }
      await refreshMenu(menu);
      return result;
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function cancelLogin(session, status, button) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    session.cancelling = true;
    session.timer && clearTimeout(session.timer);
    if (button) button.disabled = true;
    if (status) status.textContent = "Cancelling this unfinished subscription…";
    try {
      const result = await cancelPendingAccount(session.account.id, session.loginId);
      if (activeLogin !== session) return false;
      if (result.connected) {
        session.cancelling = false;
        return completeLoginOnce(session, result.account || session.account);
      }
      closeLogin(session);
      showLoginCancelled(session.account);
      if (session.menu) await refreshMenu(session.menu);
      return true;
    } catch (error) {
      if (activeLogin === session) {
        session.cancelling = false;
        if (button) button.disabled = false;
        if (status) status.textContent = `Could not cancel sign-in: ${error.message}`;
      }
      return false;
    }
  }

  async function openBrowserLogin(session, status, button) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    if (session.nativeLoginId && !session.externalBrowser) {
      status.textContent = "The official ChatGPT sign-in flow is already open.";
      return true;
    }
    const bridge = session.loginBridge;
    if (bridge == null) {
      throw new Error("The official ChatGPT sign-in bridge is unavailable. Run the Router installer again before adding an account.");
    }
    session.opening = true;
    if (button) button.disabled = true;
    status.textContent = "Opening the official ChatGPT sign-in page in your browser…";
    try {
      if (session.externalBrowser && session.nativeLoginId) {
        closeLoginBridge(session.nativeLoginId);
        session.nativeLoginId = null;
      }
      const opened = await bridge.open(session.authorizationURL);
      if (!opened?.id) throw new Error("The official ChatGPT sign-in bridge did not return a valid session.");
      if (activeLogin !== session || session.completed || session.cancelling) {
        closeLoginBridge(opened.id);
        return false;
      }
      session.nativeLoginId = opened.id;
      session.externalBrowser = opened.mode === "external";
      status.textContent = session.externalBrowser
        ? "The official sign-in page is open in your default browser. Finish there, then return here; Relay will close this confirmation when the account connects."
        : "Complete the official ChatGPT sign-in flow. This confirmation closes automatically when the account connects.";
      return true;
    } catch (error) {
      if (activeLogin === session) {
        status.textContent = `Could not open the official ChatGPT sign-in page: ${error.message}`;
      }
      throw error;
    } finally {
      session.opening = false;
      if (button) button.disabled = false;
    }
  }

  async function showBrowserLogin(menu, account, login) {
    closeLogin();
    const authorizationURL = trustedVerificationURL(readLoginValue(login, "authUrl", "auth_url"));
    const loginId = readLoginValue(login, "loginId", "login_id");
    if (!authorizationURL || !loginId) {
      throw new Error("The official ChatGPT sign-in link was not available. The unfinished subscription will be removed.");
    }
    const bridge = loginBridge();
    if (bridge == null) {
      throw new Error("The official ChatGPT sign-in bridge is unavailable. Run the Router installer again before adding an account.");
    }
    const backdrop = make("div", "codex-mux-win-modal-backdrop");
    const session = {
      account,
      authorizationURL,
      backdrop,
      cancelling: false,
      completed: false,
      loginId,
      menu,
      nativeLoginId: null,
      externalBrowser: false,
      opening: false,
      loginBridge: bridge,
      timer: null,
      unsubscribeNativeClose: null,
    };
    const dialog = make("section", "codex-mux-win-modal");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    append(
      dialog,
      make("h2", "", `Sign in to ${account.label || "subscription"}`),
      make("p", "", "Continue with the official ChatGPT sign-in page in your normal browser. Relay never asks for your password and the Codex child keeps this subscription isolated."),
    );
    dialog.append(make("p", "", "If the browser is already signed in to another ChatGPT account, choose the account switch option there. Keep this confirmation open while sign-in finishes; it closes automatically when Relay confirms the account."));
    const status = make("div", "codex-mux-win-status", "Preparing the official browser sign-in…");
    const actions = make("div", "codex-mux-win-actions");
    const open = make("button", "codex-mux-win-primary", "Open secure sign-in");
    open.type = "button";
    open.addEventListener("click", () => {
      void openBrowserLogin(session, status, open).catch(() => {
        // The status message already explains why the native window failed.
      });
    });
    const cancel = make("button", "", "Cancel sign-in");
    cancel.type = "button";
    cancel.addEventListener("click", () => { void cancelLogin(session, status, cancel); });
    append(actions, open, cancel);
    append(dialog, actions, status);
    backdrop.addEventListener("click", (event) => {
      if (event.target === backdrop) void cancelLogin(session, status, cancel);
    });
    backdrop.append(dialog);
    document.body.append(backdrop);
    activeLogin = session;
    if (typeof bridge.subscribeClosed === "function") {
      session.unsubscribeNativeClose = bridge.subscribeClosed((closed) => {
        if (activeLogin !== session || closed?.id !== session.nativeLoginId) return;
        session.nativeLoginId = null;
        if (!session.completed && !session.cancelling) {
          status.textContent = session.externalBrowser
            ? "The browser sign-in flow was closed by Relay. Open the official sign-in page again to continue, or cancel this unfinished subscription."
            : "The official ChatGPT sign-in flow was closed. Open it again to continue, or cancel this unfinished subscription.";
        }
      });
    }

    const poll = async () => {
      if (activeLogin !== session || session.completed || session.cancelling) return;
      try {
        const accounts = await loadAccounts();
        if (activeLogin !== session || session.completed) return;
        const updated = accounts.find((item) => item.id === account.id);
        if (updated?.connected) {
          completeLoginOnce(session, updated);
          return;
        }
      } catch {
        // A temporary control-service restart should not discard an official browser flow.
      }
      if (activeLogin === session && !session.completed) {
        session.timer = window.setTimeout(poll, 1500);
      }
    };
    void poll();
    try {
      await openBrowserLogin(session, status, open);
    } catch (error) {
      closeLogin(session);
      throw error;
    }
  }

  async function startSubscription(menu, button, currentAccounts) {
    if (button.disabled) return;
    button.disabled = true;
    const caption = button.lastElementChild;
    if (caption) caption.textContent = "Adding subscription…";
    setMenuStatus(menu, "");
    try {
      const created = await request("/accounts", {
        method: "POST",
        body: JSON.stringify({ label: `Subscription ${currentAccounts.length + 1}` }),
      });
      const account = created.account;
      if (!account?.id) throw new Error("Router did not return a new subscription ID");
      let result;
      try {
        result = await request(`/accounts/${encodeURIComponent(account.id)}/login`, {
          method: "POST",
          body: JSON.stringify({ mode: "chatgpt" }),
        });
      } catch (error) {
        // The account exists before the app-server begins browser login. Roll
        // it back best-effort so a start failure cannot create a ghost row.
        await cancelPendingAccount(account.id).catch(() => {});
        throw error;
      }
      try {
        await showBrowserLogin(menu, account, result.login || {});
      } catch (error) {
        await cancelPendingAccount(account.id, readLoginValue(result.login, "loginId", "login_id")).catch(() => {});
        throw error;
      }
      await refreshMenu(menu);
    } finally {
      button.disabled = false;
      if (caption) caption.textContent = "Add another subscription";
    }
  }

  async function refreshMenu(menu) {
    menu.dataset.codexMuxLastRefresh = String(Date.now());
    try {
      renderMenu(menu, await loadAccounts());
    } catch (error) {
      setMenuStatus(menu, `Router unavailable: ${error.message}`);
    }
  }

  function installIntoProfileMenu() {
    scheduled = false;
    const placement = profileMenuPlacement();
    if (!placement) return;
    let menu = document.getElementById(MENU_ID);
    let inserted = false;
    if (!menu || menu.parentElement !== placement.parent) {
      menu?.remove();
      menu = make("section", "", "");
      menu.id = MENU_ID;
      menu.setAttribute("aria-label", "Codex Relay");
      menu.addEventListener("pointerdown", (event) => event.stopPropagation());
      menu.addEventListener("click", (event) => event.stopPropagation());
      placement.parent.insertBefore(menu, placement.before);
      inserted = true;
    }
    const lastRefresh = Number(menu.dataset.codexMuxLastRefresh || 0);
    if (inserted || Date.now() - lastRefresh > 5000) void refreshMenu(menu);
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    window.setTimeout(installIntoProfileMenu, 50);
  }

  function start() {
    addStyles();
    startUpdateWatcher();
    new MutationObserver(schedule).observe(document.documentElement, { childList: true, subtree: true });
    window.setInterval(schedule, 1000);
    schedule();
  }

  // These are deliberately small, version-neutral renderer hooks. The
  // version-pinned bundle patches call them for profile data, per-account
  // Plugins RPC routing, and the native reset sheet. They do not expose a
  // password or a credential to the renderer.
  globalThis.CodexMuxWindows = {
    consumeRateLimitReset,
    getResetAccountId,
    profileData,
    rateLimitResets,
    scopePluginRequest,
    selectedResetUsageWindows,
    subscribeReset,
    usageStatus: nativeUsageStatus,
  };
  registerSurfaceElements();

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();

/*
 * Windows renderer bridge for Codex Subscription Router.
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
      .map((window) => ({
        usedPercent: Number(window.usedPercent || 0),
        remainingPercent: Math.max(0, 100 - Number(window.usedPercent || 0)),
        windowMinutes: Number(window.windowDurationMins || 0),
        resetsAt: window.resetsAt ?? null,
      }));
  }

  function selectedResetUsageWindows() {
    const account = connectedAccounts(latestAccounts).find(
      (item) => item.id === getResetAccountId(),
    );
    return account ? usageWindows(account.rateLimits) : null;
  }

  function longestUsageWindow(account) {
    const windows = [account?.rateLimits?.primary, account?.rateLimits?.secondary]
      .filter(Boolean)
      .sort((left, right) => (left.windowDurationMins || 0) - (right.windowDurationMins || 0));
    return windows.at(-1) || null;
  }

  function remainingUsage(account) {
    const usage = longestUsageWindow(account);
    return usage ? Math.max(0, 100 - Number(usage.usedPercent || 0)) : null;
  }

  function accountName(account) {
    return account.planLabel ? `${account.label} · ${account.planLabel}` : account.label || "Subscription";
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
        image.replaceWith(document.createTextNode(initials(account.label)));
      });
      shell.append(image);
    } else {
      shell.textContent = initials(account.label);
    }
    return shell;
  }

  function row(account, onCancelPending) {
    const usage = remainingUsage(account);
    const line = make("div", "codex-mux-win-row");
    const identity = make("div", "codex-mux-win-identity");
    const labels = make("div", "codex-mux-win-labels");
    const primary = make("div", "codex-mux-win-name", accountName(account));
    const secondary = make(
      "div",
      "codex-mux-win-subtext",
      account.connected ? "••••••••" : "Waiting for sign-in",
    );
    append(labels, primary, secondary);
    append(identity, avatar(account), labels);
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
      line.append(make("div", "codex-mux-win-percent", usage == null ? "–" : `${Math.round(usage)}%`));
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
      .codex-mux-win-total, .codex-mux-win-percent { margin-left: auto; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 14px; font-variant-numeric: tabular-nums; opacity: .82; }
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
      .codex-mux-win-error { margin: 4px 11px 8px; color: #e05a65; font-size: 12px; line-height: 16px; }
      .codex-mux-win-modal-backdrop { position: fixed; z-index: 2147483647; inset: 0; display: grid; place-items: center; padding: 24px; background: rgb(0 0 0 / .48); }
      .codex-mux-win-modal { width: min(420px, 100%); border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 16px; background: var(--main-surface-background, var(--token-main-surface-background, #292929)); box-shadow: 0 20px 60px rgb(0 0 0 / .42); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 20px; }
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

  function renderMenu(menu, accounts) {
    const priorError = menu.querySelector(".codex-mux-win-error")?.textContent || "";
    menu.replaceChildren();
    const connected = accounts.filter((account) => account.enabled && account.connected);
    const knownUsage = connected.map(remainingUsage).filter((value) => value != null);
    const total = knownUsage.length === connected.length && connected.length > 0
      ? knownUsage.reduce((sum, value) => sum + value, 0)
      : null;

    const summary = make("div", "codex-mux-win-summary");
    const icon = make("div", "codex-mux-win-summary-icon", "◔");
    const label = make("div", "codex-mux-win-summary-label");
    append(
      label,
      make("div", "codex-mux-win-title", "Usage remaining"),
      make("div", "codex-mux-win-subtext", `${connected.length} connected subscription${connected.length === 1 ? "" : "s"}`),
    );
    append(summary, icon, label, make("div", "codex-mux-win-total", total == null ? "–" : `${Math.round(total)}%`));
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

  function closeLogin(expected = null) {
    if (expected && activeLogin !== expected) return false;
    const current = activeLogin;
    current?.timer && clearTimeout(current.timer);
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
      make("div", "codex-mux-win-toast-detail", "This subscription is ready to use. Browser sign-in is complete."),
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

  function showBrowserLogin(menu, account, login) {
    closeLogin();
    const authorizationURL = trustedVerificationURL(readLoginValue(login, "authUrl"));
    const loginId = readLoginValue(login, "loginId", "login_id");
    if (!authorizationURL || !loginId) {
      throw new Error("The official browser sign-in link was not available. The unfinished subscription will be removed.");
    }
    const backdrop = make("div", "codex-mux-win-modal-backdrop");
    const session = {
      account,
      backdrop,
      cancelling: false,
      completed: false,
      loginId,
      menu,
      timer: null,
    };
    const dialog = make("section", "codex-mux-win-modal");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    append(
      dialog,
      make("h2", "", `Sign in to ${account.label || "subscription"}`),
      make("p", "", "Continue with the official ChatGPT sign-in page in your browser. This app never asks for your password and keeps this subscription isolated."),
    );
    dialog.append(make("p", "", "Keep this window open while sign-in finishes. It will close automatically when the account is connected."));
    const status = make("div", "codex-mux-win-status", "Opening the official ChatGPT sign-in page…");
    const actions = make("div", "codex-mux-win-actions");
    const open = make("a", "codex-mux-win-primary", "Continue to ChatGPT");
    open.href = authorizationURL;
    open.target = "_blank";
    open.rel = "noopener noreferrer";
    open.addEventListener("click", () => {
      if (!session.cancelling) status.textContent = "Waiting for the browser sign-in to finish…";
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
      const opened = typeof window.open === "function"
        ? window.open(authorizationURL, "_blank", "noopener,noreferrer")
        : null;
      status.textContent = opened
        ? "Waiting for the browser sign-in to finish…"
        : "Choose Continue to ChatGPT if your browser did not open automatically.";
    } catch {
      status.textContent = "Choose Continue to ChatGPT to open the official sign-in page.";
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
        showBrowserLogin(menu, account, result.login || {});
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
      menu.setAttribute("aria-label", "Codex Subscription Router");
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
  };
  registerSurfaceElements();

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();

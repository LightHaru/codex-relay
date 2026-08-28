const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

class FakeElement {
  constructor(tagName) {
    this.tagName = tagName;
    this.attributes = new Map();
    this.children = [];
    this.className = "";
    this.parentElement = null;
    this.removed = false;
    this.style = {};
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this.textContent = "";
    this.listeners = new Map();
  }

  append(...children) {
    for (const child of children) {
      if (child && typeof child === "object") child.parentElement = this;
      this.children.push(child);
    }
  }

  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }

  remove() {
    this.removed = true;
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((child) => child !== this);
    this.parentElement = null;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.get(name) || null;
  }

  addEventListener(type, listener) {
    if (typeof listener !== "function") return;
    const handlers = this.listeners.get(type) || [];
    handlers.push(listener);
    this.listeners.set(type, handlers);
  }

  async click() {
    const handlers = this.listeners.get("click") || [];
    for (const handler of handlers) await handler({ type: "click", target: this, currentTarget: this });
  }

  querySelector() { return null; }

  querySelectorAll() { return []; }

  get isConnected() { return !this.removed; }

  get lastElementChild() { return this.children.at(-1) || null; }

  get childElementCount() { return this.children.filter((child) => child && typeof child === "object").length; }

  get firstElementChild() { return this.children.find((child) => child && typeof child === "object") || null; }

  getBoundingClientRect() { return this.rect || { width: 0, height: 0 }; }

  getClientRects() { return [this.getBoundingClientRect()]; }

  closest() { return null; }
}

function loadBridge({ fetchImpl, privateLoginImpl, updaterImpl, eventSourceImpl, navigatorLanguage = "en-US", postMessageImpl } = {}) {
  const filename = path.join(__dirname, "windows-router-menu.js");
  const source = fs.readFileSync(filename, "utf8");
  const startupAnchor = '  if (document.readyState === "loading") {';
  const instrumented = source.replace(
    startupAnchor,
    [
      "  // Test-only instrumentation is injected by this Node VM; it is not in the production asset.",
      "  globalThis.__codexMuxWindowsTest = {",
      "    activateLogin: (session) => { activeLogin = session; },",
       "    completeLoginOnce,",
       "    cancelLogin,",
       "    dismissToast,",
       "    getActiveLogin: () => activeLogin,",
      "    getActiveToast: () => activeToast,",
       "    showUpdateToast,",
       "    dismissUpdateToast,",
       "    openBrowserLogin,",
       "    openAccountSettings,",
       "    renderAccountManager,",
       "    renderAccountResetSection,",
       "    consumeRateLimitReset,",
      "    renderMenu,",
      "    renderFallbackPoolTrigger,",
      "    toggleFallbackPoolMenu,",
      "    accountsForImmediatePaint,",
      "    seedAccountsCache: (accounts, fetchedAt) => { latestAccounts = accounts; latestAccountsFetchedAt = fetchedAt; },",
      "    openNativeSettingsFromFallback,",
      "    remainingUsage,",
      "    longestUsageWindow,",
      "    accountName,",
      "    accountIdentityDetail,",
      "    quotaResetSummary,",
      "    formatResetCountdown,",
      "    usageWindows,",
      "    usageBillingHeading,",
       "    renderUsageBillingSurface,",
		"    routingText,",
		"    startRoutingWatcher,",
		"    renderTaskRouteBadge,",
      "    setPrimaryAccount,",
      "    showBrowserLogin,",
      "  };",
      "",
      startupAnchor,
    ].join("\n"),
  );
  assert.notEqual(instrumented, source, "could not instrument the Windows bridge test hook");
  const document = {
    readyState: "loading",
    body: new FakeElement("body"),
    head: new FakeElement("head"),
		elementsById: new Map(),
    addEventListener() {},
    createElement(tagName) {
      return new FakeElement(tagName);
    },
		getElementById(id) {
			return this.elementsById.get(id) || null;
		},
  };
  const definitions = new Map();
  const storage = new Map();
  let nextTimer = 0;
  const timers = new Map();
  const fakeSetTimeout = (callback) => {
    nextTimer += 1;
    timers.set(nextTimer, callback);
    return nextTimer;
  };
  const fakeClearTimeout = (timer) => timers.delete(timer);
  const browserLogin = privateLoginImpl || {
    async open() {
      return { id: "browser-login-fixture", mode: "external" };
    },
    async close() {
      return true;
    },
    subscribeClosed() {
      return () => {};
    },
  };
  const context = {
    URL,
		URLSearchParams,
		EventSource: eventSourceImpl,
    fetch: fetchImpl || (async () => ({ ok: true, json: async () => ({ accounts: [] }) })),
    HTMLElement: FakeElement,
    getComputedStyle() { return { display: "block", visibility: "visible" }; },
    clearTimeout: fakeClearTimeout,
    customElements: {
      define(name, implementation) {
        definitions.set(name, implementation);
      },
      get(name) {
        return definitions.get(name);
      },
    },
    document,
		navigator: { language: navigatorLanguage },
    sessionStorage: {
      getItem(key) {
        return storage.get(key) || null;
      },
      removeItem(key) {
        storage.delete(key);
      },
      setItem(key, value) {
        storage.set(key, String(value));
      },
    },
    setTimeout: fakeSetTimeout,
    codexMuxLoginWindow: browserLogin,
    postMessage: postMessageImpl || (() => {}),
    codexMuxUpdater: updaterImpl || {
      async getState() { return { available: false }; },
      async install() { return { installing: false }; },
      subscribe() { return () => {}; },
    },
  };
  context.window = context;
  context.window.open = () => {
    throw new Error("The Router login flow must not call window.open");
  };
  context.window.confirm = () => false;
  vm.createContext(context);
  vm.runInContext(instrumented, context, { filename });
  return {
    bridge: context.CodexMuxWindows,
    document,
    hooks: context.__codexMuxWindowsTest,
    privateLogin: browserLogin,
    storage,
  };
}

test("browser login completion closes only its active dialog and shows one success toast", () => {
  const { document, hooks } = loadBridge();
  const session = { backdrop: new FakeElement("backdrop"), completed: false, timer: null };
  hooks.activateLogin(session);

  assert.equal(hooks.completeLoginOnce(session, { label: "Subscription 2" }), true);
  assert.equal(session.completed, true);
  assert.equal(session.backdrop.removed, true);
  assert.equal(hooks.getActiveLogin(), null);
  assert.equal(document.body.children.length, 1);
  assert.equal(document.body.children[0].className, "codex-mux-win-toast");
  assert.equal(document.body.children[0].getAttribute("role"), "status");

  assert.equal(hooks.completeLoginOnce(session, { label: "Subscription 2" }), false);
  assert.equal(document.body.children.length, 1, "a repeated poll must not duplicate the toast");
  hooks.dismissToast(hooks.getActiveToast());
});

test("native usage billing bridge exposes the all-subscription endpoint", async () => {
  const requests = [];
  const { bridge } = loadBridge({
    fetchImpl: async (url) => {
      requests.push(url);
      return { ok: true, json: async () => ({ accounts: [{ accountId: "primary" }] }) };
    },
  });
  const result = await bridge.usageStatusAll();
  assert.deepEqual(result.accounts, [{ accountId: "primary" }]);
  assert.equal(requests.some((url) => url.endsWith("/usage/all")), true);
});

test("Usage & billing renders one in-flow pool plus every account's quota details", async () => {
  const { hooks } = loadBridge({
    fetchImpl: async (url) => ({
      ok: true,
      json: async () => url.includes("rate-limit-resets")
        ? { available_count: 1, applicable_available_count: 1, credits: [{ id: "credit-1", status: "available", title: "Weekly reset" }] }
        : { accounts: [] },
    }),
  });
  const host = new FakeElement("host");
  hooks.renderUsageBillingSurface([
    { id: "primary", label: "Primary", displayName: "Bennett", enabled: true, connected: true, controller: true, planLabel: "Plus", rateLimits: { primary: { usedPercent: 17, windowDurationMins: 10080 } } },
    { id: "secondary", label: "Subscription 2", displayName: "Susan", enabled: true, connected: true, controller: false, planLabel: "Plus", rateLimits: { primary: { usedPercent: 100, windowDurationMins: 10080 } } },
  ], {
    availableCount: 2,
    accounts: [
      { accountId: "primary", connected: true, usage: { plan_type: "plus", credits: { balance: "0" } } },
      { accountId: "secondary", connected: true, usage: { plan_type: "plus", credits: { balance: "0" } } },
    ],
  }, host, { resetRenderVersion: 1 }, 1);
  const text = collectText(host);
  assert.match(text, /Codex Relay Pool/);
  assert.match(text, /One Relay task authority/);
  assert.match(text, /Confirmed remaining/);
  assert.match(text, /Quota-known sources/);
  assert.doesNotMatch(text, /Worker diagnostics/);
  assert.match(text, /Bennett/);
  assert.match(text, /Susan/);
  assert.match(text, /General usage limits/);
  assert.match(text, /Billing details/);
  await new Promise((resolve) => setImmediate(resolve));
  assert.match(collectText(host), /Weekly reset/);
});

test("Usage & billing shows a bounded account error instead of an exclamation-only state", () => {
  const { hooks } = loadBridge();
  const host = new FakeElement("host");
  hooks.renderUsageBillingSurface([
    { id: "primary", label: "Primary", displayName: "Aira", enabled: true, connected: true, controller: true, relayAuthority: true, rateLimits: {} },
    { id: "secondary", label: "Subscription 2", displayName: "Susan", enabled: true, connected: true, controller: false, rateLimits: {} },
  ], {
    availableCount: 1,
    failedCount: 1,
    accounts: [
      { accountId: "primary", connected: true, usage: { plan_type: "plus" } },
      { accountId: "secondary", connected: true, error: "fetch usage: status 429" },
    ],
  }, host, { resetRenderVersion: 1 }, 1, {
    status: { pool: { connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 100, knownSubscriptions: 1, unknownSubscriptions: 1, availableSubscriptions: 1, health: "degraded", lastError: { code: "upstream_http_error", httpStatus: 502, message: "Relay Pool model service rejected the request" } } },
  });
  const text = collectText(host);
  assert.match(text, /Susan/);
  assert.match(text, /Error/);
  assert.match(text, /fetch usage: status 429/);
  assert.match(text, /Last Relay request issue: Relay Pool model service rejected the request \(HTTP 502; code upstream_http_error\)/);
});

test("a stale browser-login poll cannot close a newer sign-in dialog", () => {
  const { hooks } = loadBridge();
  const stale = { backdrop: new FakeElement("stale"), completed: false, timer: null };
  const current = { backdrop: new FakeElement("current"), completed: false, timer: null };
  hooks.activateLogin(current);

  assert.equal(hooks.completeLoginOnce(stale, { label: "Old subscription" }), false);
  assert.equal(hooks.getActiveLogin(), current);
  assert.equal(current.backdrop.removed, false);
  assert.equal(current.completed, false);
});

test("official browser login opens only the trusted URL and cancellation closes its scoped flow", async () => {
  const requests = [];
  const opened = [];
  const closed = [];
  let closeListener = null;
  const { document, hooks } = loadBridge({
    fetchImpl: async (url, options = {}) => {
      requests.push({ url, options });
      return {
        ok: true,
        json: async () => (url.includes("/login/cancel") ? { canceled: true } : { accounts: [] }),
      };
    },
    privateLoginImpl: {
      async open(url) {
        opened.push(url);
        return { id: `browser-login-${opened.length + 1}`, mode: "external" };
      },
      async close(id) {
        closed.push(id);
        return true;
      },
      subscribeClosed(listener) {
        closeListener = listener;
        return () => {};
      },
    },
  });

  await hooks.showBrowserLogin(null, { id: "subscription-2", label: "Subscription 2" }, {
    auth_url: "https://chatgpt.com/codex/desktop-auth?state=example",
    login_id: "login-2",
  });
  const session = hooks.getActiveLogin();
  assert.ok(session, "browser login should create an active session");
  assert.deepEqual(opened, ["https://chatgpt.com/codex/desktop-auth?state=example"]);
  assert.equal(session.nativeLoginId, "browser-login-2");
  assert.equal(session.externalBrowser, true);
  assert.equal(document.body.children.length, 1);

  closeListener({ id: "browser-login-2", reason: "closed" });
  assert.equal(session.nativeLoginId, null, "a closed browser flow can be reopened");
  await hooks.openBrowserLogin(session, new FakeElement("status"), new FakeElement("button"));
  assert.deepEqual(opened, [
    "https://chatgpt.com/codex/desktop-auth?state=example",
    "https://chatgpt.com/codex/desktop-auth?state=example",
  ]);
  assert.equal(session.nativeLoginId, "browser-login-3");

  const status = new FakeElement("status");
  const button = new FakeElement("button");
  assert.equal(await hooks.cancelLogin(session, status, button), true);
  const cancellation = requests.find((entry) => entry.url.includes("/login/cancel"));
  assert.ok(cancellation, "cancel must call the local scoped cancellation endpoint");
  assert.match(cancellation.url, /accounts\/subscription-2\/login\/cancel$/);
  assert.deepEqual(JSON.parse(cancellation.options.body), { loginId: "login-2" });
  assert.deepEqual(closed, ["browser-login-3"]);
  assert.equal(session.backdrop.removed, true);
  assert.equal(hooks.getActiveLogin(), null);
});

test("Windows bridge uses official browser OAuth rather than a device code or arbitrary URL", () => {
  const source = fs.readFileSync(path.join(__dirname, "windows-router-menu.js"), "utf8");
  assert.match(source, /body: JSON\.stringify\(\{ mode: "chatgpt" \}\)/);
  assert.match(source, /Open secure sign-in/);
  assert.match(source, /codexMuxLoginWindow/);
  assert.match(source, /Cancel sign-in/);
  assert.match(source, /\/login\/cancel/);
  assert.doesNotMatch(source, /chatgptDeviceCode/);
  assert.doesNotMatch(source, /Copy code/);
  assert.doesNotMatch(source, /window\.open\(/);
});

test("Update now invokes the verified updater bridge and locks the button", async () => {
  let installCalls = 0;
  const { document, hooks } = loadBridge({
    updaterImpl: {
      async getState() { return { available: false }; },
      async install() {
        installCalls += 1;
        return { installing: true };
      },
      subscribe() { return () => {}; },
    },
  });
  hooks.showUpdateToast({
    available: true,
    version: "0.3.1",
    notes: "Verified source release",
  });
  const find = (element, predicate) => {
    if (predicate(element)) return element;
    for (const child of element.children) {
      if (!child || typeof child !== "object") continue;
      const match = find(child, predicate);
      if (match) return match;
    }
    return null;
  };
  const updateButton = find(document.body, (element) => element.className === "codex-mux-win-update-button");
  assert.ok(updateButton, "the update toast must contain an Update now button");
  await updateButton.click();
  assert.equal(installCalls, 1);
  assert.equal(updateButton.disabled, true);
  assert.equal(updateButton.textContent, "Preparing update…");
  hooks.dismissUpdateToast();
});

test("Plugins RPC routing adds the selected account marker without touching unrelated requests", () => {
  const { bridge, storage } = loadBridge();
  storage.set("codex-mux.windows.plugin-account", "subscription-2");
  const original = { forceRefresh: true };
  const scoped = bridge.scopePluginRequest("app/list", original);

  assert.deepEqual({ ...scoped }, { codexMuxAccountId: "subscription-2", forceRefresh: true });
  assert.deepEqual(original, { forceRefresh: true }, "the native request object must not be mutated");
  assert.equal(bridge.scopePluginRequest("thread/list", original), original);
});

test("version-pinned sendRequest hook records the current task for route status", () => {
  const { bridge, storage } = loadBridge();
  const threadId = "019fe645-f42f-7a20-8b2b-054f46c0af0a";
  const params = { threadId, model: "gpt-test" };
  assert.equal(bridge.scopePluginRequest("turn/start", params), params);
  assert.equal(storage.get("codex-mux.windows.current-thread"), threadId);
  bridge.scopePluginRequest("thread/start", { cwd: "C:\\fake" });
  assert.equal(storage.get("codex-mux.windows.current-thread"), undefined);
});

test("native Usage bridge reads only the token-protected local payload and fails closed", async () => {
  const requests = [];
  const { bridge } = loadBridge({
    fetchImpl: async (url, options = {}) => {
      requests.push({ url, options });
      return { ok: true, json: async () => ({ usage: { rate_limit: { limit_reached: false } } }) };
    },
  });
  const usage = await bridge.usageStatus();
  assert.deepEqual({ ...usage }, { rate_limit: { limit_reached: false } });
  assert.equal(requests.length, 1);
  assert.match(requests[0].url, /\/v1\/usage$/);
  assert.equal(requests[0].options.headers["X-Codex-Mux-Token"], "__CODEX_MUX_CONTROL_TOKEN__");

  const failed = loadBridge({
    fetchImpl: async () => ({ ok: false, json: async () => ({ error: "unavailable" }) }),
  });
  assert.deepEqual({ ...await failed.bridge.usageStatus() }, {});
});

test("Account settings renders Usage limit resets per subscription", () => {
  const { hooks } = loadBridge();
  const host = new FakeElement("div");
  host.append(hooks.renderAccountResetSection({ id: "subscription-2", connected: true }, {
    available_count: 2,
    applicable_available_count: 1,
    credits: [
      { id: "reset-1", status: "available", title: "Weekly reset", expires_at: "2099-01-02T03:04:05Z" },
      { id: "reset-2", status: "available", title: "Bonus reset", expires_at: "2099-02-03T04:05:06Z" },
    ],
  }));
  const text = collectText(host);
  assert.match(text, /Usage limit resets/);
  assert.match(text, /2 available · 1 applicable/);
  assert.match(text, /Weekly reset/);
  assert.match(text, /Bonus reset/);
  assert.match(text, /View all reset details/);
});

test("Usage limit resets exposes a guarded Use reset action and refreshes after redemption", async () => {
  const requests = [];
  let resetReads = 0;
  const { document, hooks } = loadBridge({
    fetchImpl: async (url, options = {}) => {
      requests.push({ url, options });
      if (url.endsWith("/rate-limit-resets")) {
        resetReads += 1;
        return {
          ok: true,
          json: async () => resetReads === 1
            ? {
              available_count: 1,
              applicable_available_count: 1,
              credits: [{ id: "reset-1", status: "available", title: "Full reset", expires_at: "2099-01-02T03:04:05Z" }],
            }
            : { available_count: 0, applicable_available_count: 0, credits: [] },
        };
      }
      return { ok: true, json: async () => ({ code: "reset", credit: { id: "reset-1" } }) };
    },
  });
  const host = new FakeElement("host");
  const state = { resetRenderVersion: 1 };
  const account = { id: "subscription-2", label: "Subscription 2", connected: true };
  const section = hooks.renderAccountResetSection(account, {
    available_count: 1,
    applicable_available_count: 1,
    credits: [{ id: "reset-1", status: "available", title: "Full reset", expires_at: "2099-01-02T03:04:05Z" }],
  }, "", {
    onUse: async (credit, button) => {
      button.disabled = true;
      await hooks.consumeRateLimitReset(account.id, {
        creditId: credit.id,
        redeemRequestId: "ui-test-redeem",
      });
    },
  });
  host.append(section);
  const find = (element, predicate) => {
    if (predicate(element)) return element;
    for (const child of element.children) {
      if (!child || typeof child !== "object") continue;
      const match = find(child, predicate);
      if (match) return match;
    }
    return null;
  };
  const use = find(section, (element) => element.className === "codex-mux-win-account-reset-use");
  assert.ok(use, "an available reset must expose a Use reset button");
  assert.equal(use.textContent, "Use reset");
  await use.click();
  const redemption = requests.find((entry) => entry.url.endsWith("/rate-limit-resets/consume"));
  assert.ok(redemption, "Use reset must call the scoped account endpoint");
  assert.deepEqual(JSON.parse(redemption.options.body), {
    creditId: "reset-1",
    redeemRequestId: "ui-test-redeem",
  });
  assert.equal(use.disabled, true, "the action remains locked while the redemption is in flight");
  assert.match(collectText(host), /Usage limit resets|Full reset/);
});

test("pending credential sources remain hidden from the public profile menu", () => {
  const { hooks } = loadBridge();
  const pending = new FakeElement("pending");
  hooks.renderMenu(pending, [{
    id: "pending", label: "Pending", enabled: true, connected: false, controller: false, pendingLogin: true,
  }]);
  assert.doesNotMatch(collectText(pending), /Waiting for sign-in/);
  assert.doesNotMatch(collectText(pending), /Cancel sign-in/);
  assert.match(collectText(pending), /Manage pool sources/);

  const stale = new FakeElement("stale");
  hooks.renderMenu(stale, [{
    id: "stale", label: "Stale", enabled: true, connected: false, controller: false, pendingLogin: false,
  }]);
  assert.doesNotMatch(collectText(stale), /Not connected/);
  assert.doesNotMatch(collectText(stale), /Cancel sign-in/);
});

test("Relay host remains visibly separate and cannot be removed from Account settings", () => {
  const { hooks } = loadBridge();
  const state = {
    accounts: [{ id: "primary", label: "Primary", connected: false, controller: false }],
    list: new FakeElement("list"),
    menu: new FakeElement("menu"),
    status: new FakeElement("status"),
  };
  hooks.renderAccountManager(state);
  const text = collectText(state.list);
  assert.match(text, /Relay host · separate from Codex/);
  assert.match(text, /Relay authority/);
  assert.doesNotMatch(text, /Remove/);
});

test("Manage pool sources uses a readable native-feeling modal with pool overview and accessible quota cards", async () => {
  const { document, hooks } = loadBridge({
    fetchImpl: async (url) => ({
      ok: true,
      json: async () => url.endsWith("/rate-limit-resets")
        ? { available_count: 1, applicable_available_count: 1, credits: [{ id: "reset-1", status: "available", title: "Weekly reset" }] }
        : { accounts: [] },
    }),
  });
  const accounts = [
    {
      id: "primary",
      displayName: "Agent Aira",
      email: "aira@example.invalid",
      planLabel: "Plus",
      enabled: true,
      connected: true,
      relayAuthority: true,
      rateLimits: {
        primary: { usedPercent: 12, windowDurationMins: 300 },
        secondary: { usedPercent: 31, windowDurationMins: 10080 },
      },
    },
    {
      id: "secondary",
      displayName: "Susan Jones",
      email: "susan@example.invalid",
      planLabel: "Plus",
      enabled: true,
      connected: true,
      rateLimits: { primary: { usedPercent: 100, windowDurationMins: 10080 } },
    },
  ];
  const state = hooks.openAccountSettings(new FakeElement("menu"), accounts);
  const dialog = state.dialog;
  assert.equal(document.body.children.length, 1);
  assert.match(dialog.className, /codex-mux-win-modal-manager/);
  assert.equal(dialog.getAttribute("role"), "dialog");
  assert.equal(dialog.getAttribute("aria-modal"), "true");
  assert.equal(dialog.getAttribute("tabindex"), "-1");
  assert.ok(dialog.getAttribute("aria-labelledby"));
  assert.ok(dialog.getAttribute("aria-describedby"));
  assert.equal(state.status.getAttribute("role"), "status");
  assert.equal(state.status.getAttribute("aria-live"), "polite");
  assert.equal(state.overview.children.length, 4, "the modal should expose pool summary cards and health note");
  assert.equal(state.list.children[0].className, "codex-mux-win-manager-list-heading");
  assert.equal(state.list.children.filter((child) => child?.tagName === "article").length, 2);
  const text = collectText(dialog);
  assert.match(text, /Codex Relay Pool/);
  assert.match(text, /Manage pool sources/);
  assert.match(text, /Pool quota/);
  assert.match(text, /Available now/);
  assert.match(text, /Pool sources/);
  assert.match(text, /Agent Aira/);
  assert.match(text, /Susan Jones/);
  assert.match(text, /Effective quota/);
  assert.match(text, /5h · 88% left/);
  assert.match(text, /1w · 69% left/);
  assert.match(text, /Use as authority/);
  assert.match(text, /Remove/);
  await new Promise((resolve) => setImmediate(resolve));
  assert.match(collectText(dialog), /Weekly reset/);
});

test("the Windows bridge keeps the native Usage & billing page and adds an in-flow Relay surface", () => {
  const source = fs.readFileSync(path.join(__dirname, "windows-router-menu.js"), "utf8");
  assert.match(source, /const USAGE_SURFACE_ID = "codex-mux-windows-usage-surface"/);
  assert.match(source, /nativeUsageStatusAll/);
  assert.match(source, /installUsageBillingSurface\(\);/);
  assert.match(source, /Usage & billing page/);
  assert.doesNotMatch(source, /#codex-mux-windows-usage-surface \{[^}]*position:\s*fixed/);
});

test("usage summary keeps known quota visible when another account has no quota data", () => {
  const { hooks } = loadBridge();
  const menu = new FakeElement("menu");
  hooks.renderMenu(menu, [
    {
      id: "primary",
      label: "Primary",
      planLabel: "Plus",
      enabled: true,
      connected: true,
      controller: true,
      rateLimits: { primary: { usedPercent: 3, windowDurationMins: 10080 } },
    },
    {
      id: "secondary",
      label: "Subscription 2",
      planLabel: "Plus",
      enabled: true,
      connected: true,
      controller: false,
      rateLimitAvailable: false,
      rateLimitError: "quota data is temporarily unavailable",
    },
  ]);
  const text = collectText(menu);
  assert.match(text, /97% \/ 200% left/);
  assert.match(text, /quota updating/);
  assert.doesNotMatch(text, /–/);
});

test("effective quota uses the tighter 5-hour or weekly window", () => {
  const { hooks } = loadBridge();
  const account = {
    id: "account-1",
    enabled: true,
    connected: true,
    rateLimits: {
      primary: { usedPercent: 92, windowDurationMins: 300 },
      secondary: { usedPercent: 32, windowDurationMins: 10080 },
    },
  };
  assert.equal(hooks.remainingUsage(account), 8);
  assert.equal(hooks.longestUsageWindow(account).remainingPercent, 68);

  const menu = new FakeElement("menu");
  hooks.renderMenu(menu, [account], null);
  assert.match(collectText(menu), /8% \/ 100% left/);
  assert.doesNotMatch(collectText(menu), /68% \/ 100% left/);
});

test("five fresh subscriptions are presented as one 500 percent quota pool", () => {
  const { hooks } = loadBridge();
  const menu = new FakeElement("menu");
  const accounts = Array.from({ length: 5 }, (_, index) => ({
    id: `account-${index + 1}`,
    label: `Subscription ${index + 1}`,
    enabled: true,
    connected: true,
    rateLimits: { primary: { usedPercent: 0, windowDurationMins: 10080 } },
  }));
  hooks.renderMenu(menu, accounts, {
    status: { pool: { connectedSubscriptions: 5, maximumPercent: 500, confirmedRemainingPercent: 500, knownSubscriptions: 5, unknownSubscriptions: 0, availableSubscriptions: 5 } },
  });
  const text = collectText(menu);
  assert.match(text, /Shared quota pool/);
  assert.match(text, /500% \/ 500% left/);
  assert.match(text, /5 subscriptions act as one pool/);
  assert.doesNotMatch(text, /Worker diagnostics/);
});

test("Vietnamese pooled quota copy is clear and additive", () => {
  const { hooks } = loadBridge({ navigatorLanguage: "vi-VN" });
  const menu = new FakeElement("menu");
  hooks.renderMenu(menu, [{ id: "one", enabled: true, connected: true }], {
    status: { pool: { connectedSubscriptions: 5, maximumPercent: 500, confirmedRemainingPercent: 173, knownSubscriptions: 5, unknownSubscriptions: 0, availableSubscriptions: 2 } },
  });
  const text = collectText(menu);
  assert.match(text, /Pool quota dùng chung/);
  assert.match(text, /173% \/ 500% còn lại/);
  assert.match(text, /5 tài khoản hợp thành một pool/);
});

test("profile menu exposes one Relay pool identity instead of worker routing", () => {
  const { hooks } = loadBridge();
  const menu = new FakeElement("menu");
  const accounts = [
    { id: "primary", label: "Controller Account", enabled: true, connected: true, controller: true },
    { id: "secondary", label: "Task Account", enabled: true, connected: true, controller: false },
  ];
  hooks.renderMenu(menu, accounts, {
    status: { policy: "balanced", nextCandidate: { accountId: "primary", label: "Controller Account", remainingPercent: 64, quotaKnown: true } },
    threadId: "019fe645-f42f-7a20-8b2b-054f46c0af0a",
    thread: { route: { accountId: "secondary", generation: 7, recoveryRequired: false } },
  });
  const text = collectText(menu);
  assert.match(text, /Shared quota pool/);
  assert.doesNotMatch(text, /Relay Controller/);
  assert.doesNotMatch(text, /Controller Account/);
  assert.doesNotMatch(text, /Current Task Route/);
  assert.doesNotMatch(text, /Task Account/);
  assert.doesNotMatch(text, /Next Candidate/);
  assert.doesNotMatch(text, /Rotate/);
});

test("Vietnamese profile menu keeps the one-pool public identity", () => {
	const { hooks } = loadBridge({ navigatorLanguage: "vi-VN" });
	const menu = new FakeElement("menu");
	hooks.renderMenu(menu, [
		{ id: "primary", label: "Điều khiển", enabled: true, connected: true, controller: true },
		{ id: "secondary", label: "Thực thi", enabled: true, connected: true, controller: false },
	], {
		status: { policy: "balanced", effectivePolicy: "balanced", handoffSupported: true },
		threadId: "019fe645-f42f-7a20-8b2b-054f46c0af0a",
		thread: { route: { accountId: "secondary", generation: 3 } },
	});
	const text = collectText(menu);
	assert.match(text, /Pool quota dùng chung/);
	assert.doesNotMatch(text, /Tài khoản điều khiển Relay/);
	assert.doesNotMatch(text, /Tài khoản chạy task hiện tại/);
	assert.doesNotMatch(text, /Cân bằng/);
});

test("handoff SSE refreshes the open route badge", async () => {
	let source;
	let statusReads = 0;
	class FakeEventSource {
		constructor(url) { this.url = url; source = this; }
	}
	const { document, hooks } = loadBridge({
		eventSourceImpl: FakeEventSource,
		fetchImpl: async (url) => {
			if (url.endsWith("/accounts")) return { ok: true, json: async () => ({ accounts: [] }) };
			if (url.endsWith("/router/status")) {
				statusReads += 1;
				return { ok: true, json: async () => ({ policy: "balanced", effectivePolicy: "balanced", handoffSupported: true }) };
			}
			return { ok: true, json: async () => ({ decisions: [] }) };
		},
	});
	const menu = new FakeElement("menu");
	document.elementsById.set("codex-mux-windows-menu", menu);
	hooks.startRoutingWatcher();
	assert.match(source.url, /\/events\?token=/);
	source.onmessage({ data: JSON.stringify({ type: "handoff-committed", threadId: "thread-1" }) });
	await new Promise((resolve) => setImmediate(resolve));
	assert.equal(statusReads, 1);
	assert.ok(Number(menu.dataset.codexMuxLastRefresh) > 0);
});

test("task-view route badge shows current worker, next candidate, policy, and handoff in normal flow", () => {
	const { hooks } = loadBridge();
	const badge = new FakeElement("section");
	hooks.renderTaskRouteBadge(badge, [
		{ id: "primary", label: "Agent Aira", planLabel: "Plus" },
		{ id: "secondary", label: "reo", planLabel: "Plus" },
	], {
		status: { policy: "balanced", effectivePolicy: "balanced", nextCandidate: { accountId: "primary", quotaKnown: true, remainingPercent: 100 } },
		thread: {
			route: { accountId: "secondary", generation: 6, recoveryRequired: false },
			handoffs: [{ sourceAccountId: "primary", targetAccountId: "secondary", phase: "COMMITTED" }],
		},
	});
	const text = collectText(badge);
	assert.match(text, /Running via/);
	assert.match(text, /reo · Plus · generation 6/);
	assert.match(text, /Mode/);
	assert.match(text, /balanced/);
	assert.match(text, /Next Candidate/);
	assert.match(text, /Agent Aira · Plus · 100% left/);
	assert.match(text, /Handoff/);
	assert.doesNotMatch(fs.readFileSync(path.join(__dirname, "windows-router-menu.js"), "utf8"), /#\$\{TASK_ROUTE_BADGE_ID\} \{[^}]*position:\s*fixed/);
});

test("v3 task badge exposes only the Relay Pool identity across credential failover", () => {
	const { hooks } = loadBridge();
	const badge = new FakeElement("section");
	hooks.renderTaskRouteBadge(badge, [
		{ id: "primary", label: "Agent Aira", enabled: true, connected: true },
		{ id: "secondary", label: "reo", enabled: true, connected: true },
	], {
		status: { contractVersion: 2, pool: { health: "healthy", connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 173, knownSubscriptions: 2, availableSubscriptions: 2 } },
		thread: { route: { accountId: "secondary", generation: 9, recoveryRequired: false }, pool: { health: "healthy", connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 173, knownSubscriptions: 2, availableSubscriptions: 2 } },
	});
	const text = collectText(badge);
	assert.match(text, /Running via Codex Relay Pool · generation 9/);
	assert.match(text, /173% \/ 200%/);
	assert.doesNotMatch(text, /Agent Aira/);
	assert.doesNotMatch(text, /reo/);
	assert.doesNotMatch(text, /Next Candidate|Handoff|worker/i);
});

test("task route inspector distinguishes owner, active worker, last quota worker, reasons, and timeline", () => {
	const { hooks } = loadBridge({ navigatorLanguage: "vi-VN" });
	const badge = new FakeElement("section");
	hooks.renderTaskRouteBadge(badge, [
		{ id: "primary", label: "Agent Aira", planLabel: "Plus", enabled: true, connected: true },
		{ id: "secondary", label: "reo", planLabel: "Plus", enabled: true, connected: true },
	], {
		status: { policy: "balanced", effectivePolicy: "balanced", pool: { connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 95, availableSubscriptions: 1 } },
		thread: {
			route: { accountId: "primary", generation: 7, recoveryRequired: false },
			currentOwner: { accountId: "primary", label: "Agent Aira", planLabel: "Plus" },
			activeWorker: { accountId: "secondary", label: "reo", planLabel: "Plus" },
			lastCompletedWorker: { accountId: "primary", label: "Agent Aira", planLabel: "Plus" },
			lastQuotaConsumingWorker: { accountId: "secondary", label: "reo", planLabel: "Plus" },
			previousWorker: { accountId: "primary", label: "Agent Aira", planLabel: "Plus" },
			requestedPolicy: "balanced", effectivePolicy: "balanced",
			nextCandidate: { accountId: "primary", quotaKnown: true, remainingPercent: 55 },
			workers: [
				{ accountId: "primary", label: "Agent Aira", planLabel: "Plus", quotaKnown: true, confirmedRemainingPercent: 55, reasonCode: "selected_highest_score", scoreComponents: { finalScore: 1.2 } },
				{ accountId: "secondary", label: "reo", planLabel: "Plus", quotaKnown: true, confirmedRemainingPercent: 40, reasonCode: "eligible_lower_score", scoreComponents: { finalScore: 0.8 } },
			],
			timeline: [{ id: "event-1", type: "handoff_committed", timestamp: Date.now(), targetAccountId: "secondary", reasonCode: "handoff_quota_exhausted" }],
			pool: { connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 95, availableSubscriptions: 1 },
			handoffs: [],
		},
	});
	const text = collectText(badge);
	assert.match(text, /Tài khoản đang sở hữu task Agent Aira · Plus/);
	assert.match(text, /Tài khoản thực thi hiện tại reo · Plus/);
	assert.match(text, /Quota gần nhất ghi nhận ở reo · Plus/);
	assert.match(text, /Được chọn: điểm khả dụng cao nhất/);
	assert.match(text, /Chuyển vì tài khoản trước hết quota/);
	assert.match(text, /95% Quota xác nhận còn lại \/ 200% Dung lượng tối đa/);
	assert.ok(badge.children.some((child) => child?.tagName === "details"), "inspector must use a keyboard-accessible native details control");
});

test("replayed handoff event shows only one session-scoped toast", async () => {
	let source;
	class FakeEventSource {
		constructor() { source = this; }
	}
	const { document, hooks } = loadBridge({
		eventSourceImpl: FakeEventSource,
		fetchImpl: async () => ({ ok: true, json: async () => ({ accounts: [], decisions: [] }) }),
	});
	hooks.startRoutingWatcher();
	const event = { id: "handoff-1:committed", type: "handoff_committed", previousAccountId: "primary", accountId: "secondary", data: { reasonCode: "handoff_quota_exhausted" } };
	source.onmessage({ data: JSON.stringify(event) });
	const first = hooks.getActiveToast();
	assert.ok(first, "first terminal handoff event should notify the user");
	const toastCount = document.body.children.length;
	source.onmessage({ data: JSON.stringify(event) });
	assert.equal(hooks.getActiveToast(), first);
  assert.equal(document.body.children.length, toastCount);
});

test("task recovery never becomes a global toast", () => {
  let source;
  class FakeEventSource {
    constructor() { source = this; }
  }
  const { hooks, storage } = loadBridge({
    eventSourceImpl: FakeEventSource,
    fetchImpl: async () => ({ ok: true, json: async () => ({ accounts: [], decisions: [] }) }),
  });
  hooks.startRoutingWatcher();
  const threadId = "019fe645-f42f-7a20-8b2b-054f46c0af0a";
  source.onmessage({ data: JSON.stringify({
    id: "recovery:hidden",
    type: "recovery-required",
    threadId,
    message: "A background task needs review",
  }) });
  assert.equal(hooks.getActiveToast(), null, "a blank/new chat must not show another task's recovery toast");

  storage.set("codex-mux.windows.current-thread", threadId);
  source.onmessage({ data: JSON.stringify({
    id: "recovery:visible",
    type: "recovery-required",
    threadId,
    message: "This open task needs review",
  }) });
  assert.equal(hooks.getActiveToast(), null, "the open task keeps recovery in its badge instead of a global toast");
});

test("independent Relay footer keeps account access when the native profile row is hidden", () => {
  const { hooks } = loadBridge();
  const trigger = new FakeElement("button");
  hooks.renderFallbackPoolTrigger(trigger, [
    { id: "authority", displayName: "Agent Aira", relayAuthority: true, controller: true, enabled: true, connected: true, rateLimits: { primary: { usedPercent: 10 }, secondary: { usedPercent: 20 } } },
    { id: "second", displayName: "Second", enabled: true, connected: true, rateLimits: { primary: { usedPercent: 0 }, secondary: { usedPercent: 0 } } },
  ], { status: { pool: { connectedSubscriptions: 2, maximumPercent: 200, confirmedRemainingPercent: 170 } } });
  const text = collectText(trigger);
  assert.match(text, /Agent Aira/);
  assert.doesNotMatch(text, /subscriptions/);
  assert.doesNotMatch(text, /170% left/);
  assert.match(trigger.getAttribute("aria-label"), /2 subscriptions/);
  assert.match(trigger.getAttribute("aria-label"), /170% left/);
  assert.equal(trigger.getAttribute("aria-label").includes("Open Codex Relay accounts"), true);
});

test("fallback profile popover keeps native-style usage settings and account management", () => {
  const { hooks } = loadBridge();
  const menu = new FakeElement("section");
  menu.dataset.codexMuxFallback = "true";
  hooks.renderMenu(menu, [
    { id: "authority", displayName: "Agent Aira", planLabel: "Plus", relayAuthority: true, controller: true, enabled: true, connected: true, rateLimits: { primary: { usedPercent: 10 }, secondary: { usedPercent: 20 } } },
  ], { status: { pool: { connectedSubscriptions: 1, maximumPercent: 100, confirmedRemainingPercent: 80 } } });
  const text = collectText(menu);
  assert.match(text, /Agent Aira · Plus/);
  assert.match(text, /Usage remaining/);
  assert.match(text, /Add another subscription/);
  assert.match(text, /Settings/);
  assert.match(text, /Manage pool sources/);
});

test("fallback profile Settings uses Codex's real route message without restoring a detached footer row", () => {
  const messages = [];
  const { hooks } = loadBridge({
    postMessageImpl(message, targetOrigin) {
      messages.push({ message, targetOrigin });
    },
  });

  hooks.openNativeSettingsFromFallback();

  assert.equal(messages.length, 1);
  assert.equal(messages[0].message.type, "navigate-to-route");
  assert.equal(messages[0].message.path, "/settings/general-settings");
  assert.equal(messages[0].targetOrigin, "*");
});

test("fallback profile menu paints immediately and stays within the sidebar width", async () => {
  const { document, hooks } = loadBridge({
    fetchImpl: async () => ({ ok: true, json: async () => ({ accounts: [] }) }),
  });
  const trigger = new FakeElement("button");
  trigger.rect = { left: 8, top: 990, width: 259, height: 40 };

  const refresh = hooks.toggleFallbackPoolMenu(trigger);
  const menu = document.body.children.at(-1);

  assert.ok(menu, "the menu shell must be appended synchronously");
  assert.equal(menu.style.left, "8px");
  assert.equal(menu.style.width, "259px");
  assert.match(collectText(menu), /Usage remaining/);
  assert.match(collectText(menu), /Add another subscription/);
  assert.match(collectText(menu), /Settings/);
  await refresh;
});

test("a cold popup never paints an expired quota before the live response", async () => {
  let accountReads = 0;
  const { document, hooks } = loadBridge({
    fetchImpl: async (url) => {
      if (url.endsWith("/accounts")) {
        accountReads += 1;
        return { ok: true, json: async () => ({ accounts: [{
          id: "authority", displayName: "Agent Aira", relayAuthority: true,
          enabled: true, connected: true,
          rateLimits: { primary: { usedPercent: 14, windowDurationMins: 10080 } },
        }] }) };
      }
      if (url.endsWith("/router/status")) return { ok: true, json: async () => ({}) };
      return { ok: true, json: async () => ({ decisions: [] }) };
    },
  });
  const trigger = new FakeElement("button");
  trigger.rect = { left: 8, top: 990, width: 259, height: 40 };
  hooks.seedAccountsCache([{
    id: "authority", displayName: "Agent Aira", relayAuthority: true,
    enabled: true, connected: true,
    rateLimits: { primary: { usedPercent: 0, windowDurationMins: 10080 } },
  }], Date.now() - 30_000);

  const refresh = hooks.toggleFallbackPoolMenu(trigger);
  const menu = document.body.children.at(-1);
  assert.match(collectText(menu), /Updating…/, "cold synchronous paint must not guess 100% quota");
  assert.doesNotMatch(collectText(menu), /100% \/ 100%/);
  await refresh;
  assert.match(collectText(menu), /86% \/ 100% left/);
  assert.equal(accountReads, 1);
});

test("account-updated SSE refreshes both the open popup and its footer trigger", async () => {
  let source;
  let usedPercent = 0;
  class FakeEventSource {
    constructor() { source = this; }
  }
  const { document, hooks } = loadBridge({
    eventSourceImpl: FakeEventSource,
    fetchImpl: async (url) => {
      if (url.endsWith("/accounts")) return { ok: true, json: async () => ({ accounts: [{
        id: "authority", displayName: "Agent Aira", relayAuthority: true,
        enabled: true, connected: true,
        rateLimits: { primary: { usedPercent, windowDurationMins: 10080 } },
      }] }) };
      if (url.endsWith("/router/status")) return { ok: true, json: async () => ({}) };
      return { ok: true, json: async () => ({ decisions: [] }) };
    },
  });
  const menu = new FakeElement("section");
  menu.dataset.codexMuxFallback = "true";
  const trigger = new FakeElement("button");
  document.elementsById.set("codex-mux-windows-account-popover", menu);
  document.elementsById.set("codex-mux-windows-account-trigger", trigger);
  hooks.startRoutingWatcher();

  usedPercent = 14;
  source.onmessage({ data: JSON.stringify({ type: "account-updated", accountId: "authority" }) });
  await new Promise((resolve) => setImmediate(resolve));

  assert.match(collectText(menu), /86% \/ 100% left/);
  assert.match(trigger.getAttribute("aria-label"), /86% left/);
});

test("Relay protocol errors stay in diagnostics and never create a global toast", () => {
  let source;
  class FakeEventSource {
    constructor() { source = this; }
  }
  const { document, hooks } = loadBridge({
    eventSourceImpl: FakeEventSource,
    fetchImpl: async () => ({ ok: true, json: async () => ({ accounts: [], decisions: [] }) }),
  });
  hooks.startRoutingWatcher();
  source.onmessage({ data: JSON.stringify({
    id: "router-error:1",
    type: "router-error",
    message: "Relay Pool has exhausted every usable quota source",
  }) });
  assert.equal(hooks.getActiveToast(), null, "Relay request errors must not create a desktop toast");
  assert.doesNotMatch(collectText(document.body), /Relay request failed/);
});

test("public profile menu hides credential-source identity and reset details", () => {
  const { hooks } = loadBridge();
  const resetSoon = Math.ceil((Date.now() + 90 * 60 * 1000) / 1000);
  const account = {
    id: "account-1",
    label: "Subscription 1",
    displayName: "Bennett",
    username: "bennett-user",
    email: "bennett@example.invalid",
    planLabel: "Pro 20x",
    enabled: true,
    connected: true,
    controller: true,
    rateLimits: {
      primary: { usedPercent: 20, windowDurationMins: 300, resetsAt: resetSoon },
    },
  };
  assert.match(hooks.accountName(account), /^Bennett · Pro 20x$/);
  assert.match(hooks.accountIdentityDetail(account), /bennett@example\.invalid/);
  assert.match(hooks.quotaResetSummary(account), /^Reset 5h: 1h/);
  const menu = new FakeElement("menu");
  hooks.renderMenu(menu, [account]);
  const text = collectText(menu);
  assert.doesNotMatch(text, /Bennett/);
  assert.doesNotMatch(text, /bennett@example\.invalid/);
  assert.doesNotMatch(text, /Reset 5h: 1h/);
  assert.match(text, /Shared quota pool/);
});

test("account settings Primary action calls the independent Router endpoint", async () => {
  const requests = [];
  const accounts = [
    { id: "primary", label: "Original", planLabel: "Plus", enabled: true, connected: true, controller: true },
    { id: "secondary", label: "Work", planLabel: "Plus", enabled: true, connected: true, controller: false },
  ];
  const { document, hooks } = loadBridge({
    fetchImpl: async (url, options = {}) => {
      requests.push({ url, options });
      return { ok: true, json: async () => url.endsWith("/accounts")
        ? { accounts }
        : { account: { ...accounts[1], controller: true }, restartedChildren: 2 } };
    },
  });
  const state = {
    accounts,
    list: new FakeElement("list"),
    menu: new FakeElement("menu"),
    status: new FakeElement("status"),
  };
  const button = new FakeElement("button");
  await hooks.setPrimaryAccount(state, accounts[1], button);
  const primaryRequest = requests.find((entry) => entry.url.endsWith("/accounts/secondary/primary"));
  assert.ok(primaryRequest, "Primary selection must use the Router account endpoint");
  assert.equal(primaryRequest.options.method, "POST");
  assert.equal(state.status.hidden, true);
  assert.match(collectText(document.body), /Restarted 2 Router Codex sessions/);
});

function collectText(element) {
  const own = typeof element.textContent === "string" ? element.textContent : "";
  return own + element.children.map((child) => child && typeof child === "object" ? collectText(child) : String(child || "")).join(" ");
}

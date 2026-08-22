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

function loadBridge({ fetchImpl, privateLoginImpl, updaterImpl } = {}) {
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
      "    remainingUsage,",
      "    accountName,",
      "    accountIdentityDetail,",
      "    quotaResetSummary,",
      "    formatResetCountdown,",
      "    usageWindows,",
      "    usageBillingHeading,",
      "    renderUsageBillingSurface,",
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
    addEventListener() {},
    createElement(tagName) {
      return new FakeElement(tagName);
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

test("Usage & billing renders one in-flow card for every enabled subscription", async () => {
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
  assert.match(text, /All connected subscriptions/);
  assert.match(text, /Bennett/);
  assert.match(text, /Susan/);
  assert.match(text, /General usage limits/);
  await new Promise((resolve) => setImmediate(resolve));
  assert.match(collectText(host), /Weekly reset/);
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

test("Disconnected accounts only show Cancel sign-in when the Router persisted a pending flow", () => {
  const { hooks } = loadBridge();
  const pending = new FakeElement("pending");
  hooks.renderMenu(pending, [{
    id: "pending", label: "Pending", enabled: true, connected: false, controller: false, pendingLogin: true,
  }]);
  assert.match(collectText(pending), /Waiting for sign-in/);
  assert.match(collectText(pending), /Cancel sign-in/);

  const stale = new FakeElement("stale");
  hooks.renderMenu(stale, [{
    id: "stale", label: "Stale", enabled: true, connected: false, controller: false, pendingLogin: false,
  }]);
  assert.match(collectText(stale), /Not connected/);
  assert.doesNotMatch(collectText(stale), /Cancel sign-in/);
});

test("Relay primary remains visibly separate and cannot be removed from Account settings", () => {
  const { hooks } = loadBridge();
  const state = {
    accounts: [{ id: "primary", label: "Primary", connected: false, controller: false }],
    list: new FakeElement("list"),
    menu: new FakeElement("menu"),
    status: new FakeElement("status"),
  };
  hooks.renderAccountManager(state);
  const text = collectText(state.list);
  assert.match(text, /Relay primary · separate from Codex/);
  assert.match(text, /Relay only/);
  assert.doesNotMatch(text, /Remove/);
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
  assert.match(text, /97% left/);
  assert.match(text, /Updating quota|Quota unavailable/);
  assert.doesNotMatch(text, /–/);
});

test("account rows show the real profile identity and each quota window reset", () => {
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
  assert.match(text, /Bennett/);
  assert.match(text, /bennett@example\.invalid/);
  assert.match(text, /Reset 5h: 1h/);
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

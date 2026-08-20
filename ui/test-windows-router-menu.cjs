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

  addEventListener() {}

  querySelector() { return null; }

  querySelectorAll() { return []; }

  get isConnected() { return !this.removed; }

  get lastElementChild() { return this.children.at(-1) || null; }
}

function loadBridge({ fetchImpl, privateLoginImpl } = {}) {
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
      "    openPrivateLoginWindow,",
      "    openAccountSettings,",
      "    renderMenu,",
      "    remainingUsage,",
      "    accountName,",
      "    accountIdentityDetail,",
      "    quotaResetSummary,",
      "    formatResetCountdown,",
      "    usageWindows,",
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
  const privateLogin = privateLoginImpl || {
    async open() {
      return { id: "private-login-fixture" };
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
    codexMuxLoginWindow: privateLogin,
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
    privateLogin,
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

test("private login opens only the trusted official URL and cancellation closes its scoped child", async () => {
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
        return { id: `private-login-${opened.length + 1}` };
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
    authUrl: "https://chatgpt.com/codex/desktop-auth?state=example",
    loginId: "login-2",
  });
  const session = hooks.getActiveLogin();
  assert.ok(session, "private login should create an active session");
  assert.deepEqual(opened, ["https://chatgpt.com/codex/desktop-auth?state=example"]);
  assert.equal(session.nativeLoginId, "private-login-2");
  assert.equal(document.body.children.length, 1);

  closeListener({ id: "private-login-2", reason: "closed" });
  assert.equal(session.nativeLoginId, null, "a manually closed private child can be reopened");
  await hooks.openPrivateLoginWindow(session, new FakeElement("status"), new FakeElement("button"));
  assert.deepEqual(opened, [
    "https://chatgpt.com/codex/desktop-auth?state=example",
    "https://chatgpt.com/codex/desktop-auth?state=example",
  ]);
  assert.equal(session.nativeLoginId, "private-login-3");

  const status = new FakeElement("status");
  const button = new FakeElement("button");
  assert.equal(await hooks.cancelLogin(session, status, button), true);
  const cancellation = requests.find((entry) => entry.url.includes("/login/cancel"));
  assert.ok(cancellation, "cancel must call the local scoped cancellation endpoint");
  assert.match(cancellation.url, /accounts\/subscription-2\/login\/cancel$/);
  assert.deepEqual(JSON.parse(cancellation.options.body), { loginId: "login-2" });
  assert.deepEqual(closed, ["private-login-3"]);
  assert.equal(session.backdrop.removed, true);
  assert.equal(hooks.getActiveLogin(), null);
});

test("Windows bridge uses official private login rather than a device code or default browser", () => {
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

test("Plugins RPC routing adds the selected account marker without touching unrelated requests", () => {
  const { bridge, storage } = loadBridge();
  storage.set("codex-mux.windows.plugin-account", "subscription-2");
  const original = { forceRefresh: true };
  const scoped = bridge.scopePluginRequest("app/list", original);

  assert.deepEqual({ ...scoped }, { codexMuxAccountId: "subscription-2", forceRefresh: true });
  assert.deepEqual(original, { forceRefresh: true }, "the native request object must not be mutated");
  assert.equal(bridge.scopePluginRequest("thread/list", original), original);
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

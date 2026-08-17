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
}

function loadBridge({ fetchImpl, openImpl } = {}) {
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
    };
    context.window = context;
    context.window.open = openImpl || (() => null);
  vm.createContext(context);
  vm.runInContext(instrumented, context, { filename });
  return {
    bridge: context.CodexMuxWindows,
    document,
    hooks: context.__codexMuxWindowsTest,
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

test("browser login opens only the trusted official URL and cancellation calls the scoped endpoint", async () => {
  const requests = [];
  const opened = [];
  const { document, hooks } = loadBridge({
    fetchImpl: async (url, options = {}) => {
      requests.push({ url, options });
      return {
        ok: true,
        json: async () => (url.includes("/login/cancel") ? { canceled: true } : { accounts: [] }),
      };
    },
    openImpl: (url, target, features) => {
      opened.push({ url, target, features });
      return { closed: false };
    },
  });

  hooks.showBrowserLogin(null, { id: "subscription-2", label: "Subscription 2" }, {
    authUrl: "https://chatgpt.com/codex/desktop-auth?state=example",
    loginId: "login-2",
  });
  const session = hooks.getActiveLogin();
  assert.ok(session, "browser login should create an active session");
  assert.deepEqual(opened, [{
    url: "https://chatgpt.com/codex/desktop-auth?state=example",
    target: "_blank",
    features: "noopener,noreferrer",
  }]);
  assert.equal(document.body.children.length, 1);

  const status = new FakeElement("status");
  const button = new FakeElement("button");
  assert.equal(await hooks.cancelLogin(session, status, button), true);
  const cancellation = requests.find((entry) => entry.url.includes("/login/cancel"));
  assert.ok(cancellation, "cancel must call the local scoped cancellation endpoint");
  assert.match(cancellation.url, /accounts\/subscription-2\/login\/cancel$/);
  assert.deepEqual(JSON.parse(cancellation.options.body), { loginId: "login-2" });
  assert.equal(session.backdrop.removed, true);
  assert.equal(hooks.getActiveLogin(), null);
});

test("Windows bridge uses official browser login rather than a device code", () => {
  const source = fs.readFileSync(path.join(__dirname, "windows-router-menu.js"), "utf8");
  assert.match(source, /body: JSON\.stringify\(\{ mode: "chatgpt" \}\)/);
  assert.match(source, /Continue to ChatGPT/);
  assert.match(source, /Cancel sign-in/);
  assert.match(source, /\/login\/cancel/);
  assert.doesNotMatch(source, /chatgptDeviceCode/);
  assert.doesNotMatch(source, /Copy code/);
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

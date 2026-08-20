const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

class FakeWebContents {
  constructor(owner) {
    this.owner = owner;
    this.destroyed = false;
    this.handlers = new Map();
    this.sent = [];
    this.url = "about:blank";
    this.windowOpenHandler = null;
  }

  getURL() {
    return this.url;
  }

  isDestroyed() {
    return this.destroyed;
  }

  on(name, listener) {
    const listeners = this.handlers.get(name) || [];
    listeners.push(listener);
    this.handlers.set(name, listeners);
  }

  emit(name, ...args) {
    for (const listener of this.handlers.get(name) || []) listener(...args);
  }

  send(channel, payload) {
    this.sent.push({ channel, payload });
  }

  setWindowOpenHandler(handler) {
    this.windowOpenHandler = handler;
  }
}

function createHarness() {
  const handlers = new Map();
  const sessions = new Map();
  const windows = [];
  const UUIDS = [
    "11111111-1111-4111-8111-111111111111",
    "22222222-2222-4222-8222-222222222222",
    "33333333-3333-4333-8333-333333333333",
    "44444444-4444-4444-8444-444444444444",
  ];
  let nextUUID = 0;

  class FakeWindow {
    constructor(options) {
      this.options = options;
      this.destroyed = false;
      this.handlers = new Map();
      this.webContents = new FakeWebContents(this);
      windows.push(this);
    }

    static fromWebContents(contents) {
      return contents.owner || null;
    }

    isDestroyed() {
      return this.destroyed;
    }

    on(name, listener) {
      const listeners = this.handlers.get(name) || [];
      listeners.push(listener);
      this.handlers.set(name, listeners);
    }

    once(name, listener) {
      const once = (...args) => {
        this.handlers.set(name, (this.handlers.get(name) || []).filter((candidate) => candidate !== once));
        listener(...args);
      };
      this.on(name, once);
    }

    emit(name, ...args) {
      for (const listener of [...(this.handlers.get(name) || [])]) listener(...args);
    }

    setMenuBarVisibility() {}

    removeMenu() {}

    show() {}

    focus() {}

    loadURL(url) {
      this.webContents.url = url;
      return Promise.resolve();
    }

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      this.webContents.destroyed = true;
      this.emit("closed");
    }
  }

  const electron = {
    BrowserWindow: FakeWindow,
    ipcMain: {
      handle(channel, handler) {
        handlers.set(channel, handler);
      },
    },
    session: {
      fromPartition(partition) {
        if (sessions.has(partition)) return sessions.get(partition);
        const value = {
          calls: [],
          on(name, listener) {
            this.calls.push({ name, type: "on" });
            this.downloadListener = listener;
          },
          setPermissionCheckHandler(listener) {
            this.permissionCheck = listener;
          },
          setPermissionRequestHandler(listener) {
            this.permissionRequest = listener;
          },
          clearStorageData() {
            this.calls.push({ name: "clearStorageData" });
            return Promise.resolve();
          },
          clearCache() {
            this.calls.push({ name: "clearCache" });
            return Promise.resolve();
          },
          clearAuthCache() {
            this.calls.push({ name: "clearAuthCache" });
            return Promise.resolve();
          },
        };
        sessions.set(partition, value);
        return value;
      },
    },
  };

  const source = fs.readFileSync(path.join(__dirname, "windows-router-login-main.js"), "utf8");
  const instrumented = source.replace(
    "  ipcMain.handle(OPEN_CHANNEL, (event, value) => {",
    "  globalThis.__codexMuxPrivateLoginTest = { trustedOwner, verifiedInitialURL };\n\n  ipcMain.handle(OPEN_CHANNEL, (event, value) => {",
  );
  assert.notEqual(instrumented, source, "could not instrument the private-login main bridge");
  const context = {
    URL,
    Promise,
    Map,
    Set,
    RegExp,
    Object,
    String,
    require(name) {
      if (name === "electron") return electron;
      if (name === "node:crypto") return { randomUUID: () => UUIDS[nextUUID++] };
      throw new Error(`Unexpected module requested by private-login bridge: ${name}`);
    },
  };
  vm.createContext(context);
  vm.runInContext(instrumented, context, { filename: "windows-router-login-main.js" });

  const ownerWindow = {
    destroyed: false,
    isDestroyed() {
      return this.destroyed;
    },
  };
  const sender = new FakeWebContents(ownerWindow);
  sender.url = "file:///C:/Router/resources/app.asar/webview/index.html";
  return {
    close: handlers.get("codex-mux:close-isolated-login"),
    open: handlers.get("codex-mux:open-isolated-login"),
    probe: context.__codexMuxPrivateLoginTest,
    sender,
    sessions,
    windows,
  };
}

test("private login main bridge creates a fresh locked-down session for every official login", async () => {
  const harness = createHarness();
  const event = { sender: harness.sender };

  assert.ok(harness.probe.trustedOwner(event));
  assert.equal(
    harness.probe.verifiedInitialURL("https://chatgpt.com/codex/desktop-auth?state=first"),
    "https://chatgpt.com/codex/desktop-auth?state=first",
  );

  assert.throws(
    () => harness.open(event, "https://example.invalid/sign-in"),
    /private Router sign-in window could not be opened/,
  );
  assert.equal(harness.windows.length, 0);

  const first = await harness.open(event, "https://chatgpt.com/codex/desktop-auth?state=first");
  assert.match(first.id, /^[0-9a-f-]{36}$/i);
  assert.equal(harness.windows.length, 1);
  const firstWindow = harness.windows[0];
  const preferences = firstWindow.options.webPreferences;
  assert.match(preferences.partition, /^codex-mux-login-/);
  assert.doesNotMatch(preferences.partition, /^persist:/);
  assert.equal(preferences.contextIsolation, true);
  assert.equal(preferences.nodeIntegration, false);
  assert.equal(preferences.nodeIntegrationInSubFrames, false);
  assert.equal(preferences.nodeIntegrationInWorker, false);
  assert.equal(preferences.sandbox, true);
  assert.equal(preferences.webSecurity, true);
  assert.equal(preferences.webviewTag, false);
  assert.equal(preferences.devTools, false);
  assert.equal(Object.hasOwn(preferences, "preload"), false);

  const firstSession = harness.sessions.get(preferences.partition);
  assert.equal(firstSession.permissionCheck(), false);
  let permissionResult = null;
  firstSession.permissionRequest(null, "notifications", (value) => { permissionResult = value; });
  assert.equal(permissionResult, false);
  assert.ok(firstSession.downloadListener, "downloads must be blocked for the private login session");

  const popup = firstWindow.webContents.windowOpenHandler({ url: "https://accounts.google.com/o/oauth2/auth" });
  assert.equal(popup.action, "allow");
  assert.equal(popup.overrideBrowserWindowOptions.webPreferences.partition, preferences.partition);
  assert.equal(popup.overrideBrowserWindowOptions.webPreferences.nodeIntegration, false);
  assert.equal(firstWindow.webContents.windowOpenHandler({ url: "mailto:test@example.com" }).action, "deny");

  let unsafeNavigationBlocked = false;
  firstWindow.webContents.emit("will-navigate", {
    preventDefault() {
      unsafeNavigationBlocked = true;
    },
  }, "file:///C:/not-an-auth-page.html");
  assert.equal(unsafeNavigationBlocked, true);

  assert.equal(await harness.close(event, first.id), true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(firstWindow.destroyed, true);
  assert.deepEqual(
    firstSession.calls.filter((call) => call.type !== "on").map((call) => call.name).sort(),
    ["clearAuthCache", "clearCache", "clearStorageData"],
  );

  const second = await harness.open(event, "https://auth.openai.com/authorize?state=second");
  assert.notEqual(second.id, first.id);
  assert.equal(harness.windows.length, 2);
  assert.notEqual(harness.windows[1].options.webPreferences.partition, preferences.partition);
});

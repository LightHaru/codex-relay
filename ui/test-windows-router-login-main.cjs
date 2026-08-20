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
  const windows = [];
  const externalURLs = [];
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
    shell: {
      async openExternal(url) {
        externalURLs.push(url);
      },
    },
    ipcMain: {
      handle(channel, handler) {
        handlers.set(channel, handler);
      },
    },
  };

  const source = fs.readFileSync(path.join(__dirname, "windows-router-login-main.js"), "utf8");
  const instrumented = source.replace(
    "  ipcMain.handle(OPEN_CHANNEL, async (event, value) => {",
    "  globalThis.__codexMuxPrivateLoginTest = { trustedOwner, verifiedInitialURL, isFlowID };\n\n  ipcMain.handle(OPEN_CHANNEL, async (event, value) => {",
  );
  assert.notEqual(instrumented, source, "could not instrument the browser-login main bridge");
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
      throw new Error(`Unexpected module requested by browser-login bridge: ${name}`);
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
    externalURLs,
    windows,
  };
}

test("login main bridge opens only the official URL in the default browser", async () => {
  const harness = createHarness();
  const event = { sender: harness.sender };

  assert.ok(harness.probe.trustedOwner(event));
  assert.equal(
    harness.probe.verifiedInitialURL("https://chatgpt.com/codex/desktop-auth?state=first"),
    "https://chatgpt.com/codex/desktop-auth?state=first",
  );

  await assert.rejects(
    () => harness.open(event, "https://example.invalid/sign-in"),
    /official ChatGPT sign-in link could not be opened/,
  );
  assert.equal(harness.windows.length, 0);

  const first = await harness.open(event, "https://chatgpt.com/codex/desktop-auth?state=first");
  assert.match(first.id, /^[0-9a-f-]{36}$/i);
  assert.equal(first.mode, "external");
  assert.deepEqual(harness.externalURLs, ["https://chatgpt.com/codex/desktop-auth?state=first"]);
  assert.equal(harness.windows.length, 0, "OAuth must not create an embedded Electron login window");
  assert.ok(harness.probe.isFlowID(first.id));

  assert.equal(await harness.close(event, first.id), true);
  assert.equal(await harness.close(event, first.id), false, "a closed flow cannot be closed twice");

  const second = await harness.open(event, "https://auth.openai.com/authorize?state=second");
  assert.notEqual(second.id, first.id);
  assert.equal(second.mode, "external");
  assert.deepEqual(harness.externalURLs, [
    "https://chatgpt.com/codex/desktop-auth?state=first",
    "https://auth.openai.com/authorize?state=second",
  ]);
});

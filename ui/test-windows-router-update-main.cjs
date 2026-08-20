const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadBridge() {
  const source = fs.readFileSync(path.join(__dirname, "windows-router-update-main.js"), "utf8");
  const anchor = '  ipcMain.handle(STATE_CHANNEL, (event) => {';
  const instrumented = source.replace(
    anchor,
    [
      "  globalThis.__codexMuxUpdaterTest = { approvedURL, validateManifest, isNewer };",
      "",
      anchor,
    ].join("\n"),
  );
  assert.notEqual(instrumented, source, "could not instrument update bridge");
  const handlers = new Map();
  const context = {
    URL,
    Buffer,
    Promise,
    Set,
    Date,
    Error,
    Number,
    String,
    JSON,
    Math,
    parseInt,
    setTimeout() { return 1; },
    setInterval() { return 1; },
    clearTimeout() {},
    process: {
      env: { LOCALAPPDATA: "C:\\Router", APPDATA: "C:\\AppData" },
      resourcesPath: "C:\\Router\\app\\resources",
      pid: 123,
    },
    require(name) {
      if (name === "electron") {
        return {
          app: { whenReady: () => Promise.resolve(), getPath: () => "C:\\AppData" },
          BrowserWindow: {
            fromWebContents: () => null,
            getAllWindows: () => [],
          },
          ipcMain: { handle(channel, handler) { handlers.set(channel, handler); } },
        };
      }
      if (name === "node:https") return { get() { throw new Error("network disabled in fixture"); } };
      if (name === "node:fs") return { existsSync: () => false };
      if (name === "node:path") return require("node:path");
      if (name === "node:child_process") return { spawn() { throw new Error("spawn disabled in fixture"); } };
      throw new Error(`Unexpected module: ${name}`);
    },
  };
  vm.createContext(context);
  vm.runInContext(instrumented, context, { filename: "windows-router-update-main.js" });
  return context.__codexMuxUpdaterTest;
}

test("update bridge accepts only approved HTTPS GitHub hosts", () => {
  const bridge = loadBridge();
  assert.ok(bridge.approvedURL("https://github.com/LightHaru/codex-relay/releases/latest/download/windows-update.json"));
  assert.ok(bridge.approvedURL("https://release-assets.githubusercontent.com/a/source.zip?sig=1"));
  assert.equal(bridge.approvedURL("http://github.com/a"), null);
  assert.equal(bridge.approvedURL("https://example.invalid/a"), null);
  assert.equal(bridge.approvedURL("https://github.com.evil.invalid/a"), null);
  assert.equal(bridge.approvedURL("https://github.com/a user"), null);
});

test("update bridge validates manifest and compares semantic versions", () => {
  const bridge = loadBridge();
  const raw = {
    schema: 1,
    product: "codex-subscription-router",
    version: "0.2.0",
    sourceUrl: "https://github.com/LightHaru/codex-relay/releases/download/v0.3.0/source.zip",
    sourceSha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    releaseUrl: "https://github.com/LightHaru/codex-relay/releases/tag/v0.3.0",
    notes: "safe update",
  };
  const valid = bridge.validateManifest(raw);
  assert.equal(valid.version, "0.2.0");
  assert.equal(bridge.isNewer("0.2.0", "0.1.9"), true);
  assert.equal(bridge.isNewer("0.2.0", "0.2.0"), false);
  assert.equal(bridge.isNewer("0.1.9", "0.2.0"), false);
  assert.throws(() => bridge.validateManifest({ ...raw, sourceSha256: "bad" }), /invalid source hash/);
});

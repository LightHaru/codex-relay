/*
 * Main-process companion for the Windows Codex Relay updater.
 *
 * The public release contains source only.  This bridge downloads a small
 * GitHub manifest, verifies the source archive's SHA-256 in the external
 * router-updater.exe helper, and never accepts a URL or executable path from
 * the renderer.  The official Microsoft Store app remains outside this flow.
 */
(() => {
  "use strict";

  const { app, BrowserWindow, ipcMain } = require("electron");
  const https = require("node:https");
  const fs = require("node:fs");
  const path = require("node:path");
  const { spawn } = require("node:child_process");

  const STATE_CHANNEL = "codex-mux:update-state";
  const INSTALL_CHANNEL = "codex-mux:install-update";
  const EVENT_CHANNEL = "codex-mux:update-available";
  const PRODUCT = "codex-subscription-router";
  const CURRENT_VERSION = "__CODEX_MUX_ROUTER_VERSION__";
  const MANIFEST_URL = "__CODEX_MUX_UPDATE_MANIFEST_URL__";
  const MAX_MANIFEST_BYTES = 1024 * 1024;
  const CHECK_INTERVAL_MS = 6 * 60 * 60 * 1000;
  const ALLOWED_HOSTS = new Set([
    "github.com",
    "objects.githubusercontent.com",
    "release-assets.githubusercontent.com",
    "raw.githubusercontent.com",
  ]);
  let state = {
    available: false,
    installing: false,
    currentVersion: CURRENT_VERSION,
    version: "",
    sourceUrl: "",
    sourceSha256: "",
    releaseUrl: "",
    notes: "",
    checkedAt: 0,
    error: "",
  };

  function trustedOwner(event) {
    const owner = BrowserWindow.fromWebContents(event.sender);
    if (owner == null || owner.isDestroyed() || event.sender.isDestroyed()) return null;
    try {
      const source = new URL(event.sender.getURL());
      return source.protocol === "file:" || source.protocol === "app:" ? owner : null;
    } catch {
      return null;
    }
  }

  function publicState() {
    return {
      available: state.available === true,
      installing: state.installing === true,
      currentVersion: state.currentVersion,
      version: state.version,
      releaseUrl: state.releaseUrl,
      notes: state.notes,
      checkedAt: state.checkedAt,
      error: state.error,
    };
  }

  function broadcast() {
    for (const window of BrowserWindow.getAllWindows()) {
      if (window.isDestroyed()) continue;
      try {
        window.webContents.send(EVENT_CHANNEL, publicState());
      } catch {
        // A window may close while an update check is completing.
      }
    }
  }

  function approvedURL(raw) {
    if (typeof raw !== "string" || raw.length === 0 || raw.length > 8_192) return null;
    if (/\s/.test(raw)) return null;
    try {
      const parsed = new URL(raw);
      if (parsed.protocol !== "https:" || parsed.username || parsed.password) return null;
      if (!ALLOWED_HOSTS.has(parsed.hostname.toLowerCase())) return null;
      return parsed;
    } catch {
      return null;
    }
  }

  function fetchJSON(raw, redirects = 0) {
    const parsed = approvedURL(raw);
    if (parsed == null) return Promise.reject(new Error("update URL is not an approved HTTPS GitHub URL"));
    if (redirects > 8) return Promise.reject(new Error("too many update redirects"));
    return new Promise((resolve, reject) => {
      const request = https.get(parsed, { headers: { Accept: "application/json" } }, (response) => {
        const location = response.headers.location;
        if (location && response.statusCode >= 300 && response.statusCode < 400) {
          response.resume();
          let next;
          try {
            next = new URL(location, parsed).href;
          } catch {
            reject(new Error("update redirect URL is invalid"));
            return;
          }
          fetchJSON(next, redirects + 1).then(resolve, reject);
          return;
        }
        if (response.statusCode < 200 || response.statusCode >= 300) {
          response.resume();
          reject(new Error(`update manifest returned HTTP ${response.statusCode}`));
          return;
        }
        let size = 0;
        const chunks = [];
        response.on("data", (chunk) => {
          size += chunk.length;
          if (size > MAX_MANIFEST_BYTES) {
            request.destroy(new Error("update manifest is too large"));
            return;
          }
          chunks.push(chunk);
        });
        response.on("end", () => {
          try {
            resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
          } catch {
            reject(new Error("update manifest is not valid JSON"));
          }
        });
      });
      request.setTimeout(30_000, () => request.destroy(new Error("update manifest timed out")));
      request.on("error", reject);
    });
  }

  function parseVersion(value) {
    if (typeof value !== "string" || !/^\d+\.\d+\.\d+$/.test(value.trim().replace(/^v/, ""))) return null;
    return value.trim().replace(/^v/, "").split(".").map((part) => Number(part));
  }

  function isNewer(candidate, current) {
    const left = parseVersion(candidate);
    const right = parseVersion(current);
    if (left == null || right == null) return false;
    for (let index = 0; index < 3; index += 1) {
      if (left[index] !== right[index]) return left[index] > right[index];
    }
    return false;
  }

  function validateManifest(value) {
    if (value == null || typeof value !== "object") throw new Error("update manifest is not an object");
    if (value.schema !== 1 || value.product !== PRODUCT) throw new Error("update manifest belongs to another product");
    if (parseVersion(value.version) == null) throw new Error("update manifest has an invalid version");
    if (approvedURL(value.sourceUrl) == null || approvedURL(value.releaseUrl) == null) {
      throw new Error("update manifest contains an unapproved URL");
    }
    if (typeof value.sourceSha256 !== "string" || !/^[0-9a-f]{64}$/i.test(value.sourceSha256)) {
      throw new Error("update manifest has an invalid source hash");
    }
    return {
      version: value.version.trim().replace(/^v/, ""),
      sourceUrl: value.sourceUrl,
      sourceSha256: value.sourceSha256.toLowerCase(),
      releaseUrl: value.releaseUrl,
      notes: typeof value.notes === "string" ? value.notes.slice(0, 16_384) : "",
    };
  }

  async function check() {
    try {
      const release = validateManifest(await fetchJSON(MANIFEST_URL));
      state = {
        ...state,
        ...release,
        available: isNewer(release.version, CURRENT_VERSION),
        checkedAt: Date.now(),
        error: "",
      };
    } catch (error) {
      // A missing manifest is normal before the first public release.  Keep
      // the last known update hidden rather than showing a scary network error.
      state = { ...state, available: false, checkedAt: Date.now(), error: String(error?.message || error).slice(0, 512) };
    }
    broadcast();
    return publicState();
  }

  function updaterPath() {
    const localAppData = process.env.LOCALAPPDATA;
    if (typeof localAppData !== "string" || localAppData.trim() === "") return null;
    return path.join(localAppData, "Codex Relay Updater", "router-updater.exe");
  }

  function install() {
    if (!state.available || state.installing) return publicState();
    const helper = updaterPath();
    if (helper == null || !fs.existsSync(helper)) {
      state = { ...state, error: "The Router update helper is missing. Run the Windows installer once to repair it." };
      broadcast();
      return publicState();
    }
    const appRoot = path.resolve(process.resourcesPath, "..");
    const appData = process.env.APPDATA || app.getPath("appData");
    const relayProfile = path.join(appData, "Codex Relay");
    const legacyProfile = path.join(appData, "Codex Subscription Router");
    const profile = fs.existsSync(relayProfile) || !fs.existsSync(legacyProfile)
      ? relayProfile
      : legacyProfile;
    try {
      spawn(helper, [
        "--manifest-url", MANIFEST_URL,
        "--install-root", appRoot,
        "--profile", profile,
        "--parent-pid", String(process.pid),
        "--current-version", CURRENT_VERSION,
      ], {
        cwd: path.dirname(helper),
        detached: true,
        windowsHide: true,
        stdio: "ignore",
      }).unref();
      state = { ...state, installing: true };
      broadcast();
      setTimeout(() => app.quit(), 120);
    } catch (error) {
      state = { ...state, error: `Could not start the Codex Relay updater: ${String(error?.message || error).slice(0, 400)}` };
      broadcast();
    }
    return publicState();
  }

  ipcMain.handle(STATE_CHANNEL, (event) => {
    if (trustedOwner(event) == null) throw new Error("untrusted Router update request");
    return publicState();
  });
  ipcMain.handle(INSTALL_CHANNEL, (event) => {
    if (trustedOwner(event) == null) throw new Error("untrusted Router update request");
    return install();
  });

  void app.whenReady().then(() => {
    setTimeout(() => { void check(); }, 8_000);
    setInterval(() => { void check(); }, CHECK_INTERVAL_MS);
  });
})();

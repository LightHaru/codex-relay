/*
 * Main-process companion for the Windows subscription menu.
 *
 * The official Codex app-server owns the OAuth callback listener and the
 * credential exchange. OpenAI's documented flow is to open the returned
 * authUrl in a browser, so Relay deliberately uses the user's normal browser
 * here instead of embedding auth.openai.com in an Electron child window. An
 * embedded Electron session can be rejected by the provider's bot protection
 * and cannot share the user's normal browser session reliably.
 *
 * The browser receives only the short-lived, allowlisted authorization URL.
 * Relay never receives passwords, callback codes, or OAuth tokens.
 */
(() => {
  "use strict";

  const { app, BrowserWindow, ipcMain, shell } = require("electron");
  const { randomUUID } = require("node:crypto");

  const OPEN_CHANNEL = "codex-mux:open-isolated-login";
  const CLOSE_CHANNEL = "codex-mux:close-isolated-login";
  const CLOSED_CHANNEL = "codex-mux:isolated-login-closed";
  const EXTERNAL_MODE = "external";
  const flows = new Map();

  // Give the copied process its own Windows application identity.  The
  // upstream executable is still named ChatGPT.exe for compatibility with
  // the Store layout, but AppUserModelId/name are what Windows uses to group
  // taskbar entries and identify the app beside the official Codex install.
  function enforceAppIdentity() {
    try {
      app.setName?.("Codex Relay");
      app.setAppUserModelId?.("com.lightharu.codexrelay");
    } catch {
      // Older Electron builds may not expose one of these APIs; the branded
      // window title remains the fallback identity in that case.
    }
  }
  enforceAppIdentity();
  // The upstream startup routine may set its own AppUserModelId during the
  // ready phase. Re-apply the Relay identity after that routine has run so
  // Windows does not group this copy under the official Codex/ChatGPT entry.
  app.on("ready", () => {
    enforceAppIdentity();
    setImmediate(enforceAppIdentity);
  });

  // The copied Store bundle otherwise keeps the upstream window title
  // ("ChatGPT"), which is confusing when the official app is open beside
  // Relay. This listener is injected only into the independent Relay ASAR;
  // it never runs in the Microsoft Store installation.
  function brandWindow(window) {
    if (window == null || window.isDestroyed?.()) return;
    try {
      window.on("page-title-updated", (event) => {
        event.preventDefault();
        window.setTitle("Codex Relay");
      });
      window.setTitle("Codex Relay");
    } catch {
      // A transient child window may close while the startup hook runs.
    }
  }

  app.on("browser-window-created", (_event, window) => brandWindow(window));
  for (const window of BrowserWindow.getAllWindows()) brandWindow(window);

  function verifiedInitialURL(value) {
    if (typeof value !== "string" || value.length === 0 || value.length > 16_384) return null;
    try {
      const url = new URL(value);
      if (url.protocol !== "https:" || !["chatgpt.com", "auth.openai.com"].includes(url.hostname)) {
        return null;
      }
      return url.href;
    } catch {
      return null;
    }
  }

  // Flow IDs are generated in this trusted main process. Keep validation
  // opaque-but-strict rather than tying compatibility to one UUID spelling;
  // this also tolerates older Electron/Node builds that serialize UUIDs
  // differently while still rejecting arbitrary IPC input.
  function isFlowID(value) {
    return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(value);
  }

  function trustedOwner(event) {
    const owner = BrowserWindow.fromWebContents(event.sender);
    if (owner == null || owner.isDestroyed() || event.sender.isDestroyed()) return null;
    try {
      const source = new URL(event.sender.getURL());
      // The injected menu runs only in the packaged Codex renderer. Remote
      // pages never receive this bridge.
      return source.protocol === "file:" || source.protocol === "app:" ? owner : null;
    } catch {
      return null;
    }
  }

  function notifyClosed(flow, reason) {
    if (flow.owner.isDestroyed()) return;
    try {
      flow.owner.send(CLOSED_CHANNEL, { id: flow.id, reason });
    } catch {
      // The main Router window can be closing at the same time as this flow.
    }
  }

  function closeFlow(flow, reason) {
    if (flow == null || flow.closed) return false;
    flow.closed = true;
    flows.delete(flow.id);
    notifyClosed(flow, reason);
    return true;
  }

  ipcMain.handle(OPEN_CHANNEL, async (event, value) => {
    const ownerWindow = trustedOwner(event);
    const authorizationURL = verifiedInitialURL(value);
    if (ownerWindow == null || authorizationURL == null) {
      throw new Error("The official ChatGPT sign-in link could not be opened.");
    }

    const id = String(randomUUID());
    const flow = { id, owner: event.sender, ownerWindow, closed: false };
    flows.set(id, flow);
    try {
      // shell.openExternal is the supported desktop OAuth hand-off. It lets
      // Cloudflare/passkeys/SSO use the user's real browser and still returns
      // to the localhost callback owned by the isolated Codex child process.
      await shell.openExternal(authorizationURL);
      return { id, mode: EXTERNAL_MODE };
    } catch {
      closeFlow(flow, "open-failed");
      throw new Error("The default browser could not open the official ChatGPT sign-in page.");
    }
  });

  ipcMain.handle(CLOSE_CHANNEL, (event, id) => {
    if (!isFlowID(id)) return false;
    const flow = flows.get(id);
    if (flow == null || flow.owner !== event.sender) return false;
    return closeFlow(flow, "closed-by-router");
  });
})();

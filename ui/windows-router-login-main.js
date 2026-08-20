/*
 * Main-process companion for the Windows subscription menu.
 *
 * This file is injected only into the independent Router copy. It deliberately
 * creates a new, non-persistent Electron session for each official ChatGPT
 * sign-in. The external page gets no preload or Node APIs, and this bridge
 * never receives credentials or OAuth tokens.
 */
(() => {
  "use strict";

  const { BrowserWindow, ipcMain, session } = require("electron");
  const { randomUUID } = require("node:crypto");

  const OPEN_CHANNEL = "codex-mux:open-isolated-login";
  const CLOSE_CHANNEL = "codex-mux:close-isolated-login";
  const CLOSED_CHANNEL = "codex-mux:isolated-login-closed";
  const LOGIN_TITLE = "Sign in to ChatGPT — Codex Relay";
  const flows = new Map();

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

  function isLoopbackHost(hostname) {
    return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]" || hostname === "::1";
  }

  function isAllowedLoginNavigation(value) {
    try {
      const url = new URL(value);
      return url.protocol === "https:" || (url.protocol === "http:" && isLoopbackHost(url.hostname));
    } catch {
      return false;
    }
  }

  function trustedOwner(event) {
    const owner = BrowserWindow.fromWebContents(event.sender);
    if (owner == null || owner.isDestroyed() || event.sender.isDestroyed()) return null;
    try {
      const source = new URL(event.sender.getURL());
      // The injected menu runs only in the packaged Codex renderer. Remote
      // pages, including the login page itself, have no access to this bridge.
      return source.protocol === "file:" || source.protocol === "app:" ? owner : null;
    } catch {
      return null;
    }
  }

  function isFlowID(value) {
    return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
  }

  function secureWebPreferences(partition) {
    return {
      contextIsolation: true,
      nodeIntegration: false,
      nodeIntegrationInSubFrames: false,
      nodeIntegrationInWorker: false,
      sandbox: true,
      webSecurity: true,
      allowRunningInsecureContent: false,
      webviewTag: false,
      enableRemoteModule: false,
      devTools: false,
      partition,
    };
  }

  function clearLoginSession(loginSession) {
    const settle = (operation) => {
      try {
        return Promise.resolve(operation());
      } catch {
        return Promise.resolve();
      }
    };
    void Promise.allSettled([
      settle(() => loginSession.clearStorageData()),
      settle(() => loginSession.clearCache()),
      settle(() => loginSession.clearAuthCache()),
    ]);
  }

  function configureLoginSession(partition) {
    const loginSession = session.fromPartition(partition);
    loginSession.setPermissionCheckHandler(() => false);
    loginSession.setPermissionRequestHandler((_webContents, _permission, callback) => callback(false));
    loginSession.on("will-download", (event) => event.preventDefault());
    return loginSession;
  }

  function notifyClosed(flow, reason) {
    if (flow.owner.isDestroyed()) return;
    try {
      flow.owner.send(CLOSED_CHANNEL, { id: flow.id, reason });
    } catch {
      // The main Router window can be closing at the same time as this child.
    }
  }

  function closeFlow(flow, reason) {
    if (flow.closed) return;
    flow.closed = true;
    flows.delete(flow.id);
    for (const loginWindow of flow.windows) {
      if (!loginWindow.isDestroyed()) loginWindow.destroy();
    }
    flow.windows.clear();
    clearLoginSession(flow.loginSession);
    notifyClosed(flow, reason);
  }

  function popupOptions(flow) {
    return {
      autoHideMenuBar: true,
      backgroundColor: "#202124",
      parent: flow.ownerWindow,
      title: LOGIN_TITLE,
      webPreferences: secureWebPreferences(flow.partition),
    };
  }

  function protectLoginWindow(flow, loginWindow) {
    const { webContents } = loginWindow;
    webContents.setWindowOpenHandler(({ url }) => {
      // Providers occasionally use a popup. Keep it inside the same isolated
      // Electron session rather than handing it to the user's default browser.
      if (url !== "about:blank" && !isAllowedLoginNavigation(url)) return { action: "deny" };
      return { action: "allow", overrideBrowserWindowOptions: popupOptions(flow) };
    });
    webContents.on("will-navigate", (event, url) => {
      if (!isAllowedLoginNavigation(url)) event.preventDefault();
    });
    webContents.on("will-redirect", (event, url) => {
      if (!isAllowedLoginNavigation(url)) event.preventDefault();
    });
    webContents.on("will-attach-webview", (event) => event.preventDefault());
    webContents.on("did-create-window", (child) => {
      if (flow.closed) {
        child.destroy();
        return;
      }
      trackLoginWindow(flow, child);
    });
    webContents.on("render-process-gone", () => {
      if (loginWindow === flow.root) closeFlow(flow, "renderer-gone");
    });
  }

  function trackLoginWindow(flow, loginWindow) {
    if (flow.windows.has(loginWindow)) return;
    flow.windows.add(loginWindow);
    loginWindow.setMenuBarVisibility(false);
    if (typeof loginWindow.removeMenu === "function") loginWindow.removeMenu();
    protectLoginWindow(flow, loginWindow);
    loginWindow.on("closed", () => {
      flow.windows.delete(loginWindow);
      if (loginWindow === flow.root) closeFlow(flow, "closed");
    });
  }

  ipcMain.handle(OPEN_CHANNEL, (event, value) => {
    const ownerWindow = trustedOwner(event);
    const authorizationURL = verifiedInitialURL(value);
    if (ownerWindow == null || authorizationURL == null) {
      throw new Error("The private Router sign-in window could not be opened.");
    }

    const id = randomUUID();
    // No `persist:` prefix: Electron keeps this partition in memory only. A
    // different random partition is created for every sign-in launch.
    const partition = `codex-mux-login-${randomUUID()}`;
    const loginSession = configureLoginSession(partition);
    const flow = {
      id,
      owner: event.sender,
      ownerWindow,
      loginSession,
      partition,
      root: null,
      windows: new Set(),
      closed: false,
    };
    flows.set(id, flow);

    let loginWindow;
    try {
      loginWindow = new BrowserWindow({
        ...popupOptions(flow),
        width: 580,
        height: 760,
        minWidth: 460,
        minHeight: 600,
        show: false,
      });
      flow.root = loginWindow;
      trackLoginWindow(flow, loginWindow);
      loginWindow.once("ready-to-show", () => {
        if (!flow.closed && !loginWindow.isDestroyed()) {
          loginWindow.show();
          loginWindow.focus();
        }
      });
      void loginWindow.loadURL(authorizationURL).catch(() => closeFlow(flow, "load-failed"));
      // Do not leave a blank hidden window behind if the remote page does not
      // emit ready-to-show (for example, while a provider is redirecting).
      loginWindow.show();
      loginWindow.focus();
      return { id };
    } catch {
      closeFlow(flow, "open-failed");
      throw new Error("The private Router sign-in window could not be opened.");
    }
  });

  ipcMain.handle(CLOSE_CHANNEL, (event, id) => {
    if (!isFlowID(id)) return false;
    const flow = flows.get(id);
    if (flow == null || flow.owner !== event.sender) return false;
    closeFlow(flow, "closed-by-router");
    return true;
  });
})();

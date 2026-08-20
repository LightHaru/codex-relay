/* Exposes only the minimal private-login window controls to the Router UI. */
(() => {
  "use strict";

  const { contextBridge, ipcRenderer } = require("electron");
  const OPEN_CHANNEL = "codex-mux:open-isolated-login";
  const CLOSE_CHANNEL = "codex-mux:close-isolated-login";
  const CLOSED_CHANNEL = "codex-mux:isolated-login-closed";
  const listeners = new Set();

  function isFlowID(value) {
    return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
  }

  ipcRenderer.on(CLOSED_CHANNEL, (_event, payload) => {
    if (payload == null || !isFlowID(payload.id)) return;
    for (const listener of listeners) {
      try {
        listener({ id: payload.id, reason: typeof payload.reason === "string" ? payload.reason : "closed" });
      } catch {
        // A UI listener must not affect Electron's IPC delivery.
      }
    }
  });

  contextBridge.exposeInMainWorld("codexMuxLoginWindow", {
    async open(authorizationURL) {
      const result = await ipcRenderer.invoke(
        OPEN_CHANNEL,
        typeof authorizationURL === "string" ? authorizationURL : "",
      );
      if (result == null || !isFlowID(result.id)) {
        throw new Error("The private Router sign-in window returned an invalid response.");
      }
      return { id: result.id };
    },
    async close(id) {
      if (!isFlowID(id)) return false;
      return (await ipcRenderer.invoke(CLOSE_CHANNEL, id)) === true;
    },
    subscribeClosed(listener) {
      if (typeof listener !== "function") return () => {};
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
})();

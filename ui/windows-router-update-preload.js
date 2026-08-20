/* Exposes the verified, local Router update state to the packaged renderer. */
(() => {
  "use strict";

  const STATE_CHANNEL = "codex-mux:update-state";
  const INSTALL_CHANNEL = "codex-mux:install-update";
  const EVENT_CHANNEL = "codex-mux:update-available";
  const listeners = new Set();

  function text(value, limit = 16_384) {
    return typeof value === "string" ? value.slice(0, limit) : "";
  }

  function sanitize(value) {
    if (value == null || typeof value !== "object") return { available: false };
    return {
      available: value.available === true,
      installing: value.installing === true,
      currentVersion: text(value.currentVersion, 64),
      version: text(value.version, 64),
      releaseUrl: text(value.releaseUrl, 2_048),
      notes: text(value.notes),
      checkedAt: Number.isFinite(Number(value.checkedAt)) ? Number(value.checkedAt) : 0,
      error: text(value.error, 512),
    };
  }

  ipcRenderer.on(EVENT_CHANNEL, (_event, value) => {
    const state = sanitize(value);
    for (const listener of listeners) {
      try {
        listener(state);
      } catch {
        // A renderer listener must not interrupt IPC delivery.
      }
    }
  });

  contextBridge.exposeInMainWorld("codexMuxUpdater", {
    async getState() {
      return sanitize(await ipcRenderer.invoke(STATE_CHANNEL));
    },
    async install() {
      return sanitize(await ipcRenderer.invoke(INSTALL_CHANNEL));
    },
    subscribe(listener) {
      if (typeof listener !== "function") return () => {};
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
})();

const CODEX_MUX_THREAD_API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1";
const CODEX_MUX_THREAD_TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";

function CodexMuxThreadSubscription() {
  const route = $n(sr);
  const threadId =
    route.value.routeKind === "local-thread" ? route.value.conversationId : null;
  const [account, setAccount] = TE.useState(null);

  TE.useEffect(() => {
    let active = true;
    if (!threadId) {
      setAccount(null);
      return () => {
        active = false;
      };
    }

    const refresh = async () => {
      try {
        const response = await fetch(
          `${CODEX_MUX_THREAD_API}/thread-account?threadId=${encodeURIComponent(threadId)}`,
          { headers: { "X-Codex-Mux-Token": CODEX_MUX_THREAD_TOKEN } },
        );
        if (!response.ok) throw new Error(`Request failed (${response.status})`);
        const body = await response.json();
        if (active) setAccount(body.account || null);
      } catch {
        if (active) setAccount(null);
      }
    };

    refresh();
    const events = new EventSource(
      `${CODEX_MUX_THREAD_API}/events?token=${encodeURIComponent(CODEX_MUX_THREAD_TOKEN)}`,
    );
    events.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (
          payload.type === "account-updated" ||
          payload.type === "primary-changed" ||
          payload.type === "router-restarted" ||
          (payload.type === "thread-failed-over" &&
            payload.data?.threadId === threadId)
        ) {
          refresh();
        }
      } catch {}
    };
    const warmupTimer = setTimeout(refresh, 2_000);
    const timer = setInterval(refresh, 30_000);
    return () => {
      active = false;
      clearTimeout(warmupTimer);
      clearInterval(timer);
      events.close();
    };
  }, [threadId]);

  if (!account) return null;
  const weekly = codexMuxThreadWeeklyWindow(account.rateLimits);
  const remaining = weekly == null ? null : Math.max(0, 100 - weekly.usedPercent);
  const depleted = remaining === 0;
  const reset = codexMuxThreadResetLabel(weekly?.resetsAt);
  const accountName = codexMuxThreadAccountName(account);
  const AccountAvatar = globalThis.CodexMuxAccountAvatar;
  return (0, zE.jsx)(K.Section, {
    sectionKey: "codex-mux-subscription",
    title: "Subscription",
    children: (0, zE.jsxs)("div", {
      className: "flex min-h-9 items-center justify-between gap-3 py-1 text-sm",
      children: [
        (0, zE.jsxs)("div", {
          className: "flex min-w-0 items-center gap-2",
          children: [
            AccountAvatar
              ? (0, zE.jsx)(AccountAvatar, {
                  imageUrl: account.profileImageUrl,
                  label: accountName,
                  className: "size-5 shrink-0",
                })
              : null,
            (0, zE.jsx)("span", {
              className: "truncate text-token-text-primary",
              children: account.planLabel ? `${accountName} · ${account.planLabel}` : accountName,
            }),
          ],
        }),
        (0, zE.jsx)("span", {
          className: "shrink-0 tabular-nums text-token-description-foreground",
          children:
            remaining == null
              ? "Usage unavailable"
              : depleted
                ? reset ? `Depleted · reset ${reset}` : "Depleted"
                : reset ? `${Math.round(remaining)}% · reset ${reset}` : `${Math.round(remaining)}% remaining`,
        }),
      ],
    }),
  });
}

function codexMuxThreadAccountName(account) {
  const name = [account?.displayName, account?.username, account?.email, account?.label]
    .map((value) => String(value || "").trim())
    .find(Boolean) || "Subscription";
  return name;
}

function codexMuxThreadResetLabel(resetsAt) {
  const seconds = Number(resetsAt);
  if (!Number.isFinite(seconds) || seconds <= 0) return null;
  const minutes = Math.ceil((seconds * 1000 - Date.now()) / 60000);
  if (minutes <= 0) return "now";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  if (hours < 48) return remainder ? `${hours}h ${remainder}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d${hours % 24 ? ` ${hours % 24}h` : ""}`;
}

function codexMuxThreadWeeklyWindow(rateLimits) {
  const windows = [rateLimits?.primary, rateLimits?.secondary].filter(Boolean);
  windows.sort(
    (left, right) =>
      (left.windowDurationMins || 0) - (right.windowDurationMins || 0),
  );
  return windows.at(-1) || null;
}

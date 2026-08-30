/*
 * Windows renderer bridge for Codex Relay.
 *
 * This deliberately uses DOM insertion instead of renderer-private React
 * symbols. Windows builds rename/minify those symbols independently of macOS,
 * while the profile menu continues to expose stable visible menu rows.
 */
(() => {
  "use strict";

  const API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1";
  const TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";
  const MENU_ID = "codex-mux-windows-menu";
  const FALLBACK_TRIGGER_ID = "codex-mux-windows-account-trigger";
  const FALLBACK_MENU_ID = "codex-mux-windows-account-popover";
  const TASK_ROUTE_BADGE_ID = "codex-mux-windows-task-route";
  const USAGE_SURFACE_ID = "codex-mux-windows-usage-surface";
  const STYLE_ID = "codex-mux-windows-menu-style";
  const PROFILE_ACCOUNT_KEY = "codex-mux.windows.profile-account";
  const PLUGIN_ACCOUNT_KEY = "codex-mux.windows.plugin-account";
  const RESET_ACCOUNT_KEY = "codex-mux.windows.reset-account";
  const CURRENT_THREAD_KEY = "codex-mux.windows.current-thread";
  const ROUTING_EVENT_IDS_KEY = "codex-mux.windows.routing-event-ids";
  const LIVE_QUOTA_REFRESH_MS = 1000;
  const LIVE_QUOTA_MAX_AGE_MS = 2500;
  const SCOPED_PLUGIN_METHODS = new Set([
    "app/installed",
    "app/list",
    "app/read",
    "mcpServer/oauth/login",
    "mcpServerStatus/list",
  ]);
  // The native desktop profile menu renders Settings with its keyboard
  // shortcut in the same button (for example, "Settings ⌘,").  Match the
  // smallest visible text node first, then walk back to its menu row.  This is
  // deliberately also tolerant of the Vietnamese labels used by the app.
  const PROFILE_SETTINGS_LABELS = ["Settings", "Cài đặt"];
  const PROFILE_LOGOUT_LABELS = ["Log out", "Sign out", "Đăng xuất"];
  let refreshTimer = null;
  let scheduled = false;
  let activeLogin = null;
  let activeToast = null;
  let activeUpdateToast = null;
  let updateWatcherStarted = false;
  let routingWatcherStarted = false;
  let routingEventSource = null;
  let latestAccounts = [];
  let latestAccountsFetchedAt = 0;
  let accountsRefreshPromise = null;
  let usageSurfaceVersion = 0;
  const resetSubscribers = new Set();

  const ROUTING_COPY = {
    en: {
      title: "Relay routing", controller: "Relay Controller", current: "Current Task Route",
      next: "Next Candidate", unavailable: "Unavailable", noRoute: "No route recorded",
      openTask: "Open a task to inspect its route", generation: "generation", recovery: "recovery required",
      left: "left", probation: "quota probation", effective: "Effective mode",
      unsupported: "Safe handoff is unavailable for this app version; tasks remain Sticky.",
      reviewed: "I reviewed this task · allow next turn", clearFailed: "Could not clear recovery state",
      policyFailed: "Could not change routing policy", lastDecision: "Last decision",
      sticky: "Sticky", balanced: "Balanced", rotate: "Rotate",
      runningVia: "Running via", mode: "Mode", handoff: "Handoff",
      lastUsed: "Last completed via", nextPreview: "Next Candidate (preview)", noActiveTurn: "No turn is currently running",
      details: "Routing details", currentOwner: "Current owner", activeWorker: "Active worker",
      lastQuota: "Last quota attributed to", previousWorker: "Previous worker", requestedMode: "Requested mode",
      why: "Why this account", timeline: "Routing timeline", poolSummary: "Pool summary",
      routingOnly: "Pool quota is routing capacity. Each request still runs on one subscription.",
      quotaFreshness: "Quota snapshot", score: "score", reservation: "reservation", noEvents: "No routing events yet",
      quotaAttribution: "Quota attribution", decisionId: "Scheduler decision",
      handoffComplete: "Task handoff completed", handoffFailed: "Task handoff needs attention",
      recoveryNotice: "Automatic retry stopped safely", from: "from", to: "to",
      allDepleted: "All connected subscriptions are out of quota", policyDowngraded: "Routing mode changed for safety",
      relayError: "Relay request failed",
      eligible: "eligible", depleted: "depleted", unknownQuota: "quota unknown", updated: "updated",
      sharedPool: "Shared quota pool", poolWorkers: "subscriptions act as one pool",
      poolUpdating: "quota updating", workerDiagnostics: "Quota source diagnostics",
      confirmedRemaining: "Confirmed remaining", poolMaximum: "Pool maximum", activeWorkers: "Available quota sources",
      poolTitle: "Codex Relay Pool", poolDescription: "One Relay task authority owns this chat, its tools, Goal state, and history. The pool changes only the private credential behind a request; the task stays on this one Relay.",
      poolHealth: "Health", activeRequests: "Active requests", quotaKnownSources: "Quota-known sources",
      quotaTiming: "Quota timing", nextPoolReset: "Next pool reset", quotaChecked: "Quota checked",
      unknownQuotaCount: "Unknown quota", noResetReported: "No reset reported", waitingQuotaEvidence: "Waiting for quota evidence",
      lastError: "Last Relay request issue",
    },
    vi: {
      title: "Định tuyến Relay", controller: "Tài khoản điều khiển Relay", current: "Tài khoản chạy task hiện tại",
      next: "Tài khoản dự kiến lượt kế", unavailable: "Không khả dụng", noRoute: "Chưa ghi nhận tuyến",
      openTask: "Mở một task để xem tuyến đang dùng", generation: "thế hệ", recovery: "cần kiểm tra phục hồi",
      left: "còn lại", probation: "đang kiểm tra quota", effective: "Chế độ thực tế",
      unsupported: "Phiên bản app này chưa hỗ trợ handoff an toàn; task sẽ giữ chế độ Sticky.",
      reviewed: "Đã kiểm tra task · cho phép lượt tiếp theo", clearFailed: "Không thể xóa trạng thái phục hồi",
      policyFailed: "Không thể đổi chế độ định tuyến", lastDecision: "Quyết định gần nhất",
      sticky: "Bám tài khoản", balanced: "Cân bằng", rotate: "Luân phiên",
      runningVia: "Đang chạy qua", mode: "Chế độ", handoff: "Đang bàn giao",
      lastUsed: "Lượt hoàn tất gần nhất qua", nextPreview: "Tài khoản dự kiến (chưa chạy)", noActiveTurn: "Hiện không có lượt nào đang chạy",
      details: "Chi tiết định tuyến", currentOwner: "Tài khoản đang sở hữu task", activeWorker: "Tài khoản thực thi hiện tại",
      lastQuota: "Quota gần nhất ghi nhận ở", previousWorker: "Tài khoản trước", requestedMode: "Chế độ đã chọn",
      why: "Vì sao chọn tài khoản", timeline: "Dòng thời gian định tuyến", poolSummary: "Tổng quan pool",
      routingOnly: "Quota trong pool là dung lượng định tuyến. Mỗi request vẫn chỉ chạy bằng một subscription.",
      quotaFreshness: "Thời điểm quota", score: "điểm", reservation: "giữ chỗ", noEvents: "Chưa có sự kiện định tuyến",
      quotaAttribution: "Ghi nhận tài khoản dùng quota", decisionId: "Mã quyết định scheduler",
      handoffComplete: "Đã bàn giao task", handoffFailed: "Bàn giao task cần kiểm tra",
      recoveryNotice: "Đã dừng thử lại tự động để bảo vệ task", from: "từ", to: "sang",
      allDepleted: "Tất cả tài khoản đã kết nối đều hết quota", policyDowngraded: "Đã đổi chế độ định tuyến để bảo đảm an toàn",
      relayError: "Relay gặp lỗi khi gửi request",
      eligible: "có thể nhận lượt", depleted: "đã hết quota", unknownQuota: "chưa rõ quota", updated: "cập nhật",
      sharedPool: "Pool quota dùng chung", poolWorkers: "tài khoản hợp thành một pool",
      poolUpdating: "quota đang cập nhật", workerDiagnostics: "Chẩn đoán nguồn quota",
      confirmedRemaining: "Quota xác nhận còn lại", poolMaximum: "Dung lượng tối đa", activeWorkers: "Nguồn quota khả dụng",
      poolTitle: "Pool Codex Relay", poolDescription: "Một Relay duy nhất sở hữu task, tool, trạng thái Goal và lịch sử chat. Pool chỉ đổi credential riêng phía sau request; task vẫn ở cùng một Relay duy nhất.",
      poolHealth: "Tình trạng", activeRequests: "Request đang chạy", quotaKnownSources: "Nguồn đã xác nhận quota",
      quotaTiming: "Thời gian quota", nextPoolReset: "Lần reset pool kế tiếp", quotaChecked: "Quota được kiểm tra lúc",
      unknownQuotaCount: "Quota chưa rõ", noResetReported: "Chưa có thời gian reset", waitingQuotaEvidence: "Đang chờ bằng chứng quota",
      lastError: "Lỗi request Relay gần nhất",
    },
  };

  function routingText(key) {
    const language = String(globalThis.navigator?.language || "en").toLowerCase().startsWith("vi") ? "vi" : "en";
    return ROUTING_COPY[language][key] || ROUTING_COPY.en[key] || key;
  }

  function poolPresentation(accounts, routing = null) {
    const connected = (Array.isArray(accounts) ? accounts : []).filter((account) => account?.enabled && account?.connected);
    // Every Relay Pool surface uses the same canonical 5H capacity. Weekly
    // quota remains account detail only and never drives the additive pool.
    const knownUsage = connected.map(shortestRemainingUsage).filter((value) => value != null);
    const upstream = routing?.status?.pool || routing?.pool || null;
    return {
      connected: Number.isFinite(Number(upstream?.connectedSubscriptions)) ? Number(upstream.connectedSubscriptions) : connected.length,
      maximum: Number.isFinite(Number(upstream?.maximumPercent)) ? Number(upstream.maximumPercent) : connected.length * 100,
      remaining: Number.isFinite(Number(upstream?.confirmedRemainingPercent))
        ? Number(upstream.confirmedRemainingPercent)
        : knownUsage.reduce((sum, value) => sum + value, 0),
      known: Number.isFinite(Number(upstream?.knownSubscriptions)) ? Number(upstream.knownSubscriptions) : knownUsage.length,
      unknown: Number.isFinite(Number(upstream?.unknownSubscriptions)) ? Number(upstream.unknownSubscriptions) : Math.max(0, connected.length - knownUsage.length),
      available: Number.isFinite(Number(upstream?.availableSubscriptions)) ? Number(upstream.availableSubscriptions) : connected.length,
    };
  }

  function normalize(value) {
    return String(value || "").replace(/\s+/g, " ").trim();
  }

  function isVisible(element) {
    if (!(element instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(element);
    return style.display !== "none" && style.visibility !== "hidden" && element.getClientRects().length > 0;
  }

  function make(tag, className, content) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (content != null) element.textContent = String(content);
    return element;
  }

  function append(parent, ...children) {
    for (const child of children.flat()) {
      if (child != null) parent.append(child);
    }
    return parent;
  }

  async function request(path, options = {}) {
    const response = await fetch(`${API}${path}`, {
      ...options,
      headers: {
        "Content-Type": "application/json",
        "X-Codex-Mux-Token": TOKEN,
        ...(options.headers || {}),
      },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const rawError = body?.error;
      const detail = typeof rawError === "string"
        ? rawError
        : rawError?.message || rawError?.detail || rawError?.code || "";
      throw new Error(detail || `Router request failed (${response.status})`);
    }
    return body;
  }

  function connectedAccounts(accounts) {
    return (Array.isArray(accounts) ? accounts : []).filter(
      (account) => account?.enabled && account?.connected,
    );
  }

  function readSessionValue(key, fallback = null) {
    try {
      return window.sessionStorage.getItem(key) || fallback;
    } catch {
      return fallback;
    }
  }

  function writeSessionValue(key, value) {
    try {
      if (value) window.sessionStorage.setItem(key, value);
      else window.sessionStorage.removeItem(key);
    } catch {
      // The router remains usable when the embedded renderer disables storage.
    }
  }

  function getProfileAccountId() {
    return readSessionValue(PROFILE_ACCOUNT_KEY);
  }

  function setProfileAccountId(accountId) {
    writeSessionValue(PROFILE_ACCOUNT_KEY, accountId || null);
  }

  function getPluginAccountId() {
    return readSessionValue(PLUGIN_ACCOUNT_KEY, "primary");
  }

  function setPluginAccountId(accountId) {
    writeSessionValue(PLUGIN_ACCOUNT_KEY, accountId || "primary");
  }

  function getResetAccountId() {
    return readSessionValue(RESET_ACCOUNT_KEY, "primary");
  }

  function notifyResetSubscribers() {
    for (const listener of [...resetSubscribers]) {
      try {
        listener();
      } catch {
        // A stale renderer listener must not prevent other surfaces updating.
      }
    }
  }

  function setResetAccountId(accountId) {
    writeSessionValue(RESET_ACCOUNT_KEY, accountId || "primary");
    notifyResetSubscribers();
  }

  function subscribeReset(listener) {
    resetSubscribers.add(listener);
    return () => resetSubscribers.delete(listener);
  }

  function scopePluginRequest(method, params) {
    observeThreadRequest(method, params);
    if (!SCOPED_PLUGIN_METHODS.has(method)) return params;
    if (params != null && (typeof params !== "object" || Array.isArray(params))) return params;
    const accountId = getPluginAccountId();
    return accountId ? { ...(params || {}), codexMuxAccountId: accountId } : params;
  }

  function observeThreadRequest(method, params) {
    const normalizedMethod = String(method || "");
    if (normalizedMethod === "thread/start") {
      writeSessionValue(CURRENT_THREAD_KEY, null);
      return;
    }
    if (!/^(thread|turn|item|hook)\//.test(normalizedMethod) || params == null || typeof params !== "object") return;
    const candidate = [params.threadId, params.thread_id, params.thread?.id, params.turn?.threadId, params.turn?.thread?.id]
      .map((value) => String(value || "").trim())
      .find((value) => /^[0-9a-f]{8}-[0-9a-f-]{27,}$/i.test(value));
    if (candidate) writeSessionValue(CURRENT_THREAD_KEY, candidate);
  }

  async function profileData() {
    const accountId = getProfileAccountId();
    const query = accountId ? `?accountId=${encodeURIComponent(accountId)}` : "";
    try {
      const result = await request(`/profile/combined${query}`);
      return result.profile;
    } catch (error) {
      // A deleted/disabled account can leave a stale tab-session selection.
      // Recover to the combined view instead of making the Profile page blank.
      if (!accountId) throw error;
      setProfileAccountId(null);
      const result = await request("/profile/combined");
      return result.profile;
    }
  }

  // Settings -> Usage normally reads the Store app's browser session. Relay
  // subscriptions intentionally use isolated Codex homes instead, so ask the
  // local Router for the same native usage payload. Returning null on a local
  // failure returns an empty account-shaped object. Returning an object (rather
  // than null) is deliberate: a Relay outage must never make the patched page
  // fall through to the official Codex account's browser session.
  async function nativeUsageStatus() {
    try {
      const result = await request("/usage");
      return result && typeof result.usage === "object" ? result.usage : {};
    } catch {
      return {};
    }
  }

  // The native Usage & billing page is intentionally kept as the page shell.
  // This companion request supplies one billing payload per isolated Relay
  // subscription so the in-flow surface below can show every account without
  // borrowing the official Codex session.
  async function nativeUsageStatusAll() {
    try {
      const result = await request("/usage/all");
      return result && Array.isArray(result.accounts) ? result : { accounts: [] };
    } catch {
      return { accounts: [], error: "Relay usage data is temporarily unavailable." };
    }
  }

  function currentThreadId() {
    const observed = readSessionValue(CURRENT_THREAD_KEY, "");
    if (observed) return observed;
    const source = String(globalThis.location?.href || "");
    const match = source.match(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/i);
    return match ? match[0] : "";
  }

  async function routingContext() {
	const status = await request("/router/status");
    const threadId = currentThreadId();
    let thread = null;
    if (threadId) {
      thread = await request(`/thread-route?threadId=${encodeURIComponent(threadId)}`).catch(() => null);
    }
    const query = new URLSearchParams({ limit: "1" });
    if (threadId) query.set("threadId", threadId);
    const decisionResult = await request(`/routing/decisions?${query}`).catch(() => ({ decisions: [] }));
    return { status, thread, threadId, lastDecision: decisionResult?.decisions?.at?.(-1) || null };
  }

  function startRoutingWatcher() {
    if (routingWatcherStarted || typeof globalThis.EventSource !== "function") return;
    routingWatcherStarted = true;
    routingEventSource = new EventSource(`${API}/events?token=${encodeURIComponent(TOKEN)}`);
    routingEventSource.onmessage = (event) => {
      let payload = null;
      try { payload = JSON.parse(event?.data || "null"); } catch { return; }
      const eventType = String(payload?.type || "").replaceAll("-", "_");
      if (!/^(account_updated$|route_|routing_|turn_|quota_circuit_|handoff_|recovery_|all_accounts_|policy_|router_error$)/.test(eventType)) return;
      const reasonCode = payload?.data?.reasonCode || payload?.data?.ReasonCode || "";
      // Request-level protocol errors and recovery markers remain available in
      // the task badge, Usage & billing, and Router status surfaces. They must
      // never become global desktop toasts: opening the OAuth browser, closing
      // a window, or reconnecting the renderer can emit a harmless native
      // -32600 wrapper and repeatedly cover unrelated work. Reserve toasts for
      // explicit routing transitions that need immediate user attention.
      const shouldNotify = eventType === "handoff_failed" || eventType === "all_accounts_depleted" || eventType === "policy_downgraded" || (eventType === "handoff_committed" && reasonCode === "handoff_quota_exhausted");
      if (payload?.id && shouldNotify && rememberRoutingEvent(payload.id)) {
        const detail = payload?.message || [payload?.previousAccountId, payload?.accountId].filter(Boolean).join(" → ");
        const title = eventType === "router_error"
          ? routingText("relayError")
          : eventType === "handoff_committed"
          ? routingText("handoffComplete")
          : eventType === "handoff_failed" ? routingText("handoffFailed")
            : eventType === "all_accounts_depleted" ? routingText("allDepleted")
              : eventType === "policy_downgraded" ? routingText("policyDowngraded") : routingText("recoveryNotice");
        showActionToast(title, detail || routingText("title"), eventType !== "handoff_committed");
      }
      const menu = document.getElementById?.(MENU_ID);
      if (menu?.isConnected) void refreshMenu(menu);
      const fallbackMenu = document.getElementById?.(FALLBACK_MENU_ID);
      if (fallbackMenu?.isConnected) void refreshMenu(fallbackMenu);
      const fallbackTrigger = document.getElementById?.(FALLBACK_TRIGGER_ID);
      if (fallbackTrigger?.isConnected) void refreshFallbackPoolTrigger(fallbackTrigger);
      const badge = document.getElementById?.(TASK_ROUTE_BADGE_ID);
      if (badge?.isConnected) void refreshTaskRouteBadge(badge);
    };
  }

  function rememberRoutingEvent(eventId) {
    const id = String(eventId || "").trim();
    if (!id) return false;
    let known = [];
    try { known = JSON.parse(readSessionValue(ROUTING_EVENT_IDS_KEY, "[]")); } catch { known = []; }
    if (!Array.isArray(known)) known = [];
    if (known.includes(id)) return false;
    known.push(id);
    writeSessionValue(ROUTING_EVENT_IDS_KEY, JSON.stringify(known.slice(-100)));
    return true;
  }

  function routeWorkerLabel(worker, accountsById) {
    if (!worker) return routingText("unavailable");
    const account = accountsById.get(worker.accountId);
    if (account) return accountName(account);
    const name = worker.displayName || worker.label || worker.accountId || routingText("unavailable");
    return worker.planLabel ? `${name} · ${worker.planLabel}` : name;
  }

  function routeReasonLabel(code) {
    const vi = String(globalThis.navigator?.language || "en").toLowerCase().startsWith("vi");
    const labels = {
      selected_highest_score: ["Selected: highest available score", "Được chọn: điểm khả dụng cao nhất"],
      selected_sticky_owner: ["Selected: Sticky owner", "Được chọn: tài khoản giữ task ở chế độ Bám"],
      eligible_lower_score: ["Eligible, lower score", "Có thể dùng nhưng điểm thấp hơn"],
      skipped_disabled: ["Skipped: disabled", "Bỏ qua: đã tắt"],
      skipped_disconnected: ["Skipped: disconnected", "Bỏ qua: chưa kết nối"],
      skipped_incompatible: ["Skipped: incompatible account", "Bỏ qua: tài khoản không tương thích"],
      skipped_open_circuit: ["Skipped: quota cooldown", "Bỏ qua: đang chờ quota hồi phục"],
      skipped_cooldown: ["Skipped: cooldown active", "Bỏ qua: thời gian chờ vẫn còn hiệu lực"],
      skipped_depleted: ["Skipped: depleted", "Bỏ qua: đã hết quota"],
      skipped_unknown_quota: ["Skipped: quota not confirmed", "Bỏ qua: quota chưa được xác nhận"],
      skipped_stale_quota: ["Skipped: stale quota", "Bỏ qua: dữ liệu quota đã cũ"],
      skipped_low_water_reserve: ["Skipped: reserve threshold", "Bỏ qua: chạm ngưỡng dự phòng"],
      handoff_quota_exhausted: ["Moved after quota exhaustion", "Chuyển vì tài khoản trước hết quota"],
      handoff_balanced_boundary: ["Moved at a safe balancing boundary", "Chuyển tại ranh giới an toàn để cân bằng"],
      handoff_rotate_boundary: ["Moved at a safe rotation boundary", "Chuyển tại ranh giới luân phiên an toàn"],
      policy_downgraded_unknown_profile: ["Sticky fallback: compatibility not verified", "Tạm dùng chế độ Bám: chưa xác minh tương thích"],
    };
    const value = labels[String(code || "")];
    return value ? value[vi ? 1 : 0] : String(code || routingText("unavailable")).replaceAll("_", " ");
  }

  function routeItem(key, value, className = "") {
    const item = make("div", `codex-mux-win-task-route-item ${className}`.trim());
    const output = String(value ?? routingText("unavailable"));
    const valueNode = make("span", "codex-mux-win-task-route-value", output);
    valueNode.title = output;
    append(item, make("span", "codex-mux-win-task-route-key", key), valueNode);
    return item;
  }

  function renderTaskRouteBadge(badge, accounts, routing) {
    badge.replaceChildren();
    if (!routing?.status || !routing?.thread?.route) return;
    if (Number(routing.status.contractVersion) >= 2) {
      const route = routing.thread.route;
	      const pool = poolPresentation(accounts, routing.status);
	      const status = routing.status.pool || routing.thread.pool || {};
      const compact = make("div", "codex-mux-win-task-route-compact");
      compact.append(
        routeItem(routingText("runningVia"), `${routingText("poolTitle")} · ${routingText("generation")} ${route.generation}`),
        routeItem(routingText("mode"), routingText("sharedPool")),
        routeItem(routingText("confirmedRemaining"), `${Math.round(pool.remaining)}% / ${Math.round(pool.maximum)}%`),
        routeItem(routingText("poolHealth"), String(status.health || "warming")),
      );
      if (route.recoveryRequired) compact.append(routeItem("", routingText("recovery"), "codex-mux-win-task-route-recovery"));
      badge.append(compact);
      return;
    }
    const byId = new Map((Array.isArray(accounts) ? accounts : []).map((account) => [account.id, account]));
    const thread = routing.thread;
    const route = thread.route;
    const current = byId.get(route.accountId);
    const runningWorker = thread.activeWorker || thread.currentOwner || (current ? { accountId: current.id } : { accountId: route.accountId });
    const runningStatus = (Array.isArray(thread.workers) ? thread.workers : []).find((worker) => worker.accountId === runningWorker?.accountId);
    const next = routing.thread.nextCandidate || routing.status.nextCandidate;
    const compact = make("div", "codex-mux-win-task-route-compact");
    compact.append(
      routeItem(routingText("runningVia"), `${routeWorkerLabel(runningWorker, byId)} · ${routingText("generation")} ${route.generation}`),
      routeItem(routingText("mode"), thread.effectivePolicy || routing.status.effectivePolicy || routing.status.policy),
    );
    if (!thread.activeWorker) compact.append(routeItem("", routingText("noActiveTurn"), "codex-mux-win-task-route-muted"));
    if (runningStatus?.quotaKnown && Number.isFinite(Number(runningStatus.confirmedRemainingPercent))) compact.append(routeItem(routingText("confirmedRemaining"), `${Math.round(Number(runningStatus.confirmedRemainingPercent))}%`));
    if (thread.lastCompletedWorker) compact.append(routeItem(routingText("lastUsed"), routeWorkerLabel(thread.lastCompletedWorker, byId)));
    if (next) {
      const nextAccount = byId.get(next.accountId);
      const quota = next.quotaKnown ? ` · ${Math.round(next.remainingPercent)}% ${routingText("left")}` : ` · ${routingText("probation")}`;
      compact.append(routeItem(routingText("nextPreview"), `${nextAccount ? accountName(nextAccount) : next.label || next.accountId}${quota}`));
    }
    badge.append(compact);
    const handoffs = Array.isArray(routing.thread.handoffs) ? routing.thread.handoffs : [];
    const latestHandoff = handoffs.at(-1);
    if (latestHandoff && !["FAILED", "ROLLED_BACK"].includes(latestHandoff.phase)) {
      const from = byId.get(latestHandoff.sourceAccountId);
      const to = byId.get(latestHandoff.targetAccountId);
      compact.append(routeItem(routingText("handoff"), `${from ? accountName(from) : latestHandoff.sourceAccountId} → ${to ? accountName(to) : latestHandoff.targetAccountId} · ${latestHandoff.phase}`));
      if (latestHandoff.reasonCode) compact.append(routeItem("", routeReasonLabel(latestHandoff.reasonCode)));
      if (latestHandoff.sourceGeneration || latestHandoff.targetGeneration) compact.append(routeItem(routingText("generation"), `${latestHandoff.sourceGeneration || "?"} → ${latestHandoff.targetGeneration || "?"}`));
    }
    if (route.recoveryRequired) compact.append(routeItem("", routingText("recovery"), "codex-mux-win-task-route-recovery"));

    const details = make("details", "codex-mux-win-task-route-details");
    details.append(make("summary", "codex-mux-win-task-route-summary", routingText("details")));
    const facts = make("div", "codex-mux-win-task-route-facts");
    const attribution = thread.quotaAttribution || {};
    const attributionDelta = Number.isFinite(Number(attribution.deltaPercent)) ? ` · -${Number(attribution.deltaPercent).toFixed(2)}%` : "";
    const quotaWhen = thread.quotaSnapshotAt ? new Date(thread.quotaSnapshotAt).toLocaleString() : routingText("unavailable");
    const reservation = thread.reservation
      ? `${routeWorkerLabel({ accountId: thread.reservation.accountId }, byId)} · ${Number(thread.reservation.weight || 0).toFixed(2)}`
      : routingText("unavailable");
    facts.append(
      routeItem(routingText("currentOwner"), routeWorkerLabel(thread.currentOwner || { accountId: route.accountId }, byId)),
      routeItem(routingText("activeWorker"), thread.activeWorker ? routeWorkerLabel(thread.activeWorker, byId) : routingText("noActiveTurn")),
      routeItem(routingText("lastUsed"), routeWorkerLabel(thread.lastCompletedWorker, byId)),
      routeItem(routingText("lastQuota"), routeWorkerLabel(thread.lastQuotaConsumingWorker, byId)),
      routeItem(routingText("previousWorker"), routeWorkerLabel(thread.previousWorker, byId)),
      routeItem(routingText("requestedMode"), thread.requestedPolicy || routing.status.policy),
      routeItem(routingText("effective"), thread.effectivePolicy || routing.status.effectivePolicy),
      routeItem(routingText("quotaFreshness"), `${thread.quotaFreshness || "unknown"} · ${quotaWhen}`),
      routeItem(routingText("quotaAttribution"), `${attribution.status || "unavailable"}${attributionDelta}`),
      routeItem(routingText("reservation"), reservation),
      routeItem(routingText("decisionId"), thread.schedulerDecisionId || routingText("unavailable")),
    );
    details.append(facts);

    const why = make("section", "codex-mux-win-task-route-section");
    why.append(make("h4", "", routingText("why")));
    const workers = Array.isArray(thread.workers) ? thread.workers : [];
    const workerList = make("div", "codex-mux-win-task-route-workers");
    for (const worker of workers) {
      const quota = worker.quotaKnown && Number.isFinite(Number(worker.confirmedRemainingPercent))
        ? `${Math.round(Number(worker.confirmedRemainingPercent))}%`
        : routingText("probation");
      const score = Number.isFinite(Number(worker?.scoreComponents?.finalScore)) ? ` · ${routingText("score")} ${Number(worker.scoreComponents.finalScore).toFixed(2)}` : "";
      const row = make("div", "codex-mux-win-task-route-worker");
      append(row, make("div", "codex-mux-win-task-route-worker-name", routeWorkerLabel(worker, byId)), make("div", "codex-mux-win-task-route-worker-reason", `${routeReasonLabel(worker.reasonCode)} · ${quota}${score}`));
      workerList.append(row);
    }
    if (!workers.length) workerList.append(make("div", "codex-mux-win-task-route-muted", routingText("unavailable")));
    why.append(workerList);
    details.append(why);

    const timeline = make("section", "codex-mux-win-task-route-section");
    timeline.append(make("h4", "", routingText("timeline")));
    const timelineList = make("ol", "codex-mux-win-task-route-timeline");
    const events = (Array.isArray(thread.timeline) ? thread.timeline : []).slice(-12).reverse();
    for (const event of events) {
      const worker = event.targetAccountId || event.accountId;
      const identity = worker ? routeWorkerLabel({ accountId: worker }, byId) : "";
      const when = event.timestamp ? new Date(event.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "";
      const item = make("li", "");
      append(item, make("span", "codex-mux-win-task-route-event", String(event.type || "event").replaceAll("_", " ")), make("span", "codex-mux-win-task-route-event-meta", [when, identity, routeReasonLabel(event.reasonCode)].filter(Boolean).join(" · ")));
      timelineList.append(item);
    }
    if (!events.length) timelineList.append(make("li", "codex-mux-win-task-route-muted", routingText("noEvents")));
    timeline.append(timelineList);
    details.append(timeline);

    const pool = poolPresentation(accounts, thread);
    const poolSection = make("section", "codex-mux-win-task-route-section");
    const poolUpdated = thread.pool?.quotaUpdatedAt ? new Date(thread.pool.quotaUpdatedAt).toLocaleString() : routingText("unavailable");
    poolSection.append(
      make("h4", "", routingText("poolSummary")),
      make("div", "", `${Math.round(pool.remaining)}% ${routingText("confirmedRemaining")} / ${Math.round(pool.maximum)}% ${routingText("poolMaximum")}`),
      make("div", "", `${pool.available}/${pool.connected} ${routingText("eligible")} · ${Number(thread.pool?.depletedSubscriptions || 0)} ${routingText("depleted")} · ${Number(thread.pool?.unknownSubscriptions || 0)} ${routingText("unknownQuota")}`),
      make("div", "codex-mux-win-task-route-event-meta", `${routingText("updated")}: ${poolUpdated}`),
      make("p", "codex-mux-win-task-route-note", routingText("routingOnly")),
    );
    details.append(poolSection);
    badge.append(details);
  }

  async function refreshTaskRouteBadge(badge) {
    badge.dataset.codexMuxLastRefresh = String(Date.now());
    try {
      const [accounts, routing] = await Promise.all([loadAccounts(), routingContext()]);
      renderTaskRouteBadge(badge, accounts, routing);
    } catch {
      // The native task view remains untouched when the local control API is unavailable.
    }
  }

  function taskRoutePlacement() {
    const inputs = [...(document.querySelectorAll?.('textarea, [contenteditable="true"]') || [])].filter(isVisible);
    const input = inputs.at(-1);
    if (!input) return null;
    const anchor = input.closest?.("form") || input.parentElement;
    return anchor?.parentElement ? { parent: anchor.parentElement, before: anchor } : null;
  }

  function installTaskRouteBadge() {
    let badge = document.getElementById?.(TASK_ROUTE_BADGE_ID);
    if (!currentThreadId()) {
      badge?.remove();
      return;
    }
    const placement = taskRoutePlacement();
    if (!placement) return;
    if (!badge || badge.parentElement !== placement.parent) {
      badge?.remove();
      badge = make("section", "", "");
      badge.id = TASK_ROUTE_BADGE_ID;
      badge.setAttribute("aria-label", routingText("title"));
      placement.parent.insertBefore(badge, placement.before);
    }
    const lastRefresh = Number(badge.dataset.codexMuxLastRefresh || 0);
    if (Date.now() - lastRefresh > 5000) void refreshTaskRouteBadge(badge);
  }

  function rateLimitResets(accountId) {
    return request(`/accounts/${encodeURIComponent(accountId)}/rate-limit-resets`);
  }

  function consumeRateLimitReset(accountId, input) {
    return request(`/accounts/${encodeURIComponent(accountId)}/rate-limit-resets/consume`, {
      method: "POST",
      body: JSON.stringify({
        creditId: input?.creditId ?? null,
        redeemRequestId: input?.redeemRequestId,
      }),
    });
  }

  function newRedeemRequestId() {
    try {
      if (typeof globalThis.crypto?.randomUUID === "function") return globalThis.crypto.randomUUID();
    } catch {
      // The embedded Windows renderer can expose a restricted crypto object.
    }
    return `codex-mux-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }

  function hasPendingLogin(account) {
    return account?.pendingLogin === true || account?.pendingLogin === "true";
  }

  function isRelayPrimary(account) {
    // Newer control responses mark the actual one task authority explicitly.
    // Keep the id fallback for older installed bridges and unit fixtures.
    if (account && Object.prototype.hasOwnProperty.call(account, "relayAuthority")) {
      return account.relayAuthority === true;
    }
    return account?.id === "primary";
  }

  function isRelayHost(account) {
    return account?.id === "primary";
  }

  function usageWindows(rateLimits) {
    return [rateLimits?.primary, rateLimits?.secondary]
      .filter(Boolean)
      .map((window) => {
        const usedPercent = Number(window.usedPercent);
        if (!Number.isFinite(usedPercent)) return null;
        const duration = Number(window.windowDurationMins);
        return {
          usedPercent: Math.max(0, Math.min(100, usedPercent)),
          remainingPercent: Math.max(0, Math.min(100, 100 - usedPercent)),
          windowMinutes: Number.isFinite(duration) && duration > 0 ? duration : 0,
          resetsAt: window.resetsAt ?? null,
        };
      })
      .filter(Boolean);
  }

  function shortestUsageWindow(account) {
    return usageWindows(account?.rateLimits)
      .sort((left, right) => left.windowMinutes - right.windowMinutes)[0] || null;
  }

  function shortestRemainingUsage(account) {
    return shortestUsageWindow(account)?.remainingPercent ?? null;
  }

  function selectedResetUsageWindows() {
    const account = connectedAccounts(latestAccounts).find(
      (item) => item.id === getResetAccountId(),
    );
    return account ? usageWindows(account.rateLimits) : null;
  }

  function longestUsageWindow(account) {
    const windows = usageWindows(account?.rateLimits)
      .sort((left, right) => left.windowMinutes - right.windowMinutes);
    return windows.at(-1) || null;
  }

  function remainingUsage(account) {
    const windows = usageWindows(account?.rateLimits);
    if (windows.length === 0) return null;
    // The Relay scheduler treats a subscription as available only up to the
    // tighter of its short (5-hour) and long (weekly) windows. Keep every
    // account row and the additive pool on that same effective capacity so a
    // popup cannot briefly show 408/500 (weekly-only) before settling on the
    // scheduler's 350/500 (short-window constrained) value.
    return windows.reduce((remaining, window) => Math.min(remaining, window.remainingPercent), 100);
  }

  function accountAuthInvalidated(account) {
    return String(account?.rateLimitError || "").toLowerCase().includes("authentication")
      || String(account?.error || "").toLowerCase().includes("authentication");
  }

  function usageLabel(account) {
    const remaining = remainingUsage(account);
    if (remaining != null) return `${Math.round(remaining)}% left`;
    if (account?.error) return "Account status unavailable";
    if (account?.rateLimitError) return accountAuthInvalidated(account)
      ? "Sign in required"
      : "Quota pending verification";
    return "Updating quota…";
  }

  function formatWindowDuration(minutes) {
    const value = Number(minutes);
    if (!Number.isFinite(value) || value <= 0) return "quota";
    if (value % 10080 === 0) return `${value / 10080}w`;
    if (value % 1440 === 0) return `${value / 1440}d`;
    if (value % 60 === 0) return `${value / 60}h`;
    return `${value}m`;
  }

  function formatResetCountdown(resetsAt, now = Date.now()) {
    const seconds = Number(resetsAt);
    if (!Number.isFinite(seconds) || seconds <= 0) return null;
    const remainingMinutes = Math.ceil((seconds * 1000 - now) / 60000);
    if (remainingMinutes <= 0) return "now";
    if (remainingMinutes < 60) return `${remainingMinutes}m`;
    const hours = Math.floor(remainingMinutes / 60);
    const minutes = remainingMinutes % 60;
    if (hours < 48) return minutes ? `${hours}h ${minutes}m` : `${hours}h`;
    const days = Math.floor(hours / 24);
    const leftoverHours = hours % 24;
    return leftoverHours ? `${days}d ${leftoverHours}h` : `${days}d`;
  }

  function quotaResetSummary(account) {
    const windows = usageWindows(account?.rateLimits)
      .filter((window) => Number.isFinite(Number(window.resetsAt)) && Number(window.resetsAt) > 0)
      .sort((left, right) => Number(left.resetsAt) - Number(right.resetsAt));
    if (windows.length === 0) {
      return accountAuthInvalidated(account)
        ? "Sign in again to refresh"
        : account?.rateLimitError ? "Reset time unavailable" : "Reset time not reported";
    }
    return `Reset ${windows.map((window) => `${formatWindowDuration(window.windowMinutes)}: ${formatResetCountdown(window.resetsAt)}`).join(" · ")}`;
  }

  function quotaResetTitle(account) {
    const windows = usageWindows(account?.rateLimits)
      .filter((window) => Number.isFinite(Number(window.resetsAt)) && Number(window.resetsAt) > 0)
      .sort((left, right) => Number(left.resetsAt) - Number(right.resetsAt));
    if (windows.length === 0) return quotaResetSummary(account);
    return windows.map((window) => {
      const date = new Date(Number(window.resetsAt) * 1000);
      return `${formatWindowDuration(window.windowMinutes)}: ${date.toLocaleString()}`;
    }).join(" · ");
  }

  function usageStatus(account) {
    const windows = usageWindows(account?.rateLimits);
    const quota = windows.length > 0
      ? `${Math.round(remainingUsage(account))}% quota left`
      : account?.error ? "Account status unavailable"
        : accountAuthInvalidated(account)
          ? "Sign in required"
          : account?.rateLimitError ? "Quota data unavailable" : "Quota data is updating";
    return `${quota} · ${quotaResetSummary(account)}`;
  }

  function accountDisplayName(account) {
    return [account?.displayName, account?.username, account?.email, account?.label]
      .map((value) => String(value || "").trim())
      .find(Boolean) || "Subscription";
  }

  function accountIdentityDetail(account) {
    const displayName = accountDisplayName(account);
    return [account?.email, account?.username, account?.label]
      .map((value) => String(value || "").trim())
      .filter((value, index, values) => value && value !== displayName && values.indexOf(value) === index)
      .join(" · ");
  }

  function accountName(account) {
    const name = accountDisplayName(account);
    return account?.planLabel ? `${name} · ${account.planLabel}` : name;
  }

  function initials(label) {
    const letters = String(label || "?")
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join("");
    return letters || "?";
  }

  function avatar(account) {
    const shell = make("span", "codex-mux-win-avatar");
    shell.setAttribute("aria-hidden", "true");
    if (account.profileImageUrl) {
      const image = document.createElement("img");
      image.src = account.profileImageUrl;
      image.alt = "";
      image.referrerPolicy = "no-referrer";
      image.addEventListener("error", () => {
      image.replaceWith(document.createTextNode(initials(accountDisplayName(account))));
      });
      shell.append(image);
    } else {
      shell.textContent = initials(accountDisplayName(account));
    }
    return shell;
  }

  function row(account, onCancelPending) {
    const usage = remainingUsage(account);
    const line = make("div", "codex-mux-win-row");
    const identity = make("div", "codex-mux-win-identity");
    const labels = make("div", "codex-mux-win-labels");
    const primary = make("div", "codex-mux-win-name", accountName(account));
    const identityDetail = accountIdentityDetail(account);
    const connectedStatus = account?.error
      ? `Error: ${account.error}`
      : account.connected
        ? `${account.controller ? "Primary · " : ""}${usageStatus(account)}`
        : isRelayHost(account)
          ? "Relay host · separate from Codex"
          : hasPendingLogin(account) ? "Waiting for sign-in" : "Not connected";
    const secondary = make("div", "codex-mux-win-subtext", connectedStatus);
    append(labels, primary, identityDetail ? make("div", "codex-mux-win-account-id", identityDetail) : null, secondary);
    append(identity, avatar(account), labels);
    line.title = quotaResetTitle(account);
    line.append(identity);
    if (!account.connected && !account.controller && !isRelayHost(account) && hasPendingLogin(account) && typeof onCancelPending === "function") {
      const cancel = make("button", "codex-mux-win-pending-cancel", "Cancel sign-in");
      cancel.type = "button";
      cancel.setAttribute("aria-label", `Cancel sign-in for ${account.label || "subscription"}`);
      cancel.addEventListener("click", (event) => {
        event.preventDefault();
        event.stopPropagation();
        onCancelPending(cancel);
      });
      line.append(cancel);
    } else {
      line.append(make(
        "div",
        `codex-mux-win-percent${usage == null ? " codex-mux-win-percent-muted" : ""}`,
        isRelayPrimary(account) && !account.connected ? "Relay only" : usageLabel(account),
      ));
    }
    return line;
  }

  function addStyles() {
    if (document.getElementById(STYLE_ID)) return;
    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = `
      #${MENU_ID} { box-sizing: border-box; color: var(--text-primary, var(--token-text-primary, inherit)); font-family: inherit; }
      #${MENU_ID} *, #${MENU_ID} *::before, #${MENU_ID} *::after { box-sizing: border-box; }
      #${FALLBACK_TRIGGER_ID} { display: flex; width: calc(100% - 16px); height: 40px; min-height: 40px; align-items: center; gap: 9px; margin: 0 8px; overflow: hidden; border: 0; border-radius: 9px; background: transparent; color: inherit; cursor: pointer; font: inherit; padding: 7px 9px; text-align: left; }
      #${FALLBACK_TRIGGER_ID}:hover, #${FALLBACK_TRIGGER_ID}:focus-visible, #${FALLBACK_TRIGGER_ID}[aria-expanded="true"] { background: color-mix(in srgb, currentColor 9%, transparent); outline: none; }
      #${FALLBACK_TRIGGER_ID} .codex-mux-win-summary-label { display: block; min-width: 0; }
      .codex-mux-win-footer-chevron { display: grid; width: 22px; height: 22px; flex: 0 0 22px; margin-left: auto; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 14px; opacity: .78; transition: transform 120ms ease; }
      #${FALLBACK_TRIGGER_ID}[aria-expanded="true"] .codex-mux-win-footer-chevron { transform: rotate(180deg); }
      #${FALLBACK_MENU_ID} { position: fixed; z-index: 2147483645; box-sizing: border-box; max-height: min(620px, calc(100vh - 90px)); overflow: auto; border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 14px; background: var(--main-surface-background, var(--token-main-surface-background, #292929)); box-shadow: 0 18px 52px rgb(0 0 0 / .4); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 8px; }
      .codex-mux-win-divider { height: 1px; margin: 6px 8px; background: color-mix(in srgb, currentColor 18%, transparent); opacity: .65; }
      .codex-mux-win-summary, .codex-mux-win-row, .codex-mux-win-add { display: flex; align-items: center; min-height: 42px; gap: 10px; margin: 0 3px; padding: 7px 9px; border-radius: 9px; color: inherit; }
      .codex-mux-win-summary { cursor: default; }
      .codex-mux-win-summary-icon { display: grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); }
      .codex-mux-win-summary-label { flex: 1; min-width: 0; }
      .codex-mux-win-title, .codex-mux-win-name { overflow: hidden; color: inherit; font-size: 14px; font-weight: 500; line-height: 18px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-subtext { overflow: hidden; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 16px; text-overflow: ellipsis; white-space: nowrap; opacity: .8; }
      .codex-mux-win-account-id { overflow: hidden; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; text-overflow: ellipsis; white-space: nowrap; opacity: .68; }
      .codex-mux-win-total, .codex-mux-win-percent { margin-left: auto; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 14px; font-variant-numeric: tabular-nums; opacity: .82; }
      .codex-mux-win-percent-muted { max-width: 112px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; text-align: right; white-space: normal; }
      .codex-mux-win-row { cursor: default; }
      .codex-mux-win-identity { display: flex; min-width: 0; flex: 1; align-items: center; gap: 10px; }
      .codex-mux-win-labels { min-width: 0; }
      .codex-mux-win-avatar { display: grid; width: 24px; height: 24px; flex: 0 0 24px; place-items: center; overflow: hidden; border-radius: 50%; background: color-mix(in srgb, #5b7cfa 45%, transparent); color: white; font-size: 9px; font-weight: 700; }
      .codex-mux-win-avatar img { width: 100%; height: 100%; object-fit: cover; }
      .codex-mux-win-pending-cancel { margin-left: auto; border: 0; border-radius: 7px; background: rgb(255 255 255 / .12); color: inherit; cursor: pointer; font: inherit; font-size: 12px; padding: 5px 8px; }
      .codex-mux-win-pending-cancel:hover, .codex-mux-win-pending-cancel:focus-visible { background: rgb(255 255 255 / .2); outline: none; }
      .codex-mux-win-pending-cancel:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-add { width: calc(100% - 6px); border: 0; background: transparent; cursor: pointer; font: inherit; text-align: left; }
      .codex-mux-win-add:hover, .codex-mux-win-add:focus-visible { background: color-mix(in srgb, currentColor 9%, transparent); outline: none; }
      .codex-mux-win-add:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-plus { display: inline-grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 23px; font-weight: 300; line-height: 1; }
      .codex-mux-win-settings { width: calc(100% - 6px); border: 0; background: transparent; cursor: pointer; font: inherit; text-align: left; }
      .codex-mux-win-settings:hover, .codex-mux-win-settings:focus-visible { background: color-mix(in srgb, currentColor 9%, transparent); outline: none; }
      .codex-mux-win-settings-icon { display: inline-grid; width: 21px; height: 21px; place-items: center; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 17px; }
      .codex-mux-win-modal-manager { width: min(560px, 100%); max-height: min(720px, calc(100vh - 48px)); overflow: auto; }
      .codex-mux-win-account-list { display: grid; gap: 8px; margin-top: 16px; }
      .codex-mux-win-account-card { display: grid; min-width: 0; gap: 10px; border: 1px solid color-mix(in srgb, currentColor 14%, transparent); border-radius: 12px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 10px; }
      .codex-mux-win-account-card .codex-mux-win-identity { min-width: 0; }
      .codex-mux-win-account-card-actions { display: flex; flex: 0 0 auto; flex-wrap: wrap; justify-content: flex-end; gap: 6px; }
      .codex-mux-win-account-action { min-height: 30px; border: 0; border-radius: 7px; background: rgb(255 255 255 / .12); color: inherit; cursor: pointer; font: inherit; font-size: 12px; padding: 6px 9px; }
      .codex-mux-win-account-action:hover, .codex-mux-win-account-action:focus-visible { background: rgb(255 255 255 / .2); outline: none; }
      .codex-mux-win-account-action-primary { background: #4f6bed; color: white; }
      .codex-mux-win-account-action-danger { background: color-mix(in srgb, #d95757 38%, transparent); }
      .codex-mux-win-account-action:disabled { cursor: wait; opacity: .55; }
      .codex-mux-win-account-badge { display: inline-flex; align-items: center; margin-left: 5px; border-radius: 999px; background: color-mix(in srgb, #6e86f7 32%, transparent); color: inherit; font-size: 10px; line-height: 16px; padding: 0 6px; vertical-align: 1px; }
      .codex-mux-win-account-meta { min-width: 0; flex: 1; }
      .codex-mux-win-account-meta .codex-mux-win-name { white-space: normal; }
      .codex-mux-win-account-hint { margin-top: 3px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; opacity: .82; }
      .codex-mux-win-close-button { margin-left: auto; }
      .codex-mux-win-error { margin: 4px 11px 8px; color: #e05a65; font-size: 12px; line-height: 16px; }
      .codex-mux-win-modal-backdrop { position: fixed; z-index: 2147483647; inset: 0; display: grid; place-items: center; padding: 24px; background: rgb(0 0 0 / .48); }
      .codex-mux-win-modal { width: min(420px, 100%); border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 16px; background: var(--main-surface-background, var(--token-main-surface-background, #292929)); box-shadow: 0 20px 60px rgb(0 0 0 / .42); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 20px; }
      .codex-mux-win-manager-header { display: flex; align-items: flex-start; gap: 8px; }
      .codex-mux-win-manager-header h2 { flex: 1; }
      .codex-mux-win-modal h2 { margin: 0; font-size: 17px; line-height: 24px; }
      .codex-mux-win-modal p { margin: 8px 0 0; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 14px; line-height: 20px; }
      .codex-mux-win-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 18px; }
      .codex-mux-win-actions button, .codex-mux-win-actions a { display: inline-flex; min-height: 34px; align-items: center; justify-content: center; border: 0; border-radius: 8px; background: rgb(255 255 255 / .13); color: inherit; cursor: pointer; font: inherit; font-size: 13px; padding: 7px 12px; text-decoration: none; }
      .codex-mux-win-actions button:hover, .codex-mux-win-actions a:hover { background: rgb(255 255 255 / .2); }
      .codex-mux-win-actions .codex-mux-win-primary { background: #4f6bed; color: white; }
      .codex-mux-win-status { min-height: 20px; margin-top: 12px; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 12px; }
      .codex-mux-win-route-panel { margin: 10px 12px 4px; padding: 10px; border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 10px; background: color-mix(in srgb, currentColor 4%, transparent); font-size: 11px; }
      #${TASK_ROUTE_BADGE_ID} { display: block; width: min(760px, calc(100% - 24px)); margin: 0 auto 8px; border: 1px solid color-mix(in srgb, currentColor 16%, transparent); border-radius: 10px; background: color-mix(in srgb, currentColor 4%, transparent); color: var(--text-primary, var(--token-text-primary, inherit)); padding: 7px 10px; font-size: 11px; line-height: 16px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-compact { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-item { display: flex; min-width: 0; gap: 5px; overflow: hidden; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-key { margin-right: 5px; color: var(--text-secondary, var(--token-text-secondary, #aaa)); }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-value { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-recovery { color: #e9a23b; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-muted { color: var(--text-secondary, var(--token-text-secondary, #aaa)); }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-details { margin-top: 5px; border-top: 1px solid color-mix(in srgb, currentColor 12%, transparent); padding-top: 4px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-summary { width: fit-content; border-radius: 5px; color: var(--text-secondary, var(--token-text-secondary, #aaa)); cursor: pointer; font-weight: 600; padding: 2px 4px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-summary:hover, #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-summary:focus-visible { background: color-mix(in srgb, currentColor 9%, transparent); color: inherit; outline: none; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 5px 16px; margin-top: 7px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-section { margin-top: 9px; border-top: 1px solid color-mix(in srgb, currentColor 10%, transparent); padding-top: 8px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-section h4 { margin: 0 0 5px; font-size: 11px; line-height: 16px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-workers { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 5px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-worker { min-width: 0; border: 1px solid color-mix(in srgb, currentColor 10%, transparent); border-radius: 7px; padding: 5px 7px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-worker-name { overflow: hidden; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-worker-reason, #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-event-meta, #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-note { color: var(--text-secondary, var(--token-text-secondary, #aaa)); }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-timeline { display: grid; max-height: 150px; gap: 4px; margin: 0; overflow: auto; padding-left: 20px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-timeline li { padding-left: 2px; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-event { margin-right: 5px; font-weight: 600; }
      #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-note { margin: 3px 0 0; }
      @media (max-width: 640px) { #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-facts, #${TASK_ROUTE_BADGE_ID} .codex-mux-win-task-route-workers { grid-template-columns: minmax(0, 1fr); } }
      .codex-mux-win-route-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; min-width: 0; }
      .codex-mux-win-route-row + .codex-mux-win-route-row { margin-top: 6px; }
      .codex-mux-win-route-key { color: var(--text-secondary, var(--token-text-secondary, #aaa)); }
      .codex-mux-win-route-value { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: right; }
      .codex-mux-win-policy-actions { display: flex; gap: 4px; margin-top: 8px; }
      .codex-mux-win-policy-button { flex: 1; border: 1px solid color-mix(in srgb, currentColor 18%, transparent); border-radius: 7px; padding: 5px 3px; background: transparent; color: inherit; font: inherit; cursor: pointer; }
      .codex-mux-win-policy-button[aria-pressed="true"] { background: color-mix(in srgb, #5b78ff 30%, transparent); border-color: #6680ff; }
      .codex-mux-win-toast { position: fixed; z-index: 2147483646; top: 20px; right: 20px; display: flex; width: min(380px, calc(100vw - 32px)); align-items: flex-start; gap: 11px; border: 1px solid color-mix(in srgb, #55c982 58%, transparent); border-radius: 13px; background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #164a2c); box-shadow: 0 16px 45px rgb(0 0 0 / .35); color: var(--text-primary, var(--token-text-primary, #f7f7f7)); padding: 13px 14px; }
      .codex-mux-win-toast-icon { display: grid; width: 22px; height: 22px; flex: 0 0 22px; place-items: center; border-radius: 50%; background: #2da861; color: white; font-size: 14px; font-weight: 800; }
      .codex-mux-win-toast-copy { min-width: 0; flex: 1; }
      .codex-mux-win-toast-title { font-size: 14px; font-weight: 650; line-height: 19px; }
      .codex-mux-win-toast-detail { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 12px; line-height: 17px; }
      .codex-mux-win-toast-close { width: 24px; height: 24px; border: 0; border-radius: 7px; background: transparent; color: inherit; cursor: pointer; font-size: 18px; line-height: 20px; opacity: .75; }
      .codex-mux-win-toast-close:hover, .codex-mux-win-toast-close:focus-visible { background: rgb(255 255 255 / .12); opacity: 1; outline: none; }
      .codex-mux-win-toast-neutral { border-color: color-mix(in srgb, #8ea0b8 50%, transparent); background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #273445); }
      .codex-mux-win-toast-neutral .codex-mux-win-toast-icon { background: #64748b; }
      .codex-mux-win-update-toast { border-color: color-mix(in srgb, #6e86f7 70%, transparent); background: color-mix(in srgb, var(--main-surface-background, #292929) 94%, #28375f); }
      .codex-mux-win-update-toast .codex-mux-win-toast-icon { background: #5873e8; }
      .codex-mux-win-update-actions { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 9px; }
      .codex-mux-win-update-button { min-height: 30px; border: 0; border-radius: 7px; background: #5873e8; color: white; cursor: pointer; font: inherit; font-size: 12px; font-weight: 600; padding: 6px 10px; }
      .codex-mux-win-update-button:hover, .codex-mux-win-update-button:focus-visible { background: #6e86f7; outline: none; }
      .codex-mux-win-update-button:disabled { cursor: wait; opacity: .65; }
      codex-mux-profile-picker, codex-mux-plugin-picker, codex-mux-reset-picker { display: block; }
      .codex-mux-win-surface { margin: 0 0 16px; border: 1px solid color-mix(in srgb, currentColor 16%, transparent); border-radius: 14px; background: color-mix(in srgb, currentColor 3%, transparent); color: var(--text-primary, var(--token-text-primary, inherit)); padding: 12px; }
      .codex-mux-win-surface-title { font-size: 14px; font-weight: 650; line-height: 19px; }
      .codex-mux-win-surface-description { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 17px; opacity: .85; }
      .codex-mux-win-avatar-stack { display: flex; align-items: center; min-height: 48px; margin-top: 12px; padding-left: 2px; }
      .codex-mux-win-avatar-button { position: relative; display: grid; width: 46px; height: 46px; margin-left: -11px; place-items: center; overflow: hidden; border: 3px solid var(--main-surface-background, var(--token-main-surface-background, #292929)); border-radius: 50%; background: transparent; cursor: pointer; transition: transform 120ms ease, z-index 120ms ease; }
      .codex-mux-win-avatar-button:first-child { margin-left: 0; }
      .codex-mux-win-avatar-button:hover, .codex-mux-win-avatar-button:focus-visible { z-index: 5 !important; outline: 2px solid #6e86f7; outline-offset: 2px; transform: scale(1.06); }
      .codex-mux-win-avatar-button .codex-mux-win-avatar { width: 40px; height: 40px; flex-basis: 40px; font-size: 12px; }
      .codex-mux-win-picker { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 11px; }
      .codex-mux-win-picker-option { display: flex; min-width: 0; align-items: center; gap: 8px; border: 1px solid color-mix(in srgb, currentColor 14%, transparent); border-radius: 10px; background: transparent; color: inherit; cursor: pointer; font: inherit; padding: 7px 9px; text-align: left; }
      .codex-mux-win-picker-option:hover, .codex-mux-win-picker-option:focus-visible { background: color-mix(in srgb, currentColor 8%, transparent); outline: none; }
      .codex-mux-win-picker-option[aria-pressed="true"] { border-color: color-mix(in srgb, #6e86f7 72%, transparent); background: color-mix(in srgb, #6e86f7 17%, transparent); }
      .codex-mux-win-picker-option .codex-mux-win-avatar { width: 26px; height: 26px; flex-basis: 26px; font-size: 9px; }
      .codex-mux-win-picker-copy { display: flex; min-width: 0; flex-direction: column; gap: 1px; }
      .codex-mux-win-picker-name { max-width: 230px; overflow: hidden; font-size: 12px; font-weight: 600; line-height: 16px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-picker-detail { color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 14px; opacity: .82; }
      .codex-mux-win-picker-empty { margin-top: 10px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 17px; opacity: .85; }
      .codex-mux-win-account-resets { border-top: 1px solid color-mix(in srgb, currentColor 12%, transparent); padding-top: 11px; }
      .codex-mux-win-account-resets-header { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
      .codex-mux-win-account-resets-title { font-size: 13px; font-weight: 650; line-height: 18px; }
      .codex-mux-win-account-resets-summary { margin-top: 4px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 16px; }
      .codex-mux-win-account-reset-list { display: grid; gap: 7px; margin: 9px 0 0; padding: 0; list-style: none; }
      .codex-mux-win-account-reset { display: grid; grid-template-columns: minmax(0, 1fr) auto; min-height: 54px; align-items: center; gap: 10px; border: 1px solid color-mix(in srgb, currentColor 12%, transparent); border-radius: 9px; background: color-mix(in srgb, currentColor 5%, transparent); padding: 8px 9px; }
      .codex-mux-win-account-reset-copy { min-width: 0; }
      .codex-mux-win-account-reset-name { overflow: hidden; font-size: 12px; font-weight: 600; line-height: 17px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-account-reset-meta { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; }
      .codex-mux-win-account-reset-use { min-height: 30px; border: 0; border-radius: 7px; background: #4f6bed; color: white; cursor: pointer; font: inherit; font-size: 12px; font-weight: 600; padding: 6px 10px; white-space: nowrap; }
      .codex-mux-win-account-reset-use:hover, .codex-mux-win-account-reset-use:focus-visible { background: #6680f3; outline: none; }
      .codex-mux-win-account-reset-use:disabled { cursor: default; opacity: .62; }
      .codex-mux-win-account-reset-use-disabled { background: color-mix(in srgb, currentColor 13%, transparent); color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-weight: 500; }
      .codex-mux-win-account-reset-details { margin-top: 7px; }
      .codex-mux-win-account-reset-details summary { color: var(--text-secondary, var(--token-text-secondary, currentColor)); cursor: pointer; font-size: 10px; line-height: 15px; }
      .codex-mux-win-account-reset-json { max-height: 160px; margin: 5px 0 0; overflow: auto; border-radius: 7px; background: rgb(0 0 0 / .16); color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10px; line-height: 14px; padding: 7px; white-space: pre-wrap; word-break: break-word; }
      .codex-mux-win-reset-surface { margin-top: 10px; }
      .codex-mux-win-reset-surface .codex-mux-win-surface-title { font-size: 12px; }
      /* Manage pool sources is deliberately a native-feeling management
         surface: one calm header, one compact pool summary, then readable
         source cards in their own scroll region. */
      .codex-mux-win-modal-manager { width: min(680px, calc(100vw - 32px)); height: min(760px, calc(100vh - 32px)); max-height: calc(100vh - 32px); display: flex; flex-direction: column; overflow: hidden; padding: 0; }
      .codex-mux-win-modal-manager .codex-mux-win-manager-header { flex: 0 0 auto; align-items: flex-start; border-bottom: 1px solid color-mix(in srgb, currentColor 12%, transparent); padding: 20px 22px 16px; }
      .codex-mux-win-manager-heading { min-width: 0; flex: 1; }
      .codex-mux-win-manager-kicker { color: #91a5ff; font-size: 10px; font-weight: 700; letter-spacing: .09em; line-height: 14px; text-transform: uppercase; }
      .codex-mux-win-modal-manager .codex-mux-win-manager-header h2 { margin-top: 2px; font-size: 20px; line-height: 26px; }
      .codex-mux-win-manager-description { max-width: 560px; margin-top: 6px; color: var(--text-secondary, var(--token-text-secondary, #c7c7c7)); font-size: 12px; line-height: 17px; }
      .codex-mux-win-manager-add { display: inline-flex; min-height: 32px; flex: 0 0 auto; align-items: center; gap: 6px; margin-top: 1px; border: 0; border-radius: 8px; background: #5873e8; color: white; cursor: pointer; font: inherit; font-size: 12px; font-weight: 650; padding: 7px 11px; }
      .codex-mux-win-manager-add:hover, .codex-mux-win-manager-add:focus-visible { background: #6d86f4; outline: none; }
      .codex-mux-win-manager-add:disabled { cursor: wait; opacity: .6; }
      .codex-mux-win-modal-manager .codex-mux-win-close-button { margin: 0 0 0 8px; }
      .codex-mux-win-manager-content { min-height: 0; overflow: auto; padding: 16px 22px 22px; scrollbar-gutter: stable; }
      .codex-mux-win-manager-overview { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
      .codex-mux-win-manager-overview-card { min-width: 0; border: 1px solid color-mix(in srgb, currentColor 13%, transparent); border-radius: 11px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 10px 11px; }
      .codex-mux-win-manager-overview-label { overflow: hidden; color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 10px; line-height: 14px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-manager-overview-value { margin-top: 2px; color: inherit; font-size: 17px; font-variant-numeric: tabular-nums; font-weight: 700; line-height: 22px; }
      .codex-mux-win-manager-overview-detail { margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 10px; line-height: 14px; }
      .codex-mux-win-manager-overview-note { display: flex; align-items: center; gap: 6px; margin: 11px 0 0; color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 11px; line-height: 16px; }
      .codex-mux-win-manager-overview-dot { width: 7px; height: 7px; flex: 0 0 7px; border-radius: 50%; background: #42c887; box-shadow: 0 0 0 3px color-mix(in srgb, #42c887 16%, transparent); }
      .codex-mux-win-manager-overview-dot-updating { background: #e4ae55; box-shadow: 0 0 0 3px color-mix(in srgb, #e4ae55 16%, transparent); }
      .codex-mux-win-manager-overview-dot-attention { background: #de6a73; box-shadow: 0 0 0 3px color-mix(in srgb, #de6a73 16%, transparent); }
      .codex-mux-win-manager-list-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; margin: 20px 1px 8px; }
      .codex-mux-win-manager-list-title { font-size: 13px; font-weight: 700; line-height: 18px; }
      .codex-mux-win-manager-list-count { color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 11px; line-height: 16px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-list { display: grid; gap: 10px; margin-top: 0; }
      .codex-mux-win-modal-manager .codex-mux-win-account-card { display: block; border: 1px solid color-mix(in srgb, currentColor 15%, transparent); border-radius: 14px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 14px; transition: border-color 120ms ease, background 120ms ease; }
      .codex-mux-win-modal-manager .codex-mux-win-account-card:hover { border-color: color-mix(in srgb, #8195ff 42%, transparent); background: color-mix(in srgb, #8195ff 6%, transparent); }
      .codex-mux-win-account-card-top { display: flex; min-width: 0; align-items: flex-start; gap: 10px; }
      .codex-mux-win-account-card-top .codex-mux-win-identity { min-width: 0; flex: 1; }
      .codex-mux-win-account-card-status { flex: 0 0 auto; border: 1px solid color-mix(in srgb, #42c887 42%, transparent); border-radius: 999px; background: color-mix(in srgb, #42c887 16%, transparent); color: #9af0c5; font-size: 10px; font-weight: 650; line-height: 18px; padding: 0 8px; }
      .codex-mux-win-account-card-status-depleted { border-color: color-mix(in srgb, #e0a04e 48%, transparent); background: color-mix(in srgb, #e0a04e 16%, transparent); color: #f4cb8a; }
      .codex-mux-win-account-card-status-error, .codex-mux-win-account-card-status-disconnected { border-color: color-mix(in srgb, #df6a73 45%, transparent); background: color-mix(in srgb, #df6a73 16%, transparent); color: #f3aeb2; }
      .codex-mux-win-account-card-status-updating { border-color: color-mix(in srgb, #e0a04e 42%, transparent); background: color-mix(in srgb, #e0a04e 12%, transparent); color: #edc47e; }
      .codex-mux-win-account-quota { margin: 14px 0 0 39px; }
      .codex-mux-win-account-quota-head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
      .codex-mux-win-account-quota-label { color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 10px; font-weight: 650; letter-spacing: .04em; text-transform: uppercase; }
      .codex-mux-win-account-quota-value { color: inherit; font-size: 12px; font-variant-numeric: tabular-nums; font-weight: 700; }
      .codex-mux-win-account-quota-value-muted { color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-weight: 500; }
      .codex-mux-win-account-quota-track { height: 6px; margin-top: 7px; overflow: hidden; border-radius: 999px; background: color-mix(in srgb, currentColor 14%, transparent); }
      .codex-mux-win-account-quota-fill { height: 100%; border-radius: inherit; background: linear-gradient(90deg, #5873e8, #79a2ff); transition: width 180ms ease; }
      .codex-mux-win-account-quota-fill-low { background: linear-gradient(90deg, #d56a62, #e4a452); }
      .codex-mux-win-account-quota-meta { margin-top: 6px; color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 10px; line-height: 14px; }
      .codex-mux-win-account-window-chips { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 8px; }
      .codex-mux-win-account-window-chip { border: 1px solid color-mix(in srgb, currentColor 13%, transparent); border-radius: 999px; background: color-mix(in srgb, currentColor 4%, transparent); color: var(--text-secondary, var(--token-text-secondary, #bbb)); font-size: 10px; line-height: 18px; padding: 0 7px; }
      .codex-mux-win-account-window-chip-low { border-color: color-mix(in srgb, #df6a73 35%, transparent); color: #efaaad; }
      .codex-mux-win-account-card-footer { display: flex; align-items: center; justify-content: flex-end; gap: 6px; margin-top: 13px; border-top: 1px solid color-mix(in srgb, currentColor 10%, transparent); padding-top: 11px; }
      .codex-mux-win-account-card-footer .codex-mux-win-account-card-note { margin-right: auto; color: var(--text-secondary, var(--token-text-secondary, #aaa)); font-size: 10px; line-height: 14px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-action { min-height: 31px; border-radius: 8px; font-size: 11px; font-weight: 600; padding: 6px 10px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-resets { margin: 12px 0 0 39px; border-top: 0; padding-top: 0; }
      .codex-mux-win-modal-manager .codex-mux-win-account-resets-header { border-top: 1px solid color-mix(in srgb, currentColor 10%, transparent); padding-top: 11px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-resets-title { color: var(--text-secondary, var(--token-text-secondary, #c6c6c6)); font-size: 11px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-resets-summary { font-size: 10px; line-height: 15px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-reset-list { gap: 6px; margin-top: 7px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-reset { min-height: 44px; border-radius: 8px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 7px 8px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-reset-name { font-size: 11px; }
      .codex-mux-win-modal-manager .codex-mux-win-account-reset-meta, .codex-mux-win-modal-manager .codex-mux-win-account-reset-details summary { font-size: 10px; }
      .codex-mux-win-modal-manager .codex-mux-win-status { margin: 12px 0 0; border-radius: 8px; background: color-mix(in srgb, #e0a04e 13%, transparent); color: #f0c881; font-size: 11px; line-height: 16px; padding: 8px 9px; }
      @media (max-width: 560px) { .codex-mux-win-modal-manager { width: calc(100vw - 20px); height: calc(100vh - 20px); max-height: calc(100vh - 20px); } .codex-mux-win-modal-manager .codex-mux-win-manager-header, .codex-mux-win-manager-content { padding-left: 15px; padding-right: 15px; } .codex-mux-win-manager-overview { grid-template-columns: 1fr 1fr; } .codex-mux-win-manager-overview-card:first-child { grid-column: 1 / -1; } .codex-mux-win-account-quota, .codex-mux-win-modal-manager .codex-mux-win-account-resets { margin-left: 0; } .codex-mux-win-account-card-footer { flex-wrap: wrap; justify-content: flex-start; } .codex-mux-win-account-card-footer .codex-mux-win-account-card-note { flex: 1 0 100%; } }
      @media (prefers-reduced-motion: reduce) { .codex-mux-win-modal-manager .codex-mux-win-account-card, .codex-mux-win-account-quota-fill { transition: none; } }
      #${USAGE_SURFACE_ID} { box-sizing: border-box; width: 100%; margin: 24px 0 0; border-top: 1px solid color-mix(in srgb, currentColor 14%, transparent); padding-top: 20px; }
      #${USAGE_SURFACE_ID} *, #${USAGE_SURFACE_ID} *::before, #${USAGE_SURFACE_ID} *::after { box-sizing: border-box; }
      .codex-mux-win-usage-heading { font-size: 16px; font-weight: 650; line-height: 22px; }
      .codex-mux-win-usage-description { margin-top: 4px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 12px; line-height: 17px; }
      .codex-mux-win-usage-summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-top: 14px; }
      .codex-mux-win-usage-summary-card { min-width: 0; border: 1px solid color-mix(in srgb, currentColor 14%, transparent); border-radius: 10px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 9px 10px; }
      .codex-mux-win-usage-summary-label { color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; }
      .codex-mux-win-usage-summary-value { margin-top: 2px; color: inherit; font-size: 17px; font-variant-numeric: tabular-nums; font-weight: 650; line-height: 22px; }
      .codex-mux-win-usage-accounts { display: grid; gap: 10px; margin-top: 12px; }
      .codex-mux-win-usage-account-card { min-width: 0; border: 1px solid color-mix(in srgb, currentColor 15%, transparent); border-radius: 12px; background: color-mix(in srgb, currentColor 4%, transparent); padding: 12px; }
      .codex-mux-win-usage-account-header { display: flex; min-width: 0; align-items: flex-start; gap: 9px; }
      .codex-mux-win-usage-account-title { min-width: 0; flex: 1; }
      .codex-mux-win-usage-account-name { overflow: hidden; font-size: 14px; font-weight: 650; line-height: 19px; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-usage-account-id { overflow: hidden; margin-top: 2px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; opacity: .84; text-overflow: ellipsis; white-space: nowrap; }
      .codex-mux-win-usage-account-status { flex: 0 0 auto; border-radius: 999px; background: color-mix(in srgb, #37b36a 35%, transparent); color: inherit; font-size: 10px; line-height: 18px; padding: 0 8px; }
      .codex-mux-win-usage-account-status-unavailable { background: color-mix(in srgb, #dc6262 36%, transparent); }
      .codex-mux-win-usage-columns { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 12px; margin-top: 13px; }
      .codex-mux-win-usage-column { min-width: 0; }
      .codex-mux-win-usage-column-title { font-size: 12px; font-weight: 650; line-height: 17px; }
      .codex-mux-win-usage-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; margin-top: 7px; color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-size: 11px; line-height: 15px; }
      .codex-mux-win-usage-row-value { color: inherit; font-variant-numeric: tabular-nums; text-align: right; }
      .codex-mux-win-usage-progress { height: 5px; margin-top: 5px; overflow: hidden; border-radius: 999px; background: color-mix(in srgb, currentColor 16%, transparent); }
      .codex-mux-win-usage-progress-fill { height: 100%; border-radius: inherit; background: #6680f3; }
      .codex-mux-win-usage-error { margin-top: 12px; border-radius: 8px; background: color-mix(in srgb, #d95757 25%, transparent); color: inherit; font-size: 12px; line-height: 17px; padding: 8px 9px; }
      .codex-mux-win-usage-details { margin-top: 11px; }
      .codex-mux-win-usage-details summary { color: var(--text-secondary, var(--token-text-secondary, currentColor)); cursor: pointer; font-size: 11px; line-height: 16px; }
      .codex-mux-win-usage-details pre { max-height: 180px; margin: 6px 0 0; overflow: auto; border-radius: 8px; background: rgb(0 0 0 / .16); color: var(--text-secondary, var(--token-text-secondary, currentColor)); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10px; line-height: 14px; padding: 8px; white-space: pre-wrap; word-break: break-word; }
      .codex-mux-win-usage-account-card .codex-mux-win-account-resets { margin-top: 13px; }
      @media (max-width: 620px) { .codex-mux-win-usage-summary { grid-template-columns: 1fr; } .codex-mux-win-usage-columns { grid-template-columns: 1fr; gap: 8px; } }
    `;
    document.head.append(style);
  }

  function interactiveRow(element) {
    return element.closest("button, [role='menuitem'], [role='button'], a") || element;
  }

  function labelMatches(value, labels, allowShortcut = false) {
    const text = normalize(value);
    return labels.some((label) => {
      if (text === label) return true;
      // The current Windows build folds a keyboard shortcut into the Settings
      // button text.  Keep the fallback narrowly bounded so an unrelated
      // settings page is not mistaken for the profile menu.
      return allowShortcut && text.startsWith(label) && text.length <= label.length + 24;
    });
  }

  function rowsForProfileLabel(labels) {
    const elements = [...document.querySelectorAll("body *")].filter(isVisible);
    const exact = elements.filter((element) => labelMatches(element.textContent, labels));
    const candidates = exact.length > 0
      ? exact
      : elements.filter((element) => {
        if (!element.matches("button, [role='menuitem'], [role='button'], a")) return false;
        return labelMatches(element.textContent, labels, true);
      });
    return [...new Set(candidates.map(interactiveRow).filter(isVisible))];
  }

  function isProfileMenuContainer(element) {
    const rect = element.getBoundingClientRect();
    const controls = element.querySelectorAll("button, [role='menuitem'], [role='button'], a").length;
    return rect.width > 120 && rect.width < 720 && rect.height > 50 && rect.height < 1400 && controls < 32;
  }

  function profileMenuPlacement() {
    const settingsRows = rowsForProfileLabel(PROFILE_SETTINGS_LABELS);
    const logoutRows = rowsForProfileLabel(PROFILE_LOGOUT_LABELS);
    for (const settings of settingsRows) {
      let ancestor = settings.parentElement;
      for (let depth = 0; ancestor && depth < 10; depth += 1, ancestor = ancestor.parentElement) {
        if (!isVisible(ancestor)) continue;
        if (isProfileMenuContainer(ancestor) && logoutRows.some((logout) => ancestor.contains(logout))) {
          return { parent: settings.parentElement, before: settings };
        }
      }
    }
    return null;
  }

  function sidebarSettingsPlacement() {
    const viewportHeight = Number(globalThis.innerHeight) || 900;
    const candidates = rowsForProfileLabel(PROFILE_SETTINGS_LABELS)
      .map((row) => ({ row, rect: row.getBoundingClientRect() }))
      .filter(({ row, rect }) => row.parentElement && rect.width >= 80 && rect.width <= 420 && rect.height >= 24 && rect.height <= 90 && rect.left < 420 && rect.bottom > viewportHeight * 0.55)
      .sort((left, right) => right.rect.bottom - left.rect.bottom);
    const selected = candidates[0]?.row || null;
    return selected ? { parent: selected.parentElement, before: selected } : null;
  }

  function renderFallbackPoolTrigger(trigger, accounts, routing = null) {
    const connected = connectedAccounts(accounts);
    const pool = poolPresentation(connected, routing);
    const authority = connected.find((account) => account.relayAuthority || account.controller) || connected[0] || null;
    const identity = authority ? accountName(authority) : "Codex Relay";
    const detail = pool.connected > 0 && pool.known > 0
      ? `${pool.connected} subscriptions · ${Math.round(pool.remaining)}% ${routingText("left")}`
      : routingText("poolUpdating");
    trigger.replaceChildren();
    append(
      trigger,
      authority ? avatar(authority) : make("span", "codex-mux-win-summary-icon", "◔"),
      (() => {
        const labels = make("span", "codex-mux-win-summary-label");
        append(labels, make("span", "codex-mux-win-title", identity));
        return labels;
      })(),
      make("span", "codex-mux-win-footer-chevron", "⌃"),
    );
    trigger.title = "Open Codex Relay accounts";
    trigger.setAttribute("aria-label", `${identity}. ${detail}. Open Codex Relay accounts.`);
  }

  function closeFallbackPoolMenu() {
    document.getElementById(FALLBACK_MENU_ID)?.remove();
    document.getElementById(FALLBACK_TRIGGER_ID)?.setAttribute("aria-expanded", "false");
  }

  async function toggleFallbackPoolMenu(trigger) {
    if (document.getElementById(FALLBACK_MENU_ID)) {
      closeFallbackPoolMenu();
      return;
    }
    const popover = make("section", "");
    popover.id = FALLBACK_MENU_ID;
    popover.dataset.codexMuxFallback = "true";
    popover.setAttribute("role", "dialog");
    popover.setAttribute("aria-label", "Codex Relay accounts");
    popover.addEventListener("pointerdown", (event) => event.stopPropagation());
    popover.addEventListener("click", (event) => event.stopPropagation());
    const rect = trigger.getBoundingClientRect();
    const left = Math.max(8, Number(rect.left) || 8);
    const availableWidth = Math.max(1, (Number(globalThis.innerWidth) || 1280) - left - 8);
    const sidebarWidth = Math.max(1, Number(rect.width) || 259);
    popover.style.left = `${left}px`;
    popover.style.width = `${Math.min(sidebarWidth, availableWidth)}px`;
    popover.style.bottom = `${Math.max(12, (Number(globalThis.innerHeight) || 900) - (Number(rect.top) || 0) + 8)}px`;
    // Paint identity data synchronously, but never paint an expired quota
    // number. A cold/reopened app shows "Updating…" for the first live read
    // instead of briefly claiming an incorrect 400/500 or 500/500 balance.
    renderMenu(popover, accountsForImmediatePaint(), null);
    document.body.append(popover);
    trigger.setAttribute("aria-expanded", "true");
    await refreshMenu(popover);
  }

  function installFallbackPoolTrigger() {
    let trigger = document.getElementById(FALLBACK_TRIGGER_ID);
    if (trigger?.isConnected) {
      const lastRefresh = Number(trigger.dataset.codexMuxLastRefresh || 0);
      if (Date.now() - lastRefresh >= LIVE_QUOTA_REFRESH_MS) {
        void refreshFallbackPoolTrigger(trigger);
      }
      return;
    }
    const placement = sidebarSettingsPlacement();
    if (!placement || typeof placement.parent.replaceChild !== "function") return;
    let inserted = false;
    trigger = make("button", "");
    trigger.id = FALLBACK_TRIGGER_ID;
    trigger.type = "button";
    trigger.setAttribute("aria-haspopup", "dialog");
    trigger.setAttribute("aria-expanded", "false");
    trigger.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      void toggleFallbackPoolMenu(trigger);
    });
    // Render synchronously so the footer never flashes empty while the local
    // account/usage requests are being refreshed.
    renderFallbackPoolTrigger(trigger, accountsForImmediatePaint(), null);
    placement.parent.replaceChild(trigger, placement.before);
    inserted = true;
    const lastRefresh = Number(trigger.dataset.codexMuxLastRefresh || 0);
    if (inserted || Date.now() - lastRefresh >= LIVE_QUOTA_REFRESH_MS) {
      void refreshFallbackPoolTrigger(trigger);
    }
  }

  async function refreshFallbackPoolTrigger(trigger) {
    trigger.dataset.codexMuxLastRefresh = String(Date.now());
    try {
      const accounts = await loadAccounts();
      if (trigger.isConnected) renderFallbackPoolTrigger(trigger, accounts, null);
    } catch {
      if (trigger.isConnected) renderFallbackPoolTrigger(trigger, accountsForImmediatePaint(), null);
    }
  }

  function openNativeSettingsFromFallback() {
    closeFallbackPoolMenu();
    // Codex's renderer listens for this same host message when its native
    // Settings command is invoked. Dispatching the route message preserves the
    // Relay profile footer until React performs the real page transition; a
    // detached/reinserted native button no longer owns a valid React handler.
    window.postMessage({ type: "navigate-to-route", path: "/settings/general-settings" }, "*");
  }

  async function loadAccounts() {
    if (accountsRefreshPromise) return accountsRefreshPromise;
    accountsRefreshPromise = request("/accounts")
      .then((result) => {
        latestAccounts = Array.isArray(result.accounts) ? result.accounts : [];
        latestAccountsFetchedAt = Date.now();
        return latestAccounts;
      })
      .finally(() => { accountsRefreshPromise = null; });
    return accountsRefreshPromise;
  }

  function accountsForImmediatePaint() {
    if (Date.now() - latestAccountsFetchedAt <= LIVE_QUOTA_MAX_AGE_MS) return latestAccounts;
    return latestAccounts.map((account) => {
      const copy = { ...account };
      delete copy.rateLimits;
      delete copy.rateLimitsObservedAt;
      delete copy.rateLimitAvailable;
      delete copy.quotaAllowed;
      delete copy.quotaLimitReached;
      delete copy.nextRateLimitResetAt;
      delete copy.quotaSource;
      return copy;
    });
  }

  function surface(title, description, className = "") {
    const element = make("section", `codex-mux-win-surface ${className}`.trim());
    append(
      element,
      make("div", "codex-mux-win-surface-title", title),
      make("div", "codex-mux-win-surface-description", description),
    );
    return element;
  }

  function normalizeSelectedAccount(accounts, requested, fallback = "primary") {
    if (fallback == null) {
      return accounts.some((account) => account.id === requested) ? requested : null;
    }
    if (accounts.some((account) => account.id === requested)) return requested;
    if (accounts.some((account) => account.id === fallback)) return fallback;
    return accounts[0]?.id || null;
  }

  function scheduleSurfaceReload(host, message) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", message));
    window.setTimeout(() => window.location.reload(), 140);
  }

  function profilePicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getProfileAccountId(), null);
    if (selectedId !== getProfileAccountId()) setProfileAccountId(selectedId);
    const visible = selectedId
      ? accounts.filter((account) => account.id === selectedId)
      : accounts;
    const panel = surface(
      selectedId ? "Selected subscription profile" : "Combined profile statistics",
      selectedId
        ? "Select the photo again to return to the combined view."
        : `${accounts.length} connected subscriptions. Select a photo to view one account's statistics.`,
    );
    const stack = make("div", "codex-mux-win-avatar-stack");
    visible.forEach((account, index) => {
      const button = make("button", "codex-mux-win-avatar-button");
      button.type = "button";
      button.style.zIndex = String(index + 1);
      button.title = accountName(account);
      button.setAttribute(
        "aria-label",
        selectedId ? "Show combined profile statistics" : `Show ${accountName(account)} profile statistics`,
      );
      button.append(avatar(account));
      button.addEventListener("click", () => {
        const nextId = selectedId === account.id ? null : account.id;
        setProfileAccountId(nextId);
        scheduleSurfaceReload(
          host,
          nextId ? `Opening ${account.label}'s profile statistics…` : "Returning to combined profile statistics…",
        );
      });
      stack.append(button);
    });
    panel.append(stack);
    host.replaceChildren(panel);
  }

  function pickerButton(account, selected, detail, onSelect) {
    const button = make("button", "codex-mux-win-picker-option");
    button.type = "button";
    button.setAttribute("aria-pressed", String(selected));
    append(
      button,
      avatar(account),
      append(
        make("span", "codex-mux-win-picker-copy"),
        make("span", "codex-mux-win-picker-name", accountName(account)),
        make("span", "codex-mux-win-picker-detail", detail),
      ),
    );
    button.addEventListener("click", onSelect);
    return button;
  }

  function pluginPicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getPluginAccountId());
    if (selectedId !== getPluginAccountId()) setPluginAccountId(selectedId);
    const selected = accounts.find((account) => account.id === selectedId) || null;
    const panel = surface(
      "Plugin connections",
      selected
        ? `Plugin definitions stay shared. Apps, connection status, and OAuth below use ${selected.label}.`
        : "Plugin definitions stay shared. Choose a subscription for Apps, connection status, and OAuth.",
    );
    const options = make("div", "codex-mux-win-picker");
    accounts.forEach((account) => {
      options.append(pickerButton(
        account,
        account.id === selectedId,
        account.id === selectedId ? "Selected for Apps and MCP" : "Use this subscription",
        () => {
          if (account.id === selectedId) return;
          setPluginAccountId(account.id);
          scheduleSurfaceReload(host, `Switching plugin connections to ${account.label}…`);
        },
      ));
    });
    panel.append(options);
    host.replaceChildren(panel);
  }

  async function resetPicker(host, accounts) {
    const selectedId = normalizeSelectedAccount(accounts, getResetAccountId());
    if (selectedId !== getResetAccountId()) setResetAccountId(selectedId);
    const counts = await Promise.all(accounts.map(async (account) => {
      try {
        const result = await rateLimitResets(account.id);
        if (result?.error_code || result?.errorCode) return [account.id, null];
        return [account.id, Math.max(0, Number(
          result.available_count ?? result.availableCount ??
          result.applicable_available_count ?? result.applicableAvailableCount ?? 0,
        ))];
      } catch {
        return [account.id, null];
      }
    }));
    if (!host.isConnected) return;
    const resetCounts = new Map(counts);
    const panel = surface(
      "Subscription",
      "The displayed balance and any reset are isolated to the selected subscription.",
      "codex-mux-win-reset-surface",
    );
    const options = make("div", "codex-mux-win-picker");
    accounts.forEach((account) => {
      const count = resetCounts.get(account.id);
      const detail = count == null
        ? "Resets unavailable"
        : count === 1
          ? "1 reset available"
          : `${count} resets available`;
      options.append(pickerButton(account, account.id === selectedId, detail, () => {
        if (account.id === selectedId) return;
        setResetAccountId(account.id);
        void mountResetPicker(host);
      }));
    });
    panel.append(options);
    host.replaceChildren(panel);
  }

  async function mountProfilePicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      profilePicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  async function mountPluginPicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      pluginPicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  async function mountResetPicker(host) {
    host.replaceChildren(make("div", "codex-mux-win-picker-empty", "Loading subscriptions…"));
    try {
      const accounts = connectedAccounts(await loadAccounts());
      if (!host.isConnected) return;
      if (accounts.length === 0) {
        host.replaceChildren(make("div", "codex-mux-win-picker-empty", "No connected subscriptions are available yet."));
        return;
      }
      // Force the native React reset hook to pick up the fresh account snapshot
      // even when the default account remains Primary.
      setResetAccountId(normalizeSelectedAccount(accounts, getResetAccountId()));
      await resetPicker(host, accounts);
    } catch (error) {
      if (host.isConnected) host.replaceChildren(make("div", "codex-mux-win-picker-empty", `Router unavailable: ${error.message}`));
    }
  }

  function defineSurfaceElement(name, mount) {
    if (customElements.get(name)) return;
    customElements.define(name, class extends HTMLElement {
      connectedCallback() {
        void mount(this);
      }
    });
  }

  function registerSurfaceElements() {
    defineSurfaceElement("codex-mux-profile-picker", mountProfilePicker);
    defineSurfaceElement("codex-mux-plugin-picker", mountPluginPicker);
    defineSurfaceElement("codex-mux-reset-picker", mountResetPicker);
  }

  function setMenuStatus(menu, message) {
    let status = menu.querySelector(".codex-mux-win-error");
    if (!message) {
      status?.remove();
      return;
    }
    if (!status) {
      status = make("div", "codex-mux-win-error");
      menu.append(status);
    }
    status.textContent = message;
  }

  let activeAccountManager = null;

  function closeAccountManager(expected = null) {
    if (expected && activeAccountManager !== expected) return false;
    const current = activeAccountManager;
    current?.backdrop?.remove();
    if (!expected || activeAccountManager === expected) activeAccountManager = null;
    return Boolean(current);
  }

  function showActionToast(title, detail, neutral = false) {
    dismissToast();
    const toast = make("section", `codex-mux-win-toast${neutral ? " codex-mux-win-toast-neutral" : ""}`);
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss notification");
    append(
      toast,
      make("div", "codex-mux-win-toast-icon", neutral ? "i" : "✓"),
      append(
        make("div", "codex-mux-win-toast-copy"),
        make("div", "codex-mux-win-toast-title", title),
        make("div", "codex-mux-win-toast-detail", detail),
      ),
      close,
    );
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function dismissUpdateToast() {
    const current = activeUpdateToast;
    if (!current) return false;
    current.element?.remove();
    activeUpdateToast = null;
    return true;
  }

  function showUpdateToast(update) {
    if (update?.available !== true || update?.installing === true) return;
    const version = String(update.version || "").trim();
    if (!version) return;
    if (activeUpdateToast?.version === version) return;
    dismissUpdateToast();
    const toast = make("section", "codex-mux-win-toast codex-mux-win-update-toast");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss update notification");
    const copy = make("div", "codex-mux-win-toast-copy");
    append(
      copy,
      make("div", "codex-mux-win-toast-title", `Codex Relay ${version} is available`),
      make("div", "codex-mux-win-toast-detail", update.notes || "Update now to download, restart, and reopen the Router automatically."),
    );
    const actions = make("div", "codex-mux-win-update-actions");
    const install = make("button", "codex-mux-win-update-button", "Update now");
    install.type = "button";
    install.addEventListener("click", async () => {
      const bridge = globalThis.codexMuxUpdater;
      if (bridge == null || typeof bridge.install !== "function" || install.disabled) return;
      install.disabled = true;
      install.textContent = "Preparing update…";
      const detail = copy.querySelector?.(".codex-mux-win-toast-detail");
      if (detail) detail.textContent = "The Router will close, install the verified release, and reopen automatically.";
      try {
        const result = await bridge.install();
        if (result?.installing !== true) {
          install.disabled = false;
          install.textContent = "Try again";
          if (detail) detail.textContent = result?.error || "The update could not be started. The current Router is still running.";
        }
      } catch (error) {
        install.disabled = false;
        install.textContent = "Try again";
        if (detail) detail.textContent = `The update could not be started: ${error.message}`;
      }
    });
    append(actions, install);
    copy.append(actions);
    append(toast, make("div", "codex-mux-win-toast-icon", "↑"), copy, close);
    document.body.append(toast);
    const notice = { element: toast, version };
    activeUpdateToast = notice;
    close.addEventListener("click", dismissUpdateToast);
  }

  function startUpdateWatcher() {
    if (updateWatcherStarted) return;
    updateWatcherStarted = true;
    const bridge = globalThis.codexMuxUpdater;
    if (bridge == null) return;
    const apply = (value) => {
      if (value?.available === true && value?.installing !== true) showUpdateToast(value);
    };
    if (typeof bridge.subscribe === "function") bridge.subscribe(apply);
    if (typeof bridge.getState === "function") void bridge.getState().then(apply).catch(() => {});
  }

  function accountRemovalConfirmed(account) {
    if (typeof window.confirm !== "function") return false;
    const chats = Number(account?.threadCount || 0);
    const historyWarning = chats > 0
      ? `\n\nThis subscription owns ${chats} chat${chats === 1 ? "" : "s"}. Removing it will clear Router's assignment for those chats; their local files are not deleted.`
      : "";
    return window.confirm(
      `Remove ${accountName(account)} from Codex Relay?${historyWarning}\n\nYou can add it again later by signing in.`
    );
  }

  function accountManagerStatus(state, message = "") {
    state.status.textContent = message;
    state.status.hidden = !message;
  }

  async function setPrimaryAccount(state, account, button) {
    if (button?.disabled || account?.controller) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Switching Primary to ${accountName(account)} and restarting Router sessions…`);
    try {
      const result = await request(`/accounts/${encodeURIComponent(account.id)}/primary`, { method: "POST" });
      const selected = result.account || account;
      const restarted = Number(result.restartedChildren);
      const restartText = Number.isFinite(restarted)
        ? `Restarted ${restarted} Router Codex session${restarted === 1 ? "" : "s"}.`
        : "Router Codex sessions were restarted.";
      showActionToast("Primary account changed", `${accountName(selected)} is now the Router Primary. ${restartText}`);
      const accounts = await loadAccounts();
      state.accounts = accounts;
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not change Primary: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function removeManagedAccount(state, account, button) {
    if (button?.disabled || account?.controller || !account?.id) return;
    if (!accountRemovalConfirmed(account)) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Removing ${accountName(account)}…`);
    try {
      await request(`/accounts/${encodeURIComponent(account.id)}`, {
        method: "DELETE",
        body: JSON.stringify({ force: true }),
      });
      showActionToast("Subscription removed", `${accountName(account)} was removed from Router. The native Codex app was not changed.`);
      const accounts = await loadAccounts();
      state.accounts = accounts;
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not remove subscription: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function removeRelayHostFromPool(state, account, button) {
    if (button?.disabled || !account?.id || !isRelayHost(account)) return;
    if (!accountRemovalConfirmed(account)) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Removing ${accountName(account)} from the Relay Pool…`);
    try {
      // The native Relay host profile and its local chats stay intact. Only
      // pool membership is disabled, which is the safe equivalent of removing
      // an invalidated host credential without orphaning its files.
      await request(`/accounts/${encodeURIComponent(account.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: false }),
      });
      showActionToast("Source removed from pool", `${accountName(account)} is no longer used by the Relay Pool. Its Relay host profile and local chats were kept.`);
      state.accounts = await loadAccounts();
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not remove source from pool: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function cancelManagedPending(state, account, button) {
    if (button?.disabled || !account?.id) return;
    if (button) button.disabled = true;
    accountManagerStatus(state, `Cancelling ${account.label || "subscription"} sign-in…`);
    try {
      const result = await cancelPendingAccount(account.id);
      if (result.connected) showLoginSuccess(result.account || account);
      else showLoginCancelled(account);
      state.accounts = await loadAccounts();
      renderAccountManager(state);
      if (state.menu) await refreshMenu(state.menu);
    } catch (error) {
      accountManagerStatus(state, `Could not cancel sign-in: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  function formatResetCreditExpiry(value) {
    if (value == null || value === "") return "Expiry unavailable";
    const numeric = Number(value);
    const date = Number.isFinite(numeric)
      ? new Date(numeric > 10_000_000_000 ? numeric : numeric * 1000)
      : new Date(String(value));
    return Number.isNaN(date.getTime()) ? `Expires ${String(value)}` : `Expires ${date.toLocaleString()}`;
  }

  function resetCreditJSON(payload) {
    try {
      return JSON.stringify(payload, null, 2);
    } catch {
      return "Reset payload could not be formatted.";
    }
  }

  function resetCreditStatus(credit) {
    const status = String(credit?.status || "available").trim().toLowerCase();
    return status || "available";
  }

  async function redeemAccountReset(state, account, host, renderVersion, credit, button) {
    if (button?.disabled || !account?.id) return;
    button.disabled = true;
    button.textContent = "Using…";
    try {
      const result = await consumeRateLimitReset(account.id, {
        creditId: credit?.id ?? null,
        redeemRequestId: newRedeemRequestId(),
      });
      const code = String(result?.code || "").toLowerCase();
      if (code !== "reset" && code !== "already_redeemed") {
        throw new Error(result?.message || "The reset credit was not applied.");
      }
      showActionToast(
        code === "already_redeemed" ? "Reset already used" : "Usage reset applied",
        code === "already_redeemed"
          ? `${accountName(account)} already redeemed this reset credit.`
          : `${accountName(account)} now has a fresh usage window.`,
      );
      await loadAccountResetSection(state, account, host, renderVersion);
    } catch (error) {
      if (host?.isConnected && state?.resetRenderVersion === renderVersion) {
        button.disabled = false;
        button.textContent = "Use reset";
      }
      showActionToast("Could not use reset", error.message, true);
    }
  }

  function renderAccountResetSection(account, payload = null, error = "", options = {}) {
    const section = make("section", "codex-mux-win-account-resets");
    const header = make("div", "codex-mux-win-account-resets-header");
    header.append(make("div", "codex-mux-win-account-resets-title", "Usage limit resets"));
    section.append(header);
    if (!account?.connected) {
      section.append(make("div", "codex-mux-win-account-resets-summary", "This subscription is not connected."));
      return section;
    }
    if (error) {
      section.append(make("div", "codex-mux-win-account-resets-summary", `Could not load resets: ${error}`));
      return section;
    }
    if (!payload || typeof payload !== "object") {
      section.append(make("div", "codex-mux-win-account-resets-summary", "Loading reset credits…"));
      return section;
    }
    const errorCode = String(payload.error_code || payload.errorCode || "").trim().toLowerCase();
    if (errorCode) {
      const message = errorCode === "auth_invalidated"
        ? "Authentication expired or was revoked. Sign in again to refresh reset credits."
        : String(payload.message || "Reset credits are temporarily unavailable.");
      section.append(make("div", "codex-mux-win-account-resets-summary", message));
      if (errorCode === "auth_invalidated" && typeof options.onReauth === "function") {
        const action = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Sign in again");
        action.type = "button";
        action.addEventListener("click", () => { void options.onReauth(action); });
        section.append(action);
      }
      return section;
    }
    const available = Number(payload.available_count ?? payload.availableCount ?? 0);
    const applicable = Number(payload.applicable_available_count ?? payload.applicableAvailableCount ?? available);
    const credits = Array.isArray(payload.credits) ? payload.credits : [];
    section.append(make(
      "div",
      "codex-mux-win-account-resets-summary",
      `${Number.isFinite(available) ? available : "—"} available · ${Number.isFinite(applicable) ? applicable : "—"} applicable`,
    ));
    if (credits.length > 0) {
      const list = make("ul", "codex-mux-win-account-reset-list");
      credits.forEach((credit, index) => {
        const item = make("li", "codex-mux-win-account-reset");
        const copy = make("div", "codex-mux-win-account-reset-copy");
        const title = credit?.title || credit?.name || `Usage reset ${index + 1}`;
        copy.append(
          make("div", "codex-mux-win-account-reset-name", title),
          make("div", "codex-mux-win-account-reset-meta", formatResetCreditExpiry(credit?.expires_at ?? credit?.expiresAt)),
        );
        const status = resetCreditStatus(credit);
        const action = make(
          "button",
          `codex-mux-win-account-reset-use${status !== "available" ? " codex-mux-win-account-reset-use-disabled" : ""}`,
          status === "available" ? "Use reset" : status,
        );
        action.type = "button";
        action.disabled = status !== "available";
        action.setAttribute("aria-label", `${status === "available" ? "Use" : "View"} ${title}`);
        if (status === "available" && typeof options.onUse === "function") {
          action.addEventListener("click", () => { void options.onUse(credit, action); });
        }
        append(item, copy, action);
        list.append(item);
      });
      section.append(list);
    } else if (available <= 0) {
      section.append(make("div", "codex-mux-win-account-resets-summary", "No reset credits available for this subscription."));
    }
    const details = make("details", "codex-mux-win-account-reset-details");
    details.append(make("summary", "", "View all reset details"), make("pre", "codex-mux-win-account-reset-json", resetCreditJSON(payload)));
    section.append(details);
    return section;
  }

  async function loadAccountResetSection(state, account, host, renderVersion) {
    if (!account?.connected) {
      host.replaceChildren(renderAccountResetSection(account));
      return;
    }
    try {
      const payload = await rateLimitResets(account.id);
      if (!host.isConnected || state.resetRenderVersion !== renderVersion) return;
      host.replaceChildren(renderAccountResetSection(account, payload, "", {
        onUse: (credit, button) => redeemAccountReset(state, account, host, renderVersion, credit, button),
        onReauth: (button) => reauthenticateAccount(state, account, host, renderVersion, button),
      }));
    } catch (error) {
      if (!host.isConnected || state.resetRenderVersion !== renderVersion) return;
      host.replaceChildren(renderAccountResetSection(account, null, error.message));
    }
  }

  async function reauthenticateAccount(state, account, host, renderVersion, button) {
    if (button?.disabled || !account?.id || !state?.menu) return;
    if (button) button.disabled = true;
    try {
      const result = await request(`/accounts/${encodeURIComponent(account.id)}/login`, {
        method: "POST",
        body: JSON.stringify({ mode: "chatgpt" }),
      });
      await showBrowserLogin(state.menu, account, result.login || {}, state);
      if (host?.isConnected && state.resetRenderVersion === renderVersion) {
        await loadAccountResetSection(state, account, host, renderVersion);
      }
    } catch (error) {
      showActionToast("Could not start sign-in", error.message, true);
    } finally {
      if (button) button.disabled = false;
    }
  }

  function usagePayload(entry) {
    return entry?.usage && typeof entry.usage === "object" && !Array.isArray(entry.usage)
      ? entry.usage
      : null;
  }

  function usagePayloadValue(payload, paths) {
    for (const path of paths) {
      let value = payload;
      for (const key of path.split(".")) {
        if (!value || typeof value !== "object") {
          value = null;
          break;
        }
        value = value[key];
      }
      if (value != null && value !== "") return value;
    }
    return null;
  }

  function billingPlanLabel(account, payload) {
    const value = usagePayloadValue(payload, [
      "plan_type", "planType", "plan.name", "plan.display_name", "plan.displayName",
    ]);
    return String(value || account?.planLabel || account?.planType || "Unavailable");
  }

  function billingCreditLabel(payload) {
    const value = usagePayloadValue(payload, [
      "credits.balance", "credits_balance", "credit_balance", "balance",
    ]);
    if (value == null) return "Unavailable";
    if (typeof value === "object") {
      try { return JSON.stringify(value); } catch { return "Available"; }
    }
    return String(value);
  }

  function usageWindowLabel(window) {
    if (!window) return "No quota data";
    const remaining = Number(window.remainingPercent);
    const percent = Number.isFinite(remaining) ? `${Math.round(remaining)}% left` : "Quota available";
    const reset = formatResetCountdown(window.resetsAt);
    return reset ? `${percent} · resets in ${reset}` : percent;
  }

  function usageBillingHeading() {
    const headings = [...document.querySelectorAll("h1,h2,h3,[role='heading']")].filter(isVisible);
    const exact = headings.find((element) => normalize(element.textContent) === "Usage & billing");
    if (exact) return exact;
    // A few Store builds render the settings title as a styled div instead of
    // a semantic heading. Prefer the copy that lives inside the main content
    // column, never the left navigation row.
    return [...document.querySelectorAll("body *")]
      .filter((element) => isVisible(element) && normalize(element.textContent) === "Usage & billing")
      .find((element) => element.closest?.("main,[role='main']")) || null;
  }

  function usageBillingContainer(heading) {
    let ancestor = heading;
    for (let depth = 0; ancestor && depth < 10; depth += 1, ancestor = ancestor.parentElement) {
      if (ancestor.matches?.("main,[role='main']") && isVisible(ancestor)) return ancestor;
    }
    const main = [...document.querySelectorAll("main,[role='main']")].find(isVisible);
    if (main) return main;
    return heading.parentElement || heading;
  }

  function insertUsageBillingHost(container, heading, host) {
    // Put the Relay panel after the native title/description block when the
    // renderer exposes one. This keeps it in the same content flow (and near
    // the top of the page) without assuming any private React class names.
    let anchor = heading;
    while (anchor.parentElement && anchor.parentElement !== container) anchor = anchor.parentElement;
    if (anchor.parentElement === container) {
      container.insertBefore(host, anchor.nextSibling);
    } else {
      container.append(host);
    }
  }

  function renderUsageBillingAccount(account, entry, state, renderVersion) {
    const payload = usagePayload(entry);
    const error = normalize(entry?.error || account?.error || account?.rateLimitError || "");
    const connected = entry?.connected === true || (entry == null && account?.connected === true);
    const card = make("article", "codex-mux-win-usage-account-card");
    const header = make("div", "codex-mux-win-usage-account-header");
    const title = make("div", "codex-mux-win-usage-account-title");
    append(
      title,
      make("div", "codex-mux-win-usage-account-name", accountName(account)),
      make("div", "codex-mux-win-usage-account-id", accountIdentityDetail(account) || account.id),
    );
    const status = make(
      "span",
      `codex-mux-win-usage-account-status${connected && !error ? "" : " codex-mux-win-usage-account-status-unavailable"}`,
      error ? "Error" : connected ? "Connected" : "Unavailable",
    );
    append(header, avatar(account), title, status);
    card.append(header);
    if (error || !connected) {
      card.append(make("div", "codex-mux-win-usage-error", error || "This subscription is not connected."));
      if (!connected) return card;
      // A quota payload may still be present alongside a warning. Keep the
      // verified windows visible below while making the failure explicit.
    }
    if (!connected) {
      return card;
    }

    const columns = make("div", "codex-mux-win-usage-columns");
    const limits = make("div", "codex-mux-win-usage-column");
    const billing = make("div", "codex-mux-win-usage-column");
    limits.append(make("div", "codex-mux-win-usage-column-title", "General usage limits"));
    const windows = usageWindows(account.rateLimits)
      .sort((left, right) => left.windowMinutes - right.windowMinutes);
    if (windows.length === 0) {
      const row = make("div", "codex-mux-win-usage-row");
      append(row, make("span", "", "Quota"), make("span", "codex-mux-win-usage-row-value", "Quota unavailable"));
      limits.append(row);
    } else {
      windows.forEach((window, index) => {
        const row = make("div", "codex-mux-win-usage-row");
        append(row, make("span", "", index === 0 ? "Primary" : "Secondary"), make("span", "codex-mux-win-usage-row-value", usageWindowLabel(window)));
        const progress = make("div", "codex-mux-win-usage-progress");
        const fill = make("div", "codex-mux-win-usage-progress-fill");
        fill.style.width = `${Math.max(0, Math.min(100, Number(window.usedPercent) || 0))}%`;
        progress.append(fill);
        limits.append(row, progress);
      });
    }
    billing.append(make("div", "codex-mux-win-usage-column-title", "Billing details"));
    const planRow = make("div", "codex-mux-win-usage-row");
    append(planRow, make("span", "", "Plan"), make("span", "codex-mux-win-usage-row-value", billingPlanLabel(account, payload)));
    const creditRow = make("div", "codex-mux-win-usage-row");
    append(creditRow, make("span", "", "Credits balance"), make("span", "codex-mux-win-usage-row-value", billingCreditLabel(payload)));
    billing.append(planRow, creditRow);
    append(columns, limits, billing);
    card.append(columns);

    const resetHost = make("div", "codex-mux-win-account-reset-host");
    resetHost.append(renderAccountResetSection(account));
    card.append(resetHost);
    void loadAccountResetSection(state, account, resetHost, renderVersion);

    if (payload) {
      const details = make("details", "codex-mux-win-usage-details");
      let encoded = "Usage response could not be formatted.";
      try { encoded = JSON.stringify(payload, null, 2); } catch { /* keep bounded fallback */ }
      details.append(make("summary", "", "View all billing details"), make("pre", "", encoded));
      card.append(details);
    }
    return card;
  }

  function renderUsageBillingSurface(accounts, collection, host, state, renderVersion, routing = null) {
    const section = make("section", "");
    section.id = USAGE_SURFACE_ID;
    section.setAttribute("aria-label", routingText("sharedPool"));
    append(
      section,
      make("div", "codex-mux-win-usage-heading", routingText("poolTitle")),
      make("div", "codex-mux-win-usage-description", routingText("poolDescription")),
    );
    const pool = poolPresentation(accounts, routing);
    const summary = make("div", "codex-mux-win-usage-summary");
    [[routingText("confirmedRemaining"), `${Math.round(pool.remaining)}%`], [routingText("poolMaximum"), `${Math.round(pool.maximum)}%`], [routingText("activeWorkers"), `${pool.available}/${pool.connected}`]]
      .forEach(([label, value]) => {
        const item = make("div", "codex-mux-win-usage-summary-card");
        append(item, make("div", "codex-mux-win-usage-summary-label", label), make("div", "codex-mux-win-usage-summary-value", value));
        summary.append(item);
    });
    section.append(summary);
    const status = routing?.status?.pool || routing?.pool || {};
    const details = make("div", "codex-mux-win-usage-columns codex-mux-win-pool-details");
    const operational = make("div", "codex-mux-win-usage-column");
    operational.append(make("div", "codex-mux-win-usage-column-title", routingText("poolSummary")));
    [[routingText("poolHealth"), String(status.health || "warming")], [routingText("activeRequests"), String(Number(status.activeLeaseCount) || 0)], [routingText("quotaKnownSources"), `${pool.known}/${pool.connected}`]]
      .forEach(([label, value]) => {
        const row = make("div", "codex-mux-win-usage-row");
        append(row, make("span", "", label), make("span", "codex-mux-win-usage-row-value", value));
        operational.append(row);
      });
    const timing = make("div", "codex-mux-win-usage-column");
    timing.append(make("div", "codex-mux-win-usage-column-title", routingText("quotaTiming")));
    const nextReset = Number(status.nextResetAt) > 0 ? formatResetCountdown(Number(status.nextResetAt)) : routingText("noResetReported");
    const updated = Number(status.quotaUpdatedAt) > 0 ? new Date(Number(status.quotaUpdatedAt)).toLocaleString() : routingText("waitingQuotaEvidence");
    [[routingText("nextPoolReset"), nextReset], [routingText("quotaChecked"), updated], [routingText("unknownQuotaCount"), String(pool.unknown)]]
      .forEach(([label, value]) => {
        const row = make("div", "codex-mux-win-usage-row");
        append(row, make("span", "", label), make("span", "codex-mux-win-usage-row-value", value));
        timing.append(row);
      });
    append(details, operational, timing);
    section.append(details);
    const lastError = status?.lastError;
    const lastErrorMessage = normalize(lastError?.message).slice(0, 320);
    if (lastErrorMessage) {
      const suffixParts = [];
      if (Number(lastError?.httpStatus) > 0) suffixParts.push(`HTTP ${Number(lastError.httpStatus)}`);
      const lastErrorCode = normalize(lastError?.code).slice(0, 80);
      if (lastErrorCode) suffixParts.push(`code ${lastErrorCode}`);
      const suffix = suffixParts.length > 0 ? ` (${suffixParts.join("; ")})` : "";
      section.append(make("div", "codex-mux-win-usage-error", `${routingText("lastError")}: ${lastErrorMessage}${suffix}`));
    }
    const usageAccounts = make("div", "codex-mux-win-usage-accounts");
    const collectionEntries = new Map(
      (Array.isArray(collection?.accounts) ? collection.accounts : [])
        .map((entry) => [String(entry?.accountId || ""), entry])
        .filter(([accountId]) => accountId),
    );
    (Array.isArray(accounts) ? accounts : [])
      .filter((account) => account?.enabled !== false)
      .forEach((account) => {
        usageAccounts.append(
          renderUsageBillingAccount(
            account,
            collectionEntries.get(String(account.id)) || null,
            state,
            renderVersion,
          ),
        );
      });
    if (usageAccounts.children.length > 0) section.append(usageAccounts);
    if (collection?.error) section.append(make("div", "codex-mux-win-usage-error", "Some billing metadata is temporarily unavailable; pool routing remains governed by verified quota evidence."));
    host.replaceChildren(section);
  }

  async function loadUsageBillingSurface(host, renderVersion) {
    try {
      const [accounts, routing, collection] = await Promise.all([
        loadAccounts(),
        routingContext().catch(() => null),
        nativeUsageStatusAll(),
      ]);
      if (!host.isConnected || usageSurfaceVersion !== renderVersion) return;
      const state = { resetRenderVersion: renderVersion };
      renderUsageBillingSurface(accounts, collection, host, state, renderVersion, routing);
    } catch (error) {
      if (!host.isConnected || usageSurfaceVersion !== renderVersion) return;
      host.replaceChildren(make("div", "codex-mux-win-usage-error", `Could not load Relay usage: ${error.message}`));
    }
  }

  function installUsageBillingSurface() {
    const existing = document.getElementById(USAGE_SURFACE_ID);
    const heading = usageBillingHeading();
    if (!heading) {
      existing?.remove();
      return;
    }
    const container = usageBillingContainer(heading);
    if (!container) return;
    let host = existing;
    let inserted = false;
    if (!host || host.parentElement !== container) {
      host?.remove();
      host = make("div", "");
      host.id = USAGE_SURFACE_ID;
      insertUsageBillingHost(container, heading, host);
      inserted = true;
      usageSurfaceVersion += 1;
    }
    const lastRefresh = Number(host.dataset.codexMuxLastRefresh || 0);
    if (inserted || Date.now() - lastRefresh > 15000) {
      host.dataset.codexMuxLastRefresh = String(Date.now());
      void loadUsageBillingSurface(host, usageSurfaceVersion);
    }
  }

  function managerStatus(account, quota) {
    if (!account?.connected) return hasPendingLogin(account) ? "Sign-in pending" : "Not connected";
    if (account.error && quota == null) return "Connection error";
    if (account.rateLimitError && quota == null) {
      return accountAuthInvalidated(account) ? "Sign in required" : "Quota pending verification";
    }
    if (quota == null) return "Quota updating";
    if (quota <= 0) return "Quota depleted";
    return "Connected";
  }

  function managerStatusClass(status) {
    return status === "Connected" ? ""
      : status === "Quota depleted" ? " codex-mux-win-account-card-status-depleted"
        : status === "Quota updating" || status === "Quota pending verification" ? " codex-mux-win-account-card-status-updating"
          : " codex-mux-win-account-card-status-error";
  }

  function managerOverviewCard(label, value, detail) {
    const card = make("div", "codex-mux-win-manager-overview-card");
    append(card, make("div", "codex-mux-win-manager-overview-label", label), make("div", "codex-mux-win-manager-overview-value", value), make("div", "codex-mux-win-manager-overview-detail", detail));
    return card;
  }

  function renderManagerOverview(state) {
    if (!state.overview?.replaceChildren) return;
    const accounts = Array.isArray(state.accounts) ? state.accounts : [];
    const connected = connectedAccounts(accounts);
    const pool = poolPresentation(accounts, null);
    // The short Codex window is the actionable capacity for a pool. The
    // weekly window remains visible on each source, but must never be allowed
    // to become the headline number users use to decide whether they can run a
    // task now.
    const shortWindows = connected.map(shortestUsageWindow).filter(Boolean);
    const shortKnown = shortWindows.length;
    const shortRemaining = shortWindows.reduce((sum, window) => sum + window.remainingPercent, 0);
    const shortMaximum = connected.length * 100;
    const known = shortKnown > 0;
    const available = connected.filter((account) => {
      const quota = shortestRemainingUsage(account);
      return quota != null && quota > 0;
    }).length;
    const poolValue = known ? `${Math.round(shortRemaining)}% / ${Math.round(shortMaximum)}%` : "Updating…";
    const status = shortKnown < connected.length ? "Quota updating" : available > 0 ? "Ready" : "Needs attention";
    const statusClass = status === "Ready" ? "" : status === "Quota updating" ? " codex-mux-win-manager-overview-dot-updating" : " codex-mux-win-manager-overview-dot-attention";
    state.overview.replaceChildren(
      managerOverviewCard("Pool quota (5H)", poolValue, `${shortKnown}/${connected.length} sources verified · shortest window`),
      managerOverviewCard("Connected", String(pool.connected), "private sources"),
      managerOverviewCard("Available now", String(available), `${pool.connected} connected`),
      (() => {
        const note = make("div", "codex-mux-win-manager-overview-note");
        append(note, make("span", `codex-mux-win-manager-overview-dot${statusClass}`), make("span", "", `${status} · one Relay authority owns every task`));
        return note;
      })(),
    );
  }

  function renderAccountManager(state) {
    const accounts = (Array.isArray(state.accounts) ? state.accounts : [])
      .filter((account) => account?.enabled !== false);
    state.resetRenderVersion = (state.resetRenderVersion || 0) + 1;
    const renderVersion = state.resetRenderVersion;
    renderManagerOverview(state);
    state.list.replaceChildren();
    const listHeading = make("div", "codex-mux-win-manager-list-heading");
    append(listHeading, make("div", "codex-mux-win-manager-list-title", "Pool sources"), make("div", "codex-mux-win-manager-list-count", `${accounts.length} configured`));
    state.list.append(listHeading);
    if (accounts.length === 0) {
      state.list.append(make("div", "codex-mux-win-picker-empty", "No subscriptions are configured."));
      return;
    }
    accounts.forEach((account) => {
      // Manage pool sources is intentionally 5H-first. The scheduler still
      // uses the conservative effective capacity internally, while this
      // surface answers the user's immediate question: how much short-window
      // quota is left right now?
      const quota = shortestRemainingUsage(account);
      const statusLabel = managerStatus(account, quota);
      const card = make("article", `codex-mux-win-account-card${quota != null && quota <= 0 ? " codex-mux-win-account-card-depleted" : ""}`);
      card.setAttribute("data-account-id", account.id || "");
      const top = make("div", "codex-mux-win-account-card-top");
      const identity = make("div", "codex-mux-win-identity");
      const meta = make("div", "codex-mux-win-account-meta");
      const name = make("div", "codex-mux-win-name", accountName(account));
      if (isRelayPrimary(account)) name.append(make("span", "codex-mux-win-account-badge", "Relay authority"));
      const identityDetail = accountIdentityDetail(account);
      append(meta, name, identityDetail ? make("div", "codex-mux-win-account-id", identityDetail) : null);
      append(identity, avatar(account), meta);
      append(top, identity, make("span", `codex-mux-win-account-card-status${managerStatusClass(statusLabel)}`, statusLabel));
      card.append(top);

      if (account.connected) {
        const quotaSection = make("section", "codex-mux-win-account-quota");
        quotaSection.setAttribute("aria-label", "Effective quota");
        const quotaHead = make("div", "codex-mux-win-account-quota-head");
        append(quotaHead, make("span", "codex-mux-win-account-quota-label", "Effective quota"), make("span", `codex-mux-win-account-quota-value${quota == null ? " codex-mux-win-account-quota-value-muted" : ""}`, quota == null ? "Updating…" : `${Math.round(quota)}% available`));
        const track = make("div", "codex-mux-win-account-quota-track");
        track.setAttribute("role", "progressbar");
        track.setAttribute("aria-valuemin", "0");
        track.setAttribute("aria-valuemax", "100");
        track.setAttribute("aria-valuenow", quota == null ? "0" : String(Math.round(quota)));
        const fill = make("div", `codex-mux-win-account-quota-fill${quota != null && quota <= 10 ? " codex-mux-win-account-quota-fill-low" : ""}`);
        fill.style.width = `${quota == null ? 0 : Math.max(0, Math.min(100, quota))}%`;
        track.append(fill);
        const windows = usageWindows(account.rateLimits).sort((left, right) => left.windowMinutes - right.windowMinutes);
        const chips = make("div", "codex-mux-win-account-window-chips");
        windows.forEach((window) => chips.append(make("span", `codex-mux-win-account-window-chip${window.remainingPercent <= 10 ? " codex-mux-win-account-window-chip-low" : ""}`, `${formatWindowDuration(window.windowMinutes)} · ${Math.round(window.remainingPercent)}% left`)));
        append(quotaSection, quotaHead, track, make("div", "codex-mux-win-account-quota-meta", quotaResetSummary(account)), chips);
        card.append(quotaSection);
      }

      const resetHost = make("div", "codex-mux-win-account-reset-host");
      resetHost.append(renderAccountResetSection(account));
      card.append(resetHost);

      const footer = make("div", "codex-mux-win-account-card-footer");
      const relayHostOnly = isRelayHost(account) && !account.connected;
      const note = relayHostOnly ? "Relay host · separate from Codex" : isRelayPrimary(account) ? "Current task authority" : account.connected ? "Credential source only" : "Needs sign-in";
      footer.append(make("span", "codex-mux-win-account-card-note", note));
      if (relayHostOnly) {
        const host = make("button", "codex-mux-win-account-action", "Relay host");
        host.type = "button";
        host.disabled = true;
        host.title = "This private Relay home is kept as the app host and cannot be removed";
        footer.append(host);
      } else if (isRelayPrimary(account)) {
        if (!account.connected && !hasPendingLogin(account)) {
          const signin = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Sign in");
          signin.type = "button";
          signin.title = "Re-authenticate the Relay authority in the official ChatGPT browser flow";
          signin.addEventListener("click", () => { void startExistingSubscription(state.menu, account, signin, state); });
          footer.append(signin);
        } else {
          const authority = make("button", "codex-mux-win-account-action", "Relay authority");
          authority.type = "button";
          authority.disabled = true;
          authority.title = "This subscription is the selected Relay task authority and cannot be removed while it owns the active logical worker";
          footer.append(authority);
        }
      } else if (account.connected) {
        if (isRelayHost(account) && accountAuthInvalidated(account)) {
          const signin = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Sign in again");
          signin.type = "button";
          signin.title = "Refresh the Relay host credentials in the official ChatGPT browser flow";
          signin.addEventListener("click", () => { void startExistingSubscription(state.menu, account, signin, state); });
          footer.append(signin);
          const remove = make("button", "codex-mux-win-account-action codex-mux-win-account-action-danger", "Remove");
          remove.type = "button";
          remove.title = "Remove this invalidated credential from the Relay Pool while keeping the Relay host profile and chats";
          remove.addEventListener("click", () => { void removeRelayHostFromPool(state, account, remove); });
          footer.append(remove);
        } else {
          const select = make("button", "codex-mux-win-account-action codex-mux-win-account-action-primary", "Use as authority");
          select.type = "button";
          select.title = "Make this connected subscription the Relay task authority; active turns must finish first";
          select.addEventListener("click", () => { void setPrimaryAccount(state, account, select); });
          footer.append(select);
        }
        if (isRelayHost(account)) {
          const host = make("button", "codex-mux-win-account-action", "Relay host");
          host.type = "button";
          host.disabled = true;
          host.title = "This private Relay home is kept as the app host and cannot be removed";
          footer.append(host);
        } else {
          const remove = make("button", "codex-mux-win-account-action codex-mux-win-account-action-danger", "Remove");
          remove.type = "button";
          remove.addEventListener("click", () => { void removeManagedAccount(state, account, remove); });
          footer.append(remove);
        }
      } else if (hasPendingLogin(account)) {
        const cancel = make("button", "codex-mux-win-account-action", "Cancel sign-in");
        cancel.type = "button";
        cancel.addEventListener("click", () => { void cancelManagedPending(state, account, cancel); });
        footer.append(cancel);
      } else {
        const remove = make("button", "codex-mux-win-account-action codex-mux-win-account-action-danger", "Remove");
        remove.type = "button";
        remove.addEventListener("click", () => { void removeManagedAccount(state, account, remove); });
        footer.append(remove);
      }
      card.append(footer);
      state.list.append(card);
      void loadAccountResetSection(state, account, resetHost, renderVersion);
    });
    accountManagerStatus(state, "");
  }

  function openAccountSettings(menu, accounts, routing = null) {
    closeAccountManager();
    const backdrop = make("div", "codex-mux-win-modal-backdrop");
    const dialog = make("section", "codex-mux-win-modal codex-mux-win-modal-manager");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("tabindex", "-1");
    const title = make("h2", "", "Manage pool sources");
    title.id = `codex-mux-manager-title-${Date.now()}`;
    dialog.setAttribute("aria-labelledby", title.id);
    const description = make("div", "codex-mux-win-manager-description", "Manage the private quota sources behind this Relay. Chats, Goals, tools, history, and the visible Relay identity stay in one app.");
    description.id = `${title.id}-description`;
    dialog.setAttribute("aria-describedby", description.id);
    const list = make("div", "codex-mux-win-account-list");
    const overview = make("section", "codex-mux-win-manager-overview");
    overview.setAttribute("aria-label", "Pool overview");
    const status = make("div", "codex-mux-win-status");
    status.hidden = true;
    status.setAttribute("role", "status");
    status.setAttribute("aria-live", "polite");
    const state = { accounts: Array.isArray(accounts) ? accounts.slice() : [], routing, backdrop, dialog, list, overview, menu, status };
    const header = make("div", "codex-mux-win-manager-header");
    const heading = make("div", "codex-mux-win-manager-heading");
    append(heading, make("div", "codex-mux-win-manager-kicker", "Codex Relay Pool"), title, description);
    const close = make("button", "codex-mux-win-toast-close codex-mux-win-close-button", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Close account settings");
    close.title = "Close";
    close.addEventListener("click", () => closeAccountManager(state));
    const add = make("button", "codex-mux-win-manager-add");
    add.type = "button";
    add.setAttribute("aria-label", "Add another subscription");
    append(add, make("span", "", "+"), make("span", "", "Add subscription"));
    add.addEventListener("click", async () => {
      if (add.disabled) return;
      try {
        await startSubscription(menu, add, state.accounts, state);
        state.accounts = await loadAccounts();
        renderAccountManager(state);
      } catch (error) {
        accountManagerStatus(state, `Could not add subscription: ${error.message}`);
      }
    });
    append(header, heading, add, close);
    const content = make("div", "codex-mux-win-manager-content");
    append(content, overview, list, status);
    append(dialog, header, content);
    backdrop.append(dialog);
    backdrop.addEventListener("click", (event) => {
      if (event.target === backdrop) closeAccountManager(state);
    });
    backdrop.addEventListener("keydown", (event) => {
      if (event.key === "Escape") closeAccountManager(state);
    });
    document.body.append(backdrop);
    activeAccountManager = state;
    renderAccountManager(state);
    if (typeof close.focus === "function") close.focus();
    return state;
  }

  function renderRoutingPanel(menu, accounts, routing) {
    if (!routing?.status) return;
    const byId = new Map(accounts.map((account) => [account.id, account]));
    const controller = accounts.find((account) => account.controller);
    const route = routing.thread?.route;
    const routeAccount = route ? byId.get(route.accountId) : null;
    const panel = make("section", "codex-mux-win-route-panel");
    append(
      panel,
      make("div", "codex-mux-win-title", routingText("title")),
    );
    const controllerRow = make("div", "codex-mux-win-route-row");
    append(controllerRow, make("span", "codex-mux-win-route-key", routingText("controller")), make("span", "codex-mux-win-route-value", controller ? accountName(controller) : routingText("unavailable")));
    const taskRow = make("div", "codex-mux-win-route-row");
    const taskLabel = route
      ? `${routeAccount ? accountName(routeAccount) : route.accountId} · ${routingText("generation")} ${route.generation}${route.recoveryRequired ? ` · ${routingText("recovery")}` : ""}`
      : routing.threadId ? routingText("noRoute") : routingText("openTask");
    append(taskRow, make("span", "codex-mux-win-route-key", routingText("current")), make("span", "codex-mux-win-route-value", taskLabel));
    panel.append(controllerRow, taskRow);
    const next = routing.status.nextCandidate;
    if (next) {
      const nextAccount = byId.get(next.accountId);
      const nextRow = make("div", "codex-mux-win-route-row");
      const nextLabel = `${nextAccount ? accountName(nextAccount) : next.label || next.accountId} · ${next.quotaKnown ? `${Math.round(next.remainingPercent)}% ${routingText("left")}` : routingText("probation")}`;
      append(nextRow, make("span", "codex-mux-win-route-key", routingText("next")), make("span", "codex-mux-win-route-value", nextLabel));
      panel.append(nextRow);
    }
    const effectiveRow = make("div", "codex-mux-win-route-row");
    const effectiveLabel = routing.status.effectivePolicy || routing.status.policy;
    append(effectiveRow, make("span", "codex-mux-win-route-key", routingText("effective")), make("span", "codex-mux-win-route-value", effectiveLabel));
    panel.append(effectiveRow);
    if (routing.status.handoffSupported === false) panel.append(make("div", "codex-mux-win-status", routingText("unsupported")));
    if (routing.lastDecision?.reason) {
      const decisionRow = make("div", "codex-mux-win-route-row");
      append(decisionRow, make("span", "codex-mux-win-route-key", routingText("lastDecision")), make("span", "codex-mux-win-route-value", routing.lastDecision.reason));
      panel.append(decisionRow);
    }
    if (route?.recoveryRequired && routing.threadId) {
      const recover = make("button", "codex-mux-win-policy-button", routingText("reviewed"));
      recover.type = "button";
      recover.addEventListener("click", async (event) => {
        event.preventDefault(); event.stopPropagation(); recover.disabled = true;
        try {
          await request("/thread-route/recover", { method: "POST", body: JSON.stringify({ threadId: routing.threadId }) });
          await refreshMenu(menu);
        } catch (error) {
          setMenuStatus(menu, `${routingText("clearFailed")}: ${error.message}`); recover.disabled = false;
        }
      });
      panel.append(recover);
    }
    const actions = make("div", "codex-mux-win-policy-actions");
    const policies = [["sticky", routingText("sticky")], ["balanced", routingText("balanced")], ["rotate-completed-turn", routingText("rotate")]];
    for (const [value, label] of policies) {
      const button = make("button", "codex-mux-win-policy-button", label);
      button.type = "button";
      button.setAttribute("aria-pressed", String(routing.status.policy === value));
      button.addEventListener("click", async (event) => {
        event.preventDefault();
        event.stopPropagation();
        button.disabled = true;
        try {
          await request("/routing/policy", { method: "PUT", body: JSON.stringify({ policy: value }) });
          await refreshMenu(menu);
        } catch (error) {
          setMenuStatus(menu, `${routingText("policyFailed")}: ${error.message}`);
          button.disabled = false;
        }
      });
      actions.append(button);
    }
    panel.append(actions);
    menu.append(panel);
  }

  function renderMenu(menu, accounts, routing = null) {
    const priorError = menu.querySelector(".codex-mux-win-error")?.textContent || "";
    menu.replaceChildren();
    const pool = poolPresentation(accounts, routing);

    if (menu.dataset?.codexMuxFallback === "true") {
      const connected = connectedAccounts(accounts);
      const authority = connected.find((account) => account.relayAuthority || account.controller) || connected[0] || null;
      const profile = make("div", "codex-mux-win-row");
      const identity = make("div", "codex-mux-win-identity");
      const labels = make("div", "codex-mux-win-labels");
      append(labels, make("div", "codex-mux-win-name", authority ? accountName(authority) : "Codex Relay"));
      append(identity, authority ? avatar(authority) : make("span", "codex-mux-win-summary-icon", "◔"), labels);
      profile.append(identity);
      menu.append(profile, make("div", "codex-mux-win-divider"));
    }

    const summary = make("div", "codex-mux-win-summary");
    const icon = make("div", "codex-mux-win-summary-icon", "◔");
    const label = make("div", "codex-mux-win-summary-label");
    append(
      label,
      make("div", "codex-mux-win-title", menu.dataset?.codexMuxFallback === "true" ? "Usage remaining" : routingText("sharedPool")),
      make("div", "codex-mux-win-subtext", pool.unknown > 0
        ? `${pool.known}/${pool.connected} ${routingText("poolUpdating")} · ${pool.unknown} updating`
        : `${pool.connected} ${routingText("poolWorkers")}`),
    );
    const quotaKnown = pool.connected > 0 && pool.known > 0;
    const totalLabel = quotaKnown ? `${Math.round(pool.remaining)}% / ${Math.round(pool.maximum)}% ${routingText("left")}` : "Updating…";
    append(summary, icon, label, make("div", `codex-mux-win-total${quotaKnown ? "" : " codex-mux-win-percent-muted"}`, totalLabel));
    menu.append(summary);

    const add = make("button", "codex-mux-win-add");
    add.type = "button";
    append(add, make("span", "codex-mux-win-plus", "+"), make("span", "", "Add another subscription"));
    add.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      startSubscription(menu, add, accounts).catch((error) => setMenuStatus(menu, error.message));
    });
    menu.append(make("div", "codex-mux-win-divider"), add);
    if (menu.dataset?.codexMuxFallback === "true") {
      const nativeSettings = make("button", "codex-mux-win-add codex-mux-win-settings");
      nativeSettings.type = "button";
      append(nativeSettings, make("span", "codex-mux-win-settings-icon", "⚙"), make("span", "", "Settings"));
      nativeSettings.addEventListener("click", (event) => {
        event.preventDefault();
        event.stopPropagation();
        openNativeSettingsFromFallback();
      });
      menu.append(nativeSettings);
    }
    const settings = make("button", "codex-mux-win-add codex-mux-win-settings");
    settings.type = "button";
    append(settings, make("span", "codex-mux-win-settings-icon", "⚙"), make("span", "", "Manage pool sources"));
    settings.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      openAccountSettings(menu, accounts, routing);
    });
    menu.append(settings);
    if (priorError) setMenuStatus(menu, priorError);
  }

  function readLoginValue(login, ...keys) {
    for (const key of keys) {
      const value = login?.[key];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
    return "";
  }

  function trustedVerificationURL(value) {
    try {
      const url = new URL(value);
      if (url.protocol !== "https:" || !["chatgpt.com", "auth.openai.com"].includes(url.hostname)) return "";
      return url.href;
    } catch {
      return "";
    }
  }

  function loginBridge() {
    const bridge = globalThis.codexMuxLoginWindow;
    if (
      bridge == null ||
      typeof bridge.open !== "function" ||
      typeof bridge.close !== "function"
    ) {
      return null;
    }
    return bridge;
  }

  function closeLoginBridge(id) {
    const bridge = loginBridge();
    if (!id || bridge == null) return;
    void Promise.resolve(bridge.close(id)).catch(() => {
      // The child window can already be gone when login completion races close.
    });
  }

  function closeLogin(expected = null) {
    if (expected && activeLogin !== expected) return false;
    const current = activeLogin;
    current?.timer && clearTimeout(current.timer);
    current?.unsubscribeNativeClose?.();
    closeLoginBridge(current?.nativeLoginId);
    current?.backdrop?.remove();
    if (!expected || activeLogin === expected) activeLogin = null;
    return Boolean(current);
  }

  function dismissToast(expected = null) {
    if (expected && activeToast !== expected) return false;
    const current = activeToast;
    current?.timer && clearTimeout(current.timer);
    current?.element?.remove();
    if (!expected || activeToast === expected) activeToast = null;
    return Boolean(current);
  }

  function showLoginSuccess(account) {
    dismissToast();
    const toast = make("section", "codex-mux-win-toast");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss sign-in confirmation");
    const message = append(
      make("div", "codex-mux-win-toast-copy"),
      make("div", "codex-mux-win-toast-title", `${account.label || "Subscription"} connected successfully`),
      make("div", "codex-mux-win-toast-detail", "This subscription is ready to use. Official browser sign-in is complete."),
    );
    append(toast, make("div", "codex-mux-win-toast-icon", "✓"), message, close);
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function showLoginCancelled(account) {
    dismissToast();
    const toast = make("section", "codex-mux-win-toast codex-mux-win-toast-neutral");
    toast.setAttribute("role", "status");
    toast.setAttribute("aria-live", "polite");
    const close = make("button", "codex-mux-win-toast-close", "×");
    close.type = "button";
    close.setAttribute("aria-label", "Dismiss cancellation confirmation");
    const message = append(
      make("div", "codex-mux-win-toast-copy"),
      make("div", "codex-mux-win-toast-title", `${account.label || "Subscription"} sign-in cancelled`),
      make("div", "codex-mux-win-toast-detail", "The unfinished subscription was removed. Your primary account was not changed."),
    );
    append(toast, make("div", "codex-mux-win-toast-icon", "×"), message, close);
    document.body.append(toast);
    const notice = { element: toast, timer: null };
    activeToast = notice;
    close.addEventListener("click", () => dismissToast(notice));
    notice.timer = window.setTimeout(() => dismissToast(notice), 5000);
  }

  function completeLoginOnce(session, account) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    session.completed = true;
    closeLogin(session);
    showLoginSuccess(account);
    // Refresh an open Manage pool sources dialog as soon as the browser
    // callback confirms the account; users should not need to close/reopen it.
    const managerState = session.managerState;
    if (managerState?.list?.isConnected) {
      void loadAccounts().then((accounts) => {
        if (managerState.dialog?.isConnected && activeAccountManager === managerState) {
          managerState.accounts = accounts;
          renderAccountManager(managerState);
        }
      }).catch(() => {});
    }
    schedule();
    return true;
  }

  async function cancelPendingAccount(accountId, loginId = "") {
    return request(`/accounts/${encodeURIComponent(accountId)}/login/cancel`, {
      method: "POST",
      body: JSON.stringify({ loginId: loginId || "" }),
    });
  }

  async function cancelPendingSubscription(menu, account, button = null) {
    if (!account?.id || button?.disabled) return;
    if (button) button.disabled = true;
    try {
      const result = await cancelPendingAccount(account.id);
      if (result.connected) {
        showLoginSuccess(result.account || account);
      } else {
        showLoginCancelled(account);
      }
      await refreshMenu(menu);
      return result;
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function cancelLogin(session, status, button) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    session.cancelling = true;
    session.timer && clearTimeout(session.timer);
    if (button) button.disabled = true;
    if (status) status.textContent = "Cancelling this unfinished subscription…";
    try {
      const result = await cancelPendingAccount(session.account.id, session.loginId);
      if (activeLogin !== session) return false;
      if (result.connected) {
        session.cancelling = false;
        return completeLoginOnce(session, result.account || session.account);
      }
      closeLogin(session);
      showLoginCancelled(session.account);
      if (session.menu) await refreshMenu(session.menu);
      return true;
    } catch (error) {
      if (activeLogin === session) {
        session.cancelling = false;
        if (button) button.disabled = false;
        if (status) status.textContent = `Could not cancel sign-in: ${error.message}`;
      }
      return false;
    }
  }

  async function openBrowserLogin(session, status, button) {
    if (activeLogin !== session || session.completed || session.cancelling) return false;
    if (session.nativeLoginId && !session.externalBrowser) {
      status.textContent = "The official ChatGPT sign-in flow is already open.";
      return true;
    }
    const bridge = session.loginBridge;
    if (bridge == null) {
      throw new Error("The official ChatGPT sign-in bridge is unavailable. Run the Router installer again before adding an account.");
    }
    session.opening = true;
    if (button) button.disabled = true;
    status.textContent = "Opening the official ChatGPT sign-in page in your browser…";
    try {
      if (session.externalBrowser && session.nativeLoginId) {
        closeLoginBridge(session.nativeLoginId);
        session.nativeLoginId = null;
      }
      const opened = await bridge.open(session.authorizationURL);
      if (!opened?.id) throw new Error("The official ChatGPT sign-in bridge did not return a valid session.");
      if (activeLogin !== session || session.completed || session.cancelling) {
        closeLoginBridge(opened.id);
        return false;
      }
      session.nativeLoginId = opened.id;
      session.externalBrowser = opened.mode === "external";
      status.textContent = session.externalBrowser
        ? "The official sign-in page is open in your default browser. Finish there, then return here; Relay will close this confirmation when the account connects."
        : "Complete the official ChatGPT sign-in flow. This confirmation closes automatically when the account connects.";
      return true;
    } catch (error) {
      if (activeLogin === session) {
        status.textContent = `Could not open the official ChatGPT sign-in page: ${error.message}`;
      }
      throw error;
    } finally {
      session.opening = false;
      if (button) button.disabled = false;
    }
  }

  async function showBrowserLogin(menu, account, login, managerState = null) {
    closeLogin();
    const authorizationURL = trustedVerificationURL(readLoginValue(login, "authUrl", "auth_url"));
    const loginId = readLoginValue(login, "loginId", "login_id");
    if (!authorizationURL || !loginId) {
      throw new Error("The official ChatGPT sign-in link was not available. The unfinished subscription will be removed.");
    }
    const bridge = loginBridge();
    if (bridge == null) {
      throw new Error("The official ChatGPT sign-in bridge is unavailable. Run the Router installer again before adding an account.");
    }
    const backdrop = make("div", "codex-mux-win-modal-backdrop");
    const session = {
      account,
      authorizationURL,
      backdrop,
      cancelling: false,
      completed: false,
      loginId,
      menu,
      managerState,
      nativeLoginId: null,
      externalBrowser: false,
      opening: false,
      loginBridge: bridge,
      timer: null,
      unsubscribeNativeClose: null,
    };
    const dialog = make("section", "codex-mux-win-modal");
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    append(
      dialog,
      make("h2", "", `Sign in to ${account.label || "subscription"}`),
      make("p", "", "Continue with the official ChatGPT sign-in page in your normal browser. Relay never asks for your password and the Codex child keeps this subscription isolated."),
    );
    dialog.append(make("p", "", "If the browser is already signed in to another ChatGPT account, choose the account switch option there. Keep this confirmation open while sign-in finishes; it closes automatically when Relay confirms the account."));
    const status = make("div", "codex-mux-win-status", "Preparing the official browser sign-in…");
    const actions = make("div", "codex-mux-win-actions");
    const open = make("button", "codex-mux-win-primary", "Open secure sign-in");
    open.type = "button";
    open.addEventListener("click", () => {
      void openBrowserLogin(session, status, open).catch(() => {
        // The status message already explains why the native window failed.
      });
    });
    const cancel = make("button", "", "Cancel sign-in");
    cancel.type = "button";
    cancel.addEventListener("click", () => { void cancelLogin(session, status, cancel); });
    append(actions, open, cancel);
    append(dialog, actions, status);
    backdrop.addEventListener("click", (event) => {
      if (event.target === backdrop) void cancelLogin(session, status, cancel);
    });
    backdrop.append(dialog);
    document.body.append(backdrop);
    activeLogin = session;
    if (typeof bridge.subscribeClosed === "function") {
      session.unsubscribeNativeClose = bridge.subscribeClosed((closed) => {
        if (activeLogin !== session || closed?.id !== session.nativeLoginId) return;
        session.nativeLoginId = null;
        if (!session.completed && !session.cancelling) {
          status.textContent = session.externalBrowser
            ? "The browser sign-in flow was closed by Relay. Open the official sign-in page again to continue, or cancel this unfinished subscription."
            : "The official ChatGPT sign-in flow was closed. Open it again to continue, or cancel this unfinished subscription.";
        }
      });
    }

    const poll = async () => {
      if (activeLogin !== session || session.completed || session.cancelling) return;
      try {
        const accounts = await loadAccounts();
        if (activeLogin !== session || session.completed) return;
        const updated = accounts.find((item) => item.id === account.id);
        if (updated?.connected) {
          completeLoginOnce(session, updated);
          return;
        }
      } catch {
        // A temporary control-service restart should not discard an official browser flow.
      }
      if (activeLogin === session && !session.completed) {
        session.timer = window.setTimeout(poll, 1500);
      }
    };
    void poll();
    try {
      await openBrowserLogin(session, status, open);
    } catch (error) {
      closeLogin(session);
      throw error;
    }
  }

  async function startSubscription(menu, button, currentAccounts, managerState = null) {
    if (button.disabled) return;
    button.disabled = true;
    const caption = button.lastElementChild;
    if (caption) caption.textContent = "Adding subscription…";
    setMenuStatus(menu, "");
    try {
      const created = await request("/accounts", {
        method: "POST",
        body: JSON.stringify({ label: `Subscription ${currentAccounts.length + 1}` }),
      });
      const account = created.account;
      if (!account?.id) throw new Error("Router did not return a new subscription ID");
      let result;
      try {
        result = await request(`/accounts/${encodeURIComponent(account.id)}/login`, {
          method: "POST",
          body: JSON.stringify({ mode: "chatgpt" }),
        });
      } catch (error) {
        // The account exists before the app-server begins browser login. Roll
        // it back best-effort so a start failure cannot create a ghost row.
        await cancelPendingAccount(account.id).catch(() => {});
        throw error;
      }
      try {
        await showBrowserLogin(menu, account, result.login || {}, managerState);
      } catch (error) {
        await cancelPendingAccount(account.id, readLoginValue(result.login, "loginId", "login_id")).catch(() => {});
        throw error;
      }
      await refreshMenu(menu);
    } finally {
      button.disabled = false;
      if (caption) caption.textContent = "Add another subscription";
    }
  }

  async function startExistingSubscription(menu, account, button, state = null) {
    if (button?.disabled || !account?.id) return;
    if (button) button.disabled = true;
    if (state) accountManagerStatus(state, `Opening official sign-in for ${accountName(account)}…`);
    try {
      const result = await request(`/accounts/${encodeURIComponent(account.id)}/login`, {
        method: "POST",
        body: JSON.stringify({ mode: "chatgpt" }),
      });
      await showBrowserLogin(menu, account, result.login || {}, state);
      if (state) {
        state.accounts = await loadAccounts();
        renderAccountManager(state);
      }
      await refreshMenu(menu);
    } catch (error) {
      if (state) accountManagerStatus(state, `Could not start sign-in: ${error.message}`);
      else setMenuStatus(menu, `Could not start sign-in: ${error.message}`);
    } finally {
      if (button) button.disabled = false;
    }
  }

  async function refreshMenu(menu) {
    menu.dataset.codexMuxLastRefresh = String(Date.now());
    try {
      const [accounts, routing] = await Promise.all([loadAccounts(), routingContext().catch(() => null)]);
      renderMenu(menu, accounts, routing);
    } catch (error) {
      setMenuStatus(menu, `Router unavailable: ${error.message}`);
    }
  }

  function installIntoProfileMenu() {
    scheduled = false;
    const placement = profileMenuPlacement();
    if (!placement) return;
    let menu = document.getElementById(MENU_ID);
    let inserted = false;
    if (!menu || menu.parentElement !== placement.parent) {
      menu?.remove();
      menu = make("section", "", "");
      menu.id = MENU_ID;
      menu.setAttribute("aria-label", "Codex Relay");
      menu.addEventListener("pointerdown", (event) => event.stopPropagation());
      menu.addEventListener("click", (event) => event.stopPropagation());
      placement.parent.insertBefore(menu, placement.before);
      inserted = true;
    }
    const lastRefresh = Number(menu.dataset.codexMuxLastRefresh || 0);
    if (inserted || Date.now() - lastRefresh > 5000) void refreshMenu(menu);
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    window.setTimeout(() => {
      installIntoProfileMenu();
      installFallbackPoolTrigger();
      installUsageBillingSurface();
      installTaskRouteBadge();
      const fallbackMenu = document.getElementById?.(FALLBACK_MENU_ID);
      const lastMenuRefresh = Number(fallbackMenu?.dataset?.codexMuxLastRefresh || 0);
      if (fallbackMenu?.isConnected && Date.now() - lastMenuRefresh >= LIVE_QUOTA_REFRESH_MS) {
        void refreshMenu(fallbackMenu);
      }
    }, 50);
  }

  function start() {
    addStyles();
    startUpdateWatcher();
    startRoutingWatcher();
    new MutationObserver(schedule).observe(document.documentElement, { childList: true, subtree: true });
    window.setInterval(schedule, 1000);
    schedule();
  }

  // These are deliberately small, version-neutral renderer hooks. The
  // version-pinned bundle patches call them for profile data, per-account
  // Plugins RPC routing, and the native reset sheet. They do not expose a
  // password or a credential to the renderer.
  globalThis.CodexMuxWindows = {
    consumeRateLimitReset,
    getResetAccountId,
    profileData,
    rateLimitResets,
    routingContext,
    scopePluginRequest,
    selectedResetUsageWindows,
    subscribeReset,
    usageStatus: nativeUsageStatus,
    usageStatusAll: nativeUsageStatusAll,
  };
  registerSurfaceElements();

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start, { once: true });
  } else {
    start();
  }
})();

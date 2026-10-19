import { state, isActiveTarget } from "./state.js";
import { fetchProgress } from "./api.js";
import { updateAutoUpdateStatus, updateGlobalSyncBadge, renderOverviewDashboard } from "./overview.js";
import { renderDashboardSidebar, updateDashboardDetails, updateTargetCard } from "./sidebar.js";
import { updateTerminal, appendTerminalLine } from "./dock.js";
import { refreshMeta, renderGallery } from "./gallery.js";
import { toast } from "./utils.js";

let routeApplied = false;
let onFirstLoad = null;

// whenReady registers the initial route render, run once the target list is known.
export function whenReady(fn) { onFirstLoad = fn; }

export async function pollProgress() {
  if (state.sseConnected || state.progressPollTimeout) return;
  try {
    await loadProgress();
  } catch (_) {}
  if (state.sseConnected || state.progressPollTimeout) return;
  const busy = state.cachedProgress.some(t => t.status === "running" || t.status === "queued");
  state.progressPollTimeout = setTimeout(() => {
    state.progressPollTimeout = null;
    pollProgress();
  }, busy ? 1500 : 5000);
}

function scheduleSSEReconnect() {
  if (state.sseReconnectTimeout) return;
  state.sseReconnectTimeout = setTimeout(() => {
    state.sseReconnectTimeout = null;
    initSSE();
  }, 10000);
}

export function initSSE() {
  if (state.sseSource || state.sseReconnectTimeout) return;
  try {
    state.sseSource = new EventSource("/api/events");
  } catch (_) {
    return;
  }
  state.sseSource.addEventListener("hello", () => {
    state.sseConnected = true;
    clearTimeout(state.progressPollTimeout);
    state.progressPollTimeout = null;
    loadProgress();
  });
  state.sseSource.onmessage = (e) => {
    try {
      handleSSEEvent(JSON.parse(e.data));
    } catch (err) {
      console.error("SSE parse error:", err);
    }
  };
  state.sseSource.onerror = () => {
    state.sseConnected = false;
    state.sseSource?.close();
    state.sseSource = null;
    pollProgress();
    scheduleSSEReconnect();
  };
}

// Same handle on two platforms is two targets; older events carry no platform.
const matchesEvent = (t, evt) => t.username === evt.username && (!evt.platform || t.platform === evt.platform);

function handleSSEEvent(evt) {
  if (evt.type === "log") {
    const level = (evt.level || "info").toLowerCase();
    const now = new Date().toISOString();
    const message = evt.username && evt.username !== "system" ? `[@${evt.username}] ${evt.message}` : evt.message;
    state.globalLogs.push({ timestamp: now, level, message });
    if (state.globalLogs.length > 1000) state.globalLogs = state.globalLogs.slice(-1000);

    const target = state.cachedProgress.find(t => matchesEvent(t, evt));
    if (target) {
      (target.logs ||= []).push({ timestamp: now, level, message: evt.message });
      if (target.logs.length > 1000) target.logs = target.logs.slice(-1000);
    }
    const dockLog = document.getElementById("dock-latest-log");
    if (dockLog && evt.message) dockLog.textContent = message;
    if (!state.target) appendTerminalLine({ timestamp: now, level, message });
    else if (isActiveTarget(evt.username, evt.platform)) appendTerminalLine({ timestamp: now, level, message: evt.message });
    return;
  }

  if (evt.type === "status" || evt.type === "progress") {
    const target = state.cachedProgress.find(t => matchesEvent(t, evt));
    if (target) {
      if (evt.status) target.status = evt.status;
      target.progress = evt.progress || 0;
      updateTargetCard(target);
    }
    if (isActiveTarget(evt.username, evt.platform)) updateDashboardDetails();
    if (evt.type === "progress") return;

    updateGlobalSyncBadge();
    if (evt.status === "completed" || evt.status === "failed") {
      loadProgress();
      if (evt.status === "completed" && isActiveTarget(evt.username, evt.platform)) {
        // new media landed for the open gallery; re-render only if that cannot yank the user's scroll position or selection
        const nearTop = document.getElementById("workspace-scroll-area").scrollTop < 300;
        refreshMeta().then(() => { if (nearTop && !state.selecting) renderGallery(); });
      }
    }
  }
}

export async function loadProgress() {
  try {
    const data = await fetchProgress();
    state.cachedProgress = data.targets || [];
    if (data.global_logs?.length) state.globalLogs = data.global_logs;
    state.lastSyncTime = data.last_sync || "";
    state.autoSyncInterval = data.auto_sync_interval || 0;

    updateAutoUpdateStatus();
    updateGlobalSyncBadge();
    renderDashboardSidebar();
    if (!routeApplied) {
      routeApplied = true;
      onFirstLoad?.();
    } else if (state.target) {
      updateDashboardDetails();
    } else {
      renderOverviewDashboard();
    }
    updateTerminal();
  } catch (err) {
    toast(`Failed to load progress: ${err.message}`, "error");
  }
}

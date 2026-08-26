import { state } from "./state.js";
import { fetchProgress } from "./api.js";
import { updateAutoUpdateStatus, updateGlobalSyncBadge, renderOverviewDashboard } from "./overview.js";
import { renderDashboardSidebar, updateDashboardDetails } from "./sidebar.js";
import { updateTerminal } from "./dock.js";
import { toast } from "./utils.js";

export async function pollProgress() {
  if (state.sseConnected) return;
  if (state.progressPollTimeout) return;
  try {
    await loadProgress();
  } catch (err) {
    console.error("Error loading progress:", err);
  }
  if (state.sseConnected || state.progressPollTimeout) return;
  const isRunning = state.cachedProgress.some(t => t.status === "running" || t.status === "queued");
  const delay = isRunning ? 1500 : 5000;
  state.progressPollTimeout = setTimeout(() => {
    state.progressPollTimeout = null;
    pollProgress();
  }, delay);
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
  } catch (e) {
    console.warn("SSE not supported, falling back to polling");
    return;
  }

  state.sseSource.addEventListener("hello", () => {
    state.sseConnected = true;
    if (state.progressPollTimeout) {
      clearTimeout(state.progressPollTimeout);
      state.progressPollTimeout = null;
    }
    if (state.sseReconnectTimeout) {
      clearTimeout(state.sseReconnectTimeout);
      state.sseReconnectTimeout = null;
    }
    loadProgress();
  });

  state.sseSource.onmessage = (e) => {
    try {
      const evt = JSON.parse(e.data);
      handleSSEEvent(evt);
    } catch (err) {
      console.error("SSE parse error:", err);
    }
  };

  state.sseSource.onerror = () => {
    state.sseConnected = false;
    if (state.sseSource) state.sseSource.close();
    state.sseSource = null;
    if (!state.progressPollTimeout) pollProgress();
    scheduleSSEReconnect();
  };
}

export function handleSSEEvent(evt) {
  if (evt.type === "log") {
    const globalMsg = evt.username && evt.username !== "system" ? `[@${evt.username}] ${evt.message}` : evt.message;
    state.globalLogs.push({
      timestamp: new Date().toISOString(),
      level: (evt.level || "info").toLowerCase(),
      message: globalMsg
    });
    if (state.globalLogs.length > 1000) state.globalLogs = state.globalLogs.slice(-1000);

    const target = state.cachedProgress.find(t => t.username === evt.username);
    if (target) {
      if (!target.logs) target.logs = [];
      target.logs.push({
        timestamp: new Date().toISOString(),
        level: (evt.level || "info").toLowerCase(),
        message: evt.message
      });
      if (target.logs.length > 1000) target.logs = target.logs.slice(-1000);
    }
    const dockLog = document.getElementById("dock-latest-log");
    if (dockLog && evt.message) {
      dockLog.textContent = globalMsg;
    }
    if (!state.activeTerminalUser || state.activeTerminalUser === evt.username) {
      updateTerminal();
    }
  } else if (evt.type === "status") {
    const target = state.cachedProgress.find(t => t.username === evt.username);
    if (target) {
      target.status = evt.status;
      target.progress = evt.progress;
    }
    updateGlobalSyncBadge();
    renderDashboardSidebar();
    if (state.activeTerminalUser === evt.username) {
      updateDashboardDetails();
    }
    if (evt.status === "completed" || evt.status === "failed") {
      loadProgress();
    }
  }
}

export async function loadProgress() {
  try {
    const data = await fetchProgress();
    state.cachedProgress = data.targets || [];
    if (data.global_logs && data.global_logs.length > 0) {
      state.globalLogs = data.global_logs;
    }
    state.lastSyncTime = data.last_sync || "";
    state.autoSyncInterval = data.auto_sync_interval || 0;

    updateAutoUpdateStatus();
    updateGlobalSyncBadge();
    renderDashboardSidebar();

    if (!state.activeTerminalUser) {
      renderOverviewDashboard();
    } else {
      updateDashboardDetails();
    }
    updateTerminal();
  } catch (err) {
    console.error("Progress fetch error:", err);
    toast(`Failed to load progress: ${err.message}`, "error");
  }
}

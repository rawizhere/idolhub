// ==================== Utilities ====================

function debounce(fn, ms) {
  let t;
  return function (...args) {
    clearTimeout(t);
    t = setTimeout(() => fn.apply(this, args), ms);
  };
}

function escapeHtml(text) {
  return String(text)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/\n/g, "<br>");
}

function withLoading(btn, fn) {
  if (!btn) return fn();
  if (btn.dataset.loading === "1") return Promise.resolve();
  btn.dataset.loading = "1";
  btn.dataset.disabledPrev = btn.disabled ? "1" : "0";
  btn.disabled = true;
  const originalHTML = btn.innerHTML;
  const label = btn.textContent.trim() || "Working";
  btn.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 animate-spin inline-block align-middle"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg> ${label}`;
  return Promise.resolve(fn()).finally(() => {
    btn.innerHTML = originalHTML;
    btn.disabled = btn.dataset.disabledPrev === "1";
    delete btn.dataset.loading;
    delete btn.dataset.disabledPrev;
  });
}

let toastSeq = 0;
function toast(message, type = "info", timeout = 3800) {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const id = ++toastSeq;
  const svgPaths = {
    success: '<path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/>',
    error: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>',
    info: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>',
  };
  const tones = {
    success: { bg: "bg-emerald-50", text: "text-emerald-800", border: "border-emerald-200", iconColor: "text-emerald-500" },
    error: { bg: "bg-rose-50", text: "text-rose-800", border: "border-rose-200", iconColor: "text-rose-500" },
    info: { bg: "bg-white", text: "text-slate-700", border: "border-slate-200", iconColor: "text-indigo-500" },
  };
  const t = tones[type] || tones.info;
  const iconPath = svgPaths[type] || svgPaths.info;
  const closeSvg = '<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>';
  const el = document.createElement("div");
  el.className = `pointer-events-auto flex items-start gap-2.5 ${t.bg} ${t.text} border ${t.border} shadow-lg rounded-xl pl-3 pr-3.5 py-2.5 max-w-sm text-xs font-medium translate-x-4 opacity-0 transition-all duration-200`;
  el.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4 ${t.iconColor} mt-px flex-shrink-0">${iconPath}</svg><span class="flex-1 leading-relaxed">${escapeHtml(message)}</span><button class="text-slate-400 hover:text-slate-700 cursor-pointer flex-shrink-0 -mt-0.5" aria-label="Dismiss"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5">${closeSvg}</svg></button>`;
  container.appendChild(el);
  requestAnimationFrame(() => {
    el.classList.remove("translate-x-4", "opacity-0");
  });
  const close = () => {
    el.classList.add("translate-x-4", "opacity-0");
    setTimeout(() => el.remove(), 200);
  };
  el.querySelector("button").addEventListener("click", close);
  setTimeout(close, timeout);
}

let confirmSeq = 0;
function confirmDialog({ title = "Confirm", message = "", confirmText = "Confirm", cancelText = "Cancel", tone = "danger" } = {}) {
  return new Promise((resolve) => {
    const mount = document.getElementById("confirm-mount");
    if (!mount) { resolve(window.confirm(message)); return; }
    const id = ++confirmSeq;
    const toneClass = tone === "danger"
      ? "bg-rose-600 hover:bg-rose-700 text-white"
      : "bg-indigo-600 hover:bg-indigo-700 text-white";
    const headerIcon = tone === "danger"
      ? '<path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>'
      : '<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/>';
    const overlay = document.createElement("div");
    overlay.className = "fixed inset-0 z-[150] flex items-center justify-center bg-black/60 backdrop-blur-sm p-4 opacity-0 transition-opacity duration-150";
    overlay.innerHTML = `
      <div class="bg-white border border-slate-200 rounded-2xl p-6 w-full max-w-sm shadow-xl flex flex-col gap-4 scale-95 transition-transform duration-150">
        <div class="flex items-start gap-3">
          <div class="p-2 ${tone === "danger" ? "bg-rose-50 text-rose-600" : "bg-indigo-50 text-indigo-600"} rounded-lg flex-shrink-0">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4">${headerIcon}</svg>
          </div>
          <div class="flex-1 min-w-0">
            <h3 class="text-sm font-bold text-slate-800">${escapeHtml(title)}</h3>
            <p class="text-xs text-slate-500 mt-1 leading-relaxed">${escapeHtml(message)}</p>
          </div>
        </div>
        <div class="flex items-center justify-end gap-2">
          <button data-act="cancel" class="text-xs font-semibold text-slate-600 hover:text-slate-900 bg-slate-50 hover:bg-slate-100 border border-slate-200 px-3.5 py-2 rounded-lg transition-all cursor-pointer">${escapeHtml(cancelText)}</button>
          <button data-act="confirm" class="text-xs font-semibold px-3.5 py-2 rounded-lg transition-all cursor-pointer ${toneClass}">${escapeHtml(confirmText)}</button>
        </div>
      </div>`;
    mount.appendChild(overlay);
    requestAnimationFrame(() => {
      overlay.classList.remove("opacity-0");
      overlay.firstElementChild.classList.remove("scale-95");
    });
    const panel = overlay.firstElementChild;
    const close = (result) => {
      overlay.classList.add("opacity-0");
      panel.classList.add("scale-95");
      setTimeout(() => overlay.remove(), 150);
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); close(false); }
      else if (e.key === "Enter") { e.preventDefault(); close(true); }
    };
    panel.querySelector('[data-act="confirm"]').addEventListener("click", () => close(true));
    panel.querySelector('[data-act="cancel"]').addEventListener("click", () => close(false));
    overlay.addEventListener("click", (e) => { if (e.target === overlay) close(false); });
    document.addEventListener("keydown", onKey);
    setTimeout(() => panel.querySelector('[data-act="confirm"]').focus(), 50);
  });
}

// ==================== Global State & Dashboard ====================

let activeTerminalUser = null;
let cachedProgress = [];
let currentMainTab = "dashboard";
let lastSyncTime = "";
let autoSyncInterval = 0;

let progressPollTimeout = null;
let countdownTicker = null;
let sseSource = null;
let sseConnected = false;

let terminalLevel = "all";

async function pollProgress() {
  if (sseConnected) return;
  try {
    await loadProgress();
  } catch (err) {
    console.error("Error loading progress:", err);
  }
  if (sseConnected) return;
  const isRunning = cachedProgress.some(t => t.status === "running");
  const delay = isRunning ? 1500 : 5000;
  progressPollTimeout = setTimeout(pollProgress, delay);
}

function initSSE() {
  if (sseSource) return;
  try {
    sseSource = new EventSource("/api/events");
  } catch (e) {
    console.warn("SSE not supported, falling back to polling");
    return;
  }

  sseSource.addEventListener("hello", () => {
    console.log("SSE connected");
    sseConnected = true;
    if (progressPollTimeout) {
      clearTimeout(progressPollTimeout);
      progressPollTimeout = null;
    }
    loadProgress();
  });

  sseSource.onmessage = (e) => {
    try {
      const evt = JSON.parse(e.data);
      handleSSEEvent(evt);
    } catch (err) {
      console.error("SSE parse error:", err);
    }
  };

  sseSource.onerror = () => {
    console.warn("SSE disconnected, falling back to polling");
    sseConnected = false;
    sseSource.close();
    sseSource = null;
    if (!progressPollTimeout) {
      pollProgress();
    }
    setTimeout(() => initSSE(), 10000);
  };
}

function handleSSEEvent(evt) {
  if (evt.type === "log") {
    const target = cachedProgress.find(t => t.username === evt.username);
    if (target) {
      if (!target.logs) target.logs = [];
      target.logs.push({
        timestamp: new Date().toISOString(),
        level: evt.level,
        message: evt.message
      });
      if (target.logs.length > 1000) target.logs = target.logs.slice(-1000);
    }
    if (activeTerminalUser === evt.username) {
      updateTerminal();
    }
  } else if (evt.type === "status") {
    const target = cachedProgress.find(t => t.username === evt.username);
    if (target) {
      target.status = evt.status;
      target.progress = evt.progress;
    }
    renderDashboardSidebar();
    if (activeTerminalUser === evt.username) {
      updateDashboardDetails();
    }
    if (evt.status === "completed" || evt.status === "failed") {
      loadProgress();
    }
  }
}

document.addEventListener("DOMContentLoaded", () => {
  loadConfig();
  pollProgress();
  initSSE();
  startCountdownTicker();
  updateSidebarVisibility();
  setTerminalLevel("all");

  document.getElementById("add-account-form").addEventListener("submit", addAccount);
  document.getElementById("settings-form").addEventListener("submit", saveSettings);

  const scrollContainer = document.getElementById("gallery-scroll-container");
  if (scrollContainer) {
    scrollContainer.addEventListener("scroll", () => {
      const topBtn = document.getElementById("btn-back-to-top");
      if (topBtn) {
        if (scrollContainer.scrollTop > 300) {
          topBtn.classList.remove("scale-0", "opacity-0");
          topBtn.classList.add("scale-100", "opacity-100");
        } else {
          topBtn.classList.remove("scale-100", "opacity-100");
          topBtn.classList.add("scale-0", "opacity-0");
        }
      }
    });
  }

  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      let closedAny = false;
      if (pswpGrid && pswpGrid.pswp) { pswpGrid.pswp.close(); closedAny = true; }
      if (pswpPosts && pswpPosts.pswp) { pswpPosts.pswp.close(); closedAny = true; }
      if (closedAny) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }
    const tag = (e.target.tagName || "").toLowerCase();
    const typing = tag === "input" || tag === "textarea" || tag === "select" || e.target.isContentEditable;
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return;

    if (e.key === "/") {
      const target = currentView === "posts"
        ? document.getElementById("posts-search-input")
        : document.getElementById("grid-search-input");
      if (target && document.getElementById(currentView === "posts" ? "posts-search-container" : "grid-search-container").style.display !== "none") {
        e.preventDefault();
        target.focus();
      }
    } else if (e.key === "g" && activeTerminalUser) {
      e.preventDefault();
      switchGalleryView(currentView === "grid" ? "posts" : "grid");
    }
  }, true);
});

async function loadConfig() {
  try {
    const res = await fetch("/api/config");
    if (!res.ok) throw new Error("Failed to load configuration");
    const data = await res.json();
    document.getElementById("concurrency").value = data.concurrency || 5;
    document.getElementById("twitter-auth-token").value = data.twitter_auth_token || "";
    document.getElementById("instagram-session-id").value = data.instagram_session_id || "";
    document.getElementById("auto-sync-interval").value = data.auto_sync_interval || 0;
    setInstagramMode(data.instagram_mode || "picuki");
  } catch (err) {
    console.error("Config load error:", err);
    toast(`Failed to fetch configuration: ${err.message}`, "error");
  }
}

async function saveSettings() {
  const btn = document.getElementById("btn-save-settings");
  await withLoading(btn, async () => {
    const concurrency = parseInt(document.getElementById("concurrency").value);
    const twitterAuthToken = document.getElementById("twitter-auth-token").value;
    const instagramSessionID = document.getElementById("instagram-session-id").value;
    const instagramMode = __igMode || "picuki";
    const autoSyncInterval = parseInt(document.getElementById("auto-sync-interval").value) || 0;
    try {
      const res = await fetch("/api/config");
      const current = await res.json();
      current.concurrency = concurrency;
      current.twitter_auth_token = twitterAuthToken;
      current.instagram_session_id = instagramSessionID;
      current.instagram_mode = instagramMode;
      current.auto_sync_interval = autoSyncInterval;
      const saveRes = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current)
      });
      if (!saveRes.ok) {
        const body = await saveRes.text().catch(() => "Unknown error");
        throw new Error(`Failed to save settings: ${body}`);
      }
      toast("Settings saved.", "success");
    } catch (err) {
      console.error("Save config error:", err);
      toast(`Failed to save settings: ${err.message}`, "error");
    }
  });
}

let __igMode = "picuki";
function setInstagramMode(mode) {
  __igMode = mode;
  document.querySelectorAll(".ig-mode-btn").forEach(btn => {
    if (btn.dataset.mode === mode) {
      btn.classList.add("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.remove("text-slate-500", "hover:text-slate-800");
    } else {
      btn.classList.remove("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.add("text-slate-500", "hover:text-slate-800");
    }
  });
  const sessionContainer = document.getElementById("ig-session-container");
  if (sessionContainer) {
    sessionContainer.style.display = mode === "direct" ? "flex" : "none";
  }
}
window.setInstagramMode = setInstagramMode;

async function loadProgress() {
  try {
    const res = await fetch("/api/progress");
    if (!res.ok) throw new Error("Failed to load progress details");
    const data = await res.json();
    cachedProgress = data.targets || [];
    lastSyncTime = data.last_sync || "";
    autoSyncInterval = data.auto_sync_interval || 0;

    updateAutoUpdateStatus();
    renderDashboardSidebar();
    
    if (!activeTerminalUser) {
      renderOverviewDashboard();
    } else {
      updateDashboardDetails();
      updateTerminal();
    }
    updateSidebarVisibility();
  } catch (err) {
    console.error("Progress fetch error:", err);
  }
}

function updateSidebarVisibility() {
  const overviewBtn = document.getElementById("btn-overview");
  if (overviewBtn) {
    if (activeTerminalUser) {
      overviewBtn.classList.remove("hidden");
      overviewBtn.classList.add("flex");
    } else {
      overviewBtn.classList.add("hidden");
      overviewBtn.classList.remove("flex");
    }
  }
}

function goToOverview() {
  activeTerminalUser = null;
  document.getElementById("dashboard-target-header").style.display = "none";
  document.getElementById("dashboard-gallery-card").style.display = "none";
  document.getElementById("dashboard-empty").style.display = "flex";
  renderDashboardSidebar();
  renderOverviewDashboard();
  updateSidebarVisibility();
  const terminal = document.getElementById("terminal-body");
  if (terminal) terminal.innerHTML = `<div class="text-slate-500">[SYSTEM] Select a target to view live logs.</div>`;
}
window.goToOverview = goToOverview;

function renderDashboardSidebar() {
  const container = document.getElementById("dashboard-sidebar-list");
  if (cachedProgress.length === 0) {
    container.innerHTML = `<p class="text-xs text-slate-400 py-3 text-center">No targets configured</p>`;
    return;
  }
  
  let html = "";
  cachedProgress.forEach(target => {
    const isActive = activeTerminalUser === target.username;
    const mediaCount = target.media_count || 0;
    
    const cardBorderClass = isActive 
      ? "border-indigo-400 bg-indigo-50/40 text-indigo-900" 
      : "border-slate-200 bg-white hover:bg-slate-50 text-slate-700";

    let platformBadgeClass = "bg-pink-50 text-pink-700 border-pink-100";
    if (target.platform === "twitter") {
      platformBadgeClass = "bg-sky-50 text-sky-700 border-sky-100";
    }

    let statusDotClass = "bg-slate-350";
    if (target.status === "running") {
      statusDotClass = "bg-indigo-500 animate-pulse";
    } else if (target.status === "completed") {
      statusDotClass = "bg-emerald-500";
    } else if (target.status === "failed") {
      statusDotClass = "bg-rose-500";
    }

    const authErrorDot = target.auth_error
      ? `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="w-3 h-3 text-rose-500 flex-shrink-0" title="Cookie expired"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 11"/></svg>`
      : "";

    html += `
      <div class="flex items-center justify-between p-3.5 rounded-xl border transition-all duration-200 cursor-pointer shadow-sm ${cardBorderClass}" onclick="selectTerminalUser('${target.username}')">
        <div class="flex flex-col gap-1 min-w-0">
          <span class="text-[9px] font-bold uppercase px-1.5 py-0.5 rounded border self-start ${platformBadgeClass}">
            ${target.platform}
          </span>
          <div class="flex items-center gap-1.5 min-w-0">
            <span class="w-1.5 h-1.5 rounded-full ${statusDotClass} flex-shrink-0"></span>
            <span class="text-xs font-bold tracking-tight truncate">@${target.username}</span>
            ${authErrorDot}
          </div>
        </div>
        <span class="text-[10px] bg-slate-100 text-slate-600 px-2 py-0.5 rounded-full border border-slate-200 font-semibold flex-shrink-0">${mediaCount} files</span>
      </div>
    `;
  });
  
  container.innerHTML = html;
}

function selectTerminalUser(username) {
  activeTerminalUser = username;
  renderDashboardSidebar();
  updateSidebarVisibility();

  const target = cachedProgress.find(t => t.username === username);
  if (!target) return;

  // Show details panels
  document.getElementById("dashboard-empty").style.display = "none";
  document.getElementById("dashboard-target-header").style.display = "flex";
  document.getElementById("dashboard-gallery-card").style.display = "flex";

  // Username and stats
  document.getElementById("dashboard-username-label").textContent = `@${target.username}`;
  
  // Platform badge styling
  const platformBadge = document.getElementById("dashboard-platform-badge");
  platformBadge.textContent = target.platform.toUpperCase();
  if (target.platform === "instagram") {
    platformBadge.className = "text-[10px] font-bold uppercase px-2 py-0.5 rounded border bg-pink-50 text-pink-700 border-pink-100";
  } else {
    platformBadge.className = "text-[10px] font-bold uppercase px-2 py-0.5 rounded border bg-sky-50 text-sky-700 border-sky-100";
  }

  // Set action buttons dynamically
  const isRunning = target.status === "running";
  const syncBtn = document.getElementById("dashboard-btn-sync");
  syncBtn.onclick = () => startSync(target.username, true);
  
  document.getElementById("dashboard-btn-edit").onclick = () => {
    fetch("/api/config")
      .then(res => res.json())
      .then(cfg => {
        const acc = cfg.accounts.find(a => a.username.toLowerCase() === target.username.toLowerCase());
        if (acc) {
          window.dispatchEvent(new CustomEvent("open-edit-modal", {
            detail: {
              username: acc.username,
              platform: acc.platform,
              save_text: acc.save_text || false,
              skip_retweets: acc.skip_retweets || false,
              download_photos: acc.download_photos !== false,
              download_videos: acc.download_videos !== false,
              filters: acc.filters || []
            }
          }));
        }
      });
  };

  document.getElementById("dashboard-btn-delete").onclick = async () => {
    const ok = await confirmDialog({
      title: "Delete target account",
      message: `Remove @${target.username} from configuration? Downloaded files stay on disk unless you clear the folder.`,
      confirmText: "Delete",
      tone: "danger",
    });
    if (!ok) return;
    deleteAccount(target.username);
    activeTerminalUser = null;
    document.getElementById("dashboard-target-header").style.display = "none";
    document.getElementById("dashboard-gallery-card").style.display = "none";
    document.getElementById("dashboard-empty").style.display = "flex";
    renderDashboardSidebar();
    updateSidebarVisibility();
  };

  document.getElementById("dashboard-btn-clear").onclick = async () => {
    const ok = await confirmDialog({
      title: "Clear downloads folder",
      message: `Delete all downloaded media for @${target.username}? This cannot be undone.`,
      confirmText: "Clear",
      tone: "danger",
    });
    if (!ok) return;
    clearTargetFolder(target.platform, target.username);
  };

  document.getElementById("dashboard-btn-cancel").onclick = () => cancelSync(target.username);

  // Set active target inside console card
  document.getElementById("current-terminal-target").innerText = `@${target.username}`;

  // Update details (progress, states)
  updateDashboardDetails();

  // Load console logs
  const terminal = document.getElementById("terminal-body");
  terminal.innerHTML = "";
  updateTerminal();

  // Automatically select and load target gallery details
  selectGalleryTarget(target.platform, target.username);
}
window.selectTerminalUser = selectTerminalUser;

function updateDashboardDetails() {
  if (!activeTerminalUser) {
    document.getElementById("dashboard-target-header").style.display = "none";
    document.getElementById("dashboard-gallery-card").style.display = "none";
    document.getElementById("dashboard-empty").style.display = "flex";
    updateSidebarVisibility();
    renderOverviewDashboard();
    return;
  }
  const target = cachedProgress.find(t => t.username === activeTerminalUser);
  if (!target) {
    activeTerminalUser = null;
    document.getElementById("dashboard-target-header").style.display = "none";
    document.getElementById("dashboard-gallery-card").style.display = "none";
    document.getElementById("dashboard-empty").style.display = "flex";
    updateSidebarVisibility();
    renderOverviewDashboard();
    return;
  }

  document.getElementById("dashboard-file-count").innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 text-slate-500 inline-block align-text-bottom"><polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5" rx="1"/><line x1="10" y1="13" x2="14" y2="13"/></svg> ${target.media_count || 0} files`;
  
  const percent = target.progress || 0;
  document.getElementById("dashboard-progress-bar").style.width = `${percent}%`;
  
  const statusBadge = document.getElementById("dashboard-status-badge");
  statusBadge.textContent = target.status ? target.status.toUpperCase() : "IDLE";
  if (target.status === "running") {
    statusBadge.className = "text-[10px] font-bold tracking-wider px-2 py-0.5 rounded border uppercase bg-indigo-50 text-indigo-700 border-indigo-100 animate-pulse";
  } else if (target.status === "completed") {
    statusBadge.className = "text-[10px] font-bold tracking-wider px-2 py-0.5 rounded border uppercase bg-emerald-50 text-emerald-700 border-emerald-100";
  } else if (target.status === "failed") {
    statusBadge.className = "text-[10px] font-bold tracking-wider px-2 py-0.5 rounded border uppercase bg-rose-50 text-rose-700 border-rose-100";
  } else {
    statusBadge.className = "text-[10px] font-bold tracking-wider px-2 py-0.5 rounded border uppercase bg-slate-100 text-slate-600 border-slate-200";
  }

  const isRunning = target.status === "running";
  const syncBtn = document.getElementById("dashboard-btn-sync");
  const cancelBtn = document.getElementById("dashboard-btn-cancel");
  syncBtn.disabled = isRunning;
  if (cancelBtn) {
    if (isRunning) {
      cancelBtn.classList.remove("hidden");
      cancelBtn.classList.add("flex");
    } else {
      cancelBtn.classList.add("hidden");
      cancelBtn.classList.remove("flex");
    }
  }

  const syncIcon = syncBtn.querySelector("svg");
  if (syncIcon) {
    if (isRunning) syncIcon.classList.add("animate-spin");
    else syncIcon.classList.remove("animate-spin");
  }
}

function updateTerminal() {
  if (!activeTerminalUser) return;
  const target = cachedProgress.find(t => t.username === activeTerminalUser);
  if (!target) return;

  const terminal = document.getElementById("terminal-body");
  const lines = target.logs || [];

  let filtered = lines;
  if (terminalLevel !== "all") {
    filtered = lines.filter(l => l.level.toLowerCase() === terminalLevel);
  }

  let sawNewError = false;
  const prevCount = parseInt(terminal.dataset.count || "0", 10);
  if (lines.length > prevCount) {
    for (let i = prevCount; i < lines.length; i++) {
      if (lines[i].level.toLowerCase() === "error") { sawNewError = true; break; }
    }
  }

  terminal.innerHTML = "";
  filtered.forEach(log => {
    const logTime = new Date(log.timestamp).toLocaleTimeString();
    let colorClass = "text-slate-300";
    if (log.level.toLowerCase() === "error") {
      colorClass = "text-rose-400 font-semibold";
    } else if (log.level.toLowerCase() === "info") {
      colorClass = "text-emerald-400";
    } else if (log.level.toLowerCase() === "warn") {
      colorClass = "text-amber-400";
    }
    const div = document.createElement("div");
    div.className = `whitespace-pre-wrap break-all ${colorClass}`;
    div.innerText = `[${logTime}] [${log.level}] ${log.message}`;
    terminal.appendChild(div);
  });
  terminal.dataset.count = String(lines.length);
  terminal.scrollTop = terminal.scrollHeight;

  if (sawNewError) {
    const c = document.getElementById("dashboard-console");
    if (c) c.style.display = "block";
  }
}

function setTerminalLevel(level) {
  terminalLevel = level;
  document.querySelectorAll(".terminal-filter-btn").forEach(btn => {
    if (btn.dataset.level === level) {
      btn.classList.add("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.remove("text-slate-500", "hover:text-slate-800");
    } else {
      btn.classList.remove("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.add("text-slate-500", "hover:text-slate-800");
    }
  });
  updateTerminal();
}
window.setTerminalLevel = setTerminalLevel;

function copyTerminalLogs() {
  if (!activeTerminalUser) { toast("No active target selected.", "info"); return; }
  const target = cachedProgress.find(t => t.username === activeTerminalUser);
  if (!target || !target.logs || target.logs.length === 0) { toast("No logs to copy.", "info"); return; }
  const text = target.logs.map(l => `[${new Date(l.timestamp).toLocaleTimeString()}] [${l.level}] ${l.message}`).join("\n");
  navigator.clipboard.writeText(text).then(
    () => toast("Logs copied to clipboard.", "success", 2000),
    () => toast("Failed to copy logs.", "error")
  );
}
window.copyTerminalLogs = copyTerminalLogs;

function clearTerminalLogs() {
  const terminal = document.getElementById("terminal-body");
  if (!terminal) return;
  terminal.innerHTML = `<div class="text-slate-500">[SYSTEM] Console cleared.</div>`;
  terminal.dataset.count = "0";
  const c = document.getElementById("dashboard-console");
  if (c) c.style.display = "block";
}
window.clearTerminalLogs = clearTerminalLogs;

async function addAccount() {
  const username = document.getElementById("username").value.trim();
  const platform = document.getElementById("platform").value;
  const saveText = document.getElementById("save-text").checked;
  const skipRetweets = document.getElementById("skip-retweets").checked;
  const downloadPhotos = document.getElementById("download-photos").checked;
  const downloadVideos = document.getElementById("download-videos").checked;

  const filters = [...newFilters];

  if (!username) { toast("Enter a username first.", "error"); return; }

  const btn = document.getElementById("btn-add-account");
  await withLoading(btn, async () => {
    try {
      const res = await fetch("/api/config");
      const current = await res.json();
      if (current.accounts.some(acc => acc.username.toLowerCase() === username.toLowerCase())) {
        toast(`@${username} is already configured.`, "error");
        return;
      }
      current.accounts.push({
        username,
        platform,
        save_text: saveText,
        skip_retweets: skipRetweets,
        download_photos: downloadPhotos,
        download_videos: downloadVideos,
        filters: filters
      });
      const saveRes = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current)
      });
      if (!saveRes.ok) {
        const body = await saveRes.text().catch(() => "Unknown error");
        throw new Error(`Failed to add account: ${body}`);
      }
      document.getElementById("username").value = "";
      newFilters = [];
      renderNewFilters();
      toast(`Added @${username} (${platform}).`, "success");
      loadProgress();
    } catch (err) {
      console.error(err);
      toast(`Failed to add account: ${err.message}`, "error");
    }
  });
}

async function updateTargetAccount(username, platform, saveText, skipRetweets, downloadPhotos, downloadVideos, filters) {
  try {
    const res = await fetch("/api/config");
    const current = await res.json();
    const idx = current.accounts.findIndex(acc => acc.username.toLowerCase() === username.toLowerCase());
    if (idx !== -1) {
      current.accounts[idx] = {
        username,
        platform,
        save_text: saveText,
        skip_retweets: skipRetweets,
        download_photos: downloadPhotos,
        download_videos: downloadVideos,
        filters: filters
      };
      const saveRes = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current)
      });
      if (!saveRes.ok) {
        const body = await saveRes.text().catch(() => "Unknown error");
        throw new Error(`Failed to update account: ${body}`);
      }
      toast(`Updated @${username}.`, "success");
      loadProgress();
      selectTerminalUser(username);
    }
  } catch (err) {
    console.error(err);
    toast(`Failed to update account: ${err.message}`, "error");
  }
}
window.updateTargetAccount = updateTargetAccount;

async function deleteAccount(username) {
  try {
    const res = await fetch("/api/config");
    const current = await res.json();
    current.accounts = current.accounts.filter(acc => acc.username.toLowerCase() !== username.toLowerCase());
    const saveRes = await fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(current)
    });
    if (!saveRes.ok) throw new Error("Failed to save configuration updates");
    toast(`Removed @${username}.`, "success");
    loadProgress();
  } catch (err) {
    console.error(err);
    toast(`Failed to delete account: ${err.message}`, "error");
  }
}

async function clearTargetFolder(platform, username) {
  try {
    const res = await fetch("/api/scrape/clear", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ platform, username })
    });
    if (!res.ok) throw new Error("Failed to clear downloads folder");
    toast(`Cleared folder for @${username}.`, "success");
    loadProgress();
    selectGalleryTarget(platform, username);
  } catch (err) {
    console.error(err);
    toast(`Failed to clear folder: ${err.message}`, "error");
  }
}

async function startSync(username, shouldSelect = false) {
  try {
    const res = await fetch("/api/scrape/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username })
    });
    if (!res.ok) throw new Error("Sync trigger failed");
    if (shouldSelect) {
      selectTerminalUser(username);
    }
    toast(`Sync started for @${username}.`, "success", 2500);
    loadProgress();
  } catch (err) {
    console.error(err);
    toast(`Failed to start sync: ${err.message}`, "error");
  }
}
window.startSync = startSync;

async function cancelSync(username) {
  const ok = await confirmDialog({
    title: "Cancel sync",
    message: `Stop the running sync for @${username}? Already-downloaded files are kept.`,
    confirmText: "Cancel sync",
    tone: "danger",
  });
  if (!ok) return;
  try {
    const res = await fetch("/api/scrape/cancel", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username })
    });
    if (!res.ok) throw new Error("Cancel failed");
    const data = await res.json();
    if (data.status === "cancelled") {
      toast(`Cancel signal sent for @${username}.`, "info");
    } else {
      toast(`@${username} is not currently running.`, "info");
    }
    loadProgress();
  } catch (err) {
    console.error(err);
    toast(`Failed to cancel: ${err.message}`, "error");
  }
}
window.cancelSync = cancelSync;

async function syncAll() {
  try {
    const res = await fetch("/api/scrape/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: "all" })
    });
    if (!res.ok) throw new Error("All trigger failed");
    toast("Sync started for all targets.", "success", 2500);
    loadProgress();
  } catch (err) {
    console.error(err);
    toast(`Failed to trigger sync: ${err.message}`, "error");
  }
}
window.syncAll = syncAll;

// ==================== Add Target / Settings / New Filters ====================

function toggleAddTarget() {
  const p = document.getElementById("add-target-panel");
  if (p) {
    p.classList.toggle("hidden");
    toggleTwitterOptions();
  }
}
window.toggleAddTarget = toggleAddTarget;

function toggleTwitterOptions() {
  const sel = document.getElementById("platform");
  const c = document.getElementById("save-text-container");
  if (sel && c) {
    c.classList.remove("hidden");
    const isTwitter = sel.value === "twitter";
    document.querySelectorAll("#save-text-container .twitter-only-option").forEach(el => {
      el.style.display = isTwitter ? "" : "none";
    });
  }
}
window.toggleTwitterOptions = toggleTwitterOptions;

function toggleSettings() {
  const p = document.getElementById("settings-panel");
  if (p) p.classList.toggle("hidden");
}
window.toggleSettings = toggleSettings;

let newFilters = [];
function addNewFilter() {
  const input = document.getElementById("new-tag-input");
  const val = input.value.trim();
  if (val && !newFilters.includes(val)) {
    newFilters.push(val);
    input.value = "";
    renderNewFilters();
  }
}
function renderNewFilters() {
  const list = document.getElementById("new-filters-list");
  const noLabel = document.getElementById("no-filters-label");
  if (!list) return;
  list.querySelectorAll(".filter-chip").forEach(e => e.remove());
  if (newFilters.length === 0) {
    if (noLabel) noLabel.style.display = "inline";
    return;
  }
  if (noLabel) noLabel.style.display = "none";
  newFilters.forEach((tag, idx) => {
    const chip = document.createElement("span");
    chip.className = "filter-chip inline-flex items-center gap-1 bg-indigo-50 border border-indigo-100 text-indigo-700 text-[10px] font-semibold px-2 py-0.5 rounded-full";
    chip.setAttribute("data-filter", tag);
    chip.innerHTML = `<span>${escapeHtml(tag)}</span><button type="button" class="hover:text-indigo-950 font-bold cursor-pointer" onclick="removeNewFilter(${idx})">&times;</button>`;
    list.appendChild(chip);
  });
}
function removeNewFilter(idx) {
  newFilters = newFilters.filter((_, i) => i !== idx);
  renderNewFilters();
}
window.addNewFilter = addNewFilter;
window.removeNewFilter = removeNewFilter;

// ==================== Edit Modal ====================

let editConfig = { username: "", platform: "instagram", saveText: false, skipRetweets: false, downloadPhotos: true, downloadVideos: true, filters: [] };

window.addEventListener("open-edit-modal", (e) => {
  editConfig = { ...e.detail, filters: e.detail.filters || [] };
  document.getElementById("edit-username").value = e.detail.username;
  document.getElementById("edit-platform").value = e.detail.platform;
  document.getElementById("edit-save-text").checked = e.detail.save_text;
  document.getElementById("edit-skip-retweets").checked = e.detail.skip_retweets;
  document.getElementById("edit-download-photos").checked = e.detail.download_photos;
  document.getElementById("edit-download-videos").checked = e.detail.download_videos;
  const ot = document.getElementById("edit-options-container");
  if (ot) {
    ot.style.display = "flex";
    const isTwitter = e.detail.platform === "twitter";
    document.querySelectorAll("#edit-options-container .edit-twitter-only-option").forEach(el => {
      el.style.display = isTwitter ? "" : "none";
    });
  }
  renderEditFilters();
  document.getElementById("edit-modal").style.display = "flex";
});

function closeEditModal() {
  document.getElementById("edit-modal").style.display = "none";
}

function addEditFilter() {
  const input = document.getElementById("new-edit-tag-input");
  const val = input.value.trim();
  if (val && !editConfig.filters.includes(val)) {
    editConfig.filters.push(val);
    input.value = "";
    renderEditFilters();
  }
}

function removeEditFilter(idx) {
  editConfig.filters = editConfig.filters.filter((_, i) => i !== idx);
  renderEditFilters();
}

function renderEditFilters() {
  const list = document.getElementById("edit-filters-list");
  const noLabel = document.getElementById("edit-no-filters");
  if (!list) return;
  list.querySelectorAll(".edit-filter-chip").forEach(e => e.remove());
  if (editConfig.filters.length === 0) {
    if (noLabel) noLabel.style.display = "inline";
    return;
  }
  if (noLabel) noLabel.style.display = "none";
  editConfig.filters.forEach((tag, idx) => {
    const chip = document.createElement("span");
    chip.className = "edit-filter-chip inline-flex items-center gap-1 bg-indigo-50 border border-indigo-100 text-indigo-700 text-[10px] font-semibold px-2 py-0.5 rounded-full";
    chip.setAttribute("data-filter", tag);
    chip.innerHTML = `<span>${escapeHtml(tag)}</span><button type="button" class="hover:text-indigo-950 font-bold cursor-pointer" onclick="removeEditFilter(${idx})">&times;</button>`;
    list.appendChild(chip);
  });
}

function saveEditChanges() {
  updateTargetAccount(
    editConfig.username,
    document.getElementById("edit-platform").value,
    document.getElementById("edit-save-text").checked,
    document.getElementById("edit-skip-retweets").checked,
    document.getElementById("edit-download-photos").checked,
    document.getElementById("edit-download-videos").checked,
    [...editConfig.filters]
  );
  closeEditModal();
}

window.closeEditModal = closeEditModal;
window.addEditFilter = addEditFilter;
window.removeEditFilter = removeEditFilter;
window.saveEditChanges = saveEditChanges;

// ==================== Media Gallery Workspace ====================

let galleryMeta = null;
let pswpGrid = null;
let pswpPosts = null;
let currentView = "grid";
let currentFilter = "all";
let selectedHashtags = new Set();
let selectedYears = new Set();
let selectedMonths = new Set();

let activeGalleryUser = null;
let activeGalleryPlatform = null;

let gridSearchQuery = "";
let postsSearchQuery = "";
let postsSortAsc = false;

let galleryEmptySvg = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-10 h-10 opacity-30 text-slate-400"><line x1="1" y1="1" x2="23" y2="23"/><path d="M21 21H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h3"/><path d="m3.13 3.13 17.74 17.74"/><path d="M10.5 5.5 12 3l2 3h4a2 2 0 0 1 2 2v9.34"/></svg>';

// --- PhotoSwipe shared helpers ---

const PSWP_VIDEO_W = 1280;
const PSWP_VIDEO_H = 720;

function pswpAddItemDataFilter(lb) {
  lb.addFilter("domItemData", (itemData, element, linkEl) => {
    if (linkEl) {
      const thumb = linkEl.querySelector("img");
      if (thumb && thumb.naturalWidth > 0 && thumb.naturalHeight > 0) {
        itemData.w = thumb.naturalWidth;
        itemData.h = thumb.naturalHeight;
      }
    }
    return itemData;
  });

  lb.addFilter("useContentPlaceholder", () => false);

  lb.addFilter("itemData", (itemData) => {
    if (itemData.type === "video" && itemData.src) {
      if (!itemData.w || !itemData.h) {
        itemData.w = PSWP_VIDEO_W;
        itemData.h = PSWP_VIDEO_H;
      }
      itemData.html = '<video src="' + itemData.src + '" controls playsinline preload="metadata" style="width:100%;height:100%;object-fit:contain"></video>';
    }
    return itemData;
  });
}

function pswpResizeSlide(slide, w, h) {
  if (!slide || !w || !h) return;
  slide.width = w;
  slide.height = h;
  if (slide.data) {
    slide.data.w = w;
    slide.data.h = h;
  }
  slide.calculateSize();
  slide.zoomAndPanToInitial();
  slide.applyCurrentZoomPan();
  slide.updateContentSize(true);
}

function pswpUpdateVideoSize(content, video) {
  const vw = video.videoWidth;
  const vh = video.videoHeight;
  if (!vw || !vh) return;
  content.width = vw;
  content.height = vh;
  content.data.w = vw;
  content.data.h = vh;
  pswpResizeSlide(content.slide, vw, vh);
}

function pswpAddVideoControls(lb) {
  function pauseAllVideos() {
    if (!lb.pswp || !lb.pswp.element) return;
    lb.pswp.element.querySelectorAll("video").forEach(v => v.pause());
  }

  function playCurrentVideo() {
    if (!lb.pswp || !lb.pswp.currSlide) return;
    const el = lb.pswp.currSlide.content && lb.pswp.currSlide.content.element;
    const v = el && el.querySelector && el.querySelector("video");
    if (v) v.play().catch(() => {});
  }

  function resizeImageSlide(slide) {
    if (!slide || !slide.content) return;
    const c = slide.content;
    if (!c.isImageContent || !c.isImageContent() || !c.element) return;
    const nw = c.element.naturalWidth;
    const nh = c.element.naturalHeight;
    if (nw && nh && (c.width !== nw || c.height !== nh)) {
      pswpResizeSlide(slide, nw, nh);
    }
  }

  lb.on("change", () => {
    pauseAllVideos();
    playCurrentVideo();
    if (lb.pswp && lb.pswp.currSlide) {
      requestAnimationFrame(() => resizeImageSlide(lb.pswp.currSlide));
    }
  });

  lb.on("slideActivate", (e) => {
    if (e.slide && e.slide.content && e.slide.content.state === "loaded") {
      requestAnimationFrame(() => resizeImageSlide(e.slide));
    }
  });

  lb.on("contentDeactivate", (e) => {
    const v = e.content.element && e.content.element.querySelector && e.content.element.querySelector("video");
    if (v) v.pause();
  });

  lb.on("destroy", pauseAllVideos);

  lb.on("contentActivate", (e) => {
    const v = e.content.element && e.content.element.querySelector && e.content.element.querySelector("video");
    if (!v) return;
    if (v.readyState >= 1) {
      pswpUpdateVideoSize(e.content, v);
    } else {
      v.addEventListener("loadedmetadata", function onMeta() {
        v.removeEventListener("loadedmetadata", onMeta);
        if (e.content && e.content.slide) {
          pswpUpdateVideoSize(e.content, v);
        }
      });
    }
    if (e.content.slide && e.content.slide.isActive) {
      v.play().catch(() => {});
    } else {
      v.pause();
    }
  });

  lb.on("loadComplete", (e) => {
    if (e.isError) return;
    const c = e.content;
    if (!c || !c.isImageContent || !c.isImageContent() || !c.element) return;
    const nw = c.element.naturalWidth;
    const nh = c.element.naturalHeight;
    if (!nw || !nh) return;
    requestAnimationFrame(() => {
      if (c.slide && (c.width !== nw || c.height !== nh)) {
        pswpResizeSlide(c.slide, nw, nh);
      }
    });
  });
}

async function initPhotoSwipeGrid() {
  if (pswpGrid) return;
  try {
    const mod = await import("/static/vendor/photoswipe-lightbox.esm.min.js");
    pswpGrid = new mod.default({
      gallery: "#lg-container",
      children: "a.pswp-item",
      pswpModule: () => import("/static/vendor/photoswipe.esm.min.js"),
    });
    pswpAddItemDataFilter(pswpGrid);
    pswpAddVideoControls(pswpGrid);
    pswpGrid.init();
  } catch (err) {
    console.error("PhotoSwipe grid init error:", err);
  }
}

async function initPhotoSwipePosts() {
  if (pswpPosts) return;
  try {
    const mod = await import("/static/vendor/photoswipe-lightbox.esm.min.js");
    pswpPosts = new mod.default({
      gallery: "#gallery-posts-list",
      children: "a.pswp-item",
      pswpModule: () => import("/static/vendor/photoswipe.esm.min.js"),
    });
    pswpAddItemDataFilter(pswpPosts);
    pswpAddVideoControls(pswpPosts);
    pswpPosts.init();
  } catch (err) {
    console.error("PhotoSwipe posts init error:", err);
  }
}

async function selectGalleryTarget(platform, username) {
  activeGalleryUser = username;
  activeGalleryPlatform = platform;
  currentView = "grid";

  postsSearchQuery = "";
  gridSearchQuery = "";
  const searchInput = document.getElementById("posts-search-input");
  if (searchInput) searchInput.value = "";
  const gridSearchInput = document.getElementById("grid-search-input");
  if (gridSearchInput) gridSearchInput.value = "";
  const yearFilter = document.getElementById("gallery-year-filter");
  if (yearFilter) yearFilter.value = "all";
  const monthFilter = document.getElementById("gallery-month-filter");
  if (monthFilter) monthFilter.value = "all";

  document.getElementById("gallery-empty").style.display = "none";

  const usernameLabel = document.getElementById("gallery-username-label");
  if (usernameLabel) usernameLabel.textContent = `@${username}`;

  const fileCountEl = document.getElementById("gallery-file-count");
  if (fileCountEl) fileCountEl.textContent = "";

  showGalleryState("loading");

  // Fetch metadata for hashtag filter + file count
  try {
    const metaRes = await fetch(`/api/gallery?platform=${encodeURIComponent(platform)}&username=${encodeURIComponent(username)}`);
    galleryMeta = await metaRes.json();
  } catch (err) {
    console.error("Gallery meta error:", err);
  }

  const files = galleryMeta?.files || [];
  const hasPosts = galleryMeta?.posts && galleryMeta.posts.length > 0;

  document.getElementById("tab-posts").style.display = hasPosts ? "inline-flex" : "none";

  if (fileCountEl) {
    fileCountEl.textContent = `${files.length} file${files.length !== 1 ? "s" : ""}${hasPosts ? ` · ${galleryMeta.posts.length} posts` : ""}`;
  }

  if (files.length === 0) {
    showGalleryState("empty");
    document.getElementById("gallery-empty").innerHTML = `
      ${galleryEmptySvg}
      <p class="text-xs text-slate-400 font-medium">No media files found in downloads</p>
    `;
    return;
  }

  showGalleryState("content");

  selectedHashtags.clear();
  selectedYears.clear();
  selectedMonths.clear();
  document.querySelectorAll("#year-dropdown input[type='checkbox']").forEach(cb => cb.checked = false);
  document.querySelectorAll("#month-dropdown input[type='checkbox']").forEach(cb => cb.checked = false);
  updateYearFilterButtonLabel();
  updateMonthFilterButtonLabel();
  populateHashtagDropdown();
  populateYearDropdown();

  const scrollContainer = document.getElementById("gallery-scroll-container");
  if (scrollContainer) scrollContainer.scrollTop = 0;

  switchGalleryView(currentView);
}

function showGalleryState(state) {
  document.getElementById("gallery-loading").style.display = state === "loading" ? "flex" : "none";
  document.getElementById("gallery-empty").style.display = state === "empty" ? "flex" : "none";
  document.getElementById("gallery-grid-view").style.display = (state === "content" && currentView === "grid") ? "block" : "none";
  document.getElementById("gallery-posts-view").style.display = (state === "content" && currentView === "posts") ? "block" : "none";
}

async function renderGalleryGrid() {
  const container = document.getElementById("lg-container");
  if (!container) return;
  container.innerHTML = "";
  const year = selectedYears.size > 0 ? Array.from(selectedYears).join(",") : "all";
  const month = selectedMonths.size > 0 ? Array.from(selectedMonths).join(",") : "all";
  const url = `/gallery/${encodeURIComponent(activeGalleryPlatform)}/${encodeURIComponent(activeGalleryUser)}?filter=${encodeURIComponent(currentFilter)}&q=${encodeURIComponent(gridSearchQuery)}&sort=${postsSortAsc ? "asc" : "desc"}&year=${encodeURIComponent(year)}&month=${encodeURIComponent(month)}`;
  try {
    const res = await fetch(url);
    const html = await res.text();
    container.innerHTML = html;
    if (window.htmx) htmx.process(container);
    initPhotoSwipeGrid();
  } catch (err) {
    console.error("Grid render error:", err);
    container.innerHTML = `<div class="col-span-full text-center text-xs font-semibold text-slate-400 py-12">Failed to load gallery</div>`;
  }
}

async function renderGalleryPosts() {
  const container = document.getElementById("gallery-posts-list");
  if (!container) return;
  container.innerHTML = "";
  const year = selectedYears.size > 0 ? Array.from(selectedYears).join(",") : "all";
  const month = selectedMonths.size > 0 ? Array.from(selectedMonths).join(",") : "all";
  const url = `/gallery/${encodeURIComponent(activeGalleryPlatform)}/${encodeURIComponent(activeGalleryUser)}/posts/page/1?sort=${postsSortAsc ? "asc" : "desc"}&q=${encodeURIComponent(postsSearchQuery)}&year=${encodeURIComponent(year)}&month=${encodeURIComponent(month)}`;
  try {
    const res = await fetch(url);
    const html = await res.text();
    container.innerHTML = html;
    if (window.htmx) htmx.process(container);
    initPhotoSwipePosts();
  } catch (err) {
    console.error("Posts render error:", err);
    container.innerHTML = `<div class="text-center text-xs font-semibold text-slate-400 py-12">Failed to load posts</div>`;
  }
}

function applyDateFilter() {
  if (currentView === "grid") {
    renderGalleryGrid();
  } else {
    renderGalleryPosts();
  }
}
window.applyDateFilter = applyDateFilter;

function switchGalleryView(view) {
  currentView = view;
  setActiveTab(`tab-${view}`);

  if (view === "grid") {
    document.getElementById("gallery-grid-view").style.display = "block";
    document.getElementById("gallery-posts-view").style.display = "none";
    document.getElementById("gallery-filter-tabs").style.display = "flex";
    document.getElementById("posts-sort-container").style.display = "flex";
    document.getElementById("posts-search-container").style.display = "none";
    document.getElementById("grid-search-container").style.display = "flex";
    renderGalleryGrid();
  } else {
    document.getElementById("gallery-grid-view").style.display = "none";
    document.getElementById("gallery-posts-view").style.display = "block";
    document.getElementById("gallery-filter-tabs").style.display = "none";
    document.getElementById("posts-sort-container").style.display = "flex";
    document.getElementById("posts-search-container").style.display = "flex";
    document.getElementById("grid-search-container").style.display = "none";
    renderGalleryPosts();
  }
}
window.switchGalleryView = switchGalleryView;

function applyGalleryFilter(filter) {
  currentFilter = filter;
  setActiveFilter(filter);
  if (currentView === "grid") {
    renderGalleryGrid();
  }
}
window.applyGalleryFilter = applyGalleryFilter;

function setActiveTab(tabId) {
  document.querySelectorAll(".gallery-tab").forEach(btn => {
    if (btn.id === tabId) {
      btn.classList.add("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.remove("text-slate-500", "hover:text-slate-800");
    } else {
      btn.classList.remove("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.add("text-slate-500", "hover:text-slate-800");
    }
  });
}

function setActiveFilter(filter) {
  document.querySelectorAll(".gallery-filter-btn").forEach(btn => {
    if (btn.dataset.filter === filter) {
      btn.classList.add("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.remove("text-slate-500", "hover:text-slate-800");
    } else {
      btn.classList.remove("bg-white", "text-indigo-600", "shadow-xs");
      btn.classList.add("text-slate-500", "hover:text-slate-800");
    }
  });
}

// Global sorting toggle for gallery items and posts
function togglePostsSort() {
  postsSortAsc = !postsSortAsc;
  const label = document.getElementById("sort-order-label");
  const iconHost = document.getElementById("sort-order-icon");
  if (label) label.textContent = postsSortAsc ? "Oldest First" : "Newest First";
  if (iconHost) {
    const iconHTML = postsSortAsc
      ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 text-indigo-500" id="sort-order-icon"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>'
      : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 text-indigo-500" id="sort-order-icon"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg>';
    iconHost.outerHTML = iconHTML;
  }
  if (currentView === "grid") {
    renderGalleryGrid();
  } else {
    renderGalleryPosts();
  }
}
window.togglePostsSort = togglePostsSort;

// Text search filtering for Posts timeline view
const applyPostsSearch = debounce(() => {
  const input = document.getElementById("posts-search-input");
  postsSearchQuery = input ? input.value.trim().toLowerCase() : "";
  if (currentView === "posts") renderGalleryPosts();
}, 180);
window.applyPostsSearch = applyPostsSearch;

// Filename search for Media grid view
const applyGridSearch = debounce(() => {
  const input = document.getElementById("grid-search-input");
  gridSearchQuery = input ? input.value.trim().toLowerCase() : "";
  renderGalleryGrid();
}, 180);
window.applyGridSearch = applyGridSearch;

// Smooth scroll to top of the gallery container
function scrollToTop() {
  const scrollContainer = document.getElementById("gallery-scroll-container");
  if (scrollContainer) {
    scrollContainer.scrollTo({ top: 0, behavior: "smooth" });
  }
}
window.scrollToTop = scrollToTop;

function computeCountdown() {
  if (autoSyncInterval <= 0 || !lastSyncTime) return null;
  const next = new Date(lastSyncTime).getTime() + autoSyncInterval * 3600000;
  const diff = next - Date.now();
  if (diff <= 0) return "due";
  const totalMin = Math.floor(diff / 60000);
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  return (h > 0 ? h + "h " : "") + m + "m";
}

function updateAutoUpdateStatus() {
  const badge = document.getElementById("autoupdate-badge");
  const dot = document.getElementById("autoupdate-dot");
  const text = document.getElementById("autoupdate-text");
  if (!badge || !dot || !text) return;

  if (autoSyncInterval > 0) {
    badge.className = "flex items-center gap-2 bg-emerald-50 text-emerald-700 px-3 py-1.5 rounded-full border border-emerald-100 transition-all duration-300";
    dot.className = "w-2 h-2 bg-emerald-500 rounded-full animate-pulse";
    const cd = computeCountdown();
    text.textContent = cd === null ? `every ${autoSyncInterval}h` : (cd === "due" ? "syncing…" : `next in ${cd}`);
  } else {
    badge.className = "flex items-center gap-2 bg-slate-100 text-slate-500 px-3 py-1.5 rounded-full border border-slate-200 transition-all duration-300";
    dot.className = "w-2 h-2 bg-slate-400 rounded-full";
    text.textContent = "Autoupdate: Off";
  }
}

function startCountdownTicker() {
  if (countdownTicker) return;
  countdownTicker = setInterval(updateAutoUpdateStatus, 30000);
}

function renderOverviewDashboard() {
  const container = document.getElementById("dashboard-empty");
  if (!container) return;

  if (cachedProgress.length === 0) {
    container.innerHTML = `
      <div class="flex-grow flex flex-col items-center justify-center gap-3 text-slate-400 text-xs font-medium">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-10 h-10 opacity-30"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
        <p>No target accounts configured. Add one from the sidebar to begin syncing.</p>
      </div>
    `;
    return;
  }

  // Calculate countdown to next update
  let countdownText = "—";
  let lastSyncFormatted = "Never";
  
  if (lastSyncTime) {
    const lastSyncDate = new Date(lastSyncTime);
    lastSyncFormatted = lastSyncDate.toLocaleString();

    if (autoSyncInterval > 0) {
      const nextSyncDate = new Date(lastSyncDate.getTime() + autoSyncInterval * 60 * 60 * 1000);
      const diffMs = nextSyncDate.getTime() - Date.now();
      
      if (diffMs > 0) {
        const totalMinutes = Math.floor(diffMs / 60000);
        const hours = Math.floor(totalMinutes / 60);
        const minutes = totalMinutes % 60;
        countdownText = `${hours > 0 ? hours + 'h ' : ''}${minutes}m left`;
      } else {
        countdownText = "Pending / Running";
      }
    }
  }

  const refreshSvg = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>';
  const refreshSvgSpin = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3 h-3 animate-spin"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>';
  const refreshSvgGreen = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3 h-3 text-emerald-600"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>';
  const activitySvg = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4 text-indigo-500"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>';

  let tableRows = "";
  cachedProgress.forEach(target => {
    let statusText = "IDLE";
    let statusBadgeClass = "";
    if (target.status === "running") {
      statusText = "RUNNING";
      statusBadgeClass = "bg-indigo-50 text-indigo-700 border-indigo-100 animate-pulse";
    } else if (target.status === "completed") {
      statusText = "DONE";
      statusBadgeClass = "bg-emerald-50 text-emerald-700 border-emerald-100";
    } else if (target.status === "failed") {
      statusText = "FAILED";
      statusBadgeClass = "bg-rose-50 text-rose-700 border-rose-100";
    } else {
      statusText = "IDLE";
      statusBadgeClass = "bg-slate-100 text-slate-600 border-slate-200";
    }

    let platformBadgeClass = "bg-pink-50 text-pink-700 border-pink-100";
    if (target.platform === "twitter") {
      platformBadgeClass = "bg-sky-50 text-sky-700 border-sky-100";
    }

    const lastSyncStr = target.updated_at && target.updated_at !== "0001-01-01T00:00:00Z" 
      ? new Date(target.updated_at).toLocaleString() 
      : "Never";

    const isRunning = target.status === "running";
    const syncActionBtn = isRunning
      ? `<button disabled class="bg-slate-50 text-slate-400 border border-slate-100 text-[10px] font-bold px-2 py-1 rounded-md flex items-center gap-1.5 cursor-not-allowed">
           ${refreshSvgSpin} Syncing
         </button>`
      : `<button onclick="event.stopPropagation(); startSync('${target.username}', false)" class="bg-emerald-50 hover:bg-emerald-100 text-emerald-700 border border-emerald-200 text-[10px] font-bold px-2.5 py-1 rounded-md flex items-center gap-1 cursor-pointer transition-all duration-150">
           ${refreshSvgGreen} Sync
         </button>`;

    const isInstagram = target.platform === 'instagram';
    const platformIcon = isInstagram
      ? `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 text-slate-600"><rect x="2" y="2" width="20" height="20" rx="5" ry="5"></rect><path d="M16 11.37A4 4 0 1 1 12.63 8 4 4 0 0 1 16 11.37z"></path><line x1="17.5" y1="6.5" x2="17.51" y2="6.5"></line></svg>`
      : `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 text-slate-600"><path d="M22 4s-.7 2.1-2 3.4c1.6 10-9.4 17.3-18 11.6 2.2.1 4.4-.6 6-2 C3 15.5.5 9.6 3 5c2.2 2.6 5.6 4.1 9 4-.9-4.2 4-6.6 7-3.8 1.1 0 3-1.2 3-1.2z"></path></svg>`;

    const newCount = target.new_count || 0;
    const newCountBadge = newCount > 0
      ? `<span class="ml-1.5 text-[9px] font-bold text-emerald-700 bg-emerald-50 border border-emerald-200 px-1.5 py-0.5 rounded-full">+${newCount}</span>`
      : "";

    const authErrorBadge = target.auth_error
      ? `<div class="mt-1 flex items-center gap-1 text-[9px] font-bold text-rose-700 bg-rose-50 border border-rose-200 px-2 py-0.5 rounded">
           <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="w-3 h-3 flex-shrink-0"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 11"/></svg>
           Cookie expired — update in Settings
         </div>`
      : "";

    tableRows += `
      <tr class="hover:bg-slate-50/50 transition-colors border-b border-slate-100 cursor-pointer" onclick="selectTerminalUser('${target.username}')">
        <td class="px-6 py-4 whitespace-nowrap">
          <div class="flex items-center gap-2.5">
            <div class="p-1.5 bg-slate-50 text-slate-500 rounded-md border border-slate-200 flex items-center justify-center">
              ${platformIcon}
            </div>
            <span class="text-sm font-semibold text-slate-700 select-all">
              @${target.username}
            </span>
          </div>
        </td>
        <td class="px-6 py-4 whitespace-nowrap">
          <span class="text-[10px] font-bold tracking-wider px-2.5 py-1 rounded border uppercase ${statusBadgeClass}">
            ${statusText}
          </span>
          ${authErrorBadge}
        </td>
        <td class="px-6 py-4 whitespace-nowrap text-xs text-slate-500 font-medium">
          ${lastSyncStr}
        </td>
        <td class="px-6 py-4 whitespace-nowrap text-xs text-slate-500 font-mono">
          ${target.media_count || 0} files${newCountBadge}
        </td>
        <td class="px-6 py-4 whitespace-nowrap text-right text-xs font-medium">
          ${syncActionBtn}
        </td>
      </tr>
    `;
  });

  container.innerHTML = `
    <div class="flex flex-col gap-6">
      <!-- Top header stats -->
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div class="bg-white border border-slate-200 rounded-xl p-5 shadow-sm flex flex-col gap-1">
          <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Auto-update Sync Interval</span>
          <span class="text-lg font-bold text-slate-800">${autoSyncInterval > 0 ? autoSyncInterval + ' hours' : 'Disabled'}</span>
        </div>
        <div class="bg-white border border-slate-200 rounded-xl p-5 shadow-sm flex flex-col gap-1">
          <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Last Sync Completed</span>
          <span class="text-xs font-semibold text-slate-700 mt-1">${lastSyncFormatted}</span>
        </div>
        <div class="bg-white border border-slate-200 rounded-xl p-5 shadow-sm flex flex-col gap-1">
          <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Time Until Next Sync</span>
          <span class="text-lg font-bold text-indigo-600">${countdownText}</span>
        </div>
      </div>

      <!-- Targets table -->
      <div class="bg-white border border-slate-200 rounded-xl shadow-sm overflow-hidden flex flex-col">
        <div class="px-6 py-4 border-b border-slate-200 bg-slate-50/50 flex justify-between items-center flex-wrap gap-3">
          <h3 class="text-xs font-bold text-slate-700 uppercase tracking-wider flex items-center gap-1.5">
            ${activitySvg} Target Status Overview
          </h3>
          <div class="flex items-center gap-3">
            <span class="text-[10px] bg-slate-100 text-slate-500 border border-slate-200 px-2 py-0.5 rounded-full font-bold">
              ${cachedProgress.length} target${cachedProgress.length !== 1 ? 's' : ''}
            </span>
            <button onclick="syncAll()" class="bg-indigo-600 hover:bg-indigo-700 text-white text-[11px] font-bold px-3 py-1.5 rounded-lg transition-all cursor-pointer flex items-center gap-1.5 shadow-sm hover:shadow">
              ${refreshSvg} Sync All Targets
            </button>
          </div>
        </div>
        
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-slate-200">
            <thead class="bg-slate-50">
              <tr>
                <th scope="col" class="px-6 py-3 text-left text-[9px] font-bold text-slate-400 uppercase tracking-wider">Target</th>
                <th scope="col" class="px-6 py-3 text-left text-[9px] font-bold text-slate-400 uppercase tracking-wider">Status</th>
                <th scope="col" class="px-6 py-3 text-left text-[9px] font-bold text-slate-400 uppercase tracking-wider">Last Updated</th>
                <th scope="col" class="px-6 py-3 text-left text-[9px] font-bold text-slate-400 uppercase tracking-wider">Files Synced</th>
                <th scope="col" class="px-6 py-3 text-right text-[9px] font-bold text-slate-400 uppercase tracking-wider">Action</th>
              </tr>
            </thead>
            <tbody class="bg-white divide-y divide-slate-100">
              ${tableRows}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  `;
}

// ==================== Hashtags Multi-select Filter ====================

function extractHashtags(text) {
  if (!text) return [];
  const matches = text.match(/#\w+/g);
  return matches ? matches.map(m => m.toLowerCase()) : [];
}

function getFileHashtags(filename) {
  return [];
}

function toggleHashtagDropdown(event) {
  if (event) event.stopPropagation();
  const dropdown = document.getElementById("hashtag-dropdown");
  if (!dropdown) return;
  dropdown.classList.toggle("hidden");
}
window.toggleHashtagDropdown = toggleHashtagDropdown;

window.addEventListener("click", (e) => {
  // Hashtag
  const htContainer = document.getElementById("hashtag-filter-container");
  const htDropdown = document.getElementById("hashtag-dropdown");
  if (htContainer && htDropdown && !htContainer.contains(e.target)) {
    htDropdown.classList.add("hidden");
  }

  // Year
  const yrContainer = document.getElementById("year-filter-container");
  const yrDropdown = document.getElementById("year-dropdown");
  if (yrContainer && yrDropdown && !yrContainer.contains(e.target)) {
    yrDropdown.classList.add("hidden");
  }

  // Month
  const moContainer = document.getElementById("month-filter-container");
  const moDropdown = document.getElementById("month-dropdown");
  if (moContainer && moDropdown && !moContainer.contains(e.target)) {
    moDropdown.classList.add("hidden");
  }
});

function populateHashtagDropdown() {
  const container = document.getElementById("hashtag-filter-container");
  const dropdown = document.getElementById("hashtag-dropdown");
  if (!container || !dropdown) return;

  const posts = galleryMeta?.posts || [];
  const hashtagsMap = new Map();

  posts.forEach(post => {
    const tags = extractHashtags(post.text);
    tags.forEach(tag => {
      hashtagsMap.set(tag, (hashtagsMap.get(tag) || 0) + 1);
    });
  });

  if (hashtagsMap.size === 0) {
    container.style.display = "none";
    return;
  }

  container.style.display = "inline-block";

  const sortedTags = Array.from(hashtagsMap.keys()).sort((a, b) => {
    const countA = hashtagsMap.get(a);
    const countB = hashtagsMap.get(b);
    if (countA !== countB) return countB - countA;
    return a.localeCompare(b);
  });

  let html = "";
  sortedTags.forEach(tag => {
    const isChecked = selectedHashtags.has(tag);
    const count = hashtagsMap.get(tag);
    html += `
      <label class="flex items-center gap-2 px-2 py-1.5 hover:bg-slate-50 rounded-lg cursor-pointer text-xs font-semibold text-slate-700 select-none">
        <input type="checkbox" value="${tag}" ${isChecked ? "checked" : ""} class="accent-indigo-600 rounded w-3.5 h-3.5" onchange="toggleHashtagFilter(this)">
        <span class="truncate">${tag}</span>
        <span class="text-[9px] bg-slate-100 text-slate-500 border border-slate-200 px-1.5 py-0.5 rounded-full ml-auto flex-shrink-0">${count}</span>
      </label>
    `;
  });

  dropdown.innerHTML = html;
  updateHashtagFilterButtonLabel();
}

function toggleHashtagFilter(checkbox) {
  const tag = checkbox.value;
  if (checkbox.checked) {
    selectedHashtags.add(tag);
  } else {
    selectedHashtags.delete(tag);
  }
  updateHashtagFilterButtonLabel();

  if (currentView === "posts") {
    renderGalleryPosts();
  }
}
window.toggleHashtagFilter = toggleHashtagFilter;

function updateHashtagFilterButtonLabel() {
  const label = document.getElementById("hashtag-filter-label");
  const btn = document.getElementById("btn-hashtag-filter");
  if (!label || !btn) return;

  if (selectedHashtags.size > 0) {
    label.textContent = `Hashtags (${selectedHashtags.size})`;
    btn.classList.add("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
    btn.classList.remove("bg-slate-100", "border-slate-200", "text-slate-500");
  } else {
    label.textContent = "Hashtags";
    btn.classList.add("bg-slate-100", "border-slate-200", "text-slate-500");
    btn.classList.remove("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
  }
}

window.extractHashtags = extractHashtags;
window.getFileHashtags = getFileHashtags;

function getYoutubeId(url) {
  if (!url) return null;
  const regExp = /^.*(youtu.be\/|v\/|u\/\w\/|embed\/|watch\?v=|\&v=|shorts\/)([^#\&\?]*).*/;
  const match = url.match(regExp);
  return (match && match[2].length === 11) ? match[2] : null;
}

function embedYoutubeInline(el, videoId) {
  el.outerHTML = `<div class="w-full max-w-md aspect-video rounded-xl overflow-hidden border border-slate-200 shadow-sm mt-2">
    <iframe class="w-full h-full" src="https://www.youtube.com/embed/${videoId}?autoplay=1" frameborder="0" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture" allowfullscreen></iframe>
  </div>`;
}

window.getYoutubeId = getYoutubeId;
window.embedYoutubeInline = embedYoutubeInline;

// ==================== Delegated handlers for server-rendered HTML ====================

document.addEventListener("click", (e) => {
  const yt = e.target.closest(".youtube-preview");
  if (yt && yt.dataset.youtubeId) {
    e.preventDefault();
    embedYoutubeInline(yt, yt.dataset.youtubeId);
  }
});

document.addEventListener("error", (e) => {
  if (e.target && e.target.tagName === "IMG" && e.target.dataset.fallbackSrc) {
    e.target.onerror = null;
    e.target.src = e.target.dataset.fallbackSrc;
    delete e.target.dataset.fallbackSrc;
  }
}, true);

// ==================== Date Multi-select Filters ====================

function toggleYearDropdown(event) {
  if (event) event.stopPropagation();
  const dropdown = document.getElementById("year-dropdown");
  if (dropdown) dropdown.classList.toggle("hidden");
}
window.toggleYearDropdown = toggleYearDropdown;

function toggleMonthDropdown(event) {
  if (event) event.stopPropagation();
  const dropdown = document.getElementById("month-dropdown");
  if (dropdown) dropdown.classList.toggle("hidden");
}
window.toggleMonthDropdown = toggleMonthDropdown;

function toggleYearFilter(checkbox) {
  const val = checkbox.value;
  if (checkbox.checked) {
    selectedYears.add(val);
  } else {
    selectedYears.delete(val);
  }
  updateYearFilterButtonLabel();
  applyDateFilter();
}
window.toggleYearFilter = toggleYearFilter;

function toggleMonthFilter(checkbox) {
  const val = checkbox.value;
  if (checkbox.checked) {
    selectedMonths.add(val);
  } else {
    selectedMonths.delete(val);
  }
  updateMonthFilterButtonLabel();
  applyDateFilter();
}
window.toggleMonthFilter = toggleMonthFilter;

function updateYearFilterButtonLabel() {
  const label = document.getElementById("year-filter-label");
  const btn = document.getElementById("btn-year-filter");
  if (!label || !btn) return;
  if (selectedYears.size > 0) {
    label.textContent = `Years (${selectedYears.size})`;
    btn.classList.add("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
    btn.classList.remove("bg-slate-100", "border-slate-200", "text-slate-600");
  } else {
    label.textContent = "Years";
    btn.classList.add("bg-slate-100", "border-slate-200", "text-slate-600");
    btn.classList.remove("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
  }
}
window.updateYearFilterButtonLabel = updateYearFilterButtonLabel;

function updateMonthFilterButtonLabel() {
  const label = document.getElementById("month-filter-label");
  const btn = document.getElementById("btn-month-filter");
  if (!label || !btn) return;
  if (selectedMonths.size > 0) {
    label.textContent = `Months (${selectedMonths.size})`;
    btn.classList.add("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
    btn.classList.remove("bg-slate-100", "border-slate-200", "text-slate-600");
  } else {
    label.textContent = "Months";
    btn.classList.add("bg-slate-100", "border-slate-200", "text-slate-600");
    btn.classList.remove("bg-indigo-50/70", "border-indigo-200", "text-indigo-700");
  }
}
window.updateMonthFilterButtonLabel = updateMonthFilterButtonLabel;

function populateYearDropdown() {
  const container = document.getElementById("year-filter-container");
  const dropdown = document.getElementById("year-dropdown");
  if (!container || !dropdown) return;

  const yearsSet = new Set();
  
  // Extract years from files
  const files = galleryMeta?.files || [];
  files.forEach(f => {
    if (f.date && f.date.length >= 4) {
      const yr = f.date.slice(0, 4);
      if (/^\d{4}$/.test(yr)) {
        yearsSet.add(yr);
      }
    }
  });

  // Extract years from posts
  const posts = galleryMeta?.posts || [];
  posts.forEach(p => {
    if (p.date && p.date.length >= 4) {
      const yr = p.date.slice(0, 4);
      if (/^\d{4}$/.test(yr)) {
        yearsSet.add(yr);
      }
    }
  });

  if (yearsSet.size === 0) {
    container.style.display = "none";
    return;
  }
  container.style.display = "inline-block";

  const sortedYears = Array.from(yearsSet).sort((a, b) => b.localeCompare(a)); // Descending

  let html = "";
  sortedYears.forEach(yr => {
    const isChecked = selectedYears.has(yr);
    html += `
      <label class="flex items-center gap-2 px-2 py-1.5 hover:bg-slate-50 rounded-lg cursor-pointer text-xs font-semibold text-slate-700 select-none">
        <input type="checkbox" value="${yr}" ${isChecked ? "checked" : ""} class="accent-indigo-600 rounded w-3.5 h-3.5 animate-none" onchange="toggleYearFilter(this)">
        <span>${yr}</span>
      </label>
    `;
  });

  dropdown.innerHTML = html;
  updateYearFilterButtonLabel();
}
window.populateYearDropdown = populateYearDropdown;
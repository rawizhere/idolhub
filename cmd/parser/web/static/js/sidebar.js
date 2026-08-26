import { state } from "./state.js";
import { timeAgo, toast, confirmDialog, escapeHtml, platformBadgeClass } from "./utils.js";
import { startSyncApi, cancelSyncApi, clearFolderApi, fetchConfig, postConfig } from "./api.js";
import { updateTerminal } from "./dock.js";
import { renderOverviewDashboard } from "./overview.js";
import { loadUiState, saveUiState } from "./state.js";
import {
  selectGalleryTarget,
  switchGalleryView,
  syncMediaTypePills,
  populateDateDropdown,
  populateHashtagDropdown,
  updateDateFilterLabel,
  updateHashtagLabel,
  renderPostsSortState
} from "./gallery.js";

export function filterTargets() {
  const input = document.getElementById("target-search-input");
  const clearBtn = document.getElementById("target-search-clear");
  state.targetSearchQuery = (input ? input.value : "").toLowerCase().trim();
  if (clearBtn) clearBtn.classList.toggle("hidden", !state.targetSearchQuery);
  renderDashboardSidebar();
}

export function clearTargetSearch() {
  const input = document.getElementById("target-search-input");
  if (input) input.value = "";
  filterTargets();
}

export function setPlatformFilter(platform) {
  state.targetPlatformFilter = platform;
  document.querySelectorAll("#target-platform-chips [data-platform]").forEach((btn) => {
    const active = btn.dataset.platform === platform;
    btn.className = active
      ? "chip flex-1 py-1 rounded-md font-bold text-center transition-all cursor-pointer bg-[#ff9900] text-black dark:text-black shadow-2xs border border-[#ff9900]"
      : "chip flex-1 py-1 rounded-md font-semibold text-center transition-all cursor-pointer text-slate-600 dark:text-zinc-400 hover:text-slate-900 dark:hover:text-white bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800";
  });
  renderDashboardSidebar();
}

export function renderDashboardSidebar() {
  const container = document.getElementById("dashboard-sidebar-list");
  const countLabel = document.getElementById("sidebar-count-label");
  if (!container) return;

  const filtered = state.cachedProgress.filter(t => {
    if (state.targetPlatformFilter !== "all" && t.platform !== state.targetPlatformFilter) return false;
    if (state.targetSearchQuery && t.username.toLowerCase().indexOf(state.targetSearchQuery) === -1) return false;
    return true;
  });

  if (countLabel) countLabel.textContent = `${filtered.length}/${state.cachedProgress.length} targets`;

  if (filtered.length === 0) {
    container.innerHTML = `<p class="text-xs text-slate-500 dark:text-zinc-400 py-6 text-center">${state.cachedProgress.length === 0 ? "No targets configured" : "No matching targets"}</p>`;
    return;
  }

  let html = "";
  filtered.forEach(target => {
    const isActive = state.activeTerminalUser === target.username;
    const mediaCount = target.media_count || 0;
    const isRunning = target.status === "running" || target.status === "queued";

    const cardClass = isActive
      ? "bg-[#ff9900]/15 border-[#ff9900]/50 text-slate-900 dark:text-white font-semibold shadow-xs"
      : "bg-white hover:bg-slate-100 dark:bg-zinc-900 dark:hover:bg-zinc-800 border-slate-200 dark:border-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white";

    let statusDotClass = "bg-slate-400";
    if (target.status === "running") statusDotClass = "bg-[#ff9900] animate-pulse";
    else if (target.status === "queued") statusDotClass = "bg-amber-500 animate-pulse";
    else if (target.status === "completed") statusDotClass = "bg-emerald-500";
    else if (target.status === "failed") statusDotClass = "bg-rose-500";

    const lastSync = timeAgo(target.updated_at);

    const syncButtonHTML = isRunning
      ? `<div class="w-6 h-6 rounded-md bg-amber-500/10 flex items-center justify-center text-[#ff9900]"><svg class="w-3.5 h-3.5 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg></div>`
      : `<button data-sidebar-action="quick-sync" data-username="${escapeHtml(target.username)}" class="opacity-0 group-hover:opacity-100 hover:bg-slate-200 dark:hover:bg-zinc-700 p-1 rounded-md text-slate-500 dark:text-zinc-400 hover:text-slate-900 dark:hover:text-white transition-all cursor-pointer" title="Quick Sync (@${escapeHtml(target.username)})">
           <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
         </button>`;

    const authErrorShortcut = target.auth_error
      ? `<button data-sidebar-action="open-token-settings" data-platform="${escapeHtml(target.platform)}" class="inline-flex items-center gap-1 text-[9px] font-bold text-rose-600 dark:text-rose-400 bg-rose-50 dark:bg-rose-950/50 border border-rose-200 dark:border-rose-800/80 px-1.5 py-0.5 rounded hover:bg-rose-100 cursor-pointer" title="Authentication error - click to fix token">
           ⚠️ Fix Token
         </button>`
      : "";

    html += `
      <div class="group flex items-center justify-between p-2.5 rounded-xl border transition-all duration-150 cursor-pointer ${cardClass}" data-select-target="${escapeHtml(target.username)}">
        <div class="flex items-center gap-2.5 min-w-0 flex-1 mr-2">
          <span class="w-2 h-2 rounded-full ${statusDotClass} flex-shrink-0"></span>
          <div class="flex flex-col gap-0.5 min-w-0">
            <div class="flex items-center gap-1.5 min-w-0">
              <span class="text-xs font-bold tracking-tight truncate select-none">@${escapeHtml(target.username)}</span>
              <span class="text-[9px] font-bold uppercase px-1 py-0.2 rounded border flex-shrink-0 ${platformBadgeClass(target.platform)}">${escapeHtml(target.platform)}</span>
            </div>
            <div class="flex items-center gap-1.5 text-[10px] text-slate-400 dark:text-zinc-500 font-mono">
              <span>${mediaCount} files</span>
              ${target.new_count > 0 ? `<span class="text-emerald-600 dark:text-emerald-400 font-bold bg-emerald-50 dark:bg-emerald-950/40 px-1 py-0.2 rounded border border-emerald-200 dark:border-emerald-800/80">+${target.new_count}</span>` : ""}
              ${lastSync ? `<span>· ${lastSync}</span>` : ""}
              ${authErrorShortcut}
            </div>
          </div>
        </div>
        <div class="flex items-center gap-1 flex-shrink-0">
          ${syncButtonHTML}
        </div>
      </div>
    `;
  });

  container.innerHTML = html;
}

export function selectTerminalUser(username) {
  state.activeTerminalUser = username;
  renderDashboardSidebar();
  closeSidebar();

  const target = state.cachedProgress.find(t => t.username === username);
  if (!target) return;

  const breadcrumb = document.getElementById("header-breadcrumb-label");
  if (breadcrumb) breadcrumb.textContent = `@${target.username}`;

  document.getElementById("dashboard-empty").style.display = "none";
  document.getElementById("dashboard-target-header").style.display = "flex";
  document.getElementById("dashboard-gallery-card").style.display = "flex";

  document.getElementById("dashboard-username-label").textContent = `@${target.username}`;

  const platformBadge = document.getElementById("dashboard-platform-badge");
  platformBadge.textContent = target.platform.toUpperCase();
  platformBadge.className = `text-[10px] font-bold uppercase px-2 py-0.5 rounded border ${platformBadgeClass(target.platform)}`;

  const syncBtn = document.getElementById("dashboard-btn-sync");
  syncBtn.onclick = () => startSync(target.username, true);

  document.getElementById("dashboard-btn-edit").onclick = () => {
    fetchConfig().then(cfg => {
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
    }).catch(err => toast(`Failed to load config: ${err.message}`, "error"));
  };

  document.getElementById("dashboard-btn-delete").onclick = async () => {
    const ok = await confirmDialog({
      title: "Delete Target Account",
      message: `Remove @${target.username} from active configuration? Downloaded files will remain safe on disk.`,
      confirmText: "Delete",
      tone: "danger",
    });
    if (!ok) return;
    deleteAccount(target.username);
    goToOverview();
  };

  document.getElementById("dashboard-btn-clear").onclick = async () => {
    const ok = await confirmDialog({
      title: "Clear Downloaded Files",
      message: `Permanently delete all downloaded media for @${target.username}? This action cannot be undone.`,
      confirmText: "Clear Files",
      tone: "danger",
    });
    if (!ok) return;
    clearTargetFolder(target.platform, target.username);
  };

  document.getElementById("dashboard-btn-cancel").onclick = () => cancelSync(target.username);

  updateDashboardDetails();
  updateTerminal();
  saveUiState();
  return selectGalleryTarget(target.platform, target.username);
}

let uiRestored = false;

export async function restorePersistedView() {
  if (uiRestored) return;
  uiRestored = true;
  const saved = loadUiState();
  if (!saved || !saved.activeTerminalUser) return;
  const target = state.cachedProgress.find(t => t.username === saved.activeTerminalUser);
  if (!target) return;

  await selectTerminalUser(saved.activeTerminalUser);

  state.currentView = saved.currentView === "posts" ? "posts" : "grid";
  state.currentFilter = saved.currentFilter || "all";
  state.gridSearchQuery = saved.gridSearchQuery || "";
  state.postsSearchQuery = saved.postsSearchQuery || "";
  state.targetPlatformFilter = saved.targetPlatformFilter || "all";
  (Array.isArray(saved.selectedYears) ? saved.selectedYears : []).forEach(y => state.selectedYears.add(String(y)));
  (Array.isArray(saved.selectedMonths) ? saved.selectedMonths : []).forEach(m => state.selectedMonths.add(String(m)));
  (Array.isArray(saved.selectedHashtags) ? saved.selectedHashtags : []).forEach(t => state.selectedHashtags.add(String(t)));

  populateDateDropdown();
  populateHashtagDropdown();
  updateDateFilterLabel();
  updateHashtagLabel();
  renderPostsSortState();
  switchGalleryView(state.currentView);
  syncMediaTypePills();

  const galleryInput = document.getElementById("gallery-search-input");
  if (galleryInput && state.gridSearchQuery) galleryInput.value = state.gridSearchQuery;
}

export function goToOverview() {
  state.activeTerminalUser = null;
  state.activeGalleryUser = null;
  saveUiState();
  const breadcrumb = document.getElementById("header-breadcrumb-label");
  if (breadcrumb) breadcrumb.textContent = "Overview";

  document.getElementById("dashboard-target-header").style.display = "none";
  document.getElementById("dashboard-gallery-card").style.display = "none";
  document.getElementById("dashboard-empty").style.display = "flex";

  renderDashboardSidebar();
  renderOverviewDashboard();
}

export function updateDashboardDetails() {
  if (!state.activeTerminalUser) return;
  const target = state.cachedProgress.find(t => t.username === state.activeTerminalUser);
  if (!target) return;

  const countEl = document.getElementById("dashboard-file-count");
  if (countEl) countEl.innerHTML = `<svg class="w-3 h-3 text-slate-400 inline mr-1" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5" rx="1"/><line x1="10" y1="13" x2="14" y2="13"/></svg> ${target.media_count || 0} files`;

  const isRunning = target.status === "running" || target.status === "queued";
  const syncBtn = document.getElementById("dashboard-btn-sync");
  const cancelBtn = document.getElementById("dashboard-btn-cancel");
  if (syncBtn) syncBtn.disabled = isRunning;
  if (cancelBtn) {
    cancelBtn.classList.toggle("hidden", !isRunning);
    cancelBtn.classList.toggle("flex", isRunning);
  }
}

export async function quickSyncTarget(username) {
  try {
    await startSyncApi(username);
    toast(`Sync started for @${username}`, "success", 2000);
  } catch (err) {
    toast(`Failed to sync @${username}: ${err.message}`, "error");
  }
}

export async function startSync(username, shouldSelect = false) {
  try {
    await startSyncApi(username);
    if (shouldSelect) selectTerminalUser(username);
    toast(`Sync started for @${username}`, "success", 2000);
  } catch (err) {
    toast(`Failed to start sync: ${err.message}`, "error");
  }
}

export async function fullResyncAll() {
  const ok = await confirmDialog({
    title: "Force Full Resync All Targets",
    message: "This will fully rescan all configured targets from the beginning of their feeds. Existing files will be preserved. Proceed?",
    confirmText: "Start Full Resync",
    tone: "primary",
  });
  if (!ok) return;

  try {
    const modal = document.getElementById("settings-modal");
    if (modal) modal.style.display = "none";
    await startSyncApi("all", true);
    toast("Full resync started for all targets.", "success", 2500);
  } catch (err) {
    toast(`Failed to start full resync: ${err.message}`, "error");
  }
}

export async function cancelSync(username) {
  const ok = await confirmDialog({
    title: "Cancel Active Sync",
    message: `Stop the running sync for @${username}? Downloaded files will be kept.`,
    confirmText: "Cancel Sync",
    tone: "danger",
  });
  if (!ok) return;
  try {
    await cancelSyncApi(username);
    toast(`Cancelled sync for @${username}`, "info");
  } catch (err) {
    toast(`Failed to cancel: ${err.message}`, "error");
  }
}

export async function deleteAccount(username) {
  try {
    const current = await fetchConfig();
    current.accounts = current.accounts.filter(acc => acc.username.toLowerCase() !== username.toLowerCase());
    await postConfig(current);
    toast(`Removed @${username}`, "success");
  } catch (err) {
    toast(`Failed to delete @${username}: ${err.message}`, "error");
  }
}

export async function clearTargetFolder(platform, username) {
  try {
    await clearFolderApi(platform, username);
    toast(`Cleared downloads for @${username}`, "success");
    selectGalleryTarget(platform, username);
  } catch (err) {
    toast(`Failed to clear folder: ${err.message}`, "error");
  }
}

export function openSidebar() {
  const sb = document.getElementById("sidebar-container");
  const ov = document.getElementById("sidebar-overlay");
  if (sb) sb.classList.remove("-translate-x-full");
  if (ov) ov.classList.remove("hidden");
}

export function closeSidebar() {
  const sb = document.getElementById("sidebar-container");
  const ov = document.getElementById("sidebar-overlay");
  if (sb) sb.classList.add("-translate-x-full");
  if (ov) ov.classList.add("hidden");
}

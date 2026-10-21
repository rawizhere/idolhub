import { state, targetKey, activeTarget, isActiveTarget } from "./state.js";
import { onClick, onInput } from "./actions.js";
import { timeAgo, toast, confirmDialog, escapeHtml, platformBadgeClass } from "./utils.js";
import { startSyncApi, cancelSyncApi, clearFolderApi, deleteAccountApi } from "./api.js";
import { goTarget, goOverview } from "./nav.js";
import { showTarget } from "./gallery.js";
import { openSettings } from "./settings.js";
import { renderPauseState } from "./target-settings.js";
import { loadProgress } from "./sse.js";

const $ = (id) => document.getElementById(id);
const isRunning = (t) => t.status === "running" || t.status === "queued";

export function visibleTargets() {
  return state.cachedProgress.filter(t => {
    if (state.targetPlatformFilter !== "all" && t.platform !== state.targetPlatformFilter) return false;
    if (state.targetSearchQuery && !t.username.toLowerCase().includes(state.targetSearchQuery)) return false;
    return true;
  });
}

// progressInfo is the progress of a running target. Scrapers report coarse stages, not a rate, so there is no ETA.
export function progressInfo(target) {
  if (!isRunning(target)) return null;
  return { pct: Math.max(0, Math.min(100, target.progress || 0)), queued: target.status === "queued" };
}

function cardHTML(target) {
  const active = isActiveTarget(target.username, target.platform);
  const running = isRunning(target);
  const cardClass = active
    ? "bg-[#ff9900]/15 border-[#ff9900]/50 text-slate-900 dark:text-white font-semibold shadow-xs"
    : "bg-white hover:bg-slate-100 dark:bg-zinc-900 dark:hover:bg-zinc-800 border-slate-200 dark:border-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white";

  let dot = "bg-slate-400";
  if (target.status === "running") dot = "bg-[#ff9900] animate-pulse";
  else if (target.status === "queued") dot = "bg-amber-500 animate-pulse";
  else if (target.status === "failed" || target.auth_error) dot = "bg-rose-500";
  else if (target.status === "completed") dot = "bg-emerald-500";

  const u = escapeHtml(target.username);
  const p = escapeHtml(target.platform);
  const lastSync = timeAgo(target.updated_at);
  const prog = progressInfo(target);

  const syncButton = running
    ? `<button data-action="cancel-target" data-username="${u}" data-platform="${p}" class="w-6 h-6 rounded-md bg-amber-500/10 flex items-center justify-center text-[#ff9900] hover:bg-rose-500/15 hover:text-rose-500 cursor-pointer" title="Cancel sync" aria-label="Cancel sync for @${u}"><svg class="w-3.5 h-3.5 animate-spin" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg></button>`
    : `<button data-action="quick-sync" data-username="${u}" data-platform="${p}" class="opacity-0 group-hover:opacity-100 focus:opacity-100 hover:bg-slate-200 dark:hover:bg-zinc-700 p-1 rounded-md text-slate-500 dark:text-zinc-400 hover:text-slate-900 dark:hover:text-white transition-all cursor-pointer" title="Sync @${u}" aria-label="Sync @${u}">
         <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
       </button>`;

  const authFix = target.auth_error
    ? `<button data-action="fix-cookies" data-platform="${p}" class="flex-shrink-0 text-[0.5625rem] font-bold text-rose-600 dark:text-rose-400 bg-rose-50 dark:bg-rose-950/50 border border-rose-200 dark:border-rose-800/80 px-1.5 py-1 rounded-md hover:bg-rose-100 dark:hover:bg-rose-950 cursor-pointer" title="Authentication error — update cookies">Fix cookies</button>`
    : "";

  const progress = prog
    ? `<div class="flex items-center gap-1.5 mt-1">
         <div class="flex-1 h-1 rounded-full bg-slate-200 dark:bg-zinc-800 overflow-hidden"><div class="h-full bg-[#ff9900] transition-all duration-300" style="width:${prog.queued ? 0 : prog.pct}%"></div></div>
         <span class="text-[0.5625rem] font-mono text-slate-500 dark:text-zinc-400 whitespace-nowrap">${prog.queued ? "queued" : `${prog.pct}%`}</span>
       </div>`
    : "";

  return `
    <div data-target-key="${escapeHtml(targetKey(target))}" class="group relative p-2.5 rounded-xl border transition-colors duration-150 ${cardClass} ${target.paused && !active ? "opacity-70" : ""}">
      <div class="flex items-center justify-between gap-2">
        <div class="flex items-center gap-2.5 min-w-0 flex-1">
          <span class="w-2 h-2 rounded-full ${dot} flex-shrink-0"></span>
          <div class="flex flex-col gap-0.5 min-w-0">
            <div class="flex items-center gap-1.5 min-w-0">
              <a href="/?t=${encodeURIComponent(`${target.platform}/${target.username}`)}" data-action="select-target" data-username="${u}" data-platform="${p}" ${active ? 'aria-current="page"' : ""} class="text-xs font-bold tracking-tight truncate outline-none after:absolute after:inset-0 after:rounded-xl focus-visible:after:ring-2 focus-visible:after:ring-[#ff9900]">@${u}</a>
              <span class="text-[0.5625rem] font-bold uppercase px-1 py-0.5 rounded border flex-shrink-0 leading-none ${platformBadgeClass(target.platform)}">${p}</span>
            </div>
            <div class="flex items-center gap-1.5 text-[0.625rem] text-slate-400 dark:text-zinc-500 font-mono">
              <span>${(target.media_count || 0).toLocaleString()} files</span>
              ${target.new_count > 0 ? `<span class="text-emerald-600 dark:text-emerald-400 font-bold">+${target.new_count}</span>` : ""}
              ${target.paused ? `<span class="text-amber-600 dark:text-amber-400 font-bold" title="Auto-sync paused">⏸ paused</span>` : ""}
              ${lastSync ? `<span>· ${lastSync}</span>` : ""}
            </div>
          </div>
        </div>
        <div class="relative z-10 flex items-center gap-1.5 flex-shrink-0">${authFix}${syncButton}</div>
      </div>
      ${progress}
    </div>`;
}

export function renderDashboardSidebar() {
  const container = $("dashboard-sidebar-list");
  if (!container) return;
  const filtered = visibleTargets();
  $("sidebar-count-label").textContent = `${filtered.length}/${state.cachedProgress.length} targets`;
  if (filtered.length === 0) {
    container.innerHTML = `<p class="text-xs text-slate-500 dark:text-zinc-400 py-6 text-center">${state.cachedProgress.length === 0 ? "No targets configured" : "No matching targets"}</p>`;
    return;
  }
  container.innerHTML = filtered.map(cardHTML).join("");
}

// Status and progress events touch one target; batch them per frame and replace only those cards.
const pendingCards = new Set();
let cardFrame = 0;

export function updateTargetCard(target) {
  pendingCards.add(targetKey(target));
  if (cardFrame) return;
  cardFrame = requestAnimationFrame(() => {
    cardFrame = 0;
    pendingCards.forEach(key => {
      const t = state.cachedProgress.find(x => targetKey(x) === key);
      const el = document.querySelector(`[data-target-key="${CSS.escape(key)}"]`);
      if (t && el) el.outerHTML = cardHTML(t);
    });
    pendingCards.clear();
  });
}

/* ---------- active target header ---------- */

export function showTargetChrome(target) {
  $("header-breadcrumb-label").textContent = `@${target.username}`;
  $("dashboard-empty").classList.add("hidden");
  for (const id of ["dashboard-target-header", "dashboard-gallery-card"]) {
    $(id).classList.remove("hidden");
    $(id).classList.add("flex");
  }
  $("dashboard-username-label").textContent = `@${target.username}`;
  const badge = $("dashboard-platform-badge");
  badge.textContent = target.platform.toUpperCase();
  badge.className = `text-[0.625rem] font-bold uppercase px-2 py-0.5 rounded border ${platformBadgeClass(target.platform)}`;
  document.title = `@${target.username} · IdolHub`;
  updateDashboardDetails(target);
  renderPauseState();
}

export function hideTargetChrome() {
  $("header-breadcrumb-label").textContent = "Overview";
  for (const id of ["dashboard-target-header", "dashboard-gallery-card"]) {
    $(id).classList.add("hidden");
    $(id).classList.remove("flex");
  }
  $("dashboard-empty").classList.remove("hidden");
  $("selection-bar").classList.add("hidden");
  $("selection-bar").classList.remove("flex");
  document.title = "IdolHub — Media Archiver Studio";
}

export function updateDashboardDetails(target = activeTarget()) {
  if (!target) return;
  $("dashboard-file-count").textContent = `${(target.media_count || 0).toLocaleString()} files`;
  const running = isRunning(target);
  $("dashboard-btn-sync").disabled = running;
  $("dashboard-btn-cancel").classList.toggle("hidden", !running);
  const prog = progressInfo(target);
  const box = $("dashboard-progress");
  box.classList.toggle("hidden", !prog);
  box.classList.toggle("flex", !!prog);
  if (prog) {
    $("dashboard-progress-bar").style.width = `${prog.queued ? 0 : prog.pct}%`;
    $("dashboard-progress-label").textContent = prog.queued ? "queued" : `${prog.pct}%`;
  }
}

/* ---------- actions ---------- */

export async function startSync(username, platform, forceFull = false) {
  try {
    await startSyncApi(username, forceFull, platform);
    toast(forceFull ? `Full resync started for @${username}` : `Sync started for @${username}`, "success", 2000);
  } catch (err) {
    toast(`Failed to start sync: ${err.message}`, "error");
  }
}

export async function cancelSync(username, platform) {
  const ok = await confirmDialog({
    title: "Cancel Active Sync",
    message: `Stop the running sync for @${username}? Downloaded files will be kept.`,
    confirmText: "Stop sync",
    cancelText: "Keep running",
    tone: "danger",
  });
  if (!ok) return;
  try {
    await cancelSyncApi(username, platform);
    toast(`Cancelled sync for @${username}`, "info");
  } catch (err) {
    toast(`Failed to cancel: ${err.message}`, "error");
  }
}

export async function fullResync(username, platform) {
  const ok = await confirmDialog({
    title: "Force Full Resync",
    message: `Fully rescan @${username} from the beginning of its feed? Existing files will be preserved.`,
    confirmText: "Start Full Resync",
    tone: "primary",
  });
  if (ok) startSync(username, platform, true);
}

async function deleteAccount(target) {
  const ok = await confirmDialog({
    title: "Remove Target",
    message: `Stop archiving @${target.username} (${target.platform})? Downloaded files stay on disk.`,
    confirmText: "Remove target",
    tone: "danger",
  });
  if (!ok) return;
  try {
    await deleteAccountApi(target.platform, target.username);
    toast(`Removed @${target.username}`, "success");
    goOverview();
    loadProgress();
  } catch (err) {
    toast(`Failed to remove @${target.username}: ${err.message}`, "error");
  }
}

async function clearTargetFolder(target) {
  const ok = await confirmDialog({
    title: "Erase Downloaded Files",
    message: `Permanently delete every downloaded file for @${target.username}?\nThe target stays configured. This cannot be undone.`,
    confirmText: "Erase files",
    tone: "danger",
  });
  if (!ok) return;
  try {
    await clearFolderApi(target.platform, target.username);
    toast(`Erased downloads for @${target.username}`, "success");
    state.target = null;
    showTarget(target);
    loadProgress();
  } catch (err) {
    toast(`Failed to erase files: ${err.message}`, "error");
  }
}

function withActive(fn) {
  return () => {
    document.querySelectorAll("[popover]:popover-open").forEach(p => p.hidePopover());
    const t = state.target;
    if (t) fn(t);
  };
}

export function openSidebar() {
  $("sidebar-container").classList.remove("-translate-x-full");
  $("sidebar-overlay").classList.remove("hidden");
}

export function closeSidebar() {
  $("sidebar-container").classList.add("-translate-x-full");
  $("sidebar-overlay").classList.add("hidden");
}

export function setPlatformFilter(platform) {
  state.targetPlatformFilter = platform;
  document.querySelectorAll("#target-platform-chips [data-platform]").forEach(b => b.setAttribute("aria-pressed", b.dataset.platform === platform ? "true" : "false"));
  renderDashboardSidebar();
}

onClick({
  "select-target": (el) => goTarget(el.dataset.username, el.dataset.platform),
  "quick-sync": (el) => startSync(el.dataset.username, el.dataset.platform),
  "cancel-target": (el) => cancelSync(el.dataset.username, el.dataset.platform),
  "fix-cookies": (el) => openSettings(el.dataset.platform),
  "sync-active": withActive(t => startSync(t.username, t.platform)),
  "cancel-active": withActive(t => cancelSync(t.username, t.platform)),
  "full-resync-active": withActive(t => fullResync(t.username, t.platform)),
  "clear-active": withActive(clearTargetFolder),
  "delete-active": withActive(deleteAccount),
  "open-sidebar": openSidebar,
  "close-sidebar": closeSidebar,
  "platform-filter": (el) => setPlatformFilter(el.dataset.platform),
  "clear-target-filter": () => {
    $("target-search-input").value = "";
    state.targetSearchQuery = "";
    $("target-search-clear").classList.add("hidden");
    renderDashboardSidebar();
  }
});

onInput({
  "filter-targets": (el) => {
    state.targetSearchQuery = el.value.toLowerCase().trim();
    $("target-search-clear").classList.toggle("hidden", !state.targetSearchQuery);
    renderDashboardSidebar();
  }
});

import { state } from "./state.js";
import { toast, escapeHtml } from "./utils.js";
import { startSyncApi, fetchGlobalSearch } from "./api.js";
import { initPhotoSwipeGrid, initVideoHoverPreviews } from "./gallery.js";

export function computeCountdown() {
  if (state.autoSyncInterval <= 0 || !state.lastSyncTime) return null;
  const next = new Date(state.lastSyncTime).getTime() + state.autoSyncInterval * 3600000;
  const diff = next - Date.now();
  if (diff <= 0) return { due: true };
  const totalMin = Math.floor(diff / 60000);
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  return { hours: h, minutes: m };
}

export function updateAutoUpdateStatus() {
  const dot = document.getElementById("autoupdate-dot");
  const text = document.getElementById("autoupdate-text");
  if (!dot || !text) return;

  if (state.autoSyncInterval > 0) {
    dot.className = "w-2 h-2 bg-emerald-500 rounded-full animate-pulse";
    const cd = computeCountdown();
    text.textContent = cd === null ? `Every ${state.autoSyncInterval}h` : (cd.due ? "Syncing…" : `Next: ${cd.hours > 0 ? cd.hours + "h " : ""}${cd.minutes}m`);
  } else {
    dot.className = "w-2 h-2 bg-slate-400 rounded-full";
    text.textContent = "Auto-Sync: Off";
  }
}

export function startCountdownTicker() {
  if (state.countdownTicker) return;
  state.countdownTicker = setInterval(updateAutoUpdateStatus, 30000);
}

export function updateGlobalSyncBadge() {
  const badge = document.getElementById("global-sync-status");
  const dot = document.getElementById("global-status-dot");
  const text = document.getElementById("global-status-text");
  const dockDot = document.getElementById("dock-status-dot");
  if (!badge || !dot || !text) return;

  const runningCount = state.cachedProgress.filter(t => t.status === "running" || t.status === "queued").length;
  if (runningCount > 0) {
    dot.className = "w-2 h-2 rounded-full bg-[#ff9900] animate-pulse";
    if (dockDot) dockDot.className = "w-2 h-2 rounded-full bg-[#ff9900] animate-pulse";
    text.textContent = `Syncing: ${runningCount} active`;
    badge.className = "hidden sm:flex items-center gap-2 text-[11px] font-bold px-2.5 py-1 rounded-full border bg-amber-500/10 text-[#ff9900] border-[#ff9900]/30 transition-all";
  } else {
    dot.className = "w-2 h-2 rounded-full bg-emerald-500";
    if (dockDot) dockDot.className = "w-2 h-2 rounded-full bg-emerald-500";
    text.textContent = "Idle";
    badge.className = "hidden sm:flex items-center gap-2 text-[11px] font-semibold px-2.5 py-1 rounded-full border bg-slate-100 dark:bg-zinc-900 text-slate-600 dark:text-zinc-400 border-slate-200 dark:border-zinc-800 transition-all";
  }
}

export async function syncAll() {
  try {
    await startSyncApi("all");
    toast("Sync started for all targets.", "success", 2500);
  } catch (err) {
    toast(`Failed to trigger sync: ${err.message}`, "error");
  }
}

export async function renderGlobalSearchResults(query) {
  const container = document.getElementById("dashboard-empty");
  if (!container) return;

  const q = (query || "").trim();
  if (!q) {
    renderOverviewDashboard();
    return;
  }

  container.innerHTML = `
    <div class="flex flex-col gap-4 py-8 items-center justify-center text-center">
      <div class="animate-spin rounded-full h-8 w-8 border-2 border-slate-200 dark:border-zinc-800 border-t-[#ff9900]"></div>
      <p class="text-xs font-semibold text-slate-500 dark:text-zinc-400">Searching entire library for "${escapeHtml(q)}"...</p>
    </div>
  `;

  try {
    const data = await fetchGlobalSearch(q);
    const files = data.files || [];

    if (files.length === 0) {
      container.innerHTML = `
        <div class="flex flex-col items-center justify-center py-20 gap-3 text-slate-400 dark:text-zinc-500 text-center">
          <div class="p-4 rounded-2xl bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 shadow-sm">
            <svg class="w-8 h-8 text-slate-400" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"></circle><line x1="21" x2="16.65" y1="21" y2="16.65"></line></svg>
          </div>
          <h3 class="text-sm font-bold text-slate-800 dark:text-zinc-200">No media found matching "${escapeHtml(q)}"</h3>
          <p class="text-xs max-w-sm">No photos, videos, captions, or hashtags matched your search across all target accounts.</p>
          <button onclick="window.clearGlobalSearch()" class="mt-2 text-xs font-bold text-[#ff9900] hover:underline cursor-pointer">
            Clear Search
          </button>
        </div>
      `;
      return;
    }

    let cardsHtml = "";
    files.forEach(f => {
      let platBadge = "bg-pink-50 dark:bg-pink-950/50 text-pink-700 dark:text-pink-400 border-pink-200 dark:border-pink-800";
      if (f.platform === "twitter") platBadge = "bg-sky-50 dark:bg-sky-950/50 text-sky-700 dark:text-sky-400 border-sky-200 dark:border-sky-800";
      else if (f.platform === "tiktok") platBadge = "bg-slate-100 dark:bg-zinc-800 text-slate-700 dark:text-zinc-300 border-slate-300 dark:border-zinc-700";

      cardsHtml += `
        <div class="gallery-item group relative flex flex-col overflow-hidden rounded-xl border border-slate-200 dark:border-zinc-800/80 bg-white dark:bg-zinc-900 shadow-xs hover:border-[#ff9900]/50 transition-all duration-200">
          <a class="pswp-item block w-full relative aspect-square overflow-hidden cursor-pointer bg-slate-100 dark:bg-zinc-950 video-preview-tile" href="${f.url}" title="${escapeHtml(f.filename)}" ${f.type === 'video' ? `data-pswp-type="video" data-video-src="${f.url}"` : ''}>
            <img src="${f.thumbnail_url}" alt="${escapeHtml(f.filename)}" loading="lazy" class="w-full h-full object-cover group-hover:scale-103 transition-transform duration-200 block" />
            
            <!-- Top Target & Date Badges -->
            <div class="absolute top-2 inset-x-2 flex items-center justify-between pointer-events-none z-20">
              <span onclick="event.preventDefault(); event.stopPropagation(); window.selectTerminalUser('${f.username}')" class="pointer-events-auto text-[9px] font-bold px-1.5 py-0.5 rounded border backdrop-blur-xs shadow-xs cursor-pointer hover:scale-105 transition-transform ${platBadge}">
                @${f.username}
              </span>
              ${f.date ? `<span class="text-[9px] font-mono font-bold bg-black/75 text-white px-1.5 py-0.5 rounded backdrop-blur-xs">${f.date}</span>` : ''}
            </div>

            ${f.type === 'video' ? `
              <div class="absolute inset-0 flex items-center justify-center bg-black/15 group-hover:bg-black/25 transition-all duration-200 pointer-events-none">
                <div class="bg-black/75 backdrop-blur-xs text-white p-2.5 rounded-full shadow-md scale-95 group-hover:scale-105 transition-all duration-200 flex items-center justify-center">
                  <svg viewBox="0 0 24 24" fill="currentColor" class="w-4 h-4"><path d="M8 5v14l11-7z"/></svg>
                </div>
              </div>
              <span class="absolute bottom-2 right-2 text-[9px] font-mono font-bold bg-black/80 text-white px-1.5 py-0.5 rounded backdrop-blur-xs">VIDEO</span>
            ` : ''}
          </a>
          ${f.caption ? `
            <div class="p-2 border-t border-slate-100 dark:border-zinc-800/60 bg-white dark:bg-zinc-900">
              <p class="text-[11px] text-slate-600 dark:text-zinc-400 line-clamp-2 leading-relaxed font-normal">${escapeHtml(f.caption)}</p>
            </div>
          ` : ''}
        </div>
      `;
    });

    container.innerHTML = `
      <div class="flex flex-col gap-4">
        <!-- Results Header -->
        <div class="flex justify-between items-center bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 p-3.5 rounded-xl shadow-2xs">
          <div class="flex items-center gap-2.5">
            <div class="p-2 rounded-lg bg-[#ff9900]/10 text-[#ff9900]">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"></circle><line x1="21" x2="16.65" y1="21" y2="16.65"></line></svg>
            </div>
            <div>
              <h2 class="text-xs font-bold text-slate-900 dark:text-zinc-100 uppercase tracking-wider">Global Search Results</h2>
              <p class="text-[11px] text-slate-500 dark:text-zinc-400">Found <strong class="text-slate-900 dark:text-white">${files.length}</strong> media files matching "<span class="text-[#ff9900] font-semibold">${escapeHtml(q)}</span>"</p>
            </div>
          </div>
          <button onclick="window.clearGlobalSearch()" class="text-xs font-bold text-rose-600 dark:text-rose-400 hover:underline px-3 py-1.5 cursor-pointer">
            Clear Search
          </button>
        </div>

        <!-- Stable Non-Jumping Grid -->
        <div id="global-search-grid" class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-3.5">
          ${cardsHtml}
        </div>
      </div>
    `;

    initPhotoSwipeGrid();
    initVideoHoverPreviews();
  } catch (err) {
    console.error("Global search render error:", err);
    container.innerHTML = `<div class="py-12 text-center text-xs font-semibold text-rose-500">Failed to load search results: ${escapeHtml(err.message)}</div>`;
  }
}

export function renderOverviewDashboard() {
  const container = document.getElementById("dashboard-empty");
  if (!container) return;

  if (state.cachedProgress.length === 0) {
    container.innerHTML = `
      <div class="flex-1 flex flex-col items-center justify-center py-24 gap-4 text-slate-400 dark:text-zinc-500 text-center">
        <div class="p-4 rounded-2xl bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 shadow-sm">
          <svg class="w-10 h-10 text-[#ff9900]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
        </div>
        <h3 class="text-sm font-bold text-slate-800 dark:text-zinc-200">No Target Accounts Configured</h3>
        <p class="text-xs max-w-sm">Add target Instagram, Twitter, or TikTok accounts to begin automatic media archiving.</p>
        <button onclick="window.toggleCmdPalette()" class="mt-2 bg-[#ff9900] hover:bg-[#e68a00] text-black font-bold text-xs px-4 py-2 rounded-lg transition-all shadow-xs cursor-pointer">
          Add First Target (⌘K)
        </button>
      </div>
    `;
    return;
  }

  const totalFiles = state.cachedProgress.reduce((sum, t) => sum + (t.media_count || 0), 0);
  const cd = computeCountdown();
  const countdownText = cd === null ? "Disabled" : (cd.due ? "Running" : `${cd.hours > 0 ? cd.hours + "h " : ""}${cd.minutes}m left`);
  const lastSyncStr = state.lastSyncTime ? new Date(state.lastSyncTime).toLocaleString() : "Never";

  let tableRows = "";
  state.cachedProgress.forEach(target => {
    const isRunning = target.status === "running" || target.status === "queued";
    let statusText = "IDLE";
    let statusClass = "bg-slate-100 dark:bg-zinc-800 text-slate-600 dark:text-zinc-400 border-slate-200 dark:border-zinc-700";
    if (target.status === "running") { statusText = "RUNNING"; statusClass = "bg-amber-500/10 text-[#ff9900] border-[#ff9900]/30 animate-pulse"; }
    else if (target.status === "queued") { statusText = "QUEUED"; statusClass = "bg-amber-500/10 text-amber-500 border-amber-500/30"; }
    else if (target.status === "completed") { statusText = "DONE"; statusClass = "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30"; }
    else if (target.status === "failed") { statusText = "FAILED"; statusClass = "bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/30"; }

    const targetLastSync = target.updated_at && target.updated_at !== "0001-01-01T00:00:00Z"
      ? new Date(target.updated_at).toLocaleString()
      : "Never";

    tableRows += `
      <tr class="hover:bg-slate-100/80 dark:hover:bg-zinc-800/80 transition-colors border-b border-slate-100 dark:border-zinc-800 cursor-pointer group" onclick="window.selectTerminalUser('${target.username}')">
        <td class="px-5 py-3 whitespace-nowrap">
          <div class="flex items-center gap-2.5">
            <span class="text-[10px] font-bold uppercase px-1.5 py-0.5 rounded border ${target.platform === 'instagram' ? 'bg-pink-50 dark:bg-pink-950/40 text-pink-600 dark:text-pink-400 border-pink-200 dark:border-pink-800' : (target.platform === 'twitter' ? 'bg-sky-50 dark:bg-sky-950/40 text-sky-600 dark:text-sky-400 border-sky-200 dark:border-sky-800' : 'bg-slate-100 dark:bg-zinc-800 text-slate-700 dark:text-zinc-300 border-slate-300 dark:border-zinc-700')}">${target.platform}</span>
            <span class="text-xs font-bold text-slate-800 dark:text-zinc-100 group-hover:text-[#ff9900] transition-colors">@${target.username}</span>
          </div>
        </td>
        <td class="px-5 py-3 whitespace-nowrap">
          <span class="text-[9px] font-mono font-bold tracking-wider px-2 py-0.5 rounded border uppercase ${statusClass}">${statusText}</span>
          ${target.auth_error ? '<span class="ml-2 text-[9px] text-rose-500 font-bold">⚠️ Cookie Expired</span>' : ''}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-xs text-slate-500 dark:text-zinc-400 font-mono">${targetLastSync}</td>
        <td class="px-5 py-3 whitespace-nowrap text-xs font-mono font-bold text-slate-700 dark:text-zinc-200">
          <span>${(target.media_count || 0).toLocaleString()}</span>
          ${target.new_count > 0 ? `<span class="ml-1.5 inline-flex items-center text-[10px] font-bold text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/40 px-1.5 py-0.5 rounded-md border border-emerald-200 dark:border-emerald-800/80">+${target.new_count} new</span>` : ''}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-right">
          <button onclick="event.stopPropagation(); window.startSync('${target.username}', false)" class="text-xs font-semibold px-2.5 py-1 rounded-md bg-slate-100 hover:bg-slate-200 dark:bg-zinc-800 dark:hover:bg-zinc-700 text-slate-700 hover:text-slate-900 dark:text-zinc-200 dark:hover:text-white transition-colors cursor-pointer border border-transparent dark:border-zinc-700" ${isRunning ? 'disabled' : ''}>
            ${isRunning ? 'Syncing...' : 'Sync'}
          </button>
        </td>
      </tr>
    `;
  });

  container.innerHTML = `
    <!-- Top Metric Cards -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3.5">
      <div class="bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 p-4 rounded-xl shadow-2xs flex flex-col gap-1">
        <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Configured Targets</span>
        <span class="text-xl font-bold text-slate-900 dark:text-white font-mono">${state.cachedProgress.length}</span>
      </div>
      <div class="bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 p-4 rounded-xl shadow-2xs flex flex-col gap-1">
        <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Archived Files</span>
        <span class="text-xl font-bold text-slate-900 dark:text-white font-mono">${totalFiles.toLocaleString()}</span>
      </div>
      <div class="bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 p-4 rounded-xl shadow-2xs flex flex-col gap-1">
        <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Next Auto-Sync</span>
        <span class="text-xl font-bold text-[#ff9900] font-mono">${countdownText}</span>
      </div>
      <div class="bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 p-4 rounded-xl shadow-2xs flex flex-col gap-1">
        <span class="text-[10px] font-bold text-slate-400 uppercase tracking-wider">Last Global Sync</span>
        <span class="text-xs font-semibold text-slate-600 dark:text-zinc-400 font-mono mt-1">${lastSyncStr}</span>
      </div>
    </div>

    <!-- Targets Overview Table -->
    <div class="bg-white dark:bg-zinc-900 border border-slate-200 dark:border-zinc-800 rounded-xl shadow-2xs overflow-hidden flex flex-col">
      <div class="px-5 py-3.5 border-b border-slate-200 dark:border-zinc-800 flex justify-between items-center bg-slate-50/70 dark:bg-zinc-950/40">
        <h3 class="text-xs font-bold text-slate-900 dark:text-zinc-100 uppercase tracking-wider flex items-center gap-2">
          <svg class="w-3.5 h-3.5 text-[#ff9900]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
          <span>Target Status & Archiver Overview</span>
        </h3>
        <button onclick="window.syncAll()" class="inline-flex items-center gap-1.5 bg-[#ff9900] hover:bg-[#e68a00] text-black font-bold text-xs px-3 py-1.5 rounded-lg transition-all cursor-pointer shadow-xs">
          <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
          <span>Sync All Targets</span>
        </button>
      </div>

      <div class="overflow-x-auto">
        <table class="min-w-full divide-y divide-slate-100 dark:divide-zinc-800 text-left">
          <thead class="bg-slate-50/50 dark:bg-zinc-950/60 text-[9px] font-bold text-slate-400 uppercase tracking-wider font-mono">
            <tr>
              <th scope="col" class="px-5 py-2.5">Target</th>
              <th scope="col" class="px-5 py-2.5">Status</th>
              <th scope="col" class="px-5 py-2.5">Last Updated</th>
              <th scope="col" class="px-5 py-2.5">Files</th>
              <th scope="col" class="px-5 py-2.5 text-right">Action</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-zinc-800/60">
            ${tableRows}
          </tbody>
        </table>
      </div>
    </div>
  `;
}

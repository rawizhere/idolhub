import { state } from "./state.js";
import { onClick } from "./actions.js";
import { toast, escapeHtml, platformBadgeClass, safeUrl, timeAgo } from "./utils.js";
import { startSyncApi, fetchGlobalSearch, fetchRecent } from "./api.js";
import { initVideoHoverPreviews } from "./gallery.js";

let searchAbort = null;
let recentAbort = null;

export function computeCountdown() {
  if (state.autoSyncInterval <= 0 || !state.lastSyncTime) return null;
  const next = new Date(state.lastSyncTime).getTime() + state.autoSyncInterval * 3600000;
  const diff = next - Date.now();
  if (diff <= 0) return { due: true };
  const totalMin = Math.floor(diff / 60000);
  return { hours: Math.floor(totalMin / 60), minutes: totalMin % 60 };
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
  const running = state.cachedProgress.filter(t => t.status === "running" || t.status === "queued").length;
  const dotClass = running > 0 ? "w-2 h-2 rounded-full bg-[#ff9900] animate-pulse" : "w-2 h-2 rounded-full bg-emerald-500";
  dot.className = dotClass;
  if (dockDot) dockDot.className = dotClass;
  text.textContent = running > 0 ? `Syncing: ${running} active` : "Idle";
  badge.classList.toggle("text-[#ff9900]", running > 0);
  badge.classList.toggle("border-[#ff9900]/30", running > 0);
}

async function syncAll() {
  try {
    await startSyncApi("all");
    toast("Sync started for all targets.", "success", 2500);
  } catch (err) {
    toast(`Failed to trigger sync: ${err.message}`, "error");
  }
}

onClick({ "sync-all": syncAll });

function mediaCard(f, { showCaption = true } = {}) {
  const fileUrl = escapeHtml(safeUrl(f.url));
  const thumbUrl = escapeHtml(safeUrl(f.thumbnail_url));
  const isVideo = f.type === "video";
  return `
    <div class="group relative flex flex-col overflow-hidden rounded-xl border border-slate-200 dark:border-zinc-800/80 bg-white dark:bg-zinc-900 shadow-xs hover:border-[#ff9900]/50 transition-colors">
      <a class="gallery-item pswp-item block w-full relative aspect-square overflow-hidden bg-slate-100 dark:bg-zinc-950 video-preview-tile" href="${fileUrl}" title="${escapeHtml(f.filename)}"
         data-filename="${escapeHtml(f.filename)}" data-date="${escapeHtml(f.date)}" data-caption="${escapeHtml(f.caption)}"
         ${isVideo ? `data-pswp-type="video" data-video-src="${fileUrl}"` : ""}>
        <img src="${thumbUrl}" alt="${escapeHtml(f.filename)}" loading="lazy" decoding="async" class="w-full h-full object-cover group-hover:scale-103 transition-transform duration-200 block"/>
        ${isVideo ? `<span class="absolute bottom-2 right-2 text-[0.5625rem] font-mono font-bold bg-black/80 text-white px-1.5 py-0.5 rounded">VIDEO</span>` : ""}
      </a>
      <div class="absolute top-2 inset-x-2 flex items-center justify-between gap-1 pointer-events-none z-20">
        <a href="/?t=${encodeURIComponent(`${f.platform}/${f.username}`)}" data-action="select-target" data-username="${escapeHtml(f.username)}" data-platform="${escapeHtml(f.platform)}" class="pointer-events-auto truncate text-[0.5625rem] font-bold px-1.5 py-0.5 rounded border shadow-xs ${platformBadgeClass(f.platform)}">@${escapeHtml(f.username)}</a>
        ${f.date ? `<span class="text-[0.5625rem] font-mono font-bold bg-black/75 text-white px-1.5 py-0.5 rounded">${escapeHtml(f.date)}</span>` : ""}
      </div>
      ${showCaption && f.caption ? `<p class="p-2 border-t border-slate-100 dark:border-zinc-800/60 text-[0.6875rem] text-slate-600 dark:text-zinc-400 line-clamp-2 leading-relaxed">${escapeHtml(f.caption)}</p>` : ""}
    </div>`;
}

export async function renderGlobalSearchResults(query) {
  const container = document.getElementById("dashboard-empty");
  const q = (query || "").trim();
  if (!container || !q) return;

  container.innerHTML = `
    <div class="flex flex-col gap-4 py-8 items-center justify-center text-center">
      <div class="animate-spin rounded-full h-8 w-8 border-2 border-slate-200 dark:border-zinc-800 border-t-[#ff9900]"></div>
      <p class="text-xs font-semibold text-slate-500 dark:text-zinc-400">Searching the library for “${escapeHtml(q)}”…</p>
    </div>`;

  if (searchAbort) searchAbort.abort();
  searchAbort = new AbortController();
  try {
    const data = await fetchGlobalSearch(q, searchAbort.signal);
    const files = data.files || [];
    if (files.length === 0) {
      container.innerHTML = `
        <div class="flex flex-col items-center justify-center py-20 gap-3 text-slate-400 dark:text-zinc-500 text-center">
          <h3 class="text-sm font-bold text-slate-800 dark:text-zinc-200">Nothing matches “${escapeHtml(q)}”</h3>
          <p class="text-xs max-w-sm">${q.length < 2 ? "Type at least two characters." : "No file names, dates, captions or hashtags matched across your targets."}</p>
          <button data-action="clear-search" class="btn-link mt-2">Clear search</button>
        </div>`;
      return;
    }
    const total = data.total_files || files.length;
    container.innerHTML = `
      <div class="flex flex-col gap-4">
        <div class="card flex justify-between items-center p-3.5">
          <div>
            <h2 class="text-xs font-bold uppercase tracking-wider">Search results</h2>
            <p class="text-[0.6875rem] text-slate-500 dark:text-zinc-400">${total > files.length ? `Showing newest <strong>${files.length}</strong> of ${total.toLocaleString()}` : `<strong>${files.length}</strong> files`} matching “<span class="text-[#ff9900] font-semibold">${escapeHtml(q)}</span>”</p>
          </div>
          <button data-action="clear-search" class="text-xs font-bold text-rose-600 dark:text-rose-400 hover:underline px-3 py-1.5 cursor-pointer">Clear</button>
        </div>
        <div id="global-search-grid" class="pswp-gallery grid grid-cols-[repeat(auto-fill,minmax(min(10rem,42vw),1fr))] gap-3.5">
          ${files.map(f => mediaCard(f)).join("")}
        </div>
      </div>`;
    initVideoHoverPreviews(container);
  } catch (err) {
    if (err.name === "AbortError") return;
    container.innerHTML = `<div class="py-12 text-center text-xs font-semibold text-rose-500">Search failed: ${escapeHtml(err.message)}</div>`;
  }
}

const PLATFORM_NAMES = { twitter: "Twitter / X", instagram: "Instagram", tiktok: "TikTok" };

// cookieHealth summarises a platform's saved session for the overview; platforms without targets are skipped.
export function cookieHealth(platform) {
  return cookieHealthFor(platform, state.cookieStatus?.[platform]);
}

export function cookieHealthFor(platform, st) {
  if (!st) return null;
  if (!st.set) return { tone: "muted", text: "No cookies saved" };
  if (st.error) return { tone: "bad", text: "Unreadable cookie file" };
  if (st.missing?.length) return { tone: "bad", text: `Missing ${st.missing.join(", ")}` };
  if (st.expired) return { tone: "bad", text: `Expired ${new Date(st.expires).toLocaleDateString()}` };
  if (st.expires) {
    const days = Math.floor((new Date(st.expires) - Date.now()) / 86400000);
    return { tone: days < 14 ? "warn" : "ok", text: `Valid until ${new Date(st.expires).toLocaleDateString()}` };
  }
  return { tone: "ok", text: `${st.count} cookies saved` };
}

function healthRow() {
  const platforms = [...new Set(state.cachedProgress.map(t => t.platform))].filter(p => PLATFORM_NAMES[p]);
  if (!platforms.length) return "";
  const tones = {
    ok: "text-emerald-600 dark:text-emerald-400",
    warn: "text-amber-600 dark:text-amber-400",
    bad: "text-rose-600 dark:text-rose-400",
    muted: "text-slate-500 dark:text-zinc-400"
  };
  const cells = platforms.map(p => {
    const h = cookieHealth(p) || { tone: "muted", text: "Loading…" };
    const authErrors = state.cachedProgress.filter(t => t.platform === p && t.auth_error).length;
    const tone = authErrors ? "bad" : h.tone;
    const text = authErrors ? `${authErrors} target${authErrors > 1 ? "s" : ""} rejected — refresh cookies` : h.text;
    return `
      <button data-action="fix-cookies" data-platform="${p}" class="card flex items-center justify-between gap-3 p-3 text-left hover:border-[#ff9900]/50 cursor-pointer">
        <div class="flex flex-col gap-0.5 min-w-0">
          <span class="text-[0.625rem] font-bold uppercase tracking-wider text-slate-400">${PLATFORM_NAMES[p]} session</span>
          <span class="text-xs font-semibold truncate ${tones[tone]}">${escapeHtml(text)}</span>
        </div>
        <span class="text-[0.6875rem] font-semibold text-[#ff9900] whitespace-nowrap">${tone === "ok" ? "Manage" : "Fix"} →</span>
      </button>`;
  }).join("");
  return `<div class="grid grid-cols-1 sm:grid-cols-3 gap-3.5">${cells}</div>`;
}

async function loadRecentStrip() {
  const grid = document.getElementById("recent-grid");
  if (!grid) return;
  if (recentAbort) recentAbort.abort();
  recentAbort = new AbortController();
  try {
    const { files } = await fetchRecent(12, recentAbort.signal);
    if (!grid.isConnected) return;
    const section = document.getElementById("recent-section");
    if (!files.length) {
      section?.classList.add("hidden");
      return;
    }
    grid.innerHTML = files.map(f => mediaCard(f, { showCaption: false })).join("");
    initVideoHoverPreviews(grid);
  } catch (err) {
    if (err.name !== "AbortError") document.getElementById("recent-section")?.classList.add("hidden");
  }
}

export function renderOverviewDashboard() {
  const container = document.getElementById("dashboard-empty");
  if (!container || state.target || state.query) return;

  if (state.cachedProgress.length === 0) {
    container.innerHTML = `
      <div class="flex-1 flex flex-col items-center justify-center py-24 gap-4 text-slate-400 dark:text-zinc-500 text-center">
        <h3 class="text-sm font-bold text-slate-800 dark:text-zinc-200">No Target Accounts Configured</h3>
        <p class="text-xs max-w-sm">Add Instagram, Twitter, or TikTok accounts to begin automatic media archiving.</p>
        <button data-action="open-add" class="btn btn-primary mt-2">Add First Target</button>
      </div>`;
    return;
  }

  const totalFiles = state.cachedProgress.reduce((sum, t) => sum + (t.media_count || 0), 0);
  const newFiles = state.cachedProgress.reduce((sum, t) => sum + (t.new_count || 0), 0);
  const failing = state.cachedProgress.filter(t => t.status === "failed" || t.auth_error).length;
  const cd = computeCountdown();
  const countdownText = cd === null ? "Disabled" : (cd.due ? "Running" : `${cd.hours > 0 ? cd.hours + "h " : ""}${cd.minutes}m`);
  const lastSyncStr = state.lastSyncTime && !state.lastSyncTime.startsWith("0001") ? timeAgo(state.lastSyncTime) || "just now" : "Never";

  const statusBadge = (t) => {
    const map = {
      running: ["RUNNING", "bg-amber-500/10 text-[#ff9900] border-[#ff9900]/30"],
      queued: ["QUEUED", "bg-amber-500/10 text-amber-500 border-amber-500/30"],
      completed: ["DONE", "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30"],
      failed: ["FAILED", "bg-rose-500/10 text-rose-600 dark:text-rose-400 border-rose-500/30"]
    };
    const [label, cls] = map[t.status] || ["IDLE", "bg-slate-100 dark:bg-zinc-800 text-slate-600 dark:text-zinc-400 border-slate-200 dark:border-zinc-700"];
    const pct = t.status === "running" ? ` ${t.progress || 0}%` : "";
    const paused = t.paused ? `<span class="ml-1.5 text-[0.5625rem] font-mono font-bold tracking-wider px-2 py-0.5 rounded border bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/30" title="Skipped by auto-sync">PAUSED</span>` : "";
    return `<span class="text-[0.5625rem] font-mono font-bold tracking-wider px-2 py-0.5 rounded border ${cls}">${label}${pct}</span>${paused}`;
  };

  const rows = state.cachedProgress.map(t => {
    const running = t.status === "running" || t.status === "queued";
    const u = escapeHtml(t.username);
    const p = escapeHtml(t.platform);
    return `
      <tr class="hover:bg-slate-100/80 dark:hover:bg-zinc-800/80 transition-colors">
        <td class="px-5 py-3 whitespace-nowrap">
          <a href="/?t=${encodeURIComponent(`${t.platform}/${t.username}`)}" data-action="select-target" data-username="${u}" data-platform="${p}" class="flex items-center gap-2.5 group">
            <span class="text-[0.625rem] font-bold uppercase px-1.5 py-0.5 rounded border ${platformBadgeClass(t.platform)}">${p}</span>
            <span class="text-xs font-bold group-hover:text-[#ff9900] transition-colors">@${u}</span>
          </a>
        </td>
        <td class="px-5 py-3 whitespace-nowrap">
          ${statusBadge(t)}
          ${t.auth_error ? `<button data-action="fix-cookies" data-platform="${p}" class="ml-2 text-[0.625rem] text-rose-500 font-bold hover:underline cursor-pointer">Cookies rejected — fix</button>` : ""}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-xs text-slate-500 dark:text-zinc-400 font-mono">${timeAgo(t.updated_at) || "Never"}</td>
        <td class="px-5 py-3 whitespace-nowrap text-xs font-mono font-bold">
          ${(t.media_count || 0).toLocaleString()}
          ${t.new_count > 0 ? `<span class="ml-1.5 text-[0.625rem] text-emerald-600 dark:text-emerald-400">+${t.new_count} new</span>` : ""}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-right">
          <button data-action="quick-sync" data-username="${u}" data-platform="${p}" class="btn btn-secondary py-1" ${running ? "disabled" : ""}>${running ? "Syncing…" : "Sync"}</button>
        </td>
      </tr>`;
  }).join("");

  const metric = (label, value, cls = "text-xl font-bold") => `
    <div class="card p-4 flex flex-col gap-1">
      <span class="text-[0.625rem] font-bold text-slate-400 uppercase tracking-wider">${label}</span>
      <span class="${cls} font-mono">${value}</span>
    </div>`;

  container.innerHTML = `
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
      ${metric("Targets", `${state.cachedProgress.length}${failing ? ` <span class="text-xs text-rose-500">${failing} failing</span>` : ""}`)}
      ${metric("Archived files", `${totalFiles.toLocaleString()}${newFiles ? ` <span class="text-xs text-emerald-500">+${newFiles.toLocaleString()}</span>` : ""}`)}
      ${metric("Next auto-sync", countdownText, "text-xl font-bold text-[#ff9900]")}
      ${metric("Last global sync", lastSyncStr, "text-sm font-semibold text-slate-600 dark:text-zinc-400 mt-1")}
    </div>

    ${healthRow()}

    <section id="recent-section" class="flex flex-col gap-3">
      <h3 class="text-xs font-bold uppercase tracking-wider">Recently downloaded</h3>
      <div id="recent-grid" class="pswp-gallery grid grid-cols-[repeat(auto-fill,minmax(min(8rem,28vw),1fr))] gap-3">
        ${Array.from({ length: 6 }, () => '<div class="aspect-square rounded-xl bg-slate-200/60 dark:bg-zinc-900 animate-pulse"></div>').join("")}
      </div>
    </section>

    <div class="card overflow-hidden flex flex-col">
      <div class="px-5 py-3.5 border-b border-slate-200 dark:border-zinc-800 flex justify-between items-center gap-3">
        <h3 class="text-xs font-bold uppercase tracking-wider">Targets</h3>
        <button data-action="sync-all" class="btn btn-primary">Sync All</button>
      </div>
      <div class="overflow-x-auto">
        <table class="min-w-full text-left">
          <thead class="text-[0.5625rem] font-bold text-slate-400 uppercase tracking-wider font-mono">
            <tr><th class="px-5 py-2.5">Target</th><th class="px-5 py-2.5">Status</th><th class="px-5 py-2.5">Updated</th><th class="px-5 py-2.5">Files</th><th class="px-5 py-2.5 text-right">Action</th></tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-zinc-800/60">${rows}</tbody>
        </table>
      </div>
    </div>`;
  loadRecentStrip();
}

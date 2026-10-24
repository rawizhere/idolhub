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

const PLATFORM_DOTS = { instagram: "bg-pink-400", twitter: "bg-sky-400", tiktok: "bg-zinc-200" };

// mediaCard is a tile from any target: the @handle, the one thing a tile can't show otherwise, gets the full width.
function mediaCard(f, { showCaption = true } = {}) {
  const fileUrl = escapeHtml(safeUrl(f.url));
  const thumbUrl = escapeHtml(safeUrl(f.thumbnail_url));
  const isVideo = f.type === "video";
  const captioned = showCaption && f.caption;
  return `
    <div class="group relative flex flex-col overflow-hidden rounded-xl border border-slate-200 dark:border-zinc-800/80 bg-white dark:bg-zinc-900 shadow-xs hover:border-[#ff9900]/50 transition-colors">
      <a class="gallery-item pswp-item block w-full relative aspect-square overflow-hidden bg-slate-100 dark:bg-zinc-950 video-preview-tile" href="${fileUrl}" title="${escapeHtml(f.filename)}"
         data-filename="${escapeHtml(f.filename)}" data-date="${escapeHtml(f.date)}" data-caption="${escapeHtml(f.caption)}"
         ${isVideo ? `data-pswp-type="video" data-video-src="${fileUrl}"` : ""}>
        <img src="${thumbUrl}" alt="${escapeHtml(f.filename)}" loading="lazy" decoding="async" class="w-full h-full object-cover group-hover:scale-103 transition-transform duration-200 block"/>
        ${isVideo ? `<span class="absolute ${captioned ? "bottom-2" : "top-2"} right-2 z-20 w-6 h-6 rounded-full bg-black/65 text-white flex items-center justify-center" aria-label="Video"><svg viewBox="0 0 24 24" fill="currentColor" class="w-3 h-3"><path d="M8 5v14l11-7z"></path></svg></span>` : ""}
      </a>
      <a href="/?t=${encodeURIComponent(`${f.platform}/${f.username}`)}" data-action="select-target" data-username="${escapeHtml(f.username)}" data-platform="${escapeHtml(f.platform)}"
         class="absolute inset-x-0 ${captioned ? "top-0 bg-linear-to-b pt-2 pb-5" : "bottom-0 bg-linear-to-t pt-5 pb-2"} from-black/75 to-transparent px-2 z-20 flex items-center gap-1.5 text-xs font-semibold text-white hover:text-[#ff9900]">
        <span class="w-1.5 h-1.5 rounded-full flex-shrink-0 ${PLATFORM_DOTS[f.platform] || PLATFORM_DOTS.tiktok}"></span>
        <span class="truncate">@${escapeHtml(f.username)}</span>
      </a>
      ${captioned ? `<p class="p-2 border-t border-slate-100 dark:border-zinc-800/60 text-xs text-slate-600 dark:text-zinc-400 line-clamp-2 leading-relaxed">${escapeHtml(f.caption)}</p>` : ""}
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
            <p class="text-xs text-slate-500 dark:text-zinc-400">${total > files.length ? `Showing newest <strong>${files.length}</strong> of ${total.toLocaleString()}` : `<strong>${files.length}</strong> files`} matching “<span class="text-[#ff9900] font-semibold">${escapeHtml(q)}</span>”</p>
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

const PLATFORM_NAMES = { twitter: "Twitter / X", instagram: "Instagram", tiktok: "TikTok", youtube: "YouTube" };

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

// sessionPills shows one pill per platform in use: just the name while the session is fine, the problem when it isn't.
function sessionPills() {
  const platforms = [...new Set(state.cachedProgress.map(t => t.platform))].filter(p => PLATFORM_NAMES[p]);
  if (!platforms.length) return "";
  const tones = {
    ok: "bg-emerald-50 text-emerald-700 border-emerald-200 dark:bg-emerald-950/50 dark:text-emerald-300 dark:border-emerald-900",
    warn: "bg-amber-50 text-amber-700 border-amber-200 dark:bg-amber-950/50 dark:text-amber-300 dark:border-amber-900",
    bad: "bg-rose-50 text-rose-700 border-rose-200 dark:bg-rose-950/50 dark:text-rose-300 dark:border-rose-900",
    muted: "bg-slate-100 text-slate-600 border-slate-200 dark:bg-zinc-800 dark:text-zinc-400 dark:border-zinc-700"
  };
  const pills = platforms.map(p => {
    const h = cookieHealth(p) || { tone: "muted", text: "Loading…" };
    const authErrors = state.cachedProgress.filter(t => t.platform === p && t.auth_error).length;
    const tone = authErrors ? "bad" : h.tone;
    const text = authErrors ? `${authErrors} target${authErrors > 1 ? "s" : ""} rejected — refresh cookies` : h.text;
    const label = tone === "ok" ? PLATFORM_NAMES[p] : `${PLATFORM_NAMES[p]} · ${text}`;
    return `<button data-action="fix-cookies" data-platform="${p}" title="${escapeHtml(text)}" class="text-xs font-semibold px-2.5 py-1 rounded-full border cursor-pointer hover:brightness-110 ${tones[tone]}">${escapeHtml(label)}</button>`;
  }).join("");
  return `<div class="flex items-center gap-2 flex-wrap"><span class="text-xs font-semibold text-slate-500 dark:text-zinc-400">Sessions</span>${pills}</div>`;
}

async function loadRecentStrip() {
  const grid = document.getElementById("recent-grid");
  if (!grid) return;
  if (recentAbort) recentAbort.abort();
  recentAbort = new AbortController();
  try {
    const { files } = await fetchRecent(24, recentAbort.signal);
    if (!grid.isConnected) return;
    const section = document.getElementById("recent-section");
    if (!files.length) {
      section?.classList.add("hidden");
      return;
    }
    grid.innerHTML = files.map(f => mediaCard(f, { showCaption: false })).join("");
    // two full rows, never a ragged third: the column count follows the window width
    const cols = getComputedStyle(grid).gridTemplateColumns.split(" ").length;
    [...grid.children].slice(cols * 2).forEach(el => el.remove());
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
    const paused = t.paused ? `<span class="ml-1.5 text-xs font-mono font-bold tracking-wider px-2 py-0.5 rounded border bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/30" title="Skipped by auto-sync">PAUSED</span>` : "";
    return `<span class="text-xs font-mono font-bold tracking-wider px-2 py-0.5 rounded border ${cls}">${label}${pct}</span>${paused}`;
  };

  const rows = state.cachedProgress.map(t => {
    const running = t.status === "running" || t.status === "queued";
    const u = escapeHtml(t.username);
    const p = escapeHtml(t.platform);
    return `
      <tr class="hover:bg-slate-100/80 dark:hover:bg-zinc-800/80 transition-colors">
        <td class="px-5 py-3 whitespace-nowrap">
          <a href="/?t=${encodeURIComponent(`${t.platform}/${t.username}`)}" data-action="select-target" data-username="${u}" data-platform="${p}" class="flex items-center gap-2.5 group">
            <span class="text-xs font-bold uppercase px-1.5 py-0.5 rounded border ${platformBadgeClass(t.platform)}">${p}</span>
            <span class="text-xs font-bold group-hover:text-[#ff9900] transition-colors">@${u}</span>
          </a>
        </td>
        <td class="px-5 py-3 whitespace-nowrap">
          ${statusBadge(t)}
          ${t.auth_error ? `<button data-action="fix-cookies" data-platform="${p}" class="ml-2 text-xs text-rose-500 font-bold hover:underline cursor-pointer">Cookies rejected — fix</button>` : ""}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-xs text-slate-500 dark:text-zinc-400 font-mono">${timeAgo(t.updated_at) || "Never"}</td>
        <td class="px-5 py-3 whitespace-nowrap text-xs font-mono font-bold">
          ${(t.media_count || 0).toLocaleString()}
          ${t.new_count > 0 ? `<span class="ml-1.5 text-xs text-emerald-600 dark:text-emerald-400">+${t.new_count} new</span>` : ""}
        </td>
        <td class="px-5 py-3 whitespace-nowrap text-right">
          <button data-action="quick-sync" data-username="${u}" data-platform="${p}" class="btn btn-secondary py-1" ${running ? "disabled" : ""}>${running ? "Syncing…" : "Sync"}</button>
        </td>
      </tr>`;
  }).join("");

  const stat = (value, label) => `<span><span class="text-xl font-semibold font-mono text-slate-900 dark:text-zinc-100">${value}</span> <span class="text-slate-500 dark:text-zinc-400">${label}</span></span>`;

  container.innerHTML = `
    <section aria-label="Library status" class="card px-4 py-3 flex items-center justify-between gap-4 flex-wrap text-sm">
      <div class="flex items-baseline gap-x-7 gap-y-1 flex-wrap">
        ${stat(state.cachedProgress.length, "targets")}
        ${failing ? `<span class="font-semibold text-rose-600 dark:text-rose-400">${failing} failing</span>` : ""}
        ${stat(totalFiles.toLocaleString(), "files")}
        ${newFiles ? `<span class="font-semibold text-emerald-600 dark:text-emerald-400">+${newFiles.toLocaleString()} new</span>` : ""}
        <span class="text-slate-500 dark:text-zinc-400">Last sync <span class="font-semibold text-slate-900 dark:text-zinc-100">${lastSyncStr}</span></span>
        <span class="text-slate-500 dark:text-zinc-400">Next auto-sync <span class="font-semibold text-slate-900 dark:text-zinc-100">${cd === null || cd.due ? countdownText : `in ${countdownText}`}</span></span>
      </div>
      ${sessionPills()}
    </section>

    <section id="recent-section" class="flex flex-col gap-3">
      <h3 class="text-sm font-bold">Recently downloaded</h3>
      <div id="recent-grid" class="pswp-gallery grid grid-cols-[repeat(auto-fill,minmax(min(8rem,28vw),1fr))] gap-3">
        ${Array.from({ length: 6 }, () => '<div class="aspect-square rounded-xl bg-slate-200/60 dark:bg-zinc-900 animate-pulse"></div>').join("")}
      </div>
    </section>

    <div class="card overflow-hidden flex flex-col">
      <div class="px-5 py-3.5 border-b border-slate-200 dark:border-zinc-800 flex justify-between items-center gap-3">
        <h3 class="text-sm font-bold">Targets</h3>
        <button data-action="sync-all" class="btn btn-primary">Sync All</button>
      </div>
      <div class="overflow-x-auto">
        <table class="min-w-full text-left">
          <thead class="text-xs font-semibold text-slate-500 dark:text-zinc-400">
            <tr><th class="px-5 py-2.5">Target</th><th class="px-5 py-2.5">Status</th><th class="px-5 py-2.5">Updated</th><th class="px-5 py-2.5">Files</th><th class="px-5 py-2.5 text-right">Action</th></tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-zinc-800/60">${rows}</tbody>
        </table>
      </div>
    </div>`;
  loadRecentStrip();
}

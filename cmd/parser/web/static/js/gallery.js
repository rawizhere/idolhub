import { state, targetKey, filtersDirty } from "./state.js";
import { onClick } from "./actions.js";
import { toast, escapeHtml, safeUrl, confirmDialog, withLoading } from "./utils.js";
import { fetchGalleryMeta, fetchHTML, deleteMediaApi } from "./api.js";
import { syncUrl, slideFromHash, setSlideHash } from "./router.js";
import { loadProgress } from "./sse.js";
import { renderTargetSettings, confirmLeave } from "./target-settings.js";
import PhotoSwipeLightbox from "/static/vendor/photoswipe-lightbox.esm.min.js";

const $ = (id) => document.getElementById(id);
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

let metaAbort = null;
let pageAbort = null;
// bumped on every full re-render so late infinite-scroll responses for an old filter set are dropped
let generation = 0;

/* ---------- target + rendering ---------- */

// showTarget switches the gallery to a target. route carries filters from the URL; without it a new target starts unfiltered.
export async function showTarget(target, route = null) {
  const changed = !state.target || targetKey(state.target) !== targetKey(target);
  state.target = { platform: target.platform, username: target.username };

  if (route) {
    state.view = route.view;
    state.filter = route.filter;
    state.query = route.query;
    state.sortAsc = route.sortAsc;
    state.years = new Set(route.years);
    state.months = new Set(route.months);
    state.tags = new Set(route.tags);
  } else if (changed) {
    resetFilterState();
  }

  if (changed) {
    setSelecting(false);
    state.galleryMeta = null;
    showGalleryState("loading");
    if (metaAbort) metaAbort.abort();
    metaAbort = new AbortController();
    try {
      state.galleryMeta = await fetchGalleryMeta(target.platform, target.username, metaAbort.signal);
    } catch (err) {
      if (err.name === "AbortError") return;
      toast(`Failed to load gallery: ${err.message}`, "error");
      state.galleryMeta = { file_count: 0, post_count: 0, years: [], hashtags: [] };
    }
  }

  const meta = state.galleryMeta;
  if (!meta) return;
  if (state.view === "posts" && !meta.post_count) state.view = "grid";
  syncFilterUI();
  if (state.view === "settings") {
    showGalleryState("settings");
    await renderTargetSettings();
    return;
  }
  if (!meta.file_count && !meta.post_count) {
    showGalleryState("empty", "Nothing downloaded for this target yet — run a sync.");
    return;
  }
  await renderGallery();
}

export async function refreshMeta() {
  if (!state.target) return;
  try {
    state.galleryMeta = await fetchGalleryMeta(state.target.platform, state.target.username);
    syncFilterUI();
  } catch (_) {}
}

function resetFilterState() {
  state.view = "grid";
  state.filter = "all";
  state.query = "";
  state.sortAsc = false;
  state.years = new Set();
  state.months = new Set();
  state.tags = new Set();
}

function showGalleryState(view, emptyText) {
  const grid = $("lg-container");
  const posts = $("gallery-posts-list");
  const show = (el, on, display = "flex") => {
    el.classList.toggle("hidden", !on);
    if (display === "flex") el.classList.toggle("flex", on);
  };
  show($("target-settings"), view === "settings");
  show($("gallery-loading"), view === "loading");
  show($("gallery-empty"), view === "empty");
  if (emptyText) $("gallery-empty-text").textContent = emptyText;
  grid.classList.toggle("hidden", !(view === "content" && state.view === "grid"));
  show(posts, view === "content" && state.view === "posts");
}

function pageUrl() {
  const t = state.target;
  const p = new URLSearchParams({
    sort: state.sortAsc ? "asc" : "desc",
    q: state.query,
    year: state.years.size ? [...state.years].join(",") : "all",
    month: state.months.size ? [...state.months].join(",") : "all",
    tags: state.tags.size ? [...state.tags].join(",") : "all"
  });
  const base = `/gallery/${encodeURIComponent(t.platform)}/${encodeURIComponent(t.username)}`;
  if (state.view === "posts") return `${base}/posts/page/1?${p}`;
  p.set("filter", state.filter);
  return `${base}?${p}`;
}

export async function renderGallery() {
  if (!state.target || !state.galleryMeta || state.view === "settings") return;
  generation++;
  const container = state.view === "grid" ? $("lg-container") : $("gallery-posts-list");
  if (pageAbort) pageAbort.abort();
  pageAbort = new AbortController();
  const gen = generation;
  try {
    const html = await fetchHTML(pageUrl(), pageAbort.signal);
    if (gen !== generation) return;
    container.innerHTML = html;
    const hasItems = container.querySelector(".gallery-item, .post-card");
    if (!hasItems) {
      showGalleryState("empty", "No media matches your filters.");
      return;
    }
    showGalleryState("content");
    afterInsert(container);
    openSlideFromHash();
  } catch (err) {
    if (err.name === "AbortError") return;
    container.innerHTML = "";
    showGalleryState("empty", "Failed to load gallery.");
    toast(`Failed to load gallery: ${err.message}`, "error");
  }
}

const dayLabel = new Intl.DateTimeFormat("en-US", { year: "numeric", month: "long", day: "numeric", timeZone: "UTC" });

// labelDays heads each run of same-day tiles with its date, which tiles no longer carry. Idempotent, so it runs after
// every page and every delete; a page that continues the previous day gets no second header.
function labelDays(grid) {
  if (!state.groupByDay) {
    grid.querySelectorAll(".day-header").forEach(h => h.remove());
    return;
  }
  grid.querySelectorAll(".day-header").forEach(h => {
    if (!h.nextElementSibling?.classList.contains("gallery-item")) h.remove();
  });
  let prev = null;
  grid.querySelectorAll(".gallery-item").forEach(item => {
    const date = item.dataset.date || "";
    if (date !== prev && !item.previousElementSibling?.classList.contains("day-header")) {
      const d = new Date(`${date}T00:00:00Z`);
      const h = document.createElement("h3");
      h.className = "day-header col-span-full text-sm font-bold pt-3 pb-1.5 border-b border-slate-200 dark:border-zinc-800 first:pt-0";
      h.textContent = isNaN(d) ? "Undated" : dayLabel.format(d);
      item.before(h);
    }
    prev = date;
  });
}

function afterInsert(container) {
  if (container.id === "lg-container") labelDays(container);
  container.querySelectorAll("[data-next-url]").forEach(s => scrollObserver.observe(s));
  initVideoHoverPreviews(container);
  if (state.selecting) markSelected(container);
}

/* ---------- infinite scroll ---------- */

const scrollObserver = new IntersectionObserver((entries) => {
  entries.forEach(e => { if (e.isIntersecting) loadNext(e.target); });
}, { rootMargin: "800px 0px" });

async function loadNext(sentinel) {
  if (!sentinel || sentinel.dataset.loading === "1") return;
  sentinel.dataset.loading = "1";
  scrollObserver.unobserve(sentinel);
  const gen = generation;
  try {
    const html = await fetchHTML(sentinel.dataset.nextUrl);
    if (gen !== generation || !sentinel.isConnected) return;
    const parent = sentinel.parentElement;
    const tpl = document.createElement("template");
    tpl.innerHTML = html;
    sentinel.replaceWith(tpl.content);
    afterInsert(parent);
  } catch (err) {
    delete sentinel.dataset.loading;
    toast(`Failed to load more: ${err.message}`, "error");
  }
}

/* ---------- filter UI ---------- */

function setPressed(el, on) {
  if (el) el.setAttribute("aria-pressed", on ? "true" : "false");
}

export function syncFilterUI() {
  const meta = state.galleryMeta || {};
  const isGrid = state.view === "grid";

  document.querySelectorAll("#gallery-view-tabs [data-view]").forEach(b => setPressed(b, b.dataset.view === state.view));
  $("tab-posts").classList.toggle("hidden", !meta.post_count);

  document.querySelectorAll("[data-gallery-only]").forEach(el => el.classList.toggle("hidden", state.view === "settings"));
  const typePills = $("media-type-pills");
  typePills.classList.toggle("hidden", !isGrid);
  typePills.querySelectorAll("[data-filter]").forEach(b => setPressed(b, b.dataset.filter === state.filter));

  // dates
  const dateCount = state.years.size + state.months.size;
  $("date-filter-label").textContent = dateCount ? `Dates (${dateCount})` : "Dates";
  setPressed($("btn-date-filter"), dateCount > 0);
  const years = meta.years || [];
  $("year-dropdown-list").innerHTML = years.length
    ? years.map(y => `<button type="button" class="pill" data-action="toggle-year" data-value="${escapeHtml(y)}" aria-pressed="${state.years.has(y)}">${escapeHtml(y)}</button>`).join("")
    : `<span class="text-xs text-slate-400">No dated media</span>`;
  $("month-dropdown-list").innerHTML = MONTHS.map((name, i) => {
    const v = String(i + 1).padStart(2, "0");
    return `<button type="button" class="pill justify-center" data-action="toggle-month" data-value="${v}" aria-pressed="${state.months.has(v)}">${name}</button>`;
  }).join("");

  // hashtags
  const tags = meta.hashtags || [];
  const tagBtn = $("btn-hashtag-filter");
  tagBtn.classList.toggle("hidden", tags.length === 0);
  $("hashtag-filter-label").textContent = state.tags.size ? `#tags (${state.tags.size})` : "#tags";
  setPressed(tagBtn, state.tags.size > 0);
  $("hashtag-dropdown").innerHTML = tags.map(t => `
    <button type="button" class="menu-item ${state.tags.has(t.tag) ? "bg-[#ff9900]/15" : ""}" data-action="toggle-tag" data-value="${escapeHtml(t.tag)}" aria-pressed="${state.tags.has(t.tag)}">
      <span class="w-3.5 h-3.5 rounded border flex items-center justify-center text-xs ${state.tags.has(t.tag) ? "bg-[#ff9900] border-[#ff9900] text-black" : "border-slate-300 dark:border-zinc-600"}">${state.tags.has(t.tag) ? "✓" : ""}</span>
      <span class="truncate flex-1">${escapeHtml(t.tag)}</span>
      <span class="text-xs bg-slate-100 dark:bg-zinc-800 text-slate-500 px-1.5 rounded-full">${t.count}</span>
    </button>`).join("");

  // sort
  $("sort-order-label").textContent = state.sortAsc ? "Oldest" : "Newest";
  $("sort-order-icon").style.transform = state.sortAsc ? "rotate(180deg)" : "";
  setPressed($("btn-sort-toggle"), state.sortAsc);

  $("btn-select-mode").classList.toggle("hidden", !isGrid);
  setPressed($("btn-select-mode"), state.selecting);
  setPressed($("btn-density-toggle"), state.gridDensity === "compact");
  setPressed($("btn-day-groups"), state.groupByDay);
  document.body.classList.toggle("show-tile-dates", !state.groupByDay);
  $("btn-reset-filters").classList.toggle("hidden", !filtersDirty());

  const grid = $("lg-container");
  grid.classList.toggle("density-compact", state.gridDensity === "compact");
  grid.classList.toggle("density-relaxed", state.gridDensity !== "compact");
}

// filtersChanged re-renders after any filter tweak and records it in the URL without a history entry.
function filtersChanged() {
  syncFilterUI();
  syncUrl();
  renderGallery();
}

export async function setView(view, { push = true } = {}) {
  if (view === state.view || (view === "posts" && !state.galleryMeta?.post_count)) return;
  if (state.view === "settings" && !(await confirmLeave())) return;
  state.view = view;
  if (view !== "grid") setSelecting(false);
  syncFilterUI();
  syncUrl({ push });
  if (view === "settings") {
    showGalleryState("settings");
    renderTargetSettings();
    return;
  }
  showGalleryState("loading");
  renderGallery();
}

export function setQuery(q) {
  state.query = q;
  filtersChanged();
}

function toggleIn(set, value) {
  if (set.has(value)) set.delete(value);
  else set.add(value);
}

export function resetFilters() {
  const view = state.view;
  resetFilterState();
  state.view = view;
  const input = $("search-input");
  if (input) input.value = "";
  $("search-clear")?.classList.add("hidden");
  filtersChanged();
}

onClick({
  "view": (el) => {
    document.querySelectorAll("[popover]:popover-open").forEach(p => p.hidePopover());
    setView(el.dataset.view);
  },
  "type-filter": (el) => { state.filter = el.dataset.filter; filtersChanged(); },
  "toggle-sort": () => { state.sortAsc = !state.sortAsc; filtersChanged(); },
  "toggle-year": (el) => { toggleIn(state.years, el.dataset.value); filtersChanged(); },
  "toggle-month": (el) => { toggleIn(state.months, el.dataset.value); filtersChanged(); },
  "toggle-tag": (el) => { toggleIn(state.tags, el.dataset.value); filtersChanged(); },
  "clear-dates": () => { state.years.clear(); state.months.clear(); filtersChanged(); },
  "reset-filters": resetFilters,
  "toggle-density": () => {
    state.gridDensity = state.gridDensity === "relaxed" ? "compact" : "relaxed";
    try { localStorage.setItem("idolhub-density", state.gridDensity); } catch (_) {}
    syncFilterUI();
  },
  "toggle-day-groups": () => {
    state.groupByDay = !state.groupByDay;
    try { localStorage.setItem("idolhub-day-groups", state.groupByDay ? "on" : "off"); } catch (_) {}
    syncFilterUI();
    const grid = $("lg-container");
    if (grid) labelDays(grid);
  },
  "load-more": (el) => loadNext(el.closest("[data-next-url]")),
  "youtube-play": (btn) => {
    const id = btn.dataset.youtubeId || "";
    if (!/^[A-Za-z0-9_-]{11}$/.test(id)) return;
    const frame = document.createElement("iframe");
    frame.className = "absolute inset-0 w-full h-full";
    frame.src = `https://www.youtube-nocookie.com/embed/${id}?autoplay=1&rel=0`;
    frame.title = "YouTube video player";
    frame.allow = "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share";
    frame.allowFullscreen = true;
    btn.replaceChildren(frame);
    delete btn.dataset.action;
  },
  "toggle-select": () => setSelecting(!state.selecting),
  "select-all": () => {
    $("lg-container").querySelectorAll(".gallery-item").forEach(i => state.selected.add(i.dataset.filename));
    markSelected($("lg-container"));
  },
  "download-selected": downloadSelected,
  "delete-selected": (el) => withLoading(el, deleteSelected)
});

/* ---------- selection ---------- */

let anchorItem = null;

export function setSelecting(on) {
  state.selecting = on;
  if (on) stopHoverPreview(activePreview);
  if (!on) {
    state.selected.clear();
    anchorItem = null;
  }
  $("lg-container").classList.toggle("selecting", on);
  markSelected($("lg-container"));
  setPressed($("btn-select-mode"), on);
}

function markSelected(container) {
  container.querySelectorAll(".gallery-item").forEach(i => i.classList.toggle("is-selected", state.selected.has(i.dataset.filename)));
  const bar = $("selection-bar");
  bar.classList.toggle("hidden", !state.selecting);
  bar.classList.toggle("flex", state.selecting);
  $("selection-count").textContent = state.selected.size;
}

function selectItem(item, range) {
  const items = [...$("lg-container").querySelectorAll(".gallery-item")];
  if (range && anchorItem && anchorItem.isConnected) {
    const [a, b] = [items.indexOf(anchorItem), items.indexOf(item)].sort((x, y) => x - y);
    items.slice(a, b + 1).forEach(i => state.selected.add(i.dataset.filename));
  } else {
    toggleIn(state.selected, item.dataset.filename);
    anchorItem = item;
  }
  markSelected($("lg-container"));
}

function downloadSelected() {
  if (!state.selected.size) { toast("Select some files first", "info"); return; }
  const form = document.createElement("form");
  form.method = "POST";
  form.action = "/api/media/zip";
  form.hidden = true;
  const add = (name, value) => {
    const input = document.createElement("input");
    input.type = "hidden";
    input.name = name;
    input.value = value;
    form.appendChild(input);
  };
  add("platform", state.target.platform);
  add("username", state.target.username);
  state.selected.forEach(f => add("file", f));
  document.body.appendChild(form);
  form.submit();
  form.remove();
  toast(`Preparing ZIP of ${state.selected.size} files…`, "info", 2500);
}

async function deleteSelected() {
  const files = [...state.selected];
  if (!files.length) { toast("Select some files first", "info"); return; }
  const ok = await confirmDialog({
    title: `Delete ${files.length} file${files.length === 1 ? "" : "s"}`,
    message: "The selected files and their thumbnails are removed from disk. A full resync may download them again.",
    confirmText: "Delete files",
    tone: "danger"
  });
  if (!ok) return;
  try {
    const res = await deleteMediaApi(state.target.platform, state.target.username, files);
    const gone = new Set(files);
    $("lg-container").querySelectorAll(".gallery-item").forEach(i => { if (gone.has(i.dataset.filename)) i.remove(); });
    labelDays($("lg-container"));
    setSelecting(false);
    toast(`Deleted ${res.deleted} files`, "success");
    refreshMeta();
    loadProgress();
  } catch (err) {
    toast(`Failed to delete: ${err.message}`, "error");
  }
}

/* ---------- lightbox ---------- */

const ICONS = {
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>',
  source: '<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>'
};
const toolbarIcon = (name) => `<svg viewBox="0 0 24 24" width="22" height="22" style="fill:none;stroke:#fff;stroke-width:2;stroke-linecap:round;stroke-linejoin:round">${ICONS[name]}</svg>`;

const lightbox = new PhotoSwipeLightbox({
  children: "a.pswp-item",
  showHideAnimationType: "none",
  bgOpacity: 0.92,
  pswpModule: () => import("/static/vendor/photoswipe.esm.min.js")
});

function addItemData(lb) {
  lb.addFilter("domItemData", (itemData, element, linkEl) => {
    const el = linkEl || element;
    const img = el.querySelector("img");
    if (img) {
      itemData.msrc = img.src;
      if (img.naturalWidth && img.naturalHeight) {
        itemData.w = img.naturalWidth * 2;
        itemData.h = img.naturalHeight * 2;
      }
    }
    if (!itemData.w || !itemData.h) {
      itemData.w = 1920;
      itemData.h = 1080;
    }
    itemData.filename = el.dataset.filename || "";
    itemData.caption = el.dataset.caption || "";
    itemData.date = el.dataset.date || "";
    itemData.source = safeUrl(el.dataset.source);
    return itemData;
  });

  lb.addFilter("itemData", (itemData) => {
    if (itemData.type === "video" && itemData.src) {
      itemData.w = 1920;
      itemData.h = 1080;
      const src = escapeHtml(itemData.src);
      itemData.html = `
        <div class="pswp-video-container" style="display:flex;flex-direction:column;align-items:center;justify-content:center;width:100%;height:100%;padding:20px;box-sizing:border-box;pointer-events:auto;">
          <video src="${src}" controls playsinline preload="metadata" style="max-width:100%;max-height:100%;width:auto;height:auto;object-fit:contain;border-radius:12px;box-shadow:0 25px 60px rgba(0,0,0,0.6);"></video>
          <div class="video-codec-fallback hidden flex-col items-center justify-center p-6 bg-zinc-900/95 text-white rounded-xl border border-zinc-800 text-center max-w-md gap-3 shadow-2xl">
            <p class="text-xs font-semibold text-zinc-200">Video format not supported by this browser.</p>
            <p class="text-xs text-zinc-400">Download the file and play it in an external player like QuickTime or VLC.</p>
            <a href="${src}" download class="mt-2 bg-[#ff9900] hover:bg-[#e68a00] text-black font-bold text-xs px-4 py-2 rounded-lg">Download video</a>
          </div>
        </div>`;
    }
    return itemData;
  });
}

function addToolbar(lb) {
  lb.on("uiRegister", () => {
    const ui = lb.pswp.ui;
    ui.registerElement({
      name: "download-button",
      title: "Download",
      order: 8,
      isButton: true,
      tagName: "a",
      html: toolbarIcon("download"),
      onInit: (el, pswp) => {
        el.setAttribute("download", "");
        pswp.on("change", () => { el.href = pswp.currSlide.data.src; });
      }
    });
    ui.registerElement({
      name: "source-button",
      title: "Open original post",
      order: 9,
      isButton: true,
      tagName: "a",
      html: toolbarIcon("source"),
      onInit: (el, pswp) => {
        el.target = "_blank";
        el.rel = "noopener noreferrer";
        pswp.on("change", () => {
          const src = pswp.currSlide.data.source;
          el.hidden = !src;
          if (src) el.href = src;
        });
      }
    });
    ui.registerElement({
      name: "caption",
      order: 9,
      isButton: false,
      appendTo: "root",
      onInit: (el, pswp) => {
        pswp.on("change", () => {
          const { caption, date } = pswp.currSlide.data;
          el.replaceChildren();
          if (date) {
            const t = document.createElement("time");
            t.textContent = date;
            el.appendChild(t);
          }
          if (caption) el.appendChild(document.createTextNode(caption));
          el.hidden = !caption && !date;
        });
      }
    });
  });
}

function addVideoHandling(lb) {
  const resize = (content, video) => {
    const vw = video.videoWidth;
    const vh = video.videoHeight;
    if (!vw || !vh || !content) return;
    content.width = vw;
    content.height = vh;
    const slide = content.slide;
    if (!slide) return;
    slide.width = vw;
    slide.height = vh;
    if (slide.data) { slide.data.w = vw; slide.data.h = vh; }
    slide.calculateSize();
    slide.zoomAndPanToInitial();
    slide.applyCurrentZoomPan();
    slide.updateContentSize(true);
  };
  const showFallback = (video) => {
    const fb = video.nextElementSibling;
    if (fb) { fb.classList.remove("hidden"); fb.classList.add("flex"); }
    video.style.display = "none";
  };
  const activate = (content) => {
    const v = content?.element?.querySelector?.("video");
    if (!v) return;
    // the error may already have fired before activation; inline onerror is blocked by the CSP
    if (v.error) showFallback(v);
    else v.addEventListener("error", () => showFallback(v), { once: true });
    if (v.readyState >= 1) resize(content, v);
    else v.addEventListener("loadedmetadata", () => resize(content, v), { once: true });
    v.play().catch(() => {});
  };
  lb.on("contentActivate", (e) => activate(e.content));
  lb.on("contentDeactivate", (e) => e.content?.element?.querySelector?.("video")?.pause());
  lb.on("close", () => lb.pswp?.element?.querySelectorAll("video").forEach(v => v.pause()));
}

function openLightbox(galleryEl, item) {
  // the tile under the cursor may be previewing the same video; it would keep decoding and downloading behind the viewer
  stopHoverPreview(activePreview);
  const idx = [...galleryEl.querySelectorAll("a.pswp-item")].indexOf(item);
  if (idx >= 0) lightbox.loadAndOpen(idx, { gallery: galleryEl });
}

// openSlideFromHash reopens a shared #f=<filename> link once its tile is on the page.
function openSlideFromHash() {
  const name = slideFromHash();
  if (!name) return;
  const container = state.view === "grid" ? $("lg-container") : $("gallery-posts-list");
  const item = [...container.querySelectorAll("a.pswp-item")].find(a => a.dataset.filename === name);
  if (item) openLightbox(container, item);
}

addItemData(lightbox);
addToolbar(lightbox);
addVideoHandling(lightbox);
lightbox.on("change", () => setSlideHash(lightbox.pswp?.currSlide?.data?.filename || ""));
lightbox.on("close", () => setSlideHash(""));
lightbox.init();

document.addEventListener("click", (e) => {
  const item = e.target.closest("a.pswp-item");
  if (!item) return;
  const galleryEl = item.closest(".pswp-gallery");
  if (!galleryEl) return;
  if (galleryEl.id === "lg-container" && (state.selecting || e.shiftKey)) {
    e.preventDefault();
    if (!state.selecting) setSelecting(true);
    selectItem(item, e.shiftKey);
    return;
  }
  // modifier clicks keep the browser's open-in-new-tab behaviour
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.button === 1) return;
  e.preventDefault();
  openLightbox(galleryEl, item);
});

/* ---------- hover previews ---------- */

const hoverPreviews = new WeakMap();
// at most one preview plays: each one streams a whole file and holds a video decoder
let activePreview = null;

function stopHoverPreview(tile) {
  const p = tile && hoverPreviews.get(tile);
  if (!p) return;
  if (activePreview === tile) activePreview = null;
  clearTimeout(p.hoverTimer);
  if (!p.previewEl) return;
  const el = p.previewEl;
  p.previewEl = null;
  el.pause();
  el.classList.add("opacity-0");
  setTimeout(() => {
    // dropping the src is what aborts the download and frees the decoder; removing the element alone leaves both to GC
    el.removeAttribute("src");
    el.load();
    el.remove();
  }, 200);
}

const tileObserver = new IntersectionObserver(entries => {
  entries.forEach(entry => {
    entry.target.dataset.inViewport = entry.isIntersecting ? "1" : "";
    if (!entry.isIntersecting) stopHoverPreview(entry.target);
  });
});

export function initVideoHoverPreviews(root = document) {
  if (!window.matchMedia("(hover: hover)").matches) return;
  root.querySelectorAll(".video-preview-tile[data-video-src]").forEach(tile => {
    if (hoverPreviews.has(tile)) return;
    tileObserver.observe(tile);
    const p = { previewEl: null, hoverTimer: null };
    hoverPreviews.set(tile, p);

    tile.addEventListener("mouseenter", () => {
      if (tile.dataset.inViewport !== "1" || state.selecting) return;
      p.hoverTimer = setTimeout(() => {
        if (activePreview !== tile) stopHoverPreview(activePreview);
        activePreview = tile;
        let el = p.previewEl;
        if (!el) {
          el = document.createElement("video");
          el.src = tile.dataset.videoSrc;
          el.muted = true;
          el.loop = true;
          el.playsInline = true;
          el.preload = "metadata";
          el.className = "absolute inset-0 w-full h-full object-cover z-10 opacity-0 transition-opacity duration-200 pointer-events-none";
          tile.appendChild(el);
          p.previewEl = el;
        }
        el.currentTime = 0;
        el.play().then(() => { if (p.previewEl === el) el.classList.remove("opacity-0"); }).catch(() => {});
      }, 200);
    });
    tile.addEventListener("mouseleave", () => stopHoverPreview(tile));
  });
}

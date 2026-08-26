import { state } from "./state.js";
import { toast, debounce } from "./utils.js";
import { fetchGalleryMeta, fetchGalleryFilterMeta } from "./api.js";

const PSWP_VIDEO_W = 1920;
const PSWP_VIDEO_H = 1080;

function pswpUpdateVideoSize(content, video) {
  if (!video || !content) return;
  const vw = video.videoWidth;
  const vh = video.videoHeight;
  if (!vw || !vh) return;
  content.width = vw;
  content.height = vh;
  if (content.slide) {
    content.slide.width = vw;
    content.slide.height = vh;
    if (content.slide.data) {
      content.slide.data.w = vw;
      content.slide.data.h = vh;
    }
    content.slide.calculateSize();
    content.slide.zoomAndPanToInitial();
    content.slide.applyCurrentZoomPan();
    content.slide.updateContentSize(true);
  }
}

let pswpLightboxModule = null;
let pswpModule = null;

function ensurePhotoSwipeCss() {
  if (document.querySelector('link[href="/static/vendor/photoswipe.min.css"]')) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = "/static/vendor/photoswipe.min.css";
  document.head.appendChild(link);
}

export async function preloadPhotoSwipe() {
  if (pswpLightboxModule && pswpModule) return;
  try {
    const [lbMod, psMod] = await Promise.all([
      import("/static/vendor/photoswipe-lightbox.esm.min.js"),
      import("/static/vendor/photoswipe.esm.min.js")
    ]);
    pswpLightboxModule = lbMod;
    pswpModule = psMod;
  } catch (err) {
    console.error("Failed to preload PhotoSwipe modules:", err);
  }
}

function pswpAddItemDataFilter(lb) {
  lb.addFilter("domItemData", (itemData, element, linkEl) => {
    const img = linkEl ? linkEl.querySelector("img") : null;
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
    return itemData;
  });

  lb.addFilter("itemData", (itemData) => {
    if (itemData.type === "video" && itemData.src) {
      itemData.w = PSWP_VIDEO_W;
      itemData.h = PSWP_VIDEO_H;
      itemData.html = `
        <div class="pswp-video-container" style="display:flex;flex-direction:column;align-items:center;justify-content:center;width:100%;height:100%;padding:20px;box-sizing:border-box;pointer-events:auto;">
          <video src="${itemData.src}" controls playsinline preload="metadata" style="max-width:100%;max-height:100%;width:auto;height:auto;object-fit:contain;border-radius:12px;box-shadow:0 25px 60px rgba(0,0,0,0.6);" onerror="if(!this.dataset.errored){this.dataset.errored='1';const fb=this.nextElementSibling;if(fb){fb.classList.remove('hidden');fb.classList.add('flex');}this.style.display='none';}"></video>
          <div class="video-codec-fallback hidden flex-col items-center justify-center p-6 bg-zinc-900/95 text-white rounded-xl border border-zinc-800 text-center max-w-md gap-3 shadow-2xl">
            <svg class="w-10 h-10 text-amber-500" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polygon points="23 7 16 12 23 17 23 7"></polygon><rect x="1" y="5" width="15" height="14" rx="2" ry="2"></rect></svg>
            <p class="text-xs font-semibold text-zinc-200">Video format not supported by this browser.</p>
            <p class="text-[11px] text-zinc-400">Open the file directly or play in an external player like QuickTime / VLC.</p>
            <a href="${itemData.src}" target="_blank" rel="noopener noreferrer" class="mt-2 bg-[#ff9900] hover:bg-[#e68a00] text-black font-bold text-xs px-4 py-2 rounded-lg transition-all shadow-xs inline-flex items-center gap-1.5 cursor-pointer">
              <span>Open Raw Video File</span>
              <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"></path><polyline points="15 3 21 3 21 9"></polyline><line x1="10" y1="14" x2="21" y2="3"></line></svg>
            </a>
          </div>
        </div>`;
    }
    return itemData;
  });
}

function pswpAddVideoControls(lb) {
  function handleVideo(el, content) {
    const v = el && el.querySelector && el.querySelector("video");
    if (!v) return;
    if (v.readyState >= 1) {
      pswpUpdateVideoSize(content, v);
    } else {
      v.addEventListener("loadedmetadata", () => {
        pswpUpdateVideoSize(content, v);
      }, { once: true });
    }
    v.play().catch(() => {});
  }

  lb.on("change", () => {
    if (lb.pswp && lb.pswp.element) {
      lb.pswp.element.querySelectorAll("video").forEach(v => v.pause());
      const el = lb.pswp.currSlide && lb.pswp.currSlide.content && lb.pswp.currSlide.content.element;
      if (el) handleVideo(el, lb.pswp.currSlide.content);
    }
  });

  lb.on("contentActivate", (e) => {
    const el = e.content && e.content.element;
    if (el) handleVideo(el, e.content);
  });

  lb.on("contentDeactivate", (e) => {
    const v = e.content && e.content.element && e.content.element.querySelector && e.content.element.querySelector("video");
    if (v) v.pause();
  });

  lb.on("destroy", () => {
    if (lb.pswp && lb.pswp.element) {
      lb.pswp.element.querySelectorAll("video").forEach(v => v.pause());
    }
  });
}

export async function initPhotoSwipeGrid() {
  try {
    ensurePhotoSwipeCss();
    if (state.pswpGrid) {
      try { state.pswpGrid.destroy(); } catch (_) {}
      state.pswpGrid = null;
    }
    const Lightbox = pswpLightboxModule?.default || (await import("/static/vendor/photoswipe-lightbox.esm.min.js")).default;
    state.pswpGrid = new Lightbox({
      gallery: "#lg-container, #global-search-grid",
      children: "a.pswp-item",
      showHideAnimationType: "none",
      showAnimationDuration: 0,
      hideAnimationDuration: 0,
      bgOpacity: 0.92,
      pswpModule: () => pswpModule ? Promise.resolve(pswpModule) : import("/static/vendor/photoswipe.esm.min.js"),
    });
    pswpAddItemDataFilter(state.pswpGrid);
    pswpAddVideoControls(state.pswpGrid);
    state.pswpGrid.init();
  } catch (err) {
    console.error("PhotoSwipe grid init error:", err);
  }
}

export async function initPhotoSwipePosts() {
  try {
    ensurePhotoSwipeCss();
    if (state.pswpPosts) {
      try { state.pswpPosts.destroy(); } catch (_) {}
      state.pswpPosts = null;
    }
    const Lightbox = pswpLightboxModule?.default || (await import("/static/vendor/photoswipe-lightbox.esm.min.js")).default;
    state.pswpPosts = new Lightbox({
      gallery: "#gallery-posts-list",
      children: "a.pswp-item",
      showHideAnimationType: "none",
      showAnimationDuration: 0,
      hideAnimationDuration: 0,
      bgOpacity: 0.92,
      pswpModule: () => pswpModule ? Promise.resolve(pswpModule) : import("/static/vendor/photoswipe.esm.min.js"),
    });
    pswpAddItemDataFilter(state.pswpPosts);
    pswpAddVideoControls(state.pswpPosts);
    state.pswpPosts.init();
  } catch (err) {
    console.error("PhotoSwipe posts init error:", err);
  }
}

// Global click protection to prevent direct file navigation on any platform
document.addEventListener("click", (e) => {
  const item = e.target.closest("a.pswp-item");
  if (!item) return;

  // Let middle clicks or ctrl/cmd-clicks open in new tab if user wants to
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.button === 1) return;

  e.preventDefault();

  const galleryEl = item.closest("#lg-container, #global-search-grid, #gallery-posts-list");
  if (!galleryEl) return;

  const isPosts = galleryEl.id === "gallery-posts-list";
  const lb = isPosts ? state.pswpPosts : state.pswpGrid;

  if (lb && lb.shouldOpen !== undefined) {
    const items = Array.from(galleryEl.querySelectorAll("a.pswp-item"));
    const idx = items.indexOf(item);
    if (idx >= 0) {
      lb.loadAndOpen(idx, { gallery: galleryEl });
    }
  } else {
    const initFn = isPosts ? initPhotoSwipePosts : initPhotoSwipeGrid;
    initFn().then(() => {
      const currentLb = isPosts ? state.pswpPosts : state.pswpGrid;
      if (currentLb) {
        const items = Array.from(galleryEl.querySelectorAll("a.pswp-item"));
        const idx = items.indexOf(item);
        if (idx >= 0) {
          currentLb.loadAndOpen(idx, { gallery: galleryEl });
        }
      }
    });
  }
}, false);

export function initYoutubePreviews() {
  document.querySelectorAll("button.youtube-preview[data-youtube-id]").forEach(btn => {
    if (btn.dataset.ytBound === "1") return;
    btn.dataset.ytBound = "1";
    btn.addEventListener("click", () => {
      const id = btn.dataset.youtubeId || "";
      if (!/^[A-Za-z0-9_-]{11}$/.test(id)) return;
      const frame = document.createElement("iframe");
      frame.className = "absolute inset-0 w-full h-full";
      frame.src = `https://www.youtube-nocookie.com/embed/${id}?autoplay=1&rel=0`;
      frame.title = "YouTube video player";
      frame.allow = "accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share";
      frame.allowFullscreen = true;
      btn.replaceChildren(frame);
      btn.classList.remove("cursor-pointer", "hover:shadow-md", "hover:border-[#ff9900]/50");
    });
  });
}

export function initVideoHoverPreviews() {
  document.querySelectorAll(".video-preview-tile[data-video-src]").forEach(tile => {
    if (tile.dataset.hoverBound === "1") return;
    tile.dataset.hoverBound = "1";
    const src = tile.dataset.videoSrc;
    let previewEl = null;
    let hoverTimer = null;

    tile.addEventListener("mouseenter", () => {
      hoverTimer = setTimeout(() => {
        if (!previewEl) {
          previewEl = document.createElement("video");
          previewEl.src = src;
          previewEl.muted = true;
          previewEl.loop = true;
          previewEl.playsInline = true;
          previewEl.className = "absolute inset-0 w-full h-full object-cover z-10 opacity-0 transition-opacity duration-200 pointer-events-none";
          tile.appendChild(previewEl);
        }
        previewEl.currentTime = 0;
        previewEl.play().then(() => {
          if (previewEl) previewEl.classList.remove("opacity-0");
        }).catch(() => {});
      }, 200);
    });

    tile.addEventListener("mouseleave", () => {
      clearTimeout(hoverTimer);
      if (previewEl) {
        previewEl.pause();
        previewEl.classList.add("opacity-0");
      }
    });
  });
}

export async function selectGalleryTarget(platform, username) {
  state.activeGalleryUser = username;
  state.activeGalleryPlatform = platform;
  state.currentView = "grid";

  state.postsSearchQuery = "";
  state.gridSearchQuery = "";
  const gallerySearchInput = document.getElementById("gallery-search-input");
  if (gallerySearchInput) gallerySearchInput.value = "";
  const globalSearchInput = document.getElementById("global-search-input");
  if (globalSearchInput) globalSearchInput.value = "";

  showGalleryState("loading");

  try {
    const [meta, filterMeta] = await Promise.all([
      fetchGalleryMeta(platform, username),
      fetchGalleryFilterMeta(platform, username)
    ]);
    state.galleryMeta = meta;
    state.galleryFilterMeta = filterMeta;
  } catch (err) {
    console.error("Gallery meta error:", err);
    state.galleryFilterMeta = null;
  }

  const files = state.galleryMeta?.files || [];
  const hasPosts = state.galleryMeta?.posts && state.galleryMeta.posts.length > 0;

  const tabPosts = document.getElementById("tab-posts");
  if (tabPosts) tabPosts.style.display = hasPosts ? "inline-flex" : "none";

  if (files.length === 0) {
    showGalleryState("empty");
    return;
  }

  showGalleryState("content");
  state.selectedHashtags.clear();
  state.selectedYears.clear();
  state.selectedMonths.clear();

  populateDateDropdown();
  populateHashtagDropdown();
  updateResetButtonVisibility();

  switchGalleryView(state.currentView);
}

export function showGalleryState(viewState) {
  const loading = document.getElementById("gallery-loading");
  const empty = document.getElementById("gallery-empty");
  const gridView = document.getElementById("gallery-grid-view");
  const postsView = document.getElementById("gallery-posts-view");

  if (loading) loading.style.display = viewState === "loading" ? "flex" : "none";
  if (empty) empty.style.display = viewState === "empty" ? "flex" : "none";
  if (gridView) gridView.style.display = (viewState === "content" && state.currentView === "grid") ? "block" : "none";
  if (postsView) postsView.style.display = (viewState === "content" && state.currentView === "posts") ? "block" : "none";
}

export async function renderGalleryGrid() {
  const container = document.getElementById("lg-container");
  if (!container) return;
  container.innerHTML = "";
  const year = state.selectedYears.size > 0 ? Array.from(state.selectedYears).join(",") : "all";
  const month = state.selectedMonths.size > 0 ? Array.from(state.selectedMonths).join(",") : "all";
  const tags = state.selectedHashtags.size > 0 ? Array.from(state.selectedHashtags).join(",") : "all";
  const url = `/gallery/${encodeURIComponent(state.activeGalleryPlatform)}/${encodeURIComponent(state.activeGalleryUser)}?filter=${encodeURIComponent(state.currentFilter)}&q=${encodeURIComponent(state.gridSearchQuery)}&sort=${state.postsSortAsc ? "asc" : "desc"}&year=${encodeURIComponent(year)}&month=${encodeURIComponent(month)}&tags=${encodeURIComponent(tags)}`;
  try {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const html = await res.text();
    container.innerHTML = html;
    if (window.htmx) window.htmx.process(container);
    initPhotoSwipeGrid();
    initVideoHoverPreviews();
    updateResetButtonVisibility();
  } catch (err) {
    console.error("Grid render error:", err);
    container.innerHTML = `<div class="col-span-full text-center text-xs font-semibold text-slate-500 py-12">Failed to load gallery</div>`;
    toast(`Failed to load gallery: ${err.message}`, "error");
  }
}

export async function renderGalleryPosts() {
  const container = document.getElementById("gallery-posts-list");
  if (!container) return;
  container.innerHTML = "";
  const year = state.selectedYears.size > 0 ? Array.from(state.selectedYears).join(",") : "all";
  const month = state.selectedMonths.size > 0 ? Array.from(state.selectedMonths).join(",") : "all";
  const tags = state.selectedHashtags.size > 0 ? Array.from(state.selectedHashtags).join(",") : "all";
  const url = `/gallery/${encodeURIComponent(state.activeGalleryPlatform)}/${encodeURIComponent(state.activeGalleryUser)}/posts/page/1?sort=${state.postsSortAsc ? "asc" : "desc"}&q=${encodeURIComponent(state.postsSearchQuery)}&year=${encodeURIComponent(year)}&month=${encodeURIComponent(month)}&tags=${encodeURIComponent(tags)}`;
  try {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const html = await res.text();
    container.innerHTML = html;
    if (window.htmx) window.htmx.process(container);
    initPhotoSwipePosts();
    initYoutubePreviews();
    updateResetButtonVisibility();
  } catch (err) {
    console.error("Posts render error:", err);
    container.innerHTML = `<div class="text-center text-xs font-semibold text-slate-500 py-12">Failed to load posts</div>`;
    toast(`Failed to load posts: ${err.message}`, "error");
  }
}

export function switchGalleryView(view) {
  state.currentView = view;
  document.querySelectorAll(".gallery-tab").forEach(t => {
    const active = t.id === `tab-${view}`;
    t.className = active
      ? "gallery-tab inline-flex items-center gap-1 text-xs px-2.5 py-1 rounded-md transition-all cursor-pointer bg-[#ff9900] text-black dark:text-black font-bold shadow-2xs"
      : "gallery-tab inline-flex items-center gap-1 text-xs font-medium px-2.5 py-1 rounded-md transition-all cursor-pointer text-slate-600 dark:text-zinc-400 hover:text-slate-900 dark:hover:text-white bg-transparent";
  });

  const gridView = document.getElementById("gallery-grid-view");
  const postsView = document.getElementById("gallery-posts-view");
  if (gridView) gridView.style.display = view === "grid" ? "block" : "none";
  if (postsView) postsView.style.display = view === "posts" ? "block" : "none";

  const typePills = document.getElementById("media-type-pills");
  if (typePills) typePills.style.display = view === "grid" ? "flex" : "none";

  if (view === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function applyGalleryFilter(filter) {
  state.currentFilter = filter;
  document.querySelectorAll("#media-type-pills .gallery-filter-btn").forEach(btn => {
    const active = btn.dataset.filter === filter;
    btn.className = active
      ? "gallery-filter-btn text-xs px-2.5 py-1 rounded-lg transition-all cursor-pointer bg-[#ff9900] text-black dark:text-black font-bold shadow-2xs border border-[#ff9900]"
      : "gallery-filter-btn text-xs font-medium px-2.5 py-1 rounded-lg transition-all cursor-pointer text-slate-600 dark:text-zinc-400 hover:text-slate-900 dark:hover:text-white bg-slate-100 dark:bg-zinc-950 border border-slate-200 dark:border-zinc-800";
  });
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function togglePostsSort() {
  state.postsSortAsc = !state.postsSortAsc;
  const label = document.getElementById("sort-order-label");
  const icon = document.getElementById("sort-order-icon");
  const btn = document.getElementById("btn-sort-toggle");
  if (label) label.textContent = state.postsSortAsc ? "Oldest" : "Newest";
  if (icon) {
    icon.outerHTML = state.postsSortAsc
      ? `<svg id="sort-order-icon" class="w-3.5 h-3.5 ${state.postsSortAsc ? 'text-black dark:text-black' : 'text-[#ff9900]'}" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><line x1="12" y1="19" x2="12" y2="5"></line><polyline points="5 12 12 5 19 12"></polyline></svg>`
      : `<svg id="sort-order-icon" class="w-3.5 h-3.5 text-[#ff9900]" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"></line><polyline points="19 12 12 19 5 12"></polyline></svg>`;
  }
  if (btn) {
    btn.className = state.postsSortAsc
      ? "text-xs font-bold bg-[#ff9900] text-black dark:text-black border border-[#ff9900] px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer shadow-2xs"
      : "text-xs font-semibold bg-slate-100 dark:bg-zinc-900 hover:bg-slate-200 dark:hover:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white border border-slate-200 dark:border-zinc-800 px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer";
  }
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export const applyGallerySearch = debounce(() => {
  const input = document.getElementById("gallery-search-input");
  const clearBtn = document.getElementById("gallery-search-clear");
  const val = input ? input.value.trim().toLowerCase() : "";
  if (clearBtn) clearBtn.classList.toggle("hidden", !val);
  state.postsSearchQuery = val;
  state.gridSearchQuery = val;
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}, 180);

export function clearGallerySearch() {
  const input = document.getElementById("gallery-search-input");
  if (input) input.value = "";
  applyGallerySearch();
}

export function resetAllFilters() {
  state.currentFilter = "all";
  state.postsSearchQuery = "";
  state.gridSearchQuery = "";
  state.postsSortAsc = false;
  state.selectedYears.clear();
  state.selectedMonths.clear();
  state.selectedHashtags.clear();

  const searchInput = document.getElementById("gallery-search-input");
  if (searchInput) searchInput.value = "";
  const clearBtn = document.getElementById("gallery-search-clear");
  if (clearBtn) clearBtn.classList.add("hidden");

  const globalSearchInput = document.getElementById("global-search-input");
  if (globalSearchInput) globalSearchInput.value = "";
  const globalClearBtn = document.getElementById("global-search-clear");
  if (globalClearBtn) globalClearBtn.classList.add("hidden");

  applyGalleryFilter("all");
  updateDateFilterLabel();
  updateHashtagLabel();
  populateDateDropdown();
  populateHashtagDropdown();

  const sortBtn = document.getElementById("btn-sort-toggle");
  const sortLabel = document.getElementById("sort-order-label");
  if (sortLabel) sortLabel.textContent = "Newest";
  if (sortBtn) {
    sortBtn.className = "text-xs font-semibold bg-slate-100 dark:bg-zinc-900 hover:bg-slate-200 dark:hover:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white border border-slate-200 dark:border-zinc-800 px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer";
  }

  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function updateResetButtonVisibility() {
  const resetBtn = document.getElementById("btn-reset-filters");
  if (!resetBtn) return;
  const isDirty = state.currentFilter !== "all" || state.gridSearchQuery !== "" || state.postsSearchQuery !== "" || state.selectedYears.size > 0 || state.selectedMonths.size > 0 || state.selectedHashtags.size > 0 || state.postsSortAsc;
  resetBtn.classList.toggle("hidden", !isDirty);
}

export function toggleDropdown(id) {
  const el = document.getElementById(id);
  if (el) el.classList.toggle("hidden");
}

export function populateDateDropdown() {
  const yearList = document.getElementById("year-dropdown-list");
  const monthList = document.getElementById("month-dropdown-list");
  if (!yearList || !monthList) return;

  let sortedYears = state.galleryFilterMeta?.years;
  if (!sortedYears) {
    const yearsSet = new Set();
    (state.galleryMeta?.files || []).forEach(f => {
      if (f.date && /^\d{4}-\d{2}-\d{2}/.test(f.date)) {
        yearsSet.add(f.date.slice(0, 4));
      }
    });
    (state.galleryMeta?.posts || []).forEach(p => {
      if (p.date && /^\d{4}-\d{2}-\d{2}/.test(p.date)) {
        yearsSet.add(p.date.slice(0, 4));
      }
    });
    sortedYears = Array.from(yearsSet).sort((a, b) => b.localeCompare(a));
  }
  let yHTML = "";
  sortedYears.forEach(yr => {
    const isChecked = state.selectedYears.has(yr);
    yHTML += `
      <label class="flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs select-none cursor-pointer ${isChecked ? 'bg-[#ff9900] text-black dark:text-black font-bold border border-[#ff9900]' : 'bg-slate-100 dark:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white font-semibold'}">
        <input type="checkbox" value="${yr}" ${isChecked ? "checked" : ""} class="accent-[#ff9900] rounded w-3 h-3" onchange="window.toggleYearFilter(this)">
        <span>${yr}</span>
      </label>
    `;
  });
  yearList.innerHTML = yHTML || `<span class="text-[10px] text-slate-400">All available</span>`;

  const monthNames = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  let mHTML = "";
  monthNames.forEach((name, idx) => {
    const mVal = String(idx + 1).padStart(2, "0");
    const isChecked = state.selectedMonths.has(mVal);
    mHTML += `
      <label class="flex items-center justify-center py-1.5 rounded-md text-xs select-none cursor-pointer ${isChecked ? 'bg-[#ff9900] text-black dark:text-black font-bold border border-[#ff9900]' : 'bg-slate-100 dark:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white font-semibold'}">
        <input type="checkbox" value="${mVal}" ${isChecked ? "checked" : ""} class="hidden" onchange="window.toggleMonthFilter(this)">
        <span>${name}</span>
      </label>
    `;
  });
  monthList.innerHTML = mHTML;
  updateDateFilterLabel();
}

export function toggleYearFilter(checkbox) {
  const val = checkbox.value;
  if (checkbox.checked) state.selectedYears.add(val);
  else state.selectedYears.delete(val);
  updateDateFilterLabel();
  populateDateDropdown();
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function toggleMonthFilter(checkbox) {
  const val = checkbox.value;
  if (checkbox.checked) state.selectedMonths.add(val);
  else state.selectedMonths.delete(val);
  updateDateFilterLabel();
  populateDateDropdown();
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function clearDateFilters() {
  state.selectedYears.clear();
  state.selectedMonths.clear();
  updateDateFilterLabel();
  populateDateDropdown();
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function updateDateFilterLabel() {
  const label = document.getElementById("date-filter-label");
  const btn = document.getElementById("btn-date-filter");
  if (!label || !btn) return;
  const count = state.selectedYears.size + state.selectedMonths.size;
  if (count === 0) {
    label.textContent = "Dates";
    btn.className = "text-xs font-semibold bg-slate-100 dark:bg-zinc-900 hover:bg-slate-200 dark:hover:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white border border-slate-200 dark:border-zinc-800 px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer";
  } else {
    label.textContent = `Dates (${count})`;
    btn.className = "text-xs font-bold bg-[#ff9900] text-black dark:text-black border border-[#ff9900] px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer shadow-2xs";
  }
}

function extractHashtags(text) {
  if (!text) return [];
  const matches = text.match(/#\w+/g);
  return matches ? matches.map(m => m.toLowerCase()) : [];
}

export function populateHashtagDropdown() {
  const container = document.getElementById("hashtag-filter-container");
  const dropdown = document.getElementById("hashtag-dropdown");
  if (!container || !dropdown) return;

  const serverTags = state.galleryFilterMeta?.hashtags;
  let hashtagsMap;
  if (serverTags) {
    hashtagsMap = new Map(serverTags.map(t => [t.tag, t.count]));
  } else {
    hashtagsMap = new Map();
    (state.galleryMeta?.posts || []).forEach(post => {
      extractHashtags(post.text).forEach(tag => {
        hashtagsMap.set(tag, (hashtagsMap.get(tag) || 0) + 1);
      });
    });
  }

  if (hashtagsMap.size === 0) {
    container.style.display = "none";
    return;
  }

  container.style.display = "inline-block";
  const sortedTags = Array.from(hashtagsMap.keys()).sort((a, b) => hashtagsMap.get(b) - hashtagsMap.get(a));

  let html = "";
  sortedTags.forEach(tag => {
    const isChecked = state.selectedHashtags.has(tag);
    const count = hashtagsMap.get(tag);
    html += `
      <label class="flex items-center gap-2 px-2.5 py-1.5 rounded-lg cursor-pointer text-xs select-none ${isChecked ? 'bg-[#ff9900] text-black dark:text-black font-bold' : 'hover:bg-slate-100 dark:hover:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white font-semibold'}">
        <input type="checkbox" value="${tag}" ${isChecked ? "checked" : ""} class="accent-[#ff9900] rounded w-3.5 h-3.5" onchange="window.toggleHashtagFilter(this)">
        <span class="truncate">${tag}</span>
        <span class="text-[9px] ${isChecked ? 'bg-black/15 text-black dark:text-black font-bold' : 'bg-slate-100 dark:bg-zinc-800 text-slate-400'} px-1.5 py-0.2 rounded-full ml-auto">${count}</span>
      </label>
    `;
  });
  dropdown.innerHTML = html;
  updateHashtagLabel();
}

export function toggleHashtagFilter(checkbox) {
  const tag = checkbox.value;
  if (checkbox.checked) state.selectedHashtags.add(tag);
  else state.selectedHashtags.delete(tag);
  updateHashtagLabel();
  populateHashtagDropdown();
  if (state.currentView === "grid") renderGalleryGrid();
  else renderGalleryPosts();
}

export function updateHashtagLabel() {
  const label = document.getElementById("hashtag-filter-label");
  const btn = document.getElementById("btn-hashtag-filter");
  if (!label || !btn) return;
  if (state.selectedHashtags.size > 0) {
    label.textContent = `#tags (${state.selectedHashtags.size})`;
    btn.className = "text-xs font-bold bg-[#ff9900] text-black dark:text-black border border-[#ff9900] px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer shadow-2xs";
  } else {
    label.textContent = "#tags";
    btn.className = "text-xs font-semibold bg-slate-100 dark:bg-zinc-900 hover:bg-slate-200 dark:hover:bg-zinc-800 text-slate-700 hover:text-slate-900 dark:text-zinc-300 dark:hover:text-white border border-slate-200 dark:border-zinc-800 px-2.5 py-1 rounded-lg flex items-center gap-1.5 transition-all cursor-pointer";
  }
}

export function initDensity() {
  const container = document.getElementById("lg-container");
  if (container) {
    container.classList.toggle("density-compact", state.gridDensity === "compact");
    container.classList.toggle("density-relaxed", state.gridDensity === "relaxed");
  }
}

export function toggleDensity() {
  state.gridDensity = state.gridDensity === "relaxed" ? "compact" : "relaxed";
  localStorage.setItem("idolhub-density", state.gridDensity);
  initDensity();
  toast(`Grid density: ${state.gridDensity}`, "info", 1200);
}

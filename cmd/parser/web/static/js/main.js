import { state } from "./state.js";
import { toast } from "./utils.js";
import { initSSE, pollProgress } from "./sse.js";
import { startCountdownTicker, syncAll, renderOverviewDashboard, renderGlobalSearchResults } from "./overview.js";
import {
  filterTargets,
  clearTargetSearch,
  setPlatformFilter,
  selectTerminalUser,
  goToOverview,
  quickSyncTarget,
  startSync,
  cancelSync,
  fullResyncAll,
  deleteAccount,
  clearTargetFolder,
  openSidebar,
  closeSidebar,
  renderDashboardSidebar
} from "./sidebar.js";
import {
  switchGalleryView,
  applyGalleryFilter,
  togglePostsSort,
  applyGallerySearch,
  clearGallerySearch,
  resetAllFilters,
  toggleDropdown,
  toggleYearFilter,
  toggleMonthFilter,
  clearDateFilters,
  toggleHashtagFilter,
  toggleDensity,
  initDensity,
  preloadPhotoSwipe,
  renderGalleryGrid,
  renderGalleryPosts
} from "./gallery.js";
import {
  toggleBottomConsole,
  setTerminalLevel,
  copyTerminalLogs
} from "./dock.js";
import {
  handleUsernameInput,
  selectAddPlatform,
  toggleCmdPalette,
  toggleTwitterOptions,
  handleTagInputKey,
  addNewFilter,
  removeNewFilter,
  addAccount,
  closeEditModal,
  addEditFilter,
  removeEditFilter,
  saveEditChanges,
  fullResyncTarget,
  toggleSettingsModal,
  openTokenSettings,
  loadConfig,
  saveSettings
} from "./modals.js";

export function handleGlobalSearch(query) {
  const val = (query || "").trim();
  const clearBtn = document.getElementById("global-search-clear");
  if (clearBtn) clearBtn.classList.toggle("hidden", !val);

  const gallerySearchInput = document.getElementById("gallery-search-input");
  if (gallerySearchInput) gallerySearchInput.value = val;

  state.postsSearchQuery = val.toLowerCase();
  state.gridSearchQuery = val.toLowerCase();

  if (!state.activeTerminalUser) {
    state.targetSearchQuery = val.toLowerCase();
    const targetSearchInput = document.getElementById("target-search-input");
    if (targetSearchInput) targetSearchInput.value = val;
    const targetClearBtn = document.getElementById("target-search-clear");
    if (targetClearBtn) targetClearBtn.classList.toggle("hidden", !val);
    renderDashboardSidebar();

    if (val) {
      renderGlobalSearchResults(val);
    } else {
      renderOverviewDashboard();
    }
  } else {
    if (state.currentView === "grid") renderGalleryGrid();
    else renderGalleryPosts();
  }
}

export function clearGlobalSearch() {
  const input = document.getElementById("global-search-input");
  if (input) input.value = "";
  handleGlobalSearch("");
}

export function initTheme() {
  const saved = localStorage.getItem('color-theme');
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  if (saved === 'dark' || (!saved && prefersDark)) {
    document.documentElement.classList.add('dark');
  } else {
    document.documentElement.classList.remove('dark');
  }
}

export function toggleTheme() {
  const isDark = document.documentElement.classList.contains('dark');
  if (isDark) {
    document.documentElement.classList.remove('dark');
    localStorage.setItem('color-theme', 'light');
    toast("Switched to Light mode", "info", 1000);
  } else {
    document.documentElement.classList.add('dark');
    localStorage.setItem('color-theme', 'dark');
    toast("Switched to Dark mode", "info", 1000);
  }
}

export function scrollToTop() {
  const scrollArea = document.getElementById("workspace-scroll-area");
  if (scrollArea) {
    scrollArea.scrollTo({ top: 0, behavior: "smooth" });
  } else {
    window.scrollTo({ top: 0, behavior: "smooth" });
  }
}

Object.assign(window, {
  handleGlobalSearch,
  clearGlobalSearch,
  filterTargets,
  clearTargetSearch,
  setPlatformFilter,
  selectTerminalUser,
  goToOverview,
  quickSyncTarget,
  startSync,
  cancelSync,
  fullResyncAll,
  deleteAccount,
  clearTargetFolder,
  openSidebar,
  closeSidebar,

  switchGalleryView,
  applyGalleryFilter,
  togglePostsSort,
  applyGallerySearch,
  clearGallerySearch,
  resetAllFilters,
  toggleDropdown,
  toggleYearFilter,
  toggleMonthFilter,
  clearDateFilters,
  toggleHashtagFilter,
  toggleDensity,

  toggleBottomConsole,
  setTerminalLevel,
  copyTerminalLogs,

  handleUsernameInput,
  selectAddPlatform,
  toggleCmdPalette,
  toggleTwitterOptions,
  handleTagInputKey,
  addNewFilter,
  removeNewFilter,
  addAccount,
  closeEditModal,
  addEditFilter,
  removeEditFilter,
  saveEditChanges,
  fullResyncTarget,
  toggleSettingsModal,
  openTokenSettings,
  saveSettings,
  syncAll,
  toggleTheme,
  scrollToTop
});

window.addEventListener("open-edit-modal", (e) => {
  state.editConfig = { ...e.detail, filters: e.detail.filters || [] };
  document.getElementById("edit-username").value = e.detail.username;

  const platformBadge = document.getElementById("edit-platform-badge");
  if (platformBadge) {
    platformBadge.textContent = (e.detail.platform || "").toUpperCase();
    if (e.detail.platform === "instagram") {
      platformBadge.className = "text-[10px] font-bold uppercase px-2 py-0.5 rounded border bg-pink-50 dark:bg-pink-950/40 text-pink-700 dark:text-pink-400 border-pink-200 dark:border-pink-800";
    } else if (e.detail.platform === "twitter") {
      platformBadge.className = "text-[10px] font-bold uppercase px-2 py-0.5 rounded border bg-sky-50 dark:bg-sky-950/40 text-sky-700 dark:text-sky-400 border-sky-200 dark:border-sky-800";
    } else {
      platformBadge.className = "text-[10px] font-bold uppercase px-2 py-0.5 rounded border bg-slate-100 dark:bg-zinc-800 text-slate-700 dark:text-zinc-300 border-slate-300 dark:border-zinc-700";
    }
  }

  document.getElementById("edit-save-text").checked = e.detail.save_text;
  document.getElementById("edit-skip-retweets").checked = e.detail.skip_retweets;
  document.getElementById("edit-download-photos").checked = e.detail.download_photos;
  document.getElementById("edit-download-videos").checked = e.detail.download_videos;

  const ot = document.getElementById("edit-options-container");
  if (ot) {
    const isYtdlp = e.detail.platform === "tiktok";
    ot.style.display = isYtdlp ? "none" : "flex";
    const isTwitter = e.detail.platform === "twitter";
    document.querySelectorAll("#edit-options-container .edit-twitter-only-option").forEach(el => {
      el.style.display = isTwitter ? "" : "none";
    });
  }
  import("./modals.js").then(m => m.renderEditFilters());
  const modal = document.getElementById("edit-modal");
  modal.style.display = "flex";
  modal.focus();
});

document.addEventListener("DOMContentLoaded", () => {
  initTheme();
  initDensity();
  preloadPhotoSwipe();
  loadConfig();
  pollProgress();
  initSSE();
  startCountdownTicker();
  setTerminalLevel("all");

  const addForm = document.getElementById("add-account-form");
  if (addForm) addForm.addEventListener("submit", addAccount);
  const settingsForm = document.getElementById("settings-form");
  if (settingsForm) settingsForm.addEventListener("submit", saveSettings);

  function updateBackToTop() {
    const btn = document.getElementById("btn-back-to-top");
    if (!btn) return;
    const scrollArea = document.getElementById("workspace-scroll-area");
    const top = scrollArea ? scrollArea.scrollTop : window.scrollY;
    const show = top > 300;
    btn.classList.toggle("scale-0", !show);
    btn.classList.toggle("opacity-0", !show);
    btn.classList.toggle("scale-100", show);
    btn.classList.toggle("opacity-100", show);
  }
  const scrollArea = document.getElementById("workspace-scroll-area");
  if (scrollArea) {
    scrollArea.addEventListener("scroll", updateBackToTop, { passive: true });
  } else {
    window.addEventListener("scroll", updateBackToTop, { passive: true });
  }

  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      let closed = false;
      const cmdPalette = document.getElementById("cmd-palette-modal");
      if (cmdPalette && cmdPalette.style.display === "flex") { cmdPalette.style.display = "none"; closed = true; }
      const editModal = document.getElementById("edit-modal");
      if (editModal && editModal.style.display === "flex") { editModal.style.display = "none"; closed = true; }
      const settingsModal = document.getElementById("settings-modal");
      if (settingsModal && settingsModal.style.display === "flex") { settingsModal.style.display = "none"; closed = true; }
      const dateDropdown = document.getElementById("date-filter-dropdown");
      if (dateDropdown && !dateDropdown.classList.contains("hidden")) { dateDropdown.classList.add("hidden"); closed = true; }
      const hashtagDropdown = document.getElementById("hashtag-dropdown");
      if (hashtagDropdown && !hashtagDropdown.classList.contains("hidden")) { hashtagDropdown.classList.add("hidden"); closed = true; }
      if (state.dockConsoleOpen) { toggleBottomConsole(false); closed = true; }
      if (state.pswpGrid && state.pswpGrid.pswp) { state.pswpGrid.pswp.close(); closed = true; }
      if (state.pswpPosts && state.pswpPosts.pswp) { state.pswpPosts.pswp.close(); closed = true; }
      if (closed) { e.preventDefault(); e.stopPropagation(); return; }
    }

    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
      e.preventDefault();
      toggleCmdPalette();
      return;
    }

    if ((e.ctrlKey || e.metaKey) && (e.key === "j" || e.key === "J")) {
      e.preventDefault();
      toggleBottomConsole();
      return;
    }

    const tag = (e.target.tagName || "").toLowerCase();
    const typing = tag === "input" || tag === "textarea" || tag === "select" || e.target.isContentEditable;
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return;

    if (e.key === "/") {
      e.preventDefault();
      const globalInput = document.getElementById("global-search-input");
      const galleryInput = document.getElementById("gallery-search-input");
      if (state.activeGalleryUser && galleryInput) {
        galleryInput.focus();
      } else if (globalInput) {
        globalInput.focus();
      }
    } else if (e.key === "g" && state.activeTerminalUser) {
      e.preventDefault();
      switchGalleryView(state.currentView === "grid" ? "posts" : "grid");
    }
  }, true);
});

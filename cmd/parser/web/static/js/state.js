export const state = {
  activeTerminalUser: null,
  cachedProgress: [],
  lastSyncTime: "",
  autoSyncInterval: 0,

  progressPollTimeout: null,
  countdownTicker: null,
  sseSource: null,
  sseConnected: false,
  sseReconnectTimeout: null,

  terminalLevel: "all",
  globalLogs: [],
  dockConsoleOpen: false,
  gridDensity: localStorage.getItem("idolhub-density") || "relaxed",

  targetSearchQuery: "",
  targetPlatformFilter: "all",

  galleryMeta: null,
  pswpGrid: null,
  pswpPosts: null,
  currentView: "grid",
  currentFilter: "all",
  selectedHashtags: new Set(),
  selectedYears: new Set(),
  selectedMonths: new Set(),

  activeGalleryUser: null,
  activeGalleryPlatform: null,

  gridSearchQuery: "",
  postsSearchQuery: "",
  postsSortAsc: false,

  newFilters: [],
  editConfig: {
    username: "",
    platform: "instagram",
    saveText: false,
    skipRetweets: false,
    downloadPhotos: true,
    downloadVideos: true,
    filters: []
  }
};

const UI_STATE_KEY = "idolhub-ui-state";

export function saveUiState() {
  try {
    localStorage.setItem(UI_STATE_KEY, JSON.stringify({
      activeTerminalUser: state.activeTerminalUser,
      activeGalleryUser: state.activeGalleryUser,
      activeGalleryPlatform: state.activeGalleryPlatform,
      currentView: state.currentView,
      currentFilter: state.currentFilter,
      gridSearchQuery: state.gridSearchQuery,
      postsSearchQuery: state.postsSearchQuery,
      postsSortAsc: state.postsSortAsc,
      selectedYears: Array.from(state.selectedYears),
      selectedMonths: Array.from(state.selectedMonths),
      selectedHashtags: Array.from(state.selectedHashtags),
      targetPlatformFilter: state.targetPlatformFilter
    }));
  } catch (_) {}
}

export function loadUiState() {
  try {
    const raw = localStorage.getItem(UI_STATE_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch (_) {
    return null;
  }
}

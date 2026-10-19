export const state = {
  activeTerminalUser: null,
  // the same handle can be configured on several platforms; a target is the pair
  activeTerminalPlatform: null,
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
  galleryFilterMeta: null,
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

// findTarget looks a target up by username and platform; a missing platform (state saved by older builds) takes the first username match.
export function findTarget(username, platform) {
  return state.cachedProgress.find(t => t.username === username && (!platform || t.platform === platform));
}

export function activeTarget() {
  if (!state.activeTerminalUser) return null;
  return findTarget(state.activeTerminalUser, state.activeTerminalPlatform);
}

export function isActiveTarget(username, platform) {
  return state.activeTerminalUser === username && (!platform || !state.activeTerminalPlatform || state.activeTerminalPlatform === platform);
}

const UI_STATE_KEY = "idolhub-ui-state";

export function saveUiState() {
  try {
    localStorage.setItem(UI_STATE_KEY, JSON.stringify({
      activeTerminalUser: state.activeTerminalUser,
      activeTerminalPlatform: state.activeTerminalPlatform,
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

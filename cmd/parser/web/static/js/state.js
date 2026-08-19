export const state = {
  activeTerminalUser: null,
  cachedProgress: [],
  lastSyncTime: "",
  autoSyncInterval: 0,

  progressPollTimeout: null,
  countdownTicker: null,
  sseSource: null,
  sseConnected: false,

  terminalLevel: "all",
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

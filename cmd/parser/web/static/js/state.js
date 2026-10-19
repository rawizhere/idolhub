function storedDensity() {
  try { return localStorage.getItem("idolhub-density") || "relaxed"; } catch (_) { return "relaxed"; }
}

export const state = {
  // the same handle can be configured on several platforms; a target is the pair
  target: null,
  cachedProgress: [],
  lastSyncTime: "",
  autoSyncInterval: 0,
  cookieStatus: {},

  progressPollTimeout: null,
  countdownTicker: null,
  sseSource: null,
  sseConnected: false,
  sseReconnectTimeout: null,
  // progress samples per target key, for the ETA estimate
  progressStart: new Map(),

  terminalLevel: "all",
  globalLogs: [],
  dockConsoleOpen: false,
  gridDensity: storedDensity(),

  targetSearchQuery: "",
  targetPlatformFilter: "all",

  galleryMeta: null,
  view: "grid",
  filter: "all",
  query: "",
  sortAsc: false,
  years: new Set(),
  months: new Set(),
  tags: new Set(),

  selecting: false,
  selected: new Set(),

  newFilters: [],
  editConfig: null
};

export const targetKey = (t) => `${t.platform}/${t.username}`;

export function findTarget(username, platform) {
  return state.cachedProgress.find(t => t.username === username && (!platform || t.platform === platform));
}

export function activeTarget() {
  if (!state.target) return null;
  return findTarget(state.target.username, state.target.platform);
}

export function isActiveTarget(username, platform) {
  return !!state.target && state.target.username === username && (!platform || state.target.platform === platform);
}

export function filtersDirty() {
  return state.filter !== "all" || state.query !== "" || state.sortAsc ||
    state.years.size > 0 || state.months.size > 0 || state.tags.size > 0;
}

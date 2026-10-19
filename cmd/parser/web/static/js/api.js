async function request(url, opts = {}, fallback = "Request failed") {
  const res = await fetch(url, opts);
  if (!res.ok) {
    const text = (await res.text().catch(() => "")).trim();
    let msg = text;
    try { msg = JSON.parse(text).error || text; } catch (_) {}
    const err = new Error(msg || fallback);
    err.status = res.status;
    throw err;
  }
  return res;
}

const sendJSON = (method, url, body, fallback) => request(url, {
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body)
}, fallback).then(r => (r.status === 204 ? null : r.json()));

const postJSON = (url, body, fallback) => sendJSON("POST", url, body, fallback);
const accountUrl = (platform, username) => `/api/accounts/${encodeURIComponent(platform)}/${encodeURIComponent(username)}`;

export const fetchConfig = () => request("/api/config", {}, "Failed to load configuration").then(r => r.json());

// GET /api/config never returns secrets; any secret left out of the body keeps its stored value.
export function postConfig(cfg) {
  const { cookie_status: _status, ...body } = cfg;
  return postJSON("/api/config", body, "Failed to save configuration");
}

export const fetchProgress = () => request("/api/progress", {}, "Failed to load progress data").then(r => r.json());
export const fetchIgSession = () => request("/api/instagram/session").then(r => r.json());

export const startSyncApi = (username, forceFull = false, platform = "") =>
  postJSON("/api/scrape/start", { username, platform, force_full: Boolean(forceFull) }, "Failed to trigger sync");

export const cancelSyncApi = (username, platform = "") =>
  postJSON("/api/scrape/cancel", { username, platform }, "Failed to cancel sync");

export const clearFolderApi = (platform, username) =>
  postJSON("/api/scrape/clear", { platform, username }, "Failed to clear downloads");

export const deleteMediaApi = (platform, username, files) =>
  postJSON("/api/media/delete", { platform, username, files }, "Failed to delete files");

export const fetchGalleryMeta = (platform, username, signal) =>
  request(`/api/gallery/meta?platform=${encodeURIComponent(platform)}&username=${encodeURIComponent(username)}`, { signal }, "Failed to fetch gallery metadata").then(r => r.json());

export const fetchGlobalSearch = (query, signal) =>
  request(`/api/search?q=${encodeURIComponent(query)}`, { signal }, "Failed to perform global search").then(r => r.json());

export const fetchRecent = (limit, signal) =>
  request(`/api/recent?limit=${limit}`, { signal }, "Failed to load recent media").then(r => r.json());

export const fetchHTML = (url, signal) => request(url, { signal }).then(r => r.text());

export const listAccounts = () => request("/api/accounts", {}, "Failed to load targets").then(r => r.json()).then(d => d.accounts);
export const createAccount = (body) => postJSON("/api/accounts", body, "Failed to add target");
export const patchAccount = (platform, username, body) => sendJSON("PATCH", accountUrl(platform, username), body, "Failed to update target");
export const deleteAccountApi = (platform, username) => request(accountUrl(platform, username), { method: "DELETE" }, "Failed to remove target");
export const fetchRuns = (platform, username) => request(`${accountUrl(platform, username)}/runs`).then(r => r.json()).then(d => d.runs);

export function fetchFilterPreview(platform, username, filters, signal) {
  const p = new URLSearchParams();
  filters.forEach(f => p.append("f", f));
  return request(`${accountUrl(platform, username)}/filter-preview?${p}`, { signal }).then(r => r.json());
}

export const lookupProfile = (platform, username, signal) =>
  request(`/api/lookup?platform=${encodeURIComponent(platform)}&username=${encodeURIComponent(username)}`, { signal }).then(r => r.json());

export const checkSession = (platform, username = "") =>
  postJSON(`/api/sessions/${encodeURIComponent(platform)}/check${username ? `?username=${encodeURIComponent(username)}` : ""}`, {}, "Session check failed");

export const importAccounts = (data, withSettings) =>
  postJSON(`/api/accounts/import${withSettings ? "?settings=1" : ""}`, data, "Import failed");

export const fetchStorage = () => request("/api/storage", {}, "Failed to load storage usage").then(r => r.json());
export const clearQuarantine = () => request("/api/storage/quarantine/clear", { method: "POST" }, "Failed to clear quarantine");

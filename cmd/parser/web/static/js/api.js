export async function fetchConfig() {
  const res = await fetch("/api/config");
  if (!res.ok) throw new Error("Failed to load configuration");
  return res.json();
}

export async function postConfig(configData) {
  const res = await fetch("/api/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(configData)
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "Unknown error");
    throw new Error(text || "Failed to save configuration");
  }
  return res;
}

export async function fetchProgress() {
  const res = await fetch("/api/progress");
  if (!res.ok) throw new Error("Failed to load progress data");
  return res.json();
}

export async function startSyncApi(username, forceFull = false) {
  const res = await fetch("/api/scrape/start", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, force_full: Boolean(forceFull) })
  });
  if (!res.ok) throw new Error("Failed to trigger sync");
  return res.json();
}

export async function cancelSyncApi(username) {
  const res = await fetch("/api/scrape/cancel", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username })
  });
  if (!res.ok) throw new Error("Failed to cancel sync");
  return res.json();
}

export async function clearFolderApi(platform, username) {
  const res = await fetch("/api/scrape/clear", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ platform, username })
  });
  if (!res.ok) throw new Error("Failed to clear downloads");
  return res.json();
}

export async function fetchGalleryMeta(platform, username) {
  const res = await fetch(`/api/gallery?platform=${encodeURIComponent(platform)}&username=${encodeURIComponent(username)}`);
  if (!res.ok) throw new Error("Failed to fetch gallery metadata");
  return res.json();
}

export async function fetchGlobalSearch(query) {
  const res = await fetch(`/api/search?q=${encodeURIComponent(query)}`);
  if (!res.ok) throw new Error("Failed to perform global search");
  return res.json();
}

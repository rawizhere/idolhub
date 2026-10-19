import { state } from "./state.js";

// The URL is the source of truth for what is on screen:
//   /?t=twitter/name&view=posts&type=video&q=cat&sort=asc&year=2024,2025&month=01&tags=%23a
// Target and view changes push history entries; filter tweaks replace the current one.

const list = (v) => (v ? v.split(",").filter(Boolean) : []);

export function readRoute(search = location.search) {
  const p = new URLSearchParams(search);
  const t = p.get("t") || "";
  const slash = t.indexOf("/");
  return {
    target: slash > 0 ? { platform: t.slice(0, slash), username: t.slice(slash + 1) } : null,
    view: ["posts", "settings"].includes(p.get("view")) ? p.get("view") : "grid",
    filter: ["image", "video"].includes(p.get("type")) ? p.get("type") : "all",
    query: p.get("q") || "",
    sortAsc: p.get("sort") === "asc",
    years: list(p.get("year")),
    months: list(p.get("month")),
    tags: list(p.get("tags"))
  };
}

function routeFromState() {
  const p = new URLSearchParams();
  if (state.target) {
    p.set("t", `${state.target.platform}/${state.target.username}`);
    if (state.view !== "grid") p.set("view", state.view);
    if (state.filter !== "all") p.set("type", state.filter);
    if (state.sortAsc) p.set("sort", "asc");
    if (state.years.size) p.set("year", [...state.years].join(","));
    if (state.months.size) p.set("month", [...state.months].join(","));
    if (state.tags.size) p.set("tags", [...state.tags].join(","));
  }
  if (state.query) p.set("q", state.query);
  const qs = p.toString();
  return qs ? `/?${qs}` : "/";
}

// syncUrl records the current state; push=true creates a history entry so Back returns here.
export function syncUrl({ push = false } = {}) {
  const url = routeFromState();
  if (url === location.pathname + location.search) return;
  if (push) history.pushState(null, "", url);
  else history.replaceState(null, "", url);
}

// The open lightbox slide lives in the hash so a link can point at one file.
export function slideFromHash() {
  const m = location.hash.match(/^#f=(.+)$/);
  return m ? decodeURIComponent(m[1]) : "";
}

export function setSlideHash(filename) {
  const base = location.pathname + location.search;
  history.replaceState(history.state, "", filename ? `${base}#f=${encodeURIComponent(filename)}` : base);
}

import { state } from "./state.js";
import { onClick, onInput } from "./actions.js";
import { toast, escapeHtml, openDialog, closeDialog, platformBadgeClass, compactNumber, safeUrl, debounce } from "./utils.js";
import { createAccount, lookupProfile } from "./api.js";
import { loadProgress } from "./sse.js";
import { goTarget } from "./nav.js";

const $ = (id) => document.getElementById(id);
const PLATFORM_NAMES = { twitter: "X", instagram: "Instagram", tiktok: "TikTok" };

export function parseProfileInput(raw) {
  const val = (raw || "").trim();
  if (!val) return { username: "", platform: null };
  const patterns = [
    [/(?:https?:\/\/)?(?:www\.)?instagram\.com\/([a-zA-Z0-9_.]+)/i, "instagram"],
    [/(?:https?:\/\/)?(?:www\.|mobile\.)?(?:twitter\.com|x\.com)\/([a-zA-Z0-9_]+)/i, "twitter"],
    [/(?:https?:\/\/)?(?:www\.|m\.)?tiktok\.com\/@([a-zA-Z0-9_.]+)/i, "tiktok"]
  ];
  for (const [re, platform] of patterns) {
    const m = val.match(re);
    if (m?.[1]) return { username: m[1], platform };
  }
  return { username: val.replace(/^@/, "").replace(/\/$/, ""), platform: null };
}

/* ---------- keyword chips, shared with the target settings form ---------- */

export const chipLists = { add: [] };

export function renderChips(kind) {
  const box = document.querySelector(`[data-chips="${kind}"]`);
  if (!box) return;
  const items = chipLists[kind];
  box.innerHTML = items.length
    ? items.map((tag, idx) => `<span class="inline-flex items-center gap-1 bg-[#ff9900]/15 border border-[#ff9900]/40 text-[#ff9900] text-xs font-semibold px-2 py-0.5 rounded-full">${escapeHtml(tag)}<button type="button" class="hover:text-rose-500 font-bold cursor-pointer" data-action="remove-chip" data-kind="${kind}" data-index="${idx}" aria-label="Remove ${escapeHtml(tag)}">&times;</button></span>`).join("")
    : `<span class="text-xs text-slate-400 dark:text-zinc-500">No keyword filters — everything is downloaded</span>`;
  box.dispatchEvent(new CustomEvent("chips-changed", { bubbles: true, detail: { kind } }));
}

document.addEventListener("keydown", (e) => {
  const input = e.target.closest?.("[data-chip-input]");
  if (!input || (e.key !== "Enter" && e.key !== ",")) return;
  e.preventDefault();
  const kind = input.dataset.chipInput;
  const val = input.value.replace(/,/g, "").trim();
  const items = chipLists[kind];
  if (val && !items.some(x => x.toLowerCase() === val.toLowerCase())) items.push(val);
  input.value = "";
  renderChips(kind);
});

onClick({
  "remove-chip": (el) => {
    chipLists[el.dataset.kind].splice(Number(el.dataset.index), 1);
    renderChips(el.dataset.kind);
  }
});

/* ---------- add dialog ---------- */

let defaultPlatform = "instagram";
let depth = "all";
let rows = [];
// lookups are cached per platform/handle for the page's lifetime; each one is a real request to the platform
const lookups = new Map();
let lookupQueue = Promise.resolve();

const rowKey = (r) => `${r.platform}/${r.username.toLowerCase()}`;

function parseRows() {
  const seen = new Set();
  const out = [];
  for (const token of $("add-input").value.split(/[\s,]+/)) {
    const parsed = parseProfileInput(token);
    if (!parsed.username || !/^[A-Za-z0-9._-]+$/.test(parsed.username)) continue;
    const row = { token, username: parsed.username, platform: parsed.platform || defaultPlatform, explicit: !!parsed.platform };
    const key = rowKey(row);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(row);
  }
  return out;
}

// lookup runs one profile check at a time so pasting twenty handles does not burst twenty requests at the platform
function lookup(row) {
  const key = rowKey(row);
  if (state.cachedProgress.some(t => t.platform === row.platform && t.username.toLowerCase() === row.username.toLowerCase())) {
    return { code: "exists" };
  }
  if (row.platform === "tiktok") return { code: "unsupported" };
  if (!lookups.has(key)) {
    const entry = { code: "pending" };
    lookups.set(key, entry);
    lookupQueue = lookupQueue.then(async () => {
      if (!$("add-dialog").open) { lookups.delete(key); return; }
      try {
        Object.assign(entry, await lookupProfile(row.platform, row.username));
      } catch (err) {
        Object.assign(entry, { code: "error", error: err.message });
      }
      renderRows();
    });
  }
  return lookups.get(key);
}

function statusLine(row, info) {
  if (row.result === "added") return ["text-emerald-600 dark:text-emerald-400", "Added"];
  if (row.result) return ["text-rose-600 dark:text-rose-400", row.result];
  const platform = PLATFORM_NAMES[row.platform];
  switch (info.code) {
    case "pending": return ["text-slate-400", "Checking profile…"];
    case "exists": return ["text-slate-400", "Already added — will be skipped"];
    case "unsupported": return ["text-slate-400", `${platform} profiles can't be previewed; checked on the first sync`];
    case "not_found": return ["text-rose-600 dark:text-rose-400", `No such account on ${platform} — will be skipped`];
    case "auth": return ["text-amber-600 dark:text-amber-400", `Can't check: the ${platform} session was rejected. Fix it in Settings → Sessions`];
    case "error": return ["text-amber-600 dark:text-amber-400", `Can't check right now (${info.error || "unknown error"})`];
  }
  const p = info.profile || {};
  const bits = [p.name, p.posts ? `${compactNumber(p.posts)} posts` : "", p.followers ? `${compactNumber(p.followers)} followers` : ""].filter(Boolean);
  if (p.private) return ["text-amber-600 dark:text-amber-400", `${bits.join(" · ")} · Private — the saved session must follow this account`];
  return ["text-slate-600 dark:text-zinc-300", bits.join(" · ") || "Found"];
}

const skipped = (info) => info.code === "exists" || info.code === "not_found";

function renderRows() {
  rows.forEach(r => { r.info = lookup(r); });
  $("add-rows").innerHTML = rows.map((r, i) => {
    const [tone, text] = statusLine(r, r.info);
    const avatar = safeUrl(r.info.profile?.avatar);
    return `
      <li class="flex items-center gap-3 p-2 rounded-lg border border-slate-200 dark:border-zinc-800 ${skipped(r.info) ? "opacity-60" : ""}">
        ${avatar
          ? `<img src="${escapeHtml(avatar)}" alt="" class="w-8 h-8 rounded-full object-cover flex-shrink-0 bg-slate-200 dark:bg-zinc-800" referrerpolicy="no-referrer"/>`
          : `<span class="w-8 h-8 rounded-full flex-shrink-0 bg-slate-200 dark:bg-zinc-800 flex items-center justify-center text-xs font-bold uppercase">${escapeHtml(r.username[0])}</span>`}
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-1.5">
            <span class="text-xs font-bold truncate">@${escapeHtml(r.username)}</span>
            ${r.explicit
              ? `<span class="text-xs font-bold uppercase px-1 py-0.5 rounded border ${platformBadgeClass(r.platform)}">${PLATFORM_NAMES[r.platform]}</span>`
              : `<button type="button" data-action="add-row-platform" data-index="${i}" class="text-xs font-bold uppercase px-1 py-0.5 rounded border cursor-pointer ${platformBadgeClass(r.platform)}" title="Switch platform">${PLATFORM_NAMES[r.platform]} ⇄</button>`}
          </div>
          <p class="text-xs truncate ${tone}" title="${escapeHtml(text)}">${r.info.code === "pending" ? '<span class="inline-block w-2.5 h-2.5 mr-1 align-middle border-2 border-slate-300 border-t-[#ff9900] rounded-full animate-spin"></span>' : ""}${escapeHtml(text)}</p>
        </div>
        <button type="button" data-action="add-row-remove" data-index="${i}" class="btn-icon p-1 text-xs" aria-label="Remove @${escapeHtml(r.username)}">✕</button>
      </li>`;
  }).join("");

  const addable = rows.filter(r => !skipped(r.info) && r.result !== "added");
  const btn = $("add-submit");
  btn.textContent = addable.length > 1 ? `Add ${addable.length} targets` : "Add target";
  btn.disabled = addable.length === 0 || !!depthError();
  const twitterRelevant = rows.length ? addable.some(r => r.platform === "twitter") : defaultPlatform === "twitter";
  document.querySelectorAll('#add-dialog [data-only="twitter"]').forEach(el => el.classList.toggle("hidden", !twitterRelevant));
}

const refresh = debounce(() => { rows = parseRows(); renderRows(); }, 400);

function setDefaultPlatform(p) {
  defaultPlatform = p;
  document.querySelectorAll("#add-platform-pills [data-plat]").forEach(b => b.setAttribute("aria-pressed", b.dataset.plat === p ? "true" : "false"));
  rows = parseRows();
  renderRows();
}

function depthError() {
  if (depth === "last") {
    const n = Number($("add-max-posts").value);
    if (!Number.isInteger(n) || n < 1 || n > 100000) return "Enter a number of posts between 1 and 100000.";
  }
  if (depth === "since") {
    const v = $("add-since").value;
    if (!v) return "Pick a date.";
    if (new Date(v) > new Date()) return "The date cannot be in the future.";
  }
  return "";
}

function setDepth(d) {
  depth = d;
  document.querySelectorAll("#add-depth [data-depth]").forEach(b => b.setAttribute("aria-pressed", b.dataset.depth === d ? "true" : "false"));
  for (const [id, on] of [["add-depth-last", d === "last"], ["add-depth-since", d === "since"]]) {
    $(id).classList.toggle("hidden", !on);
    $(id).classList.toggle("flex", on);
  }
  validateDepth();
}

function validateDepth() {
  const err = depthError();
  const el = $("add-depth-error");
  el.textContent = err;
  el.classList.toggle("hidden", !err);
  $("add-max-posts").setAttribute("aria-invalid", depth === "last" && !!err);
  $("add-since").setAttribute("aria-invalid", depth === "since" && !!err);
  renderRows();
}

export function openAddDialog(prefill = "") {
  $("add-input").value = prefill;
  chipLists.add = [];
  renderChips("add");
  $("add-since").max = new Date().toISOString().slice(0, 10);
  rows = parseRows();
  setDepth("all");
  openDialog("add-dialog");
  $("add-input").focus();
}

async function submit(e) {
  e.preventDefault();
  if (depthError()) return;
  const todo = rows.filter(r => !skipped(r.info) && r.result !== "added");
  if (!todo.length) return;
  const btn = $("add-submit");
  btn.disabled = true;
  const options = {
    save_text: $("add-save-text").checked,
    skip_retweets: $("add-skip-retweets").checked,
    download_photos: $("add-photos").checked,
    download_videos: $("add-videos").checked,
    filters: [...chipLists.add],
    since: depth === "since" ? $("add-since").value : "",
    max_posts: depth === "last" ? Number($("add-max-posts").value) : 0,
    sync_now: $("add-sync-now").checked
  };
  let added = 0;
  for (const r of todo) {
    btn.textContent = `Adding @${r.username}…`;
    try {
      await createAccount({ ...options, platform: r.platform, username: r.username });
      r.result = "added";
      added++;
    } catch (err) {
      r.result = err.message;
    }
    renderRows();
  }
  await loadProgress();
  const failed = todo.length - added;
  if (!failed) {
    closeDialog("add-dialog");
    toast(added === 1 ? `Added @${todo[0].username}` : `Added ${added} targets`, "success");
    if (added === 1) goTarget(todo[0].username, todo[0].platform);
    return;
  }
  // keep only what still needs attention in the input
  $("add-input").value = rows.filter(r => r.result !== "added").map(r => r.token).join("\n");
  toast(`Added ${added}, ${failed} failed — see the list`, "error");
}

onInput({
  "add-input": refresh
});

onClick({
  "add-platform": (el) => setDefaultPlatform(el.dataset.plat),
  "add-depth": (el) => setDepth(el.dataset.depth),
  "add-row-remove": (el) => {
    const r = rows[Number(el.dataset.index)];
    $("add-input").value = $("add-input").value.split(/\r?\n/).map(line => line.split(/[\s,]+/).filter(t => t !== r.token).join(" ")).filter(l => l.trim()).join("\n");
    rows = parseRows();
    renderRows();
  },
  "add-row-platform": (el) => {
    const r = rows[Number(el.dataset.index)];
    const order = ["instagram", "twitter", "tiktok"];
    const next = order[(order.indexOf(r.platform) + 1) % order.length];
    // a bare handle carries no platform, so rewrite the token as a link the parser understands
    const link = { instagram: `instagram.com/${r.username}`, twitter: `x.com/${r.username}`, tiktok: `tiktok.com/@${r.username}` }[next];
    $("add-input").value = $("add-input").value.split(/(\s+|,)/).map(t => (t === r.token ? link : t)).join("");
    rows = parseRows();
    renderRows();
  }
});

$("add-max-posts").addEventListener("input", validateDepth);
$("add-since").addEventListener("input", validateDepth);
$("add-account-form").addEventListener("submit", submit);
$("add-dialog").addEventListener("close", () => rows.forEach(r => { delete r.result; }));

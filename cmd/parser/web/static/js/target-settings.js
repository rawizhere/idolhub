import { state, findTarget } from "./state.js";
import { onClick } from "./actions.js";
import { toast, escapeHtml, confirmDialog, withLoading, timeAgo, formatDuration, debounce } from "./utils.js";
import { listAccounts, patchAccount, fetchRuns, fetchFilterPreview, checkSession } from "./api.js";
import { chipLists, renderChips } from "./accounts.js";
import { loadProgress } from "./sse.js";
import { cookieHealthFor } from "./overview.js";

const $ = (id) => document.getElementById(id);
const form = () => $("target-settings-form");

// the account as last loaded or saved; the form is dirty when it differs
let account = null;
let baseline = "";
let previewAbort = null;

const CONTENT_FIELDS = ["save_text", "skip_retweets", "download_photos", "download_videos", "filters", "since"];

function readForm() {
  const f = form();
  return {
    save_text: f.save_text.checked,
    skip_retweets: f.skip_retweets.checked,
    download_photos: f.download_photos.checked,
    download_videos: f.download_videos.checked,
    filters: [...chipLists.ts],
    since: f.since.value,
    max_posts: f.max_posts.value === "" ? 0 : Number(f.max_posts.value)
  };
}

function fillForm(acc) {
  const f = form();
  f.save_text.checked = acc.save_text;
  f.skip_retweets.checked = acc.skip_retweets;
  f.download_photos.checked = acc.download_photos;
  f.download_videos.checked = acc.download_videos;
  f.since.value = acc.since || "";
  f.since.max = new Date().toISOString().slice(0, 10);
  f.max_posts.value = acc.max_posts || "";
  chipLists.ts = [...acc.filters];
  renderChips("ts");
}

function validate(values) {
  const errors = {};
  if (account.platform !== "tiktok" && !values.download_photos && !values.download_videos) errors.media = "Pick at least photos or videos.";
  if (values.since && new Date(values.since) > new Date()) errors.since = "The date cannot be in the future.";
  if (!Number.isInteger(values.max_posts) || values.max_posts < 0 || values.max_posts > 100000) errors.max_posts = "Use a whole number up to 100000, or leave empty.";
  form().querySelectorAll("[data-error]").forEach(el => {
    const msg = errors[el.dataset.error];
    el.textContent = msg || "";
    el.classList.toggle("hidden", !msg);
  });
  form().since.setAttribute("aria-invalid", !!errors.since);
  form().max_posts.setAttribute("aria-invalid", !!errors.max_posts);
  return errors;
}

export function isDirty() {
  return !!account && JSON.stringify(readForm()) !== baseline;
}

function syncSaveBar() {
  if (!account) return;
  const values = readForm();
  const dirty = JSON.stringify(values) !== baseline;
  const invalid = Object.keys(validate(values)).length > 0;
  const bar = $("ts-savebar");
  bar.classList.toggle("hidden", !dirty);
  bar.classList.toggle("flex", dirty);
  $("ts-save").disabled = invalid;
  $("ts-savebar-text").textContent = invalid ? "Fix the highlighted fields" : "Unsaved changes";
}

const refreshPreview = debounce(async () => {
  const el = $("ts-filter-preview");
  if (!account || account.platform !== "twitter") return;
  if (previewAbort) previewAbort.abort();
  previewAbort = new AbortController();
  try {
    const { total, matched } = await fetchFilterPreview(account.platform, account.username, chipLists.ts, previewAbort.signal);
    if (!total) {
      el.textContent = form().save_text.checked
        ? "No saved posts yet to preview against — run a sync first."
        : "Preview needs saved post text: enable “Post text & captions” and sync.";
      return;
    }
    el.textContent = chipLists.ts.length
      ? `Would keep ${matched.toLocaleString()} of ${total.toLocaleString()} saved posts (${Math.round(matched / total * 100)}%).`
      : `No filters: all ${total.toLocaleString()} saved posts are kept.`;
  } catch (err) {
    if (err.name !== "AbortError") el.textContent = "";
  }
}, 300);

function renderRuns(runs) {
  const tone = { completed: "text-emerald-600 dark:text-emerald-400", failed: "text-rose-600 dark:text-rose-400", cancelled: "text-slate-500" };
  $("ts-runs").innerHTML = runs.length
    ? runs.map(r => {
      const took = new Date(r.finished_at) - new Date(r.started_at);
      return `
        <tr>
          <td class="py-1.5 pr-4 font-mono whitespace-nowrap" title="${escapeHtml(new Date(r.started_at).toLocaleString())}">${escapeHtml(timeAgo(r.started_at))}</td>
          <td class="py-1.5 pr-4 font-mono whitespace-nowrap">${formatDuration(took)}</td>
          <td class="py-1.5 pr-4 font-bold uppercase whitespace-nowrap ${tone[r.status] || ""}">${escapeHtml(r.status)}${r.full_sync ? ' <span class="font-normal normal-case text-slate-400">· full</span>' : ""}</td>
          <td class="py-1.5 pr-4 font-mono">${r.new_files ? `+${r.new_files}` : "0"}</td>
          <td class="py-1.5 text-slate-500 dark:text-zinc-400 max-w-xs truncate" title="${escapeHtml(r.error || "")}">${escapeHtml(r.error || "")}</td>
        </tr>`;
    }).join("")
    : `<tr><td colspan="5" class="py-3 text-slate-400">No syncs recorded yet.</td></tr>`;
}

function renderSession() {
  const st = account.cookies;
  const status = $("ts-cookie-status");
  if (!st) {
    status.className = "text-[0.6875rem] font-mono text-slate-400";
    status.textContent = "Uses the shared session";
  } else {
    const h = cookieHealthFor(account.platform, st);
    status.className = `text-[0.6875rem] font-mono ${h.tone === "ok" ? "text-emerald-600 dark:text-emerald-400" : h.tone === "warn" ? "text-amber-600" : "text-rose-600 dark:text-rose-400"}`;
    status.textContent = `Own session · ${h.text}`;
  }
  $("ts-cookies-remove").classList.toggle("hidden", !st);
  $("ts-cookies").value = "";
  $("ts-cookies-result").textContent = "";
  $("ts-cookies-check").classList.toggle("hidden", account.platform === "tiktok");
}

export function renderPauseState() {
  const target = state.target && findTarget(state.target.username, state.target.platform);
  const paused = !!target?.paused;
  const sw = $("ts-pause");
  sw.setAttribute("aria-checked", paused ? "false" : "true");
  $("ts-pause-help").textContent = paused
    ? "Paused: auto-sync skips this target. The Sync button still works."
    : "Included in every auto-sync run.";
  $("menu-pause").querySelector("span").textContent = paused ? "Resume auto-sync" : "Pause auto-sync";
}

// renderTargetSettings loads the account into the settings tab of the open target.
export async function renderTargetSettings() {
  const t = state.target;
  if (!t) return;
  try {
    const [accounts, runs] = await Promise.all([listAccounts(), fetchRuns(t.platform, t.username)]);
    account = accounts.find(a => a.platform === t.platform && a.username.toLowerCase() === t.username.toLowerCase()) || null;
    if (!account) return;
    fillForm(account);
    baseline = JSON.stringify(readForm());
    document.querySelectorAll('#target-settings [data-only="twitter"]').forEach(el => el.classList.toggle("hidden", account.platform !== "twitter"));
    $("ts-username").value = account.username;
    $("ts-rename-btn").disabled = true;
    $("ts-rename-error").classList.add("hidden");
    renderSession();
    renderPauseState();
    renderRuns(runs);
    syncSaveBar();
    refreshPreview();
  } catch (err) {
    toast(`Failed to load target settings: ${err.message}`, "error");
  }
}

// confirmLeave asks before navigation drops unsaved edits; it resolves true when leaving is fine.
export async function confirmLeave() {
  if (!isDirty() || state.view !== "settings") return true;
  const ok = await confirmDialog({
    title: "Discard unsaved changes?",
    message: `Your edits to @${account.username}'s settings have not been saved.`,
    confirmText: "Discard",
    cancelText: "Keep editing",
    tone: "danger"
  });
  if (ok) {
    fillForm(account);
    syncSaveBar();
  }
  return ok;
}

async function save(e) {
  e.preventDefault();
  const values = readForm();
  if (Object.keys(validate(values)).length) return;
  const before = JSON.parse(baseline);
  const patch = {};
  for (const [k, v] of Object.entries(values)) {
    if (JSON.stringify(v) !== JSON.stringify(before[k])) patch[k] = v;
  }
  await withLoading($("ts-save"), async () => {
    try {
      account = await patchAccount(account.platform, account.username, patch);
      fillForm(account);
      baseline = JSON.stringify(readForm());
      syncSaveBar();
      const rescan = CONTENT_FIELDS.some(k => k in patch);
      toast(rescan ? "Saved. The next sync re-scans the whole feed so older posts match the new settings." : "Saved.", "success", rescan ? 6000 : 2500);
      loadProgress();
    } catch (err) {
      toast(`Failed to save: ${err.message}`, "error");
    }
  });
  // withLoading restores the pre-click disabled state; recompute it from the saved form
  syncSaveBar();
}

async function togglePause() {
  const t = state.target && findTarget(state.target.username, state.target.platform);
  if (!t) return;
  document.querySelectorAll("[popover]:popover-open").forEach(p => p.hidePopover());
  try {
    await patchAccount(t.platform, t.username, { paused: !t.paused });
    t.paused = !t.paused;
    renderPauseState();
    toast(t.paused ? `Paused auto-sync for @${t.username}` : `Resumed auto-sync for @${t.username}`, "info", 2000);
    loadProgress();
  } catch (err) {
    toast(`Failed: ${err.message}`, "error");
  }
}

async function rename(btn) {
  const to = $("ts-username").value.trim().replace(/^@/, "");
  const err = $("ts-rename-error");
  if (!/^[A-Za-z0-9._-]+$/.test(to)) {
    err.textContent = "Only letters, digits, dot, dash and underscore.";
    err.classList.remove("hidden");
    return;
  }
  const ok = await confirmDialog({
    title: "Rename target",
    message: `Rename @${account.username} to @${to}? The download folder, saved posts and sync history move to the new handle.`,
    confirmText: "Rename",
    tone: "primary"
  });
  if (!ok) return;
  await withLoading(btn, async () => {
    try {
      const updated = await patchAccount(account.platform, account.username, { username: to });
      err.classList.add("hidden");
      toast(`Renamed to @${updated.username}`, "success");
      await loadProgress();
      const { goTarget } = await import("./nav.js");
      goTarget(updated.username, updated.platform, { push: false, view: "settings" });
    } catch (e) {
      err.textContent = e.message;
      err.classList.remove("hidden");
    }
  });
}

async function saveCookies(btn) {
  const raw = $("ts-cookies").value.trim();
  if (!raw) {
    $("ts-cookies-result").textContent = "Paste a cookie export first.";
    return;
  }
  await withLoading(btn, async () => {
    try {
      account = await patchAccount(account.platform, account.username, { cookies: raw });
      renderSession();
      toast("Own session saved for this target.", "success");
    } catch (err) {
      $("ts-cookies-result").className = "text-[0.6875rem] font-mono text-rose-600 dark:text-rose-400";
      $("ts-cookies-result").textContent = err.message;
    }
  });
}

async function removeCookies() {
  const ok = await confirmDialog({
    title: "Remove own session",
    message: `@${account.username} will be scraped with the shared session from Settings again.`,
    confirmText: "Remove",
    tone: "danger"
  });
  if (!ok) return;
  try {
    account = await patchAccount(account.platform, account.username, { cookies: "" });
    renderSession();
  } catch (err) {
    toast(`Failed: ${err.message}`, "error");
  }
}

async function checkCookies(btn) {
  const out = $("ts-cookies-result");
  await withLoading(btn, async () => {
    const res = await checkSession(account.platform, account.username).catch(err => ({ ok: false, error: err.message }));
    out.className = `text-[0.6875rem] font-mono ${res.ok ? "text-emerald-600 dark:text-emerald-400" : "text-rose-600 dark:text-rose-400"}`;
    out.textContent = res.ok ? `✓ Logged in as ${res.account}` : `✗ ${res.error}`;
  });
}

onClick({
  "toggle-pause-active": togglePause,
  "ts-discard": () => { fillForm(account); syncSaveBar(); refreshPreview(); },
  "ts-rename": rename,
  "ts-cookies-save": saveCookies,
  "ts-cookies-remove": removeCookies,
  "ts-cookies-check": checkCookies
});

chipLists.ts = [];
form().addEventListener("input", syncSaveBar);
form().addEventListener("change", () => { syncSaveBar(); refreshPreview(); });
form().addEventListener("submit", save);
document.addEventListener("chips-changed", (e) => {
  if (e.detail.kind !== "ts") return;
  syncSaveBar();
  refreshPreview();
});
$("ts-username").addEventListener("input", () => {
  $("ts-rename-btn").disabled = !account || $("ts-username").value.trim().replace(/^@/, "") === account.username;
});
window.addEventListener("beforeunload", (e) => {
  if (isDirty() && state.view === "settings") e.preventDefault();
});

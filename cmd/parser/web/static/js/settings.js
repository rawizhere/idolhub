import { state } from "./state.js";
import { onClick } from "./actions.js";
import { toast, escapeHtml, withLoading, openDialog, closeDialog, confirmDialog, formatBytes, platformBadgeClass } from "./utils.js";
import { fetchConfig, postConfig, startSyncApi, fetchIgSession, checkSession, listAccounts, fetchStorage, clearQuarantine, importAccounts } from "./api.js";
import { loadProgress } from "./sse.js";
import { cookieHealth } from "./overview.js";
import { syncScaleButtons, syncThemeButtons } from "./theme-toggle.js";

const $ = (id) => document.getElementById(id);
const dialog = () => $("settings-dialog");

const COOKIE_FIELDS = [
  { platform: "twitter", key: "twitter_cookies", label: "Twitter / X", help: "Export of x.com cookies while logged in. auth_token and ct0 are required; device cookies (kdt, twid) keep the session looking like its browser." },
  { platform: "instagram", key: "instagram_cookies", label: "Instagram", help: "Export of instagram.com cookies. sessionid is required; mid, ig_did and datr keep the scraper looking like the browser the session came from." },
  { platform: "tiktok", key: "tiktok_cookies", label: "TikTok", help: "Export of tiktok.com cookies, handed to yt-dlp. Only needed for age-restricted or private videos. TikTok has no cheap online check." }
];
const PRESETS = [0, 1, 3, 6, 12, 24];
const TONES = { ok: "text-emerald-600 dark:text-emerald-400", warn: "text-amber-600 dark:text-amber-400", bad: "text-rose-600 dark:text-rose-400", muted: "text-slate-400" };

let loaded = null;   // config as last loaded, for dirty tracking
let customInterval = false;
// cookie keys the user asked to delete; an empty textarea otherwise means "keep"
const removals = new Set();

/* ---------- tabs ---------- */

function showTab(tab) {
  document.querySelectorAll("#settings-tabs [role=tab]").forEach(b => b.setAttribute("aria-selected", b.dataset.tab === tab ? "true" : "false"));
  dialog().querySelectorAll("[data-panel]").forEach(p => {
    const on = p.dataset.panel === tab;
    p.classList.toggle("hidden", !on);
    p.classList.toggle("flex", on);
  });
  if (tab === "storage") loadStorage();
}

/* ---------- sessions ---------- */

function buildCookieFields() {
  const box = $("cookie-fields");
  if (box.childElementCount) return;
  const tpl = $("cookie-field-template");
  COOKIE_FIELDS.forEach((f, i) => {
    const node = tpl.content.firstElementChild.cloneNode(true);
    node.dataset.key = f.key;
    node.dataset.platform = f.platform;
    if (i > 0) node.classList.add("border-t", "border-slate-200", "dark:border-zinc-800/80", "pt-4");
    const id = `cookies-${f.platform}`;
    node.querySelector("textarea").id = id;
    const label = node.querySelector('[data-slot="label"]');
    label.textContent = f.label;
    label.htmlFor = id;
    node.querySelector('[data-slot="help"]').textContent = f.help;
    node.querySelector('[data-action="remove-cookies"]').dataset.key = f.key;
    const check = node.querySelector('[data-action="check-session"]');
    check.dataset.platform = f.platform;
    check.classList.toggle("hidden", f.platform === "tiktok");
    box.appendChild(node);
  });
}

function renderCookieFields() {
  $("cookie-fields").querySelectorAll(".cookie-field").forEach(node => {
    const st = state.cookieStatus?.[node.dataset.platform] || { set: false };
    const pending = removals.has(node.dataset.key);
    const h = pending ? { tone: "bad", text: "Will be removed on save" } : cookieHealth(node.dataset.platform) || { tone: "muted", text: "" };
    const status = node.querySelector('[data-slot="status"]');
    status.className = TONES[h.tone];
    status.textContent = st.set && !pending ? `${h.text} · ${st.count} cookies` : h.text;
    node.querySelector('[data-action="remove-cookies"]').classList.toggle("hidden", !st.set || pending);
    node.querySelector("textarea").placeholder = st.set
      ? "Saved. Paste a new export to replace it."
      : "# Netscape HTTP Cookie File …  or  [ {\"domain\": …} ]";
  });
}

// looksLikeCookies is a light client-side check; the server parses the export for real on save.
function cookieError(value) {
  if (!value) return "";
  if (value.startsWith("[")) {
    try { return Array.isArray(JSON.parse(value)) ? "" : "Expected a JSON array of cookies."; } catch (_) { return "Not valid JSON."; }
  }
  return value.split("\n").some(l => l.split("\t").length >= 7) ? "" : "Doesn't look like a Netscape cookie file (tab-separated lines).";
}

async function runSessionCheck(btn) {
  const platform = btn.dataset.platform;
  const out = btn.closest(".cookie-field").querySelector('[data-slot="check"]');
  if (btn.closest(".cookie-field").querySelector("textarea").value.trim()) {
    out.className = `text-xs font-mono ${TONES.warn}`;
    out.textContent = "Save first — the check uses the saved session.";
    return;
  }
  await withLoading(btn, async () => {
    const res = await checkSession(platform).catch(err => ({ ok: false, error: err.message }));
    out.className = `text-xs font-mono ${res.ok ? TONES.ok : TONES.bad}`;
    out.textContent = res.ok ? `✓ Logged in as ${res.account}` : `✗ ${res.error}`;
  });
}

/* ---------- sync ---------- */

function buildHourSelects() {
  for (const id of ["quiet-from", "quiet-to"]) {
    const sel = $(id);
    if (sel.options.length) continue;
    for (let h = 0; h < 24; h++) sel.add(new Option(`${String(h).padStart(2, "0")}:00`, String(h)));
  }
}

function intervalValue() {
  return Number($("auto-sync-interval").value || 0);
}

function renderIntervalPresets() {
  const v = intervalValue();
  const custom = customInterval || !PRESETS.includes(v);
  document.querySelectorAll("#interval-presets [data-hours]").forEach(b => {
    b.setAttribute("aria-pressed", (b.dataset.hours === "custom" ? custom : !custom && Number(b.dataset.hours) === v) ? "true" : "false");
  });
  $("interval-custom").classList.toggle("hidden", !custom);
  $("interval-custom").classList.toggle("flex", custom);
  $("interval-hint").textContent = v > 0
    ? `Every ${v} h, with a random 0–20 min offset; targets start a few minutes apart.`
    : "Targets only sync when you press Sync.";
}

function renderQuiet() {
  const on = $("quiet-enabled").checked;
  $("quiet-range").classList.toggle("opacity-50", !on);
  $("quiet-from").disabled = !on;
  $("quiet-to").disabled = !on;
}

/* ---------- storage ---------- */

async function loadStorage() {
  $("storage-summary").innerHTML = `<p class="text-xs text-slate-400 col-span-full">Measuring…</p>`;
  try {
    const st = await fetchStorage();
    const card = (label, value) => `<div class="card p-3"><span class="label">${label}</span><p class="text-sm font-bold font-mono mt-1">${value}</p></div>`;
    $("storage-summary").innerHTML = card("Used by targets", formatBytes(st.total_bytes)) +
      (st.free_bytes ? card("Free on disk", formatBytes(st.free_bytes)) : "") +
      card("Quarantine", st.quarantine.files ? `${st.quarantine.files} · ${formatBytes(st.quarantine.bytes)}` : "empty");
    const targets = [...st.targets].sort((a, b) => (b.bytes + b.thumb_bytes) - (a.bytes + a.thumb_bytes));
    $("storage-targets").innerHTML = targets.map(t => `
      <tr>
        <td class="py-1.5 pr-4 whitespace-nowrap"><span class="text-xs font-bold uppercase px-1 py-0.5 rounded border mr-1.5 ${platformBadgeClass(t.platform)}">${escapeHtml(t.platform)}</span>@${escapeHtml(t.username)}</td>
        <td class="py-1.5 pr-4 text-right font-mono">${t.files.toLocaleString()}</td>
        <td class="py-1.5 pr-4 text-right font-mono">${formatBytes(t.bytes)}</td>
        <td class="py-1.5 text-right font-mono text-slate-400">${formatBytes(t.thumb_bytes)}</td>
      </tr>`).join("") || `<tr><td colspan="4" class="py-3 text-slate-400">No targets.</td></tr>`;
    $("btn-clear-quarantine").disabled = !st.quarantine.files;
  } catch (err) {
    $("storage-summary").innerHTML = `<p class="text-xs text-rose-500 col-span-full">${escapeHtml(err.message)}</p>`;
  }
}

async function doClearQuarantine(btn) {
  const ok = await confirmDialog({ title: "Delete quarantined files", message: "These are truncated downloads kept for inspection. Their targets already re-download them on the next full sync.", confirmText: "Delete", tone: "danger" });
  if (!ok) return;
  await withLoading(btn, async () => {
    try {
      await clearQuarantine();
      toast("Quarantine cleared", "success");
      loadStorage();
    } catch (err) {
      toast(err.message, "error");
    }
  });
}

/* ---------- backup ---------- */

async function doImport(file) {
  const out = $("import-result");
  out.className = "text-xs font-mono text-slate-500";
  out.textContent = "Importing…";
  try {
    const data = JSON.parse(await file.text());
    const res = await importAccounts(data, $("import-settings").checked);
    out.className = `text-xs font-mono ${res.problems?.length ? TONES.warn : TONES.ok}`;
    out.textContent = `Added ${res.added}, skipped ${res.skipped} existing.${res.problems?.length ? ` Problems: ${res.problems.join("; ")}` : ""}`;
    loadProgress();
    if ($("import-settings").checked) await loadConfig();
  } catch (err) {
    out.className = `text-xs font-mono ${TONES.bad}`;
    out.textContent = err instanceof SyntaxError ? "That file is not JSON." : err.message;
  } finally {
    $("import-file").value = "";
  }
}

/* ---------- form state ---------- */

function readForm() {
  const cookies = {};
  $("cookie-fields").querySelectorAll(".cookie-field").forEach(node => {
    const value = node.querySelector("textarea").value.trim();
    if (value) cookies[node.dataset.key] = value;
    else if (removals.has(node.dataset.key)) cookies[node.dataset.key] = "";
  });
  const quietOn = $("quiet-enabled").checked;
  return {
    cookies,
    auto_sync_interval: intervalValue(),
    quiet_hours: quietOn ? `${$("quiet-from").value}-${$("quiet-to").value}` : "",
    max_concurrent: Number($("max-concurrent").value || 0),
    thumbnails_dir: $("thumbnails-dir").value.trim(),
    instagram_sidecar_url: $("sidecar-url").value.trim()
  };
}

function fillForm(cfg) {
  removals.clear();
  $("cookie-fields").querySelectorAll("textarea").forEach(t => { t.value = ""; });
  $("cookie-fields").querySelectorAll('[data-slot="check"]').forEach(el => { el.textContent = ""; });
  $("auto-sync-interval").value = cfg.auto_sync_interval || 0;
  customInterval = !PRESETS.includes(cfg.auto_sync_interval || 0);
  const quiet = /^(\d{1,2})-(\d{1,2})$/.exec(cfg.quiet_hours || "");
  $("quiet-enabled").checked = !!quiet;
  $("quiet-from").value = quiet ? quiet[1] : "1";
  $("quiet-to").value = quiet ? quiet[2] : "7";
  $("max-concurrent").value = cfg.max_concurrent || 3;
  $("thumbnails-dir").value = cfg.thumbnails_dir || "";
  $("sidecar-url").value = cfg.instagram_sidecar_url || "";
  renderIntervalPresets();
  renderQuiet();
  renderCookieFields();
}

function snapshot(cfg) {
  return JSON.stringify({
    cookies: {},
    auto_sync_interval: cfg.auto_sync_interval || 0,
    quiet_hours: cfg.quiet_hours || "",
    max_concurrent: cfg.max_concurrent || 3,
    thumbnails_dir: cfg.thumbnails_dir || "",
    instagram_sidecar_url: cfg.instagram_sidecar_url || ""
  });
}

function validate(values) {
  const errors = {};
  const iv = values.auto_sync_interval;
  if (!Number.isInteger(iv) || iv < 0 || iv > 168) errors.interval = "Use a whole number of hours from 1 to 168, or Off.";
  if (values.quiet_hours) {
    const [a, b] = values.quiet_hours.split("-");
    if (a === b) errors.quiet = "Start and end must differ.";
  }
  const mc = values.max_concurrent;
  if (!Number.isInteger(mc) || mc < 1 || mc > 8) errors.concurrency = "Choose 1 to 8.";
  if (values.instagram_sidecar_url && !/^https?:\/\/[^\s]+$/.test(values.instagram_sidecar_url)) errors.sidecar = "Use a full http(s):// address.";
  dialog().querySelectorAll("[data-error]").forEach(el => {
    const msg = errors[el.dataset.error];
    el.textContent = msg || "";
    el.classList.toggle("hidden", !msg);
  });
  $("auto-sync-interval").setAttribute("aria-invalid", !!errors.interval);
  $("max-concurrent").setAttribute("aria-invalid", !!errors.concurrency);
  $("sidecar-url").setAttribute("aria-invalid", !!errors.sidecar);
  $("cookie-fields").querySelectorAll(".cookie-field").forEach(node => {
    const ta = node.querySelector("textarea");
    const msg = cookieError(ta.value.trim());
    if (msg) errors[node.dataset.key] = msg;
    const el = node.querySelector('[data-slot="error"]');
    el.textContent = msg;
    el.classList.toggle("hidden", !msg);
    ta.setAttribute("aria-invalid", !!msg);
  });
  return errors;
}

function isDirty() {
  return !!loaded && JSON.stringify(readForm()) !== snapshot(loaded);
}

function syncFooter() {
  const values = readForm();
  const errors = validate(values);
  const dirty = isDirty();
  const invalid = Object.keys(errors).length > 0;
  $("settings-save").disabled = !dirty || invalid;
  const label = $("settings-dirty");
  label.textContent = invalid ? "Fix the highlighted fields" : dirty ? "Unsaved changes" : "No changes";
  label.className = `text-xs ${invalid ? TONES.bad : dirty ? TONES.warn : "text-slate-500 dark:text-zinc-400"}`;
}

export async function loadConfig() {
  try {
    const data = await fetchConfig();
    state.cookieStatus = data.cookie_status || {};
    loaded = data;
    buildCookieFields();
    buildHourSelects();
    fillForm(data);
    syncFooter();
  } catch (err) {
    toast(`Failed to load settings: ${err.message}`, "error");
  }
}

export async function openSettings(focusPlatform) {
  openDialog("settings-dialog");
  showTab("sessions");
  syncScaleButtons();
  syncThemeButtons();
  await loadConfig();
  fetchIgSession().then(st => {
    $("ig-session-status").textContent = st.session === "sidecar-not-configured" ? "" :
      (st.session === "ok" ? `Instagram browser sidecar: ok (${st.cookies} cookies)` : `Instagram browser sidecar: ${st.session}`);
  }).catch(() => {});
  listAccounts().then(accs => {
    const own = accs.filter(a => a.cookies).map(a => `@${a.username}`);
    $("own-session-count").textContent = own.length ? `Targets with their own session (set in each target's Settings tab): ${own.join(", ")}` : "";
  }).catch(() => {});
  if (focusPlatform) $(`cookies-${focusPlatform}`)?.focus();
}

async function saveSettings(e) {
  e.preventDefault();
  const values = readForm();
  if (Object.keys(validate(values)).length || !isDirty()) return;
  await withLoading($("settings-save"), async () => {
    try {
      // only the fields this form owns; the account list and untouched secrets stay as stored
      await postConfig({
        ...values.cookies,
        auto_sync_interval: values.auto_sync_interval,
        quiet_hours: values.quiet_hours,
        max_concurrent: values.max_concurrent,
        thumbnails_dir: values.thumbnails_dir,
        instagram_sidecar_url: values.instagram_sidecar_url
      });
      toast("Settings saved.", "success");
      await loadConfig();
      loadProgress();
    } catch (err) {
      toast(`Failed to save settings: ${err.message}`, "error");
    }
  });
  // withLoading restores the pre-click disabled state; recompute it from the saved form
  syncFooter();
}

async function fullResyncAll() {
  const ok = await confirmDialog({
    title: "Force Full Resync of All Targets",
    message: "Every configured target is rescanned from the beginning of its feed. Existing files are preserved.",
    confirmText: "Start Full Resync",
    tone: "primary",
  });
  if (!ok) return;
  try {
    await startSyncApi("all", true);
    toast("Full resync started for all targets.", "success", 2500);
  } catch (err) {
    toast(`Failed to start full resync: ${err.message}`, "error");
  }
}

onClick({
  "open-settings": () => openSettings(),
  "settings-tab": (el) => showTab(el.dataset.tab),
  "remove-cookies": (el) => { removals.add(el.dataset.key); renderCookieFields(); syncFooter(); },
  "check-session": runSessionCheck,
  "interval-preset": (el) => {
    customInterval = el.dataset.hours === "custom";
    if (!customInterval) $("auto-sync-interval").value = el.dataset.hours;
    else if (!intervalValue()) $("auto-sync-interval").value = 2;
    renderIntervalPresets();
    syncFooter();
    if (customInterval) $("auto-sync-interval").focus();
  },
  "clear-quarantine": doClearQuarantine,
  "full-resync-all": fullResyncAll
});

const form = $("settings-form");
form.addEventListener("submit", saveSettings);
form.addEventListener("input", () => { renderIntervalPresets(); syncFooter(); });
form.addEventListener("change", () => { renderQuiet(); syncFooter(); });
$("import-file").addEventListener("change", (e) => { if (e.target.files[0]) doImport(e.target.files[0]); });

// Esc, the backdrop and the close button all arrive as "cancel"; hold it while edits are unsaved
dialog().addEventListener("cancel", async (e) => {
  if (!isDirty()) return;
  e.preventDefault();
  const ok = await confirmDialog({ title: "Discard unsaved settings?", message: "Your changes have not been saved.", confirmText: "Discard", cancelText: "Keep editing", tone: "danger" });
  if (ok) {
    fillForm(loaded);
    syncFooter();
    closeDialog("settings-dialog");
  }
});

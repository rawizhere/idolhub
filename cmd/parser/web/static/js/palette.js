import { state } from "./state.js";
import { onClick } from "./actions.js";
import { escapeHtml, openDialog, closeDialog, platformBadgeClass } from "./utils.js";
import { startSyncApi } from "./api.js";
import { goTarget, goOverview } from "./nav.js";
import { openAddDialog, parseProfileInput } from "./accounts.js";
import { openSettings } from "./settings.js";
import { toggleBottomConsole } from "./dock.js";
import { toggleTheme, stepScale, setScale } from "./theme-toggle.js";

const $ = (id) => document.getElementById(id);
let items = [];
let active = 0;

function commands(query) {
  const q = query.trim().toLowerCase();
  const out = [];
  const parsed = parseProfileInput(query);
  if (parsed.platform) {
    out.push({ label: `Add @${parsed.username} (${parsed.platform})`, hint: "new target", run: () => openAddDialog(query) });
  }
  const actions = [
    { label: "Add target…", hint: "action", run: () => openAddDialog(q && !parsed.platform ? query : "") },
    { label: "Overview", hint: "go to", run: () => goOverview() },
    { label: "Sync all targets", hint: "action", run: () => startSyncApi("all") },
    { label: "Settings & cookies", hint: "open", run: () => openSettings() },
    { label: "Toggle activity log", hint: "⌘J", run: () => toggleBottomConsole() },
    { label: "Toggle dark mode", hint: "action", run: toggleTheme },
    { label: "Interface larger", hint: "scale", run: () => stepScale(1) },
    { label: "Interface smaller", hint: "scale", run: () => stepScale(-1) },
    { label: "Interface default size", hint: "scale", run: () => setScale(1) },
    { label: "Keyboard shortcuts", hint: "?", run: () => openDialog("shortcuts-dialog") }
  ];
  const targets = state.cachedProgress.map(t => ({
    label: `@${t.username}`,
    badge: t.platform,
    hint: `${(t.media_count || 0).toLocaleString()} files`,
    search: `${t.username} ${t.platform}`,
    run: () => goTarget(t.username, t.platform)
  }));
  const matches = (c) => !q || (c.search || c.label).toLowerCase().includes(q);
  // with a query, targets first: jumping is the common case
  if (q) out.push(...targets.filter(matches), ...actions.filter(matches));
  else out.push(...actions, ...targets);
  return out.slice(0, 50);
}

function render() {
  items = commands($("palette-input").value);
  active = Math.min(active, Math.max(0, items.length - 1));
  $("palette-list").innerHTML = items.length
    ? items.map((c, i) => `
      <li role="option" id="palette-opt-${i}" aria-selected="${i === active}" data-index="${i}"
          class="flex items-center gap-2 px-3 py-2 rounded-lg text-sm cursor-pointer ${i === active ? "bg-[#ff9900]/15 text-slate-900 dark:text-white" : "text-slate-700 dark:text-zinc-300"}">
        <span class="flex-1 truncate font-medium">${escapeHtml(c.label)}</span>
        ${c.badge ? `<span class="text-xs font-bold uppercase px-1 py-0.5 rounded border ${platformBadgeClass(c.badge)}">${escapeHtml(c.badge)}</span>` : ""}
        <span class="text-xs text-slate-400 font-mono">${escapeHtml(c.hint || "")}</span>
      </li>`).join("")
    : `<li class="px-3 py-6 text-center text-xs text-slate-400">No matches. Paste a profile link to add a target.</li>`;
  $("palette-input").setAttribute("aria-activedescendant", items.length ? `palette-opt-${active}` : "");
  $(`palette-opt-${active}`)?.scrollIntoView({ block: "nearest" });
}

function run(i) {
  const c = items[i];
  if (!c) return;
  closeDialog("palette-dialog");
  c.run();
}

export function openPalette() {
  const input = $("palette-input");
  input.value = "";
  active = 0;
  render();
  openDialog("palette-dialog");
  input.focus();
}

$("palette-input").addEventListener("input", () => { active = 0; render(); });
$("palette-input").addEventListener("keydown", (e) => {
  if (e.key === "ArrowDown") { e.preventDefault(); active = (active + 1) % Math.max(1, items.length); render(); }
  else if (e.key === "ArrowUp") { e.preventDefault(); active = (active - 1 + items.length) % Math.max(1, items.length); render(); }
  else if (e.key === "Enter") { e.preventDefault(); run(active); }
});
$("palette-list").addEventListener("click", (e) => {
  const li = e.target.closest("[data-index]");
  if (li) run(Number(li.dataset.index));
});

onClick({
  "open-palette": openPalette,
  "open-shortcuts": () => openDialog("shortcuts-dialog")
});

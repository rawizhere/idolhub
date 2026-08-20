import { state } from "./state.js";
import { toast } from "./utils.js";

export function toggleBottomConsole(forceState) {
  state.dockConsoleOpen = forceState !== undefined ? forceState : !state.dockConsoleOpen;
  const terminalBody = document.getElementById("terminal-body");
  const filters = document.getElementById("dock-terminal-level-filters");
  const chevron = document.getElementById("dock-toggle-chevron");

  if (terminalBody) terminalBody.classList.toggle("hidden", !state.dockConsoleOpen);
  if (filters) {
    filters.classList.toggle("hidden", !state.dockConsoleOpen);
    filters.classList.toggle("flex", state.dockConsoleOpen);
  }
  if (chevron) chevron.style.transform = state.dockConsoleOpen ? "rotate(180deg)" : "rotate(0deg)";
}

export function setTerminalLevel(level) {
  state.terminalLevel = level;
  document.querySelectorAll(".dock-filter-btn").forEach(btn => {
    const active = btn.dataset.level === level;
    btn.classList.toggle("active", active);
    btn.classList.toggle("bg-white", active);
    btn.classList.toggle("dark:bg-zinc-800", active);
    btn.classList.toggle("text-slate-900", active);
    btn.classList.toggle("dark:text-white", active);
    btn.classList.toggle("shadow-2xs", active);
  });
  updateTerminal();
}

export function updateTerminal() {
  const terminal = document.getElementById("terminal-body");
  if (!terminal) return;

  const isTargetView = Boolean(state.activeTerminalUser);
  const target = isTargetView ? state.cachedProgress.find(t => t.username === state.activeTerminalUser) : null;
  const lines = isTargetView ? (target?.logs || []) : (state.globalLogs || []);

  let filtered = lines;
  if (state.terminalLevel !== "all") {
    filtered = lines.filter(l => l && l.level && l.level.toLowerCase() === state.terminalLevel);
  }

  terminal.innerHTML = "";
  if (filtered.length === 0) {
    terminal.innerHTML = isTargetView
      ? `<div class="text-zinc-500">[SYSTEM] No log entries for active target @${state.activeTerminalUser}.</div>`
      : `<div class="text-zinc-500">[SYSTEM] Live logger initialized. Select a target or run sync.</div>`;
    return;
  }

  filtered.forEach(log => {
    if (!log || !log.level) return;
    const logTime = new Date(log.timestamp).toLocaleTimeString();
    let colorClass = "text-zinc-300";
    const level = log.level.toLowerCase();
    if (level === "error") colorClass = "text-rose-400 font-semibold";
    else if (level === "info") colorClass = "text-emerald-400";
    else if (level === "warn") colorClass = "text-amber-400 font-medium";

    const div = document.createElement("div");
    div.className = `whitespace-pre-wrap break-all ${colorClass}`;
    div.innerText = `[${logTime}] [${log.level.toUpperCase()}] ${log.message || ""}`;
    terminal.appendChild(div);
  });

  terminal.scrollTop = terminal.scrollHeight;
}

export function copyTerminalLogs() {
  const isTargetView = Boolean(state.activeTerminalUser);
  const target = isTargetView ? state.cachedProgress.find(t => t.username === state.activeTerminalUser) : null;
  const lines = isTargetView ? (target?.logs || []) : (state.globalLogs || []);
  if (!lines || lines.length === 0) {
    toast("No logs available to copy.", "info");
    return;
  }
  const text = lines.map(l => `[${new Date(l.timestamp).toLocaleTimeString()}] [${l.level}] ${l.message}`).join("\n");
  navigator.clipboard.writeText(text).then(
    () => toast("Logs copied to clipboard.", "success", 2000),
    () => toast("Failed to copy logs.", "error")
  );
}

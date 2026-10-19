import { state, activeTarget } from "./state.js";
import { onClick } from "./actions.js";
import { toast } from "./utils.js";

export function toggleBottomConsole(forceState) {
  state.dockConsoleOpen = forceState !== undefined ? forceState : !state.dockConsoleOpen;
  const open = state.dockConsoleOpen;
  document.getElementById("bottom-dock-console").classList.toggle("is-open", open);
  const body = document.getElementById("terminal-body");
  body.classList.toggle("hidden", !open);
  body.classList.toggle("flex", open);
  const filters = document.getElementById("dock-terminal-level-filters");
  filters.classList.toggle("hidden", !open);
  filters.classList.toggle("flex", open);
  document.getElementById("dock-toggle-chevron").style.transform = open ? "rotate(180deg)" : "";
  if (open) updateTerminal();
}

export function setTerminalLevel(level) {
  state.terminalLevel = level;
  document.querySelectorAll("#dock-terminal-level-filters [data-level]").forEach(b => b.setAttribute("aria-pressed", b.dataset.level === level ? "true" : "false"));
  updateTerminal();
}

function currentLines() {
  return state.target ? (activeTarget()?.logs || []) : (state.globalLogs || []);
}

const LEVEL_CLASS = { error: "text-rose-400 font-semibold", info: "text-emerald-400", warn: "text-amber-400 font-medium" };

function logLine(log) {
  const div = document.createElement("div");
  const level = (log.level || "info").toLowerCase();
  div.className = `whitespace-pre-wrap break-all ${LEVEL_CLASS[level] || "text-zinc-300"}`;
  div.textContent = `[${new Date(log.timestamp).toLocaleTimeString()}] [${level.toUpperCase()}] ${log.message || ""}`;
  return div;
}

const matchesLevel = (log) => state.terminalLevel === "all" || (log.level || "").toLowerCase() === state.terminalLevel;

// updateTerminal redraws the console; it is skipped while the console is closed.
export function updateTerminal() {
  const terminal = document.getElementById("terminal-body");
  if (!terminal || !state.dockConsoleOpen) return;
  const lines = currentLines().filter(l => l && matchesLevel(l));
  if (lines.length === 0) {
    terminal.replaceChildren();
    const empty = document.createElement("div");
    empty.className = "text-zinc-500";
    empty.textContent = state.target ? `[SYSTEM] No log entries for @${state.target.username}.` : "[SYSTEM] Live logger ready. Select a target or run a sync.";
    terminal.appendChild(empty);
    return;
  }
  terminal.replaceChildren(...lines.slice(-500).map(logLine));
  terminal.scrollTop = terminal.scrollHeight;
}

// appendTerminalLine adds one live line without redrawing the whole console.
export function appendTerminalLine(log) {
  const terminal = document.getElementById("terminal-body");
  if (!terminal || !state.dockConsoleOpen || !matchesLevel(log)) return;
  const stick = terminal.scrollHeight - terminal.scrollTop - terminal.clientHeight < 40;
  if (terminal.firstElementChild?.classList.contains("text-zinc-500")) terminal.replaceChildren();
  terminal.appendChild(logLine(log));
  while (terminal.childElementCount > 500) terminal.firstElementChild.remove();
  if (stick) terminal.scrollTop = terminal.scrollHeight;
}

function copyTerminalLogs() {
  const lines = currentLines();
  if (!lines.length) { toast("No logs available to copy.", "info"); return; }
  if (!navigator.clipboard?.writeText) { toast("Clipboard is not available in this browser.", "error"); return; }
  const text = lines.map(l => `[${new Date(l.timestamp).toLocaleTimeString()}] [${l.level}] ${l.message}`).join("\n");
  navigator.clipboard.writeText(text).then(
    () => toast("Logs copied to clipboard.", "success", 2000),
    () => toast("Failed to copy logs.", "error")
  );
}

onClick({
  "toggle-dock": () => toggleBottomConsole(),
  "log-level": (el) => setTerminalLevel(el.dataset.level),
  "copy-logs": copyTerminalLogs
});

import { state } from "./state.js";
import { onClick } from "./actions.js";
import { openDialog } from "./utils.js";
import { initSSE, pollProgress, whenReady } from "./sse.js";
import { startCountdownTicker } from "./overview.js";
import { visibleTargets } from "./sidebar.js";
import { setView, setSelecting, syncFilterUI } from "./gallery.js";
import { toggleBottomConsole, setTerminalLevel } from "./dock.js";
import { loadConfig } from "./settings.js";
import { openAddDialog } from "./accounts.js";
import { openPalette } from "./palette.js";
import { applyRoute, goTarget } from "./nav.js";
import "./theme-toggle.js";

const scrollArea = document.getElementById("workspace-scroll-area");

onClick({
  "scroll-top": () => scrollArea.scrollTo({ top: 0, behavior: "smooth" }),
  "open-add": () => openAddDialog()
});

function stepTarget(delta) {
  const list = visibleTargets();
  if (!list.length) return;
  const i = state.target ? list.findIndex(t => t.username === state.target.username && t.platform === state.target.platform) : -1;
  const next = list[(i + delta + list.length) % list.length];
  goTarget(next.username, next.platform);
  document.querySelector(`[data-target-key="${CSS.escape(`${next.platform}/${next.username}`)}"]`)?.scrollIntoView({ block: "nearest" });
}

window.addEventListener("keydown", (e) => {
  const mod = e.ctrlKey || e.metaKey;
  if (mod && e.key.toLowerCase() === "k") {
    e.preventDefault();
    openPalette();
    return;
  }
  if (mod && e.key.toLowerCase() === "j") {
    e.preventDefault();
    toggleBottomConsole();
    return;
  }
  // dialogs, popovers and the lightbox handle their own keys
  if (document.querySelector("dialog[open], [popover]:popover-open, .pswp--open")) return;

  if (e.key === "Escape") {
    if (state.selecting) setSelecting(false);
    else if (state.dockConsoleOpen) toggleBottomConsole(false);
    return;
  }

  const tag = (e.target.tagName || "").toLowerCase();
  if (tag === "input" || tag === "textarea" || tag === "select" || e.target.isContentEditable || mod || e.altKey) return;

  switch (e.key) {
    case "/":
      e.preventDefault();
      document.getElementById("search-input").focus();
      break;
    case "?":
      openDialog("shortcuts-dialog");
      break;
    case "j":
      stepTarget(1);
      break;
    case "k":
      stepTarget(-1);
      break;
    case "g":
      if (state.target) setView(state.view === "grid" ? "posts" : "grid");
      break;
    case "s":
      if (state.target && state.view === "grid") setSelecting(!state.selecting);
      break;
  }
});

scrollArea.addEventListener("scroll", () => {
  const show = scrollArea.scrollTop > 300;
  const btn = document.getElementById("btn-back-to-top");
  btn.classList.toggle("scale-0", !show);
  btn.classList.toggle("opacity-0", !show);
}, { passive: true });

whenReady(applyRoute);
syncFilterUI();
setTerminalLevel("all");
loadConfig();
pollProgress();
initSSE();
startCountdownTicker();

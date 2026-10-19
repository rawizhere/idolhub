import { state, findTarget } from "./state.js";
import { onClick, onInput } from "./actions.js";
import { debounce } from "./utils.js";
import { readRoute, syncUrl } from "./router.js";
import { showTarget, setQuery, setSelecting } from "./gallery.js";
import { renderDashboardSidebar, showTargetChrome, hideTargetChrome, closeSidebar } from "./sidebar.js";
import { renderOverviewDashboard, renderGlobalSearchResults } from "./overview.js";
import { updateTerminal } from "./dock.js";
import { confirmLeave } from "./target-settings.js";

const $ = (id) => document.getElementById(id);

function setSearchBox(value) {
  const input = $("search-input");
  input.value = value;
  input.placeholder = state.target ? `Search @${state.target.username} captions, files, dates… (/)` : "Search library… (/)";
  $("search-clear").classList.toggle("hidden", !value);
}

// goTarget opens a target with fresh filters and records a history entry; view opens a specific tab.
export async function goTarget(username, platform, { push = true, view = "" } = {}) {
  const target = findTarget(username, platform);
  if (!target || !(await confirmLeave())) return;
  closeSidebar();
  enterTarget(target, view ? { ...readRoute(""), view } : null);
  syncUrl({ push });
}

function enterTarget(target, route) {
  const wasOther = !state.target || state.target.username !== target.username || state.target.platform !== target.platform;
  state.target = state.target && !wasOther ? state.target : null;
  showTargetChrome(target);
  showTarget(target, route).then(() => setSearchBox(state.query));
  setSearchBox(route ? route.query : (wasOther ? "" : state.query));
  renderDashboardSidebar();
  updateTerminal();
  if (wasOther) $("workspace-scroll-area").scrollTop = 0;
}

export async function goOverview({ push = true, query = "" } = {}) {
  if (!(await confirmLeave())) return;
  setSelecting(false);
  state.target = null;
  state.query = query;
  hideTargetChrome();
  setSearchBox(query);
  renderDashboardSidebar();
  updateTerminal();
  if (query) renderGlobalSearchResults(query);
  else renderOverviewDashboard();
  syncUrl({ push });
}

// applyRoute renders whatever the URL describes: on first load and on Back/Forward.
export function applyRoute() {
  const route = readRoute();
  const target = route.target && findTarget(route.target.username, route.target.platform);
  if (target) enterTarget(target, route);
  else goOverview({ push: false, query: route.query });
}

const runSearch = debounce((value) => {
  if (state.target) {
    setQuery(value);
    return;
  }
  state.query = value;
  syncUrl();
  if (value) renderGlobalSearchResults(value);
  else renderOverviewDashboard();
}, 200);

onInput({
  "search": (el) => {
    const v = el.value.trim();
    $("search-clear").classList.toggle("hidden", !v);
    runSearch(v);
  }
});

onClick({
  "go-overview": () => goOverview(),
  "clear-search": () => {
    setSearchBox("");
    runSearch("");
    $("search-input").focus();
  }
});

window.addEventListener("popstate", async () => {
  if (!state.cachedProgress.length) return;
  if (!(await confirmLeave())) {
    // the browser already moved; put the settings URL back
    syncUrl({ push: true });
    return;
  }
  applyRoute();
});

import { state } from "./state.js";
import { toast, escapeHtml, withLoading, confirmDialog } from "./utils.js";
import { fetchConfig, postConfig, startSyncApi } from "./api.js";
import { loadProgress } from "./sse.js";
import { selectTerminalUser } from "./sidebar.js";

export function parseProfileInput(raw) {
  let val = (raw || "").trim();
  if (!val) return { username: "", platform: null };

  const igMatch = val.match(/(?:https?:\/\/)?(?:www\.)?instagram\.com\/([a-zA-Z0-9_.]+)/i);
  if (igMatch && igMatch[1]) {
    return { username: igMatch[1].replace(/\/$/, ""), platform: "instagram" };
  }

  const twMatch = val.match(/(?:https?:\/\/)?(?:www\.)?(?:twitter\.com|x\.com)\/([a-zA-Z0-9_]+)/i);
  if (twMatch && twMatch[1]) {
    return { username: twMatch[1].replace(/\/$/, ""), platform: "twitter" };
  }

  const ttMatch = val.match(/(?:https?:\/\/)?(?:www\.)?tiktok\.com\/@([a-zA-Z0-9_.]+)/i);
  if (ttMatch && ttMatch[1]) {
    return { username: ttMatch[1].replace(/\/$/, ""), platform: "tiktok" };
  }

  return { username: val.replace(/^@/, "").replace(/\/$/, ""), platform: null };
}

export function handleUsernameInput(el) {
  const raw = el.value.trim();
  if (!raw) return;

  const parsed = parseProfileInput(raw);
  if (parsed.platform) {
    el.value = parsed.username;
    const plat = document.getElementById("platform");
    if (plat) {
      plat.value = parsed.platform;
      toggleTwitterOptions();
    }
    updatePlatformPillUI(parsed.platform);
  }
}

export function updatePlatformPillUI(platform) {
  document.querySelectorAll("#add-platform-pills [data-plat]").forEach(pill => {
    const active = pill.dataset.plat === platform;
    pill.className = active
      ? "py-2 px-3 rounded-lg border text-xs font-bold transition-all cursor-pointer text-center bg-[#ff9900] text-black dark:text-black border-[#ff9900] shadow-xs"
      : "py-2 px-3 rounded-lg border text-xs font-semibold transition-all cursor-pointer text-center bg-white dark:bg-zinc-900 text-slate-600 dark:text-zinc-400 border-slate-200 dark:border-zinc-800";
  });
}

export function selectAddPlatform(platform) {
  const platInput = document.getElementById("platform");
  if (platInput) platInput.value = platform;
  updatePlatformPillUI(platform);
  toggleTwitterOptions();
}

export function toggleCmdPalette() {
  const p = document.getElementById("cmd-palette-modal");
  if (p) {
    const isHidden = p.style.display === "none" || !p.style.display;
    p.style.display = isHidden ? "flex" : "none";
    if (isHidden) {
      setTimeout(() => {
        const u = document.getElementById("username");
        if (u) { u.focus(); u.select(); }
      }, 50);
      toggleTwitterOptions();
    }
  }
}

export function toggleTwitterOptions() {
  const sel = document.getElementById("platform");
  const c = document.getElementById("save-text-container");
  if (!sel || !c) return;
  const isTwitter = sel.value === "twitter";
  const isTiktok = sel.value === "tiktok";

  c.classList.remove("hidden");
  c.classList.add("flex");
  document.querySelectorAll("#save-text-container .twitter-only-option").forEach(el => {
    el.style.display = isTwitter ? "" : "none";
  });
  if (isTiktok) c.classList.add("hidden");
}

export function handleTagInputKey(e) {
  if (e.key === "Enter" || e.key === ",") {
    e.preventDefault();
    addNewFilter();
  }
}

export function addNewFilter() {
  const input = document.getElementById("new-tag-input");
  const val = input.value.replace(/,/g, "").trim();
  if (val && !state.newFilters.includes(val)) {
    state.newFilters.push(val);
    input.value = "";
    renderNewFilters();
  }
}

export function removeNewFilter(idx) {
  state.newFilters = state.newFilters.filter((_, i) => i !== idx);
  renderNewFilters();
}

export function renderNewFilters() {
  const list = document.getElementById("new-filters-list");
  if (!list) return;
  list.innerHTML = "";
  state.newFilters.forEach((tag, idx) => {
    const chip = document.createElement("span");
    chip.className = "inline-flex items-center gap-1 bg-[#ff9900]/15 border border-[#ff9900]/40 text-[#ff9900] text-[10px] font-semibold px-2 py-0.5 rounded-full";
    chip.innerHTML = `<span>${escapeHtml(tag)}</span><button type="button" class="hover:text-white font-bold cursor-pointer" onclick="window.removeNewFilter(${idx})">&times;</button>`;
    list.appendChild(chip);
  });
}

export async function addAccount(e) {
  if (e) e.preventDefault();
  const username = document.getElementById("username").value.trim();
  const platform = document.getElementById("platform").value;
  const saveText = document.getElementById("save-text").checked;
  const skipRetweets = document.getElementById("skip-retweets").checked;
  const downloadPhotos = document.getElementById("download-photos").checked;
  const downloadVideos = document.getElementById("download-videos").checked;

  if (!username) {
    toast("Please enter a username or profile URL", "error");
    return;
  }

  const btn = document.querySelector("#add-account-form button[type='submit']");
  await withLoading(btn, async () => {
    try {
      const current = await fetchConfig();
      if (!current.accounts) current.accounts = [];

      const exists = current.accounts.some(acc => acc.username.toLowerCase() === username.toLowerCase());
      if (exists) {
        toast(`Account @${username} already exists`, "error");
        return;
      }

      current.accounts.push({
        username,
        platform,
        save_text: saveText,
        skip_retweets: skipRetweets,
        download_photos: downloadPhotos,
        download_videos: downloadVideos,
        filters: [...state.newFilters]
      });

      await postConfig(current);
      toast(`Added @${username} successfully`, "success");

      document.getElementById("username").value = "";
      state.newFilters = [];
      renderNewFilters();
      toggleCmdPalette();

      loadProgress();
      selectTerminalUser(username);
    } catch (err) {
      toast(`Failed to add account: ${err.message}`, "error");
    }
  });
}

export function closeEditModal() {
  document.getElementById("edit-modal").style.display = "none";
}

export function addEditFilter() {
  const input = document.getElementById("new-edit-tag-input");
  const val = input.value.replace(/,/g, "").trim();
  if (val && !state.editConfig.filters.includes(val)) {
    state.editConfig.filters.push(val);
    input.value = "";
    renderEditFilters();
  }
}

export function removeEditFilter(idx) {
  state.editConfig.filters = state.editConfig.filters.filter((_, i) => i !== idx);
  renderEditFilters();
}

export function renderEditFilters() {
  const list = document.getElementById("edit-filters-list");
  const noLabel = document.getElementById("edit-no-filters");
  if (!list) return;
  list.querySelectorAll(".edit-filter-chip").forEach(e => e.remove());
  if (state.editConfig.filters.length === 0) {
    if (noLabel) noLabel.style.display = "inline";
    return;
  }
  if (noLabel) noLabel.style.display = "none";
  state.editConfig.filters.forEach((tag, idx) => {
    const chip = document.createElement("span");
    chip.className = "edit-filter-chip inline-flex items-center gap-1 bg-[#ff9900]/15 border border-[#ff9900]/40 text-[#ff9900] text-[10px] font-semibold px-2 py-0.5 rounded-full";
    chip.innerHTML = `<span>${escapeHtml(tag)}</span><button type="button" class="hover:text-white font-bold cursor-pointer" onclick="window.removeEditFilter(${idx})">&times;</button>`;
    list.appendChild(chip);
  });
}

export async function saveEditChanges() {
  const username = state.editConfig.username;
  const platform = state.editConfig.platform;
  const saveText = document.getElementById("edit-save-text").checked;
  const skipRetweets = document.getElementById("edit-skip-retweets").checked;
  const downloadPhotos = document.getElementById("edit-download-photos").checked;
  const downloadVideos = document.getElementById("edit-download-videos").checked;
  const filters = [...state.editConfig.filters];

  try {
    const current = await fetchConfig();
    const idx = current.accounts.findIndex(acc => acc.username.toLowerCase() === username.toLowerCase());
    if (idx !== -1) {
      current.accounts[idx] = {
        username,
        platform,
        save_text: saveText,
        skip_retweets: skipRetweets,
        download_photos: downloadPhotos,
        download_videos: downloadVideos,
        filters: filters
      };
      await postConfig(current);
      toast(`Updated @${username}`, "success");
      loadProgress();
      selectTerminalUser(username);
    }
  } catch (err) {
    toast(`Failed to update @${username}: ${err.message}`, "error");
  }
  closeEditModal();
}

export async function fullResyncTarget() {
  const username = state.editConfig.username;
  const ok = await confirmDialog({
    title: "Force Full Resync",
    message: `Fully rescan @${username} from the beginning of its feed? Existing files will be preserved.`,
    confirmText: "Start Full Resync",
    tone: "primary",
  });
  if (!ok) return;

  try {
    await startSyncApi(username, true);
    toast(`Full resync started for @${username}.`, "success", 2500);
    loadProgress();
  } catch (err) {
    toast(`Failed to start full resync: ${err.message}`, "error");
  }
  closeEditModal();
}

export function toggleSettingsModal() {
  const s = document.getElementById("settings-modal");
  if (s) s.style.display = (s.style.display === "none" || !s.style.display) ? "flex" : "none";
}

export function openTokenSettings(platform) {
  toggleSettingsModal();
  setTimeout(() => {
    if (platform === "instagram") {
      const el = document.getElementById("instagram-session-id");
      if (el) { el.focus(); el.select(); }
    } else if (platform === "twitter") {
      const el = document.getElementById("twitter-auth-token");
      if (el) { el.focus(); el.select(); }
    } else if (platform === "tiktok") {
      const el = document.getElementById("tiktok-cookies");
      if (el) { el.focus(); el.select(); }
    }
  }, 100);
}

export async function loadConfig() {
  try {
    const data = await fetchConfig();
    document.getElementById("twitter-auth-token").value = data.twitter_auth_token || "";
    document.getElementById("instagram-session-id").value = data.instagram_session_id || "";
    document.getElementById("tiktok-cookies").value = data.tiktok_cookies || "";
    document.getElementById("auto-sync-interval").value = data.auto_sync_interval || 0;
  } catch (err) {
    console.error("Config load error:", err);
  }
}

export async function saveSettings() {
  const btn = document.getElementById("btn-save-settings");
  await withLoading(btn, async () => {
    try {
      const current = await fetchConfig();
      current.twitter_auth_token = document.getElementById("twitter-auth-token").value.trim();
      current.instagram_session_id = document.getElementById("instagram-session-id").value.trim();
      current.tiktok_cookies = document.getElementById("tiktok-cookies").value.trim();
      current.auto_sync_interval = parseInt(document.getElementById("auto-sync-interval").value) || 0;
      await postConfig(current);
      toast("Settings saved successfully.", "success");
      document.getElementById("settings-modal").style.display = "none";
      loadProgress();
    } catch (err) {
      toast(`Failed to save settings: ${err.message}`, "error");
    }
  });
}

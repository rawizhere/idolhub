import { onClick } from "./actions.js";

export function toggleTheme() {
  const target = document.documentElement.classList.contains("dark") ? "light" : "dark";
  const system = systemDark.matches ? "dark" : "light";
  setTheme(target === system ? "system" : target);
}

const systemDark = window.matchMedia("(prefers-color-scheme: dark)");

function storedTheme() {
  try {
    const saved = localStorage.getItem("color-theme");
    return saved === "light" || saved === "dark" ? saved : "system";
  } catch (_) {
    return "system";
  }
}

function applyTheme(mode) {
  const meta = document.querySelector('meta[name="color-scheme"]');
  if (meta) meta.content = mode === "system" ? "light dark" : mode;
  document.documentElement.classList.toggle("dark", mode === "dark" || (mode === "system" && systemDark.matches));
  syncThemeButtons(mode);
}

// setTheme takes "light", "dark" or "system"; system follows the OS setting live.
export function setTheme(mode) {
  try {
    if (mode === "system") localStorage.removeItem("color-theme");
    else localStorage.setItem("color-theme", mode);
  } catch (_) {}
  applyTheme(mode);
}

export function syncThemeButtons(mode = storedTheme()) {
  document.querySelectorAll('[data-action="set-theme"]').forEach(b => b.setAttribute("aria-pressed", b.dataset.theme === mode ? "true" : "false"));
}

systemDark.addEventListener("change", () => { if (storedTheme() === "system") applyTheme("system"); });
window.addEventListener("storage", (e) => { if (e.key === "color-theme" || e.key === null) applyTheme(storedTheme()); });

export const UI_SCALES = [0.875, 1, 1.125, 1.25];

export function currentScale() {
  return parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--ui-scale")) || 1;
}

export function setScale(scale) {
  document.documentElement.style.setProperty("--ui-scale", String(scale));
  try { localStorage.setItem("ui-scale", String(scale)); } catch (_) {}
  syncScaleButtons();
}

// stepScale moves one preset up or down from the current multiplier.
export function stepScale(dir) {
  const i = UI_SCALES.indexOf(currentScale());
  const next = UI_SCALES[Math.min(UI_SCALES.length - 1, Math.max(0, (i < 0 ? 1 : i) + dir))];
  setScale(next);
}

export function syncScaleButtons() {
  const cur = currentScale();
  document.querySelectorAll('[data-action="ui-scale"]').forEach(b => b.setAttribute("aria-pressed", parseFloat(b.dataset.scale) === cur ? "true" : "false"));
}

onClick({
  "toggle-theme": toggleTheme,
  "set-theme": (el) => setTheme(el.dataset.theme),
  "ui-scale": (el) => setScale(parseFloat(el.dataset.scale))
});

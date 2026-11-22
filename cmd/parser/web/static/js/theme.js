// Runs synchronously in <head> so the first paint already has the right theme; kept out of inline script for the CSP.
(() => {
  let saved = null;
  try { saved = localStorage.getItem("color-theme"); } catch (_) {}
  const scheme = saved === "light" || saved === "dark" ? saved : "light dark";
  const meta = document.querySelector('meta[name="color-scheme"]');
  if (meta) meta.content = scheme;
  const dark = scheme === "dark" || (scheme === "light dark" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);

  // per-browser UI scale multiplier; the root font size already adapts to the window width
  let scale = null;
  try { scale = parseFloat(localStorage.getItem("ui-scale")); } catch (_) {}
  if (scale > 0) document.documentElement.style.setProperty("--ui-scale", String(scale));
})();

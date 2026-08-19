export function debounce(fn, ms) {
  let t;
  return function (...args) {
    clearTimeout(t);
    t = setTimeout(() => fn.apply(this, args), ms);
  };
}

export function escapeHtml(text) {
  return String(text || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/\n/g, "<br>");
}

export function withLoading(btn, fn) {
  if (!btn) return fn();
  if (btn.dataset.loading === "1") return Promise.resolve();
  btn.dataset.loading = "1";
  btn.dataset.disabledPrev = btn.disabled ? "1" : "0";
  btn.disabled = true;
  const originalHTML = btn.innerHTML;
  const label = btn.textContent.trim() || "Working";
  btn.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 animate-spin inline-block align-middle"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg> ${label}`;
  return Promise.resolve(fn()).finally(() => {
    btn.innerHTML = originalHTML;
    btn.disabled = btn.dataset.disabledPrev === "1";
    delete btn.dataset.loading;
    delete btn.dataset.disabledPrev;
  });
}

export function timeAgo(dateStr) {
  if (!dateStr || dateStr === "0001-01-01T00:00:00Z") return "";
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  if (isNaN(then)) return "";
  const sec = Math.floor((now - then) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const days = Math.floor(hr / 24);
  return `${days}d ago`;
}

export function toast(message, type = "info", timeout = 3500) {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const svgPaths = {
    success: '<path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/>',
    error: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>',
    info: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>',
  };
  const tones = {
    success: { bg: "bg-emerald-950/90 text-emerald-300 border-emerald-800", iconColor: "text-emerald-400" },
    error: { bg: "bg-rose-950/90 text-rose-300 border-rose-800", iconColor: "text-rose-400" },
    info: { bg: "bg-white/95 dark:bg-zinc-900/95 text-slate-800 dark:text-zinc-200 border-slate-200 dark:border-zinc-800", iconColor: "text-[#ff9900]" },
  };
  const t = tones[type] || tones.info;
  const iconPath = svgPaths[type] || svgPaths.info;
  const el = document.createElement("div");
  el.className = `pointer-events-auto flex items-start gap-2.5 ${t.bg} border shadow-xl rounded-xl pl-3 pr-3.5 py-2.5 max-w-sm text-xs font-medium translate-y-2 opacity-0 transition-all duration-200 backdrop-blur-md`;
  el.innerHTML = `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4 ${t.iconColor} mt-0.5 flex-shrink-0">${iconPath}</svg>
    <span class="flex-1 leading-relaxed">${escapeHtml(message)}</span>
    <button class="text-slate-400 hover:text-slate-600 dark:hover:text-white cursor-pointer flex-shrink-0 -mt-0.5" aria-label="Dismiss">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>`;
  container.appendChild(el);
  requestAnimationFrame(() => {
    el.classList.remove("translate-y-2", "opacity-0");
  });
  const close = () => {
    el.classList.add("translate-y-2", "opacity-0");
    setTimeout(() => el.remove(), 200);
  };
  el.querySelector("button").addEventListener("click", close);
  setTimeout(close, timeout);
}

export function confirmDialog({ title = "Confirm", message = "", confirmText = "Confirm", cancelText = "Cancel", tone = "danger" } = {}) {
  return new Promise((resolve) => {
    const mount = document.getElementById("confirm-mount");
    if (!mount) { resolve(window.confirm(message)); return; }
    const toneClass = tone === "danger"
      ? "bg-rose-600 hover:bg-rose-700 text-white shadow-xs"
      : "bg-[#ff9900] hover:bg-[#e68a00] text-black font-bold shadow-xs";
    const headerIcon = tone === "danger"
      ? '<path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>'
      : '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>';
    const overlay = document.createElement("div");
    overlay.className = "fixed inset-0 z-[150] flex items-center justify-center bg-black/70 backdrop-blur-xs p-4 opacity-0 transition-opacity duration-150";
    overlay.innerHTML = `
      <div class="bg-white dark:bg-zinc-950 border border-slate-200 dark:border-zinc-800 rounded-2xl p-6 w-full max-w-sm shadow-2xl flex flex-col gap-4 scale-95 transition-transform duration-150">
        <div class="flex items-start gap-3">
          <div class="p-2 ${tone === "danger" ? "bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400" : "bg-amber-100 dark:bg-amber-950/50 text-[#ff9900]"} rounded-xl flex-shrink-0">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4">${headerIcon}</svg>
          </div>
          <div class="flex-1 min-w-0">
            <h3 class="text-sm font-bold text-slate-900 dark:text-zinc-100">${escapeHtml(title)}</h3>
            <p class="text-xs text-slate-500 dark:text-zinc-400 mt-1 leading-relaxed">${escapeHtml(message)}</p>
          </div>
        </div>
        <div class="flex items-center justify-end gap-2 pt-2 border-t border-slate-100 dark:border-zinc-800">
          <button data-act="cancel" class="text-xs font-semibold text-slate-700 dark:text-zinc-300 bg-slate-100 hover:bg-slate-200 dark:bg-zinc-900 dark:hover:bg-zinc-800 px-3.5 py-2 rounded-lg transition-all cursor-pointer">${escapeHtml(cancelText)}</button>
          <button data-act="confirm" class="text-xs font-semibold px-3.5 py-2 rounded-lg transition-all cursor-pointer ${toneClass}">${escapeHtml(confirmText)}</button>
        </div>
      </div>`;
    mount.appendChild(overlay);
    requestAnimationFrame(() => {
      overlay.classList.remove("opacity-0");
      overlay.firstElementChild.classList.remove("scale-95");
    });
    const panel = overlay.firstElementChild;
    const close = (result) => {
      overlay.classList.add("opacity-0");
      panel.classList.add("scale-95");
      setTimeout(() => overlay.remove(), 150);
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); close(false); }
      else if (e.key === "Enter") { e.preventDefault(); close(true); }
    };
    panel.querySelector('[data-act="confirm"]').addEventListener("click", () => close(true));
    panel.querySelector('[data-act="cancel"]').addEventListener("click", () => close(false));
    overlay.addEventListener("click", (e) => { if (e.target === overlay) close(false); });
    document.addEventListener("keydown", onKey);
    setTimeout(() => panel.querySelector('[data-act="confirm"]').focus(), 50);
  });
}

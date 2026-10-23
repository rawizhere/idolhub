import { onClick } from "./actions.js";

export function debounce(fn, ms) {
  let t;
  return function (...args) {
    clearTimeout(t);
    t = setTimeout(() => fn.apply(this, args), ms);
  };
}

export function escapeHtml(text) {
  return String(text ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

const PLATFORM_BADGE_CLASSES = {
  instagram: "bg-pink-50 dark:bg-pink-950/40 text-pink-700 dark:text-pink-400 border-pink-200 dark:border-pink-800",
  twitter: "bg-sky-50 dark:bg-sky-950/40 text-sky-700 dark:text-sky-400 border-sky-200 dark:border-sky-800",
  tiktok: "bg-slate-100 dark:bg-zinc-800 text-slate-700 dark:text-zinc-300 border-slate-300 dark:border-zinc-700",
  youtube: "bg-rose-50 dark:bg-rose-950/40 text-rose-700 dark:text-rose-400 border-rose-200 dark:border-rose-900"
};

export function platformBadgeClass(platform) {
  return PLATFORM_BADGE_CLASSES[platform] || PLATFORM_BADGE_CLASSES.tiktok;
}

export function safeUrl(url) {
  const val = String(url || "");
  if (/^https?:\/\//i.test(val)) return val;
  if (val.startsWith("/") && !val.startsWith("//")) return val;
  return "";
}

export function withLoading(btn, fn) {
  if (!btn) return fn();
  if (btn.dataset.loading === "1") return Promise.resolve();
  btn.dataset.loading = "1";
  const wasDisabled = btn.disabled;
  btn.disabled = true;
  const originalHTML = btn.innerHTML;
  const label = btn.textContent.trim() || "Working";
  btn.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-3.5 h-3.5 animate-spin inline-block align-middle"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg> ${escapeHtml(label)}`;
  return Promise.resolve(fn()).finally(() => {
    btn.innerHTML = originalHTML;
    btn.disabled = wasDisabled;
    delete btn.dataset.loading;
  });
}

export function timeAgo(dateStr) {
  if (!dateStr || dateStr.startsWith("0001-01-01")) return "";
  const then = new Date(dateStr).getTime();
  if (isNaN(then)) return "";
  const sec = Math.floor((Date.now() - then) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

export function formatDuration(ms) {
  const sec = Math.max(1, Math.round(ms / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m`;
  return `${Math.floor(min / 60)}h ${min % 60}m`;
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
  const el = document.createElement("div");
  el.setAttribute("role", type === "error" ? "alert" : "status");
  el.className = `pointer-events-auto flex items-start gap-2.5 ${t.bg} border shadow-xl rounded-xl pl-3 pr-3.5 py-2.5 max-w-sm text-xs font-medium translate-y-2 opacity-0 transition-all duration-200 backdrop-blur-md`;
  el.innerHTML = `
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4 ${t.iconColor} mt-0.5 flex-shrink-0">${svgPaths[type] || svgPaths.info}</svg>
    <span class="flex-1 leading-relaxed">${escapeHtml(message)}</span>
    <button class="text-slate-400 hover:text-slate-600 dark:hover:text-white cursor-pointer flex-shrink-0 -mt-0.5" aria-label="Dismiss">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="w-3.5 h-3.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </button>`;
  container.appendChild(el);
  requestAnimationFrame(() => el.classList.remove("translate-y-2", "opacity-0"));
  const close = () => {
    el.classList.add("translate-y-2", "opacity-0");
    setTimeout(() => el.remove(), 200);
  };
  el.querySelector("button").addEventListener("click", close);
  setTimeout(close, timeout);
}

// Dialogs are native <dialog>: showModal traps focus, Esc closes, and the backdrop click is handled below.
export function openDialog(id) {
  const d = document.getElementById(id);
  if (d && !d.open) d.showModal();
  return d;
}

export function closeDialog(id) {
  const d = typeof id === "string" ? document.getElementById(id) : id;
  if (d?.open) d.close();
}

// requestClose closes a dialog the way Esc does: through a cancelable "cancel" event, so a dialog with
// unsaved edits can intercept the backdrop click and the close button too.
export function requestClose(d) {
  if (!d?.open) return;
  if (d.dispatchEvent(new Event("cancel", { cancelable: true }))) d.close("cancel");
}

document.addEventListener("click", (e) => {
  // a click whose target is the dialog element itself landed on the ::backdrop
  if (e.target instanceof HTMLDialogElement && e.target.open) {
    const r = e.target.getBoundingClientRect();
    const inside = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
    if (!inside) requestClose(e.target);
  }
});

onClick({ "close-dialog": (el) => requestClose(el.closest("dialog")) });

export function formatBytes(n) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1000)));
  return `${(n / 1000 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
}

export const compactNumber = (n) => new Intl.NumberFormat("en", { notation: "compact" }).format(n || 0);

const CONFIRM_ICONS = {
  danger: '<path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
  primary: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>'
};

export function confirmDialog({ title = "Confirm", message = "", confirmText = "Confirm", cancelText = "Cancel", tone = "danger" } = {}) {
  const d = document.getElementById("confirm-dialog");
  const slot = (name) => d.querySelector(`[data-slot="${name}"]`);
  slot("title").textContent = title;
  slot("message").textContent = message;
  slot("cancel").textContent = cancelText;
  const confirmBtn = slot("confirm");
  confirmBtn.textContent = confirmText;
  confirmBtn.className = `btn ${tone === "danger" ? "btn-danger" : "btn-primary"}`;
  const icon = slot("icon");
  icon.className = `p-2 rounded-xl flex-shrink-0 ${tone === "danger" ? "bg-rose-100 dark:bg-rose-950/50 text-rose-600 dark:text-rose-400" : "bg-amber-100 dark:bg-amber-950/50 text-[#ff9900]"}`;
  icon.innerHTML = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4">${CONFIRM_ICONS[tone] || CONFIRM_ICONS.primary}</svg>`;
  d.returnValue = "";
  d.showModal();
  // destructive actions default to Cancel so a stray Enter is harmless
  (tone === "danger" ? slot("cancel") : confirmBtn).focus();
  return new Promise((resolve) => {
    d.addEventListener("close", () => resolve(d.returnValue === "confirm"), { once: true });
  });
}

// Popovers render in the top layer; anchor each one under the button that opened it.
document.addEventListener("toggle", (e) => {
  const pop = e.target;
  if (!(pop instanceof HTMLElement) || !pop.hasAttribute("popover") || e.newState !== "open") return;
  const invoker = document.querySelector(`[popovertarget="${pop.id}"]`);
  if (!invoker) return;
  const r = invoker.getBoundingClientRect();
  const w = pop.offsetWidth;
  const h = pop.offsetHeight;
  let left = pop.dataset.align === "end" ? r.right - w : r.left;
  left = Math.min(Math.max(8, left), window.innerWidth - w - 8);
  let top = r.bottom + 6;
  if (top + h > window.innerHeight - 8 && r.top - h - 6 > 8) top = r.top - h - 6;
  pop.style.left = `${left}px`;
  pop.style.top = `${top}px`;
}, true);

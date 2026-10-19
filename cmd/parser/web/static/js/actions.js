// Delegated event wiring: markup names a handler with data-action (click), data-input-action (input)
// or data-chip-input (Enter/comma in a chip field); modules register handlers here instead of on window.
const clickHandlers = {};
const inputHandlers = {};

export function onClick(map) { Object.assign(clickHandlers, map); }
export function onInput(map) { Object.assign(inputHandlers, map); }

document.addEventListener("click", (e) => {
  const el = e.target.closest("[data-action]");
  if (!el) return;
  const fn = clickHandlers[el.dataset.action];
  if (!fn) return;
  if (el.tagName === "A" && (e.metaKey || e.ctrlKey || e.shiftKey || e.button === 1)) return;
  e.preventDefault();
  fn(el, e);
});

document.addEventListener("input", (e) => {
  const el = e.target.closest("[data-input-action]");
  if (!el) return;
  inputHandlers[el.dataset.inputAction]?.(el, e);
});

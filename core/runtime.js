// Liquid runtime: relays user events to the server and applies returned HTML
// patches. Served as a static file — no inline JS, CSP-friendly (D24).
(() => {
  "use strict";

  // applyPatch swaps the innerHTML at the component's [hydroId] boundary for
  // the server's re-render (D14), preserving focus by element id and never
  // overwriting the focused input's in-flight value (D21). The boundary
  // element itself stays in place — a known v0.1 limitation is that a patch
  // does not update attributes on the [hydroId] element itself.
  function applyPatch(root, patch) {
    const active = document.activeElement;
    const focusId = active && active.id ? active.id : null;
    const hasValue = active && "value" in active;
    const value = hasValue ? active.value : null;
    let selStart = null;
    let selEnd = null;
    if (hasValue) {
      try {
        selStart = active.selectionStart;
        selEnd = active.selectionEnd;
      } catch {
        // Some input types have no selection; focus alone is preserved.
      }
    }

    const tpl = document.createElement("template");
    tpl.innerHTML = patch;
    const next =
      tpl.content.querySelector("[data-hydro-id]") ||
      tpl.content.firstElementChild;
    if (!next) {
      console.error("liquid: patch carries no element to swap in", patch);
      return;
    }
    root.replaceChildren(...next.childNodes);

    if (!focusId) return;
    const el = document.getElementById(focusId);
    if (!el) return;
    el.focus();
    if (hasValue && "value" in el) {
      el.value = value;
      if (selStart !== null) {
        try {
          el.setSelectionRange(selStart, selEnd);
        } catch {
          // Selection is best-effort; the value is already restored.
        }
      }
    }
  }

  async function fire(root, action) {
    const res = await fetch("/hydro-event", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ hydroId: root.dataset.hydroId, action }),
    });
    if (!res.ok) return;
    const env = await res.json();
    if (env.redirect) {
      window.location.assign(env.redirect);
      return;
    }
    if (env.patch) applyPatch(root, env.patch);
  }

  document.addEventListener("click", (e) => {
    if (!(e.target instanceof Element)) return;
    const bound = e.target.closest("[data-liquid-action]");
    if (!bound) return;
    const root = bound.closest("[data-hydro-id]");
    if (!root) return;
    e.preventDefault();
    fire(root, bound.dataset.liquidAction);
  });
})();

// Liquid runtime: relays user events to the server and applies returned HTML
// patches. Served as a static file — no inline JS, CSP-friendly (D24).
(() => {
  "use strict";

  // applyPatch reconciles the component's [hydroId] subtree with the server's
  // re-render (D14). It morphs the new HTML into the live DOM in place with
  // Idiomorph (#106, ADR-0005) — preserving element identity, scroll position,
  // media playback, and CSS-transition state that a wholesale innerHTML swap
  // would discard — then restores the focused input's in-flight value and
  // selection (D21), which a re-render must never clobber. Nodes are matched by
  // id, so keyed lists reorder rather than rebuild. The boundary element itself
  // stays in place; a patch still does not update attributes on it.
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
    Idiomorph.morph(root, next, { morphStyle: "innerHTML" });

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

  // csrfToken reads the render's CSRF token from the meta tag the document
  // shell stamps (D15); the server refuses events without it.
  function csrfToken() {
    const meta = document.querySelector('meta[name="liquid-csrf"]');
    return meta ? meta.content : "";
  }

  // refreshCSRF applies a patch answer's re-minted token (#46): the
  // liquid-csrf meta tag the click/submit path reads, and any csrf_token
  // hidden inputs the server re-rendered into the just-swapped subtree (whose
  // value still carries the original render's token). Keeping the token in
  // lockstep with the server's re-mint tracks the session's sliding idle
  // window, so a long-open page stops earning a spurious 403 (D15).
  function refreshCSRF(root, token) {
    const meta = document.querySelector('meta[name="liquid-csrf"]');
    if (meta) meta.content = token;
    for (const input of root.querySelectorAll('input[name="csrf_token"]')) {
      input.value = token;
    }
  }

  // navigate performs a redirect answer's client-side navigation. The target
  // is author-supplied and rides the envelope verbatim, so a javascript: or
  // data: scheme would execute in this origin if handed straight to
  // location.assign. Resolve it and navigate only to http(s): relative paths
  // and absolute http(s) URLs (including cross-origin — an author redirect is
  // trusted to that extent, D19) are allowed; a hostile scheme is dropped.
  function navigate(target) {
    let dest = null;
    try {
      dest = new URL(target, window.location.href);
    } catch {
      dest = null;
    }
    if (dest && (dest.protocol === "http:" || dest.protocol === "https:")) {
      window.location.assign(dest.href);
    } else {
      console.error("liquid: refusing redirect to non-http(s) target", target);
    }
  }

  async function fire(root, action, payload) {
    const res = await fetch("/hydro-event", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        hydroId: root.dataset.hydroId,
        action,
        payload,
        csrfToken: csrfToken(),
      }),
    });
    if (!res.ok) return;
    const env = await res.json();
    if (env.redirect) {
      navigate(env.redirect);
      return;
    }
    if (env.patch) applyPatch(root, env.patch);
    // The swap has replaced root's children; restamp the re-minted token into
    // the fresh subtree and the shared meta tag (#46).
    if (env.csrf) refreshCSRF(root, env.csrf);
  }

  document.addEventListener("click", (e) => {
    if (!(e.target instanceof Element)) return;
    const bound = e.target.closest("[data-liquid-action]");
    if (!bound) return;
    const root = bound.closest("[data-hydro-id]");
    if (!root) return;
    e.preventDefault();
    // Extra data-* attributes on the bound element ride along as the payload,
    // so a click can say *which* item it was — e.g. data-id="3" arrives as
    // e.String("id"), or binds into a typed payload field. The framework's own
    // data-liquid-* and data-hydro-id are excluded. No data-* => undefined,
    // preserving the bare-click contract.
    let payload = null;
    for (const key in bound.dataset) {
      if (key === "liquidAction" || key === "hydroId") continue;
      if (!payload) payload = {};
      payload[key] = bound.dataset[key];
    }
    fire(root, bound.dataset.liquidAction, payload || undefined);
  });

  // Value events (#104). (input) fires on every keystroke, so it is debounced
  // to coalesce a typing burst into one dispatch; (change) fires on commit
  // (blur / select), so it dispatches immediately. Both send the element's
  // current value as {value}, and neither calls preventDefault — that would
  // break typing and native control behavior. Handlers ride the same
  // allowlist + CSRF + payload-contract path as (click)/(submit).
  const INPUT_DEBOUNCE_MS = 200;
  const inputTimers = new WeakMap();

  document.addEventListener("input", (e) => {
    if (!(e.target instanceof Element)) return;
    const bound = e.target.closest("[data-liquid-input]");
    if (!bound) return;
    const root = bound.closest("[data-hydro-id]");
    if (!root) return;
    const prior = inputTimers.get(bound);
    if (prior) clearTimeout(prior);
    inputTimers.set(
      bound,
      setTimeout(() => {
        inputTimers.delete(bound);
        fire(root, bound.dataset.liquidInput, { value: bound.value });
      }, INPUT_DEBOUNCE_MS),
    );
  });

  document.addEventListener("change", (e) => {
    if (!(e.target instanceof Element)) return;
    const bound = e.target.closest("[data-liquid-change]");
    if (!bound) return;
    const root = bound.closest("[data-hydro-id]");
    if (!root) return;
    fire(root, bound.dataset.liquidChange, { value: bound.value });
  });

  // Server push (D3): interactive pages hold one SSE stream for the whole
  // browser session; pushed patches are applied at their [data-hydro-id]
  // boundary. A reconnect — the server dropped us, or the network did —
  // means patches may have been missed, and there is no replay (D20): the
  // page reloads into a full re-render of current state instead.
  function connect() {
    if (!document.querySelector("[data-hydro-id]")) return;
    const source = new EventSource("/hydro-sse");
    let everConnected = false;
    source.addEventListener("open", () => {
      if (everConnected) {
        source.close();
        window.location.reload();
        return;
      }
      everConnected = true;
    });
    source.addEventListener("error", () => {
      // Transient drops leave readyState CONNECTING and the browser retries
      // (landing in the reload above). CLOSED is fatal — the server refused
      // the stream, typically an expired or evicted session — and only a
      // fresh page load can re-establish one.
      if (source.readyState === EventSource.CLOSED) {
        window.location.reload();
      }
    });
    source.addEventListener("patch", (e) => {
      const { hydroId, patch, csrf } = JSON.parse(e.data);
      const root = document.querySelector(
        `[data-hydro-id="${CSS.escape(hydroId)}"]`,
      );
      if (!root) return;
      applyPatch(root, patch);
      // Restamp the push's re-minted token (#52), mirroring the fetch path
      // (#46): a page driven purely by server push must keep its CSRF token
      // sliding with the session the stream is keeping alive, or the next
      // user event 403s against a token frozen at the original render.
      if (csrf) refreshCSRF(root, csrf);
    });
    // A deferred component's content arriving (#26): unlike a patch, this
    // replaces the fallback slot element itself, so the component's own root
    // (its attributes, id, classes) lands in the DOM. Thereafter it is an
    // ordinary [data-hydro-id] boundary that patch frames re-render.
    source.addEventListener("swap", (e) => {
      const { hydroId, patch } = JSON.parse(e.data);
      const slot = document.querySelector(
        `[data-hydro-id="${CSS.escape(hydroId)}"]`,
      );
      if (!slot) return;
      const tpl = document.createElement("template");
      tpl.innerHTML = patch;
      const next =
        tpl.content.querySelector("[data-hydro-id]") ||
        tpl.content.firstElementChild;
      if (!next) {
        console.error("liquid: swap carries no element to swap in", patch);
        return;
      }
      slot.replaceWith(next);
    });
  }
  connect();

  document.addEventListener("submit", (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement)) return;
    const action = form.getAttribute("data-liquid-submit");
    if (!action) return;
    const root = form.closest("[data-hydro-id]");
    if (!root) return;
    e.preventDefault();
    // Serialize the form as single-valued fields (v0.1); the auto-injected
    // csrf_token input rides along harmlessly beside the top-level token.
    const payload = Object.fromEntries(new FormData(form));
    fire(root, action, payload);
  });
})();

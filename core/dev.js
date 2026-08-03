// Liquid dev loop (D16): served only by dev builds, injected into every
// page shell. Holds one ?dev=1 EventSource for the dev server's frames —
// `diagnostics` renders the build-failure overlay, `reload` and a stream
// reconnect (the app was rebuilt and restarted) reload the page.
(() => {
  "use strict";

  const OVERLAY_ID = "liquid-dev-overlay";

  function clearOverlay() {
    const el = document.getElementById(OVERLAY_ID);
    if (el) el.remove();
  }

  // showOverlay renders the D13 diagnostics array as a fixed panel. Text is
  // set via textContent — diagnostics quote source, never trust it as HTML.
  function showOverlay(diags) {
    clearOverlay();
    const panel = document.createElement("div");
    panel.id = OVERLAY_ID;
    panel.style.cssText =
      "position:fixed;inset:0;z-index:2147483647;background:rgba(20,20,20,.92);" +
      "color:#ff8080;font:14px/1.5 ui-monospace,monospace;padding:2em;overflow:auto";
    const title = document.createElement("h2");
    title.textContent = "liquid build failed";
    title.style.cssText = "color:#fff;margin:0 0 1em;font-size:18px";
    panel.appendChild(title);
    for (const d of diags) {
      const row = document.createElement("pre");
      row.style.cssText = "white-space:pre-wrap;margin:0 0 1em";
      let text = `${d.file}:${d.line}:${d.col} ${d.severity}[${d.code}]: ${d.message}`;
      if (d.suggestion) text += `\n  suggestion: ${d.suggestion}`;
      row.textContent = text;
      panel.appendChild(row);
    }
    const hint = document.createElement("p");
    hint.textContent = "Fix the source and save — this overlay reloads on the next clean build.";
    hint.style.cssText = "color:#aaa";
    panel.appendChild(hint);
    document.body.appendChild(panel);
  }

  function connect() {
    const source = new EventSource("/hydro-sse?dev=1");
    let everConnected = false;
    source.addEventListener("open", () => {
      // A re-open means the app was rebuilt and restarted: reload into the
      // new build (the same contract runtime.js applies to patch streams).
      if (everConnected) {
        source.close();
        window.location.reload();
        return;
      }
      everConnected = true;
    });
    source.addEventListener("error", () => {
      // CLOSED is fatal for this EventSource; keep dialing — the app is
      // likely mid-restart and the next connect lands on the new build.
      if (source.readyState === EventSource.CLOSED) {
        source.close();
        setTimeout(connect, 1000);
      }
    });
    source.addEventListener("reload", () => {
      source.close();
      window.location.reload();
    });
    source.addEventListener("diagnostics", (e) => {
      const diags = JSON.parse(e.data);
      if (Array.isArray(diags) && diags.length) showOverlay(diags);
      else clearOverlay();
    });
  }
  connect();
})();

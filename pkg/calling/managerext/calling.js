// KRIONIX: "Llamadas WhatsApp" card inside each instance's settings page of the
// Evolution GO Manager. It uses the Manager's own login (global API key stored in
// localStorage "evolution-auth") and the /calling API. Plain readable source on
// purpose: the Manager bundle itself is never modified.
(() => {
  "use strict";
  const ROUTE = /^\/manager\/instances\/([0-9a-f-]{36})\/settings\/?$/i;
  const CARD_ID = "kx-calling-card";
  const STATUS = {
    active: ["Activo", "#22c55e"],
    disabled: ["Desactivado", "#9ca3af"],
    pending_apply: ["Pendiente de aplicar", "#eab308"],
    offline: ["Se aplica al conectar", "#eab308"],
    global_disabled: ["Apagado en el servidor", "#9ca3af"]
  };

  function auth() {
    try {
      const raw = JSON.parse(localStorage.getItem("evolution-auth") || "{}");
      const s = raw.state || raw;
      return { url: String(s.apiUrl || "").replace(/\/+$/, ""), key: s.apiKey || "" };
    } catch {
      return { url: "", key: "" };
    }
  }

  async function api(method, path) {
    const a = auth();
    const opts = { method, headers: { apikey: a.key, "Content-Type": "application/json" } };
    if (arguments.length > 2) opts.body = JSON.stringify(arguments[2]);
    const res = await fetch(a.url + path, opts);
    let data = null;
    try { data = await res.json(); } catch {}
    if (!res.ok) {
      const err = new Error((data && data.error) || "HTTP " + res.status);
      err.status = res.status;
      throw err;
    }
    return data;
  }

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  // The card goes right after the Manager's "Configurações Avançadas" card.
  function anchor() {
    for (const h of document.querySelectorAll("h2")) {
      if (h.textContent.trim() === "Configurações Avançadas") {
        return h.closest(".rounded-lg") || h.parentElement;
      }
    }
    return null;
  }

  function buildCard(id) {
    const card = el("div", "rounded-lg border border-sidebar-border bg-card p-6");
    card.id = CARD_ID;
    card.style.marginTop = "1.5rem";
    card.dataset.instance = id;
    const head = el("div", "flex items-center justify-between mb-4");
    head.append(el("h2", "text-lg font-semibold text-foreground", "Llamadas WhatsApp (Nix360)"));
    const badge = el("span", "text-xs font-medium");
    badge.style.cssText = "padding:2px 10px;border-radius:999px;white-space:nowrap";
    head.append(badge);
    card.append(head);

    const row = el("div", "flex items-center justify-between gap-2");
    const text = el("div");
    const label = el("label", "text-sm font-medium text-foreground cursor-pointer", "Habilitar llamadas en Nix360");
    label.htmlFor = "kxCallsEnabled";
    text.append(label, el("p", "text-xs text-muted-foreground",
      "Activado: Evolution Go recibe las llamadas de este número y las envía a Nix360. Desactivado: WhatsApp mantiene su comportamiento normal."));
    const box = el("input", "rounded border-input w-4 h-4 cursor-pointer");
    box.type = "checkbox";
    box.id = "kxCallsEnabled";
    row.append(text, box);
    card.append(row);

    const notice = el("p", "text-xs mt-3");
    notice.style.color = "#eab308";
    const meta = el("p", "text-xs text-muted-foreground mt-3");
    const apply = el("button", "mt-3 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 cursor-pointer",
      "Aplicar ahora (reconecta solo esta instancia)");
    apply.type = "button";
    card.append(notice, apply, meta);

    function render(st) {
      const [txt, color] = STATUS[st.status] || [st.status, "#9ca3af"];
      badge.textContent = txt;
      badge.style.color = color;
      badge.style.background = color + "1a";
      box.checked = !!st.callsEnabled;
      box.disabled = st.status === "global_disabled";
      const lines = [];
      if (st.status === "global_disabled") lines.push("CALLS_ENABLED=false en el servidor: ninguna instancia recibe llamadas.");
      if (st.status === "pending_apply") {
        lines.push(st.callsEnabled
          ? "Guardado. Aplica el cambio para empezar a recibir llamadas en Nix360."
          : "Las nuevas llamadas ya no llegan a Nix360. Aplica el cambio para retirar por completo el motor de llamadas.");
      }
      if (st.status === "offline" && st.callsEnabled) lines.push("Se activará cuando la instancia se conecte.");
      if (st.callsEnabled && st.rejectCall) lines.push("Atención: \"Reject Call\" está activo, las llamadas se rechazarán en vez de llegar a Nix360.");
      if (st.activeCalls) lines.push(st.activeCalls + " llamada(s) en curso.");
      notice.textContent = lines.join(" ");
      notice.hidden = !lines.length;
      apply.hidden = st.status !== "pending_apply";
      apply.disabled = false;
      meta.textContent = st.changedAt
        ? "Último cambio: " + new Date(st.changedAt).toLocaleString() + (st.changedBy ? " · " + st.changedBy : "")
        : "";
      meta.hidden = !st.changedAt;
    }
    function fail(msg) {
      badge.textContent = "Error";
      badge.style.color = "#ef4444";
      badge.style.background = "#ef44441a";
      notice.textContent = msg;
      notice.hidden = false;
    }
    async function refresh() {
      try {
        render(await api("GET", "/calling/instances/" + id));
      } catch (e) {
        fail(e.status === 401 ? "Requiere la API key global (inicia sesión en el Manager con ella)." : "No se pudo leer el estado: " + e.message);
        box.disabled = true;
        apply.hidden = true;
      }
    }

    box.addEventListener("change", async () => {
      const enabled = box.checked;
      const msg = enabled
        ? "¿Activar llamadas en Nix360 para esta instancia?\n\nDespués hay que aplicar el cambio (reconecta solo esta instancia; la sesión de WhatsApp se conserva)."
        : "¿Desactivar llamadas en Nix360 para esta instancia?\n\nLas nuevas llamadas dejarán de llegar a Nix360 desde ahora y sonarán normal en el teléfono. Las llamadas en curso no se cortan.";
      if (!confirm(msg)) { box.checked = !enabled; return; }
      box.disabled = true;
      try {
        render(await api("PUT", "/calling/instances/" + id, { enabled, actor: "manager" }));
      } catch (e) {
        box.checked = !enabled;
        box.disabled = false;
        fail("No se pudo guardar: " + e.message);
      }
    });

    apply.addEventListener("click", async () => {
      if (!confirm("Se reconectará SOLO esta instancia durante unos segundos. La sesión de WhatsApp se conserva. ¿Continuar?")) return;
      apply.disabled = true;
      try {
        await api("POST", "/calling/instances/" + id + "/apply");
      } catch (e) {
        if (e.status === 409 && confirm("Hay llamadas en curso en esta instancia. Aplicar ahora las cortará. ¿Forzar?")) {
          try { await api("POST", "/calling/instances/" + id + "/apply?force=true"); } catch (e2) { fail("No se pudo aplicar: " + e2.message); return; }
        } else {
          apply.disabled = false;
          fail("No se pudo aplicar: " + e.message);
          return;
        }
      }
      notice.textContent = "Reconectando esta instancia…";
      notice.hidden = false;
      setTimeout(refresh, 4000);
      setTimeout(refresh, 10000);
    });

    refresh();
    return card;
  }

  function sync() {
    const m = location.pathname.match(ROUTE);
    const existing = document.getElementById(CARD_ID);
    if (!m) {
      if (existing) existing.remove();
      return;
    }
    const id = m[1].toLowerCase();
    if (existing && existing.dataset.instance === id && existing.isConnected) return;
    if (existing) existing.remove();
    const a = anchor();
    if (a) a.after(buildCard(id));
  }

  let queued = false;
  new MutationObserver(() => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; sync(); });
  }).observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener("popstate", sync);
  sync();
})();

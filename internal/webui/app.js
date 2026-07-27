(() => {
  const $ = (id) => document.getElementById(id);
  const form = $("form"), submit = $("submit"), formError = $("formError"), buildsEl = $("builds");
  const STORE_KEY = "apk-builder-builds";

  const STATUS = {
    pending:  { label: "En attente",  cls: "pending"  },
    building: { label: "Compilation", cls: "building" },
    success:  { label: "Prêt",        cls: "success"  },
    failed:   { label: "Échec",       cls: "failed"   },
  };
  const isTerminal = (s) => s === "success" || s === "failed";

  // Icônes SVG réutilisées dans le HTML généré côté JS (au lieu d'emojis/symboles).
  const ICONS = {
    check:    '<svg class="ic" viewBox="0 0 24 24"><path d="M4 12l5 5L20 6"/></svg>',
    cross:    '<svg class="ic" viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12"/></svg>',
    download: '<svg class="ic" viewBox="0 0 24 24"><path d="M12 4v11m0 0-4-4m4 4 4-4"/><path d="M5 19h14"/></svg>',
    external: '<svg class="ic" viewBox="0 0 24 24"><path d="M14 5h5v5"/><path d="M19 5 10 14"/><path d="M8 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-2"/></svg>',
    offline:  '<svg class="ic" viewBox="0 0 24 24"><rect x="4" y="7" width="16" height="13" rx="2"/><path d="M8 7V5a4 4 0 0 1 8 0v2"/></svg>',
  };

  let builds = load();

  function load() {
    try { return JSON.parse(localStorage.getItem(STORE_KEY)) || []; }
    catch { return []; }
  }
  function save() {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(builds));
    } catch {
      // Quota dépassé : on retire les aperçus d'icônes les plus anciens et on réessaie.
      builds.slice(8).forEach(b => { b.icon = null; });
      try { localStorage.setItem(STORE_KEY, JSON.stringify(builds)); } catch {}
    }
  }

  function esc(s) {
    return String(s).replace(/[&<>"]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;" }[c]));
  }

  function stepClasses(step) {
    if (step.status === "in_progress") return { li: "active", ic: "active", txt: "" };
    if (step.status === "completed") {
      if (step.conclusion === "failure") return { li: "failed", ic: "failed", txt: ICONS.cross };
      return { li: "done", ic: "done", txt: ICONS.check };
    }
    return { li: "", ic: "queued", txt: "·" };
  }

  function renderProgress(b) {
    const pct = Math.max(0, Math.min(100, b.progress || 0));
    const stepLabel = b.current_step || (b.status === "pending" ? "Démarrage du workflow…" : "En cours…");
    const steps = (b.steps || []).map(s => {
      const c = stepClasses(s);
      return `<li class="${c.li}"><span class="step-ic ${c.ic}">${c.txt}</span>${esc(s.name)}</li>`;
    }).join("");
    return `
      <div class="progress-wrap">
        <div class="progress-head">
          <span class="progress-step">${esc(stepLabel)}</span>
          <span class="progress-pct">${pct}%</span>
        </div>
        <div class="track"><span style="width:${pct}%"></span></div>
        ${steps ? `<ul class="steps">${steps}</ul>` : ""}
      </div>`;
  }

  let activeFilter = "all";
  const searchEl = $("search");

  // Vignette : dégradé déterministe dérivé du nom + initiale.
  function thumbStyle(name) {
    const s = name || "app";
    let h = 0;
    for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
    return `background:linear-gradient(150deg,hsl(${h},42%,40%),hsl(${(h + 45) % 360},46%,26%))`;
  }
  function matchFilter(b) {
    if (activeFilter === "active") return b.status === "pending" || b.status === "building";
    if (activeFilter === "success") return b.status === "success";
    if (activeFilter === "failed") return b.status === "failed";
    return true;
  }
  function pillHTML(b) {
    const st = STATUS[b.status] || STATUS.pending;
    return `<span class="pill ${st.cls} pcard-pill"><span class="dot"></span>${st.label}</span>`;
  }
  // Vignette : stable (jamais recréée sur une simple mise à jour de progression).
  function thumbHTML(b) {
    const initial = ((b.app_name || "?").trim()[0] || "?").toUpperCase();
    const isURL = b.mode !== "bundle" && /^https?:/.test(b.url || "");
    const mshots = isURL ? `https://s.wordpress.com/mshots/v1/${encodeURIComponent(b.url)}?w=600&h=400` : "";
    let inner;
    if (b.thumbReady) {
      inner = `<img class="pcard-shot" src="/api/builds/${b.id}/thumb" alt="" onerror="this.remove()" />`;
    } else if (isURL) {
      inner = `<span class="pcard-initial">${esc(initial)}</span><img class="pcard-shot" src="${mshots}" alt="" loading="lazy" onerror="this.remove()" />`;
    } else {
      inner = `<span class="pcard-initial">${esc(initial)}</span>`;
    }
    return `<div class="pcard-thumb" style="${thumbStyle(b.app_name)}">${inner}</div>`;
  }
  // Corps dynamique de la carte : titre, sous-titre, actions.
  // (La progression détaillée est affichée dans le dock flottant.)
  function bodyHTML(b) {
    const failed = b.status === "failed";
    const download = b.status === "success"
      ? `<a class="icon-btn primary" href="/api/builds/${b.id}/apk" title="Télécharger l'APK" download>${ICONS.download}</a>` : "";
    const runLink = b.run_url
      ? `<a class="icon-btn" href="${esc(b.run_url)}" target="_blank" rel="noopener" title="Voir le run">${ICONS.external}</a>` : "";
    const remove = `<button class="icon-btn" data-remove="${b.id}" title="Retirer de la liste">${ICONS.cross}</button>`;
    const subText = failed && b.error ? b.error
      : (b.mode === "bundle" ? `${b.url} · hors-ligne` : b.url);
    const sub = failed && b.error ? esc(b.error)
      : (b.mode === "bundle" ? `<span class="pcard-offline">${ICONS.offline}${esc(b.url)} · hors-ligne</span>` : esc(b.url));
    return `
      <div class="pcard-main">
        <div class="pcard-info">
          <p class="pcard-title">${esc(b.app_name)}</p>
          <p class="pcard-sub"${failed ? ' style="color:var(--err)"' : ''} title="${esc(subText)}">${sub}</p>
        </div>
        <div class="actions">${download}${runLink}${remove}</div>
      </div>`;
  }
  function card(b) {
    return `<div class="pcard" data-card="${b.id}">${thumbHTML(b)}${pillHTML(b)}<div class="pcard-body">${bodyHTML(b)}</div></div>`;
  }

  // Met à jour UNE carte en place (pastille + corps) sans recréer la vignette.
  function isVisible(b) {
    const q = (searchEl && searchEl.value || "").trim().toLowerCase();
    return matchFilter(b) && (!q || `${b.app_name} ${b.url}`.toLowerCase().includes(q));
  }
  function patch(b) {
    const el = buildsEl.querySelector(`.pcard[data-card="${b.id}"]`);
    if (el && !isVisible(b)) { el.remove(); if (!buildsEl.querySelector(".pcard")) render(); }
    else if (!el) { if (isVisible(b)) render(); }
    else {
      const p = el.querySelector(".pcard-pill"); if (p) p.outerHTML = pillHTML(b);
      const body = el.querySelector(".pcard-body"); if (body) body.innerHTML = bodyHTML(b);
    }
    patchActive(b); // met à jour le dock des builds en cours
  }

  // ---- dock flottant des builds en cours ----
  const activeBuilds = () => builds.filter(b => b.status === "pending" || b.status === "building");
  function stepLabel(b) {
    return b.current_step || (b.status === "pending" ? "Démarrage du workflow…" : "En cours…");
  }
  function activeRow(b) {
    const pct = Math.max(0, Math.min(100, b.progress || 0));
    return `<div class="active-item" data-active="${b.id}">
      <div class="active-item-head">
        <span class="active-name">${esc(b.app_name)}</span>
        <span class="active-pct">${pct}%</span>
      </div>
      <div class="track"><span style="width:${pct}%"></span></div>
      <div class="active-step">${esc(stepLabel(b))}</div>
    </div>`;
  }
  function renderActive() {
    const list = activeBuilds();
    const dock = $("activeDock");
    if (!list.length) { dock.hidden = true; return; }
    dock.hidden = false;
    $("activeTitle").textContent = `Builds en cours (${list.length})`;
    $("activeList").innerHTML = list.map(activeRow).join("");
  }
  // Mise à jour en place d'une ligne du dock (barre lisse, sans clignotement).
  function patchActive(b) {
    const item = $("activeList").querySelector(`[data-active="${b.id}"]`);
    const active = b.status === "pending" || b.status === "building";
    if (active !== !!item) { renderActive(); return; } // entrée/sortie -> reconstruit
    if (!active) return;
    const pct = Math.max(0, Math.min(100, b.progress || 0));
    item.querySelector(".active-pct").textContent = pct + "%";
    item.querySelector(".track > span").style.width = pct + "%";
    item.querySelector(".active-step").textContent = stepLabel(b);
    $("activeTitle").textContent = `Builds en cours (${activeBuilds().length})`;
  }
  $("activeToggle").addEventListener("click", () => {
    const d = $("activeDock");
    const collapsed = d.classList.toggle("collapsed");
    $("activeToggle").title = collapsed ? "Agrandir" : "Réduire";
  });
  function render() {
    const q = (searchEl && searchEl.value || "").trim().toLowerCase();
    const list = builds.filter(b => matchFilter(b) &&
      (!q || `${b.app_name} ${b.url}`.toLowerCase().includes(q)));
    if (!list.length) {
      buildsEl.innerHTML = `<div class="builds-empty">${builds.length
        ? "Aucun build ne correspond."
        : "Aucun build pour l'instant — crée ta première app ci-dessus."}</div>`;
      renderActive();
      return;
    }
    buildsEl.innerHTML = list.map(card).join("");
    renderActive();
  }

  // Sonde les miniatures locales (générées en ~5-15 s) et marque chaque build
  // `thumbReady` dès qu'elle existe. Une fois prête, elle n'est plus rechargée
  // (cache immuable + on ne re-render que quand une nouvelle devient dispo).
  const expectsThumb = (b) => b.mode === "bundle" || /^https?:/.test(b.url || "");
  function pollThumbs(attempt) {
    const pending = builds.filter(b => !b.thumbReady && expectsThumb(b));
    if (!pending.length || attempt > 15) return;
    let remaining = pending.length, changed = false;
    const done = () => {
      if (--remaining > 0) return;
      if (changed) { save(); render(); }
      setTimeout(() => pollThumbs(attempt + 1), 3000);
    };
    pending.forEach(b => {
      const im = new Image();
      im.onload = () => { b.thumbReady = true; changed = true; done(); };
      im.onerror = done;
      im.src = `/api/builds/${b.id}/thumb`; // 404 tant que pas prête (non mis en cache)
    });
  }

  buildsEl.addEventListener("click", (e) => {
    const id = e.target.getAttribute?.("data-remove");
    if (id) { builds = builds.filter(b => b.id !== id); save(); render(); }
  });

  // Onglets de filtre + recherche.
  $("tabs").addEventListener("click", (e) => {
    const f = e.target.getAttribute("data-filter");
    if (!f) return;
    activeFilter = f;
    [...$("tabs").children].forEach(t => t.classList.toggle("active", t.getAttribute("data-filter") === f));
    render();
  });
  searchEl.addEventListener("input", render);

  const streams = new Map();

  // Souscription temps réel via Server-Sent Events.
  function subscribe(id) {
    if (streams.has(id)) return;
    const es = new EventSource(`/api/builds/${id}/events`);
    streams.set(id, es);
    es.onmessage = (e) => {
      let data;
      try { data = JSON.parse(e.data); } catch { return; }
      const b = builds.find(x => x.id === id);
      if (!b) { es.close(); streams.delete(id); return; }
      Object.assign(b, {
        status: data.status,
        run_url: data.run_url || b.run_url,
        error: data.error || "",
        progress: data.progress || 0,
        current_step: data.current_step || "",
        steps: data.steps || [],
      });
      save(); patch(b);
      if (isTerminal(b.status)) { es.close(); streams.delete(id); }
    };
    // Les erreurs de connexion sont gérées par la reconnexion auto d'EventSource.
  }

  // ---- panneau "Personnaliser" dépliable ----
  $("customToggle").addEventListener("click", (e) => {
    const t = e.currentTarget;
    const open = t.getAttribute("aria-expanded") === "true";
    t.setAttribute("aria-expanded", String(!open));
    $("customPanel").hidden = open;
    $("customStack").classList.toggle("open", !open);
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    formError.textContent = "";

    const appName = $("name").value.trim();
    const pkg = $("pkg").value.trim();
    const vname = $("vname").value.trim(), vcode = $("vcode").value.trim();

    if (!appName) return fail("Le nom de l'application est requis.");
    if (pkg && !/^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/.test(pkg)) return fail("Package invalide (ex. com.exemple.monapp).");

    // Toujours en multipart : permet d'inclure icône + couleur de splash uniformément.
    const fd = new FormData();
    fd.append("app_name", appName);
    if (pkg) fd.append("package", pkg);
    if (vname) fd.append("version_name", vname);
    if (vcode) fd.append("version_code", vcode);
    fd.append("splash_bg", splashHex());
    fd.append("hide_scrollbar", $("hideScrollbar").checked ? "true" : "false");
    fd.append("debug_console", $("debugConsole").checked ? "true" : "false");
    fd.append("fix_images", $("fixImages").checked ? "true" : "false");
    if (iconBlob) fd.append("icon", iconBlob, "icon.png");

    let fetchOpts, listURL;
    if (mode === "bundle") {
      if (!distBlob) return fail("Choisis un dossier ou un .zip contenant index.html.");
      fd.append("dist", distBlob, "dist.zip");
      fetchOpts = { method: "POST", body: fd };
      listURL = distName || "dist.zip";
    } else {
      const url = $("url").value.trim();
      if (!/^https?:\/\//.test(url)) return fail("L'URL doit commencer par http:// ou https://");
      fd.append("url", url);
      fetchOpts = { method: "POST", body: fd };
      listURL = url;
    }

    submit.disabled = true; submit.textContent = "Envoi…";
    try {
      const res = await fetch("/api/builds", fetchOpts);
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Erreur serveur");

      builds.unshift({
        id: data.id, status: data.status || "pending",
        url: listURL, app_name: appName, mode,
        icon: iconPreview || distIconPreview || null, splash: splashHex(),
      });
      save(); render(); subscribe(data.id);
      pollThumbs(0);
      form.reset(); clearDist(); clearIcon(); syncSplash(); updatePreview();
    } catch (err) {
      fail(err.message);
    } finally {
      submit.disabled = false; submit.textContent = "Générer l'APK";
    }
  });

  function fail(msg) { formError.textContent = msg; }

  // ---- icône + splash + aperçu ----
  let iconBlob = null, iconURL = null, iconPreview = null;
  const splashColor = $("splashColor"), splashHexInput = $("splashHex");

  function splashHex() {
    const v = splashHexInput.value.trim();
    return /^#[0-9a-fA-F]{6}$/.test(v) ? v : splashColor.value;
  }
  function clearIcon() {
    iconBlob = null; iconPreview = null;
    if (iconURL) { URL.revokeObjectURL(iconURL); iconURL = null; }
    $("iconImg").style.backgroundImage = "";
    $("phoneIcon").style.backgroundImage = "";
  }
  function setIcon(blob) {
    if (iconURL) URL.revokeObjectURL(iconURL);
    iconBlob = blob; iconURL = URL.createObjectURL(blob);
    $("iconImg").style.backgroundImage = `url(${iconURL})`;
    $("phoneIcon").style.backgroundImage = `url(${iconURL})`;
  }
  function updatePreview() {
    $("phoneScreen").style.background = splashHex();
    $("phoneName").textContent = $("name").value.trim() || "Mon Application";
  }
  function syncSplash() {
    // aligne le champ hexa sur le sélecteur de couleur
    splashHexInput.value = splashColor.value.toUpperCase();
  }

  splashColor.addEventListener("input", () => { syncSplash(); updatePreview(); });
  splashHexInput.addEventListener("input", () => {
    const v = splashHexInput.value.trim();
    if (/^#[0-9a-fA-F]{6}$/.test(v)) { splashColor.value = v; updatePreview(); }
  });
  $("name").addEventListener("input", updatePreview);

  // --- cropper ---
  const cropModal = $("cropModal"), cropCanvas = $("cropCanvas"), cctx = cropCanvas.getContext("2d");
  const VIEW = 288;
  let cImg = null, cScale = 1, cMin = 1, cx = 0, cy = 0, dragging = false, lastX = 0, lastY = 0;

  $("iconTile").addEventListener("click", () => $("icon").click());
  $("icon").addEventListener("change", (e) => {
    const f = e.target.files[0];
    if (!f) return;
    const img = new Image();
    img.onload = () => { openCropper(img); URL.revokeObjectURL(img.src); };
    img.onerror = () => fail("Image illisible.");
    img.src = URL.createObjectURL(f);
    e.target.value = ""; // permet de re-sélectionner le même fichier
  });

  function openCropper(img) {
    cImg = img;
    cMin = Math.max(VIEW / img.width, VIEW / img.height);
    cScale = cMin;
    $("zoom").min = cMin; $("zoom").max = cMin * 4; $("zoom").value = cMin;
    cx = (VIEW - img.width * cScale) / 2;
    cy = (VIEW - img.height * cScale) / 2;
    drawCrop();
    cropModal.classList.add("open");
  }
  function clampCrop() {
    const w = cImg.width * cScale, h = cImg.height * cScale;
    cx = Math.min(0, Math.max(VIEW - w, cx));
    cy = Math.min(0, Math.max(VIEW - h, cy));
  }
  function drawCrop() {
    clampCrop();
    cctx.clearRect(0, 0, VIEW, VIEW);
    cctx.drawImage(cImg, cx, cy, cImg.width * cScale, cImg.height * cScale);
  }
  $("zoom").addEventListener("input", (e) => {
    const ns = +e.target.value, k = ns / cScale;
    // zoom centré
    cx = VIEW / 2 - (VIEW / 2 - cx) * k;
    cy = VIEW / 2 - (VIEW / 2 - cy) * k;
    cScale = ns; drawCrop();
  });
  function cpt(e) { const r = cropCanvas.getBoundingClientRect();
    const s = VIEW / r.width; const p = e.touches ? e.touches[0] : e;
    return { x: (p.clientX - r.left) * s, y: (p.clientY - r.top) * s }; }
  function dStart(e) { dragging = true; const p = cpt(e); lastX = p.x; lastY = p.y; }
  function dMove(e) { if (!dragging) return; e.preventDefault(); const p = cpt(e);
    cx += p.x - lastX; cy += p.y - lastY; lastX = p.x; lastY = p.y; drawCrop(); }
  function dEnd() { dragging = false; }
  cropCanvas.addEventListener("mousedown", dStart);
  window.addEventListener("mousemove", dMove);
  window.addEventListener("mouseup", dEnd);
  cropCanvas.addEventListener("touchstart", dStart, { passive: false });
  cropCanvas.addEventListener("touchmove", dMove, { passive: false });
  cropCanvas.addEventListener("touchend", dEnd);

  $("cropCancel").addEventListener("click", () => cropModal.classList.remove("open"));
  $("cropOk").addEventListener("click", () => {
    const out = document.createElement("canvas"); out.width = out.height = 512;
    const octx = out.getContext("2d");
    const k = 512 / VIEW;
    octx.drawImage(cImg, cx * k, cy * k, cImg.width * cScale * k, cImg.height * cScale * k);
    // Petit aperçu (128px) persisté en localStorage pour les cartes.
    const prev = document.createElement("canvas"); prev.width = prev.height = 128;
    prev.getContext("2d").drawImage(out, 0, 0, 128, 128);
    iconPreview = prev.toDataURL("image/png");
    out.toBlob((blob) => { if (blob) setIcon(blob); }, "image/png");
    cropModal.classList.remove("open");
  });

  syncSplash(); updatePreview();

  // ---- source : URL (défaut) ou projet importé (bundle), mode implicite ----
  let mode = "url";
  function enterURLMode() {
    mode = "url";
    $("summarySection").hidden = true;
    $("dropZone").classList.remove("has-project");
    $("url").disabled = false;
    formError.textContent = "";
  }
  function enterBundleMode(label) {
    mode = "bundle";
    $("projectName").textContent = label;
    $("summarySection").hidden = false;
    $("dropZone").classList.add("has-project");
    $("url").disabled = true;
    formError.textContent = "";
  }
  $("projectClear").addEventListener("click", () => clearDist());

  // ---- dist : dossier ou .zip ----
  let distBlob = null, distName = "", distIconPreview = null;
  const mb = (bytes) => bytes < 1048576 ? `${Math.max(1, Math.round(bytes / 1024))} Ko` : `${(bytes / 1048576).toFixed(1)} MB`;
  function setDist(blob, name) { distBlob = blob; distName = name; enterBundleMode(name); }
  function clearDist() { distBlob = null; distName = ""; distIconPreview = null; enterURLMode(); }

  const IMG_MIME = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", webp: "image/webp", gif: "image/gif", svg: "image/svg+xml", ico: "image/x-icon" };
  function mimeOfPath(p) { return IMG_MIME[p.split(".").pop().toLowerCase()] || "application/octet-stream"; }

  // Redimensionne une image (blob) en dataURL carré de `size`px (contain).
  function downscaleToDataURL(blob, size) {
    return new Promise((resolve, reject) => {
      const url = URL.createObjectURL(blob);
      const img = new Image();
      img.onload = () => {
        try {
          const c = document.createElement("canvas"); c.width = c.height = size;
          const ctx = c.getContext("2d");
          const r = Math.min(size / img.width, size / img.height);
          const w = img.width * r, h = img.height * r;
          ctx.drawImage(img, (size - w) / 2, (size - h) / 2, w, h);
          resolve(c.toDataURL("image/png"));
        } catch (e) { reject(e); } finally { URL.revokeObjectURL(url); }
      };
      img.onerror = () => { URL.revokeObjectURL(url); reject(new Error("img")); };
      img.src = url;
    });
  }

  // Choisit la meilleure icône parmi les fichiers du dist et en fait un aperçu.
  async function extractDistIcon(files) {
    const imgs = files.filter(f => /\.(png|jpe?g|webp|svg|ico)$/i.test(f.path));
    if (!imgs.length) return null;
    const score = (f) => {
      const p = f.path.toLowerCase(); let s = f.data.length;
      if (/icon|favicon|logo|apple-touch/.test(p)) s += 5e6;
      if (/192|180|256|512/.test(p)) s += 2e6;
      if (p.endsWith(".svg")) s += 1e6;
      if (p.endsWith(".ico")) s -= 2e6;
      return s;
    };
    imgs.sort((a, b) => score(b) - score(a));
    for (const f of imgs.slice(0, 4)) {
      try { return await downscaleToDataURL(new Blob([f.data], { type: mimeOfPath(f.path) }), 128); }
      catch { /* essaie le suivant */ }
    }
    return null;
  }

  // Constructeur de ZIP (méthode "store", sans dépendance externe).
  const CRC_TABLE = (() => {
    const t = new Uint32Array(256);
    for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1); t[n] = c >>> 0; }
    return t;
  })();
  function crc32(buf) {
    let c = 0xFFFFFFFF;
    for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xFF] ^ (c >>> 8);
    return (c ^ 0xFFFFFFFF) >>> 0;
  }
  function makeZip(files) {
    const enc = new TextEncoder(), parts = [], central = [];
    let offset = 0;
    for (const f of files) {
      const name = enc.encode(f.path), crc = crc32(f.data), size = f.data.length;
      const lh = new DataView(new ArrayBuffer(30));
      lh.setUint32(0, 0x04034b50, true); lh.setUint16(4, 20, true);
      lh.setUint32(14, crc, true); lh.setUint32(18, size, true); lh.setUint32(22, size, true);
      lh.setUint16(26, name.length, true);
      parts.push(new Uint8Array(lh.buffer), name, f.data);
      const ch = new DataView(new ArrayBuffer(46));
      ch.setUint32(0, 0x02014b50, true); ch.setUint16(4, 20, true); ch.setUint16(6, 20, true);
      ch.setUint32(16, crc, true); ch.setUint32(20, size, true); ch.setUint32(24, size, true);
      ch.setUint16(28, name.length, true); ch.setUint32(42, offset, true);
      central.push(new Uint8Array(ch.buffer), name);
      offset += 30 + name.length + size;
    }
    let cdSize = 0; for (const c of central) cdSize += c.length;
    const eocd = new DataView(new ArrayBuffer(22));
    eocd.setUint32(0, 0x06054b50, true);
    eocd.setUint16(8, files.length, true); eocd.setUint16(10, files.length, true);
    eocd.setUint32(12, cdSize, true); eocd.setUint32(16, offset, true);
    return new Blob([...parts, ...central, new Uint8Array(eocd.buffer)], { type: "application/zip" });
  }

  // Transforme une liste {path, file} en un zip prêt à uploader.
  async function processDistFiles(list) {
    const junk = (n) => { const b = n.split("/").pop(); return b === ".DS_Store" || b === "Thumbs.db" || b.startsWith("._"); };
    const folder = (list[0]?.path.split("/")[0]) || "dist";
    const files = [];
    let hasIndex = false;
    for (const e of list) {
      if (junk(e.path)) continue;
      const rel = e.path.split("/").slice(1).join("/"); // retire le dossier racine
      if (!rel) continue;
      if (/(^|\/)index\.html$/i.test(rel)) hasIndex = true;
      files.push({ path: rel, data: new Uint8Array(await e.file.arrayBuffer()) });
    }
    if (!hasIndex) throw new Error("Le dossier doit contenir un index.html.");
    return { blob: makeZip(files), folder, count: files.length, files };
  }
  async function ingest(list) {
    if (!list.length) return;
    formError.textContent = ""; enterBundleMode("Préparation…");
    try {
      const { blob, folder, count, files } = await processDistFiles(list);
      setDist(blob, `${folder}/ · ${count} fichiers (${mb(blob.size)})`);
      distIconPreview = await extractDistIcon(files); // aperçu depuis l'icône du projet
    } catch (err) { clearDist(); fail(err.message); }
  }

  // Parcours récursif d'une entrée déposée (drag-drop de dossier).
  function readDirEntries(reader) {
    return new Promise((resolve, reject) => {
      const all = [];
      const rd = () => reader.readEntries((es) => { if (!es.length) resolve(all); else { all.push(...es); rd(); } }, reject);
      rd();
    });
  }
  async function walkEntry(entry, prefix, out) {
    if (entry.isFile) {
      const file = await new Promise((res, rej) => entry.file(res, rej));
      out.push({ path: prefix + entry.name, file });
    } else if (entry.isDirectory) {
      const es = await readDirEntries(entry.createReader());
      for (const c of es) await walkEntry(c, prefix + entry.name + "/", out);
    }
  }

  const dropZone = $("dropZone");

  // Bouton unique "Importer" -> menu (Dossier / Fichier .zip).
  const importBtn = $("importBtn"), importMenu = $("importMenu");
  function closeImportMenu() { importMenu.hidden = true; importBtn.setAttribute("aria-expanded", "false"); }
  function toggleImportMenu() {
    const open = importBtn.getAttribute("aria-expanded") === "true";
    importMenu.hidden = open;
    importBtn.setAttribute("aria-expanded", String(!open));
  }
  importBtn.addEventListener("click", (e) => { e.stopPropagation(); toggleImportMenu(); });
  document.addEventListener("click", (e) => {
    if (!importMenu.hidden && !importMenu.contains(e.target) && e.target !== importBtn) closeImportMenu();
  });
  $("pickZip").addEventListener("click", () => { closeImportMenu(); $("dist").click(); });
  $("pickDir").addEventListener("click", () => { closeImportMenu(); $("distDir").click(); });
  $("dist").addEventListener("change", () => { const f = $("dist").files[0]; if (f) setDist(f, `${f.name} (${mb(f.size)})`); });
  $("distDir").addEventListener("change", () =>
    ingest([...$("distDir").files].map((f) => ({ path: f.webkitRelativePath || f.name, file: f }))));

  ["dragover", "dragenter"].forEach((ev) =>
    dropZone.addEventListener(ev, (e) => { e.preventDefault(); dropZone.classList.add("drag"); }));
  ["dragleave"].forEach((ev) =>
    dropZone.addEventListener(ev, (e) => { e.preventDefault(); if (e.target === dropZone) dropZone.classList.remove("drag"); }));
  dropZone.addEventListener("drop", async (e) => {
    e.preventDefault(); dropZone.classList.remove("drag");
    const items = e.dataTransfer.items;
    if (items && items.length && items[0].webkitGetAsEntry) {
      const roots = [];
      for (const it of items) { const en = it.webkitGetAsEntry(); if (en) roots.push(en); }
      if (roots.length === 1 && roots[0].isFile && /\.zip$/i.test(roots[0].name)) {
        const file = await new Promise((res, rej) => roots[0].file(res, rej));
        return setDist(file, `${file.name} (${mb(file.size)})`);
      }
      if (roots.length) {
        const collected = [];
        for (const r of roots) await walkEntry(r, "", collected);
        return ingest(collected);
      }
    }
    const f = e.dataTransfer.files[0];
    if (f && /\.zip$/i.test(f.name)) setDist(f, `${f.name} (${mb(f.size)})`);
    else fail("Dépose un dossier ou un fichier .zip.");
  });

  // Aperçu du package déduit (miroir de derivePackage côté serveur).
  function derivePackage(name) {
    let slug = name.toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 30);
    if (!slug || !/^[a-z]/.test(slug)) slug = "app" + slug;
    return "app.webview." + slug;
  }
  $("name").addEventListener("input", (e) => {
    const name = e.target.value.trim();
    $("pkg").placeholder = name ? derivePackage(name) : "app.webview.monapplication";
  });

  // ---- connexion Google (optionnelle) : historique privé par compte ----
  async function loadServerHistory() {
    try {
      const res = await fetch("/api/builds");
      if (!res.ok) return;
      const serverBuilds = await res.json();
      builds = (serverBuilds || []).map(b => ({
        id: b.id, status: b.status, url: b.url, app_name: b.app_name, mode: b.mode,
        run_url: b.run_url, error: b.error, progress: b.progress || 0,
        current_step: b.current_step || "", steps: b.steps || [],
      }));
      save(); render();
      builds.filter(b => !isTerminal(b.status)).forEach(b => subscribe(b.id));
      pollThumbs(0);
    } catch { /* API indisponible : on garde l'historique local */ }
  }

  // Affiche la vraie photo de profil Google ; repli sur l'initiale du nom/email
  // si aucune photo n'est fournie ou si son chargement échoue.
  function setAvatar(user) {
    const img = $("accountAvatarImg"), initial = $("accountAvatarInitial");
    initial.textContent = ((user.name || user.email || "?").trim()[0] || "?").toUpperCase();
    if (user.picture) {
      img.hidden = false;
      img.onerror = () => { img.hidden = true; };
      img.src = user.picture;
    } else {
      img.hidden = true;
      img.removeAttribute("src");
    }
  }

  async function initAccount() {
    try {
      const res = await fetch("/api/me");
      const data = await res.json();
      if (!data.enabled) return; // connexion Google non configurée : widget masqué
      $("account").hidden = false;
      if (data.logged_in) {
        $("accountLogin").hidden = true;
        $("accountUser").hidden = false;
        $("accountName").textContent = data.user.name || data.user.email || "Compte";
        setAvatar(data.user);
        await loadServerHistory();
      } else {
        $("accountLogin").hidden = false;
        $("accountUser").hidden = true;
      }
    } catch { /* API indisponible : on ignore, l'app reste utilisable sans compte */ }
  }
  $("accountLogin").addEventListener("click", () => { location.href = "/auth/google/login"; });

  // Puce compte : menu déroulant (déconnexion), fermeture au clic extérieur.
  const accountTrigger = $("accountTrigger"), accountMenu = $("accountMenu");
  function closeAccountMenu() { accountMenu.hidden = true; accountTrigger.setAttribute("aria-expanded", "false"); }
  accountTrigger.addEventListener("click", (e) => {
    e.stopPropagation();
    const open = accountTrigger.getAttribute("aria-expanded") === "true";
    accountMenu.hidden = open;
    accountTrigger.setAttribute("aria-expanded", String(!open));
  });
  document.addEventListener("click", (e) => {
    if (!accountMenu.hidden && !accountMenu.contains(e.target) && e.target !== accountTrigger) closeAccountMenu();
  });
  $("accountLogout").addEventListener("click", async () => {
    await fetch("/auth/logout", { method: "POST" });
    location.reload();
  });

  // Reprise du suivi temps réel au chargement pour les builds non terminés.
  render();
  builds.filter(b => !isTerminal(b.status)).forEach(b => subscribe(b.id));
  pollThumbs(0);
  initAccount();
})();

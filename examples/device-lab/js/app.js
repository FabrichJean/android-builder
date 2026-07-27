(() => {
  const $ = (id) => document.getElementById(id);

  // ---- onglets ----
  const tabs = document.getElementById("tabs");
  const panels = { audio: $("panel-audio"), video: $("panel-video"), image: $("panel-image"), other: $("panel-other") };
  tabs.addEventListener("click", (e) => {
    const btn = e.target.closest(".tab");
    if (!btn) return;
    const name = btn.dataset.tab;
    [...tabs.children].forEach((t) => t.classList.toggle("active", t === btn));
    Object.entries(panels).forEach(([k, el]) => el.classList.toggle("active", k === name));
    if (panels[name] && panels[name].dataset.media) loadMedia(name);
  });

  // ---- pont natif (absent hors de l'APK, ex. aperçu dans un navigateur) ----
  const bridge = () => window.AndroidMedia || null;
  const loaded = { audio: false, video: false, image: false };

  window.onMediaPermission = (type, granted) => {
    if (granted) renderMedia(type);
    else showGate(type, true);
  };

  function fmtSize(bytes) {
    if (bytes < 1048576) return `${Math.max(1, Math.round(bytes / 1024))} Ko`;
    return `${(bytes / 1048576).toFixed(1)} Mo`;
  }
  function fmtDuration(ms) {
    const s = Math.round((ms || 0) / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }

  function showGate(type, deniedOrMissing) {
    const panel = panels[type];
    const gate = panel.querySelector(".gate");
    gate.classList.add("show");
    const b = bridge();
    if (!b) {
      gate.innerHTML = `<p>Cette fonctionnalité nécessite l'app compilée (le pont natif n'est pas disponible dans un simple aperçu navigateur).</p>`;
      return;
    }
    gate.innerHTML = `<p>${deniedOrMissing ? "Permission refusée." : "Autorise l'accès pour lister tes fichiers."}</p><button type="button">Autoriser l'accès</button>`;
    gate.querySelector("button").addEventListener("click", () => b.requestPermission(type));
  }

  function loadMedia(type) {
    if (loaded[type]) return;
    const b = bridge();
    if (!b) { showGate(type, false); return; }
    if (!b.hasPermission(type)) { showGate(type, false); return; }
    renderMedia(type);
  }

  function renderMedia(type) {
    const panel = panels[type];
    panel.querySelector(".gate").classList.remove("show");
    const b = bridge();
    let items = [];
    try { items = JSON.parse(b.list(type) || "[]"); } catch { items = []; }
    loaded[type] = true;

    if (type === "audio") {
      const list = panel.querySelector(".list");
      if (!items.length) { list.innerHTML = `<div class="empty">Aucune musique trouvée.</div>`; return; }
      list.innerHTML = items.map((it) => `
        <div class="item">
          <div class="item-head">
            <span class="item-title">${esc(it.name)}${it.artist ? " — " + esc(it.artist) : ""}</span>
            <span class="item-meta">${fmtDuration(it.duration)} · ${fmtSize(it.size)}</span>
          </div>
          <audio controls preload="none" src="${esc(it.url)}"></audio>
        </div>`).join("");
      return;
    }

    const grid = panel.querySelector(".grid");
    if (!items.length) { grid.innerHTML = `<div class="empty">Aucun fichier trouvé.</div>`; return; }
    if (type === "video") {
      grid.innerHTML = items.map((it) => `
        <div class="thumb" data-url="${esc(it.url)}" data-kind="video">
          <video src="${esc(it.url)}#t=0.5" preload="metadata" muted></video>
          <div class="play"><svg class="ic" viewBox="0 0 24 24"><path d="M8 5l12 7-12 7z"/></svg></div>
          <div class="name">${esc(it.name)}</div>
        </div>`).join("");
    } else {
      grid.innerHTML = items.map((it) => `
        <div class="thumb" data-url="${esc(it.url)}" data-kind="image">
          <img src="${esc(it.url)}" loading="lazy" alt="" />
        </div>`).join("");
    }
    grid.querySelectorAll(".thumb").forEach((el) => el.addEventListener("click", () => openLightbox(el.dataset.kind, el.dataset.url)));
  }

  function esc(s) {
    return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  }

  // ---- lightbox (photo / vidéo en plein écran) ----
  const lightbox = $("lightbox"), lbImg = $("lightboxImg"), lbVideo = $("lightboxVideo");
  function openLightbox(kind, url) {
    lightbox.classList.remove("mode-image", "mode-video");
    if (kind === "video") {
      lbVideo.src = url; lbVideo.currentTime = 0;
      lightbox.classList.add("mode-video", "open");
      lbVideo.play().catch(() => {});
    } else {
      lbImg.src = url;
      lightbox.classList.add("mode-image", "open");
    }
  }
  function closeLightbox() {
    lightbox.classList.remove("open");
    lbVideo.pause(); lbVideo.src = ""; lbImg.src = "";
  }
  $("lightboxClose").addEventListener("click", closeLightbox);
  lightbox.addEventListener("click", (e) => { if (e.target === lightbox) closeLightbox(); });

  // ---- onglet "Autres" : vibration, partage, batterie ----
  const otherResult = $("otherResult");
  const device = () => window.AndroidDevice || null;

  $("btnVibrate").addEventListener("click", () => {
    const d = device();
    if (!d) return (otherResult.textContent = "Pont natif indisponible (aperçu navigateur).");
    d.vibrate(300);
    otherResult.textContent = "Vibration envoyée.";
  });
  $("btnShare").addEventListener("click", () => {
    const d = device();
    if (!d) return (otherResult.textContent = "Pont natif indisponible (aperçu navigateur).");
    d.share("Testé depuis Device Lab, généré par APK Builder.");
  });
  $("btnBattery").addEventListener("click", () => {
    const d = device();
    if (!d) return (otherResult.textContent = "Pont natif indisponible (aperçu navigateur).");
    otherResult.textContent = `Batterie : ${d.batteryLevel()}%`;
  });

  // Charge l'onglet actif au démarrage (Musique).
  loadMedia("audio");
})();

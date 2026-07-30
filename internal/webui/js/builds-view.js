// Rendu des cartes de build : liste "Historique" (avec filtres/recherche),
// aperçu "Builds récents" sur l'accueil, et dock flottant des builds en cours.
import { $, esc, ICONS, builds, STATUS, saveLocal, removeBuildById, getRecentCount } from "./core.js";
import { switchView } from "./router.js";
import { setDistWithFiles, updatePreview, fail } from "./create-form.js";
import { openEditor } from "./editor.js";
import { readZip, makeZip, mb } from "./zip.js";

const buildsEl = $("builds");
const recentEl = $("recentBuilds");
const searchEl = $("search");

let activeFilter = "all";
// La barre de recherche de la sidebar est un simple déclencheur (readonly,
// vidée en permanence) : le vrai texte saisi vit dans le modal ⌘K, l'état de
// la recherche courante est gardé ici plutôt que dans son .value.
let searchQuery = "";

// Vignette : dégradé déterministe dérivé du nom + initiale.
function thumbStyle(name) {
  const s = name || "app";
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return `background:linear-gradient(150deg,hsl(${h},42%,40%),hsl(${(h + 45) % 360},46%,26%))`;
}
function matchFilter(b) {
  if (activeFilter === "active") return b.status === "uploading" || b.status === "pending" || b.status === "building";
  if (activeFilter === "success") return b.status === "success";
  if (activeFilter === "failed") return b.status === "failed";
  return true;
}
function pillHTML(b, extraClass = "pcard-pill") {
  const st = STATUS[b.status] || STATUS.pending;
  return `<span class="pill ${st.cls} ${extraClass}"><span class="dot"></span>${st.label}</span>`;
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
  // Le code source (dist.zip) est conservé côté serveur pour tout build "bundle"
  // (projet importé ou généré par IA), indépendamment du statut du build APK.
  const sourceLink = b.mode === "bundle"
    ? `<a class="icon-btn" href="/api/builds/${b.id}/source" title="Télécharger le code source" download>${ICONS.code}</a>
       <button type="button" class="icon-btn" data-edit-source="${b.id}" title="Éditer le code et relancer un build">${ICONS.edit}</button>` : "";
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
      <div class="actions">${download}${sourceLink}${runLink}${remove}</div>
    </div>`;
}
function card(b) {
  return `<div class="pcard" data-card="${b.id}">${thumbHTML(b)}${pillHTML(b)}<div class="pcard-body">${bodyHTML(b)}</div></div>`;
}

// Met à jour UNE carte en place (pastille + corps) sans recréer la vignette.
function isVisible(b) {
  const q = searchQuery.trim().toLowerCase();
  return matchFilter(b) && (!q || `${b.app_name} ${b.url}`.toLowerCase().includes(q));
}
export function patch(b) {
  const el = buildsEl.querySelector(`.pcard[data-card="${b.id}"]`);
  if (el && !isVisible(b)) { el.remove(); if (!buildsEl.querySelector(".pcard")) render(); }
  else if (!el) { if (isVisible(b)) render(); }
  else {
    const p = el.querySelector(".pcard-pill"); if (p) p.outerHTML = pillHTML(b);
    const body = el.querySelector(".pcard-body"); if (body) body.innerHTML = bodyHTML(b);
  }
  patchRecent(b);
  patchActive(b); // met à jour le dock des builds en cours
}

// ---- "Builds récents" sur l'Accueil (aperçu, toujours les N derniers) ----
export function renderRecent() {
  const list = builds.slice(0, getRecentCount());
  recentEl.innerHTML = list.length ? list.map(card).join("")
    : `<div class="builds-empty">Aucun build pour l'instant — crée ta première app ci-dessus.</div>`;
}
function patchRecent(b) {
  const inTop = builds.slice(0, getRecentCount()).some(x => x.id === b.id);
  const el = recentEl.querySelector(`.pcard[data-card="${b.id}"]`);
  if (!inTop) { if (el) renderRecent(); return; }
  if (!el) { renderRecent(); return; }
  const p = el.querySelector(".pcard-pill"); if (p) p.outerHTML = pillHTML(b);
  const body = el.querySelector(".pcard-body"); if (body) body.innerHTML = bodyHTML(b);
}
// Recharge le code source (dist.zip) d'un build "bundle" existant dans
// l'éditeur, puis installe le résultat modifié comme source du formulaire
// (l'utilisateur relance lui-même le build via le bouton du formulaire —
// il peut aussi juste consulter le code sans rien changer).
async function editAndRebuild(id) {
  const b = builds.find((x) => x.id === id);
  switchView("home");
  fail("");
  try {
    const res = await fetch(`/api/builds/${id}/source`);
    if (!res.ok) throw new Error("code source indisponible");
    const blob = await res.blob();
    const files = await readZip(blob);
    const updated = await openEditor(files, `Éditer ${b?.app_name || "le projet"}`);
    if (!updated) return;
    const zip = makeZip(updated);
    if (b?.app_name) $("name").value = b.app_name;
    if (b?.package) $("pkg").value = b.package;
    updatePreview();
    setDistWithFiles(zip, `${b?.app_name || "Projet"} (modifié) · ${updated.length} fichiers (${mb(zip.size)})`, updated);
    $("form").scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (err) {
    fail(err.message || "Impossible de charger le code source.");
  }
}

// Supprime réellement le build côté serveur (soft delete : la ligne et les
// fichiers restent en base/disque, mais le build disparaît de partout) avant
// de le retirer de la liste locale — best-effort si le réseau échoue, pour
// ne pas bloquer l'utilisateur sur une action de nettoyage.
async function deleteBuild(id) {
  try { await fetch(`/api/builds/${id}`, { method: "DELETE" }); } catch { /* best-effort */ }
  removeBuildById(id); saveLocal(); render();
}

// Pré-remplit le formulaire avec les infos (nom, package, splash) de ce
// build, sans toucher au dist/code — l'utilisateur relance lui-même un
// nouveau build avec les infos modifiées (même logique que "Éditer le code",
// mais côté métadonnées plutôt que fichiers).
function modifyInfo(id) {
  const b = builds.find((x) => x.id === id);
  if (!b) return;
  switchView("home");
  if (b.app_name) $("name").value = b.app_name;
  if (b.package) $("pkg").value = b.package;
  if (b.splash) {
    const hexInput = $("splashHex");
    hexInput.value = b.splash;
    hexInput.dispatchEvent(new Event("input", { bubbles: true }));
  }
  updatePreview();
  $("form").scrollIntoView({ behavior: "smooth", block: "start" });
}

// ---- vue détail : grand panneau (aperçu, statut, actions) ouvert au clic
// sur une carte (hors zone d'actions, qui garde son comportement propre). ----
const detailModal = $("detailModal"), detailCard = $("detailCard");

function detailTopChipLabel(b) { return b.mode === "bundle" ? "Projet hors ligne" : "Projet en ligne"; }
function detailPreviewHTML(b) {
  const shot = b.thumbReady
    ? `<img class="detail-preview-shot" src="/api/builds/${b.id}/thumb" alt="" onerror="this.remove()" />` : "";
  const subText = b.mode === "bundle" ? "Projet importé · hors-ligne" : esc(b.url || "");
  const playBtn = b.mode !== "bundle" && b.url
    ? `<a class="detail-play" href="${esc(b.url)}" target="_blank" rel="noopener">${ICONS.external}Ouvrir le site</a>`
    : b.status === "success"
      ? `<a class="detail-play" href="/api/builds/${b.id}/apk" download>${ICONS.download}Télécharger l'APK</a>`
      : "";
  return `<div class="detail-preview" style="${thumbStyle(b.app_name)}">
    ${shot}
    <span class="detail-preview-name">${esc(b.app_name)}</span>
    <span class="detail-preview-sub">${subText}</span>
    ${playBtn}
  </div>`;
}
function detailActionsHTML(b) {
  const canDownload = b.status === "success";
  const download = `<a class="detail-action${canDownload ? " primary" : ""}"
    ${canDownload ? `href="/api/builds/${b.id}/apk" download` : "disabled"} title="Télécharger l'APK">
    ${ICONS.download}<span class="detail-action-label">Télécharger</span></a>`;
  const editCode = b.mode === "bundle"
    ? `<button type="button" class="detail-action" data-detail-edit="${b.id}" title="Éditer le code">
        ${ICONS.code}<span class="detail-action-label">Éditer code</span></button>` : "";
  const editInfo = `<button type="button" class="detail-action" data-detail-info="${b.id}" title="Modifier les infos">
    ${ICONS.edit}<span class="detail-action-label">Modifier infos</span></button>`;
  const openRun = b.run_url
    ? `<a class="detail-action" href="${esc(b.run_url)}" target="_blank" rel="noopener" title="Voir le run">
        ${ICONS.external}<span class="detail-action-label">Ouvrir</span></a>` : "";
  const del = `<button type="button" class="detail-action danger" data-detail-delete="${b.id}" title="Supprimer">
    ${ICONS.trash}<span class="detail-action-label">Supprimer</span></button>`;
  return [download, editCode, editInfo, openRun, del].filter(Boolean).join("");
}
function detailFootHTML(b) {
  const srcIcon = b.mode === "bundle" ? ICONS.folder : ICONS.external;
  const srcLabel = b.mode === "bundle" ? (b.url || "dist.zip") : (b.url || "");
  return `<div class="detail-foot">
    <p class="detail-foot-name">${esc(b.app_name)}</p>
    <div class="detail-foot-src">${srcIcon}<span>${esc(srcLabel)}</span></div>
    <div class="detail-actions">${detailActionsHTML(b)}</div>
  </div>`;
}
function renderDetailHTML(b) {
  return `
    <span class="detail-topchip">${esc(detailTopChipLabel(b))}</span>
    <div class="detail-body">
      <div class="detail-header">
        ${pillHTML(b, "")}
        <button type="button" class="detail-close" id="detailClose" title="Fermer">${ICONS.cross}</button>
      </div>
      ${detailPreviewHTML(b)}
      ${detailFootHTML(b)}
    </div>`;
}
function openDetail(b) {
  detailCard.innerHTML = renderDetailHTML(b);
  detailModal.classList.add("open");
}
function closeDetail() {
  detailModal.classList.remove("open");
  detailCard.innerHTML = "";
}
detailModal.addEventListener("click", (e) => { if (e.target === detailModal) closeDetail(); });
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && detailModal.classList.contains("open")) closeDetail();
});
detailCard.addEventListener("click", (e) => {
  if (e.target.closest("#detailClose")) { closeDetail(); return; }
  const editId = e.target.closest("[data-detail-edit]")?.dataset.detailEdit;
  if (editId) { closeDetail(); editAndRebuild(editId); return; }
  const infoId = e.target.closest("[data-detail-info]")?.dataset.detailInfo;
  if (infoId) { closeDetail(); modifyInfo(infoId); return; }
  const delId = e.target.closest("[data-detail-delete]")?.dataset.detailDelete;
  if (delId) { closeDetail(); deleteBuild(delId); }
});

// Clic sur une carte : ouvre la vue détail, sauf si le clic vient de sa zone
// d'actions (téléchargement, édition, suppression…) qui garde son propre
// comportement (navigation native ou handler dédié).
function cardClickHandler(e) {
  const editId = e.target.closest?.("[data-edit-source]")?.dataset.editSource;
  if (editId) { editAndRebuild(editId); return; }
  const delId = e.target.closest?.("[data-remove]")?.dataset.remove;
  if (delId) { deleteBuild(delId); return; }
  if (e.target.closest(".actions")) return;
  const cardEl = e.target.closest(".pcard");
  if (!cardEl) return;
  const b = builds.find((x) => x.id === cardEl.dataset.card);
  if (b) openDetail(b);
}
recentEl.addEventListener("click", cardClickHandler);
$("recentViewAll").addEventListener("click", () => switchView("history"));

// ---- dock flottant des builds en cours ----
const activeBuilds = () => builds.filter(b => b.status === "uploading" || b.status === "pending" || b.status === "building");
function stepLabel(b) {
  if (b.status === "uploading") return "Envoi du projet…";
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

export function render() {
  const q = searchQuery.trim().toLowerCase();
  const list = builds.filter(b => matchFilter(b) &&
    (!q || `${b.app_name} ${b.url}`.toLowerCase().includes(q)));
  if (!list.length) {
    buildsEl.innerHTML = `<div class="builds-empty">${builds.length
      ? "Aucun build ne correspond."
      : "Aucun build pour l'instant — crée ta première app ci-dessus."}</div>`;
  } else {
    buildsEl.innerHTML = list.map(card).join("");
  }
  renderRecent();
  renderActive();
}

buildsEl.addEventListener("click", cardClickHandler);

// Onglets de filtre + recherche.
$("tabs").addEventListener("click", (e) => {
  const f = e.target.getAttribute("data-filter");
  if (!f) return;
  activeFilter = f;
  [...$("tabs").children].forEach(t => t.classList.toggle("active", t.getAttribute("data-filter") === f));
  render();
});
// ---- palette de recherche rapide (⌘K) : la barre de recherche de la
// sidebar n'est plus qu'un déclencheur (readonly) — la saisie et les
// résultats vivent dans ce modal, façon command palette. ----
const cmdModal = $("cmdModal"), cmdInput = $("cmdInput"), cmdResults = $("cmdResults");
let cmdSelected = 0;

function cmdMatches(query) {
  const q = query.trim().toLowerCase();
  const list = q
    ? builds.filter(b => `${b.app_name} ${b.url}`.toLowerCase().includes(q))
    : builds.slice(0, 8);
  return list.slice(0, 8);
}
function cmdRowHTML(b, i) {
  const initial = ((b.app_name || "?").trim()[0] || "?").toUpperCase();
  // Reprend l'icône choisie à la création du build quand elle existe (upload,
  // suggestion, logo de marque) plutôt que le dégradé + initiale générique.
  const iconInner = b.icon
    ? `<img src="${esc(b.icon)}" alt="" />`
    : esc(initial);
  const iconStyle = b.icon ? "" : ` style="${thumbStyle(b.app_name)}"`;
  return `<div class="command-row${i === cmdSelected ? " selected" : ""}" data-id="${b.id}" data-index="${i}">
    <div class="command-row-icon"${iconStyle}>${iconInner}</div>
    <div class="command-row-text">
      <div class="command-row-title">${esc(b.app_name)}</div>
      <div class="command-row-sub">${esc(b.url)}</div>
    </div>
    ${i === cmdSelected ? '<span class="command-row-kbd">↵</span>' : ""}
  </div>`;
}
function renderCmdResults() {
  const matches = cmdMatches(cmdInput.value);
  cmdSelected = matches.length ? Math.min(cmdSelected, matches.length - 1) : 0;
  cmdResults.innerHTML = matches.length
    ? matches.map((b, i) => cmdRowHTML(b, i)).join("")
    : `<div class="command-empty">Aucun build ne correspond${cmdInput.value.trim() ? ` à « ${esc(cmdInput.value.trim())} »` : ""}.</div>`;
  cmdResults.dataset.count = matches.length;
}
function openCmdModal() {
  cmdModal.classList.add("open");
  cmdSelected = 0;
  cmdInput.value = "";
  renderCmdResults();
  cmdInput.focus();
}
function closeCmdModal() {
  cmdModal.classList.remove("open");
}
function selectCmdMatch(id) {
  const b = builds.find(x => x.id === id);
  closeCmdModal();
  if (!b) return;
  switchView("history");
  searchQuery = b.app_name;
  render();
  requestAnimationFrame(() => {
    const card = buildsEl.querySelector(`.pcard[data-card="${id}"]`);
    if (!card) return;
    card.scrollIntoView({ behavior: "smooth", block: "center" });
    card.classList.add("flash");
    setTimeout(() => card.classList.remove("flash"), 1200);
  });
}
cmdInput.addEventListener("input", () => { cmdSelected = 0; renderCmdResults(); });
cmdResults.addEventListener("click", (e) => {
  const row = e.target.closest(".command-row");
  if (row) selectCmdMatch(row.dataset.id);
});
cmdModal.addEventListener("click", (e) => { if (e.target === cmdModal) closeCmdModal(); });
cmdInput.addEventListener("keydown", (e) => {
  const count = +cmdResults.dataset.count || 0;
  if (e.key === "ArrowDown") {
    e.preventDefault();
    if (count) { cmdSelected = Math.min(count - 1, cmdSelected + 1); renderCmdResults(); }
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    if (count) { cmdSelected = Math.max(0, cmdSelected - 1); renderCmdResults(); }
  } else if (e.key === "Enter") {
    e.preventDefault();
    const row = cmdResults.querySelector(`.command-row[data-index="${cmdSelected}"]`);
    if (row) selectCmdMatch(row.dataset.id);
  } else if (e.key === "Escape") {
    closeCmdModal();
  }
});
// Déclencheurs : clic sur la barre de recherche de la sidebar, ou ⌘K / Ctrl+K
// depuis n'importe où dans l'app.
searchEl.addEventListener("click", openCmdModal);
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    if (cmdModal.classList.contains("open")) closeCmdModal();
    else openCmdModal();
  }
});

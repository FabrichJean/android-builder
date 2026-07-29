// Rendu des cartes de build : liste "Historique" (avec filtres/recherche),
// aperçu "Builds récents" sur l'accueil, et dock flottant des builds en cours.
import { $, esc, ICONS, builds, STATUS, saveLocal, removeBuildById, getRecentCount } from "./core.js";
import { switchView } from "./router.js";

const buildsEl = $("builds");
const recentEl = $("recentBuilds");
const searchEl = $("search");

let activeFilter = "all";

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
recentEl.addEventListener("click", (e) => {
  const id = e.target.getAttribute?.("data-remove");
  if (id) { removeBuildById(id); saveLocal(); render(); }
});
$("recentViewAll").addEventListener("click", () => switchView("history"));

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

export function render() {
  const q = (searchEl && searchEl.value || "").trim().toLowerCase();
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

buildsEl.addEventListener("click", (e) => {
  const id = e.target.getAttribute?.("data-remove");
  if (id) { removeBuildById(id); saveLocal(); render(); }
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

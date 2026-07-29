// Génération d'app par IA (CLI claude du serveur, comptes connectés) : décrire
// une app dans le prompteur, répondre aux questions de clarification, suivre
// le budget de tokens, puis enchaîner automatiquement sur le build.
import { $, esc } from "./core.js";
// Dépendance circulaire assumée avec create-form.js (voir sa note en tête de
// fichier) : sûre car aucun de ces exports n'est utilisé à l'évaluation du
// module, seulement à l'intérieur de gestionnaires d'événements.
import { getMode, clearDist, setDist, updatePreview, fail } from "./create-form.js";
import { refreshAccountCredits } from "./account.js";

const genPanel = $("genPanel"), genToggle = $("genToggle");
let genMode = false, genBusy = false, genSession = null, genES = null;
// Anonyme : le bouton reste visible mais redirige vers la connexion Google
// au lieu d'activer le mode génération (réservé aux comptes connectés).
let genNeedsLogin = false;
const URL_PLACEHOLDER = "https://exemple.com — ou glisse-dépose un dossier / .zip…";
const GEN_PLACEHOLDER = "Décris l'app à générer (ex. une liste de courses avec catégories et totaux)…";

export function isGenMode() { return genMode; }
export function setGenNeedsLogin(v) { genNeedsLogin = v; }

export function setGenMode(on) {
  genMode = on;
  genToggle.classList.toggle("active", on);
  $("url").placeholder = on ? GEN_PLACEHOLDER : URL_PLACEHOLDER;
  if (on && getMode() === "bundle") clearDist();
  if (!on) genReset();
}
genToggle.addEventListener("click", () => {
  if (genNeedsLogin) { location.href = "/auth/google/login?next=gen"; return; }
  setGenMode(!genMode);
});

function genReset() {
  if (genES) { genES.close(); genES = null; }
  genSession = null; genBusy = false;
  genPanel.hidden = true; genPanel.innerHTML = "";
}

export async function genStart() {
  if (genBusy) return;
  const desc = $("url").value.trim();
  if (desc.length < 10) return fail("Décris l'app un peu plus (10 caractères minimum).");
  if (/^https?:\/\//.test(desc)) return fail("Ceci ressemble à une URL — désactive « Générer par IA » pour encapsuler une URL.");
  genReset();
  genBusy = true; $("submit").disabled = true;
  genPanel.hidden = false;
  genPanel.innerHTML = `<div class="gen-progress"><span class="spin"></span>Claude prépare des questions pour préciser ton app…</div>`;
  try {
    const res = await fetch("/api/generate", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ description: desc }),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "Erreur serveur");
    genSession = data;
    if (data.status === "banned") renderGenBanned(data);
    else if (data.status === "no_credits") renderGenNoCredits(data);
    else if (data.status === "rejected") renderGenRejected(data);
    else if (data.status === "over_budget") renderGenOverBudget(data);
    else renderGenQuestions(data);
    refreshAccountCredits();
  } catch (err) {
    genReset();
    fail(err.message);
  } finally {
    genBusy = false; $("submit").disabled = false;
  }
}

function renderGenQuestions(s) {
  const qs = (s.questions || []).map((q, i) => `
    <div class="gq" data-q="${esc(q.id || "q" + i)}">
      <div class="gq-label">${esc(q.label)}</div>
      <div class="gq-opts">${(q.options || []).map(o =>
        `<button type="button" class="gq-opt" data-val="${esc(o)}">${esc(o)}</button>`).join("")}
      </div>
    </div>`).join("");
  const creditsNote = s.credits_remaining != null
    ? ` · crédits restants aujourd'hui : ~${s.credits_remaining} tokens`
    : "";
  genPanel.innerHTML = `
    ${qs}
    <div class="gen-note">Consommation estimée : ~${s.estimated_tokens || "?"} tokens (limite ${s.token_limit}${creditsNote}).</div>
    <div class="gen-actions">
      <button type="button" class="btn-ghost" id="genCancel">Annuler</button>
      <button type="button" class="btn-gold" id="genGo" disabled>Lancer la génération</button>
    </div>`;
  genPanel.querySelectorAll(".gq-opt").forEach(btn => {
    btn.addEventListener("click", () => {
      btn.closest(".gq").querySelectorAll(".gq-opt").forEach(b => b.classList.toggle("selected", b === btn));
      const all = [...genPanel.querySelectorAll(".gq")]
        .every(g => g.querySelector(".gq-opt.selected"));
      $("genGo").disabled = !all;
    });
  });
  $("genCancel").addEventListener("click", genReset);
  $("genGo").addEventListener("click", genLaunch);
}

async function genLaunch() {
  if (!genSession || genBusy) return;
  const answers = {};
  genPanel.querySelectorAll(".gq").forEach(g => {
    const sel = g.querySelector(".gq-opt.selected");
    if (sel) answers[g.dataset.q] = sel.dataset.val;
  });
  genBusy = true;
  renderGenProgress(0, genSession.token_limit);
  try {
    const res = await fetch(`/api/generate/${genSession.id}/answers`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answers }),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "Erreur serveur");
    genSubscribe(genSession.id);
  } catch (err) {
    genBusy = false;
    genReset();
    fail(err.message);
  }
}

function renderGenProgress(used, limit) {
  const pct = Math.min(100, Math.round(used * 100 / (limit || 1)));
  genPanel.innerHTML = `
    <div class="gen-progress"><span class="spin"></span>Claude génère ton app…
      <span class="gen-tokens">${used} / ${limit} tokens</span>
    </div>
    <div class="track"><span style="width:${pct}%"></span></div>`;
}

function genSubscribe(id) {
  genES = new EventSource(`/api/generate/${id}/events`);
  genES.onmessage = async (e) => {
    let s;
    try { s = JSON.parse(e.data); } catch { return; }
    if (s.status === "generating") { renderGenProgress(s.tokens_used || 0, s.token_limit); return; }
    genES.close(); genES = null; genBusy = false;
    if (s.status === "success") await genFinish(s);
    else if (s.status === "over_budget") renderGenOverBudget(s);
    else { genReset(); fail(s.error || "Génération échouée."); }
    refreshAccountCredits();
  };
}

// Récupère le dist généré, l'installe comme projet importé et enchaîne le build.
async function genFinish(s) {
  try {
    const res = await fetch(`/api/generate/${s.id}/dist.zip`);
    if (!res.ok) throw new Error("archive générée indisponible");
    const blob = await res.blob();
    if (!$("name").value.trim() && s.app_name) {
      $("name").value = s.app_name;
      updatePreview();
    }
    setGenMode(false);
    setDist(blob, `App générée par IA · ${s.tokens_used} tokens`);
    $("form").requestSubmit(); // lance le build APK avec le pipeline bundle existant
  } catch (err) {
    genReset();
    fail(err.message);
  }
}

function renderGenOverBudget(s) {
  const lighter = s.lighter_description || "";
  genPanel.hidden = false;
  genPanel.innerHTML = `
    <div class="gen-over">
      <div class="gen-over-title">Budget de tokens dépassé</div>
      <div class="gen-note">${esc(s.error || `L'app décrite dépasserait la limite de ${s.token_limit} tokens.`)}</div>
      ${lighter ? `<div class="gen-lighter">Suggestion plus légère : « ${esc(lighter)} »</div>` : ""}
    </div>
    <div class="gen-actions">
      <button type="button" class="btn-ghost" id="genCancel">Fermer</button>
      ${lighter ? `<button type="button" class="btn-gold" id="genUseLighter">Utiliser cette description</button>` : ""}
    </div>`;
  $("genCancel").addEventListener("click", genReset);
  const useBtn = $("genUseLighter");
  if (useBtn) useBtn.addEventListener("click", () => {
    $("url").value = lighter;
    genReset();
    genStart();
  });
}

// Demande refusée par le filtre de sécurité du prompt parser (avant même
// l'estimation de tokens) : pas de suggestion allégée, ce n'est pas un
// problème de budget mais de contenu.
function renderGenRejected(s) {
  genPanel.hidden = false;
  genPanel.innerHTML = `
    <div class="gen-over">
      <div class="gen-over-title">Demande refusée</div>
      <div class="gen-note">${esc(s.error || "Cette demande ne peut pas être générée.")}</div>
    </div>
    <div class="gen-actions">
      <button type="button" class="btn-ghost" id="genCancel">Fermer</button>
    </div>`;
  $("genCancel").addEventListener("click", genReset);
}

// Compte banni de la génération IA (trop de demandes refusées) : le bouton
// est désactivé pour la suite de la session, pas de nouvelle tentative.
function renderGenBanned(s) {
  genPanel.hidden = false;
  genPanel.innerHTML = `
    <div class="gen-over">
      <div class="gen-over-title">Compte banni de la génération IA</div>
      <div class="gen-note">${esc(s.error || "Ce compte a dépassé la limite de demandes refusées.")}</div>
    </div>
    <div class="gen-actions">
      <button type="button" class="btn-ghost" id="genCancel">Fermer</button>
    </div>`;
  $("genCancel").addEventListener("click", genReset);
  genToggle.disabled = true;
  genToggle.title = "Génération IA désactivée pour ce compte (trop de demandes refusées)";
}

// Crédits journaliers épuisés (5 crédits = 5000 tokens/jour, remis à zéro le
// lendemain) : le bouton reste actif, la limite se lève automatiquement au
// jour suivant.
function renderGenNoCredits(s) {
  genPanel.hidden = false;
  genPanel.innerHTML = `
    <div class="gen-over">
      <div class="gen-over-title">Crédits de génération épuisés</div>
      <div class="gen-note">${esc(s.error || "Tu as utilisé tous tes crédits de génération pour aujourd'hui. Reviens demain.")}</div>
    </div>
    <div class="gen-actions">
      <button type="button" class="btn-ghost" id="genCancel">Fermer</button>
    </div>`;
  $("genCancel").addEventListener("click", genReset);
}

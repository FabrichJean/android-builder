// Espace admin (/admin) : liste des comptes avec leur consommation de tokens
// IA du jour, budget journalier modifiable par utilisateur, et remise à zéro
// des crédits (un compte ou tous). Réservé aux emails de ADMIN_EMAILS — le
// serveur re-vérifie à chaque appel /api/admin, l'UI ne fait que masquer.
import { $, esc } from "./core.js";

let defaultBudget = 0;

function showError(msg) { $("adminError").textContent = msg || ""; }

function fmtLastLogin(ms) {
  if (!ms) return "jamais connecté";
  return `vu le ${new Date(ms).toLocaleDateString("fr-FR")}`;
}

function userCell(u) {
  const label = u.name || u.email || u.id;
  const initial = (label.trim()[0] || "?").toUpperCase();
  return `
    <div class="admin-user">
      <span class="admin-avatar">${u.picture ? `<img src="${esc(u.picture)}" alt="" onerror="this.hidden=true" />` : ""}<span>${esc(initial)}</span></span>
      <div class="admin-user-txt">
        <span class="admin-user-name">${esc(label)}${u.admin ? ' <span class="admin-tag">admin</span>' : ""}${u.banned ? ' <span class="admin-tag banned">banni</span>' : ""}</span>
        <span class="admin-user-sub">${esc(u.email || u.id)} · ${fmtLastLogin(u.last_login)}</span>
      </div>
    </div>`;
}

function render(users) {
  const tbody = $("adminUsers");
  if (!users.length) {
    tbody.innerHTML = `<tr><td colspan="4" class="admin-empty">Aucun compte connu pour l'instant — les comptes apparaissent ici après leur première connexion Google.</td></tr>`;
    return;
  }
  tbody.innerHTML = users.map((u) => {
    const pct = u.daily_budget ? Math.min(100, Math.round(u.tokens_used * 100 / u.daily_budget)) : 0;
    return `
    <tr data-id="${esc(u.id)}">
      <td>${userCell(u)}</td>
      <td>
        <div class="admin-usage">
          <span>${u.tokens_used} / ${u.daily_budget}</span>
          <span class="admin-usage-track"><span style="width:${pct}%"></span></span>
        </div>
      </td>
      <td>
        <div class="admin-budget">
          <input type="number" min="0" step="500" class="admin-budget-input"
            value="${u.custom_budget || ""}" placeholder="défaut (${defaultBudget})" />
          <button type="button" class="btn-ghost admin-save" title="Enregistrer le budget (vide ou 0 = défaut)">OK</button>
        </div>
      </td>
      <td>
        <button type="button" class="btn-ghost admin-reset" ${u.tokens_used ? "" : "disabled"}
          title="Remettre les crédits du jour à zéro">Réinitialiser</button>
      </td>
    </tr>`;
  }).join("");
}

export async function loadAdmin() {
  showError("");
  try {
    const res = await fetch("/api/admin/users");
    const data = await res.json();
    if (!res.ok) { showError(data.error || "chargement impossible"); return; }
    defaultBudget = data.default_budget;
    $("adminNote").textContent =
      `Budget par défaut : ${defaultBudget} tokens/jour et par compte. Un budget personnalisé le remplace pour ce compte ; les crédits sont remis à zéro chaque nuit.`;
    render(data.users || []);
  } catch {
    showError("API indisponible");
  }
}

async function call(url, opts) {
  showError("");
  try {
    const res = await fetch(url, opts);
    const data = await res.json();
    if (!res.ok) { showError(data.error || "action impossible"); return false; }
    return true;
  } catch {
    showError("API indisponible");
    return false;
  }
}

$("adminUsers").addEventListener("click", async (e) => {
  const row = e.target.closest("tr[data-id]");
  if (!row) return;
  const id = row.dataset.id;
  if (e.target.closest(".admin-save")) {
    const tokens = parseInt(row.querySelector(".admin-budget-input").value, 10) || 0;
    if (await call(`/api/admin/users/${encodeURIComponent(id)}/budget`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ daily_tokens: tokens }),
    })) loadAdmin();
  } else if (e.target.closest(".admin-reset")) {
    if (await call(`/api/admin/users/${encodeURIComponent(id)}/reset-credits`, { method: "POST" })) loadAdmin();
  }
});

$("adminResetAll").addEventListener("click", async () => {
  if (!confirm("Remettre à zéro les crédits IA du jour de TOUS les comptes ?")) return;
  if (await call("/api/admin/reset-credits", { method: "POST" })) loadAdmin();
});

// Ouvre l'accès admin (bouton sidebar) et charge la liste quand on entre sur
// la vue. Appelé par account.js une fois /api/me connu.
export function enableAdmin() {
  $("sbAdmin").hidden = false;
  if (location.pathname === "/admin") loadAdmin();
  $("sbAdmin").addEventListener("click", loadAdmin);
}

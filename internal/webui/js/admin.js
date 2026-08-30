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

function fmtWhen(ms) {
  return new Date(ms).toLocaleString("fr-FR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
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
        <div class="admin-actions">
          <button type="button" class="btn-ghost admin-reset" ${u.tokens_used ? "" : "disabled"}
            title="Remettre les crédits du jour à zéro">Réinitialiser</button>
          <button type="button" class="btn-ghost admin-rejections" title="Voir les prompts refusés">Prompts refusés</button>
          ${u.banned ? `<button type="button" class="btn-ghost admin-unban" title="Lever le bannissement de génération IA">Débannir</button>` : ""}
        </div>
      </td>
    </tr>
    <tr class="admin-rejections-row" data-rej-for="${esc(u.id)}" hidden>
      <td colspan="4"><div class="admin-rejections-body"></div></td>
    </tr>`;
  }).join("");
}

function renderRejections(list) {
  if (!list || !list.length) return `<p class="admin-empty">Aucun prompt refusé enregistré pour ce compte.</p>`;
  return `<ul class="admin-rej-list">${list.map((r) => `
    <li>
      <div class="admin-rej-head">
        <span class="admin-rej-when">${esc(fmtWhen(r.created_at))}</span>
        ${r.reason ? `<span class="admin-rej-reason">${esc(r.reason)}</span>` : ""}
      </div>
      <p class="admin-rej-desc">${esc(r.description)}</p>
    </li>`).join("")}</ul>`;
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
  } else if (e.target.closest(".admin-unban")) {
    if (!confirm("Lever le bannissement de génération IA pour ce compte ?")) return;
    if (await call(`/api/admin/users/${encodeURIComponent(id)}/unban`, { method: "POST" })) loadAdmin();
  } else if (e.target.closest(".admin-rejections")) {
    const rejRow = $("adminUsers").querySelector(`tr.admin-rejections-row[data-rej-for="${CSS.escape(id)}"]`);
    const body = rejRow.querySelector(".admin-rejections-body");
    const opening = rejRow.hidden;
    rejRow.hidden = !opening;
    if (opening && !body.dataset.loaded) {
      body.textContent = "Chargement…";
      try {
        const res = await fetch(`/api/admin/users/${encodeURIComponent(id)}/rejections`);
        const data = await res.json();
        if (!res.ok) { body.textContent = data.error || "chargement impossible"; return; }
        body.innerHTML = renderRejections(data.rejections);
        body.dataset.loaded = "1";
      } catch {
        body.textContent = "API indisponible";
      }
    }
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

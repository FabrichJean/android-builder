// Connexion Google (optionnelle) : bascule anonyme / connecté, avatar, menu
// du compte, et historique de builds privé quand un compte est connecté.
import { $, builds, isTerminal, replaceBuilds, saveLocal, setPersistLocal, setRecentCount } from "./core.js";
import { render, renderRecent } from "./builds-view.js";
import { subscribe, pollThumbs } from "./realtime.js";
import { switchView, viewForPath } from "./router.js";
import { setGenMode, setGenNeedsLogin } from "./gen.js";

async function loadServerHistory() {
  try {
    const res = await fetch("/api/builds");
    if (!res.ok) return;
    const serverBuilds = await res.json();
    replaceBuilds((serverBuilds || []).map(b => ({
      id: b.id, status: b.status, url: b.url, app_name: b.app_name, mode: b.mode,
      run_url: b.run_url, error: b.error, progress: b.progress || 0,
      current_step: b.current_step || "", steps: b.steps || [],
    })));
    saveLocal(); render();
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

export async function initAccount() {
  try {
    const res = await fetch("/api/me");
    const data = await res.json();
    if (!data.enabled || !data.logged_in) { setAnonymousMode(data.enabled); return; }
    $("account").hidden = false;
    $("accountLogin").hidden = true;
    $("accountUser").hidden = false;
    $("accountName").textContent = data.user.name || data.user.email || "Compte";
    setAvatar(data.user);
    setLoggedInMode(!!data.gen, !!data.gen_banned);
    await loadServerHistory();
  } catch {
    // API indisponible : on retombe sur le mode anonyme (historique local).
    setAnonymousMode(false);
  }
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

// ---- mode anonyme (pas de compte) : pas de sidebar, tout sur une seule vue ----
// Sans sidebar, "/historique" n'est plus atteignable depuis l'UI : on affiche
// directement l'historique complet (local) sous le formulaire, sans limite.
function setAnonymousMode(canLogin) {
  setPersistLocal(true);
  setGenMode(false);
  // Visible mais mène à la connexion Google : la génération elle-même
  // reste réservée aux comptes connectés.
  const genToggle = $("genToggle");
  genToggle.hidden = !canLogin;
  genToggle.disabled = false;
  setGenNeedsLogin(canLogin);
  genToggle.title = "Connecte-toi avec Google pour générer une app par IA";
  $("sidebar").hidden = true;
  $("anonTopbar").hidden = !canLogin;
  setRecentCount(Infinity);
  $("recentViewAll").hidden = true;
  $("recentTitle").textContent = "Historique des builds";
  switchView("home", false);
  renderRecent();
}
function setLoggedInMode(genAvailable, genBanned) {
  setPersistLocal(false);
  setGenNeedsLogin(false);
  const genToggle = $("genToggle");
  genToggle.hidden = !genAvailable;
  genToggle.disabled = !!genBanned;
  genToggle.title = genBanned
    ? "Génération IA désactivée pour ce compte (trop de demandes refusées)"
    : "Décris ton app, Claude la génère sur le serveur";
  if (genBanned) setGenMode(false);
  // Retour de la connexion Google déclenchée par un clic sur "Générer par
  // IA" en anonyme (?gen=1, posé par le callback OAuth) : rallume le mode
  // génération directement, sans re-clic, puis nettoie l'URL.
  if (genAvailable && !genBanned && new URLSearchParams(location.search).get("gen") === "1") {
    setGenMode(true);
    history.replaceState(null, "", location.pathname);
  }
  $("sidebar").hidden = false;
  $("anonTopbar").hidden = true;
  setRecentCount(6);
  $("recentViewAll").hidden = false;
  $("recentTitle").textContent = "Builds récents";
  switchView(viewForPath(location.pathname), false);
  renderRecent();
}
$("anonLogin").addEventListener("click", () => { location.href = "/auth/google/login"; });

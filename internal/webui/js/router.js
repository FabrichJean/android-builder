// Navigation sidebar Accueil/Historique — vraies pages "/app" et "/historique"
// (pushState/popstate), pas un simple bascule d'attribut hidden : rechargement,
// retour arrière et lien direct pointent vers la même page index.html, qui lit
// ensuite location.pathname pour afficher la bonne vue.
import { $ } from "./core.js";

const sbViews = { home: $("view-home"), history: $("view-history"), admin: $("view-admin") };
const VIEW_PATH = { home: "/app", history: "/historique", admin: "/admin" };

export function viewForPath(path) {
  if (path === "/historique") return "history";
  if (path === "/admin") return "admin";
  return "home";
}

export function switchView(name, push = true) {
  document.querySelectorAll(".sb-item").forEach((b) => b.classList.toggle("active", b.dataset.view === name));
  Object.entries(sbViews).forEach(([k, el]) => { el.hidden = k !== name; });
  const path = VIEW_PATH[name] || "/app";
  if (push && location.pathname !== path) history.pushState({ view: name }, "", path);
}

document.querySelectorAll(".sb-item").forEach((btn) => {
  btn.addEventListener("click", () => switchView(btn.dataset.view));
});
window.addEventListener("popstate", () => switchView(viewForPath(location.pathname), false));
$("sbCollapse").addEventListener("click", () => {
  $("sidebar").classList.toggle("collapsed");
});

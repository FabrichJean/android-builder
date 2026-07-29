// Bascule thème clair/sombre — préférence partagée (localStorage) entre la
// landing et le studio. Sombre par défaut (design d'origine) ; on ne respecte
// pas prefers-color-scheme automatiquement pour ne pas surprendre un visiteur
// habitué au thème sombre par défaut de l'app — la bascule reste manuelle.
const KEY = "apk-builder-theme";

function apply(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
    btn.setAttribute("aria-pressed", String(theme === "light"));
    btn.title = theme === "light" ? "Passer en thème sombre" : "Passer en thème clair";
  });
}

function current() {
  try { return localStorage.getItem(KEY) === "light" ? "light" : "dark"; }
  catch { return "dark"; }
}

function toggle() {
  const next = current() === "light" ? "dark" : "light";
  try { localStorage.setItem(KEY, next); } catch {}
  apply(next);
}

apply(current());
document.querySelectorAll("[data-theme-toggle]").forEach((btn) => {
  btn.addEventListener("click", toggle);
});

// Carte "Contribuer au projet" en pied de sidebar : fermeture mémorisée
// (localStorage) pour ne pas ressurgir à chaque visite une fois écartée.
import { $ } from "./core.js";

const KEY = "apk-builder-promo-dismissed";
const promo = $("sbPromo");

try {
  if (localStorage.getItem(KEY) === "1") promo.hidden = true;
} catch { /* stockage indisponible : la carte reste visible */ }

$("sbPromoClose").addEventListener("click", () => {
  promo.hidden = true;
  try { localStorage.setItem(KEY, "1"); } catch {}
});

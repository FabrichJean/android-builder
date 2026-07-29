// Carte "Contribuer au projet" en pied de sidebar : fermeture mémorisée
// (localStorage) pour ne pas ressurgir à chaque visite une fois écartée.
import { $ } from "./core.js";

const KEY = "apk-builder-promo-dismissed";
const promo = $("sbPromo");
const close = $("sbPromoClose");

if (promo) {
  try {
    if (localStorage.getItem(KEY) === "1") promo.hidden = true;
  } catch { /* stockage indisponible : la carte reste visible */ }

  if (close) {
    close.addEventListener("click", () => {
      promo.hidden = true;
      try { localStorage.setItem(KEY, "1"); } catch {}
    });
  }
}

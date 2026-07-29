// Point d'entrée : assemble les modules (js/*.js) et lance le chargement
// initial (reprise du suivi temps réel, état du compte). La logique elle-même
// vit dans internal/webui/js/ — un module par responsabilité (state des
// builds, rendu, temps réel, routage, formulaire, génération IA, compte).
import { builds, isTerminal } from "./js/core.js";
import { render } from "./js/builds-view.js";
import { subscribe, pollThumbs } from "./js/realtime.js";
import { initAccount } from "./js/account.js";
import "./js/router.js";
import "./js/create-form.js";
import "./js/gen.js";

// Reprise du suivi temps réel au chargement pour les builds non terminés.
render();
builds.filter(b => !isTerminal(b.status)).forEach(b => subscribe(b.id));
pollThumbs(0);
initAccount();

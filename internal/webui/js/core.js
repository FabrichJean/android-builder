// Socle partagé : accès DOM, icônes, et état des builds (liste + persistance
// locale). Les autres modules importent depuis ici plutôt que de dupliquer
// cet état — `builds` est une référence stable, toujours mutée en place
// (push/splice/replaceBuilds) plutôt que réassignée, pour que les imports
// restent valides dans tous les modules qui la partagent.
export const $ = (id) => document.getElementById(id);

export function esc(s) {
  return String(s).replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]));
}

// Icônes SVG réutilisées dans le HTML généré côté JS (au lieu d'emojis/symboles).
export const ICONS = {
  check:    '<svg class="ic" viewBox="0 0 24 24"><path d="M4 12l5 5L20 6"/></svg>',
  cross:    '<svg class="ic" viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12"/></svg>',
  download: '<svg class="ic" viewBox="0 0 24 24"><path d="M12 4v11m0 0-4-4m4 4 4-4"/><path d="M5 19h14"/></svg>',
  external: '<svg class="ic" viewBox="0 0 24 24"><path d="M14 5h5v5"/><path d="M19 5 10 14"/><path d="M8 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-2"/></svg>',
  offline:  '<svg class="ic" viewBox="0 0 24 24"><rect x="4" y="7" width="16" height="13" rx="2"/><path d="M8 7V5a4 4 0 0 1 8 0v2"/></svg>',
  code:     '<svg class="ic" viewBox="0 0 24 24"><path d="M9 18 3 12l6-6"/><path d="M15 6l6 6-6 6"/></svg>',
  edit:     '<svg class="ic" viewBox="0 0 24 24"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4 12.5-12.5z"/></svg>',
  folder:   '<svg class="ic" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4.5l2 2H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>',
  trash:    '<svg class="ic" viewBox="0 0 24 24"><path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 13h10l1-13"/></svg>',
  kebab:    '<svg class="ic" viewBox="0 0 24 24"><circle cx="5" cy="12" r="1.8" style="fill:currentColor;stroke:none"/><circle cx="12" cy="12" r="1.8" style="fill:currentColor;stroke:none"/><circle cx="19" cy="12" r="1.8" style="fill:currentColor;stroke:none"/></svg>',
};

export const STATUS = {
  uploading: { label: "Envoi…",      cls: "pending"  },
  pending:   { label: "En attente",  cls: "pending"  },
  building:  { label: "Compilation", cls: "building" },
  success:   { label: "Prêt",        cls: "success"  },
  failed:    { label: "Échec",       cls: "failed"   },
};
export const isTerminal = (s) => s === "success" || s === "failed";

const STORE_KEY = "apk-builder-builds";

function loadLocal() {
  try { return JSON.parse(localStorage.getItem(STORE_KEY)) || []; }
  catch { return []; }
}

export const builds = loadLocal();

// Le localStorage ne doit contenir QUE l'historique anonyme (sans compte).
// Quand un compte est connecté, `builds` est remplacé par l'historique serveur
// (privé à ce compte) mais ne doit jamais être persisté ici, sinon il polluerait
// l'historique anonyme et resterait visible après déconnexion.
let persistLocal = true;
export function setPersistLocal(v) { persistLocal = v; }

export function saveLocal() {
  if (!persistLocal) return;
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify(builds));
  } catch {
    // Quota dépassé : on retire les aperçus d'icônes les plus anciens et on réessaie.
    builds.slice(8).forEach(b => { b.icon = null; });
    try { localStorage.setItem(STORE_KEY, JSON.stringify(builds)); } catch {}
  }
}

export function replaceBuilds(list) {
  builds.length = 0;
  builds.push(...list);
}

export function removeBuildById(id) {
  const idx = builds.findIndex(b => b.id === id);
  if (idx >= 0) builds.splice(idx, 1);
}

// Nombre de builds affichés dans "Builds récents" sur l'accueil : 6 pour un
// compte connecté (avec lien "Tout voir" vers l'historique), illimité en
// anonyme (pas de page historique séparée accessible sans sidebar).
let recentCount = 6;
export function setRecentCount(n) { recentCount = n; }
export function getRecentCount() { return recentCount; }

// Suivi temps réel d'un build (SSE) et sondage des miniatures locales.
import { builds, isTerminal, saveLocal } from "./core.js";
import { patch, render } from "./builds-view.js";

const streams = new Map();

// Souscription temps réel via Server-Sent Events.
export function subscribe(id) {
  if (streams.has(id)) return;
  const es = new EventSource(`/api/builds/${id}/events`);
  streams.set(id, es);
  es.onmessage = (e) => {
    let data;
    try { data = JSON.parse(e.data); } catch { return; }
    const b = builds.find(x => x.id === id);
    if (!b) { es.close(); streams.delete(id); return; }
    // L'envoi du projet occupe déjà les premiers % de la barre (voir
    // create-form.js) : la progression du build serveur (0-100) est
    // recalée dans le reste de la plage pour rester une seule progression
    // continue, plutôt que de repartir de 0 après l'envoi.
    const raw = data.progress || 0;
    const progress = b.uploadCap ? b.uploadCap + raw * (100 - b.uploadCap) / 100 : raw;
    Object.assign(b, {
      status: data.status,
      run_url: data.run_url || b.run_url,
      error: data.error || "",
      progress: Math.round(progress),
      current_step: data.current_step || "",
      steps: data.steps || [],
    });
    saveLocal(); patch(b);
    if (isTerminal(b.status)) { es.close(); streams.delete(id); }
  };
  // Les erreurs de connexion sont gérées par la reconnexion auto d'EventSource.
}

// Sonde les miniatures locales (générées en ~5-15 s) et marque chaque build
// `thumbReady` dès qu'elle existe. Une fois prête, elle n'est plus rechargée
// (cache immuable + on ne re-render que quand une nouvelle devient dispo).
const expectsThumb = (b) => b.mode === "bundle" || /^https?:/.test(b.url || "");
export function pollThumbs(attempt) {
  const pending = builds.filter(b => !b.thumbReady && expectsThumb(b));
  if (!pending.length || attempt > 15) return;
  let remaining = pending.length, changed = false;
  const done = () => {
    if (--remaining > 0) return;
    if (changed) { saveLocal(); render(); }
    setTimeout(() => pollThumbs(attempt + 1), 3000);
  };
  pending.forEach(b => {
    const im = new Image();
    im.onload = () => { b.thumbReady = true; changed = true; done(); };
    im.onerror = done;
    im.src = `/api/builds/${b.id}/thumb`; // 404 tant que pas prête (non mis en cache)
  });
}

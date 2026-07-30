// Éditeur de code du dist (CodeMirror 6, chargé à la demande) : permet de
// modifier les fichiers texte d'un projet (importé, généré par IA, ou
// rechargé depuis un build existant) avant de lancer/relancer un build.
// CodeMirror n'est chargé (dynamic import depuis esm.sh) qu'à la première
// ouverture réelle de l'éditeur, pour ne pas alourdir le chargement initial.
import { $, esc } from "./core.js";

const TEXT_EXT = /\.(html?|css|jsx?|mjs|cjs|tsx?|json[c5]?|svg|xml|txt|md|ya?ml|csv|webmanifest)$/i;
const LANG_BY_EXT = {
  html: "html", htm: "html",
  css: "css",
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript", ts: "javascript", tsx: "javascript",
  json: "json", jsonc: "json", json5: "json", webmanifest: "json",
};

let cmMod = null;
// Versions exactes (pas de plage "@6") : la plage large a un jour résolu vers
// une "codemirror@6.65.7" fantôme sur esm.sh — un numéro de version qui n'a
// jamais existé dans les publications officielles (la vraie dernière 6.x est
// 6.0.2) et qui exposait en fait l'ancienne API CodeMirror 5. Épingler la
// version exacte évite de retomber sur une résolution de ce genre.
async function loadCM() {
  if (cmMod) return cmMod;
  const [core, html, css, js, json] = await Promise.all([
    import("https://esm.sh/codemirror@6.0.2"),
    import("https://esm.sh/@codemirror/lang-html@6.4.11"),
    import("https://esm.sh/@codemirror/lang-css@6.3.1"),
    import("https://esm.sh/@codemirror/lang-javascript@6.2.5"),
    import("https://esm.sh/@codemirror/lang-json@6.0.2"),
  ]);
  cmMod = {
    EditorView: core.EditorView, basicSetup: core.basicSetup,
    html: html.html, css: css.css, javascript: js.javascript, json: json.json,
  };
  return cmMod;
}

function extOf(path) { const m = /\.([a-z0-9]+)$/i.exec(path); return m ? m[1].toLowerCase() : ""; }
function isTextFile(path) { return TEXT_EXT.test(path); }
function langExtension(cm, path) {
  switch (LANG_BY_EXT[extOf(path)]) {
    case "html": return cm.html();
    case "css": return cm.css();
    case "javascript": return cm.javascript();
    case "json": return cm.json();
    default: return [];
  }
}
function decodeMaybeText(data) {
  try { return new TextDecoder("utf-8", { fatal: true }).decode(data); }
  catch { return null; }
}

const modal = $("editorModal"), filesEl = $("editorFiles"), cmHost = $("editorCm");
const fileNameEl = $("editorFileName"), errorEl = $("editorError");
const saveBtn = $("editorSave"), cancelBtn = $("editorCancel");

let view = null;      // instance CodeMirror de l'onglet actif
let working = null;   // Map path -> { data, text, editable, dirty }
let order = [];
let currentPath = null;
let resolveFn = null;

function fileRowHTML(path) {
  const entry = working.get(path);
  return `<button type="button" class="editor-file${path === currentPath ? " active" : ""}" data-path="${esc(path)}">
    <span class="editor-file-path">${esc(path)}</span>${entry.dirty ? '<span class="editor-file-dirty">●</span>' : ""}
  </button>`;
}
function renderFileList() { filesEl.innerHTML = order.map(fileRowHTML).join(""); }

async function selectFile(path) {
  if (currentPath && view) {
    const prev = working.get(currentPath);
    if (prev.editable) prev.text = view.state.doc.toString();
  }
  currentPath = path;
  renderFileList();
  const entry = working.get(path);
  fileNameEl.textContent = path;
  if (view) { view.destroy(); view = null; }
  cmHost.innerHTML = "";
  if (!entry.editable) {
    cmHost.innerHTML = `<div class="editor-binary">Fichier binaire — non éditable ici.</div>`;
    return;
  }
  const cm = await loadCM();
  view = new cm.EditorView({
    doc: entry.text,
    extensions: [
      cm.basicSetup,
      langExtension(cm, path),
      cm.EditorView.updateListener.of((u) => {
        if (u.docChanged && !entry.dirty) { entry.dirty = true; renderFileList(); }
      }),
    ],
    parent: cmHost,
  });
}

filesEl.addEventListener("click", (e) => {
  const btn = e.target.closest(".editor-file");
  if (btn && btn.dataset.path !== currentPath) selectFile(btn.dataset.path);
});

function closeModal() {
  modal.classList.remove("open");
  if (view) { view.destroy(); view = null; }
  cmHost.innerHTML = ""; filesEl.innerHTML = ""; fileNameEl.textContent = "";
  working = null; order = []; currentPath = null; resolveFn = null;
  errorEl.textContent = "";
}
cancelBtn.addEventListener("click", () => { const r = resolveFn; closeModal(); if (r) r(null); });
modal.addEventListener("click", (e) => { if (e.target === modal) cancelBtn.click(); });
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && modal.classList.contains("open")) cancelBtn.click();
});

saveBtn.addEventListener("click", () => {
  if (currentPath && view) {
    const entry = working.get(currentPath);
    if (entry.editable) entry.text = view.state.doc.toString();
  }
  const enc = new TextEncoder();
  const files = order.map((path) => {
    const entry = working.get(path);
    return { path, data: entry.editable ? enc.encode(entry.text) : entry.data };
  });
  const r = resolveFn;
  closeModal();
  if (r) r(files);
});

// Ouvre l'éditeur sur une liste [{path, data:Uint8Array}] ; résout avec la
// liste modifiée (même forme) si l'utilisateur clique "Enregistrer", ou
// `null` s'il annule.
export function openEditor(files, title) {
  return new Promise((resolve) => {
    $("editorTitle").textContent = title || "Éditer le code";
    working = new Map();
    order = files.map((f) => f.path).sort();
    for (const f of files) {
      const editable = isTextFile(f.path);
      const text = editable ? decodeMaybeText(f.data) : null;
      working.set(f.path, { data: f.data, text: text ?? "", editable: editable && text !== null, dirty: false });
    }
    resolveFn = resolve;
    errorEl.textContent = "";
    modal.classList.add("open");
    const first = order.find((p) => working.get(p).editable) || order[0];
    if (first) selectFile(first).catch((err) => { errorEl.textContent = "Éditeur indisponible : " + err.message; });
  });
}

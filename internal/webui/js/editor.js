// Éditeur de code du dist, façon VS Code : arborescence de dossiers repliable,
// onglets de fichiers ouverts, icônes par type, barre de statut (langage,
// position du curseur) et minimap — au-dessus de CodeMirror 6, chargé à la
// demande (dynamic import depuis esm.sh) pour ne pas alourdir le chargement
// initial de la page.
import { $, esc } from "./core.js";

const TEXT_EXT = /\.(html?|css|jsx?|mjs|cjs|tsx?|json[c5]?|svg|xml|txt|md|ya?ml|csv|webmanifest)$/i;
const LANG_BY_EXT = {
  html: "html", htm: "html",
  css: "css",
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript", ts: "javascript", tsx: "javascript",
  json: "json", jsonc: "json", json5: "json", webmanifest: "json",
};
const LANG_LABEL = {
  html: "HTML", css: "CSS", javascript: "JavaScript", json: "JSON",
};
// Badge de fichier (couleur + abréviation) — pas de police d'icônes externe,
// juste un petit pastille cohérente avec le reste des icônes SVG maison.
const ICON_COLOR = {
  html: "#e34c26", htm: "#e34c26",
  css: "#2965f1",
  js: "#e8c15a", mjs: "#e8c15a", cjs: "#e8c15a", jsx: "#e8c15a",
  ts: "#3178c6", tsx: "#3178c6",
  json: "#8bc34a", jsonc: "#8bc34a", json5: "#8bc34a", webmanifest: "#8bc34a",
  svg: "#ab6bc9", png: "#ab6bc9", jpg: "#ab6bc9", jpeg: "#ab6bc9", gif: "#ab6bc9", webp: "#ab6bc9", ico: "#ab6bc9",
  md: "#4aa3e0", txt: "#8a8577", yml: "#c9986a", yaml: "#c9986a",
};
const ICON_LABEL = {
  html: "&lt;&gt;", htm: "&lt;&gt;", css: "#", js: "JS", mjs: "JS", cjs: "JS", jsx: "JS",
  ts: "TS", tsx: "TS", json: "{}", jsonc: "{}", json5: "{}", webmanifest: "{}",
  svg: "◇", png: "▨", jpg: "▨", jpeg: "▨", gif: "▨", webp: "▨", ico: "▨", md: "M↓", txt: "≡",
  yml: "Y", yaml: "Y",
};

let cmMod = null;
// Versions exactes (pas de plage "@6") : une plage large a un jour résolu vers
// une "codemirror@6.65.7" fantôme sur esm.sh — un numéro de version qui n'a
// jamais existé dans les publications officielles (la vraie dernière 6.x est
// 6.0.2) et qui exposait en fait l'ancienne API CodeMirror 5. Épingler la
// version exacte évite de retomber sur une résolution de ce genre.
async function loadCM() {
  if (cmMod) return cmMod;
  const [core, html, css, js, json, minimap] = await Promise.all([
    import("https://esm.sh/codemirror@6.0.2"),
    import("https://esm.sh/@codemirror/lang-html@6.4.11"),
    import("https://esm.sh/@codemirror/lang-css@6.3.1"),
    import("https://esm.sh/@codemirror/lang-javascript@6.2.5"),
    import("https://esm.sh/@codemirror/lang-json@6.0.2"),
    import("https://esm.sh/@replit/codemirror-minimap@0.5.2"),
  ]);
  cmMod = {
    EditorView: core.EditorView, basicSetup: core.basicSetup,
    html: html.html, css: css.css, javascript: js.javascript, json: json.json,
    showMinimap: minimap.showMinimap,
  };
  return cmMod;
}

function extOf(path) { const m = /\.([a-z0-9]+)$/i.exec(path); return m ? m[1].toLowerCase() : ""; }
function isTextFile(path) { return TEXT_EXT.test(path); }
function baseName(path) { return path.split("/").pop(); }
function langOf(path) { return LANG_BY_EXT[extOf(path)] || null; }
function langExtension(cm, path) {
  switch (langOf(path)) {
    case "html": return cm.html();
    case "css": return cm.css();
    case "javascript": return cm.javascript();
    case "json": return cm.json();
    default: return [];
  }
}
function fileBadgeHTML(path, extra = "") {
  const ext = extOf(path);
  const color = ICON_COLOR[ext] || "#8a8577";
  const label = ICON_LABEL[ext] || baseName(path)[0]?.toUpperCase() || "?";
  return `<span class="file-badge ${extra}" style="color:${color}">${label}</span>`;
}
function decodeMaybeText(data) {
  try { return new TextDecoder("utf-8", { fatal: true }).decode(data); }
  catch { return null; }
}

const FOLDER_ICON = `<svg class="tree-folder-ic" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4.5l2 2H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg>`;
const CHEVRON_ICON = `<svg class="tree-chev" viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>`;

const modal = $("editorModal");
const treeEl = $("editorTree"), tabsEl = $("editorTabs"), cmHost = $("editorCm");
const statusLangEl = $("editorStatusLang"), statusPosEl = $("editorStatusPos");
const errorEl = $("editorError");
const cancelBtn = $("editorCancel");

let working = null;      // Map path -> { data, textInitial, editable }
let order = [];          // tous les chemins, triés
let tree = null;         // arbre {type:'dir', name, path, children:Map} racine
let expandedDirs = null; // Set des chemins de dossiers dépliés
let openTabs = [];        // chemins actuellement ouverts en onglet
let activeTab = null;
let viewsByPath = null;   // Map path -> { view, host }  (instances CodeMirror vivantes)
let dirtyPaths = null;    // Set des chemins modifiés
let resolveFn = null;

function buildTree(paths) {
  const root = { type: "dir", name: "", path: "", children: new Map() };
  for (const p of paths) {
    const parts = p.split("/");
    let node = root;
    for (let i = 0; i < parts.length; i++) {
      const name = parts[i];
      if (i === parts.length - 1) {
        node.children.set(name, { type: "file", name, path: p });
      } else {
        const dirPath = parts.slice(0, i + 1).join("/");
        if (!node.children.has(name)) node.children.set(name, { type: "dir", name, path: dirPath, children: new Map() });
        node = node.children.get(name);
      }
    }
  }
  return root;
}
function allDirPaths(node, out = []) {
  for (const child of node.children.values()) {
    if (child.type === "dir") { out.push(child.path); allDirPaths(child, out); }
  }
  return out;
}
function sortedChildren(node) {
  return [...node.children.values()].sort((a, b) => {
    if (a.type !== b.type) return a.type === "dir" ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}
function renderTreeNode(node, depth) {
  return sortedChildren(node).map((child) => {
    if (child.type === "file") {
      const dirty = dirtyPaths.has(child.path);
      const active = child.path === activeTab;
      return `<button type="button" class="tree-row tree-file${active ? " active" : ""}" data-path="${esc(child.path)}" style="--depth:${depth}">
        ${fileBadgeHTML(child.path)}
        <span class="tree-label">${esc(child.name)}</span>
        ${dirty ? '<span class="dirty-dot"></span>' : ""}
      </button>`;
    }
    const open = expandedDirs.has(child.path);
    return `<div class="tree-node">
      <button type="button" class="tree-row tree-dir${open ? " open" : ""}" data-toggle="${esc(child.path)}" style="--depth:${depth}">
        ${CHEVRON_ICON}${FOLDER_ICON}
        <span class="tree-label">${esc(child.name)}</span>
      </button>
      ${open ? `<div class="tree-children">${renderTreeNode(child, depth + 1)}</div>` : ""}
    </div>`;
  }).join("");
}
function renderTree() { treeEl.innerHTML = renderTreeNode(tree, 0); }

treeEl.addEventListener("click", (e) => {
  const toggle = e.target.closest("[data-toggle]");
  if (toggle) {
    const p = toggle.dataset.toggle;
    if (expandedDirs.has(p)) expandedDirs.delete(p); else expandedDirs.add(p);
    renderTree();
    return;
  }
  const file = e.target.closest("[data-path]");
  if (file) openTab(file.dataset.path);
});

function renderTabs() {
  tabsEl.innerHTML = openTabs.map((p) => {
    const dirty = dirtyPaths.has(p);
    const active = p === activeTab;
    return `<div class="editor-tab${active ? " active" : ""}" data-tab="${esc(p)}" title="${esc(p)}">
      ${fileBadgeHTML(p, "small")}
      <span class="editor-tab-name">${esc(baseName(p))}</span>
      ${dirty ? '<span class="dirty-dot"></span>' : ""}
      <button type="button" class="editor-tab-close" data-close="${esc(p)}" title="Fermer">×</button>
    </div>`;
  }).join("");
}
tabsEl.addEventListener("click", (e) => {
  const close = e.target.closest("[data-close]");
  if (close) { closeTab(close.dataset.close); return; }
  const tab = e.target.closest("[data-tab]");
  if (tab) activateTab(tab.dataset.tab);
});
tabsEl.addEventListener("auxclick", (e) => {
  // clic milieu : ferme aussi l'onglet, comme dans VS Code
  if (e.button === 1) { const tab = e.target.closest("[data-tab]"); if (tab) closeTab(tab.dataset.tab); }
});

function updateStatusBar(path, state) {
  statusLangEl.textContent = LANG_LABEL[langOf(path)] || "Texte brut";
  const pos = state.selection.main.head;
  const line = state.doc.lineAt(pos);
  statusPosEl.textContent = `Ln ${line.number}, Col ${pos - line.from + 1}`;
}

async function mountView(path) {
  const entry = working.get(path);
  const host = document.createElement("div");
  host.className = "editor-view-host";
  cmHost.appendChild(host);
  if (!entry.editable) {
    host.innerHTML = `<div class="editor-binary">Fichier binaire — non éditable ici.</div>`;
    return { view: null, host };
  }
  const cm = await loadCM();
  const view = new cm.EditorView({
    doc: entry.textInitial,
    extensions: [
      cm.basicSetup,
      langExtension(cm, path),
      cm.showMinimap.of({
        create: () => ({ dom: document.createElement("div") }),
        displayText: "blocks",
      }),
      cm.EditorView.updateListener.of((u) => {
        if (u.docChanged && !dirtyPaths.has(path)) { dirtyPaths.add(path); renderTree(); renderTabs(); }
        if (path === activeTab && (u.docChanged || u.selectionSet)) updateStatusBar(path, u.state);
      }),
    ],
    parent: host,
  });
  return { view, host };
}

async function activateTab(path) {
  if (activeTab && viewsByPath.has(activeTab)) {
    viewsByPath.get(activeTab).host.classList.remove("shown");
  }
  activeTab = path;
  renderTree(); renderTabs();
  errorEl.textContent = "";
  if (!viewsByPath.has(path)) {
    try { viewsByPath.set(path, await mountView(path)); }
    catch (err) { errorEl.textContent = "Éditeur indisponible : " + err.message; return; }
  }
  const entry = viewsByPath.get(path);
  entry.host.classList.add("shown");
  if (entry.view) { entry.view.focus(); updateStatusBar(path, entry.view.state); }
  else { statusLangEl.textContent = "Binaire"; statusPosEl.textContent = ""; }
}
function openTab(path) {
  if (!openTabs.includes(path)) openTabs.push(path);
  activateTab(path);
}
function closeTab(path) {
  const idx = openTabs.indexOf(path);
  if (idx === -1) return;
  openTabs.splice(idx, 1);
  const mounted = viewsByPath.get(path);
  if (mounted) { if (mounted.view) mounted.view.destroy(); mounted.host.remove(); viewsByPath.delete(path); }
  if (activeTab === path) {
    const next = openTabs[idx] || openTabs[idx - 1];
    activeTab = null;
    if (next) activateTab(next);
    else { renderTree(); renderTabs(); statusLangEl.textContent = ""; statusPosEl.textContent = ""; }
  } else {
    renderTree(); renderTabs();
  }
}

function closeModal() {
  modal.classList.remove("open");
  if (viewsByPath) for (const { view } of viewsByPath.values()) view?.destroy();
  cmHost.innerHTML = ""; treeEl.innerHTML = ""; tabsEl.innerHTML = "";
  statusLangEl.textContent = ""; statusPosEl.textContent = "";
  working = null; order = []; tree = null; expandedDirs = null;
  openTabs = []; activeTab = null; viewsByPath = null; dirtyPaths = null; resolveFn = null;
  errorEl.textContent = "";
}
cancelBtn.addEventListener("click", () => { const r = resolveFn; closeModal(); if (r) r(null); });
modal.addEventListener("click", (e) => { if (e.target === modal) cancelBtn.click(); });

function doSave() {
  const enc = new TextEncoder();
  const files = order.map((path) => {
    const entry = working.get(path);
    if (!entry.editable) return { path, data: entry.data };
    const mounted = viewsByPath.get(path);
    const text = mounted?.view ? mounted.view.state.doc.toString() : entry.textInitial;
    return { path, data: enc.encode(text) };
  });
  const r = resolveFn;
  closeModal();
  if (r) r(files);
}
// Pas de gros boutons "Enregistrer"/"Annuler" : Ctrl/Cmd+S sauvegarde,
// Échap annule (l'icône × du titre fait la même chose que Échap).
document.addEventListener("keydown", (e) => {
  if (!modal.classList.contains("open")) return;
  if (e.key === "Escape") { cancelBtn.click(); return; }
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") { e.preventDefault(); doSave(); }
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
      working.set(f.path, { data: f.data, textInitial: text ?? "", editable: editable && text !== null });
    }
    tree = buildTree(order);
    expandedDirs = new Set(allDirPaths(tree));
    openTabs = []; activeTab = null; viewsByPath = new Map(); dirtyPaths = new Set();
    resolveFn = resolve;
    errorEl.textContent = "";
    modal.classList.add("open");
    renderTree(); renderTabs();
    const first = order.find((p) => working.get(p).editable) || order[0];
    if (first) openTab(first);
  });
}

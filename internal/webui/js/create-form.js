// Formulaire de création : source (URL ou projet importé), personnalisation
// (icône, splash, options), et soumission du build.
import { $, builds, saveLocal, removeBuildById } from "./core.js";
import { render, patch } from "./builds-view.js";
import { subscribe, pollThumbs } from "./realtime.js";
import { mb, processDistFiles, extractDistIcon } from "./zip.js";
// Dépendance circulaire assumée avec gen.js (qui importe getMode/clearDist/
// setDist/updatePreview/fail d'ici) : chaque module n'utilise les exports de
// l'autre qu'à l'intérieur de gestionnaires d'événements, jamais à
// l'évaluation du module, donc l'ordre de chargement n'a pas d'importance.
import { isGenMode, genStart } from "./gen.js";

const form = $("form"), submit = $("submit"), formError = $("formError");

export function fail(msg) { formError.textContent = msg; }

// fetch() n'expose pas la progression d'upload : XHR est nécessaire pour
// suivre l'envoi du dist.zip (parfois volumineux) sur /api/builds.
function postBuild(fd, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/builds");
    if (onProgress) {
      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total);
      });
    }
    xhr.addEventListener("load", () => {
      let data = {};
      try { data = JSON.parse(xhr.responseText); } catch { /* réponse non-JSON */ }
      resolve({ ok: xhr.status >= 200 && xhr.status < 300, data });
    });
    xhr.addEventListener("error", () => reject(new Error("Erreur réseau pendant l'envoi.")));
    xhr.send(fd);
  });
}

// ---- panneau "Personnaliser" dépliable ----
$("customToggle").addEventListener("click", (e) => {
  const t = e.currentTarget;
  const open = t.getAttribute("aria-expanded") === "true";
  t.setAttribute("aria-expanded", String(!open));
  $("customPanel").hidden = open;
  $("customStack").classList.toggle("open", !open);
});

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  formError.textContent = "";
  if (isGenMode() && mode !== "bundle") { genStart(); return; }

  const appName = $("name").value.trim();
  const pkg = $("pkg").value.trim();
  const vname = $("vname").value.trim(), vcode = $("vcode").value.trim();

  if (!appName) return fail("Le nom de l'application est requis.");
  if (pkg && !/^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/.test(pkg)) return fail("Package invalide (ex. com.exemple.monapp).");

  // Toujours en multipart : permet d'inclure icône + couleur de splash uniformément.
  const fd = new FormData();
  fd.append("app_name", appName);
  if (pkg) fd.append("package", pkg);
  if (vname) fd.append("version_name", vname);
  if (vcode) fd.append("version_code", vcode);
  fd.append("splash_bg", splashHex());
  fd.append("hide_scrollbar", $("hideScrollbar").checked ? "true" : "false");
  fd.append("debug_console", $("debugConsole").checked ? "true" : "false");
  fd.append("fix_images", $("fixImages").checked ? "true" : "false");
  fd.append("background_player", $("backgroundPlayer").checked ? "true" : "false");
  fd.append("picture_in_picture", $("pictureInPicture").checked ? "true" : "false");
  if (iconBlob) fd.append("icon", iconBlob, "icon.png");

  let listURL;
  if (mode === "bundle") {
    if (!distBlob) return fail("Choisis un dossier ou un .zip contenant index.html.");
    fd.append("dist", distBlob, "dist.zip");
    listURL = distName || "dist.zip";
  } else {
    const url = $("url").value.trim();
    if (!/^https?:\/\//.test(url)) return fail("L'URL doit commencer par http:// ou https://");
    fd.append("url", url);
    listURL = url;
  }

  submit.disabled = true;
  const showProgress = mode === "bundle";
  // Part de la barre de progression globale réservée à l'envoi (le reste
  // suit la compilation serveur) : une seule entrée, une seule progression
  // continue dans le dock "Builds en cours", pas deux barres qui se suivent.
  const UPLOAD_CAP = 15;
  let entry = null;
  if (showProgress) {
    entry = {
      id: "upload-" + Date.now(), status: "uploading", progress: 0, uploadCap: UPLOAD_CAP,
      app_name: appName, url: listURL, mode,
      icon: iconPreview || distIconPreview || null, splash: splashHex(),
    };
    builds.unshift(entry);
    render();
  }
  try {
    const { ok, data } = await postBuild(fd, entry ? (frac) => {
      entry.progress = Math.round(frac * UPLOAD_CAP);
      patch(entry);
    } : null);
    if (!ok) throw new Error(data.error || "Erreur serveur");

    if (entry) {
      Object.assign(entry, { id: data.id, status: data.status || "pending", progress: UPLOAD_CAP });
    } else {
      entry = {
        id: data.id, status: data.status || "pending",
        url: listURL, app_name: appName, mode,
        icon: iconPreview || distIconPreview || null, splash: splashHex(),
      };
      builds.unshift(entry);
    }
    saveLocal(); render(); subscribe(data.id);
    pollThumbs(0);
    form.reset(); clearDist(); clearIcon(); syncSplash(); updatePreview();
  } catch (err) {
    if (entry) { removeBuildById(entry.id); entry = null; render(); }
    fail(err.message);
  } finally {
    submit.disabled = false;
  }
});

// ---- icône + splash + aperçu ----
let iconBlob = null, iconURL = null, iconPreview = null;
const splashColor = $("splashColor"), splashHexInput = $("splashHex");

function splashHex() {
  const v = splashHexInput.value.trim();
  return /^#[0-9a-fA-F]{6}$/.test(v) ? v : splashColor.value;
}
function clearIcon() {
  iconBlob = null; iconPreview = null;
  if (iconURL) { URL.revokeObjectURL(iconURL); iconURL = null; }
  $("iconImg").style.backgroundImage = "";
  $("phoneIcon").style.backgroundImage = "";
  deselectIconSuggestion();
}
function setIcon(blob) {
  if (iconURL) URL.revokeObjectURL(iconURL);
  iconBlob = blob; iconURL = URL.createObjectURL(blob);
  $("iconImg").style.backgroundImage = `url(${iconURL})`;
  $("phoneIcon").style.backgroundImage = `url(${iconURL})`;
}
export function updatePreview() {
  $("phoneScreen").style.background = splashHex();
  $("phoneName").textContent = $("name").value.trim() || "Mon Application";
}
function syncSplash() {
  // aligne le champ hexa sur le sélecteur de couleur
  splashHexInput.value = splashColor.value.toUpperCase();
}

splashColor.addEventListener("input", () => { syncSplash(); updatePreview(); });
splashHexInput.addEventListener("input", () => {
  const v = splashHexInput.value.trim();
  if (/^#[0-9a-fA-F]{6}$/.test(v)) { splashColor.value = v; updatePreview(); }
});
$("name").addEventListener("input", updatePreview);

// --- cropper ---
const cropModal = $("cropModal"), cropCanvas = $("cropCanvas"), cctx = cropCanvas.getContext("2d");
const VIEW = 288;
let cImg = null, cScale = 1, cMin = 1, cx = 0, cy = 0, dragging = false, lastX = 0, lastY = 0;

$("iconTile").addEventListener("click", () => $("icon").click());
$("icon").addEventListener("change", (e) => {
  const f = e.target.files[0];
  if (!f) return;
  const img = new Image();
  img.onload = () => { openCropper(img); URL.revokeObjectURL(img.src); };
  img.onerror = () => fail("Image illisible.");
  img.src = URL.createObjectURL(f);
  e.target.value = ""; // permet de re-sélectionner le même fichier
});

function openCropper(img) {
  cImg = img;
  cMin = Math.max(VIEW / img.width, VIEW / img.height);
  cScale = cMin;
  $("zoom").min = cMin; $("zoom").max = cMin * 4; $("zoom").value = cMin;
  cx = (VIEW - img.width * cScale) / 2;
  cy = (VIEW - img.height * cScale) / 2;
  drawCrop();
  cropModal.classList.add("open");
}
function clampCrop() {
  const w = cImg.width * cScale, h = cImg.height * cScale;
  cx = Math.min(0, Math.max(VIEW - w, cx));
  cy = Math.min(0, Math.max(VIEW - h, cy));
}
function drawCrop() {
  clampCrop();
  cctx.clearRect(0, 0, VIEW, VIEW);
  cctx.drawImage(cImg, cx, cy, cImg.width * cScale, cImg.height * cScale);
}
$("zoom").addEventListener("input", (e) => {
  const ns = +e.target.value, k = ns / cScale;
  // zoom centré
  cx = VIEW / 2 - (VIEW / 2 - cx) * k;
  cy = VIEW / 2 - (VIEW / 2 - cy) * k;
  cScale = ns; drawCrop();
});
function cpt(e) { const r = cropCanvas.getBoundingClientRect();
  const s = VIEW / r.width; const p = e.touches ? e.touches[0] : e;
  return { x: (p.clientX - r.left) * s, y: (p.clientY - r.top) * s }; }
function dStart(e) { dragging = true; const p = cpt(e); lastX = p.x; lastY = p.y; }
function dMove(e) { if (!dragging) return; e.preventDefault(); const p = cpt(e);
  cx += p.x - lastX; cy += p.y - lastY; lastX = p.x; lastY = p.y; drawCrop(); }
function dEnd() { dragging = false; }
cropCanvas.addEventListener("mousedown", dStart);
window.addEventListener("mousemove", dMove);
window.addEventListener("mouseup", dEnd);
cropCanvas.addEventListener("touchstart", dStart, { passive: false });
cropCanvas.addEventListener("touchmove", dMove, { passive: false });
cropCanvas.addEventListener("touchend", dEnd);

$("cropCancel").addEventListener("click", () => cropModal.classList.remove("open"));
$("cropOk").addEventListener("click", () => {
  const out = document.createElement("canvas"); out.width = out.height = 512;
  const octx = out.getContext("2d");
  const k = 512 / VIEW;
  octx.drawImage(cImg, cx * k, cy * k, cImg.width * cScale * k, cImg.height * cScale * k);
  // Petit aperçu (128px) persisté en localStorage pour les cartes.
  const prev = document.createElement("canvas"); prev.width = prev.height = 128;
  prev.getContext("2d").drawImage(out, 0, 0, 128, 128);
  iconPreview = prev.toDataURL("image/png");
  out.toBlob((blob) => { if (blob) setIcon(blob); }, "image/png");
  cropModal.classList.remove("open");
  deselectIconSuggestion();
});

// ---- galerie de suggestions d'icône (pictogrammes réels sur fond dégradé) ----
const iconSuggestions = $("iconSuggestions");
// Glyphes ligne (style Feather, viewBox 24x24) — mêmes conventions que ICONS
// dans core.js (stroke, pas de remplissage), un par teinte de dégradé.
const ICON_SUGGESTIONS = [
  { hue: 18,  d: "M9 18V5l12-2v13M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM21 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z" }, // musique
  { hue: 48,  d: "M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z M15.2 13a3.2 3.2 0 1 1-6.4 0 3.2 3.2 0 0 1 6.4 0z" }, // caméra
  { hue: 95,  d: "M1 1h4l2.68 13.39a2 2 0 0 0 2 1.61h9.72a2 2 0 0 0 2-1.61L23 6H6" }, // panier
  { hue: 150, d: "M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.6l-1-1a5.5 5.5 0 0 0-7.8 7.8l1 1L12 21l7.8-7.6 1-1a5.5 5.5 0 0 0 0-7.8z" }, // cœur
  { hue: 190, d: "M21 11.5a8.4 8.4 0 0 1-8.5 8.4 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8A8.4 8.4 0 0 1 12.5 3a8.5 8.5 0 0 1 8.5 8.5z" }, // chat
  { hue: 230, d: "M13 2 3 14h7l-1 8 11-14h-7z" }, // éclair
  { hue: 275, d: "M12 2l2.9 6.9 7.4.6-5.6 4.9 1.7 7.3L12 17.9 5.6 21.7l1.7-7.3L1.7 9.5l7.4-.6L12 2z" }, // étoile
  { hue: 320, d: "M4 19.5A2.5 2.5 0 0 1 6.5 17H20 M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" }, // livre
];
function iconBg(hue) {
  return `linear-gradient(150deg,hsl(${hue},55%,55%),hsl(${(hue + 40) % 360},60%,32%))`;
}
function deselectIconSuggestion() {
  const sel = iconSuggestions.querySelector(".selected");
  if (sel) sel.classList.remove("selected");
}
function renderIconSuggestions() {
  iconSuggestions.innerHTML = ICON_SUGGESTIONS.map((s, i) => `
    <button type="button" class="icon-suggestion" data-i="${i}" title="Utiliser cette icône"
      style="background:${iconBg(s.hue)}">
      <svg class="icon-suggestion-svg" viewBox="0 0 24 24"><path d="${s.d}"/></svg>
    </button>`).join("");
}
function applyIconSuggestion(btn) {
  const s = ICON_SUGGESTIONS[+btn.dataset.i];
  const size = 512;
  const c = document.createElement("canvas"); c.width = c.height = size;
  const ictx = c.getContext("2d");
  const grad = ictx.createLinearGradient(0, 0, size, size);
  grad.addColorStop(0, `hsl(${s.hue},55%,55%)`);
  grad.addColorStop(1, `hsl(${(s.hue + 40) % 360},60%,32%)`);
  ictx.fillStyle = grad; ictx.fillRect(0, 0, size, size);
  // Glyphe centré, avec une marge (comme la zone de sécurité d'une icône adaptative).
  const scale = (size * 0.56) / 24;
  ictx.translate((size - 24 * scale) / 2, (size - 24 * scale) / 2);
  ictx.scale(scale, scale);
  ictx.strokeStyle = "rgba(255,255,255,.92)";
  ictx.lineWidth = 2; ictx.lineCap = "round"; ictx.lineJoin = "round";
  ictx.stroke(new Path2D(s.d));
  const prev = document.createElement("canvas"); prev.width = prev.height = 128;
  prev.getContext("2d").drawImage(c, 0, 0, 128, 128);
  iconPreview = prev.toDataURL("image/png");
  c.toBlob((blob) => { if (blob) setIcon(blob); }, "image/png");
  deselectIconSuggestion();
  btn.classList.add("selected");
}
iconSuggestions.addEventListener("click", (e) => {
  const btn = e.target.closest(".icon-suggestion");
  if (btn) applyIconSuggestion(btn);
});
renderIconSuggestions();

syncSplash(); updatePreview();

// ---- source : URL (défaut) ou projet importé (bundle), mode implicite ----
let mode = "url";
export function getMode() { return mode; }
function enterURLMode() {
  mode = "url";
  $("summarySection").hidden = true;
  $("dropZone").classList.remove("has-project");
  $("url").disabled = false;
  formError.textContent = "";
}
function enterBundleMode(label) {
  mode = "bundle";
  $("projectName").textContent = label;
  $("summarySection").hidden = false;
  $("dropZone").classList.add("has-project");
  $("url").disabled = true;
  formError.textContent = "";
}
$("projectClear").addEventListener("click", () => clearDist());

// ---- dist : dossier ou .zip ----
let distBlob = null, distName = "", distIconPreview = null;
export function setDist(blob, name) { distBlob = blob; distName = name; enterBundleMode(name); }
export function clearDist() { distBlob = null; distName = ""; distIconPreview = null; enterURLMode(); }

async function ingest(list) {
  if (!list.length) return;
  formError.textContent = ""; enterBundleMode("Préparation…");
  try {
    const { blob, folder, count, files } = await processDistFiles(list);
    setDist(blob, `${folder}/ · ${count} fichiers (${mb(blob.size)})`);
    distIconPreview = await extractDistIcon(files); // aperçu depuis l'icône du projet
  } catch (err) { clearDist(); fail(err.message); }
}

// Parcours récursif d'une entrée déposée (drag-drop de dossier).
function readDirEntries(reader) {
  return new Promise((resolve, reject) => {
    const all = [];
    const rd = () => reader.readEntries((es) => { if (!es.length) resolve(all); else { all.push(...es); rd(); } }, reject);
    rd();
  });
}
async function walkEntry(entry, prefix, out) {
  if (entry.isFile) {
    const file = await new Promise((res, rej) => entry.file(res, rej));
    out.push({ path: prefix + entry.name, file });
  } else if (entry.isDirectory) {
    const es = await readDirEntries(entry.createReader());
    for (const c of es) await walkEntry(c, prefix + entry.name + "/", out);
  }
}

const dropZone = $("dropZone");

// Bouton unique "Importer" -> menu (Dossier / Fichier .zip).
const importBtn = $("importBtn"), importMenu = $("importMenu");
function closeImportMenu() { importMenu.hidden = true; importBtn.setAttribute("aria-expanded", "false"); }
function toggleImportMenu() {
  const open = importBtn.getAttribute("aria-expanded") === "true";
  importMenu.hidden = open;
  importBtn.setAttribute("aria-expanded", String(!open));
}
importBtn.addEventListener("click", (e) => { e.stopPropagation(); toggleImportMenu(); });
document.addEventListener("click", (e) => {
  if (!importMenu.hidden && !importMenu.contains(e.target) && e.target !== importBtn) closeImportMenu();
});
$("pickZip").addEventListener("click", () => { closeImportMenu(); $("dist").click(); });
$("pickDir").addEventListener("click", () => { closeImportMenu(); $("distDir").click(); });
$("dist").addEventListener("change", () => { const f = $("dist").files[0]; if (f) setDist(f, `${f.name} (${mb(f.size)})`); });
$("distDir").addEventListener("change", () =>
  ingest([...$("distDir").files].map((f) => ({ path: f.webkitRelativePath || f.name, file: f }))));

["dragover", "dragenter"].forEach((ev) =>
  dropZone.addEventListener(ev, (e) => { e.preventDefault(); dropZone.classList.add("drag"); }));
["dragleave"].forEach((ev) =>
  dropZone.addEventListener(ev, (e) => { e.preventDefault(); if (e.target === dropZone) dropZone.classList.remove("drag"); }));
dropZone.addEventListener("drop", async (e) => {
  e.preventDefault(); dropZone.classList.remove("drag");
  const items = e.dataTransfer.items;
  if (items && items.length && items[0].webkitGetAsEntry) {
    const roots = [];
    for (const it of items) { const en = it.webkitGetAsEntry(); if (en) roots.push(en); }
    if (roots.length === 1 && roots[0].isFile && /\.zip$/i.test(roots[0].name)) {
      const file = await new Promise((res, rej) => roots[0].file(res, rej));
      return setDist(file, `${file.name} (${mb(file.size)})`);
    }
    if (roots.length) {
      const collected = [];
      for (const r of roots) await walkEntry(r, "", collected);
      return ingest(collected);
    }
  }
  const f = e.dataTransfer.files[0];
  if (f && /\.zip$/i.test(f.name)) setDist(f, `${f.name} (${mb(f.size)})`);
  else fail("Dépose un dossier ou un fichier .zip.");
});

// Aperçu du package déduit (miroir de derivePackage côté serveur).
function derivePackage(name) {
  let slug = name.toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 30);
  if (!slug || !/^[a-z]/.test(slug)) slug = "app" + slug;
  return "app.webview." + slug;
}
$("name").addEventListener("input", (e) => {
  const name = e.target.value.trim();
  $("pkg").placeholder = name ? derivePackage(name) : "app.webview.monapplication";
});

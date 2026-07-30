// Constructeur de ZIP (méthode "store", sans dépendance externe) + extraction
// d'une icône depuis les fichiers d'un projet importé.
export const mb = (bytes) => bytes < 1048576 ? `${Math.max(1, Math.round(bytes / 1024))} Ko` : `${(bytes / 1048576).toFixed(1)} MB`;

const IMG_MIME = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", webp: "image/webp", gif: "image/gif", svg: "image/svg+xml", ico: "image/x-icon" };
function mimeOfPath(p) { return IMG_MIME[p.split(".").pop().toLowerCase()] || "application/octet-stream"; }

// Redimensionne une image (blob) en dataURL carré de `size`px (contain).
function downscaleToDataURL(blob, size) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(blob);
    const img = new Image();
    img.onload = () => {
      try {
        const c = document.createElement("canvas"); c.width = c.height = size;
        const ctx = c.getContext("2d");
        const r = Math.min(size / img.width, size / img.height);
        const w = img.width * r, h = img.height * r;
        ctx.drawImage(img, (size - w) / 2, (size - h) / 2, w, h);
        resolve(c.toDataURL("image/png"));
      } catch (e) { reject(e); } finally { URL.revokeObjectURL(url); }
    };
    img.onerror = () => { URL.revokeObjectURL(url); reject(new Error("img")); };
    img.src = url;
  });
}

// Choisit la meilleure icône parmi les fichiers du dist et en fait un aperçu.
export async function extractDistIcon(files) {
  const imgs = files.filter(f => /\.(png|jpe?g|webp|svg|ico)$/i.test(f.path));
  if (!imgs.length) return null;
  const score = (f) => {
    const p = f.path.toLowerCase(); let s = f.data.length;
    if (/icon|favicon|logo|apple-touch/.test(p)) s += 5e6;
    if (/192|180|256|512/.test(p)) s += 2e6;
    if (p.endsWith(".svg")) s += 1e6;
    if (p.endsWith(".ico")) s -= 2e6;
    return s;
  };
  imgs.sort((a, b) => score(b) - score(a));
  for (const f of imgs.slice(0, 4)) {
    try { return await downscaleToDataURL(new Blob([f.data], { type: mimeOfPath(f.path) }), 128); }
    catch { /* essaie le suivant */ }
  }
  return null;
}

// Constructeur de ZIP (méthode "store", sans dépendance externe).
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1); t[n] = c >>> 0; }
  return t;
})();
function crc32(buf) {
  let c = 0xFFFFFFFF;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xFF] ^ (c >>> 8);
  return (c ^ 0xFFFFFFFF) >>> 0;
}
export function makeZip(files) {
  const enc = new TextEncoder(), parts = [], central = [];
  let offset = 0;
  for (const f of files) {
    const name = enc.encode(f.path), crc = crc32(f.data), size = f.data.length;
    const lh = new DataView(new ArrayBuffer(30));
    lh.setUint32(0, 0x04034b50, true); lh.setUint16(4, 20, true);
    lh.setUint32(14, crc, true); lh.setUint32(18, size, true); lh.setUint32(22, size, true);
    lh.setUint16(26, name.length, true);
    parts.push(new Uint8Array(lh.buffer), name, f.data);
    const ch = new DataView(new ArrayBuffer(46));
    ch.setUint32(0, 0x02014b50, true); ch.setUint16(4, 20, true); ch.setUint16(6, 20, true);
    ch.setUint32(16, crc, true); ch.setUint32(20, size, true); ch.setUint32(24, size, true);
    ch.setUint16(28, name.length, true); ch.setUint32(42, offset, true);
    central.push(new Uint8Array(ch.buffer), name);
    offset += 30 + name.length + size;
  }
  let cdSize = 0; for (const c of central) cdSize += c.length;
  const eocd = new DataView(new ArrayBuffer(22));
  eocd.setUint32(0, 0x06054b50, true);
  eocd.setUint16(8, files.length, true); eocd.setUint16(10, files.length, true);
  eocd.setUint32(12, cdSize, true); eocd.setUint32(16, offset, true);
  return new Blob([...parts, ...central, new Uint8Array(eocd.buffer)], { type: "application/zip" });
}

// Lit un zip (méthode "store" ou "deflate") en mémoire -> liste {path, data}.
// Utilisé par l'éditeur de code pour ouvrir un dist déjà zippé (projet importé
// en .zip, dist.zip généré par IA côté serveur, ou source.zip d'un build existant).
export async function readZip(blob) {
  const buf = new Uint8Array(await blob.arrayBuffer());
  const dv = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  let eocd = -1;
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 22 - 65536); i--) {
    if (dv.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd < 0) throw new Error("Archive zip invalide.");
  const count = dv.getUint16(eocd + 10, true);
  let off = dv.getUint32(eocd + 16, true);
  const dec = new TextDecoder();
  const files = [];
  for (let i = 0; i < count; i++) {
    if (dv.getUint32(off, true) !== 0x02014b50) throw new Error("Archive zip invalide (entrée centrale).");
    const method = dv.getUint16(off + 10, true);
    const compSize = dv.getUint32(off + 20, true);
    const nameLen = dv.getUint16(off + 28, true);
    const extraLen = dv.getUint16(off + 30, true);
    const commentLen = dv.getUint16(off + 32, true);
    const lhOff = dv.getUint32(off + 42, true);
    const name = dec.decode(buf.subarray(off + 46, off + 46 + nameLen));
    off += 46 + nameLen + extraLen + commentLen;
    if (name.endsWith("/")) continue; // dossier : pas d'entrée de données
    const lhNameLen = dv.getUint16(lhOff + 26, true);
    const lhExtraLen = dv.getUint16(lhOff + 28, true);
    const dataStart = lhOff + 30 + lhNameLen + lhExtraLen;
    const compData = buf.subarray(dataStart, dataStart + compSize);
    let data;
    if (method === 0) {
      data = compData;
    } else if (method === 8) {
      const stream = new Blob([compData]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
      data = new Uint8Array(await new Response(stream).arrayBuffer());
    } else {
      throw new Error(`Méthode de compression non supportée (${method}) pour ${name}.`);
    }
    files.push({ path: name, data });
  }
  return files;
}

// Transforme une liste {path, file} en un zip prêt à uploader.
export async function processDistFiles(list) {
  const junk = (n) => { const b = n.split("/").pop(); return b === ".DS_Store" || b === "Thumbs.db" || b.startsWith("._"); };
  const folder = (list[0]?.path.split("/")[0]) || "dist";
  const files = [];
  let hasIndex = false;
  for (const e of list) {
    if (junk(e.path)) continue;
    const rel = e.path.split("/").slice(1).join("/"); // retire le dossier racine
    if (!rel) continue;
    if (/(^|\/)index\.html$/i.test(rel)) hasIndex = true;
    files.push({ path: rel, data: new Uint8Array(await e.file.arrayBuffer()) });
  }
  if (!hasIndex) throw new Error("Le dossier doit contenir un index.html.");
  return { blob: makeZip(files), folder, count: files.length, files };
}

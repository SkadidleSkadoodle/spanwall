// Spanwall live runtime. A scene fills a character grid laid out in desk mm;
// this page draws it on the GPU through each monitor's measured shape, so
// characters line up across bezels. One page covers every monitor.
//   /live/            the wallpaper (inside Spanwall's window behind the icons)
//   /live/?preview=1  the whole desk, flat, for the editor
//   /live/?editor=1   the scene's whole canvas animated behind the layout editor
//                     (framing and settings come from the editor by postMessage)
//   &scene=name       another scene than the active one

const Q = new URLSearchParams(location.search);
const SNAP = Q.has("snapshot");
const WORLD = SNAP && Q.has("world"); // snapshot of the scene's whole canvas
const EDITOR = Q.has("editor"); // behind the layout editor
const PREVIEW = Q.has("preview") || SNAP || EDITOR; // not the wallpaper: no reports
let stopped = false;
const host = document.getElementById("views");

const SW = 40, SH = 72, PAD = 4, AW = 2048, AH = 2048; // atlas: slot size, padding, size
const PER_ROW = Math.floor(AW / SW), SLOTS = PER_ROW * Math.floor(AH / SH);
const MAX_CELLS = 400000;
const DEFAULT_FONT = "'Cascadia Mono', Consolas, 'Courier New', monospace";

let st = null, scene = null, g = null, FR = null, views = [], raf = 0, timer = 0, failed = null;
let coveredRects = [], live = [], t0 = performance.now(), next = 0, step = 1000 / 30;
let stats = { frames: 0, ms: 0 }, readySent = false, loading = null;
let EV = { vb: null }; // editor mode: the editor's viewBox (desk mm shown)

// ---- grid: what scenes see ---------------------------------------------------

const RAMP = " .:-=+*#%@";

function hash(x, y) { // integer hash -> [0,1)
  let h = (Math.imul(x | 0, 374761393) + Math.imul(y | 0, 668265263)) | 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967296;
}
function rand(seed) { // mulberry32
  let a = seed >>> 0;
  return () => { a = (a + 0x6d2b79f5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
}
function noise(x, y, z = 0) { // smooth value noise in [0,1)
  const xi = Math.floor(x), yi = Math.floor(y), zi = Math.floor(z);
  const xf = x - xi, yf = y - yi, zf = z - zi;
  const u = xf * xf * (3 - 2 * xf), v = yf * yf * (3 - 2 * yf), w = zf * zf * (3 - 2 * zf);
  const h = (a, b, c) => hash(a + Math.imul(c, 1013), b - Math.imul(c, 7919));
  const l = (a, b, t) => a + (b - a) * t;
  return l(l(l(h(xi, yi, zi), h(xi + 1, yi, zi), u), l(h(xi, yi + 1, zi), h(xi + 1, yi + 1, zi), u), v),
    l(l(h(xi, yi, zi + 1), h(xi + 1, yi, zi + 1), u), l(h(xi, yi + 1, zi + 1), h(xi + 1, yi + 1, zi + 1), u), v), w);
}
const clamp8 = (v) => (v < 0 ? 0 : v > 255 ? 255 : v | 0);
function rgb(r, g, b) { return (clamp8(r) << 16) | (clamp8(g) << 8) | clamp8(b); }
function hsl(h, s, l) { // h in turns (0..1), s and l 0..1
  h = ((h % 1) + 1) % 1;
  const a = s * Math.min(l, 1 - l), f = (n) => { const k = (n + h * 12) % 12; return l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)); };
  return rgb(f(0) * 255, f(8) * 255, f(4) * 255);
}
function mix(a, b, t) {
  return rgb(((a >> 16) & 255) * (1 - t) + ((b >> 16) & 255) * t, ((a >> 8) & 255) * (1 - t) + ((b >> 8) & 255) * t, (a & 255) * (1 - t) + (b & 255) * t);
}
function scale(c, k) { return rgb(((c >> 16) & 255) * k, ((c >> 8) & 255) * k, (c & 255) * k); }
const cp = (ch) => (typeof ch === "number" ? ch : ch ? ch.codePointAt(0) : 32);

// makeFrame places the scene's canvas (world, mm) behind the monitors the
// way the editor places an image: cover the monitors' box, zoom, move by
// dx/dy (fractions of the canvas), mirror, turn clockwise about its centre.
function makeFrame(desk, f) {
  f = f || {};
  const [bx, by, bw, bh] = desk.box, [W, H] = desk.world;
  const z = f.zoom > 0 ? f.zoom : 1.5;
  const m = Math.max(bw / W, bh / H) * z, rw = W * m, rh = H * m; // m: desk mm per canvas mm
  const cx = bx + bw / 2 + (f.dx || 0) * rw, cy = by + bh / 2 + (f.dy || 0) * rh;
  const a = ((f.rotate || 0) * Math.PI) / 180, c = Math.cos(a), sn = Math.sin(a), fx = f.flipX ? -1 : 1, fy = f.flipY ? -1 : 1;
  // canvas = A * desk + T
  const A = [(fx * c) / m, (fx * sn) / m, (-fy * sn) / m, (fy * c) / m]; // row-major
  const T = [W / 2 - (A[0] * cx + A[1] * cy), H / 2 - (A[2] * cx + A[3] * cy)];
  return {
    m, W, H, A, T,
    toWorld: (x, y) => [A[0] * x + A[1] * y + T[0], A[2] * x + A[3] * y + T[1]],
    toDesk(wx, wy) { const ex = (wx - W / 2) * m * fx, ey = (wy - H / 2) * m * fy; return [cx + c * ex - sn * ey, cy + sn * ex + c * ey]; },
  };
}

function makeGrid(desk, def, F) {
  let ch = st.charMM || (def.cell && def.cell.h) || 4.2;
  const aspect = (def.cell && def.cell.aspect) || 0.55;
  // ch is in canvas mm: at the default framing (1:1) that is its size on the
  // monitors, and zooming the canvas zooms the characters with it.
  const w = F.W, h = F.H, x0 = 0, y0 = 0;
  while (Math.ceil(w / (ch * aspect)) * Math.ceil(h / ch) > MAX_CELLS) ch *= 1.1;
  const cw = ch * aspect, cols = Math.ceil(w / cw), rows = Math.ceil(h / ch), n = cols * rows;
  const G = {
    cols, rows, n, cw, ch, x0, y0, width: w, height: h,
    chars: new Uint32Array(n).fill(32), fg: new Uint32Array(n).fill(0xffffff), bg: new Uint32Array(n), mask: new Uint8Array(n),
    bgUsed: false, monitors: [], main: null, preview: PREVIEW, params: {}, paramsVersion: 0, location: st.location || null,
    idx: (c, r) => r * cols + c,
    dt: 0, frameNo: 0, _ph: {},
    // phase(name, rate): time scaled by a rate that may change (a speed setting).
    // It advances by rate x frame time once per frame, so changing the rate
    // changes how fast things move from now on instead of jumping them.
    phase(name, rate) {
      const p = G._ph[name] || (G._ph[name] = { v: 0, f: -1 });
      if (p.f !== G.frameNo) { p.v += rate * G.dt; p.f = G.frameNo; }
      return p.v;
    },
    put(c, r, chr, fg, bg) {
      if (c < 0 || r < 0 || c >= cols || r >= rows) return;
      const i = r * cols + c; G.chars[i] = cp(chr);
      if (fg !== undefined) G.fg[i] = fg;
      if (bg !== undefined && bg !== null) { G.bg[i] = bg < 0 ? 0 : (0xff000000 | bg) >>> 0; G.bgUsed = true; }
    },
    text(c, r, s, fg, bg) { let k = 0; for (const chr of String(s)) G.put(c + k++, r, chr, fg, bg); },
    clear(chr = 32, fg = 0xffffff, bg) {
      G.chars.fill(cp(chr)); G.fg.fill(fg);
      if (bg !== undefined && bg !== null) { G.bg.fill(bg < 0 ? 0 : (0xff000000 | bg) >>> 0); G.bgUsed = true; } else if (G.bgUsed) G.bg.fill(0);
    },
    ramp: (v, chars = RAMP) => chars.codePointAt(Math.max(0, Math.min(chars.length - 1, (v * chars.length) | 0))),
    mm: (c, r) => [x0 + (c + 0.5) * cw, y0 + (r + 0.5) * ch],
    cell: (xmm, ymm) => [Math.floor((xmm - x0) / cw), Math.floor((ymm - y0) / ch)],
    rgb, hsl, mix, scale, hash, rand, noise,
  };
  placeMonitors(G, desk, F);
  return G;
}

// placeMonitors works out which cells land on a monitor (the mask: scenes
// can skip the rest) and each monitor's cell box, for the current framing.
function placeMonitors(G, desk, F) {
  const { cols, rows, cw, ch } = G;
  G.mask.fill(WORLD || EDITOR ? 1 : 0); // the canvas view and the editor show the whole canvas
  G.monitors = [];
  for (const m of desk.monitors) {
    const poly = outline(m).map(([x, y]) => F.toWorld(x, y));
    let bx0 = Infinity, by0 = Infinity, bx1 = -Infinity, by1 = -Infinity;
    for (const [x, y] of poly) { bx0 = Math.min(bx0, x); by0 = Math.min(by0, y); bx1 = Math.max(bx1, x); by1 = Math.max(by1, y); }
    const c0 = Math.max(0, Math.floor(bx0 / cw)), r0 = Math.max(0, Math.floor(by0 / ch));
    const c1 = Math.min(cols - 1, Math.floor(bx1 / cw)), r1 = Math.min(rows - 1, Math.floor(by1 / ch));
    if (!WORLD && !EDITOR) for (let r = r0; r <= r1; r++) for (let c = c0; c <= c1; c++) {
      if (inside(poly, (c + 0.5) * cw, (r + 0.5) * ch)) G.mask[r * cols + c] = 1;
    }
    G.monitors.push({ id: m.id, name: m.name, primary: !!m.primary, x: bx0, y: by0, w: bx1 - bx0, h: by1 - by0, c0, r0, c1, r1, cc: ((c0 + c1) / 2) | 0, cr: ((r0 + r1) / 2) | 0 });
  }
  G.main = G.monitors.find((m) => m.primary) || G.monitors.reduce((a, b) => (!a || b.w * b.h > a.w * a.h ? b : a), null) || { c0: 0, r0: 0, c1: cols - 1, r1: rows - 1, cc: cols >> 1, cr: rows >> 1 };
  // g.home / g.anchor: the same boxes under the default framing. Scenes lay
  // their content out from these, so the user's framing (and rebuilding on a
  // settings change) moves the whole canvas consistently instead of
  // re-centring the content on wherever the monitor now sits.
  const D = makeFrame(desk, null);
  G.home = desk.monitors.map((m) => {
    const poly = outline(m).map(([x, y]) => D.toWorld(x, y));
    let bx0 = Infinity, by0 = Infinity, bx1 = -Infinity, by1 = -Infinity;
    for (const [x, y] of poly) { bx0 = Math.min(bx0, x); by0 = Math.min(by0, y); bx1 = Math.max(bx1, x); by1 = Math.max(by1, y); }
    const c0 = Math.max(0, Math.floor(bx0 / cw)), r0 = Math.max(0, Math.floor(by0 / ch)), c1 = Math.min(cols - 1, Math.floor(bx1 / cw)), r1 = Math.min(rows - 1, Math.floor(by1 / ch));
    return { id: m.id, name: m.name, primary: !!m.primary, x: bx0, y: by0, w: bx1 - bx0, h: by1 - by0, c0, r0, c1, r1, cc: ((c0 + c1) / 2) | 0, cr: ((r0 + r1) / 2) | 0 };
  });
  G.anchor = G.home.find((m) => m.primary) || G.home.reduce((a, b) => (!a || b.w * b.h > a.w * a.h ? b : a), null) || G.main;
}

function outline(m) { // boundary of a monitor's mesh, in mm
  const p = [], at = (i, j) => [m.mm[2 * (j * m.nx + i)], m.mm[2 * (j * m.nx + i) + 1]];
  for (let i = 0; i < m.nx; i++) p.push(at(i, 0));
  for (let j = 1; j < m.ny; j++) p.push(at(m.nx - 1, j));
  for (let i = m.nx - 2; i >= 0; i--) p.push(at(i, m.ny - 1));
  for (let j = m.ny - 2; j > 0; j--) p.push(at(0, j));
  return p;
}
function inside(poly, x, y) {
  let a = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const [xi, yi] = poly[i], [xj, yj] = poly[j];
    if ((yi > y) !== (yj > y) && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) a = !a;
  }
  return a;
}

// ---- glyph atlas: one 2D canvas shared by every view --------------------------------

const A = { canvas: document.createElement("canvas"), ctx: null, ascii: new Int16Array(128), map: new Map(), next: 1, version: 0, font: "" };
A.canvas.width = AW; A.canvas.height = AH;
A.ctx = A.canvas.getContext("2d", { alpha: false });
let glyphs = new Uint16Array(0); // atlas slot per cell, for the current frame

function setFont(font) {
  if (A.font === font) return;
  A.font = font; A.ascii.fill(-1); A.map.clear(); A.next = 1; A.version++;
  A.ctx.fillStyle = "#000"; A.ctx.fillRect(0, 0, AW, AH);
}

function addGlyph(code) {
  if (A.next >= SLOTS) return 0;
  const s = A.next++, x = (s % PER_ROW) * SW, y = Math.floor(s / PER_ROW) * SH, ac = A.ctx;
  const iw = SW - 2 * PAD, ih = SH - 2 * PAD;
  ac.save(); ac.beginPath(); ac.rect(x, y, SW, SH); ac.clip();
  const block = { 0x2588: [0, 0, 1, 1], 0x2580: [0, 0, 1, 0.5], 0x2584: [0, 0.5, 1, 0.5], 0x258c: [0, 0, 0.5, 1], 0x2590: [0.5, 0, 0.5, 1] }[code];
  const shade = { 0x2591: 0.25, 0x2592: 0.5, 0x2593: 0.75 }[code];
  if (block || shade) { // cell-filling blocks reach into the padding so neighbours join without seams
    const [bx, by, bw, bh] = block || [0, 0, 1, 1];
    ac.fillStyle = `rgba(255,255,255,${shade || 1})`;
    const X = (v, full) => (v === 0 ? 0 : v === 1 ? full : PAD + v * (full - 2 * PAD));
    ac.fillRect(x + X(bx, SW), y + X(by, SH), X(bx + bw, SW) - X(bx, SW), X(by + bh, SH) - X(by, SH));
  } else {
    ac.fillStyle = "#fff"; ac.textAlign = "center"; ac.textBaseline = "middle";
    ac.font = `${Math.round(ih * 0.8)}px ${A.font}`;
    const chr = String.fromCodePoint(code), mw = ac.measureText(chr).width;
    if (mw > iw) { ac.translate(x + SW / 2, 0); ac.scale(iw / mw, 1); ac.fillText(chr, 0, y + PAD + ih * 0.54); }
    else ac.fillText(chr, x + SW / 2, y + PAD + ih * 0.54);
  }
  ac.restore();
  if (code < 128) A.ascii[code] = s; else A.map.set(code, s);
  A.version++;
  return s;
}

function mapGlyphs(G) {
  if (glyphs.length !== G.n) glyphs = new Uint16Array(G.n);
  const ch = G.chars, out = glyphs, ascii = A.ascii;
  let lastC = -1, lastS = 0;
  for (let i = 0; i < G.n; i++) {
    let c = ch[i];
    if (c !== lastC) {
      lastC = c;
      if (CS) c = remap(c);
      if (c === 32 || c === 0) lastS = 0;
      else if (c < 128) { lastS = ascii[c]; if (lastS < 0) lastS = addGlyph(c); }
      else { lastS = A.map.get(c); if (lastS === undefined) lastS = addGlyph(c); }
    }
    out[i] = lastS;
  }
}

// ---- character sets: draw any scene with other characters of the same density --------

const CHARSETS = {
  ascii: " .'`^\",:;Il!i><~+_-?][}{1)(|/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$",
  blocks: " ░▒▓█",
  braille: "⠀⠁⠃⠇⡇⡏⡟⡿⣿⠂⠆⠖⠶⠷⠿",
  katakana: " ･ｰｧｨｩｪｫｬｭｮｯｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ",
  binary: " 01",
  dots: " .·•●",
  lines: " ·-─=═┼╬█",
};
let CS = null; // the active set: { cps, dens } sorted by density, plus a cache
const densCache = new Map();
function density(code) { // share of the cell the glyph inks
  if (densCache.has(code)) return densCache.get(code);
  const c = density.c || (density.c = Object.assign(document.createElement("canvas"), { width: 24, height: 40 }).getContext("2d", { willReadFrequently: true }));
  c.fillStyle = "#000"; c.fillRect(0, 0, 24, 40);
  c.fillStyle = "#fff"; c.textAlign = "center"; c.textBaseline = "middle"; c.font = `32px ${A.font}`;
  c.fillText(String.fromCodePoint(code), 12, 21);
  const d = c.getImageData(0, 0, 24, 40).data;
  let sum = 0;
  for (let k = 0; k < d.length; k += 4) sum += d[k];
  const v = sum / (255 * 24 * 40);
  densCache.set(code, v);
  return v;
}
function setCharset(name) {
  const chars = CHARSETS[name];
  if (!chars) { CS = null; return; }
  const cps = [...new Set(Array.from(chars, (c) => c.codePointAt(0)))].map((c) => [c, c === 32 || c === 0x2800 ? 0 : density(c)]).sort((a, b) => a[1] - b[1]);
  const max = cps[cps.length - 1][1] || 1;
  CS = { cps: cps.map((x) => x[0]), dens: cps.map((x) => x[1] / max), ref: density(64) || 0.3, map: new Map() };
}
function remap(code) {
  if (code === 32 || code === 0) return code;
  let m = CS.map.get(code);
  if (m === undefined) {
    const v = Math.min(1, density(code) / CS.ref); // how dark this glyph is, '@' = 1
    let best = 0;
    for (let k = 1; k < CS.dens.length; k++) if (Math.abs(CS.dens[k] - v) < Math.abs(CS.dens[best] - v)) best = k;
    m = v > 0.02 && CS.cps[best] === 32 ? CS.cps[Math.min(1, CS.cps.length - 1)] : CS.cps[best];
    CS.map.set(code, m);
  }
  return m;
}

// ---- colour themes: tint any scene on the GPU -----------------------------------------

const THEMES = { // dark and bright ends of the tint
  phosphor: ["#03260f", "#b8ffcb"], amber: ["#2a1200", "#ffd27a"], ice: ["#061a2e", "#e0f6ff"],
  mono: ["#151515", "#ffffff"], synthwave: ["#2b1055", "#ff7ad9"], ocean: ["#021d2e", "#5ef2d6"], blood: ["#1e0000", "#ff5a4a"],
};
let theme = null; // [[r,g,b],[r,g,b]] 0..1, or null for the scene's own colours
const hex3 = (h) => { const v = parseColor(h, 0); return [((v >> 16) & 255) / 255, ((v >> 8) & 255) / 255, (v & 255) / 255]; };

// ---- per-scene settings ------------------------------------------------------------------

// applyParams sets the scene's settings (declared defaults, overridden by
// saved values) and the ones every scene takes: _theme, _tintA, _tintB, _charset.
function applyParams(values) {
  values = values || {};
  const out = {}, decl = (scene && scene.params) || {};
  for (const [k, d] of Object.entries(decl)) out[k] = d && "default" in d ? d.default : undefined;
  Object.assign(out, values);
  if (g) { g.params = out; g.paramsVersion++; }
  const t = values._theme;
  theme = t === "custom" ? [hex3(values._tintA || "#0a3d2a"), hex3(values._tintB || "#c8ffe0")] : THEMES[t] ? THEMES[t].map(hex3) : null;
  const before = CS;
  setCharset(values._charset);
  if (before !== CS) glyphs = new Uint16Array(0); // map every cell again
  for (const v of views) { v.dirty = true; v.stale = true; }
  return out;
}

// ---- views: one WebGL canvas each ------------------------------------------------------
// The wallpaper has one per monitor, so only real screen area is drawn and
// composited, and a monitor hidden behind a window costs nothing. The
// preview has one for the whole desk.

const VS = `#version 300 es
in vec2 aPx; in vec2 aMM; uniform vec2 uWin; out vec2 vMM;
void main(){ vMM = aMM; gl_Position = vec4(aPx.x / uWin.x * 2.0 - 1.0, 1.0 - aPx.y / uWin.y * 2.0, 0.0, 1.0); }`;
const FS = `#version 300 es
precision highp float; precision highp int; precision highp usampler2D;
in vec2 vMM; out vec4 o;
uniform vec2 uOrigin, uCell, uT; uniform mat2 uA; uniform ivec2 uGrid; uniform vec3 uBack, uTintA, uTintB; uniform float uDim; uniform int uTheme;
uniform usampler2D uGlyph; uniform sampler2D uFg, uBg, uAtlas;
uniform vec2 uAtlasSize, uSlot; uniform float uPad; uniform int uPerRow;
void main(){
  vec2 g = (uA * vMM + uT - uOrigin) / uCell; // desk mm -> the scene's canvas -> cells
  ivec2 c = ivec2(floor(g));
  if (c.x < 0 || c.y < 0 || c.x >= uGrid.x || c.y >= uGrid.y) { o = vec4(uBack * uDim, 1.0); return; }
  int s = int(texelFetch(uGlyph, c, 0).r);
  vec4 fg = texelFetch(uFg, c, 0), bg = texelFetch(uBg, c, 0);
  vec3 back = bg.a > 0.5 ? bg.bgr : uBack;
  vec2 inner = uSlot - 2.0 * uPad;
  vec2 base = vec2(float(s % uPerRow), float(s / uPerRow)) * uSlot + uPad;
  vec2 uv = (base + fract(g) * inner) / uAtlasSize;
  vec2 k = inner / uAtlasSize;
  float a = s == 0 ? 0.0 : textureGrad(uAtlas, uv, dFdx(g) * k, dFdy(g) * k).r;
  vec3 ink = fg.bgr;
  if (uTheme == 1) ink = mix(uTintA, uTintB, clamp(dot(ink, vec3(0.299, 0.587, 0.114)) * 1.3, 0.0, 1.0));
  o = vec4(mix(back, ink, a) * uDim, 1.0);
}`;

// makeView puts a canvas at x, y (window pixels), w x h, drawing meshes
// [{nx, ny, px, mm, dim}] whose px are relative to the canvas. i is the
// monitor it shows (-1 for the preview).
function makeView(x, y, w, h, meshes, i) {
  const dpr = devicePixelRatio || 1, cv = document.createElement("canvas");
  cv.width = Math.round(w); cv.height = Math.round(h);
  Object.assign(cv.style, { position: "absolute", left: x / dpr + "px", top: y / dpr + "px", width: w / dpr + "px", height: h / dpr + "px" });
  host.appendChild(cv);
  const gl = cv.getContext("webgl2", { antialias: false, alpha: false, depth: false, stencil: false, powerPreference: "low-power", preserveDrawingBuffer: SNAP });
  if (!gl) throw new Error("WebGL2 is not available");
  const sh = (type, src) => { const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s); if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s)); return s; };
  const p = gl.createProgram();
  gl.attachShader(p, sh(gl.VERTEX_SHADER, VS)); gl.attachShader(p, sh(gl.FRAGMENT_SHADER, FS)); gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p));
  const u = {}; for (const n of ["uWin", "uOrigin", "uCell", "uA", "uT", "uGrid", "uBack", "uTintA", "uTintB", "uTheme", "uDim", "uGlyph", "uFg", "uBg", "uAtlas", "uAtlasSize", "uSlot", "uPad", "uPerRow"]) u[n] = gl.getUniformLocation(p, n);
  const tex = (filter) => {
    const t = gl.createTexture(); gl.bindTexture(gl.TEXTURE_2D, t);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filter); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filter === gl.NEAREST ? gl.NEAREST : gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    return t;
  };
  const v = { cv, gl, p, u, w: cv.width, h: cv.height, i, tAtlas: tex(gl.LINEAR_MIPMAP_LINEAR), tGlyph: tex(gl.NEAREST), tFg: tex(gl.NEAREST), tBg: tex(gl.NEAREST),
    atlasVersion: -1, stale: true, dirty: true, meshes: [] };
  gl.bindTexture(gl.TEXTURE_2D, v.tAtlas); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, 2);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1); // grid rows are any width
  setMeshes(v, meshes);
  return v;
}

// setMeshes (re)loads a view's meshes [{nx, ny, px, mm, dim}].
function setMeshes(v, meshes) {
  const gl = v.gl, p = v.p;
  for (const m of v.meshes) { gl.deleteVertexArray(m.vao); gl.deleteBuffer(m.vb); gl.deleteBuffer(m.ib); }
  v.meshes = [];
  const aPx = gl.getAttribLocation(p, "aPx"), aMM = gl.getAttribLocation(p, "aMM");
  for (const m of meshes) {
    const n = m.nx * m.ny, data = new Float32Array(n * 4);
    for (let k = 0; k < n; k++) data.set([m.px[2 * k], m.px[2 * k + 1], m.mm[2 * k], m.mm[2 * k + 1]], 4 * k);
    const idx = [];
    for (let j = 0; j < m.ny - 1; j++) for (let q = 0; q < m.nx - 1; q++) { const a = j * m.nx + q; idx.push(a, a + 1, a + m.nx, a + 1, a + m.nx + 1, a + m.nx); }
    const vao = gl.createVertexArray(); gl.bindVertexArray(vao);
    const vb = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, vb); gl.bufferData(gl.ARRAY_BUFFER, data, gl.STATIC_DRAW);
    const ib = gl.createBuffer(); gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, ib); gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, new Uint16Array(idx), gl.STATIC_DRAW);
    gl.enableVertexAttribArray(aPx); gl.vertexAttribPointer(aPx, 2, gl.FLOAT, false, 16, 0);
    gl.enableVertexAttribArray(aMM); gl.vertexAttribPointer(aMM, 2, gl.FLOAT, false, 16, 8);
    gl.bindVertexArray(null);
    v.meshes.push({ vao, vb, ib, count: idx.length, dim: m.dim || 1 });
  }
  v.dirty = true;
}

// editorMeshes: the whole canvas dimmed, the monitors bright, placed by the
// editor's viewBox (desk mm) in this frame's pixels.
function editorMeshes(v) {
  if (!EV.vb) return [];
  const { x, y, w, h } = EV.vb, k = Math.min(v.w / w, v.h / h), ox = (v.w - w * k) / 2, oy = (v.h - h * k) / 2;
  const px = (mm) => mm.map((val, j) => (j % 2 ? oy + (val - y) * k : ox + (val - x) * k));
  const world = [[0, 0], [FR.W, 0], [0, FR.H], [FR.W, FR.H]].flatMap(([a, b]) => FR.toDesk(a, b));
  return [{ nx: 2, ny: 2, px: px(world), mm: world, dim: 0.4 }, ...st.desk.monitors.map((m) => ({ nx: m.nx, ny: m.ny, px: px(m.mm), mm: m.mm }))];
}

function dropViews() {
  for (const v of views) { const x = v.gl.getExtension("WEBGL_lose_context"); if (x) x.loseContext(); v.cv.remove(); }
  views = [];
}

function buildViews(desk) {
  dropViews();
  if (WORLD) { // the canvas, flat: corners mapped back to desk mm so the same shader draws it
    const w = 2048, h = Math.round((2048 * FR.H) / FR.W);
    const mm = [[0, 0], [FR.W, 0], [0, FR.H], [FR.W, FR.H]].flatMap(([x, y]) => FR.toDesk(x, y));
    views.push(makeView(0, 0, w, h, [{ nx: 2, ny: 2, px: [0, 0, w, 0, 0, h, w, h], mm }], -1));
    return;
  }
  if (EDITOR) {
    const v = makeView(0, 0, Math.round(innerWidth * devicePixelRatio), Math.round(innerHeight * devicePixelRatio), [], -1);
    views.push(v); setMeshes(v, editorMeshes(v));
    return;
  }
  if (PREVIEW) { // the whole desk, flat: dimmed behind, the monitors at full brightness
    const w = Math.round(innerWidth * devicePixelRatio), h = Math.round(innerHeight * devicePixelRatio);
    const [x, y, dw, dh] = desk.mm, k = Math.min(w / dw, h / dh), ox = (w - dw * k) / 2, oy = (h - dh * k) / 2;
    const flat = (mm) => mm.map((v, j) => (j % 2 ? oy + (v - y) * k : ox + (v - x) * k));
    const back = [x, y, x + dw, y, x, y + dh, x + dw, y + dh];
    views.push(makeView(0, 0, w, h, [{ nx: 2, ny: 2, px: flat(back), mm: back, dim: 0.28 }, ...desk.monitors.map((m) => ({ nx: m.nx, ny: m.ny, px: flat(m.mm), mm: m.mm }))], -1));
    return;
  }
  desk.monitors.forEach((m, i) => {
    const x = m.screen[0] - desk.window[0], y = m.screen[1] - desk.window[1];
    views.push(makeView(x, y, m.screen[2], m.screen[3], [{ nx: m.nx, ny: m.ny, px: m.px.map((v, j) => v - (j % 2 ? y : x)), mm: m.mm }], i));
  });
}

function sizeView(v, G) {
  const gl = v.gl;
  gl.bindTexture(gl.TEXTURE_2D, v.tGlyph); gl.texImage2D(gl.TEXTURE_2D, 0, gl.R16UI, G.cols, G.rows, 0, gl.RED_INTEGER, gl.UNSIGNED_SHORT, null);
  gl.bindTexture(gl.TEXTURE_2D, v.tFg); gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, G.cols, G.rows, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
  gl.bindTexture(gl.TEXTURE_2D, v.tBg); gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, G.cols, G.rows, 0, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.n * 4));
  v.stale = true; v.dirty = true;
}

function uploadView(v, G) {
  const gl = v.gl;
  gl.bindTexture(gl.TEXTURE_2D, v.tGlyph); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RED_INTEGER, gl.UNSIGNED_SHORT, glyphs);
  gl.bindTexture(gl.TEXTURE_2D, v.tFg); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.fg.buffer));
  if (G.bgUsed) { gl.bindTexture(gl.TEXTURE_2D, v.tBg); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.bg.buffer)); }
  if (v.atlasVersion !== A.version) {
    gl.bindTexture(gl.TEXTURE_2D, v.tAtlas); gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, gl.RED, gl.UNSIGNED_BYTE, A.canvas);
    gl.generateMipmap(gl.TEXTURE_2D); v.atlasVersion = A.version;
  }
  v.stale = false;
}

function parseColor(c, dflt) {
  if (typeof c === "number") return c;
  const m = /^#?([0-9a-f]{6})$/i.exec(c || "");
  return m ? parseInt(m[1], 16) : dflt;
}

function drawView(v, G) {
  const gl = v.gl, u = v.u, back = parseColor(scene && scene.background, 0x05070a);
  gl.viewport(0, 0, gl.drawingBufferWidth, gl.drawingBufferHeight);
  if (v.i < 0) { gl.clearColor(0, 0, 0, 1); gl.clear(gl.COLOR_BUFFER_BIT); } // a monitor's mesh covers its whole canvas
  gl.useProgram(v.p);
  gl.uniform2f(u.uWin, v.w, v.h); gl.uniform2f(u.uOrigin, G.x0, G.y0); gl.uniform2f(u.uCell, G.cw, G.ch);
  const A = FR.A; gl.uniformMatrix2fv(u.uA, false, [A[0], A[2], A[1], A[3]]); gl.uniform2f(u.uT, FR.T[0], FR.T[1]); // column-major
  gl.uniform2i(u.uGrid, G.cols, G.rows); gl.uniform3f(u.uBack, ((back >> 16) & 255) / 255, ((back >> 8) & 255) / 255, (back & 255) / 255);
  gl.uniform1i(u.uTheme, theme ? 1 : 0);
  if (theme) { gl.uniform3fv(u.uTintA, theme[0]); gl.uniform3fv(u.uTintB, theme[1]); }
  gl.uniform2f(u.uAtlasSize, AW, AH); gl.uniform2f(u.uSlot, SW, SH); gl.uniform1f(u.uPad, PAD); gl.uniform1i(u.uPerRow, PER_ROW);
  [v.tGlyph, v.tFg, v.tBg, v.tAtlas].forEach((t, k) => { gl.activeTexture(gl.TEXTURE0 + k); gl.bindTexture(gl.TEXTURE_2D, t); });
  gl.uniform1i(u.uGlyph, 0); gl.uniform1i(u.uFg, 1); gl.uniform1i(u.uBg, 2); gl.uniform1i(u.uAtlas, 3);
  for (const m of v.meshes) { gl.uniform1f(u.uDim, m.dim); gl.bindVertexArray(m.vao); gl.drawElements(gl.TRIANGLES, m.count, gl.UNSIGNED_SHORT, 0); }
  gl.bindVertexArray(null);
  v.dirty = false;
}

const seen = (v) => v.i < 0 || live[v.i]; // not covered by a window
const anySeen = () => views.some(seen);

// render pushes the grid to every view that can be seen. Views behind
// windows are skipped entirely (no upload, no draw, nothing to composite)
// and catch up when they show again.
function render(changed) {
  if (changed || glyphs.length !== g.n) { mapGlyphs(g); for (const v of views) v.stale = true; }
  for (const v of views) {
    if (!seen(v)) continue;
    if (v.stale) { uploadView(v, g); v.dirty = true; }
    if (v.dirty) drawView(v, g);
  }
}

// ---- loading and the frame loop ---------------------------------------------------

async function load() {
  const qs = Q.get("scene") ? "?scene=" + encodeURIComponent(Q.get("scene")) : "";
  st = await (await fetch("/api/live/runtime" + qs, { cache: "no-store" })).json();
  if (st.error) throw new Error(st.error);
  failed = null;
  try {
    const mod = await import(`/live/scenes/${encodeURIComponent(st.scene)}.js?v=${st.version}`);
    scene = mod.default || mod;
    if (typeof scene.draw !== "function") throw new Error("the scene has no draw(g, t, dt) function");
  } catch (e) { scene = { draw() { return false; } }; failed = e; failed.line = await syntaxLine(st.scene, st.version); }
  const font = st.font || scene.font || DEFAULT_FONT;
  try { await document.fonts.load(`48px ${font}`); } catch (e) { /* fall back */ }
  setFont(font);
  if (EDITOR && EV.frame) st.frame = EV.frame;
  if (EDITOR && EV.params) st.params = EV.params;
  FR = makeFrame(st.desk, st.frame);
  g = makeGrid(st.desk, scene, FR);
  applyParams(st.params);
  buildViews(st.desk);
  for (const v of views) sizeView(v, g);
  live = st.desk.monitors.map(() => true); applyCovered();
  step = 1000 / Math.max(1, Math.min(60, st.fps || scene.fps || 30));
  if (!failed && scene.setup) { try { await scene.setup(g); } catch (e) { failed = e; } }
  if (EDITOR) tell({ type: "scene", scene: st.scene, params: (scene && scene.params) || {}, values: st.params || {}, cols: g.cols, rows: g.rows });
  if (failed) showError(failed);
  else report({ loaded: true, monitors: live.length, visible: live.filter(Boolean).length, views: views.length });
  // Draw the first frame now: the window stays hidden (and gets no animation
  // frames) until the page says it is ready.
  if (raf) { cancelAnimationFrame(raf); raf = 0; }
  if (timer) { clearTimeout(timer); timer = 0; }
  t0 = performance.now(); next = t0;
  frame(t0, true);
  if (!readySent) { readySent = true; try { window.chrome.webview.postMessage("ready"); } catch (e) { /* preview */ } }
  if (SNAP) setTimeout(snapshot, 1500);
}

function snapshot() {
  const name = st.scene, ver = WORLD ? `${st.version}@${st.charMM || 0}` : st.version; // matches the server's worldVersion
  const done = (ok) => { stopped = true; try { parent.postMessage({ snapshot: name, world: WORLD, ok }, location.origin); } catch (e) { /* not in a frame */ } };
  views[0].cv.toBlob((b) => {
    if (!b) return done(false);
    fetch(`/api/live/thumb?name=${encodeURIComponent(name)}&ver=${encodeURIComponent(ver)}${WORLD ? "&kind=world" : ""}`, { method: "POST", body: b })
      .then((r) => done(r.ok)).catch(() => done(false));
  }, "image/png");
}

// errorLine finds the scene's line in an error's stack.
function errorLine(e) {
  if (e && e.line) return e.line;
  const m = new RegExp(`/live/scenes/${st.scene}\\.js[^:\\s]*:(\\d+):(\\d+)`).exec((e && e.stack) || "");
  return m ? +m[1] : 0;
}

// syntaxLine finds where a scene that doesn't even load breaks: a module
// script with a syntax error reports its line to window.onerror.
function syntaxLine(name, ver) {
  return new Promise((resolve) => {
    const el = document.createElement("script"), done = (n) => { removeEventListener("error", onErr); el.remove(); resolve(n); };
    const onErr = (ev) => { if (ev.filename && ev.filename.includes(`/live/scenes/${name}.js`)) { ev.preventDefault(); done(ev.lineno || 0); } };
    addEventListener("error", onErr);
    el.type = "module"; el.src = `/live/scenes/${encodeURIComponent(name)}.js?v=${ver}&check=${Date.now()}`;
    el.onload = () => done(0);
    document.head.appendChild(el);
    setTimeout(() => done(0), 3000);
  });
}

function tell(msg) { try { if (parent !== window) parent.postMessage(msg, location.origin); } catch (e) { /* no editor */ } }

function showError(e) {
  const msg = String((e && e.message) || e).slice(0, 300), line = errorLine(e);
  g.clear(32, 0xffffff);
  const m = g.main;
  g.text(m.c0 + 2, m.r0 + 2, `Scene "${st.scene}" failed${line ? " at line " + line : ""}:`, 0xff6b6b);
  msg.split("\n").forEach((l, k) => g.text(m.c0 + 2, m.r0 + 4 + k, l, 0xffd0d0));
  render(true);
  report({ error: msg, line });
  tell({ type: "error", scene: st.scene, message: msg, line });
}

// Frames are paced by a timer, then drawn in the next animation frame, so a
// 240 Hz monitor doesn't wake the page 240 times a second.
function schedule() {
  if (stopped || timer || raf || !anySeen()) return;
  if (SNAP) { timer = setTimeout(() => { timer = 0; frame(performance.now()); }, Math.max(0, next - performance.now())); return; } // hidden frames get no rAF
  timer = setTimeout(() => { timer = 0; raf = requestAnimationFrame(frame); }, Math.max(0, next - performance.now() - 3));
}

function frame(now, first) {
  raf = 0;
  if (!first && !anySeen()) return; // every monitor covered: stop until one shows again
  if (!first && now < next - 1.5) { schedule(); return; }
  next = Math.max(next + step, now + step * 0.5);
  const w0 = performance.now();
  let changed = false;
  if (!failed) {
    g.dt = step / 1000; g.frameNo++;
    try { changed = scene.draw(g, (now - t0) / 1000, step / 1000) !== false; }
    catch (e) { failed = e; showError(e); }
  }
  render(changed);
  stats.frames++; stats.ms += performance.now() - w0;
  schedule();
}

function report(extra) {
  if (PREVIEW) return;
  const body = Object.assign({ scene: st && st.scene, cols: g && g.cols, rows: g && g.rows, glyphs: A.next - 1 }, extra);
  fetch("/api/live/report", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).catch(() => {});
}
setInterval(() => {
  if (!st || (!stats.frames && !failed)) return;
  report({ fps: +(stats.frames / 10).toFixed(1), ms: stats.frames ? +(stats.ms / stats.frames).toFixed(2) : 0 });
  stats = { frames: 0, ms: 0 };
}, 10000);

function applyCovered() {
  if (!st) return;
  live = st.desk.monitors.map((m) => !coveredRects.some((c) => Math.abs(c[0] - m.screen[0]) < 4 && Math.abs(c[1] - m.screen[1]) < 4 && Math.abs(c[2] - m.screen[2]) < 8));
  for (const v of views) v.dirty = true;
  if (g) render(false);
  schedule();
}

// called by Spanwall's window
window.spanwall = {
  reload() { if (!loading) loading = load().catch((e) => report({ error: String(e) })).finally(() => (loading = null)); },
  covered(rects) { coveredRects = rects || []; applyCovered(); },
  // framing changed (dragged in the editor, or saved): no reload, the scene keeps running
  setFrame(m) {
    if (!st || !g || (m.scene && m.scene !== st.scene)) return;
    st.frame = m.frame; FR = makeFrame(st.desk, m.frame); placeMonitors(g, st.desk, FR);
    if (EDITOR) for (const v of views) setMeshes(v, editorMeshes(v));
    for (const v of views) v.dirty = true;
    render(false); schedule();
  },
  setParams(m) {
    if (!st || !g || (m.scene && m.scene !== st.scene)) return;
    st.params = m.params; applyParams(m.params);
    if (!failed && scene.paramsChanged) { try { scene.paramsChanged(g); } catch (e) { failed = e; showError(e); } }
    render(true); schedule();
  },
};
// the layout editor drives the editor mode: its view, the framing being dragged, settings
addEventListener("message", (e) => {
  if (!EDITOR || e.origin !== location.origin || !e.data) return;
  const d = e.data;
  if (d.type === "view") {
    EV.vb = d.vb;
    if (d.frame) { EV.frame = d.frame; if (st) window.spanwall.setFrame({ frame: d.frame }); }
    for (const v of views) setMeshes(v, editorMeshes(v));
    if (g) { render(false); schedule(); }
  } else if (d.type === "params") {
    EV.params = d.params;
    window.spanwall.setParams({ params: d.params });
  }
});
document.addEventListener("visibilitychange", () => { for (const v of views) v.dirty = true; schedule(); });
if (PREVIEW) addEventListener("resize", () => window.spanwall.reload());
if (EDITOR) tell({ type: "hello" });
window.spanwall.reload();

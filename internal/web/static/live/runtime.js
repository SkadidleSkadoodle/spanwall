// Spanwall live runtime. A scene fills a character grid laid out in desk mm;
// this page draws it on the GPU through each monitor's measured shape, so
// characters line up across bezels. One page covers every monitor.
//   /live/            the wallpaper (inside Spanwall's window behind the icons)
//   /live/?preview=1  the whole desk, flat, for the editor
//   &scene=name       preview another scene than the active one

const Q = new URLSearchParams(location.search);
const PREVIEW = Q.has("preview");
const canvas = document.getElementById("c");
const gl = canvas.getContext("webgl2", { antialias: false, alpha: false, depth: false, stencil: false, powerPreference: "low-power" });
if (!gl) fetch("/api/live/report", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ error: "WebGL2 is not available" }) });

const SW = 40, SH = 72, PAD = 4, AW = 2048, AH = 2048; // atlas: slot size, padding, size
const PER_ROW = Math.floor(AW / SW), SLOTS = PER_ROW * Math.floor(AH / SH);
const MAX_CELLS = 400000;
const DEFAULT_FONT = "'Cascadia Mono', Consolas, 'Courier New', monospace";

let st = null, scene = null, g = null, R = null, raf = 0, failed = null;
let coveredRects = [], live = [], needDraw = true, t0 = performance.now(), last = 0, step = 1000 / 30;
let stats = { frames: 0, ms: 0 }, readySent = false, loading = null;

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

function makeGrid(desk, def) {
  let ch = st.charMM || (def.cell && def.cell.h) || 4.2;
  const aspect = (def.cell && def.cell.aspect) || 0.55;
  const [x0, y0, w, h] = desk.mm;
  while (Math.ceil(w / (ch * aspect)) * Math.ceil(h / ch) > MAX_CELLS) ch *= 1.1;
  const cw = ch * aspect, cols = Math.ceil(w / cw), rows = Math.ceil(h / ch), n = cols * rows;
  const G = {
    cols, rows, n, cw, ch, x0, y0, width: w, height: h,
    chars: new Uint32Array(n).fill(32), fg: new Uint32Array(n).fill(0xffffff), bg: new Uint32Array(n), mask: new Uint8Array(n),
    bgUsed: false, monitors: [], main: null, preview: PREVIEW,
    idx: (c, r) => r * cols + c,
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
  // which cells are on a monitor, and each monitor's cell box
  for (const m of desk.monitors) {
    const poly = outline(m);
    const [bx, by, bw, bh] = m.box;
    const c0 = Math.max(0, Math.floor((bx - x0) / cw)), r0 = Math.max(0, Math.floor((by - y0) / ch));
    const c1 = Math.min(cols - 1, Math.floor((bx + bw - x0) / cw)), r1 = Math.min(rows - 1, Math.floor((by + bh - y0) / ch));
    for (let r = r0; r <= r1; r++) for (let c = c0; c <= c1; c++) {
      if (inside(poly, x0 + (c + 0.5) * cw, y0 + (r + 0.5) * ch)) G.mask[r * cols + c] = 1;
    }
    G.monitors.push({ id: m.id, name: m.name, primary: !!m.primary, x: bx, y: by, w: bw, h: bh, c0, r0, c1, r1, cc: ((c0 + c1) / 2) | 0, cr: ((r0 + r1) / 2) | 0 });
  }
  G.main = G.monitors.find((m) => m.primary) || G.monitors.reduce((a, b) => (!a || b.w * b.h > a.w * a.h ? b : a), null) || { c0: 0, r0: 0, c1: cols - 1, r1: rows - 1, cc: cols >> 1, cr: rows >> 1 };
  return G;
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

// ---- renderer ------------------------------------------------------------------

const VS = `#version 300 es
in vec2 aPx; in vec2 aMM; uniform vec2 uWin; out vec2 vMM;
void main(){ vMM = aMM; gl_Position = vec4(aPx.x / uWin.x * 2.0 - 1.0, 1.0 - aPx.y / uWin.y * 2.0, 0.0, 1.0); }`;
const FS = `#version 300 es
precision highp float; precision highp int; precision highp usampler2D;
in vec2 vMM; out vec4 o;
uniform vec2 uOrigin, uCell; uniform ivec2 uGrid; uniform vec3 uBack; uniform float uDim;
uniform usampler2D uGlyph; uniform sampler2D uFg, uBg, uAtlas;
uniform vec2 uAtlasSize, uSlot; uniform float uPad; uniform int uPerRow;
void main(){
  vec2 g = (vMM - uOrigin) / uCell;
  ivec2 c = ivec2(floor(g));
  if (c.x < 0 || c.y < 0 || c.x >= uGrid.x || c.y >= uGrid.y) { o = vec4(uBack * uDim, 1.0); return; }
  int s = int(texelFetch(uGlyph, c, 0).r);
  vec4 fg = texelFetch(uFg, c, 0), bg = texelFetch(uBg, c, 0);
  vec3 back = bg.a > 0.5 ? bg.bgr : uBack;
  vec2 inner = uSlot - 2.0 * uPad;
  vec2 base = vec2(float(s % uPerRow), float(s / uPerRow)) * uSlot + uPad;
  vec2 f = fract(g);
  vec2 uv = (base + f * inner) / uAtlasSize;
  vec2 k = inner / uAtlasSize;
  float a = s == 0 ? 0.0 : textureGrad(uAtlas, uv, dFdx(g) * k, dFdy(g) * k).r;
  o = vec4(mix(back, fg.bgr, a) * uDim, 1.0);
}`;

function compile(type, src) {
  const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s);
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s));
  return s;
}

function tex(filter) {
  const t = gl.createTexture(); gl.bindTexture(gl.TEXTURE_2D, t);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filter); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filter === gl.NEAREST ? gl.NEAREST : gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
  return t;
}

function makeRenderer() {
  const p = gl.createProgram();
  gl.attachShader(p, compile(gl.VERTEX_SHADER, VS)); gl.attachShader(p, compile(gl.FRAGMENT_SHADER, FS)); gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p));
  const u = {}; for (const n of ["uWin", "uOrigin", "uCell", "uGrid", "uBack", "uDim", "uGlyph", "uFg", "uBg", "uAtlas", "uAtlasSize", "uSlot", "uPad", "uPerRow"]) u[n] = gl.getUniformLocation(p, n);
  const atlas = document.createElement("canvas"); atlas.width = AW; atlas.height = AH;
  const ac = atlas.getContext("2d", { alpha: false }); ac.fillStyle = "#000"; ac.fillRect(0, 0, AW, AH);
  const r = { p, u, atlas, ac, tAtlas: tex(gl.LINEAR_MIPMAP_LINEAR), tGlyph: tex(gl.NEAREST), tFg: tex(gl.NEAREST), tBg: tex(gl.NEAREST),
    ascii: new Int16Array(128).fill(-1), map: new Map(), next: 1, atlasDirty: true, glyph: null, meshes: [], font: "" };
  gl.bindTexture(gl.TEXTURE_2D, r.tAtlas); gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, 2);
  gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1); // grid rows are any width
  return r;
}

function setFont(r, font) {
  if (r.font === font) return;
  r.font = font; r.ascii.fill(-1); r.map.clear(); r.next = 1;
  r.ac.fillStyle = "#000"; r.ac.fillRect(0, 0, AW, AH); r.atlasDirty = true;
}

function addGlyph(r, code) {
  if (r.next >= SLOTS) return 0;
  const s = r.next++, x = (s % PER_ROW) * SW, y = Math.floor(s / PER_ROW) * SH, ac = r.ac;
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
    ac.font = `${Math.round(ih * 0.8)}px ${r.font}`;
    const chr = String.fromCodePoint(code), mw = ac.measureText(chr).width;
    if (mw > iw) { ac.translate(x + SW / 2, 0); ac.scale(iw / mw, 1); ac.fillText(chr, 0, y + PAD + ih * 0.54); }
    else ac.fillText(chr, x + SW / 2, y + PAD + ih * 0.54);
  }
  ac.restore();
  if (code < 128) r.ascii[code] = s; else r.map.set(code, s);
  r.atlasDirty = true;
  return s;
}

function sizeTextures(r, G) {
  r.glyph = new Uint16Array(G.n);
  gl.bindTexture(gl.TEXTURE_2D, r.tGlyph); gl.texImage2D(gl.TEXTURE_2D, 0, gl.R16UI, G.cols, G.rows, 0, gl.RED_INTEGER, gl.UNSIGNED_SHORT, r.glyph);
  gl.bindTexture(gl.TEXTURE_2D, r.tFg); gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, G.cols, G.rows, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
  gl.bindTexture(gl.TEXTURE_2D, r.tBg); gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, G.cols, G.rows, 0, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.n * 4));
}

function upload(r, G) {
  const ch = G.chars, out = r.glyph, ascii = r.ascii;
  let lastC = -1, lastS = 0;
  for (let i = 0; i < G.n; i++) {
    const c = ch[i];
    if (c !== lastC) {
      lastC = c;
      if (c === 32 || c === 0) lastS = 0;
      else if (c < 128) { lastS = ascii[c]; if (lastS < 0) lastS = addGlyph(r, c); }
      else { lastS = r.map.get(c); if (lastS === undefined) lastS = addGlyph(r, c); }
    }
    out[i] = lastS;
  }
  gl.bindTexture(gl.TEXTURE_2D, r.tGlyph); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RED_INTEGER, gl.UNSIGNED_SHORT, out);
  gl.bindTexture(gl.TEXTURE_2D, r.tFg); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.fg.buffer));
  if (G.bgUsed) { gl.bindTexture(gl.TEXTURE_2D, r.tBg); gl.texSubImage2D(gl.TEXTURE_2D, 0, 0, 0, G.cols, G.rows, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(G.bg.buffer)); }
  if (r.atlasDirty) {
    gl.bindTexture(gl.TEXTURE_2D, r.tAtlas); gl.texImage2D(gl.TEXTURE_2D, 0, gl.R8, gl.RED, gl.UNSIGNED_BYTE, r.atlas);
    gl.generateMipmap(gl.TEXTURE_2D); r.atlasDirty = false;
  }
}

// meshes: triangles from each monitor's grid, in window pixels (wallpaper) or
// a flat fit of the desk (preview)
function buildMeshes(r, desk) {
  for (const m of r.meshes) { gl.deleteVertexArray(m.vao); gl.deleteBuffer(m.vb); gl.deleteBuffer(m.ib); }
  r.meshes = [];
  const toPx = PREVIEW ? previewMap(desk) : null;
  const add = (nx, ny, px, mm, dim, i) => {
    const v = new Float32Array(nx * ny * 4);
    for (let k = 0; k < nx * ny; k++) {
      const [x, y] = toPx ? toPx(mm[2 * k], mm[2 * k + 1]) : [px[2 * k], px[2 * k + 1]];
      v.set([x, y, mm[2 * k], mm[2 * k + 1]], 4 * k);
    }
    const idx = [];
    for (let j = 0; j < ny - 1; j++) for (let q = 0; q < nx - 1; q++) { const a = j * nx + q; idx.push(a, a + 1, a + nx, a + 1, a + nx + 1, a + nx); }
    const vao = gl.createVertexArray(); gl.bindVertexArray(vao);
    const vb = gl.createBuffer(); gl.bindBuffer(gl.ARRAY_BUFFER, vb); gl.bufferData(gl.ARRAY_BUFFER, v, gl.STATIC_DRAW);
    const ib = gl.createBuffer(); gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, ib); gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, new Uint16Array(idx), gl.STATIC_DRAW);
    const aPx = gl.getAttribLocation(r.p, "aPx"), aMM = gl.getAttribLocation(r.p, "aMM");
    gl.enableVertexAttribArray(aPx); gl.vertexAttribPointer(aPx, 2, gl.FLOAT, false, 16, 0);
    gl.enableVertexAttribArray(aMM); gl.vertexAttribPointer(aMM, 2, gl.FLOAT, false, 16, 8);
    gl.bindVertexArray(null);
    r.meshes.push({ vao, vb, ib, count: idx.length, dim, i });
  };
  if (PREVIEW) { // the whole desk, dimmed, then the monitors at full brightness
    const [x, y, w, h] = desk.mm;
    add(2, 2, null, [x, y, x + w, y, x, y + h, x + w, y + h], 0.28, -1);
  }
  desk.monitors.forEach((m, i) => add(m.nx, m.ny, m.px, m.mm, 1, i));
}

function previewMap(desk) {
  const [x, y, w, h] = desk.mm, cw = canvas.width, chh = canvas.height;
  const k = Math.min(cw / w, chh / h), ox = (cw - w * k) / 2, oy = (chh - h * k) / 2;
  return (mx, my) => [ox + (mx - x) * k, oy + (my - y) * k];
}

function parseColor(c, dflt) {
  if (typeof c === "number") return c;
  const m = /^#?([0-9a-f]{6})$/i.exec(c || "");
  return m ? parseInt(m[1], 16) : dflt;
}

function draw(r, G) {
  const W = PREVIEW ? canvas.width : st.desk.window[2], H = PREVIEW ? canvas.height : st.desk.window[3];
  gl.viewport(0, 0, gl.drawingBufferWidth, gl.drawingBufferHeight);
  gl.clearColor(0, 0, 0, 1); gl.clear(gl.COLOR_BUFFER_BIT);
  gl.useProgram(r.p);
  const u = r.u, back = parseColor(scene && scene.background, 0x05070a);
  gl.uniform2f(u.uWin, W, H); gl.uniform2f(u.uOrigin, G.x0, G.y0); gl.uniform2f(u.uCell, G.cw, G.ch);
  gl.uniform2i(u.uGrid, G.cols, G.rows); gl.uniform3f(u.uBack, ((back >> 16) & 255) / 255, ((back >> 8) & 255) / 255, (back & 255) / 255);
  gl.uniform2f(u.uAtlasSize, AW, AH); gl.uniform2f(u.uSlot, SW, SH); gl.uniform1f(u.uPad, PAD); gl.uniform1i(u.uPerRow, PER_ROW);
  [r.tGlyph, r.tFg, r.tBg, r.tAtlas].forEach((t, k) => { gl.activeTexture(gl.TEXTURE0 + k); gl.bindTexture(gl.TEXTURE_2D, t); });
  gl.uniform1i(u.uGlyph, 0); gl.uniform1i(u.uFg, 1); gl.uniform1i(u.uBg, 2); gl.uniform1i(u.uAtlas, 3);
  for (const m of r.meshes) {
    if (m.i >= 0 && !live[m.i]) continue; // covered by a window: nobody sees it
    gl.uniform1f(u.uDim, m.dim); gl.bindVertexArray(m.vao); gl.drawElements(gl.TRIANGLES, m.count, gl.UNSIGNED_SHORT, 0);
  }
  gl.bindVertexArray(null);
}

// ---- loading and the frame loop ---------------------------------------------------

async function load() {
  const qs = Q.get("scene") ? "?scene=" + encodeURIComponent(Q.get("scene")) : "";
  st = await (await fetch("/api/live/runtime" + qs, { cache: "no-store" })).json();
  if (st.error) throw new Error(st.error);
  if (PREVIEW) { canvas.width = Math.round(innerWidth * devicePixelRatio); canvas.height = Math.round(innerHeight * devicePixelRatio); }
  else { canvas.width = st.desk.window[2]; canvas.height = st.desk.window[3]; }
  failed = null;
  try {
    const mod = await import(`/live/scenes/${encodeURIComponent(st.scene)}.js?v=${st.version}`);
    scene = mod.default || mod;
    if (typeof scene.draw !== "function") throw new Error("the scene has no draw(g, t, dt) function");
  } catch (e) { scene = { draw() { return false; } }; failed = e; }
  const font = st.font || scene.font || DEFAULT_FONT;
  try { await document.fonts.load(`48px ${font}`); } catch (e) { /* fall back */ }
  if (!R) R = makeRenderer();
  setFont(R, font);
  g = makeGrid(st.desk, scene);
  sizeTextures(R, g);
  buildMeshes(R, st.desk);
  live = st.desk.monitors.map(() => true); applyCovered();
  step = 1000 / Math.max(1, Math.min(60, st.fps || scene.fps || 30));
  t0 = performance.now(); last = 0; needDraw = true;
  if (!failed && scene.setup) { try { await scene.setup(g); } catch (e) { failed = e; } }
  if (failed) showError(failed);
  else report({ loaded: true, monitors: live.length, visible: live.filter(Boolean).length, webgl: !!gl });
  schedule();
}

function showError(e) {
  const msg = String((e && e.message) || e).slice(0, 300);
  g.clear(32, 0xffffff);
  const m = g.main;
  g.text(m.c0 + 2, m.r0 + 2, `Scene "${st.scene}" failed:`, 0xff6b6b);
  msg.split("\n").forEach((line, k) => g.text(m.c0 + 2, m.r0 + 4 + k, line, 0xffd0d0));
  upload(R, g); needDraw = true;
  report({ error: msg });
}

// Frames are paced by a timer, then drawn in the next animation frame, so a
// 240 Hz monitor doesn't wake the page 240 times a second.
let timer = 0;
function schedule() {
  if (timer || raf || !live.some(Boolean)) return;
  timer = setTimeout(() => { timer = 0; raf = requestAnimationFrame(frame); }, Math.max(0, last + step - performance.now() - 4));
}

function frame(now) {
  raf = 0; last = now;
  if (!live.some(Boolean)) return; // every monitor covered: stop until one shows again
  const w0 = performance.now();
  let changed = false;
  if (!failed) {
    try { changed = scene.draw(g, (now - t0) / 1000, step / 1000) !== false; }
    catch (e) { failed = e; showError(e); }
  }
  if (changed) upload(R, g);
  if (changed || needDraw) { draw(R, g); needDraw = false; }
  if (!readySent) { readySent = true; try { window.chrome.webview.postMessage("ready"); } catch (e) { /* preview */ } }
  stats.frames++; stats.ms += performance.now() - w0;
  schedule();
}

function report(extra) {
  if (PREVIEW) return;
  const body = Object.assign({ scene: st && st.scene, cols: g && g.cols, rows: g && g.rows, glyphs: R ? R.next - 1 : 0,
    buffer: [gl.drawingBufferWidth, gl.drawingBufferHeight] }, extra);
  fetch("/api/live/report", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).catch(() => {});
}
setInterval(() => {
  if (!st || !stats.frames && !failed) return;
  report({ fps: Math.round(stats.frames / 10), ms: stats.frames ? +(stats.ms / stats.frames).toFixed(2) : 0 });
  stats = { frames: 0, ms: 0 };
}, 10000);

function applyCovered() {
  if (!st) return;
  live = st.desk.monitors.map((m) => !coveredRects.some((c) => Math.abs(c[0] - m.screen[0]) < 4 && Math.abs(c[1] - m.screen[1]) < 4 && Math.abs(c[2] - m.screen[2]) < 8));
  needDraw = true;
  schedule();
}

// called by Spanwall's window
window.spanwall = {
  reload() { if (!loading) loading = load().catch((e) => report({ error: String(e) })).finally(() => (loading = null)); },
  covered(rects) { coveredRects = rects || []; applyCovered(); },
};
document.addEventListener("visibilitychange", () => { needDraw = true; schedule(); });
if (PREVIEW) addEventListener("resize", () => window.spanwall.reload());
window.spanwall.reload();

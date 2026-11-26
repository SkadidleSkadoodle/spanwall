"use strict";
// Spanwall UI. Plain JS, no build step. Talks to the background app's API.
// Layout units are mm on the desk: x grows right, y grows down, and each
// position is the top-left corner of a monitor's visible area.

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const NS = "http://www.w3.org/2000/svg";

const S = {
  status: null,
  displays: [],          // from /api/displays
  view: "",              // layout shown in the editor
  saved: {},             // id -> {xMM, yMM} as stored
  cur: {},               // id -> {xMM, yMM} being edited
  sel: null,
  img: null,             // wallpaper drawn in the editor (path); null = last applied
  calibrating: false,
  dragging: null,
  guides: [],
  vb: null,              // current viewBox {x, y, w, h}
  frame: null,           // framing being edited (the active view's), see act()
  frames: {},            // key -> {img, ids, w, h, zoom, dx, dy, flipX, flipY}; key = image, or image|group IDs
  framesSaved: {},       // key -> {zoom, dx, dy, flipX, flipY} as stored
  framesLoading: {},
  activeKey: "",
  moveImg: false,
  imgDrag: null,
  imgResize: null,
  lib: [],
  folders: [],           // [{name, count, builtin}]
  libTab: "all",         // all, fav, ban, or a folder name
};

// ---- API ------------------------------------------------------------------------
async function api(path, body) {
  const opt = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const r = await fetch(path, opt);
  const t = await r.text();
  let j = null;
  try { j = t ? JSON.parse(t) : null; } catch { /* not json */ }
  if (!r.ok) throw new Error((j && j.error) || t || r.statusText);
  return j;
}

let toastTimer;
function toast(msg, err = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "toast" + (err ? " err" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), err ? 7000 : 3000);
}
const fail = (e) => toast(e.message || String(e), true);

// ---- loading --------------------------------------------------------------------
async function loadStatus() {
  S.status = await api("/api/status");
  const st = S.status;
  $("#status").innerHTML = `<b>${st.monitors}</b> monitors · layout <b>${esc(st.activeLayout)}</b> · desktops <b>${esc(st.desktopMode)}</b>` +
    (st.overlay ? ` · <b style="color:var(--accent2)">${{ calibrate: "calibrating", identify: "identifying", photo: "dot pattern up" }[st.overlay] || st.overlay}</b>` : "");
  $("#btnDots").classList.toggle("on", st.overlay === "photo");
  $("#btnIdentify").classList.toggle("on", st.overlay === "identify");
  $("#btnIdentify").textContent = st.overlay === "identify" ? "Hide numbers" : "Identify";
  $("#btnDots").textContent = st.overlay === "photo" ? "Hide dot pattern" : "Show dot pattern";
  S.calibrating = st.overlay === "calibrate";
  $("#btnCalibrate").classList.toggle("on", S.calibrating);
  $("#about").textContent = `Spanwall ${st.version} · pid ${st.pid} · up ${st.uptime} · settings in ${st.configDir}`;
  $("#chkAuto").checked = st.autoSwitch;
  if (document.activeElement !== $("#inpDir")) $("#inpDir").value = st.wallpaperDir;
  showSlideshow();
}

async function loadLayout(keepEdits = false) {
  S.displays = await api("/api/displays");
  if (!S.view) S.view = S.status.activeLayout;
  const lay = await api("/api/layout?name=" + encodeURIComponent(S.view));
  const sel = $("#layoutSelect");
  const names = [...new Set([...lay.saved, S.view])];
  sel.innerHTML = names.map((n) => `<option ${n === S.view ? "selected" : ""}>${esc(n)}</option>`).join("") + liveOption();
  const saved = {};
  let right = 0;
  for (const m of lay.monitors) if (m.placed) right = Math.max(right, m.xMM + m.widthMM);
  for (const m of lay.monitors) {
    if (m.placed) saved[m.id] = { xMM: m.xMM, yMM: m.yMM, scale: m.scale || 1, quad: m.quad || null, mesh: m.mesh || null, meshNX: m.meshNX, meshNY: m.meshNY, qb: m.quad ? { xMM: m.xMM, yMM: m.yMM, scale: m.scale || 1 } : null };
    else { saved[m.id] = { xMM: right + 40, yMM: 0, scale: 1, unplaced: true }; right += m.widthMM + 40; }
  }
  S.saved = saved;
  if (!keepEdits || !Object.keys(S.cur).length || !isDirty()) S.cur = clone(saved);
  if (!S.displays.find((d) => d.id === S.sel)) S.sel = null;
  fit();
  render();
}

// ---- geometry helpers -----------------------------------------------------------
const clone = (o) => JSON.parse(JSON.stringify(o));
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const r1 = (v) => Math.round(v * 10) / 10;
const disp = (id) => S.displays.find((d) => d.id === id);
const sc = (id) => (S.cur[id] && S.cur[id].scale) || 1;
const W = (d) => d.widthMM * sc(d.id);   // apparent size (photo calibration may scale it)
const Hh = (d) => d.heightMM * sc(d.id);

function isDirty() {
  if (frameDirty()) return true;
  if (!Object.keys(S.cur).length) return false;
  for (const d of S.displays) {
    const a = S.cur[d.id], b = S.saved[d.id];
    if (!a || !b || b.unplaced || !!a.resetShape !== !!b.resetShape || Math.abs(a.xMM - b.xMM) > 0.05 || Math.abs(a.yMM - b.yMM) > 0.05 || Math.abs((a.scale || 1) - (b.scale || 1)) > 1e-4) return true;
  }
  return false;
}

// The editable part of a frame, and whether two of them differ.
const frameVals = (f) => ({ zoom: f.zoom, dx: f.dx, dy: f.dy, flipX: !!f.flipX, flipY: !!f.flipY, rotate: f.rotate || 0 });
const frameDiff = (f, g) => Math.abs(f.zoom - g.zoom) > 1e-4 || Math.abs(f.dx - g.dx) > 1e-4 || Math.abs(f.dy - g.dy) > 1e-4 || !!f.flipX !== !!g.flipX || !!f.flipY !== !!g.flipY ||
  Math.abs((f.rotate || 0) - (g.rotate || 0)) > 1e-3;

function frameDirty() {
  return Object.keys(S.frames).some((k) => S.framesSaved[k] && frameDiff(S.frames[k], S.framesSaved[k]));
}

// What the editor shows behind the monitors: one view for a spanned image,
// or one per span group when the last wallpaper was applied as groups.
function views() {
  if (LV.on) return liveViews();
  const img = $("#chkWallpaper").checked && wallpaperPath();
  if (!img) return [];
  const la = S.status && S.status.lastApplied;
  if (!S.img && la && la.mode === "groups" && S.status.groups && la.images && la.images.length) {
    return S.status.groups.map((g, gi) => {
      const ids = g.map((n) => S.displays[n - 1]).filter(Boolean).map((d) => d.id);
      const im = la.images[Math.min(gi, la.images.length - 1)];
      return { key: im + "|" + ids.join(","), img: im, ids, b: bboxOf(ids) };
    });
  }
  return [{ key: img, img, ids: null, b: bbox() }];
}

// The view being moved (the last one clicked), with S.frame set to its frame.
function act() {
  const vs = views();
  const v = vs.find((x) => x.key === S.activeKey) || vs[0];
  S.activeKey = v ? v.key : "";
  S.frame = v ? S.frames[v.key] || null : null;
  return v;
}
const activeB = () => { const v = act(); return v ? v.b : bbox(); };

async function loadFrame(v) {
  if (v.live) return liveLoadFrame(v);
  if (S.framesLoading[v.key]) return;
  S.framesLoading[v.key] = true;
  try {
    const q = "image=" + encodeURIComponent(v.img) + (v.ids ? "&group=" + encodeURIComponent(v.ids.join(",")) : "");
    const r = await api("/api/framing?" + q);
    S.frames[v.key] = { img: v.img, ids: v.ids, w: r.width, h: r.height, zoom: r.frame.zoom || 1, dx: r.frame.dx || 0, dy: r.frame.dy || 0, flipX: !!r.frame.flipX, flipY: !!r.frame.flipY, rotate: r.frame.rotate || 0 };
    S.framesSaved[v.key] = frameVals(S.frames[v.key]);
  } catch (e) { /* drawn with the default fill */ }
  render();
}

// turnPt turns (x, y) clockwise by deg degrees about (cx, cy).
function turnPt(x, y, cx, cy, deg) {
  if (!deg) return [x, y];
  const a = deg * Math.PI / 180, c = Math.cos(a), s = Math.sin(a), dx = x - cx, dy = y - cy;
  return [cx + c * dx - s * dy, cy + s * dx + c * dy];
}
const normDeg = (d) => { d = ((d % 360) + 360) % 360; return d > 180 ? d - 360 : d; };
function setRotate(deg) {
  act();
  if (!S.frame) return;
  S.frame.rotate = Math.round(normDeg(deg) * 10) / 10;
  render();
}

// Where an image sits (mm), exactly as the renderer places it: cover the
// bounding box b, then zoom and move.
function imgRect(b, f = S.frame) {
  const m = Math.max(b.w / f.w, b.h / f.h) * f.zoom;
  const w = f.w * m, h = f.h * m;
  return { x: b.x0 + b.w / 2 - w / 2 + f.dx * w, y: b.y0 + b.h / 2 - h / 2 + f.dy * h, w, h, base: m / f.zoom };
}

function bboxOf(ids) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const d of S.displays) {
    const p = S.cur[d.id];
    if (!p || (ids && !ids.includes(d.id))) continue;
    x0 = Math.min(x0, p.xMM); y0 = Math.min(y0, p.yMM);
    x1 = Math.max(x1, p.xMM + W(d)); y1 = Math.max(y1, p.yMM + Hh(d));
  }
  return { x0, y0, x1, y1, w: x1 - x0, h: y1 - y0 };
}

function bbox() { return bboxOf(null); }

function fit() {
  let b = bbox();
  if (!isFinite(b.w)) return;
  if (LV.on) b = liveFitBox(b); // show the scene's whole canvas
  const wrap = $("#canvasWrap");
  const aspect = wrap.clientWidth > 0 && wrap.clientHeight > 0 ? wrap.clientWidth / wrap.clientHeight : 1.6;
  const m = Math.max(b.w, b.h) * 0.12 + 40;
  let w = b.w + 2 * m, h = b.h + 2 * m;
  if (w / h < aspect) w = h * aspect; else h = w / aspect;
  S.vb = { x: b.x0 + b.w / 2 - w / 2, y: b.y0 + b.h / 2 - h / 2, w, h };
}

// ---- canvas ---------------------------------------------------------------------
function wallpaperPath() {
  if (S.img) return S.img;
  const la = S.status && S.status.lastApplied;
  return la && (la.mode === "span" || la.mode === "groups") && la.images && la.images[0];
}

function render() {
  const svg = $("#canvas");
  if (!S.vb) return;
  updateFlipButtons();
  const { x, y, w, h } = S.vb;
  svg.setAttribute("viewBox", `${x} ${y} ${w} ${h}`);
  const b = bbox();
  const img = LV.on || ($("#chkWallpaper").checked && wallpaperPath());
  let out = `<defs><pattern id="g100" width="100" height="100" patternUnits="userSpaceOnUse"><path d="M100 0H0V100" fill="none" class="gridline"/></pattern>`;
  out += `<clipPath id="mons">`;
  for (const d of S.displays) {
    const p = S.cur[d.id]; if (!p) continue;
    const q = viewQuad(p);
    out += q ? `<polygon points="${quadPts(q)}"/>` : `<rect x="${p.xMM}" y="${p.yMM}" width="${W(d)}" height="${Hh(d)}"/>`;
  }
  out += `</clipPath></defs>`;
  if (!LV.on) out += `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="url(#g100)"/>`; // live: the scene runs behind, in #liveBg
  if (img && isFinite(b.w)) {
    act();
    views().forEach((v, vi) => {
      const href = v.live ? liveHref(v) : "/api/thumb?w=1800&image=" + encodeURIComponent(v.img);
      // Clip to this view's monitors.
      out += `<clipPath id="mv${vi}">`;
      for (const d of S.displays) {
        const p = S.cur[d.id];
        if (!p || (v.ids && !v.ids.includes(d.id))) continue;
        const q = viewQuad(p);
        out += q ? `<polygon points="${quadPts(q)}"/>` : `<rect x="${p.xMM}" y="${p.yMM}" width="${W(d)}" height="${Hh(d)}"/>`;
      }
      out += `</clipPath>`;
      const f = S.frames[v.key];
      if (!f) {
        loadFrame(v);
        if (v.live) return;
        out += `<image href="${href}" x="${v.b.x0}" y="${v.b.y0}" width="${v.b.w}" height="${v.b.h}" preserveAspectRatio="xMidYMid slice" opacity="0.16"/>`;
        out += `<image href="${href}" x="${v.b.x0}" y="${v.b.y0}" width="${v.b.w}" height="${v.b.h}" preserveAspectRatio="xMidYMid slice" clip-path="url(#mv${vi})"/>`;
        return;
      }
      const r = imgRect(v.b, f);
      const active = v.key === S.activeKey;
      // Flips mirror the image in place, then it turns about its own centre.
      const cx = r.x + r.w / 2, cy = r.y + r.h / 2, rot = f.rotate || 0;
      const tf = f.flipX || f.flipY || rot ? ` transform="translate(${cx} ${cy}) rotate(${rot}) scale(${f.flipX ? -1 : 1} ${f.flipY ? -1 : 1}) translate(${-cx} ${-cy})"` : "";
      const a = `href="${href}" x="${r.x}" y="${r.y}" width="${r.w}" height="${r.h}" preserveAspectRatio="none"${tf}`;
      if (!v.live) {
        out += `<image ${a} opacity="${S.moveImg && active ? 0.4 : 0.16}"/>`;
        out += `<g clip-path="url(#mv${vi})"><image ${a}/></g>`;
      }
      if (S.moveImg && active) {
        // Outline and handles turn with the image.
        const turn = (x, y) => turnPt(x, y, cx, cy, rot);
        const corners = [["nw", r.x, r.y], ["ne", r.x + r.w, r.y], ["se", r.x + r.w, r.y + r.h], ["sw", r.x, r.y + r.h]].map(([c, x, y]) => [c, ...turn(x, y)]);
        out += `<polygon class="imgframe" points="${corners.map((c) => c[1] + "," + c[2]).join(" ")}"/>`;
        // Corner handles: drag to resize.
        const hs = S.vb.w / 70;
        corners.forEach(([c, x, y]) => {
          out += `<rect class="imghandle ${c}" data-corner="${c}" x="${x - hs / 2}" y="${y - hs / 2}" width="${hs}" height="${hs}"/>`;
        });
        // Curved arrows just outside the left and right edges: drag to rotate.
        const R = hs * 1.9, sw = R * 0.14, hs70 = Math.sin(70 * Math.PI / 180), hc70 = Math.cos(70 * Math.PI / 180);
        [[-1, r.x - R * 1.5], [1, r.x + r.w + R * 1.5]].forEach(([side, x]) => {
          const [hx, hy] = turn(x, cy);
          // A 140 degree arc bulging outwards, with an arrowhead at each end
          // pointing along the arc.
          const x0 = side * R * hc70, y0 = R * hs70;
          const head = (px, py, dx, dy) => { const L = R * 0.42, nx = -dy, ny = dx;
            return `M ${px + nx * L * 0.6 - dx * L} ${py + ny * L * 0.6 - dy * L} L ${px} ${py} L ${px - nx * L * 0.6 - dx * L} ${py - ny * L * 0.6 - dy * L}`; };
          const top = head(x0, -y0, -hs70 * side, -hc70);
          const bot = head(x0, y0, -hs70 * side, hc70);
          out += `<g class="rotarrow" data-rot="1" transform="translate(${hx} ${hy}) rotate(${rot})">` +
            `<circle r="${R * 1.25}" class="rothit"/>` +
            `<path d="M ${x0} ${-y0} A ${R} ${R} 0 0 ${side > 0 ? 1 : 0} ${x0} ${y0} ${top} ${bot}" stroke-width="${sw}"/>` +
            `<title>Drag to rotate (Shift: 15° steps)</title></g>`;
        });
      }
    });
  }
  const fs = S.vb.w / 70; // label size in mm, so labels stay readable at any zoom
  S.displays.forEach((d, i) => {
    const p = S.cur[d.id];
    if (!p) return;
    const cls = "mon" + (S.sel === d.id ? " selected" : "") + (S.dragging && S.dragging.id === d.id ? " dragging" : "");
    out += `<g class="${cls}" data-id="${esc(d.id)}" transform="translate(${p.xMM} ${p.yMM})">`;
    const q = viewQuad(p);
    if (q) {
      // Photo-measured shape inside its bounding box (which is what you drag).
      out += `<rect class="frame bbox" width="${W(d)}" height="${Hh(d)}" rx="3"/>`;
      out += `<polygon class="quad" transform="translate(${-p.xMM} ${-p.yMM})" points="${quadPts(q)}"/>`;
    } else out += `<rect class="frame" width="${W(d)}" height="${Hh(d)}" rx="3"/>`;
    out += `<text x="${fs * 0.6}" y="${fs * 1.6}" font-size="${fs * 1.3}" font-weight="700">${i + 1}</text>`;
    out += `<text x="${fs * 0.6 + fs * 1.2}" y="${fs * 1.5}" font-size="${fs * 0.85}" font-weight="600">${esc(d.name)}</text>`;
    out += `<text class="sub" x="${fs * 0.6}" y="${Hh(d) - fs * 0.6}" font-size="${fs * 0.7}">${r1(p.xMM)}, ${r1(p.yMM)} mm${(p.scale || 1) !== 1 ? " · ×" + (p.scale).toFixed(3) : ""}${p.unplaced ? " · not placed yet" : ""}</text>`;
    out += `</g>`;
  });
  for (const g of S.guides) {
    out += g.axis === "x" ? `<line class="guide" x1="${g.v}" x2="${g.v}" y1="${y}" y2="${y + h}"/>` : `<line class="guide" y1="${g.v}" y2="${g.v}" x1="${x}" x2="${x + w}"/>`;
  }
  svg.innerHTML = out;
  updateDirty();
  renderSide();
  if (LV.on) liveAfterRender();
}

function svgPoint(evt) {
  const svg = $("#canvas");
  const pt = svg.createSVGPoint();
  pt.x = evt.clientX; pt.y = evt.clientY;
  return pt.matrixTransform(svg.getScreenCTM().inverse());
}

// Snap the dragged monitor's edges and centre to other monitors' edges and
// centres, within ~10 screen pixels.
function snap(id, x, y, noSnap) {
  S.guides = [];
  if (noSnap || !$("#chkSnap").checked) return { x, y };
  const d = disp(id);
  const thr = (10 * S.vb.w) / $("#canvas").clientWidth;
  let bx = null, by = null;
  for (const o of S.displays) {
    if (o.id === id) continue;
    const p = S.cur[o.id];
    for (const mo of [0, W(d) / 2, W(d)]) for (const t of [p.xMM, p.xMM + W(o) / 2, p.xMM + W(o)]) {
      const dist = Math.abs(x + mo - t);
      if (dist < thr && (!bx || dist < bx.dist)) bx = { dist, x: t - mo, v: t };
    }
    for (const mo of [0, Hh(d) / 2, Hh(d)]) for (const t of [p.yMM, p.yMM + Hh(o) / 2, p.yMM + Hh(o)]) {
      const dist = Math.abs(y + mo - t);
      if (dist < thr && (!by || dist < by.dist)) by = { dist, y: t - mo, v: t };
    }
  }
  if (bx) { x = bx.x; S.guides.push({ axis: "x", v: bx.v }); }
  if (by) { y = by.y; S.guides.push({ axis: "y", v: by.v }); }
  return { x, y };
}

function initCanvas() {
  const svg = $("#canvas");
  svg.addEventListener("pointerdown", (e) => {
    if (S.moveImg && !(e.target.dataset && e.target.dataset.corner) && !e.target.closest("[data-rot]")) {
      // Pick the group under the pointer (span groups).
      const p0 = svgPoint(e), vs = views();
      const v = vs.find((x) => p0.x >= x.b.x0 && p0.x <= x.b.x1 && p0.y >= x.b.y0 && p0.y <= x.b.y1);
      if (v && v.key !== S.activeKey) { S.activeKey = v.key; }
    }
    act();
    if (S.moveImg && S.frame && e.target.closest("[data-rot]")) {
      const r = imgRect(activeB()), p = svgPoint(e);
      const cx = r.x + r.w / 2, cy = r.y + r.h / 2;
      S.imgRotate = { cx, cy, a0: Math.atan2(p.y - cy, p.x - cx), rot0: S.frame.rotate || 0 };
      svg.setPointerCapture(e.pointerId);
      return;
    }
    if (S.moveImg && S.frame && e.target.dataset && e.target.dataset.corner && S.frame.rotate) {
      // Turned image: resize about its centre.
      const r = imgRect(activeB()), p = svgPoint(e);
      const cx = r.x + r.w / 2, cy = r.y + r.h / 2;
      S.imgResize = { centre: true, cx, cy, d0: Math.hypot(p.x - cx, p.y - cy), z0: S.frame.zoom, r };
      svg.setPointerCapture(e.pointerId);
      return;
    }
    if (S.moveImg && S.frame && e.target.dataset && e.target.dataset.corner) {
      const r = imgRect(activeB()), c = e.target.dataset.corner;
      // The opposite corner stays where it is.
      const ax = c.includes("w") ? r.x + r.w : r.x, ay = c.includes("n") ? r.y + r.h : r.y;
      S.imgResize = { ax, ay, cx: c.includes("w") ? r.x : r.x + r.w, cy: c.includes("n") ? r.y : r.y + r.h, r };
      svg.setPointerCapture(e.pointerId);
      return;
    }
    if (S.moveImg && S.frame) {
      const p0 = svgPoint(e);
      S.imgDrag = { px: p0.x, py: p0.y, dx0: S.frame.dx, dy0: S.frame.dy, r: imgRect(activeB()) };
      svg.setPointerCapture(e.pointerId);
      return;
    }
    if (LV.on) return; // the monitors stay put while framing a live scene
    const g = e.target.closest(".mon");
    if (!g) { S.sel = null; render(); return; }
    const id = g.dataset.id;
    S.sel = id;
    const p0 = svgPoint(e);
    S.dragging = { id, ox: S.cur[id].xMM, oy: S.cur[id].yMM, px: p0.x, py: p0.y };
    svg.setPointerCapture(e.pointerId);
    render();
  });
  svg.addEventListener("pointermove", (e) => {
    if (S.imgRotate) {
      const d = S.imgRotate, p = svgPoint(e);
      let deg = d.rot0 + (Math.atan2(p.y - d.cy, p.x - d.cx) - d.a0) * 180 / Math.PI;
      if (e.shiftKey) deg = Math.round(deg / 15) * 15;
      setRotate(deg);
      return;
    }
    if (S.imgResize && S.imgResize.centre) {
      const d = S.imgResize, p = svgPoint(e), b = activeB();
      const z = Math.min(10, Math.max(0.25, d.z0 * Math.hypot(p.x - d.cx, p.y - d.cy) / Math.max(d.d0, 1e-6)));
      const w = S.frame.w * d.r.base * z, h = S.frame.h * d.r.base * z;
      S.frame.zoom = z;
      S.frame.dx = (d.cx - w / 2 - (b.x0 + b.w / 2 - w / 2)) / w;
      S.frame.dy = (d.cy - h / 2 - (b.y0 + b.h / 2 - h / 2)) / h;
      render();
      return;
    }
    if (S.imgResize) {
      const d = S.imgResize, p = svgPoint(e), b = activeB();
      // Scale by how far the pointer is along the anchor-to-corner diagonal.
      const vx = d.cx - d.ax, vy = d.cy - d.ay;
      let k = ((p.x - d.ax) * vx + (p.y - d.ay) * vy) / (vx * vx + vy * vy);
      const z0 = d.z0 || (d.z0 = S.frame.zoom);
      k = Math.min(10 / z0, Math.max(0.25 / z0, k));
      const w = d.r.w * k, h = d.r.h * k;
      const x = vx < 0 ? d.ax - w : d.ax, y = vy < 0 ? d.ay - h : d.ay;
      S.frame.zoom = z0 * k;
      S.frame.dx = (x - (b.x0 + b.w / 2 - w / 2)) / w;
      S.frame.dy = (y - (b.y0 + b.h / 2 - h / 2)) / h;
      render();
      return;
    }
    if (S.imgDrag) {
      const d = S.imgDrag, p = svgPoint(e);
      S.frame.dx = d.dx0 + (p.x - d.px) / d.r.w;
      S.frame.dy = d.dy0 + (p.y - d.py) / d.r.h;
      render();
      return;
    }
    if (!S.dragging) return;
    const dr = S.dragging, p = svgPoint(e);
    const s = snap(dr.id, dr.ox + p.x - dr.px, dr.oy + p.y - dr.py, e.altKey);
    S.cur[dr.id] = { ...S.cur[dr.id], xMM: r1(s.x), yMM: r1(s.y) };
    render();
    calibrateSoon();
  });
  const end = () => {
    if (S.imgDrag || S.imgResize || S.imgRotate) { S.imgDrag = null; S.imgResize = null; S.imgRotate = null; render(); return; }
    if (!S.dragging) return;
    S.dragging = null; S.guides = [];
    fit(); render(); calibrateSoon();
  };
  svg.addEventListener("pointerup", end);
  svg.addEventListener("pointercancel", end);
  // Scroll zooms the wallpaper around the cursor in Move image mode.
  svg.addEventListener("wheel", (e) => {
    act();
    if (!S.moveImg || !S.frame) return;
    e.preventDefault();
    const b = activeB(), r = imgRect(b), p = svgPoint(e);
    const u = (p.x - r.x) / r.w, v = (p.y - r.y) / r.h;
    const z = Math.min(10, Math.max(0.25, S.frame.zoom * (e.deltaY < 0 ? 1.08 : 1 / 1.08)));
    const w = S.frame.w * r.base * z, h = S.frame.h * r.base * z;
    S.frame.zoom = z;
    S.frame.dx = (p.x - u * w - (b.x0 + b.w / 2 - w / 2)) / w;
    S.frame.dy = (p.y - v * h - (b.y0 + b.h / 2 - h / 2)) / h;
    render();
  }, { passive: false });
  window.addEventListener("resize", () => { fit(); render(); });
  document.addEventListener("keydown", (e) => {
    if (!$("#tab-layout").classList.contains("active")) return;
    // Move image mode: arrows move the wallpaper (1 mm, Shift 10 mm, Alt 0.2 mm);
    // + and - zoom by 1% (Shift 5%).
    if (S.moveImg) act();
    if (S.moveImg && S.frame && !["INPUT", "SELECT", "TEXTAREA"].includes(document.activeElement.tagName)) {
      const step = e.shiftKey ? 10 : e.altKey ? 0.2 : 1;
      const d = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
      const r = imgRect(activeB());
      if (d) {
        e.preventDefault();
        S.frame.dx += d[0] / r.w;
        S.frame.dy += d[1] / r.h;
        render();
        return;
      }
      if ((e.key === "[" || e.key === "]" || e.key === "{" || e.key === "}") && !e.ctrlKey && !e.metaKey) {
        e.preventDefault();
        const step = e.shiftKey ? 15 : e.altKey ? 0.1 : 1;
        setRotate((S.frame.rotate || 0) + (e.key === "]" || e.key === "}" ? step : -step));
        return;
      }
      if ((e.key === "h" || e.key === "v") && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.preventDefault();
        flipImg(e.key === "h" ? "flipX" : "flipY");
        return;
      }
      if (["+", "=", "-", "_"].includes(e.key)) {
        e.preventDefault();
        const b = activeB(), cx = r.x + r.w / 2, cy = r.y + r.h / 2; // zoom about the image centre
        const f = (e.key === "+" || e.key === "=" ? 1 : -1) * (e.shiftKey ? 0.05 : 0.01);
        const z = Math.min(10, Math.max(0.25, S.frame.zoom * (1 + f)));
        const w = S.frame.w * r.base * z, h = S.frame.h * r.base * z;
        S.frame.zoom = z;
        S.frame.dx = (cx - w / 2 - (b.x0 + b.w / 2 - w / 2)) / w;
        S.frame.dy = (cy - h / 2 - (b.y0 + b.h / 2 - h / 2)) / h;
        render();
        return;
      }
    }
    if (!S.sel) return;
    if (["INPUT", "SELECT", "TEXTAREA"].includes(document.activeElement.tagName)) return;
    const step = e.shiftKey ? 10 : e.altKey ? 0.2 : 1;
    const d = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }[e.key];
    if (d) { e.preventDefault(); nudge(d[0], d[1]); }
    if (e.key === "Escape") { S.sel = null; render(); }
    if (e.key === "Tab") {
      e.preventDefault();
      const i = S.displays.findIndex((x) => x.id === S.sel);
      S.sel = S.displays[(i + (e.shiftKey ? S.displays.length - 1 : 1)) % S.displays.length].id;
      render();
    }
  });
}

function nudge(dx, dy) {
  const p = S.cur[S.sel];
  S.cur[S.sel] = { ...p, xMM: r1(p.xMM + dx), yMM: r1(p.yMM + dy) };
  render();
  calibrateSoon();
}

// ---- side panel -----------------------------------------------------------------
// The smallest image that covers the whole layout with no monitor upscaled:
// the layout's size in mm at the highest monitor density.
function neededSize() {
  const b = bbox();
  if (!isFinite(b.w) || b.w <= 0) return null;
  let ppi = 0, who = null;
  for (const d of S.displays) {
    if (!S.cur[d.id]) continue;
    const v = d.resW / (W(d) / 25.4);
    if (v > ppi) { ppi = v; who = d; }
  }
  if (!who) return null;
  return { w: Math.ceil(b.w / 25.4 * ppi), h: Math.ceil(b.h / 25.4 * ppi), ratio: b.w / b.h, ppi, who, mm: [Math.round(b.w), Math.round(b.h)] };
}

function needHTML() {
  const n = neededSize();
  if (!n) return "";
  let cur = "";
  const v = act(), f = v && S.frames[v.key];
  if (f && f.w && f.h && !v.ids) {
    const k = Math.max(n.w / (f.w * (f.zoom || 1)), n.h / (f.h * (f.zoom || 1)));
    cur = k <= 1.001 ? `<div class="ok">Current wallpaper ${f.w} × ${f.h}: sharp on every monitor</div>`
      : `<div class="warn">Current wallpaper ${f.w} × ${f.h}: ${n.who.name} is upscaled ${k.toFixed(2)}×</div>`;
  }
  return `<div class="need"><label>Wallpaper size to cover all monitors</label>
    <div class="big">${n.w} × ${n.h}</div>
    <div class="muted">ratio ${n.ratio.toFixed(2)} : 1 · layout ${n.mm[0]} × ${n.mm[1]} mm · set by ${esc(n.who.name)} (${n.ppi.toFixed(0)} PPI)</div>${cur}
    <div class="note">Anything at least this big covers every monitor with no upscaling. A different shape still works: the extra is cropped.</div></div>`;
}

function renderSide() {
  if (LV.on) { liveRenderSide(); return; }
  const side = $("#side");
  if (side.dataset.mode === "live") { side.dataset.mode = ""; side.classList.remove("live-side"); }
  const d = S.sel && disp(S.sel);
  if (!d) { side.innerHTML = needHTML() + `<div class="empty">Select a monitor to edit its position.<br><br>Moving a monitor <b>up</b> here moves the image on it <b>down</b>.</div>`; return; }
  if (side.dataset.for === d.id && side.contains(document.activeElement) && document.activeElement.tagName === "INPUT") {
    if ($("#needBox")) $("#needBox").innerHTML = needHTML();
    const p = S.cur[d.id];
    if (document.activeElement.id !== "sx") $("#sx").value = p.xMM;
    if (document.activeElement.id !== "sy") $("#sy").value = p.yMM;
    if (document.activeElement.id !== "ss") $("#ss").value = (p.scale || 1).toFixed(4);
    return;
  }
  const p = S.cur[d.id];
  side.dataset.for = d.id;
  side.innerHTML = `
    <h3>${esc(d.name)}</h3><div class="id">${esc(d.id)}</div>
    <div class="kv"><span>Resolution</span><span>${d.resW} × ${d.resH}</span>
      <span>Size</span><span>${r1(d.widthMM)} × ${r1(d.heightMM)} mm</span>
      <span>Density</span><span>${d.ppi.toFixed(1)} PPI</span>
      <span>Windows puts it at</span><span>${d.windowsX}, ${d.windowsY}</span></div>
    <label>Position (mm)</label>
    <div class="pair"><input id="sx" type="number" step="0.5" value="${p.xMM}" title="x"><input id="sy" type="number" step="0.5" value="${p.yMM}" title="y"></div>
    <label>Apparent scale (1 = physical size; photo calibration sets this)</label>
    <input id="ss" type="number" step="0.005" min="0.5" max="2" value="${(p.scale || 1).toFixed(4)}">
    ${p.quad ? `<div class="row"><span class="muted">Perspective shape from photo calibration</span><button id="btnShape" title="Use a plain rectangle again">Reset shape</button></div>` : ""}
    <div class="nudge"><span></span><button data-n="0,-1">↑</button><span></span>
      <button data-n="-1,0">←</button><button data-n="0,1">↓</button><button data-n="1,0">→</button></div>
    <label>Measured visible size (mm), if the detected one is off</label>
    <div class="pair"><input id="sw" type="number" step="0.5" value="${r1(d.widthMM)}"><input id="sh" type="number" step="0.5" value="${r1(d.heightMM)}"></div>
    <div class="row"><button id="btnSize">Save size</button><button id="btnSizeReset">Use detected</button></div>
    <div class="note">The detected size comes from the monitor's EDID and is usually right to about 1 mm. Measure only the lit area, not the bezel.</div>
    <div id="needBox">${needHTML()}</div>`;
  const setXY = () => {
    const x = parseFloat($("#sx").value), y = parseFloat($("#sy").value);
    const k = parseFloat($("#ss").value);
    if (isFinite(x) && isFinite(y)) { S.cur[d.id] = { ...S.cur[d.id], xMM: r1(x), yMM: r1(y), scale: isFinite(k) && k > 0 ? k : 1 }; render(); calibrateSoon(); }
  };
  if ($("#btnShape")) $("#btnShape").addEventListener("click", () => { S.cur[d.id] = { ...S.cur[d.id], quad: null, mesh: null, qb: null, resetShape: true }; side.dataset.for = ""; render(); toast("Shape reset; Save to keep it"); });
  $("#sx").addEventListener("input", setXY);
  $("#sy").addEventListener("input", setXY);
  $("#ss").addEventListener("input", setXY);
  $$(".nudge button", side).forEach((b) => b.addEventListener("click", () => { const [dx, dy] = b.dataset.n.split(",").map(Number); nudge(dx, dy); }));
  $("#btnSize").addEventListener("click", async () => {
    try {
      await api("/api/config", { monitor: d.id, widthMM: parseFloat($("#sw").value), heightMM: parseFloat($("#sh").value) });
      toast("Size saved"); await loadLayout(true);
    } catch (e) { fail(e); }
  });
  $("#btnSizeReset").addEventListener("click", async () => {
    try { await api("/api/config", { monitor: d.id, clearSize: true }); toast("Using the detected size"); await loadLayout(true); } catch (e) { fail(e); }
  });
}

function updateDirty() {
  const dirty = isDirty();
  $("#dirty").classList.toggle("hidden", !dirty);
  $("#btnSave").disabled = !dirty;
  $("#btnRevert").disabled = !dirty;
}

// ---- live calibration -------------------------------------------------------------
// While on, every edit re-renders the physical test pattern on the real
// screens. At most one request is in flight; the latest positions win.
let calInFlight = false, calPending = false, calTimer = null;
function calibrateSoon() {
  if (!S.calibrating) return;
  calPending = true;
  if (calTimer || calInFlight) return;
  calTimer = setTimeout(sendCalibration, 120);
}
async function sendCalibration() {
  calTimer = null;
  if (!calPending || !S.calibrating) return;
  calPending = false; calInFlight = true;
  try { await api("/api/calibrate", { layout: S.view, placements: plain(S.cur) }); }
  catch (e) { fail(e); }
  calInFlight = false;
  if (calPending) calTimer = setTimeout(sendCalibration, 60);
}
const plain = (cur) => Object.fromEntries(Object.entries(cur).map(([k, v]) => [k, { xMM: v.xMM, yMM: v.yMM, scale: v.scale || 1, ...(v.resetShape ? { resetShape: true } : {}), ...(curQuad(v) ? { quad: curQuad(v).map(r1) } : {}), ...(carried(v, v.mesh) ? { mesh: carried(v, v.mesh), meshNX: v.meshNX, meshNY: v.meshNY } : {}) }]));

// A photo-measured shape (x,y list), following any move or rescale in the editor.
function carried(p, pts) {
  if (!p || !pts || !p.qb) return null;
  const k = (p.scale || 1) / (p.qb.scale || 1);
  const q = [];
  for (let i = 0; i + 1 < pts.length; i += 2) q.push(Math.round((p.xMM + k * (pts[i] - p.qb.xMM)) * 100) / 100, Math.round((p.yMM + k * (pts[i + 1] - p.qb.yMM)) * 100) / 100);
  return q;
}
const curQuad = (p) => (p && p.quad ? carried(p, p.quad) : null);
// The shape the editor draws: the photo-measured (angled) one, or with the
// perspective view off, the plain upright rectangle. Rendering is unaffected.
const viewQuad = (p) => ($("#chkPersp").checked ? curQuad(p) : null);
const quadPts = (q) => [0, 2, 4, 6].map((i) => `${q[i]},${q[i + 1]}`).join(" ");

// ---- toolbar ------------------------------------------------------------------------
function initToolbar() {
  $("#layoutSelect").addEventListener("change", async (e) => {
    const v = e.target.value;
    if (v === LIVE_VALUE) { liveEnter(); return; }
    if (LV.on) {
      if (frameDirty() && !confirm("Discard unsaved changes to the framing?")) { e.target.value = LIVE_VALUE; return; }
      liveLeave(); if (v === S.view) return; e.target.value = v;
    }
    if (isDirty() && !confirm("Discard unsaved changes?")) { e.target.value = S.view; return; }
    S.view = e.target.value; S.cur = {};
    await loadLayout();
  });
  $("#chkWallpaper").addEventListener("change", render);
  $("#btnIdentify").addEventListener("click", () => api("/api/identify", { toggle: true })
    .then((r) => { toast(r.on ? "Numbers are up. Click Identify again to hide them." : "Numbers hidden, wallpaper restored"); return loadStatus(); })
    .catch(fail));
  $("#btnCalibrate").addEventListener("click", async () => {
    try {
      if (S.calibrating) {
        await api("/api/calibrate", { stop: true });
        S.calibrating = false; toast("Calibration off, wallpaper restored");
      } else {
        S.calibrating = true;
        await api("/api/calibrate", { layout: S.view, placements: plain(S.cur) });
        toast("Calibration pattern is on your screens. Drag until the lines continue straight across the bezels.");
      }
      $("#btnCalibrate").classList.toggle("on", S.calibrating);
    } catch (e) { S.calibrating = false; fail(e); }
  });
  $("#btnRevert").addEventListener("click", () => {
    S.cur = clone(S.saved);
    for (const k in S.frames) if (S.framesSaved[k]) Object.assign(S.frames[k], S.framesSaved[k]);
    if (LV.on) livePushFrame(true);
    fit(); render(); calibrateSoon();
  });
  $("#btnMoveImg").addEventListener("click", () => {
    if (!$("#chkWallpaper").checked || !wallpaperPath()) { toast("Show a wallpaper first (Library: Show in editor, or apply one)", true); return; }
    S.moveImg = !S.moveImg;
    $("#btnMoveImg").classList.toggle("on", S.moveImg);
    $("#canvas").classList.toggle("moving-img", S.moveImg);
    if (S.moveImg) toast("Drag or use the arrow keys to move the wallpaper; drag a corner, scroll or +/- to resize; drag a curved arrow or press [ ] to rotate (H/V mirror). Save & apply to use it.");
    render();
  });
  $("#btnResetImg").addEventListener("click", () => { act(); if (S.frame) { Object.assign(S.frame, { zoom: S.frame.def || 1, dx: 0, dy: 0, flipX: false, flipY: false, rotate: 0 }); render(); } });
  $("#btnAutoImg").addEventListener("click", async () => {
    const v = act();
    if (!v) { toast("Show a wallpaper first (Library: Show in editor, or apply one)", true); return; }
    try {
      await api("/api/framing", { image: v.img, group: v.ids ? v.ids.join(",") : "", auto: true });
      delete S.frames[v.key]; delete S.framesSaved[v.key]; S.framesLoading[v.key] = false;
      await loadFrame(v);
      toast(S.status && S.status.smartFrame ? "Smart framing places this image again. Save & apply to use it." : "Back to the plain fill (smart framing is off in Settings)");
    } catch (e) { fail(e); }
  });
  $("#btnRotL").addEventListener("click", () => { act(); if (S.frame) setRotate((S.frame.rotate || 0) - 90); else toast("Show a wallpaper first", true); });
  $("#btnRotR").addEventListener("click", () => { act(); if (S.frame) setRotate((S.frame.rotate || 0) + 90); else toast("Show a wallpaper first", true); });
  $("#inpRot").addEventListener("change", (e) => { const v = parseFloat(e.target.value); if (!isNaN(v)) setRotate(v); });
  $("#btnSave").addEventListener("click", () => save().then(() => toast("Layout saved")).catch(fail));
  $("#btnApply").addEventListener("click", async () => {
    try {
      if (LV.on) { await save(); toast("Saved: the animation uses this framing now"); return; }
      await save();
      if (S.view !== S.status.activeLayout) await api("/api/layout/action", { action: "activate", name: S.view });
      const la = S.status.lastApplied;
      const images = S.img ? [S.img] : la && la.images;
      if (!images) { toast("Saved. Pick a wallpaper in the Library to apply."); return; }
      const t0 = performance.now();
      const res = await api("/api/apply", { images, mode: S.img ? "span" : la.mode, layout: S.view });
      S.calibrating = false; S.img = null;
      $("#btnCalibrate").classList.remove("on");
      toast(`Applied in ${Math.round(performance.now() - t0)} ms` + (res.notes && res.notes.length ? " · " + res.notes[res.notes.length - 1] : ""));
      await refresh();
    } catch (e) { fail(e); }
  });
}

// Mirror the active wallpaper view left-right (flipX) or top-bottom (flipY).
function flipImg(axis) {
  if (!$("#chkWallpaper").checked || !wallpaperPath()) { toast("Show a wallpaper first (Library: Show in editor, or apply one)", true); return; }
  act();
  if (!S.frame) { toast("Image still loading, try again", true); return; }
  S.frame[axis] = !S.frame[axis];
  updateFlipButtons();
  render();
}
function updateFlipButtons() {
  act();
  if (document.activeElement !== $("#inpRot")) $("#inpRot").value = S.frame ? (S.frame.rotate || 0) : 0;
}

async function save() {
  for (const k in S.frames) {
    const f = S.frames[k], g = S.framesSaved[k];
    if (!g || !frameDiff(f, g)) continue;
    if (f.live) await api("/api/live/frame", Object.assign({ scene: f.scene }, frameVals(f)));
    else await api("/api/framing", Object.assign({ image: f.img, group: f.ids ? f.ids.join(",") : "" }, frameVals(f)));
    S.framesSaved[k] = frameVals(f);
  }
  if (LV.on) { render(); return; } // framing a live scene: the layout itself is untouched
  await api("/api/layout/save", { layout: S.view, placements: plain(S.cur) });
  S.saved = clone(S.cur);
  for (const k in S.saved) delete S.saved[k].unplaced;
  for (const k in S.cur) delete S.cur[k].unplaced;
  render();
}

// ---- first run ---------------------------------------------------------------------
function welcomeSeen() { try { return localStorage.getItem("spanwall.welcome") === "1"; } catch (e) { return false; } }
function showWelcomeIfNeeded() {
  const unplaced = S.displays.length && S.displays.every((d) => !S.saved[d.id] || S.saved[d.id].unplaced);
  const asked = new URLSearchParams(location.search).has("welcome");
  if ((asked || unplaced) && !welcomeSeen()) {
    $("#wDir").textContent = (S.status && S.status.wallpaperDir) || "";
    const ext = S.status && S.status.extension;
    $("#wExt").innerHTML = ext ? `open <code>${esc(ext)}</code> and Claude Desktop installs the Spanwall extension.` : "install Claude Desktop, then run spanwall install again.";
    $("#welcome").classList.remove("hidden");
  }
}

function initWelcome() {
  const close = () => { $("#welcome").classList.add("hidden"); try { localStorage.setItem("spanwall.welcome", "1"); } catch (e) {} };
  $("#btnWelcomeClose").addEventListener("click", close);
  $("#btnWelcomeDone").addEventListener("click", close);
  $$("#welcome [data-goto]").forEach((a) => a.addEventListener("click", (e) => { e.preventDefault(); switchTab(a.dataset.goto); }));
  $("#wIdentify").addEventListener("click", () => $("#btnIdentify").click());
  $("#wPhoto").addEventListener("click", () => { $("#welcome").classList.add("hidden"); $("#photoPanel").classList.remove("hidden"); });
  // Windows' arrangement in pixels, converted with each monitor's density,
  // as a starting point (unsaved until Save).
  $("#wWindows").addEventListener("click", () => {
    for (const d of S.displays) S.cur[d.id] = { xMM: r1(d.windowsX / d.ppi * 25.4), yMM: r1(d.windowsY / d.ppi * 25.4), scale: 1 };
    fit(); render();
    toast("Placed from Windows' arrangement. Drag them to where they really are, then Save & apply.");
  });
}

// ---- photo calibration --------------------------------------------------------------
function initPhoto() {
  $("#btnPhoto").addEventListener("click", () => $("#photoPanel").classList.toggle("hidden"));
  $("#btnPhotoClose").addEventListener("click", () => $("#photoPanel").classList.add("hidden"));
  $("#btnDots").addEventListener("click", async () => {
    try {
      const on = S.status && S.status.overlay === "photo";
      await api("/api/photo-pattern", { stop: on });
      if (!on) { S.calibrating = false; $("#btnCalibrate").classList.remove("on"); }
      await loadStatus();
    } catch (e) { fail(e); }
  });
  const drop = $("#drop");
  $("#photoFile").addEventListener("change", (e) => e.target.files[0] && uploadPhoto(e.target.files[0]));
  drop.addEventListener("dragover", (e) => { e.preventDefault(); drop.classList.add("over"); });
  drop.addEventListener("dragleave", () => drop.classList.remove("over"));
  drop.addEventListener("drop", (e) => { e.preventDefault(); drop.classList.remove("over"); if (e.dataTransfer.files[0]) uploadPhoto(e.dataTransfer.files[0]); });
}

async function uploadPhoto(file) {
  const fd = new FormData();
  fd.append("photo", file);
  fd.append("mode", $("#photoMode").value);
  fd.append("layout", S.view);
  $("#photoResult").innerHTML = `<p class="muted">Analysing ${esc(file.name)}…</p>`;
  try {
    const r = await fetch("/api/align", { method: "POST", body: fd });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || r.statusText);
    showProposal(j);
    await loadStatus();
  } catch (e) { $("#photoResult").innerHTML = `<p class="bad">${esc(e.message)}</p>`; }
}

function showProposal(p) {
  $("#photoPanel").classList.remove("hidden");
  const rows = p.monitors.map((m) => `<tr><td>${m.number} ${esc(m.name)}</td>
    <td>${m.found ? `${m.xMM}, ${m.yMM}` : "—"}</td><td>${m.found ? "×" + m.scale.toFixed(3) : ""}</td>
    <td>${m.found ? m.movedMM + " mm" : ""}</td><td class="${m.found ? "" : "bad"}">${esc(m.quality)}${m.found && m.quad ? " · perspective shape" : m.found && m.residualMM > 3 ? ` · <span class="bad">${m.residualMM.toFixed(1)} mm left over</span>` : ""}</td></tr>`).join("");
  $("#photoResult").innerHTML = `
    <img src="data:image/jpeg;base64,${p.overlay}" alt="Detected screens">
    <table><tr><th>Monitor</th><th>Position (mm)</th><th>Scale</th><th>Moves</th><th>Quality</th></tr>${rows}</table>
    <div class="notes">${(p.notes || []).map(esc).join("<br>")} · ${p.mode} mode · ${p.tookMs} ms</div>
    <div class="row"><button id="btnPropEditor">Load into editor</button><button id="btnPropApply" class="primary">Save &amp; apply</button></div>`;
  $("#btnPropEditor").addEventListener("click", () => {
    for (const m of p.monitors) if (m.found) S.cur[m.id] = { xMM: m.xMM, yMM: m.yMM, scale: m.scale, quad: m.quad || null, mesh: m.mesh || null, meshNX: m.mesh ? 17 : 0, meshNY: m.mesh ? 9 : 0, qb: m.quad ? { xMM: m.xMM, yMM: m.yMM, scale: m.scale } : null };
    if (S.view !== p.layout) S.view = p.layout;
    fit(); render(); calibrateSoon();
    $("#photoPanel").classList.add("hidden");
    toast("Loaded the photo result as unsaved edits. Check it with Live calibration, then Save & apply.");
  });
  $("#btnPropApply").addEventListener("click", async () => {
    try { await api("/api/align/apply", {}); toast("Layout saved and wallpaper re-applied"); $("#photoPanel").classList.add("hidden"); S.cur = {}; await refresh(); } catch (e) { fail(e); }
  });
}

// ---- library ----------------------------------------------------------------------
async function loadLibrary() {
  try {
    const [r] = await Promise.all([api("/api/wallpapers"), liveLoadScenes()]);
    S.lib = r.images || [];
    S.folders = r.folders || [];
    $("#libDir").textContent = r.dir;
    renderLibrary();
    showSlideshow();
  } catch (e) { fail(e); }
}

const inFolder = (im, name) => (im.folders || []).some((f) => f.toLowerCase() === name.toLowerCase());

function renderLibTabs() {
  const tab = S.libTab;
  if (!["all", "fav", "ban", "live"].includes(tab) && !S.folders.some((f) => f.name.toLowerCase() === tab.toLowerCase())) S.libTab = "all";
  const n = (pred) => S.lib.filter(pred).length;
  const tabs = [["all", "All wallpapers", S.lib.length], ["fav", "Favourites", n((im) => im.rating === "fav")], ["live", "Live wallpapers", LV.scenes.length]]
    .concat(S.folders.map((f) => [f.name, f.name, f.count]));
  const bans = n((im) => im.rating === "ban");
  if (bans) tabs.push(["ban", "Banned", bans]);
  const cur = S.folders.find((f) => f.name.toLowerCase() === S.libTab.toLowerCase());
  $("#libTabs").innerHTML = tabs.map(([k, label, c]) => `<button data-libtab="${esc(k)}" class="${k.toLowerCase() === S.libTab.toLowerCase() ? "active" : ""}">${esc(label)}<span class="n">${c}</span></button>`).join("") +
    `<button class="new" data-libnew="1" title="Make a folder; a wallpaper can be in several">+ New folder</button>` +
    (S.libTab === "live" ? `<span class="tools"><button data-livemake="1" title="Turn a GIF, video or picture into ASCII art">+ From a GIF, video or picture</button></span>` : "") +
    (cur && !cur.builtin ? `<span class="tools"><button data-libren="1">Rename</button><button data-libdel="1" class="danger">Delete folder</button></span>` : "");
}

function renderLibrary() {
  renderLibTabs();
  if (S.libTab === "live") { liveRenderCards(); return; }
  const cur = wallpaperPath();
  const t = S.libTab;
  const keep = (im) => t === "all" || (t === "fav" ? im.rating === "fav" : t === "ban" ? im.rating === "ban" : inFolder(im, t));
  const cards = S.lib.map((im, i) => !keep(im) ? "" : `
      <div class="card ${im.path === cur ? "current" : ""} ${im.rating === "ban" ? "banned" : ""}" style="--ar:${im.width && im.height ? Math.min(6, Math.max(0.4, im.width / im.height)).toFixed(4) : 1.7778}">
        <img loading="lazy" src="/api/thumb?w=${im.width / im.height > 2.2 ? 1200 : 640}&trim=0&image=${encodeURIComponent(im.path)}" data-i="${i}" title="Show in the layout editor">
        <div class="meta"><b title="${esc(im.name)}">${esc(im.name)}</b><span class="muted">${im.width}×${im.height} · ${im.ratio}</span></div>
        <div class="rate">
          <button data-rate="fav" data-i="${i}" class="${im.rating === "fav" ? "on" : ""}" title="Favourite: comes up more often in the slideshow">★</button>
          <button data-rate="ban" data-i="${i}" class="${im.rating === "ban" ? "on" : ""}" title="Never show in the slideshow">Ban</button>
          <button class="fbtn" data-fmenu="${i}" title="${esc((im.folders || []).join(", ") || "In no folder")}">Folders${im.folders && im.folders.length ? " (" + im.folders.length + ")" : ""} ▾</button>
          <button class="rm" data-remove="${i}" title="Remove from the library (moves the file to the Recycle Bin)">🗑</button>
        </div>
        <div class="acts"><button data-act="show" data-i="${i}">Show in editor</button><button data-act="apply" data-i="${i}" class="primary">Apply</button></div>
      </div>`).join("");
  const empty = !S.lib.length ? "No images in this folder yet." : t === "fav" ? "No favourites yet: press ★ on a wallpaper." :
    t === "all" ? "" : "Nothing here yet: use a wallpaper's Folders ▾ menu to add it.";
  $("#libGrid").innerHTML = cards.trim() ? cards + `<div class="fill"></div>` : `<p class="muted" style="padding:18px">${empty}</p>`;
}

// Folder checkboxes for one wallpaper, next to its Folders button.
function openFolderMenu(i, btn) {
  const im = S.lib[i];
  const m = $("#folderMenu");
  m.innerHTML = S.folders.map((f) => `<label><input type="checkbox" data-fold="${esc(f.name)}" ${inFolder(im, f.name) ? "checked" : ""}> ${esc(f.name)}</label>`).join("") +
    `<div class="add"><button data-foldnew="1">+ New folder…</button></div>`;
  m.dataset.i = i;
  m.classList.remove("hidden");
  const r = btn.getBoundingClientRect();
  const h = m.offsetHeight, w = m.offsetWidth;
  m.style.left = Math.max(8, Math.min(innerWidth - w - 8, r.right - w)) + "px";
  m.style.top = (r.bottom + 4 + h > innerHeight ? r.top - h - 4 : r.bottom + 4) + "px";
}
const closeFolderMenu = () => $("#folderMenu").classList.add("hidden");

async function newFolder(addIndex) {
  const name = (prompt("Folder name") || "").trim();
  if (!name) return null;
  try {
    await api("/api/folders", { action: "create", name });
    if (addIndex !== undefined) await api("/api/rate", { image: S.lib[addIndex].path, folder: name, in: true });
    toast(`Folder ${name} made` + (addIndex !== undefined ? ` with ${S.lib[addIndex].name} in it` : ""));
    return name;
  } catch (e) { fail(e); return null; }
}

// fmtMinutes writes a slideshow interval the short way: 45 min, 2 hours,
// 1 day 6 hours, 1 hour 30 min.
function fmtMinutes(m) {
  const d = Math.floor(m / 1440), h = Math.floor((m % 1440) / 60), mm = m % 60;
  const part = (n, one, many) => (n ? `${n} ${n === 1 ? one : many}` : "");
  return [part(d, "day", "days"), part(h, "hour", "hours"), part(mm, "min", "min")].filter(Boolean).join(" ") || "0 min";
}

const EVERY = [[1, "1 min"], [5, "5 min"], [10, "10 min"], [15, "15 min"], [30, "30 min"], [60, "1 hour"], [180, "3 hours"], [360, "6 hours"], [720, "12 hours"], [1440, "1 day"]];

function showSlideshow() {
  const ss = S.status && S.status.slideshow;
  if (!ss) return;
  const sc = S.status.schedule || {};
  const b = $("#btnSlideToggle");
  b.classList.toggle("on", ss.enabled);
  b.textContent = ss.enabled ? "❚❚ Pause" : "▶ Play";
  // Album: all, favourites, then every folder (new ones appear here).
  const opts = [["", "All wallpapers"], ["fav", "Favourites"], ["live", "Live wallpapers"]].concat(S.folders.map((f) => [f.name, f.name]));
  if (ss.source && !opts.some((o) => o[0] === ss.source)) opts.push([ss.source, ss.source]);
  const html = opts.map(([v, l]) => `<option value="${esc(v)}">${esc(l)}</option>`).join("");
  if ($("#slideSource").dataset.html !== html) { $("#slideSource").innerHTML = html; $("#slideSource").dataset.html = html; }
  $("#slideSource").value = ss.source || "";
  $("#slideSource").disabled = !!sc.enabled;
  $("#slideSource").title = sc.enabled ? "Day/night is on, so the Day and Night folders are playing" : "Which album the slideshow plays";
  const ev = EVERY.some(([m]) => m === ss.intervalMin) ? EVERY : EVERY.concat([[ss.intervalMin, fmtMinutes(ss.intervalMin)]]).sort((a, c) => a[0] - c[0]);
  const eh = ev.map(([m, l]) => `<option value="${m}">${l}</option>`).join("") + `<option value="custom">Custom…</option>`;
  if ($("#slideEvery").dataset.html !== eh) { $("#slideEvery").innerHTML = eh; $("#slideEvery").dataset.html = eh; }
  $("#slideEvery").value = ss.intervalMin;
  $("#btnSlideOrder").classList.toggle("on", ss.order !== "name");
  $$("input[name=smode]").forEach((r) => (r.checked = r.value === ss.mode));
  const next = ss.enabled && ss.next ? " · next " + new Date(ss.next).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
  const now = ss.current && ss.current.length ? "Now: " + ss.current.join(", ") : "";
  $("#slideInfo").textContent = now + next;
  $("#slideInfo").title = now + next;
  $("#btnSlidePrev").disabled = !ss.canGoBack;
  $("#btnDayNight").classList.toggle("on", !!sc.enabled);
  $("#btnDayNight").textContent = sc.enabled ? (sc.period === "night" ? "☾ Night" : "☀ Day") : "☀ Day/night";
  $("#btnDayNight").title = sc.enabled ? `Day/night on: playing the ${sc.period === "night" ? "Night" : "Day"} folder. Click to turn off.` : "Day folder by day, Night folder at night";
}

function initLibrary() {
  $("#btnLibRefresh").addEventListener("click", loadLibrary);
  $("#btnLibPull").addEventListener("click", async (e) => {
    const b = e.target;
    b.disabled = true; b.textContent = "Pulling…";
    const st = S.status || {};
    const msgs = [], errs = [];
    if (st.wallhavenUser || st.wallhavenKey) {
      try {
        const r = await api("/api/wallhaven/sync", {});
        const n = r.downloaded ? r.downloaded.length : 0;
        msgs.push(n ? `${n} new from ${r.collection}` : `${r.collection} up to date`);
        if (r.errors && r.errors.length) errs.push(r.errors[0]);
      } catch (err) { errs.push(err.message); }
    }
    const so = st.sources || {};
    if (so.bing || so.apod || so.wikimedia || so.reddit || so.unsplash) {
      try {
        const r = await api("/api/sources/sync", {});
        const n = r.downloaded ? r.downloaded.length : 0;
        msgs.push(n ? `${n} new from sources` : "sources up to date");
        if (r.errors && r.errors.length) errs.push(r.errors[0]);
      } catch (err) { errs.push(err.message); }
    }
    if (!msgs.length && !errs.length) toast("Nothing to pull from yet: set up Wallhaven or sources in Settings", true);
    else if (errs.length) toast((msgs.length ? msgs.join(" · ") + " · " : "") + errs[0], true);
    else toast(msgs.join(" · "));
    await loadLibrary();
    b.disabled = false; b.textContent = "Pull new wallpapers";
  });
  $("#libTabs").addEventListener("click", async (e) => {
    const t = e.target.closest("[data-libtab]");
    if (t) { S.libTab = t.dataset.libtab; renderLibrary(); $("#libGrid").scrollTop = 0; return; }
    if (e.target.closest("[data-livemake]")) { liveMake(); return; }
    if (e.target.closest("[data-libnew]")) { const n = await newFolder(); if (n) { S.libTab = n; await loadLibrary(); } return; }
    if (e.target.closest("[data-libren]")) {
      const to = (prompt(`Rename folder ${S.libTab} to`, S.libTab) || "").trim();
      if (!to || to === S.libTab) return;
      try { await api("/api/folders", { action: "rename", name: S.libTab, to }); S.libTab = to; toast("Folder renamed"); await loadLibrary(); } catch (err) { fail(err); }
      return;
    }
    if (e.target.closest("[data-libdel]")) {
      if (!confirm(`Delete the folder ${S.libTab}? Its wallpapers stay in the library.`)) return;
      try { await api("/api/folders", { action: "delete", name: S.libTab }); toast("Folder deleted"); S.libTab = "all"; await loadLibrary(); } catch (err) { fail(err); }
    }
  });
  $("#folderMenu").addEventListener("change", async (e) => {
    const c = e.target.closest("[data-fold]");
    if (!c) return;
    const im = S.lib[+$("#folderMenu").dataset.i];
    try {
      const r = await api("/api/rate", { image: im.path, folder: c.dataset.fold, in: c.checked });
      im.folders = r.folders || [];
      const f = S.folders.find((x) => x.name === c.dataset.fold);
      if (f) f.count += c.checked ? 1 : -1;
      const keepOpen = $("#folderMenu").dataset.i;
      renderLibrary();
      const btn = $(`[data-fmenu="${keepOpen}"]`);
      if (btn) openFolderMenu(+keepOpen, btn); else closeFolderMenu();
    } catch (err) { fail(err); c.checked = !c.checked; }
  });
  $("#folderMenu").addEventListener("click", async (e) => {
    if (!e.target.closest("[data-foldnew]")) return;
    const i = +$("#folderMenu").dataset.i;
    closeFolderMenu();
    if (await newFolder(i)) await loadLibrary();
  });
  document.addEventListener("mousedown", (e) => { if (!e.target.closest("#folderMenu") && !e.target.closest("[data-fmenu]")) closeFolderMenu(); });
  $("#libGrid").addEventListener("scroll", closeFolderMenu);
  $("#btnDayNight").addEventListener("click", async () => {
    const sc = (S.status && S.status.schedule) || {};
    const on = !sc.enabled;
    if (on && !sc.dayCount && !sc.nightCount) { toast("Add wallpapers to the Day and Night folders first (Folders ▾ on each wallpaper)", true); return; }
    try {
      await api("/api/config", { schedule: { enabled: on } });
      await loadStatus();
      const s2 = S.status.schedule;
      toast(on ? `Day/night on: it's ${s2.period}, showing the ${s2.period === "day" ? "Day" : "Night"} folder` + (s2.sunrise ? ` (sunrise ${s2.sunrise}, sunset ${s2.sunset})` : "") : "Day/night off: the slideshow uses all wallpapers");
    } catch (e) { fail(e); }
  });
  const step = async (back) => {
    try { const r = await api("/api/slideshow/next", { back }); toast((back ? "Back to " : "Now showing ") + r.images.join(", ")); await loadStatus(); loadLibrary(); } catch (e) { fail(e); }
  };
  $("#btnSlideNext").addEventListener("click", () => step(false));
  $("#btnSlidePrev").addEventListener("click", () => step(true));
  $("#btnSlideToggle").addEventListener("click", async () => {
    try { await api("/api/slideshow", { enabled: !S.status.slideshow.enabled }); await loadStatus(); } catch (e) { fail(e); }
  });
  const showCustom = (on) => {
    $("#everyCustom").classList.toggle("hidden", !on);
    $("#slideEvery").classList.toggle("hidden", on);
    if (on) {
      // Start from the current interval in the biggest unit it divides into.
      const m = (S.status && S.status.slideshow.intervalMin) || 30;
      const u = m % 1440 === 0 ? 1440 : m % 60 === 0 ? 60 : 1;
      $("#everyUnit").value = String(u); $("#everyNum").value = m / u;
      $("#everyNum").focus(); $("#everyNum").select();
    }
  };
  const setCustom = () => {
    const n = parseFloat($("#everyNum").value), u = parseInt($("#everyUnit").value, 10);
    const m = Math.round(n * u);
    if (!isFinite(m) || m < 1 || m > 30 * 1440) { toast("Pick 1 minute to 30 days", true); return; }
    api("/api/slideshow", { intervalMin: m }).then(async () => { showCustom(false); await loadStatus(); toast("Changes every " + fmtMinutes(m)); }).catch(fail);
  };
  $("#slideEvery").addEventListener("change", (e) => {
    if (e.target.value === "custom") { showCustom(true); return; }
    api("/api/slideshow", { intervalMin: parseInt(e.target.value, 10) }).then(loadStatus).catch(fail);
  });
  $("#everySet").addEventListener("click", setCustom);
  $("#everyNum").addEventListener("keydown", (e) => { if (e.key === "Enter") setCustom(); if (e.key === "Escape") { showCustom(false); showSlideshow(); } });
  $("#everyCancel").addEventListener("click", () => { showCustom(false); showSlideshow(); });
  $("#btnSlideOrder").addEventListener("click", () => {
    const shuffle = !$("#btnSlideOrder").classList.contains("on");
    api("/api/slideshow", { order: shuffle ? "shuffle" : "name" }).then(() => { toast(shuffle ? "Shuffle on" : "Shuffle off: playing by file name"); return loadStatus(); }).catch(fail);
  });
  $("#btnSlideMore").addEventListener("click", () => $("#slideMenu").classList.toggle("hidden"));
  document.addEventListener("mousedown", (e) => { if (!e.target.closest(".morewrap")) $("#slideMenu").classList.add("hidden"); });
  $$("input[name=smode]").forEach((r) => r.addEventListener("change", () => api("/api/slideshow", { mode: r.value }).then(() => { toast("Saved"); return loadStatus(); }).catch(fail)));
  $("#slideSource").addEventListener("change", (e) => api("/api/slideshow", { source: e.target.value }).then(async () => {
    await loadStatus();
    toast(`Slideshow plays ${e.target.value ? e.target.options[e.target.selectedIndex].text : "all wallpapers"}` + (S.status.slideshow.enabled ? "" : ". Press Play to start, or ⏭ for the next one now."));
  }).catch(fail));
  $("#libGrid").addEventListener("click", async (e) => {
    const rb = e.target.closest("[data-rate]");
    if (rb) {
      const im = S.lib[+rb.dataset.i];
      const rating = im.rating === rb.dataset.rate ? "none" : rb.dataset.rate;
      try {
        const r = await api("/api/rate", { image: im.path, rating });
        toast(rating === "fav" ? `${im.name} is a favourite` : rating === "ban" ? `${im.name} won't show in the slideshow` + (r.skipped ? "; moved on to the next one" : "") : `${im.name}: rating cleared`);
        await loadLibrary(); if (r.skipped) loadStatus();
      } catch (err) { fail(err); }
      return;
    }
    const rmb = e.target.closest("[data-remove]");
    if (rmb) {
      const im = S.lib[+rmb.dataset.remove];
      if (!confirm(`Remove ${im.name} from the library?\n\nThe file goes to the Recycle Bin (you can restore it from there), and Wallhaven sync and your sources won't download it again.`)) return;
      try {
        const r = await api("/api/remove", { image: im.name });
        toast(`${im.name} moved to the Recycle Bin` + (r.wasOnScreen ? ". It stays on screen until the next wallpaper." : ""));
        await loadLibrary();
      } catch (err) { fail(err); }
      return;
    }
    const fb = e.target.closest("[data-fmenu]");
    if (fb) {
      const m = $("#folderMenu");
      if (!m.classList.contains("hidden") && m.dataset.i === fb.dataset.fmenu) closeFolderMenu(); else openFolderMenu(+fb.dataset.fmenu, fb);
      return;
    }
    const el = e.target.closest("[data-i]");
    if (!el) return;
    const im = S.lib[+el.dataset.i];
    const act = el.dataset.act || "show";
    if (act === "show") { S.img = im.path; switchTab("layout"); render(); toast("Showing " + im.name + ". Save & apply to set it."); }
    if (act === "apply") {
      try {
        el.disabled = true;
        const mode = $("#libMode").value;
        const res = await api("/api/apply", { images: [im.path], mode });
        S.img = null;
        toast(`Applied ${im.name} in ${res.totalMs} ms`);
        await refresh(); loadLibrary();
      } catch (e2) { fail(e2); } finally { el.disabled = false; }
    }
  });
}

// ---- desktops -----------------------------------------------------------------------
async function loadDesktops() {
  try {
    if (!S.lib.length) { const r = await api("/api/wallpapers"); S.lib = r.images || []; }
    const d = await api("/api/desktops");
    $$("input[name=dmode]").forEach((r) => (r.checked = r.value === d.mode));
    const opts = (sel) => `<option value="">(last applied wallpaper)</option>` + S.lib.map((im) => `<option value="${esc(im.path)}" ${im.path === sel ? "selected" : ""}>${esc(im.name)}</option>`).join("");
    $("#desktopList").innerHTML = `<div class="dlist">` + d.desktops.map((dk) => `
      <div class="d"><span>Desktop ${dk.number}${dk.name ? " · " + esc(dk.name) : ""} ${dk.current ? '<span class="cur">current</span>' : ""}</span>
      <select data-id="${esc(dk.id)}">${opts((d.assign || {})[dk.id])}</select></div>`).join("") + `</div>`;
    $("#panoramaSelect").innerHTML = S.lib.map((im) => `<option value="${esc(im.path)}" ${im.path === d.panorama ? "selected" : ""}>${esc(im.name)} (${im.width}×${im.height})</option>`).join("");
    showDesktopMode(d.mode);
  } catch (e) { fail(e); }
}

function showDesktopMode(mode) {
  $("#desktopList").classList.toggle("hidden", mode !== "individual");
  $("#panoramaPick").classList.toggle("hidden", mode !== "panorama");
}

function initDesktops() {
  $$("input[name=dmode]").forEach((r) => r.addEventListener("change", () => showDesktopMode(r.value)));
  $("#btnDesktopsSave").addEventListener("click", async () => {
    const mode = ($("input[name=dmode]:checked") || {}).value || "same";
    const assign = {};
    $$("#desktopList select").forEach((s) => { if (s.value) assign[s.dataset.id] = s.value; });
    const body = { mode, assign };
    if (mode === "panorama") body.panorama = $("#panoramaSelect").value;
    try {
      $("#desktopsMsg").textContent = "Rendering…";
      await api("/api/desktops", body);
      $("#desktopsMsg").textContent = mode === "same" ? "Saved." : "Saved. Other desktops are being pre-rendered in the background.";
      await refresh();
    } catch (e) { $("#desktopsMsg").textContent = ""; fail(e); }
  });
}

// ---- settings -----------------------------------------------------------------------
function initSettings() {
  $("#btnDirSave").addEventListener("click", () => api("/api/config", { wallpaperDir: $("#inpDir").value.trim() }).then(() => { toast("Folder saved"); loadLibrary(); }).catch(fail));
  $("#chkAuto").addEventListener("change", (e) => api("/api/config", { autoSwitch: e.target.checked }).then(() => toast("Saved")).catch(fail));
  const act = async (action) => {
    const name = $("#inpLayoutName").value.trim();
    try {
      if (action === "delete") {
        if (!confirm(`Delete layout "${S.view}"?`)) return;
        await api("/api/layout/action", { action, name: S.view });
        S.view = "";
      } else {
        if (!name) { toast("Type a layout name first", true); return; }
        await api("/api/layout/action", { action, name, from: S.view });
        S.view = name;
      }
      $("#inpLayoutName").value = "";
      toast("Done"); await refresh();
    } catch (e) { fail(e); }
  };
  $("#btnLayoutCopy").addEventListener("click", () => act("copy"));
  $("#btnLayoutRename").addEventListener("click", () => act("rename"));
  $("#btnLayoutDelete").addEventListener("click", () => act("delete"));
  $("#btnImportSP").addEventListener("click", () => api("/api/layout/import-superpaper", { layout: S.view }).then(() => { toast("Imported"); refresh(); }).catch(fail));
}

// ---- wallhaven ----------------------------------------------------------------------
const WH = { page: 1, last: 1, seed: "", results: [], fit: null };

async function whSearch(more = false) {
  WH.page = more ? WH.page + 1 : 1;
  const p = new URLSearchParams({ q: $("#whQ").value.trim(), sorting: $("#whSort").value, categories: $("#whCat").value, page: WH.page });
  if ($("#whSort").value === "toplist") p.set("topRange", $("#whRange").value);
  if ($("#whSort").value === "random" && more && WH.seed) p.set("seed", WH.seed);
  if ($("#whRatio").value) p.set("ratios", $("#whRatio").value);
  if ($("#whMin").value.trim()) p.set("atleast", $("#whMin").value.trim());
  $("#whInfo").textContent = "Searching…";
  try {
    const r = await api("/api/wallhaven/search?" + p);
    WH.last = r.lastPage; WH.seed = r.seed || "";
    WH.results = more ? WH.results.concat(r.results || []) : r.results || [];
    whFit(r.layout);
    $("#whInfo").textContent = `${r.total} results` + (r.total ? ` · page ${r.page} of ${r.lastPage}` : "") + " · safe for work only";
    whRender();
    $("#whMore").classList.toggle("hidden", WH.page >= WH.last);
  } catch (e) { $("#whInfo").textContent = e.message; }
}

function whRender() {
  $("#whGrid").innerHTML = WH.results.map((w, i) => `
      <div class="card wh">
        <img loading="lazy" referrerpolicy="no-referrer" src="${esc(w.thumbs.small)}" title="${esc(w.url)}">
        <div class="meta"><b>${esc(w.resolution)}</b><span class="badge">${esc(w.ratio)} · ${(w.file_size / 1048576).toFixed(1)} MB · ${esc(w.file_type.replace("image/", ""))} · ♥ ${w.favorites}</span></div>
        <div class="acts"><a href="${esc(w.url)}" target="_blank" rel="noreferrer"><button>Open</button></a><button data-wh="${i}">Download</button><button data-wh="${i}" data-apply="1" class="primary">Download &amp; apply</button></div>
      </div>`).join("") || `<p class="muted pad">Nothing found. Try fewer filters.</p>`;
}

// Suggest filters that fit the layout (once, without overwriting the user's choices).
function whFit(fit) {
  if (!fit || WH.fit) return;
  WH.fit = fit;
  const ratios = ["", "16x9", "16x10", "21x9", "32x9", "48x9", "4x3", "3x2"];
  if (!ratios.includes(fit.nearest)) ratios.push(fit.nearest);
  $("#whRatio").innerHTML = ratios.map((r) => `<option value="${r}" ${r === fit.nearest ? "selected" : ""}>${r ? r + (r === fit.nearest ? " (fits layout)" : "") : "any ratio"}</option>`).join("");
  if (!$("#whMin").value) $("#whMin").value = fit.atleast;
  $("#whMin").title = `Your layout is ${fit.widthMM} × ${fit.heightMM} mm (ratio ${fit.ratio}). ${fit.atleast} keeps the lowest density monitor sharp.`;
}

function whInit() {
  whFit(S.status && S.status.layoutFit);
  if (!$("#whRatio").options.length) $("#whRatio").innerHTML = `<option value="">any ratio</option>`;
}

let WHC = null; // collection being shown {id, page, last}

async function whCollections() {
  try {
    const r = await api("/api/wallhaven/collections");
    $("#whColl").innerHTML = `<option value="">My collections…</option>` +
      (r.collections || []).map((c) => `<option value="${c.id}">${esc(c.label)} (${c.count})${c.public ? "" : " · private"}</option>`).join("");
  } catch (e) { $("#whColl").innerHTML = `<option value="">${esc(e.message.includes("Settings") ? "Set your account in Settings" : "Collections unavailable")}</option>`; }
}

async function whShowCollection(id, more = false) {
  WHC = { id, page: more && WHC ? WHC.page + 1 : 1 };
  $("#whInfo").textContent = "Loading collection…";
  try {
    const r = await api(`/api/wallhaven/collection?id=${id}&page=${WHC.page}`);
    WHC.last = r.lastPage;
    WH.results = more ? WH.results.concat(r.results || []) : r.results || [];
    $("#whInfo").textContent = `${r.total} wallpapers in this collection` + (r.total ? ` · page ${r.page} of ${r.lastPage}` : "") + " · safe for work only";
    whRender();
    $("#whMore").classList.toggle("hidden", WHC.page >= WHC.last);
  } catch (e) { $("#whInfo").textContent = e.message; }
}

function initWallhaven() {
  $("#whColl").addEventListener("change", (e) => { if (e.target.value) whShowCollection(+e.target.value); });
  $("#whForm").addEventListener("submit", (e) => { e.preventDefault(); WHC = null; whSearch(false); });
  $("#whMore").addEventListener("click", () => (WHC ? whShowCollection(WHC.id, true) : whSearch(true)));
  $("#whGrid").addEventListener("click", async (e) => {
    const b = e.target.closest("[data-wh]");
    if (!b) return;
    const w = WH.results[+b.dataset.wh];
    const apply = !!b.dataset.apply;
    b.disabled = true; const t = b.textContent; b.textContent = apply ? "Applying…" : "Downloading…";
    try {
      const r = await api("/api/wallhaven/download", { id: w.id, apply });
      toast(`Saved ${r.path.split(/[\\/]/).pop()} (${r.resolution})` + (apply ? " and applied" : ""));
      b.textContent = apply ? "Applied" : "Saved";
      if (apply) refresh();
    } catch (e2) { fail(e2); b.textContent = t; b.disabled = false; }
  });
}

// ---- settings: groups, startup, hotkeys -----------------------------------------------
function loadSettingsExtra() {
  const st = S.status;
  if (!st) return;
  $("#chkAutostart").checked = !!st.autostart;
  $("#chkTray").checked = !!st.tray;
  if (document.activeElement !== $("#inpWhUser")) $("#inpWhUser").value = st.wallhavenUser || "";
  $("#inpWhKey").placeholder = st.wallhavenKey ? "API key saved (paste to replace)" : "API key";
  const ws = st.wallhavenSync || {};
  $("#chkWhSync").checked = !!ws.enabled;
  if (document.activeElement !== $("#inpWhSync")) $("#inpWhSync").value = ws.collection || "Spanwall";
  loadSettingsNew(st);
  $("#whSyncInfo").textContent = ws.lastRun ? `last sync ${new Date(ws.lastRun).toLocaleString()}: ${ws.lastResult}` : "not synced yet";
  // Groups: a select per monitor (group 1..n or own image).
  const groupOf = {};
  (st.groups || []).forEach((g, gi) => { if (g.length > 1) g.forEach((n) => (groupOf[n] = gi + 1)); });
  const n = S.displays.length;
  $("#groupList").innerHTML = `<div class="glist">` + S.displays.map((d, i) => `<span>${i + 1} · ${esc(d.name)}</span>
    <select data-n="${i + 1}"><option value="0">Own image</option>${Array.from({ length: n }, (_, k) => `<option value="${k + 1}" ${groupOf[i + 1] === k + 1 ? "selected" : ""}>Group ${k + 1}</option>`).join("")}</select>`).join("") + `</div>`;
  if (!$("#hotkeyList").contains(document.activeElement)) {
    const names = { next: "Next wallpaper", previous: "Previous wallpaper", pause: "Start or pause slideshow", open: "Open this page", favorite: "Favourite this wallpaper", ban: "Never show this wallpaper" };
    $("#hotkeyList").innerHTML = Object.entries(names).map(([a, label]) => `<span>${label}</span><input data-hk="${a}" value="${esc((st.hotkeys || {})[a] || "")}" spellcheck="false">`).join("");
  }
}

function initSettingsExtra() {
  $("#btnGroupsSave").addEventListener("click", async () => {
    const groups = {};
    $$("#groupList select").forEach((sel) => { const g = +sel.value; if (g) (groups[g] = groups[g] || []).push(sel.dataset.n); });
    const list = Object.values(groups);
    if (list.some((g) => g.length < 2)) { toast("A group needs at least two monitors; use Own image for one", true); return; }
    try { await api("/api/config", { groups: list }); toast("Span groups saved"); await loadStatus(); loadSettingsExtra(); } catch (e) { fail(e); }
  });
  $("#btnGroupsClear").addEventListener("click", () => api("/api/config", { groups: [] }).then(async () => { toast("Spanning everything"); await loadStatus(); loadSettingsExtra(); }).catch(fail));
  $("#chkAutostart").addEventListener("change", (e) => api("/api/config", { autostart: e.target.checked }).then(() => toast(e.target.checked ? "Starts at sign-in" : "Won't start at sign-in")).catch(fail));
  $("#btnWhSave").addEventListener("click", async () => {
    const body = { wallhavenUser: $("#inpWhUser").value.trim() };
    const k = $("#inpWhKey").value.trim();
    if (k) body.wallhavenKey = k;
    try { await api("/api/config", body); $("#inpWhKey").value = ""; toast("Wallhaven account saved"); await loadStatus(); loadSettingsExtra(); } catch (e) { fail(e); }
  });
  $("#chkWhSync").addEventListener("change", (e) => api("/api/config", { wallhavenSync: e.target.checked, wallhavenSyncCollection: $("#inpWhSync").value.trim() }).then(() => toast(e.target.checked ? "Auto-download on; syncing now" : "Auto-download off")).catch(fail));
  $("#inpWhSync").addEventListener("change", (e) => api("/api/config", { wallhavenSyncCollection: e.target.value.trim() }).then(() => toast("Collection saved")).catch(fail));
  $("#btnWhSyncNow").addEventListener("click", async (e) => {
    e.target.disabled = true; $("#whSyncInfo").textContent = "syncing…";
    try { const r = await api("/api/wallhaven/sync", {}); toast(`${r.downloaded ? r.downloaded.length : 0} new from ${r.collection}`); } catch (err) { fail(err); }
    e.target.disabled = false; await loadStatus(); loadSettingsExtra();
  });
  $("#btnWhKeyRemove").addEventListener("click", () => api("/api/config", { wallhavenKey: "" }).then(async () => { toast("Key removed"); await loadStatus(); loadSettingsExtra(); }).catch(fail));
  $("#chkTray").addEventListener("change", (e) => api("/api/config", { tray: e.target.checked }).then(() => toast("Saved")).catch(fail));
  $("#btnHotkeysSave").addEventListener("click", async () => {
    const hk = {};
    $$("#hotkeyList input").forEach((i) => (hk[i.dataset.hk] = i.value.trim().toLowerCase()));
    try { await api("/api/config", { hotkeys: hk }); toast("Hotkeys saved"); await loadStatus(); } catch (e) { fail(e); }
  });
}

// ---- settings: setups, smart framing, look, day and night, sources ------------------
function loadSettingsNew(st) {
  const ps = (st.profiles || []).slice().sort((a, b) => (b.current - a.current) || b.count - a.count);
  $("#profileList").innerHTML = ps.length ? `<div class="plist">` + ps.map((p) => `
    <span>${esc((p.monitors || []).join(" + ") || p.count + " monitors")} ${p.current ? '<span class="cur">connected now</span>' : ""}</span>
    <span class="muted">layout ${esc(p.layout)} · ${esc(p.mode)} · ${esc((p.images || []).join(", "))}</span>
    <button data-forget="${esc(p.key)}">Forget</button>`).join("") + `</div>` : `<p class="muted">None yet. Apply a wallpaper and this setup is remembered.</p>`;
  $("#chkSmart").checked = !!st.smartFrame;
  const sy = st.sync || {};
  $("#chkAccent").checked = !!sy.accent;
  $("#chkLock").checked = !!sy.lockScreen;
  $("#lookInfo").textContent = sy.last ? "Last: " + sy.last : (sy.accent || sy.lockScreen ? "Takes effect with the next wallpaper." : "");
  const sc = st.schedule || {};
  $("#chkSched").checked = !!sc.enabled;
  if (!$("#sunPick").contains(document.activeElement) && document.activeElement !== $("#schedMode")) {
    $("#schedMode").value = sc.mode || "sun";
    if (sc.lat || sc.lon) { $("#inpLat").value = sc.lat; $("#inpLon").value = sc.lon; }
  }
  $("#sunPick").classList.toggle("hidden", $("#schedMode").value !== "sun");
  $("#schedInfo").textContent = `${sc.enabled ? "On" : "Off"} · it's ${sc.period || "?"} now` + (sc.sunrise ? ` · sunrise ${sc.sunrise}, sunset ${sc.sunset}` : "") +
    (sc.mode === "theme" ? " (Windows is in " + (sc.period === "day" ? "light" : "dark") + " mode)" : "") + ` · Day folder ${sc.dayCount || 0}, Night folder ${sc.nightCount || 0}`;
  const so = st.sources || {};
  if (!$("#tab-settings").contains(document.activeElement) || document.activeElement.type === "checkbox") {
    $$("[data-src]").forEach((c) => (c.checked = !!so[c.dataset.src]));
    $("#inpSubs").value = so.subreddits || "";
    $("#inpQuery").value = so.query || "";
    $("#inpMinW").value = so.minWidth || 2560;
    $("#inpNasaKey").placeholder = so.nasaKey ? "NASA key saved (paste to replace)" : "NASA API key (optional)";
    $("#inpUnsplashKey").placeholder = so.unsplashKey ? "Unsplash key saved (paste to replace)" : "Unsplash access key";
  }
  $("#srcInfo").textContent = so.lastRun ? `last check ${new Date(so.lastRun).toLocaleString()}: ${so.lastResult}` : "not checked yet";
}

function initSettingsNew() {
  const save = (body, msg) => api("/api/config", body).then(async () => { toast(msg); await loadStatus(); loadSettingsExtra(); }).catch(fail);
  $("#profileList").addEventListener("click", async (e) => {
    const b = e.target.closest("[data-forget]");
    if (!b) return;
    try { await api("/api/profiles", { forget: b.dataset.forget }); toast("Setup forgotten"); await loadStatus(); loadSettingsExtra(); } catch (err) { fail(err); }
  });
  $("#chkSmart").addEventListener("change", (e) => save({ smartFrame: e.target.checked }, e.target.checked ? "Smart framing on (next wallpaper)" : "Smart framing off"));
  $("#chkAccent").addEventListener("change", (e) => save({ accent: e.target.checked }, e.target.checked ? "Accent colour follows the wallpaper from the next change" : "Accent colour left alone"));
  $("#chkLock").addEventListener("change", (e) => save({ lockScreen: e.target.checked }, e.target.checked ? "Lock screen follows the wallpaper from the next change" : "Lock screen left alone"));
  $("#schedMode").addEventListener("change", () => $("#sunPick").classList.toggle("hidden", $("#schedMode").value !== "sun"));
  $("#btnGeo").addEventListener("click", () => {
    if (!navigator.geolocation) { toast("This browser can't share a location; type it in", true); return; }
    navigator.geolocation.getCurrentPosition((p) => {
      $("#inpLat").value = p.coords.latitude.toFixed(3); $("#inpLon").value = p.coords.longitude.toFixed(3);
      toast("Location filled in. Save to use it.");
    }, (err) => toast("Location unavailable: " + err.message, true), { timeout: 15000 });
  });
  $("#btnSchedSave").addEventListener("click", () => {
    const mode = $("#schedMode").value;
    const sc = { mode };
    if (mode === "sun") { sc.lat = parseFloat($("#inpLat").value) || 0; sc.lon = parseFloat($("#inpLon").value) || 0; }
    save({ schedule: sc }, "Saved");
  });
  $("#chkSched").addEventListener("change", (e) => save({ schedule: { enabled: e.target.checked } }, e.target.checked ? "Day and night on" : "Day and night off"));
  $("#btnSrcSave").addEventListener("click", async () => {
    const sources = { subreddits: $("#inpSubs").value, query: $("#inpQuery").value, minWidth: parseInt($("#inpMinW").value, 10) || 2560 };
    $$("[data-src]").forEach((c) => (sources[c.dataset.src] = c.checked));
    const body = { sources };
    if ($("#inpNasaKey").value.trim()) body.nasaKey = $("#inpNasaKey").value.trim();
    if ($("#inpUnsplashKey").value.trim()) body.unsplashKey = $("#inpUnsplashKey").value.trim();
    if (sources.unsplash && !body.unsplashKey && !(S.status.sources || {}).unsplashKey) { toast("Unsplash needs an access key (free at unsplash.com/developers)", true); return; }
    await save(body, "Sources saved; checking them now");
    $("#inpNasaKey").value = ""; $("#inpUnsplashKey").value = "";
  });
  $("#btnSrcNow").addEventListener("click", async (e) => {
    e.target.disabled = true; $("#srcInfo").textContent = "checking…";
    try { const r = await api("/api/sources/sync", {}); toast(`${r.downloaded ? r.downloaded.length : 0} new from ${r.checked} checked` + (r.errors && r.errors.length ? ` · ${r.errors[0]}` : ""), !!(r.errors && r.errors.length && !(r.downloaded || []).length)); } catch (err) { fail(err); }
    e.target.disabled = false; await loadStatus(); loadSettingsExtra();
  });
}

// ---- tabs, events, start ------------------------------------------------------------
function switchTab(name) {
  $$("nav button").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  $$(".tab").forEach((t) => t.classList.toggle("active", t.id === "tab-" + name));
  if (name === "layout") { fit(); render(); }
  if (name === "library") loadLibrary();
  if (name === "desktops") loadDesktops();
  if (name === "wallhaven") { whInit(); whCollections(); if (!WH.results.length) whSearch(false); }
  if (name === "settings") loadSettingsExtra();
  liveTab(name === "layout");
}

async function refresh() {
  await loadStatus();
  if (!S.dragging) await loadLayout(true);
}

function listen() {
  const es = new EventSource("/api/events");
  es.addEventListener("changed", async (e) => {
    const what = JSON.parse(e.data);
    try {
      await loadStatus();
      if (what === "proposal") { const r = await api("/api/align"); if (r.proposal) { switchTab("layout"); showProposal(r.proposal); toast("A photo calibration result is ready"); } }
      if (["layout", "config", "monitors", "wallpaper"].includes(what) && !S.dragging && !isDirty()) await loadLayout();
      if (["library", "wallpaper"].includes(what) && $("#tab-library").classList.contains("active")) loadLibrary();
      if (what === "live") liveChanged();
      if (what === "config" && $("#tab-settings").classList.contains("active")) loadSettingsExtra();
      else render();
    } catch { /* app restarting */ }
  });
  es.onerror = () => { $("#status").textContent = "reconnecting…"; };
  es.addEventListener("hello", () => refresh().catch(() => {}));
}

window.addEventListener("beforeunload", (e) => {
  if (S.calibrating) navigator.sendBeacon && fetch("/api/calibrate", { method: "POST", body: JSON.stringify({ stop: true }), headers: { "Content-Type": "application/json" }, keepalive: true });
  if (isDirty()) { e.preventDefault(); e.returnValue = ""; }
});

$$("nav button").forEach((b) => b.addEventListener("click", () => switchTab(b.dataset.tab)));
try { $("#chkPersp").checked = localStorage.getItem("spanwall.persp") !== "0"; } catch (e) { /* default on */ }
$("#chkPersp").addEventListener("change", (e) => { try { localStorage.setItem("spanwall.persp", e.target.checked ? "1" : "0"); } catch (err) { /* ignore */ } render(); });
initCanvas(); initToolbar(); initWelcome(); initPhoto(); initLibrary(); initWallhaven(); initDesktops(); initSettings(); initSettingsExtra(); initSettingsNew();
refresh().then(showWelcomeIfNeeded).catch(fail);
listen();

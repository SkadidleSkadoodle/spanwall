// Live (animated ASCII) wallpaper in the editor. "Live" in the Layout tab's
// dropdown is the layout editor with the monitors locked: the scene's whole
// canvas runs behind them (#liveBg, the page in editor mode), framed with the
// image tools, and framing changes show on the desktop as you drag. The side
// panel has the scene's details, its settings, its look (colour theme,
// character set), day and night scenes, and a snapshot picker. The Library
// has a Live wallpapers folder.
const LIVE_VALUE = "__live";
const LV = { st: null, scenes: [], on: false, code: null, snapQ: [], snapping: null, moveWas: false,
  schema: {}, vals: {}, err: {}, bgKey: "", viewPending: false, pushTimer: 0, saveTimer: 0, lastPush: "" };

const THEME_NAMES = [["", "Scene's own colours"], ["phosphor", "Green phosphor"], ["amber", "Amber"], ["ice", "Ice"], ["mono", "Mono"],
  ["synthwave", "Synthwave"], ["ocean", "Ocean"], ["blood", "Blood"], ["custom", "Custom…"]];
const CHARSET_NAMES = [["", "Scene's own characters"], ["ascii", "ASCII"], ["blocks", "Blocks ░▒▓█"], ["braille", "Braille ⣿"],
  ["katakana", "Katakana ｱ"], ["binary", "Binary 01"], ["dots", "Dots ·•●"], ["lines", "Lines ─┼╬"]];

const liveOption = () => `<option value="${LIVE_VALUE}" ${LV.on ? "selected" : ""}>Live</option>`;
const liveScene = (name) => LV.scenes.find((s) => s.name === name) || null;
const liveCur = () => (LV.st ? liveScene(LV.st.scene) : null);
const bgFrame = () => $("#liveBg");
const tellBg = (msg) => { const f = bgFrame(); if (f && f.contentWindow) f.contentWindow.postMessage(msg, location.origin); };

async function liveLoadScenes() {
  try { LV.st = await api("/api/live"); LV.scenes = LV.st.scenes || []; } catch (e) { LV.scenes = []; }
  return LV.st;
}

// ---- the Live view ---------------------------------------------------------------------

function liveViews() {
  const name = LV.st && LV.st.scene;
  return name ? [{ key: "live:" + name, live: true, scene: name, img: null, ids: null, b: bbox() }] : [];
}
const liveHref = () => "";

async function liveLoadFrame(v) {
  if (S.framesLoading[v.key]) return;
  S.framesLoading[v.key] = true;
  try {
    const r = await api("/api/live/frame?scene=" + encodeURIComponent(v.scene));
    const f = r.frame;
    S.frames[v.key] = { live: true, scene: v.scene, w: r.width, h: r.height, def: r.default.zoom,
      zoom: f.zoom, dx: f.dx || 0, dy: f.dy || 0, flipX: !!f.flipX, flipY: !!f.flipY, rotate: f.rotate || 0 };
    S.framesSaved[v.key] = frameVals(S.frames[v.key]);
    LV.lastPush = JSON.stringify(S.framesSaved[v.key]);
  } catch (e) { S.framesLoading[v.key] = false; return; }
  fit(); render();
}

// liveFitBox grows the monitors' box to the scene's canvas, turned as framed.
function liveFitBox(b) {
  const v = liveViews()[0], f = v && S.frames[v.key];
  if (!f) return b;
  const r = imgRect(b, f), cx = r.x + r.w / 2, cy = r.y + r.h / 2;
  let x0 = b.x0, y0 = b.y0, x1 = b.x1, y1 = b.y1;
  for (const [x, y] of [[r.x, r.y], [r.x + r.w, r.y], [r.x, r.y + r.h], [r.x + r.w, r.y + r.h]]) {
    const [px, py] = turnPt(x, y, cx, cy, f.rotate || 0);
    x0 = Math.min(x0, px); y0 = Math.min(y0, py); x1 = Math.max(x1, px); y1 = Math.max(y1, py);
  }
  return { x0, y0, x1, y1, w: x1 - x0, h: y1 - y0 };
}

function liveForgetFrames() {
  for (const k of Object.keys(S.frames)) if (k.startsWith("live:")) { delete S.frames[k]; delete S.framesSaved[k]; S.framesLoading[k] = false; }
}

// The background frame: the page in editor mode, for the current scene.
function liveSetBg(scene) {
  const wrap = $("#canvasWrap");
  let f = bgFrame();
  if (!scene || !$("#tab-layout").classList.contains("active")) { if (f) f.remove(); LV.bgKey = ""; return; }
  const s = liveScene(scene), key = scene + "@" + (s ? s.version : "");
  if (f && key === LV.bgKey) return;
  LV.bgKey = key;
  if (!f) { f = document.createElement("iframe"); f.id = "liveBg"; f.title = "Live scene"; wrap.insertBefore(f, wrap.firstChild); }
  f.src = `/live/?editor=1&scene=${encodeURIComponent(scene)}`;
}

// After each editor render: the background follows the view and the framing
// (next animation frame), and the desktop follows the framing (throttled).
function liveAfterRender() {
  if (!LV.viewPending) {
    LV.viewPending = true;
    requestAnimationFrame(() => { LV.viewPending = false; liveSendView(); });
  }
  const f = S.frames["live:" + (LV.st && LV.st.scene)];
  if (f && JSON.stringify(frameVals(f)) !== LV.lastPush && !LV.pushTimer) LV.pushTimer = setTimeout(() => { LV.pushTimer = 0; livePushFrame(); }, 120);
}

function liveSendView() {
  const f = S.frames["live:" + (LV.st && LV.st.scene)];
  tellBg({ type: "view", vb: S.vb, frame: f ? frameVals(f) : null });
}

function livePushFrame() {
  const f = S.frames["live:" + (LV.st && LV.st.scene)];
  if (!f) return;
  const vals = frameVals(f), key = JSON.stringify(vals);
  if (key === LV.lastPush) return;
  LV.lastPush = key;
  api("/api/live/frame", Object.assign({ scene: f.scene, preview: true }, vals)).catch(() => {});
}

async function liveEnter() {
  LV.on = true;
  try { localStorage.setItem("spanwall.liveView", "1"); } catch (e) { /* ignore */ }
  $("#tab-layout").classList.add("live-mode");
  LV.moveWas = S.moveImg;
  S.moveImg = true; // the monitors are locked; dragging moves the scene
  $("#canvas").classList.add("moving-img");
  $("#layoutSelect").value = LIVE_VALUE;
  $("#btnResetImg").textContent = "Reset framing";
  await liveLoadScenes();
  // The animation follows the active layout, so frame against that one.
  if (S.status && S.view !== S.status.activeLayout && !isDirty()) { S.view = S.status.activeLayout; S.cur = {}; await loadLayout(); }
  liveRefresh();
}

function liveLeave() {
  // unsaved framing: put the desktop back as saved
  const f = S.frames["live:" + (LV.st && LV.st.scene)], saved = f && S.framesSaved["live:" + f.scene];
  if (f && saved && frameDiff(f, saved)) { Object.assign(f, saved); livePushFrame(); }
  LV.on = false;
  try { localStorage.removeItem("spanwall.liveView"); } catch (e) { /* ignore */ }
  $("#tab-layout").classList.remove("live-mode");
  $("#liveCode").classList.add("hidden");
  liveForgetFrames();
  S.moveImg = LV.moveWas;
  $("#canvas").classList.toggle("moving-img", S.moveImg);
  $("#btnResetImg").textContent = "Reset image";
  liveSetBg("");
  $("#layoutSelect").value = S.view;
  fit(); render();
}

function liveTab(active) {
  if (!LV.on) return;
  if (active) liveRefresh();
  else liveSetBg("");
}

function liveRefresh() {
  const st = LV.st;
  if (!st) return;
  const tg = $("#liveToggle");
  tg.textContent = "Animated wallpaper: " + (st.enabled ? (st.running ? "on" : "starting…") : "off");
  tg.classList.toggle("on", st.enabled);
  $("#liveFps").value = String(st.fps || 0);
  $("#liveChar").value = st.charMM || "";
  $("#liveChar").placeholder = "auto";
  if (!LV.on) return;
  liveSetBg(st.scene);
  if (S.displays && S.displays.length) { fit(); render(); }
}

async function liveSet(body, msg) {
  await api("/api/live", body);
  if (msg) toast(msg);
  await liveLoadScenes();
  liveRefresh();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

async function livePick(name) {
  if (LV.st && name === LV.st.scene) return;
  if (frameDirty() && !confirm("Discard unsaved changes to this scene's framing?")) return;
  liveForgetFrames();
  await liveSet({ scene: name });
}

// ---- settings ---------------------------------------------------------------------------

// The scene's settings: its declared defaults, then what was saved, then edits.
function liveValues(name) {
  if (!LV.vals[name]) LV.vals[name] = Object.assign({}, (LV.st && LV.st.params && LV.st.params[name]) || {});
  return LV.vals[name];
}

function liveParamChanged(name) {
  const vals = liveValues(name);
  tellBg({ type: "params", params: vals }); // the editor's view at once
  clearTimeout(LV.saveTimer);
  LV.saveTimer = setTimeout(() => api("/api/live/params", { scene: name, params: vals }).catch(fail), 350); // saved, and on the desktop
}

function liveSettingsHTML(name) {
  const sc = LV.schema[name], vals = liveValues(name);
  const ctl = (k, d) => {
    const v = k in vals ? vals[k] : d.default, label = esc(d.label || k), id = `lp-${k}`;
    switch (d.type) {
      case "range": return `<label class="set" for="${id}"><span>${label}</span><b>${esc(v)}</b><input id="${id}" type="range" data-lp="${esc(k)}" min="${d.min ?? 0}" max="${d.max ?? 1}" step="${d.step ?? 0.01}" value="${esc(v)}"></label>`;
      case "color": return `<label class="set" for="${id}"><span>${label}</span><input id="${id}" type="color" data-lp="${esc(k)}" value="${esc(v)}"></label>`;
      case "toggle": return `<label class="set" for="${id}"><span>${label}</span><input id="${id}" type="checkbox" data-lp="${esc(k)}" ${v ? "checked" : ""}></label>`;
      case "select": return `<label class="set" for="${id}"><span>${label}</span><select id="${id}" data-lp="${esc(k)}">${(d.options || []).map((o) => `<option ${o === v ? "selected" : ""}>${esc(o)}</option>`).join("")}</select></label>`;
      default: return `<label class="set" for="${id}"><span>${label}</span><input id="${id}" type="text" data-lp="${esc(k)}" value="${esc(v ?? "")}" spellcheck="false"></label>`;
    }
  };
  const own = sc ? Object.entries(sc.params || {}).map(([k, d]) => ctl(k, d)).join("") : `<div class="muted">Loading…</div>`;
  const theme = vals._theme || "", cs = vals._charset || "";
  return `<h4>Settings</h4>${own || `<div class="muted">This scene has no settings of its own.</div>`}
    <h4>Look</h4>
    <label class="set"><span>Colours</span><select data-lp="_theme">${THEME_NAMES.map(([v, l]) => `<option value="${v}" ${v === theme ? "selected" : ""}>${l}</option>`).join("")}</select></label>
    ${theme === "custom" ? `<label class="set"><span>Dark</span><input type="color" data-lp="_tintA" value="${esc(vals._tintA || "#0a3d2a")}"></label>
      <label class="set"><span>Bright</span><input type="color" data-lp="_tintB" value="${esc(vals._tintB || "#c8ffe0")}"></label>` : ""}
    <label class="set"><span>Characters</span><select data-lp="_charset">${CHARSET_NAMES.map(([v, l]) => `<option value="${v}" ${v === cs ? "selected" : ""}>${l}</option>`).join("")}</select></label>
    <div class="row"><button data-lside="resetparams" title="Back to the scene's own settings and look">Reset settings</button></div>`;
}

function liveScheduleHTML() {
  const st = LV.st, sc = st.schedule || {}, opts = (sel) => `<option value="">(none)</option>` + LV.scenes.map((x) => `<option value="${esc(x.name)}" ${x.name === sel ? "selected" : ""}>${esc(x.title)}</option>`).join("");
  const now = st.period ? `It is ${st.period} now (${st.periodMode === "theme" ? "Windows light and dark mode" : "sunrise and sunset"}).` :
    "Set your location or choose light and dark mode in Settings > Day and night.";
  return `<h4>Day and night</h4>
    <label class="set"><span>Switch scenes by day and night</span><input type="checkbox" data-lsched="enabled" ${sc.enabled ? "checked" : ""}></label>
    <label class="set"><span>Day</span><select data-lsched="day">${opts(sc.day)}</select></label>
    <label class="set"><span>Night</span><select data-lsched="night">${opts(sc.night)}</select></label>
    <div class="note">${esc(now)}</div>`;
}

// ---- side panel ---------------------------------------------------------------------------

function liveRenderSide() {
  const side = $("#side"), st = LV.st;
  if (!st) { side.innerHTML = `<div class="empty">Loading…</div>`; return; }
  if (side.dataset.mode !== "live") {
    side.dataset.mode = "live"; side.dataset.for = ""; side.classList.add("live-side");
    side.innerHTML = `<div class="sidetabs"><button data-ltab="settings">Settings</button><button data-ltab="scenes">Scenes</button></div>
      <div data-lpane="settings"><div id="liveInfo"></div><div id="liveSettings"></div><div id="liveSched"></div></div>
      <div data-lpane="scenes"><div id="livePicks" class="picks"></div>
      <div class="note">Scenes are files in <span id="liveDirNote"></span>: edit them there, here, or ask Claude to write one. Favourites, bans and the slideshow are in Library &gt; Live wallpapers.</div></div>`;
  }
  liveShowTab();
  const s = liveCur() || { name: st.scene, title: st.scene };
  $("#liveDirNote").textContent = st.scenesDir;
  const f = S.frames["live:" + st.scene];
  const on = st.enabled && st.running;
  const fps = st.fps || s.fps || 30, ch = st.charMM || s.charMM || 4.2;
  const rel = f ? f.zoom / (f.def || 1.5) : 1; // 1 = the canvas at 1:1 on the monitors
  const fr = f ? imgRect(bbox(), f) : null;
  const p = st.page, err = LV.err[st.scene] || (on && p && p.error ? { message: p.error, line: p.line } : null);
  const kv = [
    ["Desktop", on ? "showing this scene" : st.enabled ? "starting…" : "animation off"],
    ["Frame rate", `${fps} fps` + (st.fps ? ` (scene's own ${s.fps || 30})` : " (scene's own)")],
    ["Characters", `${(ch * rel).toFixed(1)} mm tall on screen` + (Math.abs(rel - 1) > 0.005 ? ` (${ch} mm at 1:1)` : "")],
    f ? ["Canvas", `${Math.round(f.w)} × ${Math.round(f.h)} mm, ${(f.w / Math.max(1, bbox().w)).toFixed(1)}× your monitors`] : null,
    f ? ["Framing", `${rel.toFixed(2)}× zoom` + (fr ? `, moved ${Math.round(f.dx * fr.w)}, ${Math.round(f.dy * fr.h)} mm` : "") + (f.rotate ? `, turned ${f.rotate}°` : "") + (f.flipX || f.flipY ? ", mirrored" : "")] : null,
    on && p && p.fps !== undefined ? ["Running", `${p.fps} fps, ${p.ms} ms a frame, ${p.cols}×${p.rows} characters`] : null,
  ].filter(Boolean);
  $("#liveInfo").innerHTML = `<h3>${esc(s.title)}</h3>
    <div class="badges"><span class="badge">${s.user ? (s.builtin ? "edited built-in" : "yours") : "built in"}</span><span class="badge">${esc(s.file || s.name + ".js")}</span>${s.framed ? `<span class="badge">framed</span>` : ""}${on ? `<span class="badge on">on screen</span>` : ""}</div>
    <div class="id">${esc(s.description || "")}</div>
    ${err ? `<div class="err">Error${err.line ? ` at line ${err.line}` : ""}: ${esc(err.message)}</div>` : ""}
    <div class="kv">${kv.map(([k, v]) => `<span>${esc(k)}</span><span>${esc(v)}</span>`).join("")}</div>
    <div class="row rate"><button data-lrate="fav" data-n="${esc(s.name)}" class="${s.rating === "fav" ? "on" : ""}" title="Comes up more often in the live slideshow">★ Favourite</button>
      <button data-lrate="ban" data-n="${esc(s.name)}" class="${s.rating === "ban" ? "on" : ""}" title="Never shown by the live slideshow">Ban</button></div>
    <div class="row"><button data-lside="edit">Edit code</button><button data-lside="dup" title="Copy this scene under a new name to make it your own">Duplicate</button>${s.user ? `<button data-lside="del" class="danger" title="${s.builtin ? "Remove your edit and go back to the built-in scene" : "Delete this scene"}">${s.builtin ? "Revert" : "Delete"}</button>` : ""}${on ? "" : `<button data-lside="show" class="primary">Show on desktop</button>`}</div>
    <div class="note">Drag the scene to move it, drag a corner or scroll to zoom, the curved arrows or [ ] to turn, H and V to mirror; the desktop follows as you drag. Save &amp; apply to keep it.</div>`;
  const setKey = st.scene + JSON.stringify(LV.schema[st.scene] || null) + (liveValues(st.scene)._theme || "");
  const sets = $("#liveSettings");
  if (sets.dataset.key !== setKey && !sets.contains(document.activeElement)) { sets.dataset.key = setKey; sets.innerHTML = liveSettingsHTML(st.scene); }
  const schedKey = JSON.stringify([st.schedule, st.period, LV.scenes.map((x) => x.name + x.title)]);
  const sch = $("#liveSched");
  if (sch.dataset.key !== schedKey && !sch.contains(document.activeElement)) { sch.dataset.key = schedKey; sch.innerHTML = liveScheduleHTML(); }
  const key = LV.scenes.map((x) => x.name + x.version + x.thumb + x.rating).join() + st.scene;
  const picks = $("#livePicks");
  if (picks.dataset.key !== key) {
    picks.dataset.key = key;
    picks.innerHTML = LV.scenes.map((x) => `<button class="pick ${x.name === st.scene ? "sel" : ""} ${x.rating === "ban" ? "banned" : ""}" data-lpick="${esc(x.name)}" title="${esc(x.description || "")}">` +
      (x.thumb ? `<img loading="lazy" src="/api/live/thumb?name=${encodeURIComponent(x.name)}&v=${encodeURIComponent(x.version)}">` : `<div class="pending">snapshot…</div>`) +
      `<span>${x.rating === "fav" ? "★ " : ""}${esc(x.title)}</span></button>`).join("");
    for (const x of LV.scenes) if (!x.thumb) liveSnap(x.name);
  }
}

function liveShowTab() {
  let t = "settings";
  try { t = localStorage.getItem("spanwall.liveTab") || t; } catch (e) { /* ignore */ }
  for (const b of $$("[data-ltab]")) b.classList.toggle("active", b.dataset.ltab === t);
  for (const p of $$("[data-lpane]")) p.classList.toggle("hidden", p.dataset.lpane !== t);
}

$("#side").addEventListener("click", async (e) => {
  if (!LV.on) return;
  const tb = e.target.closest("[data-ltab]");
  if (tb) { try { localStorage.setItem("spanwall.liveTab", tb.dataset.ltab); } catch (err) { /* ignore */ } liveShowTab(); return; }
  const pk = e.target.closest("[data-lpick]"), sd = e.target.closest("[data-lside]"), rt = e.target.closest("[data-lrate]");
  try {
    if (pk) { await livePick(pk.dataset.lpick); try { localStorage.setItem("spanwall.liveTab", "settings"); } catch (err) { /* ignore */ } liveShowTab(); }
    else if (sd && sd.dataset.lside === "edit") await liveOpenCode(LV.st.scene);
    else if (sd && sd.dataset.lside === "dup") await liveDuplicate(LV.st.scene);
    else if (sd && sd.dataset.lside === "del") await liveDelete(LV.st.scene);
    else if (sd && sd.dataset.lside === "show") await liveSet({ enabled: true }, "Showing on the desktop");
    else if (sd && sd.dataset.lside === "resetparams") {
      LV.vals[LV.st.scene] = {}; $("#liveSettings").dataset.key = "";
      tellBg({ type: "params", params: {} });
      await api("/api/live/params", { scene: LV.st.scene, params: {} });
      await liveLoadScenes(); render();
    } else if (rt) await liveRate(rt.dataset.n, rt.dataset.lrate);
  } catch (err) { fail(err); }
});

$("#side").addEventListener("input", (e) => {
  const el = e.target.closest("[data-lp]");
  if (!LV.on || !el) return;
  const name = LV.st.scene, vals = liveValues(name), k = el.dataset.lp;
  vals[k] = el.type === "checkbox" ? el.checked : el.type === "range" ? +el.value : el.value;
  if (el.type === "range") { const b = el.parentElement.querySelector("b"); if (b) b.textContent = el.value; }
  if (k === "_theme") { $("#liveSettings").dataset.key = ""; render(); } // custom shows two more colours
  liveParamChanged(name);
});

$("#side").addEventListener("change", async (e) => {
  const el = e.target.closest("[data-lsched]");
  if (!LV.on || !el) return;
  const box = $("#liveSched"), sc = { enabled: box.querySelector("[data-lsched=enabled]").checked,
    day: box.querySelector("[data-lsched=day]").value, night: box.querySelector("[data-lsched=night]").value };
  if (sc.enabled && !sc.day && !sc.night) { toast("Pick a day scene and a night scene", true); }
  try { await liveSet({ schedule: sc }, sc.enabled ? "Day and night scenes on" : "Day and night scenes off"); } catch (err) { fail(err); }
});

async function liveRate(name, r) {
  const s = liveScene(name) || { title: name };
  const rating = s.rating === r ? "none" : r;
  await api("/api/live/rate", { name, rating });
  toast(rating === "fav" ? `${s.title} is a favourite` : rating === "ban" ? `${s.title} won't show in the slideshow` : `${s.title}: rating cleared`);
  await liveLoadScenes();
  if (LV.on) render();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

async function liveDuplicate(name) {
  const to = (prompt(`Copy ${name} as (lower-case letters, digits, - and _)`, name + "-copy") || "").trim();
  if (!to) return;
  const r = await api("/api/live/duplicate", { from: name, name: to, activate: LV.on });
  toast(`Made ${r.scene}: it's yours to edit`);
  liveForgetFrames();
  await liveLoadScenes(); liveRefresh();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
  return r.scene;
}

async function liveDelete(name) {
  const s = liveScene(name) || { title: name };
  const q = s.builtin ? `Remove your edit of ${s.title} and go back to the built-in version?` : `Delete ${s.title} (${name}.js) with its snapshots and settings? This can't be undone.`;
  if (!confirm(q)) return;
  await api("/api/live/delete", { name });
  toast(s.builtin ? `${s.title} is back to the built-in version` : `Deleted ${s.title}`);
  delete LV.vals[name]; delete LV.schema[name]; delete LV.err[name];
  liveForgetFrames();
  await liveLoadScenes(); liveRefresh();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

// ---- code editor --------------------------------------------------------------------------

async function liveOpenCode(name) {
  const r = await api("/api/live/scene?name=" + encodeURIComponent(name));
  LV.code = r.name;
  $("#liveCodeName").textContent = r.name + ".js" + (r.user ? "" : " (built-in: saving makes your own copy of the same name; Duplicate keeps the original too)");
  $("#liveCodeText").value = r.code;
  $("#liveCode").classList.remove("hidden");
  liveShowCodeError();
}

function liveShowCodeError() {
  const bar = $("#liveCodeErr"), e = LV.code && LV.err[LV.code];
  bar.classList.toggle("hidden", !e);
  if (!e) return;
  bar.textContent = (e.line ? `Line ${e.line}: ` : "") + e.message;
  bar.dataset.line = e.line || "";
}

function liveGotoLine(n) {
  const t = $("#liveCodeText"), lines = t.value.split("\n");
  if (!n || n > lines.length) return;
  let a = 0;
  for (let i = 0; i < n - 1; i++) a += lines[i].length + 1;
  t.focus();
  t.setSelectionRange(a, a + lines[n - 1].length);
  const lh = parseFloat(getComputedStyle(t).lineHeight) || 19;
  t.scrollTop = Math.max(0, (n - 4) * lh);
}

$("#liveCodeErr").addEventListener("click", () => liveGotoLine(+$("#liveCodeErr").dataset.line));
$("#liveCodeClose").addEventListener("click", () => $("#liveCode").classList.add("hidden"));
$("#liveCodeSave").addEventListener("click", async () => {
  try {
    delete LV.err[LV.code]; liveShowCodeError();
    await api("/api/live/scene", { name: LV.code, code: $("#liveCodeText").value, activate: true });
    toast("Saved"); await liveLoadScenes(); liveRefresh();
  } catch (e) { fail(e); }
});
$("#liveCodeText").addEventListener("keydown", (e) => {
  if (e.key === "Tab") { e.preventDefault(); const t = e.target, a = t.selectionStart; t.setRangeText("  ", a, t.selectionEnd, "end"); }
  if (e.key === "s" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); $("#liveCodeSave").click(); }
});

// ---- messages from the background frame and snapshots --------------------------------------

addEventListener("message", (e) => {
  if (e.origin !== location.origin || !e.data) return;
  const d = e.data;
  if (d.snapshot) return liveSnapDone(d.snapshot, d.ok);
  if (d.type === "hello") { liveSendView(); const v = LV.st && LV.vals[LV.st.scene]; if (v) tellBg({ type: "params", params: v }); }
  else if (d.type === "scene") { LV.schema[d.scene] = { params: d.params }; delete LV.err[d.scene]; if (LV.on) render(); liveShowCodeError(); }
  else if (d.type === "error") { LV.err[d.scene] = { message: d.message, line: d.line }; if (LV.on) render(); liveShowCodeError(); }
});

// Server-sent "live" event: something about the animation changed.
async function liveChanged() {
  await liveLoadScenes();
  if (LV.on) liveRefresh();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

$("#liveToggle").addEventListener("click", () => liveSet({ enabled: !(LV.st && LV.st.enabled) }).catch(fail));
$("#liveSave").addEventListener("click", () => { liveForgetFrames(); liveSet({ fps: +$("#liveFps").value, charMM: +$("#liveChar").value || 0 }, "Set").catch(fail); });

// ---- the Live wallpapers folder in the Library ----------------------------------------

function liveRenderCards() {
  const st = LV.st || {};
  const cards = LV.scenes.map((s) => {
    const cur = st.enabled && s.name === st.scene;
    const img = s.thumb ? `<img src="/api/live/thumb?name=${encodeURIComponent(s.name)}&v=${encodeURIComponent(s.version)}" data-lact="view" data-n="${esc(s.name)}" title="Open in the Live view">`
      : `<div class="pending">making a snapshot…</div>`;
    return `<div class="card live ${cur ? "current" : ""} ${s.rating === "ban" ? "banned" : ""}" style="--ar:1.714">${img}
      <div class="meta"><b title="${esc(s.description || "")}">${esc(s.title)}</b><span class="muted">${s.user ? (s.builtin ? "edited" : "yours") : "built in"}</span></div>
      <div class="rate">
        <button data-lrate="fav" data-n="${esc(s.name)}" class="${s.rating === "fav" ? "on" : ""}" title="Favourite: comes up more often in the slideshow">★</button>
        <button data-lrate="ban" data-n="${esc(s.name)}" class="${s.rating === "ban" ? "on" : ""}" title="Never show in the slideshow">Ban</button>
        <button data-lact="dup" data-n="${esc(s.name)}" title="Copy this scene under a new name to make it your own">Duplicate</button>
        ${s.user ? `<button data-lact="del" data-n="${esc(s.name)}" class="danger" title="${s.builtin ? "Remove your edit and go back to the built-in scene" : "Delete this scene"}">${s.builtin ? "Revert" : "Delete"}</button>` : ""}
      </div>
      <div class="acts"><button data-lact="edit" data-n="${esc(s.name)}">Edit code</button><button data-lact="apply" data-n="${esc(s.name)}" class="primary">${cur ? "On screen" : "Apply"}</button></div>
    </div>`;
  }).join("");
  $("#libGrid").innerHTML = cards ? cards + `<div class="fill"></div>` : `<p class="muted" style="padding:18px">No live wallpapers yet.</p>`;
  for (const s of LV.scenes) if (!s.thumb) liveSnap(s.name);
}

// Snapshots: a scene is drawn for a moment in a hidden frame, which saves a
// picture of the desk; one at a time, and again whenever the scene changes.
function liveSnap(name) {
  if (LV.snapping === name || LV.snapQ.includes(name)) return;
  LV.snapQ.push(name);
  liveSnapNext();
}

function liveSnapNext() {
  if (LV.snapping || !LV.snapQ.length) return;
  const name = LV.snapQ.shift();
  LV.snapping = name;
  let f = $("#snapFrame");
  if (!f) { f = document.createElement("iframe"); f.id = "snapFrame"; f.title = "snapshot"; document.body.appendChild(f); }
  f.src = `/live/?snapshot=1&scene=${encodeURIComponent(name)}`;
  clearTimeout(LV.snapTimer);
  LV.snapTimer = setTimeout(() => liveSnapDone(name, false), 12000);
}

async function liveSnapDone(name, ok) {
  if (LV.snapping !== name) return;
  clearTimeout(LV.snapTimer);
  LV.snapping = null;
  $("#snapFrame").src = "about:blank";
  if (ok) {
    await liveLoadScenes();
    if (LV.on) render();
    if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
  }
  liveSnapNext();
}

$("#libGrid").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-lrate],[data-lact]");
  if (!b) return;
  const name = b.dataset.n, s = liveScene(name) || { title: name };
  try {
    if (b.dataset.lrate) await liveRate(name, b.dataset.lrate);
    else if (b.dataset.lact === "apply") await liveSet({ scene: name, enabled: true }, `Showing ${s.title}`);
    else if (b.dataset.lact === "dup") await liveDuplicate(name);
    else if (b.dataset.lact === "del") await liveDelete(name);
    else {
      if (name !== (LV.st && LV.st.scene)) await api("/api/live", { scene: name });
      switchTab("layout");
      await liveEnter();
      if (b.dataset.lact === "edit") await liveOpenCode(name);
    }
  } catch (err) { fail(err); }
});

async function liveMake() {
  const path = (prompt("Path to a GIF, video or picture (videos need ffmpeg)") || "").trim().replace(/^"|"$/g, "");
  if (!path) return;
  const base = path.split(/[\\/]/).pop().replace(/\.[^.]+$/, "").toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "");
  const name = (prompt("Scene name (lower-case letters, digits, - and _)", base) || "").trim();
  if (!name) return;
  toast("Converting…");
  try {
    const r = await api("/api/live/convert", { path, name });
    toast(`Made "${r.scene}": ${r.frames} frames, ${r.cols}×${r.rows} characters`);
    await loadLibrary();
  } catch (e) { fail(e); }
}

try { if (localStorage.getItem("spanwall.liveView") === "1") setTimeout(() => liveEnter().catch(() => {}), 0); } catch (e) { /* ignore */ }

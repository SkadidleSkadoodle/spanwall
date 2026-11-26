// Live (animated ASCII) wallpaper in the editor: "Live" in the Layout tab's
// dropdown shows the scene's canvas behind the monitors (which stay put) to
// move, zoom and turn like an image, with a side panel to pick scenes; the
// Library has a Live wallpapers folder.
const LIVE_VALUE = "__live";
const LV = { st: null, scenes: [], on: false, code: null, snapQ: [], snapping: null, previewKey: "", moveWas: false };

const liveOption = () => `<option value="${LIVE_VALUE}" ${LV.on ? "selected" : ""}>Live</option>`;
const liveScene = (name) => LV.scenes.find((s) => s.name === name) || null;
const liveCur = () => (LV.st ? liveScene(LV.st.scene) : null);

async function liveLoadScenes() {
  try { LV.st = await api("/api/live"); LV.scenes = LV.st.scenes || []; } catch (e) { LV.scenes = []; }
  return LV.st;
}

// ---- the Live view: the layout editor with the scene's canvas as the image ----------

function liveViews() {
  const name = LV.st && LV.st.scene;
  return name ? [{ key: "live:" + name, live: true, scene: name, img: null, ids: null, b: bbox() }] : [];
}

// The editor draws a snapshot of the scene's whole canvas (made on demand).
function liveHref(v) {
  const s = liveScene(v.scene);
  if (s && s.worldThumb) return `/api/live/thumb?name=${encodeURIComponent(s.name)}&kind=world&v=${encodeURIComponent(s.worldVersion)}`;
  liveSnap(v.scene, true, true);
  return "";
}

async function liveLoadFrame(v) {
  if (S.framesLoading[v.key]) return;
  S.framesLoading[v.key] = true;
  try {
    const r = await api("/api/live/frame?scene=" + encodeURIComponent(v.scene));
    const f = r.frame;
    S.frames[v.key] = { live: true, scene: v.scene, w: r.width, h: r.height, def: r.default.zoom,
      zoom: f.zoom, dx: f.dx || 0, dy: f.dy || 0, flipX: !!f.flipX, flipY: !!f.flipY, rotate: f.rotate || 0 };
    S.framesSaved[v.key] = frameVals(S.frames[v.key]);
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
  LV.on = false;
  try { localStorage.removeItem("spanwall.liveView"); } catch (e) { /* ignore */ }
  $("#tab-layout").classList.remove("live-mode");
  $("#liveCode").classList.add("hidden");
  liveForgetFrames();
  S.moveImg = LV.moveWas;
  $("#btnResetImg").textContent = "Reset image";
  $("#canvas").classList.toggle("moving-img", S.moveImg);
  liveSetPreview("");
  $("#layoutSelect").value = S.view;
  fit(); render();
}

function liveTab(active) {
  if (!LV.on) return;
  if (active) liveRefresh();
  else liveSetPreview("");
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
  if (LV.on) { if (S.displays && S.displays.length) { fit(); render(); } }
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

// ---- side panel: a small live preview, the scene's details, and a picker -------------

function liveSetPreview(scene, force) {
  const f = $("#liveSidePreview");
  if (!f) { LV.previewKey = ""; return; }
  const s = liveScene(scene);
  const key = scene ? scene + "@" + (s ? s.version : "") : "";
  if (key === LV.previewKey && !force) return;
  LV.previewKey = key;
  f.src = scene && $("#tab-layout").classList.contains("active") ? `/live/?preview=1&scene=${encodeURIComponent(scene)}` : "about:blank";
}

function liveRenderSide() {
  const side = $("#side"), st = LV.st;
  if (!st) { side.innerHTML = `<div class="empty">Loading…</div>`; return; }
  if (side.dataset.mode !== "live") {
    side.dataset.mode = "live"; side.dataset.for = ""; side.classList.add("live-side");
    side.innerHTML = `<div class="preview"><iframe id="liveSidePreview" title="Live preview"></iframe></div>
      <div id="liveInfo"></div><label>Scenes</label><div id="livePicks" class="picks"></div>
      <div class="note">Scenes are files in <span id="liveDirNote"></span>: edit them there, here, or ask Claude to write one. Favourites, bans and the slideshow are in Library &gt; Live wallpapers.</div>`;
    LV.previewKey = "";
  }
  const s = liveCur() || { name: st.scene, title: st.scene };
  liveSetPreview(st.scene);
  $("#liveDirNote").textContent = st.scenesDir;
  const f = S.frames["live:" + st.scene];
  const on = st.enabled && st.running;
  const fps = st.fps || s.fps || 30, ch = st.charMM || s.charMM || 4.2;
  const rel = f ? f.zoom / (f.def || 1.5) : 1; // 1 = the canvas at 1:1 on the monitors
  const fr = f ? imgRect(bbox(), f) : null;
  const p = st.page;
  const kv = [
    ["Desktop", on ? "showing this scene" : st.enabled ? "starting…" : "animation off"],
    ["Frame rate", `${fps} fps` + (st.fps ? ` (scene's own ${s.fps || 30})` : " (scene's own)")],
    ["Characters", `${(ch * rel).toFixed(1)} mm tall on screen` + (Math.abs(rel - 1) > 0.005 ? ` (${ch} mm at 1:1)` : "")],
    f ? ["Canvas", `${Math.round(f.w)} × ${Math.round(f.h)} mm, ${(f.w / Math.max(1, bbox().w)).toFixed(1)}× your monitors`] : null,
    f ? ["Framing", `${rel.toFixed(2)}× zoom` + (fr ? `, moved ${Math.round(f.dx * fr.w)}, ${Math.round(f.dy * fr.h)} mm` : "") + (f.rotate ? `, turned ${f.rotate}°` : "") + (f.flipX || f.flipY ? ", mirrored" : "")] : null,
    on && p && p.fps !== undefined ? ["Running", `${p.fps} fps, ${p.ms} ms a frame, ${p.cols}×${p.rows} characters`] : null,
    on && p && p.error ? ["Error", p.error] : null,
  ].filter(Boolean);
  $("#liveInfo").innerHTML = `<h3>${esc(s.title)}</h3>
    <div class="badges"><span class="badge">${s.user ? (s.builtin ? "edited built-in" : "yours") : "built in"}</span><span class="badge">${esc(s.file || s.name + ".js")}</span>${s.framed ? `<span class="badge">framed</span>` : ""}${on ? `<span class="badge on">on screen</span>` : ""}</div>
    <div class="id">${esc(s.description || "")}</div>
    <div class="kv">${kv.map(([k, v]) => `<span>${esc(k)}</span><span>${esc(v)}</span>`).join("")}</div>
    <div class="row rate"><button data-lrate="fav" data-n="${esc(s.name)}" class="${s.rating === "fav" ? "on" : ""}" title="Comes up more often in the live slideshow">★ Favourite</button>
      <button data-lrate="ban" data-n="${esc(s.name)}" class="${s.rating === "ban" ? "on" : ""}" title="Never shown by the live slideshow">Ban</button></div>
    <div class="row"><button data-lside="edit">Edit code</button>${on ? "" : `<button data-lside="show" class="primary">Show on desktop</button>`}</div>
    <div class="note">Drag the scene to move it, drag a corner or scroll to zoom, the curved arrows or [ ] to turn, H and V to mirror. Save &amp; apply to use it.</div>`;
  const key = LV.scenes.map((x) => x.name + x.version + x.thumb + x.rating).join() + st.scene;
  const picks = $("#livePicks");
  if (picks.dataset.key !== key) {
    picks.dataset.key = key;
    picks.innerHTML = LV.scenes.map((x) => `<button class="pick ${x.name === st.scene ? "sel" : ""} ${x.rating === "ban" ? "banned" : ""}" data-lpick="${esc(x.name)}" title="${esc(x.description || "")}">` +
      (x.thumb ? `<img loading="lazy" src="/api/live/thumb?name=${encodeURIComponent(x.name)}&v=${encodeURIComponent(x.version)}">` : `<div class="pending">snapshot…</div>`) +
      `<span>${x.rating === "fav" ? "★ " : ""}${esc(x.title)}</span></button>`).join("");
    for (const x of LV.scenes) if (!x.thumb) liveSnap(x.name, false);
  }
}

$("#side").addEventListener("click", async (e) => {
  if (!LV.on) return;
  const pk = e.target.closest("[data-lpick]"), sd = e.target.closest("[data-lside]"), rt = e.target.closest("[data-lrate]");
  try {
    if (pk) await livePick(pk.dataset.lpick);
    else if (sd && sd.dataset.lside === "edit") await liveOpenCode(LV.st.scene);
    else if (sd && sd.dataset.lside === "show") await liveSet({ enabled: true }, "Showing on the desktop");
    else if (rt) await liveRate(rt.dataset.n, rt.dataset.lrate);
  } catch (err) { fail(err); }
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

async function liveOpenCode(name) {
  const r = await api("/api/live/scene?name=" + encodeURIComponent(name));
  LV.code = r.name;
  $("#liveCodeName").textContent = r.name + ".js" + (r.user ? "" : " (built-in)");
  $("#liveCodeText").value = r.code;
  $("#liveCode").classList.remove("hidden");
}

// Server-sent "live" event: something about the animation changed.
async function liveChanged() {
  await liveLoadScenes();
  if (LV.on) { liveRefresh(); liveSetPreview(LV.st && LV.st.scene, true); }
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

$("#liveToggle").addEventListener("click", () => liveSet({ enabled: !(LV.st && LV.st.enabled) }).catch(fail));
$("#liveSave").addEventListener("click", () => { liveForgetFrames(); liveSet({ fps: +$("#liveFps").value, charMM: +$("#liveChar").value || 0 }, "Set").catch(fail); });
$("#liveCodeClose").addEventListener("click", () => $("#liveCode").classList.add("hidden"));
$("#liveCodeSave").addEventListener("click", async () => {
  try {
    await api("/api/live/scene", { name: LV.code, code: $("#liveCodeText").value, activate: true });
    toast("Saved"); await liveLoadScenes(); liveRefresh();
  } catch (e) { fail(e); }
});
$("#liveCodeText").addEventListener("keydown", (e) => {
  if (e.key === "Tab") { e.preventDefault(); const t = e.target, a = t.selectionStart; t.setRangeText("  ", a, t.selectionEnd, "end"); }
  if (e.key === "s" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); $("#liveCodeSave").click(); }
});

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
      </div>
      <div class="acts"><button data-lact="edit" data-n="${esc(s.name)}">Edit code</button><button data-lact="apply" data-n="${esc(s.name)}" class="primary">${cur ? "On screen" : "Apply"}</button></div>
    </div>`;
  }).join("");
  $("#libGrid").innerHTML = cards ? cards + `<div class="fill"></div>` : `<p class="muted" style="padding:18px">No live wallpapers yet.</p>`;
  for (const s of LV.scenes) if (!s.thumb) liveSnap(s.name, false);
}

// Snapshots: a scene is drawn for a moment in a hidden frame, which saves a
// picture (of the desk, or of the scene's whole canvas for the editor); one
// at a time, and again whenever the scene changes.
function liveSnap(name, world, first) {
  const id = name + (world ? "#world" : "");
  if (LV.snapping === id || LV.snapQ.includes(id)) return;
  if (first) LV.snapQ.unshift(id); else LV.snapQ.push(id);
  liveSnapNext();
}

function liveSnapNext() {
  if (LV.snapping || !LV.snapQ.length) return;
  const id = LV.snapQ.shift(), [name, world] = id.split("#");
  LV.snapping = id;
  let f = $("#snapFrame");
  if (!f) { f = document.createElement("iframe"); f.id = "snapFrame"; f.title = "snapshot"; document.body.appendChild(f); }
  f.src = `/live/?snapshot=1${world ? "&world=1" : ""}&scene=${encodeURIComponent(name)}`;
  clearTimeout(LV.snapTimer);
  LV.snapTimer = setTimeout(() => liveSnapDone(id, false), 12000);
}

async function liveSnapDone(id, ok) {
  if (LV.snapping !== id) return;
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

addEventListener("message", (e) => {
  if (e.origin === location.origin && e.data && e.data.snapshot) liveSnapDone(e.data.snapshot + (e.data.world ? "#world" : ""), e.data.ok);
});

$("#libGrid").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-lrate],[data-lact]");
  if (!b) return;
  const name = b.dataset.n, s = liveScene(name) || { title: name };
  try {
    if (b.dataset.lrate) await liveRate(name, b.dataset.lrate);
    else if (b.dataset.lact === "apply") await liveSet({ scene: name, enabled: true }, `Showing ${s.title}`);
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

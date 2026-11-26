// Live (animated ASCII) wallpaper in the editor: a "Live" choice in the
// Layout tab's dropdown, and the Live wallpapers folder in the Library.
const LIVE_VALUE = "__live";
const LV = { st: null, scenes: [], on: false, code: null, snapQ: [], snapping: null, previewKey: "" };

const liveOption = () => `<option value="${LIVE_VALUE}" ${LV.on ? "selected" : ""}>Live</option>`;

async function liveLoadScenes() {
  try { LV.st = await api("/api/live"); LV.scenes = LV.st.scenes || []; } catch (e) { LV.scenes = []; }
  return LV.st;
}

// ---- the Live view in the Layout tab ------------------------------------------

function liveEnter() {
  LV.on = true;
  try { localStorage.setItem("spanwall.liveView", "1"); } catch (e) { /* ignore */ }
  $("#tab-layout").classList.add("live-mode");
  $("#layoutSelect").value = LIVE_VALUE;
  liveRefresh().catch(fail);
}

function liveLeave() {
  LV.on = false;
  try { localStorage.removeItem("spanwall.liveView"); } catch (e) { /* ignore */ }
  $("#tab-layout").classList.remove("live-mode");
  $("#liveCode").classList.add("hidden");
  liveSetPreview("");
  $("#layoutSelect").value = S.view;
  fit(); render();
}

function liveTab(active) {
  if (!LV.on) return;
  if (active) liveRefresh().catch(fail);
  else liveSetPreview(""); // don't animate a hidden preview
}

function liveSetPreview(scene) {
  const f = $("#livePreview");
  const key = scene ? scene + "@" + ((LV.scenes.find((s) => s.name === scene) || {}).version || "") : "";
  if (key === LV.previewKey) return;
  LV.previewKey = key;
  f.src = scene ? `/live/?preview=1&scene=${encodeURIComponent(scene)}` : "about:blank";
}

async function liveRefresh() {
  const st = await liveLoadScenes();
  if (!st) return;
  const tg = $("#liveToggle");
  tg.textContent = "Animated wallpaper: " + (st.enabled ? (st.running ? "on" : "starting…") : "off");
  tg.classList.toggle("on", st.enabled);
  $("#liveScene").innerHTML = LV.scenes.map((s) => `<option value="${esc(s.name)}">${esc(s.title)}</option>`).join("");
  $("#liveScene").value = st.scene;
  $("#liveFps").value = String(st.fps || 0);
  $("#liveChar").value = st.charMM || "";
  $("#liveChar").placeholder = "auto";
  $("#liveDir").textContent = st.scenesDir;
  const p = st.page;
  $("#liveStats").textContent = st.enabled && p ? (p.error ? "Scene error: " + p.error : p.fps !== undefined ? `${p.fps} fps, ${p.ms} ms a frame, ${p.cols}×${p.rows} characters` : "") : "";
  const s = LV.scenes.find((x) => x.name === st.scene) || {};
  $("#liveTitle").textContent = s.title || st.scene;
  $("#liveDesc").textContent = s.description || "";
  if (LV.on && $("#tab-layout").classList.contains("active")) liveSetPreview(st.scene);
}

async function liveSet(body, msg) {
  await api("/api/live", body);
  if (msg) toast(msg);
  await liveRefresh();
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
  if (LV.on) await liveRefresh();
  if (S.libTab === "live" && $("#tab-library").classList.contains("active")) liveRenderCards();
}

$("#liveToggle").addEventListener("click", () => liveSet({ enabled: !(LV.st && LV.st.enabled) }).catch(fail));
$("#liveScene").addEventListener("change", (e) => liveSet({ scene: e.target.value }).catch(fail));
$("#liveSave").addEventListener("click", () => liveSet({ fps: +$("#liveFps").value, charMM: +$("#liveChar").value || 0 }, "Saved").catch(fail));
$("#liveEdit").addEventListener("click", () => liveOpenCode(LV.st.scene).catch(fail));
$("#liveCodeClose").addEventListener("click", () => $("#liveCode").classList.add("hidden"));
$("#liveCodeSave").addEventListener("click", async () => {
  try {
    await api("/api/live/scene", { name: LV.code, code: $("#liveCodeText").value, activate: true });
    toast("Saved"); await liveRefresh();
  } catch (e) { fail(e); }
});
$("#liveCodeText").addEventListener("keydown", (e) => {
  if (e.key === "Tab") { e.preventDefault(); const t = e.target, a = t.selectionStart; t.setRangeText("  ", a, t.selectionEnd, "end"); }
  if (e.key === "s" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); $("#liveCodeSave").click(); }
});

// ---- the Live wallpapers folder in the Library ---------------------------------

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
  for (const s of LV.scenes) if (!s.thumb && !LV.snapQ.includes(s.name) && LV.snapping !== s.name) LV.snapQ.push(s.name);
  liveSnapNext();
}

// Snapshots: each scene is drawn for a moment in a hidden frame, which saves
// a picture; one at a time, and again whenever a scene's code changes.
function liveSnapNext() {
  if (LV.snapping || !LV.snapQ.length) return;
  const name = LV.snapQ.shift();
  LV.snapping = name;
  let f = $("#snapFrame");
  if (!f) { f = document.createElement("iframe"); f.id = "snapFrame"; f.title = "snapshot"; document.body.appendChild(f); }
  f.src = `/live/?snapshot=1&scene=${encodeURIComponent(name)}`;
  clearTimeout(LV.snapTimer);
  LV.snapTimer = setTimeout(() => liveSnapDone(name, false), 10000);
}

async function liveSnapDone(name, ok) {
  if (LV.snapping !== name) return;
  clearTimeout(LV.snapTimer);
  LV.snapping = null;
  $("#snapFrame").src = "about:blank";
  if (ok) {
    await liveLoadScenes();
    if (S.libTab === "live" && $("#tab-library").classList.contains("active")) { liveRenderCards(); return; }
  }
  liveSnapNext();
}

addEventListener("message", (e) => {
  if (e.origin === location.origin && e.data && e.data.snapshot) liveSnapDone(e.data.snapshot, e.data.ok);
});

$("#libGrid").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-lrate],[data-lact]");
  if (!b) return;
  const name = b.dataset.n, s = LV.scenes.find((x) => x.name === name) || {};
  try {
    if (b.dataset.lrate) {
      const rating = s.rating === b.dataset.lrate ? "none" : b.dataset.lrate;
      await api("/api/live/rate", { name, rating });
      toast(rating === "fav" ? `${s.title} is a favourite` : rating === "ban" ? `${s.title} won't show in the slideshow` : `${s.title}: rating cleared`);
      await liveLoadScenes(); liveRenderCards();
    } else if (b.dataset.lact === "apply") {
      await liveSet({ scene: name, enabled: true }, `Showing ${s.title}`);
    } else {
      if (name !== (LV.st && LV.st.scene)) await api("/api/live", { scene: name });
      switchTab("layout");
      liveEnter();
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

try { if (localStorage.getItem("spanwall.liveView") === "1") liveEnter(); } catch (e) { /* ignore */ }

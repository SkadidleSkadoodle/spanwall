// Live tab: the animated (ASCII scene) wallpaper.
const LV = { st: null, sel: "", active: false, code: null };

function liveTab(active) {
  LV.active = active;
  if (active) liveLoad().catch(fail);
  else $("#livePreview").src = "about:blank"; // don't animate a hidden preview
}

async function liveLoad() {
  LV.st = await api("/api/live");
  const st = LV.st;
  if (!LV.sel || !st.scenes.some((s) => s.name === LV.sel)) LV.sel = st.scene;
  const tg = $("#liveToggle");
  tg.textContent = "Animated wallpaper: " + (st.enabled ? (st.running ? "on" : "starting…") : "off");
  tg.classList.toggle("on", st.enabled);
  $("#liveScene").innerHTML = st.scenes.map((s) => `<option value="${s.name}">${esc(s.title)}</option>`).join("");
  $("#liveScene").value = st.scene;
  $("#liveFps").value = String(st.fps || 0);
  $("#liveChar").value = st.charMM || "";
  $("#liveChar").placeholder = "scene's";
  $("#liveDir").textContent = st.scenesDir;
  const p = st.page;
  $("#liveStats").textContent = st.enabled && p ? (p.error ? "Scene error: " + p.error : `${p.fps ?? "?"} fps, ${p.ms ?? "?"} ms a frame, ${p.cols}×${p.rows} characters`) : "";
  $("#liveList").innerHTML = st.scenes.map((s) =>
    `<button data-n="${s.name}" class="${s.name === LV.sel ? "sel" : ""} ${st.enabled && s.name === st.scene ? "on" : ""}"><b>${esc(s.title)}</b><span>${s.user ? (s.builtin ? "edited" : "yours") : ""}</span></button>`).join("");
  $$("#liveList button").forEach((b) => b.addEventListener("click", () => { LV.sel = b.dataset.n; liveShowSel(); liveLoadList(); }));
  liveShowSel();
}

function liveLoadList() {
  $$("#liveList button").forEach((b) => b.classList.toggle("sel", b.dataset.n === LV.sel));
}

function liveShowSel() {
  const s = LV.st.scenes.find((x) => x.name === LV.sel) || {};
  $("#liveTitle").textContent = s.title || LV.sel;
  $("#liveDesc").textContent = s.description || "";
  const src = `/live/?preview=1&scene=${encodeURIComponent(LV.sel)}`;
  const f = $("#livePreview");
  if (LV.active && f.dataset.src !== src + LV.st.scenes.length) { f.dataset.src = src + LV.st.scenes.length; f.src = src; }
}

function esc(s) { return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]); }

async function liveSet(body, msg) {
  LV.st = await api("/api/live", body);
  if (msg) toast(msg);
  await liveLoad();
}

$("#liveToggle").addEventListener("click", () => liveSet({ enabled: !LV.st.enabled }).catch(fail));
$("#liveScene").addEventListener("change", (e) => { LV.sel = e.target.value; liveSet({ scene: LV.sel }).catch(fail); });
$("#liveSave").addEventListener("click", () => liveSet({ fps: +$("#liveFps").value, charMM: +$("#liveChar").value || 0 }, "Saved").catch(fail));
$("#liveUse").addEventListener("click", () => liveSet({ scene: LV.sel, enabled: true }, "Showing on the desktop").catch(fail));
$("#liveEdit").addEventListener("click", async () => {
  try {
    const r = await api("/api/live/scene?name=" + encodeURIComponent(LV.sel));
    LV.code = r.name;
    $("#liveCodeName").textContent = r.name + ".js" + (r.user ? "" : " (built-in)");
    $("#liveCodeText").value = r.code;
    $("#liveCode").classList.remove("hidden");
  } catch (e) { fail(e); }
});
$("#liveCodeClose").addEventListener("click", () => $("#liveCode").classList.add("hidden"));
$("#liveCodeSave").addEventListener("click", async () => {
  try {
    await api("/api/live/scene", { name: LV.code, code: $("#liveCodeText").value, activate: true });
    LV.sel = LV.code; $("#livePreview").dataset.src = "";
    toast("Saved"); await liveLoad();
  } catch (e) { fail(e); }
});
$("#liveCodeText").addEventListener("keydown", (e) => {
  if (e.key === "Tab") { e.preventDefault(); const t = e.target, a = t.selectionStart; t.setRangeText("  ", a, t.selectionEnd, "end"); }
  if (e.key === "s" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); $("#liveCodeSave").click(); }
});
$("#liveConvert").addEventListener("click", async () => {
  const b = $("#liveConvert");
  b.disabled = true; b.textContent = "Converting…";
  try {
    const r = await api("/api/live/convert", { path: $("#liveConvPath").value.trim().replace(/^"|"$/g, ""), name: $("#liveConvName").value.trim(),
      cols: +$("#liveConvCols").value || 0, mono: $("#liveConvMono").checked, activate: true });
    LV.sel = r.scene; $("#livePreview").dataset.src = "";
    toast(`Made "${r.scene}": ${r.frames} frames, ${r.cols}×${r.rows} characters`);
    await liveLoad();
  } catch (e) { fail(e); } finally { b.disabled = false; b.textContent = "Convert"; }
});

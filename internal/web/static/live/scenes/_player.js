// Plays an ASCII clip converted by Spanwall (sw_live_convert or the editor):
// scenes made from a GIF, video or picture are one line that calls this.
export default function player(file, opts = {}) {
  return {
    name: opts.name || file,
    description: opts.description || "Converted ASCII clip.",
    fps: opts.fps || 30,
    cell: { h: opts.charMM || 4.2 },
    background: opts.background || "#000000",
    params: {
      fit: { label: "Fit", type: "select", options: ["cover", "contain"], default: opts.fit === "contain" ? "contain" : "cover" },
      speed: { label: "Speed", type: "range", min: 0.25, max: 3, step: 0.05, default: 1 },
      mono: { label: "One colour", type: "toggle", default: false },
      color: { label: "Colour (one colour)", type: "color", default: "#b8f5c8" },
    },
    async setup() {
      this.clip = null;
      const res = await fetch(new URL(file, import.meta.url).href, { cache: "no-store" });
      const clip = await res.json();
      clip.total = clip.frames.reduce((s, f) => s + f.ms, 0);
      clip.rgb = clip.frames.map((f) => (f.rgb ? Uint8Array.from(atob(f.rgb), (c) => c.charCodeAt(0)) : null));
      this.clip = clip; this.last = -1;
    },
    draw(g, t) {
      const clip = this.clip, P = g.params;
      if (!clip) return false;
      let ms = (g.phase("T", P.speed) * 1000) % clip.total, k = 0;
      while (k < clip.frames.length - 1 && ms >= clip.frames[k].ms) ms -= clip.frames[k++].ms;
      if (k === this.last && this.pv === g.paramsVersion) return false;
      this.last = k; this.pv = g.paramsVersion;
      const f = clip.frames[k], rgb = P.mono ? null : clip.rgb[k], { cols, rows, chars, fg } = g;
      // fit the clip on the canvas: "cover" fills it, "contain" shows all of it
      const sx = clip.cols / cols, sy = clip.rows / rows;
      const s = P.fit === "contain" ? Math.max(sx, sy) : Math.min(sx, sy);
      const ox = (clip.cols - cols * s) / 2, oy = (clip.rows - rows * s) / 2;
      const tint = parseInt(String(P.color).slice(1), 16) || 0xb8f5c8;
      for (let r = 0; r < rows; r++) {
        const sr = Math.floor(oy + r * s);
        for (let c = 0; c < cols; c++) {
          const i = r * cols + c, sc = Math.floor(ox + c * s);
          if (sr < 0 || sc < 0 || sr >= clip.rows || sc >= clip.cols) { chars[i] = 32; continue; }
          const j = sr * clip.cols + sc;
          chars[i] = f.c.charCodeAt(j);
          fg[i] = rgb ? (rgb[3 * j] << 16) | (rgb[3 * j + 1] << 8) | rgb[3 * j + 2] : tint;
        }
      }
    },
  };
}

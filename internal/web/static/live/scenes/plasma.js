// Flowing colour waves, shaded with ASCII. Wavelengths are in desk mm, so the
// pattern runs on across bezels at the same physical size on every monitor.
// The per-cell work is table lookups: sines per column and per row, and each
// cell's distance from the canvas centre, are worked out once.
const TAU = Math.PI * 2, LUT = 2048;

export default {
  name: "Plasma",
  description: "Slow flowing colour waves in ASCII shading.",
  fps: 20,
  cell: { h: 4.2 },
  background: "#05070a",
  params: {
    speed: { label: "Speed", type: "range", min: 0.1, max: 3, step: 0.1, default: 1 },
    size: { label: "Wave size", type: "range", min: 0.3, max: 3, step: 0.1, default: 1 },
    hue: { label: "Colour", type: "range", min: 0, max: 1, step: 0.01, default: 0.5 },
    range: { label: "Colour range", type: "range", min: 0, max: 1, step: 0.05, default: 0.4 },
    drift: { label: "Colour drift", type: "range", min: 0, max: 0.1, step: 0.005, default: 0.01 },
    bright: { label: "Brightness", type: "range", min: 0.3, max: 1.5, step: 0.05, default: 1 },
  },
  setup(g) {
    this.pal = new Uint32Array(256);
    this.ramp = Uint32Array.from(" .:-=+*#%@", (c) => c.codePointAt(0));
    this.sin = Float32Array.from({ length: LUT }, (_, k) => Math.sin((k / LUT) * TAU));
    this.colA = new Float32Array(g.cols); this.colS = new Float32Array(g.cols); this.colC = new Float32Array(g.cols);
    this.rowA = new Float32Array(g.rows); this.rowS = new Float32Array(g.rows); this.rowC = new Float32Array(g.rows);
    this.dist = new Uint16Array(g.n);
    this.ver = -1;
  },
  draw(g, t) {
    const P = g.params, k = 1 / P.size, T = t * P.speed;
    if (this.ver !== g.paramsVersion) { // the radial term's phase per cell depends on the wave size
      this.ver = g.paramsVersion;
      const cx = g.width / 2, cy = g.height / 2;
      for (let r = 0; r < g.rows; r++) for (let c = 0; c < g.cols; c++) {
        this.dist[r * g.cols + c] = Math.round(((Math.hypot(c * g.cw - cx, r * g.ch - cy) * k) / 47 / TAU) * LUT) % LUT;
      }
    }
    const { cols, rows, chars, fg, mask, cw, ch } = g, { pal, ramp, sin, dist, colA, colS, colC, rowA, rowS, rowC } = this;
    for (let i = 0; i < 256; i++) pal[i] = g.hsl(P.hue + (i / 255) * P.range + t * P.drift, 0.8, Math.min(0.95, (0.28 + (i / 255) * 0.5) * P.bright));
    for (let c = 0; c < cols; c++) { const x = c * cw * k; colA[c] = Math.sin(x / 83 + T * 0.7); colS[c] = Math.sin(x / 120 + T * 0.5); colC[c] = Math.cos(x / 120 + T * 0.5); }
    for (let r = 0; r < rows; r++) { const y = r * ch * k; rowA[r] = Math.sin(y / 61 - T * 0.9); rowS[r] = Math.sin(y / 120); rowC[r] = Math.cos(y / 120); }
    const shift = Math.round((((-T % TAU) + TAU) / TAU) * LUT), n = ramp.length, m = LUT - 1;
    for (let r = 0; r < rows; r++) {
      const ra = rowA[r], rs = rowS[r], rc = rowC[r], base = r * cols;
      for (let c = 0; c < cols; c++) {
        const i = base + c;
        if (!mask[i]) continue;
        const v = (colA[c] + ra + colS[c] * rc + colC[c] * rs + sin[(dist[i] + shift) & m]) * 0.125 + 0.5;
        chars[i] = ramp[(v * n) | 0] || ramp[n - 1];
        fg[i] = pal[(v * 255) | 0];
      }
    }
  },
};

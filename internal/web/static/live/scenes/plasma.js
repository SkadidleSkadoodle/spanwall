// Flowing colour waves, shaded with ASCII. Wavelengths are in desk mm, so the
// pattern runs on across bezels at the same physical size on every monitor.
// The per-cell work is table lookups: sines per column and per row, and each
// cell's distance from the desk centre, are worked out once.
const TAU = Math.PI * 2, LUT = 2048;

export default {
  name: "Plasma",
  description: "Slow flowing colour waves in ASCII shading.",
  fps: 20,
  cell: { h: 4.2 },
  background: "#05070a",
  setup(g) {
    this.pal = new Uint32Array(256);
    this.ramp = Uint32Array.from(" .:-=+*#%@", (c) => c.codePointAt(0));
    this.sin = Float32Array.from({ length: LUT }, (_, k) => Math.sin((k / LUT) * TAU));
    const cx = g.width / 2, cy = g.height / 2;
    this.dist = new Uint16Array(g.n); // radial phase per cell, in LUT steps
    for (let r = 0; r < g.rows; r++) for (let c = 0; c < g.cols; c++) {
      this.dist[r * g.cols + c] = Math.round(((Math.hypot(c * g.cw - cx, r * g.ch - cy) / 47 / TAU) * LUT)) % LUT;
    }
    this.colA = new Float32Array(g.cols); this.colS = new Float32Array(g.cols); this.colC = new Float32Array(g.cols);
    this.rowA = new Float32Array(g.rows); this.rowS = new Float32Array(g.rows); this.rowC = new Float32Array(g.rows);
  },
  draw(g, t) {
    const { cols, rows, chars, fg, mask, cw, ch } = g, { pal, ramp, sin, dist, colA, colS, colC, rowA, rowS, rowC } = this;
    for (let k = 0; k < 256; k++) pal[k] = g.hsl(0.5 + (k / 255) * 0.4 + t * 0.01, 0.8, 0.28 + (k / 255) * 0.5);
    // sin(x/83 + .7t) per column; sin((x+y)/120 + .5t) = sin(a)cos(b) + cos(a)sin(b) with a per column, b per row
    for (let c = 0; c < cols; c++) { const x = c * cw; colA[c] = Math.sin(x / 83 + t * 0.7); colS[c] = Math.sin(x / 120 + t * 0.5); colC[c] = Math.cos(x / 120 + t * 0.5); }
    for (let r = 0; r < rows; r++) { const y = r * ch; rowA[r] = Math.sin(y / 61 - t * 0.9); rowS[r] = Math.sin(y / 120); rowC[r] = Math.cos(y / 120); }
    const shift = Math.round(((-t % TAU) + TAU) / TAU * LUT), n = ramp.length, mask_ = LUT - 1;
    for (let r = 0; r < rows; r++) {
      const ra = rowA[r], rs = rowS[r], rc = rowC[r], base = r * cols;
      for (let c = 0; c < cols; c++) {
        const i = base + c;
        if (!mask[i]) continue;
        const v = (colA[c] + ra + colS[c] * rc + colC[c] * rs + sin[(dist[i] + shift) & mask_]) * 0.125 + 0.5;
        chars[i] = ramp[(v * n) | 0] || ramp[n - 1];
        fg[i] = pal[(v * 255) | 0];
      }
    }
  },
};

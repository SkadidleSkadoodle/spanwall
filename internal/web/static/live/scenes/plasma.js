// Flowing colour waves, shaded with ASCII. Wavelengths are in desk mm, so the
// pattern runs on across bezels at the same physical size on every monitor.
export default {
  name: "Plasma",
  description: "Slow flowing colour waves in ASCII shading.",
  fps: 20,
  cell: { h: 4.2 },
  background: "#05070a",
  setup(g) {
    this.pal = new Uint32Array(256);
    this.ramp = " .:-=+*#%@";
  },
  draw(g, t) {
    const { cols, rows, chars, fg, mask, cw, ch } = g, pal = this.pal;
    for (let k = 0; k < 256; k++) pal[k] = g.hsl(0.5 + (k / 255) * 0.4 + t * 0.01, 0.8, 0.28 + (k / 255) * 0.5);
    const ramp = this.ramp, n = ramp.length;
    const cx = g.width / 2, cy = g.height / 2;
    for (let r = 0; r < rows; r++) {
      const y = r * ch;
      const sy = Math.sin(y / 61 - t * 0.9);
      for (let c = 0; c < cols; c++) {
        const i = r * cols + c;
        if (!mask[i]) continue;
        const x = c * cw;
        const v = (Math.sin(x / 83 + t * 0.7) + sy + Math.sin((x + y) / 120 + t * 0.5) + Math.sin(Math.hypot(x - cx, y - cy) / 47 - t)) * 0.125 + 0.5;
        chars[i] = ramp.charCodeAt(Math.min(n - 1, (v * n) | 0));
        fg[i] = pal[(v * 255) | 0];
      }
    }
  },
};

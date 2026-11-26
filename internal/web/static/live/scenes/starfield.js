// Flying through stars. The vanishing point is the middle of the scene's
// canvas, so the field opens out across all monitors as one space.
export default {
  name: "Starfield",
  description: "Warp through a field of stars.",
  fps: 30,
  cell: { h: 4.2 },
  background: "#02030a",
  params: {
    speed: { label: "Speed", type: "range", min: 0.1, max: 4, step: 0.1, default: 1 },
    density: { label: "Stars", type: "range", min: 0.2, max: 2, step: 0.1, default: 1 },
    spread: { label: "Spread", type: "range", min: 0.3, max: 2, step: 0.05, default: 1 },
    colourful: { label: "Coloured stars", type: "toggle", default: true },
  },
  draw(g, t) {
    const P = g.params;
    if (this.ver !== g.paramsVersion) {
      this.ver = g.paramsVersion;
      const rnd = g.rand(7), n = Math.round(Math.min(8000, (g.width * g.height / 250) * P.density));
      this.stars = Array.from({ length: n }, () => ({ x: rnd() * 2 - 1, y: rnd() * 2 - 1, z: rnd(), tint: rnd() }));
      this.glyph = [".", "·", "+", "*", "✦", "@"].map((s) => s.codePointAt(0));
    }
    const { chars, fg, cols, rows } = g;
    chars.fill(32);
    const cx = g.width / 2, cy = g.height / 2, reach = Math.max(g.width, g.height) * 0.11 * P.spread;
    for (const s of this.stars) {
      const z = 1 - ((s.z + t * 0.07 * P.speed) % 1); // 1 far -> 0 near
      const [c, r] = g.cell(g.x0 + cx + (s.x / (z + 0.02)) * reach, g.y0 + cy + (s.y / (z + 0.02)) * reach);
      if (c < 0 || r < 0 || c >= cols || r >= rows) continue;
      const near = 1 - z, i = r * cols + c;
      chars[i] = this.glyph[Math.min(5, (near * near * 6) | 0)];
      const b = 60 + 195 * Math.min(1, near * 1.4);
      fg[i] = !P.colourful ? g.rgb(b, b, b) : s.tint < 0.15 ? g.rgb(b, b * 0.8, b * 0.55) : s.tint < 0.3 ? g.rgb(b * 0.7, b * 0.8, b) : g.rgb(b, b, b);
    }
  },
};

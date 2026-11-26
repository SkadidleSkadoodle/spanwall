// Flying through stars. The vanishing point is the middle of the desk, so the
// field opens out across all monitors as one space.
export default {
  name: "Starfield",
  description: "Warp through a field of stars centred on the desk.",
  fps: 30,
  cell: { h: 4.2 },
  background: "#02030a",
  setup(g) {
    const rnd = g.rand(7), n = Math.round(Math.min(6000, g.width * g.height / 250));
    this.stars = Array.from({ length: n }, () => ({ x: rnd() * 2 - 1, y: rnd() * 2 - 1, z: rnd(), tint: rnd() }));
    this.chars = [".", "·", "+", "*", "✦", "@"].map((s) => s.codePointAt(0));
  },
  draw(g, t) {
    const { chars, fg, cols, rows } = g;
    chars.fill(32);
    const cx = g.width / 2, cy = g.height / 2, reach = Math.max(g.width, g.height) * 0.9;
    for (const s of this.stars) {
      const z = 1 - ((s.z + t * 0.07) % 1); // 1 far -> 0 near
      const px = cx + (s.x / (z + 0.02)) * reach * 0.12, py = cy + (s.y / (z + 0.02)) * reach * 0.12;
      const [c, r] = g.cell(g.x0 + px, g.y0 + py);
      if (c < 0 || r < 0 || c >= cols || r >= rows) continue;
      const near = 1 - z, i = r * cols + c;
      chars[i] = this.chars[Math.min(5, (near * near * 6) | 0)];
      const b = 60 + 195 * Math.min(1, near * 1.4);
      fg[i] = s.tint < 0.15 ? g.rgb(b, b * 0.8, b * 0.55) : s.tint < 0.3 ? g.rgb(b * 0.7, b * 0.8, b) : g.rgb(b, b, b);
    }
  },
};

// The classic spinning ASCII donut (after a1k0n's donut.c), lit from above,
// centred on your main monitor.
export default {
  name: "Donut",
  description: "The classic spinning ASCII torus, lit and shaded.",
  fps: 24,
  cell: { h: 4.2 },
  background: "#06060a",
  params: {
    speed: { label: "Speed", type: "range", min: 0.1, max: 3, step: 0.1, default: 1 },
    size: { label: "Size", type: "range", min: 0.15, max: 0.6, step: 0.01, default: 0.38 },
    hue: { label: "Colour", type: "range", min: 0, max: 1, step: 0.01, default: 0.08 },
  },
  setup(g) {
    this.z = new Float32Array(g.n);
    this.lum = ".,-~:;=!*#$@";
  },
  draw(g, t) {
    const P = g.params, { cols, chars, fg } = g, zb = this.z, lum = this.lum, m = g.main;
    chars.fill(32); zb.fill(0);
    const A = t * 0.9 * P.speed, B = t * 0.45 * P.speed, cA = Math.cos(A), sA = Math.sin(A), cB = Math.cos(B), sB = Math.sin(B);
    const hCells = (m.r1 - m.r0) * P.size; // radius on screen, in rows
    const kx = (hCells * g.ch) / g.cw, ky = hCells; // round in mm
    for (let j = 0; j < 6.283; j += 0.035) {
      const ct = Math.cos(j), stt = Math.sin(j);
      for (let i = 0; i < 6.283; i += 0.012) {
        const sp = Math.sin(i), cp = Math.cos(i);
        const h = ct + 2, D = 1 / (sp * h * sA + stt * cA + 5);
        const tt = sp * h * cA - stt * sA;
        const x = (m.cc + kx * 1.6 * D * (cp * h * cB - tt * sB)) | 0;
        const y = (m.cr + ky * 1.6 * D * (cp * h * sB + tt * cB)) | 0;
        if (x < 0 || y < 0 || x >= cols || y >= g.rows) continue;
        const o = y * cols + x;
        if (D <= zb[o]) continue;
        zb[o] = D;
        const L = (stt * sA - sp * ct * cA) * cB - sp * ct * sA - stt * cA - cp * ct * sB;
        const k = Math.max(0, Math.min(11, (L * 8) | 0));
        chars[o] = lum.charCodeAt(k);
        fg[o] = g.hsl(P.hue + k / 70, 0.85, 0.3 + k / 22);
      }
    }
  },
};

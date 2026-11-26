// The classic spinning ASCII donut (after a1k0n's donut.c), lit from above,
// centred on your biggest monitor.
export default {
  name: "Donut",
  description: "The classic spinning ASCII torus, lit and shaded.",
  fps: 24,
  cell: { h: 4.2 },
  background: "#06060a",
  setup(g) {
    this.z = new Float32Array(g.n);
    this.lum = ".,-~:;=!*#$@";
  },
  draw(g, t) {
    const { cols, chars, fg } = g, zb = this.z, lum = this.lum, m = g.main;
    chars.fill(32); zb.fill(0);
    const A = t * 0.9, B = t * 0.45, cA = Math.cos(A), sA = Math.sin(A), cB = Math.cos(B), sB = Math.sin(B);
    const hCells = (m.r1 - m.r0) * 0.38; // donut radius on screen, in rows
    const kx = (hCells * g.ch) / g.cw, ky = hCells; // keep it round in mm
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
        const L = (stt * sA - sp * ct * cA) * cB - sp * ct * sA - stt * cA - cp * ct * sB; // -1.4..1.4
        const k = Math.max(0, Math.min(11, (L * 8) | 0));
        chars[o] = lum.charCodeAt(k);
        fg[o] = g.hsl(0.08 + k / 70, 0.85, 0.3 + k / 22);
      }
    }
  },
};

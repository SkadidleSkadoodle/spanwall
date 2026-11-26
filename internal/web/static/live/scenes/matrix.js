// Digital rain. Each column falls at its own speed; glyphs flicker as they go.
const GLYPHS = "ｦｱｳｴｵｶｷｹｺｻｼｽｾｿﾀﾂﾃﾅﾆﾇﾈﾊﾋﾎﾏﾐﾑﾒﾓﾔﾕﾗﾘﾜ0123456789Z:.=*+-<>¦";

export default {
  name: "Digital rain",
  description: "Falling green katakana streams, like the film.",
  fps: 20,
  cell: { h: 4.6 },
  background: "#000000",
  setup(g) {
    this.codes = Array.from(GLYPHS, (s) => s.codePointAt(0));
    this.pal = new Uint32Array(32);
    for (let k = 0; k < 32; k++) this.pal[k] = g.rgb(20 * (k / 31) ** 2, 40 + 215 * (k / 31), 70 * (k / 31) ** 3);
  },
  draw(g, t) {
    const { cols, rows, chars, fg, mask, hash } = g, codes = this.codes, pal = this.pal, nc = codes.length;
    chars.fill(32);
    for (let c = 0; c < cols; c++) {
      for (let s = 0; s < 2; s++) { // two streams per column, at different speeds
        const speed = 6 + hash(c, s) * 14; // rows per second
        const trail = 8 + ((hash(c, s + 7) * 28) | 0);
        const span = rows + trail + ((hash(c, s + 3) * rows) | 0);
        const head = ((t * speed + hash(c, s + 11) * span) % span) | 0;
        for (let k = 0; k < trail; k++) {
          const r = head - k;
          if (r < 0 || r >= rows) continue;
          const i = r * cols + c;
          if (!mask[i]) continue;
          const flick = (t * (k === 0 ? 20 : 3) + hash(r, c) * 10) | 0;
          chars[i] = codes[(hash(c * 31 + r, flick) * nc) | 0];
          fg[i] = k === 0 ? 0xd8ffe0 : pal[(31 * (1 - k / trail)) | 0];
        }
      }
    }
  },
};

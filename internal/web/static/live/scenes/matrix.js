// Digital rain. Each column falls at its own speed; glyphs flicker as they go.
const SETS = {
  katakana: "ｦｱｳｴｵｶｷｹｺｻｼｽｾｿﾀﾂﾃﾅﾆﾇﾈﾊﾋﾎﾏﾐﾑﾒﾓﾔﾕﾗﾘﾜ0123456789Z:.=*+-<>¦",
  latin: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
  digits: "0123456789",
  binary: "01",
  symbols: "+-*/=<>[]{}()#$%&@!?;:~^",
};

export default {
  name: "Digital rain",
  description: "Falling glyph streams, like the film.",
  fps: 20,
  cell: { h: 4.6 },
  background: "#000000",
  params: {
    speed: { label: "Speed", type: "range", min: 0.2, max: 3, step: 0.1, default: 1 },
    streams: { label: "Streams per column", type: "range", min: 1, max: 4, step: 1, default: 2 },
    length: { label: "Trail length", type: "range", min: 0.3, max: 2.5, step: 0.1, default: 1 },
    glyphs: { label: "Glyphs", type: "select", options: Object.keys(SETS), default: "katakana" },
    color: { label: "Colour", type: "color", default: "#33ff66" },
    head: { label: "Head colour", type: "color", default: "#d8ffe0" },
  },
  draw(g, t) {
    const P = g.params;
    if (this.ver !== g.paramsVersion) {
      this.ver = g.paramsVersion;
      this.codes = Array.from(SETS[P.glyphs] || SETS.katakana, (s) => s.codePointAt(0));
      const c = parseInt(String(P.color).slice(1), 16) || 0x33ff66;
      this.pal = new Uint32Array(32);
      for (let k = 0; k < 32; k++) this.pal[k] = g.scale(c, 0.15 + 0.85 * (k / 31) ** 1.5);
      this.headc = parseInt(String(P.head).slice(1), 16) || 0xd8ffe0;
    }
    const { cols, rows, chars, fg, mask, hash } = g, codes = this.codes, pal = this.pal, nc = codes.length;
    chars.fill(32);
    for (let c = 0; c < cols; c++) {
      for (let s = 0; s < P.streams; s++) {
        const speed = (6 + hash(c, s) * 14) * P.speed; // rows per second
        const trail = Math.round((8 + hash(c, s + 7) * 28) * P.length);
        const span = rows + trail + ((hash(c, s + 3) * rows) | 0);
        const head = ((t * speed + hash(c, s + 11) * span) % span) | 0;
        for (let k = 0; k < trail; k++) {
          const r = head - k;
          if (r < 0 || r >= rows) continue;
          const i = r * cols + c;
          if (!mask[i]) continue;
          const flick = (t * (k === 0 ? 20 : 3) + hash(r, c) * 10) | 0;
          chars[i] = codes[(hash(c * 31 + r, flick) * nc) | 0];
          fg[i] = k === 0 ? this.headc : pal[(31 * (1 - k / trail)) | 0];
        }
      }
    }
  },
};

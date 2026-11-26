// A big block clock on the largest monitor. It only redraws when the time
// changes, so it costs almost nothing.
const FONT = { // 5x7
  0: ["01110", "10001", "10011", "10101", "11001", "10001", "01110"], 1: ["00100", "01100", "00100", "00100", "00100", "00100", "01110"],
  2: ["01110", "10001", "00001", "00010", "00100", "01000", "11111"], 3: ["11111", "00010", "00100", "00010", "00001", "10001", "01110"],
  4: ["00010", "00110", "01010", "10010", "11111", "00010", "00010"], 5: ["11111", "10000", "11110", "00001", "00001", "10001", "01110"],
  6: ["00110", "01000", "10000", "11110", "10001", "10001", "01110"], 7: ["11111", "00001", "00010", "00100", "01000", "01000", "01000"],
  8: ["01110", "10001", "10001", "01110", "10001", "10001", "01110"], 9: ["01110", "10001", "10001", "01111", "00001", "00010", "01100"],
  ":": ["0", "1", "1", "0", "1", "1", "0"],
};

export default {
  name: "Clock",
  description: "A large block clock with the date, on your biggest monitor.",
  fps: 2,
  cell: { h: 4.2 },
  background: "#07090d",
  draw(g) {
    const now = new Date(), key = now.getHours() * 60 + now.getMinutes() + (now.getSeconds() % 2) * 0.5;
    if (key === this.key) return false; // nothing changed: skip the upload and redraw
    this.key = key;
    const hh = String(now.getHours()).padStart(2, "0"), mm = String(now.getMinutes()).padStart(2, "0");
    const blink = now.getSeconds() % 2 === 0;
    const text = hh + ":" + mm, m = g.main;
    const width = [...text].reduce((w, ch) => w + FONT[ch][0].length + 1, -1);
    const px = Math.max(1, Math.floor(((m.c1 - m.c0) * 0.55) / width)); // cells per font pixel (x)
    const py = Math.max(1, Math.round((px * g.cw) / g.ch)); // keep pixels square in mm
    g.clear(32, 0x2a3140);
    for (let r = 0; r < g.rows; r += 3) for (let c = (r / 3) % 2 ? 2 : 0; c < g.cols; c += 6) g.put(c, r, "·", 0x1b2029);
    let x = m.cc - Math.floor((width * px) / 2);
    const y = m.cr - Math.floor((7 * py) / 2);
    for (const ch of text) {
      const rows = FONT[ch], on = ch !== ":" || blink;
      for (let fy = 0; fy < 7; fy++) for (let fx = 0; fx < rows[fy].length; fx++) {
        if (rows[fy][fx] !== "1" || !on) continue;
        for (let a = 0; a < py; a++) for (let b = 0; b < px; b++) g.put(x + fx * px + b, y + fy * py + a, "█", g.hsl(0.55 + (fy / 7) * 0.08, 0.6, 0.62));
      }
      x += (rows[0].length + 1) * px;
    }
    const date = now.toLocaleDateString(undefined, { weekday: "long", year: "numeric", month: "long", day: "numeric" }).toUpperCase();
    g.text(m.cc - (date.length >> 1), y + 7 * py + py * 2, date, 0x8a96aa);
  },
};

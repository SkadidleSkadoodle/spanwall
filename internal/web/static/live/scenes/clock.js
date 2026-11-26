// A big block clock on your main monitor. It only redraws when the time
// changes, so it costs almost nothing.
import { big, textWidth } from "./_font.js";

export default {
  name: "Clock",
  description: "A large block clock with the date, on your main monitor.",
  fps: 2,
  cell: { h: 4.2 },
  background: "#07090d",
  params: {
    h24: { label: "24-hour", type: "toggle", default: true },
    seconds: { label: "Seconds", type: "toggle", default: false },
    date: { label: "Date", type: "toggle", default: true },
    size: { label: "Size", type: "range", min: 0.2, max: 0.95, step: 0.05, default: 0.55 },
    color: { label: "Colour", type: "color", default: "#6fb4ff" },
    dots: { label: "Dot background", type: "toggle", default: true },
  },
  draw(g) {
    const P = g.params, now = new Date();
    const key = [now.getHours(), now.getMinutes(), P.seconds ? now.getSeconds() : now.getSeconds() % 2, g.paramsVersion].join();
    if (key === this.key) return false; // nothing changed: skip the upload and redraw
    this.key = key;
    let h = now.getHours();
    const ampm = h < 12 ? "AM" : "PM";
    if (!P.h24) h = h % 12 || 12;
    const sep = P.seconds || now.getSeconds() % 2 === 0 ? ":" : " ";
    let text = String(h).padStart(2, P.h24 ? "0" : " ") + sep + String(now.getMinutes()).padStart(2, "0");
    if (P.seconds) text += ":" + String(now.getSeconds()).padStart(2, "0");
    const m = g.main, width = textWidth(text);
    const px = Math.max(1, Math.floor(((m.c1 - m.c0) * P.size) / width)); // cells per font pixel (x)
    const py = Math.max(1, Math.round((px * g.cw) / g.ch)); // keep pixels square in mm
    const base = parseInt(String(P.color).slice(1), 16) || 0x6fb4ff;
    g.clear(32, 0x2a3140);
    if (P.dots) for (let r = 0; r < g.rows; r += 3) for (let c = (r / 3) % 2 ? 2 : 0; c < g.cols; c += 6) g.put(c, r, "·", 0x1b2029);
    const x = m.cc - Math.floor((width * px) / 2), y = m.cr - Math.floor((7 * py) / 2);
    big(g, text, x, y, px, py, (fx, fy) => g.mix(base, 0xffffff, fy / 14));
    if (!P.h24) g.text(x + width * px + px, y, ampm, base);
    if (P.date) {
      const date = now.toLocaleDateString(undefined, { weekday: "long", year: "numeric", month: "long", day: "numeric" }).toUpperCase();
      g.text(m.cc - (date.length >> 1), y + 7 * py + py * 2, date, g.mix(base, 0x8a96aa, 0.6));
    }
  },
};

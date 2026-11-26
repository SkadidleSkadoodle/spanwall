// Today's weather as ASCII art on your main monitor, the next days under it,
// and rain or snow falling across every monitor. Data from Open-Meteo (free,
// no key), refreshed every 15 minutes. The location is the city you set, or
// the one Spanwall's day and night setting uses.
import { big, textWidth } from "./_font.js";

const ART = {
  clear: ["    \\   /    ", "     .-.     ", "  ― (   ) ―  ", "     `-'     ", "    /   \\    "],
  partly: ["   \\  /      ", ' _ /"".-.    ', "   \\_(   ).  ", "   /(___(__) ", "             "],
  cloudy: ["             ", "     .--.    ", "  .-(    ).  ", " (___.__)__) ", "             "],
  fog: ["             ", " _ - _ - _ - ", "  _ - _ - _  ", " _ - _ - _ - ", "             "],
  rain: ["     .-.     ", "    (   ).   ", "   (___(__)  ", "    ' ' ' '  ", "   ' ' ' '   "],
  snow: ["     .-.     ", "    (   ).   ", "   (___(__)  ", "    *  *  *  ", "   *  *  *   "],
  storm: ["     .-.     ", "    (   ).   ", "   (___(__)  ", "    ,/' ,/'  ", "    /   /    "],
};
function kind(code) {
  if (code === 0) return "clear";
  if (code <= 2) return "partly";
  if (code === 3) return "cloudy";
  if (code === 45 || code === 48) return "fog";
  if ((code >= 71 && code <= 77) || code === 85 || code === 86) return "snow";
  if (code >= 95) return "storm";
  return "rain";
}
const WORDS = { clear: "Clear", partly: "Partly cloudy", cloudy: "Cloudy", fog: "Fog", rain: "Rain", snow: "Snow", storm: "Thunderstorm" };

export default {
  name: "Weather",
  description: "Today's weather and the next days, with rain or snow across the desk.",
  fps: 12,
  cell: { h: 4.2 },
  background: "#05080c",
  params: {
    city: { label: "City (empty: Spanwall's location)", type: "text", default: "" },
    units: { label: "Units", type: "select", options: ["celsius", "fahrenheit"], default: "celsius" },
    color: { label: "Colour", type: "color", default: "#9fd3ff" },
    particles: { label: "Rain and snow", type: "toggle", default: true },
  },
  setup() { this.data = null; this.err = ""; this.at = 0; this.key = ""; },
  async refresh(g) {
    const P = g.params;
    try {
      let lat, lon, place = P.city;
      if (P.city) {
        const r = await (await fetch(`https://geocoding-api.open-meteo.com/v1/search?count=1&name=${encodeURIComponent(P.city)}`)).json();
        if (!r.results || !r.results.length) throw new Error(`no place called "${P.city}"`);
        ({ latitude: lat, longitude: lon, name: place } = r.results[0]);
      } else if (g.location) ({ lat, lon } = g.location);
      else throw new Error("set a city in this scene's settings");
      const u = `https://api.open-meteo.com/v1/forecast?latitude=${lat}&longitude=${lon}&timezone=auto&forecast_days=5` +
        `&current=temperature_2m,weather_code,wind_speed_10m,relative_humidity_2m&daily=weather_code,temperature_2m_max,temperature_2m_min&temperature_unit=${P.units}`;
      const d = await (await fetch(u)).json();
      if (!d.current) throw new Error("no forecast");
      this.data = Object.assign(d, { place: place || "" });
      this.err = "";
    } catch (e) { this.err = String(e.message || e); }
    this.key = "";
  },
  draw(g, t) {
    const P = g.params;
    if (this.pv !== g.paramsVersion) { this.pv = g.paramsVersion; this.at = 0; }
    if (Date.now() - this.at > 15 * 60e3) { this.at = Date.now(); this.refresh(g); }
    const d = this.data, k = d ? kind(d.current.weather_code) : "cloudy";
    const fall = P.particles && d && (k === "rain" || k === "snow" || k === "storm");
    const key = [d && d.current.time, this.err, g.paramsVersion].join();
    if (!fall && key === this.key) return false; // nothing moves: no redraw until new data
    this.key = key;
    const base = parseInt(String(P.color).slice(1), 16) || 0x9fd3ff, dim = g.scale(base, 0.45);
    g.clear(32, base);
    if (fall) { // particles over the whole canvas, deterministic in t
      const snow = k === "snow", n = Math.round(g.cols * g.rows / (snow ? 90 : 60));
      for (let i = 0; i < n; i++) {
        const speed = snow ? 2 + g.hash(i, 1) * 3 : 18 + g.hash(i, 2) * 14; // rows per second
        const r = ((g.hash(i, 3) * g.rows + t * speed) % g.rows) | 0;
        const c = ((g.hash(i, 4) * g.cols + (snow ? Math.sin(t * 0.8 + i) * 2 : t * speed * 0.15)) % g.cols + g.cols) % g.cols | 0;
        g.put(c, r, snow ? (i % 3 ? "·" : "*") : i % 4 ? "|" : "'", g.scale(base, snow ? 0.8 : 0.35 + g.hash(i, 5) * 0.3));
      }
    }
    const m = g.main;
    if (!d) { const msg = this.err ? `Weather: ${this.err}` : "Loading the weather…"; g.text(m.cc - (msg.length >> 1), m.cr, msg, base); return; }
    const cur = d.current, unit = P.units === "fahrenheit" ? "F" : "C";
    const temp = String(Math.round(cur.temperature_2m)) + "°" + unit;
    const px = Math.max(1, Math.floor(((m.c1 - m.c0) * 0.22) / textWidth(temp))), py = Math.max(1, Math.round((px * g.cw) / g.ch));
    const art = ART[k], top = m.r0 + Math.round((m.r1 - m.r0) * 0.18);
    // the icon, drawn 2x in each direction, left of the temperature
    const ax = m.cc - Math.round((m.c1 - m.c0) * 0.3);
    art.forEach((line, y) => [...line].forEach((ch, x) => { if (ch !== " ") for (let a = 0; a < 2; a++) for (let b = 0; b < 2; b++) g.put(ax + x * 2 + b, top + y * 2 + a, ch, base); }));
    const tx = m.cc - Math.round((m.c1 - m.c0) * 0.02);
    big(g, temp, tx, top, px, py, base);
    const under = top + Math.max(12, 7 * py) + 2;
    const line = `${WORDS[k]}  ·  wind ${Math.round(cur.wind_speed_10m)} km/h  ·  humidity ${cur.relative_humidity_2m}%` + (d.place ? `  ·  ${d.place}` : "");
    g.text(m.cc - (line.length >> 1), under, line, base);
    // the next days
    const days = d.daily.time.slice(1, 5), w = 18, x0 = m.cc - Math.round((days.length * w) / 2), y0 = under + 4;
    days.forEach((day, i) => {
      const x = x0 + i * w, dk = kind(d.daily.weather_code[i + 1]);
      const name = new Date(day + "T12:00").toLocaleDateString(undefined, { weekday: "short" });
      g.text(x + 3, y0, name, base);
      ART[dk].forEach((l, y) => g.text(x, y0 + 1 + y, l, dim));
      g.text(x + 2, y0 + 7, `${Math.round(d.daily.temperature_2m_min[i + 1])}° / ${Math.round(d.daily.temperature_2m_max[i + 1])}°`, base);
    });
  },
};

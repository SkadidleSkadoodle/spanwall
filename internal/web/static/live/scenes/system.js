// CPU and memory as ASCII gauges and history graphs on your main monitor,
// from Spanwall's own numbers (polled every few seconds). Between updates
// nothing is redrawn.
const BARS = " ▁▂▃▄▅▆▇█";

export default {
  name: "System monitor",
  description: "CPU and memory gauges and history, uptime and battery.",
  fps: 2,
  cell: { h: 4.2 },
  background: "#05070a",
  params: {
    every: { label: "Update every (s)", type: "range", min: 1, max: 10, step: 1, default: 2 },
    history: { label: "History (samples)", type: "range", min: 30, max: 300, step: 10, default: 120 },
    color: { label: "Colour", type: "color", default: "#7dffb2" },
    warn: { label: "Busy colour", type: "color", default: "#ff7a59" },
  },
  setup() { this.cpu = []; this.mem = []; this.s = null; this.at = 0; this.fresh = true; },
  async poll() {
    try { this.s = await (await fetch("/api/live/stats", { cache: "no-store" })).json(); } catch (e) { return; }
    this.cpu.push(this.s.cpu); this.mem.push(this.s.memPercent);
    this.fresh = true;
  },
  draw(g) {
    const P = g.params;
    if (Date.now() - this.at > P.every * 1000) { this.at = Date.now(); this.poll(); }
    if (!this.fresh && this.pv === g.paramsVersion) return false;
    this.fresh = false; this.pv = g.paramsVersion;
    const keep = P.history | 0;
    if (this.cpu.length > keep) { this.cpu.splice(0, this.cpu.length - keep); this.mem.splice(0, this.mem.length - keep); }
    const col = parseInt(String(P.color).slice(1), 16) || 0x7dffb2, warn = parseInt(String(P.warn).slice(1), 16) || 0xff7a59;
    const tint = (v) => g.mix(col, warn, Math.max(0, Math.min(1, (v - 50) / 45)));
    g.clear(32, col);
    const m = g.main, w = Math.min(keep, m.c1 - m.c0 - 12), x = m.cc - (w >> 1);
    let y = m.r0 + Math.round((m.r1 - m.r0) * 0.15);
    const s = this.s;
    if (!s) { g.text(m.cc - 5, m.cr, "Starting…", col); return; }
    const gauge = (label, v, hist, extra) => {
      g.text(x, y, label, col);
      const txt = `${v.toFixed(0).padStart(3)}%  ${extra}`;
      g.text(x + w - txt.length, y, txt, tint(v));
      y += 1;
      const filled = Math.round((w * v) / 100);
      for (let i = 0; i < w; i++) g.put(x + i, y, i < filled ? "█" : "░", i < filled ? tint((i / w) * 100) : g.scale(col, 0.25));
      y += 2;
      const h = 6; // history graph, h rows tall
      for (let i = 0; i < hist.length && i < w; i++) {
        const val = hist[hist.length - 1 - i], c = x + w - 1 - i, levels = (val / 100) * h * 8;
        for (let r = 0; r < h; r++) {
          const lv = Math.max(0, Math.min(8, Math.round(levels - r * 8)));
          if (lv) g.put(c, y + h - 1 - r, BARS[lv], tint(val));
        }
      }
      y += h + 2;
    };
    gauge("CPU", s.cpu, this.cpu, "");
    gauge("MEMORY", s.memPercent, this.mem, `${s.memUsed.toFixed(1)} / ${s.memTotal.toFixed(1)} GB`);
    const up = s.uptimeSec, days = Math.floor(up / 86400), hrs = Math.floor((up % 86400) / 3600), mins = Math.floor((up % 3600) / 60);
    let foot = `up ${days ? days + "d " : ""}${hrs}h ${mins}m`;
    if (s.battery) foot += `   battery ${s.battery.percent}%${s.battery.plugged ? " (plugged in)" : ""}`;
    foot += `   ${new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
    g.text(x, y, foot, g.scale(col, 0.7));
  },
};

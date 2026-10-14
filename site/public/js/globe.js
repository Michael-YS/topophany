// The hero art, ported from internal/tui/about.go and banner_test.go: a lit,
// dotted globe on which declared places are the only lights, and the 5x7
// wordmark. Every dot is a round dot, as in a terminal, but the ground under
// it is shaded smoothly rather than one cell at a time.

const hex = (v) => [(v >> 16) & 255, (v >> 8) & 255, v & 255];
export const C = {
  ink: hex(0x0a1016), muted: hex(0x8393a3), accent: hex(0x5eead4), accent2: hex(0x7dd3fc),
  glow: hex(0xf0fdfa), text: hex(0xd5dee7),
  landDay: hex(0xb4f5e8), landDusk: hex(0x2f9389), landDark: hex(0x1a4448),
  seaNight: hex(0x0c1922), seaDay: hex(0x1a4b69), seaGlint: hex(0x4c93b8),
  groundDark: hex(0x0e2321), groundDay: hex(0x23584f), graticule: hex(0x3e86ad), limb: hex(0x235a77),
  city: hex(0xfcd34d), up: hex(0x4ade80), warn: hex(0xfbbf24), down: hex(0xf87171), unknown: hex(0x6b7a8c),
};

const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
export function mix(a, b, t) {
  t = clamp(t, 0, 1);
  return [Math.round(a[0] + (b[0] - a[0]) * t), Math.round(a[1] + (b[1] - a[1]) * t), Math.round(a[2] + (b[2] - a[2]) * t)];
}
const css = (c, a = 1) => (a >= 1 ? `rgb(${c[0]},${c[1]},${c[2]})` : `rgba(${c[0]},${c[1]},${c[2]},${a})`);

export const bayer8 = (() => {
  const b = [
    [0, 32, 8, 40, 2, 34, 10, 42], [48, 16, 56, 24, 50, 18, 58, 26],
    [12, 44, 4, 36, 14, 46, 6, 38], [60, 28, 52, 20, 62, 30, 54, 22],
    [3, 35, 11, 43, 1, 33, 9, 41], [51, 19, 59, 27, 49, 17, 57, 25],
    [15, 47, 7, 39, 13, 45, 5, 37], [63, 31, 55, 23, 61, 29, 53, 21],
  ];
  return b.map((row) => row.map((v) => (v + 0.5) / 64));
})();
export const dither = (px, py, v) => v > bayer8[py & 7][px & 7];

// Natural Earth 1:110m land, 0.5° cells (internal/tui/land.bin).
let landBits = null;
export async function loadLand(url) {
  const res = await fetch(url);
  landBits = new Uint8Array(await res.arrayBuffer());
}
export function land(lat, lon) {
  if (!landBits) return false;
  lon = (lon + 180) % 360;
  if (lon < 0) lon += 360;
  const col = Math.min(719, Math.floor(lon * 2));
  const row = Math.min(359, Math.max(0, Math.floor((90 - lat) * 2)));
  const i = row * 720 + col;
  return (landBits[i >> 3] & (0x80 >> (i & 7))) !== 0;
}

const norm = (v) => {
  const l = Math.hypot(v[0], v[1], v[2]);
  return [v[0] / l, v[1] / l, v[2] / l];
};
const dot3 = (a, b) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
export function sphere(lat, lon) {
  const la = (lat * Math.PI) / 180, lo = (lon * Math.PI) / 180;
  return [Math.cos(la) * Math.sin(lo), Math.sin(la), Math.cos(la) * Math.cos(lo)];
}

const LIGHT = norm([-0.62, 0.42, 0.66]);
const HALF = norm([LIGHT[0], LIGHT[1], LIGHT[2] + 1]);

// Globe draws onto a canvas at a dot pitch. Places are the warm lights;
// links between them are lifted arcs coloured by their evidence, faint while
// healthy so problems stand out, with pulses only where traffic gets through.
export class Globe {
  constructor(canvas, { places, links = [], lon = 0, labels = true }) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.places = places;
    this.links = links;
    this.lon0 = lon;
    this.labels = labels;
    this.drag = 0;
    this.ground = document.createElement('canvas');
    this.gctx = this.ground.getContext('2d');
  }

  resize(cssSize, pitch) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.dpr = dpr;
    this.pitch = pitch;
    this.W = this.H = Math.max(24, Math.floor(cssSize / pitch));
    this.canvas.width = Math.round(this.W * pitch * dpr);
    this.canvas.height = Math.round(this.H * pitch * dpr);
    this.ground.width = this.W;
    this.ground.height = this.H;
    this.img = this.gctx.createImageData(this.W, this.H);
    this.grid = new Uint8Array(this.W * this.H);
    this.band = new Int16Array(this.W * this.H);
  }

  draw(t) {
    const { W, H, ctx, pitch, dpr } = this;
    const R = Math.min(W, H) / 2 - Math.max(3, Math.min(W, H) * 0.05);
    const cx = W / 2, cy = H / 2;
    const spin = (this.lon0 * Math.PI) / 180 - 0.35 + t * 0.11 + this.drag;
    const tilt = 0.36;
    const ss = Math.sin(spin), sc = Math.cos(spin), ts = Math.sin(-tilt), tc = Math.cos(-tilt);
    const toWorld = (v) => {
      const y = v[1] * tc - v[2] * ts, z = v[1] * ts + v[2] * tc;
      return [v[0] * sc + z * ss, y, -v[0] * ss + z * sc];
    };
    const vs = Math.sin(-spin), vc = Math.cos(-spin), ws = Math.sin(tilt), wc = Math.cos(tilt);
    const toView = (v) => {
      const x = v[0] * vc + v[2] * vs, z = -v[0] * vs + v[2] * vc;
      return [x, v[1] * wc - z * ws, v[1] * ws + z * wc];
    };
    const { grid, band, img } = this;
    const data = img.data;
    data.fill(0);
    // Land or sea and the graticule band of every dot on the disc.
    for (let py = 0; py < H; py++) {
      for (let px = 0; px < W; px++) {
        const i = py * W + px;
        band[i] = -1;
        grid[i] = 0;
        const nx = (px + 0.5 - cx) / R, ny = (py + 0.5 - cy) / R;
        if (Math.hypot(px + 0.5 - cx, py + 0.5 - cy) > R + 0.5) continue;
        const wv = toWorld([nx, -ny, Math.sqrt(Math.max(0, 1 - nx * nx - ny * ny))]);
        const lat = (Math.asin(clamp(wv[1], -1, 1)) * 180) / Math.PI;
        const lon = (Math.atan2(wv[0], wv[2]) * 180) / Math.PI;
        grid[i] = land(lat, lon) ? 1 : 0;
        let b = Math.floor((lat + 90) / 30) * 16;
        if (Math.abs(lat) < 70) b += Math.floor((lon + 180) / 30) + 1;
        band[i] = b;
      }
    }
    const isLand = (px, py) => px >= 0 && py >= 0 && px < W && py < H && grid[py * W + px] === 1;
    const onLine = (px, py) => {
      const i = py * W + px;
      const differs = (j) => {
        const a = band[i], b = band[j];
        if (b < 0) return false;
        return Math.floor(a / 16) !== Math.floor(b / 16) || (a % 16 !== 0 && b % 16 !== 0 && a % 16 !== b % 16);
      };
      return (px + 1 < W && differs(i + 1)) || (py + 1 < H && differs(i + W));
    };
    const landColor = (lit, z) => {
      lit = Math.round(lit * 10) / 10;
      z = Math.round(z * 5) / 5;
      let c = mix(C.landDark, C.landDusk, Math.min(1, lit * 3));
      if (lit > 0.33) c = mix(C.landDusk, C.landDay, Math.pow((lit - 0.33) / 0.67, 0.8));
      return mix(C.ink, c, 0.5 + 0.5 * Math.sqrt(z));
    };
    const dots = new Map();
    const put = (px, py, c, r = 1) => {
      const k = c[0] << 16 | c[1] << 8 | c[2];
      let list = dots.get(k);
      if (!list) dots.set(k, (list = []));
      list.push(px, py, r);
    };
    const thick = Math.max(3, R * 0.09);
    for (let py = 0; py < H; py++) {
      for (let px = 0; px < W; px++) {
        const dx = px + 0.5 - cx, dy = py + 0.5 - cy;
        const r = Math.hypot(dx, dy);
        const side = r > 0 ? Math.max(0, (dx * LIGHT[0] - dy * LIGHT[1]) / r) : 0;
        if (r > R + thick) continue;
        if (r > R + 0.5) {
          // Atmosphere: an ordered-dither haze, brightest sunward.
          const f = 1 - (r - R) / thick;
          if (dither(px, py, f * f * (0.18 + 0.82 * side))) put(px, py, mix(C.ink, C.accent2, 0.25 + (0.5 * Math.round(f * 4)) / 4));
          continue;
        }
        const nx = dx / R, ny = dy / R;
        const v = [nx, -ny, Math.sqrt(Math.max(0, 1 - nx * nx - ny * ny))];
        const nl = dot3(v, LIGHT);
        const lit = Math.max(0, nl);
        let day = clamp((nl + 0.12) / 0.42, 0, 1);
        day = day * day * (3 - 2 * day);
        const level = Math.round((0.1 + 0.9 * day * (0.4 + 0.6 * lit)) * Math.pow(v[2], 0.3) * 16) / 16;
        const sea = mix(C.seaNight, C.seaDay, level);
        const onLand = grid[py * W + px] === 1;
        let g = sea;
        if (onLand) g = mix(C.groundDark, C.groundDay, level);
        else {
          const spec = Math.pow(Math.max(0, dot3(v, HALF)), 24);
          if (spec > 0.04) g = mix(g, C.seaGlint, Math.round(spec * 8) / 8);
        }
        const o = (py * W + px) * 4;
        data[o] = g[0]; data[o + 1] = g[1]; data[o + 2] = g[2];
        data[o + 3] = Math.round(255 * clamp(R + 0.5 - r, 0, 1));
        if (r > R - 1) {
          put(px, py, mix(C.limb, C.accent2, ((Math.round(side * 4) / 4) * 0.6)));
        } else if (onLand) {
          if (!isLand(px - 1, py) || !isLand(px + 1, py) || !isLand(px, py - 1) || !isLand(px, py + 1)) {
            put(px, py, landColor(Math.max(lit, 0.2), v[2]));
          } else if (dither(px, py, 0.22 + 0.6 * lit)) {
            put(px, py, landColor(lit, v[2]));
          }
        } else if (onLine(px, py)) {
          put(px, py, mix(sea, C.graticule, 0.3 + 0.4 * level));
        }
      }
    }
    const project = (v) => {
      const p = toView(v);
      return [cx + p[0] * R, cy - p[1] * R, p];
    };
    // Links: great circles lifted off the surface, drawn as dots.
    for (const [li, link] of this.links.entries()) {
      const a = sphere(link.from[0], link.from[1]), b = sphere(link.to[0], link.to[1]);
      const ang = Math.acos(clamp(dot3(a, b), -1, 1));
      const n = Math.max(8, Math.round(ang * R * 1.1));
      const lift = 0.06 + 0.22 * (ang / Math.PI);
      const col = { up: C.up, warn: C.warn, down: C.down }[link.state] || C.unknown;
      const faint = link.state === 'up' ? 0.62 : link.state === 'down' ? 0.08 : 0.25;
      const head = (t * 0.32 + li * 0.37) % 1;
      for (let k = 0; k <= n; k++) {
        const u = k / n;
        if (link.state === 'down' && Math.floor(k / 2) % 2 === 1) continue;
        if (link.state === 'unknown' && k % 3 !== 0) continue;
        const s1 = Math.sin((1 - u) * ang), s2 = Math.sin(u * ang), s0 = Math.sin(ang) || 1;
        const h = 1 + lift * Math.sin(Math.PI * u);
        const p = [(a[0] * s1 + b[0] * s2) / s0 * h, (a[1] * s1 + b[1] * s2) / s0 * h, (a[2] * s1 + b[2] * s2) / s0 * h];
        const [sx, sy, vv] = project(p);
        // Hidden behind the globe?
        if (vv[2] < 0 && Math.hypot(vv[0], vv[1]) < 1) continue;
        let c = mix(col, C.ink, faint);
        if (link.state === 'up') {
          const d = Math.min(Math.abs(u - head), 1 - Math.abs(u - head));
          const glint = Math.exp(-(d * d) / 0.0016);
          if (glint > 0.05) c = mix(c, mix(col, C.glow, 0.5), glint);
        }
        put(Math.round(sx - 0.5), Math.round(sy - 0.5), c);
      }
    }
    // Places: a bright core and a slow ring.
    const marks = [];
    for (const [i, p] of this.places.entries()) {
      const [sx, sy, v] = project(sphere(p.lat, p.lon));
      if (v[2] < 0.08) continue;
      const c = p.state ? { up: C.up, warn: C.warn, down: C.down }[p.state] || C.city : C.city;
      put(sx, sy, c, 2.1);
      const grow = Math.min(1, R / 60);
      const ring = 2 + 3 * grow * ((t * 0.6 + i * 0.23) % 1);
      const steps = Math.max(12, Math.round(ring * 7));
      const rc = mix(c, C.ink, 0.3 + ring / 8);
      for (let k = 0; k < steps; k++) {
        const a2 = (2 * Math.PI * k) / steps;
        put(Math.round(sx + Math.cos(a2) * ring - 0.5), Math.round(sy + Math.sin(a2) * ring - 0.5), rc);
      }
      if (v[2] > 0.3) marks.push({ x: sx, y: sy, name: p.name, c, z: v[2] });
    }

    // Paint: the smooth ground, then every dot.
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    this.gctx.putImageData(img, 0, 0);
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = 'high';
    ctx.drawImage(this.ground, 0, 0, this.canvas.width, this.canvas.height);
    const s = pitch * dpr;
    const rad = s * 0.36;
    for (const [k, list] of dots) {
      ctx.fillStyle = `rgb(${k >> 16},${(k >> 8) & 255},${k & 255})`;
      ctx.beginPath();
      for (let j = 0; j < list.length; j += 3) {
        const x = (list[j] + 0.5) * s, y = (list[j + 1] + 0.5) * s, r = rad * list[j + 2];
        ctx.moveTo(x + r, y);
        ctx.arc(x, y, r, 0, Math.PI * 2);
      }
      ctx.fill();
    }
    if (this.labels) {
      // The page's monospace stack, whose CJK fonts follow its language.
      this.mono ??= getComputedStyle(document.documentElement).getPropertyValue('--mono').trim() || 'monospace';
      ctx.font = `600 ${Math.round(12 * dpr)}px ${this.mono}`;
      ctx.textBaseline = 'middle';
      ctx.lineJoin = 'round';
      ctx.lineWidth = 4 * dpr;
      for (const m of marks) {
        const a = clamp((m.z - 0.3) * 4, 0, 1);
        const x = (m.x + 6.5) * s, y = (m.y + 0.5) * s;
        ctx.strokeStyle = css(C.ink, 0.75 * a);
        ctx.strokeText(m.name, x, y);
        ctx.fillStyle = css(mix(m.c, C.text, 0.55), a);
        ctx.fillText(m.name, x, y);
      }
    }
  }
}

// The 5x7 face of the wordmark and its gloss.
const FACE = {
  T: ['#####', '..#..', '..#..', '..#..', '..#..', '..#..', '..#..'],
  O: ['.###.', '#...#', '#...#', '#...#', '#...#', '#...#', '.###.'],
  P: ['####.', '#...#', '#...#', '####.', '#....', '#....', '#....'],
  H: ['#...#', '#...#', '#...#', '#####', '#...#', '#...#', '#...#'],
  A: ['.###.', '#...#', '#...#', '#####', '#...#', '#...#', '#...#'],
  N: ['#...#', '##..#', '#.#.#', '#..##', '#...#', '#...#', '#...#'],
  Y: ['#...#', '#...#', '.#.#.', '..#..', '..#..', '..#..', '..#..'],
  E: ['#####', '#....', '#....', '####.', '#....', '#....', '#####'],
  R: ['####.', '#...#', '#...#', '####.', '#.#..', '#..#.', '#...#'],
  C: ['.###.', '#...#', '#....', '#....', '#....', '#...#', '.###.'],
  F: ['#####', '#....', '#....', '####.', '#....', '#....', '#....'],
  L: ['#....', '#....', '#....', '#....', '#....', '#....', '#####'],
};

// Wordmark: TOPOPHANY three dots to a pixel, an ordered-dither gradient and a
// sparse shadow, a glint that sweeps across now and then; under it the gloss.
export class Wordmark {
  constructor(canvas, { word = 'TOPOPHANY', gloss = 'THE APPEARANCE OF A PLACE' } = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.word = word;
    this.gloss = gloss;
    const s = 3;
    this.s = s;
    this.ww = (word.length * 6 - 1) * s;
    this.wh = 7 * s;
    let gw = -2;
    for (const ch of gloss) gw += ch === ' ' ? 4 : 7;
    this.gw = gw;
    this.cols = this.ww + s;
    this.rows = this.wh + s + 7 + 7;
  }

  resize(cssWidth) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.pitch = Math.min(4.6, cssWidth / this.cols);
    this.dpr = dpr;
    this.canvas.width = Math.round(this.cols * this.pitch * dpr);
    this.canvas.height = Math.round(this.rows * this.pitch * dpr);
    this.canvas.style.width = `${this.cols * this.pitch}px`;
    this.canvas.style.height = `${this.rows * this.pitch}px`;
  }

  on(x, y) {
    const fx = Math.floor(x / this.s), fy = Math.floor(y / this.s);
    if (x < 0 || y < 0 || fx >= this.word.length * 6 || fy >= 7 || fx % 6 === 5) return false;
    return FACE[this.word[Math.floor(fx / 6)]][fy][fx % 6] === '#';
  }

  // t: seconds since the page opened. Returns whether it is still moving.
  draw(t) {
    const { ctx, s, ww, wh } = this;
    const ramp = [C.glow, hex(0xa7f3e4), C.accent, hex(0x2db3a3), hex(0x1f7f78)];
    const dots = new Map();
    const put = (x, y, c) => {
      const k = c[0] << 16 | c[1] << 8 | c[2];
      let list = dots.get(k);
      if (!list) dots.set(k, (list = []));
      list.push(x, y);
    };
    // The glint crosses every nine seconds; the word dithers in on opening.
    const cycle = 9, sweep = ((t - 1.2) % cycle) / 1.8;
    const band = sweep >= 0 && sweep <= 1 ? -30 + sweep * (ww + 80) : -999;
    let moving = t < 1.6 || (sweep >= -0.05 && sweep <= 1.05);
    for (let y = 0; y < wh + s; y++) {
      for (let x = 0; x < ww + s; x++) {
        const reveal = clamp(t * 1.4 - (x / ww) * 0.9, 0, 1);
        if (this.on(x, y)) {
          if (reveal < bayer8[y & 7][x & 7]) continue;
          const v = (y / (wh - 1)) * (ramp.length - 1) + bayer8[y & 7][x & 7] - 0.5;
          let c = ramp[clamp(Math.round(v), 0, ramp.length - 1)];
          const g = Math.exp(-Math.pow(x - band - y * 0.6, 2) / 60);
          if (g > 0.05) c = mix(c, C.glow, g * 0.85);
          put(x, y, c);
        } else if (this.on(x - 2, y - 2) && reveal >= 1 && dither(x, y, 0.5)) {
          put(x, y, mix(C.accent, C.ink, 0.7));
        }
      }
    }
    let tx = Math.floor((ww - this.gw) / 2);
    const ty = wh + s + 7;
    const gc = mix(C.accent, C.ink, 0.45);
    const fade = clamp((t - 0.8) * 1.5, 0, 1);
    for (const ch of this.gloss) {
      const g = FACE[ch];
      for (let y = 0; y < 7 && g; y++) {
        for (let x = 0; x < 5; x++) {
          if (g[y][x] === '#' && fade > bayer8[(ty + y) & 7][(tx + x) & 7]) put(tx + x, ty + y, gc);
        }
      }
      tx += ch === ' ' ? 4 : 7;
    }
    const p = this.pitch * this.dpr;
    const r = p * 0.36;
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    for (const [k, list] of dots) {
      ctx.fillStyle = `rgb(${k >> 16},${(k >> 8) & 255},${k & 255})`;
      ctx.beginPath();
      for (let j = 0; j < list.length; j += 2) {
        const x = (list[j] + 0.5) * p, y = (list[j + 1] + 0.5) * p;
        ctx.moveTo(x + r, y);
        ctx.arc(x, y, r, 0, Math.PI * 2);
      }
      ctx.fill();
    }
    return moving || t < 2.4;
  }
}

// The hero art: Topophany's globe as topophany-bar's app icon and
// scripts/icon.py draw it, in ink on paper and an 8×8 ordered dither, here
// turning; and the 5×7 wordmark. Every cell is a whole number of device
// pixels, so the dither stays crisp.

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

const INK = [26, 26, 26];
const PAPER = [255, 255, 255];
const clamp = (v, lo, hi) => (v < lo ? lo : v > hi ? hi : v);
const smoothstep = (e0, e1, x) => { const t = clamp((x - e0) / (e1 - e0), 0, 1); return t * t * (3 - 2 * t); };
const RAD = Math.PI / 180;

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

const LIGHT = (() => { const v = [-0.55, 0.6, 0.58], l = Math.hypot(...v); return v.map((a) => a / l); })();

// Globe draws the world facing latitude `lat`, turning under a fixed light so
// the terminator stays where the icon has it.
export class Globe {
  constructor(canvas, { lon = 140, lat = 20 } = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.lon0 = lon;
    this.tilt = lat * RAD;
    this.drag = 0;
    this.cells = document.createElement('canvas');
    this.cctx = this.cells.getContext('2d');
  }

  // resize lays the globe out in cells of about `cell` CSS pixels and works
  // out what turning does not change: where each cell's four samples fall
  // with the globe at rest, and whether the cell is ink over land or over sea.
  resize(cssSize, cell) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const px = Math.max(1, Math.round(cell * dpr));
    const N = Math.max(32, Math.floor((cssSize * dpr) / px));
    const R = N * 0.46;
    Object.assign(this, { px, N });
    this.canvas.width = this.canvas.height = N * px;
    this.canvas.style.width = this.canvas.style.height = `${(N * px) / dpr}px`;
    this.cells.width = this.cells.height = N;
    this.img = this.cctx.createImageData(N, N);
    const st = Math.sin(this.tilt), ct = Math.cos(this.tilt);
    this.inside = new Uint8Array(N * N);
    this.overLand = new Uint8Array(N * N);
    this.overSea = new Uint8Array(N * N);
    this.rows = new Int32Array(N * N * 4);
    this.lons = new Float32Array(N * N * 4);
    for (let j = 0; j < N; j++) {
      for (let i = 0; i < N; i++) {
        const c = j * N + i;
        const x = (i + 0.5 - N / 2) / R, y = (N / 2 - j - 0.5) / R;
        const r2 = x * x + y * y;
        if (r2 >= 1) continue;
        const z = Math.sqrt(1 - r2);
        this.inside[c] = 1;
        // By day the sea is a checkerboard and the land paper, each a little
        // darker away from the light; night falls into solid ink, and ink
        // draws the limb.
        const nl = x * LIGHT[0] + y * LIGHT[1] + z * LIGHT[2];
        const shade = 1 - Math.max(0, nl), day = smoothstep(-0.12, 0.45, nl);
        const rim = smoothstep(0.45, 0.85, 1 - z);
        const t = bayer8[j & 7][i & 7];
        this.overLand[c] = Math.max(0.15 * shade * day + (1 - day), rim) > t;
        this.overSea[c] = Math.max((0.5 + 0.1 * shade) * day + (1 - day), rim) > t;
        for (let k = 0; k < 4; k++) {
          const sx = x + ((k & 1) - 0.5) / 2 / R, sy = y + ((k >> 1) - 0.5) / 2 / R;
          const sz = Math.sqrt(Math.max(0, 1 - sx * sx - sy * sy));
          const lat = Math.asin(clamp(sz * st + sy * ct, -1, 1)) / RAD;
          this.rows[c * 4 + k] = Math.min(359, Math.max(0, Math.floor((90 - lat) * 2))) * 720;
          this.lons[c * 4 + k] = Math.atan2(sx, sz * ct - sy * st) / RAD;
        }
      }
    }
  }

  // t: seconds since the page opened; the world turns once in 90 of them.
  draw(t) {
    const { N, px, inside, overLand, overSea, rows, lons, ctx } = this;
    if (!N) return;
    const facing = this.lon0 - t * 4 - this.drag;
    const data = this.img.data;
    for (let c = 0; c < N * N; c++) {
      const o = c * 4;
      if (!inside[c]) { data[o + 3] = 0; continue; }
      // Land where most of the cell is.
      let hits = 0;
      for (let k = c * 4; k < c * 4 + 4; k++) {
        let l = (lons[k] + facing + 180) % 360;
        if (l < 0) l += 360;
        const b = rows[k] + Math.min(719, Math.floor(l * 2));
        if (landBits && landBits[b >> 3] & (0x80 >> (b & 7))) hits++;
      }
      const col = (hits >= 2 ? overLand[c] : overSea[c]) ? INK : PAPER;
      data[o] = col[0]; data[o + 1] = col[1]; data[o + 2] = col[2]; data[o + 3] = 255;
    }
    this.cctx.putImageData(this.img, 0, 0);
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(this.cells, 0, 0, N * px, N * px);
  }
}

// The 5×7 face of the wordmark and its gloss.
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

// Wordmark: TOPOPHANY in solid ink, three cells to a pixel of the face, over
// a shadow in the dither's half tone, and under it the gloss, a cell to a
// pixel, as scripts/icon.py draws the social card. It develops on opening,
// cell by cell in the dither's order, and then holds still.
export class Wordmark {
  constructor(canvas, { word = 'TOPOPHANY', gloss = 'THE APPEARANCE OF A PLACE' } = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    const s = 3;
    const inked = new Set();
    // A letter takes `advance` pixels of the face, and a space four.
    const letters = (text, scale, advance, y0) => {
      let x = 0;
      for (const ch of text) {
        if (ch === ' ') { x += 4 * scale; continue; }
        FACE[ch].forEach((line, fy) => [...line].forEach((c, fx) => {
          if (c !== '#') return;
          for (let v = 0; v < scale; v++) for (let u = 0; u < scale; u++) inked.add(`${x + fx * scale + u},${y0 + fy * scale + v}`);
        }));
        x += advance * scale;
      }
      return x - (advance - 5) * scale;
    };
    const ww = letters(word, s, 6, 0), wh = 7 * s;
    const cells = [];
    for (let y = 0; y < wh + s; y++) {
      for (let x = 0; x < ww + s; x++) {
        if (inked.has(`${x},${y}`)) cells.push(x, y);
        else if (inked.has(`${x - s + 1},${y - s + 1}`) && dither(x, y, 0.5)) cells.push(x, y);
      }
    }
    // The gloss is spaced out to about the word's width, as the banner sets it.
    const gy = wh + s + 6;
    inked.clear();
    const gw = letters(gloss, 1, 7, gy);
    for (const key of inked) { const [x, y] = key.split(',').map(Number); cells.push(x, y); }
    this.list = cells;
    this.cols = Math.max(ww + s, gw);
    this.rows = gy + 7;
  }

  resize(cssWidth) {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.px = clamp(Math.floor((cssWidth * dpr) / this.cols), 1, Math.round(3.5 * dpr));
    this.canvas.width = this.cols * this.px;
    this.canvas.height = this.rows * this.px;
    this.canvas.style.width = `${(this.cols * this.px) / dpr}px`;
    this.canvas.style.height = `${(this.rows * this.px) / dpr}px`;
  }

  // t: seconds since the page opened. Returns whether it is still developing.
  draw(t) {
    const { ctx, px, list, cols } = this;
    ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
    ctx.fillStyle = 'rgb(26,26,26)';
    ctx.beginPath();
    for (let k = 0; k < list.length; k += 2) {
      const x = list[k], y = list[k + 1];
      if (clamp(t * 1.5 - (x / cols) * 0.6, 0, 1) < bayer8[y & 7][x & 7]) continue;
      ctx.rect(x * px, y * px, px, px);
    }
    ctx.fill();
    return t * 1.5 - 0.6 < 1;
  }
}

// A terminal screen on a canvas. Cells are drawn the way a good terminal
// emulator draws them: text from the system monospace font, but box drawing,
// blocks, braille and the status glyphs as geometry, so lines join without
// gaps and dots stay round on every platform and at every size.

// The page's monospace stack (--mono), whose CJK fonts follow its language.
const FONT = getComputedStyle(document.documentElement).getPropertyValue('--mono').trim()
  || 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, "Cascadia Mono", "DejaVu Sans Mono", "Noto Sans Mono", monospace, sans-serif';
const TEXT = '#d5dee7';
const CONT = '\u0001'; // the right half of a wide character

// Box drawing: arms up, right, down, left; 1 light, 2 heavy.
const BOX = {
  '─': [0, 1, 0, 1], '━': [0, 2, 0, 2], '│': [1, 0, 1, 0], '┃': [2, 0, 2, 0],
  '┌': [0, 1, 1, 0], '┐': [0, 0, 1, 1], '└': [1, 1, 0, 0], '┘': [1, 0, 0, 1],
  '├': [1, 1, 1, 0], '┤': [1, 0, 1, 1], '┬': [0, 1, 1, 1], '┴': [1, 1, 0, 1], '┼': [1, 1, 1, 1],
  '╴': [0, 0, 0, 1], '╵': [1, 0, 0, 0], '╶': [0, 1, 0, 0], '╷': [0, 0, 1, 0],
};
const DASH = { '╌': [0, 2], '┄': [0, 3], '┈': [0, 4], '╎': [1, 2], '┆': [1, 3], '┊': [1, 4] };
const ARC = { '╭': [1, 1], '╮': [-1, 1], '╯': [-1, -1], '╰': [1, -1] };

const hex = (n) => '#' + n.toString(16).padStart(6, '0');

export class Screen {
  constructor(canvas, cols, rows, styles) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.cols = cols;
    this.rows = rows;
    this.chars = new Array(cols * rows).fill(' ');
    this.style = new Int32Array(cols * rows);
    this.styles = styles.map(([fg, bg, attr]) => ({
      fg: fg < 0 ? TEXT : hex(fg), bg: hex(bg), bold: !!(attr & 1), italic: !!(attr & 4), underline: !!(attr & 8),
    }));
    this.dirty = new Uint8Array(cols * rows).fill(1);
    this.resize();
  }

  // resize fits the canvas to its box: cells are twice as tall as wide, so
  // braille dots form a square grid.
  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    const cssW = this.canvas.clientWidth || this.canvas.parentElement.clientWidth;
    const w = Math.max(1, Math.round(cssW * dpr));
    const cw = w / this.cols;
    const h = Math.round(cw * 2 * this.rows);
    if (w === this.canvas.width && h === this.canvas.height) return false;
    this.canvas.width = w;
    this.canvas.height = h;
    this.cw = cw;
    this.ch = cw * 2;
    const c = this.ctx;
    c.font = `100px ${FONT}`;
    const adv = c.measureText('M').width / 100 || 0.6;
    this.px = Math.max(4, Math.min(this.cw / adv, this.ch * 0.86));
    c.font = `${this.px}px ${FONT}`;
    const m = c.measureText('H');
    this.capH = m.actualBoundingBoxAscent || this.px * 0.7;
    this.line = Math.max(1, Math.round(this.cw * 0.11));
    this.fontKey = '';
    this.dirty.fill(1);
    return true;
  }

  // apply writes one recorded frame: [y, x, text, [style, count, ...]].
  apply(ops) {
    for (const [y, x0, text, runs] of ops) {
      let i = y * this.cols + x0;
      let r = 0, left = runs[1];
      for (const chr of text) {
        this.chars[i] = chr;
        this.style[i] = runs[r];
        this.dirty[i] = 1;
        i++;
        if (--left === 0) { r += 2; left = runs[r + 1]; }
      }
    }
  }

  paint() {
    const { cols, rows, chars, dirty } = this;
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < cols; x++) {
        const i = y * cols + x;
        if (!dirty[i]) continue;
        let k = x;
        // A dirty right half repaints its wide character.
        if (chars[i] === CONT && x > 0) k = x - 1;
        this.cell(k, y);
        dirty[i] = 0;
        if (k + 1 < cols && chars[y * cols + k + 1] === CONT) dirty[y * cols + k + 1] = 0;
      }
    }
  }

  // Cell edges are rounded to device pixels so neighbours meet exactly.
  edges(x, y, span = 1) {
    return [Math.round(x * this.cw), Math.round(y * this.ch), Math.round((x + span) * this.cw), Math.round((y + 1) * this.ch)];
  }

  cell(x, y) {
    const i = y * this.cols + x;
    const chr = this.chars[i];
    const st = this.styles[this.style[i]];
    const wide = x + 1 < this.cols && this.chars[i + 1] === CONT;
    const [x0, y0, x1, y1] = this.edges(x, y, wide ? 2 : 1);
    const c = this.ctx;
    c.fillStyle = st.bg;
    c.fillRect(x0, y0, x1 - x0, y1 - y0);
    if (chr === ' ' || chr === CONT) return;
    c.fillStyle = st.fg;
    c.strokeStyle = st.fg;
    const code = chr.codePointAt(0);
    if (code >= 0x2800 && code <= 0x28ff) return this.braille(code - 0x2800, x0, y0, x1, y1);
    if (this.geometry(chr, x0, y0, x1, y1, st)) return;
    const key = (st.italic ? 'italic ' : '') + (st.bold ? '600 ' : '400 ');
    if (key !== this.fontKey) {
      c.font = `${key}${this.px}px ${FONT}`;
      this.fontKey = key;
    }
    c.textAlign = 'center';
    c.textBaseline = 'alphabetic';
    c.fillText(chr, (x0 + x1) / 2, (y0 + y1) / 2 + this.capH / 2, (x1 - x0) * 1.15);
    if (st.underline) c.fillRect(x0, Math.round((y0 + y1) / 2 + this.capH / 2 + this.line * 2), x1 - x0, this.line);
  }

  braille(bits, x0, y0, x1, y1) {
    const c = this.ctx;
    const w = x1 - x0, h = y1 - y0;
    const r = Math.max(0.6, Math.min(w / 2, h / 4) * 0.36);
    // Dot order: 1 2 3 7 down the left column, 4 5 6 8 down the right.
    const pos = [[0, 0], [0, 1], [0, 2], [1, 0], [1, 1], [1, 2], [0, 3], [1, 3]];
    c.beginPath();
    for (let b = 0; b < 8; b++) {
      if (!(bits & (1 << b))) continue;
      const [cx, cy] = pos[b];
      const px = x0 + w * (cx * 0.5 + 0.25), py = y0 + h * (cy * 0.25 + 0.125);
      c.moveTo(px + r, py);
      c.arc(px, py, r, 0, Math.PI * 2);
    }
    c.fill();
  }

  // geometry draws the glyphs a font would get wrong; false leaves the
  // character to the font.
  geometry(chr, x0, y0, x1, y1) {
    const c = this.ctx;
    const w = x1 - x0, h = y1 - y0;
    const t = this.line;
    // Line centres, aligned to the pixel grid identically in every cell.
    const vx = x0 + Math.floor((w - t) / 2), hy = y0 + Math.floor((h - t) / 2);
    const box = BOX[chr];
    if (box) {
      const [u, r, d, l] = box;
      const th = (k) => (k === 2 ? t * 2 : t);
      const off = (k) => (k === 2 ? Math.floor(t / 2) : 0);
      const wide = Math.max(th(u), th(d)), tall = Math.max(th(l), th(r));
      if (u) c.fillRect(vx - off(u), y0, th(u), hy - y0 + tall);
      if (d) c.fillRect(vx - off(d), hy, th(d), y1 - hy);
      if (l) c.fillRect(x0, hy - off(l), vx - x0 + wide, th(l));
      if (r) c.fillRect(vx, hy - off(r), x1 - vx, th(r));
      return true;
    }
    const dash = DASH[chr];
    if (dash) {
      const [vertical, n] = dash;
      const len = (vertical ? h : w) / n;
      for (let k = 0; k < n; k++) {
        const a = Math.round(k * len + len * 0.2), b = Math.round(k * len + len * 0.8);
        if (vertical) c.fillRect(vx, y0 + a, t, b - a);
        else c.fillRect(x0 + a, hy, b - a, t);
      }
      return true;
    }
    const arc = ARC[chr];
    if (arc) {
      const [sx, sy] = arc;
      const cx = vx + t / 2, cy = hy + t / 2;
      const ex = sx > 0 ? x1 : x0, ey = sy > 0 ? y1 : y0;
      const r = Math.min(Math.abs(ex - cx), Math.abs(ey - cy));
      c.lineWidth = t;
      c.lineCap = 'butt';
      c.beginPath();
      c.moveTo(ex + sx, cy);
      c.lineTo(cx + sx * r, cy);
      c.arcTo(cx, cy, cx, cy + sy * r, r);
      c.lineTo(cx, ey + sy);
      c.stroke();
      return true;
    }
    const mid = y0 + Math.round(h / 2), cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
    const s = Math.min(w, h / 2);
    switch (chr) {
      case '█': c.fillRect(x0, y0, w, h); return true;
      case '▀': c.fillRect(x0, y0, w, mid - y0); return true;
      case '▄': c.fillRect(x0, mid, w, y1 - mid); return true;
      case '▌': c.fillRect(x0, y0, Math.round(w / 2), h); return true;
      case '▐': c.fillRect(x0 + Math.round(w / 2), y0, w - Math.round(w / 2), h); return true;
      case '▎': c.fillRect(x0, y0, Math.max(1, Math.round(w / 4)), h); return true;
      case '▏': c.fillRect(x0, y0, Math.max(1, Math.round(w / 8)), h); return true;
      case '╱':
        c.lineWidth = t;
        c.beginPath(); c.moveTo(x1, y0); c.lineTo(x0, y1); c.stroke();
        return true;
      case '●': disc(c, cx, cy, s * 0.36); return true;
      case '○': ring(c, cx, cy, s * 0.32, t); return true;
      case '◉': ring(c, cx, cy, s * 0.38, t); disc(c, cx, cy, s * 0.2); return true;
      case '◎': ring(c, cx, cy, s * 0.38, t); ring(c, cx, cy, s * 0.17, t); return true;
      case '■': c.fillRect(cx - s * 0.3, cy - s * 0.3, s * 0.6, s * 0.6); return true;
      case '◆': poly(c, [[cx, cy - s * 0.42], [cx + s * 0.38, cy], [cx, cy + s * 0.42], [cx - s * 0.38, cy]]); return true;
      case '▲': poly(c, [[cx, cy - s * 0.38], [cx + s * 0.42, cy + s * 0.32], [cx - s * 0.42, cy + s * 0.32]]); return true;
      case '▸': poly(c, [[cx - s * 0.24, cy - s * 0.3], [cx + s * 0.3, cy], [cx - s * 0.24, cy + s * 0.3]]); return true;
      case '◂': poly(c, [[cx + s * 0.24, cy - s * 0.3], [cx - s * 0.3, cy], [cx + s * 0.24, cy + s * 0.3]]); return true;
      case '✖': {
        const a = s * 0.3;
        c.lineWidth = Math.max(t * 1.6, s * 0.16);
        c.lineCap = 'round';
        c.beginPath();
        c.moveTo(cx - a, cy - a); c.lineTo(cx + a, cy + a);
        c.moveTo(cx + a, cy - a); c.lineTo(cx - a, cy + a);
        c.stroke();
        return true;
      }
      case '✓': {
        c.lineWidth = Math.max(t * 1.4, s * 0.12);
        c.lineCap = 'round';
        c.lineJoin = 'round';
        c.beginPath();
        c.moveTo(cx - s * 0.32, cy); c.lineTo(cx - s * 0.08, cy + s * 0.26); c.lineTo(cx + s * 0.34, cy - s * 0.3);
        c.stroke();
        return true;
      }
      case '⚑': {
        c.fillRect(cx - s * 0.3, cy - s * 0.42, Math.max(1, t), s * 0.86);
        poly(c, [[cx - s * 0.3, cy - s * 0.42], [cx + s * 0.36, cy - s * 0.24], [cx - s * 0.3, cy - s * 0.02]]);
        return true;
      }
    }
    return false;
  }
}

function disc(c, x, y, r) {
  c.beginPath();
  c.arc(x, y, r, 0, Math.PI * 2);
  c.fill();
}

function ring(c, x, y, r, t) {
  c.lineWidth = t;
  c.beginPath();
  c.arc(x, y, r, 0, Math.PI * 2);
  c.stroke();
}

function poly(c, pts) {
  c.beginPath();
  c.moveTo(pts[0][0], pts[0][1]);
  for (const [x, y] of pts.slice(1)) c.lineTo(x, y);
  c.closePath();
  c.fill();
}

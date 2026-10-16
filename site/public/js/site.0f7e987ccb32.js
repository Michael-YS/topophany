import { Screen } from './term.48019be56a9f.js';
import { Globe, Wordmark, loadLand, land, bayer8 } from './globe.e04feafd00bd.js';

const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];

// The page's words for its scripts, written into it in its language
// (scripts/build_site.py); a missing word reads as its key.
const STRINGS = (() => {
  try { return JSON.parse($('#strings').textContent); } catch { return {}; }
})();
const t = (key, vars = {}) => (STRINGS[key] ?? key).replace(/\{(\w+)\}/g, (m, k) => vars[k] ?? m);

// Natural Earth land, for the globe and the vantage map alike, from the URL
// the page preloads it at.
const landLoaded = loadLand($('link[rel="preload"][as="fetch"]').href);

// ---------------------------------------------------------------- nav

const nav = $('.nav');
const onScroll = () => nav.classList.toggle('scrolled', scrollY > 24);
addEventListener('scroll', onScroll, { passive: true });
onScroll();

// ---------------------------------------------------------------- hero

// The About tab's reference places, named in the page's language; links
// carry the map's evidence colours.
const PLACES = [
  { id: 'shanghai', lat: 31.2, lon: 121.5 }, { id: 'tokyo', lat: 35.7, lon: 139.7 },
  { id: 'singapore', lat: 1.35, lon: 103.8 }, { id: 'frankfurt', lat: 50.1, lon: 8.7 },
  { id: 'london', lat: 51.5, lon: -0.1 }, { id: 'ashburn', lat: 39.0, lon: -77.5 },
  { id: 'los-angeles', lat: 34.05, lon: -118.25 }, { id: 'sao-paulo', lat: -23.5, lon: -46.6 },
  { id: 'sydney', lat: -33.9, lon: 151.2 }, { id: 'mumbai', lat: 19.1, lon: 72.9 },
].map((p) => ({ ...p, name: t(`place.${p.id}`) }));
const at = (id) => { const p = PLACES.find((q) => q.id === id); return [p.lat, p.lon]; };
const LINKS = [
  ['shanghai', 'tokyo', 'up'], ['shanghai', 'los-angeles', 'down'], ['tokyo', 'los-angeles', 'up'],
  ['singapore', 'sydney', 'up'], ['singapore', 'mumbai', 'up'], ['mumbai', 'frankfurt', 'warn'],
  ['frankfurt', 'london', 'up'], ['london', 'ashburn', 'up'], ['ashburn', 'sao-paulo', 'unknown'],
  ['los-angeles', 'ashburn', 'up'], ['shanghai', 'singapore', 'up'],
].map(([a, b, state]) => ({ from: at(a), to: at(b), state }));

function hero() {
  const heroEl = $('.hero');
  const sky = $('.sky');
  const sctx = sky.getContext('2d');
  const wordmark = new Wordmark($('.wordmark'));
  const globeEl = $('.globe');
  const globe = new Globe(globeEl, { places: PLACES, links: LINKS, lon: 121.5 });
  let stars = [];
  let landReady = false;

  const layout = () => {
    const dpr = Math.min(devicePixelRatio || 1, 2);
    const r = heroEl.getBoundingClientRect();
    sky.width = Math.round(r.width * dpr);
    sky.height = Math.round(r.height * dpr);
    // Sparse stars, fixed per layout, each with its own twinkle.
    let seed = 7;
    const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
    const n = Math.round((r.width * r.height) / 5200);
    stars = Array.from({ length: n }, () => ({
      x: rnd() * sky.width, y: rnd() * sky.height, r: (0.5 + rnd() * 0.9) * dpr,
      b: 0.15 + rnd() * 0.5, s: 0.4 + rnd() * 1.4, p: rnd() * 6.28,
    }));
    wordmark.resize($('.hero-copy').clientWidth);
    const size = globeEl.clientWidth || 480;
    globe.resize(size, Math.max(3.4, Math.min(5.2, size / 116)));
  };

  const drawSky = (t) => {
    sctx.clearRect(0, 0, sky.width, sky.height);
    for (const s of stars) {
      const tw = 0.5 + 0.5 * Math.sin(t * s.s + s.p);
      sctx.fillStyle = `rgba(131,147,163,${(s.b * (0.35 + 0.65 * tw)).toFixed(3)})`;
      sctx.beginPath();
      sctx.arc(s.x, s.y, s.r, 0, 6.283);
      sctx.fill();
    }
  };

  // Drag turns the world, and lets go with a little momentum.
  let dragging = false, lastX = 0, vel = 0;
  globeEl.addEventListener('pointerdown', (e) => {
    dragging = true; lastX = e.clientX; vel = 0;
    globeEl.setPointerCapture(e.pointerId);
    globeEl.classList.add('dragging');
  });
  globeEl.addEventListener('pointermove', (e) => {
    if (!dragging) return;
    const dx = e.clientX - lastX;
    lastX = e.clientX;
    vel = dx * 0.006;
    globe.drag += vel;
    if (reduced) globe.draw(0);
  });
  const release = () => { dragging = false; globeEl.classList.remove('dragging'); };
  globeEl.addEventListener('pointerup', release);
  globeEl.addEventListener('pointercancel', release);

  layout();
  let resizeTimer;
  new ResizeObserver(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { layout(); if (reduced) still(); }, 60);
  }).observe(heroEl);

  const still = () => {
    drawSky(0);
    wordmark.draw(6);
    if (landReady) globe.draw(0);
  };

  landLoaded.then(() => {
    landReady = true;
    globeEl.classList.add('ready');
    if (reduced) still();
  });

  if (reduced) { still(); return; }

  let visible = true, start = performance.now(), last = 0;
  new IntersectionObserver(([e]) => { visible = e.isIntersecting; if (visible) requestAnimationFrame(frame); }).observe(heroEl);
  function frame(now) {
    if (!visible || document.hidden) return;
    requestAnimationFrame(frame);
    if (now - last < 33) return; // 30 fps is plenty for a slow world
    last = now;
    const t = (now - start) / 1000;
    if (!dragging && Math.abs(vel) > 0.0002) { globe.drag += vel; vel *= 0.94; }
    drawSky(t);
    wordmark.draw(t);
    if (landReady) globe.draw(t);
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) requestAnimationFrame(frame); });
  requestAnimationFrame(frame);
}

// ---------------------------------------------------------------- tour

class Player {
  constructor(root, data) {
    this.root = root;
    this.data = data;
    this.canvas = $('#term-canvas', root);
    // The box keeps the recording's shape before and after it paints: cells
    // are twice as tall as wide.
    this.canvas.parentElement.style.aspectRatio = `${data.size[0]} / ${data.size[1] * 2}`;
    this.screen = new Screen(this.canvas, data.size[0], data.size[1], data.styles);
    this.buttons = $$('#chapters button');
    this.chapters = data.chapters.map((c) => ({ ...c, total: c.frames.reduce((s, f) => s + f.d, 0) }));
    this.index = new Map(this.chapters.map((c, i) => [c.id, i]));
    this.ci = 0;
    this.fi = 0;
    this.acc = 0;
    this.played = 0;
    this.playing = !reduced;
    this.visible = false;
    this.last = 0;
    this.title = $('#cap-title');
    this.text = $('#cap-text');
    this.toastEl = null;

    this.buttons.forEach((b) => b.addEventListener('click', () => {
      const i = this.index.get(b.dataset.chapter);
      if (i !== undefined) this.seek(i, true);
    }));
    $('.term-play', root).addEventListener('click', () => this.toggle());
    this.canvas.addEventListener('keydown', (e) => this.key(e));
    this.canvas.addEventListener('click', (e) => this.click(e));
    new ResizeObserver(() => { if (this.screen.resize()) this.screen.paint(); }).observe(this.canvas.parentElement);
    new IntersectionObserver(([e]) => {
      this.visible = e.isIntersecting;
      if (this.visible) this.kick();
    }, { threshold: 0.2 }).observe(root);
    document.addEventListener('visibilitychange', () => this.kick());

    root.classList.add('loaded');
    if (reduced) {
      // A still: the outage, settled.
      this.seek(0);
      const ch = this.chapters[0];
      for (let i = 1; i < ch.frames.length; i++) this.screen.apply(ch.frames[i].ops);
      this.fi = ch.frames.length - 1;
      this.screen.paint();
      this.setPlaying(false);
    } else {
      this.seek(0);
    }
  }

  seek(i, user = false) {
    this.ci = (i + this.chapters.length) % this.chapters.length;
    this.fi = 0;
    this.acc = 0;
    this.played = 0;
    const ch = this.chapters[this.ci];
    this.screen.apply(ch.frames[0].ops);
    this.screen.paint();
    this.buttons.forEach((b, k) => {
      const on = b.dataset.chapter === ch.id;
      if (on) b.setAttribute('aria-current', 'step'); else b.removeAttribute('aria-current');
      b.parentElement.classList.toggle('done', k < this.ci);
      $('.ch-bar i', b).style.width = '';
      if (on) {
        this.title.textContent = b.dataset.title;
        this.text.textContent = b.dataset.text;
        this.canvas.setAttribute('aria-label', t('recording', { title: b.dataset.title, text: b.dataset.text }));
        const strip = b.closest('.chapters');
        if (strip.scrollWidth > strip.clientWidth) strip.scrollTo({ left: b.parentElement.offsetLeft - 8, behavior: reduced ? 'auto' : 'smooth' });
      }
    });
    if (user && !this.playing && !reduced) this.setPlaying(true);
    this.progress();
  }

  progress() {
    const ch = this.chapters[this.ci];
    const b = this.buttons.find((x) => x.dataset.chapter === ch.id);
    if (b) $('.ch-bar i', b).style.width = `${Math.min(100, ((this.played + this.acc) / ch.total) * 100).toFixed(2)}%`;
  }

  step() {
    const ch = this.chapters[this.ci];
    this.played += ch.frames[this.fi].d;
    this.fi++;
    if (this.fi >= ch.frames.length) return this.seek(this.ci + 1);
    this.screen.apply(ch.frames[this.fi].ops);
  }

  kick() {
    if (this.playing && this.visible && !document.hidden && !this.raf) {
      this.last = performance.now();
      this.raf = requestAnimationFrame((t) => this.tick(t));
    }
  }

  tick(now) {
    this.raf = 0;
    if (!this.playing || !this.visible || document.hidden) return;
    this.acc += Math.min(250, now - this.last);
    this.last = now;
    let moved = false;
    for (let guard = 0; guard < 40; guard++) {
      const d = this.chapters[this.ci].frames[this.fi].d;
      if (this.acc < d) break;
      this.acc -= d;
      this.step();
      moved = true;
    }
    if (moved) this.screen.paint();
    this.progress();
    this.kick();
  }

  setPlaying(on) {
    this.playing = on;
    this.root.classList.toggle('paused', !on);
    const btn = $('.term-play', this.root);
    btn.setAttribute('aria-label', t(on ? 'pause' : 'play'));
    btn.setAttribute('aria-pressed', String(!on));
    $('.term-state', this.root).textContent = t(on ? 'replay' : 'paused');
    this.kick();
  }

  toggle() { this.setPlaying(!this.playing); }

  // Numbers pick a chapter; the TUI's own keys pick the chapter that shows them.
  key(e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const id = this.chapters[this.ci].id;
    const maps = ['virtual', 'geo', 'network'];
    const go = (name) => { this.seek(this.index.get(name), true); e.preventDefault(); };
    const k = e.key;
    if (k >= '1' && k <= '9' && +k <= this.chapters.length) return go(this.chapters[+k - 1].id);
    switch (k) {
      case ' ': this.toggle(); e.preventDefault(); return;
      case 'm': return go(maps[(maps.indexOf(id) + 1) % maps.length]);
      case 'v': return go('vantage');
      case 'x': return go('outage');
      case 'Enter': return go(id === 'incidents' ? 'incidents' : 'route');
      case '?': return go('about');
      case 'ArrowRight': return go(this.chapters[(this.ci + 1) % this.chapters.length].id);
      case 'ArrowLeft': return go(this.chapters[(this.ci - 1 + this.chapters.length) % this.chapters.length].id);
    }
  }

  click(e) {
    const r = this.canvas.getBoundingClientRect();
    const [cols, rows] = this.data.size;
    const x = Math.floor(((e.clientX - r.left) / r.width) * cols);
    const y = Math.floor(((e.clientY - r.top) / r.height) * rows);
    if (y !== 1) return;
    const tab = this.data.tabs.findIndex(([tx, tw]) => x >= tx && x < tx + tw);
    if (tab < 0) return;
    const i = this.chapters.findIndex((c) => c.tab === tab);
    if (i >= 0) this.seek(i, true); else this.toast(tab);
  }

  // toast says a tab is not recorded, by the name the TUI gives it.
  toast(tab) {
    if (!this.toastEl) {
      this.toastEl = document.createElement('div');
      this.toastEl.className = 'term-toast';
      this.toastEl.setAttribute('role', 'status');
      $('.term-screen', this.root).append(this.toastEl);
    }
    this.toastEl.innerHTML = '';
    const b = document.createElement('b');
    b.textContent = this.data.names?.[tab] ?? String(tab + 1);
    const code = document.createElement('code');
    code.textContent = 'topo preview';
    const parts = { tab: b, cmd: code };
    const words = t('missing').split(/(\{\w+\})/).filter((s) => s !== '');
    this.toastEl.append(...words.map((s) => (/^\{\w+\}$/.test(s) && parts[s.slice(1, -1)]) || s));
    this.toastEl.classList.add('on');
    clearTimeout(this.toastTimer);
    this.toastTimer = setTimeout(() => this.toastEl.classList.remove('on'), 2600);
  }
}

function tour() {
  const root = $('#term');
  let started = false;
  const start = async () => {
    if (started) return;
    started = true;
    try {
      const res = await fetch(root.dataset.tour);
      const data = await res.json();
      new Player(root, data);
    } catch (err) {
      $('.term-loading', root).lastChild.textContent = t('failed');
      console.error(err);
    }
  };
  new IntersectionObserver(([e], io) => {
    if (e.isIntersecting) { io.disconnect(); start(); }
  }, { rootMargin: '900px 0px' }).observe(root);
}

// ---------------------------------------------------------------- dot numerals

const DIGITS = {
  0: ['.###.', '#...#', '#...#', '#...#', '#...#', '#...#', '.###.'],
  1: ['..#..', '.##..', '..#..', '..#..', '..#..', '..#..', '.###.'],
  2: ['.###.', '#...#', '....#', '...#.', '..#..', '.#...', '#####'],
  3: ['#####', '...#.', '..#..', '...#.', '....#', '#...#', '.###.'],
  4: ['...#.', '..##.', '.#.#.', '#..#.', '#####', '...#.', '...#.'],
  5: ['#####', '#....', '####.', '....#', '....#', '#...#', '.###.'],
  6: ['..##.', '.#...', '#....', '####.', '#...#', '#...#', '.###.'],
  7: ['#####', '....#', '...#.', '..#..', '.#...', '.#...', '.#...'],
  8: ['.###.', '#...#', '#...#', '.###.', '#...#', '#...#', '.###.'],
  9: ['.###.', '#...#', '#...#', '.####', '....#', '...#.', '.##..'],
};
const RAMP = ['#f0fdfa', '#a7f3e4', '#5eead4', '#2db3a3', '#1f7f78'];

function dotNumerals() {
  for (const el of $$('[data-dots]')) {
    const s = el.dataset.dots;
    const sub = 2; // two dots to a pixel of the face
    const w = (s.length * 6 - 1) * sub, h = 7 * sub;
    let out = '';
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        const fx = Math.floor(x / sub), fy = Math.floor(y / sub);
        if (fx % 6 === 5) continue;
        const g = DIGITS[s[Math.floor(fx / 6)]];
        const on = g && g[fy][fx % 6] === '#';
        if (on) {
          const v = (y / (h - 1)) * (RAMP.length - 1) + bayer8[y & 7][x & 7] - 0.5;
          const c = RAMP[Math.max(0, Math.min(RAMP.length - 1, Math.round(v)))];
          out += `<circle cx="${x * 4 + 2}" cy="${y * 4 + 2}" r="1.45" fill="${c}"/>`;
        } else {
          out += `<circle cx="${x * 4 + 2}" cy="${y * 4 + 2}" r=".7" fill="#1a2631"/>`;
        }
      }
    }
    el.innerHTML = `<svg viewBox="0 0 ${w * 4} ${h * 4}" aria-hidden="true">${out}</svg>`;
  }
}

// ---------------------------------------------------------------- evidence

// vantageMap lays the land under the vantage panel's observers as dots, on
// the projection its places and paths were drawn in.
function vantageMap() {
  const g = $('.v-land');
  if (!g) return;
  const [lon, lat, scale, top, bottom] = ['lon', 'lat', 'scale', 'top', 'bottom'].map((k) => +g.dataset[k]);
  const width = g.ownerSVGElement.viewBox.baseVal.width;
  const pitch = 5.2; // units between dots, whatever the scale
  landLoaded.then(() => {
    let out = '';
    for (let y = top + pitch / 2; y < bottom; y += pitch) {
      for (let x = pitch / 2; x < width; x += pitch) {
        if (land(lat - (y - top) / scale, lon + x / scale)) out += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="1.35"/>`;
      }
    }
    g.innerHTML = out;
  }).catch(() => {});
}

// replays plays each sequence diagram once, the first time it is seen. It
// is armed here, so a page without its script shows the whole exchange.
function replays() {
  if (reduced) return;
  for (const seq of $$('.seq')) {
    seq.classList.add('armed');
    new IntersectionObserver(([e], io) => {
      if (e.isIntersecting) { io.disconnect(); seq.classList.add('play'); }
    }, { threshold: 0.6 }).observe(seq);
  }
}

// strips draw recent samples as a check's evidence does in the TUI: one
// slot per sample coloured by its outcome (o passed, x failed, . none
// taken), over a bar for what each passing sample took.
function strips() {
  for (const svg of $$('svg[data-strip]')) {
    const slots = [...svg.dataset.strip];
    let seed = 11;
    const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
    const took = slots.map((c) => (c === 'o' ? Math.round(158 + rnd() * 34 + (rnd() < 0.1 ? rnd() * 80 : 0)) : 0));
    const slowest = Math.max(...took);
    const w = 12, h = 46;
    let out = '';
    slots.forEach((c, i) => {
      const x = i * w + w / 2;
      if (c === 'o') out += `<circle cx="${x}" cy="7" r="3.3" fill="#4ade80"/>`;
      else if (c === 'x') out += `<path d="M${x - 3} 4l6 6m0-6l-6 6" stroke="#f87171" stroke-width="1.8" stroke-linecap="round"/>`;
      else out += `<circle cx="${x}" cy="7" r="1.2" fill="#4a5868"/>`;
      if (took[i]) {
        const bh = Math.max(2, Math.round((took[i] / slowest) * 24));
        out += `<rect x="${x - 4}" y="${h - bh}" width="8" height="${bh}" rx="1" fill="#70bebb"/>`;
      }
    });
    svg.setAttribute('viewBox', `0 0 ${slots.length * w} ${h}`);
    svg.innerHTML = out;
    const sorted = took.filter(Boolean).sort((a, b) => a - b);
    const at = (q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];
    const label = $('.samples-l', svg.parentElement);
    if (label && sorted.length) label.textContent = `p50 ${at(0.5)} ms · p95 ${at(0.95)} ms`;
  }
}

// ---------------------------------------------------------------- small things

function copyButtons() {
  for (const box of $$('[data-copy]')) {
    const btn = $('.copy', box);
    if (!btn) continue;
    const icon = $('use', btn);
    btn.addEventListener('click', async () => {
      const text = box.dataset.copy;
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        const range = document.createRange();
        range.selectNodeContents($('code', box));
        const sel = getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        return;
      }
      btn.classList.add('done');
      icon.setAttribute('href', '#check');
      const label = btn.getAttribute('aria-label');
      btn.setAttribute('aria-label', t('copied'));
      setTimeout(() => {
        btn.classList.remove('done');
        icon.setAttribute('href', '#copy');
        btn.setAttribute('aria-label', label);
      }, 1600);
    });
  }
}

function tabs() {
  for (const box of $$('[data-tabs]')) {
    const list = $$('[role="tab"]', box);
    const select = (tab, focus) => {
      for (const t of list) {
        const on = t === tab;
        t.setAttribute('aria-selected', String(on));
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute('aria-controls')).hidden = !on;
      }
      if (focus) tab.focus();
    };
    list.forEach((t, i) => {
      t.addEventListener('click', () => select(t));
      t.addEventListener('keydown', (e) => {
        const d = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
        if (d) { select(list[(i + d + list.length) % list.length], true); e.preventDefault(); }
        if (e.key === 'Home') { select(list[0], true); e.preventDefault(); }
        if (e.key === 'End') { select(list[list.length - 1], true); e.preventDefault(); }
      });
    });
  }
}

// languageMenu closes the language list when focus or a click goes elsewhere.
function languageMenu() {
  const menu = $('details.lang');
  if (!menu) return;
  const close = () => { menu.open = false; };
  document.addEventListener('click', (e) => { if (!menu.contains(e.target)) close(); });
  menu.addEventListener('focusout', (e) => { if (!menu.contains(e.relatedTarget)) close(); });
  menu.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && menu.open) { close(); $('summary', menu).focus(); }
  });
}

// suggestLanguage offers this page in the visitor's own language when they
// arrive from elsewhere and the site has it. It never moves them: the
// offer is a link, and closing it stores nothing.
function suggestLanguage() {
  const offers = STRINGS.languages || [];
  const here = document.documentElement.lang.toLowerCase();
  let from = '';
  try { from = document.referrer && new URL(document.referrer).origin; } catch { /* no referrer */ }
  if (!offers.length || from === location.origin) return;
  const primary = (tag) => tag.toLowerCase().split('-')[0];
  let best;
  for (const want of navigator.languages || [navigator.language || '']) {
    best = offers.find((o) => o.tag.toLowerCase() === want.toLowerCase())
      || offers.find((o) => primary(o.tag) === primary(want));
    if (best) break;
  }
  if (!best || best.tag.toLowerCase() === here) return;
  const bar = document.createElement('aside');
  bar.className = 'lang-offer';
  bar.lang = best.tag;
  const link = document.createElement('a');
  link.href = best.href + location.hash;
  link.hreflang = best.tag;
  link.textContent = `${best.suggest} →`;
  const shut = document.createElement('button');
  shut.type = 'button';
  shut.setAttribute('aria-label', best.dismiss);
  shut.textContent = '×';
  shut.addEventListener('click', () => bar.remove());
  bar.append(link, shut);
  document.body.append(bar);
}

hero();
tour();
dotNumerals();
vantageMap();
replays();
strips();
copyButtons();
tabs();
languageMenu();
suggestLanguage();

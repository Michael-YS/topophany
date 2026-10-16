import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { chromium } from 'playwright';

const server = spawn('python3', [new URL('./server.py', import.meta.url).pathname], { stdio: ['ignore', 'pipe', 'inherit'] });
let browser;
try {
  const lines = createInterface({ input: server.stdout });
  const ports = await new Promise((resolve, reject) => {
    lines.once('line', (line) => resolve(JSON.parse(line)));
    server.once('exit', (code) => reject(new Error(`fixture exited: ${code}`)));
    server.once('error', reject);
  });
  lines.close();
  const base = `http://127.0.0.1:${ports.site}`;
  const collector = `http://127.0.0.1:${ports.collector}`;
  browser = await chromium.launch({ executablePath: process.env.CHROME || undefined, headless: true });
  const context = await browser.newContext({ viewport: { width: 2200, height: 1600 } });
  await context.addInitScript(() => {
    window.viewerStats = { sent: 0, received: 0, inFlight: 0, peak: 0, frame: null };
    const NativeWorker = Worker;
    window.Worker = class extends NativeWorker {
      constructor(...args) {
        super(...args);
        this.addEventListener('message', ({ data }) => {
          viewerStats.received++;
          viewerStats.inFlight--;
          const result = data.result ? JSON.parse(data.result) : data;
          if (result.frame) viewerStats.frame = result.frame;
        });
      }
      postMessage(data) {
        viewerStats.sent++;
        viewerStats.inFlight++;
        viewerStats.peak = Math.max(viewerStats.peak, viewerStats.inFlight);
        super.postMessage(data);
      }
    };
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const ready = () => page.waitForSelector('.is-ready', { timeout: 30000 });
  async function connect(path) {
    await page.goto(base + '/app/');
    await page.locator('#collector-server').fill(collector + path);
    await page.locator('#collector-project').fill('test');
    await page.locator('#collector-token').fill('x'.repeat(64));
    await page.locator('[type=submit]').click();
    await ready();
    assert.equal(await page.locator('#collector-token').inputValue(), '');
  }
  await context.addCookies([{ name: 'must-not-send', value: 'test', url: collector }]);
  await connect('/slow');
  await page.waitForFunction(() => document.querySelector('#demo-transcript').textContent.includes("refused this client's token"), null, { timeout: 15000 });
  const stats = await (await context.request.get(collector + '/stats')).json();
  assert.ok(stats.slow >= 2, 'body timeout must release busy and retry');
  assert.ok(stats.canceled >= 1, 'body timeout must cancel the network stream');
  assert.equal(stats.cookies, 0);
  console.log('PASS body deadline cancels stream and retries; credentials omitted');

  await connect('/redirect');
  await page.waitForFunction(() => document.querySelector('#demo-transcript').textContent.includes('unreachable'));
  assert.equal((await (await context.request.get(collector + '/stats')).json()).redirected, 0);
  await page.locator('canvas').press('Control+c');
  await page.waitForSelector('#collector-connect:not([hidden])');
  console.log('PASS redirect refused; disconnect returns cleared connection form');

  await page.goto(base + '/demo/');
  await ready();
  await page.locator('canvas').press('3');
  await page.waitForFunction(() => viewerStats.frame.tab === 2);
  await page.keyboard.press('/');
  await page.waitForFunction(() => viewerStats.frame.searching);
  await page.locator('.demo-input').pressSequentially('Seattle');
  await page.waitForFunction(() => viewerStats.frame.text?.includes('Filter Seattle'));
  await page.keyboard.press('Escape');
  await page.waitForFunction(() => !viewerStats.frame.searching);
  await page.keyboard.press('9');
  await page.waitForFunction(() => viewerStats.frame.tab === 8);
  const burst = await page.evaluate(() => new Promise((resolve) => {
    let count = 0;
    const start = performance.now(), sent = viewerStats.sent;
    const timer = setInterval(() => {
      for (let i = 0; i < 10; i++) document.querySelector('canvas').dispatchEvent(new WheelEvent('wheel', { deltaY: 1, cancelable: true }));
      if (++count === 36) {
        clearInterval(timer);
        resolve({ elapsed: performance.now() - start, messages: viewerStats.sent - sent, peak: viewerStats.peak });
      }
    }, 80);
  }));
  assert.equal(burst.peak, 1);
  assert.ok(burst.messages < 80, JSON.stringify(burst));
  const quitAt = Date.now();
  await page.keyboard.press('q');
  await page.waitForFunction(() => viewerStats.frame.closed);
  assert.ok(Date.now() - quitAt < 1000, 'quit must not sit behind scrolling');
  console.log('PASS input ordering, bounded bursts, quit', JSON.stringify({ ...burst, quitMs: Date.now() - quitAt }));

  // A response that never settles exercises cancellation during startup.
  await page.route('**/*.wasm?*', () => {});
  await page.goto(base + '/demo/');
  await page.waitForSelector('.demo-loading.can-restart');
  await page.locator('.demo-loading').click();
  assert.ok((await page.locator('.demo-loading').textContent()).includes('Session ended'));
  await page.unroute('**/*.wasm?*');
  await page.locator('.demo-loading').click();
  await ready();
  console.log('PASS startup cancel and retry');

  const timeoutPage = await context.newPage();
  // Shorten only the page's 30-second startup watchdog for this regression.
  await timeoutPage.addInitScript(() => {
    const timeout = window.setTimeout;
    window.setTimeout = (fn, delay, ...args) => timeout(fn, delay === 30000 ? 300 : delay, ...args);
  });
  await timeoutPage.route('**/*.wasm?*', () => {});
  await timeoutPage.goto(base + '/demo/');
  await timeoutPage.waitForFunction(() => document.querySelector('.demo-loading').textContent.includes('Terminal unavailable'));
  assert.equal(await timeoutPage.locator('#live-demo').getAttribute('aria-busy'), 'false');
  await timeoutPage.close();
  console.log('PASS startup watchdog restores retry UI');
  assert.deepEqual(errors, []);
} finally {
  await browser?.close();
  server.kill();
}

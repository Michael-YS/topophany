// The Go runtime and viewer live off the page's main thread. This worker only
// loads local assets and sends character patches; no terminal server is used.
import './wasm_exec.js';

let ready = false;
let clock = 0;
let last = 0;

async function handle(data) {
  try {
    if (!ready) {
      if (data.action !== 'init') return;
      const go = new Go();
      const response = await fetch(data.wasm);
      if (!response.ok) throw new Error(`WASM HTTP ${response.status}`);
      const { instance } = response.headers.get('content-type')?.split(';')[0] === 'application/wasm'
        ? await WebAssembly.instantiateStreaming(response, go.importObject)
        : await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
      go.run(instance).catch((error) => self.postMessage({ error: String(error) }));
      ready = true;
    }
    const wall = performance.now();
    if (data.action === 'init' || data.action === 'reset') {
      clock = Date.now();
    } else if (!data.resume) {
      // Hidden tabs pause the demo; never simulate hours of missed samples.
      clock += Math.min(1000, Math.max(0, wall - last));
    }
    last = wall;
    const result = await globalThis.topophanyDemo(JSON.stringify({ ...data, now: Math.floor(clock) }));
    // Send the encoded frame directly: parse once on the page, avoiding a
    // second object tree and its structured clone on every frame.
    self.postMessage({ action: data.action, result });
  } catch (error) {
    self.postMessage({ action: data.action, error: String(error) });
  }
}

// The page waits for each reply before sending another bounded input batch.
// Reject a broken producer instead of retaining an unbounded Promise chain.
let busy = false;
self.onmessage = ({ data }) => {
  if (busy) { self.postMessage({ error: 'viewer request already in flight' }); return; }
  busy = true;
  handle(data).finally(() => { busy = false; });
};

import assert from 'node:assert/strict';
import test from 'node:test';
import { ViewerInput } from '../src/js/viewer-input.js';

test('wheel bursts and resize collapse without losing ordered commands', () => {
  const input = new ViewerInput();
  for (let i = 0; i < 10000; i++) input.key('wheel-down');
  input.resized = true;
  input.key('/');
  input.key('paste:成都');
  input.key('q'); // Search text must still reach Go after /.
  assert.deepEqual(input.next(), { action: 'input', keys: ['/', 'paste:成都', 'q'], resume: false });
  assert.equal(input.next().action, 'tick');
});

test('pending inputs, payloads and batches are bounded', () => {
  const input = new ViewerInput();
  for (let i = 0; i < 10000; i++) input.key('x'.repeat(9000));
  assert.equal(input.keys.length, 64);
  for (let i = 0; i < 4; i++) {
    const batch = input.next();
    assert.equal(batch.keys.length, 16);
    assert.ok(batch.keys.every((key) => key.length === 8198));
  }
  assert.equal(input.next().action, 'tick');
});

test('resume survives queued input and is consumed once', () => {
  const input = new ViewerInput();
  input.resumed = true;
  input.key('3');
  assert.deepEqual(input.next(), { action: 'input', keys: ['3'], resume: true });
  assert.equal(input.next().resume, false);
});

// bus.test.js — the fan-out bus's contract: hint filtering, wildcard
// (null = all hints), and in-order delivery (subscription order is the
// emission order — the old hand-wired switch's order, preserved).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createBus } from './bus.js';

test('hint-filtered subscription fires only on its hints', () => {
  const bus = createBus();
  const got = [];
  bus.on('notice', (key, hint) => got.push(`notice:${key}`));
  bus.on(['switch', 'rooms'], (key, hint) => got.push(`sr:${hint}`));
  bus.emit('proj-x', 'members');
  bus.emit('proj-x', 'switch');
  bus.emit('proj-y', 'notice');
  bus.emit('proj-y', 'rooms');
  assert.deepEqual(got, ['sr:switch', 'notice:proj-y', 'sr:rooms']);
});

test('null subscription is the wildcard (fires on every hint)', () => {
  const bus = createBus();
  let n = 0;
  bus.on(null, () => n++);
  bus.on('unread', () => n++);
  bus.emit('a', 'switch');
  bus.emit('a', 'unread');
  bus.emit('a', 'status');
  assert.equal(n, 4); // wildcard 3 次 + unread 1 次
});

test('delivery preserves subscription order within one emit', () => {
  const bus = createBus();
  const order = [];
  bus.on(null, () => order.push('first'));
  bus.on('status', () => order.push('status-only'));
  bus.on(null, () => order.push('last'));
  bus.emit('lobby', 'status');
  assert.deepEqual(order, ['first', 'status-only', 'last']);
  order.length = 0;
  bus.emit('lobby', 'members');
  assert.deepEqual(order, ['first', 'last']);
});

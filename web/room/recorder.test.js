// recorder.test.js — r_32：录制腿的纯逻辑面——变化过滤/去抖/攒批冲
// 账/切房先冲旧 key。post 注入假桩，不碰网络不碰定时器（start/stop
// 的生命周期挂在 DOM 面，由 dayreplay.dom.test 的冒烟层盖）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { Recorder, FLUSH_EVERY, FEED_DEBOUNCE } from './recorder.js';

const frame = (x, b = '', hold = '') => ([{ n: '甲', x, y: 100, d: 2, b, w: true, hold, c: '#a03', hair: '01' }]);

function mkRecorder() {
  const sent = [];
  const rec = new Recorder((key, frames, opts) => {
    sent.push({ key, frames, opts });
    return Promise.resolve({ stored: frames.length, skipped: 0 });
  });
  return { rec, sent };
}

test('契约：冲账节拍 20s、去抖 250ms', () => {
  assert.equal(FLUSH_EVERY, 20_000);
  assert.equal(FEED_DEBOUNCE, 250);
});

test('feed：首帧必收、无变化丢、位移/泡/手持变化收', () => {
  const { rec } = mkRecorder();
  rec.attach('alpha');
  assert.equal(rec.feed(1000, frame(10)), true, '首帧无对照面必发');
  assert.equal(rec.feed(1300, frame(10)), false, '同拍去抖');
  assert.equal(rec.feed(2000, frame(10.1)), false, '亚半像素抖动视为无变化');
  assert.equal(rec.feed(2500, frame(40)), true, '位移过阈值');
  assert.equal(rec.feed(3000, frame(40, '你好')), true, '泡标签变了');
  assert.equal(rec.feed(3500, frame(40, '你好', 'sheet')), true, '手持物变了');
  assert.equal(rec.pending.length, 4);
});

test('flush：一批一帖、帖后即清、失败也清（水位线兜底不重试）', async () => {
  const { rec, sent } = mkRecorder();
  rec.attach('alpha');
  rec.feed(1000, frame(10));
  rec.feed(2000, frame(30));
  assert.equal(rec.flush(), 2);
  assert.equal(sent.length, 1);
  assert.equal(sent[0].key, 'alpha');
  assert.deepEqual(sent[0].frames.map((f) => f.t), [1000, 2000]);
  assert.equal(rec.flush(), 0, '清后再冲是空批');

  const boom = new Recorder(() => Promise.reject(new Error('offline')));
  boom.attach('alpha');
  boom.feed(1000, frame(10));
  assert.equal(boom.flush(), 1);
  assert.equal(boom.pending.length, 0, '失败也清——丢了 20s 尾巴不是一致性');
});

test('attach：切房先冲旧 key 的尾巴，新房无对照面', () => {
  const { rec, sent } = mkRecorder();
  rec.attach('alpha');
  rec.feed(1000, frame(10));
  rec.attach('beta');
  assert.equal(sent.length, 1, '切房冲了 alpha 的尾批');
  assert.equal(sent[0].key, 'alpha');
  assert.equal(rec.feed(1100, frame(10)), true, '新房首帧无对照面必发');
  assert.equal(rec.pending[0].a[0].x, 10);
});

test('feed：未 attach（无房）不吃帧', () => {
  const { rec } = mkRecorder();
  assert.equal(rec.feed(1000, frame(10)), false);
  assert.equal(rec.pending.length, 0);
});

test('flush：keepalive 选项随冲账透传（pagehide 最后一截）', () => {
  const { rec, sent } = mkRecorder();
  rec.attach('alpha');
  rec.feed(1000, frame(10));
  rec.flush({ keepalive: true });
  assert.equal(sent[0].opts.keepalive, true);
});

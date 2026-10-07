// exporter.test.js — r_32：导出腿的纯逻辑面——分段（断档/空场剪）、
// 时间轴推进（跨段硬切）、编码探测（无 MediaRecorder 的环境答 null，
// 其余功能不连带坏）。浏览器腿（exportDayReplay 的录制循环）由
// dayreplay.dom.test 的冒烟层盖。

import test from 'node:test';
import assert from 'node:assert/strict';
import { segmentsOf, nextT, pickMime, EXPORT_FPS, GAP_MS, EMPTY_MS } from './exporter.js';

const fr = (t, n = 1) => ({ t, a: Array.from({ length: n }, (_, i) => ({ n: `甲${i}`, x: 0, y: 0, d: 0 })) });

test('契约：断档 30s、空场 10s、采样 12fps', () => {
  assert.equal(GAP_MS, 30_000);
  assert.equal(EMPTY_MS, 10_000);
  assert.equal(EXPORT_FPS, 12);
});

test('segmentsOf：连续帧一段到尾', () => {
  const frames = [fr(0), fr(500), fr(1000), fr(1500)];
  assert.deepEqual(segmentsOf(frames), [{ s: 0, e: 1500 }]);
});

test('segmentsOf：断档 >30s 硬切（app 关着的窗不进视频）', () => {
  const frames = [fr(0), fr(1000), fr(40_000), fr(41_000)];
  assert.deepEqual(segmentsOf(frames), [{ s: 0, e: 1000 }, { s: 40_000, e: 41_000 }]);
});

test('segmentsOf：短于 30s 的断档保留（一顿午饭的间隙还在）', () => {
  const frames = [fr(0), fr(1000), fr(20_000), fr(21_000)];
  assert.deepEqual(segmentsOf(frames), [{ s: 0, e: 21_000 }]);
});

test('segmentsOf：全帧无人 >10s 剪掉（前段收到空场起点）', () => {
  const frames = [
    fr(0), fr(500),           // 有人
    fr(1000, 0), fr(11_500, 0), // 空场 10.5s
    fr(12_000), fr(12_500),   // 有人
  ];
  assert.deepEqual(segmentsOf(frames), [{ s: 0, e: 1000 }, { s: 12_000, e: 12_500 }]);
});

test('segmentsOf：短空场不剪（人只是路过了一下）', () => {
  const frames = [fr(0), fr(500, 0), fr(9_000, 0), fr(9_500), fr(10_000)];
  assert.deepEqual(segmentsOf(frames), [{ s: 0, e: 10_000 }]);
});

test('segmentsOf：坏行跳过、空表空答', () => {
  assert.deepEqual(segmentsOf([]), []);
  // {t:1} 无 a 视作空帧（不炸），与后段连成一段
  assert.deepEqual(segmentsOf([null, { t: 1 }, fr(500)]), [{ s: 1, e: 500 }]);
});

test('nextT：段内前进、跨段硬切、过尾 null', () => {
  const segs = [{ s: 0, e: 1000 }, { s: 40_000, e: 41_000 }];
  assert.equal(nextT(segs, 500, 200), 700, '段内');
  assert.equal(nextT(segs, 900, 200), 40_000, '跨段落到下一段头');
  assert.equal(nextT(segs, 40_900, 200), null, '过尾 null');
  assert.equal(nextT([], 0, 100), 100, '无段裸进（调用方兜底）');
  assert.equal(nextT(segs, -5_000, 100), 0, '段前钳到段头');
});

test('pickMime：无 MediaRecorder 的环境答 null（不抛）', () => {
  // node 裸环境没有 MediaRecorder——这正是要验的档
  assert.equal(pickMime(), null);
});

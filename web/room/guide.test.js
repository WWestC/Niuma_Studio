// guide.test.js — 首次观察引导的时序闸（t_159 修复）：guideDue 判式的
// 真值表（可见/有房/门已落/未弹过——缺一门不放行），再按 usage.test.js
// 的接线扫描先例钉装配契约——roomview 的三重门接线、app.js 的开门补
// 拍、guide.dismiss 的不记账语义，任一处被删，这里必红（气泡又会在
// 黑屏/启动门上抢跑，房主实录的回归）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { guideDue } from './util.js';

// ── ① guideDue 真值表 ────────────────────────────────────────────

const st = (over = {}) => ({ guideShown: false, visible: true, roomKey: 'niuma', doorDown: true, ...over });

test('四门齐备才放行', () => {
  assert.equal(guideDue(st()), true);
});

test('房主实录的抢跑两拍都不放行：路由首拍（无房＋门未落）、setRoom 后门未落', () => {
  // boot 序列：route() 先于 book.start/setRoom/开门
  assert.equal(guideDue(st({ roomKey: null, doorDown: false })), false, '路由首拍：黑屏上有气泡');
  assert.equal(guideDue(st({ doorDown: false })), false, '房数据已装但启动门还压着');
  assert.equal(guideDue(st({ roomKey: null })), false, '门落了但房没装（防御拍）');
  assert.equal(guideDue(st({ visible: false })), false, '人不在办公室板块');
});

test('弹过一次就不再放行（guideShown 防重入——maybeStart 只认「看过」标记）', () => {
  assert.equal(guideDue(st({ guideShown: true })), false);
});

// ── ② 接线扫描（删闸必红）───────────────────────────────────────

const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');

test('三重门在位：revealed/setRoom 走 tryGuide、开门补拍 doorDropped、离场 dismiss 不记账', () => {
  const rv = read('./roomview.js');
  assert.match(rv, /import \{ clamp, guideDue, inRect, rand \} from '\.\/util\.js';/, 'roomview 引入判式');
  assert.match(rv, /tryGuide\(\) \{\s*\n\s*if \(!guideDue\(this\)\) return;/, 'tryGuide 用 util 判式把关');
  // revealed/setRoom 只许经 tryGuide 间接弹——maybeStart 的直呼点唯一（tryGuide 体内）
  const maybeCalls = rv.match(/this\.guide\.maybeStart\(\)/g) || [];
  assert.equal(maybeCalls.length, 1, 'maybeStart 直呼点唯一（tryGuide 体内）');
  assert.match(rv, /this\.wake\(\);\s*\n\s*this\.tryGuide\(\); \/\/ t_159/, 'setRoom 尾拍补弹');
  assert.match(rv, /this\.tryGuide\(\); \/\/ t_159：首次进办公室/, 'revealed 经闸');
  assert.match(rv, /doorDropped\(\) \{ this\.doorDown = true; this\.tryGuide\(\); \}/, '开门补拍口');
  assert.match(rv, /this\.guide\.dismiss\(\); this\.guideShown = false;/, '离场收层并复位防重入');

  const app = read('../app.js');
  // 顺序语义：开门（dismissBoot）必须早于补拍（doorDropped）——中间隔
  // 插件装载是合理节奏（板块齐了再教），不要求正则紧邻
  const gateAt = app.indexOf('await dismissBoot();');
  const dropAt = app.indexOf('roomView?.doorDropped();');
  assert.ok(gateAt >= 0 && dropAt > gateAt, 'app.js 开门后才补拍（顺序）');

  const ui = read('../ui/guide.js');
  const dismiss = ui.match(/dismiss\(\) \{[\s\S]*?\n  \}/)?.[0] || '';
  assert.ok(dismiss.includes('teardown'), 'dismiss 收层');
  assert.ok(!dismiss.includes('setPref'), 'dismiss 不写「看过」标记（finish 才记账）');
});

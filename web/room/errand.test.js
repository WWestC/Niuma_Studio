// errand.test.js — 演员占用一口径真源（errand.js）的语义面＋接线闸
// 源码契约（cleaner.test 先例：roomview 是不可实例化的 DOM 类，删闸
// 必红）。三案同根的行为面分别在 printer.test.js / eat.test.js /
// water.test.js，这里钉「口径本身」与「所有调度器真的在用它」。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { onErrand, claimed, dispatchable, sendOnErrand, leaveDesk } from './errand.js';
import { DIR } from './actor.js';

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, f), 'utf8');

// ── 语义面：claimed / dispatchable / onErrand ────────────────────

const idle = (over = {}) => Object.assign({
  name: '小鹿', meetIdx: -1, breakIdx: -1, socialGroup: null,
  holding: null, eating: null, fetching: null, drinking: null,
  isLocal: false, isNPC: false,
}, over);
const BUSY = new Set(['忙人']);
const FREE = new Set();

test('claimed：无占用＋名册在册外 → 闲', () => {
  assert.equal(claimed(idle(), FREE), false);
});

test('claimed：外部占有者全票否决（会/歇/摸鱼/忙名册/任何外勤/纸张在手）', () => {
  assert.equal(claimed(idle({ meetIdx: 2 }), FREE), true);
  assert.equal(claimed(idle({ breakIdx: 1 }), FREE), true);
  assert.equal(claimed(idle({ socialGroup: {} }), FREE), true);
  assert.equal(claimed(idle({ name: '忙人' }), BUSY), true);
  assert.equal(claimed(idle({ fetching: true }), FREE, 'eating'), true);   // 别人的外勤
  assert.equal(claimed(idle({ drinking: true }), FREE, 'fetching'), true);
  assert.equal(claimed(idle({ holding: 'task' }), FREE, 'eating'), true);  // 纸张在手＝手满
});

test('claimed：own 豁免——自己的外勤态与自己的 food 道具不算数（一帧自弃的旧疤钉）', () => {
  // 吃链自己：eating + holding='food' 都不构成「别人占了我」
  assert.equal(claimed(idle({ eating: { phase: 'eat' }, holding: 'food' }), FREE, 'eating'), false);
  // 但 food 在手对外派活仍是占用（dispatchable / own=null 视角）
  assert.equal(claimed(idle({ eating: { phase: 'eat' }, holding: 'food' }), FREE, null), true);
  // 取材/接水同理：own 豁免自己那态
  assert.equal(claimed(idle({ fetching: true }), FREE, 'fetching'), false);
  assert.equal(claimed(idle({ drinking: true }), FREE, 'drinking'), false);
});

test('claimed：名册未就绪一律保守视为被占（宁无戏不抢人）', () => {
  assert.equal(claimed(idle(), null), true);
  assert.equal(claimed(idle(), undefined), true);
});

test('dispatchable：本机/NPC 永不可派；占用者不可派；闲人可派', () => {
  assert.equal(dispatchable(idle({ isLocal: true }), FREE), false);
  assert.equal(dispatchable(idle({ isNPC: true }), FREE), false);
  assert.equal(dispatchable(idle({ fetching: true }), FREE), false);
  assert.equal(dispatchable(idle({ holding: 'notice' }), FREE), false);
  assert.equal(dispatchable(idle(), FREE), true);
  assert.equal(dispatchable(null, FREE), false);
});

test('onErrand：三外勤态任一在身即真', () => {
  assert.equal(onErrand(idle()), false);
  assert.equal(onErrand(idle({ eating: {} })), true);
  assert.equal(onErrand(idle({ fetching: true })), true);
  assert.equal(onErrand(idle({ drinking: {} })), true);
});

// ── 语义面：sendOnErrand / leaveDesk（走位下发纪律）──────────────

test('sendOnErrand：摘 working 锚＋目标落位＋朝向真源（坐着派目标不动是旧疤）', () => {
  const a = idle({ working: true, hasTarget: false, tx: 0, ty: 0, workFace: DIR.DOWN });
  sendOnErrand(a, 226, 498);
  assert.equal(a.working, false, 'working 锚必须摘——actor.step 里 working 优先于 hasTarget');
  assert.equal(a.hasTarget, true);
  assert.equal(a.tx, 226);
  assert.equal(a.ty, 498);
  assert.equal(a.workFace, DIR.UP, '缺省朝北');
  sendOnErrand(a, 1, 2, DIR.DOWN);
  assert.equal(a.workFace, DIR.DOWN, '显式朝向生效');
  sendOnErrand(null, 1, 2); // 空演员零崩
});

test('leaveDesk：release 走 desks 池（带钟）＋lastDesk 记旧座＋deskIdx 归 -1', () => {
  const rel = [];
  const a = idle({ deskIdx: 3, lastDesk: -1 });
  leaveDesk(a, { desks: { release: (i, c) => rel.push([i, c]) }, clock: 42 });
  assert.deepEqual(rel, [[3, 42]]);
  assert.equal(a.lastDesk, 3);
  assert.equal(a.deskIdx, -1);
  // 无工位/无池都是零操作
  leaveDesk(idle({ deskIdx: -1 }), null);
  leaveDesk(idle({ deskIdx: 2 }), { desks: null });
});

// ── 接线闸（源码契约：删闸必红）───────────────────────────────────
// 资格链分散在 roomview（DOM 类不可实例化）——按 cleaner.test 的接线
// 扫描先例钉源码：所有调度器必须引用一口径，谁绕开口径手搓过滤表，
// 这里就红给谁看。

test('roomview 接线：落座改写豁免外勤（applyBusy 双闸）＋接水节拍＋复位', () => {
  const s = src('roomview.js');
  assert.ok(s.includes('!onErrand(a) && !a.holding'), 'applyBusy 的外勤/手持件豁免闸（releaseDesk 与 idle 落座两处共用此形）');
  assert.ok(s.includes('this.water.step(a, dt)'), 'update 循环推进接水外勤');
  assert.ok(s.includes('this.water.beat()'), 'tryPantry 节拍捎带接水（旧 240px 隔空配音已退役）');
  assert.ok(s.includes('this.printer.reset()'), '换房清打印仪式的在途引用');
  assert.ok(!s.includes('a.x - 14, a.y - 132'), '旧接水选人的硬编码半径必须不复存在');
});

test('printer/energy/social 接线：三链全部走一口径', () => {
  assert.ok(src('printer.js').includes('sendOnErrand'), '打印取材走位必须经 sendOnErrand（不再等一个没派过的人）');
  assert.ok(src('printer.js').includes('FETCH_WAIT'), '取材走位必须带死线');
  assert.ok(src('energy.js').includes("claimed(a, board.busy, 'eating')"), '吃链中断守卫必须走 claimed(own=eating)');
  assert.ok(src('social.js').includes('dispatchable('), '摸鱼资格必须走 dispatchable');
  assert.ok(src('actor.js').includes('this.eating || this.fetching || this.drinking'), '外勤中人到点必须锚定（防闲逛抢方向盘）');
});

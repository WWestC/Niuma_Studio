// printer.test.js — 打印仪式行为链（errand 根治的回归钉）：入机即派
// 走位、到锚取材、外部占用放人、死线弃置、回座送达。旧世界的病：
// take 阶段等一个从未被派出走位目标的人，4 秒后一切降级无人认领——
// 「受派人取纸」整套戏从未上演过。这里全链钉死。

import test from 'node:test';
import assert from 'node:assert/strict';
import { PrinterRitual, SHEET_KIND } from './printer.js';
import { Actor } from './actor.js';

// geom 投影只用到 spot（走位/取件锚）；其余面给最小合法形状
const GEOM = {
  printer: {
    rect: [150, 430, 120, 64],
    spot: [226, 498],
    tray: [170, 470, 40, 8],
    led: [240, 440, 6, 4],
    panel: [160, 440, 24, 12],
  },
};

function fakeBoard() {
  const b = {
    clock: 0,
    busy: new Set(),
    actors: new Map(),
    art: { geom: GEOM },
    desks: { release() {}, spots: [{ x: 300, y: 260, face: 1 }], use: new Map(), hold: new Map() },
  };
  b.desks.claimIdle = (name, clock) => {
    if (!b.desks.use.has(0) && (b.desks.hold.get(0) ?? 0) <= clock) {
      b.desks.use.set(0, name);
      return { idx: 0, spot: b.desks.spots[0] };
    }
    return null;
  };
  return b;
}

const member = (b, name, x, y, over = {}) => {
  const a = new Actor({ name, color: '#d88' }, x, y, null);
  Object.assign(a, over);
  b.actors.set(name, a);
  return a;
};

// 步进 n 拍（dt 秒），钟随手走
const run = (r, b, dt, n) => { for (let i = 0; i < n; i++) { r.step(dt); b.clock += dt; } };

test('入机即派走位：取材人拿到目标＝取件锚点，面北，占用声明在身', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小鹿', 400, 300);
  r.print(SHEET_KIND.TASK, '小鹿', 't1');
  r.step(0.1);
  assert.equal(a.fetching, true, '占用声明（所有调度器经 errand.js 豁免）');
  assert.equal(a.hasTarget, true, '走位目标必须在此刻下发——旧世界从未有过');
  assert.equal(a.tx, 226);
  assert.equal(a.ty, 498);
  assert.equal(a.workFace, 1 /* DIR.UP */);
  assert.equal(a.working, false);
});

test('全链：亮灯→出纸→到锚取材→手持件在身→送回工位（S4 回座）', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小鹿', 400, 300, { deskIdx: 0 });
  r.print(SHEET_KIND.TASK, '小鹿', 't1');
  run(r, b, 0.25, 9); // 2.25s：light(0.5) + paper(1.5) 已过，take 中
  assert.equal(r.job.phase, 'take');
  // 走到位（演员位移由 actor.step 管，测试直接落位模拟到达）
  a.x = 226; a.y = 498; a.hasTarget = false; a.moving = false;
  r.step(0.1);
  assert.equal(a.holding, 'task', '手持件在身');
  assert.equal(a.fetching, false, '占用声明随取材完成归还');
  assert.equal(r.job, null, '机器立刻让位下一单');
  assert.equal(r.unattended.length, 0, '有人认领——不走无人认领分支');
  assert.equal(a.hasTarget, true, '取完即返程：目标回到工位锚');
  assert.equal(a.tx, 300);
  assert.equal(a.ty, 260);
  // 落座即 pop（maybeRelease 的 working 路径）
  a.working = true;
  r.maybeRelease(a);
  assert.equal(a.holding, null);
  assert.equal(a.sheetPopT, 1.5);
});

test('无工位的取材人：取完顺手认空位返程（claimIdle 路径）', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小马', 400, 300, { deskIdx: -1 });
  r.print(SHEET_KIND.NOTICE, '小马', 't2');
  run(r, b, 0.25, 9);
  a.x = 226; a.y = 498; a.hasTarget = false; a.moving = false;
  r.step(0.1);
  assert.equal(a.holding, 'notice');
  assert.equal(a.deskIdx, 0, '顺手认了空位');
  assert.equal(a.tx, 300, '返程目标＝新座位');
});

test('外部占用接管：派活半路抢人——占用放行、纸降级无人认领', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小鹿', 400, 300);
  r.print(SHEET_KIND.TASK, '小鹿', 't3');
  r.step(0.1);
  assert.equal(a.fetching, true);
  b.busy.add('小鹿'); // 派活
  r.step(0.1);
  assert.equal(a.fetching, false, '占用声明立即归还（链条下一帧自清）');
  assert.equal(a.holding, null);
  run(r, b, 0.25, 9); // 走完 light+paper，take 首拍即降级
  assert.equal(r.unattended.length, 1, '纸走无人认领（滞留淡出）');
  assert.equal(r.job, null);
});

test('入机即忙的取材人：认领判据保守——直接无人认领，绝无走位', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  b.busy.add('忙人');
  const a = member(b, '忙人', 400, 300);
  r.print(SHEET_KIND.DONE, '忙人', 't4');
  r.step(0.1);
  assert.equal(a.fetching, false, '忙人不派走位');
  assert.equal(a.hasTarget, false);
});

test('走位死线：人到不了（被堵/路远）——占用散场、纸降级无人认领', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小鹿', 400, 300); // 永远不动（模拟被堵）
  r.print(SHEET_KIND.TASK, '小鹿', 't5');
  run(r, b, 0.5, 30); // 15s > FETCH_WAIT(14)
  assert.equal(a.fetching, false, '死线散场，占用归还');
  assert.equal(a.hasTarget, false);
  assert.equal(r.unattended.length, 1);
  assert.equal(r.job, null);
});

test('手持食品不叠画白纸、不触发材料送达 pop（两手持件互斥）', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  const a = member(b, '小鹿', 100, 100, { holding: 'food', foodKey: 'food-apple' });
  const ctx = { fillStyle: '', fillRect() {}, strokeStyle: '', lineWidth: 1, strokeRect() {}, beginPath() {}, arc() {}, moveTo() {}, lineTo() {}, stroke() {} };
  r.paintHeldSheet(ctx, a); // 'food' 早退——不抛不画
  a.working = true;
  r.maybeRelease(a); // 'food' 不走送达 pop
  assert.equal(a.holding, 'food');
  assert.equal(a.sheetPopT, 0);
});

test('reset：换房清场（队列/在机任务/滞留纸）', () => {
  const b = fakeBoard();
  const r = new PrinterRitual(b);
  member(b, '小鹿', 400, 300);
  r.print(SHEET_KIND.TASK, '小鹿', 't6');
  r.print(SHEET_KIND.NOTICE, null, 't7');
  r.step(0.1);
  r.reset();
  assert.equal(r.job, null);
  assert.equal(r.queue.length, 0);
  assert.equal(r.unattended.length, 0);
});

// water.test.js — 接水外勤行为链（t_105 氛围行为根治的回归钉）。
// 旧世界：挑「饮水机 240px 内」的闲人原地说「接杯水～」，机器那头
// 自己放水——240px 覆盖近半屋，人不动水自流。根治：只挑机器跟前的
// （PICK_R 90），走去机前锚点、到位才放水说台词，演完散场。

import test from 'node:test';
import assert from 'node:assert/strict';
import { WaterRitual } from './water.js';
import { Actor } from './actor.js';

// DispenserRect native (4,104,24,160) → [x,y,w,h]；锚点＝(dx+10, dy+dh+6)＝(14,166)
function fakeBoard() {
  const said = [];
  return {
    clock: 50,
    busy: new Set(),
    pourT: 0,
    actors: new Map(),
    art: { geom: { interact: { dispenser: [4, 104, 20, 56] } } },
    desks: { release() {} },
    say: (...args) => said.push(args),
    said,
  };
}

const member = (b, name, x, y, over = {}) => {
  const a = new Actor({ name, color: '#d88' }, x, y, null);
  Object.assign(a, over);
  b.actors.set(name, a);
  return a;
};

test('近人才选：机器跟前的闲人拿到走位目标＝机前锚点，面北', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a = member(b, '小鹿', 20, 180); // 距锚点 (14,166) ~15px
  w.beat();
  assert.ok(a.drinking, '接水外勤发起');
  assert.equal(a.hasTarget, true);
  assert.equal(a.tx, 14);
  assert.equal(a.ty, 166);
  assert.equal(a.workFace, 1 /* DIR.UP */);
});

test('远处不选：半屋外的闲人永远不被隔空配音（旧 240 半径的根治钉）', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a = member(b, '小马', 600, 400); // 距锚点 ~590px
  for (let i = 0; i < 20; i++) w.beat();
  assert.equal(a.drinking, null, '隔空接水必须不复存在');
  assert.equal(b.said.length, 0);
});

test('到位才出水：pourT/台词在到达那帧落，不在派单时落', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a = member(b, '小鹿', 20, 180);
  w.beat();
  assert.equal(b.pourT, 0, '派单那刻机器不出水');
  assert.equal(b.said.length, 0, '台词也不说');
  a.hasTarget = false; a.moving = false; // 模拟到达
  w.step(a, 0.1);
  assert.equal(a.drinking.phase, 'pour');
  assert.equal(b.pourT, 1.2, '到位才放水');
  assert.equal(b.said.length, 1, '到位才说话');
});

test('接完散场：pour 满 1.2s——外勤清、锚摘、机器让位', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a = member(b, '小鹿', 20, 180);
  w.beat();
  a.hasTarget = false; a.moving = false;
  w.step(a, 0.1);
  for (let i = 0; i < 15; i++) { w.step(a, 0.1); b.clock += 0.1; }
  assert.equal(a.drinking, null);
  assert.equal(a.working, false);
  assert.equal(w.active.size, 0, '机器让位下一杯');
});

test('一人一杯：在途时 beat 不再派（含机器正出水）', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a1 = member(b, '小鹿', 20, 180);
  const a2 = member(b, '小马', 30, 170);
  w.beat();
  assert.ok(a1.drinking || a2.drinking, '派出一位');
  w.beat();
  const both = !!a1.drinking + !!a2.drinking;
  assert.ok(both <= 1, '同时最多一人占机');
  b.pourT = 0.5; // 机器正出水（房主点击面）
  a1.drinking = null; a2.drinking = null; w.active.clear();
  w.beat();
  assert.equal(a1.drinking || a2.drinking ? 1 : 0, 0, '出水期间不派');
});

test('中断纪律：半路被派活——零残留散场，路权归接管者', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  const a = member(b, '小鹿', 20, 180);
  w.beat();
  b.busy.add('小鹿');
  w.step(a, 0.1);
  assert.equal(a.drinking, null);
  assert.equal(a.working, false);
  assert.equal(w.active.size, 0);
});

test('陈名清场：离房成员不留残影（残影会永远占住机器）', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  w.active.add('幽灵');
  member(b, '小鹿', 20, 180);
  w.beat(); // 首行清陈名后正常派单
  const a = b.actors.get('小鹿');
  assert.ok(a.drinking, '残影清掉后机器可用');
});

test('reset：换房清在途名单', () => {
  const b = fakeBoard();
  const w = new WaterRitual(b);
  w.active.add('旧房人');
  w.reset();
  assert.equal(w.active.size, 0);
});

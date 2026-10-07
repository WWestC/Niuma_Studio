// eat.test.js — 觅食-吃-丢 行为链（errand 根治的回归钉）。旧世界的
// 病一对三：①到位开吃设 holding='food'，下一帧中断守卫把自己的道具
// 读成「别人的取材」自弃——吃相位只活一帧，体力永远吃不回、垃圾永远
// 不会产生；②出发即扣库存且中断不退款——架上被「幻影吃空」；③坐着
// 的人派了目标不动（working 锚没摘）。这里全链钉死。

import test from 'node:test';
import assert from 'node:assert/strict';
import { EnergyEngine, EPARAMS } from './energy.js';
import { Actor } from './actor.js';

function fakeBoard() {
  return {
    clock: 100,
    busy: new Set(),
    actors: new Map(),
    // pantryZone (440,160,768,320) → [x,y,w,h]；props[4]=pantryBin 脚印
    art: { geom: { pantry: { rect: [440, 160, 328, 160], props: [
      [472, 164, 296, 42], [724, 164, 44, 152], [560, 208, 16, 24], [636, 208, 16, 24],
      [488, 206, 18, 26], // bin：中心 (497,219)
      [450, 172, 26, 48],
    ] } } },
    desks: { release() {} },
    emoter: { emote() {} },
  };
}

const member = (b, name, x, y, over = {}) => {
  const a = new Actor({ name, color: '#d88' }, x, y, null);
  Object.assign(a, over);
  b.actors.set(name, a);
  return a;
};

test('派单不预扣库存：出发时架上份数不动，目标＝货架前台面', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.energy.set('小鹿', 5); // < critAt：强制觅食（确定性）
  const a = member(b, '小鹿', 300, 300, { working: true, deskIdx: 2 });
  e.forageBeat();
  assert.ok(a.eating, '吃链已发起');
  assert.equal(a.eating.phase, 'walk');
  assert.equal(e.snacks, EPARAMS.snackCapacity, '库存到手才扣——出发不预扣');
  assert.equal(a.hasTarget, true);
  assert.equal(a.tx, 440 + 328 - 30, '货架前台面 (738,334)');
  assert.equal(a.ty, 160 + 160 + 14);
  assert.equal(a.working, false, 'working 锚已摘——坐着派目标不动是旧疤');
});

test('一帧自弃的旧疤钉：到位开吃后连步数十帧，吃链必须还活着', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.energy.set('小鹿', 5);
  const a = member(b, '小鹿', 700, 330);
  e.forageBeat();
  a.hasTarget = false; a.moving = false; // 模拟到达
  e.stepEat(a, 0.1); // 到位→开吃（holding='food'）
  assert.equal(a.holding, 'food');
  assert.equal(a.eating.phase, 'eat');
  for (let i = 0; i < 40; i++) e.stepEat(a, 0.05); // 旧世界：第 1 帧后即自弃
  assert.ok(a.eating, '吃链仍在中（自己的 food 道具不再是「别人的取材」）');
  assert.equal(a.eating.phase, 'eat');
  assert.equal(e.snacks, EPARAMS.snackCapacity - 1, '开吃那一刻扣一份');
});

test('全链：吃完回体力＋走到桶边丢——桶计数 +1，链清场', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.energy.set('小鹿', 10);
  const a = member(b, '小鹿', 700, 330);
  e.forageBeat();
  a.hasTarget = false; a.moving = false;
  e.stepEat(a, 0.1); // 开吃
  for (let i = 0; i < 30; i++) e.stepEat(a, 0.1); // 3s > 2.5s：吃完
  assert.equal(a.eating.phase, 'toss');
  assert.equal(Math.round(e.en('小鹿')), 40, '10 + eatGain(30)');
  assert.equal(a.working, false, '吃位锚已摘——丢垃圾的路走得动');
  assert.ok(a.hasTarget, '去桶边的目标在位');
  a.x = 517; a.y = 225; a.hasTarget = false; a.moving = false; // 到桶边（距桶心 ~21px，站定距离内）
  e.stepEat(a, 0.1);
  assert.equal(e.bin, 1, '垃圾进桶');
  assert.equal(a.eating, null, '链清场');
  assert.equal(a.holding, null);
  assert.equal(a.foodKey, '');
});

test('满桶落地：bin 到帽时丢垃圾改落地残骸（软阻挡数据面有输入了）', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.bin = EPARAMS.binCapacity;
  e.energy.set('小鹿', 10);
  const a = member(b, '小鹿', 700, 330);
  e.forageBeat();
  a.hasTarget = false; a.moving = false;
  e.stepEat(a, 0.1);
  for (let i = 0; i < 30; i++) e.stepEat(a, 0.1);
  a.x = 517; a.y = 225; a.hasTarget = false; a.moving = false; // 到桶边
  e.stepEat(a, 0.1);
  assert.equal(e.bin, EPARAMS.binCapacity, '桶不超帽');
  assert.equal(e.litter.length, 1, '落地一件');
});

test('中断纪律：吃相半被派活——零残留散场（链/道具/锚全清）', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.energy.set('小鹿', 5);
  const a = member(b, '小鹿', 700, 330);
  e.forageBeat();
  a.hasTarget = false; a.moving = false;
  e.stepEat(a, 0.1); // 开吃
  b.busy.add('小鹿');
  e.stepEat(a, 0.1);
  assert.equal(a.eating, null);
  assert.equal(a.holding, null);
  assert.equal(a.foodKey, '');
  assert.equal(a.working, false);
});

test('售罄到位判：走到发现空架——不扣不吃，confused 散场', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.snacks = 0;
  e.energy.set('小鹿', 5);
  const a = member(b, '小鹿', 700, 330);
  e.forageBeat();
  assert.ok(a.eating, '架空也走过去（真源在架上，白走一趟的失望是戏）');
  a.hasTarget = false; a.moving = false;
  e.stepEat(a, 0.1);
  assert.equal(a.eating, null);
  assert.equal(a.holding, null);
  assert.equal(e.snacks, 0);
});

test('走位死线：被堵 15s——零损失散场（库存未扣）', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  e.energy.set('小鹿', 5);
  const a = member(b, '小鹿', 300, 300);
  e.forageBeat();
  b.clock += 16; // 走位 16s 还没到
  a.hasTarget = true; a.moving = true; // 仍在途（被堵）
  e.stepEat(a, 0.1);
  assert.equal(a.eating, null, '死线散场');
  assert.equal(e.snacks, EPARAMS.snackCapacity, '库存零损失');
});

test('觅食资格：忙人/会中人/歇脚中/取材在途者不派（一口径）', () => {
  const b = fakeBoard();
  const e = new EnergyEngine(b);
  const busyA = member(b, '忙人', 300, 300); b.busy.add('忙人');
  const meetA = member(b, '会人', 300, 300, { meetIdx: 1 });
  const fetchA = member(b, '取材人', 300, 300, { fetching: true });
  for (const a of [busyA, meetA, fetchA]) e.energy.set(a.name, 5);
  e.forageBeat();
  assert.equal(busyA.eating, null);
  assert.equal(meetA.eating, null);
  assert.equal(fetchA.eating, null);
});

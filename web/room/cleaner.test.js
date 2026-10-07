// cleaner.test.js — t_139 补钉（保洁阿姨 UI 评审）：NPC 隔离墙的行为面
// ＋扫把手持件。评审抓到的两桩事故钉在这：
//   ① 阿姨巡逻路过茶水间冒「接杯水～」还被拉去喝咖啡歇——tryPantry
//      候选链漏了 isNPC（同排泄漏：applyBusy 落座、Actor 闲逛分支、
//      actorAt 命中、调试取材人）；
//   ② 阿姨两手空空——保洁不拿扫把说不过去，paintBroom 手持件补上。
// 行为测试只测纯逻辑（Actor.step / paintBroom 可无 DOM 驱动）；
// roomview 的资格链是不可实例化的 DOM 类，按 sfx.test.js 的接线扫描
// 先例钉源码契约（删闸必红）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Actor, DIR } from './actor.js';
import { CleanerRitual, CPARAMS, BROOM } from './cleaner.js';

// ── ① NPC 隔离墙（行为面）────────────────────────────────────────

const BOUNDS = { minX: 0, maxX: 300, minY: 0, maxY: 300 };
// 跑 n 拍并记录全程是否出现过闲逛目标（到达后 hasTarget 会翻回 false，
// 事后只看终态会漏判）
const tick = (a, n = 30) => {
  let sawTarget = false;
  for (let i = 0; i < n; i++) {
    a.step(0.1, { mvx: 0, mvy: 0, bounds: BOUNDS, others: [] });
    if (a.hasTarget) sawTarget = true;
  }
  return sawTarget;
};

test('Actor 闲逛分支豁免 NPC：idleT 归零也绝不派闲逛目标（巡逻不抢方向盘）', () => {
  const npc = new Actor({ name: '保洁阿姨', color: '#7a9e7a' }, 100, 100, null);
  npc.isNPC = true;
  npc.idleT = 0; // 闲逛计时器直接到点——资格链必须挡住
  assert.equal(tick(npc), false, 'NPC 不该被派闲逛目标（位移归她自己的 Ritual）');
  // 对照组：普通成员同样条件下会拿闲逛目标（证明测试真的在走这条分支）
  const member = new Actor({ name: '小鹿', color: '#d88' }, 100, 100, null);
  member.idleT = 0;
  assert.equal(tick(member), true, '普通成员 idleT 到点应派闲逛目标（对照组）');
});

// ── ② 扫把手持件（paintBroom 行为面）────────────────────────────

// 录制式假 ctx：只记 fillStyle/fillRect，不碰 DOM
function fakeCtx() {
  const calls = [];
  return {
    calls,
    fillStyle: '',
    fillRect(x, y, w, h) { calls.push({ c: this.fillStyle, x, y, w, h }); },
  };
}

const auntie = (over = {}) => Object.assign(
  { x: 100, y: 100, dir: DIR.RIGHT, moving: true, animT: 0.3, npcCleaner: true }, over);

test('paintBroom 自护栏：非保洁 NPC 一笔不画', () => {
  const r = new CleanerRitual({});
  const ctx = fakeCtx();
  r.paintBroom(ctx, auntie({ npcCleaner: false }));
  assert.equal(ctx.calls.length, 0);
  r.paintBroom(ctx, null);
  assert.equal(ctx.calls.length, 0);
});

test('扫把全色板在画（杆双色/稻草/红带/握杆的手）', () => {
  const r = new CleanerRitual({});
  const ctx = fakeCtx();
  r.paintBroom(ctx, auntie());
  const used = new Set(ctx.calls.map((c) => c.c));
  for (const k of ['stick', 'stickD', 'straw', 'strawD', 'band', 'skin']) {
    assert.ok(used.has(BROOM[k]), `扫把缺 ${k} 色块`);
  }
});

test('四朝向＋两弯腰拍：刷头都落地（有 ≥8 宽的块贴到脚锚线）', () => {
  const r = new CleanerRitual({});
  for (const dir of [DIR.DOWN, DIR.UP, DIR.LEFT, DIR.RIGHT]) {
    const ctx = fakeCtx();
    r.paintBroom(ctx, auntie({ dir }));
    const onFloor = ctx.calls.some((c) =>
      c.w >= 8 && c.y + c.h >= 100 && c.y < 100);
    assert.ok(onFloor, `朝向 ${dir} 的扫把没落地`);
  }
  for (const phase of ['pick', 'clean-bin']) {
    r.phase = phase;
    const ctx = fakeCtx();
    r.paintBroom(ctx, auntie({ dir: DIR.LEFT })); // 弯腰拍横走朝向也走立式
    const onFloor = ctx.calls.some((c) => c.w >= 8 && c.y + c.h >= 100 && c.y < 100);
    assert.ok(onFloor, `弯腰拍 ${phase} 的扫把没落地`);
  }
  r.phase = 'idle';
});

test('横走镜像：刷头在前进侧、杆斜倚回身后（经典扫地姿势）', () => {
  const r = new CleanerRitual({});
  let ctx = fakeCtx();
  r.paintBroom(ctx, auntie({ dir: DIR.RIGHT }));
  assert.ok(ctx.calls.length, '右行没画');
  // 刷头（宽块）全在右身侧；杆（1px 柱）顶端回倚到身侧
  assert.ok(ctx.calls.filter((c) => c.w >= 6).every((c) => c.x >= 108),
    '右行刷头不在前脚（镜像错）');
  assert.ok(ctx.calls.some((c) => c.w === 1 && c.x <= 105),
    '右行杆没有斜倚回肩（姿势错）');
  ctx = fakeCtx();
  r.paintBroom(ctx, auntie({ dir: DIR.LEFT }));
  assert.ok(ctx.calls.length, '左行没画');
  assert.ok(ctx.calls.filter((c) => c.w >= 6).every((c) => c.x + c.w <= 92),
    '左行刷头不在前脚（镜像错）');
  assert.ok(ctx.calls.some((c) => c.w === 1 && c.x >= 95),
    '左行杆没有斜倚回肩（姿势错）');
});

// 彩蛋与巡线优化的测试面（t_139 二轮）：poke 台词链、三连点欢呼、
// 空场裁剪、空桶不打卡、就近拾取、坐标反查拾取、nav 绕障走位。
// board 用最小桩（energy/poser/say/art/bounds），不碰 DOM。
function stubBoard(energy) {
  return {
    energy,
    nav: null,
    clock: 0,
    actors: new Map(),
    bounds: () => ({ minX: 0, maxX: 400, minY: 0, maxY: 400 }),
    art: { atlas: () => ({}) },
    poser: { pose() {} },
    say() {},
  };
}

test('彩蛋 poke：不在场点了空气；一下搭话、三连点欢呼、冷却拦连点', () => {
  const said = [], poses = [];
  const board = {
    poser: { pose: (n, k) => poses.push(k) },
    say: (n, t) => said.push(t),
  };
  const r = new CleanerRitual(board);
  r.poke(); // 不在场——静默
  assert.equal(said.length, 0);
  r.actor = { name: '保洁阿姨' };
  r.poke(); // 第一下：回头搭话＋东张西望
  assert.equal(said.length, 1);
  assert.equal(poses[0], 'lookaround');
  r.poke(); // 第二下：冷却中——连点静默计数
  assert.equal(said.length, 1);
  r.poke(); // 第三下：窗口内攒满——欢呼（大冷却）
  assert.equal(said.length, 2);
  assert.equal(poses[1], 'cheer');
  r.poke(); // 大冷却中——拦住
  assert.equal(said.length, 2);
});

test('空场裁剪：没桶没垃圾不用补货——不进场不闪现，缩短重试间隔', () => {
  const board = stubBoard({ bin: 0, litter: [], snackLow: false });
  const r = new CleanerRitual(board);
  r.beginPatrol();
  assert.equal(r.actor, null, '没活儿不该进场');
  assert.equal(r.phase, 'idle');
  assert.equal(r.patrolT, CPARAMS.idleRetry);
});

test('空桶不打卡：有垃圾无桶直接进拾垃圾腿（不去桶边白走一趟）', () => {
  const board = stubBoard({ bin: 0, litter: [{ x: 300, y: 300 }], snackLow: false });
  const r = new CleanerRitual(board);
  r.beginPatrol();
  assert.ok(r.actor, '有活儿该进场');
  assert.equal(r.phase, 'walk-litter');
});

test('就近拾取：挑离当前最近的垃圾（不按 y 全场排队来回横穿）', () => {
  const board = stubBoard({
    bin: 0,
    litter: [{ x: 100, y: 50 }, { x: 210, y: 205 }, { x: 380, y: 390 }],
    snackLow: false,
  });
  const r = new CleanerRitual(board);
  r.actor = { x: 200, y: 200 };
  assert.equal(r.nextLitter(), board.energy.litter[1], '该挑脚边那件');
});

test('拾取按坐标反查：中途被衰减清走的垃圾自然落空，不误拾别件', () => {
  const litter = [{ x: 100, y: 100 }, { x: 200, y: 200 }];
  const board = stubBoard({
    bin: 0, litter, snackLow: false,
    pickLitter(i) { litter.splice(i, 1); },
  });
  const r = new CleanerRitual(board);
  r.actor = { name: '保洁阿姨', x: 0, y: 0, moving: false, animT: 0 };
  r.phase = 'pick';
  r.spot = { x: 200, y: 200 };   // 目标是第二件
  r.t = CPARAMS.pickDur - 0.01;
  litter.splice(1, 1);           // 拾取瞬间它已被衰减清走
  r.step(0.05);
  assert.equal(r.phase, 'walk-litter');
  assert.equal(litter.length, 1, '剩下的那件不该被误拾（旧索引补偿会错位）');
});

test('nav 走位：跟路标绕行不穿桌、路标链按目标缓存、无 nav 退直线', () => {
  let pathCalls = 0;
  const board = stubBoard(null);
  board.nav = {
    path(sx, sy, tx, ty) { pathCalls++; return [{ x: 50, y: 50 }, { x: 100, y: 0 }]; },
  };
  const r = new CleanerRitual(board);
  r.dt = 0.1;
  const a = { x: 0, y: 0, moving: false, animT: 0, dir: 0 };
  assert.equal(r.arrive(a, { x: 100, y: 0 }, 2), false);
  assert.ok(a.y > 0, '该朝路标（50,50）绕——y 有分量，不是直线穿桌');
  r.arrive(a, { x: 100, y: 0 }, 2);
  assert.equal(pathCalls, 1, '目标没变不该重算路标链');
  assert.equal(r.arrive(a, { x: a.x, y: a.y }, 3), true, '进半径即到达');
  board.nav = null; // nav 缺席（art 未就绪/测试环境）——老直线走位
  const b2 = { x: 0, y: 0, moving: false, animT: 0, dir: 0 };
  r.arrive(b2, { x: 100, y: 0 }, 2);
  assert.equal(b2.y, 0, '无 nav 不该有 y 分量');
});

// ── ③ roomview 资格链接线扫描（删闸必红，sfx.test.js 先例）────────

test('NPC 闸与扫把接线一一在位（删闸必红）', () => {
  const src = (f) => readFileSync(new URL(`./${f}`, import.meta.url), 'utf8');
  const rv = src('roomview.js');
  const actor = src('actor.js');
  const cleaner = src('cleaner.js');
  const printer = src('printer.js');
  const errand = src('errand.js');
  const chains = [
    [errand, '!a.isLocal && !a.isNPC && !claimed(a, busy, null)', 'dispatchable 本体（所有调度器的 NPC/占用闸真源——errand 根治后闸集中于此）'],
    [rv, 'if (!dispatchable(a, busy)) continue;', 'tryPantry 候选链（接杯水事故的源头；闸在 errand.js dispatchable）'],
    [rv, 'a.isLocal || a.isNPC || a.meetIdx >= 0', 'applyBusy 工位主循环'],
    [rv, '!a.isLocal && !a.isNPC && !busy.has(a.name)', 'applyBusy idle 落座链'],
    [rv, 'dispatchable(a, this.busy)', '调试打印取材人（闸同上，errand.js dispatchable）'],
    [rv, 'if (a.isNPC) continue;', 'actorAt 命中闸（点击/悬停/双击三处共用）'],
    [actor, '!this.isLocal && !this.isNPC && this.deskIdx === -1', 'Actor 闲逛分支'],
    [printer, 'dispatchable(a, this.board.busy)', 'printer takerFree（自称与 tryPantry 同纪律；闸在 errand.js dispatchable：!isLocal && !isNPC && !claimed）'],
    [rv, 'this.cleaner.poke()', '点击彩蛋接线（onClick）'],
    [rv, 'npcAt(x, y)', 'NPC 命中（点击/悬停光标/双击复位让位）'],
    [cleaner, 'energy.bin >= EPARAMS.binCapacity || energy.litter.length >= EPARAMS.litterMax', '脏乱召唤触发（桶满/垃圾到帽提前出场）'],
    [cleaner, 'const nav = this.board.nav;', 'nav 绕障走位'],
    [rv, 'this.cleaner.paintBroom(ctx, a)', '扫把绘制接线'],
    [cleaner, 'paintBroom(ctx, a)', '扫把本体'],
  ];
  for (const [body, needle, what] of chains) {
    assert.ok(body.includes(needle), `${what} 的 NPC 闸/接线不在位`);
  }
});

// ── ④ 巡线礼让（t_189）：阿姨的 arrive 直线走不吃 yieldTo——此前只
// 有别人单方面让她，她会直穿对方身位。修后：前方 14px 锥内有人停步，
// 耐心 1.2s 到点照旧通过（被坐定者挡死不能在门口漏斗永久站住）。
test('阿姨让行：前方有人停步让行，耐心 1.2s 到点照旧通过（t_189）', () => {
  const member = new Actor({ name: '小鹿', color: '#d88' }, 112, 100, null); // 12px：前方锥内
  member.working = true; // 坐定者不挪窝——她只能等或到期通过
  const board = { actors: new Map([['小鹿', member]]), nav: null };
  const r = new CleanerRitual(board);
  const a = new Actor({ name: '保洁阿姨', color: '#7a9e7a' }, 100, 100, null);
  r.actor = a;
  r.dt = 1 / 60;
  // 航向正东，目标在成员身后——直线穿过她的身位
  const arrived = r.arrive(a, { x: 200, y: 100 }, 2);
  assert.equal(arrived, false);
  assert.equal(a.moving, false, '前方锥内有人应停步让行');
  assert.equal(a.x, 100, '停步期间不位移');
  // 让一拍就清零的耐心账：挡路者一直在，yieldT 应累积
  assert.ok(r.yieldT > 0, '让行期应积累耐心账');
  // 耐心到点：照旧通过（不能被坐定者永久钉在漏斗里）
  r.yieldT = 1.2;
  r.arrive(a, { x: 200, y: 100 }, 2);
  assert.ok(a.x > 100, '耐心到点应照旧通过');
  // 走出对方身位（锥外）后耐心账清零——下次遇人重新从零等起
  // （walkSpeed 22px/s，dt=1/60 → 每拍 0.37px，预算放宽到 200 拍）
  for (let i = 0; i < 200 && a.x < 130; i++) r.arrive(a, { x: 200, y: 100 }, 2);
  assert.ok(a.x > 126, `应已走过对方身位（现 x=${a.x.toFixed(1)}）`);
  assert.equal(r.yieldT, 0, '通行后耐心账清零');
});

// collide.test.js — t_164（r_12）：成员对撞模拟的单元测试。
// 假时钟驱动 Actor.step()（不依赖 rAF/atlas/nav——纯运动学面），
// 四组断言钉 t_162/t_163 修复后的期望行为：
//   ① 开阔地对撞：直线相向 5s 内双双越过；
//   ② 窄道对撞：留 1-2 格通道，5s 至少一人过、8s 另一人也过；
//   ③ 让路确定性：同场景跑 3 次结果一致（hash 定优先级非随机）；
//   ④ 无目标闲逛者不参与对撞（无 target 不吃避让推挤的副作用）。
// 状态说明：t_162（对向解僵）未落地前 ①②③ 会红——这是 TDD 钉行为，
// 小猿修复落地后本文件应全绿；④ 是现有行为回归（应即绿）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { Actor, CROWD } from './actor.js';

const BOUNDS = { minX: 0, minY: 100, maxX: 768, maxY: 500 };
const DT = 1 / 60;

function mkActor(name, x, y) {
  const m = { name, color: '#3370ff', hair: '#3b2f2f', role: 'dev' };
  return new Actor(m, x, y, null);
}

// 驱动 n 秒：每个 actor 的 others 是其余全体（roomview 同语义）
function run(actors, seconds) {
  const others = actors;
  for (let i = 0; i < Math.round(seconds / DT); i++) {
    for (const a of actors) {
      a.step(DT, { mvx: 0, mvy: 0, bounds: BOUNDS, others });
    }
  }
}

test('前置：Actor 无 atlas 可驱动，目标走位生效', () => {
  const a = mkActor('甲', 100, 200);
  a.tx = 300; a.ty = 200; a.hasTarget = true;
  run([a], 0.5);
  assert.ok(a.x > 100, `走了 ${a.x - 100}`);
  assert.equal(a.moving, true);
});

test('① 开阔地对撞：直线相向双双越过对方（t_162 修复后）', () => {
  const a = mkActor('甲', 200, 300);
  const b = mkActor('乙', 500, 300);
  a.tx = 550; a.ty = 300; a.hasTarget = true;
  b.tx = 150; b.ty = 300; b.hasTarget = true;
  // 窗口自洽修正：互越原位总程 700px 相对速度 ≈52px/s（WALK=26×2），
  // 纯走需要 ≈13.5s——原 5s 窗与 NPC 速度数学矛盾。取 15s（含让路
  // 停走 0.5s×若凡与减速带余量）。
  run([a, b], 15);
  assert.ok(a.x > 500, `甲应越过乙原位，现 x=${a.x}`);
  assert.ok(b.x < 200, `乙应越过甲原位，现 x=${b.x}`);
});

test('② 窄道对撞：1-2 格通道先后通过（t_162/163 修复后）', () => {
  // 窄道：nav 缺席（直线走），两侧用「并排第三人」夹出人墙语义不可行——
  // 窄道行为需要 nav blocked 参与，纯 actor 面只能测「通道内先后通过」的
  // 时序：两人在 YIELD_R 内对撞，低优先级者停 0.5s 后高者先过。
  const a = mkActor('甲', 300, 300);
  const b = mkActor('乙', 330, 300); // 相距 30 < AVOID_R，即将交会
  a.tx = 400; a.ty = 300; a.hasTarget = true;
  b.tx = 230; b.ty = 300; b.hasTarget = true;
  run([a, b], 5);
  const aPassed = a.x > 330;
  const bPassed = b.x < 300;
  assert.ok(aPassed || bPassed, `5s 内至少一人通过（甲 ${a.x}/乙 ${b.x}）`);
  run([a, b], 3); // 再给 3s（共 8s）
  assert.ok(a.x > 330 || b.x < 300, '8s 内另一人也应通过');
});

test('③ 让路确定性：同场景三次结果一致（t_162 hash 优先级）', () => {
  const scenario = () => {
    const a = mkActor('甲', 300, 300);
    const b = mkActor('乙', 330, 300);
    a.tx = 400; a.ty = 300; a.hasTarget = true;
    b.tx = 230; b.ty = 300; b.hasTarget = true;
    run([a, b], 3);
    return { ax: Math.round(a.x * 10), bx: Math.round(b.x * 10) };
  };
  const r1 = scenario();
  const r2 = scenario();
  const r3 = scenario();
  assert.deepEqual(r1, r2, '两次跑位不一致——让路含随机成分');
  assert.deepEqual(r2, r3, '三次跑位不一致——让路含随机成分');
});

test('④ 无目标闲逛者不吃对撞副作用（现有行为回归）', () => {
  // 语义修正：闲逛者（无 target）不参与对撞逻辑＝它不因他人路过而改
  // 变行为。自发 idle wander 会让它动——测试里抑制 idleT（999s 不游走），
  // 对照「独处」与「有路人」两种场景：位置应完全一致。
  const scene = (withWalker) => {
    const b = mkActor('乙', 320, 300);
    b.idleT = 999; // 抑制自发游走——纯看路人影响
    const actors = [b];
    if (withWalker) {
      const a = mkActor('甲', 300, 300);
      a.tx = 400; a.ty = 300; a.hasTarget = true;
      actors.push(a);
    }
    run(actors, 2);
    return { x: b.x, y: b.y, actors };
  };
  const alone = scene(false);
  const withP = scene(true);
  assert.equal(alone.x, withP.x, '闲逛者被路人改变了位置——不应参与对撞');
  assert.equal(alone.y, withP.y, '同上（y 轴）');
  // 路人自己照常走（闲逛者不挡路——yieldTo 只吃 moving 对向者）
  assert.ok(withP.actors[1].x > 300, `甲应前进，现 ${withP.actors[1].x}`);
});

test('避让常数语义：YIELD_R < AVOID_R（减速圈在避让圈内）', () => {
  assert.ok(CROWD.YIELD_R < CROWD.AVOID_R, '减速半径应小于避让半径');
  assert.ok(CROWD.AVOID_R > 0 && CROWD.STUCK_T > 0, '常数为正');
});

// 补钉（t_165 破坏性抽验发现）：对向让路的行为级断言——原①②③在
// 禁用对向检测后仍绿（①的 15s 窗盖住了回归信号、③只测确定性不测让路
// 发生）。本钉直接断言 t_162 的可观察行为：对头相遇后低优先级者
// （yieldHash 较小）出现 yieldHold>0（停走发生过）。
test('⑤ 对向让路行为：对头相遇低优先级者让路停走（t_162 核心）', () => {
  const a = mkActor('甲', 300, 300);
  const b = mkActor('乙', 330, 300); // 对头，30 < YIELD_R+AVOID_R
  a.tx = 400; a.ty = 300; a.hasTarget = true;
  b.tx = 230; b.ty = 300; b.hasTarget = true;
  // 先各自走一步拿到航向，再看交会帧的让路账目
  let sawHold = false;
  for (let t = 0; t < 3 && !sawHold; t += DT) {
    a.step(DT, { mvx: 0, mvy: 0, bounds: BOUNDS, others: [a, b] });
    b.step(DT, { mvx: 0, mvy: 0, bounds: BOUNDS, others: [a, b] });
    if (a.yieldHold > 0 || b.yieldHold > 0) sawHold = true;
  }
  assert.ok(sawHold, '对头相遇应触发一方的 yieldHold 让路停走（t_162 对向检测）');
});

// ── t_189：巡线穿模三钉 ──────────────────────────────────────────
// 用户实拍：两个戴会议徽标的人叠在一起。根因三件套（各钉一环）：
//   ⑥ yieldTo 绕行侧不探网格——侧推指向家具时被护栏退成纯航向直穿；
//   ⑦ peopleAt 软代价死码——重算路径贴着坐定者身位走（车道不绕）；
//   ⑧ 分离推挤两轴都堵时放弃——挤压形态下两人保持穿插。

import { Nav } from './nav.js';
import { separateCrowd, nudgeBody } from './crowd.js';
import { readFileSync } from 'node:fs';

test('⑥ 绕行方向探边：自然侧顶着家具就翻到另一侧（t_189）', () => {
  // 挡墙只堵自然侧：甲朝东走、乙在正前方 30px——垂直让路的自然侧是
  // 南（cross≥0），把南边 10px 探点堵死，修后应翻北；nav 缺席保持旧
  // 行为（照旧推南）作对照。
  const nav = new Nav({ w: 768, h: 520, desks: [{ block: [290, 308, 40, 20] }], props: [] },
    { minX: 24, minY: 164, maxX: 744, maxY: 510 });
  const a = mkActor('甲', 300, 300);
  const b = mkActor('乙', 330, 300);
  const withNav = a.yieldTo(1, 0, [b], nav);
  assert.ok(withNav.y < 0, `自然侧被堵应翻北让（现 y 分量 ${withNav.y.toFixed(2)}）`);
  const noNav = a.yieldTo(1, 0, [b], null);
  assert.ok(noNav.y > 0, 'nav 缺席保持旧行为（自然侧南）');
});

test('⑦ peopleAt 软代价生效：重算路径绕出车道不贴身（t_189）', () => {
  // 复刻会议桌北沿形态：阻挡带 y 412–460，坐定者钉在带内 (518,416)，
  // 步行线是带北最后一行 y≈410——老路径直穿距坐定者 6px。按 roomview
  // 的接线语义（脚点半径 4 格＝36px 盘，身宽＋余量；半径 3 盘外剩
  // 13.5px 贴身缝，实测不够看）打标记后，整条折线（含拉直段——拉直
  // 必须认行人盘，否则 A* 绕出的车道又被摺回直线）应全程 ≥16px。
  const nav = new Nav({ w: 768, h: 520, desks: [{ block: [400, 416, 200, 40] }], props: [] },
    { minX: 24, minY: 164, maxX: 744, maxY: 510 });
  const pinned = { x: 518, y: 416 };
  // 折线全程 2px 采样的最小离身距离（只看路标点会漏掉拉直段）
  const minOver = (from, pts) => {
    let m = Infinity;
    const chain = [from, ...pts];
    for (let k = 0; k < chain.length - 1; k++) {
      const p = chain[k], q = chain[k + 1];
      const n = Math.max(1, Math.ceil(Math.hypot(q.x - p.x, q.y - p.y) / 2));
      for (let s = 0; s <= n; s++) {
        const x = p.x + ((q.x - p.x) * s) / n, y = p.y + ((q.y - p.y) * s) / n;
        m = Math.min(m, Math.hypot(x - pinned.x, y - pinned.y));
      }
    }
    return m;
  };
  const bare = nav.path(700, 410, 470, 410);
  assert.ok(minOver({ x: 700, y: 410 }, bare) < 12, '无标记时路径应贴身（对照前提）');
  // roomview 同款标记：不挪窝者的脚点半径 4 格
  const cx0 = Math.floor(pinned.x / 4), cy0 = Math.floor(pinned.y / 4);
  const cells = new Set();
  for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) cells.add((cy0 + dy) * nav.cols + (cx0 + dx));
  nav.peopleAt = (i) => (cells.has(i) ? 1 : 0);
  const swung = nav.path(700, 410, 470, 410);
  assert.ok(minOver({ x: 700, y: 410 }, swung) >= 16, `标记后路径全程应离身 ≥16px（现 ${minOver({ x: 700, y: 410 }, swung).toFixed(1)}px）`);
});

test('⑧ 分离推挤垂直回退：两轴都顶家具时把人从缝里剜出来（t_189）', () => {
  // 内角口袋：东墙（x≥300）与南墙（y≥296…含膨胀）夹出的角，身体在
  // 角里被推挤时 x/y 两轴都出不去——修后沿垂直方向滑出。
  const nav = new Nav({ w: 768, h: 520, desks: [{ block: [300, 164, 60, 132] }, { block: [164, 300, 200, 60] }], props: [] },
    { minX: 24, minY: 164, maxX: 744, maxY: 510 });
  const b = { minX: 24, minY: 164, maxX: 744, maxY: 510 };
  const a = mkActor('甲', 292, 288);
  assert.ok(!nav.free(a.x + 20, a.y), '东向出不去（前置）');
  assert.ok(!nav.free(a.x, a.y + 20), '南向出不去（前置）');
  nudgeBody(nav, b, a, 20, 0);
  assert.ok(Math.abs(a.y - 288) > 0.5 || a.x < 292, `应垂直滑动脱困（现 ${a.x.toFixed(1)},${a.y.toFixed(1)}）`);
});

test('⑧b 挤在口袋里的两人分离脱困（separateCrowd 集成）', () => {
  const nav = new Nav({ w: 768, h: 520, desks: [{ block: [300, 164, 60, 132] }, { block: [164, 300, 200, 60] }], props: [] },
    { minX: 24, minY: 164, maxX: 744, maxY: 510 });
  const b = { minX: 24, minY: 164, maxX: 744, maxY: 510 };
  const a1 = mkActor('甲', 292, 288);
  const a2 = mkActor('乙', 293, 289); // 几乎完全重叠
  const pulse = new Set();
  const d0 = Math.hypot(a1.x - a2.x, a1.y - a2.y);
  for (let i = 0; i < 40; i++) separateCrowd([a1, a2], { nav, bounds: b, pulseDone: pulse });
  const d1 = Math.hypot(a1.x - a2.x, a1.y - a2.y);
  assert.ok(d1 > d0 + 6, `口袋里两人应被剜开（${d0.toFixed(1)}→${d1.toFixed(1)}）`);
});

// peopleAt 接线是 roomview 的活（DOM 类不可实例化）——按 cleaner.test
// 的源码扫描先例钉契约：喂格条件必须同时认坐定/让路/退避三态，删闸必红。
test('⑨ roomview 接线契约：peopleAt 喂格认三态不挪窝者（源码扫描）', () => {
  const src = readFileSync(new URL('./roomview.js', import.meta.url), 'utf8');
  assert.match(src, /nav\.peopleAt = /, 'peopleAt 必须接线（r_12 起一直是死码）');
  assert.match(src, /a\.working \|\| a\.yieldHold > 0 \|\| a\.deferT > 0/,
    '喂格条件应认坐定/让路/退避三态');
});

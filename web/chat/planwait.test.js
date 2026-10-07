// planwait.test.js — 提案 submitted 卡的等待行（时间显示＋代收预计）与
// 直接推进的契约面。第一性：自动推进的「不推进」几乎总是一口看不见的
// 钟在走——宽限没满、房主在场让位、槽已换代——钟不亮出来，房主只能
// 眼睁睁等；等待行把已候时长与预计代收时刻亮在卡上，直接推进给房主
// 本人的手一条跳过宽限的近路（引擎的代行只服务缺席）。planWaitText 纯
// 函数直测各档钟面；卡片渲染走 streamnav 同款 stage 冒烟（房主出钮、
// 访客不出）；点击分发按 gitcard 先例做源码断言（stub 世界 closest 恒
// null，真点击不可达——#onClick 的处理存在性钉在源面上）。
import test from 'node:test';
import assert from 'node:assert';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dom, fetchStub } from '../room/testdom.js';

dom();
fetchStub([{ auto_advance: true, knobs: { accept_delay_s: 120 } }]);

const { StreamView, planWaitText, OWNER_PRESENCE_S } = await import('./stream.js');
const { Msg } = await import('../wire/wire.js');

const T0 = 1791000000; // 任取的整秒锚（2026-09-30 附近，与人无关）

const planFrame = (over = {}) => ({
  type: Msg.Plan, event: 'submitted', from: '小苗', ts: T0,
  plan: { id: 'p_01', project_key: 'r', submitted_by: '小苗', submitted_ts: T0, ...over },
});

test('宽限未满：已候＋预计（宽限终点）自动代收', () => {
  const s = planWaitText(planFrame(), {
    now: T0 + 30, advance: true, acceptDelayS: 120, ownerLastActiveS: 0,
  });
  assert.match(s, /已候 刚刚/, '候时在报');
  assert.match(s, /预计 .* 自动代收/, '预计时刻在报（ETA=提交+宽限）');
  assert.ok(!s.includes('在场'), '房主没说过话：不报让位');
});

test('候时口语化随窗升档：刚刚 → 分钟 → 小时', () => {
  const at = (sec) => planWaitText(planFrame(), {
    now: T0 + sec, advance: false, // 推进关：只剩候时一档，升档看得最干净
  });
  assert.match(at(30), /已候 刚刚/);
  assert.match(at(5 * 60), /已候 5 分钟/);
  assert.match(at(3 * 3600 + 1200), /已候 3 小时/);
});

test('房主在场让位：宽限已满但 ETA 被在场窗压后，措辞明说让位', () => {
  const saidAt = T0 + 300; // 提交 5 分钟后房主在房里说了话
  const s = planWaitText(planFrame(), {
    now: T0 + 400, advance: true, acceptDelayS: 120, ownerLastActiveS: saidAt,
  });
  assert.match(s, /已候 6 分钟/, '候时在报');
  assert.match(s, /你在场，代收让位中/, '让位档亮明——「没推进」的真因上卡');
  // ETA＝最后发言＋在场窗（120s 宽限早被压在窗内）
  const etaS = saidAt + OWNER_PRESENCE_S;
  const eta = new Date(etaS * 1000).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
  assert.ok(s.includes(eta), `ETA ${eta}（最后发言+10min）`);
});

test('宽限已满且房主不在场：下一拍放行档', () => {
  const s = planWaitText(planFrame(), {
    now: T0 + 121, advance: true, acceptDelayS: 120, ownerLastActiveS: 0,
  });
  assert.match(s, /代收条件已满，引擎下一拍放行/, '不再报已过期的 ETA');
});

test('推进没开/房暂停：只报候时，不编引擎的钟', () => {
  const base = { now: T0 + 60, acceptDelayS: 120, ownerLastActiveS: 0 };
  assert.equal(planWaitText(planFrame(), { ...base, advance: false }), '⏱ 已候 1 分钟');
  assert.equal(planWaitText(planFrame(), { ...base, advance: true, paused: true }), '⏱ 已候 1 分钟');
});

test('槽已换代（pending=false）与无提交时刻：整行撤下', () => {
  assert.equal(planWaitText(planFrame(), { now: T0, pending: false, advance: true }), '');
  assert.equal(planWaitText({ type: Msg.Plan, event: 'submitted' }, { now: T0 }), '');
});

// ---- 渲染面（streamnav 同款 stage 冒烟） -----------------------------------

function stage(msgs = [], owner = '房主') {
  const room = {
    key: 'r', name: '项目房', members: new Map(), messages: msgs,
    questions: new Map(), acks: new Map(), reacts: new Map(),
    reads: new Map(), queues: new Map(), readsLoaded: false, readSince: 0,
    notice: null, paused: false,
  };
  const frames = [];
  const book = {
    current: 'r', rooms: new Map([['r', room]]), currentRoom: () => room,
    frameTo: (key, frame) => { frames.push({ key, frame }); return true; },
  };
  const container = document.createElement('div');
  container.querySelector = () => null;
  container.querySelectorAll = () => [];
  const v = new StreamView(container, book, owner, {
    pill: document.createElement('button'),
    jump: document.createElement('button'),
    back: document.createElement('button'),
  });
  return { v, room, container, frames };
}

test('提案卡渲染：等待行＋房主视角出 直接推进；访客不出', () => {
  const f = planFrame();
  const { v, room, container } = stage([{ frame: f }]);
  v.onChange('r', 'switch');
  const html = container.children.map((el) => el._html || '').join('\n');
  assert.match(html, /data-plan-wait/, '等待行在卡上');
  assert.match(html, /已候/, '候时在报');
  assert.match(html, /data-plan-accept/, '直接推进钮（房主视角）');
  assert.match(html, /data-plan-review/, '去审批仍在');
  assert.match(html, /⚡ 直接推进/, '钮面文案');

  const vis = stage([{ frame: f }], null);
  vis.v.onChange('r', 'switch');
  const vhtml = vis.container.children.map((el) => el._html || '').join('\n');
  assert.ok(!vhtml.includes('data-plan-accept'), '访客/只读不出推进钮（frameTo 也无写面）');
  assert.match(vhtml, /data-plan-review/, '去审批访客仍可看');
});

test('点击分发与动词（源码断言——stub 世界 closest 恒 null，真点击不可达）', () => {
  const src = readFileSync(fileURLToPath(new URL('./stream.js', import.meta.url)), 'utf8');
  assert.match(src, /\[data-plan-accept\]/, '#onClick 须接住直接推进钮');
  assert.match(src, /type: Msg\.PlanAccept, plan_id:/, '动词走 PlanAccept（审阅浮层接受的同一条腿）');
  assert.match(src, /waitTimer\.unref\?\.\(\)/, '钟摆 interval 不挂住测试进程');
});

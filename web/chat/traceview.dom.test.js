// traceview.dom.test.js — 工作过程抽屉的第一层冒烟（t_182 基座）：
// v2.9 统一后生命周期行的渲染面——注入折叠块（src 提源上标）、权限
// 问答、sys 注记都要上时间线；空态只属于没干过活的成员。
import test from 'node:test';
import assert from 'node:assert';
import { dom, fetchStub } from '../room/testdom.js';

dom();

const ENTRIES = [
  { seq: 1, ts: 1, from: '小策', kind: 'input', src: '房主', text: '【办公室消息｜来自 房主】把登录页做了' },
  { seq: 2, ts: 2, from: '小策', kind: 'ask', text: '请求权限：Bash' },
  { seq: 3, ts: 3, from: '小策', kind: 'answer', ok: true, text: '放行（策略 allow）' },
  { seq: 4, ts: 4, from: '小策', kind: 'sys', text: '注入超时（ack 未归，结果未知）——已弃单不重试' },
];
fetchStub([{ members: [{ from: '小策', working: true, entries: ENTRIES }] }]);

const { TracePanel, elapsedSuffix } = await import('./traceview.js');

// 假 book：只补 TracePanel 消费的面（rooms.get(key).members、
// foldTrace/traceOf 的 seq-upsert 语义照 rooms.js 仿写）。working 摘
// 自名册行（member_work 的真相面），与快照回包无关。
function fakeBook(working = true) {
  const room = {
    members: new Map([['小策', { working, working_since: working ? 1 : 0 }]]),
    trace: new Map(),
  };
  return {
    rooms: new Map([['p1', room]]),
    foldTrace(_key, entries) {
      for (const e of entries) {
        if (!e?.from || !e.kind || !e.seq) continue;
        let ring = room.trace.get(e.from);
        if (!ring) { ring = new Map(); room.trace.set(e.from, ring); }
        ring.set(e.seq, e);
      }
    },
    traceOf(_key, name) { return [...(room.trace.get(name)?.values() || [])]; },
  };
}

const settle = () => new Promise((r) => setTimeout(r, 30)); // hydrate 的 fetch 回包

test('生命周期行上时间线：注入带 src、问答成对、sys 注记在', async () => {
  const panel = new TracePanel(fakeBook(), {});
  panel.open('p1', '小策');
  await settle();
  const html = panel.sheet.innerHTML;
  assert.ok(html.includes('tv-input'), '注入折叠块在');
  assert.ok(html.includes('注入 · 房主'), 'src 提源上标');
  assert.ok(html.includes('tv-sys'), 'sys 注记行在');
  assert.ok(html.includes('反问'), 'ask 行在');
  assert.ok(html.includes('应答 ✓'), 'answer 行带裁定');
  assert.ok(!html.includes('tv-empty'), '不再是空态');
  panel.close();
});

test('空态只属于没干过活的成员', async () => {
  fetchStub([{ members: [{ from: '小策', working: false, entries: [] }] }]);
  const panel = new TracePanel(fakeBook(false), {});
  panel.open('p1', '小策');
  await settle();
  assert.ok(panel.sheet.innerHTML.includes('还没干过活'), '空态文案是统一后的新口径');
  panel.close();
});

test('chip 亮着的空窗按实话另说（v2.10 收口）', async () => {
  fetchStub([{ members: [{ from: '小策', working: true, entries: [] }] }]);
  const panel = new TracePanel(fakeBook(true), {});
  panel.open('p1', '小策');
  await settle();
  const html = panel.sheet.innerHTML;
  assert.ok(html.includes('工作过程正在路上'), '亮灯空窗说「在路上」');
  assert.ok(!html.includes('还没干过活'), '不得误报「没干过活」');
  panel.close();
});

test('运行态卡带实时已耗时，TaskOutput 摘要说人话（等待期不再零反馈）', async () => {
  const now = Math.floor(Date.now() / 1000);
  const runEntries = [
    { seq: 1, ts: 1, from: '小猿', kind: 'input', text: '【办公室消息｜来自 小牛】取证验证＋实锤确认' },
    { seq: 2, ts: 2, from: '小猿', kind: 'turn', state: 'start' },
    { seq: 3, ts: now - 130, from: '小猿', kind: 'tool', call: 'c1', tool: 'TaskOutput', state: 'run', input: { block: true, task_id: 'exec_ab12-3456cd' } },
    { seq: 4, ts: now - 5, from: '小猿', kind: 'tool', call: 'c2', tool: 'Bash', state: 'run', input: { command: 'go test ./dispatch/ -count=10' } },
    { seq: 5, ts: now - 40, from: '小猿', kind: 'tool', call: 'c3', tool: 'Bash', state: 'done', ms: 800 },
  ];
  fetchStub([{ members: [{ from: '小猿', working: true, entries: runEntries }] }]);
  const panel = new TracePanel(fakeBook(true), {});
  try {
    panel.open('p1', '小猿');
    await settle();
    const html = panel.sheet.innerHTML;
    assert.ok(html.includes('后台任务 exec_ab12-3456cd'), 'TaskOutput 卡摘要报出在等的后台任务');
    assert.ok(!/tv-tool-sum[^<]*>true</.test(html), '摘要不得再顶出一句 "true"');
    assert.ok(/已 2 分 \d+ 秒/.test(html), '长跑卡显示分钟级已耗时');
    assert.ok(/已 \d+ 秒/.test(html), '新起卡显示秒级已耗时');
    assert.equal((html.match(/data-tv-elapsed/g) || []).length, 2, '计时只挂在运行态卡上');
    assert.ok(html.includes('tv-chip ok'), 'done 卡不带计时芯片');
  } finally {
    panel.close(); // 断言炸了也得关——tickTimer 不泄漏，进程才退得出去
  }
});

test('elapsedSuffix 纯函数：滴答推进、边界不显（testdom 桩世界 DOM 面不可测，滴答语义在此验）', () => {
  const now = 1_000_000;
  const a = elapsedSuffix(now - 130, now);
  assert.match(a, /已 2 分 10 秒/, '130s 走分钟口径');
  assert.notEqual(a, elapsedSuffix(now - 130, now + 1), '1s 推进文案跟着走');
  assert.match(elapsedSuffix(now - 5, now), /已 5 秒/, '5s 走秒口径');
  assert.equal(elapsedSuffix(0, now), '', '空 ts 不显示');
  assert.equal(elapsedSuffix(now, now), '', '未起步不显示');
});

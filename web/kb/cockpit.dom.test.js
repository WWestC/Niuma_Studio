// cockpit.dom.test.js — t_205（r_24）：房主驾驶舱的 DOM 冒烟。四象限
// 各一例＋空数据降级＋访客档子集断言（敏感面断言族：访客帧无
// working-detail 类名——定稿 §五·6）。r_26 增⑤成本象限（耗粮多少：
// 日/周窗已耗 vs 预算三档色签、熔断态、访客不出数字）。数据面＝
// 七接口的真实载荷形状。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

// 今日日期串（本地自然日，cockpit.js fmtDate 同式）——编年史首条的
// 日期用运行时的今天：写死提交日的条目过一夜就跌出今日口径（10-02
// 的夹具 10-03 零点即红）；昨日条目保持旧日期，永远不出。
function todayStr() {
  const d = new Date(), p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// 七接口的满载回放（fetchStub 按序耗尽后重复末项——cockpit 的拉取是
// Promise.all 并发，序号即接口注册序：people/tasks/capacity/doc(chronicle)
// /achieve/visitor/budget；chronicle 走 getDoc 的 {body} 包装形）
function fullPayloads() {
  return [
    // GET /kb/people
    [{ name: '小猿', role: '前端开发', online: true, working: true, room: 'default', rank: 3 },
     { name: '小牛', role: '编排者', online: true, working: false, room: 'proj-a', rank: 8 },
     { name: '小鹿', role: '设计', online: false, rank: 2 }],
    // GET /kb/tasks
    [{ id: 't_1', title: '驾驶舱面板', assignee: '小猿', status: 'doing', updated_ts: 100 },
     { id: 't_0', title: '旧单', assignee: '小猿', status: 'done', updated_ts: 200 }],
    // GET /kb/capacity?all=1
    { rooms: [
      { room: 'default', doing: 1, seated: 2, sat: 50, open: 4, water: 3, patrol_ts: 300, updated_ts: 400 },
      { room: 'proj-a', doing: 0, seated: 1, sat: 0, open: 1, water: 3, updated_ts: 400 },
    ], now: 400 },
    // GET /kb/docs/ops/chronicle
    { body: `体例节（被解析器跳过）\n${todayStr()}｜交付｜小猿｜驾驶舱面板完工\n2026-10-01｜交付｜小牛｜昨日旧条目` },
    // GET /achieve/tests-green
    { unlocked: { 'first@小猿': 200, 'ten@小狐': 100 },
      achievements: [
        { key: 'first', name: '首单交付', text: '第一张任务单', perMember: true, medal: 0 },
        { key: 'ten', name: '累计十单', text: '十张任务单', perMember: true, medal: 1 },
      ] },
    // GET /visitor
    { on: true, count: 3 },
    // GET /budget（r_26 成本象限）：日 120万/500万=24% 预算内、
    // 周 2100万/3000万=70% 预算内（同档双窗）；熔断未触发
    { day_tokens: 5_000_000, week_tokens: 30_000_000,
      day_used: 1_200_000, week_used: 21_000_000,
      day_start: 100, week_start: 90,
      tripped_day: false, tripped_week: false,
      breaker_live: true, usage_error: false },
  ];
}

// dayStart 是「今日」口径的纯函数钉子（自然日本地零点）
test('cockpit：今日口径＝本地自然日零点（dayStart）', async () => {
  const { dayStart } = await import('./cockpit.js');
  // 2026-10-02 15:30:45 本地 → 零点 00:00:00
  const d = new Date(2026, 9, 2, 15, 30, 45);
  assert.equal(dayStart(d.getTime()), new Date(2026, 9, 2, 0, 0, 0).getTime() / 1000);
  // 恰在零点：就是零点本身
  const z = new Date(2026, 9, 2, 0, 0, 0);
  assert.equal(dayStart(z.getTime()), z.getTime() / 1000);
});

// 水位三档色签（r_13 同口径）
test('cockpit：水位三档（open≥target 绿 / target-1 黄 / 更少红）', async () => {
  const { waterLevel } = await import('./cockpit.js');
  assert.equal(waterLevel(3, 3).key, 'ok');
  assert.equal(waterLevel(5, 3).key, 'ok');
  assert.equal(waterLevel(2, 3).key, 'mid');
  assert.equal(waterLevel(1, 3).key, 'low');
  assert.equal(waterLevel(0, 3).key, 'low');
});

// 预算三档色签（r_26 成本象限，水位同形；limit≤0=不限）
test('cockpit：预算三档（used≥limit 红 / ≥80% 黄 / 额内绿 / 0 上限灰）', async () => {
  const { budgetLevel } = await import('./cockpit.js');
  assert.equal(budgetLevel(50, 100).key, 'ok');
  assert.equal(budgetLevel(80, 100).key, 'mid');
  assert.equal(budgetLevel(99, 100).key, 'mid');
  assert.equal(budgetLevel(100, 100).key, 'low');
  assert.equal(budgetLevel(250, 100).key, 'low');
  assert.equal(budgetLevel(999, 0).key, 'off');
});

async function loadPanel(payloads) {
  dom();
  fetchStub(payloads);
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel();
  const root = new El('div');
  p.root = root;
  p.paintShell();
  await p.refresh();
  return { p, root };
}

// 四象限内容的断言视图（testdom 的 El 不是真树——象限面板存于
// lastPaint；壳与象限拼起来就是整屏）
function fullHTML(p, root) {
  return root.innerHTML + Object.values(p.lastPaint).join('');
}

test('cockpit：四象限满载渲染（人在哪/活在干/池有多深/今天值得看）', async () => {
  const { p, root } = await loadPanel(fullPayloads());
  const html = fullHTML(p, root);
  // 壳与纪律文案（评审补充④）
  assert.ok(html.includes('只读总览 · 管理请走各入口'), '头部纪律文案在');
  // ① 人在哪：四态计数＋访客计数
  assert.ok(html.includes('人在哪'), '象限①标题在');
  assert.ok(html.includes('干活中'), '干活中计数在');
  assert.ok(html.includes('在项目房'), '项目房计数在');
  assert.ok(html.includes('👁 3'), '访客计数在');
  // ② 活在干：doing 卡＋今日 done 计数
  assert.ok(html.includes('活在干'), '象限②标题在');
  assert.ok(html.includes('驾驶舱面板'), 'doing 任务标题在');
  assert.ok(html.includes('t_1'), '任务号在');
  assert.ok(html.includes('今日已闭环'), '今日 done 计数在');
  // ③ 池有多深：房间行＋水位色签＋饱和度＋巡报采集时刻
  assert.ok(html.includes('池有多深'), '象限③标题在');
  assert.ok(html.includes('Niuma_Studio'), 'Niuma_Studio 行在');
  assert.ok(html.includes('4 条 · 健康'), 'open=4/water=3 → 健康绿签');
  assert.ok(html.includes('1 条 · 见底'), 'open=1/water=3 → 见底红签');
  assert.ok(html.includes('饱和 50%'), '饱和度在');
  assert.ok(html.includes('采集于'), '巡报采集时刻在（评审补充②）');
  // ④ 今天值得看：编年史今日条目（旧日期不出）＋成就三枚
  assert.ok(html.includes('今天值得看'), '象限④标题在');
  assert.ok(html.includes('驾驶舱面板完工'), '今日编年史条目在');
  assert.ok(!html.includes('昨日旧条目'), '昨日条目不出（今日口径）');
  assert.ok(html.includes('首单交付'), '成就奖章在');
  // ⑤ 耗粮多少（r_26 成本象限）：通栏、双窗人话数字＋百分比＋色签、
  // 熔断未触发不出警示
  assert.ok(html.includes('耗粮多少'), '象限⑤标题在');
  assert.ok(html.includes('120.0 万 / 500.0 万 · 预算内'), '今日已耗/上限行在');
  assert.ok(html.includes('24%'), '今日百分比在');
  assert.ok(html.includes('2100.0 万 / 3000.0 万'), '本周行在');
  assert.ok(!html.includes('预算熔断'), '额内不出熔断警示');
  resetDom();
});

test('cockpit：今日 done 计数只算当日（跨夜 ts 排除）', async () => {
  const payloads = fullPayloads();
  // done 单 updated_ts=200（1970 纪元远端）≠ 今日——计数应为 0
  const { p, root } = await loadPanel(payloads);
  const m = /今日已闭环 <strong>(\d+)<\/strong> 单/.exec(fullHTML(p, root));
  assert.ok(m, '今日计数行在');
  assert.equal(m[1], '0', '跨夜 done 不计入今日');
  resetDom();
});

test('cockpit：空数据降级（五象限各自空态不白屏不拖邻区）', async () => {
  const { p, root } = await loadPanel([
    [],   // people 空
    [],   // tasks 空
    { rooms: [], now: 1 }, // capacity 空房间集
    { body: '' },          // chronicle 空
    { unlocked: {}, achievements: [] }, // achieve 空
    { on: false, count: 0 }, // visitor 关
    { day_tokens: 0, week_tokens: 0, day_used: 0, week_used: 0,
      tripped_day: false, tripped_week: false, breaker_live: true }, // budget 未设
  ]);
  const html = fullHTML(p, root);
  assert.ok(html.includes('当前没有进行中的任务'), '② 空任务降级');
  assert.ok(html.includes('暂无数据'), '③ 空水位降级');
  assert.ok(html.includes('今天还没有编年史条目'), '④ 空编年史降级');
  assert.ok(html.includes('还没有解锁的奖章'), '④ 空成就降级');
  assert.ok(html.includes('未设预算'), '⑤ 未设预算降级');
  resetDom();
});

test('cockpit：单接口失败只挂一区（邻区照渲染）', async () => {
  const payloads = fullPayloads();
  payloads[0] = { ok: false, status: 500, json: { error: 'x' } }; // people 挂
  const { p, root } = await loadPanel(payloads);
  const html = fullHTML(p, root);
  assert.ok(html.includes('名册读取失败'), '① 错误态');
  assert.ok(html.includes('今日已闭环'), '② 照常渲染——一区失败不拖邻区');
  resetDom();
});

test('cockpit：访客档子集——水位/成就/访客数在，四态与任务流水整段不出', async () => {
  dom();
  globalThis.__NIUMA_VISITOR__ = true; // visitorMode() 的判位标记
  fetchStub(fullPayloads());
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel();
  const root = new El('div');
  p.root = root;
  p.paintShell();
  await p.refresh();
  const html = fullHTML(p, root);
  // 出：水位数字＋色签、成就、访客计数、今日交付聚合数
  assert.ok(html.includes('4 条 · 健康'), '访客档：水位数字在');
  assert.ok(html.includes('首单交付'), '访客档：公开荣誉在');
  assert.ok(html.includes('👁 3'), '访客档：访客计数在');
  assert.ok(html.includes('今日已交付'), '访客档：匿名聚合交付数在');
  // 不出：working-detail 类名整段（敏感面断言族——DOM 断言访客帧无）
  assert.ok(!html.includes('working-detail'), '访客帧无 working 明细类名');
  assert.ok(!html.includes('干活中'), '访客帧无四态明细');
  assert.ok(!html.includes('驾驶舱面板'), '访客帧无任务流水（在做内容不公开）');
  assert.ok(!html.includes('驾驶舱面板完工'), '访客帧无编年史全文');
  // ⑤ 成本面（r_26）：占位在、耗粮数字整段不出（成本是钱面，出门即账本）
  assert.ok(html.includes('成本面仅房主可见'), '访客档：成本面占位在');
  assert.ok(!html.includes('120.0 万'), '访客帧无今日耗粮数字');
  assert.ok(!html.includes('预算内'), '访客帧无预算色签');
  resetDom();
  delete globalThis.__NIUMA_VISITOR__;
});

// ⑤ 成本象限的熔断态（r_26）：到额行红签「已到额」＋警示行「只关推进」
test('cockpit：成本象限熔断态——到额红签＋只关推进警示', async () => {
  const payloads = fullPayloads();
  payloads[6] = { day_tokens: 1_000_000, week_tokens: 30_000_000,
    day_used: 1_200_000, week_used: 21_000_000,
    day_start: 100, week_start: 90,
    tripped_day: true, tripped_week: false,
    breaker_live: true, usage_error: false };
  const { p, root } = await loadPanel(payloads);
  const html = fullHTML(p, root);
  assert.ok(html.includes('120.0 万 / 100.0 万 · 已到额'), '到额红签在');
  assert.ok(html.includes('预算熔断：自动推进已关'), '熔断警示行在');
  assert.ok(html.includes('补货不受影响'), '警示言明只关推进');
  resetDom();
});

// ⑤ 台账读挂不误报熔断：usage_error 时 tripped 位是服务端「旋钮已设」
// 的种子值（budget.go），当真渲染就是撒谎——只出降级行，熔断横幅不出。
test('cockpit：台账故障不误报熔断——usage_error 抑制横幅', async () => {
  const payloads = fullPayloads();
  payloads[6] = { day_tokens: 1_000_000, week_tokens: 30_000_000,
    day_used: 0, week_used: 0,
    day_start: 100, week_start: 90,
    tripped_day: true, tripped_week: true,
    breaker_live: true, usage_error: true };
  const { p, root } = await loadPanel(payloads);
  const html = fullHTML(p, root);
  assert.ok(html.includes('耗粮台账暂不可读'), '降级行在');
  assert.ok(!html.includes('预算熔断'), '熔断横幅不出（种子值不可信）');
  resetDom();
});

test('cockpit：paint 门槛——相同 HTML 不再写 innerHTML', async () => {
  const { p } = await loadPanel(fullPayloads());
  const el = p.els.p1;
  const before = el.innerHTML;
  const wrote = { n: 0 };
  const orig = Object.getOwnPropertyDescriptor(El.prototype, 'innerHTML');
  Object.defineProperty(el, 'innerHTML', {
    set(v) { wrote.n++; orig.set.call(this, v); },
    get() { return orig.get.call(this); },
    configurable: true,
  });
  p.paint('p1', before);       // 同串：跳过
  p.paint('p1', before + ' '); // 异串：写入
  assert.equal(wrote.n, 1, '同串零写、异串一写');
  resetDom();
});

// 轮询契约（验收③）：20s 拍／首拉不等拍（load 即 refresh）／stop 即清。
// 假钟：setInterval/clearInterval 捕进桩里，不真等 20 秒。
test('cockpit：轮询生命周期——load 首拉立即、20s 拍、stop 即清、hidden 跳拍', async () => {
  dom();
  const timers = { set: [], cleared: [], hidden: false };
  const realSet = globalThis.setInterval;
  const realClear = globalThis.clearInterval;
  globalThis.setInterval = (fn, ms) => { timers.set.push(ms); return 100 + timers.set.length; };
  globalThis.clearInterval = (id) => { timers.cleared.push(id); };
  Object.defineProperty(globalThis.document, 'hidden', {
    get: () => timers.hidden, configurable: true,
  });
  fetchStub(fullPayloads());
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel();
  const root = new El('div');
  p.root = root;
  let refreshes = 0;
  const realRefresh = p.refresh.bind(p);
  p.refresh = async () => { refreshes++; await realRefresh(); };

  p.load(root);
  assert.equal(refreshes, 1, 'load 首拉立即（不等首拍）');
  assert.deepEqual(timers.set, [20_000], '20s 拍（与 META_EVERY 同族）');

  p.tick();
  assert.equal(refreshes, 2, '可见时 tick 刷新');
  timers.hidden = true;
  p.tick();
  assert.equal(refreshes, 2, 'document.hidden 跳拍');

  p.stop();
  assert.deepEqual(timers.cleared, [101], 'stop 即 clearInterval（不看就不烧）');
  assert.equal(p.timer, 0, '定时器归零');
  globalThis.setInterval = realSet;
  globalThis.clearInterval = realClear;
  resetDom();
});

// 切走页签再回来：pane 的 innerHTML 被别的面板整面重写，缓存的象限
// 元素脱树（真 DOM isConnected=false）——壳必须重建重取引用，否则数据
// 照拉照写全落进脱树旧节点，面板停在上一页签内容（「点了没反应」）。
// testdom 假节点无 isConnected 位＝活树口径：照旧靠缓存判不重建。
test('cockpit：切走页签重进——脱树旧壳重建、活壳不重建', async () => {
  const { p, root } = await loadPanel(fullPayloads());
  const live = p.els;
  await p.refresh();
  assert.equal(p.els, live, '活壳（未脱树）不重建——paint 门槛缓存判不破');
  for (const k of ['p1', 'p2', 'p3', 'p4', 'p5']) live[k].isConnected = false;
  fetchStub(fullPayloads());
  await p.refresh();
  assert.notEqual(p.els, live, '脱树旧壳弃用——壳重建、象限引用重取');
  assert.ok(root.innerHTML.includes('只读总览 · 管理请走各入口'), '壳重写进 pane');
  assert.ok(Object.values(p.lastPaint).join('').includes('人在哪'), '重建后象限照常渲染');
  resetDom();
});

// ⑨ t_206 验收偏差裁定移交：重试钮接线——errHtml 带委托锚点、bindRetry
// 点击触发 refresh（不再死按钮）。
test('cockpit：错误象限重试钮接线（t_206 移交一行）', async () => {
  const { p } = await loadPanel(fullPayloads());
  const html = p.errHtml('名册读取失败');
  assert.ok(html.includes('data-ck-retry'), '重试钮带委托锚点');
  let fired = 0;
  const root = new El('div');
  const calls = [];
  root.addEventListener = (type, fn) => calls.push({ type, fn });
  p.bindRetry(root);
  assert.equal(calls.length, 1, '一次委托挂载');
  assert.equal(calls[0].type, 'click', 'click 委托');
  p.bindRetry(root); // 重复进出 load()——委托不叠加（retryBound 一次挂）
  assert.equal(calls.length, 1, '重复 bind 不叠加监听');
  // 模拟点击命中重试锚点：refresh 被调（假桩）
  p.refresh = async () => { fired++; };
  const ev = { target: { closest: (sel) => (sel === '[data-ck-retry]' ? {} : null) } };
  await calls[0].fn(ev);
  assert.equal(fired, 1, '点重试触发 refresh');
  resetDom();
});

// ⑩ 志按办公室分本后的④聚合：chronSources 喂大厅＋项目各一本——今日
// 条目跨本聚合、项目条目带房名标注；项目志未建档（404）读作空不当错
// 误，大厅那本挂了才算编年史错误（fetch 序＝people/tasks/cap/大厅志/
// 项目志/achieve/visitor/budget）。
test('cockpit：④聚合各房志的今日条目（房名标注、项目缺席=空）', async () => {
  const payloads = fullPayloads();
  payloads.splice(4, 0, { body: `${todayStr()}｜交付｜小鹿｜小说第一章完稿` });
  fetchStub(payloads);
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel(() => [
    { key: 'ops/chronicle', room: '' },
    { key: 'p/book/chronicle', room: '小说写作' },
  ]);
  const root = new El('div');
  p.root = root;
  p.paintShell();
  await p.refresh();
  const html = fullHTML(p, root);
  assert.ok(html.includes('驾驶舱面板完工'), 'Niuma_Studio 本今日条目在');
  assert.ok(html.includes('第一章完稿'), '项目本今日条目聚合进来');
  assert.ok(html.includes('小说写作 ·'), '项目条目带房名标注');
  assert.ok(!html.includes('编年史读取失败'), '聚合成功不出错误态');
  resetDom();
});

test('cockpit：④项目志 404 读作空；Niuma_Studio 志挂了才报错', async () => {
  // 项目志 404（该房还没有事件＝未建档）：空处理，大厅条目照渲染
  {
    const payloads = fullPayloads();
    payloads.splice(4, 0, { ok: false, status: 404, json: { error: 'not found' } });
    fetchStub(payloads);
    const { CockpitPanel } = await import('./cockpit.js');
    const p = new CockpitPanel(() => [
      { key: 'ops/chronicle', room: '' },
      { key: 'p/book/chronicle', room: '小说写作' },
    ]);
    const root = new El('div');
    p.root = root;
    p.paintShell();
    await p.refresh();
    const html = fullHTML(p, root);
    assert.ok(html.includes('驾驶舱面板完工'), 'Niuma_Studio 条目照常');
    assert.ok(!html.includes('编年史读取失败'), '项目志 404 不当错误');
    resetDom();
  }
  // 大厅志挂了：④报编年史错误（全室志源失联是真故障）
  {
    dom();
    const payloads = fullPayloads();
    payloads[3] = { ok: false, status: 500, json: { error: 'x' } };
    fetchStub(payloads);
    const { CockpitPanel } = await import('./cockpit.js');
    const p = new CockpitPanel(() => [{ key: 'ops/chronicle', room: '' }]);
    const root = new El('div');
    p.root = root;
    p.paintShell();
    await p.refresh();
    assert.ok(fullHTML(p, root).includes('编年史读取失败'), 'Niuma_Studio 志失联报错');
    resetDom();
  }
});

// ── 页签切换竞态（kb.dom.test.js 同族的驾驶舱面板单测面）────────────

test('cockpit：切走即弃——stop() 作废在途 refresh（迟到回包不落漆）', async () => {
  dom();
  const resolvers = [];
  globalThis.fetch = () => new Promise((r) => resolvers.push(r)); // 全部挂在途
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel();
  const root = new El('div');
  p.root = root;
  const done = p.refresh();
  assert.ok(root.innerHTML.includes('cockpit-grid'), '壳先立（同步）');
  p.stop(); // 切走页签：token 作废
  resolvers.splice(0).forEach((r) => r({
    ok: true, status: 200,
    headers: { get: (k) => (String(k).toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => ({}),
  }));
  await done;
  assert.equal(Object.keys(p.lastPaint).length, 0, '迟到回包一个象限都不画');
  assert.ok(root.innerHTML.includes('cockpit-grid'), '壳不被动');
  resetDom();
});

test('cockpit：重进即愈——壳被别家页签重写后 load() 立即重建（不空等下一拍）', async () => {
  dom();
  fetchStub(fullPayloads());
  const { CockpitPanel } = await import('./cockpit.js');
  const p = new CockpitPanel();
  const root = new El('div');
  p.load(root); // 起拍（真定时器——测试尾必停）
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(root.innerHTML.includes('cockpit-grid'), '首拍壳立');
  // 别家页签把 pane 整面重写：象限元素全数脱树（真 DOM 语义，测试直设该位）
  p.els.p1.isConnected = false;
  root.innerHTML = '<div class="foreign">别家页签的内容</div>';
  p.load(root); // 重进驾驶舱（定时器已在跑——旧码此处直接返回，干等 20s）
  await new Promise((r) => setTimeout(r, 0));
  assert.ok(root.innerHTML.includes('cockpit-grid'), '重进立即重建壳');
  assert.ok(!root.innerHTML.includes('foreign'), '别家内容被盖回');
  assert.ok((p.lastPaint.p1 || '').includes('人在哪'), '象限照常落漆');
  p.stop();
  resetDom();
});

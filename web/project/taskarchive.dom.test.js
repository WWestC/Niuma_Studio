// taskarchive.dom.test.js — t_212（r_26）：任务档案浮层与履历渲染的 DOM
// 冒烟。foldLog/foldLogHTML 纯逻辑＋TaskArchive 浮层四态＋履历三件套
// （喂 getPersonHistory 载荷）＋非考核断言（无 sort/compare 类名）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

// 构造一条 log：同秒即同日——days 分组按日期段，三条同日两条备注折一行
const LOG = [
  { ts: 1790000000, by: '小牛', note: '创建任务，指派给 小猿' },   // 骨架
  { ts: 1790000060, by: '小猿', note: '状态 待处理→进行中' },        // 骨架
  { ts: 1790000120, by: '小猿', note: '第一段备注补充细节。' },      // 备注
  { ts: 1790000180, by: '小猿', note: '第二段备注。' },              // 备注（同折）
  { ts: 1790086400, by: '小猿', note: '状态 进行中→已完成' },        // 骨架（次日）
  { ts: 1790086460, by: '小猿', note: '结单补充说明。' },            // 备注（次日新折）
];

// ① 三层折叠：骨架平铺、同日备注合并成一行、跨日切分隔线。
test('foldLog：骨架平铺＋同日合并＋跨日分组', async () => {
  const { foldLog } = await import('../project/taskarchive.js');
  const days = foldLog(LOG);
  assert.equal(days.length, 2, '两天分组');
  const d1 = days[0];
  assert.equal(d1.rows.length, 3, '首日三行：骨架/骨架/折行');
  assert.equal(d1.rows[0].type, 'row');
  assert.equal(d1.rows[2].type, 'fold');
  assert.equal(d1.rows[2].entries.length, 2, '两条同日备注并一折');
  const d2 = days[1];
  assert.equal(d2.rows.length, 2, '次日：骨架＋新折');
});

// ② 骨架判定：状态流转关键词行永远平铺（isSkeleton）。
test('isSkeleton：创建/指派/状态流转行是骨架', async () => {
  const { isSkeleton } = await import('../project/taskarchive.js');
  assert.ok(isSkeleton('创建任务，指派给 小猿'));
  assert.ok(isSkeleton('状态 待处理→进行中'));
  assert.ok(!isSkeleton('普通备注不是骨架'));
  assert.ok(!isSkeleton(''));
});

// ③ foldLogHTML：默认全收（折行不展开）、展开态显全量、空 log 降级。
test('foldLogHTML：收起/展开/空降级三态', async () => {
  dom(); // 立测试世界——zh 钉子（navigator zh-CN）住在 dom() 里，t() 才落中文
  const { foldLogHTML } = await import('../project/taskarchive.js');
  const collapsed = foldLogHTML(LOG);
  assert.ok(collapsed.includes('arch-fold'), '折行按钮在');
  assert.ok(collapsed.includes('2 条备注'), '折行带条数');
  assert.ok(!collapsed.includes('第一段备注'), '收起态不显备注内容');
  assert.ok(collapsed.includes('创建任务'), '骨架平铺');
  // 展开：任一折键
  const m = /data-fold="([^"]+)"/.exec(collapsed);
  assert.ok(m, '折键在');
  const expanded = foldLogHTML(LOG, new Set([m[1]]));
  assert.ok(expanded.includes('第一段备注'), '展开显全量');
  // 空 log
  assert.ok(foldLogHTML([]).includes('（暂无流转记录）'), '空降级文案');
});

// ④ 浮层渲染：十字段＋折叠 log（与 CLI task show 同源对照——同一载荷
// 字段全渲染）。
test('TaskArchive：浮层渲染十字段＋log（CLI 同源对照）', async () => {
  dom();
  fetchStub([{ id: 't_186', title: '成就清单定稿', assignee: '小鹿', status: 'done',
    req: 'r_18', plan_id: 'p_20', created_by: '小牛', progress: '100%',
    progress_pct: 100, log: LOG, start_ts: 1790000000, end_ts: 1790086400 }]);
  const { TaskArchive } = await import('../project/taskarchive.js');
  const a = new TaskArchive();
  await a.open('t_186');
  const html = a.sheet ? a.sheet.innerHTML : '';
  for (const want of ['成就清单定稿', 't_186', '小鹿', 'r_18', 'p_20',
    '小牛', '100%', '创建任务', '2 条备注']) {
    assert.ok(html.includes(want), `浮层应含 ${want}`);
  }
  a.close();
  assert.equal(a.el, null, 'close 清场');
  resetDom();
});

// ⑤ 浮层错误降级：getTask 失败给重试不白屏。
test('TaskArchive：读取失败降级重试钮', async () => {
  dom();
  fetchStub([{ ok: false, status: 500, json: { error: 'x' } }]);
  const { TaskArchive } = await import('../project/taskarchive.js');
  const a = new TaskArchive();
  await a.open('t_404');
  assert.ok(a.sheet.innerHTML.includes('档案读取失败'), '错误态在');
  assert.ok(a.sheet.innerHTML.includes('重试'), '重试钮在');
  a.close();
  resetDom();
});

// ⑥ 履历三件套：入职锚/徽带色/分组列表（喂 history 载荷渲染节 HTML——
// 纯渲染面从 DetailView 抽出来不现实，这里以同形函数钉契约：构造同款
// 载荷断言关键结构，等价于 detail.js 的 #historyHTML 输出形状）。
test('履历三件套：锚/徽带/分组计数（非考核断言）', async () => {
  dom();
  fetchStub([{
    name: '小猿', joined: 1790000000,
    lines: [
      { req: 'r_22', title: '触屏', color: '#3370ff',
        done: [{ id: 't_201', title: '触屏引擎', ts: 1790086400 },
               { id: 't_205', title: '驾驶舱面板', ts: 1790172800 }] },
      { req: 'r_08', title: '测试基建', color: '#00a9ff',
        done: [{ id: 't_184', title: 'DOM 冒烟', ts: 1790080000 }] },
    ],
  }]);
  const { getPersonHistory } = await import('../wire/api.js');
  const h = await getPersonHistory('小猿');
  assert.equal(h.lines.length, 2, '两条需求线');
  assert.equal(h.lines[0].done.length, 2, 'r_22 组 2 张');
  assert.ok(h.joined > 0, '入职锚在');
  assert.match(h.lines[0].color, /^#[0-9a-f]{6}$/, '徽带色 hex 稳定形');
  // 非考核断言（定稿 §三·4）：载荷无排序键/对比度量字段
  const flat = JSON.stringify(h);
  for (const banned of ['"speed"', '"rate"', '"rank_by"', '"compare"', '"score"']) {
    assert.ok(!flat.includes(banned), `履历载荷不该有度量字段 ${banned}`);
  }
  resetDom();
});

// ⑦ 甘特入口契约：selbar 查档案钮（paintSelbar 的 HTML 含 data-archive）。
test('甘特 selbar：查档案入口在（两处入口同浮层）', async () => {
  const src = await import('node:fs').then((fs) =>
    fs.readFileSync(new URL('../gantt/gantt.js', import.meta.url), 'utf8'));
  assert.ok(src.includes("data-archive>${esc(t('查档案'))}"), 'selbar 查档案钮在');
  assert.ok(src.includes('openArchive(this.selId)'), '点击走共享浮层');
});

// ⑧ 访客边界（r_26 定稿 §五·小牛注记风险②）：/view 帧不挂档案入口——
// visitor.js 的裁剪清单含 #nav-people/#nav-project（履历节与甘特/任务卡
// 的宿主板块访客不可达），且驾驶舱访客档无 working-detail（档案入口的
// 近亲面）。静态断言裁剪清单持续覆盖。
test('访客边界：档案与履历的宿主入口在访客裁剪清单里', async () => {
  const fs = await import('node:fs');
  const v = fs.readFileSync(new URL('../ui/visitor.js', import.meta.url), 'utf8');
  assert.ok(v.includes('#nav-people'), '人物页（履历节宿主）访客藏');
  assert.ok(v.includes('#nav-project'), '项目/甘特（档案入口宿主）访客藏');
  // 档案浮层自身不被访客初始化路径引用
  const app = fs.readFileSync(new URL('../app.js', import.meta.url), 'utf8');
  if (app.includes('taskarchive')) {
    assert.ok(!/visitorMode\(\)[\s\S]{0,200}taskarchive/.test(app),
      '访客初始化路径不该挂档案浮层');
  }
});

// minutes.dom.test.js — t_209（r_25）：评审纪要的前端钉。黑板独立页签
// 已撤（纪要即文档——与文档页签重复）：key 契约（反解依据）、文档树
// 归并（ops/meetings 文件夹即列表）、需求行折叠时间线、访客态隔离。
// 数据面契约（meeting/minutes.go）：key=ops/meetings/<需求号小写>-<序号>、
// 标题「内部 · 评审纪要 …」、正文 markdown 三节结构（开场/发言纪要/结论）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { docTree } from './kb.js';

test('纪要 key 契约：ops/meetings/ 前缀＋需求号小写＋序号（渲染过滤的依据）', () => {
  // 前端正则从 key 反解需求号：r25-1 → r_25（需求行折叠时间线反解用）
  const re = /^ops\/meetings\/(.+?)-\d+$/;
  assert.deepEqual(re.exec('ops/meetings/r25-1')?.slice(1), ['r25']);
  assert.deepEqual(re.exec('ops/meetings/r24-2')?.slice(1), ['r24']);
  assert.equal(re.exec('ops/chronicle'), null, '非纪要 key 不匹配');
  assert.deepEqual(re.exec('ops/meetings/free-1')?.slice(1), ['free'], 'free 形（无需求号的会）也匹配');
});

test('纪要归并文档页签：docTree 落进 ops/meetings 文件夹（独立页签已撤）', () => {
  const tree = docTree([
    { key: 'ops/meetings/r25-1', title: '内部 · 评审纪要 r_25 会议转录归档', updated_ts: 1790937000, rev: 1 },
    { key: 'design/r25-final', title: '定稿', updated_ts: 1790936000, rev: 1 },
  ]);
  const meetings = tree.dirs.get('ops')?.dirs.get('meetings');
  assert.ok(meetings, 'ops/meetings 文件夹在（文档页签文件树即纪要列表）');
  assert.equal(meetings.docs.length, 1, '前缀归位只收纪要');
  assert.ok(meetings.docs[0].title.includes('内部'), '标题带内部标记');
  assert.equal(tree.dirs.get('design')?.docs.length, 1, '其余文档不受影响');
  // 无纪要的工作室：文档树照常，不额外渲染空区块
  assert.equal(docTree([{ key: 'readme' }]).dirs.size, 0);
});

test('需求行折叠时间线：minutesByReq 缓存渲染（chron-item 结构）', () => {
  // #minutesHTML 的纯渲染面（不 new ReqsView——它拉台账；直测同等逻辑）：
  // 条目即按钮（与详情卡同源 minuteItemsHTML），点条目直接跳黑板文档面
  const by = new Map([['r_25', [{ key: 'ops/meetings/r25-1', title: '内部 · 评审纪要 r_25 会议转录（10-02）' }]]]);
  const list = by.get('r_25') || [];
  const html = `<details class="req-minutes"><summary>评审记录（${list.length} 场）</summary>` +
    `<div class="chron-list">${list.map((d) =>
      `<div class="chron-item"><span class="chron-tp chron-tp-event">评审</span>` +
      `<button type="button" class="req-min-item" data-open-doc="${d.key}" title="在黑板里打开这份纪要">${d.title}</button></div>`).join('')}</div></details>`;
  assert.ok(html.includes('req-minutes'), '折叠块在');
  assert.ok(html.includes('评审记录（1 场）'), '计数在');
  assert.ok(html.includes('内部 · 评审纪要'), '标题渲染');
  assert.ok(html.includes('data-open-doc="ops/meetings/r25-1"'), '条目即按钮：点条目直接跳黑板');
  // 无纪要的需求：零渲染（不渲染空折叠块）
  assert.equal((by.get('r_99') || []).length, 0);
});

test('访客态隔离：黑板板块在访客裁剪范围外（纪要不需单独藏）', async () => {
  // 定稿小马④：/view 不出纪要。访客态裁剪整侧栏（t_192 的 body.visitor
  // #side visibility:hidden），黑板板块 nav-kb 在侧栏导航里——天然不可
  // 达。断言：访客裁剪样式不豁免 nav-kb（不在裁剪名单≠可见——侧栏整体
  // 收纳盖住）。
  const fs = await import('node:fs');
  const vjs = fs.readFileSync(new URL('../ui/visitor.js', import.meta.url), 'utf8');
  assert.ok(vjs.includes('body.visitor #side { visibility: hidden; }'),
    '访客态侧栏整体收纳在 visitor.js 注入样式里（黑板导航在内的内部面全不可达）');
});

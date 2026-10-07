// reqs.dom.test.js — 需求详情浮层（点行看全文）的 DOM 冒烟：行带
// data-req-open、点行弹只读卡（编号/状态/标题/正文全文/元信息）、
// 无正文明示、纪要条目可点跳黑板（详情卡与行内折叠区都是直跳）、
// 按钮与解除武装那一击不弹卡、关闭清账。testdom 第一层
// （files.dom.test.js 的 click 桩同款——closest 按选择器回桩，
// onClick 公开面直饮）。
//
//   node --test web/project/reqs.dom.test.js

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, fetchStub, resetDom, El } from '../room/testdom.js';

const LEDGER = {
  reqs: [
    { id: 'r_01', title: '聊天导出', body: '第一行\n第二行才是验收口径', status: 'open',
      created_by: '房主', created_ts: 1700000000 },
    { id: 'r_02', title: '无正文需求', body: '', status: 'closed',
      created_by: '房主', created_ts: 1700000000, closed_ts: 1700003600 },
  ],
};
const MINUTES_DOC = { key: 'ops/meetings/r_01-3', title: '10-14 评审' };

async function boot(docs = []) {
  dom();
  // zh 钉子（reqs.test.js 同款）：断言的中文文案全线走 t()，先钉 zh
  globalThis.localStorage.setItem('dh.ui.prefs', JSON.stringify({ lang: 'zh' }));
  const { ReqsView } = await import('./reqs.js');
  // reload 的取数序：纪要索引（/kb/docs）先行，台账（/p/{key}/reqs）随后
  fetchStub([docs, LEDGER]);
  const board = {
    key: 'default',
    roomName: () => '大厅',
    book: { rooms: new Map() },
    toast: { show() {} },
    jumpToGit() {},
  };
  const v = new ReqsView(new El('div'), board);
  await v.reload();
  return v;
}

// 点击桩：target.closest 按选择器回桩（真 El 的 closest 恒 null）
function clickAt(hits) {
  return { target: { closest: (sel) => hits[sel] || null } };
}
const rowHit = (id) => ({ '[data-req-open]': { dataset: { reqOpen: id } } });

// ① 行带点击目标：id 与「点击看全文」提示都画在行上
test('需求行带 data-req-open 与看全文提示', async () => {
  const v = await boot();
  assert.ok(v.container.innerHTML.includes('data-req-open="r_01"'), '行带点击目标');
  assert.ok(v.container.innerHTML.includes('点击看全文'), '悬停提示在');
  resetDom();
});

// ② 点行弹只读卡：正文全文（列表里被两行截断的正是它）＋状态＋录入人
test('点行弹详情卡：全文＋元信息', async () => {
  const v = await boot();
  v.onClick(clickAt(rowHit('r_01')));
  assert.ok(v.detail, '详情卡已开');
  const card = v.detail.el.innerHTML;
  assert.ok(card.includes('r_01'), '编号在');
  assert.ok(card.includes('聊天导出'), '标题在');
  assert.ok(card.includes('第一行'), '正文第一行在');
  assert.ok(card.includes('第二行才是验收口径'), '正文第二行在——不被 line-clamp 截走');
  assert.ok(card.includes('待拆解'), '状态签在');
  assert.ok(card.includes('录入 房主'), '录入人在');
  assert.ok(card.includes('data-f-cancel'), '关闭钮在');
  resetDom();
});

// ③ 无正文需求明示「（无正文）」；closed 行带完成时刻与状态签
test('无正文明示＋完成时刻', async () => {
  const v = await boot();
  v.onClick(clickAt(rowHit('r_02')));
  const card = v.detail.el.innerHTML;
  assert.ok(card.includes('（无正文）'), '空正文明示');
  assert.ok(card.includes('已关闭'), '状态签在');
  assert.ok(card.includes('完成于'), '完成时刻在');
  resetDom();
});

// ④ 纪要进卡且可点：条目即按钮，带 data-open-doc（dh:open-doc 桥）；
//    MinutesKey 现行键形（r_01-3）与老键形（r01-2）都归到 r_01 名下
test('详情卡纪要条目可点跳黑板', async () => {
  const v = await boot([
    { key: 'ops/meetings/r_01-3', title: '10-14 评审' },
    { key: 'p/alpha/meetings/r01-2', title: '10-13 评审' },
  ]);
  v.onClick(clickAt(rowHit('r_01')));
  assert.ok(v.detail, '详情卡已开');
  assert.ok(v.detail.el.innerHTML.includes('评审记录（2 场）'), '两种键形都归队，计数 2');
  assert.ok(v.detail.el.innerHTML.includes('data-open-doc="ops/meetings/r_01-3"'), '现行键形条目在');
  assert.ok(v.detail.el.innerHTML.includes('data-open-doc="p/alpha/meetings/r01-2"'), '老键形条目在');
  resetDom();
});

// ④b 行内折叠区纪要条目同款直跳：条目即按钮（data-open-doc），点击发
//     dh:open-doc 桥事件跳黑板——不弹详情卡中转；路由接手须在
//     .req-minutes 消音早退之前（折叠区里的点击不是开合余量）
test('行内纪要条目点击直接跳黑板', async () => {
  const v = await boot([{ key: 'ops/meetings/r_01-3', title: '10-14 评审' }]);
  assert.ok(v.container.innerHTML.includes('data-open-doc="ops/meetings/r_01-3"'), '行内条目即按钮');
  const fired = [];
  globalThis.window.dispatchEvent = (ev) => fired.push(ev); // testdom 的 window 无派发面：钉上观测
  v.onClick(clickAt({ '[data-open-doc]': { dataset: { openDoc: 'ops/meetings/r_01-3' } } }));
  assert.equal(fired.length, 1, '桥事件已发');
  assert.equal(fired[0].type, 'dh:open-doc');
  assert.equal(fired[0].detail, 'ops/meetings/r_01-3', '携文档 key');
  assert.equal(v.detail, null, '不弹详情卡——直接跳，不中转');
  resetDom();
});

// ⑤ 按钮各自接手不弹卡；武装行上的那一击只解除武装、不开卡
test('按钮与解除武装那一击不弹详情卡', async () => {
  const v = await boot();
  v.onClick(clickAt({ '[data-req-git]': { dataset: { reqGit: 'r_01' } } }));
  assert.equal(v.detail, null, '分支动向钮不弹卡');
  v.delArmed = 'r_01';
  v.onClick(clickAt(rowHit('r_01')));
  assert.equal(v.delArmed, null, '武装已解除');
  assert.equal(v.detail, null, '解除那一击不弹卡');
  resetDom();
});

// ⑥ 开新卡先收旧卡：连点两行只留一张（槽位单一）
test('连点两行旧卡先收', async () => {
  const v = await boot();
  v.onClick(clickAt(rowHit('r_01')));
  const first = v.detail.el;
  v.onClick(clickAt(rowHit('r_02')));
  assert.ok(v.detail, '新卡在');
  assert.notEqual(v.detail.el, first, '旧卡已换新');
  resetDom();
});

// ⑦ 关闭清账：close() 后 onClose 把 detail 槽清空（浮层摘除走 dismiss）
test('详情卡关闭清账', async () => {
  const v = await boot();
  v.onClick(clickAt(rowHit('r_01')));
  assert.ok(v.detail, '先开');
  v.detail.close();
  assert.equal(v.detail, null, '关闭后槽位清空');
  resetDom();
});

// ⑧ 接线静态断言：详情卡样式在 app.css（自 index.html 抽出）、行点击目标在 reqs.js
test('接线：req-detail 样式与 data-req-open 在位', async () => {
  const fs = await import('node:fs');
  const css = fs.readFileSync(new URL('../app.css', import.meta.url), 'utf8');
  for (const sel of ['.req-detail-title', '.req-detail-body', '.req-min-item', '.req-detail-head']) {
    assert.ok(css.includes(sel), `CSS ${sel} 在`);
  }
  const src = fs.readFileSync(new URL('./reqs.js', import.meta.url), 'utf8');
  assert.ok(src.includes('data-req-open'), '行点击目标在');
  assert.ok(src.includes('dh:open-doc'), '纪要跳黑板桥在');
});

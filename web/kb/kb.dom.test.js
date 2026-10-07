// kb.dom.test.js — 黑板板块页签切换的竞态冒烟。快速点页签时，离场面
// 板的在途请求若不被作废，慢回包的 paint 会把新页签刚画好的内容整面
// 重写回旧页签（驾驶舱壳被志盖掉后，再点驾驶舱又因 load 幂等守卫不
// 重画——「卡住了点不了」的完整链条）。switchTab 现场作废离场面板
//（token 自增，迟到回包对不上号即弃），这里钉住两条方向。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

// 今日日期串（本地自然日）——志条目夹具用运行时的今天，永不跌出「今日」口径
function todayStr() {
  const d = new Date(), p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

const tick = () => new Promise((r) => setTimeout(r, 0));

// 按 URL 路由的 fetch 假体：说明书走 resp.text()（getText 通道），其余
// 走 resp.json()（getJSON 通道）——fetchStub 只备 json 面，这里双通道齐
function routeFetch() {
  globalThis.fetch = async (url) => {
    const u = String(url);
    let body;
    if (u.includes('/kb/manual')) body = '# 手册正文\n\n第一段。';
    else if (u.includes('/kb/docs/ops/chronicle')) body = `${todayStr()}｜交付｜小猿｜志的迟到条目`;
    else if (u.includes('/kb/docs')) body = '[]';
    else if (u.includes('/kb/people')) body = '[]';
    else if (u.includes('/kb/tasks')) body = '[]';
    else if (u.includes('/kb/capacity')) body = '{"rooms":[],"now":0}';
    else if (u.includes('/achieve')) body = '{"unlocked":{},"achievements":[]}';
    else if (u.includes('/visitor')) body = '{"on":false,"count":0}';
    else if (u.includes('/budget')) body = '{"day_tokens":0,"week_tokens":0,"day_used":0,"week_used":0}';
    else body = '{}';
    return {
      ok: true, status: 200,
      headers: { get: (k) => (String(k).toLowerCase() === 'content-type' ? 'application/json' : null) },
      text: async () => body,
      json: async () => JSON.parse(body),
    };
  };
}

function newBoard(KbBoard) {
  const root = new El('div');
  const book = { current: '', rooms: new Map(), ownerSend: () => true };
  const toast = { show() {}, loading() { return { close() {} }; } };
  return new KbBoard(root, book, toast);
}

test('kb：快速切页签——志的迟到回包不覆盖驾驶舱', async () => {
  dom();
  routeFetch();
  const { KbBoard } = await import('./kb.js');
  const board = newBoard(KbBoard);
  await tick(); // 说明书首拍就位
  assert.ok(board.pane.innerHTML.includes('手册正文'), '说明书先就位');

  board.switchTab('chronicle'); // 志起拍——getDoc 在途
  board.switchTab('cockpit');   // 立刻切走：志当场作废；驾驶舱壳同步立起
  assert.ok(board.pane.innerHTML.includes('cockpit-grid'), '驾驶舱壳已立');

  await tick(); await tick();   // 两路在途回包全落
  assert.ok(board.pane.innerHTML.includes('cockpit-grid'), '志的迟到回包没把驾驶舱整面盖掉');
  assert.ok(!board.pane.innerHTML.includes('chron-item') &&
            !board.pane.innerHTML.includes('迟到的条目'), '志内容不落 pane');
  // 驾驶舱自己照常完成渲染（不是「什么都不画」的假绿）——五象限全落漆
  const painted = Object.values(board.cockpit.lastPaint).join('');
  assert.ok(painted.includes('人在哪'), '驾驶舱象限照常落漆');

  board.cockpit.stop(); // 测试世界跑的是真 setInterval——收尾必停
  resetDom();
});

test('kb：快速切页签——驾驶舱切走后不再画（说明书回到 pane）', async () => {
  dom();
  routeFetch();
  const { KbBoard } = await import('./kb.js');
  const board = newBoard(KbBoard);
  await tick();
  board.switchTab('cockpit');
  await tick(); await tick(); // 驾驶舱满拍
  assert.ok(board.pane.innerHTML.includes('cockpit-grid'), '驾驶舱先就位');

  board.switchTab('manual'); // 切走：stop 即作废在途，说明书同步回 pane
  assert.ok(board.pane.innerHTML.includes('手册正文'), '说明书立即回 pane');
  await tick(); await tick();
  assert.ok(board.pane.innerHTML.includes('手册正文') &&
            !board.pane.innerHTML.includes('cockpit-grid'), '驾驶舱的迟到回包不抢回 pane');

  resetDom();
});

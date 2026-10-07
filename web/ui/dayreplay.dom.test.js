// dayreplay.dom.test.js — r_32：分享页初始化分支的 DOM 冒烟＋敏感面。
// 与 visitor.dom.test 同款纪律：testdom 假环境＋location 注入。断言：
// /dayreplay/{key} 路径 → 标记对象（key/date/token）＋裁剪样式注入（只
// 留办公室板块，写面全藏）＋分享顶条；主应用路径零动作。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom } from '../room/testdom.js';

test('initDayReplay：/dayreplay 路径 → 分享态标记＋裁剪样式＋顶条', async () => {
  dom();
  globalThis.location = { pathname: '/dayreplay/alpha', search: '?t=abc123&date=2026-10-03' };
  const { initDayReplay, dayReplayMode } = await import('./dayreplay.js');
  assert.equal(initDayReplay(), true, '/dayreplay 路径判定为分享页');
  assert.equal(dayReplayMode(), true, '分享态判定为真');
  const dr = globalThis.__NIUMA_DAYREPLAY__;
  assert.ok(dr && dr.key === 'alpha' && dr.token === 'abc123' && dr.date === '2026-10-03',
    `标记对象完整（got ${JSON.stringify(dr)}）`);
  // 裁剪样式：只留办公室，写面/侧栏/其他板块全藏
  const style = globalThis.document.head.children.find((c) => c.tag === 'style');
  assert.ok(style && style._html.includes('.board:not(#board-room)'), '裁剪样式只留办公室板块');
  assert.ok(style._html.includes('#composer'), '发送面藏');
  assert.ok(style._html.includes('#side,'), '侧栏藏');
  // 分享顶条挂载（含日期）
  const bar = globalThis.document.body.children.find((c) => c.id === 'dayreplay-bar');
  assert.ok(bar, '分享顶条挂载');
  assert.ok(String(bar._html || bar.textContent || '').includes('2026-10-03'), '顶条带日期');
  resetDom();
});

test('initDayReplay：主应用路径 → 零动作（入口照旧进主应用）', async () => {
  dom();
  delete globalThis.__NIUMA_DAYREPLAY__;
  globalThis.location = { pathname: '/app', search: '' };
  const { initDayReplay, dayReplayMode, main } = await import('./dayreplay.js');
  assert.equal(initDayReplay(), false, '主应用路径不进分享态');
  assert.equal(dayReplayMode(), false);
  await main(); // no-op：不炸不挂 DOM
  assert.ok(!globalThis.document.body.children.some(c => c.id === 'dayreplay-bar'), '零 DOM 动作');
  resetDom();
});

// 敏感面（r_19 口径的 r_32 延伸）：访客页（/view）不建 replay UI 的纪律
// 不变——dayreplay 是独立分支（有自己的 token 门），不是访客档开口子。
// 抽 roomview 源文本锚：访客守卫仍在分享守卫之前。
import { readFileSync } from 'node:fs';
const roomSrc = readFileSync(new URL('../room/roomview.js', import.meta.url), 'utf8');
test('敏感面：/view 访客仍不建回放 UI；分享页才摘导出面', () => {
  const visitorGate = roomSrc.indexOf('if (globalThis.__NIUMA_VISITOR__) return;');
  const shareGate = roomSrc.indexOf('__NIUMA_DAYREPLAY__) {');
  assert.ok(visitorGate > -1, '访客档守卫在（/view 不开放时间轴——r_19 定稿）');
  assert.ok(shareGate > -1, '分享页导出面摘除守卫在');
  // 录制腿双守卫：访客与分享页都不录
  assert.match(roomSrc, /!globalThis\.__NIUMA_VISITOR__ && !globalThis\.__NIUMA_DAYREPLAY__/,
    '录制腿的访客/分享页双守卫在');
});

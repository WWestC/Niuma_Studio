// navstack.test.js — 板块导航前进/回退栈的纯函数契约：note 记账
// （重复忽略/同板块子路径合并/新板块截断 push）、back/forward 弹目标
// 且程序性落位不记账（pending 计数，连点不乱账）、亮灭广播随栈位走。

import test from 'node:test';
import assert from 'node:assert/strict';
import { NavStack } from './navstack.js';

function stacked(...hashes) {
  const seen = [];
  const nav = new NavStack((b, f) => seen.push(`${b ? 'B' : '-'}${f ? 'F' : '-'}`));
  for (const h of hashes) nav.note(h);
  return { nav, seen };
}

test('首条建栈：无处回退也无处前进', () => {
  const { nav } = stacked('#/room');
  assert.equal(nav.canBack, false);
  assert.equal(nav.canFwd, false);
  assert.equal(nav.back(), null);
  assert.equal(nav.forward(), null);
});

test('换板块 push：可回退；重复 hash 与重入的 route() 不多记', () => {
  const { nav } = stacked('#/room', '#/chat');
  assert.equal(nav.canBack, true);
  assert.equal(nav.note('#/chat'), false, '同 hash 忽略（boot 期两次 route 重入）');
  assert.equal(nav.stack.length, 2);
  assert.equal(nav.note('#/room'), true, '回旧板块也是新记录（浏览器同款语义）');
  assert.deepEqual(nav.stack, ['#/room', '#/chat', '#/room']);
});

test('同板块子路径合并：栈顶换成最新页签，不增长', () => {
  const { nav } = stacked('#/kb', '#/kb/chronicle', '#/kb/docs');
  assert.equal(nav.stack.length, 1);
  assert.equal(nav.stack[0], '#/kb/docs', '回退一步跨板块，前进回来落在离开时的页签');
});

test('back/forward：弹目标、程序性落位不记账、亮灭随之', () => {
  const { nav, seen } = stacked('#/room', '#/chat', '#/project');
  assert.equal(nav.back(), '#/chat');
  assert.equal(nav.note('#/chat'), false, 'pending 消费：程序性落位不 push');
  assert.equal(nav.canFwd, true);
  assert.equal(nav.forward(), '#/project');
  assert.equal(nav.note('#/project'), false);
  assert.equal(nav.canFwd, false);
  assert.ok(seen.length > 0, '亮灭广播有拍');
  assert.equal(nav.stack.length, 3, '来回走不动账');
});

test('回退后走新路：前向记录截断作废（浏览器同款）', () => {
  const { nav } = stacked('#/room', '#/chat', '#/project');
  nav.back(); nav.note('#/chat'); // ← 一步
  nav.note('#/term'); // 从 chat 走新路
  assert.equal(nav.canFwd, false, '旧前程已作废');
  assert.deepEqual(nav.stack, ['#/room', '#/chat', '#/term']);
});

test('连点 back()：两个 pending 各自消费，第二落位不误记 push', () => {
  const { nav } = stacked('#/room', '#/chat', '#/project');
  assert.equal(nav.back(), '#/chat');
  assert.equal(nav.back(), '#/room');
  assert.equal(nav.note('#/chat'), false);
  assert.equal(nav.note('#/room'), false);
  assert.equal(nav.stack.length, 3, '连点两步不添新账');
  assert.equal(nav.canFwd, true);
  assert.equal(nav.forward(), '#/chat');
});

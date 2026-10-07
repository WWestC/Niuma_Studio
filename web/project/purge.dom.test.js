// purge.dom.test.js — 项目管理概览卡危险区（清退成员/重置项目）的 DOM
// 冒烟（testdom 第一层）：按钮只在「开张中的非大厅项目」亮（草稿没有
// 房与人、归档封存只读、大厅是全工作室的）；两个动作走 wire 封装发
// POST /p/{key}/dismiss | /reset 且口令 DISMISS/RESET 由封装代填；成功
// 走 ok 回执＋刷新，拒绝把 data.error 交给 toast、绝不重载窗口（重载
// 是成功路径的专属收尾）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

const PROJECTS = [
  { key: 'default', name: 'Niuma_Studio', status: 'active' },
  { key: 'alpha', name: '甲项目', status: 'active' },
  { key: 'delta', name: '丁草稿', status: 'draft' },
  { key: 'gamma', name: '戊归档', status: 'archived' },
];

function fakeBook(current = 'default') {
  return {
    current,
    owner: { name: '房主' },
    rooms: new Map(),
    frameTo: () => true,
    refreshRooms: async () => {},
  };
}

function resp(body, ok = true, status = 200) {
  return {
    ok, status,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
  };
}

// posts 收集本世界里的全部写请求；POST 的应答形状可注（默认成功回执）。
function stubAPI(posts, postReply) {
  globalThis.fetch = async (url, opts) => {
    const u = String(url);
    if (opts && opts.method === 'POST') {
      posts.push({ url: u, body: JSON.parse(opts.body || '{}') });
      if (postReply) return postReply;
      return resp({ ok: true, steps: [], note: '回执' });
    }
    if (u.includes('/projects')) return resp(PROJECTS);
    if (u.includes('/agents')) return resp([]);
    if (u.includes('/staffing')) return resp({ rows: [] });
    if (u.includes('/plan')) return resp({ plan: null });
    if (u.includes('/tasks')) return resp([]);
    if (u.includes('/schedule')) return resp({});
    return resp({});
  };
}

async function boardWith(current = 'default', posts = [], postReply) {
  const d = dom();
  stubAPI(posts, postReply);
  const toasts = [];
  const { ProjectBoard } = await import('./project.js');
  const board = new ProjectBoard(new El('div'), fakeBook(current),
    { show(msg, kind) { toasts.push({ msg, kind }); } }, {});
  return { board, toasts, ...d };
}

test('危险区按钮只对开张中的非 Niuma_Studio 项目亮', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  await new Promise((r) => setTimeout(r, 0)); // 概览首刷排干
  const html = board.els.panes.overview.innerHTML;
  assert.match(html, /data-pact="dismiss"/, '开张项目应有「清退成员」');
  assert.match(html, /data-pact="reset"/, '开张项目应有「重置项目」');

  for (const key of ['default', 'delta', 'gamma']) {
    board.selectKey(key);
    await new Promise((r) => setTimeout(r, 0));
    const h = board.els.panes.overview.innerHTML;
    assert.doesNotMatch(h, /data-pact="dismiss"/, `${key} 不该有清退按钮`);
    assert.doesNotMatch(h, /data-pact="reset"/, `${key} 不该有重置按钮`);
  }
  resetDom();
});

test('清退成员：POST /dismiss 带口令，成功回执走 ok toast', async () => {
  const posts = [];
  const { board, toasts } = await boardWith('alpha', posts);
  await board.revealed();
  await board.dismissMembers();
  assert.equal(posts.length, 1, '应恰发一写');
  assert.equal(posts[0].url, '/p/alpha/dismiss');
  assert.deepEqual(posts[0].body, { confirm: 'DISMISS' });
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].kind, 'ok');
  resetDom();
});

test('清退被拒：data.error 进 toast（err），不装成功', async () => {
  const posts = [];
  const { board, toasts } = await boardWith('alpha', posts,
    resp({ error: '确认口令不符' }, false, 400));
  await board.revealed();
  await board.dismissMembers();
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].kind, 'err');
  assert.match(toasts[0].msg, /确认口令不符/);
  resetDom();
});

test('重置项目：POST /reset 带口令，成功后整页重载', async () => {
  const posts = [];
  const loc = { reloads: 0, reload() { this.reloads++; } };
  const { board, toasts } = await boardWith('alpha', posts);
  globalThis.location = loc; // 重载是成功路径的专属收尾——测试世界记账
  await board.revealed();
  await board.resetProject();
  assert.equal(posts.length, 1, '应恰发一写');
  assert.equal(posts[0].url, '/p/alpha/reset');
  assert.deepEqual(posts[0].body, { confirm: 'RESET' });
  assert.equal(toasts[0].kind, 'ok');
  await new Promise((r) => setTimeout(r, 900)); // 重载令牌的 800ms 延时
  assert.equal(loc.reloads, 1, '成功后应整页重载一次');
  resetDom();
});

test('重置被拒：err toast 且绝不重载', async () => {
  const posts = [];
  const loc = { reloads: 0, reload() { this.reloads++; } };
  const { board, toasts } = await boardWith('alpha', posts,
    resp({ error: '已归档项目封存只读' }, false, 409));
  globalThis.location = loc;
  await board.revealed();
  await board.resetProject();
  assert.equal(toasts.length, 1);
  assert.equal(toasts[0].kind, 'err');
  assert.match(toasts[0].msg, /封存只读/);
  await new Promise((r) => setTimeout(r, 50));
  assert.equal(loc.reloads, 0, '拒绝路径不得重载');
  resetDom();
});

// ── 删除项目（v2.16 除名：草稿/归档的概览卡入口） ──

test('删除按钮只对草稿/归档亮（开张中先归档、大厅永不）', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  await new Promise((r) => setTimeout(r, 0));
  assert.doesNotMatch(board.els.panes.overview.innerHTML, /data-pact="delete"/, '开张中的项目没有删除钮');
  for (const key of ['default']) {
    board.selectKey(key);
    await new Promise((r) => setTimeout(r, 0));
    assert.doesNotMatch(board.els.panes.overview.innerHTML, /data-pact="delete"/, `${key} 没有删除钮`);
  }
  for (const key of ['delta', 'gamma']) {
    board.selectKey(key);
    await new Promise((r) => setTimeout(r, 0));
    assert.match(board.els.panes.overview.innerHTML, /data-pact="delete"/, `${key} 应有删除钮`);
  }
  resetDom();
});

test('删除项目：POST /delete 带口令，成功走 ok toast，不重载窗口', async () => {
  const posts = [];
  const loc = { reloads: 0, reload() { this.reloads++; } };
  const { board, toasts } = await boardWith('delta', posts);
  globalThis.location = loc;
  await board.revealed();
  await board.deleteProject();
  assert.equal(posts.length, 1, '应恰发一写');
  assert.equal(posts[0].url, '/p/delta/delete');
  assert.deepEqual(posts[0].body, { confirm: 'DELETE' });
  assert.equal(toasts[0].kind, 'ok');
  await new Promise((r) => setTimeout(r, 50));
  assert.equal(loc.reloads, 0, '删除不重载窗口（除名不动活房，重置才重载）');
  resetDom();
});

test('删除被拒：data.error 进 toast（err）', async () => {
  const posts = [];
  const { board, toasts } = await boardWith('gamma', posts,
    resp({ error: '确认口令不符' }, false, 400));
  await board.revealed();
  await board.deleteProject();
  assert.equal(toasts[0].kind, 'err');
  assert.match(toasts[0].msg, /确认口令不符/);
  resetDom();
});

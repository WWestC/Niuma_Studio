// project.dom.test.js — 项目管理跟办公室走（进门落位＋板上换房＋跳转
// 占位）的 DOM 冒烟（testdom 第一层）。口径：跟房一视同仁——大厅也是
// 一间房，回大厅就落大厅（设置中心 project() 同款优先级）；跨板跳转
// （去审批/跳转条）指名的项目优先于跟房。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

const PROJECTS = [
  { key: 'default', name: 'Niuma_Studio', status: 'active' },
  { key: 'alpha', name: '甲项目', status: 'active' },
  { key: 'beta', name: '乙项目', status: 'active' },
];

// book 替身：ProjectBoard 只喝 current/rooms/owner；frameTo 不进门
function fakeBook(current = 'default') {
  return {
    current,
    owner: { name: '房主' },
    rooms: new Map(),
    frameTo: () => true,
  };
}

// getJSON 契约五面的最小响应（照 fetchStub 的形状手搓——本文件需要按
// URL 分流而不是按序：ProjectBoard 一进门并发拉项目/人员/编制/计划/
// 概览多路，跳转竞态用例里顺序桩太脆）
function resp(body) {
  return {
    ok: true, status: 200,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
  };
}

// 按端点回 JSON：/projects /agents /staffing /plan /tasks /schedule，
// 其余端点一律空对象（概览拿不到任务只是空态卡，不炸）
function stubAPI() {
  globalThis.fetch = async (url) => {
    const u = String(url);
    if (u.includes('/projects')) return resp(PROJECTS);
    if (u.includes('/agents')) return resp([]);
    if (u.includes('/staffing')) return resp({ rows: [] });
    if (u.includes('/plan')) return resp({ plan: null });
    if (u.includes('/tasks')) return resp([]);
    if (u.includes('/schedule')) return resp({});
    return resp({});
  };
}

async function boardWith(current = 'default') {
  const d = dom();
  stubAPI();
  const { ProjectBoard } = await import('./project.js');
  const board = new ProjectBoard(new El('div'), fakeBook(current), { show() {} }, {});
  return { board, ...d };
}

test('进门跟房：人在甲项目办公室，板块落甲项目', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  assert.equal(board.key, 'alpha', '查看项目跟着当前房落位');
  assert.match(board.els.select.innerHTML, /value="alpha" selected/, '选择器选中甲项目');
  resetDom();
});

test('回 Niuma_Studio 落 Niuma_Studio：跟房一视同仁，进门顶掉手动选择', async () => {
  const { board } = await boardWith('default');
  await board.revealed();
  assert.equal(board.key, 'default', 'Niuma_Studio 也是一间房');
  board.selectKey('beta'); // 手动切走查看项目
  board.conceal();
  await board.revealed(); // 仍在大厅，进门跟房落回大厅
  assert.equal(board.key, 'default');
  resetDom();
});

test('板上换房跟走：可见才跟，隐藏零动作', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  board.book.current = 'beta';
  board.onRoomSwitch(); // 板块在屏上：跟着换
  assert.equal(board.key, 'beta');
  await new Promise((r) => setTimeout(r, 0)); // onRoomSwitch 的 refresh 排干
  board.conceal();
  board.book.current = 'alpha';
  board.onRoomSwitch(); // 板块隐藏：不动，等进门 revealed 落位
  assert.equal(board.key, 'beta');
  resetDom();
});

test('跨板跳转占位：进门跟房给指名项目让位', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  assert.equal(board.key, 'alpha');
  // app.js 的真实时序：先起 reviewPending（同步占位），hashchange 的
  // route→revealed 后到——revealed 的跟房不得把指名项目顶掉
  const jump = board.reviewPending('beta');
  const entry = board.revealed();
  await Promise.all([jump, entry]);
  assert.equal(board.key, 'beta', '跳转指名的乙项目站稳');
  // 占位一次性：消费后再进门回到跟房口径
  board.book.current = 'alpha';
  await board.revealed();
  assert.equal(board.key, 'alpha');
  resetDom();
});

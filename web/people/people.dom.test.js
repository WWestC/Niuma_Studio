// people.dom.test.js — 牛马管理跟办公室走（进门落位＋板上换房＋跳转
// 占位）的 DOM 冒烟（testdom 第一层）。口径：跟房一视同仁——大厅也是
// 一间房，回大厅就落大厅（与项目管理板、设置中心 project() 同款优先
// 级）；跨板跳转（聊天「加编制」）指名的项目优先于跟房。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom, El } from '../room/testdom.js';

const PROJECTS = [
  { key: 'default', name: 'Niuma_Studio', status: 'active' },
  { key: 'alpha', name: '甲项目', status: 'active' },
  { key: 'beta', name: '乙项目', status: 'active' },
];
const PEOPLE = [
  { name: '阿甲', role: '编排者', online: true, room: 'default', rank: 3 },
  { name: '小乙', role: 'Backend Dev', online: true, room: 'alpha', rank: 2 },
];

// book 替身：PeopleBoard 只喝 current/owner/ownerSend
function fakeBook(current = 'default') {
  return { current, owner: { name: '房主' }, rooms: new Map(), ownerSend: () => true };
}

// getJSON 契约五面的最小响应（照 fetchStub 的形状手搓——本文件需要按
// URL 分流而不是按序：board 一进门并发拉项目/名册/编制多路）
function resp(body) {
  return {
    ok: true, status: 200,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
  };
}

// 按端点回 JSON：/projects /kb/people /establishment /agents /staffing，
// 其余端点一律空对象（编制面拿不到行只是空表卡，不炸）
function stubAPI() {
  globalThis.fetch = async (url) => {
    const u = String(url);
    if (u.includes('/projects')) return resp(PROJECTS);
    if (u.includes('/kb/people')) return resp(PEOPLE);
    if (u.includes('/establishment')) return resp({ rows: [] });
    if (u.includes('/agents')) return resp([]);
    if (u.includes('/staffing')) return resp({ rows: [] });
    return resp({});
  };
}

async function boardWith(current = 'default') {
  const d = dom();
  stubAPI();
  const { PeopleBoard } = await import('./people.js');
  const board = new PeopleBoard(new El('div'), fakeBook(current), { show() {} });
  return { board, ...d };
}

test('进门跟房：人在甲项目办公室，花名册筛子与编制面落甲项目', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  assert.equal(board.roster.projectFilter, 'alpha', '筛子跟着当前房落位');
  assert.equal(board.staffing.project, 'alpha', '编制面的项目同步落位');
  const html = board.els.panes.roster.innerHTML;
  assert.match(html, /value="alpha" selected/, '项目下拉选中甲项目');
  assert.ok(html.includes('data-person="小乙"'), '甲项目成员在列');
  assert.ok(!html.includes('data-person="阿甲"'), '别项目成员被筛掉');
  board.dispose();
  resetDom();
});

test('回 Niuma_Studio 落 Niuma_Studio：筛子跟房一视同仁（首进即 Niuma_Studio）', async () => {
  const { board } = await boardWith('default');
  await board.revealed();
  assert.equal(board.roster.projectFilter, 'default', 'Niuma_Studio 也是一间房');
  board.roster.projectFilter = 'beta'; // 手动切走筛子
  board.conceal();
  await board.revealed(); // 仍在大厅，进门跟房落回大厅
  assert.equal(board.roster.projectFilter, 'default');
  board.dispose();
  resetDom();
});

test('板上换房跟走：可见才跟，隐藏零动作', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  assert.equal(board.roster.projectFilter, 'alpha');
  board.book.current = 'beta';
  board.onRoomSwitch(); // 板块在屏上：跟着换
  assert.equal(board.roster.projectFilter, 'beta');
  assert.equal(board.staffing.project, 'beta');
  await new Promise((r) => setTimeout(r, 0)); // 编制面的 reload 排干
  board.conceal();
  board.book.current = 'alpha';
  board.onRoomSwitch(); // 板块隐藏：不动，等进门 revealed 落位
  assert.equal(board.roster.projectFilter, 'beta');
  board.dispose();
  resetDom();
});

test('跨板跳转占位：「加编制」指名的项目不被进门跟房顶掉', async () => {
  const { board } = await boardWith('alpha');
  await board.revealed();
  assert.equal(board.roster.projectFilter, 'alpha');
  // app.js 的真实时序：先起 prefillEstablishment（同步占位＋自己落
  // 位），hashchange 的 route→revealed 后到——跟房不得顶掉指名项目
  board.prefillEstablishment('beta', { key: 'dev', role: '开发' });
  await board.revealed();
  assert.equal(board.tab, 'staffing');
  assert.equal(board.staffing.project, 'beta', '加编制落在指名的乙项目');
  // 占位一次性：消费后再进门回到跟房口径
  board.book.current = 'alpha';
  board.conceal();
  await board.revealed();
  assert.equal(board.roster.projectFilter, 'alpha');
  assert.equal(board.staffing.project, 'alpha');
  board.dispose();
  resetDom();
});

// roomspause.test.js — 房间暂停的前端面：
//
// 折算（rooms.js）：welcome 帧水合 paused、pause 广播帧翻 room.paused、
// /projects 轮询随行——三条路都进 roomList() 的 paused 位。
// 侧栏（switcher.js）：时间角落已撤、暂停/开始按钮在位（含状态翻面）、
// 点击不换房、乐观翻面＋写失败回滚。
//
// 驱动方式与 navbadge.test.js 同款：RoomBook 直构、拨号置空、从
// room.conn.ev.onFrame 喂帧走真实 #intake 入口。

import test from 'node:test';
import assert from 'node:assert/strict';
import { El } from '../room/testdom.js';

globalThis.document = { hidden: false, createElement: (t) => new El(t) };
let posts = [];
let failPause = false;
let projectsFail = false;
let projectsPayload = [
  { key: 'alpha', name: '项目甲', status: 'active' },
  { key: 'beta', name: '项目乙', status: 'active', paused: true },
];
globalThis.fetch = (url, init = {}) => {
  const u = String(url);
  if (u.endsWith('/projects')) {
    if (projectsFail) return Promise.reject(new Error('poll down'));
    return Promise.resolve({
      ok: true, status: 200,
      headers: { get: () => null },
      json: async () => projectsPayload,
    });
  }
  if (init.method === 'POST' && /\/pause$/.test(u)) {
    posts.push({ url: u, body: JSON.parse(init.body) });
    if (failPause) {
      return Promise.resolve({ ok: false, status: 500, headers: { get: () => null }, json: async () => ({ reason: 'boom' }) });
    }
    return Promise.resolve({ ok: true, status: 200, headers: { get: () => null }, json: async () => ({ paused: true }) });
  }
  return Promise.reject(new Error('stub: no hydrate'));
};

const { RoomBook } = await import('./rooms.js');
const { ObserverConn } = await import('../wire/conn.js');
const { LobbyKey, Msg } = await import('../wire/wire.js');

ObserverConn.prototype.start = function () {};

async function freshBook() {
  const book = new RoomBook({
    onChange: () => {}, onNotice: (t) => notices.push(t), onEvent: () => {},
    onChatLine: () => {}, onWorkEvent: () => {},
  });
  book.owner = { name: '房主', role: 'owner' };
  await book.refreshRooms(); // 大厅＋甲/乙（乙带着 paused:true）
  return book;
}
let notices = [];

test('welcome 水合：迟到窗口一见即冻结——room.paused 落位并上 roomList', async () => {
  const book = await freshBook();
  assert.equal(book.rooms.get('beta').paused, true, '/projects 随行已折算（轮询兜底路）');
  assert.equal(book.rooms.get('alpha').paused, false);

  book.rooms.get('alpha').conn.ev.onFrame({ type: Msg.Welcome, members: [], paused: true });
  assert.equal(book.rooms.get('alpha').paused, true, 'welcome 帧应水合暂停态');
  const row = book.roomList().find((r) => r.key === 'alpha');
  assert.equal(row.paused, true, 'roomList 应暴露 paused 位');
});

test('pause 广播帧：paused/resumed 翻面，不进消息流', async () => {
  const book = await freshBook();
  const room = book.rooms.get('alpha');
  const lenBefore = room.messages.length;

  room.conn.ev.onFrame({ type: Msg.Pause, event: 'paused', from: '房主', ts: 1 });
  assert.equal(room.paused, true);
  room.conn.ev.onFrame({ type: Msg.Pause, event: 'resumed', from: '房主', ts: 2 });
  assert.equal(room.paused, false);
  assert.equal(room.messages.length, lenBefore, 'pause 帧不进消息流（留痕走 system 行）');
});

// ---- 侧栏渲染面（switcher.js）----

function fakeNav() {
  return {
    _html: '',
    children: [],
    listeners: {},
    set innerHTML(v) { this._html = String(v); this.children = []; },
    get innerHTML() { return this._html; },
    appendChild(c) { this.children.push(c); return c; },
    addEventListener(type, fn) { this.listeners[type] = fn; },
    fire(ev) { this.listeners.click(ev); },
  };
}

async function switcherWith(book) {
  const { SwitcherView } = await import('./switcher.js');
  const nav = fakeNav();
  const view = new SwitcherView(nav, book);
  view.render();
  return { view, nav };
}

function rowOf(nav, key) {
  return nav.children.find((c) => c.dataset && c.dataset.room === key);
}

test('侧栏行：时间角落已撤、暂停/开始按钮在位；已暂停的房翻成恢复钮', async () => {
  const book = await freshBook();
  const { nav } = await switcherWith(book);

  const rowHtml = rowOf(nav, 'alpha').innerHTML;
  assert.doesNotMatch(rowHtml, /room-time/, '时间角落已删');
  assert.match(rowHtml, /data-pausetoggle="alpha"/, '暂停按钮在位');
  assert.match(rowHtml, /room-pause["' ]/, '暂停钮类名');
  const running = rowOf(nav, 'alpha');
  assert.match(running.innerHTML, /room-pause(?!-)/, '运行中的房显示暂停动作');

  const paused = rowOf(nav, 'beta');
  assert.match(paused.className, /paused/, '暂停房行带 paused 态类');
  assert.match(paused.innerHTML, /room-pause on/, '暂停房按钮翻成 on（恢复动作）');
});

test('点击暂停钮：不换房、POST 落写、乐观翻面；写失败回滚并提示', async () => {
  const book = await freshBook();
  const { nav } = await switcherWith(book);
  posts = [];

  const tog = { dataset: { pausetoggle: 'alpha' } };
  nav.fire({ target: { closest: (sel) => (sel === '[data-pausetoggle]' ? tog : null) } });

  await new Promise((r) => setTimeout(r, 20));
  assert.equal(book.current, LobbyKey, '点暂停钮不换房');
  assert.equal(posts.length, 1, '一次 POST');
  assert.deepEqual(posts[0].body, { paused: true });
  assert.equal(book.rooms.get('alpha').paused, true, '乐观翻面');
  assert.match(rowOf(nav, 'alpha').innerHTML, /room-pause on/, '按钮翻成恢复态');

  // 写失败回滚：再点一次（恢复方向，假 500）——按钮回滚到点击前的
  // 暂停态，不撒谎
  failPause = true;
  nav.fire({ target: { closest: (sel) => (sel === '[data-pausetoggle]' ? tog : null) } });
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(book.rooms.get('alpha').paused, true, '写失败回滚到点击前状态——按钮不撒谎');
  assert.ok(notices.some((n) => n.includes('boom')), '失败要有提示（带失败原因）');
  failPause = false;
});

test('行点击照旧换房（暂停钮缺席时不受影响）', async () => {
  const book = await freshBook();
  const { nav } = await switcherWith(book);
  const row = { dataset: { room: 'beta' } };
  nav.fire({ target: { closest: (sel) => (sel === '[data-room]' ? row : null) } });
  assert.equal(book.current, 'beta');
});

// ── 大厅行的轮询随行（回归：暂停「自己恢复」的洞）─────────────────
//
// 旧伤：refreshRooms 给大厅合成裸条目（不带 paused/autopilot），折算循
// 环把暂停中的大厅拍回 false——暂停后 ≤60s 按钮翻回、小人解冻。修复后
// 大厅条目从 /projects 的大厅记录带位；读失败保持现状。

test('大厅暂停的轮询随行：/projects 大厅记录带 paused:true → 不被拍回 false', async () => {
  projectsPayload = [
    { key: 'default', name: 'Niuma_Studio', status: 'active' },
    { key: 'alpha', name: '项目甲', status: 'active' },
  ];
  const book = await freshBook();
  const lobby = book.rooms.get(LobbyKey);
  assert.equal(lobby.paused, false, '初始未暂停');

  lobby.conn.ev.onFrame({ type: Msg.Pause, event: 'paused', from: '房主', ts: 1 });
  assert.equal(lobby.paused, true, '广播帧已暂停');

  // 下一拍轮询：大厅记录带着真源 paused:true（旧代码的合成裸条目在这
  // 里把大厅拍回 false——「暂停过一会自己恢复」的洞）
  projectsPayload = [
    { key: 'default', name: 'Niuma_Studio', status: 'active', paused: true },
    { key: 'alpha', name: '项目甲', status: 'active' },
  ];
  await book.refreshRooms();
  assert.equal(lobby.paused, true, '轮询不得把暂停中的大厅拍回去');
  projectsPayload = [
    { key: 'alpha', name: '项目甲', status: 'active' },
    { key: 'beta', name: '项目乙', status: 'active', paused: true },
  ];
});

test('大厅暂停的轮询恢复：大厅记录不带 paused（服务端 omitempty=false）→ 如实翻回', async () => {
  projectsPayload = [{ key: 'default', name: 'Niuma_Studio', status: 'active' }];
  const book = await freshBook();
  const lobby = book.rooms.get(LobbyKey);
  lobby.conn.ev.onFrame({ type: Msg.Pause, event: 'paused', from: '房主', ts: 1 });
  assert.equal(lobby.paused, true);

  // 服务端已恢复（别处翻的）而本窗口错过帧：轮询是兜底同步，该翻回
  projectsPayload = [{ key: 'default', name: 'Niuma_Studio', status: 'active', paused: false }];
  await book.refreshRooms();
  assert.equal(lobby.paused, false, '真源已恢复时轮询应如实同步');
  projectsPayload = [
    { key: 'alpha', name: '项目甲', status: 'active' },
    { key: 'beta', name: '项目乙', status: 'active', paused: true },
  ];
});

test('读失败不清账：/projects 拒答时暂停中的大厅保持暂停', async () => {
  const book = await freshBook();
  const lobby = book.rooms.get(LobbyKey);
  lobby.conn.ev.onFrame({ type: Msg.Pause, event: 'paused', from: '房主', ts: 1 });

  projectsFail = true;
  try {
    await book.refreshRooms();
    assert.equal(lobby.paused, true, '读失败不得拿合成值覆盖真状态');
  } finally {
    projectsFail = false;
  }
});

test('右簇顺序：未读红签在内侧、暂停钮收尾贴行右缘', async () => {
  const book = await freshBook();
  book.rooms.get('beta').unread = 11;
  book.rooms.get('beta').workFresh = 2;
  const { nav } = await switcherWith(book);
  const html = rowOf(nav, 'beta').innerHTML;
  const dotAt = html.indexOf('room-taskdot');
  const badgeAt = html.indexOf('room-badge');
  const pauseAt = html.indexOf('data-pausetoggle');
  assert.ok(dotAt >= 0 && badgeAt > dotAt, '绿点→红签依次在内');
  assert.ok(pauseAt > badgeAt && pauseAt > dotAt, '暂停钮在最外（红点与暂停换位后的钉子）');
});

// ── 办公室冻结的接线扫描（roomview 是 canvas 重模块，走 usage.test.js
// 的源码断言先例：删闸必红——面板哑火不是「没数据」是「没接线」）──────

import { readFileSync } from 'node:fs';
const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');

test('办公室冻结接线在位：update 门前＋纱幕面＋roomPaused 取数', () => {
  const src = read('../room/roomview.js');
  const gate = src.indexOf('if (this.roomPaused()) {');
  assert.ok(gate >= 0, 'update() 的暂停门——世界定格（clock 不走）');
  // 暂停块内、定格收口前，出生淡入照走：重启落回暂停房时全员是新
  // Actor（spawnT 满格 0.35），冻着不走则 draw 的 globalAlpha 恒 0
  // ——名牌浮在空地上、小人永远隐形。删这条腿必红。
  const block = src.slice(gate, gate + 500);
  const ret = block.indexOf('return;');
  assert.ok(ret > 0, '暂停门块内有定格收口');
  assert.match(block.slice(0, ret), /spawnT/, '出生淡入在收口前放行（暂停房不隐身）');
  assert.match(src, /roomPaused\(\) \{/, 'roomPaused() 取数口在位');
  assert.match(src, /if \(!target && this\.roomPaused\(\)\)/, 'draw 的暂停纱幕（导出腿不带纱）');
  assert.match(src, /⏸ 已暂停/, '纱幕徽标文案');
});

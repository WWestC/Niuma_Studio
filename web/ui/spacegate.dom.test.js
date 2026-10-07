// spacegate.dom.test.js — 项目空间（v2.12）的 DOM 冒烟。
//
// 守门面：无项目（大厅除外）必须上墙强制立项、门上无关闭路径；有项目
// （草稿/归档皆算创建过）不设门、已上门即落门放行；房册清单未落
// （null）fail-open 不设门；立项链路 POST /projects → lifecycle
// activate → 房册刷新 → 切进新房 → 落门（开张失败不拦人）。
//
// browse 面（侧栏入口）：open() 列全部项目（大厅＋草稿/归档照列、有
// 关闭钮）；pick() 开张项目进房、草稿/归档交 onInspect；守门模式
// close() 拒绝；面板开着时 sync() 原地重画。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom, resetDom } from '../room/testdom.js';

const LOBBY = { key: 'default', name: 'Niuma_Studio', status: 'active' };

function resp(body, ok = true, status = 200) {
  return {
    ok, status,
    headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
    json: async () => body,
    text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
  };
}

// 按方法＋端点分流（POST /projects 与 POST /p/x/lifecycle 都要记里程）
function stubAPI({ activate, delOK = true } = {}) {
  const calls = [];
  globalThis.fetch = async (url, init = {}) => {
    const u = String(url);
    calls.push({ url: u, method: init.method || 'GET' });
    if ((init.method || 'GET') === 'POST' && u.includes('/lifecycle')) {
      return activate ? resp(activate) : resp({ error: 'boom' }, false, 500);
    }
    if ((init.method || 'GET') === 'POST' && u.endsWith('/delete')) {
      return delOK ? resp({ ok: true, steps: [], note: '项目 x 已删除' }) : resp({ error: '确认口令不符' }, false, 400);
    }
    if ((init.method || 'GET') === 'POST' && u.endsWith('/projects')) {
      return resp({ key: 'alpha', name: '甲项目', status: 'draft' });
    }
    return resp({});
  };
  return calls;
}

function fakeBook(projects) {
  return {
    projects,
    current: 'default',
    rooms: new Map([['alpha', { key: 'alpha', members: new Map([['小牛', {}], ['小马', {}]]) }]]),
    refreshRooms: async function () { this.projects = [LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }]; },
    switchTo() {},
  };
}

async function gateWith(projects) {
  const d = dom();
  stubAPI();
  const toasts = [];
  const { SpaceGate } = await import('./spacegate.js');
  const gate = new SpaceGate({
    book: fakeBook(projects),
    toast: { show: (text, kind) => toasts.push([text, kind]) },
    ownerName: '房主',
  });
  return { gate, toasts, ...d };
}

test('无项目（只有大厅）→ 上墙强制立项：无关闭路径', async () => {
  const { gate } = await gateWith([LOBBY]);
  gate.sync();
  assert.equal(gate.shouldHold(), true, '大厅以外无项目即该守');
  assert.equal(gate.up, true, '门上墙');
  assert.equal(gate.el.hidden, false);
  assert.match(gate.el.innerHTML, /工作室还没有项目/);
  assert.match(gate.el.innerHTML, /立项/);
  assert.match(gate.el.innerHTML, /会话出生地/, '门上先亮工作区风险（v2.13）');
  assert.doesNotMatch(gate.el.innerHTML, /data-close/, '门上没有关闭钮');
  resetDom();
});

test('有项目不设门：草稿也算创建过', async () => {
  const { gate } = await gateWith([LOBBY, { key: 'alpha', name: '甲', status: 'draft' }]);
  gate.sync();
  assert.equal(gate.up, false, 'draft 已满足「创建过项目」');
  resetDom();
});

test('已上门的项目册再出现项目 → 落门，hold() 放行', async () => {
  const { gate } = await gateWith([LOBBY]);
  gate.sync();
  assert.equal(gate.up, true);
  let passed = false;
  gate.hold().then(() => { passed = true; });
  gate.book.projects = [LOBBY, { key: 'alpha', name: '甲', status: 'active' }];
  gate.sync();
  await Promise.resolve();
  assert.equal(gate.up, false, '项目出现即落门');
  assert.equal(gate.el.hidden, true);
  assert.equal(passed, true, 'hold() 的等待者被放行');
  resetDom();
});

test('房册清单未落（null）→ fail-open 不设门', async () => {
  const { gate } = await gateWith(null);
  assert.equal(gate.shouldHold(), false, '未知清单不守门');
  gate.sync();
  assert.equal(gate.up, false);
  resetDom();
});

test('立项链路：创建 → 自动开张 → 刷新 → 切房 → 落门', async () => {
  const d = dom();
  const calls = stubAPI({ activate: { key: 'alpha', name: '甲项目', status: 'active' } });
  const book = fakeBook([LOBBY]);
  const switched = [];
  book.switchTo = (k) => switched.push(k);
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (text, kind) => toasts.push(kind) }, ownerName: '房主' });
  gate.sync();
  assert.equal(gate.up, true);
  await gate.create({ key: 'alpha', name: '甲项目', workspace: '/tmp/x' });
  const lifecycle = calls.find((c) => c.method === 'POST' && c.url.includes('/lifecycle'));
  assert.ok(lifecycle, '自动开张调了 lifecycle');
  assert.equal(switched[0], 'alpha', '切进新项目房');
  assert.equal(gate.up, false, '落门放行');
  assert.ok(toasts.includes('ok'), '开张成功 toast');
  resetDom();
});

test('开张失败不拦人：草稿在册即落门，不切房，err toast', async () => {
  const d = dom();
  stubAPI({ activate: null }); // lifecycle → 500
  const book = fakeBook([LOBBY]);
  const switched = [];
  book.switchTo = (k) => switched.push(k);
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (text, kind) => toasts.push(kind) }, ownerName: '房主' });
  gate.sync();
  await gate.create({ key: 'alpha', name: '甲项目', workspace: '/tmp/x' });
  assert.equal(gate.up, false, '草稿已在册，门照落');
  assert.equal(switched.length, 0, '没开张就没有房可切');
  assert.ok(toasts.includes('err'), '开张失败亮 err toast');
  resetDom();
});

// ── browse 面（侧栏「项目空间」入口） ──

async function browseGate(projects) {
  const d = dom();
  stubAPI();
  const book = fakeBook(projects);
  const switched = [];
  book.switchTo = (k) => switched.push(k);
  const inspected = [];
  const { SpaceGate } = await import('./spacegate.js');
  const gate = new SpaceGate({
    book,
    toast: { show: () => {} },
    ownerName: '房主',
    onInspect: (key) => inspected.push(key),
  });
  return { gate, book, switched, inspected, ...d };
}

test('open()：列全部项目（草稿/归档照列），有关闭钮与立项', async () => {
  const { gate } = await browseGate([
    LOBBY,
    { key: 'alpha', name: '甲项目', status: 'active' },
    { key: 'draft1', name: '乙草稿', status: 'draft' },
    { key: 'old', name: '丙封存', status: 'archived' },
  ]);
  gate.open();
  assert.equal(gate.up, true);
  assert.equal(gate.forced, false, '入口开的是 browse 面');
  assert.match(gate.el.innerHTML, /data-close/, 'browse 面有关闭钮');
  assert.match(gate.el.innerHTML, /data-pick="alpha"/);
  assert.match(gate.el.innerHTML, /data-pick="draft1"/, '草稿也照列');
  assert.match(gate.el.innerHTML, /data-pick="old"/, '归档也照列');
  assert.match(gate.el.innerHTML, /data-pick="default"/, '大厅也是一行');
  assert.match(gate.el.innerHTML, /立项/, '底部常驻立项');
  gate.close();
  assert.equal(gate.up, false, 'browse 面可关');
  resetDom();
});

test('pick()：开张项目进房切座；草稿/归档交 onInspect', async () => {
  const { gate, switched, inspected } = await browseGate([
    LOBBY,
    { key: 'alpha', name: '甲项目', status: 'active' },
    { key: 'draft1', name: '乙草稿', status: 'draft' },
  ]);
  gate.open();
  gate.pick('alpha');
  assert.equal(switched[0], 'alpha', '开张项目切进它的房');
  assert.equal(gate.up, false, '点行后面板收起');
  gate.open();
  gate.pick('draft1');
  assert.deepEqual(inspected, ['draft1'], '草稿交给项目管理落位');
  assert.equal(switched.length, 1, '草稿没有房可切');
  resetDom();
});

test('守门模式 close() 拒绝：唯一出路是立项', async () => {
  const { gate } = await gateWith([LOBBY]);
  gate.sync();
  assert.equal(gate.up, true);
  gate.close();
  assert.equal(gate.up, true, '守门面点 ✕/Esc 都关不掉');
  resetDom();
});

test('browse 开着时项目册变动原地重画；守门开着时清空不误落', async () => {
  const { gate, book } = await browseGate([LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }]);
  gate.open();
  book.projects = [LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }, { key: 'beta', name: '乙项目', status: 'active' }];
  gate.sync();
  assert.equal(gate.up, true, 'browse 面不受 sync 强落');
  assert.match(gate.el.innerHTML, /data-pick="beta"/, '新项目原地长出来');
  resetDom();
});

// ── 删除面（v2.16 项目除名＋开张行同权：非大厅行尾常显删除钮） ──

test('删除钮对全部非大厅行常亮（开张中也亮、大厅行没有）', async () => {
  const { gate } = await browseGate([
    LOBBY,
    { key: 'alpha', name: '甲项目', status: 'active' },
    { key: 'draft1', name: '乙草稿', status: 'draft' },
    { key: 'old', name: '丙封存', status: 'archived' },
  ]);
  gate.open();
  const html = gate.el.innerHTML;
  assert.match(html, /data-pdel="alpha"/, '开张中的项目也有删除钮（一体流：先归档再除名）');
  assert.match(html, /data-pdel="draft1"/, '草稿行应有删除钮');
  assert.match(html, /data-pdel="old"/, '归档行应有删除钮');
  assert.doesNotMatch(html, /data-pdel="default"/, '大厅永远没有删除钮');
  // 可删行结构：button 不嵌套——行容器 div 内主按钮＋删除钮并列
  assert.match(html, /class="sg-item has-del[^"]*"/, '可删行走三件套结构');
  resetDom();
});

test('deleteProject：POST /p/{key}/delete 带口令，成功后刷新房册并重画列表', async () => {
  const d = dom();
  const calls = stubAPI();
  const refreshed = [];
  const book = fakeBook([LOBBY, { key: 'draft1', name: '乙草稿', status: 'draft' }]);
  book.refreshRooms = async function () { refreshed.push(1); this.projects = [LOBBY]; };
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (msg, kind) => toasts.push(kind) }, ownerName: '房主' });
  gate.open();
  await gate.deleteProject('draft1');
  const del = calls.find((c) => c.method === 'POST' && c.url.endsWith('/delete'));
  assert.ok(del, '应发出删除写');
  assert.equal(del.url, '/p/draft1/delete');
  assert.equal(refreshed.length, 1, '成功后应刷新房册');
  assert.equal(gate.up, true, 'browse 面开着不收');
  assert.doesNotMatch(gate.el.innerHTML, /data-pick="draft1"/, '被删行原地消失');
  assert.ok(toasts.includes('ok'), '删除成功亮 ok toast');
  resetDom();
});

test('deleteProject 被拒：data.error 进 toast（err），列表不动', async () => {
  const d = dom();
  const calls = stubAPI({ delOK: false });
  const book = fakeBook([LOBBY, { key: 'draft1', name: '乙草稿', status: 'draft' }]);
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (msg, kind) => toasts.push(kind) }, ownerName: '房主' });
  gate.open();
  await gate.deleteProject('draft1');
  assert.ok(toasts.some((k) => k === 'err'), '拒绝亮 err toast');
  assert.match(gate.el.innerHTML, /data-pick="draft1"/, '拒绝路径列表不动');
  resetDom();
});

// ── 开张行的一体流：先 lifecycle archive，再 /delete ──

test('deleteProject（开张中）：先 archive 再 delete，顺序不乱，成功刷新重画', async () => {
  const d = dom();
  const calls = stubAPI({ activate: { key: 'alpha', name: '甲项目', status: 'archived' } });
  const book = fakeBook([LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }]);
  book.refreshRooms = async function () { this.projects = [LOBBY]; };
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (msg, kind) => toasts.push(kind) }, ownerName: '房主' });
  gate.open();
  await gate.deleteProject('alpha');
  const arch = calls.findIndex((c) => c.method === 'POST' && c.url.includes('/lifecycle'));
  const del = calls.findIndex((c) => c.method === 'POST' && c.url.endsWith('/delete'));
  assert.ok(arch >= 0, '开张行删除前先发 lifecycle archive');
  assert.equal(del, arch + 1, 'archive 成功后才发 delete，且紧随其后');
  assert.equal(calls[del].url, '/p/alpha/delete');
  assert.ok(toasts.includes('ok'), '删除成功亮 ok toast');
  assert.doesNotMatch(gate.el.innerHTML, /data-pick="alpha"/, '被删行原地消失');
  resetDom();
});

test('deleteProject（开张中）归档被拒：不发删除写，err toast，行还在', async () => {
  const d = dom();
  const calls = stubAPI({ activate: null }); // lifecycle → 500
  const book = fakeBook([LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }]);
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (msg, kind) => toasts.push([msg, kind]) }, ownerName: '房主' });
  gate.open();
  await gate.deleteProject('alpha');
  assert.equal(calls.some((c) => c.url.endsWith('/delete')), false, '归档没成，删除写一个都不发');
  const errToast = toasts.find(([msg, kind]) => kind === 'err');
  assert.ok(errToast && /归档未成/.test(errToast[0]), '拒绝原因进 err toast');
  assert.match(gate.el.innerHTML, /data-pick="alpha"/, '列表不动');
  resetDom();
});

test('deleteProject（开张中）删除被拒：toast 附「已归档——可重试或复活」去向', async () => {
  const d = dom();
  stubAPI({ activate: { key: 'alpha', name: '甲项目', status: 'archived' }, delOK: false });
  const book = fakeBook([LOBBY, { key: 'alpha', name: '甲项目', status: 'active' }]);
  const { SpaceGate } = await import('./spacegate.js');
  const toasts = [];
  const gate = new SpaceGate({ book, toast: { show: (msg, kind) => toasts.push([msg, kind]) }, ownerName: '房主' });
  gate.open();
  await gate.deleteProject('alpha');
  const errToast = toasts.find(([msg, kind]) => kind === 'err');
  assert.ok(errToast, '拒绝亮 err toast');
  assert.match(errToast[0], /已归档/, '提示带上「项目已归档」的中间态去向');
  resetDom();
});

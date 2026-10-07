// notify.test.js — 声色同门契约（t_170：工单绿点＋通知叮，对话红点＋
// 叮咚各成一对）：绿点的计数（workFresh——任务/需求同一绿语言）与通知
// 叮的放行（onWorkEvent）必须出自同一道 if——听见叮必有绿点指路，看见
// 绿点必曾叮过（冷却窗合并的连珠工单除外，那是 msg 弹窗合并计数的同款
// 纪律）。
//
// 驱动方式：rooms.js 的 DOM 触点全在方法内（document.hidden／conn 拨号
// 均不在导入期），测试直接实例化 RoomBook，用 fetch 假面喂 /projects、
// 拨号置空（ObserverConn.prototype.start），再从 room.conn.ev.onFrame
// 直喂帧——走的是与真实观察连接完全相同的 #intake 入口，不是旁路。

import test from 'node:test';
import assert from 'node:assert/strict';

// DOM/网络假面（构造与驱动期的全部触点；导入期无人碰它们）
globalThis.document = { hidden: false };
globalThis.fetch = (url) => {
  if (String(url).endsWith('/projects')) {
    return Promise.resolve({
      ok: true, status: 200,
      headers: { get: () => null },
      json: async () => [{ key: 'alpha', name: '项目甲', status: 'active' }],
    });
  }
  // 水合端点（notice/acks/reads/reacts/questions）：一律拒——调用方
  // 各自带 .catch 静默降级，与断服同款
  return Promise.reject(new Error('stub: no hydrate'));
};

const { RoomBook } = await import('./rooms.js');
const { SwitcherView } = await import('./switcher.js');
const { ObserverConn } = await import('../wire/conn.js');
const { LobbyKey, Msg, Event } = await import('../wire/wire.js');

// 拨号置空：连接对象只当帧入口的载体，绝不真连
ObserverConn.prototype.start = function () {};

/** 一间带记录面的书：hint／工单钩子／对话钩子全程留痕。 */
async function freshBook() {
  const trace = { hints: [], works: [], chats: [] };
  const book = new RoomBook({
    onChange: (key, hint) => trace.hints.push([key, hint]),
    onNotice: () => {},
    onEvent: () => {},
    onChatLine: (f, room, atMe) => trace.chats.push([f, room.key, atMe]),
    onWorkEvent: (f, room) => trace.works.push([f, room.key]),
  });
  await book.refreshRooms(); // default（大厅）＋ alpha 两间落座
  return { book, trace };
}

const TASK_FRAME = (event = 'created') => ({
  type: Msg.Task, event, task: { id: 't1', title: '改个需求', assignee: '小鹿' }, ts: 1,
});
// 服务端 reqs.go 的立案广播同形（reqCreatedBroadcast）
const REQ_FRAME = (who = '房主') => ({
  type: Msg.Req, event: 'created', from: who,
  req: { id: 'r_01', title: '要个夜间模式', status: 'open' }, text: `${who} 立了需求 r_01「要个夜间模式」`, ts: 3,
});
const SAY_FRAME = { type: Msg.Say, from: '小鹿', text: '在吗', ts: 2 };

test('声色同门：工单变动 workFresh 与 onWorkEvent 同门成对（红路径不受扰）', async () => {
  const { book, trace } = await freshBook();
  const alpha = book.rooms.get('alpha');
  const feed = (f) => alpha.conn.ev.onFrame(f);

  feed(TASK_FRAME());
  feed(TASK_FRAME('updated'));
  assert.equal(alpha.workFresh, 2, '两次任务变动计两枚绿点');
  assert.deepEqual(trace.works.map(([, k]) => k), ['alpha', 'alpha'], '通知叮逐次放行，与计数同门');

  // 'unread' hint 每次都发（switcher 绿点与导航钉都靠它重画）
  assert.equal(trace.hints.filter(([, h]) => h === 'unread').length >= 2, true);

  // 对话走自己的红门：unread 计数、onChatLine 放行、workFresh 不动
  alpha.conn.ev.onFrame(SAY_FRAME);
  assert.equal(alpha.unread, 1, '对话照常计红点');
  assert.equal(alpha.workFresh, 2, '对话不进绿点');
  assert.deepEqual(trace.chats.map(([, k]) => k), ['alpha'], '对话提醒照常放行');
});

test('重编译通告不badge不叮：全室同刻落地的环境新闻不欠任何一间房一次已读', async () => {
  const { book, trace } = await freshBook();
  const alpha = book.rooms.get('alpha');

  // 真实词形的两条通告（发起＋成功收口），直播进没在看的房——
  // 一次「重新编译并重启」不该让所有项目各冒两轮红点＋弹窗
  alpha.conn.ev.onFrame({
    type: Msg.System, event: Event.Rebuild,
    text: '房主已发起源码重编译——编译期间工作室照常运行，完成后将自动重启', ts: 1,
  });
  alpha.conn.ev.onFrame({
    type: Msg.System, event: Event.Rebuild,
    text: '源码已重新编译（耗时 7s）——工作室即将重启，调度成员将由调度器自动归位', ts: 2,
  });
  assert.equal(alpha.unread, 0, 'rebuild 通告不挂红点');
  assert.deepEqual(trace.chats, [], '也不出提醒叮/弹窗（与未读同一道门）');

  // 行本身照进消息窗：开门仍读得到，只是不报信
  assert.equal(alpha.messages.filter((m) => m.frame?.event === Event.Rebuild).length, 2,
    '通告行照落消息窗（历史可读性不因豁免受损）');

  // 同族钉旧史：agg 聚合行同样豁免（老大厅史回放也不欠红点）
  alpha.conn.ev.onFrame({ type: Msg.System, event: Event.Agg, text: '[proj-x] 张三 等 3 条动静', ts: 3 });
  assert.equal(alpha.unread, 0, 'agg 聚合行同门豁免');

  // 对照组：无标记的普通系统行照常 badge＋叮（豁免是白名单不是一刀切）
  alpha.conn.ev.onFrame({ type: Msg.System, text: '房主开启了什么开关', ts: 4 });
  assert.equal(alpha.unread, 1, '普通系统行照常计未读');
  assert.equal(trace.chats.length, 1, '普通系统行照常放行提醒');
});

test('新增需求同门：req 立案广播计绿点＋放行通知叮，不进红点', async () => {
  const { book, trace } = await freshBook();
  const alpha = book.rooms.get('alpha');
  alpha.conn.ev.onFrame(REQ_FRAME());
  assert.equal(alpha.workFresh, 1, '立案广播计一枚绿点');
  assert.equal(trace.works.length, 1, '通知叮与绿点同门放行');
  assert.equal(trace.works[0][0].type, Msg.Req, '钩子拿到的是需求帧');
  assert.equal(alpha.unread, 0, '需求不是对话，不进红签');

  // 事件行照进消息窗（与 task 族同形——stream 画「需求 · 新建」行）
  const last = alpha.messages[alpha.messages.length - 1];
  assert.equal(last?.frame?.type, Msg.Req, '需求帧入消息窗走事件行');
});

test('无快照的工单帧不进绿点（回执/拒执没有「变动」可言）', async () => {
  const { book, trace } = await freshBook();
  const alpha = book.rooms.get('alpha');
  alpha.conn.ev.onFrame({ type: Msg.Task, event: 'denied', ts: 1 });
  alpha.conn.ev.onFrame({ type: Msg.Req, event: 'denied', ts: 1 });
  assert.equal(alpha.workFresh, 0);
  assert.equal(trace.works.length, 0);
});

test('backfill（断线补窗/开页载史）是回放：不计绿点、不出通知叮', async () => {
  const { book, trace } = await freshBook();
  const alpha = book.rooms.get('alpha');
  alpha.conn.ev.onBackfill([TASK_FRAME(), REQ_FRAME()], true);
  assert.equal(alpha.workFresh, 0);
  assert.equal(trace.works.length, 0);
});

test('正看着这间房的办公室＝当面看见：门关（不进绿点不出叮），离开/后台再开', async () => {
  const { book, trace } = await freshBook();
  book.switchTo('alpha');
  book.setRoomVisible(true); // 人在办公室板块，看着 alpha 的任务纸打出来

  book.rooms.get('alpha').conn.ev.onFrame(TASK_FRAME());
  book.rooms.get('alpha').conn.ev.onFrame(REQ_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 0, '当面看见的变动不报信');
  assert.equal(trace.works.length, 0);

  book.setRoomVisible(false); // 去了别的板块——下一张纸看不见了
  book.rooms.get('alpha').conn.ev.onFrame(TASK_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 1, '离开办公室后照常绿点＋叮');
  assert.equal(trace.works.length, 1);

  // 页面后台不算看着：办公室板块虽在路由上，document.hidden 时不让门
  globalThis.document.hidden = true;
  book.rooms.get('alpha').conn.ev.onFrame(REQ_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 2, '后台页的工单变动照报');
  globalThis.document.hidden = false;
});

test('看办公室只豁免当前房：别间房的工单变动照常绿点＋叮', async () => {
  const { book, trace } = await freshBook(); // current=default
  book.setRoomVisible(true);
  book.rooms.get('alpha').conn.ev.onFrame(TASK_FRAME());
  book.rooms.get('alpha').conn.ev.onFrame(REQ_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 2, '别家房的纸看不见，照报');
  assert.equal(trace.works.length, 2);
});

test('进门即看：落到办公室板块当场清当前房 workFresh（别房不动）', async () => {
  const { book } = await freshBook();
  book.rooms.get('alpha').conn.ev.onFrame(TASK_FRAME());
  book.rooms.get(LobbyKey).conn.ev.onFrame(REQ_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 1);

  book.switchTo('alpha');
  book.setRoomVisible(true); // 进门
  assert.equal(book.rooms.get('alpha').workFresh, 0, '当前房绿点进门即清');
  assert.equal(book.rooms.get(LobbyKey).workFresh, 1, '别房没看见，绿点留着');
});

test('看着办公室换房＝进新屋看见纸：switchTo 清目标房；不在办公室则留', async () => {
  const { book } = await freshBook();
  book.rooms.get('alpha').conn.ev.onFrame(REQ_FRAME());
  assert.equal(book.rooms.get('alpha').workFresh, 1);

  book.switchTo('alpha'); // roomVisible=false：人还在别的板块
  assert.equal(book.rooms.get('alpha').workFresh, 1, '没见过纸，绿点诚实留着');

  book.switchTo(LobbyKey);
  book.setRoomVisible(true);
  book.switchTo('alpha'); // 正看着办公室换房
  assert.equal(book.rooms.get('alpha').workFresh, 0, '当面换进来，进门即看');
});

test('switcher 渲染：绿点挂别家房、当前房豁免、与红签并存各说各话', async () => {
  const { book } = await freshBook();
  const alpha = book.rooms.get('alpha');
  const lobby = book.rooms.get(LobbyKey);
  alpha.conn.ev.onFrame(REQ_FRAME());    // 别家房：绿点（需求也走绿语言）
  alpha.conn.ev.onFrame(SAY_FRAME);      // ＋红签
  lobby.conn.ev.onFrame(TASK_FRAME());   // 当前房：只进导航钉，房行豁免
  assert.equal(lobby.workFresh, 1, '当前房的绿点真源在（喂导航钉）');

  const nav = { innerHTML: '', children: [], addEventListener() {}, appendChild(c) { this.children.push(c); } };
  const realCreate = globalThis.document.createElement;
  globalThis.document.createElement = () => ({ dataset: {}, style: {} });
  try {
    new SwitcherView(nav, book).render();
  } finally {
    globalThis.document.createElement = realCreate;
  }
  const rowOf = (key) => nav.children.find((c) => c.dataset.room === key);
  const alphaHtml = rowOf('alpha').innerHTML;
  assert.ok(alphaHtml.includes('room-taskdot'), 'alpha 房行有绿点');
  assert.ok(alphaHtml.includes('room-badge'), 'alpha 房行红签并存');
  assert.ok(!rowOf(LobbyKey).innerHTML.includes('room-taskdot'), '当前房行不挂绿点（导航钉替它报信）');

  // roomList 行数据带 workFresh——视图只认这一列，别走私有房记录
  const row = book.roomList().find((r) => r.key === 'alpha');
  assert.equal(row.workFresh, 1, 'roomList 携带绿点真源');
});

test('进门落未读的锚：清账前摘未读尾首（对话行才数，工单/聚合不入红账）', async () => {
  const { book } = await freshBook();
  const alpha = book.rooms.get('alpha');
  const feed = (f) => alpha.conn.ev.onFrame(f);
  feed({ type: Msg.Say, from: '小牛', text: '未读一', ts: 1 });
  feed(TASK_FRAME()); // 工单行：绿账的事，不占红位
  feed({ type: Msg.Say, from: '小鹿', text: '未读二', ts: 2 });
  feed({ type: Msg.System, from: 'x', event: Event.Agg, text: '别房汇总', ts: 3 });
  feed({ type: Msg.Say, from: '小马', text: '未读三', ts: 4 });
  assert.equal(alpha.unread, 3, '三条对话行计三（工单/聚合不入红账）');
  assert.equal(alpha.workFresh, 1, '工单行照进绿账');

  book.setChatVisible(true); // 人在对话面（看着大厅）
  book.switchTo('alpha'); // 进门：清账前摘锚递给流（飞书式进门落未读）
  assert.equal(alpha.unread, 0, '进门即读（清账口径不破）');
  assert.equal(alpha.entryMark?.frame?.text, '未读一', '锚＝未读尾巴的第一条');
});

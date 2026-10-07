// navbadge.test.js — 徽标一信一挂（牛马对话导航红点只替当前房数数）：
// 别家房的动静由它自己的房行红签报信（switcher），导航徽标不再全房
// 合计——人在 A 项目时 B 项目 @我，红点只落 B 的房行上；点进 B（不在
// 对话面换房，未读诚实留着）牛马对话的导航徽标才有数可显。与房行的
// 「当前房不挂红签（导航徽标替它报信）」是同一纪律的两侧：谁报信唯谁
// 报信。
//
// 驱动方式与 notify.test.js 同款：RoomBook 直构、拨号置空、从
// room.conn.ev.onFrame 喂帧走真实 #intake 入口。app.js 是带顶层副作用
// 的入口（import 即拨 dev SSE 热重载），navBadge 渲染函数不可导入——
// 对它的取数口径走源码断言（usage.test.js 对 app.js 同款先例）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

// DOM/网络假面（构造与驱动期的全部触点；导入期无人碰它们）
globalThis.document = { hidden: false };
globalThis.fetch = (url) => {
  if (String(url).endsWith('/projects')) {
    return Promise.resolve({
      ok: true, status: 200,
      headers: { get: () => null },
      json: async () => [
        { key: 'alpha', name: '项目甲', status: 'active' },
        { key: 'beta', name: '项目乙', status: 'active' },
      ],
    });
  }
  // 水合端点一律拒——调用方各自带 .catch 静默降级
  return Promise.reject(new Error('stub: no hydrate'));
};

const { RoomBook } = await import('./rooms.js');
const { ObserverConn } = await import('../wire/conn.js');
const { LobbyKey, Msg } = await import('../wire/wire.js');

// 拨号置空：连接对象只当帧入口的载体，绝不真连
ObserverConn.prototype.start = function () {};

async function freshBook() {
  const book = new RoomBook({
    onChange: () => {}, onNotice: () => {}, onEvent: () => {},
    onChatLine: () => {}, onWorkEvent: () => {},
  });
  book.owner = { name: '房主', role: 'owner' }; // @我 判定要认得出名字
  await book.refreshRooms(); // default（大厅）＋ 项目甲/乙 三间落座
  return book;
}

// B 房小鹿点名房主的一句（mentions 由服务端解析）
const AT_SAY = { type: Msg.Say, from: '小鹿', text: '@房主 在吗', mentions: ['房主'], ts: 2 };

test('一信一挂：人在 A 房，B 房来信（含@我）——徽标口径读当前房为 0，红点只在 B 房行', async () => {
  const book = await freshBook();
  book.switchTo('alpha'); // 人在项目甲，且不在对话板块
  book.rooms.get('beta').conn.ev.onFrame(AT_SAY);
  book.rooms.get('beta').conn.ev.onFrame({ type: Msg.Say, from: '小鹿', text: '又一句', ts: 3 });

  // navBadge 的取数口径（app.js）：currentRoom().unread / .atMe
  const cur = book.currentRoom();
  assert.equal(cur.key, 'alpha');
  assert.equal(cur.unread, 0, '当前房没来信——牛马对话导航徽标不亮');
  assert.equal(cur.atMe, false, 'B 房的 @我 不渗进导航徽标');

  const betaRow = book.roomList().find((r) => r.key === 'beta');
  assert.equal(betaRow.unread, 2, 'B 房行红签照报（项目那边有红点）');
  assert.equal(betaRow.atMe, true, 'B 房行「有人@我」小签照报');
});

test('点到对应项目才亮：非对话面换房不清未读——切到 B 后徽标口径亮起，房行红签豁免', async () => {
  const book = await freshBook();
  book.switchTo('alpha');
  book.rooms.get('beta').conn.ev.onFrame(AT_SAY);

  book.switchTo('beta'); // chatVisible=false：人还没来看流，未读随房携带
  const cur = book.currentRoom();
  assert.equal(cur.unread, 1, '点进对应项目，导航徽标此时才有数可显');
  assert.equal(cur.atMe, true, '@我 标同样只在当前房时报');

  const betaRow = book.roomList().find((r) => r.key === 'beta');
  assert.equal(betaRow.current, true);
  assert.equal(betaRow.unread, 1, '真源仍在——房行渲染时 current 豁免不画红签（同一纪律的另一侧）');

  // 落到对话面（进面对读）：当前房未读清零，徽标随之熄灭
  book.setChatVisible(true);
  assert.equal(book.currentRoom().unread, 0, '回对话面看见即读，徽标熄灭');
  assert.equal(book.currentRoom().atMe, false);
});

test('app.js navBadge 取数口径＝当前房（源码断言）：全房合计已退场', async () => {
  const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'app.js'), 'utf8');
  assert.match(src, /const room = book\.currentRoom\(\);\s*\n\s*const n = room\?\.unread \|\| 0;/,
    'navBadge 读 currentRoom().unread——只替当前房数数');
  assert.ok(!src.includes('book.totalUnread'), '未读不再全房合计（别家房由房行红签报信）');
  assert.ok(!src.includes('book.anyAtMe'), '@我 前缀同样只看当前房');
});

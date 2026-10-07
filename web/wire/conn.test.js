// conn.test.js — 观察者连接的心跳喂狗契约：服务端每拍 SendTo 一帧
// {type:"ping"}（server/session.go serveObserver 的心跳腿），conn.js 的
// 60s 入站看门狗靠它认「连接还活着」——浏览器对 JS 隐藏协议级
// ping/pong，修掉这条前安静房间每分钟被前端自己掐断一次（聊天流里
// 的「断线补窗」分隔线就是它的脚印）。两条纪律：
//   1. ping 帧喂狗但绝不外漏（onFrame/onBackfill 一帧不见）；
//   2. 看门狗本身没有被拆——真断粮 60s 照常掐线进退避重连。

import test from 'node:test';
import assert from 'node:assert/strict';

// 网络假面（wsURL 读 location；backfill 的 getHistory 走 fetch）
globalThis.location = { protocol: 'http:', host: '127.0.0.1:7777' };
globalThis.fetch = (url) => Promise.resolve({
  ok: true, status: 200,
  headers: { get: () => null },
  json: async () => ({ messages: [], has_more: false, next_since: 0 }),
});

class FakeWebSocket {
  static last;
  constructor(url) {
    this.url = url;
    this.closed = false;
    this.sent = [];
    FakeWebSocket.last = this;
  }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() {
    if (this.closed) return;
    this.closed = true;
    this.onclose?.();
  }
}
globalThis.WebSocket = FakeWebSocket;

const { ObserverConn } = await import('./conn.js');

/** 驱到拨号在线：hello 已发、welcome 已消化、backfill 已收口（gating 落回 false）。 */
async function online(t) {
  const seen = { frames: [], states: [], backfills: [] };
  const conn = new ObserverConn('default', {
    onFrame: (f) => seen.frames.push(f),
    onBackfill: (list, initial) => seen.backfills.push({ list, initial }),
    onState: (mode) => seen.states.push(mode),
    onNotice: () => {},
  });
  // start 前先启用假时钟：拨号即装真 60s 看门狗，事后启用会留下一颗没人清的真定时器吊住进程。
  t.mock.timers.enable({ apis: ['setTimeout'] });
  conn.start();
  const sock = FakeWebSocket.last;
  sock.onopen();
  assert.equal(sock.sent[0].observer, true, '观察者拨号应带 observer 标记');
  sock.onmessage({ data: JSON.stringify({ type: 'welcome', members: [] }) });
  await new Promise((r) => setImmediate(r)); // backfill 收口
  return { conn, sock, seen };
}

test('ping 帧喂狗：安静房两个心跳周期不断线、帧不外漏', async (t) => {
  const { conn, sock, seen } = await online(t);
  // 5 拍 × 25s＝125s 全程零真实消息：看门狗每次都在到期前被重置
  for (let i = 0; i < 5; i++) {
    t.mock.timers.tick(25_000);
    sock.onmessage({ data: JSON.stringify({ type: 'ping', ts: Date.now() }) });
  }
  assert.equal(sock.closed, false, '安静房不得被看门狗掐线');
  assert.equal(seen.frames.filter((f) => f.type === 'ping').length, 0,
    'ping 帧不得流出到 onFrame');
  conn.stop();
});

test('看门狗仍在：真断粮 60s 照常掐线进重连', async (t) => {
  const { conn, sock, seen } = await online(t);
  t.mock.timers.tick(60_001);
  assert.equal(sock.closed, true, '无入站帧 60s 应强制断开');
  assert.ok(seen.states.includes('reconnecting'), '断后应进入重连态');
  conn.stop();
});

// --- r_19 治理修订：访客 token 只在 /view 分享页的拨号里带 -----------------
// 房主自己的窗口永远不带——服务端只认带 token 的拨号为访客（限流/TTL）。
// 事故回归：修订前服务端对所有 observer 拨号无差别计数，房主多开几个
// 窗口就把每房 5 个名额占满，自己被「当前访客较多」锁在门外（仅缓存）。

test('访客 token 随 hello 带；房主拨号不带', async (t) => {
  const mk = (visitor) => {
    const c = new ObserverConn('default', {
      onFrame: () => {}, onBackfill: () => {}, onState: () => {}, onNotice: () => {},
    }, visitor);
    c.start();
    const sock = FakeWebSocket.last;
    sock.onopen();
    return { c, sock };
  };
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const v = mk('tok-1');
  assert.equal(v.sock.sent[0].visitor, 'tok-1', '访客拨号应带 token');
  assert.equal(v.sock.sent[0].observer, true, '观察者标记照带');
  v.c.stop();
  const owner = mk();
  assert.ok(!('visitor' in owner.sock.sent[0]), '房主拨号不得带 visitor 字段');
  owner.c.stop();
});

test('死链落轨：error 404（token 已废）停摆不再重连、落 offline', async (t) => {
  const states = [];
  const conn = new ObserverConn('default', {
    onFrame: () => {}, onBackfill: () => {}, onState: (m) => states.push(m), onNotice: () => {},
  }, 'tok-1');
  t.mock.timers.enable({ apis: ['setTimeout'] });
  conn.start();
  const sock = FakeWebSocket.last;
  sock.onopen();
  sock.onmessage({ data: JSON.stringify({ type: 'error', text: '链接已失效', status: 404 }) });
  sock.onclose(); // 服务端发完错误帧即收线
  assert.ok(states.includes('offline'), '死链应落 offline');
  t.mock.timers.tick(35_000); // 推过整个退避序列（帽 30s）
  assert.equal(FakeWebSocket.last, sock, '死链不得再拨');
  conn.stop();
});

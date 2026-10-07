// streamnav.test.js — 流导航（新消息浮标/「以下是新消息」界线/回原位
// 浮标/窗帽裁头锚定）的契约面：从第一性原理钉住三件事——①提示只指
// 「内容相对视口的方向」：新消息只会长在尾部，计数浮标永远住底部且带
// ↓；②跳转必可逆：程序跳转（发言拉底/点浮标跳未读/引用跳转）先记来
// 路——来路在上方时流顶驻「↑ 回到刚才看的位置」（飞书式返回原位置），
// 用户手势踩回来路附近（240px 门）浮标自会收；③未读有界线（微信式）
// ：读历史时未见批次第一条前压「以下是新消息」，点浮标是飞书式跳到
// 未读——送到线前落位往下补读，不跳过未读直落底（线已读过/锚被窗帽
// 匀走才落底）；进门（switch/回板块）book 递锚则落在未读处；到尾（读
// 齐）才连同计数一起收，'rooms' 保读位整流重画后界线随锚行复位。
// 走 StreamView 的公开面（scrolled/ownSent/goBack/onChange/pillClick/
// entryLand）——真监听器与测试同一道门；trimPlan 是纯函数直测。
import test from 'node:test';
import assert from 'node:assert';
import { dom } from '../room/testdom.js';

dom();

const { StreamView, trimPlan } = await import('./stream.js');
const { Msg, MESSAGE_CAP } = await import('../wire/wire.js');

// 布景：一间房 + 真实形状的 El 容器与三枚浮标钮。几何（scrollHeight/
// clientHeight）由用例自设——El 没有布局，数是我们说了算的确定值；
// querySelector 按实例改写为「空容器」口径（stub 默认恒返新节点，
// #appendNew 的空态守卫会被它骗成整流重画）。
function stage(msgs = [], owner = '房主') {
  const room = {
    key: 'r', name: '项目房', members: new Map(), messages: msgs,
    questions: new Map(), acks: new Map(), reacts: new Map(),
    reads: new Map(), queues: new Map(), readsLoaded: false, readSince: 0,
    notice: null,
  };
  const book = { current: 'r', rooms: new Map([['r', room]]), currentRoom: () => room };
  const container = document.createElement('div');
  container.querySelector = () => null; // 空容器口径（空态/公告条都不在）
  container.querySelectorAll = () => [];
  const pill = document.createElement('button');
  const jump = document.createElement('button');
  const back = document.createElement('button');
  const v = new StreamView(container, book, owner, { pill, jump, back });
  return { v, room, container, pill, jump, back };
}

const say = (from, text, extra = {}) => ({
  type: Msg.Say, from, text, ts: 1700000000 + Math.random(), seq: Math.floor(Math.random() * 1e6), ...extra,
});

const dividerIn = (container) =>
  container.children.find((el) => String(el.className || '').includes('unread-divider')) || null;

const geometry = (container, { top = 0, height = 3000, view = 500 }) => {
  container.scrollTop = top;
  container.scrollHeight = height;
  container.clientHeight = view;
};

function deliver(v, room, frames) {
  for (const f of frames) {
    room.messages.push({ frame: f });
    while (room.messages.length > MESSAGE_CAP) room.messages.shift();
    v.onChange('r', 'messages');
  }
}

test('读历史时来信：底部计数浮标亮（带 ↓），第一条未读前压「以下是新消息」界线', () => {
  const seed = Array.from({ length: 3 }, (_, i) => say('小马', `旧话 ${i}`));
  const { v, room, container, pill } = stage(seed.map((f) => ({ frame: f })));
  v.onChange('r', 'switch');
  geometry(container, { top: 400 }); // 读历史：视口悬在半山
  v.scrolled(400); // 上拨一下——脱钉（此后新内容只涨浮标不拉底）
  assert.equal(v.pinned, false, '上拨脱钉');

  deliver(v, room, [say('小牛', '新活来了'), say('小鹿', '接上')]);
  assert.equal(v.pillCount, 2, '两条新消息计数');
  assert.equal(pill.hidden, false, '底部浮标亮');
  assert.ok(pill.textContent.includes('2'), '计数上浮标');
  assert.ok(pill.textContent.includes('↓'), '微信式向下箭头——新消息永远在尾');
  const div = dividerIn(container);
  assert.ok(div, '界线在流里');
  assert.ok(String(div.textContent).includes('以下是新消息'), '界线文案（微信同款）');
  // 界线压在第一条未读前：其后紧跟「新活来了」那行
  const idx = container.children.indexOf(div);
  assert.ok(container.children[idx + 1]._frame?.text === '新活来了', '界线下面就是第一条未读');
});

test('有人@我染红前缀：浮标先报「有人@我 ·」再报条数', () => {
  const { v, room, pill } = stage();
  v.onChange('r', 'switch');
  v.pinned = false;
  deliver(v, room, [say('小牛', '@房主 看一眼', { mentions: ['房主'] })]);
  assert.equal(v.pillAtMe, true, '@我记账');
  assert.ok(pill.textContent.includes('有人@我'), '浮标带 @我 前缀');
});

test('滚回尾部即读齐：计数与界线一起收（界线不是常驻 bookmarks，读齐就撤）', () => {
  const { v, room, container, pill } = stage();
  v.onChange('r', 'switch');
  v.pinned = false;
  deliver(v, room, [say('小牛', '新话')]);
  assert.equal(pill.hidden, false, '先亮着');
  geometry(container, { top: 2600, height: 3000, view: 500 }); // 距底 -100 → 到尾
  v.scrolled(2600);
  assert.equal(v.pillCount, 0, '计数清零');
  assert.equal(pill.hidden, true, '浮标收');
  assert.equal(dividerIn(container), null, '界线收');
  assert.equal(v.pinned, true, '近底回钉');
});

test('跳转必可逆（一）：读历史时发言拉底——流顶驻「↑ 回到刚才看的位置」，点它送回读位', () => {
  const { v, container, back } = stage();
  v.onChange('r', 'switch');
  geometry(container, { top: 1200 });
  v.pinned = false;
  v.ownSent(); // 发言＝程序拉底（先记来路）
  assert.equal(v.backTop, 1200, '来路已记');
  assert.equal(container.scrollTop, 3000, '视口已送到底');
  assert.equal(back.hidden, false, '流顶浮标亮');
  assert.ok(back.textContent.includes('回到刚才看的位置'), '浮标文案');
  assert.ok(back.textContent.includes('↑'), '向上箭头——来路在上方');

  v.goBack();
  assert.equal(container.scrollTop, 1200, '送回来路');
  assert.equal(v.backTop, null, '一级回撤：回到即清锚');
  assert.equal(back.hidden, true, '浮标收');
});

test('跳转必可逆（二）：无新消息、来路在下方时，底部浮标翻「回到刚才看的位置 ↓」档', () => {
  const { v, container, pill, back } = stage();
  v.onChange('r', 'switch');
  geometry(container, { top: 2400 });
  v.pinned = false;
  v.backTop = 2400; // 引用跳转的等价记账：来路在当前视口下方
  geometry(container, { top: 200 });
  v.scrolled(200); // 跳上去后的一次滚动同步
  assert.equal(pill.hidden, false, '底部浮标亮（回原位档）');
  assert.ok(pill.textContent.includes('回到刚才看的位置'), '翻档文案');
  assert.ok(pill.textContent.includes('↓'), '向下箭头——来路在下方');
  assert.equal(back.hidden, true, '流顶不掺和：上方没有来路');
  // 计数优先于回原位：来信后底部让位给「N 条新消息 ↓」
  const { v: v2, room: r2, container: c2, pill: p2 } = stage();
  v2.onChange('r', 'switch');
  geometry(c2, { top: 2400 });
  v2.pinned = false;
  v2.backTop = 2400;
  geometry(c2, { top: 200 });
  deliver(v2, r2, [say('小牛', '插一条')]);
  assert.ok(p2.textContent.includes('新消息'), '计数档优先');
});

test('跳转必可逆（三）：手势自己踩回来路附近（240px 门内），浮标自会收——不抢手势的路', () => {
  const { v, container, back } = stage();
  v.onChange('r', 'switch');
  v.pinned = false;
  v.backTop = 1000;
  geometry(container, { top: 2400 });
  v.scrolled(2400); // 远离来路——浮标驻
  assert.equal(back.hidden, false);
  geometry(container, { top: 1100 });
  v.scrolled(1100); // 手势滚回来路 100px 内
  assert.equal(v.backTop, null, '踩回来路即清锚');
  assert.equal(back.hidden, true, '浮标收');
});

test('「以下是新消息」界线扛住保读位整流重画（rooms hint）：随锚行复位、计数不清', () => {
  const seed = Array.from({ length: 3 }, (_, i) => say('小马', `旧话 ${i}`));
  const { v, room, container, pill } = stage(seed.map((f) => ({ frame: f })));
  v.onChange('r', 'switch');
  geometry(container, { top: 300 });
  v.scrolled(300);
  deliver(v, room, [say('小牛', '新活')]);
  assert.ok(dividerIn(container), '界线在');
  v.onChange(null, 'rooms'); // 房列表变动：内容等价重画，保读位
  const div = dividerIn(container);
  assert.ok(div, '重画后界线复位');
  assert.ok(container.children[container.children.indexOf(div) + 1]._frame?.text === '新活', '仍压在第一条未读前');
  assert.equal(v.pillCount, 1, '计数不清');
  assert.equal(pill.hidden, false, '浮标不灭');
});

test('换房整流：来路与未读一起清——旧房欠的路不带到新房', () => {
  const { v, room, container, pill, back } = stage();
  v.onChange('r', 'switch');
  geometry(container, { top: 1200 });
  v.pinned = false;
  deliver(v, room, [say('小牛', '新话')]);
  v.backTop = 1200;
  v.onChange('r', 'switch');
  assert.equal(v.pillCount, 0, '计数清');
  assert.equal(v.backTop, null, '来路清');
  assert.equal(pill.hidden, true, '底部浮标收');
  assert.equal(back.hidden, true, '顶部浮标收');
  assert.equal(dividerIn(container), null, '界线收');
});

test('批次头是断线补窗记录（无行）：界线锚顺延到第一条真行，不落空', () => {
  const { v, room, container, pill } = stage();
  v.onChange('r', 'switch');
  v.pinned = false;
  room.messages.push({ divider: '断线补窗 2 条', ts: 1700000000 });
  v.onChange('r', 'messages');
  room.messages.push({ frame: say('小牛', '补窗后的第一条') });
  v.onChange('r', 'messages');
  const div = dividerIn(container);
  assert.ok(div, '界线在（锚没有落空）');
  assert.ok(container.children[container.children.indexOf(div) + 1]._frame?.text === '补窗后的第一条',
    '压在第一条真行前');
  assert.equal(v.pillCount, 1, '补窗记录不计数（无行），真行计 1');
  assert.equal(pill.hidden, false);
});

// 系统行也是对话行（rooms 红门与 #entryMark 都计入），锚会落在系统行
// 上——行不背帧的年代点浮标失锚落底、进门落不了未读、界线随重画消失。
const sys = (text, extra = {}) => ({ type: Msg.System, event: '', text, ts: 1700000000 + Math.random(), ...extra });

test('未读锚是系统行（暂停/重编译等通知打头）：点浮标照跳界线前，不失锚落底', () => {
  const seed = Array.from({ length: 3 }, (_, i) => say('小马', `旧话 ${i}`));
  const { v, room, container, pill } = stage(seed.map((f) => ({ frame: f })));
  v.onChange('r', 'switch');
  geometry(container, { top: 400 });
  v.scrolled(400);
  deliver(v, room, [sys('房间已暂停'), say('小牛', '新活来了')]);
  assert.equal(v.pillCount, 2, '系统行计入未读（与红门同一道门）');
  assert.ok(dividerIn(container), '界线压在系统行前');
  container.getBoundingClientRect = () => ({ top: 0 });
  container.children.find((el) => el.textContent === '房间已暂停')
    .getBoundingClientRect = () => ({ top: 1600 });
  v.pillClick();
  assert.equal(container.scrollTop, 400 + 1600 - 48, '送到界线前落位（失锚的旧病是落底 3000）');
  assert.equal(v.pinned, false, '半山落位不钉');
  assert.equal(v.pillCount, 0, '送到即应答');
  assert.ok(dividerIn(container), '界线留作读位');
  assert.ok(container.children[container.children.indexOf(dividerIn(container)) + 1].textContent === '房间已暂停',
    '线仍压系统行前');
  assert.equal(pill.hidden, true, '浮标收');
});

test('进门锚是系统行：entryLand 照落未读处（rooms.#entryMark 摘得到系统行）', () => {
  const seed = Array.from({ length: 6 }, (_, i) => say('小马', `旧话 ${i}`));
  seed.push(sys('房间已恢复'), say('小牛', '未读一'), say('小鹿', '未读二'));
  const recs = seed.map((f) => ({ frame: f }));
  const { v, room, container } = stage(recs);
  geometry(container, { height: 3000, view: 500 });
  container.getBoundingClientRect = () => ({ top: 0 });
  v.onChange('r', 'switch'); // 先整流落底，再递锚落位（switch 消费锚的既有接线）
  container.children.find((el) => el.textContent === '房间已恢复')
    .getBoundingClientRect = () => ({ top: -1200 });
  room.entryMark = recs[6];
  v.entryLand();
  assert.equal(container.scrollTop, 3000 - 1200 - 48, '落在界线前（失锚的旧病是留在底）');
  assert.ok(dividerIn(container), '进门即压界线');
  assert.ok(container.children[container.children.indexOf(dividerIn(container)) + 1].textContent === '房间已恢复',
    '线压锚行（系统行）前');
  assert.equal(room.entryMark, null, '锚一次性');
});

test('锚是系统行时 rooms 保读位整流重画：界线随锚行复位、不消失', () => {
  const seed = Array.from({ length: 3 }, (_, i) => say('小马', `旧话 ${i}`));
  const { v, room, container, pill } = stage(seed.map((f) => ({ frame: f })));
  v.onChange('r', 'switch');
  geometry(container, { top: 300 });
  v.scrolled(300);
  deliver(v, room, [sys('房间已暂停'), say('小牛', '新活')]);
  assert.ok(dividerIn(container), '界线在');
  v.onChange(null, 'rooms');
  const div = dividerIn(container);
  assert.ok(div, '重画后界线复位（系统行锚的旧病是界线消失）');
  assert.ok(container.children[container.children.indexOf(div) + 1].textContent === '房间已暂停',
    '仍压在锚行前');
  assert.equal(v.pillCount, 2, '计数不清');
  assert.equal(pill.hidden, false, '浮标不灭');
});

test('程序送位在途不清来路（flight 结航才恢复到达门）——平滑飞行起飞慢也不会误清锚', () => {
  const { v, container } = stage();
  v.onChange('r', 'switch');
  v.pinned = false;
  v.backTop = 1200;
  v.flightTop = 2600; // ownSent/点浮标后的平滑送位在途（目的地在底）
  v.flightAt = performance.now();
  geometry(container, { top: 1200 });
  v.scrolled(1200); // 起飞期的 scroll 事件——视口尚未离开原位
  assert.equal(v.backTop, 1200, '在途不清锚');
  geometry(container, { top: 1300 });
  v.scrolled(1300); // 飞行中段（还在 240px 门里，但航程未结）
  assert.equal(v.backTop, 1200, '飞行中段仍在门里不清锚');
  v.flightTop = null; // 到达结航（或 1.5s 超时强结）
  geometry(container, { top: 1250 });
  v.scrolled(1250);
  assert.equal(v.backTop, null, '结航后手势踩回来路即清');
});

test('点浮标＝飞书式跳到未读：送到绿线前落位（不落底、不钉），来路记下指得回去', () => {
  const seed = Array.from({ length: 3 }, (_, i) => say('小马', `旧话 ${i}`));
  const { v, room, container, pill, back, jump } = stage(seed.map((f) => ({ frame: f })));
  v.onChange('r', 'switch');
  geometry(container, { top: 400 }); // 读历史：视口悬在半山
  v.scrolled(400);
  deliver(v, room, [say('小牛', '新活来了'), say('小鹿', '接上')]);
  assert.ok(dividerIn(container), '界线先在');
  // 几何假面：视口悬在 400，锚行（第一条未读）在视口下方 1600px 处
  container.getBoundingClientRect = () => ({ top: 0 });
  container.children.find((el) => el._frame?.text === '新活来了')
    .getBoundingClientRect = () => ({ top: 1600 });
  v.pillClick();
  assert.equal(container.scrollTop, 400 + 1600 - 48, '落位在界线前（线停视口上部）');
  assert.equal(v.pinned, false, '半山落位不回钉——新消息照旧只涨浮标');
  assert.equal(v.pillCount, 0, '送到即应答：计数收');
  assert.equal(pill.hidden, true, '浮标收');
  assert.ok(dividerIn(container), '界线留作读位（到尾读齐才收）');
  assert.ok(container.children[container.children.indexOf(dividerIn(container)) + 1]._frame?.text === '新活来了',
    '线仍压第一条未读前');
  assert.equal(back.hidden, false, '来路在上方：流顶驻回原位浮标');
  assert.equal(jump.hidden, false, '不在底：回底圆钮亮着');
  v.goBack();
  assert.equal(container.scrollTop, 400, '跳转必可逆：送回来路');
  assert.equal(back.hidden, true, '回到即收');
});

test('点浮标时线已被读过（锚在视口上方）：落底兜底——没读的只剩尾', () => {
  const { v, room, container, pill } = stage();
  v.onChange('r', 'switch');
  geometry(container, { top: 400 });
  v.scrolled(400);
  deliver(v, room, [say('小牛', '新话')]);
  // 顺着读到线以深（还没到尾）：锚行已在视口上方
  geometry(container, { top: 1200 });
  v.scrolled(1200);
  container.getBoundingClientRect = () => ({ top: 0 });
  container.children.find((el) => el._frame?.text === '新话')
    .getBoundingClientRect = () => ({ top: -300 });
  v.pillClick();
  assert.equal(container.scrollTop, 3000, '落底兜底');
  assert.equal(v.pinned, true, '落底回钉');
  assert.equal(dividerIn(container), null, '读齐清账：界线收');
  assert.equal(pill.hidden, true, '浮标收');
});

test('锚被窗帽匀走（计数在、锚不在）：点浮标回落底兜底', () => {
  const { v, room, container } = stage();
  v.onChange('r', 'switch');
  geometry(container, { top: 400 });
  v.scrolled(400);
  deliver(v, room, [say('小牛', '新话')]);
  v.unreadMark = null; // 锚丢了（窗帽匀走——计数不撤的既有口径）
  v.pillClick();
  assert.equal(container.scrollTop, 3000, '落底');
  assert.equal(v.pinned, true);
  assert.equal(v.pillCount, 0);
});

test('进门落未读：锚在视口上方也照落（进门豁免）——落在绿线前、不钉、锚一次性', () => {
  const seed = Array.from({ length: 6 }, (_, i) => say('小马', `旧话 ${i}`));
  seed.push(say('小牛', '未读一'), say('小鹿', '未读二'));
  const recs = seed.map((f) => ({ frame: f }));
  const { v, room, container, jump } = stage(recs);
  geometry(container, { height: 3000, view: 500 });
  v.onChange('r', 'switch'); // 无锚进门：落底（既有口径不破）
  assert.equal(container.scrollTop, 3000, '无锚进门：落底');
  // book 清账前摘好锚递来（rooms.js #entryMark 的产物）；落底出发的
  // 视口里锚行（内容位 800）在上方 2200px——进门落位豁免「锚在上方」
  room.entryMark = recs[6];
  container.getBoundingClientRect = () => ({ top: 0 });
  container.children.find((el) => el._frame?.text === '未读一')
    .getBoundingClientRect = () => ({ top: -2200 });
  v.entryLand();
  assert.equal(container.scrollTop, 3000 - 2200 - 48, '落位在界线前（线停视口上部）');
  assert.ok(dividerIn(container), '进门即压界线');
  assert.ok(container.children[container.children.indexOf(dividerIn(container)) + 1]._frame?.text === '未读一',
    '线压未读尾首前');
  assert.equal(v.pinned, false, '半山落位不钉');
  assert.equal(v.pillCount, 0, '送到即应答：计数从零起（新消息再亮）');
  assert.equal(room.entryMark, null, '锚一次性：落没落成都清');
  assert.equal(jump.hidden, false, '不在底：回底圆钮亮');
});

test('进门落未读的接线：switch 消费房上的锚（一次性），无锚整流不再落', () => {
  const recs = [say('小马', '旧话'), say('小牛', '未读一')].map((f) => ({ frame: f }));
  const { v, room } = stage(recs);
  room.entryMark = recs[1];
  v.onChange('r', 'switch');
  assert.equal(room.entryMark, null, 'switch 消费锚（流侧 entryLand）');
  v.onChange(null, 'rooms'); // 房列表整流：锚已清，不再落
  assert.equal(room.entryMark, null);
});

test('trimPlan（纯函数）：超帽从前头匀、无主续行顺带裁、续行外不多裁', () => {
  const mk = (cls, h = 40) => ({ className: cls, offsetHeight: h, removed: false,
    remove() { this.removed = true; } });
  const kids = [
    mk('line msg'), mk('line msg grouped'), mk('line msg grouped'),
    ...Array.from({ length: MESSAGE_CAP - 1 }, () => mk('line msg')),
    mk('stream-notice-bar'), // 公告条不数行
  ];
  const cut = trimPlan(kids, MESSAGE_CAP);
  assert.equal(cut.length, 3, '超帽 2 行＋末尾补裁的无主续行 1 行');
  assert.ok(cut.slice(0, 3).every((el) => String(el.className).includes('line')), '裁的都是行');
  assert.ok(!cut.includes(kids[kids.length - 1]), '公告条不裁');
  const few = [mk('line msg'), mk('line msg'), mk('stream-empty')];
  assert.equal(trimPlan(few, MESSAGE_CAP).length, 0, '未超帽不裁');
});

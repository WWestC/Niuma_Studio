// app.js — the workbench shell (v2 P1 skeleton → P3-d room view →
// CAP-M4 people board → P4-d project board → P6 pixel room board).
//
// Hash routing keeps the four boards (#/room #/people #/project
// #/chat, frontend 稿 §4.0); the pixel room board is the live face —
// the merged app's front door (v2 P6: the Ebiten window retired, the
// little people render in this window's canvas). P3-d wires the
// multi-room shape: a RoomBook opens one observer WS per active room
// (switching rooms is a view switch — no stream drops), the sidebar
// switcher lists 大厅＋项目办公室 with unread badges, the chat board adds
// the owner-identity composer (读走 observer 连接、写走房主面，§4.6-1). CAP-M4 adds the people board over the same
// book — reads via wire/api, management verbs via the lobby owner face
// — plus the global toast every board's write receipts land on. P4-d
// adds the project board (需求 / 任务台账 / 排期甘特＋plan 审阅) over
// the same book, speaking task/plan verbs through the VIEWED project's
// owner face, and the gantt's 「向编排者提要求」 bridge back into the
// chat composer. State lives in Go; this page only projects frames and
// speaks as the owner.

import { getPeople, getDoc, probeHTTP, httpAlive, postLang } from './wire/api.js';
import { Msg } from './wire/wire.js';
import { bindSfx } from './room/sfxport.js';
import { sfx as realSfx } from './ui/sound.js';

// r_09：房间侧声音端口装配（真 sfx 注入；测试环境不 bind 即静默）
bindSfx(realSfx);
import { t, langPref, langPicked, applyLangMeta, applyStaticI18n } from './ui/i18n.js';
import { esc, avatarText, groupAvatarText, memberColor, safeColor } from './ui/dom.js';
import { bootGate, bootNote, bootFail, dismissBoot } from './ui/boot.js';
import { NavStack } from './ui/navstack.js'; // 板块导航历史（ZCode 式 ←→）
import { initVisitor, visitorMode, visitorToken, initVisitorBadge } from './ui/visitor.js'; // t_192（r_19）
import './ui/breakpoint.js'; // 飞书响应式断点（html[data-bp]）＋XS 导航抽屉
import './ui/selectmenu.js'; // select 弹层接管——系统 NSMenu 深色玻璃盖控件（Select 篇补章）
import { RoomBook } from './chat/rooms.js';
import { SwitcherView } from './chat/switcher.js';
import { StreamView } from './chat/stream.js';
import { MembersView } from './chat/members.js';
import { Composer } from './chat/composer.js';
import { NoticePanel } from './chat/notice.js';
import { MsgPop } from './chat/msgpop.js';
import { TracePanel } from './chat/traceview.js';
import { UsagePanel } from './ui/usageview.js';
import { ProfileCard } from './ui/profilecard.js';
import { SettingsWin, prefs, applyTheme, announce, setPluginThemes } from './ui/settings.js';
import { openClockPop } from './ui/clockpop.js';
import { PeopleBoard } from './people/people.js';
import { ProjectBoard } from './project/project.js';
import { RoomView } from './room/roomview.js';
import { KbBoard } from './kb/kb.js';
import { AssistantBoard } from './assistant/assistant.js';
import { TermBoard } from './term/termboard.js'; // 终端总览（r_17）
import { FilesBoard } from './files/files.js'; // 文件板块（类 VSCode 资源管理器，跟房看工作区）
import { SpaceGate } from './ui/spacegate.js'; // 项目空间首启门（v2.12）：无项目必须立项
import { Toast } from './ui/toast.js';
import { createBus } from './ui/bus.js'; // 房间变化扇出总线（订阅制，替手写 onChange 链）
import { pluginBus, bootPlugins, pluginThemes } from './plugins/loader.js';
import { installExtLinks } from './ui/extlink.js'; // 外链跳默认浏览器（对话/公告/文档共面拦截）

const $ = (sel) => document.querySelector(sel);

const toast = new Toast();

// 外链拦截先装（document 捕获，早于一切正文面的监听）：壳内有
// openExternal 桥就把 .mlink 等可外开链接交默认浏览器，见 ui/extlink.js
installExtLinks();

// 主题落位（<head> 内联脚本已防闪地先落过一次）：这里再走一遍是给
// 运行时把同步做齐——原生标题栏 setChromeColor 与 PWA theme-color
// 跟着解析后的 --titlebar 走；跟随系统的换季监听在 settings.js 里
applyTheme();

// 语言落位（<head>/门闩内联脚本已先落过 lang）：标题与 <html lang>
// 兜底、静态 DOM 文本（data-i 标记族）翻译。给后端推语言只在手动选
// 过时发生（langPicked 门）——跟随系统的自动判定不写后端，否则任何英
// 文环境的页面（自动化浏览器、别的机器代开）一加载就把全工作室的后端
// 语言连同落盘捋走；手动选择者的窗口每次启动重推一遍，兼作自愈
// （fire-and-forget，失败静默——服务端回落默认中文）
applyLangMeta();
applyStaticI18n();
if (langPicked()) postLang(langPref()).catch(() => {});

// ---------- the room book and its views ----------

let peopleBoard = null;   // built after probeOwner — the book's taps guard on it
let projectBoard = null;
let roomView = null;      // the pixel room canvas board (P6)
let kbBoard = null;       // the blackboard board (P6 merge)
let assistantBoard = null; // the 小助手 board (usage advisor over the shared zcode bridge)
let noticePanel = null;   // the per-room group-announcement panel (飞书式群公告)
let msgpop = null;        // 新消息提醒＋提示音（系统通知横幅，微信/飞书式）
let tracePanel = null;    // 工作过程抽屉（v2.8）：牛马会话的思考/工具/子智能体时间线
let termBoard = null;     // 终端总览板（r_17）：全员会话的完整进出（输入/输出/反问）
let filesBoard = null;    // 文件板：当前房项目工作区的只读浏览（大厅=本仓）
let usagePanel = null;    // 耗粮统计抽屉：牛马员工 token 台账（总览→每轮明细）
let spaceGate = null;     // 项目空间首启门：无项目（大厅除外）时拦在门外（v2.12）

const chatSearch = $('#chat-search'); // 本办公室消息搜索框（过滤态喂给 stream）

// 房间变化扇出走订阅总线（ui/bus.js）：各视图只登记自己吃的 hint，
// 不再是 app.js 手接每一条 board×hint 线——加板加 hint 不动别人的线。
// 订阅顺序＝派发顺序（与旧手写扇出一致，见 bus.test.js 的顺序钉）。
const bus = createBus();

const book = new RoomBook({
  onChange: (key, hint) => {
    if (hint === 'switch') { // 换办公室即退搜索——搜索是办公室内一时的视窗
      chatSearch.value = '';
      stream.setFilter('');
    }
    bus.emit(key, hint);
  },
  // the pixel room's live feed: say/report frames pop bubbles on the
  // little people of the room being viewed; the same frames feed the
  // 形态 B plugins' chat.watch subscriptions (v3 — read-only sugar,
  // plugin callbacks can never throw into the room's own path).
  // backfill（载史/断线补窗的重放帧）必须透传——办公室效果与插件订阅
  // 都只吃直播帧（t_197：整窗历史同帧齐放＝「效果集中蹦出来」的根因）
  onLiveFrame: (f, key, backfill) => {
    roomView?.frame(f, key, backfill);
    pluginBus.emitChat(f, key, backfill);
  },
  // 新消息提醒（声音＋弹窗）：与未读徽标同一道门放行的对话行到了，
  // MsgPop 自带全部策略（自己的话不提醒、点名叮铃、后台转系统通知）
  onChatLine: (f, room, atMe) => msgpop?.feed(f, room, atMe),
  // 工单变动通知叮（声色同门）：与 workFresh 绿点同一道 if 放行（见
  // rooms.js #intake，任务与需求同门）——叮声与绿点永远成对出现；音色
  // 沿用任务落地轻叮（scene-ding，自带 2s 冷却窗兜连珠工单、提示音总
  // 闸与深夜静音）
  onWorkEvent: () => realSfx('scene-ding'),
  // 工作区变更推送（ws fs_dirty）：带项目键转交文件板——正浏览该项目
  // 且在屏才消费（文件板自己取舍），其余板块零动作。
  onFsDirty: (project) => filesBoard?.onFsDirty(project),
  onNotice: (text) => composer.note(text),
  // write-face receipts: kick/rank_set/archive system replies,
  // capability outcomes, denied task frames and plan outcomes — toast
  // them (§4.0's global layer) and let the boards resync off the change.
  onOwnerFrame: (f) => {
    if (f.type === Msg.System) {
      toast.show(f.text || '', /被拒|失败|denied|rejected|failed/.test(f.text || '') ? 'err' : 'ok');
      peopleBoard?.requestRefresh();
    } else if (f.type === Msg.SkillEvt || f.type === Msg.McpEvt) {
      toast.show(f.text || f.event || '', f.event === 'denied' ? 'err' : 'ok');
      peopleBoard?.requestRefresh();
    } else if (f.type === Msg.Task) {
      // One true-outcome toast per task verb (the plan/capability receipt
      // contract): the sender no longer toasts at send time — a green
      // 「已发出」next to the red denial was the double-popup bug. Every
      // task verb's receipt rides back on this face, denied or not.
      toast.show(f.text || f.event || '', f.event === 'denied' ? 'err' : 'ok');
      stream.taskOutcome?.(f); // the seatless task face never broadcasts — repaint from the private outcome
      projectBoard?.acceptTaskReceipt(f); // the create→attach chain (tasknew)
      projectBoard?.requestRefresh();
    } else if (f.type === Msg.Plan) {
      toast.show(f.text || f.event || '', f.event === 'denied' ? 'err' : 'ok');
      projectBoard?.requestRefresh();
    } else if (f.type === Msg.Kb) {
      kbBoard?.onKbReceipt(f); // doc writes: conflict carries current_rev
    } else if (f.type === Msg.Notice) {
      if (f.event === 'denied') noticePanel?.onDenied(f); // 公告保存被拒（rev 冲突保草稿重试）
    }
  },
  // reminder-only broadcasts from any room: the people and project
  // boards resync (agent/task/plan drift) without badging — and the
  // plugins' events.watch subscribers see the same read-only frames.
  onEvent: (f) => {
    peopleBoard?.requestRefresh();
    projectBoard?.requestRefresh();
    if (f?.type === Msg.Kb) kbBoard?.requestRefresh();
    pluginBus.emitEvent(f);
  },
});

const switcher = new SwitcherView($('#room-switch'), book);
const composer = new Composer({
  root: $('#composer'), input: $('#composer-input'), send: $('#composer-send'),
  pick: $('#mention-pick'), status: $('#composer-status'),
  count: $('#composer-count'),
  quote: $('#composer-quote'), quoteText: $('#composer-quote .q-text'),
  at: $('#composer-at'), emoji: $('#composer-emoji'), emojiPick: $('#emoji-pick'),
  outdent: $('#composer-outdent'), indent: $('#composer-indent'),
  image: $('#composer-image'), file: $('#composer-file'), thumbs: $('#composer-thumbs'),
}, book, {
  // 点输入井的引用条 → 与流内引用块同款跳回原消息（seq 在场时精确命中）
  jump: (from, snip, seq) => stream.jumpToMessage(from, snip, seq),
  // 发出消息即回底：发送是拉底手势的等价物——重新挂上贴底跟随并送
  // 到底，读历史时回了句话视口跟着自己的话走（飞书式）
  onSent: () => stream.ownSent(),
  // 输入井/引用条/图片条的自增高缩矮改流高：钉着就跟着到新底
  // （stream.relayout 的 composer 腿，与 paintWorkHint 同一口）
  onRelayout: () => stream.relayout(),
});
// 牛马真相汇聚（名片卡的数据源）：/kb/people 优先（presence/rank 是
// 它的权威，侧栏 30s 轮询缓存），退回当前房名册（role/color 仍真），
// 都没有 → null（卡片降级为纯色名＋「不在名册中」）。函数声明提升，
// 名片卡构造时即可引用（真正调用都在交互之后）。
function personOf(name) {
  const p = sideMembers.person(name);
  if (p) return p;
  const m = book.currentRoom()?.members.get(name);
  return m ? { ...m } : null;
}

// 共享牛马名片卡（头像篇＋气泡卡片篇）：对话流的头像/发送者名/@令牌/
// 引用者/事件行人名与侧栏成员行点开的是同一张卡——@ 提及把人带回对话
// 板落进输入井（别的板块点了先切板），牛马主页跨板直达牛马详情。
const profileCard = new ProfileCard({
  person: personOf,
  mention: (name) => {
    if ((location.hash.replace(/^#\/?/, '') || 'room') !== 'chat') location.hash = '#/chat';
    composer.insertMention(name);
  },
  open: (name) => { // 牛马主页 — cross-board, same as the room's taps
    peopleBoard?.select(name);
    location.hash = '#/people';
  },
  trace: (name) => { // 工作过程 — 当前房该成员的思考/工具时间线（v2.8）
    tracePanel?.open(book.current, name);
  },
});

// 设置中心（v2.9）：ident-row 齿轮弹的居中模态窗——左分类导航（通用/
// 外观/通知/智能/数据/连接/维护/关于）右内容面板，分类多了不再挤一张
// 小列表。连接真相、房间时间（点行弹表盘面板，面板浮在窗上、窗不收，
// clockpop.js）、工作室地址/版本（GET /）与立即生效的偏好开关都搬进
// 各分类面板。
const settingsWin = new SettingsWin({
  conn: () => connNow,
  toast: (text, kind) => toast.show(text, kind),
  onManual: () => { location.hash = '#/kb'; }, // 黑板默认落在说明书页签
  onChronicle: () => { location.hash = '#/kb/chronicle'; }, // t_153：工作室志页签（黑板 hash 路由的子路径）
  onGuide: () => { location.hash = '#/room'; setTimeout(() => roomView?.guide?.start(), 200); }, // t_159：回办公室重看引导（等板块落位）
  onClock: (row) => openClockPop(row),
  // 全智能模式开关的作用域＝当前房——大厅与项目房一视同仁（大厅本就
  // 是项目，旗舰编排者驻大厅，工作室级自治同样能跑）；只有还没有当前
  // 房（启动早期房册未落）才看项目板块的选中项，两头都没有就落大厅
  project: () => {
    const room = book.currentRoom();
    if (room && room.key) {
      return { key: room.key, name: room.name || room.key };
    }
    const cur = projectBoard?.currentProject();
    if (cur && cur.key) {
      return { key: cur.key, name: cur.name || cur.key };
    }
    return { key: 'default', name: t('Niuma_Studio') };
  },
});
$('#side-settings').addEventListener('click', (ev) => settingsWin.openFor(ev.currentTarget));
$('#side-usage').addEventListener('click', () => usagePanel.open()); // 耗粮统计（只读台账）
// 「正在处理…」hint — the dispatcher's member_work tell, pinned
// between the stream and the composer: a member whose turn is in
// flight shows here with a breathing dot and the elapsed seconds, so
// a long decomposition never reads as a hang. Pure client tick: the
// roster rows flip on member_work frames; this line re-derives from
// the roster once a second (it must — it counts). 名字即按钮：点谁
// 开谁的工作过程抽屉（v2.8）——「正在处理」的底下是什么，一眼可查。
const workHintEl = $('#work-hint');
const paintWorkHint = () => {
  const room = book.currentRoom();
  if (!room || !workHintEl) return;
  const names = [];
  let since = 0;
  for (const m of room.members.values()) {
    if (!m.working || m.name === stream.ownerName) continue;
    names.push(m.name);
    if (m.working_since && (!since || m.working_since < since)) since = m.working_since;
  }
  const wasHidden = workHintEl.hidden;
  if (!names.length) {
    if (!wasHidden) workHintEl.hidden = true;
  } else {
    names.sort((a, b) => a.localeCompare(b, 'zh'));
    const elapsed = since ? Math.max(0, Math.round(Date.now() / 1000 - since)) : 0;
    workHintEl.innerHTML =
      '<i class="wh-dot"></i>' +
      `<span>${names.map((n) => `<button type="button" class="wh-name" data-trace="${esc(n)}">${esc(n)}</button>`).join(t('、'))} ${t('正在处理…')}${since ? `（${t('已 {dur}', { dur: elapsed >= 60 ? t('{m} 分 {s} 秒', { m: Math.floor(elapsed / 60), s: elapsed % 60 }) : t('{s} 秒', { s: elapsed }) })}）` : ''}</span>`;
    workHintEl.hidden = false;
  }
  // 提示行收放会改流高：出现＝流被压矮、消失＝流回涨，钉着的视口都
  // 离底差一截（高度变化不触发 scroll），翻面即喊流补送底。
  if (workHintEl.hidden !== wasHidden) stream.relayout();
};
workHintEl.addEventListener('click', (ev) => {
  const n = ev.target.closest('[data-trace]');
  if (n) tracePanel?.open(book.current, n.dataset.trace);
});
setInterval(paintWorkHint, 1000);

const stream = new StreamView($('#stream'), book, null, { // owner name set post-probe
  pill: $('#stream-pill'),          // the scroll-up new-message jump
  jump: $('#stream-jump'),          // the round ↓ back-to-latest button
  back: $('#stream-back'),          // 流顶的回原位浮标：程序跳转把视口挪走后，来路在上方时驻留
  toast,
  quote: (f) => composer.quoteFrom(f), // the hover 引用 action aims the composer
  onSearchClear: () => { chatSearch.value = ''; },
  card: profileCard,                // 头像/@令牌/引用者 → 牛马名片卡
  // 点消息头的发送者名＝@TA（微信式点名字喊人）：与名片卡的 @ 提及 同
  // 一条落点——别的板块点了先切回对话板，再把 @名字 落进输入井
  mention: (name) => {
    if ((location.hash.replace(/^#\/?/, '') || 'room') !== 'chat') location.hash = '#/chat';
    composer.insertMention(name);
  },
  person: personOf,                 // 消息头岗位/等级小标的牛马真相（函数声明提升，调用都在渲染时）
  openNotice: () => noticePanel?.open(book.current), // 置顶公告条 → 群公告面板
  // 提案 submitted 卡的「去审批」：跳项目管理、切到 plan.project_key
  // 自己的项目并开审阅浮层——审批动作留在浮层里，聊天侧只负责把人送到
  openPlan: (f) => {
    location.hash = '#/project';
    projectBoard?.reviewPending(f?.plan?.project_key || '');
  },
  // 编制建议条的「加编制」：跳牛马管理的编制页签并按建议预填加岗
  // 浮层——确认（加入编制表）留在浮层里，聊天侧不越权直接写表
  openEstablish: (offer, projectKey) => {
    location.hash = '#/people';
    peopleBoard?.prefillEstablishment(projectKey, offer);
  },
  // 黑板写播报卡的「看全文」：拉整篇 doc（GET /kb/docs/{key}）开全文
  // 面板——roomview 的 showDoc，与头顶气泡点开同一张限高自滚卡；
  // 文档没了（被删/回滚掉）如实说一句，不静默装读到了
  openDoc: async (key) => {
    try {
      const doc = await getDoc(key);
      roomView?.showDoc(doc);
    } catch { toast.show(t('全文没取到——文档可能已删或不在黑板上了'), 'err'); }
  },
  // 消息快捷入口（拆解/排期/需求环节的跳转条）：审提案落待审浮层、
  // 看排期落排期甘特、看需求落需求页签——项目随 offer 落位，落地面
  // 自己解释现状（待审被新提案取代时浮层会说明）
  openJump: (offer, projectKey) => {
    location.hash = '#/project';
    projectBoard?.jumpTo(offer, projectKey);
  },
});
noticePanel = new NoticePanel(book, toast); // 飞书式群公告面板（房主发布/撤下，全员可读）
msgpop = new MsgPop({ book }); // 新消息提醒调度（声音＋系统通知）——onChatLine 的落点
tracePanel = new TracePanel(book, { // 工作过程抽屉（v2.8）——数据真源在 RoomBook 的 room.trace
  person: personOf,
  toast: (text, kind) => toast.show(text, kind),
});
usagePanel = new UsagePanel({ // 耗粮统计抽屉——真源 GET /usage 族，开着就拉不存第二份账
  toast: (text, kind) => toast.show(text, kind),
});
$('#chat-head-pin').addEventListener('click', () => noticePanel.open(book.current));
chatSearch.addEventListener('input', () => stream.setFilter(chatSearch.value));
chatSearch.addEventListener('keydown', (ev) => {
  if (ev.key !== 'Escape') return; // Esc＝清词退出搜索并还焦点给流
  chatSearch.value = '';
  stream.setFilter('');
  chatSearch.blur();
});
// the member roster renders once — the global sidebar rail (§4.1③);
// the chat board used to duplicate it as its own aside (removed as noise).
// 点一行 = 开牛马名片卡（@ 提及/牛马主页/工作过程都在卡里；@ 的落板
// 逻辑由 profileCard 的 mention 动作统一——别的板块点了先切回对话板）
const sideMembers = new MembersView($('#member-list'), book, {
  head: $('#member-head'),
  search: $('#member-search'), // 标题右侧的名字过滤框（Esc 清词）
  onPerson: (name, anchor) => profileCard.openFor(anchor, name),
  // 「干活中」chip 直开工作过程抽屉（v2.8）
  onTrace: (name) => tracePanel.open(book.current, name),
  // /kb/people 采集落地 → 对话流原地补消息头的岗位/等级小标（不重画整流）
  onPeople: () => stream.peopleSync(),
});

// ---------- room-change subscriptions (the bus fan-out) ----------
// 与旧手写扇出逐行同构、顺序一致；懒建面板照旧 ?. 守卫。

// 常驻四视图：每个 hint 都跟（null = 通配）。
bus.on(null, (key, hint) => {
  switcher.onChange(key, hint);
  stream.onChange(key, hint);
  sideMembers.onChange(key, hint);
  composer.onChange(key, hint); // 会话草稿按房存取
});
// 像素办公室画布：换房重钉场景，名册翻新只看当前房。
bus.on('switch', () => roomView?.setRoom(book.current));
bus.on('members', (key) => { if (key === book.current) roomView?.syncRoster(); });
// 头栏/连接灯/徽标族。
bus.on(['switch', 'rooms'], () => { connState(); navBadge(); chatHead(); });
bus.on('status', () => connState());
bus.on('members', (key) => { if (key === book.current) chatHead(); });
bus.on('unread', () => navBadge());
// 置顶条/📌/红标随公告走；他房的更新弹通知提醒框（右上角）。
bus.on('notice', (key) => {
  noticePanel?.onSync(key); chatHead();
  if (key !== book.current) {
    const room = book.rooms.get(key);
    if (room?.notice) announce({ // 设置卡通知总闸：横幅关了不出框也不出声
      kind: 'info', title: t('群公告已更新'),
      text: t('「{room}」发布了新公告', { room: room.name || key }),
      actions: [{ label: t('查看'), onClick: () => noticePanel?.open(key) }],
    });
  }
});
// 工作过程抽屉（v2.8）：直播帧重画＋成员工作戳跟帧。
bus.on('trace', (key, hint) => tracePanel?.onSync(key, hint));
bus.on('members', (key) => tracePanel?.onMembers(key));
// 终端总览板（r_17）：直播帧、名册跟画、换房钉新房。
bus.on('term', (key, hint) => termBoard?.onSync(key, hint));
bus.on('members', (key) => termBoard?.onSync(key, 'members'));
bus.on('switch', () => termBoard?.onSwitch());
// 换房跟走的四块板＋项目空间门：黑板/项目/文件/牛马管理与房册。
bus.on('switch', () => kbBoard?.onRoomSwitch());      // 文档/工作室志换 scope 重载
bus.on('switch', () => projectBoard?.onRoomSwitch()); // 正看着板块换房，查看项目落新房
bus.on('switch', () => filesBoard?.onRoomSwitch());   // 可见才跟房换工作区
bus.on('switch', () => peopleBoard?.onRoomSwitch());  // 花名册筛子/编制面落新房
bus.on('rooms', () => spaceGate?.sync()); // 清空重上墙、有项目落门

// ---------- sidebar chrome ----------

// 连接态的第四档「仅缓存」：WS 断线时读面可能还活着——断线期间每轮
// 重渲都发一次（节流）探测，探测失败会清零存活标记，橙灰随时落定。
let probeInFlight = false;

// 连接态 verdict（§4.1④ 的常驻行已删——连接态归对话区头栏小灯与设置
// 气泡卡两处）：四档 online/connecting·reconnecting/cache/offline 快照
// 进 connNow，设置卡开着就 sync 跟帧（「仅缓存」的解释挂 title）。
let connNow = { mode: 'connecting', label: t('连接中…'), roomName: '', note: '' };
function connState() {
  const room = book.currentRoom();
  const raw = room?.status || 'offline';
  let mode = raw;
  let label = {
    online: t('已连接'), connecting: t('连接中…'), reconnecting: t('重连中…'), offline: t('离线'),
  }[raw] || raw;
  if (raw === 'reconnecting' || raw === 'offline') {
    if (!probeInFlight) {
      probeInFlight = true;
      probeHTTP().finally(() => { probeInFlight = false; connState(); });
    }
    if (httpAlive(15_000)) {
      mode = 'cache';
      label = t('仅缓存 · HTTP 仍活');
    }
  }
  if (mode === 'reconnecting' && (room?.statusTries || 0) > 1) {
    label = t('重连中（第 {n} 次退避）…', { n: room.statusTries });
  }
  connNow = {
    mode, label,
    roomName: room?.name || '',
    note: mode === 'cache'
      ? t('实时流（WebSocket）已断，HTTP 读面仍活——页面数据停在最近一次同步')
      : '',
  };
  const ch = $('#chat-head-state'); // 对话区头栏的连接态小灯同帧
  ch.dataset.mode = mode;
  ch.textContent = label;
  settingsWin?.sync();
}

// 对话区头栏：房头像＋房名＋成员数（roster 权威，未欢迎时退回名册）
function chatHead() {
  const room = book.currentRoom();
  const avatar = $('#chat-head-avatar');
  avatar.textContent = room ? groupAvatarText(room.name) : '？';
  avatar.style.background = room ? safeColor(memberColor(room.key)) : '#8a9186';
  $('#chat-head-name').textContent = room ? room.name : '—';
  $('#chat-search').placeholder = room ? t('搜索{room}的消息', { room: room.name }) : t('搜索消息');
  const n = room
    ? (room.rosterLoaded ? room.members.size : book.sidebarRoster().length)
    : 0;
  $('#chat-head-meta').textContent = room
    ? (n > 0 ? t('{n} 名成员 · {key}', { n, key: room.key }) : room.key)
    : '';
  // 群公告 📌 入口（飞书式）：房主常驻（随时可发布），只读窗口有公告
  // 才显示；本房不在焦点时公告更新过 → 图钉带红点（进门即读清除）
  const pin = $('#chat-head-pin');
  pin.hidden = !(room && (room.notice || book.owner));
  pin.classList.toggle('fresh', !!(room && room.noticeUnread));
  pin.title = room?.notice ? t('查看 / 编辑群公告') : t('发布群公告');
}

function navBadge() {
  const el = $('#nav-chat-badge');
  // 徽标只替当前房数数（与下方工单绿点同一口径）：别家房的动静由它
  // 自己的房行红签报信（switcher），这里再全房合计就是一信两挂——
  // 人在 A 项目时 B 项目 @我，红点只落在 B 的房行上；点进 B（不在
  // 对话面换房，未读不清）它才挂上牛马对话的导航徽标
  const room = book.currentRoom();
  const n = room?.unread || 0;
  const atMe = !!room?.atMe;
  el.hidden = n === 0;
  // 有人@我：徽标数字前加 @（Slack 式点名标）——不只「有动静」，
  // 是有人在等回话；title 把话说明白
  const num = n > 99 ? '99+' : String(n);
  el.textContent = atMe ? `@${num}` : num;
  el.classList.toggle('atme', atMe);
  el.title = n === 0 ? '' : atMe ? t('{n} 条新消息，有人@你', { n }) : t('{n} 条新消息', { n });
  // 办公室导航的工单绿点（红徽标的镜像面）：当前房有没当面看见的任务/
  // 需求变动才亮——点进办公室即清（进门就看见任务纸了）；与 onWorkEvent
  // 的叮声同一道门（rooms.js #intake），声至点必至
  const dot = $('#nav-room-dot');
  const fresh = book.currentRoom()?.workFresh || 0;
  dot.hidden = fresh === 0;
  dot.title = fresh > 0 ? t('当前房间有任务/需求变动——回办公室看看') : '';
}

// ---------- hash routing (§4.0: no SPA fallback, /app is the entry) ----------

// 核心六板块（HTML 预置）＋插件板块注册表（v3 形态 B）：同一循环、同一
// 套 #board-*/#nav-* 锚点约定——插件板块是一等公民，不是 iframe 外挂。
const boards = ['room', 'people', 'project', 'files', 'chat', 'term', 'assistant', 'kb'];
const pluginBoards = new Map(); // id → {view, el, mounted}

// 插件板块的默认图标（manifest 没给 icon 时用）：两块交叠的扩展块
const PLUGIN_BOARD_ICON =
  '<svg class="ico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true"><rect x="2.5" y="2.5" width="7" height="7" rx="1.2"/><rect x="6.5" y="6.5" width="7" height="7" rx="1.2"/></svg>';

// registerPluginBoard — 形态 B 插件的板块落位（loader.js ctx.registerBoard
// 的实现）：建导航锚点＋板块容器，注册后立即 route() 让悬着的 #/hash
// 落地。撞 id 拒绝并 toast（服务端 Load() 已拦一道，这里兜底）。
function registerPluginBoard(plugin, meta, view) {
  const id = view.id || meta.id;
  if (!id || boards.includes(id) || pluginBoards.has(id)) {
    toast.show(t('插件板块 {id} 与内置/其他板块撞 id，未装载', { id: view.id || meta.id || t('(无 id)') }), 'err');
    return false;
  }
  const title = view.title || meta.title || id;
  const nav = document.createElement('a');
  nav.id = `nav-${id}`;
  nav.href = `#/${id}`;
  const iconPath = view.icon || meta.icon;
  const icon = iconPath
    ? `<img src="/plugins/${plugin.id}/${iconPath}" alt="">`
    : PLUGIN_BOARD_ICON;
  nav.innerHTML = `<i class="nv-ico" aria-hidden="true">${icon}</i><span class="nv-txt">${esc(title)}</span>`;
  $('#board-nav').appendChild(nav);
  const el = document.createElement('div');
  el.className = 'board';
  el.id = `board-${id}`;
  el.hidden = true;
  $('#boards').appendChild(el);
  pluginBoards.set(id, { view, el, mounted: false });
  route(); // 悬着的 #/<插件板块> 现在能落位了
  return true;
}

// registerPluginWidget — 形态 B 插件的挂件落位（loader.js 的
// ctx.registerWidget）：目前开放一个挂件位 room-hud（办公室画板左上
// 浮层，随板块显隐）。挂件注册即挂载（与核心板块开机即挂同语义），
// mount 抛错只 console＋toast。
function registerPluginWidget(plugin, view) {
  const hud = $('#room-hud');
  if (!hud || (view.spot || 'room-hud') !== 'room-hud') {
    toast.show(t('插件挂件位 {spot} 未知，未挂载', { spot: view.spot || t('(未给)') }), 'err');
    return false;
  }
  const el = document.createElement('div');
  el.className = 'rh-widget';
  el.dataset.plugin = plugin.id;
  hud.appendChild(el);
  try {
    view.mount?.(el);
  } catch (err) {
    console.error(`[plugin widget ${plugin.id}]`, err);
    toast.show(t('插件「{name}」挂件挂载失败：{err}', { name: plugin.name || plugin.id, err: err?.message || err }), 'err');
  }
  return true;
}

function route() {
  const hash = location.hash.replace(/^#\/?/, '') || 'room';
  const all = [...boards, ...pluginBoards.keys()];
  // t_153：黑板子路径（#/kb/chronicle）——斜杠前段是板块名，后段是
  // 页签深链；不认识的组合仍回 room
  const [head, sub] = hash.split('/');
  const board = all.includes(head) ? head : 'room';
  // 离开对话板块先存消息流读位——display:none 会丢滚动位置，回场时
  // revealed 依钉态还回（脱钉读历史的不被拉回底）
  if (!$('#board-chat').hidden && board !== 'chat') stream.conceal();
  for (const b of all) {
    $(`#board-${b}`).hidden = b !== board;
    $(`#nav-${b}`).classList.toggle('active', b === board);
  }
  if (board === 'room') roomView?.revealed();
  else roomView?.hidden_();
  // 插件板块生命周期：首次到达才 mount（懒装载），reveal/conceal 与
  // 核心板块同语义；插件抛错只 console＋toast，绝不挡路由
  const pb = pluginBoards.get(board);
  if (pb && !pb.mounted) {
    pb.mounted = true;
    try {
      pb.view.mount?.(pb.el);
    } catch (err) {
      console.error(`[plugin board ${board}] mount`, err);
      toast.show(t('插件板块「{id}」初始化失败：{err}', { id: board, err: err?.message || err }), 'err');
    }
  }
  if (pb) {
    try { pb.view.reveal?.(); } catch (err) { console.error(`[plugin board ${board}] reveal`, err); }
  }
  for (const [bid, other] of pluginBoards) {
    if (bid !== board && other.mounted) {
      try { other.view.conceal?.(); } catch { /* 插件的锅不挡路由 */ }
    }
  }
  // 未读的「在场」语义随路由走：只有对话板块展示中，当前房的新消息
  // 才算在场看着（不徽标）；人在别的板块时当前房也照常计数，牛马对话
  // 导航才有数可显。进对话板块即读——当前房未读与@我当场清。
  // 工单绿点同构：办公室板块展示中＝当前房的任务/需求变动当面看见
  // （不进绿点不出通知叮），进办公室板块即清当前房 workFresh
  book.setChatVisible(board === 'chat');
  book.setRoomVisible(board === 'room');
  if (board === 'chat') {
    stream.revealed();
    composer.revealed(); // 藏着切房时输入井量不出高度（scrollHeight＝0）——露面补量
    if (prefs().focusComposer !== false) composer.focus(); // 设置开关：有人不爱被抢焦点
  }
  if (board === 'people') peopleBoard?.revealed();
  else peopleBoard?.conceal(); // 可见性记账——换房跟走只吃在屏时
  if (board === 'project') projectBoard?.revealed();
  else projectBoard?.conceal(); // 任务抽屉/plan 审阅不跟人飘出项目管理
  if (board === 'term') termBoard?.revealed();
  else termBoard?.conceal(); // 转录水合不跟人飘出终端板
  if (board === 'files') filesBoard?.revealed();
  else filesBoard?.conceal(); // 文件树的懒加载态不跟人飘出文件板
  if (board === 'assistant') assistantBoard?.revealed();
  if (board === 'kb') {
    if (head === 'kb' && sub) kbBoard?.openTabDeep?.(sub); // t_153：#/kb/<tab> 深链（chronicle 等）
    kbBoard?.revealed();
  } else kbBoard?.conceal?.(); // 驾驶舱 20s 轮询不跟人飘出黑板板块——离场即停拍
  // 落位即喂导航历史账（空 hash 与 #/room 同站归一；同 hash 重入与
  // 同板块子路径由 navstack 合并——boot 期两次 route() 不多记）
  nav.note(location.hash || '#/room');
}

// ---------- 板块导航历史（ZCode 式 ←→） ----------
// navstack 记账、route() 落位后喂账；←→ 小钮点击向栈要目标 hash 去
// 导航（hashchange → route() → note 消费 pending，程序性移动不记账）。
// 亮灭随栈位：无路可回/进才灰（disabled）。
const navBack = $('#nav-back'), navFwd = $('#nav-fwd');
const nav = new NavStack((canBack, canFwd) => {
  navBack.disabled = !canBack;
  navFwd.disabled = !canFwd;
});
navBack.addEventListener('click', () => {
  const h = nav.back();
  if (h !== null) location.hash = h;
});
navFwd.addEventListener('click', () => {
  const h = nav.forward();
  if (h !== null) location.hash = h;
});
// 标题条穿透带上报：原生壳的 28pt 拖拽罩只在按钮簇的 x 区间放行点击
// （shell 的 setTitlebarPassZone → 罩子 hitTest 开洞）；量的是首末钮
// 的实际占位（簇容器整条通宽，不能拿它量）。浏览器/分享页无此绑定，
// 静默跳过。load 后量一次；resize 兜一遍（簇钉在左上，宽基本不动）。
if (typeof window.setTitlebarPassZone === 'function') {
  const reportPassZone = () => {
    const first = $('#nav-toggle'), last = $('#nav-fwd');
    if (!first || !last) return;
    const r1 = first.getBoundingClientRect(), r2 = last.getBoundingClientRect();
    if (r1.width > 0) window.setTitlebarPassZone(r1.left, r2.right - r1.left);
  };
  window.addEventListener('load', reportPassZone);
  window.addEventListener('resize', reportPassZone);
  requestAnimationFrame(reportPassZone);
}

// ---------- boot ----------

// t_192（r_19）：访客态分支最先判——/view/{project}?t= 进入时裁 UI＋
// 存 token cookie，后续房主专属 boot 段（owner 探测/身份卡/角标）跳过
const isVisitor = initVisitor();

// The ZCode boot gate goes up FIRST (in parallel with everything
// below): the app is bound to ZCode, so the splash door stays shut
// until the bridge is recognized — the boards keep booting behind it
// and the door drops only after both are done.
const zcodeGate = bootGate();

// The owner probe mirrors cli/mirror.go: the local:true person in
// /kb/people is the one human this machine trusts. No owner → the
// workbench stays read-only with the reason shown in place.
async function probeOwner() {
  try {
    const me = (await getPeople()).find((p) => p.local);
    if (me) return { name: me.name, role: me.role, color: me.color };
  } catch { /* read-only fallback below */ }
  return null;
}

window.addEventListener('hashchange', route);
window.addEventListener('pagehide', () => book.byeAll());
route();

const owner = isVisitor ? null : await probeOwner(); // 访客态不探测房主身份
if (!isVisitor) initVisitorBadge(); // 房主侧访客计数角标（访客不可见）
// §4.1④ 房主身份卡：与在线牛马区同一套头像语言，点开跳我的牛马页；
// 识别不到房主时亮红条说明只读模式
const ownerCard = $('#owner-card');
if (owner) {
  // 只留头像＋名字：240px 侧栏里 名字+房主徽章+角色文案 挤一行会把徽章和
  // 角色各截成一个「…」，身份细节走牛马页（点本行即达）
  ownerCard.innerHTML =
    `<span class="avatar sm" style="background:${safeColor(owner.color || memberColor(owner.name))}">${esc(avatarText(owner.name))}</span>` +
    `<span class="oc-name">${esc(owner.name)}</span>`;
  ownerCard.hidden = false;
  ownerCard.onclick = () => { peopleBoard?.select(owner.name); location.hash = '#/people'; };
} else {
  $('#owner-miss').hidden = false;
}
composer.setIdentity(owner?.name || null);
stream.ownerName = owner?.name || null;
// the pixel room board: the little people of the viewed room, fed by
// the same observer stream (clicks bridge into the management boards)
roomView = new RoomView($('#room-canvas'), book, {
  toast: (text, kind) => toast.show(text, kind),
  openPerson: (name) => {
    peopleBoard?.select(name);
    location.hash = '#/people';
  },
  openBoard: (id) => { location.hash = `#/${id}`; },
});
peopleBoard = new PeopleBoard($('#board-people'), book, toast);
// the 小助手 board: the usage advisor over the shared zcode bridge —
// polls /assistant only while a turn runs, degrades to a notice card
// when dispatch is off
assistantBoard = new AssistantBoard($('#board-assistant'), toast);
// the blackboard board (P6 merge): manual/notice/docs/AI-接入 — the
// retired pixel blackboard's pages, reading the same /kb truth
kbBoard = new KbBoard($('#board-kb'), book, toast);
// people detail's manual link deep-links into the docs tab
window.addEventListener('dh:open-doc', (ev) => {
  kbBoard?.openDoc(ev.detail);
  location.hash = '#/kb';
});
// the gantt's single gesture bridges boards: pre-aim the target room's
// composer with the @排期编排组 draft, then hand the user the chat board
projectBoard = new ProjectBoard($('#board-project'), book, toast, {
  draftTo: (key, text) => {
    if (!owner) {
      toast.show(t('未识别到房主身份，无法预填消息（/kb/people 无 local:true）'), 'err');
      return;
    }
    book.switchTo(key);
    location.hash = '#/chat';
    composer.draft(text);
  },
});
// the terminal board (r_17): the complete per-member session I/O —
// injected lines in, full think/tool/turn streams out, asks and answers
// between — over the same book's term lanes
termBoard = new TermBoard($('#board-term'), book, {
  toast: (text, kind) => toast.show(text, kind),
});
// the files board: the current room's workspace as a VSCode-style explorer
// （大厅=Niuma_Studio 本仓；选择器可改看别家，draft/归档照读）
filesBoard = new FilesBoard($('#board-files'), book, toast);
route(); // re-route with the boards live (a #/people or #/project landing missed revealed above)
try {
  // 访客页把 r_19 token 递进来：观察者 hello 带上才计为访客（服务端
  // 治理面只认带 token 的拨号）；房主窗口空串——自己的窗口永不进
  // 访客计数（「仅缓存·HTTP存活」事故的根治面）。
  await book.start(owner, isVisitor ? visitorToken() : ''); // rooms connect; writers dial lazily per send (null owner = read-only)
} catch (err) {
  // 房间起不来和 ZCode 识别失败同一铁律：原因亮在门上，门不开
  // （bootFail 见 ui/boot.js；错误仍上抛给 __errs 侧漏斗）
  bootFail(t('办公室启动失败'), String(err?.message || err));
  throw err;
}
roomView.setRoom(book.current);
globalThis.roomView = roomView; // the debug/testing handle (console triage)

// 项目空间门（v2.12）：工作室还没有任何项目（Niuma_Studio 大厅除外）
// 时不进办公室——门上只放「立项」一件事（创建即自动开张）。房册没读
// 到清单（读面失败）不设门；访客页/只读态（房主未识别）不设门——
// 边界与判据见 spacegate.js 类头。侧栏「项目空间」入口随时可开同一张
// 面板的 browse 面（全部项目＋立项）。
if (owner && !isVisitor) {
  spaceGate = new SpaceGate({
    book, toast, ownerName: owner.name,
    // 草稿/归档行的落点：项目管理指名落位（开张/复活都在概览卡上）
    onInspect: (key) => {
      if (!projectBoard) return;
      if ((location.hash.replace(/^#\/?/, '') || 'room') === 'project') {
        projectBoard.selectKey(key); // 已在板上：直接落（不经跟房）
      } else {
        projectBoard.seedInspect(key); // 进门给指名跳转让位
        location.hash = '#/project';
      }
    },
  });
} else {
  $('#side-space').hidden = true; // 只读态/访客无面板——入口不点空
}
$('#side-space').addEventListener('click', () => spaceGate?.open());

// Both boots must be done before the door opens: ZCode recognized
// (boot.js polled /zcode since the top) AND the rooms connected. A
// minimum beat keeps the gate readable, then the fade hands over.
await zcodeGate;
if (spaceGate?.shouldHold()) {
  // 无项目：splash 之后接的是项目空间门，不是办公室——立项（自动
  // 开张）落门才放行。门 z-96 高于 splash，等它淡完再上墙
  bootNote(t('正在打开项目空间…'));
  await dismissBoot();
  spaceGate.sync();
  await spaceGate.hold();
} else {
  bootNote(t('正在进入办公室…'));
  await dismissBoot();
}

// 形态 B 插件装载（v3）：开门后取索引 → activate → 主题清单就位。
// await 是有意的：持久化的插件主题必须等索引到手才能叠加（铺半套
// 主题比不铺更糟）；板块晚门一拍长出来是可接受的节奏。
await bootPlugins({
  registerBoard: registerPluginBoard,
  registerWidget: registerPluginWidget,
  book,
  toast: (text, kind) => toast.show(text, kind),
});
setPluginThemes(pluginThemes);
applyTheme(); // 重放一次：把 prefs.themePlugin 记住的插件调色板铺上
roomView?.doorDropped(); // t_159：门落地才放首次引导（黑屏气泡实录的最后一门）

// dev 热重载（NIUMA_DEV=1 时后端才挂 /app/__reload 的 SSE）：watcher
// 见 web/ 资源落盘即推一条 reload，窗口自己重载——改一行前端代码存
// 盘即见效，免编译免重启。生产没有这个端点（404 触发 onerror，关闭
// 收场），页面零负担。
const devReload = new EventSource('/app/__reload');
devReload.onmessage = () => { devReload.close(); location.reload(); };
devReload.onerror = () => devReload.close();

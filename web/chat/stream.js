// stream.js — the room's message stream (frontend 稿 §4.4, 飞书式重排):
// one container, re-rendered per room switch and appended per frame.
// Rendering is the observer's projection — say/report rows with letter
// avatars and grouped headers (same sender ≤5min collapses the name
// line AND the avatar; hovering a collapsed row shows its moment in the
// avatar gutter), time-gap dividers, hover copy/quote actions, row
// selection (click parks the action bar, Esc/blank click clears),
// Markdown bodies (markdown.js — the room talks mostly in AI prose:
// code fences, lists, tables, quotes, emphasis; t_NN chips and
// @mention tokens still dress inside), bare URLs linked, @mention
// tokens with a 有人@我 flag, the owner's
// own say lines in a Feishu-lightblue bubble, quote-replies restored
// as gray quote blocks that jump back to the source message (新消息的
// 引用走结构化字段 frame.quote（chat.Quote），正文天生干净——复制/转
// 发/检索不再把引用头带下来；the @X dresses as a reply-mention chip:
// 飞书式回复 @某人，发送即点名通知 TA；#N is the source seq, the
// jump's exact anchor with the from+snippet match as the legacy
// fallback),
// 表情回应（飞书式表情）：气泡下方每枚已用表情一枚 chip（emoji＋人
// 数，点 chip 翻转自己的回应），操作条与 ⊕ 增补钮开快捷盘——回应是
// 原消息的事实，不是新消息，不badge、不会再@人（room.reacts 真源，
// react 帧增量 + /reacts 冷窗水合）, 转发（操作条 → 选目标办公室，
// 引用格式送达——@作者保留，跨房不误点名）, report/event bot cards
// (task frames carry a live snapshot and render an inline task card
// whose op buttons work; long bodies — say bubbles, reports, notice
// cards and task descs alike — auto-fold to a ~6-line preview with a
// 展开/收起 text trigger, fold.js), 收到回执小签
// （飞书式：气泡下方「N人 收到」，操作条里的 收到 钮发 {type:"ack"}
// 回执——事实挂在原消息下，不是新消息，不badge、不会再@人）, 已读
// 小签（飞书式：say 气泡下方「已读 N · 未读 M」——读者＝被调度器真正
// 注入过这条消息的成员，GET /p/{key}/reads 水合 + read 帧增量；台账
// 下限之前的老消息无签，宁缺毋谎；点小签浮读者名单卡）, person
// surfaces — avatars, @mention tokens, quote sources and notice actors —
// open the shared profile card (ui/profilecard.js: presence dot + rank
// tag + @提及/牛马主页); the sender NAME is the exception: 点名字＝@TA
// (微信式点名字喊人——opts.mention 直落输入井，看人请点头像); sender
// heads carry 岗位＋等级 tags (opts.person truth, rankLabel's shared
// vocabulary; the old bridge/mirror origin stamp was engine-internal
// and retired from the user face), and system/join/leave as centered
// quiet
// lines. t_NN tokens chip into task cards (taskcard.js). Scrolling is
// pin-to-bottom (飞书式): the follow is a sticky bit only the user's own
// gestures move — any upward flick un-pins it, and while un-pinned NOTHING
// may yank the viewport down (new messages, task/question cards, room-list
// re-renders and board re-entry all park a 新消息 pill over the composer
// instead); scrolling back near the bottom re-pins. A round ↓ button rides
// for history reading. New-message navigation is direction-aware (微信/
// 飞书同款第一性：提示只指「内容相对视口的方向」，跳转必可逆）——新
// 消息只会长在尾部，计数浮标永远住底部（「N 条新消息 ↓」），点浮标
// 是飞书式跳到未读：送到「以下是新消息」界线（微信式绿线，压在读历
// 史时未见批次第一条前）前落位、顺着线往下补读，不跳过未读直落底——
// 线已被读过（锚在视口上方）或锚被窗帽匀走才落底；进门（换房/回板
// 块）时 book 清账前把未读尾首摘成锚递来，进门直接落在未读处。
// 而程序跳转（浮标跳未读、回底圆钮、引用跳转、自己发言拉底）先记下
// 来路——来路在上方时流顶驻一枚「↑ 回到刚才看的位置」浮标（飞书式
// 返回原位置），来路在下方且无新消息时底部浮标翻「回到刚才看的位置
// ↓」档；用户的滚动手势踩回来路附近（240px 门）浮标自会收。
// 窗帽匀头（MESSAGE_CAP）裁掉头顶的行时按裁高补还
// scrollTop——读位不被匀走的行拽动。A head-bar search (setFilter)
// narrows the stream to matching rows (hits highlighted) without
// touching the room's message window.

import { Msg, Event, LobbyKey, Origin, MESSAGE_CAP } from '../wire/wire.js';
import { getAutopilot } from '../wire/api.js';
import { esc, fmtTime, fmtDateTime, safeColor, memberColor, avatarText, groupAvatarText, icon, dismiss, snipOf, plainifyMD, copyToClipboard } from '../ui/dom.js';
import { t, t as i18n, localeTag } from '../ui/i18n.js';
import { rankLabel } from '../ui/person.js';
import { kbDiffHTML } from './kbdiff.js';
import { gitCommitHTML, mergeCardHTML } from './gitcard.js';
import { foldZoneHTML, wireFoldZones, toggleFoldBtn } from './fold.js';
import { TaskCard, taskCardHTML } from './taskcard.js';

import { TASK_ID_RE, ALL_AT_RE, GROUP_WINDOW, GAP_DIVIDER, BACK_NEAR, UNREAD_PAD, Q_SUPP, REACT_SET, ASK_LINE_RE, QUOTE_RE, KIND_META, EVENT_LABEL, JUMP_META, planWaitText, trimPlan, parseQuote, normText, richHTML, parseEstOffers, parseJumpOffers, hasCls, dueLabel } from './rich.js';
// 前导纯函数的既有导入面经此 re-export 保持（dress 供小助手面板与
// roomview、planWaitText/trimPlan/OWNER_PRESENCE_S 供直测）——只缩不涨。
export { planWaitText, trimPlan, dress, OWNER_PRESENCE_S } from './rich.js';



export class StreamView {
  /**
   * @param {HTMLElement} container #stream
   * @param {import('./rooms.js').RoomBook} book
   * @param {string|null} ownerName 有人@我 flag target
   * @param {Object} [opts]
   * @param {HTMLElement} [opts.pill] #stream-pill — the scroll-up new-message jump
   * @param {HTMLElement} [opts.back] #stream-back — 流顶的回原位浮标：程序跳转
   *   把视口挪走后来路在上方时的「↑ 回到刚才看的位置」（来路在下方时与
   *   pill 合用底部槽，见 #paintBottomPill）
   * @param {import('../ui/toast.js').Toast} [opts.toast]
   * @param {(f: import('../wire/wire.js').Frame) => void} [opts.quote] 引用回复 — aim the composer with a quote bar
   * @param {() => void} [opts.onSearchClear] the filter's 退出搜索 button — the head-bar input lives in app.js
   * @param {(f: import('../wire/wire.js').Frame) => void} [opts.openPlan]
   *   提案 submitted 卡的 去审批 — 跳项目管理开该提案的审阅浮层（app.js 挂载）
   * @param {(offer: Object, projectKey: string|null) => void} [opts.openEstablish]
   *   编制建议行的 加编制 — 跳牛马管理的编制页签并按建议预填加岗浮层
   *   （app.js 挂载；确认动作留在浮层里，聊天侧不越权直接写表）
   * @param {(offer: {kind:'plan'|'gantt'|'req', id:string, label:string}, projectKey: string|null) => void} [opts.openJump]
   *   消息快捷入口（拆解/排期/需求环节）的跳转 — 审提案落项目管理的
   *   审阅浮层、看排期落排期甘特、看需求落需求页签（app.js 挂载；
   *   项目号探不到给 null，落地面按当前选择落位）
   * @param {() => void} [opts.openNotice] 置顶公告条/头栏 📌 的点击 — 打开群公告面板（app.js 挂载）
   * @param {import('../ui/profilecard.js').ProfileCard} [opts.card] 牛马名片卡 — 头像/@令牌/引用者
   *   点开的是同一张卡（app.js 构造，Esc/点外/滚动收口都在卡里自理）
   * @param {(name: string) => void} [opts.mention] 消息头发送者名的点击 —
   *   点名字＝@TA（微信式点名字喊人）：把 @名字 落进输入井（app.js 挂载，
   *   与名片卡的 @ 提及 同一条落点）；未接线时名字退回开名片卡
   * @param {(name: string) => ({role?: string, rank?: number, local?: boolean,
   *   color?: string, online?: boolean, grace?: boolean}|null)} [opts.person]
   *   牛马真相解析器（app.js 的 personOf：/kb/people 的 role/rank 权威，
   *   名册 role 兜底）——消息头的岗位/等级小标读它；缺省则裸名无标
   */
  constructor(container, book, ownerName, opts = {}) {
    this.container = container;
    this.book = book;
    this.ownerName = ownerName;
    this.opts = opts;
    this.lastRec = null;   // 已画到哪条记录——增量游标是记录本身，不是条数（#appendNew）
    this.lastTs = 0;       // last appended frame's ts — the divider cursor
    this.lastTalk = null;  // {from, type, ts} — the grouping cursor
    this.pillCount = 0;    // rows appended while scrolled up
    this.pillAtMe = false; // …any of them @我
    this.pinned = true;    // 贴底跟随位：钉着才随新内容走，用户上滑即脱钉
    this.lastTop = 0;      // 手势方向探测——上一次 scroll 事件的 scrollTop
    this.savedTop = null;  // 离开对话板块时存的读位（display:none 会丢位置）
    this.backTop = null;   // 程序跳转前的读位（回原位浮标的锚，null＝没欠来路）
    this.flightTop = null; // 程序送位在途的目的地——到达门让路：平滑飞行
                           // 起飞期视口还压在原位（且前段飞得慢），不判
                           // 「踩回来路」，否则锚刚记下就会被顺手清掉
    this.flightAt = 0;     // 航程起飞时刻——超时（1.5s）强结航（真平滑滚
                           // 动再长也到不了；防被取消的航程永久关掉到达门）
    this.unreadMark = null; // 第一条未见消息的记录——「以下是新消息」界线的锚
    this.filter = '';      // head-bar search stem ('' = the whole stream)
    this.selRow = null;    // the clicked-and-parked row (its action bar stays)
    this.snapshots = new Map(); // task id -> latest frame snapshot (op targets)
    this.rdEl = null;      // 读者卡（悬停/点击已读小签）：当前卡元素
    this.rdAnchor = null;  // …对应的小签锚点（换签先收旧卡、原地钉住判据）
    this.rdPinned = false; // …点击钉住态：不吃 mouseleave 收口，点空白/Esc 才收
    this.rdOpenT = 0;      // 悬停开卡计时（停 0.16s 才开，扫过不误触）
    this.rdCloseT = 0;     // 离签/离卡收口计时（时延内回来即撤销）
    this.ownerActAt = 0;   // 房主本房最近一次点卡应答时刻——在场估计的应
                           // 答腿（say 腿由 messages 倒扫覆盖，应答不落帧
                           // 只能记本地；换房清零，房各一口钟）
    this.apInfo = new Map(); // projectKey → {advance, delayS, at, loading}
                           // ——等待行的引擎口径缓存（GET /p/{key}
                           // /autopilot，60s 热更；访客页/拉取失败只报候时）
    this.waitTimer = setInterval(() => this.#tickWaits(), 30_000); // 等待行的钟摆
    this.waitTimer.unref?.(); // Node（node --test）的 interval 会挂住测试
                              // 进程——unref 只在 Node 的 Timeout 上存在
                              //（浏览器返数字），进程不为我续命、跑着照敲
    this.card = new TaskCard({
      onOp: (task, op) => this.#taskOp(task, op),
      toast: opts.toast,
      // 待确认卡的动词按查看者身份分流（房主→仲裁，Need 内→接单/婉拒），
      // ownerName 探针后回填，读在 paint 时所以弹层总能拿到最新身份
      viewer: () => this.ownerName || '',
    });
    container.addEventListener('click', (ev) => this.#onClick(ev));
    // 已读小签悬停即出读者卡（免点）：进入停留 0.16s 开卡、离开留
    // 0.26s 缓冲放指针挪进卡里；点一下则钉住（老语义：点空白/Esc 才收）。
    // 触屏不悬停，仍走点击。
    container.addEventListener('pointerover', (ev) => this.#readHover(ev, true));
    container.addEventListener('pointerout', (ev) => this.#readHover(ev, false));
    container.addEventListener('scroll', () => this.scrolled(container.scrollTop), { passive: true });
    // 容器高度哨（贴底钉的另一半）：#stream 的高度会被邻居改变——
    // 「正在处理…」行随成员干活收放、输入井随草稿自增高缩矮、窗口
    // 缩放。高度变化不触发 scroll 事件，钉着的视口会停在旧位、离新
    // 底差一截，直到下一条消息才被 #follow 捞回；哨一响就补送底。
    // 脱钉读历史时绝不拉底；板块切走（display:none）不动作。
    new ResizeObserver(() => this.relayout()).observe(container);
    window.addEventListener('resize', () => this.relayout());
    // 底部浮标（#stream-pill）双档：计数档「N 条新消息 ↓」点＝飞书式
    // 跳到未读（pillClick——真监听与测试同一道门）；回原位档「回到刚
    // 才看的位置 ↓」点＝直接送回来路（goBack）。
    opts.pill?.addEventListener('click', () => this.pillClick());
    opts.jump?.addEventListener('click', () => {
      this.#saveBack(); // 回底圆钮同款：程序跳转记来路
      this.pinned = true; // 点了就是「带我到底」，回钉再送底
      this.#toBottom(true);
      this.#syncBack();
    });
    opts.back?.addEventListener('click', () => this.goBack());
    // 看图浮层（输入图片）：点大图任意处即关（光标 zoom-out 已示意）；
    // Esc 同关（与其他弹层同一条键盘收口）。
    this.imgEl = document.getElementById('img-overlay');
    this.imgEl?.addEventListener('click', () => this.#closeImage());
    document.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Escape') return;
      if (this.rdEl) this.#closeReaders();
      if (this.rpEl) this.#closeReactPop();
      if (this.fwEl) this.#closeForwardPop();
      this.#closeImage();
      this.#select(null);
    });
  }

  /** 滚动手势的统一处理口（真监听器与测试同一道门）：读者卡收口、方向
   *  探测、贴底钉的翻面、到尾清未读、回原位浮标的驻留判定。 */
  scrolled(st) {
    if (this.rdEl && !this.rdPinned) this.#closeReaders(); // 悬停卡不追滚动，流一动就收
    const down = st >= this.lastTop;
    this.lastTop = st;
    // 贴底跟随（飞书式）：只有用户自己的手势改钉——向上拨一下就脱钉，
    // 之后新到的一切（消息/任务卡/问题卡/整流重画）只涨浮标、绝不拉底；
    // 滚回底附近跟随才重新挂上。程序送底（#follow/#toBottom）产生的
    // 也是向下滚动事件，天然维持钉态。
    // 高度钳制豁免：流的高度变了（「正在处理…」行收放、输入井自增
    // 高缩矮、窗口缩放）时浏览器会把 scrollTop 压到新的上限——这样
    // 的滚动事件方向向下、却不是用户手势，且恰好落在底上；不豁免，
    // 钉会被这类事件吃掉，之后新消息只涨浮标不拉底（发完消息总差
    // 一截的根因之一）。真用户上拨必然离底，撞不上这道豁免（钳制
    // 落点距底 0～2px——亚像素高度会量出 1）。
    const clamp = !down && this.pinned && this.#nearBottom(2);
    this.pinned = (down && this.#nearBottom(30)) || clamp;
    if (this.#nearBottom()) this.#clearUnread(); // 到尾即读齐：计数、界线一起收
    this.#syncBack();
    this.#syncJump();
  }

  /** @param {string|null} key @param {string} hint */
  onChange(key, hint) {
    const room = this.book.currentRoom();
    if (!room) return;
    if (hint === 'switch' || hint === 'rooms') {
      if (hint === 'switch') { // 新房从底出发：钉回去、旧读位作废
        this.pinned = true;
        this.lastTop = 0;
        this.savedTop = null;
        this.backTop = null; // 旧房的来路不欠了——回原位浮标随房清
        this.ownerActAt = 0; // 在场估计的应答落戳也是旧房的事——随房清
      }
      // 'rooms'（房列表真变了）是内容等价的整流重画——保住读位，
      // 不得借机把正在读历史的人拉到底
      this.#renderAll(room, hint === 'rooms');
      this.#renderQuestions(room); // 换房整流后问题卡栈按新房重建
      if (hint === 'switch') this.entryLand(); // 飞书式进门落未读（book 递锚则在，落完自清）
      return;
    }
    if (hint === 'questions' && key === room.key) {
      this.#renderQuestions(room); // 开放提问增量/水合/收口：只重画卡栈
      return;
    }
    if (hint === 'notice' && (key === room.key || key === null)) {
      this.#renderNoticeBar(room); // 发布/更新/撤下只动置顶条，不整流重画
      return;
    }
    if (hint === 'acks' && key === room.key) {
      this.#refreshAcks(); // 收到回执增量/水合：只刷小签与按钮态，不整流重画
      return;
    }
    if (hint === 'reacts' && key === room.key) {
      this.#refreshReacts(); // 表情回应增量/水合：只刷 chips，不整流重画
      return;
    }
    if (hint === 'reads' && key === room.key) {
      this.#refreshReads(); // 已读回执增量/水合：只刷小签，不整流重画
      return;
    }
    if (hint === 'queues' && key === room.key) {
      this.#refreshReads(); // 排队中投影变了：只刷小签的中间档，不整流重画
      return;
    }
    if (hint === 'members' && key === room.key) {
      this.#refreshReads(); // 名册变了：已读/未读的分母跟着变
      return;
    }
    if (hint === 'messages' && key === room.key) {
      this.#appendNew(room);
      this.#tickWaits(); // plan 帧落地（代收/换代）即刻重算等待行——槽位
                         // 存续判据读 messages，新帧到钟就得走，不等 30s 摆
    }
  }

  /**
   * 消息搜索（对话区头栏的搜索框）：按发送者＋正文＋事件名过滤当前
   * 房的流，空串恢复整流。过滤态不落时间分隔线、不涨新消息浮标。
   * @param {string} q
   */
  setFilter(q) {
    const next = String(q || '').trim().toLowerCase();
    if (next === this.filter) return;
    this.filter = next;
    const room = this.book.currentRoom();
    if (room) this.#renderAll(room);
    if (room) this.#renderQuestions(room); // 搜索态收起卡栈，清搜索复原
  }

  /** @param {import('../wire/wire.js').Frame} frame */
  #match(frame) {
    return `${frame.from || ''}\n${frame.text || ''}\n${frame.event || ''}`
      .toLowerCase()
      .includes(this.filter);
  }

  revealed() { // board tab re-entry: 钉在底才回底；读历史的把离场前
    // 存的位置还回去——display:none 丢的滚动位置由此代管，回场本身
    // 不是拉底的理由。book 递了未读锚（离场期间攒的未读，清账前摘的）
    // 且人原是钉在底的，先落未读——飞书式回场落在第一条未读前。
    const room = this.book.currentRoom();
    const mark = room?.entryMark || null;
    if (room) room.entryMark = null; // 锚是一次性的：落没落成都不欠
    if (this.savedTop !== null && !this.pinned) {
      this.#navTo(Math.min(this.savedTop, this.container.scrollHeight), false);
      this.savedTop = null;
    } else if (!mark || !this.#deliverUnread(mark, false, true)) {
      this.#clearUnread();
      this.#toBottom(false);
    }
    this.#syncBack();
    this.#syncJump();
  }

  /** 进门落未读（飞书式，换房 switch 与回对话面 revealed 共用的公开
   *  门）：book 清账前把未读尾首摘成锚挂在房上递来（rooms.js 的
   *  #entryMark）——进门落在第一条未读前顺着补读，不一杆子捅到底。
   *  锚是一次性的：落没落成都清，'rooms' 保读位重画不再落。 */
  entryLand() {
    const room = this.book.currentRoom();
    const mark = room?.entryMark || null;
    if (room) room.entryMark = null;
    if (mark) this.#deliverUnread(mark, false, true);
    this.#syncBack();
    this.#syncJump();
  }

  /** 离开对话板块（元素还带着布局）先存读位，revealed 依钉态还回。 */
  conceal() {
    this.savedTop = this.container.scrollTop;
  }

  #renderAll(room, keep = false) {
    // keep：内容等价的整流重画（房列表变动）——保读位，不重置浮标
    const keepTop = keep && !this.pinned ? this.container.scrollTop : null;
    this.#select(null);
    this.#closeReaders(); // 固定定位的读者卡不随房走，换房先收
    this.#closeReactPop();
    this.#closeForwardPop();
    this.opts.card?.close();
    this.container.innerHTML = '';
    this.lastRec = null;
    this.lastTs = 0;
    this.lastTalk = null;
    if (!keep) { // 换房/兜底整流：未读计数与来路一起清（'rooms' 保读位不清）
      this.#clearUnread();
      this.backTop = null;
      this.flightTop = null; // 在途航程随房作废
    }
    if (this.filter) { // 搜索态：命中信息条置顶，下面只画匹配行
      const info = document.createElement('div');
      info.className = 'stream-filter-info';
      info.innerHTML = `<span>${t('搜索「{q}」· 命中 {n} 条', { q: `<b>${esc(this.filter)}</b>`, n: '<b class="fi-hits">0</b>' })}</span>` +
        `<button type="button" data-filter-x>${t('退出搜索')}</button>`;
      this.container.appendChild(info);
      let hits = 0;
      for (const rec of room.messages) {
        if (rec.frame && this.#match(rec.frame)) {
          this.#appendRow(rec, { bare: true });
          hits++;
        }
      }
      info.querySelector('.fi-hits').textContent = String(hits);
      this.lastRec = room.messages[room.messages.length - 1] || null;
      if (!hits) {
        const empty = document.createElement('div');
        empty.className = 'stream-empty';
        empty.innerHTML = `<strong>${t('没有匹配的消息')}</strong><small>${t('换个词试试，或退出搜索回到完整消息流')}</small>`;
        this.container.appendChild(empty);
      }
      this.container.scrollTop = this.container.scrollHeight;
      this.#syncJump();
      this.#syncBack();
      return;
    }
    this.#renderNoticeBar(room); // 置顶公告条常驻（含空房态）；搜索态无条
    if (!room.messages.length) {
      const empty = document.createElement('div');
      empty.className = 'stream-empty';
      empty.innerHTML =
        `<span class="avatar se-avatar" style="background:${safeColor(memberColor(room.key))}">${esc(groupAvatarText(room.name))}</span>` +
        `<strong>${t('{room} 还没有消息', { room: esc(room.name) })}</strong>` +
        `<small>${t('说第一句话，或 @ 成员派活——对话、汇报和任务卡都长在这条流里')}</small>`;
      this.container.appendChild(empty);
      this.#syncJump();
      this.#syncBack();
      return;
    }
    for (const rec of room.messages) this.#appendRow(rec);
    this.lastRec = room.messages[room.messages.length - 1] || null;
    if (keep && this.unreadMark) this.#paintUnreadDivider(room); // 保读位重画：界线随锚行复位
    if (keepTop !== null) this.container.scrollTop = Math.min(keepTop, this.container.scrollHeight);
    else this.container.scrollTop = this.container.scrollHeight;
    this.#syncJump();
    this.#syncBack();
  }

  // 置顶公告条（飞书式）：本房有群公告时常驻流顶，点击开公告面板；
  // 无公告/搜索态无条。发布/更新/撤下只走这里单刷，不整流重画。
  #renderNoticeBar(room) {
    this.container.querySelector('.stream-notice-bar')?.remove();
    if (!room.notice) return;
    const n = room.notice;
    const one = String(n.content || '').split('\n').map((l) => l.trim()).find(Boolean) || t('（空公告）');
    const excerpt = one.length > 80 ? `${one.slice(0, 80)}…` : one;
    const who = n.by ? ` · ${n.by}` : '';
    const bar = document.createElement('button');
    bar.type = 'button';
    bar.className = 'stream-notice-bar';
    bar.title = t('查看群公告全文');
    bar.innerHTML =
      '<span class="snb-pin">' + icon('megaphone') + ` ${t('群公告')}</span>` +
      `<span class="snb-text">${esc(excerpt)}${esc(who)}</span>` +
      `<span class="snb-view">${t('查看全文')}</span>`;
    bar.addEventListener('click', () => this.opts.openNotice?.());
    this.container.prepend(bar);
  }

  #appendNew(room) {
    if (!room.messages.length) return;
    // 空态/命中信息条还挂在容器上时整流重画，别在它们后面续行
    if (this.container.querySelector('.stream-empty, .stream-filter-info')) {
      this.#renderAll(room);
      return;
    }
    // 增量游标＝已画到哪条记录（记录本身，不是条数）：房窗到帽
    // （MESSAGE_CAP）后每进一条就从头上匀一条走，窗长永远停在帽上——
    // 按条数找「第一条没画的」在满窗时永远差着一格，新来的那条找不出
    // 来（流冻在旧尾：房主与成员的新消息一律不显示，直到换房整流才救
    // 回——「有时候不显示」的根因）。按记录定位，头怎么匀都追得上尾巴。
    let start = 0;
    if (this.lastRec) {
      const at = room.messages.lastIndexOf(this.lastRec);
      if (at < 0) { // 游标记录已不在窗内（异常换血）：整流重画兜底
        this.#renderAll(room);
        return;
      }
      start = at + 1;
    }
    let added = 0;
    let frames = 0; // 其中有消息体的行——浮标报「N 条新消息」，断线
                    // 补窗的标记行不是消息，不计数（重连不虚高一）
    let atMe = false;
    let askRow = false;
    let markRow = null; // 本批第一行——「以下是新消息」界线的落点
    let markRec = null; // …对应的记录（界线锚；批次头若是断线补窗等无行
                        //   记录，锚顺延到第一条真有行的）
    for (let i = start; i < room.messages.length; i++) {
      const rec = room.messages[i];
      if (this.filter) { // 搜索态：只续匹配行，不动浮标
        if (rec.frame && this.#match(rec.frame)) this.#appendRow(rec, { bare: true });
        continue;
      }
      const row = this.#appendRow(rec, { anim: true });
      if (row && !markRow) { markRow = row; markRec = rec; }
      added += 1;
      if (rec.frame) frames += 1;
      const f = rec.frame;
      if (f && (f.type === Msg.Say || f.type === Msg.Report) && this.#atMe(f)) atMe = true;
      // 提问行落地：先到的开放卡可能还在流尾栈里——搬进自己的行
      if (f && f.type === Msg.Say && ASK_LINE_RE.test(f.text || '')) askRow = true;
    }
    this.lastRec = room.messages[room.messages.length - 1] || null;
    if (!added) return;
    this.#trimCapped();
    if (askRow && [...room.questions.values()].some((q) => q && !q.status)) {
      this.#renderQuestions(room);
    }
    this.#follow(); // 贴底跟随：钉着才随新内容走，脱钉时只涨浮标不拉底
    if (!this.pinned) {
      // 未见批次的第一锚（0→1，或批次头是无行记录时顺延到首个有行
      // 批次）：第一条未读前压「以下是新消息」界线（微信式）——点浮
      // 标送到线前往下补读（飞书式跳到未读），线随读齐一起收。
      if (!this.unreadMark && markRec) {
        this.unreadMark = markRec;
        this.container.insertBefore(this.#unreadDividerEl(), markRow);
      }
      this.pillCount += frames;
      this.pillAtMe = this.pillAtMe || atMe;
    }
    this.#syncBack(); // 底部浮标的统一渲染口（计数档/回原位档）
    this.#syncJump();
  }

  // 消息窗的 DOM 镜面裁头：房窗到帽后每进一条就从头上匀一条走，DOM
  // 若只增不减，久坐一间房行数无界上涨。方案算在 trimPlan（纯函数）；
  // 裁的是头顶的行——浏览器不补还 scrollTop，读历史的人会被匀走的行
  // 拽着一跳，按裁高把读位（与来路锚）送回原处（钉底的不必：#follow
  // 随后自会送底）。
  #trimCapped() {
    const cut = trimPlan([...this.container.children], MESSAGE_CAP);
    if (!cut.length) return;
    let h = 0;
    for (const el of cut) { h += el.offsetHeight || 0; el.remove(); }
    if (!h || this.pinned) return;
    this.container.scrollTop = Math.max(0, this.container.scrollTop - h);
    if (this.backTop !== null) this.backTop = Math.max(0, this.backTop - h);
  }

  /** @param {record} record @param {{bare?: boolean, anim?: boolean}} [opts]
   *  bare = 搜索态，跳过分隔线；anim = 增量追加的新行播入场动画 */
  #appendRow(record, opts = {}) {
    const frag = document.createDocumentFragment();
    let row = null;
    if (record.divider) {
      if (!opts.bare) frag.appendChild(this.#dividerEl(record.divider));
      this.lastTalk = null;
    } else if (record.frame) {
      const d = opts.bare ? null : this.#timeDividerEl(record.frame.ts);
      if (d) { frag.appendChild(d); this.lastTalk = null; }
      row = this.#frameRow(record.frame, opts.bare);
      if (row) {
        if (opts.anim) row.classList.add('new');
        frag.appendChild(row);
        // 回应/收到/已读小签随行落位（渲染真源 room.reacts/acks/reads；
        // 后续增量走 #refreshReacts/#refreshAcks/#refreshReads）
        if (record.frame.seq) {
          this.#paintReacts(row, record.frame, this.book.currentRoom());
          this.#paintAcks(row, record.frame, this.book.currentRoom());
          this.#paintReads(row, record.frame, this.book.currentRoom());
        }
        if (this.filter) this.#highlightRow(row);
      }
      if (record.frame.ts) this.lastTs = record.frame.ts;
    }
    if (frag.childNodes.length) {
      // 问题卡栈永远压在流尾：新消息行插到栈前，不把卡顶出视口
      const stack = this.container.querySelector('.q-stack');
      if (stack) this.container.insertBefore(frag, stack);
      else this.container.appendChild(frag);
    }
    if (row) {
      this.#wireFold(row); // 入 DOM 后才能量正文高度（对话/汇报/通知/任务描述）
      // 懒加载图片入列时高度为 0、加载完才撑开行高——撑高既不触发
      // scroll 也不改容器盒子（ResizeObserver 看不见），钉着的话在
      // load 里补一脚跟底；读历史时不动。图片撑高也可能把正文顶过
      // 折叠线，同一脚里重量高（幂等：已定折的不改判）。
      for (const img of row.querySelectorAll('.msg-imgs img')) {
        if (!img.complete) img.addEventListener('load', () => {
          this.#wireFold(row);
          if (this.pinned) this.#follow();
        }, { once: true });
      }
    }
    return row;
  }

  // 收到回执（飞书式）：小签挂在气泡下方（icon＋「若干人 收到」），
  // 操作条里的 收到 钮随之翻 已收到。#paintAcks 是唯一渲染口——初始
  // 落行与 ack 帧增量都走这里，两条路永不各画各的。
  #paintAcks(row, frame, room) {
    const ackers = room ? [...(room.acks.get(frame.seq) || [])] : [];
    const body = row.querySelector('.msg-body');
    if (!body) return;
    let line = body.querySelector('.ack-line');
    if (ackers.length) {
      if (!line) {
        line = document.createElement('div');
        line.className = 'ack-line';
        body.appendChild(line);
      }
      line.innerHTML = icon('check', 12) + `<span>${t('{who} 收到', { who: esc(ackers.join(t('、'))) })}</span>`;
    } else if (line) {
      line.remove();
    }
    const btn = row.querySelector('.msg-actions [data-act="ack"]');
    if (btn) {
      const mine = !!this.ownerName && ackers.includes(this.ownerName);
      btn.classList.toggle('done', mine);
      btn.textContent = mine ? t('已收到') : t('收到');
      btn.title = mine ? t('已回执收到') : t('回执收到（不回复、不打扰、不会触发新的 @）');
    }
  }

  /** ack 帧增量/HTTP 水合的刷新口：已渲染行里带 seq 的逐行重画小签。 */
  #refreshAcks() {
    for (const row of this.container.querySelectorAll('.line.msg')) {
      const f = row._frame;
      if (f?.seq) this.#paintAcks(row, f, this.book.currentRoom());
    }
  }

  // 表情回应（飞书式）：气泡下方的回应 chips——每枚已用表情一枚（emoji
  // ＋人数，点 chip 翻转自己的回应，title 报名单），尾部一枚 ⊕ 增补钮
  // 开快捷盘。#paintReacts 是唯一渲染口（#paintAcks 纪律）：初始落行
  // 与 react 帧增量都走这里，两条路永不各画各的。回应排位在 收到 小签
  // 之前（紧贴气泡，飞书同款次序）；已读小签住气泡侧边（bubble-row），
  // 不在这条流里。
  #paintReacts(row, frame, room) {
    const body = row.querySelector('.msg-body');
    if (!body) return;
    const byEmoji = room ? room.reacts.get(frame.seq) : null;
    let line = body.querySelector('.react-line');
    if (!byEmoji || !byEmoji.size) {
      line?.remove();
      return;
    }
    if (!line) {
      line = document.createElement('div');
      line.className = 'react-line';
      body.insertBefore(line, body.querySelector('.ack-line'));
    }
    const mine = this.ownerName || '';
    line.innerHTML = [...byEmoji.entries()].map(([emoji, names]) => {
      const i = names.has(mine);
      const roll = [...names].join('、');
      return `<button type="button" class="react-chip${i ? ' mine' : ''}" data-react="${esc(emoji)}"` +
        ` title="${esc(roll)}${i ? t('（点一下撤回我的回应）') : ''}">${esc(emoji)}<span class="rc-n">${names.size}</span></button>`;
    }).join('') +
    `<button type="button" class="react-add" title="${t('添加表情回应')}">` + icon('smile', 13) + '</button>';
  }

  /** react 帧增量/HTTP 水合的刷新口：已渲染行里带 seq 的逐行重画 chips。 */
  #refreshReacts() {
    const room = this.book.currentRoom();
    for (const row of this.container.querySelectorAll('.line.msg')) {
      const f = row._frame;
      if (f?.seq) this.#paintReacts(row, f, room);
    }
  }

  // 已读回执（飞书式三态）：气泡侧边的小签——我的消息贴泡左、别人
  // 的消息贴泡右（bubble-row 并排，底缘对齐）。读者＝回合真正消费了
  // 这条 say 的成员（终点才读——注入只是送达）；排队中＝车道还压着
  // 这条的成员（未注入、未读、未丢）。台账下限（readSince）之前的
  // 消息没有已读事实，画「未读」就是说谎，一律不出签；水合完成前
  // 同样不画（readsLoaded）。#paintReads 是唯一渲染口（#paintAcks 同
  // 纪律）。分母＝名册人数−发送者本人：发送者不读自己的消息。
  #paintReads(row, frame, room) {
    if (frame.type !== Msg.Say) return; // 汇报不进会话，系统行是房间自己的声音
    const host = row.querySelector('.bubble-row');
    if (!host) return;
    let line = host.querySelector('.read-line');
    const canPaint = room && room.readsLoaded && frame.seq > (room.readSince || 0);
    if (!canPaint) {
      line?.remove();
      return;
    }
    const readers = [...(room.reads.get(frame.seq) || [])];
    const queued = [...(room.queues.get(frame.seq) || [])];
    const mine = !!this.ownerName && frame.from === this.ownerName;
    // 分母：除发送者外的在册成员（含宽限座——他们仍是可读的模型）
    const denom = Math.max(0, room.members.size - 1);
    if (!mine && readers.length === 0 && queued.length === 0) {
      line?.remove(); // 别人的消息没人读也没人排队：不出签（已读是低调的事实）
      return;
    }
    if (!line) {
      line = document.createElement('div');
      line.className = 'read-line'; // 名单在悬停/点击出的读者卡里，不再挂原生 title
      host.appendChild(line);
    }
    const rest = Math.max(0, denom - readers.length - queued.length);
    const parts = [];
    if (readers.length) parts.push(t('已读 {n}', { n: readers.length }));
    if (queued.length) parts.push(t('排队中 {n}', { n: queued.length }));
    let label;
    if (mine) {
      if (!parts.length) {
        label = t('未读');
      } else {
        if (rest > 0) parts.push(t('未读 {n}', { n: rest }));
        label = parts.join(' · ');
      }
    } else {
      label = readers.length
        ? t('{n}人 已读', { n: readers.length }) + (queued.length ? ` · ${t('{n}人 排队中', { n: queued.length })}` : '') + (rest > 0 ? ` · ${t('未读 {n}', { n: rest })}` : '')
        : t('{n}人 排队中', { n: queued.length }) + (rest > 0 ? ` · ${t('未读 {n}', { n: rest })}` : '');
    }
    // 图标随主档换装：全员未读但有排队的挂钟（等的是投递不是阅读），
    // 其余用眼（看的是阅读事实）；钟档整签转琥珀
    const waiting = queued.length && !readers.length;
    line.classList.toggle('waiting', !!waiting);
    line.innerHTML = icon(waiting ? 'clock' : 'eye', 12) + `<span>${esc(label)}</span>`;
  }

  /** read 帧增量/HTTP 水合的刷新口：已渲染行里带 seq 的逐行重画小签。 */
  #refreshReads() {
    for (const row of this.container.querySelectorAll('.line.msg')) {
      const f = row._frame;
      if (f?.seq) this.#paintReads(row, f, this.book.currentRoom());
    }
  }

  // 读者卡（气泡卡片篇）：悬停或点 已读 小签浮出的名单——三段排开（已
  // 读／排队中／未读，中间档挂钟形说明「消息还压在该成员的待投队列」），
  // 每人一行（字母头像＋名字＋岗位），空段不占行、段头带计数。悬停开的
  // 卡随指针走（挪进卡里不算离开，离开卡才收）；点击开的卡钉住，点空
  // 白/Esc 才收。
  #openReaders(anchor, frame) {
    this.#closeReaders();
    const room = this.book.currentRoom();
    if (!room) return;
    const readers = [...(room.reads.get(frame.seq) || [])];
    const queued = [...(room.queues.get(frame.seq) || [])].filter((n) => !readers.includes(n));
    const denom = Math.max(0, room.members.size - 1);
    const rest = Math.max(0, denom - readers.length - queued.length);
    const rowHTML = (name, wait) => {
      const m = room.members.get(name);
      const color = safeColor(m?.color || memberColor(name));
      return '<div class="rd-row">' +
        `<span class="avatar rd-avatar" style="background:${color}">${esc(avatarText(name))}</span>` +
        `<span class="rd-name">${esc(name)}</span>` +
        (wait ? `<span class="rd-wait">${t('排队待投')}</span>` : '') +
        `<span class="rd-role">${esc(m?.role || '')}</span></div>`;
    };
    const section = (title, names, wait, cls) => !names.length ? '' :
      `<div class="rd-sec${cls ? ' ' + cls : ''}"><b>${title} ${names.length}</b></div>` + names.map((n) => rowHTML(n, wait)).join('');
    // 未读者名册倒推：在册成员 − 发送者 − 已读 − 排队
    const namesLeft = new Set([...room.members.keys()]
      .filter((n) => n !== frame.from && !readers.includes(n) && !queued.includes(n)));
    const el = document.createElement('div');
    el.className = 'readers-card';
    el.innerHTML = '<div class="rd-head">' + icon('eye', 13) +
      `<b>${t('已读 {n}', { n: readers.length })}</b>` +
      (queued.length ? `<span>${t('排队中 {n}', { n: queued.length })}</span>` : '') +
      (rest > 0 ? `<span>${t('未读 {n}', { n: rest })}</span>` : '') + '</div>' +
      '<div class="rd-list">' +
      section(t('已读'), readers, false) +
      section(t('排队中'), queued, true, 'rd-sec-q') +
      (namesLeft.size ? `<div class="rd-sec rd-sec-dim"><b>${t('未读')}</b></div>` +
        [...namesLeft].map((n) => rowHTML(n, false)).join('') : '') +
      '</div>';
    document.body.appendChild(el);
    this.rdEl = el;
    this.rdAnchor = anchor;
    // clamp：小签下方优先，放不下翻上方（气泡卡片篇同款算法）
    const a = anchor.getBoundingClientRect();
    const width = 240, margin = 16, gap = 8;
    const h = el.offsetHeight || 140;
    let left = a.left + a.width / 2 - width / 2;
    left = Math.max(margin, Math.min(left, innerWidth - width - margin));
    const below = a.bottom + gap + h <= innerHeight - margin;
    const top = below ? a.bottom + gap : Math.max(margin, a.top - h - gap);
    el.classList.add(below ? 'rc-below' : 'rc-above');
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
    // 悬停态的存活圈：指针进卡即续命、离卡就计收口（时延内回到小签会被
    // pointerover 的 #rdStay 撤销）；点开钉住的卡不吃 mouseleave 关门，
    // 仍由点空白/Esc 收口
    el.addEventListener('mouseenter', () => this.#rdStay());
    el.addEventListener('mouseleave', () => { if (!this.rdPinned) this.#rdLeave(120); });
    this.rdOnDoc = (ev2) => {
      if (el.contains(ev2.target) || anchor.contains(ev2.target)) return; // 点自己的小签＝钉住，不当「外面」
      this.#closeReaders();
    };
    document.addEventListener('mousedown', this.rdOnDoc, true);
  }

  // 悬停车道（pointerover/out 委托）：进出已读小签的语义判定——进出都看
  // relatedTarget（来的地方/去的地方）：去向还在同一枚小签里的是子元素
  // 间移动，不算进出。进入停留 0.16s 开卡（扫过不误触），离开留 0.26s
  // 让指针能从小签挪进卡（#rdLeave 统一计收口时延）。
  #readHover(ev, over) {
    if (ev.pointerType === 'touch') return; // 触屏没有「移过去」，仍走点击
    const rl = ev.target.closest?.('.read-line');
    if (!rl) return;
    const rel = ev.relatedTarget; // 去向（over＝从哪来，out＝到哪去）
    if (rel && rel.closest?.('.read-line') === rl) return;
    const f = rl.closest('.msg')?._frame;
    if (over) {
      this.#rdStay(); // 回来了：撤销离卡计时的收口
      if (this.rdEl && this.rdAnchor !== rl) this.#closeReaders(); // 换了一枚小签：旧卡先收
      if (!this.rdEl && f) {
        if (this.rdOpenT) clearTimeout(this.rdOpenT);
        this.rdOpenT = setTimeout(() => {
          this.rdOpenT = 0;
          if (!rl.isConnected) return; // 等待期整流重画/换房，小签已不在
          this.#openReaders(rl, f);
        }, 160);
      }
    } else if (!this.rdPinned) {
      this.#rdLeave(260);
    }
  }

  // 点击小签＝把卡钉住（悬停已开的原地钉，不闪重开）
  #pinReaders(rl, f) {
    if (this.rdOpenT) { clearTimeout(this.rdOpenT); this.rdOpenT = 0; }
    if (this.rdEl && this.rdAnchor === rl) { this.rdPinned = true; return; }
    this.#openReaders(rl, f);
    this.rdPinned = true;
  }

  // 取消「离卡收口」的计时，卡续命（指针进卡/回到小签时调）
  #rdStay() {
    if (this.rdOpenT) { clearTimeout(this.rdOpenT); this.rdOpenT = 0; }
    if (this.rdCloseT) { clearTimeout(this.rdCloseT); this.rdCloseT = 0; }
  }

  // 开始「离开即收」的倒计时（指针出了小签/卡；时延内回来会被 #rdStay 撤销）
  #rdLeave(ms) {
    if (!this.rdEl) return;
    if (this.rdCloseT) clearTimeout(this.rdCloseT);
    this.rdCloseT = setTimeout(() => {
      this.rdCloseT = 0;
      this.#closeReaders();
    }, ms);
  }

  #closeReaders() {
    this.#rdStay();
    this.rdPinned = false;
    this.rdAnchor = null;
    if (!this.rdEl) return;
    dismiss(this.rdEl, 150);
    this.rdEl = null;
    if (this.rdOnDoc) document.removeEventListener('mousedown', this.rdOnDoc, true);
  }

  // 固定定位小浮层的钳位（气泡卡片篇算法）：锚点下方优先，放不下翻上方。
  #clampPop(el, anchor, width) {
    const a = anchor.getBoundingClientRect();
    const margin = 16, gap = 8;
    const h = el.offsetHeight || 140;
    let left = a.left + a.width / 2 - width / 2;
    left = Math.max(margin, Math.min(left, innerWidth - width - margin));
    const below = a.bottom + gap + h <= innerHeight - margin;
    const top = below ? a.bottom + gap : Math.max(margin, a.top - h - gap);
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
  }

  // 翻转房主对某条消息的一枚回应：在册即撤回（on=false），不在即添上。
  #toggleReact(seq, emoji) {
    const names = this.book.currentRoom()?.reacts.get(seq)?.get(emoji);
    const on = !(names && names.has(this.ownerName || ''));
    this.book.reactTo(this.book.current, seq, emoji, on);
  }

  // 表情快捷盘（飞书式）：常用表情一排，已在消息上用过的亮 mine 态；
  // 点选即翻转自己的回应后收盘。点空白/Esc/换房收盘（读者卡同款纪律）。
  #openReactPop(anchor, frame) {
    this.#closeReactPop();
    this.#closeForwardPop();
    const byEmoji = this.book.currentRoom()?.reacts.get(frame.seq);
    const mine = this.ownerName || '';
    const el = document.createElement('div');
    el.className = 'react-pop';
    el.innerHTML = `<div class="rp-head">${t('表情回应')}<small>${t('回应挂在消息下，不是新消息、不会@人')}</small></div>` +
      '<div class="rp-grid">' + REACT_SET.map((e) => {
        const set = byEmoji?.get(e);
        const on = !!(set && set.has(mine));
        return `<button type="button" class="rp-emoji${on ? ' mine' : ''}" data-emoji="${e}"` +
          ` title="${set?.size ? t('{n} 人已回应', { n: set.size }) + (on ? t('（含我）') : '') : ''}">${e}</button>`;
      }).join('') + '</div>';
    el.addEventListener('click', (ev2) => {
      const b = ev2.target.closest('[data-emoji]');
      if (!b || !frame.seq) return;
      this.#toggleReact(frame.seq, b.dataset.emoji);
      this.#closeReactPop();
    });
    document.body.appendChild(el);
    this.rpEl = el;
    this.#clampPop(el, anchor, 268);
    this.rpOnDoc = (ev2) => { if (!el.contains(ev2.target)) this.#closeReactPop(); };
    document.addEventListener('mousedown', this.rpOnDoc, true);
  }

  #closeReactPop() {
    if (!this.rpEl) return;
    dismiss(this.rpEl, 150);
    this.rpEl = null;
    if (this.rpOnDoc) document.removeEventListener('mousedown', this.rpOnDoc, true);
  }

  // 转发（飞书式转发到其他会话）：挑一个目标办公室，把这条消息以引用
  // 格式落进去。@作者 保留——作者在目标房在座时会被点名（TA 该看见转
  // 发），不在座自然解析不到、不误伤；#seq 不带（跨房 seq 无意义，目
  // 标房的引用跳转走作者＋摘要匹配）。
  #openForwardPop(anchor, frame) {
    this.#closeForwardPop();
    this.#closeReactPop();
    const others = [...this.book.rooms.values()]
      .filter((r) => r.key !== this.book.current)
      .sort((a, b) => (a.key === LobbyKey ? -1 : b.key === LobbyKey ? 1 : a.key.localeCompare(b.key)));
    if (!others.length) {
      this.opts.toast?.show(t('没有其他办公室可转发——先立项一个吧'), 'info');
      return;
    }
    const el = document.createElement('div');
    el.className = 'react-pop fwd-pop';
    el.innerHTML = `<div class="rp-head">${t('转发到…')}<small>${t('以你的发言、引用格式送达')}</small></div>` +
      '<div class="rp-rooms">' + others.map((r) =>
        `<button type="button" class="rp-room" data-key="${esc(r.key)}">` +
          `<span class="avatar xs" style="background:${safeColor(memberColor(r.key))}">${esc(groupAvatarText(r.name))}</span>` +
          `<span class="rp-room-name">${esc(r.name)}</span>` +
          `<span class="rp-room-key">${esc(r.key)}</span></button>`).join('') + '</div>';
    el.addEventListener('click', (ev2) => {
      const b = ev2.target.closest('[data-key]');
      if (!b) return;
      const here = this.book.currentRoom();
      const hereName = here?.name || this.book.current;
      // 结构化转发（根治版）：原文全文作干净正文送达（复制即全文，不再
      // 是掐头的引用串），引用语境走字段——@作者保留（跨房点名照旧），
      // via 缀转发来源，snip 供引用块展示与跳转匹配；存量正文自带的旧
      // 式引用头剥掉不搬。
      const legacy = parseQuote(frame.text || '');
      const fwdText = legacy ? legacy.rest : (frame.text || '');
      this.book.sendTo(b.dataset.key, fwdText || t('（转发自「{room}」）', { room: hereName }),
        { from: frame.from || t('成员'), at: true, snip: snipOf(fwdText), via: hereName });
      const dest = others.find((r) => r.key === b.dataset.key);
      this.opts.toast?.show(t('已转发到「{room}」', { room: dest?.name || b.dataset.key }), 'ok');
      this.#closeForwardPop();
    });
    document.body.appendChild(el);
    this.fwEl = el;
    this.#clampPop(el, anchor, 268);
    this.fwOnDoc = (ev2) => { if (!el.contains(ev2.target)) this.#closeForwardPop(); };
    document.addEventListener('mousedown', this.fwOnDoc, true);
  }

  #closeForwardPop() {
    if (!this.fwEl) return;
    dismiss(this.fwEl, 150);
    this.fwEl = null;
    if (this.fwOnDoc) document.removeEventListener('mousedown', this.fwOnDoc, true);
  }

  // 长内容自动折叠（飞书式）：行入 DOM 后量高——对话气泡/汇报/通知卡
  // /任务描述超线（约 6 行）自动收起成渐隐预览＋「展开」，短内容零铬面。
  // 词汇与量高都在 fold.js（幂等：已定折的不改判）。
  // @param {HTMLElement} row
  #wireFold(row) {
    wireFoldZones(row);
  }

  // ---- 成员问题卡（向房主提问） ------------------------------------------
  // 开放中的提问卡锚在自己的提问 say 行下方（就是那条会话的下一步，
  // 不在流尾另起堆叠）；找不到宿主行（水合早于行渲染、历史截断）才
  // 退回流尾栈。点选只选中、勾选与「其他」自填同录，卡级「提交回答」
  // 一次送全——送出即冻成「已提交」；done 后正文原地翻读档态（选中项
  // 高亮、自填留值）＋「已选」脚注。卡是瞬时铬面（真源 room.questions，
  // question 帧增量 + /questions 冷窗水合），提问的长期留痕是成员自己
  // 的 say 行——重画时整卡搬迁，不动消息流。

  /** @param {import('./rooms.js').Room} room */
  #renderQuestions(room) {
    this.container.querySelectorAll('.q-card').forEach((n) => n.remove());
    this.container.querySelector('.q-stack')?.remove();
    if (this.filter) return; // 搜索态：搜索的对象是消息行，卡栈收起
    const qs = [...room.questions.values()].filter(Boolean);
    if (!qs.length) return;
    const open = qs.filter((q) => !q.status);
    const closed = qs.filter((q) => q.status);
    if (!open.length && !closed.length) return;
    const stack = document.createElement('div');
    stack.className = 'q-stack';
    let tailOpen = false;
    for (const q of [...open, ...closed]) {
      const card = this.#questionCard(room, q);
      const host = this.#askHostRow(q);
      if (host) { // 行内锚定：挂在提问行的 msg-body 底（入口条同位）
        host.querySelector('.msg-body')?.appendChild(card);
        card.classList.add('inline');
      } else {
        stack.appendChild(card);
        if (!q.status) tailOpen = true;
      }
    }
    if (stack.childElementCount) this.container.appendChild(stack);
    // 钉着才送到底；脱钉读历史时新卡照常落在流尾，绝不拉底（提问的
    // say 行已计过浮标，卡本身不 badge）
    if (tailOpen) this.#follow();
  }

  /** 提问卡的宿主行：同成员的提问 say 行里时刻最贴近 q.ts 的那一行
   *  （±5 分钟窗——重连重排不脱锚，隔天的旧问不会错认新行）。
   *  @param {import('../wire/wire.js').Question} q */
  #askHostRow(q) {
    let best = null;
    let bestD = Infinity;
    for (const row of this.container.querySelectorAll('.line.msg.ask-host')) {
      const a = row._askLine;
      if (!a || (a.from || '') !== (q.from || '')) continue;
      const d = Math.abs((a.ts || 0) - (q.ts || 0));
      if (d < 300 && d < bestD) { best = row; bestD = d; }
    }
    return best;
  }

  /** @param {import('./rooms.js').Room} room @param {import('../wire/wire.js').Question} q */
  #questionCard(room, q) {
    const card = document.createElement('div');
    card.className = 'q-card' + (q.status ? ' closed' : '');
    card.dataset.qid = q.id;
    const member = room.members.get(q.from);
    const color = safeColor(member?.color || memberColor(q.from || ''));
    const asks = (q.asks && q.asks.length) ? q.asks
      : [{ question: q.prompt || '请房主补充说明', options: [] }];
    card.innerHTML =
      '<div class="q-head">' +
        `<span class="avatar q-avatar" style="background:${color}">${esc(avatarText(q.from || '?'))}</span>` +
        `<b class="q-from">${esc(q.from || t('成员'))}</b><span class="q-kind">${t('向你提问')}</span>` +
        (q.status ? '' : `<span class="q-due">${esc(dueLabel(q.due))}</span>`) +
      '</div>' +
      (q.prompt && asks.length > 1 ? `<div class="q-lead">${esc(q.prompt)}</div>` : '') +
      asks.map((a, i) => this.#askBlockHTML(a, i)).join('');
    if (q.status) { // 收口态：正文定格为读档——答案在原位留痕（选中的
      // 选项亮 sel、补充文本留在框里只读），脚注「已选」做汇总；选项
      // 不可点、输入不可改（closed 态的选项是记录，不是还能操作的控件）
      asks.forEach((a, i) => {
        const block = card.querySelector(`.q-block[data-qi="${i}"]`);
        if (!block) return;
        const ans = (q.answer?.[a.question] || '').trim();
        // 补充尾巴先剥掉：`<选项部分>；补充：<文本>`——选项部分照拼装
        // 规则对号入座，尾巴文本回输入框
        let selPart = ans;
        let supp = '';
        const cut = ans.indexOf(Q_SUPP);
        if (cut >= 0) {
          selPart = ans.slice(0, cut).trim();
          supp = ans.slice(cut + Q_SUPP.length).trim();
        }
        const labels = (a.options || []).map((o) => o.label);
        // 选项部分判定：单选＝全等某选项；多选＝拆「、」后每一项都是
        // 选项。对不上号（老纯自填答案、或自填文本恰好含分隔符）就整
        // 条进输入框，不错拆
        const fromOpts = !!selPart && (a.multi
          ? selPart.split('、').map((s) => s.trim()).every((s) => labels.includes(s))
          : labels.includes(selPart));
        block.querySelectorAll('.q-opt').forEach((b) => {
          b.disabled = true;
          if (fromOpts && (a.multi
            ? selPart.split('、').map((s) => s.trim()).includes(b.dataset.v)
            : b.dataset.v === selPart)) b.classList.add('sel');
        });
        const inp = block.querySelector('.q-other-input');
        if (inp) {
          inp.readOnly = true;
          if (supp || (ans && !fromOpts)) { // 补充留值；纯自填整条入框
            inp.value = fromOpts ? supp : ans;
            inp.closest('.q-other')?.classList.add('on');
          }
        }
      });
      const foot = document.createElement('div');
      foot.className = 'q-foot';
      if (q.status === 'answered') {
        const picked = asks.map((a) => (q.answer?.[a.question]
          ? `<span class="q-picked">${esc(q.answer[a.question])}</span>` : ''))
          .filter(Boolean).join('');
        foot.innerHTML = icon('check', 12) +
          `<span>${t('已选：{picked}', { picked: picked || '—' })}</span>` +
          `<span class="q-wait">${t('{by} 已应答，{who} 拿着答案继续', { by: esc(q.by || t('房主')), who: esc(q.from || t('成员')) })}</span>`;
      } else {
        foot.innerHTML = icon('clock', 12) +
          `<span>${t('已过期（房主未应答）——可直接 @{who} 补充说明', { who: esc(q.from || t('成员')) })}</span>`;
      }
      card.appendChild(foot);
      return card;
    }
    // 交互态：点选/勾选/补充一律先记账、不发送——卡级「提交回答」
    // 一次送全。单问单选曾走「点选项即答」，房主只想先选中端详一
    // 眼就被秒交，误触收不了场——选中必须能反悔。
    // 两条值来源并存记账：_sel 是点选（.q-opt 点击；单选单值、多选
    // 「、」拼接），_supp 是补充（input 事件实时收）——补充不顶掉
    // 点选、点选不清补充（选「V2」再补「角再圆润一点」是常态诉
    // 求）；两边都没有才缺答，只填补充不点选＝纯自填答案。回车不
    // 提交：补充打到一半的回车（输入法组词尤其频繁）不能把半截话
    // 送出去——发送只有「提交回答」一个入口。
    card._sel = {};
    card._supp = {};
    card._ansOf = (i) => {
      const s = card._sel[i];
      const p = card._supp[i];
      return (s && p) ? s + Q_SUPP + p : (s || p || '');
    };
    const send = document.createElement('button');
    send.type = 'button';
    send.className = 'q-send';
    send.disabled = true;
    send.textContent = t('提交回答');
    card.appendChild(send);
    const trySend = () => {
      const missing = asks.findIndex((a, i) => !card._ansOf(i));
      if (missing >= 0) {
        this.opts.toast?.show(t('还有问题没答完（第 {n} 问）——选个选项或填点补充再提交', { n: missing + 1 }), 'warn');
        return;
      }
      this.#sendAnswer(q, asks, card);
    };
    card.addEventListener('click', (ev) => {
      const opt = ev.target.closest('.q-opt');
      if (opt) {
        const i = +opt.dataset.q;
        if (asks[i].multi) {
          opt.classList.toggle('sel');
          const sel = [...card.querySelectorAll(`.q-opt.sel[data-q="${i}"]`)].map((b) => b.dataset.v);
          if (sel.length) card._sel[i] = sel.join('、');
          else delete card._sel[i];
        } else {
          card.querySelectorAll(`.q-opt.sel[data-q="${i}"]`).forEach((b) => b.classList.remove('sel'));
          opt.classList.add('sel');
          card._sel[i] = opt.dataset.v;
        }
        this.#qSyncSend(card, asks);
        return;
      }
      if (ev.target.closest('.q-send')) trySend();
    });
    card.addEventListener('input', (ev) => { // 补充实时入账：非空即亮边（与点选并存，互不清除）
      const inp = ev.target;
      if (!(inp instanceof HTMLInputElement) || !inp.classList.contains('q-other-input')) return;
      const i = +inp.dataset.q;
      const v = inp.value.trim();
      if (v) card._supp[i] = v; else delete card._supp[i];
      inp.closest('.q-other')?.classList.toggle('on', !!v);
      this.#qSyncSend(card, asks);
    });
    return card;
  }

  /** @param {import('../wire/wire.js').QuestionAsk} a @param {number} i */
  #askBlockHTML(a, i) {
    const opts = (a.options || []).map((o) =>
      `<button type="button" class="q-opt" data-q="${i}" data-v="${esc(o.label)}">` +
        `<span class="q-opt-label">${esc(o.label)}</span>` +
        (o.desc ? `<span class="q-opt-desc">${esc(o.desc)}</span>` : '') +
      '</button>').join('');
    return `<div class="q-block" data-qi="${i}">` +
      (a.header ? `<span class="q-chip">${esc(a.header)}</span>` : '') +
      `<div class="q-q">${esc(a.question)}${a.multi ? `<span class="q-multi">${t('多选')}</span>` : ''}</div>` +
      (opts ? `<div class="q-opts">${opts}</div>` : '') +
      `<div class="q-other"><input type="text" class="q-other-input" data-q="${i}" placeholder="${t('补充说明（可选，可与选项同填）…')}" maxlength="500">` +
      '</div></div>';
  }

  // 提交钮可用态：全部问题都有值才亮（选项或补充任一，ansOf 合流；
  // AskUserQuestion 一次调用一次收口，半答不如不答）。
  #qSyncSend(card, asks) {
    const send = card.querySelector('.q-send');
    if (send) send.disabled = asks.some((a, i) => !card._ansOf(i));
  }

  #sendAnswer(q, asks, card) {
    const answers = {};
    asks.forEach((a, i) => {
      const v = card._ansOf ? card._ansOf(i) : '';
      if (a && v) answers[a.question] = v;
    });
    if (!Object.keys(answers).length) return;
    if (!this.book.answerTo(this.book.current, q.id, answers)) {
      this.opts.toast?.show(t('应答未送出：写通道不可用，或这个问题已应答/已过期'), 'err');
      return;
    }
    // 在场估计的应答腿：服务端 Dispatcher.Answer 会给 hub 落
    // NoteOwnerActivity，等待行同口径记一笔（say 腿走 messages 扫描）。
    this.ownerActAt = Math.floor(Date.now() / 1000);
    // 已提交冻结：送出即把按钮翻成「已提交」、控件全冻——已提交的卡
    // 不能再摆着一副还能提交的面孔（含点提交到 question_done 回执整卡
    // 重画成读档态之间的窗口）；不解冻不回滚——回执与广播才是翻面的
    // 唯一驱动，拒绝（已应答/已过期）走 err toast 说真话
    const send = card.querySelector('.q-send');
    if (send) { send.textContent = t('已提交'); send.disabled = true; }
    card.querySelectorAll('.q-opt').forEach((b) => { b.disabled = true; });
    card.querySelectorAll('.q-other-input').forEach((inp) => { inp.readOnly = true; });
    this.opts.toast?.show(t('已应答——成员拿着你的选择继续干活'), 'ok');
  }

  // ---- row builders ------------------------------------------------------

  /** 有人@我：服务端解析的逐人 mentions 命中房主，或非房主发言里的
   *  全员令牌（@所有人/@全员——bridge 回显豁免，与服务端 summons 门
   *  同源：回显里引用到的令牌不点名全房）。@param {import('../wire/wire.js').Frame} frame */
  #atMe(frame) {
    if (!this.ownerName) return false;
    if ((frame.mentions || []).includes(this.ownerName)) return true;
    return frame.from !== this.ownerName &&
      frame.origin !== Origin.Bridge && ALL_AT_RE.test(frame.text || '');
  }

  #frameRow(frame, bare) {
    const room = this.book.currentRoom();
    const member = room?.members.get(frame.from);
    switch (frame.type) {
      case Msg.Say:
      case Msg.Report:
        return this.#talkRow(frame, member, bare);
      case Msg.System:
        if (frame.event === Event.Interrupted && (frame.interrupted || []).length)
          return this.#interruptedRow(frame);
        return this.#sysLine(frame, frame.text || t('系统消息'));
      case Msg.Join:
        return this.#sysLine(frame, t('{who} 进入办公室', { who: frame.from }));
      case Msg.Leave:
        return this.#sysLine(frame, t('{who} 离开办公室', { who: frame.from }));
      case Msg.Rename:
        return this.#sysLine(frame, frame.text || `${frame.from} → ${frame.to}`);
      case Msg.Task:
      case Msg.Agent:
      case Msg.Kb:
      case Msg.SkillEvt:
      case Msg.McpEvt:
      case Msg.Plan:
      case Msg.Meeting:
      case Msg.Git:
      case Msg.Merge:
        return this.#noticeRow(frame);
      default:
        return null;
    }
  }

  /** 消息头的岗位＋等级小标（标签篇）：牛马真相合成两枚 .tag——岗位
   *  一枚（people 的 role 权威，名册 role 兜底），等级一枚（person.js
   *  rankLabel，与名片卡/名册行同一套词）；等级色与办公室名牌同一副
   *  色阶（index.html 的 --rank-1..9——深色主题照抄名牌、浅色同色相
   *  压暗），房主走 .you 金字槽不叠色；查无此人 → 空，裸名（与旧
   *  bridge/mirror 章缺省同形）。 */
  #headTags(name, member) {
    const p = this.opts.person?.(name || '') || null;
    const role = p?.role || member?.role || '';
    const rank = p && (p.local || p.rank !== undefined) ? rankLabel(p) : '';
    let tags = '';
    if (role) tags += `<span class="tag dim">${esc(role)}</span>`;
    if (rank) {
      const host = p.local || p.rank === 99;
      const rc = !host && p.rank >= 1 && p.rank <= 9 ? ` style="color:var(--rank-${p.rank})"` : '';
      tags += `<span class="tag${host ? ' you' : ''}"${rc}>${esc(rank)}</span>`;
    }
    return tags;
  }

  /** 牛马真相刷新（MembersView 的 /kb/people 轮询回灌）：只补/换已画
   *  消息头里的岗位/等级小标，不整流重画——滚动位置、折叠态、选中态
   *  原样；折叠行无头，展开时自然带新标。 */
  peopleSync() {
    const room = this.book.currentRoom();
    if (!room) return;
    for (const row of this.container.querySelectorAll('.line.msg')) {
      const frame = row._frame;
      const slot = row.querySelector('.msg-head .head-tags');
      if (!frame || !slot) continue;
      const next = this.#headTags(frame.from, room.members.get(frame.from));
      if (slot.innerHTML !== next) slot.innerHTML = next;
    }
  }

  #talkRow(frame, member, bare) {
    const grouped = !bare && !!this.lastTalk &&
      this.lastTalk.from === frame.from &&
      this.lastTalk.type === frame.type &&
      Math.max(0, (frame.ts || 0) - this.lastTalk.ts) < GROUP_WINDOW;
    this.lastTalk = { from: frame.from, type: frame.type, ts: frame.ts || 0 };

    const atMe = this.#atMe(frame);
    // 房主自己的发言（含汇报）披飞书式浅蓝气泡，别人灰泡
    const mine = (frame.type === Msg.Say || frame.type === Msg.Report) &&
      !!this.ownerName && frame.from === this.ownerName;
    const row = document.createElement('div');
    row.className = 'line msg ' + (frame.type === Msg.Report ? 'report' : 'say') +
      (grouped ? ' grouped' : '') + (atMe ? ' at-me' : '') + (mine ? ' mine' : '');
    row._frame = frame; // the hover actions read the whole frame back

    const color = safeColor(member?.color || memberColor(frame.from || ''));
    const badge = this.#headTags(frame.from, member);
    // 折叠行悬停时，头像槽让位给这一条的时刻（飞书式连发回看）；时刻
    // 都带完整日期的 title——消息头里的时分只是折叠视图的省略
    const stamp = fmtDateTime(frame.ts) ? ` title="${fmtDateTime(frame.ts)}"` : '';
    const head = grouped
      ? `<span class="g-ts"${stamp}>${fmtTime(frame.ts)}</span>`
      : `<div class="msg-head"><button type="button" class="who" data-pfat="${esc(frame.from)}" style="color:${color}" title="${t('点名字 @ TA')}">${esc(frame.from)}</button><span class="head-tags">${badge}</span>` +
        `<span class="ts"${stamp}>${fmtTime(frame.ts)}</span></div>`;
    const atTag = atMe ? `<span class="at-tag">${t('有人@我')}</span>` : '';
    // 引用的两代形态归一成同一视图：新消息走结构化字段（frame.quote —
    // 正文天生干净，复制/转发/检索不再把引用头带下来）；旧存量从正文头
    // 的前缀解析（QUOTE_RE 回落，字段照旧喂给下面的引用块）。
    const quote = frame.quote
      ? { seq: frame.quote.seq || 0, at: !!frame.quote.at, from: frame.quote.from || '',
          snip: frame.quote.snip || '', via: frame.quote.via || '', rest: frame.text || '' }
      : parseQuote(frame.text || '');
    const text = quote ? quote.rest : (frame.text || '');
    // 引用块：@名牌（quote.at——引用即点名的回复-mention）亮成回复-点名
    // chip（飞书式「回复 @某人」，发送那一刻 TA 已被服务端按字段/前缀点
    // 名通知）；无 @ 的机械语境头（迟到回答的自动引用）与旧存量裸名照
    // 旧灰字。data-qseq 带原消息 seq，跳转先精确命中，再退回发送者＋摘
    // 要匹配。via（转发自）缀在块尾小字。摘要展示前再过一道 snipOf 的
    // md 剥法——composer 落笔时已剥，这道是给存量历史（裸 **/# 上线前
    // 发的）兜底；data-qsnip 留原文，跳转的摘要匹配仍然贴着原文找。
    const quoteEl = quote
      ? `<div class="msg-quote" data-qjump data-qseq="${quote.seq || ''}" data-qfrom="${esc(quote.from)}" data-qsnip="${esc(quote.snip)}" title="${t('跳到原消息')}${quote.at ? t('（这条回复已 @ 通知 {name}）', { name: esc(quote.from) }) : ''}">` +
        '<span class="q-ico">' + icon('reply') + '</span>' +
        `<span class="q-from${quote.at ? ' q-at' : ''}" data-pfat="${esc(quote.from)}">${esc(quote.at ? `@${quote.from}` : quote.from)}</span>` +
        `<span class="q-snip">${esc(snipOf(quote.snip))}</span>` +
        (quote.via ? `<span class="q-via">${t('转发自「{via}」', { via: esc(quote.via) })}</span>` : '') +
        '</div>'
      : '';
    // 收到钮（飞书式回执）：别人的 say/汇报行才挂——收到答别人，不答
    // 自己；点击走房主写面发 {type:"ack",seq}，回执经广播回到小签。
    const ackBtn = frame.seq && frame.from !== this.ownerName
      ? `<button type="button" data-act="ack">${t('收到')}</button>`
      : '';
    // 表情回应/转发（飞书式）：say/汇报行都可挂——回应连自己的行也行
    // （观点不是回话，与 收到 的规矩分家）；转发把这条以引用格式送进
    // 别的办公室。图标钮与文字钮同条，悬停即现。
    const reactBtn = frame.seq
      ? `<button type="button" data-act="react" title="${t('表情回应')}">` + icon('smile', 13) + '</button>'
      : '';
    const fwdBtn = frame.seq
      ? `<button type="button" data-act="forward" title="${t('转发到其他办公室')}">` + icon('right', 13) + '</button>'
      : '';
    // 操作条住进气泡（飞书式）：CSS 把它锚在气泡右上角——气泡多宽跟到
    // 哪，不再吊在整行右缘（窄泡时右边一整片空地，工具条孤悬远处）
    const actions = '<div class="msg-actions">' +
      `<button type="button" data-act="copy" title="${t('复制原文')}">${t('复制')}</button>` +
      `<button type="button" data-act="quote" title="${t('引用回复（发送将 @ 并通知被引用人）')}">${t('引用')}</button>` +
      reactBtn + fwdBtn + ackBtn +
      '</div>';
    // 长文自动折叠（飞书式，fold.js）：对话与汇报的正文（引用块/文字/
    // 图片都算正文）住进折叠区——插入后 #wireFold 量高，超线（约 6 行）
    // 收起为渐隐预览＋「展开」，短文零铬面。操作条留在折叠区外，收起
    // 态悬停仍可得。
    const body = frame.type === Msg.Report
      // 汇报与发言同一气泡语言（泡色随身份灰/蓝）：泡内小标题＋正文，
      // 小标题留在折叠区外——收起时仍看得见这是汇报
      ? '<div class="bubble"><div class="mc-head">' + icon('megaphone', 14) + ` ${t('工作汇报')}</div>` +
        foldZoneHTML(atTag + richHTML(text)) + actions + '</div>'
      // 飞书式回复：引用块与回文同住一个气泡，泡色随身份（灰/蓝）；
      // bubble-row 让气泡与已读小签并排——我的小签贴泡左、别人的贴泡右
      : '<div class="bubble-row"><div class="bubble">' +
        foldZoneHTML(quoteEl + `<div class="msg-text">${atTag}${richHTML(text)}</div>` + this.#imgsHTML(frame)) +
        actions + '</div></div>';
    // 快捷入口条（编制建议条的泛化）：编制建议（竖线岗位行）照旧出金
    // 钮「加编制」；提案/排期/需求环节的消息再各挂跳转钮——审提案是等
    // 房主的动作所以同为金钮，看排期/看需求只是导航用素钮。同一条 UI、
    // 同一条纪律：聊天侧只送人，动作留在落地面里。
    const estOffers = parseEstOffers(text);
    if (estOffers.length) {
      for (const o of estOffers) o.from = frame.from || '';
    }
    const jumpOffers = parseJumpOffers(text);
    const offerBar = (estOffers.length || jumpOffers.length)
      ? '<div class="est-offer">' +
        `<span class="eo-tag">${icon(jumpOffers.length ? 'right' : 'plus', 12)}${t(jumpOffers.length ? '快捷入口' : '编制建议')}</span>` +
        estOffers.map((o, i) =>
          `<button type="button" class="btn small gold" data-est-offer="${i}"` +
          ` title="${t('跳到 牛马管理 → 项目编制，按建议预填加岗表单——确认即入表')}">${t('加编制 · {name} →', { name: esc(o.name) })}</button>`).join('') +
        jumpOffers.map((o, i) =>
          `<button type="button" class="btn small${o.kind === 'plan' ? ' gold' : ''}" data-jump-offer="${i}"` +
          ` title="${esc(JUMP_META[o.kind].title)}">${esc(o.label)} →</button>`).join('') +
        '</div>'
      : '';
    row.innerHTML =
      `<span class="avatar" data-pfat="${esc(frame.from)}" style="background:${color}" title="${t('{name} 的名片', { name: esc(frame.from) })}">${esc(avatarText(frame.from))}</span>` +
      `<div class="msg-body">${head}${body}${offerBar}</div>`; // xss-ok: body/offerBar 为上方 esc 过的局部拼接
    if (estOffers.length) row._estOffers = estOffers;
    if (jumpOffers.length) row._jumpOffers = jumpOffers;
    // 提问行（向房主提问）：开放问题卡要锚进这一行的行内——记下归属
    if (frame.type === Msg.Say && ASK_LINE_RE.test(text)) {
      row.classList.add('ask-host');
      row._askLine = { from: frame.from || '', ts: frame.ts || 0 };
    }
    return row;
  }

  /** 发言气泡的图片网格（输入图片）：1 张大图、2 张并排、3+ 一排三列
   * （.n1/.n2 由张数定，CSS 收口）。缩略图走 /media/{id}；点开是看图
   * 浮层。空 text 纯图消息的气泡只有网格——正文井留白不占位。
   * @param {import('../wire/wire.js').Frame} frame */
  #imgsHTML(frame) {
    const imgs = frame.images || [];
    if (!imgs.length) return '';
    const cls = imgs.length === 1 ? ' n1' : imgs.length === 2 ? ' n2' : '';
    return `<div class="msg-imgs${cls}">` + imgs.map((im) =>
      `<a href="/media/${esc(im.id)}" target="_blank" rel="noopener" title="${esc(im.name || t('图片'))}">` +
      `<img src="/media/${esc(im.id)}" alt="${esc(im.name || t('图片'))}" loading="lazy"></a>`).join('') + '</div>';
  }

  /** 看图浮层：大图直出（原分辨率，窗口尺寸收口在 CSS max-）。 */
  #viewImage(url) {
    if (!this.imgEl) return;
    this.imgEl.querySelector('img').src = url;
    this.imgEl.hidden = false;
  }

  #closeImage() {
    if (!this.imgEl || this.imgEl.hidden) return;
    this.imgEl.hidden = true;
    this.imgEl.querySelector('img').src = '';
  }

  /** 快捷入口落哪个项目：发在项目房 → 该项目；大厅里的消息按 offer 的
   *  id 找本房见过的帧（提案帧 p_NNN 与其 req=r_NNN、任务帧 t_NNN）拿
   *  project_key，再探不到 → null（落地面按当前项目选择落位）。
   *  @param {import('../wire/wire.js').Frame} frame @param {{kind:string, id:string}} offer */
  #offerProjectKey(frame, offer) {
    const cur = this.book.current;
    if (cur && cur !== LobbyKey && this.book.rooms.has(cur)) return cur;
    const room = this.book.currentRoom();
    if (!room) return null;
    if (offer.kind === 'plan' || offer.kind === 'req') {
      for (const rec of room.messages) {
        const p = rec.frame?.plan;
        if (!p || !p.project_key || p.project_key === LobbyKey) continue;
        if ((offer.kind === 'plan' && p.id === offer.id) ||
            (offer.kind === 'req' && p.req === offer.id)) return p.project_key;
      }
      return null;
    }
    // 编制建议/看排期：按正文提到的任务 id 找任务帧（t_NNN 帧自带项目）
    const ids = [...new Set([...String(frame?.text || '').matchAll(TASK_ID_RE)].map((m) => m[0]))];
    for (const id of ids) {
      for (const rec of room.messages) {
        const t = rec.frame?.task;
        if (t?.id === id && t.project_key && t.project_key !== LobbyKey) return t.project_key;
      }
    }
    return null;
  }

  #noticeRow(frame) {
    const meta = KIND_META[frame.type] || { label: frame.type, icon: 'bell' };
    const event = EVENT_LABEL[frame.event] || frame.event || '';
    const actor = frame.from
      ? ` · <span class="nc-actor" data-pfat="${esc(frame.from)}" title="${t('查看名片')}">${esc(frame.from)}</span>`
      : '';
    let body;
    if (frame.type === Msg.Task && frame.task && frame.event !== 'denied') {
      this.snapshots.set(frame.task.id, frame.task);
      // live card, op buttons wired below — 待确认卡按查看者身份出动词；
      // 超长描述的折叠在卡内自理（tk-desc 即折叠区，操作钮在区外）
      body = taskCardHTML(frame.task, { viewer: this.ownerName || '' });
    } else if (frame.type === Msg.Kb && (frame.diff || []).length) {
      // 黑板写的红绿对比（git 式）：帧自带 diff 行（'-' 删 '+' 增）——
      // 正文不再是孤零零的事件词，改了什么/加了什么当面看得见；无
      // diff（denied 带理由、旧服务端、内容未变的写）走下面的旧渲染。
      // 整篇定稿的写入动辄几十行——超线自动收起，展开看全清单。diff
      // 本身还有百行帽（diff_more），「看全文」是永远在场的逃生口：
      // 整篇 doc 开一张限高自滚的全文卡，读到哪滚到哪——作为区脚
      // 尾件（fold.js 的 tail）与「展开」同排一行，不再各占一行
      body = foldZoneHTML(kbDiffHTML(frame), frame.doc?.key
        ? '<div class="nc-actions"><button type="button" class="btn small" data-kbfull="' +
          esc(frame.doc.key) + `">${t('看全文 →')}</button></div>`
        : '');
    } else if (frame.type === Msg.Git && frame.git) {
      // 提交播报卡（v2.8 gitflow）：谁刚提交了什么、动了哪些文件各红
      // 绿多少行——与黑板 diff 同一「看得见」的纪律；文件多时同款折叠。
      body = foldZoneHTML(gitCommitHTML(frame));
    } else if (frame.type === Msg.Merge && frame.merge && frame.event !== 'denied') {
      // 合并提案卡：分支 → 主线＋红绿合计＋涉及任务号；待审卡给房主
      // 出 验收并入/驳回 按钮（动作在 #onClick，经 frameTo 发 WS——
      // 房主自己的手，不是越权代审）。
      body = mergeCardHTML(frame, { viewer: this.ownerName || '' });
    } else {
      // 纯文本播报同样有长文（denied 的理由、旧格式的整段播报）：
      // 与对话/黑板同一套折叠语言
      body = foldZoneHTML(`<div class="nc-body">${richHTML(frame.text || frame.event || '')}</div>`);
    }
    // 提案 submitted 卡：等待行（时间显示）＋ 直接推进/去审批。等待行亮
    // 出代收的钟——已候多久、开着自动推进时预计何时代收（房主在场让位
    // 中就明说）；首渲染先用已知口径（候时/在场），旋钮拉取与 30s 钟摆
    // 随后补齐。直接推进是房主本人的手立刻放行（跳过宽限与在场让位——
    // 引擎的代行只服务缺席；编排者无权收案，房里喊「推进」接不住的就
    // 是这单），PlanAccept 与审阅浮层的 接受 同一条腿；访客/只读页不
    // 出钮。旧卡点击时提案已审结/被取代 → 落点与回执自会说明。
    if (frame.type === Msg.Plan && frame.event === 'submitted') {
      body += `<div class="nc-wait" data-plan-wait="1">${planWaitText(frame, this.#planWaitState(frame))}</div>`;
      const quick = this.ownerName
        ? `<button type="button" class="btn small gold" data-plan-accept` +
          ` title="${t('跳过宽限立即放行这单提案——与审阅浮层的「接受」同一条腿')}">${t('⚡ 直接推进 →')}</button>`
        : '';
      body += `<div class="nc-actions">${quick}` +
        `<button type="button" class="btn small" data-plan-review>${t('去审批 →')}</button></div>`;
    }
    // 提案 accepted 卡的「看排期」：任务刚落库，排期此刻才真实存在——
    // 跳项目管理的排期甘特（项目随 plan 载荷落位），只读镜面照单全收
    if (frame.type === Msg.Plan && frame.event === 'accepted') {
      body += '<div class="nc-actions"><button type="button" class="btn small" data-plan-gantt' +
        ` title="${t('跳到 项目管理 → 排期甘特，看这批新任务排上之后的整体盘面')}">${t('看排期 →')}</button></div>`;
    }
    const row = document.createElement('div');
    row.className = 'line notice';
    row._frame = frame; // 去审批 读回整帧（project_key 在 plan 载荷里）
    row.innerHTML =
      '<div class="notice-card">' +
        `<div class="nc-head"><span class="nc-icon">${icon(meta.icon, 14)}</span><b>${esc(meta.label)}</b>` +
        (event ? `<span class="nc-event">${esc(event)}</span>` : '') +
        `<span class="nc-actor">${actor}</span>` +
        `<span class="ts"${frame.ts ? ` title="${fmtDateTime(frame.ts)}"` : ''}>${fmtTime(frame.ts)}</span></div>` +
        body +
      '</div>';
    return row;
  }

  // ---- 提案等待行（时间显示＋代收预计）的口径面 ----------------------------

  /** 引擎口径缓存：GET /p/{key}/autopilot 的 auto_advance＋accept_delay
   *  旋钮，60s 热更（旋钮写面下一拍即新值）；拉取失败/访客页回落「只
   *  报候时」——宁缺毋谎。回填后立即 tick 一拍，不等钟摆。 */
  #apOf(key) {
    const hit = this.apInfo.get(key);
    if (hit && (hit.loading || Date.now() - hit.at < 60_000)) return hit;
    const entry = { advance: false, delayS: 0, at: Date.now(), loading: true };
    this.apInfo.set(key, entry);
    getAutopilot(key)
      .then((d) => {
        entry.advance = !!d?.auto_advance;
        entry.delayS = +(d?.knobs?.accept_delay_s || 0) || 0;
      })
      .catch(() => {}) // 读不到就只报候时；下一拍再试
      .finally(() => {
        entry.loading = false;
        this.#tickWaits();
      });
    return entry;
  }

  /** 房主在场估计（服务端 hub.NoteOwnerActivity 的镜像）：本房房主最近
   *  一条 say 的 ts（messages 倒扫——历史回放与实时同一条腿）与点卡
   *  应答落戳（#sendAnswer）取大。汇报/回执/表情不算——服务端也只认
   *  say 与应答两条腿。 */
  #ownerLastActiveS() {
    const room = this.book.currentRoom();
    const owner = this.ownerName;
    let last = this.ownerActAt || 0;
    if (room && owner) {
      for (let i = room.messages.length - 1; i >= 0; i--) {
        const f = room.messages[i]?.frame;
        if (f?.type === Msg.Say && f.from === owner) {
          if ((f.ts || 0) > last) last = f.ts;
          break; // 倒扫首见即最新
        }
      }
    }
    return last;
  }

  /** 本卡的提案还是不是槽里的那单（等待行/直接推进的存续判据）：倒扫
   *  messages，该项目的最新一条 plan 帧才是槽的真相——accepted/denied/
   *  换代 submitted 都算已出槽，历史卡不再摆着钟假装在等。 */
  #planPending(frame) {
    const room = this.book.currentRoom();
    if (!room) return true;
    const key = frame?.plan?.project_key || this.book.current;
    for (let i = room.messages.length - 1; i >= 0; i--) {
      const f = room.messages[i]?.frame;
      if (f?.type !== Msg.Plan) continue;
      if ((f.plan?.project_key || this.book.current) !== key) continue;
      return f.event === 'submitted' && f.plan?.id === frame.plan?.id;
    }
    return true; // 本房没有该项目的 plan 帧记录（窗帽匀走/旧台账）：保守按待审
  }

  /** 等待行的状态包（首渲染与钟摆共用一口井）。 */
  #planWaitState(frame) {
    const key = frame?.plan?.project_key || this.book.current;
    const room = this.book.rooms?.get?.(key) || this.book.currentRoom();
    const ap = this.#apOf(key);
    return {
      now: Math.floor(Date.now() / 1000),
      advance: !!ap.advance,
      acceptDelayS: ap.delayS,
      ownerLastActiveS: this.#ownerLastActiveS(),
      paused: !!(room && room.paused),
      pending: this.#planPending(frame),
    };
  }

  /** 等待行的钟摆（30s，#apOf 回填时也敲一拍）：已候在涨、ETA 在挪、
   *  槽位可能已换代——逐卡重算，文案变了才落笔；已出槽的卡撤行藏钮
   *  （直接推进点了也只会被拒，不如不摆）。 */
  #tickWaits() {
    for (const row of this.container.querySelectorAll('.line.notice')) {
      const f = row._frame;
      if (!f || f.type !== Msg.Plan || f.event !== 'submitted') continue;
      const wait = row.querySelector?.('[data-plan-wait]');
      const txt = planWaitText(f, this.#planWaitState(f));
      if (wait && wait.innerHTML !== txt) wait.innerHTML = txt;
      const btn = row.querySelector?.('[data-plan-accept]');
      if (btn && !btn.disabled) btn.style.display = txt ? '' : 'none';
    }
  }

  #sysLine(frame, text) {
    this.lastTalk = null;
    const row = document.createElement('div');
    row.className = 'line sys-line';
    row.textContent = text;
    // 行背帧：未读锚的行查找（#deliverUnread/#paintUnreadDivider）按
    // _frame 对号——System 也是对话行（红门与 #entryMark 都计入），
    // 锚落在系统行上时行不背帧，「跳到未读」失锚落底、界线随重画消失
    row._frame = frame;
    return row;
  }

  /** 中断接续卡（system+interrupted）：告别快照抓到有人正在干活时
   *  工作室退场——那一轮随子进程死掉、重启不重放，但用户不该靠自己
   *  想起来。重启后服务端已自动逐人点名接续（空车道注入接续线、有停
   *  车道的走车道），这张卡降级为兜底手动通道：名单带每人去向，「让
   *  TA 继续」替房主向 TA 所在的房间再发一条点名（成员会话上下文完
   *  好，点名即接续）；点过的钮翻成待回话态防连点重复点名。席位两人
   *  及以上再挂一枚「全部继续」——一口气把卡里还没点过的席位挨个点名
   *  （已点过的跳过，不重复喊人）。 */
  #interruptedRow(frame) {
    const row = document.createElement('div');
    row.className = 'line notice';
    row._frame = frame;
    const list = frame.interrupted || [];
    const seats = list.map((s) =>
      '<div class="iw-seat">' +
        `<span class="nc-actor" data-pfat="${esc(s.name)}" title="${t('查看名片')}">${esc(s.name)}</span>` +
        (s.room ? `<span class="iw-room">@${esc(s.room)}</span>` : `<span class="iw-room">@${t('Niuma_Studio')}</span>`) +
        `<button type="button" class="btn small gold" data-continue="${esc(s.name)}" data-continue-room="${esc(s.project || LobbyKey)}">${t('让 TA 继续 →')}</button>` +
      '</div>').join('');
    const all = list.length > 1
      ? `<div class="nc-actions"><button type="button" class="btn small gold" data-continue-all>${t('全部继续 →')}</button></div>`
      : '';
    const stamp = frame.ts ? ` title="${fmtDateTime(frame.ts)}"` : '';
    row.innerHTML =
      '<div class="notice-card iw-card">' +
        `<div class="nc-head"><span class="nc-icon">${icon('bolt', 14)}</span><b>${t('有待接续的工作')}</b>` +
        `<span class="ts"${stamp}>${fmtTime(frame.ts)}</span></div>` +
        `<div class="nc-body">${t('上次退出时有成员的活儿干到一半被中断——已自动点名接续；若有人迟迟没接上，可再点名催一次：')}</div>` +
        `<div class="iw-list">${seats}</div>` +
        all +
      '</div>';
    return row;
  }

  /** 席位「让 TA 继续」的共通落点：替房主向 TA 所在的房间发一条点名
   *  （成员会话上下文完好，点名即接续），钮翻待回话态防连点——单点
   *  与「全部继续」走同一道门，喊法一字不差。 */
  #continueSeat(btn) {
    const name = btn.dataset.continue;
    btn.disabled = true;
    btn.textContent = t('已点名，等 TA 回话…');
    this.book.sendTo(btn.dataset.continueRoom || LobbyKey, `@${name} 请继续刚才中断的工作`);
  }

  #dividerEl(text) {
    const el = document.createElement('div');
    el.className = 'time-divider';
    el.textContent = text;
    return el;
  }

  /** A divider between calendar days or after a ≥10min quiet gap. */
  #timeDividerEl(ts) {
    if (!ts) return null;
    const text = this.#dividerText(ts);
    if (!this.lastTs) return this.#dividerEl(text);
    const newDay = new Date(this.lastTs * 1000).toDateString() !== new Date(ts * 1000).toDateString();
    if (newDay || ts - this.lastTs >= GAP_DIVIDER) return this.#dividerEl(text);
    return null;
  }

  #dividerText(ts) {
    const d = new Date(ts * 1000);
    const now = new Date();
    const hm = d.toLocaleTimeString(localeTag(), { hour12: false, hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return hm;
    const yest = new Date(now);
    yest.setDate(now.getDate() - 1);
    if (d.toDateString() === yest.toDateString()) return t('昨天 {hm}', { hm });
    const md = t('{m}月{d}日', { m: d.getMonth() + 1, d: d.getDate() });
    return d.getFullYear() === now.getFullYear() ? `${md} ${hm}` : `${t('{y}年', { y: d.getFullYear() })}${md} ${hm}`;
  }

  // ---- gestures ----------------------------------------------------------

  #onClick(ev) {
    const t = ev.target;
    // 发出的图片（输入图片）：缩略图点开看大图（浮层，点任意处/Esc 关；
    // href 只是语义与右键备用，默认导航拦下走浮层——webview 里开新窗
    // 未必可用）。
    const imgLink = t.closest('.msg-imgs a');
    if (imgLink) {
      ev.preventDefault();
      this.#viewImage(imgLink.href);
      return;
    }
    const filterX = t.closest('[data-filter-x]');
    if (filterX) {
      this.setFilter('');
      this.opts.onSearchClear?.();
      return;
    }
    // 消息头发送者名＝@TA（微信式点名字喊人）：点了直接把 @名字 落进
    // 输入井（opts.mention，app.js 带落板路由），看人请点头像——名片卡
    // 的入口还剩 头像/@令牌/引用者/事件行人名。须先于泛用 [data-pfat]
    // 分支（.who 也带 data-pfat）。
    const who = t.closest('.who[data-pfat]');
    if (who && this.opts.mention) {
      this.opts.mention(who.dataset.pfat);
      return;
    }
    // 牛马表面（头像/@令牌/引用者/事件行人名）→ 共享名片卡；
    // 先于引用跳转等分支——点引用块里的名字是看人，点块其余处才是跳消息
    const pf = t.closest('[data-pfat]');
    if (pf) {
      this.opts.card?.openFor(pf, pf.dataset.pfat);
      return;
    }
    const contAll = t.closest('[data-continue-all]');
    if (contAll) { // 一键全部继续：卡内还没点过的席位挨个点名，自己也翻待回话态
      const rest = contAll.closest('.iw-card')?.querySelectorAll('button[data-continue]:not([disabled])') || [];
      rest.forEach((btn) => this.#continueSeat(btn));
      contAll.disabled = true;
      contAll.textContent = i18n('已全部点名，等回话…');
      return;
    }
    const cont = t.closest('[data-continue]');
    if (cont) { // 中断接续卡：替房主向 TA 所在的房间点一次名；翻待回话态防连点
      this.#continueSeat(cont);
      return;
    }
    const act = t.closest('[data-act]');
    if (act) {
      const f = act.closest('.msg')?._frame;
      if (!f) return;
      if (act.dataset.act === 'copy') this.#copy(f);
      if (act.dataset.act === 'quote') this.opts.quote?.(f);
      if (act.dataset.act === 'ack') this.#ack(f, act);
      if (act.dataset.act === 'react') this.#openReactPop(act, f);
      if (act.dataset.act === 'forward') this.#openForwardPop(act, f);
      return;
    }
    const rchip = t.closest('.react-chip');
    if (rchip) { // 点回应 chip → 翻转自己的那枚回应（再点撤回）
      const f = rchip.closest('.msg')?._frame;
      if (f?.seq) this.#toggleReact(f.seq, rchip.dataset.react);
      return;
    }
    const radd = t.closest('.react-add');
    if (radd) { // ⊕ 增补钮 → 表情快捷盘
      const f = radd.closest('.msg')?._frame;
      if (f?.seq) this.#openReactPop(radd, f);
      return;
    }
    const rl = t.closest('.read-line');
    if (rl) { // 已读小签 → 读者名单卡（悬停已开的原地钉住，未开的即开即钉）
      const f = rl.closest('.msg')?._frame;
      if (f) this.#pinReaders(rl, f);
      return;
    }
    const qj = t.closest('[data-qjump]');
    if (qj) { // 引用块 → 跳回原消息（seq 精确命中，旧格式退回摘要匹配）
      this.jumpToMessage(qj.dataset.qfrom, qj.dataset.qsnip, +qj.dataset.qseq || 0);
      return;
    }
    const fold = t.closest('[data-mcfold]');
    if (fold) { // 折叠触发钮（对话/汇报/通知/任务描述共用）：翻折叠态换文案。
      // 先于 .tk-card 分支——任务描述的钮在卡内，落在卡上不该开弹层
      toggleFoldBtn(fold);
      return;
    }
    const kbfull = t.closest('[data-kbfull]');
    if (kbfull) { // 黑板写播报卡的 看全文：整篇 doc 开全文面板（app.js
      // 拉 /kb/docs/{key} 落 roomview 的 showDoc——与气泡点开同一张卡）
      this.opts.openDoc?.(kbfull.dataset.kbfull);
      return;
    }
    const opBtn = t.closest('.tk-card [data-op]');
    if (opBtn) {
      const id = opBtn.closest('.tk-card')?.dataset.task;
      const task = id && this.snapshots.get(id);
      if (task) this.#taskOp(task, opBtn.dataset.op);
      return;
    }
    const card = t.closest('.tk-card');
    if (card) { // click anywhere else on a live card -> its detail popover
      const snap = this.snapshots.get(card.dataset.task);
      if (snap) this.card.openWith(snap, card);
      return;
    }
    const chip = t.closest('.task-chip');
    if (chip) { this.card.open(chip.dataset.task, chip, this.book.current); return; }
    const advBtn = t.closest('[data-plan-accept]');
    if (advBtn) { // 提案 submitted 卡的 直接推进：房主本人的手立刻放行
      // （跳过宽限与在场让位——引擎的代行只服务缺席，房主在场点一下
      // 就是最快路径）。PlanAccept 与审阅浮层的 接受 同一条腿；防连点，
      // 结果由 plan accepted/denied 帧回流收口（新卡＋回执）。
      const f = advBtn.closest('.line.notice')?._frame;
      const p = f?.plan;
      if (p?.id) {
        const key = p.project_key && this.book.rooms.has(p.project_key)
          ? p.project_key
          : this.book.current;
        advBtn.disabled = true; // 防连点；成败都由广播回流翻卡
        advBtn.textContent = i18n('推进中…');
        this.book.frameTo(key, {
          type: Msg.PlanAccept, plan_id: p.id, project: p.project_key || key,
        }, `${p.id} ${i18n('直接推进')}`);
      }
      return;
    }
    const planBtn = t.closest('[data-plan-review]');
    if (planBtn) { // 提案 submitted 卡的 去审批 → 项目管理的审阅浮层
      const f = planBtn.closest('.line.notice')?._frame;
      if (f) this.opts.openPlan?.(f);
      return;
    }
    const mBtn = t.closest('[data-merge-accept],[data-merge-reject]');
    if (mBtn) { // 合并提案卡的房主动词（v2.8 gitflow）：验收并入/驳回——
      // 按钮只在房主视角的待审卡上出现，点击经 frameTo 发 WS 动词；
      // 结果以 merged/rejected 广播回流（本行重画由 intake 侧负责）。
      const f = mBtn.closest('.line.notice')?._frame;
      const m = f?.merge;
      if (m?.id) {
        const accept = mBtn.hasAttribute('data-merge-accept');
        const key = m.project_key && this.book.rooms.has(m.project_key)
          ? m.project_key
          : this.book.current;
        mBtn.disabled = true; // 防连点；成败都由广播回流翻卡
        mBtn.textContent = accept ? i18n('并入中…') : i18n('驳回中…');
        this.book.frameTo(key, accept
          ? { type: Msg.MergeAccept, merge_id: m.id, project: m.project_key || key }
          : { type: Msg.MergeReject, merge_id: m.id, project: m.project_key || key },
          `${m.id} ${accept ? i18n('验收并入') : i18n('驳回')}`);
      }
      return;
    }
    const ganttBtn = t.closest('[data-plan-gantt]');
    if (ganttBtn) { // 提案 accepted 卡的 看排期 → 项目管理的排期甘特
      const f = ganttBtn.closest('.line.notice')?._frame;
      if (f) this.opts.openJump?.({ kind: 'gantt', id: '', label: i18n('看排期') }, f.plan?.project_key || '');
      return;
    }
    const estBtn = t.closest('[data-est-offer]');
    if (estBtn) { // 编制建议条：跳编制表并按建议预填（确认留在浮层里）
      const msgRow = estBtn.closest('.line.msg');
      const frame = msgRow?._frame;
      const offer = msgRow?._estOffers?.[+estBtn.dataset.estOffer];
      if (offer) this.opts.openEstablish?.(offer, this.#offerProjectKey(frame, offer));
      return;
    }
    const jumpBtn = t.closest('[data-jump-offer]');
    if (jumpBtn) { // 快捷入口条：把人送到对应板块（动作留在落地面里）
      const msgRow = jumpBtn.closest('.line.msg');
      const frame = msgRow?._frame;
      const offer = msgRow?._jumpOffers?.[+jumpBtn.dataset.jumpOffer];
      if (offer) this.opts.openJump?.(offer, this.#offerProjectKey(frame, offer));
      return;
    }
    const row = t.closest('.line.msg');
    if (row) { // 行选中：点行驻留操作条，再点同一行取消
      if (t.closest('button, a, .tk-card, .q-card, .msg-actions, .msg-quote')) return;
      this.#select(row === this.selRow ? null : row);
      return;
    }
    this.#select(null); // 点流内空白处收起选中
  }

  #select(row) {
    if (this.selRow) this.selRow.classList.remove('sel');
    this.selRow = row || null;
    if (row) row.classList.add('sel');
  }

  /**
   * 跳回一条历史发言并一闪标定：seq 在场（新引用格式「引用 #N …」）先
   * 按 seq 精确命中——成员复读机式的重复文本再也绊不倒跳转；未命中或
   * 旧格式退回原算法（发送者＋空白折叠后包含的摘要，倒序找）。引用块
   * 和输入井的引用条都走这里。
   * @param {string} from @param {string} snip @param {number} [seq]
   */
  jumpToMessage(from, snip, seq = 0) {
    const room = this.book.currentRoom();
    if (!room) return;
    let target = null;
    if (seq) {
      for (const rec of room.messages) {
        if (rec.frame && rec.frame.seq === seq) { target = rec.frame; break; }
      }
    }
    if (!target) {
      // 摘要兜底两侧都试：needle 可能是剥过 md 的（snipOf 落笔即剥），
      // 原文命中存量的裸 needle；plainify 后的原文命中跨标记的新 needle
      // （「1. 更大」对「1. **更大**」——剥掉的 ** 夹在中间时不再是
      // 逐字子串）。
      const needle = normText(snip);
      for (let i = room.messages.length - 1; i >= 0; i--) {
        const f = room.messages[i].frame;
        if (!f || (f.type !== Msg.Say && f.type !== Msg.Report)) continue;
        if ((f.from || '') !== (from || '')) continue;
        if (needle && !normText(f.text).includes(needle)
          && !normText(plainifyMD(f.text)).includes(needle)) continue;
        target = f;
        break;
      }
    }
    if (!target) {
      this.opts.toast?.show(t('原消息已滚出最近消息窗口'), 'info');
      return;
    }
    for (const el of this.container.querySelectorAll('.line.msg')) {
      if (el._frame !== target) continue;
      this.#saveBack(); // 跳走前记读位——来路在下，底部浮标指得回来（飞书式返回原位置）
      this.#markFlight(el); // scrollIntoView 的航程记账：在途不清来路
      el.scrollIntoView({ block: 'center' });
      el.classList.remove('flash');
      void el.offsetWidth; // restart the animation on repeat jumps
      el.classList.add('flash');
      setTimeout(() => el.classList.remove('flash'), 1300);
      this.#syncBack();
      return;
    }
    this.opts.toast?.show(t('原消息已滚出最近消息窗口'), 'info');
  }

  /** 搜索态命中高亮：文本节点里包 mark，按钮/链接/已高亮段跳过。 */
  #highlightRow(row) {
    if (!this.filter) return;
    const stems = this.filter.split(/\s+/).filter(Boolean)
      .map((s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    if (!stems.length) return;
    const re = new RegExp(`(${stems.join('|')})`, 'gi');
    const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT, {
      acceptNode: (n) => n.parentElement.closest('button, a, mark')
        ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
    });
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const text = node.nodeValue;
      re.lastIndex = 0;
      if (!re.test(text)) continue;
      re.lastIndex = 0;
      const frag = document.createDocumentFragment();
      let lastIdx = 0, m;
      while ((m = re.exec(text))) {
        if (m.index > lastIdx) frag.appendChild(document.createTextNode(text.slice(lastIdx, m.index)));
        const mark = document.createElement('mark');
        mark.className = 'hit';
        mark.textContent = m[0];
        frag.appendChild(mark);
        lastIdx = m.index + m[0].length;
      }
      if (lastIdx < text.length) frag.appendChild(document.createTextNode(text.slice(lastIdx)));
      node.parentNode.replaceChild(frag, node);
    }
  }

  async #copy(frame) {
    const ok = await copyToClipboard(frame.text || '');
    this.opts.toast?.show(ok ? t('已复制消息') : t('复制失败——可选中消息文字后 ⌘C/Ctrl+C'), ok ? 'ok' : 'err');
  }

  // 回执「收到」（飞书式）：一条 {type:"ack",seq} 走房主写面——不是聊天
  // 消息，不进流、不badge、不会再@任何人。已回执过的行点击无操作。
  #ack(frame, btn) {
    const room = this.book.currentRoom();
    if (!room || !frame.seq) return;
    if (btn?.classList.contains('done')) return;
    if (this.book.ackTo(room.key, frame.seq)) {
      this.opts.toast?.show(t('已回执收到'), 'ok');
      btn?.classList.add('done');
    }
  }

  // Task verbs from the inline cards and the popover: one WS frame over
  // the task's own project room's owner face (the lobby room key stands
  // in for unattached tasks). true = queued, the card closes. NO toast
  // here: every task verb's outcome rides back privately on the same
  // face (denied → red toast, success → green toast in app.js's
  // onOwnerFrame) — toasting at send time painted a green「已发出」next
  // to the red denial whenever the engine refused (the double-popup bug:
  // the host clicking 接单 on a proposal whose Need list names someone
  // else).
  #taskOp(task, op) {
    const mk = {
      confirm: () => [{ type: Msg.TaskConfirm, task_id: task.id }, t('接单')],
      decline: () => [{ type: Msg.TaskDecline, task_id: task.id }, t('婉拒')],
      approve: () => [{ type: Msg.TaskArbitrate, task_id: task.id, approve: true }, t('仲裁批准')],
      reject: () => [{ type: Msg.TaskArbitrate, task_id: task.id, approve: false }, t('仲裁驳回')],
      start: () => [{ type: Msg.TaskUpdate, task_id: task.id, patch: { status: 'doing' } }, t('开始')],
      finish: () => [{ type: Msg.TaskUpdate, task_id: task.id, patch: { status: 'done' } }, t('完成')],
      cancel: () => [{ type: Msg.TaskUpdate, task_id: task.id, patch: { status: 'cancelled' } }, t('取消')],
    }[op];
    if (!mk) return false;
    const [frame, label] = mk();
    const key = task.project_key && this.book.rooms.has(task.project_key)
      ? task.project_key
      : this.book.current;
    return this.book.frameTo(key, frame, `${task.id} ${label}`);
  }

  // The lobby's seatless task face never broadcasts — the outcome frame
  // rides back privately on the write face (server/session.go). Repaint
  // every inline card of that task, and the popover when it sits on the
  // same id, from the fresh snapshot.
  taskOutcome(frame) {
    const t = frame.task;
    if (!t?.id || frame.event === 'denied') return;
    this.snapshots.set(t.id, t);
    for (const card of this.container.querySelectorAll(`.tk-card[data-task="${t.id}"]`)) {
      const wrap = document.createElement('div');
      wrap.innerHTML = taskCardHTML(t, { viewer: this.ownerName || '' });
      const fresh = wrap.firstElementChild;
      card.replaceWith(fresh);
      wireFoldZones(fresh); // 新卡的描述折叠区重量高（入 DOM 后才有高度）
    }
    this.card.repaint(t);
  }

  // ---- the scroll-up pill & the way back ---------------------------------

  /** 底部浮标的 click 门（真监听与测试同一道门），双档：计数档「N 条
   *  新消息 ↓」＝飞书式跳到未读——送到「以下是新消息」界线前落位、
   *  顺着线往下补读，不跳过未读直落底；线已读过（锚在视口上方）或锚
   *  被窗帽匀走才落底兜底。先记来路——顶上的回原位浮标指得回来，跳
   *  转必可逆。回原位档「回到刚才看的位置 ↓」＝直接送回来路。 */
  pillClick() {
    if (this.pillCount) {
      this.#saveBack(); // 程序跳转必可逆：先记来路
      if (!this.#deliverUnread(this.unreadMark, true, false)) {
        this.pinned = true; // 回钉：平滑送底途中新消息也继续跟随
        this.#clearUnread();
        this.#toBottom(true);
      }
      this.#syncBack();
      this.#syncJump();
    } else {
      this.goBack();
    }
  }

  /** 自己发了一条（composer 的 onSent 回调）：发送是拉底手势的等价物
   *  ——钉回去、收浮标、直送到底。读历史时回了句话，视口跟着自己的
   *  话走（飞书式）；拉底前记下来路——顶部浮标指得回读位，跳转可逆。
   *  随后消息帧回流，#follow 顺钉驻底。 */
  ownSent() {
    this.#saveBack();
    this.pinned = true;
    this.#clearUnread();
    this.#toBottom(false);
    this.#syncBack();
    this.#syncJump();
  }

  /** 流的高度变了（「正在处理…」行收放、输入井自增高缩矮、窗口缩
   *  放）：钉着就补一脚送底。ResizeObserver 的兜字底——渲染循环被挂
   *  起（窗口遮挡/最小化）时 RO 不响，改高度的那只手（app.js 的
   *  paintWorkHint、composer 的 #grow）直接喊这一声；窗口 resize 在
   *  构造里也接了同一个口。脱钉读历史时绝不拉底。 */
  relayout() {
    if (!this.pinned || !this.container.clientHeight) return;
    this.#follow();
    this.#syncJump();
  }

  /** 贴底跟随的唯一送底口：钉着才把视口送到流底，脱钉时静默——
   *  「什么都不许把我拉到底」的纪律只认这一个出口。 */
  #follow() {
    if (!this.pinned) return;
    this.#navTo(this.container.scrollHeight, false);
  }

  /** 回原位（飞书式「返回原位置」）：程序跳转把视口挪走后，顶部/底部
   *  浮标指回跳转前的读位。一级即可——再跳再记；用户的滚动手势踩回
   *  来路附近（#syncBack 的 240px 门）浮标自会收，不抢手势的路。 */
  goBack() {
    if (this.backTop === null) return;
    const top = Math.max(0, Math.min(this.backTop, this.container.scrollHeight));
    this.backTop = null;
    this.#navTo(top, true);
    this.#syncBack();
  }

  /** 程序跳转前的读位记账——浮标/圆钮/引用跳转/自己发言拉底都先喊这
   *  一声：跳转必可逆，来路就是回去的路。 */
  #saveBack() {
    this.backTop = this.container.scrollTop;
  }

  /** 程序送位在途记账：scrollIntoView 的落点按几何自算（block:center
   *  的人为语义没有返回值），其余送位走 #navTo 记真目的地。 */
  #markFlight(el) {
    const c = this.container.getBoundingClientRect();
    const r = el.getBoundingClientRect();
    this.flightTop = Math.max(0, this.container.scrollTop + (r.top - c.top) -
      (this.container.clientHeight - r.height) / 2);
    this.flightAt = performance.now();
  }

  #toBottom(smooth) {
    this.#navTo(this.container.scrollHeight, smooth);
  }

  /** 程序送位的唯一大门（#follow/#toBottom/goBack 都从这里走）：先记
   *  航程（在途不判「踩回来路」），再送位。注意 scrollTop 直赋在真浏
   *  器器里未必瞬时落地（布局陈旧时先落半路、后续 append 再追）——视
   *  口会一路穿过回原位的 240px 判定带，没有航程记账就会把刚存的来
   *  路当「已踩回」顺手清掉（回位浮标时隐时现的竞态根因）。 */
  #navTo(top, smooth) {
    this.flightTop = Math.max(0, Math.min(top, this.container.scrollHeight));
    this.flightAt = performance.now();
    if (smooth && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
      this.container.scrollTo({ top: this.flightTop, behavior: 'smooth' });
    } else {
      this.container.scrollTop = this.flightTop;
    }
  }

  #nearBottom(tol = 80) {
    const el = this.container;
    return el.scrollHeight - el.scrollTop - el.clientHeight < tol;
  }

  /** 底部浮标的统一渲染口，双档：计数档「N 条新消息 ↓」（有人@我染红
   *  前缀）永远优先——新消息只会长在尾部，这一档永远指下方；没有新
   *  消息且来路在下方时翻回原位档「回到刚才看的位置 ↓」。 */
  #paintBottomPill() {
    const pill = this.opts.pill;
    if (!pill) return;
    const st = this.container.scrollTop || 0;
    const backBelow = !this.pillCount && this.backTop !== null && this.backTop - st > BACK_NEAR;
    if (!this.pillCount && !backBelow) {
      pill.hidden = true;
      return;
    }
    pill.classList.toggle('atme', this.pillAtMe);
    pill.textContent = this.pillCount
      ? (this.pillAtMe ? t('有人@我') + ' · ' : '') + t('{n} 条新消息', { n: this.pillCount }) + ' ↓'
      : t('回到刚才看的位置') + ' ↓';
    pill.hidden = false;
  }

  /** 未读账清零（视口到尾即读齐）：计数、@我、「以下是新消息」界线
   *  一起收——底部浮标交回 #syncBack/#paintBottomPill 统一翻面。 */
  #clearUnread() {
    this.pillCount = 0;
    this.pillAtMe = false;
    this.unreadMark = null;
    this.#removeUnreadDivider();
    this.#paintBottomPill();
  }

  /** 未读落位（飞书式「跳到未读」的唯一出口）：界线压在锚行前、视口
   *  送到线前（UNREAD_PAD——线停在视口上部一眼可见），往下读即是补
   *  读。送到即应答：浮标计数收（新消息再亮），界线留作读位，读齐到
   *  尾自会清（近底门管着）；不钉——半山落位后新消息照旧只涨浮标，
   *  绝不趁机拉底。锚行不在流里（窗帽匀走）返回 false；锚已在视口上
   *  方且不许落（点浮标时已顺着读过线——没读的只剩尾，落底才顺路）
   *  也交回落底兜底，allowAbove 豁免给进门落位（进门视口本就从底出
   *  发，锚天然在上方）。smooth＝点浮标的平滑飞行；进门是换房重画后
   *  的第一步，直落不演动画。 */
  #deliverUnread(mark, smooth, allowAbove) {
    if (!mark || this.filter) return false;
    this.#removeUnreadDivider();
    let row = null;
    for (const el of this.container.children) {
      if (el._frame && el._frame === mark.frame) { row = el; break; }
    }
    if (!row) return false;
    const c = this.container.getBoundingClientRect();
    const r = row.getBoundingClientRect();
    const rel = r.top - c.top;
    if (rel < 0 && !allowAbove) return false;
    this.unreadMark = mark;
    this.container.insertBefore(this.#unreadDividerEl(), row);
    this.pillCount = 0; // 送到即应答——账交「到尾读齐」收，界线是读位
    this.pillAtMe = false;
    this.pinned = false;
    this.#navTo(Math.max(0, this.container.scrollTop + rel - UNREAD_PAD), smooth);
    return true;
  }

  // 「以下是新消息」界线（微信式）：读历史时未见批次的第一条前压一道
  // 界——比时刻线醒一档（绿的），跳到底回头补读顺着线就是起点。
  #unreadDividerEl() {
    const el = document.createElement('div');
    el.className = 'time-divider unread-divider';
    el.textContent = t('以下是新消息');
    return el;
  }

  /** 界线随锚行复位（'rooms' 保读位整流重画后）：锚记录已被窗帽匀走
   *  就撤线（计数不撤——数的是没看见的，不是还画得出的）。 */
  #paintUnreadDivider(room) {
    this.#removeUnreadDivider();
    if (!this.unreadMark || this.filter) return;
    if (room.messages.lastIndexOf(this.unreadMark) < 0) {
      this.unreadMark = null;
      return;
    }
    for (const el of this.container.children) {
      if (el._frame && el._frame === this.unreadMark.frame) {
        this.container.insertBefore(this.#unreadDividerEl(), el);
        return;
      }
    }
  }

  #removeUnreadDivider() {
    for (const el of [...this.container.children]) {
      if (hasCls(el, 'unread-divider')) el.remove();
    }
  }

  /** 回原位浮标的驻留判定（每次滚动/跳转后跑）：来路在上方（>240px）
   *  才在流顶驻「↑ 回到刚才看的位置」；来路在下方不由它管——底部浮标
   *  的回原位档接手。手势踩回来路附近（240px 门）即清锚收标；程序送位
   *  在途（flightTop）不判——那是飞机没起飞/飞得慢，不是人回来了。 */
  #syncBack() {
    const st = this.container.scrollTop || 0;
    if (this.flightTop !== null) {
      if (Math.abs(st - this.flightTop) <= 80 || performance.now() - this.flightAt > 1500) {
        this.flightTop = null; // 到达（或航程超时强结）——到达门恢复
      }
    } else if (this.backTop !== null && Math.abs(st - this.backTop) <= BACK_NEAR) {
      this.backTop = null; // 已踩回来路（或从没走远）——不必再指路
    }
    const b = this.opts.back;
    if (b) {
      const show = this.backTop !== null && st - this.backTop > BACK_NEAR;
      b.hidden = !show;
      if (show) b.textContent = '↑ ' + t('回到刚才看的位置');
    }
    this.#paintBottomPill();
  }

  /** 回底圆钮随滚动驻留/收起——读历史时随时一键回底，不等新消息。 */
  #syncJump() {
    const j = this.opts.jump;
    if (j) j.hidden = this.#nearBottom();
  }
}

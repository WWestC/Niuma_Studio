// rooms.js — the chat domain's state core (P3-d): one room per active
// project plus the lobby, each with its own observer connection opened
// at boot (switching rooms never drops a stream), its roster, its
// message window and its unread count. The owner write channel hangs
// off the same room record, dialed lazily on first send. Views never
// touch connections — they read RoomBook and listen to its change
// hints: 'rooms' (the set changed) | 'switch' (current moved) |
// 'messages' (lines appended) | 'members' | 'status' | 'unread'.

import { ObserverConn, OwnerChannel } from '../wire/conn.js';
import { getProjects, getPeople, getRoomNotice, getRoomAcks, getRoomReads, getRoomReacts, getRoomQuestions } from '../wire/api.js';
import { t } from '../ui/i18n.js';
import { LobbyKey, Msg, Event, Origin } from '../wire/wire.js';

const MESSAGE_CAP = 300; // the DOM mirror of the room's own window cap

// Reminder-only broadcasts render as event lines but never badge.
const EVENT_FRAMES = new Set([Msg.Task, Msg.Req, Msg.Agent, Msg.Kb, Msg.SkillEvt, Msg.McpEvt, Msg.Plan, Msg.Meeting]);

// 会话列表的摘要文案：一帧一句话。标签与 stream.js 的卡面同义，
// 留一份本地小副本，避免状态层反向引用视图层。摘要行是 esc 过的
// 纯文本，只写文字标签——图标归 stream 卡面的 icon()。
const KIND_LABEL = {
  [Msg.Task]: t('任务'), [Msg.Req]: t('需求'), [Msg.Agent]: t('牛马'), [Msg.Kb]: t('黑板'),
  [Msg.SkillEvt]: t('技能'), [Msg.McpEvt]: t('MCP'), [Msg.Plan]: t('提案'), [Msg.Meeting]: t('会议'),
};

// 全员性令牌（@所有人 点名 / @全员 唤醒）：Go 侧 chat.allMentionKind
// 是带引号/列举标点边界细判的真源，前端认徽标只认裸令牌——宁可多标
// 一枚「有人@我」，不漏一次全房点名。
const ALL_AT_RE = /@(所有人|全员)/;

// RoleStem 的前端同源（Go 侧 chat.RoleStem）：岗位取首个分隔符前的
// 分类段——composer 的「@<岗位>组」候选和服务端岗位群呼解析同一套词表。
// （导出：房间画板认「排期编排」岗也用这套词，不再另写一份。）
const ROLE_SEPS = '·：:（(，,／/　 ';
export function roleStem(role) {
  role = String(role || '');
  for (const sep of ROLE_SEPS) {
    const idx = role.indexOf(sep);
    if (idx > 0) role = role.slice(0, idx);
  }
  return role.trim();
}

/** The tail frame's one-line preview — {text, ts} or null. Say/Report
 * carry a sender prefix（我：/名字：）so a busy room list stays attributable.
 * 带图的 say 给「[图片]」标记（飞书式预览）；纯图消息正文空，全靠它露出。 */
function previewOf(room, ownerName) {
  const who = (f) => (!f.from ? '' : f.from === ownerName ? t('我') : f.from);
  const imgTag = (f) => (f.images && f.images.length ? t('[图片]') : '');
  for (let i = room.messages.length - 1; i >= 0; i--) {
    const f = room.messages[i].frame;
    if (!f) continue;
    switch (f.type) {
      case Msg.Say: return { text: t('{who}：{text}', { who: who(f), text: [f.text || '', imgTag(f)].filter(Boolean).join(' ') }), ts: f.ts };
      case Msg.Report: return { text: t('汇报 · {who}：{text}', { who: who(f), text: f.text || '' }), ts: f.ts };
      case Msg.System: return { text: f.text || t('系统消息'), ts: f.ts };
      case Msg.Join: return { text: t('{who} 进入办公室', { who: f.from }), ts: f.ts };
      case Msg.Leave: return { text: t('{who} 离开办公室', { who: f.from }), ts: f.ts };
      case Msg.Rename: return { text: `${f.from} → ${f.to || ''}`, ts: f.ts };
      default:
        if (EVENT_FRAMES.has(f.type)) {
          return { text: `${KIND_LABEL[f.type] || t('事件')} · ${f.event || ''}`.trim(), ts: f.ts };
        }
    }
  }
  return null;
}

export class RoomBook {
  /**
   * @param {Object} ev
   * @param {(key: string|null, hint: string) => void} ev.onChange
   * @param {(text: string) => void} ev.onNotice
   * @param {(f: import('../wire/wire.js').Frame) => void} [ev.onOwnerFrame] write-face receipts (kick/rank_set system replies, capability outcomes) — the management boards' feedback channel
   * @param {(f: import('../wire/wire.js').Frame, key: string) => void} [ev.onEvent] reminder-only broadcasts (task/agent/kb/capability) from any room — cross-board refresh taps, no badge semantics
   * @param {(f: import('../wire/wire.js').Frame, key: string, backfill: boolean) => void} [ev.onLiveFrame] every intake frame, any room — the room board's live feed (bubbles, roster taps); backfill=true marks replayed gap frames
   * @param {(f: import('../wire/wire.js').Frame, room: Object, atMe: boolean) => void} [ev.onChatLine] 一条会徽标未读的对话行（say/report/system 非 agg）——消息提醒（声音＋弹窗）的落点，与未读徽标同一语义
   * @param {(f: import('../wire/wire.js').Frame, room: Object) => void} [ev.onWorkEvent] 一条会亮工单绿点的变动（task/req 帧带快照、非 backfill、没正看着这间房的办公室）——通知叮的落点，与 workFresh 计数同一道门（声色同门）
   */
  constructor(ev) {
    this.ev = ev;
    this.rooms = new Map();      // key -> room record
    this.projects = null;        // 全量项目清单（refreshRooms 落账；null＝还没读到——项目空间门的判据）
    this.current = LobbyKey;
    this.owner = null;           // {name, role} — the local:true person
    this.visitorToken = '';      // r_19：/view 访客页的 token（观察者 hello 才带；房主窗口恒空）
    this.peopleFallback = [];    // online PersonSummary[] — lobby cold start (frontend 稿 §4.1③)
    this.boardChat = false;      // 对话板块正被路由展示（route() 喂）
    this.chatVisible = false;    // boardChat && 页面未后台——当前房的新消息才算「在场看着」
    this.roomVisible = false;    // 办公室板块正被路由展示（route() 喂）——当前房的任务变动才算「当面看见」（不进绿点、不出通知叮）
  }

  /**
   * Boot: probe the owner, seed the /kb/people fallback, connect every
   * active room, then keep the room set fresh (projects change rarely;
   * P4's verbs are not live yet, so a slow poll is plenty).
   * @param {{name: string, role?: string}} owner
   * @param {string} [visitorToken] r_19 访客 token（/view 分享页传入）——
   *   观察者 hello 带上它才计为访客；房主窗口空串，永不进访客计数
   */
  async start(owner, visitorToken = '') {
    this.owner = owner;
    this.visitorToken = visitorToken;
    try {
      // /kb/people is the studio-wide roster (a project-room seat is
      // online too); the lobby fallback slice keeps to the lobby's own
      // people until the welcome frame lands the true roster.
      this.peopleFallback = (await getPeople())
        .filter((p) => p.online && (p.room || LobbyKey) === LobbyKey);
    } catch { this.peopleFallback = []; }
    await this.refreshRooms();
    window.addEventListener('focus', () => { this.refreshRooms(); });
    setInterval(() => { this.refreshRooms(); }, 60_000);
    // 前后台切换同样动「在场看着」：后台里到达的消息照徽标，回前台
    // （对话板块仍展示）即读——流一直停在底，回来看见就是读过
    document.addEventListener('visibilitychange', () => this.#applySeen());
  }

  async refreshRooms() {
    let list;
    // 读失败不清 projects 账（保留上次已知）：空间门跟着这份清单走，
    // 一次 60s 轮询的网络抖动不该把门判断拍回「未知」
    try { list = await getProjects(); this.projects = list; } catch { list = []; }
    // 大厅条目也从 /projects 的大厅记录带位（autopilot/paused）——本地
    // 合成的裸条目没有这两位，折算循环会把它们拍回 false：暂停后下一
    // 次 60s 轮询「自己恢复」就是这个洞（读失败时保持现状，不拿合成
    // 值覆盖真状态——与 projects 清单本身的读失败纪律同款）。
    const curLobby = this.rooms.get(LobbyKey);
    const lobbyRec = list.find((p) => p && p.key === LobbyKey) || null;
    const active = new Map([[LobbyKey, {
      key: LobbyKey,
      name: t('Niuma_Studio'),
      autopilot: lobbyRec ? !!lobbyRec.autopilot : !!(curLobby && curLobby.autopilot),
      paused: lobbyRec ? !!lobbyRec.paused : !!(curLobby && curLobby.paused),
    }]]);
    for (const p of list) {
      if (p.status === 'active' && p.key !== LobbyKey) active.set(p.key, p);
    }
    let changed = false;
    for (const key of [...this.rooms.keys()]) {
      if (!active.has(key)) {
        const room = this.rooms.get(key);
        room.conn.stop();
        room.writer?.bye();
        this.rooms.delete(key);
        changed = true;
      }
    }
    for (const [key, p] of active) {
      const name = p.name || key;
      if (!this.rooms.has(key)) {
        this.#openRoom(key, name);
        changed = true;
      } else if (this.rooms.get(key).name !== name) {
        this.rooms.get(key).name = name;
        changed = true;
      }
      // 全智能模式标志（/projects 随行）：翻转要触发重渲染——切换栏的
      // ⚡ 小标跟着开关亮灭
      const room = this.rooms.get(key);
      const ap = !!p.autopilot;
      if (room && !!room.autopilot !== ap) {
        room.autopilot = ap;
        changed = true;
      }
      // 房间暂停标志（/projects 随行）：与 autopilot 同款随行——切换栏
      // 的 ⏯ 按钮跟着翻（实时同步走 pause 帧，这里是 60s 轮询的兜底）
      if (room && !!p.paused !== !!room.paused) {
        room.paused = !!p.paused;
        changed = true;
      }
    }
    if (!this.rooms.has(this.current)) {
      this.current = LobbyKey;
      changed = true;
    }
    // a quiet poll must not yank the stream back to the bottom: the
    // 'rooms' hint triggers full re-renders, so emit it only on a real
    // diff (the switcher's badges refresh off 'unread' anyway)
    if (changed) this.#emit(null, 'rooms');
  }

  #openRoom(key, name) {
    const room = {
      key, name,
      status: 'connecting',
      statusTries: 0,          // §4.1④ 重连中的退避序数（0 = 未在重连）
      rosterLoaded: false,     // welcome seen — the roster switches off the fallback
      members: new Map(),      // name -> Member (grace ghosts ride the roster)
      messages: [],            // {frame} | {divider, ts}
      unread: 0,
      atMe: false,             // 有人@房主 while the room sat unfocused (switcher tag)
      entryMark: null,         // 进门落未读的锚（清账前摘的未读尾首记录，#entryMark）——流落位后自清，一次性
      workFresh: 0,            // 没看着这间房时落下的工单变动数（任务/需求，switcher 绿点；进门即看就清）
      autopilot: false,        // 全智能模式运行中（/projects 随行；switcher ⚡ 小标）
      paused: false,           // 房间暂停（welcome 水合＋pause 帧同步＋/projects 随行；switcher 暂停/开始按钮）
      notice: null,            // 该房当前群公告（Notice|null；水合 + notice 帧同步）
      noticeUnread: false,     // 公告更新于本房不在焦点时（switcher 📢 转红＋预览行露摘要）
      acks: new Map(),         // seq -> Set(ackers)：收到回执（HTTP 水合 + ack 帧同步，不进消息流）
      reads: new Map(),        // seq -> Set(readers)：已读回执（同上；读者=回合真正消费了该行的成员——终点才读）
      readSince: 0,            // 已读追踪下限——更早的消息不出小签（宁缺毋谎）
      readsLoaded: false,      // /reads 水合完成——小签只在水合后渲染
      queues: new Map(),       // seq -> Set(who)：排队中投影（同上；成员车道还压着该行——未注入、未读、未丢）
      reacts: new Map(),       // seq -> Map(emoji -> Set(reactors))：表情回应（HTTP 水合 + react 帧同步，不进消息流）
      questions: new Map(),    // qid -> Question（向房主提问）：开放中的选项卡（HTTP 水合 + question 帧同步，不进消息流）；done 后原地标 status 留到换房
      trace: new Map(),        // name -> Map(seq -> TraceEntry)（工作过程）：直播帧按 seq upsert（服务端合并条目带原 seq 全文），快照同一入口；按需水合——抽屉开着才拉
      term: new Map(),         // name -> Map(seq -> TraceEntry)（终端转录 r_17）：同一 upsert 语义，seq 是房间级单调序（合流总览按 seq 归并即时间序）；按需水合——终端板开着才拉
      conn: null,
      writer: null,
    };
    // 群公告水合：HTTP 读面是同步真源，notice 帧只做增量同步
    getRoomNotice(key)
      .then((body) => { room.notice = body?.notice || null; this.#emit(key, 'notice'); })
      .catch(() => {}); // 房间不存在/读失败——置顶条与入口自然不显示
    // 收到回执水合：台账不进聊天历史，冷窗必须拉一次快照才能画出消息下的
    // 「N人收到」小签；读失败静默降级（无小签，功能其余不受影响）
    getRoomAcks(key)
      .then((body) => {
        for (const row of body?.acks || []) {
          if (!row?.seq) continue;
          room.acks.set(row.seq, new Set(row.ackers || []));
        }
        this.#emit(key, 'acks');
      })
      .catch(() => {});
    // 已读回执水合：同收到回执，另带追踪下限 since——下限之前的消息
    // （功能上线前的历史）没有台账，画「未读」就是说谎，不出小签
    getRoomReads(key)
      .then((body) => {
        room.readSince = body?.since || 0;
        for (const row of body?.reads || []) {
          if (!row?.seq) continue;
          room.reads.set(row.seq, new Set(row.readers || []));
        }
        for (const row of body?.queues || []) {
          if (!row?.seq) continue;
          room.queues.set(row.seq, new Set(row.who || []));
        }
        room.readsLoaded = true;
        this.#emit(key, 'reads');
        this.#emit(key, 'queues');
      })
      .catch(() => { // 读面不在（老服务端）：水合完成但下限拉满，永不画签
        room.readsLoaded = true;
        room.readSince = Number.MAX_SAFE_INTEGER;
      });
    // 表情回应水合：同收到回执——台账不进聊天历史，冷窗拉一次快照才能
    // 画出消息下的回应 chips；读失败静默降级（无 chips，功能其余不受影响）
    getRoomReacts(key)
      .then((body) => {
        for (const row of body?.reacts || []) {
          if (!row?.seq) continue;
          const byEmoji = new Map();
          for (const set of row.reactions || []) {
            if (set?.emoji) byEmoji.set(set.emoji, new Set(set.names || []));
          }
          if (byEmoji.size) room.reacts.set(row.seq, byEmoji);
        }
        this.#emit(key, 'reacts');
      })
      .catch(() => {});
    // 开放提问水合：问题帧不进聊天历史，冷窗必须拉一次开放集才能画出
    // 成员问题卡（向房主提问）；读失败静默降级（无卡，房主仍可 @成员 口头答）
    getRoomQuestions(key)
      .then((body) => {
        for (const q of body?.questions || []) {
          if (q?.id) room.questions.set(q.id, q);
        }
        this.#emit(key, 'questions');
      })
      .catch(() => {});
    room.conn = new ObserverConn(key, {
      onFrame: (f) => this.#intake(room, f, false),
      onBackfill: (list, initial) => this.#intakeBackfill(room, list, initial),
      onState: (mode, tries) => {
        room.status = mode;
        room.statusTries = tries || 0; // §4.1④ 重连中(第 N 次退避)的 N
        this.#emit(key, 'status');
      },
      onNotice: (t) => this.ev.onNotice(t),
    }, this.visitorToken);
    if (this.owner) {
      room.writer = new OwnerChannel(key, this.owner, {
        onNotice: (t) => this.ev.onNotice(t),
        onFrame: (f) => {
          // 应答回执（question_done）同时折进本房状态——观察连接若正逢
          // 重连错过广播，收据这一份也能把卡翻成已选态
          if (f.type === Msg.QuestionDone && f.qid) this.#intake(room, f, false);
          this.ev.onOwnerFrame?.(f);
        },
      });
    }
    room.conn.start();
    this.rooms.set(key, room);
  }

  switchTo(key) {
    if (!this.rooms.has(key) || key === this.current) return;
    this.current = key;
    const room = this.rooms.get(key);
    if (this.chatVisible) { // 对话面在场才谈得上进门即读——在别的
      // 板块点侧栏换房，人还没来看流，未读与@我留着才诚实
      room.entryMark = this.#entryMark(room); // 清账前摘锚：流好把视口送到第一条未读前（飞书式进门落未读，不一杆子捅到底）
      room.unread = 0;
      room.atMe = false; // 进门即读：@我 标与未读一起清
      room.noticeUnread = false; // 公告红标同理——置顶条进门即见
    }
    if (this.roomVisible) {
      // 同一条纪律的工单面：正看着办公室换房，新屋的任务纸当面打出来，
      // 绿点进门即清；在别的板块换房则留着——人还没见过那间屋的纸
      room.workFresh = 0;
    }
    this.#emit(null, 'switch');
    this.#emit(null, 'unread');
  }

  /** 对话板块可见性（app.js 的 route() 喂）：板块在视窗里且页面未后台
   *  时，当前房的新消息就是「在场看着」——不徽标；否则（看别的板块/
   *  整个应用后台）当前房的新消息照常计未读与@我，牛马对话导航上
   *  才有数可显——且只在导航徽标一处亮：切换栏的当前房行签豁免
   *  （switcher 渲染时跳过 r.current），正坐的屋不在门口挂「有信」。
   *  进面对读：流一直停在底，回来看见即清。 */
  setChatVisible(v) {
    this.boardChat = !!v;
    this.#applySeen();
  }

  /** 办公室板块可见性（app.js 的 route() 喂）：板块在视窗里时，当前房的
   * 工单变动（任务/需求）就是「当面看见」——任务单纸打出来、氛围叮响，
   * 不进绿点也不出通知叮（#intake 的声色同门关着）；落到办公室板块当场
   * 清当前房的 workFresh——进门即看，绿点没有存在的理由。 */
  setRoomVisible(v) {
    this.roomVisible = !!v;
    if (this.roomVisible) {
      const room = this.rooms.get(this.current);
      if (room && room.workFresh) {
        room.workFresh = 0;
        this.#emit(null, 'unread');
      }
    }
  }

  #applySeen() {
    const vis = this.boardChat && !document.hidden;
    if (vis === this.chatVisible) return;
    this.chatVisible = vis;
    if (vis) {
      const room = this.rooms.get(this.current);
      if (room && (room.unread || room.atMe)) {
        room.entryMark = this.#entryMark(room); // 同一道清账的另一只手：回对话面也先摘锚——流落未读，不一杆子捅到底
        room.unread = 0;
        room.atMe = false;
        this.#emit(null, 'unread');
      }
    }
  }

  /** 进门落未读的锚（清账前摘）：未读尾巴的第一条记录——与徽标同一道
   *  红门算题的逆走（从尾往前数对话行，数满 unread 条即锚）。流拿它压
   *  「以下是新消息」界线、把视口送到线前。窗帽匀掉了头（数不满）返回
   *  null，流自己回落底兜底。 */
  #entryMark(room) {
    let left = room.unread;
    for (let i = room.messages.length - 1; i >= 0 && left > 0; i--) {
      const f = room.messages[i]?.frame;
      // 与 #intake 的红点同门：Say/Report/System 才是对话行，聚合行与
      // 重建公告（别房的话已各自计过账）不入
      if (f && (f.type === Msg.Say || f.type === Msg.Report || f.type === Msg.System) &&
          !(f.type === Msg.System && (f.event === Event.Agg || f.event === Event.Rebuild))) {
        left -= 1;
        if (!left) return room.messages[i];
      }
    }
    return null;
  }

  currentRoom() { return this.rooms.get(this.current) || null; }

  /** Ordered switcher rows: the lobby first, projects by key. Rows carry
   * the last frame's timestamp (feeds the row's time corner only — the
   * switcher doesn't show chat-content previews) and the roster headcount
   * (0 while unwelcomed) — the Feishu conversation-list face. */
  roomList() {
    return [...this.rooms.values()]
      .sort((a, b) => (a.key === LobbyKey ? -1 : b.key === LobbyKey ? 1 : a.key.localeCompare(b.key)))
      .map((r) => ({
        key: r.key, name: r.name, unread: r.unread, atMe: r.atMe,
        workFresh: r.workFresh,
        current: r.key === this.current, status: r.status,
        autopilot: !!r.autopilot,
        paused: !!r.paused,
        count: r.rosterLoaded ? r.members.size : 0,
        last: previewOf(r, this.owner?.name || null),
        notice: !!r.notice, noticeUnread: r.noticeUnread,
        // 公告首行摘要（置顶条同款取法）：未读时预览行直接露出内容——
        // 「本房有公告」不能只靠一枚小图标自证
        noticeText: String(r.notice?.content || '').split('\n').map((l) => l.trim()).find(Boolean) || t('（空公告）'),
      }));
  }

  /** @补全名单：当前办公室在线∪宽限（roster）— the same set the server resolves @ against. */
  mentionCandidates() {
    const room = this.currentRoom();
    if (!room) return [];
    return [...room.members.values()]
      .filter((m) => m.name !== this.owner?.name);
  }

  /** 「@<岗位>组」群呼候选：当前名册按岗位分类段（roleStem）分组，
   *  排除房主与空岗位——可达面与服务端 groupWakeLocked 一致。
   *  @returns {{stem: string, name: string, count: number}[]} */
  groupCandidates() {
    const room = this.currentRoom();
    if (!room) return [];
    const groups = new Map(); // stem -> 成员数
    for (const m of room.members.values()) {
      if (m.name === this.owner?.name) continue;
      const s = roleStem(m.role);
      if (!s) continue;
      groups.set(s, (groups.get(s) || 0) + 1);
    }
    return [...groups.entries()]
      .sort((a, b) => a[0].localeCompare(b[0], 'zh-CN'))
      .map(([stem, count]) => ({ stem, name: `${stem}组`, count }));
  }

  /** Sidebar cold start: the roster once welcomed, else the /kb/people online slice (lobby only). */
  sidebarRoster() {
    const room = this.currentRoom();
    if (!room) return [];
    if (room.rosterLoaded) return [...room.members.values()];
    if (room.key === LobbyKey) {
      return this.peopleFallback.map((p) =>
        ({ name: p.name, color: p.color, role: p.role, online: true, grace: p.grace }));
    }
    return [];
  }

  /** Speak into the current room as the owner (the write face dials itself).
   * images（输入图片）随行——已上传的媒体引用；quote（结构化引用回复）
   * 以字段随行——正文保持干净，见 wire.conn.say。 */
  send(text, images, quote) {
    const room = this.currentRoom();
    if (!room) return;
    if (!room.writer) {
      this.ev.onNotice(t('未识别到房主身份（/kb/people 无 local:true），只读模式'));
      return;
    }
    room.writer.say(text, images, quote);
  }

  /**
   * Speak into ONE named room as the owner — the project board's
   * 「让编排者拆解」 path fires into the requirement's own room even
   * while the chat board sits elsewhere. quote（结构化引用，转发的
   * 载体）以字段随行。
   * @param {string} key @param {string} text
   * @param {import('../wire/wire.js').Quote} [quote]
   */
  sendTo(key, text, quote) {
    const room = this.rooms.get(key);
    if (!room || !room.writer) {
      this.ev.onNotice(t('未识别到房主身份（/kb/people 无 local:true），只读模式'));
      return;
    }
    room.writer.say(text, undefined, quote);
  }

  /**
   * Queue one task/plan management frame over ONE named room's owner
   * face — task verbs broadcast into the task's project room and the
   * plan gate defaults its project to the dialed room, so the project
   * board speaks through the room it is viewing. Receipts (denied task
   * frames, plan outcomes) arrive via onOwnerFrame.
   * @param {string} key
   * @param {import('../wire/wire.js').Frame} frame
   * @param {string} [label]
   * @returns {boolean} false = no owner write face (read-only window)
   */
  frameTo(key, frame, label) {
    const room = this.rooms.get(key);
    if (!room?.writer) {
      this.ev.onNotice(t('未识别到房主身份（/kb/people 无 local:true），管理操作不可用'));
      return false;
    }
    room.writer.send(frame, label);
    return true;
  }

  /**
   * 以房主名义对某房的一条 say 消息回「收到」（飞书式回执，非聊天
   * 消息）：走该房的房主写面；回执经 ack 广播回到小签，不进消息流。
   * @param {string} key @param {number} seq
   * @returns {boolean} false = no owner write face (read-only window)
   */
  ackTo(key, seq) {
    if (!seq) return false;
    return this.frameTo(key, { type: Msg.Ack, seq }, t('收到'));
  }

  /**
   * 以房主名义对某房的一条 say 消息回表情（飞书式回应，非聊天消息）：
   * 走该房的房主写面；事实经 react 广播回到 chips，不进消息流、不会
   * 唤醒任何人。on=false 撤回自己的回应。
   * @param {string} key @param {number} seq @param {string} emoji @param {boolean} on
   * @returns {boolean} false = no owner write face (read-only window)
   */
  reactTo(key, seq, emoji, on) {
    if (!seq || !emoji) return false;
    return this.frameTo(key, { type: Msg.React, seq, emoji, on }, t('回应'));
  }

  /**
   * 应答成员的开放提问（向房主提问）：把房主在问题卡上点选/填写的
   * 答案经房主写面送回——该房调度器把悬着的 AskUserQuestion 调用当场
   * 唤醒、成员拿着答案继续干活。
   * @param {string} key @param {string} qid
   * @param {Object.<string, string>} answers 问题文本 → 所选 label 或自填文本
   * @returns {boolean} false = no owner write face (read-only window)
   */
  answerTo(key, qid, answers) {
    if (!qid) return false;
    return this.frameTo(key, { type: Msg.Answer, qid, answer: answers }, t('应答提问'));
  }

  /**
   * Send one management frame (kick / rank_set / agent_archive /
   * assemble / skill_save) over the LOBBY write face — those
   * verbs are lobby-anchored (the seatless mirror channel) and the
   * assemble/skill_save verbs take their project explicitly in
   * the payload. Receipts arrive via onOwnerFrame.
   * @param {import('../wire/wire.js').Frame} frame
   * @param {string} [label]
   * @returns {boolean} false = no owner write face (read-only window)
   */
  ownerSend(frame, label) {
    const room = this.rooms.get(LobbyKey);
    if (!room?.writer) {
      this.ev.onNotice(t('未识别到房主身份（/kb/people 无 local:true），管理操作不可用'));
      return false;
    }
    room.writer.send(frame, label);
    return true;
  }

  /** Polite teardown for pagehide — releases project-room seats at once. */
  byeAll() { for (const room of this.rooms.values()) room.writer?.bye(); }

  #intakeBackfill(room, list, initial) {
    if (!list.length) return;
    // 初次水合不落「断线补窗」——那是重连语境的词，开页载历史就是常态
    if (!initial) room.messages.push({ divider: t('断线补窗 {n} 条', { n: list.length }), ts: list[0].ts });
    for (const f of list) this.#intake(room, f, true);
    room.messages.splice(0, Math.max(0, room.messages.length - MESSAGE_CAP));
  }

  #intake(room, frame, fromBackfill) {
    this.ev.onLiveFrame?.(frame, room.key, fromBackfill);
    switch (frame.type) {
      case Msg.Welcome:
        room.rosterLoaded = true;
        // 房间暂停水合：welcome 带当前态（迟到窗口一见即冻结办公室）
        if (!!room.paused !== !!frame.paused) {
          room.paused = !!frame.paused;
          this.#emit(room.key, 'pause');
        }
        if (!fromBackfill) { // the roster is welcome-authoritative
          room.members.clear();
          for (const m of frame.members || []) room.members.set(m.name, m);
          this.#emit(room.key, 'members');
        }
        return;
      case Msg.MemberWork: { // dispatcher turn tell: stamp the roster row (no stream line)
        const wm = room.members.get(frame.from);
        if (wm) {
          wm.working = !!frame.working;
          wm.working_since = frame.working ? (frame.since || 0) : 0;
          this.#emit(room.key, 'members');
        }
        return;
      }
      case Msg.Join:
        if (frame.member) {
          room.members.set(frame.member.name, frame.member);
          if (!fromBackfill) this.#emit(room.key, 'members');
        }
        break;
      case Msg.Leave:
        room.members.delete(frame.from);
        if (!fromBackfill) this.#emit(room.key, 'members');
        break;
      case Msg.Rename:
        if (!fromBackfill && room.members.has(frame.from)) {
          const m = room.members.get(frame.from);
          room.members.delete(frame.from);
          m.name = frame.to;
          room.members.set(frame.to, m);
          this.#emit(room.key, 'members');
        }
        break;
      case Msg.Say:
      case Msg.Report:
      case Msg.System:
        // chat lines badge the room unless its stream is in view (the
        // chat board shows this room AND the page is foreground) —
        // except the aggregator's roll-ups (event:"agg": they summarize
        // OTHER rooms' speech that already badged its own room, so
        // counting them here would redden 大厅 for every project talk)
        // and rebuild announcements (event:"rebuild": studio-level news
        // landing in every room at once — a recompile must not redden
        // every project's dot, twice per restart)
        if (!(room.key === this.current && this.chatVisible) && !fromBackfill &&
            !(frame.type === Msg.System && (frame.event === Event.Agg || frame.event === Event.Rebuild))) {
          room.unread += 1;
          // 有人@我：逐人 mentions（服务端解析），或非房主发言里的
          // 全员令牌——房主豁免点名，但该看见「有人@了全房」。全员令牌
          // 兜底豁免 bridge 回显（与服务端 summons 门同一条纪律：回显
          // 是交付物不是新语境，其中「引用到的」@所有人 不点名全房）。
          let atMe = false;
          if (frame.type !== Msg.System && this.owner &&
              frame.from !== this.owner.name &&
              ((frame.mentions || []).includes(this.owner.name) ||
               (frame.origin !== Origin.Bridge && ALL_AT_RE.test(frame.text || '')))) {
            room.atMe = true;
            atMe = true;
          }
          this.#emit(room.key, 'unread');
          // 消息提醒钩子（声音＋弹窗，app.js 接到 MsgPop）：与未读徽标
          // 同一道门——不在眼前看着的对话才算新动静，backfill（断线
          // 补窗/开页载历史）与聚合行天然不进来
          this.ev.onChatLine?.(frame, room, atMe);
        }
        break;
      case Msg.Pause:
        // 房间暂停同步帧（不进消息流、不badge——留痕走 system 行）：
        // 翻转 room.paused，办公室小人定格/解冻、切换栏按钮翻面。
        if ((frame.event === 'paused') !== !!room.paused) {
          room.paused = frame.event === 'paused';
          this.#emit(room.key, 'pause');
        }
        return;
      case Msg.Notice:
        // 群公告数据同步帧（不进消息流——留痕走 system 行）：跟新
        // room.notice；有公告且本房不在焦点时亮 📌 红标，撤下不亮
        // （没有可读的公告了）。
        room.notice = frame.event === 'cleared' ? null : (frame.notice || null);
        room.noticeUnread = room.key !== this.current && !!room.notice;
        this.#emit(room.key, 'notice');
        this.#emit(room.key, 'unread');
        return;
      case Msg.Ack:
        // 收到回执增量（不进消息流、不badge——回执是原消息的事实，
        // 不是新消息）：fold 进 room.acks，视图只刷小签。
        if (frame.from && Array.isArray(frame.seqs)) {
          for (const seq of frame.seqs) {
            if (!seq) continue;
            if (!room.acks.has(seq)) room.acks.set(seq, new Set());
            room.acks.get(seq).add(frame.from);
          }
          this.#emit(room.key, 'acks');
        }
        return;
      case Msg.React:
        // 表情回应增量（不进消息流、不badge——回应是原消息的事实，
        // 不是新消息）：fold 进 room.reacts，视图只刷 chips。
        if (frame.from && frame.seq && frame.emoji) {
          const byEmoji = room.reacts.get(frame.seq) || new Map();
          const names = byEmoji.get(frame.emoji) || new Set();
          if (frame.on) names.add(frame.from);
          else names.delete(frame.from);
          if (names.size) byEmoji.set(frame.emoji, names);
          else byEmoji.delete(frame.emoji);
          if (byEmoji.size) room.reacts.set(frame.seq, byEmoji);
          else room.reacts.delete(frame.seq);
          this.#emit(room.key, 'reacts');
        }
        return;
      case Msg.Read:
        // 已读回执增量（同收到回执的纪律）：from 的回合终点冲账了
        // seqs 里的每一条 say（终点才读——注入只是送达）。fold 进
        // room.reads，视图只刷小签。
        if (frame.from && Array.isArray(frame.seqs)) {
          for (const seq of frame.seqs) {
            if (!seq) continue;
            if (!room.reads.has(seq)) room.reads.set(seq, new Set());
            room.reads.get(seq).add(frame.from);
          }
          this.#emit(room.key, 'reads');
        }
        return;
      case Msg.Queue:
        // 排队中投影（全态替换，无增减歧义）：from 的车道此刻恰好压
        // 着 seqs 这些 say（空 = 排空）。fold 进 room.queues，视图只刷
        // 小签的「排队中」档与侧栏徽标——不进消息流、不badge。
        if (frame.from) {
          for (const [seq, set] of room.queues) {
            set.delete(frame.from);
            if (!set.size) room.queues.delete(seq);
          }
          for (const seq of frame.seqs || []) {
            if (!seq) continue;
            if (!room.queues.has(seq)) room.queues.set(seq, new Set());
            room.queues.get(seq).add(frame.from);
          }
          this.#emit(room.key, 'queues');
        }
        return;
      case Msg.Question:
        // 开放提问（不进消息流、不badge——提问的留痕是成员自己的 say
        // 行）：fold 进 room.questions，视图只画/更新问题卡。
        if (frame.qid && frame.question) {
          room.questions.set(frame.qid, frame.question);
          this.#emit(room.key, 'questions');
        }
        return;
      case Msg.QuestionDone:
        // 提问收口（answered|expired）：原地标 status，卡面翻成已选/
        // 已过期态；closed 条目留到换房整流时消失——卡是瞬时铬面，
        // 长期留痕在提问行与成员拿着答案的后续回复里。
        if (frame.qid) {
          const q = room.questions.get(frame.qid);
          if (q) {
            q.status = frame.qstatus || 'expired';
            q.answer = frame.answer || null;
            q.by = frame.from || '';
            this.#emit(room.key, 'questions');
          }
        }
        return;
      case Msg.Trace:
        // 工作过程增量（不进消息流、不badge——展示是抽屉开着才有的事）：
        // 按 seq upsert 进 room.trace（服务端 fold 保证：合并的文本条目
        // 与回填的工具卡都带「原 seq + 全量字段」，upsert 即无损）。
        if (this.foldTrace(room.key, frame.trace)) {
          this.#emit(room.key, 'trace');
        }
        return;
      case Msg.Term:
        // 终端转录增量（r_17，trace 帧同款纪律：不进消息流、不badge、
        // 不唤醒）：同一 upsert 语义进 room.term，只有终端板开着才有人
        // 消费这个 hint。
        if (this.foldTerm(room.key, frame.trace)) {
          this.#emit(room.key, 'term');
        }
        return;
      case Msg.FsDirty:
        // 工作区变更推送（文件板自动刷新的服务端半边）：帧带 project
        // （哪个工作区动了），每间房都收——正浏览该项目的文件板才消费。
        // 不进消息流、不badge、不唤醒；onFsDirty 拿到项目键自己取舍。
        this.ev.onFsDirty?.(frame.project);
        return;
      case Msg.Task:
      case Msg.Req:
        // 工单变动绿点（声色同门）：任务或需求的真变动（带快照——任务
        // 在 frame.task、需求在 frame.req；直播非补窗）落在「没正看着
        // 这间房的办公室」时，计一枚 workFresh＋放行 onWorkEvent（通知
        // 叮）——点与声同一道 if；看着办公室的场合门关（任务纸当面打出
        // 来、氛围叮归房间画板），不看点也不计：看见了的变动不该再报信
        if (!fromBackfill && (frame.task || frame.req) &&
            !(room.key === this.current && this.roomVisible && !document.hidden)) {
          room.workFresh += 1;
          this.#emit(room.key, 'unread');
          this.ev.onWorkEvent?.(frame, room);
        }
        this.ev.onEvent?.(frame, room.key); // cross-board refresh tap
        break; // event lines, no red badge（红点是对话的语言，工单是绿点）
      case Msg.Agent:
      case Msg.Kb:
      case Msg.SkillEvt:
      case Msg.McpEvt:
      case Msg.Plan:
      case Msg.Meeting:
        this.ev.onEvent?.(frame, room.key); // cross-board refresh tap
        break; // event lines, no badge
      default:
        return;
    }
    room.messages.push({ frame });
    while (room.messages.length > MESSAGE_CAP) room.messages.shift();
    this.#emit(room.key, 'messages');
  }

  /**
   * 工作过程的唯一 fold 口：直播帧与快照水合同一套语义——按 seq
   * upsert（新条目插入、文本合并/工具回填原位替换），Map 的插入序即
   * 时间线序。单成员帽比服务端环宽一格（300），溢出丢最旧。
   * @param {string} key room key
   * @param {import('../wire/wire.js').TraceEntry[]} entries
   * @returns {boolean} 是否有落账（帧为空/全脏时 false，不空发 hint）
   */
  foldTrace(key, entries) {
    const room = this.rooms.get(key);
    if (!room || !Array.isArray(entries)) return false;
    let changed = false;
    for (const e of entries) {
      if (!e?.from || !e.kind || !e.seq) continue;
      let ring = room.trace.get(e.from);
      if (!ring) {
        ring = new Map();
        room.trace.set(e.from, ring);
      }
      ring.set(e.seq, e);
      while (ring.size > 300) ring.delete(ring.keys().next().value);
      changed = true;
    }
    return changed;
  }

  /**
   * 一名成员的工作过程时间线（seq 升序的浅拷贝数组）——抽屉的读口。
   * @param {string} key room key @param {string} name member
   * @returns {import('../wire/wire.js').TraceEntry[]}
   */
  traceOf(key, name) {
    const room = this.rooms.get(key);
    const ring = room?.trace.get(name);
    return ring ? [...ring.values()] : [];
  }

  /**
   * 终端转录的唯一 fold 口（r_17）：与 foldTrace 同一套 seq upsert 语义
   * （服务端 term 冲水带的合并文本/工具回填条目都携原 seq 全量字段）。
   * 单成员帽 3000（比 trace 宽一个量级——终端是史册不是速览）。
   * @param {string} key room key
   * @param {import('../wire/wire.js').TraceEntry[]} entries
   * @returns {boolean} 是否有落账（帧为空/全脏时 false，不空发 hint）
   */
  foldTerm(key, entries) {
    const room = this.rooms.get(key);
    if (!room || !Array.isArray(entries)) return false;
    let changed = false;
    for (const e of entries) {
      if (!e?.from || !e.kind || !e.seq) continue;
      let ring = room.term.get(e.from);
      if (!ring) {
        ring = new Map();
        room.term.set(e.from, ring);
      }
      ring.set(e.seq, e);
      while (ring.size > 3000) ring.delete(ring.keys().next().value);
      changed = true;
    }
    return changed;
  }

  /**
   * 一名成员的终端转录时间线（seq 升序浅拷贝）。
   * @param {string} key room key @param {string} name member
   * @returns {import('../wire/wire.js').TraceEntry[]}
   */
  termOf(key, name) {
    const room = this.rooms.get(key);
    const ring = room?.term.get(name);
    return ring ? [...ring.values()] : [];
  }

  /**
   * 全房合流时间线（r_17 终端板的「总览」态）：term 的 seq 是房间级
   * 单调发号，跨成员按 seq 归并即真实时间序。
   * @param {string} key room key
   * @returns {import('../wire/wire.js').TraceEntry[]}
   */
  termAllOf(key) {
    const room = this.rooms.get(key);
    if (!room) return [];
    const all = [];
    for (const ring of room.term.values()) all.push(...ring.values());
    all.sort((a, b) => a.seq - b.seq);
    return all;
  }

  #emit(key, hint) { this.ev.onChange(key, hint); }
}

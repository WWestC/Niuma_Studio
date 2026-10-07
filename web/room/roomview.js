// roomview.js — the pixel office board (v2 P6): the retired Ebiten
// window's world, ported whole into the app window's canvas. This file
// is now the board's orchestration only — assets (art.js), the little
// people and their sim (actor.js), the desk pool (desks.js), the
// nameplates (plates.js) and the speech bubbles (bubbles.js) live
// beside it. The same wire frames the chat board streams drive the
// office: joins spawn actors, say/report pop bubbles, the task ledger
// seats the busy at their desks and lights their desk lamps. Pure
// ambience over state that lives in Go; nothing here writes back.

import { getPeople, getTasks, getMeeting, getPlan, getRoomNotice, getAchievements, postReplay, getReplayFrames, getReplayDays, postReplayShare } from '../wire/api.js';
import { Msg } from '../wire/wire.js';
import { roleStem } from '../chat/rooms.js';
import { ArtKit, AVATAR_FRAMES, SCENE_W, SCENE_H } from './art.js';
import { Actor, DIR, SPEAK_BEAT, speakHop } from './actor.js';
import { DeskPool } from './desks.js';
import { Nav } from './nav.js';
import { drawPlate, drawTaskPlate } from './plates.js';
import { drawBubbles, layoutBubbles, makeBubble } from './bubbles.js';
import { clamp, guideDue, inRect, rand } from './util.js';
import { dismissHide, icon, memberColor, copyToClipboard } from '../ui/dom.js';
import { t, localeTag } from '../ui/i18n.js';
import { PrinterRitual, SHEET_KIND } from './printer.js';
import { EmoteRitual } from './emote.js';
import { PoseRitual } from './poses.js';
import { InteractRitual } from './interact.js';
import { SocialEngine } from './social.js';
import { ReplayBuf, shadowAt, shadowRoomAt, dayBufOf, shadowFrame } from './replay.js';
import { poseLift } from './poses.js';
import { Recorder } from './recorder.js';
import { exportDayReplay, downloadBlob, pickMime } from './exporter.js';
import { sfx } from './sfxport.js';
import { EnergyEngine, EPARAMS } from './energy.js';
import { WaterRitual } from './water.js';
import { dispatchable, onErrand, leaveDesk } from './errand.js';
import { CleanerRitual } from './cleaner.js';
import { separateCrowd, nudgeBody } from './crowd.js'; // t_189：人群分离纯函数化
import { FirstGuide } from '../ui/guide.js'; // t_159：首次观察引导
import { setLitterPenalty } from './nav.js';
import { openClockPop } from '../ui/clockpop.js';
import { Dialog } from '../ui/feedback.js';
import { markdown } from '../chat/markdown.js';
import { dress } from '../chat/stream.js';

const META_EVERY = 20_000; // ranks/doing/meeting poll
const SAMPLE_EVERY_REPLAY = 0.5; // r_23：回放采样帧距（秒）

// t_197：办公室效果门——回放帧（开页载史/断线补窗）一律沉默。重放的
// say/report 若照播效果，整窗历史会在同一帧齐放（对话气泡＋表情泡＋
// 啵声「集中蹦出来」）；历史本身由聊天流的补窗分隔线交代，办公室只演
// 「现在」。Join/公告/提交/任务各分支的 !backfill 守卫同此一门。
export function sayEffectsLive(f, backfill) {
  return !backfill && (f.type === Msg.Say || f.type === Msg.Report);
}

// 提案评审的座次（geom 的会议室六位：0–2 北排面镜头、3–5 南排背镜
// 头）：提交人先坐 0 号主持位，房主坐对面 3 号审阅位——隔桌相对；再
// 多人按南北配对续座。
const PLAN_REVIEW_SEATS = [0, 3, 1, 4, 2, 5];

// palette.go's board-level slice (the plate/bubble families live in
// their own modules). The office has no computers — a working desk's
// tell is its lamp: the fore layer paints unlit brass shades on both
// rows, and these repaint the shade lit with a breathing warm halo.
const CLR = {
  hoverRing: 'rgba(255,255,255,.667)', // α170
  lampShade: '#f2c14e',                // the lit brass shade
  lampCore: '#ffe9a8',                 // the bright core inside the shade
  lampHaloA: 0.34,                     // halo alpha, breathing frames
  lampHaloB: 0.20,
  glintBase: '#7d95bd', // the chair fabric, lifted — the shimmer
  glintHi: '#b3c6e6',   // rides the steel-blue, not the old wood
  // 会议室大屏点亮时的呼吸光晕 + 那页开场幻灯片（蓝标题条、灰正文、
  // 绿对勾）——与台灯同一套「暗座画进图层、亮态画布重绘」契约
  screenGlowA: 'rgba(64,128,255,.32)',
  screenGlowB: 'rgba(64,128,255,.20)',
  screenFace: '#e8f1fd',
  screenBand: '#1456f0',
  screenLine: '#a9b2bb',
  screenOK: '#34c724',
};

// paintScreenLit repaints the meeting room's wall display lit (geom's
// screen rect) with a breathing halo on the 2s clock phase — the
// meeting-is-on tell. Painted right over the back layer, BEFORE the
// actors, so the near row's heads overlap the slide's bottom edge the
// way real sitters occlude a wall screen.
function paintScreenLit(ctx, [sx, sy, sw, sh], phase) {
  ctx.fillStyle = phase ? CLR.screenGlowA : CLR.screenGlowB;
  ctx.fillRect(sx - 8, sy - 8, sw + 16, sh + 16);
  ctx.fillStyle = CLR.screenFace;
  ctx.fillRect(sx, sy, sw, sh);
  // the slide: a brand-blue title band, gray body lines, a green check
  ctx.fillStyle = CLR.screenBand;
  ctx.fillRect(sx + 4, sy + 4, sw - 8, 5);
  ctx.fillStyle = CLR.screenLine;
  const rows = [[10, 0.72], [16, 0.55], [22, 0.62]];
  for (const [dy, frac] of rows) {
    ctx.fillRect(sx + 4, sy + 4 + dy, Math.round((sw - 8) * frac), 2);
  }
  ctx.fillStyle = CLR.screenOK;
  ctx.fillRect(sx + sw - 16, sy + sh - 8, 6, 2);
  ctx.fillRect(sx + sw - 10, sy + sh - 11, 2, 4);
}

// paintLampLit repaints one lamp shade lit (geom's lamp rect) with a
// warm halo that breathes on the 2s clock phase — the working tell.
function paintLampLit(ctx, [lx, ly, lw, lh], phase) {
  ctx.fillStyle = `rgba(242,193,78,${phase ? CLR.lampHaloA : CLR.lampHaloB})`;
  ctx.fillRect(lx - 3, ly - 3, lw + 6, lh + 5);
  ctx.fillStyle = CLR.lampShade;
  ctx.fillRect(lx, ly, lw, lh);
  ctx.fillStyle = phase ? CLR.lampCore : '#ffdf8a';
  ctx.fillRect(lx + 2, ly + 1, lw - 4, lh - 2);
}

// chairGlintOffset's deterministic hash, uint32-exact against Go.
function glintDx(desk, frame) {
  let h = (((desk + 1) >>> 0) * 747796405 + ((frame + 1) >>> 0) * 2891336453) >>> 0;
  h = (h ^ (h >>> 13)) >>> 0;
  h = Math.imul(h, 2654435761) >>> 0;
  h = (h ^ (h >>> 7)) >>> 0;
  return h % 4;
}

// shadowBright 是回放态的灯面（r_32.1）：影子帧里 working＋有工位＋带
// 任务牌＝当时在工位干活——灯与椅背流光随历史走，不读现在的座次。
function shadowBright(list, nDesks) {
  const out = new Array(nDesks).fill(false);
  for (const a of list) {
    if (a.working && a.label && a.deskIdx >= 0 && a.deskIdx < nDesks) out[a.deskIdx] = true;
  }
  return out;
}

// achTrophyCount：荣誉墙奖杯数（paintShowcase 只消费计数——排序取前
// 8 枚，展位画法与具体哪枚无关）。
function achTrophyCount() {
  const ach = globalThis.__NIUMA_ACH__;
  if (!ach || !ach.unlocked) return 0;
  return Math.min(8, Object.keys(ach.unlocked).length);
}

// ── the board ──────────────────────────────────────────────────────
export class RoomView {
  /**
   * @param {HTMLCanvasElement} canvas
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {Object} ev { toast(text,kind), openPerson(name), openBoard(id) }
   */
  constructor(canvas, book, ev) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.book = book;
    this.ev = ev;
    this.art = new ArtKit();
    this.actors = new Map();
    this.desks = new DeskPool();
    this.nav = null; // the walk grid (desks/walls/table) — built once the geom lands
    // everything (actors, seats, bubbles, hit tests) lives in the
    // office scene's fixed coordinate space; resize only re-fits the
    // stage to that scene
    this.w = SCENE_W; this.h = SCENE_H;
    this.scale = 1; this.offX = 0; this.offY = 0;
    // 观察相机（r_11，t_158）：zoom 是观察倍率（1–3 连续）、panX/panY
    // 是观察平移（场景 px）——this.scale/offX/offY 仍是 resize 的 letterbox
    // fit 基准（不动），相机在 draw/canvasPos 两端各乘一层。纯观察态，
    // 场景坐标系零改动（定稿 §〇：相机不是镜头）。
    this.zoom = 1;
    this.panX = 0; this.panY = 0;
    this.clock = 0;
    this.visible = false;
    this.hoverX = -1; this.hoverY = -1; this.hoverOn = false;
    this.keys = new Set();
    this.metaTimer = 0;
    this.metaQueued = false;
    this.raf = 0; this.lastT = 0;
    this.roomKey = null;
    this.meeting = null; // 需求评审会议：live 记录（null = 会议室空闲）
    // 提案评审（评审环节）：评审槽里的待审提案 + 已入席的名册。会议室
    // 的占用真源是需求评审会议——会议 live 时评审借座让位，散会回座。
    this.pendingPlan = null;
    this.planSeated = new Set();
    this.planQueued = false;
    // 茶水间（t_103）：歇脚位占用表（geom.pantry.spots 的下标 → 名字），
    // pantryT 是挑人去喝咖啡的节拍器
    this.pantryUse = new Map();
    this.pantryT = 0;
    // 大型打印机（r_03/t_116）：打印仪式状态机——亮灯/出纸/取材三阶段，
    // 手持件与自动出纸分支都在里面；业务事件经 this.printer.print() 进来
    this.printer = new PrinterRitual(this);
    // t_105 交互态：饮水机倒水动画余时、绿植浇水时间戳（成长度纯装饰）
    this.pourT = 0;
    this.plantWater = new Map();
    // 表情系统（r_04/t_121）：E1–E12 表情泡——say 关键词/task 事件/
    // react/idle 四路触发，单例队列一人一泡
    this.emoter = new EmoteRitual(this);
    // 时间轴回放（r_23/t_198）：环形快照缓冲（0.5s 帧/10min 窗）＋
    // 回放态（replayT ≥ 0 = 正在看历史；-1 = 实时）。实时模拟永不暂停。
    this.replayBuf = new ReplayBuf();
    this.replayT = -1;
    this.replaySamplerT = 0;
    // 一日回放（r_32）：日缓冲（t=墙钟 ms，loadReplayDay 分页折入）、
    // 源开关（ring=即席回看最近 10 分钟 / day=当日文件）、播放态与倍速。
    this.dayBuf = null;
    this.replaySrc = 'ring';
    this.replayPlaying = false;
    this.replaySpeed = 4;
    this.replayLoading = false;
    this.replayToken = ''; // r_32 分享页的帧读 token（房主页为空）
    this.buildReplayUI();
    // r_32 录制腿：只有房主页落盘（访客/分享页不录——一办公室一个录制
    // 者，房主的模拟才是「正史」）。
    this.recorder = (typeof globalThis !== 'undefined' &&
      !globalThis.__NIUMA_VISITOR__ && !globalThis.__NIUMA_DAYREPLAY__)
      ? new Recorder(postReplay) : null;
    if (this.recorder) this.recorder.start();
    // 动作系统（r_04/t_122）：A1–A15 姿势覆盖层——事件触发/idle 随机两
    // 路，单例一人一动作，纯 canvas 直绘不动 atlas
    this.poser = new PoseRitual(this);
    // 交互系统（r_04/t_123）：I1–I12 物件点击反馈——纯前端 Ritual，
    // 每件 ≤1.5s、零业务副作用、命中走 geom/绘制同源常量
    this.interact = new InteractRitual(this);
    // 摸鱼社交引擎（r_06/t_131）：空闲牛马自发小剧场——M1–M12 十二套
    // 机制、被发现批评链、派活即散（endBreak 同语义）
    this.social = new SocialEngine(this);
    // 体力×零食×垃圾引擎（r_07/t_137+t_138）：隐藏体力驱动觅食-吃-丢
    // 链、桶容量渐满、落地垃圾软阻挡数据面
    this.energy = new EnergyEngine(this);
    // 接水外勤（errand 根治）：路过的闲人走到饮水机前真接一杯——
    // 占用声明 a.drinking 进 errand.js 一口径
    this.water = new WaterRitual(this);
    // t_139：保洁阿姨 NPC——m-cleaner-cycle 常驻巡逻（清桶/拾地/补货），
    // isNPC 豁免一切成员机制，全天在岗（夜班照巡）
    this.cleaner = new CleanerRitual(this);
    // t_188（r_18）：成就快照装载（一次）——货架奖杯 roster 与清单页签
    // 同源（GET /achieve/tests-green 的解锁键集，前端从键反解最近达成）
    if (!globalThis.__NIUMA_ACH__) {
      getAchievements().then((b) => {
        globalThis.__NIUMA_ACH__ = b || {};
      }).catch(() => { globalThis.__NIUMA_ACH__ = {}; });
    }
    // t_159：首次引导（三步气泡）——看过不再弹，「再看一遍」走设置窗。
    // 弹的时机走 tryGuide 三重门（见下）：可见＋有房＋门已落，任一缺
    // 席就等补拍——路由首拍 revealed 跑在 setRoom/开门之前，直接弹会
    // 把气泡画在黑屏和启动门上（guide-layer z-120 压过门 z-90，实录）
    this.guide = new FirstGuide();
    this.guideShown = false; // 本会话是否已弹过（防翻板块翻回从头重来）
    this.doorDown = !document.getElementById('boot'); // 无门可守的嵌入页视为已开门
    // t_136 食品目录装载（一次）：手持真形/残骸形/挑零食都从这里取
    if (!globalThis.__NIUMA_FOODS__) {
      fetch('/art/foods.json').then((r) => r.json()).then((list) => {
        globalThis.__NIUMA_FOODS__ = list || [];
      }).catch(() => { /* 目录缺席——占位渲染照旧，吃链不阻塞 */ });
    }
    setLitterPenalty(EPARAMS.penalty); // 软阻挡代价进调参区真源
    // 幕墙昼夜相位的慢节拍：每 20 秒对一次真实时钟，跨相位换背景图层
    this.todT = 0;

    this.onResizeObs = (() => {
      let t = 0;
      return () => { clearTimeout(t); t = setTimeout(() => this.resize(), 250); };
    });
    // t_116 调试触发口：控制台 __niumaPrintDebug() 即空触发一轮完整
    // 打印仪式（亮灯→出纸→取材三阶段，取材人挑当前第一个空闲成员；
    // 传 'task'|'onboard'|'notice'|'done' 换纸张种类）——验收不依赖
    // 真实业务事件。挂着不改任何业务路径，发布无需摘除。
    try {
      if (typeof window !== 'undefined' && !window.__niumaPrintDebug) {
        window.__niumaPrintDebug = (kind, taker) => {
          const kinds = ['task', 'onboard', 'notice', 'done', 'commit'];
          const k = kinds.includes(kind) ? kind : 'task';
          let who = taker;
          if (!who) {
            for (const a of this.actors.values()) {
              if (dispatchable(a, this.busy)) { who = a.name; break; } // errand.js 一口径（外勤中人不当调试取材人）
            }
          }
          if (!who) { this.printer.print(k, null, '调试·无人认领'); return '调试打印：无人认领分支（自动出纸）'; }
          this.printer.print(k, who, '调试·' + k);
          return `调试打印：${k} → ${who}`;
        };
      }
    } catch { /* 非 window 环境（测试）静默 */ }

    this.ro = new ResizeObserver(() => this.onResizeObs()());
    // watch the pane, not the stage: the stage is scene-sized by
    // resize(), it never moves when the window does
    this.ro.observe(canvas.closest('#board-room') || canvas.parentElement);
    canvas.addEventListener('mousemove', (e) => this.onMove(e));
    canvas.addEventListener('mouseleave', () => { this.hoverOn = false; });
    // t_158 三段式：click 退役（拖拽阈值裁决定稿 §三——<4px 算点击）
    canvas.addEventListener('mousedown', (e) => this.onDown(e));
    // r_22 触屏（t_201）：touches 全集驱动（防 pinch 的 target 漂移——
    // 定稿 §三·3）；画布区 touch-action:none 在 CSS 精确圈定
    canvas.addEventListener('touchstart', (e) => this.onTouchStart(e), { passive: false });
    canvas.addEventListener('touchmove', (e) => this.onTouchMove(e), { passive: false });
    canvas.addEventListener('touchend', (e) => this.onTouchEnd(e), { passive: false });
    canvas.addEventListener('wheel', (e) => this.onWheel(e), { passive: false });
    canvas.addEventListener('dblclick', (e) => this.onDblClick(e));
    // t_158 补：框外复位钮——观察态离家（缩放/平移过）才现身，点击归位
    this.camResetBtn = document.getElementById('room-cam-reset');
    if (this.camResetBtn) {
      this.camResetBtn.addEventListener('click', () => this.resetCam());
      this.syncCamReset();
    }
    window.addEventListener('keydown', (e) => this.onKey(e, true));
    window.addEventListener('keyup', (e) => this.onKey(e, false));
    this.resize();
  }

  // 相机合成（t_158）：观察 zoom/pan 与 fit 基准的乘积——一切绘制与
  // 命中换算只用这两个合成值，观察态的进入与退出对场景零感知。
  camScale() { return this.scale * this.zoom; }
  camOffX() { return this.offX * this.zoom + this.panX; }
  camOffY() { return this.offY * this.zoom + this.panY; }

  // ── geometry ────────────────────────────────────────────────────
  bounds() {
    const g = this.art.geom;
    const wallH = g ? g.wallH : 128;
    const footer = 10; // breathing room below the feet (plates float overhead)
    const marginX = 24; // 12 legacy px ×S
    return { minX: marginX, minY: wallH + 36, maxX: this.w - marginX, maxY: this.h - footer };
  }

  async resize() {
    // the office scene is one fixed size the server painted; the stage
    // is measured into that scene's exact 768:520 aspect and centered
    // by #board-room's flex, so the stage frame hugs the pixel art —
    // no letterbox bands of bare page background inside it (the old
    // pane-sized canvas showed one below the floor whenever the pane
    // ran taller than the scene). The draw pass then maps the scene
    // 1:1 onto the stage; seats and actors never re-lay.
    const stage = this.canvas.parentElement;
    const pane = stage.closest('#board-room') || stage.parentElement;
    const pcs = getComputedStyle(pane);
    const pw = Math.floor(pane.clientWidth - parseFloat(pcs.paddingLeft) - parseFloat(pcs.paddingRight));
    const ph = Math.floor(pane.clientHeight - parseFloat(pcs.paddingTop) - parseFloat(pcs.paddingBottom));
    if (pw < 2 || ph < 2) return;
    const dpr = window.devicePixelRatio || 1;
    const scale = Math.min(pw / SCENE_W, ph / SCENE_H);
    const w = Math.max(2, Math.floor(SCENE_W * scale));
    const h = Math.max(2, Math.floor(SCENE_H * scale));
    this.scale = Math.min(w / SCENE_W, h / SCENE_H);
    this.offX = 0; this.offY = 0;
    if (this.zoom !== undefined) this.clampPan(); // t_158：fit 变了重钳观察平移
    // identical writes are skipped so the observer fire this write
    // triggers settles instead of looping
    if (stage.style.width !== `${w}px` || stage.style.height !== `${h}px`) {
      stage.style.width = `${w}px`;
      stage.style.height = `${h}px`;
    }
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    this.canvas.style.width = `${w}px`;
    this.canvas.style.height = `${h}px`;
    if (this.art.ready()) return;
    try {
      await this.art.load();
      // the scene is one fixed size — the walk grid lays once off the
      // same geom the layers were painted with
      this.nav = new Nav(this.art.geom, this.bounds());
    } catch (e) {
      this.ev.toast?.(t('办公室图层加载失败：{msg}', { msg: e.message || e }), 'err');
    }
  }

  // ── roster & frames (the RoomBook taps) ─────────────────────────
  setRoom(key) {
    if (this.roomKey === key) return;
    this.roomKey = key;
    this.actors.clear();
    this.cleaner.reset(); // t_139：阿姨引用随房清空，下轮巡逻重进场
    this.printer.reset(); // errand 根治：在机任务/队列/滞留纸的演员引用随房清
    this.water.reset();   // 接水在途名单同理
    this.desks.reset();
    this.pantryUse.clear();
    this.meeting = null;
    this.pendingPlan = null;
    this.planSeated.clear();
    this.syncRoster();
    this.applyBusy(true);
    this.refreshMeeting();
    this.refreshPlan();
    this.recorder?.attach(key); // r_32：录制随房（切房先冲旧 key 的尾批）
    if (this.replaySrc === 'day') this.exitReplay(); // 日缓冲是旧房的历史——随房收档
    this.wake();
    this.tryGuide(); // t_159：房数据落地——首拍 revealed 先于此的就等这拍
  }

  syncRoster() {
    const room = this.book.rooms.get(this.roomKey);
    if (!room) return;
    const names = new Set(room.members.keys());
    for (const name of [...this.actors.keys()]) {
      if (!names.has(name)) {
        this.desks.drop(name);
        this.actors.delete(name);
        // 歇脚位一并释放（按值扫，表就五格）
        for (const [k, n] of this.pantryUse) if (n === name) this.pantryUse.delete(k);
      }
    }
    const b = this.bounds();
    for (const m of room.members.values()) {
      if (this.actors.has(m.name)) {
        const a = this.actors.get(m.name);
        a.color = m.color || a.color; a.hair = m.hair || a.hair; a.role = m.role || '';
        a.atlas = this.art.atlas(a.color, a.hair, a.name); // r_05：who 走分层签名
        continue;
      }
      let x = 0, y = 0;
      for (let i = 0; i < 12; i++) {
        x = rand(b.minX, b.maxX); y = rand(b.minY, b.maxY);
        if (this.spotFree(x, y) && !this.inMeetingRoom(x, y) &&
          (!this.nav || this.nav.free(x, y))) break;
      }
      const a = new Actor(m, x, y, this.art.atlas(m.color, m.hair, m.name)); // r_05：who 走分层签名
      this.actors.set(m.name, a);
    }
    this.markLocal();
  }

  markLocal() {
    const owner = this.book.owner?.name;
    for (const a of this.actors.values()) {
      a.isLocal = a.name === owner;
      // the host exemption from desk duty stands (applyBusy skips the
      // local actor); the local actor just roams the floor
      if (a.isLocal) { a.deskIdx = -1; this.desks.drop(a.name); a.working = false; }
    }
  }

  spotFree(x, y) {
    for (const a of this.actors.values()) {
      const dx = a.x - x, dy = a.y - y;
      if (dx * dx + dy * dy < 26 * 26) return false;
    }
    return true;
  }

  // 会议室的玻璃盒子（外扩一圈）：出生点与闲逛别落进去
  inMeetingRoom(x, y) {
    const g = this.art.geom;
    if (!g || !g.meet) return false;
    const [rx, ry, rw, rh] = g.meet.room;
    return x >= rx - 10 && x < rx + rw + 10 && y >= ry - 10 && y < ry + rh + 10;
  }

  frame(f, key, backfill) {
    if (key !== this.roomKey) return;
    if (sayEffectsLive(f, backfill)) {
      this.say(f.from, f.text, f.ts, f.type === Msg.Report,
        (f.mentions || []).includes(this.book.owner?.name), f.origin === 'mirror');
    }
    // t_118（r_03 接线表 #2）：新成员入座（join 帧）→ 在岗人事（HR）
    // 打「入职材料」（姓名＋岗位）。backfill（断线补窗/开页载史）里
    // 的 join 是回放不是入职，不触发；HR 不在名册或正忙则机器自动出纸。
    if (!backfill && f.type === Msg.Join && f.member) {
      this.emoter.emote(f.member.name, 'wave'); // t_121 E12：入座挥手
      this.poser.pose(f.member.name, 'cheer'); // t_122 A5：到岗欢呼
      let hr = null;
      for (const a of this.actors.values()) {
        if (!a.isLocal && roleStem(a.role) === '人事') { hr = a.name; break; }
      }
      if (hr) this.printer.print(SHEET_KIND.ONBOARD, hr, f.member.name);
      else this.printer.print(SHEET_KIND.ONBOARD, null, f.member.name);
    }
    // t_121：react 帧——正面表情回应给原作者冒 love 泡。帧只带
    // from(回应者)+seq(原消息序号)，作者按 seq 从房间消息流反查
    if (f.type === Msg.React) {
      const room = this.book.rooms.get(key);
      let target = null;
      if (room) {
        // 消息列表按 seq 升序，倒着找最近的同 seq 行
        for (let i = room.messages.length - 1; i >= 0; i--) {
          const fr = room.messages[i] && room.messages[i].frame;
          if (fr && fr.seq === f.seq && fr.from) { target = fr.from; break; }
        }
      }
      if (target) this.emoter.onReact(f.emoji, target);
    }
    // t_118（#3/#3b）：公告发布/更新 → 发布者本人打「公告纸」；系统/
    // 无人格发布（from 非成员名）→ 无人认领自动出纸。cleared 不打。
    if (!backfill && f.type === Msg.Notice && (f.event === 'published' || f.event === 'updated')) {
      const who = this.actors.get(f.from) && !this.actors.get(f.from).isLocal ? f.from : null;
      this.printer.print(SHEET_KIND.NOTICE, who, '公告');
    }
    if (f.type === Msg.Task) {
      this.queueMeta();
      // t_117（r_03 接线表 #1/#4）：plan accept 落库的 created 帧带着
      // 受派人逐张打「任务单」（「待招·XX」占位名也照打——纸等人）；
      // 状态转 done 的 updated 帧给负责人打「验收单」。纯表现层，
      // 打印失败静默（printer.print 从不抛）
      if (f.task && !backfill) {
        if (f.event === 'created' && f.task.assignee) {
          this.printer.print(SHEET_KIND.TASK, f.task.assignee, f.task.id);
          this.emoter.onTaskEvent(f.event, f.task);
          this.poser.pose(f.task.assignee, 'point'); // t_122 A8：领任务指认
          // 氛围叮只出给人看得见的办公室（声色同门：看不见的场合让位
          // rooms.js 的通知门——绿点＋叮在那里同一道 if 放行）
          if (this.visible && !document.hidden) sfx('scene-ding'); // r_09：任务落地轻叮（冷却窗兜多任务同帧）
        } else if (f.event === 'updated' && f.task.status === 'done' && f.task.assignee) {
          this.printer.print(SHEET_KIND.DONE, f.task.assignee, f.task.id);
          this.emoter.onTaskEvent(f.event, f.task);
          this.poser.pose(f.task.assignee, 'cheer'); // t_122 A5：完工欢呼
          if (this.visible && !document.hidden) sfx('scene-ding');
        } else if (f.event === 'confirmed' && f.task.assignee) {
          this.emoter.onTaskEvent(f.event, f.task);
          this.poser.pose(f.task.assignee, 'bow'); // t_122 A4：确认致意
        }
      }
    }
    if (f.type === Msg.Plan) this.queuePlanSync();
    // v2.8 gitflow：提交播报帧 → 提交者本人打「提交纸」（无人格作者
    // 无人认领自动出纸）；合并 merged 帧 → 批准者打「验收单」——纸面
    // 与聊天卡同源，办公室里看得见谁刚落了一笔代码。
    if (!backfill && f.type === Msg.Git && f.event === 'commit') {
      const who = this.actors.get(f.from) && !this.actors.get(f.from).isLocal ? f.from : null;
      this.printer.print(SHEET_KIND.COMMIT, who, (f.git && f.git.sha || '').slice(0, 7) || '提交');
    }
    if (!backfill && f.type === Msg.Merge && f.event === 'merged') {
      const who = this.actors.get(f.from) && !this.actors.get(f.from).isLocal ? f.from : null;
      this.printer.print(SHEET_KIND.DONE, who, (f.merge && f.merge.id) || '合并');
    }
    if (f.type === Msg.Meeting) {
      // started/advanced/extended 帧都带 live 记录：会还开着（advanced＝
      // 主持提前结束讨论段直进总结、extended＝主持延时）；ended 帧带的是
      // 已结束的存档——会议室已释放，屏幕熄灭（评审借座随后回座）；
      // denied 是给请求者的私信拒执，不动屏幕
      if (f.event === 'started' || f.event === 'advanced' || f.event === 'extended') {
        if (f.meeting) this.meeting = f.meeting;
      } else if (f.event === 'ended') {
        this.meeting = null;
      }
      this.applyMeeting();
      this.applyPlanReview();
    }
  }

  say(name, text, ts, report, mention, mirror) {
    const a = this.actors.get(name);
    if (!a) return;
    this.emoter.onSay(name, text); // t_121：关键词/emoji 命中冒泡
    a.bubble = makeBubble(this.ctx, {
      text, from: name, ts, report, mention, mirror,
      seated: a.working && a.deskIdx >= 0,
    }, this.w);
    if (!mirror) a.speakT = SPEAK_BEAT;
  }

  // ── meta (ranks, grace, the doing set) ──────────────────────────
  queueMeta() {
    if (this.metaQueued) return;
    this.metaQueued = true;
    setTimeout(() => { this.metaQueued = false; this.refreshMeta(); }, 2000);
  }

  async refreshMeta() {
    let people = [], tasks = [];
    try { people = await getPeople(); } catch { /* keep the old set */ }
    try { tasks = await getTasks(); } catch { /* keep the old set */ }
    this.refreshMeeting();
    this.refreshPlan();
    const ranks = new Map(), grace = new Set();
    for (const p of people) {
      if (typeof p.rank === 'number') ranks.set(p.name, p.rank);
      if (p.grace) grace.add(p.name);
    }
    const busy = new Set(), labels = new Map();
    for (const t of tasks) {
      if (t.status === 'doing' && t.assignee && !busy.has(t.assignee)) {
        busy.add(t.assignee);
        labels.set(t.assignee, t.id);
      }
    }
    // t_104：上一轮还在 doing、这一轮已不在的成员——任务刚 done，
    // 头顶亮 2 秒的绿 ✓（r_01 §二「done 后气泡变 ✓ 2s 消散」）
    const prevBusy = this.busy || new Set();
    for (const name of prevBusy) {
      if (!busy.has(name)) {
        const a = this.actors.get(name);
        if (a) a.doneT = 2;
      }
    }
    this.busy = busy;
    for (const a of this.actors.values()) {
      a.rank = ranks.get(a.name) ?? null;
      a.grace = grace.has(a.name);
      a.label = labels.get(a.name) || '';
    }
    this.applyBusy();
  }

  // ── 需求评审会议：会议室占用态的同步与入座 ─────────────────────────
  //
  // 会议 live 时：PM + 参会人离开工位，经门口走位到会议室的站位
  // （先到门口、再入座的两段路），站定面向会议桌；散会帧/轮询释放
  // 所有人回正常的忙/闲落座。屏幕只在 live 时点亮（draw 里的
  // paintScreenLit）。

  async refreshMeeting() {
    const key = this.roomKey;
    if (!key) return;
    try {
      const rep = await getMeeting(key);
      if (this.roomKey !== rep.project) return; // 切房竞态：丢弃过期应答
      this.meeting = rep.meeting && rep.meeting.status === 'live' ? rep.meeting : null;
      this.applyMeeting();
      this.applyPlanReview(); // 散会释放完座位，评审借座的人接着回座
    } catch { /* keep the old state */ }
  }

  applyMeeting() {
    const g = this.art.geom;
    if (!g || !g.meet) return;
    const m = this.meeting && this.meeting.status === 'live' ? this.meeting : null;
    const roster = m ? [m.chair, ...(m.participants || [])] : [];
    const seated = new Set();
    roster.forEach((name, i) => {
      const a = this.actors.get(name);
      const seat = g.meet.seats[i % g.meet.seats.length];
      if (!a || !seat) return;
      seated.add(name);
      if (a.meetIdx === i && (a.hasTarget || a.working)) return; // already placed / walking
      this.walkToMeetingSeat(a, i, seat);
    });
    for (const a of this.actors.values()) {
      if (a.meetIdx >= 0 && !seated.has(a.name)) {
        // 散会：站位与面向清空，回正常的忙/闲落座节奏（评审借座一并
        // 让位——会议室现在归会议）
        a.meetIdx = -1;
        a.meetLeg = null;
        a.working = false;
        a.hasTarget = false;
        a.idleT = rand(0, 2);
        this.planSeated.delete(a.name);
      }
    }
  }

  // walkToMeetingSeat 送一人进会议室 i 号位——会议与提案评审共用的
  // 入座管线：离开工位（保留 desk 的回座缓冲），会议室优先于一切工位
  // 逻辑；人还在办公区时先走到门口、再进屋入座（meetLeg 由 update 链
  // 接着走）。
  walkToMeetingSeat(a, i, seat) {
    const g = this.art.geom;
    if (a.breakIdx >= 0) this.endBreak(a); // 会议优先，咖啡歇就地散场
    if (a.deskIdx >= 0) {
      this.desks.release(a.deskIdx, this.clock);
      a.lastDesk = a.deskIdx;
    }
    a.deskIdx = -1;
    a.meetIdx = i;
    a.meetLeg = null;
    a.working = false;
    a.workFace = seat.face === 0 ? DIR.DOWN : DIR.UP;
    const [sx, sy] = seat.spot;
    const [rx, ry, rw, rh] = g.meet.room;
    const inside = a.x >= rx && a.x < rx + rw && a.y >= ry && a.y < ry + rh;
    if (inside) {
      a.tx = sx; a.ty = sy; a.hasTarget = true;
    } else {
      // 还在办公区：先走到门口，再进屋入座（meetLeg 由 update 链走）
      const [dx, dy] = g.meet.door;
      a.tx = dx; a.ty = dy; a.hasTarget = true;
      a.meetLeg = { x: sx, y: sy };
    }
  }

  // ── 提案评审（评审环节）：待审提案把房主与提交人带去会议室 ────────
  //
  // plan_submit 落槽即进入评审环节：提交人（编排者）坐 0 号主持位、
  // 房主坐对面审阅位，隔桌对坐等房主过稿，大屏点亮待审灯——直到
  // accept/reject/superseded 弹槽散场。会议室的占用真源仍是需求评审
  // 会议：会议 live 时评审让位不占房，散会自动回座。真值两条腿：
  // plan 帧（submitted/accepted/…）即时驱动 + 名册轮询周期兜底。

  queuePlanSync() {
    if (this.planQueued) return;
    this.planQueued = true;
    setTimeout(() => { this.planQueued = false; this.refreshPlan(); }, 1200);
  }

  async refreshPlan() {
    const key = this.roomKey;
    if (!key) return;
    try {
      const rep = await getPlan(key);
      if (this.roomKey !== rep.project) return; // 切房竞态：丢弃过期应答
      this.pendingPlan = rep.plan || null;
    } catch { /* keep the old slot */ }
    this.applyPlanReview();
  }

  // 评审座次：提交人 0 号（会议主持位，面向镜头），房主对面 3 号（隔
  // 桌相对）；再多人按南北配对续座。提交人即提案的 submitted_by（房
  // 主代提交时两者合一，只坐一人），缺失时退回名册里持「排期编排」
  // 岗者。
  planReviewRoster() {
    const p = this.pendingPlan;
    if (!p) return [];
    const owner = this.book.owner?.name || '';
    let who = (p.submitted_by || '').trim();
    if (!who) {
      for (const a of this.actors.values()) {
        if (roleStem(a.role) === '排期编排') { who = a.name; break; }
      }
    }
    const names = [];
    if (who) names.push(who);
    if (owner && !names.includes(owner)) names.push(owner);
    return names;
  }

  applyPlanReview() {
    const g = this.art.geom;
    if (!g || !g.meet) return;
    // 会议 live：会议室归会议（applyMeeting 已把评审借座的人请离）
    if (this.meeting && this.meeting.status === 'live') {
      this.planSeated.clear();
      return;
    }
    const seats = PLAN_REVIEW_SEATS;
    const roster = this.planReviewRoster();
    const prev = this.planSeated;
    const seated = new Set();
    roster.forEach((name, i) => {
      const idx = seats[i % seats.length] % g.meet.seats.length;
      const seat = g.meet.seats[idx];
      const a = this.actors.get(name);
      if (!a || !seat) return;
      seated.add(name);
      if (a.meetIdx === idx && (a.hasTarget || a.working)) return; // 已入座/在路上
      this.walkToMeetingSeat(a, idx, seat);
    });
    // 散场：审完/被取代/人已离房的人离席——还在玻璃盒子里的先走到门口
    // 出去，别让谁孤零零站在熄了屏的空会议室里
    for (const a of this.actors.values()) {
      if (!prev.has(a.name) || seated.has(a.name)) continue;
      if (a.meetIdx < 0) continue;
      a.meetIdx = -1;
      a.meetLeg = null;
      a.working = false;
      a.hasTarget = false;
      a.idleT = rand(0, 2);
      const [rx, ry, rw, rh] = g.meet.room;
      if (a.x >= rx && a.x < rx + rw && a.y >= ry && a.y < ry + rh) {
        const [dx, dy] = g.meet.door;
        a.tx = dx; a.ty = dy; a.hasTarget = true;
      }
    }
    // 入席名册每轮重建：屏亮跟着「确已入席」的人走，离房者的陈名不留
    this.planSeated = seated;
  }

  // SetBusy + SetIdleSeating (world.go): busy commute to desks, idle
  // members fill what's left, the host never seats.
  applyBusy(force) {
    if (!this.art.geom) return;
    this.desks.layout(this.art.geom);
    const busy = this.busy || new Set();
    for (const f of this.desks.evictSitters(busy)) {
      const a = this.actors.get(f.name);
      if (a && a.deskIdx === f.idx) { a.deskIdx = -1; a.working = false; a.hasTarget = false; a.idleT = rand(0, 2); }
    }
    for (const a of this.actors.values()) {
      if (a.isLocal || a.isNPC || a.meetIdx >= 0) continue; // 会议室里的人不吃工位逻辑；NPC 无工位（t_139）
      if (busy.has(a.name)) {
        if (a.breakIdx >= 0) this.endBreak(a); // 派活打断咖啡歇
        if (onErrand(a) || a.holding) {
          // errand 根治：外勤/手持件被派活打断——链条下一帧自清占用，
          // 这里把路权还给工位（否则取材人到了机器前才「坐下」，忙人
          // 钉在打印机前不回桌）
          a.working = false;
          a.hasTarget = false;
          const s = a.deskIdx >= 0 ? this.desks.spots[a.deskIdx] : null;
          if (s) { a.tx = s.x; a.ty = s.y; a.hasTarget = true; }
        }
        this.assignDesk(a);
      } else if (a.breakIdx < 0 && !onErrand(a) && !a.holding) {
        // 歇脚中/外勤中/手持件在身的人不吃落座改写（走位不被踩——
        // errand 根治：旧版这里无条件 releaseDesk，觅食者半路被拽回）
        this.releaseDesk(a);
      }
    }
    // idle seating (t_53): a full office — name-sorted for stability
    const idle = [];
    for (const a of this.actors.values()) {
      if (!a.isLocal && !a.isNPC && !busy.has(a.name) && a.breakIdx < 0 && !a.socialGroup && a.deskIdx === -1 && a.meetIdx < 0 &&
          !onErrand(a) && !a.holding) idle.push(a); // 外勤/手持件在身不落座（errand 根治）
    }
    idle.sort((x, y) => x.name.localeCompare(y.name));
    for (const a of idle) {
      const got = this.desks.claimIdle(a.name, this.clock);
      if (got) {
        a.deskIdx = got.idx; a.tx = got.spot.x; a.ty = got.spot.y; a.hasTarget = true;
        a.workFace = got.spot.face === 0 ? DIR.DOWN : DIR.UP;
      }
    }
    void force;
  }

  assignDesk(a) {
    if (a.deskIdx !== -1) return;
    const spots = this.desks.spots;
    if (!spots.length) {
      const b = this.bounds();
      a.deskIdx = -2;
      a.tx = (b.minX + b.maxX) / 2; a.ty = b.minY + 6;
      a.hasTarget = true; a.working = false;
      return;
    }
    const got = this.desks.claimBusy(a.name, a.x, a.y, a.lastDesk, this.clock);
    if (got) {
      a.deskIdx = got.idx; a.tx = got.spot.x; a.ty = got.spot.y;
      a.workFace = got.spot.face === 0 ? DIR.DOWN : DIR.UP;
    } else {
      const last = spots[spots.length - 1];
      const b = this.bounds();
      a.deskIdx = -2;
      a.tx = clamp(last.x + 88, b.minX, b.maxX);
      a.ty = clamp(last.y, b.minY, b.maxY);
    }
    a.hasTarget = true; a.working = false;
  }

  releaseDesk(a) {
    if (a.deskIdx >= 0) {
      this.desks.release(a.deskIdx, this.clock);
      a.lastDesk = a.deskIdx;
    }
    a.deskIdx = -1; a.working = false; a.hasTarget = false;
    a.idleT = rand(0, 1.5);
  }

  // ── 茶水间咖啡歇（t_103）──────────────────────────────────────────
  //
  // 空闲成员的偶发小歇：每隔几拍三成几率挑一名闲人（坐着的让出工位，
  // 保留回座缓冲）走到茶水间的一个歇脚位站定，面向茶水台/方桌停
  // 12–22 秒——干活、开会、房主巡场的人从不动。散场自己回工位/闲逛
  // 节奏；被派活或被叫进会议室立即终止。

  endBreak(a) {
    if (a.breakIdx >= 0) this.pantryUse.delete(a.breakIdx);
    a.breakIdx = -1; a.breakUntil = 0;
    a.working = false; a.hasTarget = false;
    a.idleT = rand(0.5, 2);
  }

  tryPantry() {
    const p = this.art.geom && this.art.geom.pantry;
    if (!p || !p.spots || !p.spots.length) return;
    // 首帧 busy 名册未就绪时不挑人——免得把正在干活的派去喝咖啡
    if (!this.busy) return;
    // 到点的歇脚结束，回正常的忙/闲节奏（回座缓冲还在，多半坐回原座）
    for (const a of this.actors.values()) {
      if (a.breakIdx >= 0 && this.clock >= a.breakUntil) this.endBreak(a);
    }
    const free = [];
    for (let i = 0; i < p.spots.length; i++) if (!this.pantryUse.has(i)) free.push(i);
    if (!free.length) return;
    const busy = this.busy || new Set();
    const cands = [];
    for (const a of this.actors.values()) {
      // 资格走 errand.js 一口径（t_139 补钉的 isNPC 与 errand 根治的
      // 外勤/手持件豁免都在 dispatchable 里——半路抢外勤中人是旧疤）
      if (a.breakIdx >= 0) continue; // 歇脚中的不算候选（散场见上）
      if (!dispatchable(a, busy)) continue;
      cands.push(a);
    }
    if (!cands.length) return;
    // t_105 氛围行为（errand 根治）：接水是真外勤——只挑机器跟前的
    // 闲人走去接（water.js），不再是半屋半径的隔空配音
    if (Math.random() < 0.1) this.water.beat();
    // 咖啡歇是偶发的小歇不是点名：三成几率才挑人
    if (Math.random() > 0.3) return;
    const a = cands[(Math.random() * cands.length) | 0];
    const idx = free[(Math.random() * free.length) | 0];
    const s = p.spots[idx];
    leaveDesk(a, this); // 暂离工位（回座缓冲保留，散场坐回原座）
    this.pantryUse.set(idx, a.name);
    a.breakIdx = idx;
    a.breakUntil = this.clock + rand(12, 22);
    a.working = false;
    a.workFace = [DIR.DOWN, DIR.UP, DIR.RIGHT, DIR.LEFT][s.face] ?? DIR.UP;
    a.tx = s.spot[0]; a.ty = s.spot[1];
    a.hasTarget = true;
  }

  // ── the loop ────────────────────────────────────────────────────
  //
  // The world lives whether or not anyone's watching: the sim ticks
  // from the first setRoom (draw runs only while revealed — painting a
  // hidden canvas is wasted work, stepping thirty actors is not), and
  // the meta poll keeps ranks/busy/meeting fresh in the background.
  // Switching boards and back must find the office where the WORLD
  // left it, not where the viewer did.
  wake() {
    if (!this.raf) {
      this.lastT = performance.now();
      const step = (t) => {
        this.raf = requestAnimationFrame(step);
        const dt = Math.min(0.05, (t - this.lastT) / 1000);
        this.lastT = t;
        this.update(dt);
        if (this.visible) this.draw();
      };
      this.raf = requestAnimationFrame(step);
    }
    if (!this.metaTimer) {
      this.refreshMeta();
      this.metaTimer = setInterval(() => this.refreshMeta(), META_EVERY);
    }
  }

  revealed() {
    this.visible = true;
    this.resize();
    this.wake();
    this.tryGuide(); // t_159：首次进办公室弹观察引导（看过即静默——重进不烦人）
  }

  hidden_() {
    // freeze nothing — the sim keeps ticking without a viewer. Keys
    // release, though: a direction key held at switch time must not
    // walk the owner around a board nobody is looking at
    this.visible = false;
    this.keys.clear();
    // 引导跟着办公室走：没看完就走，收层但不写「看过」标记（那是
    // finish/跳过才有的账）——下次回办公室接着弹，guideShown 同步
    // 复位，别把半途的当看过
    if (this.guideShown) { this.guide.dismiss(); this.guideShown = false; }
  }

  // t_159：引导的三重门——可见、有房、启动门已落（app.js dismissBoot
  // 后调 doorDropped 补最后一门）。判式在 util.guideDue（可测）。
  tryGuide() {
    if (!guideDue(this)) return;
    this.guideShown = true;
    this.guide.maybeStart();
  }

  doorDropped() { this.doorDown = true; this.tryGuide(); }

  update(dt) {
    // 房间暂停：世界定格——clock 不走、小人不动、节拍器全停（气泡/表情
    // 的寿命都挂在这只 clock 上，一并冻结）。draw 照跑（画的是同一帧
    // 定格＋暂停纱幕）；回放腿也停——暂停是全屋的暂停。
    // 唯一例外是出生淡入（spawnT）：它是视图层的入场过渡，不挂世界钟
    // ——重启落回暂停房时全员是新 Actor（spawnT 满格），冻着不走，
    // draw 的 globalAlpha 恒 0——名牌浮在空地上、小人永远隐形。
    if (this.roomPaused()) {
      for (const a of this.actors.values()) {
        if (a.spawnT > 0) a.spawnT = Math.max(0, a.spawnT - dt);
      }
      return;
    }
    this.clock += dt;
    const bounds = this.bounds();
    let mvx = 0, mvy = 0;
    if (this.keys.has('ArrowLeft') || this.keys.has('a')) mvx -= 1;
    if (this.keys.has('ArrowRight') || this.keys.has('d')) mvx += 1;
    if (this.keys.has('ArrowUp') || this.keys.has('w')) mvy -= 1;
    if (this.keys.has('ArrowDown') || this.keys.has('s')) mvy += 1;
    // 闲逛别撞进会议室的玻璃盒子（ inflate 一圈，门口留缝）
    const g = this.art.geom;
    let avoid = null;
    if (g && g.meet) {
      const [rx, ry, rw, rh] = g.meet.room;
      avoid = { x: rx - 12, y: ry - 12, w: rw + 24, h: rh + 24 };
    }
    const others = [...this.actors.values()];
    for (const a of others) a.step(dt, { mvx, mvy, bounds, avoid, nav: this.nav, others });
    // 入会的第二段路：到达门口后接着走向自己的站位
    for (const a of others) {
      if (a.meetLeg && !a.hasTarget && !a.working) {
        a.tx = a.meetLeg.x; a.ty = a.meetLeg.y;
        a.hasTarget = true;
        a.meetLeg = null;
      }
    }
    // 茶水间节拍器：每 3 秒一拍，到点看看有没有人该去喝杯咖啡/散场
    this.pantryT -= dt;
    if (this.pantryT <= 0) { this.pantryT = 3; this.tryPantry(); }
    // 打印仪式节拍：出纸推进、无人认领淡出、到站取材判定
    this.printer.step(dt);
    // t_105：倒水动画倒计时
    if (this.pourT > 0) this.pourT = Math.max(0, this.pourT - dt);
    // t_121：表情泡节拍（衰老 + idle 自发）
    this.emoter.step(dt);
    // t_122：动作覆盖层节拍（衰老 + idle 自发小动作）
    this.poser.step(dt);
    // t_123：交互反馈节拍（动画衰老）
    this.interact.step(dt);
    // t_131：摸鱼社交引擎节拍（成团/时间线/发现判定/中断散场）
    this.social.step(dt);
    // r_23 采样节拍：每 0.5s 一帧全量快照进环形缓冲（回放态下照录——
    // 实时从未停，退出即回的就是「现在」）。r_32：同一帧搭录制腿的便
    // 车（变化过滤＋攒批落盘），录制态与回放态无关——回放看历史不妨
    // 碍正在发生的被记下来。r_32.2：快照升为全量视态（演员面＋房间级
    // r——draw 链读到的每一个状态源都进帧）。
    this.replaySamplerT -= dt;
    if (this.replaySamplerT <= 0) {
      this.replaySamplerT = SAMPLE_EVERY_REPLAY;
      const sampled = this.replayBuf.sample(this.clock, this.actors.values(),
        this.replayRoomSnap(), {
          poseOf: (n) => this.poser.byName.get(n) || null,
          emoteOf: (n) => { const b = this.emoter.byName.get(n); return b ? b.key : null; },
        });
      if (this.recorder && sampled) this.recorder.feed(Date.now(), sampled);
    }
    // r_32 播放腿：day 源自动前进（replayT 是墙钟 ms，倍速乘 dt）；到
    // 尾自停。ring 源不播（实时锚外的 10 分钟只作拖动回看）。
    if (this.replayT >= 0 && this.replayPlaying && this.replaySrc === 'day' && this.dayBuf) {
      const end = this.dayBuf.latestT;
      this.replayT = Math.min(end, this.replayT + dt * 1000 * this.replaySpeed);
      if (this.replayT >= end) this.setReplayPlaying(false);
      this.refreshReplaySlider();
    }
    // t_137/t_138：体力结算＋觅食节拍＋吃链推进＋垃圾衰减
    this.energy.step(dt);
    this.cleaner.step(dt); // t_139：保洁阿姨巡逻节拍
    for (const a of others) this.energy.stepEat(a, dt);
    for (const a of others) this.water.step(a, dt); // 接水外勤推进（errand 根治）
    // 软阻挡喂格：nav 的 litterAt 挂上垃圾表（能量引擎的真源）
    if (this.nav) {
      this.nav.litterAt = (i) => {
        const cx = (i % this.nav.cols) * 4 + 2;
        const cy = ((i / this.nav.cols) | 0) * 4 + 2;
        for (const l of this.energy.litter) {
          if (Math.abs(l.x - cx) < 4 && Math.abs(l.y - cy) < 4) return 1;
        }
        return 0;
      };
      // 行人软代价喂格（r_12 t_163 的 peopleAt 钩子此前一直是死码）：
      // 不挪窝的身体——坐定（工位/会议座/歇脚锚）、让路停走、错峰退
      // 避——脚点周围四格（9×9 格 ≈ 36px 盘，身宽 32px＋余量）打上
      // 惩罚，重算的路径自然绕出一条车道。半径实测卡过两档：3 格
      // （28px）盘外正好剩 13.5px 的贴身缝，4 格才把车道推到 20px+
      // 。走路的人不打：他们自己会让、也会被推开，把他们的格子罚了
      // 反而让对向重算互相绕晕。
      const cells = this._peopleCells || (this._peopleCells = new Set());
      cells.clear();
      for (const a of others) {
        if (a.isNPC) continue;
        if (!(a.working || a.yieldHold > 0 || a.deferT > 0)) continue;
        const cx0 = Math.floor(a.x / 4), cy0 = Math.floor(a.y / 4);
        for (let dy = -4; dy <= 4; dy++) {
          for (let dx = -4; dx <= 4; dx++) {
            const cx = cx0 + dx, cy = cy0 + dy;
            if (cx >= 0 && cy >= 0 && cx < this.nav.cols && cy < this.nav.rows) cells.add(cy * this.nav.cols + cx);
          }
        }
      }
      this.nav.peopleAt = (i) => (cells.has(i) ? 1 : 0);
    }
    for (const a of others) {
      if (a.sheetPopT) a.sheetPopT = Math.max(0, a.sheetPopT - dt);
      if (a.doneT) a.doneT = Math.max(0, a.doneT - dt);
      this.printer.maybeRelease(a);
    }
    // 幕墙的昼夜相位：慢节拍对表，跨相位换一张背景图层（窗外跟着
    // 真实时间走——白天/黄昏/黑夜/黎明）
    this.todT -= dt;
    if (this.todT <= 0) { this.todT = 20; this.art.tickTod(); }
    // 硬碰撞兜底：绕行没让开的两具身体被互相推开，绝不重叠
    if (this.nav) this.resolveCrowd(others);
  }

  // 人群分离：委托 crowd.js 的纯函数（t_189 抽出——collide.test 同款
  // 假时钟可直接驱动）。_pulseDone（对子键）由本板持有，多帧复用。
  resolveCrowd(list) {
    this._pulseDone = this._pulseDone || new Set();
    separateCrowd(list, { nav: this.nav, bounds: this.bounds(), pulseDone: this._pulseDone });
  }

  nudge(a, mx, my) {
    nudgeBody(this.nav, this.bounds(), a, mx, my);
  }

  // ── hit tests ───────────────────────────────────────────────────
  //
  // touchHitScale（r_22 t_201 定稿 §二）：触屏命中补偿因子——camScale<1
  // 时（小窗 fit 基准不足 1 倍）场景命中区在屏幕上缩小，读类命中按
  // 1/min(camScale,1) 放大搜索半径。写/管理类命中不扩边（热区纪律：
  // 危险操作热区与危险度成反比）。鼠标态恒 1。
  touchHitScale() {
    if (!this._touchActive) return 1;
    const cs = this.camScale();
    return cs < 1 ? 1 / cs : 1;
  }

  actorAt(x, y) {
    let best = null, bestY = -1;
    const g = this.art.geom;
    const k = this.touchHitScale();
    const half = (g ? g.avatarW : 32) / 2 * k, hh = (g ? g.avatarH : 48) * k;
    for (const a of this.actors.values()) {
      // t_139 补钉：NPC 不吃命中——点她没有成员主页可开（悬停光标/
      // 点击/双击复位三处共用这一道闸）
      if (a.isNPC) continue;
      if (x < a.x - half || x >= a.x + half || y < a.y - hh || y >= a.y + 1) continue;
      if (a.y > bestY) { best = a; bestY = a.y; }
    }
    return best;
  }

  // t_139 彩蛋：NPC 命中（与 actorAt 同脚印，只查 NPC）——点保洁阿姨
  // 搭话走这条，不与成员名片流混线
  npcAt(x, y) {
    const g = this.art.geom;
    const half = (g ? g.avatarW : 32) / 2, hh = g ? g.avatarH : 48;
    for (const a of this.actors.values()) {
      if (!a.isNPC) continue;
      if (x < a.x - half || x >= a.x + half || y < a.y - hh || y >= a.y + 1) continue;
      return a;
    }
    return null;
  }

  bubbleAt(x, y) {
    for (const a of this.actors.values()) {
      const bb = a.bubble;
      if (bb && bb.rect && inRect(x, y, bb.rect)) return bb;
    }
    return null;
  }

  onWhiteboard(x, y) {
    const g = this.art.geom;
    if (!g) return false;
    const [bx, by, bw, bh] = g.whiteboard;
    return inRect(x, y, { x: bx, y: by, w: bw, h: bh });
  }

  // t_105 交互物命中（geom.interact）：饮水机与两株绿植
  onDispenser(x, y) {
    const it = this.art.geom && this.art.geom.interact;
    if (!it) return false;
    const [dx, dy, dw, dh] = it.dispenser;
    return inRect(x, y, { x: dx, y: dy, w: dw, h: dh });
  }

  onPlant(x, y) {
    const it = this.art.geom && this.art.geom.interact;
    if (!it) return -1;
    for (let i = 0; i < (it.plants || []).length; i++) {
      const [px, py, pw, ph] = it.plants[i];
      if (inRect(x, y, { x: px, y: py, w: pw, h: ph })) return i;
    }
    return -1;
  }

  // 会议室大屏的命中测试（会中点开看评审、空闲时提示占用态）
  onScreen(x, y) {
    const g = this.art.geom;
    if (!g || !g.meet) return false;
    const [sx, sy, sw, sh] = g.meet.screen;
    return inRect(x, y, { x: sx, y: sy, w: sw, h: sh });
  }

  // 墙上挂钟的命中测试（点开表盘面板——与设置卡「房间时间」行同一张）
  onClock(x, y) {
    const g = this.art.geom;
    if (!g || !g.clock) return false; // 旧 geom 无此字段：钟只当背景
    const [cx, cy, cw, ch] = g.clock;
    return inRect(x, y, { x: cx, y: cy, w: cw, h: ch });
  }

  // 挂钟的表盘面板锚：场景矩形经 resize 的拟合换回视口坐标（clockpop
  // 的第二种锚形态——box 每问必算，面板开在当下这一帧的位置上）
  clockAnchor() {
    const g = this.art.geom;
    const r = this.canvas.getBoundingClientRect();
    const [cx, cy, cw, ch] = g.clock;
    const left = r.left + this.camOffX() + cx * this.camScale();
    const top = r.top + this.camOffY() + cy * this.camScale();
    const width = cw * this.camScale(), height = ch * this.camScale();
    return {
      box: () => ({ left, top, width, height, right: left + width, bottom: top + height }),
      hit: (ev) => {
        if (ev.target !== this.canvas) return false;
        const p = this.canvasPos(ev);
        return this.onClock(p.x, p.y);
      },
    };
  }

  // ── 观察相机事件（r_11 t_158）────────────────────────────────────

  // ZOOM_RANGE/WHEEL_STEP/DRAG_EPS：定稿 §一/§三 契约值（调参区语义）。
  // ZOOM_MIN＝1 是显示下限（2026-10-02 定）：舞台按场景比例贴合，缩到
  // 1x 以下办公室小于画布，四周露出页面白底——最小就是本身大小。
  static get ZOOM_MIN() { return 1; }
  static get ZOOM_MAX() { return 3; }
  static get WHEEL_STEP() { return 1.15; }
  static get DRAG_EPS() { return 4; }

  // onWheel：滚轮缩放（1x–3x 连续，鼠标为锚）；触摸板捏合同走此路
  //（macOS 触摸板 wheel 自带 deltaY——双指捏合映射即此，定稿 §五）
  // zoomAt（r_22 t_201）：锚点公式唯一实现——滚轮/键盘/pinch 三入口
  // 共享（定稿 §一：改 ZOOM_MAX 三路同红）。锚点屏幕坐标＋连续倍率
  // 因子；clamp 在内；返回是否真的变焦（到界 false）。
  zoomAt(mx, my, factor) {
    const oldZoom = this.zoom;
    const nz = Math.max(RoomView.ZOOM_MIN, Math.min(RoomView.ZOOM_MAX, oldZoom * factor));
    if (nz === oldZoom) return false;
    this.panX = mx - (mx - this.camOffX()) * (nz / oldZoom) - this.offX * nz;
    this.panY = my - (my - this.camOffY()) * (nz / oldZoom) - this.offY * nz;
    this.zoom = nz;
    this.clampPan();
    return true;
  }

  onWheel(e) {
    e.preventDefault();
    const r = this.canvas.getBoundingClientRect();
    const mx = e.clientX - r.left, my = e.clientY - r.top;
    if (this.zoomAt(mx, my, e.deltaY < 0 ? RoomView.WHEEL_STEP : 1 / RoomView.WHEEL_STEP)) {
      this.syncCamReset();
    }
  }

  // clampPan：平移边界钳制（定稿 §二——可视区与场景外扩 60px 的交集
  // 不得为空；≤1x 时画布比舞台大，钳制自然归零即拖不动，正确行为）
  clampPan() {
    const r = this.canvas.getBoundingClientRect();
    const cs = this.camScale();
    const sceneW = (this.w + 120) * cs; // 场景外扩 60px 呼吸边（两侧）
    const sceneH = (this.h + 120) * cs;
    // 舞台在视口中的可达范围：pan 使场景矩形不与视口脱交
    const minX = r.width - sceneW - this.offX * this.zoom; // 右边界：场景右缘 ≥ 视口右缘-120边
    const maxX = this.offX * this.zoom + 120 * this.zoom;  // 左边界
    if (minX > maxX) this.panX = (minX + maxX) / 2; // 场景大于视口才有平移余量
    else this.panX = Math.max(minX, Math.min(maxX, this.panX));
    const minY = r.height - sceneH - this.offY * this.zoom;
    const maxY = this.offY * this.zoom + 120 * this.zoom;
    if (minY > maxY) this.panY = (minY + maxY) / 2;
    else this.panY = Math.max(minY, Math.min(maxY, this.panY));
  }

  // onDown/move/up 三段式：拖拽阈值 4px 内算点击（mouseup 派发 onClick）
  onDown(e) {
    if (e.button !== 0) return; // 左键观察（不独占右键）
    const r = this.canvas.getBoundingClientRect();
    this.drag = { x: e.clientX, y: e.clientY, moved: 0, panning: false };
    this._dragMove = (ev) => {
      if (!this.drag) return;
      const dx = ev.clientX - this.drag.x, dy = ev.clientY - this.drag.y;
      this.drag.moved = Math.max(this.drag.moved, Math.hypot(dx, dy));
      if (this.drag.moved >= RoomView.DRAG_EPS) {
        this.drag.panning = true;
        this.canvas.style.cursor = 'grabbing'; // 拖拽中指针固定（§三）
      }
      // 平移增量：相对上一 move 位置（阈值跨过后从当帧起算）
      if (this.drag.panning) {
        if (this.drag.lastX !== undefined) {
          this.panX += ev.clientX - this.drag.lastX;
          this.panY += ev.clientY - this.drag.lastY;
          this.clampPan();
          this.syncCamReset();
        }
        this.drag.lastX = ev.clientX; this.drag.lastY = ev.clientY;
      }
    };
    this._dragUp = (ev) => {
      window.removeEventListener('mousemove', this._dragMove);
      window.removeEventListener('mouseup', this._dragUp);
      const wasDrag = this.drag && this.drag.panning;
      this.drag = null;
      if (!wasDrag) this.onClick(ev); // <4px：算点击（全链照常）
    };
    window.addEventListener('mousemove', this._dragMove);
    window.addEventListener('mouseup', this._dragUp);
  }

  // onDblClick：双击复位（300ms 内两击且都未超阈值）——只在空白处生效
  //（第一击命中可交互物的，复位让位，定稿 §四）
  onDblClick(e) {
    const p = this.canvasPos(e);
    const hitInteractive = !!this.bubbleAt(p.x, p.y) || !!this.actorAt(p.x, p.y) ||
      !!this.npcAt(p.x, p.y) || // t_139 彩蛋：点她说话，复位让位
      this.onWhiteboard(p.x, p.y) || this.onScreen(p.x, p.y) || this.onClock(p.x, p.y) ||
      this.onDispenser(p.x, p.y) || this.onPlant(p.x, p.y) >= 0;
    if (hitInteractive) return; // 让位给 onClick
    this.resetCam(); // 复位（同 0 键）
  }

  // ── r_22 触屏状态机（t_201，定稿 §〇/§三）──────────────────────
  //
  // 同一台观察相机（zoom/pan 状态零契约变更），三个新输入源：单指拖＝
  // 平移（drag 阈值 4px 同鼠标）、双指＝pinch（中点锚+间距比连续
  // zoomAt）、双 tap（<300ms 两击且未超阈）＝复位。touches 全集驱动
  // ——一指抬起剩一指降级为平移不跳变。tap 判定（<4px）转 onClick
  // （三段式同鼠标纪律）。

  touchState() {
    // 惰性单例：{startX/Y, moved, panning, pinchD0, lastTapT, lastTapX/Y, lastPX/Y}
    if (!this._touch) this._touch = {};
    return this._touch;
  }

  onTouchStart(e) {
    e.preventDefault(); // 画布区全吃（touch-action:none 双保险）
    this._touchActive = true; // 44px 补偿启用（§二——读类命中）
    const r = this.canvas.getBoundingClientRect();
    const ts = this.touchState();
    const t0 = e.touches[0];
    ts.startX = t0.clientX; ts.startY = t0.clientY;
    ts.moved = 0; ts.panning = false;
    ts.lastPX = t0.clientX; ts.lastPY = t0.clientY;
    if (e.touches.length === 2) {
      // pinch 起点：两指距为手势基准（间距比＝zoomAt factor）
      ts.pinchD0 = Math.hypot(
        e.touches[0].clientX - e.touches[1].clientX,
        e.touches[0].clientY - e.touches[1].clientY);
      ts.pinchZ0 = this.zoom;
      ts.panning = false; // 两指态不吃单指拖
    } else {
      ts.pinchD0 = 0;
    }
    this.paintTouchRing(t0.clientX - r.left, t0.clientY - r.top);
  }

  onTouchMove(e) {
    e.preventDefault();
    const ts = this.touchState();
    if (e.touches.length === 2 && ts.pinchD0 > 0) {
      // pinch：两指中点为锚、间距比连续 zoomAt（一次手势多帧）
      const r = this.canvas.getBoundingClientRect();
      const mx = (e.touches[0].clientX + e.touches[1].clientX) / 2 - r.left;
      const my = (e.touches[0].clientY + e.touches[1].clientY) / 2 - r.top;
      const d = Math.hypot(
        e.touches[0].clientX - e.touches[1].clientX,
        e.touches[0].clientY - e.touches[1].clientY);
      if (this.zoomAt(mx, my, (d / ts.pinchD0) * (this.zoom / ts.pinchZ0) || 1)) {
        this.syncCamReset();
      }
      // 基准滚动：把比值锚到手势全程（不累积——每帧从手势基准算绝对比）
      return;
    }
    if (e.touches.length !== 1) return;
    const t = e.touches[0];
    const dx = t.clientX - ts.startX, dy = t.clientY - ts.startY;
    ts.moved = Math.max(ts.moved, Math.hypot(dx, dy));
    if (ts.moved >= RoomView.DRAG_EPS) ts.panning = true;
    if (ts.panning) {
      // 单指拖＝平移（增量同鼠标三段式）
      this.panX += t.clientX - (ts.lastPX ?? t.clientX);
      this.panY += t.clientY - (ts.lastPY ?? t.clientY);
      this.clampPan();
      this.canvas.style.cursor = 'grabbing';
      this.syncCamReset();
    }
    ts.lastPX = t.clientX; ts.lastPY = t.clientY;
  }

  onTouchEnd(e) {
    const ts = this.touchState();
    // 一指抬起剩一指：降级为平移（重定单指基准，不跳变）
    if (e.touches.length === 1) {
      const t = e.touches[0];
      ts.startX = t.clientX; ts.startY = t.clientY;
      ts.lastPX = t.clientX; ts.lastPY = t.clientY;
      ts.pinchD0 = 0;
      ts.panning = true; // 已在手势中——直接续平移
      ts.moved = RoomView.DRAG_EPS + 1;
      return;
    }
    if (e.touches.length > 0) return;
    // 全抬起：tap 判定（<4px）→ onClick 或双 tap 复位
    const wasPan = ts.panning;
    ts.panning = false; ts.pinchD0 = 0;
    this.canvas.style.cursor = 'default';
    if (!wasPan) {
      const now = performance.now();
      const t = e.changedTouches[0];
      if (t) {
        if (ts.lastTapT && now - ts.lastTapT < 300 &&
          Math.hypot(t.clientX - (ts.lastTapX ?? t.clientX), t.clientY - (ts.lastTapY ?? t.clientY)) < 40) {
          // 双 tap 复位（与鼠标双击同语义——命中可交互物的让位在 onDblClick；
          // 触屏版直接复位空白tap，交互物tap走 onClick 单击）
          this.resetCam ? this.resetCam() : (this.zoom = 1, this.panX = 0, this.panY = 0);
          ts.lastTapT = 0;
          return;
        }
        ts.lastTapT = now; ts.lastTapX = t.clientX; ts.lastTapY = t.clientY;
        this.onClick({ clientX: t.clientX, clientY: t.clientY }); // tap＝点击（三段式同款）
      }
    }
  }

  // paintTouchRing：按压圈态（§五·1——白圈空地/蓝圈命中，视觉在 draw
  // 里按 _touchRing 画：120ms 出场 80ms 收场，判定即写）
  paintTouchRing(cx, cy) {
    const p = this.canvasToScene(this.touchState().startX ?? cx, this.touchState().startY ?? cy);
    const hit = !!(this.actorAt(p.x, p.y) || this.bubbleAt(p.x, p.y) ||
      this.onWhiteboard(p.x, p.y) || this.onScreen(p.x, p.y) || this.onClock(p.x, p.y) ||
      this.onDispenser(p.x, p.y) || this.onPlant(p.x, p.y) >= 0);
    this._touchRing = { x: cx, y: cy, t: performance.now(), hit };
  }

  // canvasToScene：client 坐标→场景（canvasPos 同式；tap 补偿用）
  canvasToScene(clientX, clientY) {
    const r = this.canvas.getBoundingClientRect();
    return {
      x: (clientX - r.left - this.camOffX()) / this.camScale(),
      y: (clientY - r.top - this.camOffY()) / this.camScale(),
    };
  }

  // 键盘观察键（定稿 §一）：+ = 放大、- 缩小、0 复位——与方向键不冲突
  observeKey(e) {
    if (e.key === '+' || e.key === '=') {
      this.zoomByStep(RoomView.WHEEL_STEP);
      return true;
    }
    if (e.key === '-') {
      this.zoomByStep(1 / RoomView.WHEEL_STEP);
      return true;
    }
    if (e.key === '0') {
      this.resetCam();
      return true;
    }
    return false;
  }

  zoomByStep(f) {
    // 键盘缩放锚画布中心——zoomAt 共享（r_22 t_201 消复制粘贴）
    const r = this.canvas.getBoundingClientRect();
    if (this.zoomAt(r.width / 2, r.height / 2, f)) this.syncCamReset();
  }

  // resetCam（t_158 补）：观察相机归位——0 键/双击空白/框外角标钮三口归一
  resetCam() {
    this.zoom = 1; this.panX = 0; this.panY = 0;
    this.syncCamReset();
  }

  // syncCamReset（t_158 补）：框外复位钮的现身条件——离家（zoom≠1 或
  // 有平移）才露面；在家则 hidden（归位钮在家是 no-op，不占注意力）
  syncCamReset() {
    if (this.camResetBtn) {
      this.camResetBtn.hidden = this.zoom === 1 && !this.panX && !this.panY;
    }
  }

  onMove(e) {
    if (this.drag && this.drag.panning) return; // 拖拽中指针固定（§三）
    const p = this.canvasPos(e);
    this.hoverX = p.x; this.hoverY = p.y; this.hoverOn = true;
    const onActor = !!this.actorAt(p.x, p.y);
    const onNPC = !!this.npcAt(p.x, p.y); // t_139 彩蛋：阿姨可点
    const onBubble = !!this.bubbleAt(p.x, p.y);
    const onBoard = this.onWhiteboard(p.x, p.y);
    const onScreen = this.onScreen(p.x, p.y);
    const onClock = this.onClock(p.x, p.y);
    const onProps = this.onDispenser(p.x, p.y) || this.onPlant(p.x, p.y) >= 0;
    // t_123：打印机/茶水间小件/工位/地板彩蛋的指针反馈
    const pr = this.art.geom && this.art.geom.printer;
    let onPrint = false;
    if (pr) {
      const [rx, ry, rw, rh] = pr.rect;
      onPrint = p.x >= rx && p.x < rx + rw && p.y >= ry && p.y < ry + rh;
    }
    const onInter = !!this.interact.hitPantry(p.x, p.y) || onPrint ||
      !!this.interact.hitFloor(p.x, p.y);
    this.canvas.style.cursor = (onBubble || onActor || onNPC || onBoard || onScreen || onClock || onProps || onInter) ? 'pointer' : 'default';
  }

  onClick(e) {
    const p = this.canvasPos(e);
    const bubble = this.bubbleAt(p.x, p.y);
    if (bubble) { this.showBubbleText(bubble); return; }
    // t_139 彩蛋：点保洁阿姨——她回头搭话（三连点有小惊喜）
    if (this.npcAt(p.x, p.y)) { this.cleaner.poke(); return; }
    const a = this.actorAt(p.x, p.y);
    if (a) {
      // I7 摸成员（t_123）：名片之外补一个挥手动作＋wave 泡
      this.interact.onMember(a.name);
      this.ev.openPerson?.(a.name);
      return;
    }
    // I1 摸打印机（t_123）：LED 快闪＋托盘纸跳（geom.printer 全套）
    {
      const pr = this.art.geom && this.art.geom.printer;
      if (pr) {
        const [rx, ry, rw, rh] = pr.rect;
        if (p.x >= rx && p.x < rx + rw && p.y >= ry && p.y < ry + rh) {
          this.interact.live.set('printer', { t: 0 });
          return;
        }
      }
    }
    // I4/I8/I9/I10 茶水间小件（t_123）：咖啡机/垃圾桶/零食架/微波炉
    if (this.interact.onPantry(p.x, p.y)) return;
    // 收藏品货架（t_188 荣誉墙改版）：点展示柜开成就清单页签（黑板深链）
    if (this.interact.hitHonorWall(p.x, p.y)) { location.hash = '#/kb/achievements'; return; }
    // I11 摸工位（t_123）：屏幕亮闪一下
    if (this.interact.onDesk(p.x, p.y)) return;
    // I12 摸地板（t_123）：脚印粒子彩蛋——只加反馈不消费（地板点击的
    // 既有语义「不响应左键走位」由函数尾部兜底，这里不 return）
    this.interact.onFloor(p.x, p.y);
    if (this.onWhiteboard(p.x, p.y)) { this.writeBoard(); return; }
    if (this.onDispenser(p.x, p.y)) { this.pourWater(); return; }
    const plant = this.onPlant(p.x, p.y);
    if (plant >= 0) { this.waterPlant(plant); return; }
    if (this.onClock(p.x, p.y)) { openClockPop(this.clockAnchor()); return; }
    if (this.onScreen(p.x, p.y)) {
      const m = this.meeting;
      if (m && m.status === 'live') {
        this.ev.toast?.(t('需求评审会进行中：{req}《{title}》｜主持 {chair}（编排者）｜参会 {who}', {
          req: m.req || '', title: m.req_title || '', chair: m.chair,
          who: (m.participants || []).join(t('、')),
        }), 'ok');
        this.ev.openBoard?.('project');
      } else if (this.pendingPlan) {
        const pl = this.pendingPlan;
        this.ev.toast?.(t('提案评审中：{id}「{title}」· 提交人 {by}，正与房主在会议室过稿', {
          id: pl.id || '', title: pl.title || '', by: pl.submitted_by || '—',
        }), 'ok');
        this.ev.openBoard?.('project');
      } else {
        this.ev.toast?.(t('会议室空闲（屏幕熄灭）——需求在库且编排者与相关成员空闲时，编排者会召集评审；提案待审时房主与提交人也会在此过稿'), 'ok');
      }
      return;
    }
    // 地板不响应左键走位：房主的脚只认键盘（方向键/WASD）——点击保
    // 留给交互物（气泡/小牛马/白板/挂钟/大屏），误点地板不再把人拽走
  }

  // ── t_105 物品交互（r_01 §三）──────────────────────────────────────
  //
  // 白板可写一行：点击白板弹一行输入（房主），确认后写进当前房的群公告
  // 通道（notice_save）——全员可见、房间留痕，「替代部分公告功能」的正
  // 解。空输入或取消不动任何东西；非房主只读提示。

  async writeBoard() {
    const key = this.roomKey;
    if (!key) return;
    let notice = null;
    try { notice = await getRoomNotice(key); } catch { /* 读不到就当首发 */ }
    // Dialog 的 html 只收字符串——input 的值在关闭前由 input 事件存进闭包
    let line = '';
    const holder = document.createElement('div');
    holder.innerHTML =
      '<input data-wb-line type="text" maxlength="40" placeholder="' + t('写一句话上墙…') + '" ' +
      'style="width:340px;box-sizing:border-box;padding:8px 10px;font:inherit;font-size:13px;' +
      'border:1px solid var(--line-2);border-radius:8px;background:var(--board);color:var(--chalk)"/>';
    const input = holder.firstChild;
    if (notice && notice.text) input.value = notice.text.split('\n')[0].slice(0, 40);
    input.addEventListener('input', () => { line = input.value; });
    const dlg = await Dialog.show({
      title: t('白板写一行'),
      desc: t('写下的内容全员可见（作为本房公告）；清空并「写上」即撤下'),
      html: holder.innerHTML,
      size: 's',
      actions: [
        { label: t('取消'), value: null },
        { label: t('写上'), primary: true, value: 'ok' },
      ],
    });
    if (dlg !== 'ok') return; // 取消/ESC/遮罩——什么都不动
    const text = line.trim();
    // 房主写面：notice_save（expect_rev 条件写）。写上的同时白板会亮
    // 新字（公告帧回来经 notice 渲染链）；空串即撤下公告。
    const expect = notice?.rev ?? 0;
    const ok = this.book.frameTo(key, {
      type: Msg.NoticeSave, text, expect_rev: expect, silent: true,
    }, t('白板写字'));
    if (ok) this.ev.toast?.(text ? t('已写上白板：「{text}」', { text }) : t('白板已擦净（公告撤下）'), 'ok');
  }

  // pourWater：饮水机倒水动画（点击）＋空闲成员路过自发使用（氛围行为）
  pourWater() {
    // 主人的手不占成员外勤；动画重开即响（旧版 pourT>0 静默吞点击——
    // 正在出水时点机器毫无反馈是同一族「静默 no-op」病）
    this.pourT = 1.2;           // 动画时长（秒）
    sfx('scene-pour'); // r_09：出水首帧汩汩
    this.ev.toast?.(t('接了一杯水') + t('（饮水机）'), 'ok');
  }

  // waterPlant：绿植浇水（点击）——成长度纯装饰，本地态
  waterPlant(i) {
    const now = (this.clock || 0);
    this.plantWater = this.plantWater || new Map();
    this.plantWater.set(i, now);
    this.ev.toast?.(t('给绿植浇了点水（它会慢慢长的）'), 'ok');
  }

  onKey(e, down) {
    const tag = (e.target && e.target.tagName) || '';
    if (down && (tag === 'INPUT' || tag === 'TEXTAREA' || e.target?.isContentEditable)) {
      // r_23 补钉（r_32 扩面）：焦点在回放控件（滑杆/⟲/实时锚/选日/
      // 倍速）上时 ←/→/Esc/Space 仍走回放键——否则拖完滑杆键盘步进就
      // 死；别的输入框打字不受扰
      const rc = this._replayUI;
      if (!(this.replayT >= 0 && rc && rc.wrap.contains(e.target))) return;
    }
    // r_23 回放键优先（回放态下 ←/→ 是步进不是走位、Esc 是退出不是
    // 观察键、Space 是播放暂停）——必须挡在移动键分栏之前，Esc 才到得
    // 了 replayKey；只吃 keydown（keyup 再吃一遍就是一步 10s）
    if (down && this.replayT >= 0 &&
        (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Escape' || e.key === ' ') &&
        this.replayKey(e)) {
      e.preventDefault();
      return;
    }
    const keys = ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'w', 'a', 's', 'd'];
    if (!keys.includes(e.key)) {
      // 观察键（t_158）：+/-/0——可见时才吃，preventDefault 防浏览器缩放
      if (this.visible && this.observeKey(e)) e.preventDefault();
      return;
    }
    if (!this.visible) return;
    e.preventDefault();
    if (down) this.keys.add(e.key); else this.keys.delete(e.key);
  }

  canvasPos(e) {
    // pane px → scene px, through the letterbox fit + observe camera
    //（观察缩放/平移在这层逆变换——定稿 §五：命中框一层逆变换）
    const r = this.canvas.getBoundingClientRect();
    return {
      x: (e.clientX - r.left - this.camOffX()) / this.camScale(),
      y: (e.clientY - r.top - this.camOffY()) / this.camScale(),
    };
  }

  showBubbleText(b) {
    const overlay = document.getElementById('bubble-overlay');
    if (!overlay) return;
    const time = b.ts ? new Date(b.ts * 1000).toLocaleTimeString(localeTag(), { hour12: false }) : '';
    const head = `${b.from}${time ? ' · ' + time : ''}${b.report ? ' · ' + t('汇报') : ''}`;
    overlay.querySelector('.bo-who').textContent = head;
    // 关闭钮图标只填一次（静态 DOM 持久层，textContent 每次都换的是 bo-who）
    const x = overlay.querySelector('.bo-x');
    if (!x.firstElementChild) x.innerHTML = icon('close', 14);
    // 正文与对话流同一渲染语言（markdown.js ＋ stream.js 的 dress）：头顶
    // 小气泡被 plainifyMD 剥成净文本预告，点开这里读到的才是完整排版
    overlay.querySelector('.bo-body').innerHTML = markdown(b.raw, { dress });
    overlay.hidden = false;
    overlay.onclick = (ev) => {
      if (ev.target.closest('.bo-x')) { dismissHide(overlay); return; }
      const chip = ev.target.closest('.task-chip');
      if (chip) { this.ev.openBoard?.('project'); return; } // t_NN → 项目看板（任务的家，操作都在那）
      const pf = ev.target.closest('[data-pfat]');
      if (pf) { this.ev.openPerson?.(pf.dataset.pfat); return; } // @提及 → 牛马主页（与点小牛马同路）
      // 卡面内点了不收（长文要滚动、选中复制——点正文关弹窗读不下去），
      // 点遮罩才收（牛马名片卡同款「点外部关」）
      if (ev.target !== overlay) return;
      dismissHide(overlay);
    };
  }

  // showDoc（黑板写播报卡「看全文」的落点，stream 经 app.js 转来）：
  // 整篇文档与气泡点开同一张全文卡——markdown 渲染与黑板板块同语言
  // （chat/markdown.js），头行给题名＋最近更新时间
  showDoc(d) {
    this.showBubbleText({
      from: d?.title || d?.key || t('黑板'),
      ts: d?.updated_ts || 0,
      raw: d?.body || '',
      report: false,
    });
  }

  // paintShowcase（t_188 荣誉墙改版）：收藏品货架的运行时奖杯 roster——
  // 最近 8 枚达成（解锁键按 ts 倒序）摆上两层货架，整柜 8 展位由运行
  // 时接管（清位重绘，空位给展示底座），盖过 back 层静息样例；快照
  // 缺席（未达任意成就）时静息面照旧。柜面坐标与 pixart drawShowcase
  // 同源：legacy (172,30) 起 40×28、4 展位×9、两层（架面在柜内第
  // 12/22 行），奖杯 7×8。r_32.2：入参 count（绘制只消费计数——排序
  // 前 8 枚的画法与具体哪枚无关；回放的计数随帧走，缺省读实时成就）。
  paintShowcase(ctx, count) {
    const n = count === undefined ? achTrophyCount() : count;
    if (!n) return;
    const entries = new Array(n);
    const X = 172 * 2, Y = 30 * 2; // 柜面 native 原点
    const PALS = [['#f2c14e', '#b8860b'], ['#c8cfd6', '#8f959e'], ['#e0a03a', '#a05a2a']];
    for (let i = 0; i < 8; i++) {
      const slot = i % 4, deck = (i / 4) | 0;
      const x = X + (2 + slot * 9) * 2;
      const y = Y + (deck ? 14 : 4) * 2; // 展位行：一层 4-11 / 二层 14-21
      ctx.fillStyle = '#1c1f25';         // 清位（盖掉样例/旧底座）
      ctx.fillRect(x, y, 14, 16);
      if (i >= entries.length) {
        ctx.fillStyle = '#3d454f';       // 空位展示底座
        ctx.fillRect(x + 4, y + 14, 6, 2);
        continue;
      }
      // 站姿奖杯（drawTrophy 孪生）——金银铜按 i 轮换，最近达成金
      const pal = PALS[i % 3];
      ctx.fillStyle = pal[0];
      ctx.fillRect(x + 2, y, 10, 2);      // 杯口沿
      ctx.fillRect(x, y + 2, 14, 2);      // 杯腹最宽（连耳）
      ctx.fillRect(x + 2, y + 4, 10, 2);  // 杯腹
      ctx.fillRect(x + 4, y + 6, 6, 2);   // 杯颈收束
      ctx.fillRect(x + 6, y + 8, 2, 2);   // 杯柄
      ctx.fillRect(x + 4, y + 10, 6, 2);  // 底座张口
      ctx.fillStyle = pal[1];
      ctx.fillRect(x, y + 4, 2, 2);       // 杯耳
      ctx.fillRect(x + 12, y + 4, 2, 2);
      ctx.fillRect(x + 4, y + 12, 6, 2);  // 座踝
      ctx.fillRect(x + 2, y + 14, 10, 2); // 底板
      ctx.fillStyle = '#ffffff';
      ctx.fillRect(x + 4, y, 2, 2);       // 口沿高光
    }
  }

  // ── r_23 时间轴回放 UI（§四契约）＋ r_32 当日播放器 ───────────────
  //
  // ⟲ 圆钮开合，面板两档源：ring（最近 10 分钟即席回看——滑杆拖动）与
  // day（当日/历史日文件——选日、播放/暂停、倍速、导出）。三路退出（点
  // 实时锚/再点 ⟲/Esc），←/→ 步进 5s、Space 播放暂停。访客态（/view）
  // 不建此 UI——历史是房主私产面（§三联动条款，DOM 无 replay 类名）；
  // 分享页（/dayreplay）建播放面但无导出面。

  buildReplayUI() {
    if (globalThis.__NIUMA_VISITOR__) return; // 访客档位：不开放时间轴
    const stage = this.canvas.parentElement;
    const wrap = document.createElement('div');
    wrap.id = 'replay-ui';
    wrap.innerHTML =
      '<button id="replay-btn" type="button" title="' + t('时间轴回放（←/→ 步进 5s · Space 播放/暂停）') + '">⟲</button>' +
      '<div id="replay-bar" hidden>' +
      '<select id="replay-day" class="rp-day" title="' + t('回放日期') + '"></select>' +
      '<button id="replay-play" type="button" class="rp-play" title="' + t('播放/暂停（Space）') + '">▶</button>' +
      '<select id="replay-speed" class="rp-speed" title="' + t('倍速') + '">' +
      '<option value="1">1×</option><option value="2">2×</option><option value="4" selected>4×</option>' +
      '<option value="8">8×</option><option value="16">16×</option><option value="60">60×</option>' +
      '</select>' +
      '<span id="replay-earliest" class="rp-t"></span>' +
      '<input id="replay-slider" type="range" min="0" max="0" step="1">' +
      '<span id="replay-latest" class="rp-t"></span>' +
      '<button id="replay-live" type="button" class="rp-live">● ' + t('实时') + '</button>' +
      '<button id="replay-export" type="button" class="rp-export" title="' + t('导出「公司一日回放」视频') + '">⬇</button>' +
      '</div>';
    stage.appendChild(wrap);
    const btn = wrap.querySelector('#replay-btn');
    const bar = wrap.querySelector('#replay-bar');
    const slider = wrap.querySelector('#replay-slider');
    const live = wrap.querySelector('#replay-live');
    const est = wrap.querySelector('#replay-earliest');
    const lst = wrap.querySelector('#replay-latest');
    const day = wrap.querySelector('#replay-day');
    const play = wrap.querySelector('#replay-play');
    const speed = wrap.querySelector('#replay-speed');
    const exp = wrap.querySelector('#replay-export');
    btn.addEventListener('click', () => this.toggleReplay());
    live.addEventListener('click', () => this.exitReplay());
    slider.addEventListener('input', () => {
      this.replayT = Number(slider.value);
      this.setReplayPlaying(false); // 拖动即停——不与自动前进抢时间轴
    });
    day.addEventListener('change', () => this.selectReplayDay(day.value));
    play.addEventListener('click', () => this.toggleReplayPlay());
    speed.addEventListener('change', () => { this.replaySpeed = Number(speed.value) || 4; });
    if (!globalThis.__NIUMA_DAYREPLAY__) {
      exp.addEventListener('click', () => this.openDayExport());
    } else {
      exp.remove(); // 分享页无导出面（视频是房主侧产物）
    }
    this._replayUI = { wrap, btn, bar, slider, live, est, lst, day, play, speed };
  }

  toggleReplay() {
    if (this.replayT >= 0) { this.exitReplay(); return; }
    // 环不足 2s 也开面板——r_32 的主用法是选历史日（ring 只是即席档）
    this.replaySrc = 'ring';
    this.replayT = this.replayBuf.latestT; // 从「现在」开始往回拖
    this._replayUI.bar.hidden = false;
    this._replayUI.btn.classList.add('on');
    this.refreshReplayDays();
    this.refreshReplaySlider();
  }

  exitReplay() {
    this.replayT = -1; // 1 帧回实时（架构白送：实时从未停）
    this.replaySrc = 'ring';
    this.dayBuf = null; // 日缓冲随收档（下次选日重拉）
    this.setReplayPlaying(false);
    if (this._replayUI) {
      this._replayUI.bar.hidden = true;
      this._replayUI.btn.classList.remove('on');
      this._replayUI.day.value = '';
    }
  }

  // refreshReplayDays 灌日期下拉：即席环档 ＋ 有录制的日子（服务端
  // days 面；不可用时只剩环档——即席回看照常）。分享页只有授权日一档
  // （days 面是房主数据面，不问）。
  async refreshReplayDays() {
    const ui = this._replayUI;
    if (!ui || !this.roomKey) return;
    const dr = globalThis.__NIUMA_DAYREPLAY__;
    if (dr && dr.date) {
      ui.day.innerHTML = `<option value="${dr.date}">${dr.date}</option>`;
      return;
    }
    try {
      const { days } = await getReplayDays(this.roomKey);
      ui.day.innerHTML = ['<option value="">' + t('最近 10 分钟') + '</option>']
        .concat((days || []).map((d) => `<option value="${d}">${d}</option>`))
        .join('');
    } catch { /* days 面不可用——环档兜底 */ }
  }

  // selectReplayDay 是 day 档的入口：'' 回即席环，'YYYY-MM-DD' 拉当日
  // 全量帧（分页折入 dayBuf，t=墙钟 ms）并从日头开演。
  async selectReplayDay(date) {
    if (!date) {
      this.replaySrc = 'ring';
      this.dayBuf = null;
      this.setReplayPlaying(false);
      this.replayT = this.replayBuf.latestT;
      this.refreshReplaySlider();
      return;
    }
    if (!this.roomKey || this.replayLoading) return;
    this.replayLoading = true;
    try {
      const rows = [];
      let since = 0;
      for (;;) {
        const page = await getReplayFrames(this.roomKey, date,
          { since, limit: 2000, token: this.replayToken || undefined });
        rows.push(...(page.frames || []));
        if (!page.has_more || !page.frames.length) break;
        since = page.next_since;
      }
      const buf = dayBufOf(rows);
      if (buf.frames.length < 2) {
        this.ev.toast?.(t('{date} 还没有可回放的录像', { date }), 'err');
        this._replayUI.day.value = '';
        return;
      }
      this.dayBuf = buf;
      this.replaySrc = 'day';
      this.replayT = buf.earliestT;
      this._replayUI.bar.hidden = false;
      this._replayUI.btn.classList.add('on');
      this.refreshReplaySlider();
      this.setReplayPlaying(true); // 选了日子就开演
    } catch (e) {
      this.ev.toast?.(t('回放读取失败：{msg}', { msg: e.message || e }), 'err');
      this._replayUI.day.value = '';
    } finally {
      this.replayLoading = false;
    }
  }

  toggleReplayPlay() {
    if (this.replaySrc !== 'day' || !this.dayBuf || this.dayBuf.frames.length < 2) return;
    this.setReplayPlaying(!this.replayPlaying);
  }

  setReplayPlaying(on) {
    this.replayPlaying = on && this.replaySrc === 'day';
    const ui = this._replayUI;
    if (ui && ui.play) {
      ui.play.textContent = this.replayPlaying ? '⏸' : '▶';
      ui.play.classList.toggle('on', this.replayPlaying);
    }
  }

  // replayBufActive 是回放时间轴的当前源（day=当日文件 / ring=即席环）。
  replayBufActive() {
    return this.replaySrc === 'day' ? this.dayBuf : this.replayBuf;
  }

  // replaySec 是走路摆帧的秒钟（day 源的 t 是墙钟 ms）。
  replaySec() {
    return this.replaySrc === 'day' ? this.replayT / 1000 : this.replayT;
  }

  // replayWallT 是回放时间轴对应的墙钟 ms（day 源原生；ring 源倒推）。
  replayWallT() {
    if (this.replaySrc === 'day') return this.replayT;
    return Date.now() - (this.clock - this.replayT) * 1000;
  }

  refreshReplaySlider() {
    if (!this._replayUI || this.replayT < 0) return;
    const buf = this.replayBufActive();
    if (!buf || !buf.frames.length) return;
    const { slider, est, lst } = this._replayUI;
    slider.min = Math.round(buf.earliestT);
    slider.max = Math.round(buf.latestT);
    slider.value = Math.round(this.replayT);
    const fmt = (tt) => (this.replaySrc === 'day'
      ? new Date(tt)
      : new Date(Date.now() - (this.clock - tt) * 1000))
      .toLocaleTimeString(localeTag(), { hour12: false });
    est.textContent = fmt(buf.earliestT);
    lst.textContent = fmt(buf.latestT);
  }

  // replayKey：回放键盘（§四 ←/→ 5s；Esc 退出；r_32 Space 播放暂停）
  // ——与相机键（+/-/0）不冲突。挂在 onKey 的观察键段。
  replayKey(e) {
    if (this.replayT < 0) return false;
    if (e.key === 'Escape') { this.exitReplay(); return true; }
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      const buf = this.replayBufActive();
      if (!buf) return false;
      const step = this.replaySrc === 'day' ? 5000 : 5;
      this.replayT = Math.max(buf.earliestT,
        Math.min(buf.latestT, this.replayT + (e.key === 'ArrowLeft' ? -step : step)));
      this.refreshReplaySlider();
      return true;
    }
    if (e.key === ' ') { this.toggleReplayPlay(); return true; }
    return false;
  }

  // ── r_32 导出卡：公司一日回放的产物面（视频文件＋分享链接）──────
  //
  // ⬇ 从回放面板进来。两张产物：① WebM 视频文件（离屏画布逐帧录制，
  // 烧日期时间码，空白时段硬切——发社区的主产物，不依赖服务器可达）；
  // ② 当日分享链接（token 门控只读页——服务器可达时可用，含气泡文本
  // 故先确认再铸）。
  async openDayExport() {
    const ui = this._replayUI;
    const day = ui && ui.day ? ui.day.value : '';
    if (this.replaySrc !== 'day' || !this.dayBuf || this.dayBuf.frames.length < 2 || !day) {
      this.ev.toast?.(t('先选一个回放日期再导出'), 'err');
      return;
    }
    if (!pickMime()) {
      this.ev.toast?.(t('此环境不支持视频录制，无法导出视频（回放本身不受影响）'), 'err');
      return;
    }
    const stage = this.canvas.parentElement;
    if (stage.querySelector('#day-export')) return; // 卡已开
    const room = this.book.rooms.get(this.roomKey);
    const studio = (room && room.name) || this.roomKey || '';
    const card = document.createElement('div');
    card.id = 'day-export';
    card.innerHTML =
      '<header><strong>' + t('公司一日回放') + '</strong><span>' + day + '</span>' +
      '<button type="button" class="de-x" aria-label="' + t('关闭') + '">×</button></header>' +
      '<div class="de-row"><span class="de-k">' + t('范围') + '</span>' +
      '<label class="de-opt"><input type="radio" name="de-range" value="day" checked>' + t('整天') + '</label>' +
      '<label class="de-opt"><input type="radio" name="de-range" value="here">' + t('当前时间轴起 30 分钟') + '</label></div>' +
      '<div class="de-row"><span class="de-k">' + t('成片时长') + '</span>' +
      '<label class="de-opt"><input type="radio" name="de-dur" value="30">' + t('30 秒') + '</label>' +
      '<label class="de-opt"><input type="radio" name="de-dur" value="60" checked>' + t('1 分钟') + '</label>' +
      '<label class="de-opt"><input type="radio" name="de-dur" value="180">' + t('3 分钟') + '</label></div>' +
      '<p class="de-note">' + t('空白时段（无人在线）自动跳过；导出按成片时长实时录制，期间可做别的。') + '</p>' +
      '<div class="de-prog" hidden><div class="de-bar"><i></i></div><span class="de-pct">0%</span>' +
      '<button type="button" class="de-stop">' + t('取消') + '</button></div>' +
      '<div class="de-link" hidden><input readonly><button type="button" class="de-copy">' + t('复制') + '</button></div>' +
      '<footer><button type="button" class="de-share">🔗 ' + t('分享这一天（链接）') + '</button>' +
      '<button type="button" class="de-go">' + t('开始导出') + '</button></footer>';
    stage.appendChild(card);
    const $ = (sel) => card.querySelector(sel);
    $('.de-x').addEventListener('click', () => card.remove());

    let cancelled = false;
    const go = $('.de-go'), stop = $('.de-stop');
    go.addEventListener('click', async () => {
      const range = card.querySelector('input[name="de-range"]:checked').value;
      const dur = Number(card.querySelector('input[name="de-dur"]:checked').value) || 60;
      const t0 = range === 'here' ? this.replayT : this.dayBuf.earliestT;
      const t1 = range === 'here'
        ? Math.min(this.dayBuf.latestT, t0 + 30 * 60 * 1000)
        : this.dayBuf.latestT;
      if (!(t1 > t0)) { this.ev.toast?.(t('所选区间没有录像'), 'err'); return; }
      go.disabled = true;
      card.querySelector('.de-prog').hidden = false;
      const bar = card.querySelector('.de-bar i'), pct = $('.de-pct');
      try {
        const blob = await exportDayReplay({
          rv: this, t0, t1, targetSec: dur, scale: 2,
          hudLabel: `${studio} · ${t('公司一日回放')}`,
          onProgress: (f) => {
            bar.style.width = (f * 100).toFixed(1) + '%';
            pct.textContent = Math.round(f * 100) + '%';
          },
          cancelled: () => cancelled,
        });
        downloadBlob(blob, `niuma-dayreplay-${day.replace(/-/g, '')}-${this.roomKey}.webm`);
        this.ev.toast?.(t('回放视频已导出'), 'ok');
        card.remove();
      } catch (e) {
        this.ev.toast?.(t('导出失败：{msg}', { msg: e.message || e }), 'err');
        go.disabled = false;
        card.querySelector('.de-prog').hidden = true;
      }
    });
    stop.addEventListener('click', () => { cancelled = true; stop.disabled = true; });

    $('.de-share').addEventListener('click', async () => {
      const ok = await Dialog.confirm({
        title: t('分享这一天？'),
        text: t('链接包含当日回放画面与对话气泡文本；随时可关闭分享，链接即失效。'),
      });
      if (!ok) return;
      try {
        const out = await postReplayShare(this.roomKey, { date: day, confirm: 'REPLAY' });
        const url = location.origin + out.path;
        const row = card.querySelector('.de-link');
        row.hidden = false;
        row.querySelector('input').value = url;
        row.querySelector('.de-copy').addEventListener('click', async () => {
          const ok = await copyToClipboard(url);
          this.ev.toast?.(ok
            ? t('链接已复制——仅在服务器可达时能打开（发社区推荐用视频文件）')
            : t('复制失败，请手动选中复制'), ok ? 'ok' : 'err');
        });
      } catch (e) {
        this.ev.toast?.(t('分享失败：{msg}', { msg: e.message || e }), 'err');
      }
    });
  }

  // paintFoodHand：吃链中的食品手持件（r_07/t_136）——条目 Hand 形
  //（5×5 legacy 字符画）canvas 直绘在身前，色取条目双色调色。
  paintFoodHand(ctx, a) {
    if (a.holding !== 'food' || !a.foodKey) return;
    const f = (globalThis.__NIUMA_FOODS__ || []).find((x) => x && x.key === a.foodKey);
    if (!f) return;
    const sway = a.moving ? (Math.floor(a.animT * 6) % 2 ? 1 : 0) : 0;
    const x = Math.round(a.x + 6), y = Math.round(a.y - 16 + sway);
    for (let ry = 0; ry < f.hand.length; ry++) {
      for (let rx = 0; rx < f.hand[ry].length; rx++) {
        const ch = f.hand[ry][rx];
        if (ch === '.') continue;
        ctx.fillStyle = ch === '2' ? f.hex2 : f.hex;
        ctx.fillRect(x + rx * 2, y + ry * 2, 2, 2); // 1 legacy px ＝ 2 native
      }
    }
  }

  // ── painting ────────────────────────────────────────────────────
  brightDesks() {
    const g = this.art.geom;
    const out = new Array(g ? g.desks.length : 0).fill(false);
    for (const [idx, name] of this.desks.use) {
      if (idx < 0 || idx >= out.length) continue;
      const a = this.actors.get(name);
      if (a && a.working && a.label) out[idx] = true;
    }
    return out;
  }

  // ── r_32.2 全量视态：录制快照与绘制状态源 ────────────────────────
  // 第一性：回放要与实时同一张脸，唯一构造性做法是「同一画笔、状态源
  // 切换」——draw 链的每一处状态读都走 VS（视态）：实时＝liveViewState
  // （板上活状态的快照），回放＝shadowRoomAt（帧还原的同形鸭子型）。
  // 录制腿的 replayRoomSnap 是同一张脸的 wire 版（量化压变化过滤噪声）。

  // liveViewState：实时视态（精确值，不量化）。
  liveViewState() {
    const prn = this.printer.snapshot();
    const room = this.book && this.book.rooms ? this.book.rooms.get(this.roomKey) : null;
    return {
      clock: this.clock || 0,
      lit: !!((this.meeting && this.meeting.status === 'live') ||
        (this.planSeated && this.planSeated.size)),
      note: (room && room.notice && room.notice.text) || '',
      pourT: this.pourT || 0,
      plantWater: this.plantWater || new Map(),
      prn: { clock: this.clock || 0, job: prn.job, un: prn.un },
      en: this.energy.snapshot(),
      tro: achTrophyCount(),
      ia: this.interact.live,
    };
  }

  // replayRoomSnap：房间级视态的 wire 快照（0.5s 栅格上的量化版——
  // 瞬时态各有节拍，量化让变化过滤不为连续衰减多落帧；缺省面不上账）。
  replayRoomSnap() {
    const clock = this.clock || 0;
    const live = this.liveViewState();
    const r = { lit: live.lit };
    if (live.note) r.note = live.note.split('\n')[0].trim().slice(0, 48);
    if (live.pourT > 0) r.pour = Math.round(live.pourT * 20) / 20;
    const pw = {};
    for (const [i, at] of live.plantWater) {
      const remain = 8 - (clock - at);
      if (remain > 0) pw[i] = Math.round(remain * 2) / 2;
    }
    if (Object.keys(pw).length) r.pw = pw;
    if (live.prn.job || live.prn.un.length) {
      r.prn = {
        job: live.prn.job
          ? { ph: live.prn.job.ph, k: live.prn.job.k, t: Math.round(live.prn.job.t * 4) / 4 }
          : null,
        un: live.prn.un
          .map(([k, remain]) => [k, Math.round(remain * 2) / 2])
          .filter(([, remain]) => remain > 0),
      };
    }
    if (live.en.bin > 0 || live.en.snacks === 0 || live.en.lt.length) {
      r.en = {
        bin: live.en.bin,
        snacks: live.en.snacks,
        lt: live.en.lt.map((l) => [
          Math.round(l.x * 2) / 2, Math.round(l.y * 2) / 2,
          Math.round(l.age / 4) * 4, l.foodKey || '',
        ]),
      };
    }
    if (live.tro > 0) r.tro = live.tro;
    if (live.ia.size) {
      r.ia = [...live.ia].map(([k, a]) =>
        [k, Math.round(a.t * 4) / 4, a.x, a.y, a.i]);
    }
    return r;
  }

  // draw paints the office. target 参数化腿（r_32）：缺省画到本画布
  // （letterbox＋观察相机＋dpr）；给 {ctx, W, H, scale, hud:'export'} 时
  // 画到导出离屏画布——全景（相机归一）、无回放灰层与 ⟲ 角标、烧导出
  // HUD（右下日期时间码＋左上 hudLabel 工作室名）。
  draw(target) {
    const ctx = target ? target.ctx : this.ctx, g = this.art.geom;
    if (!g || !this.art.ready()) return;
    // wipe the whole pane, then draw the fixed scene through one
    // letterbox transform — scene coords in, device px out; smoothing
    // stays off so the pixel art upscales crisp
    const dpr = target ? 1 : (window.devicePixelRatio || 1);
    const camS = target ? target.scale : this.camScale();
    const camX = target ? 0 : this.camOffX();
    const camY = target ? 0 : this.camOffY();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, target ? target.W : this.canvas.width, target ? target.H : this.canvas.height);
    ctx.setTransform(dpr * camS, 0, 0, dpr * camS, dpr * camX, dpr * camY);
    ctx.imageSmoothingEnabled = false; // nearest-neighbor（定稿 §一：放大必须锐利）

    ctx.drawImage(this.art.back, 0, 0);

    // r_32.2 视态源：实时＝板上活状态；回放＝帧还原的鸭子型。draw 链
    // 的每一处状态读（大屏/白板/倒水/浇花/打印机/能量/荣誉墙/交互反
    // 馈/节拍钟）都走 VS——同一画笔，像素一致由构造保证。
    const VS = this.replayT >= 0
      ? shadowRoomAt(this.replayBufActive(), this.replayT)
      : this.liveViewState();
    const phase = Math.floor(VS.clock) % 2;

    // 会议进行中或提案评审中：会议室大屏点亮（呼吸光晕 + 一页开场
    // 幻灯片）。画在演员之前——近排坐者的头自然压住屏幕下缘，像真坐
    // 在幕布前
    if (g.meet && VS.lit) {
      paintScreenLit(ctx, g.meet.screen, phase);
    }

    // 打印机的活态（r_03）：亮灯/出纸/无人认领的纸——画在演员之前，
    // 顶段托盘上的纸被路过的身体自然遮挡，与家具同一深度语言
    this.printer.paintState(ctx, VS.prn);

    // actors and away-facing chairs in ONE painter's pass: a chair must
    // cover its own sitter yet yield to anyone walking south of it — a
    // depth no static layer pair can express (a fore-layer chair used
    // to paint over EVERY body near it — the 穿模), so chair.png blits
    // per face==1 seat (pods' near row, meeting south row), sorted with
    // the bodies by feet y; the sprite's base sits 8 native px below
    // the seat spot, its sort key
    // r_23 回放态：影子演员替代实时列表（paint 全链照常——只换源，
    // 实时 actors 一直在 step 只是没被画）。影子是普通对象——名牌/
    // 气泡/绘制的字段面与 Actor 同构（drawPlate 兼容鸭子型）。
    const liveList = [...this.actors.values()].sort((a, b2) => a.y - b2.y);
    const list = this.replayT >= 0
      ? shadowAt(this.replayBufActive(), this.replayT).sort((a, b2) => a.y - b2.y)
      : liveList;
    // 灯面（r_32.1）：回放态随影子走历史——谁当时坐工位干活谁的灯亮；
    // 实时 brightDesks 读「现在」的座次，盖在历史画面上是穿越。
    const bright = this.replayT >= 0
      ? shadowBright(list, g.desks.length)
      : this.brightDesks();
    // 回放态 hover 圈不亮（命中面是实时列表，圈会亮在历史画面之外的人上）
    const hoverA = this.replayT < 0 && this.hoverOn ? this.actorAt(this.hoverX, this.hoverY) : null;
    const cs = g.chairSpr;
    const items = [];
    if (cs) {
      g.desks.forEach((d, i) => {
        if (d.face !== 1) return;
        items.push({ y: d.spot[1] + cs.dy + cs.h, desk: i });
      });
      for (const s of g.meet ? g.meet.seats : []) {
        if (s.face !== 1) continue;
        items.push({ y: s.spot[1] + cs.dy + cs.h, meet: s });
      }
    }
    for (const a of list) items.push({ y: a.y, a });
    items.sort((p, q) => p.y - q.y);
    for (const it of items) {
      if (it.a) {
        const a = it.a;
        // r_23 补钉（r_32 扩面）：影子是普通对象（九字段，无 atlas/
        // frameIndex）——图先借实时同名的分层签名 atlas（同人同图），
        // 借不到（历史在册、现已离场）再凭帧里的外观键 c/hair 直取
        // /art/avatar（分享页没有实时名册，全靠这条腿）。帧取 r_32 的
        // 走路摆帧（相邻帧位移推断 walking，静站定格）。
        const img = a.atlas || this.actors.get(a.name)?.atlas ||
          (a.c ? this.art.atlas(a.c, a.hair || '01', '') : null);
        if (!img) continue;
        const fi = a.frameIndex
          ? a.frameIndex()
          : shadowFrame(a.dir, a.walking, this.replaySec());
        if (img.complete && img.naturalWidth) {
          const fw = img.naturalWidth / AVATAR_FRAMES;
          const fh = img.naturalHeight;
          ctx.globalAlpha = a.spawnT > 0 ? 1 - a.spawnT / 0.35 : 1;
          const hop = speakHop(a.speakT || 0); // 影子带 speakT（r_32.1：说话弹跳随泡起源重放），缺省 0 防 NaN
          // t_122：动作覆盖层的整体抬升（A3 jump/A7 stretch）——实时
          // 问仪式队列，回放问影子的 po 键（r_32.2：历史动作随帧走）
          const lift = this.replayT < 0 ? this.poser.liftOf(a.name) : poseLift(a.pose);
          ctx.drawImage(img, fi * fw, 0, fw, fh,
            Math.round(a.x - fw / 2), Math.round(a.y - fh - hop - lift), fw, fh);
          ctx.globalAlpha = 1;
          this.printer.paintHeldSheet(ctx, a); // 手持打印材料（r_03）
          this.paintFoodHand(ctx, a); // t_136：手持食品真形（kind=food）
          if (a.npcCleaner) this.cleaner.paintBroom(ctx, a); // t_139 补钉：阿姨的扫把不离手
          // t_122：姿势覆盖层（sprite 之上、前景层之下——桌面正确遮
          // 挡）。r_32.2：回放影子自带 po 键＋已进行秒——历史动作与实
          // 时同一条像素路径（paint 的 entry 参数）。
          this.poser.paint(ctx, a, {
            avatarH: fh, clock: VS.clock,
            shirt: memberColor(a.name), hair: '#3b2f2f',
            food: a.holding === 'food' ? '#e8b84c' : undefined,
          }, this.replayT >= 0 && a.pose ? { key: a.pose, t: a.poseT } : undefined);
        }
        continue;
      }
      // a chair: blit, then the typing shimmer rolls across its mesh
      // back (working desks only — the same glint the fore layer used
      // to wear, now riding the sorted sprite)
      const d = it.desk !== undefined ? g.desks[it.desk] : null;
      const spot = d ? d.spot : it.meet.spot;
      ctx.drawImage(this.art.chair, spot[0] + cs.dx, spot[1] + cs.dy, cs.w, cs.h);
      if (d && bright[it.desk] && d.chair[2]) {
        const dx = glintDx(it.desk, phase);
        const [cx, cy, cw, ch] = d.chair;
        const gw = Math.max(8, Math.round(cw * 0.4));
        const gx = cx + Math.floor((dx * (cw - gw)) / 3);
        ctx.fillStyle = CLR.glintBase;
        ctx.fillRect(gx, cy, gw, ch);
        ctx.fillStyle = CLR.glintHi;
        ctx.fillRect(gx, cy, gw, Math.max(2, Math.round(ch / 2)));
      }
    }

    ctx.drawImage(this.art.fore, 0, 0);

    // working lamps: lit shades with breathing halos, on the same 2s
    // beat the screens used to keep — painted over the fore layer,
    // where the unlit brass shades now live (both rows)
    g.desks.forEach((d, i) => {
      if (bright[i]) paintLampLit(ctx, d.lamp, phase);
    });

    // t_105：饮水机倒水动画——出水口一段蓝水流＋接水杯（1.2s）。
    // r_32.2：视态走 VS（回放的剩余量随帧起点前进）。
    if (VS.pourT > 0 && g.interact) {
      const [dx, dy, dw, dh] = g.interact.dispenser;
      const t = 1.2 - VS.pourT; // 0→1.2
      // 水流：中段持续，头尾各 0.15s 渐现渐隐
      const flowA = Math.min(1, t / 0.15, (VS.pourT) / 0.15);
      ctx.globalAlpha = flowA;
      ctx.fillStyle = '#8fd0e8';
      ctx.fillRect(dx + dw / 2 - 1, dy + dh * 0.42, 2, dh * 0.28);
      ctx.fillStyle = '#b4e2f2';
      ctx.fillRect(dx + dw / 2 - 1, dy + dh * 0.42, 1, dh * 0.28);
      // 接水杯：水流下方的一只小杯，水位随时间涨
      const cupY = dy + dh * 0.70;
      ctx.fillStyle = '#f0f3f6';
      ctx.fillRect(dx + dw / 2 - 3, cupY, 6, 5);
      ctx.fillStyle = '#8fd0e8';
      ctx.fillRect(dx + dw / 2 - 2, cupY + 4 - Math.min(3, t * 3), 4, Math.min(3, t * 3));
      ctx.globalAlpha = 1;
    }
    // t_105：绿植浇水——浇过的盆土湿一拍（8s 内土色加深），水珠三滴。
    // r_32.2：视态走 VS（回放的 plantWater 是帧还原的临时 Map——born
    // 反推成「年龄＝剩余」，删除无副作用；实时 VS.plantWater 就是板上
    // 的活 Map，行为照旧）。
    if (g.interact) {
      (g.interact.plants || []).forEach(([px, py, pw, ph], i) => {
        const at = VS.plantWater.get(i);
        if (!at) return;
        const age = VS.clock - at;
        if (age > 8) { VS.plantWater.delete(i); return; }
        // 湿土：盆面一条深土色
        ctx.fillStyle = 'rgba(61,99,57,.55)';
        ctx.fillRect(px + 2, py + ph * 0.62, pw - 4, 2);
        // 头 0.8s：三滴水珠从叶上落下
        if (age < 0.8) {
          ctx.fillStyle = '#8fd0e8';
          for (let d = 0; d < 3; d++) {
            const dy2 = age * 22 + d * 5;
            if (dy2 < ph * 0.55) ctx.fillRect(px + pw / 2 - 4 + d * 4, py + ph * 0.25 + dy2, 1, 2);
          }
        }
      });
    }

    // the whiteboard's hover ring (I1's shared cue)
    if (this.hoverOn && this.onWhiteboard(this.hoverX, this.hoverY)) {
      const [bx, by, bw, bh] = g.whiteboard;
      ctx.strokeStyle = CLR.hoverRing;
      ctx.lineWidth = 2;
      ctx.strokeRect(bx - 1, by - 1, bw + 2, bh + 2);
    }

    // t_105：白板上的字——本房公告的首行（点击白板可写，notice 面
    // 全员同步）。粉笔风格：白板面上居中一行蓝字（太长截断）。
    // r_32.2：视态走 VS（回放的白板字随帧——分享页也有历史公告）。
    if (g.interact) {
      const note = VS.note;
      if (note) {
        const line = note.split('\n')[0].trim().slice(0, 24);
        if (line) {
          const [bx, by, bw, bh] = g.interact.board;
          ctx.font = '7px system-ui, "PingFang SC", sans-serif';
          ctx.textAlign = 'center';
          ctx.textBaseline = 'middle';
          ctx.fillStyle = '#3370ff';
          ctx.fillText(line, bx + bw / 2, by + bh / 2, bw - 8);
          ctx.textAlign = 'left';
        }
      }
    }

    // 会议室大屏的悬停圈（点开看评审/占用态，与白板同一手势语）
    if (this.hoverOn && this.onScreen(this.hoverX, this.hoverY) && g.meet) {
      const [sx, sy, sw, sh] = g.meet.screen;
      ctx.strokeStyle = CLR.hoverRing;
      ctx.lineWidth = 2;
      ctx.strokeRect(sx - 2, sy - 2, sw + 4, sh + 4);
    }

    // 墙上挂钟的悬停圈（点开表盘面板，与白板/大屏同一手势语）
    if (this.hoverOn && this.onClock(this.hoverX, this.hoverY)) {
      const [cx, cy, cw, ch] = g.clock;
      ctx.strokeStyle = CLR.hoverRing;
      ctx.lineWidth = 2;
      ctx.strokeRect(cx - 2, cy - 2, cw + 4, ch + 4);
    }

    // hover ring + nameplates over the fore layer: the front row sits
    // behind the near desks, so their plates must not drown in the
    // bench art
    if (hoverA) {
      ctx.fillStyle = CLR.hoverRing;
      ctx.fillRect(hoverA.x - 7, hoverA.y - 1, 14, 2);
      ctx.fillRect(hoverA.x - 4, hoverA.y, 8, 1);
    }
    for (const a of list) drawPlate(ctx, a, { hovered: a === hoverA, viewW: this.w, avatarH: g.avatarH });
    // t_104：干活中的任务气泡（🔨 t_NN）／刚 done 的绿 ✓（2s 淡出）
    for (const a of list) drawTaskPlate(ctx, a, { viewW: this.w, avatarH: g.avatarH });
    for (const a of list) this.printer.paintSheetPop(ctx, a, g.avatarH); // 材料送达小泡（r_03）
    this.energy.paintState(ctx, VS.en); // t_137/t_138：桶渐满＋落地垃圾＋售罄纸条（r_32.2 视态）
    // t_121/t_123：表情泡与交互反馈。r_32.2 全量视态：回放不再豁免——
    // 表情泡读影子的 em 键（记录列，idle/react/任务事件全收），交互反
    // 馈读帧还原的活动表（静音——历史不播音）。
    if (this.replayT < 0) {
      this.emoter.paint(ctx, list); // t_121：表情泡（E1–E12）
      this.interact.paint(ctx); // t_123：交互反馈动画（I1–I12）
    } else {
      this.emoter.paintShadow(ctx, list, VS.clock);
      this.interact.paint(ctx, VS.ia, true);
    }
    this.paintShowcase(ctx, VS.tro); // t_188：货架最近奖杯（r_32.2 计数随视态走）

    // r_23/r_32.1：泡的渲染源同样换影子——历史泡从快照文本现测成泡记
    // 录，分层（汇报蓝/提及金/镜像羊皮纸）与坐席锚定（窄两行、锚桌沿）
    // 都按帧里的 bt/dk 复原；ttl 直读影子算好的剩余生命——弹入、停留、
    // 淡出与实时同一节拍（不再定格中段）。实时泡回放态不上画面。
    drawBubbles(ctx, this.replayT >= 0
      ? layoutBubbles(list.map((a) => {
          if (!a.bubble) return a;
          const b = makeBubble(ctx, {
            text: a.bubble, from: a.name,
            report: a.bt === 1, mention: a.bt === 2, mirror: a.bt === 3,
            seated: a.working && a.deskIdx >= 0,
          }, this.w);
          b.ttl = a.bubbleTtl;
          return { ...a, bubble: b };
        }), g, this.w)
      : layoutBubbles(this.actors.values(), g, this.w),
      { on: this.hoverOn, x: this.hoverX, y: this.hoverY });

    // r_23 回放态皮肤（§二）：灰层盖画布（演员之上），角标胶囊随画。
    // r_32 导出档不叠（视频皮肤走自己的 HUD——灰层与 ⟲ 角标是「正在
    // 回看」的界面语，不是分享产物的语言）。
    if (this.replayT >= 0 && !(target && target.hud === 'export')) {
      // 灰层：盖画布不盖 UI 控件（控件在 DOM 层——canvas 内只盖演员）
      ctx.fillStyle = 'rgba(100,100,110,0.28)';
      ctx.fillRect(0, 0, this.w, this.h);
      // 角标胶囊：「⟲ 回放 HH:MM:SS」（day 档带日期——墙钟即帧原生 t）
      // r_29 认领微优化：时间串秒级缓存（toLocaleTimeString 是 draw 链
      // 少数非位块操作，秒不变复用上帧串）
      const rpSec = Math.floor(this.replaySec());
      if (this._rpLabelSec !== rpSec || !this._rpLabel) {
        const ts = new Date(this.replayWallT());
        this._rpLabel = this.replaySrc === 'day'
          ? t('⟲ 回放 {date} {time}', {
              date: ts.toLocaleDateString(localeTag()),
              time: ts.toLocaleTimeString(localeTag(), { hour12: false }),
            })
          : t('⟲ 回放 {time}', { time: ts.toLocaleTimeString(localeTag(), { hour12: false }) });
        this._rpLabelSec = rpSec;
      }
      const label = this._rpLabel;
      ctx.font = '12px system-ui, "PingFang SC", sans-serif';
      const tw = ctx.measureText(label).width;
      const bx = 10, by = 10, bw = tw + 20, bh = 22;
      ctx.fillStyle = '#fffdf5';
      ctx.strokeStyle = '#282828';
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.roundRect ? ctx.roundRect(bx, by, bw, bh, 11) : ctx.rect(bx, by, bw, bh);
      ctx.fill(); ctx.stroke();
      ctx.fillStyle = '#282828';
      ctx.textBaseline = 'middle';
      ctx.fillText(label, bx + 10, by + bh / 2 + 0.5);
      ctx.textBaseline = 'alphabetic';
    }

    // r_32 导出 HUD：分享产物的皮肤——右下日期时间码（墙钟真源，跟着
    // 时间轴走）、左上 hudLabel 工作室名。画在变换后的场景坐标系里
    //（768×520 逻辑面，随 scale 放大保持像素风）。
    if (target && target.hud === 'export') {
      const ts = new Date(this.replayWallT());
      const line = `${ts.toLocaleDateString(localeTag())} ${ts.toLocaleTimeString(localeTag(), { hour12: false })}`;
      ctx.font = 'bold 12px system-ui, "PingFang SC", sans-serif';
      const chip = (x, y, w, text, align) => {
        ctx.fillStyle = 'rgba(24,24,28,.72)';
        ctx.beginPath();
        if (ctx.roundRect) ctx.roundRect(x, y, w, 24, 6); else ctx.rect(x, y, w, 24);
        ctx.fill();
        ctx.fillStyle = '#fffdf5';
        ctx.textBaseline = 'middle';
        ctx.textAlign = align;
        ctx.fillText(text, align === 'right' ? x + w - 10 : x + 10, y + 12.5);
        ctx.textAlign = 'left';
        ctx.textBaseline = 'alphabetic';
      };
      chip(this.w - ctx.measureText(line).width - 30, this.h - 34,
        ctx.measureText(line).width + 20, line, 'right');
      if (target.hudLabel) {
        chip(10, 10, ctx.measureText(target.hudLabel).width + 20, target.hudLabel, 'left');
      }
    }

    // 房间暂停纱幕：轻纱压暗＋一枚居中徽标——世界定格在原地，回来一
    // 眼就知道为什么人人不动（回放灰层之下、场景之上）。只画活画布：
    // 导出/离屏腿（target）不带纱，产物里冻结的画面自己说话。
    if (!target && this.roomPaused()) {
      const label = t('⏸ 已暂停');
      ctx.font = 'bold 16px system-ui, "PingFang SC", sans-serif';
      const tw = ctx.measureText(label).width;
      ctx.fillStyle = 'rgba(16,16,20,.30)';
      ctx.fillRect(0, 0, this.w, this.h);
      ctx.fillStyle = 'rgba(24,24,28,.78)';
      ctx.beginPath();
      if (ctx.roundRect) ctx.roundRect((this.w - tw - 28) / 2, this.h / 2 - 16, tw + 28, 32, 16);
      else ctx.rect((this.w - tw - 28) / 2, this.h / 2 - 16, tw + 28, 32);
      ctx.fill();
      ctx.fillStyle = '#fffdf5';
      ctx.textBaseline = 'middle';
      ctx.fillText(label, (this.w - tw) / 2, this.h / 2 + 0.5);
      ctx.textBaseline = 'alphabetic';
    }
  }

  /** 房间暂停态（服务端真源经房册）：update 每拍直读——pause 帧一到，
   * 下一拍世界即定格；恢复帧一到，下一拍照旧走。 */
  roomPaused() {
    const room = this.book && this.book.rooms ? this.book.rooms.get(this.roomKey) : null;
    return !!(room && room.paused);
  }
}

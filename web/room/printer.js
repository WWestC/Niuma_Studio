// printer.js — the 大型打印机's print ritual (r_03, t_116): the
// three-phase animation state machine + the handheld sheet + the
// unattended branch, all pure presentation over the wire frames the
// business side already emits. The machine itself lives in the art
// (drawBigPrinter) and geom.json's printer block (rect/spot/tray/led/
// panel); this module drives WHO walks over, WHEN the LED lights, HOW
// the sheet slides out, and WHO carries it home. 打印永远是仪式不是
// 闸门——no business flow ever waits on any of this.
//
// errand 根治：仪式自己拥有取材人的走位下发——入机即派（dispatch，
// 经 errand.js 的 sendOnErrand/leaveDesk 同一通道），占用声明挂
// a.fetching（errand.js 一口径），外部占用（派活/叫会/别的外勤）半路
// 接管就放人走无人认领；走位带死线（FETCH_WAIT），到不了也自动出纸
//——不再等一个从没人派过来的人。取材后送回工位，落座即 pop。

import { DIR } from './actor.js';
import { rand } from './util.js';
import { sfx } from './sfxport.js';
import { claimed, dispatchable, sendOnErrand } from './errand.js';

// 标准版三阶段节拍（design/r03-printer §三）。r_32.2 导出：回放从帧
// 里的相位时刻沿表前进（快照只存采样瞬间的相位＋已进行秒）。
const PHASE = {
  LIGHT: 0.5,   // 亮灯＋机身高光脉冲
  PAPER: 1.5,   // 出纸：4 段步进，每段 ≈0.375s
  TAKE: 1.0,    // 取材：伸手拿纸＋转身
};
export const PRINTER_PHASE = PHASE;
const SEGMENTS = 4;
// 自动出纸分支：无人认领时纸留出纸槽的时长（S3.2 之后）
const UNATTENDED_HOLD = 8;
// 取材人走位死线（秒，自入机起）：WALK 26px/s、全屋对角 ~15s——
// 死线内没走到就当无人认领出纸，取材人原地散场（被堵/目标不可达）
const FETCH_WAIT = 14;
// 无工位可回的取材人：拿纸后最多随身这么久必 pop（idle 落座是主路径，
// 这是兜底——纸不能跟人一辈子）
const CARRY_GRACE = 8;
// 手持件尺寸（legacy 3×4 → native 6×8 的视觉）
export const SHEET_KIND = {
  TASK: 'task',     // 任务单：品牌蓝标题条
  ONBOARD: 'onboard', // 入职材料：头像框
  NOTICE: 'notice', // 公告纸：灰字条
  DONE: 'done',     // 验收单：绿✓
  COMMIT: 'commit', // 提交纸（v2.8 gitflow）：分支节点＋短横——谁刚落了一笔代码
};

// one print job through the machine
class Job {
  constructor(kind, taker, label) {
    this.kind = kind;        // SHEET_KIND — the sheet's summary swatch
    this.taker = taker;      // the member who walks over (null = unattended)
    this.label = label;      // short text for debugging/logs
    this.phase = 'light';    // light → paper → take → carried
    this.t = 0;              // seconds into the current phase
    this.age = 0;            // seconds since admission — the fetch deadline clock
    this.segments = 0;       // how many paper segments have slid out
    this.sheet = null;       // the loose sheet's rect once fully out
    this.taken = false;      // the taker grabbed it
  }
}

export class PrinterRitual {
  /**
   * @param {import('./roomview.js').RoomView} board the office board —
   *        it lends geom (printer block), the actor map, the sim clock
   *        and the walk-to machinery (the same one the pantry uses).
   */
  constructor(board) {
    this.board = board;
    this.job = null;        // the live job at the machine (one at a time)
    this.queue = [];        // pending jobs (serial per the design)
    this.unattended = [];   // sheets left in the tray, fading
  }

  get geom() { return this.board.art.geom && this.board.art.geom.printer; }

  /** 房切换（setRoom）：演员全换，队列/在机任务/滞留纸一并清场。 */
  reset() {
    this.queue.length = 0;
    this.job = null;
    this.unattended.length = 0;
  }

  // ── the public trigger (t_117/t_118 wire business events here) ──
  /**
   * Schedule one print. kind picks the sheet's summary swatch; taker
   * is the member name who should walk over (null/undefined/unknown →
   * the unattended branch: the machine prints alone, the sheet fades).
   * 入机那一刻（step 认领任务时）即派走位——纸与人同一钟出发。
   * Never throws, never blocks — a ritual, not a gate.
   */
  print(kind, taker, label) {
    const actor = taker && this.board.actors.get(taker);
    this.queue.push(new Job(kind, actor || null, label || ''));
  }

  // ── the tick (the board calls this from update) ─────────────────
  step(dt) {
    const g = this.geom;
    if (!g) return;
    const clock = this.board.clock;
    // admit the next job when the machine is idle
    if (!this.job && this.queue.length) {
      this.job = this.queue.shift();
      const j = this.job;
      j.t = 0; j.age = 0;
      if (j.taker && !dispatchable(j.taker, this.board.busy)) j.taker = null;
      if (j.taker) this.dispatch(j.taker); // ← 根治点：走位在此下发
    }
    const j = this.job;
    if (j) {
      j.t += dt; j.age += dt;
      // the taker's availability can change mid-flight (assigned work,
      // called into a meeting, another errand) — release our claim, let
      // the claimer own the legs, sheet drops to unattended
      if (j.taker && claimed(j.taker, this.board.busy, 'fetching')) {
        this.dropClaim(j.taker);
        j.taker = null;
      }
      switch (j.phase) {
        case 'light':
          if (j.t >= PHASE.LIGHT) { j.phase = 'paper'; j.t = 0; j.segments = 0; }
          break;
        case 'paper': {
          const seg = Math.min(SEGMENTS, Math.floor(j.t / (PHASE.PAPER / SEGMENTS)));
          if (seg > j.segments) {
            j.segments = seg; // each step = 1 tray bump frame
            // r_09：出纸「吱」——第 1/3 段各一声（吱两声对应出纸节奏，
            // 非四连响；冷却窗兜底）
            if (seg === 1 || seg === 3) sfx('scene-print');
          }
          if (j.t >= PHASE.PAPER) { j.phase = 'take'; j.t = 0; }
          break;
        }
        case 'take':
          // the taker must be at the anchor to grab; unattended sheets
          // just wait out the hold then fade
          if (j.taker && this.atAnchor(j.taker)) {
            this.grab(j.taker, j.kind);
            j.taken = true;
            j.phase = 'carried';
            this.job = null; // machine free for the next
          } else if (!j.taker) {
            // unattended (never claimed / claimed away): leave the sheet
            this.unattended.push({ kind: j.kind, until: clock + UNATTENDED_HOLD });
            this.job = null;
          } else if (j.age >= FETCH_WAIT) {
            // taker never arrived (stuck/pathing far) — give up the walk,
            // leave the sheet like unattended
            this.abandon(j.taker);
            j.taker = null;
            this.unattended.push({ kind: j.kind, until: clock + UNATTENDED_HOLD });
            this.job = null;
          }
          break;
      }
    }
    // fading unattended sheets
    for (let i = this.unattended.length - 1; i >= 0; i--) {
      if (clock >= this.unattended[i].until) this.unattended.splice(i, 1);
    }
  }

  // ── 取材外勤三段：派出／放人／弃置（占用纪律见 errand.js）──────

  // dispatch：入机即派——走位到取件锚点，面北朝机器。取材是快外勤，
  // 工位不退（deskIdx 保留，applyBusy 的外勤豁免看守它）——抓完纸
  // 直接回原座，S4 送达戏完整。a.fetching 是占用声明，所有调度器经
  // errand.js 豁免。
  dispatch(a) {
    sendOnErrand(a, this.geom.spot[0], this.geom.spot[1], DIR.UP);
    a.fetching = true;
  }

  // grab：到锚点拿纸——占用态换成手持件，随即送回工位（落座 pop 是
  // 送达语义，S4）。无工位可回者由 CARRY_GRACE 兜底 pop。
  grab(a, kind) {
    a.fetching = false;
    a.working = false;
    a.workFace = DIR.UP; // 转身面北拿纸的一拍
    a.holding = kind;
    a.sheetGrabAt = this.board.clock || 0;
    const d = this.board.desks;
    const s = a.deskIdx >= 0 && d && d.spots && d.spots[a.deskIdx];
    if (s) {
      // 回原座：落座朝向沿用工位面（0 面向镜头，其余背对）
      sendOnErrand(a, s.x, s.y, s.face === 0 ? DIR.DOWN : DIR.UP);
    } else if (d && d.claimIdle && d.spots && d.spots.length) {
      const got = d.claimIdle(a.name, this.board.clock || 0); // 顺手认个空位（旧座 30s 缓冲内的不让坐——走到哪个算哪个）
      if (got) { a.deskIdx = got.idx; sendOnErrand(a, got.spot.x, got.spot.y, got.spot.face === 0 ? DIR.DOWN : DIR.UP); }
    }
  }

  // dropClaim：外部占有者（派活/叫会）接管路权——只收回我们自己的
  // 占用声明，目标留给接管者改写（越权清别人的路是旧疤的镜像）。
  dropClaim(a) {
    if (!a) return;
    a.fetching = false;
    a.working = false;
  }

  // abandon：死线到没人来——占用与目标一并散（无人接管的场景）。
  abandon(a) {
    if (!a) return;
    this.dropClaim(a);
    a.hasTarget = false;
    a.idleT = rand(0.5, 2);
  }

  // takerFree: the admission census — errand.js 一口径（外部占用全票
  // 否决；fetching 在途者也不再领新纸）。kept for the debug hook.
  takerFree(a) { return dispatchable(a, this.board.busy); }

  atAnchor(a) {
    const [sx, sy] = this.geom.spot;
    return Math.abs(a.x - sx) < 8 && Math.abs(a.y - sy) < 8;
  }

  // ── painting (the board calls this inside its draw pass) ─────────
  /**
   * Paint the machine's live state OVER the back layer, before the
   * actors: the lit LED + panel pulse, the sliding sheet (4 segments)
   * and the unattended fading sheets. The handheld sheet is painted by
   * the board's actor pass (it rides the carrier's hip).
   */
  paint(ctx) {
    // r_32.2 视态化：像素全在 paintState，paint 是实时适配腿（快照即
    // 视态——同一条像素路径，回放喂帧还原的同形状态）。
    this.paintState(ctx, this.snapshot());
  }

  // snapshot：机器当前的归一化视态——录制入帧／回放还原／实时绘制三
  // 方共用的单一真源形状：{job: {ph, k, t} | null, un: [[kind, remain]]}。
  snapshot() {
    return {
      job: this.job ? { ph: this.job.phase, k: this.job.kind, t: this.job.t } : null,
      un: this.unattended.map((u) => [u.kind, u.until - (this.board.clock || 0)]),
    };
  }

  /** paintState：按归一化视态画（亮灯呼吸／出纸滑段／托盘滞留淡出）。
   * st.clock 是节拍钟（相位的快闪用），st.job.t 是相位内已进行秒。 */
  paintState(ctx, st) {
    const g = this.geom;
    if (!g || !st) return;
    const j = st.job;
    const clock = st.clock ?? this.board.clock ?? 0;
    const phase1s = Math.floor(clock * 4) % 2; // fast blink, 0.25s
    if (j && (j.ph === 'light' || j.ph === 'paper' || j.ph === 'take')) {
      // 亮灯：brandB LED（呼吸）＋面板高光脉冲
      const [lx, ly, lw, lh] = g.led;
      ctx.fillStyle = phase1s ? '#4c7bf0' : '#7da2f7';
      ctx.fillRect(lx, ly, lw, lh);
      const [px, py, pw, ph] = g.panel;
      ctx.fillStyle = `rgba(76,123,240,${phase1s ? 0.18 : 0.10})`;
      ctx.fillRect(px, py, pw, ph);
    }
    if (j && (j.ph === 'paper' || j.ph === 'take')) {
      // 出纸：白纸从托盘逐段滑出（每段带内容色块摘要）
      const [tx, ty, tw, th] = g.tray;
      const frac = j.ph === 'paper'
        ? Math.min(1, j.t / PHASE.PAPER)
        : 1;
      const w = Math.round(tw * frac);
      if (w > 0) {
        ctx.fillStyle = '#fbfbf8';
        ctx.fillRect(tx, ty - 2, w, 6);
        ctx.fillStyle = '#e7ebef';
        ctx.fillRect(tx, ty + 3, w, 1);
        // 内容摘要色块：任务单=蓝标题条 / 入职=头像框 / 公告=灰字条 / 验收=绿✓
        this.paintSummary(ctx, j.k, tx, ty - 2, w, 6);
      }
    }
    // unattended sheets lying in the tray, fading
    for (const u of st.un || []) {
      const [kind, remain] = u;
      if (!(remain > 0)) continue;
      const [tx, ty, tw] = g.tray;
      const alpha = Math.min(1, remain / 2); // last 2s fade
      ctx.globalAlpha = alpha;
      ctx.fillStyle = '#fbfbf8';
      ctx.fillRect(tx + 2, ty - 2, tw - 4, 6);
      this.paintSummary(ctx, kind, tx + 2, ty - 2, tw - 4, 6);
      ctx.globalAlpha = 1;
    }
  }

  // the sheet's summary swatch — one 2px accent band per kind
  paintSummary(ctx, kind, x, y, w, h) {
    const b = Math.max(2, Math.min(6, Math.round(w * 0.3)));
    switch (kind) {
      case SHEET_KIND.TASK: // 品牌蓝标题条
        ctx.fillStyle = '#3370ff';
        ctx.fillRect(x + 2, y + 1, Math.min(b, w - 4), 1);
        break;
      case SHEET_KIND.ONBOARD: { // 头像框
        ctx.strokeStyle = '#3370ff';
        ctx.lineWidth = 1;
        ctx.strokeRect(x + 2, y + 1, 3, 3);
        break;
      }
      case SHEET_KIND.NOTICE: // 灰字条
        ctx.fillStyle = '#a9b2bb';
        ctx.fillRect(x + 2, y + 2, Math.min(b + 2, w - 4), 1);
        break;
      case SHEET_KIND.DONE: // 绿✓
        ctx.fillStyle = '#34c724';
        ctx.fillRect(x + 2, y + 2, 3, 1);
        ctx.fillRect(x + 4, y + 1, 1, 2);
        break;
      case SHEET_KIND.COMMIT: { // 提交纸：分支节点＋连线（与版本管理图标同语）
        ctx.strokeStyle = '#8f7df8';
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.arc(x + 3, y + 1, 1, 0, Math.PI * 2);
        ctx.moveTo(x + 3, y + 2);
        ctx.lineTo(x + 3, y + 4);
        ctx.stroke();
        break;
      }
    }
  }

  // paintHeldSheet: the handheld sheet riding a carrier — the board
  // calls it per actor right after the sprite (3×4 legacy white sheet
  // at the hip, a 1px sway with the walk beat). 'food' 是吃链的道具
  //（paintFoodHand 画真形）——两手持件不叠画。
  paintHeldSheet(ctx, a) {
    if (!a.holding || a.holding === 'food') return;
    const g = this.geom;
    const sway = a.moving ? (Math.floor(a.animT * 6) % 2 ? 1 : 0) : 0;
    const x = Math.round(a.x + 8), y = Math.round(a.y - 14 + sway);
    ctx.fillStyle = '#fbfbf8';
    ctx.fillRect(x, y, 6, 8);
    ctx.fillStyle = '#e7ebef';
    ctx.fillRect(x, y + 7, 6, 1);
    this.paintSummary(ctx, a.holding, x, y, 6, 8);
  }

  // releaseHeld: the carrier reached home (their desk / the owner) —
  // the sheet dissolves into a brief icon pop (S4). The board calls
  // this when the carrier next sits/idles at their desk. 'food' 不走
  // 送达 pop（那是吃链自己的道具，吃链自管）；无工位者由 CARRY_GRACE
  // 兜底——纸不能跟人一辈子。
  maybeRelease(a) {
    if (!a.holding || a.holding === 'food') return;
    if (a.working || (a.deskIdx >= 0 && !a.hasTarget) ||
        ((this.board.clock || 0) - (a.sheetGrabAt || 0) > CARRY_GRACE && !a.hasTarget)) {
      a.sheetPopT = 1.5; // the icon pop's life (seconds)
      a.holding = null;
    }
  }

  // paintSheetPop: the 1.5s completion pop above the carrier's head —
  // a tiny pixel sheet icon rising and fading (no emoji, per design).
  paintSheetPop(ctx, a, avatarH) {
    if (!a.sheetPopT || a.sheetPopT <= 0) return;
    const rise = (1.5 - a.sheetPopT) * 6; // rises 9px over its life
    const alpha = Math.min(1, a.sheetPopT / 0.6);
    ctx.globalAlpha = alpha;
    const x = Math.round(a.x - 3), y = Math.round(a.y - avatarH - 6 - rise);
    ctx.fillStyle = '#fbfbf8';
    ctx.fillRect(x, y, 6, 8);
    ctx.fillStyle = '#3370ff';
    ctx.fillRect(x + 1, y + 1, 4, 1);
    ctx.globalAlpha = 1;
  }

  tickSheetPop(dt) { /* the board subtracts via actors — kept for symmetry */ }
}

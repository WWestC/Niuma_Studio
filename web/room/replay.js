// replay.js — the 时间轴回放 engine (r_23, t_198) + the 一日回放 legs
// (r_32): the snapshot ring buffer (live scrub), the day buffer loader
// (wall-clock frames off the server), the shadow rebuild with walk
// inference, and the recorder's change filter. r_23 三总纲照抄：
//   ① 快照重建不做事件重放（14 处 Math.random 使重放不可行）；
//   ② 实时模拟永不暂停——回放只是 paint 换渲染源（影子演员列表），
//      「退出 1 帧回实时」架构白送；
//   ③ 影子演员：只读、不 step、不进避让、不挂 emoter/poser/interact
//      输入——天然隔离，实时事件不叠加。
//
// 采样纪律（§一架构裁定版）：每 0.5s 一帧全量快照，环形保 10 分钟
// （1200 帧/人 ≈234KB），挤出最旧（sfx 冷却环同机制）；回放取 T 前后
// 两帧线性插值位置，朝向/泡取前帧离散值。仪式动画首版降级「进行中…」
// 小签（§二）。
//
// r_32 增补：帧面从六字段扩到九（hold/c/hair——手持物与外观自足键，
// 离场成员与分享页没有实时 atlas 可借，凭 c/hair 直取 /art/avatar）；
// dayBufOf 把服务端当日 jsonl 行折进一个 t=墙钟 ms 的 ReplayBuf（实时
// 环与日缓冲同构，shadowAt 原样复用）；相邻帧位移推断 walking，走路
// 摆帧补回六字段时代回放不了的走位动画。
//
// r_32.1 回放一致性修订：泡标签的字段面是 bubbles.js makeBubble 的
// 「raw」（首版误读不存在的 .text，气泡从未入帧——回放里泡全没了）；
// 帧面再扩五列：泡分层 bt／工位 dk／任务牌 lab／落定 ✓ dn／食品键 fk，
// 影子重建按起源扫描恢复泡与 ✓ 的完整生命（弹入→寿命→淡出，稀疏日
// 缓冲里不再定格到天荒地老），说话弹跳与表情泡（键从泡文本确定性推
// 出）一并随影子走历史。
//
// r_32.2 全量视态（根治版）：回放必须与实时同一张脸——由构造保证，
// 不靠逐项复刻。两条铁律：
//   ① 录制腿把 draw 链读到的**每一个**状态源快照进帧：演员面再扩
//      mi/gr/rk/loc/npc/po/em/sp（会籍/宽限/职级/本人/阿姨/动作/表情/
//      纸弹），房间面新增 r 对象（大屏/白板/倒水/浇花/打印机/能量/
//      荣誉墙/交互反馈）；
//   ② 绘制链只认「视态」这一种输入：实时＝板上活状态的快照，回放＝
//      shadowRoomAt 从帧还原的同形鸭子型——同一条画笔，像素一致是
//      构造性质。瞬时态（倒水/出纸/垃圾龄）按帧起源＋时长表前进，
//      变化过滤按量级量化压噪声。

import { emoteFromText } from './emote.js';
import { POSE_DUR } from './poses.js';
import { INTERACT_T } from './interact.js';
import { PRINTER_PHASE } from './printer.js';
import { EPARAMS } from './energy.js';

// 生命节拍镜像（改源必须同步：bubbles.js BUBBLE_LIFE / actor.js
// SPEAK_BEAT / emote.js SHOW / roomview 的 doneT 谢幕初值 t_104）。
// 影子侧统一按秒消费；day 缓冲的 ms 帧面由 shadowAt 内部换算。
export const BUBBLE_LIFE = 6;   // bubbles.js BUBBLE_LIFE
export const SPEAK_BEAT = 0.5;  // actor.js SPEAK_BEAT（说话弹跳）
export const EMOTE_SHOW = 2;    // emote.js SHOW（表情泡停留）
export const DONE_SHOW = 2;     // roomview doneT 初值（✓ 谢幕 2s）
export const SHEET_POP_LIFE = 1.5; // printer.js 材料送达小弹的生命

// 泡标签的入帧帽（r_32.1 从 24 字放宽）：live 泡折行预览最多 4 行，
// 120 字覆盖四行正文；超出部分绘制侧 makeBubble 照常折行省略——
// 回放泡与实时泡同一张脸。
export const BUBBLE_CAP = 120;

// 起源扫描的步数帽：泡/✓ 寿命秒级，0.5s 栅格下同值游程 ≤12 帧，
// 64 是宽松上界（异常长游程不拖慢 draw 链）。
const RUN_CAP = 64;

// t_199 验收抽验锚：采样节拍与环形窗宽导出（破坏性抽验「改间隔必红」的断言面）
export const SAMPLE_EVERY = 0.5;   // s（§一定时帧）
export const RETAIN = 600;         // s（10 分钟环形窗）

// Frame is one full sample of every tracked actor.
// tiny on purpose: name/x/y/dir/bubble/working (+ r_32 hold/c/hair/bt/dk/
// lab/dn/fk，r_32.2 mi/gr/rk/loc/npc/po/em/sp) — the shadow rebuild reads
// exactly these. live（r_32.2）＝仪式队列的查询面：{poseOf(name) →
// {key,t}|null, emoteOf(name) → key|null}——动作/表情活在 ritual 的队
// 列里不在演员身上，采样时从这儿借。
export function frameOf(actors, live) {
  const list = [];
  for (const a of actors) {
    const bub = a.bubble;
    const f = {
      n: a.name, x: Math.round(a.x * 4) / 4, y: Math.round(a.y * 4) / 4,
      d: a.dir,
      // 泡文本读 makeBubble 的 raw（.text 从不存在——首版读它，气泡
      // 一帧都没录上；测试 mock 曾造 {text} 假形状掩盖了断线）。
      b: (bub && bub.raw) ? String(bub.raw).slice(0, BUBBLE_CAP) : '',
      w: a.working,
      hold: a.holding ? String(a.holding) : '',
      c: a.color || '',
      hair: a.hair || '',
    };
    // r_32.1 头顶面（缺省值不上账——变化过滤的噪声面越小越好）：
    // 泡分层（1 汇报/2 提及/3 镜像，0 聊天缺省）、工位号（0 是真工
    // 位，只在有落点时上账）、任务牌、落定 ✓、食品键。
    const bt = bub ? (bub.report ? 1 : bub.mention ? 2 : bub.mirror ? 3 : 0) : 0;
    if (bt) f.bt = bt;
    if (typeof a.deskIdx === 'number' && a.deskIdx >= 0) f.dk = a.deskIdx;
    if (a.label) f.lab = String(a.label).slice(0, 16);
    if (a.doneT > 0) f.dn = true;
    if (a.holding === 'food' && a.foodKey) f.fk = String(a.foodKey).slice(0, 24);
    // r_32.2 名牌/会籍/人物面：会议室站位（会标 📹）、宽限省略号、
    // 职级色、本人标记、阿姨扫把、动作键（po——起源扫描按 POSE_DUR
    // 钳生命）、表情泡键（em——显式记录，idle/react/任务事件等不经
    // 泡文本的来源也全收）、材料送达小弹（sp）。
    if (typeof a.meetIdx === 'number' && a.meetIdx >= 0) f.mi = a.meetIdx;
    if (a.grace) f.gr = true;
    if (typeof a.rank === 'number') f.rk = a.rank;
    if (a.isLocal) f.loc = true;
    if (a.npcCleaner) f.npc = true;
    const pose = live && live.poseOf ? live.poseOf(a.name) : null;
    if (pose && pose.key) f.po = String(pose.key).slice(0, 16);
    const em = live && live.emoteOf ? live.emoteOf(a.name) : null;
    if (em) f.em = String(em).slice(0, 16);
    if (a.sheetPopT > 0) f.sp = true;
    list.push(f);
  }
  return list;
}

// ReplayBuf is the ring: sample() pushes one frame with the board
// clock; frames older than RETAIN fall off the front (容量裁剪——
// 验收 1 的口径：数帧即可，无需事件帧). The t unit is the caller's
// business — the live ring feeds board-clock seconds, dayBufOf feeds
// wall-clock ms; frameAt/shadowAt never look past ordering.
export class ReplayBuf {
  constructor(opts = {}) {
    this.frames = [];   // [{t, list}] ascending t
    this.ms = !!opts.ms; // r_32.1：day 缓冲的 t 是墙钟 ms——生命动画按秒换算
  }

  // sample pushes one frame with the board clock; frames older than
  // RETAIN fall off the front. room/live（r_32.2）：房间级视态快照随帧
  // 同行（缺省 undefined＝纯演员面老用法不破），live 是仪式队列查询面
  // （poseOf/emoteOf——frameOf 借它入列 po/em）。返回 {a, r}（Recorder
  // 兼容收裸数组）。The t unit is the caller's business — the live ring
  // feeds board-clock seconds, dayBufOf feeds wall-clock ms.
  sample(clock, actors, room, live) {
    const t = Math.round(clock * 2) / 2; // 0.5s 栅格对齐
    const list = frameOf(actors, live);
    const last = this.frames[this.frames.length - 1];
    if (last && last.t === t) {
      last.list = list; // 同栅格重采样（resize 抖动）——覆盖
      last.room = room;
      return { a: list, r: room };
    }
    this.frames.push({ t, list, room });
    const floor = t - RETAIN;
    while (this.frames.length > 1 && this.frames[1].t <= floor) {
      this.frames.shift(); // 挤出最旧（保首帧防全空）
    }
    return { a: list, r: room };
  }

  get span() {
    if (this.frames.length < 2) return 0;
    return this.frames[this.frames.length - 1].t - this.frames[0].t;
  }

  get latestT() {
    return this.frames.length ? this.frames[this.frames.length - 1].t : 0;
  }

  get earliestT() {
    return this.frames.length ? this.frames[0].t : 0;
  }

  // frameAt picks the bracket around T: [前帧, 后帧]（T 恰在帧上时两帧
  // 同指——插值退化为静止）。bracketAt 是它的索引版——起源扫描需要
  // 前帧的下标。
  frameAt(T) {
    const br = this.bracketAt(T);
    return br ? [br.a, br.b] : null;
  }

  bracketAt(T) {
    const n = this.frames.length;
    if (n === 0) return null;
    let i = n - 1;
    if (T <= this.frames[0].t) return { i: 0, a: this.frames[0], b: this.frames[0] };
    if (T >= this.frames[n - 1].t) i = n - 1;
    else {
      for (let j = 1; j < n; j++) {
        if (this.frames[j].t >= T) { i = j - 1; break; }
      }
    }
    return { i, a: this.frames[i], b: this.frames[Math.min(i + 1, n - 1)] };
  }
}

// WALK_MIN_PX is the bracket displacement that reads as walking: the
// stroll floor is 26px/s (0.5s → 13px), idle jitter is 0 (positions are
// 0.25px-quantized) — anything past 1px between adjacent frames is a
// deliberate step (r_32).
const WALK_MIN_PX = 1;

// actorIn finds one actor row by name in a frame's list (null when the
// frame doesn't track them — 离场帧).
function actorIn(frame, name) {
  if (!frame) return null;
  for (const f of frame.list) {
    if (f.n === name) return f;
  }
  return null;
}

// runOrigin walks back from frame i while the actor's field holds val —
// the first frame of the run（这条泡/这个 ✓ 是哪帧起的）.
function runOrigin(buf, i, name, get, val) {
  let j = i;
  const floor = Math.max(0, i - RUN_CAP);
  while (j > floor) {
    const prev = actorIn(buf.frames[j - 1], name);
    if (!prev || get(prev) !== val) break;
    j--;
  }
  return j;
}

// runChangeT walks forward for the first frame where the field leaves val
// (null = 到缓冲尾还是同值——泡还活着). 泡在播放里的真实消亡时刻：源
// 帧一切，画面跟着切。
function runChangeT(buf, i, name, get, val) {
  const ceil = Math.min(buf.frames.length - 1, i + RUN_CAP);
  for (let j = i + 1; j <= ceil; j++) {
    const next = actorIn(buf.frames[j], name);
    if (!next || get(next) !== val) return buf.frames[j].t;
  }
  return null;
}

// shadowAt rebuilds one shadow actor view-model from interpolated
// frames — a PLAIN object (not a stepped Actor): the paint loop reads
// name/x/y/dir/bubble/working (+hold/c/hair，r_32.1 再加 bt/bubbleTtl/
// speakT/emote/deskIdx/label/doneT/foodKey), nothing else touches
// it. walking is inferred from the bracket's displacement so the paint
// side can run the walk cycle the snapshot's animT never carried.
//
// r_32.1 生命动画：泡与 ✓ 的出现时刻用起源扫描定到「同值游程的首
// 帧」，寿命＝起源＋LIFE 与「游程后首帧」双钳——回放里泡弹入、停
// 留、淡出与实时同一节拍；稀疏日缓冲的漫长空档不再把前帧的泡定格
// 到天荒地老。speakT/emoteT 同源推出（表情键从泡文本确定性匹配，
// 与实时 say → onSay 同一条规则，无需另录）。
export function shadowAt(buf, T) {
  const br = buf.bracketAt(T);
  if (!br) return [];
  const { i: ia, a: fa, b: fb } = br;
  const span = fb.t - fa.t;
  const k = span > 0 ? Math.min(1, Math.max(0, (T - fa.t) / span)) : 0;
  const u = buf.ms ? 1000 : 1; // 帧面时间单位→秒（ring 秒 / day 墙钟 ms）
  const byName = new Map();
  for (const f of fb.list) byName.set(f.n, f);
  const out = [];
  for (const f of fa.list) {
    const g = byName.get(f.n);
    if (!g) continue; // 前帧在、后帧掉线：不画（保守——离场即消影）
    // 泡的完整生命（r_32.1）：文本取前帧离散值（§一），生死由游程定。
    let bubble = '', bt = 0, bubbleTtl = 0, speakT = 0, emote = null, emoteT = 0;
    if (f.b) {
      const org = runOrigin(buf, ia, f.n, (x) => x.b, f.b);
      const age = (T - buf.frames[org].t) / u;
      const changeT = runChangeT(buf, org, f.n, (x) => x.b, f.b);
      const death = Math.min(buf.frames[org].t / u + BUBBLE_LIFE,
        changeT === null ? Infinity : changeT / u);
      bubbleTtl = Math.min(BUBBLE_LIFE, Math.max(0, death - T / u));
      if (bubbleTtl > 0) {
        bubble = f.b;
        bt = f.bt || 0;
        if (bt !== 3 && age < SPEAK_BEAT) speakT = SPEAK_BEAT - age; // 镜像泡不弹跳（实时同口径）
        if (age < EMOTE_SHOW) {
          const key = emoteFromText(f.b);
          if (key) { emote = key; emoteT = EMOTE_SHOW - age; }
        }
      }
    }
    // 落定 ✓ 的谢幕钟：起源帧起 2s 随时间淡出（t_104 同款）。
    let doneT = 0;
    if (f.dn) {
      const org = runOrigin(buf, ia, f.n, (x) => !!x.dn, true);
      doneT = Math.max(0, DONE_SHOW - (T - buf.frames[org].t) / u);
    }
    // r_32.2 动作覆盖层：po 起源扫描按 POSE_DUR 钳生命（carry=0 无限
    // 期——跟手持物走，游程多长活多长），poseT 是已进行秒（paint 的
    // 节拍输入）。
    let pose = '', poseT = 0;
    if (f.po) {
      const org = runOrigin(buf, ia, f.n, (x) => x.po, f.po);
      const age = (T - buf.frames[org].t) / u;
      const dur = POSE_DUR[f.po];
      if (dur === undefined || dur <= 0 || age < dur) {
        pose = f.po;
        poseT = age;
      }
    }
    // r_32.2 表情泡：优先显式记录的 em（idle/react/任务事件等不经泡
    // 文本的来源全收）——它在场即是权威（实时 byName 一头一泡，react
    // 顶掉 say），窗过了也清掉推导值；帧里没有 em 再从泡文本确定性
    // 推出（r_32.1 的老路，兜老帧）。
    if (f.em) {
      emote = null; emoteT = 0;
      const org = runOrigin(buf, ia, f.n, (x) => x.em, f.em);
      const age = (T - buf.frames[org].t) / u;
      if (age < EMOTE_SHOW) { emote = f.em; emoteT = EMOTE_SHOW - age; }
    }
    // r_32.2 材料送达小弹：起源起 1.5s 上浮淡出。
    let sheetPopT = 0;
    if (f.sp) {
      const org = runOrigin(buf, ia, f.n, (x) => !!x.sp, true);
      sheetPopT = Math.max(0, SHEET_POP_LIFE - (T - buf.frames[org].t) / u);
    }
    const walking = Math.hypot(g.x - f.x, g.y - f.y) > WALK_MIN_PX;
    out.push({
      name: f.n,
      x: f.x + (g.x - f.x) * k,
      y: f.y + (g.y - f.y) * k,
      dir: f.d,          // 朝向取前帧离散值（§一）
      bubble,            // 泡文本（寿命内才有——死了不画）
      bt,                // 泡分层（0 聊天/1 汇报/2 提及/3 镜像）
      bubbleTtl,         // 泡剩余生命（秒）——绘制侧直读，弹入/淡出随它走
      speakT,            // 说话弹跳余量（秒，SPEAK_BEAT 内）
      emote, emoteT,     // 表情泡键＋剩余秒（记录列优先，泡文本兜底）
      pose, poseT,       // 动作覆盖层键＋已进行秒（paintState 的节拍）
      sheetPopT,         // 材料送达小弹剩余秒
      working: f.w,
      hold: f.hold || '',
      holding: f.hold || '', // 手持件绘制面（paintHeldSheet/paintFoodHand 读它）
      foodKey: f.fk || '',
      c: f.c || '',
      hair: f.hair || '',
      deskIdx: typeof f.dk === 'number' ? f.dk : -1,
      label: f.lab || '',
      doneT,
      meetIdx: typeof f.mi === 'number' ? f.mi : -1,
      grace: !!f.gr,
      rank: typeof f.rk === 'number' ? f.rk : null,
      isLocal: !!f.loc,
      npcCleaner: !!f.npc,
      walking,
      moving: walking,   // 手持件摆动门（与实时同字段名）
      animT: T / u,      // 摆动钟（回放秒——与 shadowFrame 的 replaySec 同拍）
    });
  }
  return out;
}

// ── 一日回放（r_32）───────────────────────────────────────────────

// dayBufOf folds fetched day rows ({t: 墙钟 ms, a: [wire actor]}) into
// a ReplayBuf — the same engine the live ring uses, t in ms. Junk rows
// (bad t / missing list) drop on the fold; the caller pages frames in
// ascending next_since order so the push stays sorted.
export function dayBufOf(rows) {
  const buf = new ReplayBuf({ ms: true }); // t＝墙钟 ms——生命动画按秒换算
  for (const r of rows) {
    if (!r || typeof r.t !== 'number' || !Array.isArray(r.a)) continue;
    const last = buf.frames[buf.frames.length - 1];
    if (last && r.t <= last.t) continue; // 乱序护门：frameAt 假设升序
    buf.frames.push({ t: r.t, list: r.a, room: r.r });
  }
  return buf;
}

// ── r_32.2 房间级视态：录制入帧的 r 与影子还原 ──────────────────────

// sameRoom is the change filter's room half: sparse objects built by the
// same recorder code (fixed key order) — stringify equality is exact and
// cheap. undefined ∷ undefined 是无房态（老帧），相等。
export function sameRoom(a, b) {
  const sa = a === undefined ? '' : JSON.stringify(a);
  const sb = b === undefined ? '' : JSON.stringify(b);
  return sa === sb;
}

// prnWalk advances a recorded printer job through its phases to elapsed:
// 快照只存采样瞬间的相位＋已进行秒，回放沿 PRINTER_PHASE 前进；出了
// take 相位活儿就完了（job null）。轻误差 ≤0.5s 采样栅格，肉眼无感。
const PRN_SEQ = [['light', PRINTER_PHASE.LIGHT], ['paper', PRINTER_PHASE.PAPER], ['take', PRINTER_PHASE.TAKE]];
function prnWalk(job, elapsed) {
  if (!job || !job.ph) return null;
  let idx = PRN_SEQ.findIndex(([ph]) => ph === job.ph);
  if (idx < 0) return null;
  let t = (job.t || 0) + elapsed;
  while (idx < PRN_SEQ.length - 1 && t >= PRN_SEQ[idx][1]) {
    t -= PRN_SEQ[idx][1];
    idx++;
  }
  if (idx === PRN_SEQ.length - 1 && t >= PRN_SEQ[idx][1]) return null; // 取材完，机器空
  return { ph: PRN_SEQ[idx][0], k: job.k, t };
}

// shadowRoomAt rebuilds the room-level view state at T — the duck-typed
// twin of RoomView's live snapshot (lit/note/pourT/plantWater/prn/en/tro/
// ia). 离散面取前帧；瞬时态（倒水/浇花/出纸/垃圾龄/交互反馈）按帧
// 起点沿时长表前进——稀疏帧之间的空档里它们照常走完自己的生命，不
// 定格、不穿越。clock 是回放秒（节拍闪烁用）。
export function shadowRoomAt(buf, T) {
  const br = buf.bracketAt(T);
  const u = buf.ms ? 1000 : 1;
  const clock = T / u;
  const r = (br && br.a && br.a.room) || {};
  const elapsed = br ? (T - br.a.t) / u : 0;
  const plantWater = new Map();
  for (const [k, v] of Object.entries(r.pw || {})) {
    const remain = v - elapsed;
    if (remain > 0) plantWater.set(Number(k), clock - remain); // born 反推：age 公式照旧
  }
  const prn = r.prn || {};
  const ia = new Map();
  for (const e of r.ia || []) {
    if (!Array.isArray(e) || !e.length) continue;
    const t = (e[1] || 0) + elapsed;
    if (t >= (INTERACT_T[e[0]] || 1)) continue; // 寿终的反馈不上画面
    ia.set(e[0], { t, x: e[2], y: e[3], i: e[4] });
  }
  return {
    clock,
    lit: !!r.lit,
    note: r.note || '',
    pourT: Math.max(0, (r.pour || 0) - elapsed),
    plantWater,
    prn: { clock, job: prnWalk(prn.job, elapsed), un: (prn.un || [])
      .map(([kind, remain]) => [kind, remain - elapsed])
      .filter(([, remain]) => remain > 0) },
    en: {
      bin: r.en && typeof r.en.bin === 'number' ? r.en.bin : 0,
      snacks: r.en && typeof r.en.snacks === 'number' ? r.en.snacks : 0,
      lt: ((r.en && r.en.lt) || [])
        .map((l) => ({ x: l[0], y: l[1], age: (l[2] || 0) + elapsed, foodKey: l[3] || '' }))
        .filter((l) => l.age < EPARAMS.litterDecay),
    },
    tro: r.tro || 0,
    ia,
  };
}

// sameSample is the recorder's change filter: true when two frames
// carry nothing worth persisting (sub-half-pixel drift and identical
// discrete fields). An idle office folds to one frame an hour.
// r_32.1：bt/dk/lab/dn/fk 同在离散面——泡分层换了、工位换了、任务牌
// 换了、✓ 亮了、食品换了，都是要落盘的变化。
// r_32.2：入参升为快照形状（{a, r} 或裸数组——老用法不破），房间级
// r 一并对比（sameRoom）。
export function sameSample(snapA, snapB) {
  const a = Array.isArray(snapA) ? snapA : snapA?.a;
  const b = Array.isArray(snapB) ? snapB : snapB?.a;
  if (!a || !b) return false;
  if (!sameRoom(Array.isArray(snapA) ? undefined : snapA.r, Array.isArray(snapB) ? undefined : snapB.r)) return false;
  if (a.length !== b.length) return false;
  const dkOf = (p) => (typeof p.dk === 'number' ? p.dk : -1);
  for (let i = 0; i < a.length; i++) {
    const p = a[i], q = b[i];
    if (!p || !q || p.n !== q.n || p.d !== q.d || p.b !== q.b ||
        p.w !== q.w || p.hold !== q.hold || p.c !== q.c || p.hair !== q.hair ||
        (p.bt || 0) !== (q.bt || 0) || dkOf(p) !== dkOf(q) ||
        (p.lab || '') !== (q.lab || '') || !!p.dn !== !!q.dn ||
        (p.fk || '') !== (q.fk || '') ||
        (p.mi ?? -1) !== (q.mi ?? -1) || !!p.gr !== !!q.gr ||
        (p.rk ?? null) !== (q.rk ?? null) || !!p.loc !== !!q.loc ||
        !!p.npc !== !!q.npc || (p.po || '') !== (q.po || '') ||
        (p.em || '') !== (q.em || '') || !!p.sp !== !!q.sp) {
      return false;
    }
    if (Math.abs(p.x - q.x) >= 0.5 || Math.abs(p.y - q.y) >= 0.5) return false;
  }
  return true;
}

// Shadow walk-cycle frames, indexed by DIR (0 down / 1 up / 2 right /
// 3 left), byte-stable against art.js's FRAME order (downA/B 0/1,
// upA/B 3/4, leftA/B 5/6, rightA/B 7/8) — Actor.frameIndex's moving
// branch minus the blink cue (snapshots carry no blink clock). STANCE
// mirrors roomview's SHADOW_STANCE for the still case.
const SHADOW_WALK = [[1, 0], [4, 3], [8, 7], [6, 5]]; // [B, A] per dir
const SHADOW_STAND = [0, 3, 7, 5];
export const SHADOW_ANIM_FPS = 6; // actor.js ANIM_FPS 的镜像

// shadowFrame picks the shadow's atlas frame: stance when still, the
// A/B walk cycle at SHADOW_ANIM_FPS when the bracket moved (tSec is
// the playback clock — day mode feeds ms/1000).
export function shadowFrame(dir, walking, tSec) {
  const d = (dir >= 0 && dir <= 3) ? dir : 0;
  if (!walking) return SHADOW_STAND[d];
  const ab = SHADOW_WALK[d];
  return Math.floor(tSec * SHADOW_ANIM_FPS) % 2 ? ab[0] : ab[1];
}

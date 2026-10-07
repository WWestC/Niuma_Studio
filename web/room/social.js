// social.js — the 摸鱼行为引擎 (r_06, t_131): idle members' spontaneous
// social theater. Pure ambience over the busy/idle truth — a 摸鱼团
// forms when ≥2 idle-and-reachable members (takerFree 同口径) cluster at
// an anchor, plays its 行为脚本 (走位＋动作＋表情, all r_04 assets), and
// scatters the moment work calls (endBreak 同语义). The「被发现」judgment
// (Lv.2+ within 120 native) plays the 批评链 — theater, never a gate:
// 摸鱼是办公室的呼吸，不是剧情系统.
//
// t_133 的调参面：PARAMS 与台词池全部数据化（本文件顶部常量区——
// 改值即调参，不碰逻辑）。台词是成员口吻的系统播报文案——t() 译。

import { t } from '../ui/i18n.js';
import { dispatchable } from './errand.js';

// ── 平衡参数（初值＝定稿 §三；t_133 调参区）─────────────────────
export const PARAMS = {
  BEAT: 8,               // 节拍器间隔（秒）
  CHANCE: 0.15,          // 每拍对符合条件的候选团发起概率
  COOLDOWN: 300,         // 同一成员两轮摸鱼冷却（秒）
  MAX_GROUPS: 2,         // 全室同时最多摸鱼团数
  SIGHT_R: 120,          // 「被发现」视野半径（native）
  CRIT_DELAY: 1.5,       // 发现→批评链前摇（秒，转身动作时长）
  MOOD_T: 20,            // 被批者情绪持续时间（秒）
  CHAT_ROUNDS: 3,        // 闲聊气泡轮次上限
};

// ── 台词池（t_133 内容填充区）────────────────────────────────────
// 按画像标签配语气：orchestrator（编排催进度）/ hr（打圆场）/
// dev（前端梗）/ plain（通用）。抽一条：按说话者首个命中标签过滤，
// 池空退 plain。
export const LINES = {
  // 批评语（发现者→摸鱼团）
  criticize: {
    orchestrator: [
      t('排期都压着呢，茶歇完了？'),
      t('这个迭代的活儿可不会自己长脚。'),
      t('你们的任务卡都红了，还有心思站这儿？'),
    ],
    hr: [
      t('休息归休息，工位还是要回的哈～'),
      t('我看好你们，别让我在报告里写摸鱼。'),
    ],
    dev: [
      t('bug 不修，茶不香。'),
      t('构建红了，你们忍心？'),
      t('这滤镜一上像素全糊了…啊不是，说你们呢。'),
    ],
    plain: [
      t('上班时间哦。'),
      t('老板一会儿要转过来了。'),
      t('活儿堆着呢，聊完这轮散了吧。'),
    ],
  },
  // 被批回应（摸鱼团成员）
  reply: {
    plain: [
      t('…马上回去。'),
      t('这就走这就走。'),
      t('刚要回去来着。'),
      t('就说两句，两句。'),
      t('这不是正好聊到重点嘛。'),
      t('工位还热乎着呢，这就回。'),
      t('收到收到。'),
    ],
  },
  // 闲聊话题（摸鱼团气泡轮；M11 吐槽大会用加料池）
  chat: {
    plain: [
      t('昨晚那个需求你看了吗？'),
      t('今天的像素风挺顺眼的。'),
      t('听说下个版本要换调色板？'),
      t('茶水间的咖啡豆换了。'),
      t('这地图的走位你摸熟了吗？'),
    ],
    gripe: [ // M11 专属：需求梗
      t('这需求改到第四版了！'),
      t('「再最后调一版」是什么经济学？'),
      t('验收标准和需求文档对不上啊喂。'),
    ],
  },
  // 自言自语（单人机制）
  solo: {
    plain: [
      t('发会儿呆…'),
      t('窗外天气不错。'),
      t('快递应该到了吧？'),
      t('这行代码昨天还好好的。'),
      t('茶水间的灯有点晃眼。'),
    ],
  },
  // 关心语（M5 窗边发呆被 Lv.4+ 发现）
  care: {
    plain: [t('累了就眯会儿，我盯着。')],
  },
};

// 按成员画像选台词池标签（role → 池键；未匹配落 plain）。这里的
// 编排/人事/前端/开发/设计是对后端中文岗位名的匹配字面量，不是
// 显示文案——不译。
function tagOf(actor) {
  const r = String(actor.role || '');
  if (r.includes('编排')) return 'orchestrator';
  if (r.includes('人事')) return 'hr';
  if (r.includes('前端') || r.includes('开发') || r.includes('设计')) return 'dev';
  return 'plain';
}

function pick(pool, tag) {
  const list = (pool[tag] && pool[tag].length) ? pool[tag] : pool.plain;
  if (!list || !list.length) return '';
  return list[(Math.random() * list.length) | 0];
}

// ── 机制清单（M1–M12，定稿 §三逐条）──────────────────────────────
// 每套：人数范围、锚点策略、脚本步骤（简化 DSL：每步＝{dur, do(ctx)}）。
// r_04 动作（A7/A8…）由 roomview 的覆盖层渲染；表情经 board.emoter；
// 本引擎只编排时间线。资产延期时降级为「站位＋表情泡」（保底演出）。

const M = {}; // key → mechanism

function mech(key, min, max, build, opts) { M[key] = { key, min, max, build, opts }; }

// M1 茶水间咖啡叙旧：pantry 锚点围站＋stretch＋coffee 泡＋闲聊 2–3 轮
mech('m1-coffee-chat', 2, 3, (ctx) => [
  { dur: 0, walk: 'pantry', face: 'table' },
  { dur: 1.0, emote: 'coffee', act: 'stretch' },
  { dur: 4, chat: true, rounds: 2 + ((Math.random() * 2) | 0) },
  { dur: 1.0, disperse: true },
]);

// M2 工位串门：甲走向乙工位侧站＋lean＋happy＋对话 2 轮
mech('m2-desk-visit', 2, 2, (ctx) => [
  { dur: 0, walk: 'partner-desk', face: 'partner' },
  { dur: 1.0, emote: 'happy', act: 'lean' },
  { dur: 4, chat: true, rounds: 2 },
  { dur: 1.0, disperse: true },
]);

// M3 围观他人屏幕：围到 doing 者身后＋star＋指指点点；被围观者不批
mech('m3-screen-crowd', 2, 3, (ctx) => [
  { dur: 0, walk: 'busy-desk', face: 'busy' },
  { dur: 1.0, emote: 'star', act: 'point' },
  { dur: 5, chat: true, rounds: 2, quiet: true },
  { dur: 1.0, disperse: true },
], { special: 'crowd' }); // 发现语义反转：围观 doing 者不批评

// M4 白板涂鸦：白板前＋typing 手部＋涂鸦贴纸 3s
mech('m4-doodle', 1, 2, (ctx) => [
  { dur: 0, walk: 'whiteboard', face: 'board' },
  { dur: 3, act: 'typing', doodle: true },
  { dur: 1.0, disperse: true },
]);

// M5 窗边发呆：窗下锚点＋sleepy＋lean，静止 15s；单人不批评（Lv.4+ 关心）
mech('m5-window-daze', 1, 1, (ctx) => [
  { dur: 0, walk: 'window', face: 'window' },
  { dur: 15, emote: 'sleepy', act: 'lean', solo: true },
  { dur: 1.0, disperse: true },
], { special: 'no-criticize', careRank: 4 });

// M6 假装取快递：打印机旁空手取纸＋sweat 心虚
mech('m6-fake-package', 1, 1, (ctx) => [
  { dur: 0, walk: 'printer', face: 'printer' },
  { dur: 1.5, emote: 'sweat', act: 'point' },
  { dur: 2, solo: true, caughtEmote: 'confused' }, // 被发现才演「其实没快递」
  { dur: 1.0, disperse: true },
], { special: 'no-criticize' });

// M7 工位拉伸：就地 stretch 两轮＋happy；发现者自己也摸过→心照不宣
mech('m7-stretch', 1, 2, (ctx) => [
  { dur: 0, walk: 'inplace' },
  { dur: 3, act: 'stretch', emote: 'happy' },
  { dur: 3, act: 'stretch', emote: 'happy' },
  { dur: 1.0, disperse: true },
], { special: 'tacit' });

// M8 围炉煮茶：高吧台站位＋coffee＋lookaround 慢扫＋茶话 3 轮
mech('m8-tea-party', 2, 4, (ctx) => [
  { dur: 0, walk: 'bar', face: 'bar' },
  { dur: 1.0, emote: 'coffee', act: 'lookaround' },
  { dur: 6, chat: true, rounds: 3 },
  { dur: 1.0, disperse: true },
]);

// M9 猜拳倒水：pantry 面对面＋point 出拳 2 轮＋输家走 I3 饮水机
mech('m9-rock-paper', 2, 2, (ctx) => [
  { dur: 0, walk: 'pantry', face: 'partner' },
  { dur: 2.5, act: 'point', rounds: 2 },
  { dur: 0, loserWalk: 'dispenser' }, // 输家接 I3 倒水演出
  { dur: 2.5, emote: 'done' },
  { dur: 1.0, disperse: true },
], { special: 'legit' }); // 正当团建：不批评；Lv.5+ 可加入下轮（预留）

// M10 传纸条：甲递纸（carry 文件夹）乙接＋done
mech('m10-note-pass', 2, 2, (ctx) => [
  { dur: 0, walk: 'partner-desk', face: 'partner', carry: 'done' },
  { dur: 2.5, chat: false, act: 'point' },
  { dur: 1.0, emote: 'done' },
  { dur: 1.0, disperse: true },
]);

// M11 集体吐槽大会：围站＋angry/happy 交替＋吐槽 3 轮；发现→瞬间安静
mech('m11-gripe-fest', 3, 4, (ctx) => [
  { dur: 0, walk: 'floor-cluster', face: 'center' },
  { dur: 6, chat: true, rounds: 3, gripe: true, altEmote: ['angry', 'happy'] },
  { dur: 1.0, disperse: true },
], { special: 'instant-quiet' });

// M12 午睡代表：深夜 0–6 点工位原位 sleepy＋Z 泡渐大；房主点击才醒
mech('m12-nap', 1, 1, (ctx) => [
  { dur: 0, walk: 'inplace' },
  { dur: 30, emote: 'sleepy', act: 'none', nap: true },
  { dur: 1.0, disperse: true },
], { special: 'no-criticize', nightOnly: true, wakeOnTap: true });

// MECHS 是机制清单的导出视图（验收计数/调试用；键＝机制 key）
export const MECHS = M;

// ── 摸鱼团（一场进行中的社交）────────────────────────────────────
class Group {
  constructor(mech, actors) {
    this.mech = mech;
    this.actors = actors;       // Actor[]
    this.steps = mech.build();
    this.si = 0;                // step index
    this.t = 0;                 // seconds into step
    this.chatDone = 0;          // 闲聊轮数已放
    this.caught = false;        // 被发现了
    this.critic = null;         // 批评者（发现后定）
    this.done = false;
    // t_132：moodUntil 迁引擎级（原挂团上，disperse 移团后轮询即停
    // ——被批后的 20s sweat 情绪实际从不冒），团上不再留副本
  }
}

// ── 引擎 ──────────────────────────────────────────────────────────
export class SocialEngine {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.groups = [];
    this.beatT = 0;
    this.cooldown = new Map(); // name → clock 下次可摸鱼时刻
    // t_132：情绪引擎级 Map——moodUntil（被批后 20s sweat 周期冒）＋
    // lowMood（5 分钟 idle 低落池），生命周期独立于团
    this.moodUntil = new Map(); // name → clock 情绪截止
    this.lowMood = new Map(); // name → clock 低落期截止
  }

  // 锚点解析：机制脚本里的 walk 关键字 → 场景坐标（复用现有几何）
  anchor(kind, ctx) {
    const g = this.board.art.geom;
    if (!g) return null;
    const b = this.board.bounds();
    switch (kind) {
      case 'pantry': { // 茶水间锚点（歇脚位空着就借一个）
        const spots = (g.pantry && g.pantry.spots) || [];
        if (spots.length) {
          const s = spots[(Math.random() * spots.length) | 0];
          return { x: s.spot[0], y: s.spot[1] };
        }
        return null;
      }
      case 'printer':
        return g.printer ? { x: g.printer.spot[0], y: g.printer.spot[1] } : null;
      case 'whiteboard': {
        const [bx, by, bw, bh] = g.whiteboard;
        return { x: bx + bw / 2, y: by + bh + 10 };
      }
      case 'window':
        return { x: 150, y: b.minY + 20 };
      case 'bar': { // 高吧台（茶水台北臂台前）
        const p = g.pantry;
        if (p) return { x: (p.rect[0] + p.rect[2] / 2), y: p.rect[1] + p.rect[3] + 12 };
        return null;
      }
      case 'partner-desk': { // 伙伴工位侧
        const pa = ctx.partner;
        return pa ? { x: pa.x + 24, y: pa.y } : null;
      }
      case 'busy-desk': { // 被围观者（doing）身后
        const ba = ctx.busy;
        return ba ? { x: ba.x + 18, y: ba.y + 14 } : null;
      }
      case 'dispenser':
        return g.interact ? { x: g.interact.dispenser[0] + 10, y: g.interact.dispenser[1] + g.interact.dispenser[3] + 6 } : null;
      case 'floor-cluster': { // 开放地板成团点（发起者附近）
        const lead = ctx.lead;
        return lead ? { x: lead.x + 30, y: lead.y } : null;
      }
      case 'inplace':
      default:
        return null; // 原地
    }
  }

  // 成员资格：errand.js 一口径（非本机/非 NPC/非忙/非会/非歇/无任何
  // 外勤在途/手里没件——旧注释自称「非打印取材中」但取材在途从未有
  // 状态可查，a.fetching 现在真的存在了）
  eligible(a) {
    if (!a || a.isLocal || a.isNPC) return false; // t_139：NPC 不进摸鱼池
    if (a.socialGroup) return false; // 已在团
    return dispatchable(a, this.board.busy);
  }

  // 深夜判定（M12）：本地时钟 0–6 点
  nightNow() {
    const h = new Date().getHours();
    return h < 6;
  }

  // 节拍：成团判定＋发起
  beat() {
    if (this.groups.length >= PARAMS.MAX_GROUPS) return;
    const clock = this.board.clock || 0;
    const idle = [];
    for (const a of this.board.actors.values()) {
      if (!this.eligible(a)) continue;
      if ((this.cooldown.get(a.name) || 0) > clock) continue;
      idle.push(a);
    }
    if (idle.length < 1) return;
    // 挑机制：M12 仅深夜；其余按场景条件过滤后随机
    const cands = Object.values(M).filter((m) => {
      if (m.opts && m.opts.nightOnly) return this.nightNow();
      if (m.opts && m.opts.nightOnly === false) return false;
      return true;
    });
    if (!cands.length) return;
    if (Math.random() > PARAMS.CHANCE) return;
    const mech = cands[(Math.random() * cands.length) | 0];
    // 按机制人数聚人（可寻路近似：同房间地板即连通——现有房间无隔断区）
    const want = mech.min + ((Math.random() * (mech.max - mech.min + 1)) | 0);
    const team = idle.slice(0, Math.max(mech.min, Math.min(want, idle.length)));
    if (team.length < mech.min) return;
    // 特定语境对象
    const ctx = { lead: team[0], partner: team[1] || null, busy: null };
    if (mech.key === 'm3-screen-crowd') {
      for (const a of this.board.actors.values()) {
        if (!a.isLocal && a.working && a.deskIdx >= 0) { ctx.busy = a; break; }
      }
      if (!ctx.busy) return; // 没有 doing 者围观什么
    }
    if (mech.key === 'm11-gripe-fest' && team.length < 3) return;
    const grp = new Group(mech, team);
    for (const a of team) a.socialGroup = grp;
    this.groups.push(grp);
    if (team.length >= 2) sfx('scene-whisper'); // r_09：≥2 人成团窃窃私语（冷却 8s 兜一轮摸鱼一声）
    // 冷却立即起算（从成团时刻）
    for (const a of team) this.cooldown.set(a.name, clock + PARAMS.COOLDOWN);
  }

  // 发现判定：Lv.2+ 在座成员与团内任一人距离 ≤ SIGHT_R（纯距离无遮挡）。
  // t_132：房主（isLocal，Lv.99）同样有「看到」资格——老板的注视也是
  // 监督；eating 中的成员在吃（生理需求豁免，不参与发现也不算数）。
  findCatches(grp) {
    const out = [];
    for (const w of this.board.actors.values()) {
      if (w.isNPC) continue; // t_139：保洁阿姨的注视不算监督（isNPC 豁免位）
      if (w.eating) continue; // r_07 豁免：吃东西的人不看别人摸鱼
      if (!w.isLocal && grp.actors.includes(w)) continue;
      const rk = w.isLocal ? 99 : (w.rank == null ? 1 : w.rank);
      if (rk < 2) continue; // Lv.2+ 才有「看到」的资格
      if (!w.isLocal && (!this.board.busy || !this.board.busy.has(w.name))) continue; // 闲人没立场（不批评，但也算发现吗？定稿：发现者闲→nervous 不批评——记录但标记）
      for (const a of grp.actors) {
        if (Math.hypot(w.x - a.x, w.y - a.y) <= PARAMS.SIGHT_R) { out.push(w); break; }
      }
    }
    return out;
  }

  // 批评链（定稿 §二四拍）
  playCriticism(grp, watchers) {
    const board = this.board;
    // 机制特判：正当团建/心照不宣/瞬间安静
    const o = grp.mech.opts || {};
    if (o.special === 'legit' || o.special === 'tacit' || o.special === 'no-criticize') {
      if (o.special === 'instant-quiet' || o.special !== 'no-criticize') {
        // M11：全员 E8 立即散；M9/M7：各自 E8 心照不宣散
        for (const a of grp.actors) board.emoter.emote(a.name, 'sweat');
        this.disperse(grp);
      }
      return;
    }
    // 批评者＝发现者中最忙的那个（doing 优先）；全闲→不批评只 nervous
    let critic = null;
    for (const w of watchers) {
      if (board.busy && board.busy.has(w.name)) { critic = w; break; }
    }
    if (!critic) {
      for (const a of grp.actors) board.emoter.emote(a.name, 'confused');
      return; // 闲人发现：团收到 nervous（用 confused 代）不加戏自然收
    }
    // 四拍：①批评者转身（前摇）②走近摸鱼处＋point 动作＋台词 ③被批者
    // 回应散场回工位 ④情绪标记（t_132：sad 池挂 5 分钟，idle 偏低落）
    grp.critic = critic;
    board.emoter.emote(critic.name, 'working');
    // t_132：批评者走向摸鱼团（1 步距离＝锚点外 40 native），朝向团心
    const cx = grp.actors.reduce((s, a) => s + a.x, 0) / grp.actors.length;
    const cy = grp.actors.reduce((s, a) => s + a.y, 0) / grp.actors.length;
    const d = Math.hypot(critic.x - cx, critic.y - cy) || 1;
    const keep = 40; // 站定距离：训话不走脸上
    const tx = cx - (cx - critic.x) / d * keep;
    const ty = cy - (cy - critic.y) / d * keep;
    if (Math.hypot(critic.x - cx, critic.y - cy) > keep) {
      critic.tx = tx; critic.ty = ty; critic.hasTarget = true;
    }
    setTimeout(() => {
      if (grp.done) return;
      // t_132：训话动作 A8 point（t_122 的姿势覆盖层）
      board.poser.pose(critic.name, 'point', 1.6);
      board.say(critic.name, pick(LINES.criticize, tagOf(critic)), Math.floor(Date.now() / 1000), false, false, false);
      for (const a of grp.actors) {
        board.emoter.emote(a.name, Math.random() < 0.5 ? 'angry' : 'sweat');
      }
      setTimeout(() => {
        if (grp.done) return;
        for (const a of grp.actors) {
          board.say(a.name, pick(LINES.reply, 'plain'), Math.floor(Date.now() / 1000), false, false, false);
        }
        this.disperse(grp);
        // 情绪挂 20s：回工位后 sweat 泡周期性冒（引擎级 Map——团散了
        // 轮询也不断，t_132 修正）
        const until = (board.clock || 0) + PARAMS.MOOD_T;
        for (const a of grp.actors) this.moodUntil.set(a.name, until);
        // t_132：短期情绪标记——5 分钟内 idle 表情偏低落（跨团存引擎级）
        const lowUntil = (board.clock || 0) + 300;
        for (const a of grp.actors) this.lowMood.set(a.name, lowUntil);
      }, 1000);
    }, PARAMS.CRIT_DELAY * 1000);
  }

  disperse(grp) {
    grp.done = true;
    for (const a of grp.actors) {
      if (a.socialGroup === grp) a.socialGroup = null;
      a.hasTarget = false;
      a.idleT = 0.5 + Math.random() * 1.5;
    }
    const i = this.groups.indexOf(grp);
    if (i >= 0) this.groups.splice(i, 1);
  }

  // tick：节拍＋团时间线推进＋发现判定
  step(dt) {
    const board = this.board;
    this.beatT -= dt;
    if (this.beatT <= 0) { this.beatT = PARAMS.BEAT; this.beat(); }
    const clock = board.clock || 0;
    // t_132：情绪轮询提引擎级（原在团循环里——disperse 后即停，被批者
    // 的 20s sweat 从不冒；两个情绪 Map 一并过期清理）
    for (const [name, until] of this.moodUntil) {
      if (clock >= until) { this.moodUntil.delete(name); continue; }
      if (Math.random() < dt * 0.2) board.emoter.emote(name, 'sweat');
    }
    for (const [name, until] of this.lowMood) {
      if (clock >= until) this.lowMood.delete(name);
    }
    for (const grp of [...this.groups]) {
      if (grp.done) continue;
      // 中断纪律：任何成员被派活/叫会/取材 → 立即散（endBreak 同语义）
      for (const a of grp.actors) {
        if (!this.eligible(a) && a.socialGroup === grp) { this.disperse(grp); break; }
      }
      if (grp.done) continue;
      // 发现判定（每拍一次即可——这里每 step 判，节流到 1s）
      grp.sightT = (grp.sightT || 0) + dt;
      if (!grp.caught && grp.sightT >= 1) {
        grp.sightT = 0;
        const watchers = this.findCatches(grp);
        if (watchers.length) {
          grp.caught = true; // 该轮不加戏：闲聊轮次封顶
          this.playCriticism(grp, watchers);
        }
      }
      // 时间线推进
      const step = grp.steps[grp.si];
      if (!step) { this.disperse(grp); continue; }
      grp.t += dt;
      // 走位（step 0 时执行一次）
      if (!step.walked) {
        step.walked = true;
        const ctx = { lead: grp.actors[0], partner: grp.actors[1] || null, busy: null };
        for (const m of ['m3-screen-crowd']) {
          if (grp.mech.key === m) {
            for (const a of board.actors.values()) {
              if (!a.isLocal && a.working && a.deskIdx >= 0) { ctx.busy = a; break; }
            }
          }
        }
        grp.actors.forEach((a, i) => {
          const an = this.anchor(step.walk, ctx);
          if (an && step.walk !== 'inplace') {
            // 围站：以锚点为中心小散布
            a.tx = an.x + (i - (grp.actors.length - 1) / 2) * 22;
            a.ty = an.y;
            a.hasTarget = true;
          }
        });
        if (step.emote) for (const a of grp.actors) board.emoter.emote(a.name, step.emote);
      }
      // 闲聊轮（chat step：每 1.8s 放一人的气泡）
      if (step.chat && grp.t > grp.chatDone * 1.8 && (!grp.caught || grp.chatDone < 2)) {
        const a = grp.actors[grp.chatDone % grp.actors.length];
        const pool = step.gripe ? LINES.chat.gripe : LINES.chat.plain;
        board.say(a.name, pick({ plain: pool }, 'plain'), Math.floor(Date.now() / 1000), false, false, false);
        grp.chatDone++;
        if (grp.chatDone >= (step.rounds || 2) * grp.actors.length) step.chatDone = true;
      }
      // 步进
      if (grp.t >= step.dur) { grp.si++; grp.t = 0; }
    }
  }
}

// emote.js — the 表情系统 (r_04, t_121): pixel emoji bubbles over the
// little people's heads. Twelve icons (E1–E12 per design/r04-expressive
// §一), each drawn live on the canvas in the office's own language —
// 16-grid shapes at 2px stroke, bubbleEdge outline, two frames with a
// 0.6s micro-beat. One bubble per head at a time (a new one displaces
// the old). Pure presentation: triggers come from say keywords, task
// events and idle randomness, and nothing here ever blocks or throws.

import { sfx } from './sfxport.js';

const EDGE = '#282828';   // bubbleEdge — the readability outline
const SHOW = 2.0;          // seconds a bubble stays (fade on the last 0.4s)
const BEAT = 0.6;          // the two-frame micro-beat period

// EMOJIS — the twelve pixel bubbles. draw(ctx, x, y, f) paints icon
// frame f (0|1) with its center-ish anchor at (x, y); every shape is
// built from small rects/paths at 2px weight in the 分册's palette.
export const EMOJIS = {
  // E1 happy：弧线嘴＋弯眼（暖金）
  happy: { emoji: '😊', draw(ctx, x, y, f) {
    const w = f ? 0 : 1; // 微动：整体上 1px
    ctx.strokeStyle = '#d9b96a'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(x, y - 3 + w, 3, 0.15 * Math.PI, 0.85 * Math.PI); ctx.stroke(); // 弯嘴
    ctx.fillStyle = '#d9b96a';
    ctx.fillRect(x - 5, y - 6 + w, 2, 2); ctx.fillRect(x + 3, y - 6 + w, 2, 2); // 弯眼两点
  } },
  // E2 thinking：头顶「…」三点渐次点亮
  thinking: { emoji: '🤔', draw(ctx, x, y, f) {
    ctx.fillStyle = EDGE;
    const on = Math.floor(Date.now() / 300) % 3;
    for (let i = 0; i < 3; i++) {
      if ((f ? on >= i : i === 0)) ctx.fillRect(x - 5 + i * 4, y - 4 - i * 2, 2, 2);
    }
    ctx.strokeStyle = EDGE; ctx.lineWidth = 2;
    ctx.strokeRect(x - 1, y + 1, 3, 3); // 头顶托腮的问号底框简化为点框
  } },
  // E3 working：锤子＋火花（与任务气泡的锤子同形）
  working: { emoji: '🔨', draw(ctx, x, y, f) {
    ctx.strokeStyle = '#d9b96a'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.moveTo(x - 4, y + 4); ctx.lineTo(x, y); ctx.stroke(); // 柄
    ctx.strokeRect(x - 3, y - 6, 6, 3); // 锤头
    if (f) { ctx.fillStyle = '#f2c14e'; ctx.fillRect(x + 4, y - 7, 2, 2); ctx.fillRect(x + 6, y - 4, 2, 2); } // 火花帧
  } },
  // E4 done：绿✓＋小星
  done: { emoji: '✅', draw(ctx, x, y, f) {
    ctx.strokeStyle = '#34c724'; ctx.lineWidth = 2.4;
    ctx.beginPath(); ctx.moveTo(x - 5, y); ctx.lineTo(x - 1, y + 4); ctx.lineTo(x + 6, y - 5); ctx.stroke();
    if (f) { ctx.fillStyle = '#f2c14e'; ctx.fillRect(x + 5, y - 8, 2, 2); } // 星闪
  } },
  // E5 confused：立起的「?」
  confused: { emoji: '❓', draw(ctx, x, y, f) {
    ctx.strokeStyle = '#a9b2bb'; ctx.lineWidth = 2;
    const dy = f ? -1 : 0;
    ctx.beginPath(); ctx.arc(x, y - 4 + dy, 3, Math.PI, 0); // ? 的弯
    ctx.lineTo(x + 3, y + dy); ctx.stroke();
    ctx.fillStyle = '#a9b2bb'; ctx.fillRect(x - 1, y + 3 + dy, 2, 2); // 点
  } },
  // E6 sleepy：Z 渐大 ×3（呼应品牌 Z）
  sleepy: { emoji: '😴', draw(ctx, x, y, f) {
    ctx.strokeStyle = '#8f99a3'; ctx.lineWidth = 2;
    const zs = [[-6, 2, 4], [0, -2, 6], [7, -7, 8]];
    zs.forEach(([zx, zy, s], i) => {
      if (f && i === 0) return; // 微动：最小的 Z 交替隐现
      ctx.strokeRect(x + zx, y + zy, s, s);
    });
  } },
  // E7 coffee：像素咖啡杯＋热气两缕
  coffee: { emoji: '☕', draw(ctx, x, y, f) {
    ctx.fillStyle = '#fbfbf8'; ctx.strokeStyle = EDGE; ctx.lineWidth = 2;
    ctx.fillRect(x - 5, y - 3, 8, 6); ctx.strokeRect(x - 5, y - 3, 8, 6); // 杯
    ctx.beginPath(); ctx.moveTo(x + 3, y - 1); ctx.quadraticCurveTo(x + 7, y, x + 3, y + 2); ctx.stroke(); // 柄
    ctx.strokeStyle = '#b4e2f2';
    const w = f ? 1 : 0;
    ctx.beginPath(); ctx.moveTo(x - 3, y - 6 + w); ctx.lineTo(x - 4, y - 9 + w); ctx.stroke();
    ctx.beginPath(); ctx.moveTo(x + 1, y - 6 - w); ctx.lineTo(x, y - 9 - w); ctx.stroke(); // 热气
  } },
  // E8 sweat：汗滴×2 斜挂额角
  sweat: { emoji: '😰', draw(ctx, x, y, f) {
    ctx.fillStyle = '#8fd0e8';
    const dy = f ? 1 : 0;
    ctx.fillRect(x - 6, y - 4 + dy, 2, 4);
    ctx.fillRect(x + 4, y - 2 - dy, 2, 4);
    ctx.strokeStyle = EDGE; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(x, y + 3, 3, 0.1 * Math.PI, 0.9 * Math.PI); ctx.stroke(); // 苦嘴
  } },
  // E9 angry：红「#!」双符
  angry: { emoji: '😠', draw(ctx, x, y, f) {
    ctx.fillStyle = '#f54a45';
    ctx.fillRect(x - 4, y - 5, 2, 8); // 竖线
    ctx.fillRect(x - 4, y + 4, 2, 2); // 点
    ctx.fillRect(x + 1, f ? y - 6 : y - 5, 2, 2); ctx.fillRect(x + 2, f ? y - 3 : y - 2, 2, 6); // 闪电
  } },
  // E10 love：红心＋光点
  love: { emoji: '❤️', draw(ctx, x, y, f) {
    ctx.fillStyle = '#f54a45';
    ctx.fillRect(x - 5, y - 4, 4, 3); ctx.fillRect(x + 1, y - 4, 4, 3); // 两瓣
    ctx.fillRect(x - 5, y - 1, 10, 3); ctx.fillRect(x - 3, y + 2, 6, 2); ctx.fillRect(x - 1, y + 4, 2, 1); // 收尖
    if (f) { ctx.fillStyle = '#f2c14e'; ctx.fillRect(x + 6, y - 7, 2, 2); } // 光点
  } },
  // E11 star：金星两帧交替
  star: { emoji: '⭐', draw(ctx, x, y, f) {
    ctx.fillStyle = '#f2c14e';
    ctx.fillRect(x - 1, y - 6, 3, 10); ctx.fillRect(x - 5, y - 2, 11, 3); // 十字
    ctx.fillRect(x - 3, y - 4, 7, 7);
    if (f) { ctx.fillStyle = '#fffdf5'; ctx.fillRect(x - 1, y - 4, 1, 2); } // 高光闪
  } },
  // E12 wave：挥手小手＋弧线
  wave: { emoji: '👋', draw(ctx, x, y, f) {
    ctx.fillStyle = '#e9bd92'; // skin
    ctx.fillRect(x - 2, y - 4, 5, 5); // 掌
    const lift = f ? 2 : 0;
    ctx.fillRect(x - 1, y - 8 + (f ? 0 : 1), 4, 4 - (f ? 1 : 0)); // 摆动的上臂
    ctx.strokeStyle = '#d9b96a'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(x + 4, y - 6 + lift, 2, -0.5 * Math.PI, 0.5 * Math.PI); ctx.stroke(); // 挥手弧
  } },
};

// 每像素泡 ↔ 聊天 emoji 的映射（分册定稿口径：只收 12 枚有像素对应的，
// 其余不触发——宁缺毋滥）
export const EMOJI_TO_EMOTE = new Map(
  Object.entries(EMOJIS).map(([k, v]) => [v.emoji, k]));

// say 关键词表（小牛场景映射分册的轻量版——定稿前的保守集合）：
// 命中即冒对应泡，一句话只冒一个（首个命中）
const KEYWORDS = [
  [/谢|谢谢|感谢|好耶|太好了|厉害|棒/, 'happy'],
  [/哈哈|笑|有趣/, 'happy'], // emoji（😄😊）不进正则——直配通道按首码点定优先级
  [/想想|思考|让我想|Hmm|嗯…/, 'thinking'],
  [/为什么|怎么|？|\?|不懂|没明白/, 'confused'],
  [/累|困|睡了|zzz|Zzz/, 'sleepy'],
  [/咖啡|喝茶|接水|摸鱼/, 'coffee'],
  [/糟|bug|坏了|失败|错误|完蛋/, 'sweat'],
  [/气|怒|可恶|气死/, 'angry'],
  [/喜欢|爱|❤|心/, 'love'],
  [/你好|hi|Hi|嗨|欢迎|欢迎新|大家好|早上好|晚上好|到岗|上岗/, 'wave'],
];

/** Which emote (if any) a say line's text should pop. */
export function emoteFromText(text) {
  if (!text) return null;
  for (const [re, key] of KEYWORDS) {
    if (re.test(text)) return key;
  }
  // 聊天 emoji 直配（😊 → happy）：整条消息里第一个被映射的 emoji
  for (const ch of String(text)) {
    const k = EMOJI_TO_EMOTE.get(ch);
    if (k) return k;
  }
  return null;
}

// idle 随机池（空闲牛马的低频自发表情——分册触发源之三）
const IDLE_POOL = ['happy', 'thinking', 'sleepy', 'coffee', 'confused', 'star'];
// t_132 低落池：被批评后 5 分钟内的 idle 表情偏向这里（跳过 happy/star
// 正面款——r_06 定稿 §二.3「期间 idle 表情池跳过 happy 类」）
const IDLE_LOW_POOL = ['sweat', 'thinking', 'sleepy', 'confused', 'working'];

/** One live bubble on one head. */
class Bubble {
  constructor(key, isIdle) {
    this.key = key;
    this.isIdle = !!isIdle; // idle 泡计入同屏 2 泡上限
    this.t = SHOW;
  }
}

export class EmoteRitual {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.byName = new Map(); // 单例队列：一人一头同时最多一泡，新的顶旧的
    this.idleT = 0;          // 自发表情节拍
    // r04-scenemap v1.1 纪律（t_121 口径对齐）：idle 泡「宁稀勿滥」——
    // 间隔 45-120s 随机、概率再乘 0.3、同屏最多 2 个 idle 泡（超出丢弃
    // 不排队）；love 的防谄媚会话限次——同一成员每次页面会话至多自发
    // love 泡 1 次（react 触发不受限：那是别人的真心不是自夸）
    this.idleCount = () => this.idleBubbles;
    this.idleBubbles = 0;
    this.loveShown = new Set(); // 会话内已自发过 love 的成员
  }

  /** Pop an emote bubble on a member's head (unknown name is a no-op).
   *  isIdle marks the bubble as idle-sourced (counts toward the
   *  same-screen cap of 2). */
  emote(name, key, isIdle) {
    if (!EMOJIS[key]) return;
    const a = this.board.actors.get(name);
    if (!a) return;
    // 防谄媚（r04-scenemap v1.1）：自发路径（idle 池）里的 love 每次
    // 会话每成员至多 1 次——别人 react 的 love 不受限（onReact 直调）
    if (isIdle && key === 'love' && this.loveShown.has(name)) return;
    if (isIdle && key === 'love') this.loveShown.add(name);
    // 替换旧泡时若是 idle 泡先还计数
    const prev = this.byName.get(name);
    if (prev && prev.isIdle) this.idleBubbles = Math.max(0, this.idleBubbles - 1);
    if (isIdle) this.idleBubbles++;
    this.byName.set(name, new Bubble(key, !!isIdle));
    sfx('scene-pop'); // r_09：泡出现「啵」（全路：idle/事件/关键词；冷却 1.5s 兜密集泡）
  }

  /** The say-frame trigger (roomview.say calls this). */
  onSay(name, text) {
    const key = emoteFromText(text);
    if (key) this.emote(name, key);
  }

  /** The task-event triggers (roomview.frame calls this). */
  onTaskEvent(event, task) {
    if (!task) return;
    if (event === 'created' && task.assignee) this.emote(task.assignee, 'working');
    else if (event === 'updated' && task.status === 'done' && task.assignee) this.emote(task.assignee, 'done');
    else if (event === 'confirmed' && task.assignee) this.emote(task.assignee, 'point' in EMOJIS ? 'point' : 'working');
  }

  /** The react trigger: a positive react on someone's line pops love. */
  onReact(emoji, targetName) {
    if (EMOJI_TO_EMOTE.get(emoji) === 'love' || /❤|💗|🥰|👍|👏/.test(emoji)) {
      this.emote(targetName, 'love');
    }
  }

  // the tick: age the bubbles, and occasionally pop idle emotes on the
  // truly idle (same eligibility pool as the pantry's tryPantry)
  step(dt) {
    for (const [name, b] of this.byName) {
      b.t -= dt;
      if (b.t <= 0) {
        if (b.isIdle) this.idleBubbles = Math.max(0, this.idleBubbles - 1);
        this.byName.delete(name);
      }
    }
    this.idleT -= dt;
    if (this.idleT <= 0) {
      // r04-scenemap v1.1：间隔 45–120s 随机（宁稀勿滥），概率 ×0.3
      this.idleT = 45 + Math.random() * 75;
      const busy = this.board.busy;
      if (!busy) return;
      // 同屏 idle 泡上限 2（正在显示的泡里数 idle 来源的——用标记位）
      if (this.idleBubbles >= 2) return;
      const idle = [];
      for (const a of this.board.actors.values()) {
        if (!a.isLocal && !a.isNPC && a.meetIdx < 0 && a.breakIdx < 0 && !busy.has(a.name)) idle.push(a);
      }
      if (idle.length && Math.random() < 0.3) {
        const a = idle[(Math.random() * idle.length) | 0];
        // t_132：低落期成员抽低落池（社交引擎的 short-term 情绪标记）
        const low = this.board.social && this.board.social.lowMood &&
          (this.board.social.lowMood.get(a.name) || 0) > (this.board.clock || 0);
        const pool = low ? IDLE_LOW_POOL : IDLE_POOL;
        const key = pool[(Math.random() * pool.length) | 0];
        this.emote(a.name, key, true);
      }
    }
  }

  /** Paint every live bubble (the board calls this after the plates). */
  paint(ctx, list) {
    const g = this.board.art.geom;
    if (!g) return;
    const frame = Math.floor((this.board.clock || 0) / BEAT) % 2;
    for (const a of list) {
      const b = this.byName.get(a.name);
      if (!b) continue;
      this.paintOne(ctx, a, b.key, Math.min(1, b.t / 0.4), frame, g);
    }
  }

  /** 回放影子的表情泡（r_32.1）：影子自带 emote 键＋剩余秒——历史画
   *  面与实时同一张脸（键由泡文本确定性推出，见 replay.js shadowAt，
   *  不走 byName 的实时队列）。clockSec 是回放秒钟（摆帧同拍）。 */
  paintShadow(ctx, list, clockSec) {
    const g = this.board.art.geom;
    if (!g) return;
    const frame = Math.floor((clockSec || 0) / BEAT) % 2;
    for (const a of list) {
      if (!a.emote || !(a.emoteT > 0)) continue;
      this.paintOne(ctx, a, a.emote, Math.min(1, a.emoteT / 0.4), frame, g);
    }
  }

  // One bubble on one head: the pixel card + tail + icon（实时/回放共用）。
  paintOne(ctx, a, key, alpha, frame, g) {
    const icon = EMOJIS[key];
    if (!icon) return;
    // 锚点：名牌之上再高一层（与任务气泡错开——表情泡浮在头顶右上，
    // 不与锤子气泡/名牌抢同一竖线）
    const x = Math.round(a.x + 14);
    const y = Math.round(a.y - (g.avatarH || 48) - 26);
    ctx.save();
    ctx.globalAlpha = alpha;
    // 泡底：小圆角卡片（bubbleChat 底＋bubbleEdge 描边，分册总纲）
    ctx.fillStyle = '#fffdf5';
    ctx.strokeStyle = EDGE;
    ctx.lineWidth = 1.5;
    const r = 9;
    ctx.beginPath();
    ctx.roundRect ? ctx.roundRect(x - r, y - r, r * 2, r * 2, 4)
                  : ctx.rect(x - r, y - r, r * 2, r * 2);
    ctx.fill(); ctx.stroke();
    // 尾巴：指向头顶的小三角
    ctx.beginPath();
    ctx.moveTo(x - 2, y + r); ctx.lineTo(x + 2, y + r); ctx.lineTo(x, y + r + 3);
    ctx.closePath(); ctx.fillStyle = '#fffdf5'; ctx.fill();
    ctx.strokeStyle = EDGE; ctx.stroke();
    icon.draw(ctx, x, y, frame);
    ctx.restore();
  }
}

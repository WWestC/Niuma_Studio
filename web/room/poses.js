// poses.js — t_122 动作系统（r_04 表现力分册 §二 A1–A15）：Avatar 姿势
// 覆盖层。与 EmoteRitual 同哲学——纯表现、单例（一人同时一个动作）、
// 从不抛、失败静默；不动 atlas 帧（wire 零改动），在已绘 sprite 之上
// canvas 直绘「上半身姿势帧」：偏移/手臂色块/手持件，下半身由走路
// 状态决定（分册机制假设）。
//
// 接入：RoomView 每帧 paintActor 后调 paint(ctx, a)；pose(name, key,
// dur) 由触发源（social.js 机制步、energy.js 吃链、任务事件、idle 随
// 机）调用。坐姿动作（nod/shake/typing/lean）有桌面遮挡兜底，站姿动
// 作（bow/cheer/point/…）走齐不挑位置。

import { DIR } from './actor.js';

// 动作时长（秒）——每款 1.2–2.4s，比表情泡（2s 展示）略长半拍：动作
// 是身体的语言，节奏该比脸慢一点。
const DUR = {
  nod: 1.6, shake: 1.6, jump: 1.0, bow: 1.8, cheer: 2.0, facepalm: 2.0,
  stretch: 1.8, point: 1.6, lean: 3.0, typing: 2.4, lookaround: 3.2,
  carry: 0, // carry 不是限时动作——它跟着 holding 走，直到放下
  eat: 2.4, toss: 0.8, bend: 1.6,
};

// 动作库：每款一个 paint(ctx, a, t, env)——t 是已进行秒数，env 是
// {avatarH, clock}。画在 sprite 的本地原点（a.x, a.y ＝ 脚锚）上。
// 色彩从 actor 的 atlas 采样不现实（canvas 跨域＋性能），这里用成员
// 色板约定：skin/arm 用统一肤色（与 sprite 的 S 档一致走浅肤），袖口
// 用成员衬衫色（board 侧传入 memberShirt(name)）。
const SKIN = '#f0c8a0';
const SKIN_D = '#d9a878'; // 暗一档：手臂下缘

const POSES = {
  // A1 nod：头部下移 1px 两帧循环——头顶盖一块「发色块」上下颤
  nod: {
    headOnly: true,
    paint(ctx, a, t, env) {
      const dip = Math.floor(t * 4) % 2; // 4Hz 循环
      const y = headTopY(a, env) + dip;
      ctx.fillStyle = env.hair;
      ctx.fillRect(a.x - 4, y, 8, 2);
    },
  },
  // A2 shake：头部左右 1px 交替
  shake: {
    headOnly: true,
    paint(ctx, a, t, env) {
      const side = Math.floor(t * 5) % 2 ? 1 : -1;
      const y = headTopY(a, env);
      ctx.fillStyle = env.hair;
      ctx.fillRect(a.x - 4 + side, y, 8, 2);
    },
  },
  // A3 jump：整体上移 2px＋落地压扁——sprite 重绘走 board 侧的 poseLift，
  // 这里只画落地时脚下的两粒尘
  jump: {
    lift: 1, // board 对该动作整体上抬
    paint(ctx, a, t, env) {
      const land = t > 0.5; // 上半程悬空、下半程落地
      if (!land) return;
      ctx.fillStyle = 'rgba(120,110,100,.55)';
      ctx.fillRect(a.x - 7, a.y - 1, 3, 2);
      ctx.fillRect(a.x + 4, a.y - 1, 3, 2);
    },
  },
  // A4 bow：上身前倾——头前移＋一条前倾的背线
  bow: {
    paint(ctx, a, t, env) {
      const k = Math.sin(Math.min(1, t / 0.7) * Math.PI / 2); // ease in 持平
      const dx = Math.round(4 * k * (a.dir === DIR.LEFT ? -1 : 1));
      const y = headTopY(a, env) + Math.round(3 * k);
      ctx.fillStyle = env.hair;
      ctx.fillRect(a.x - 4 + dx, y, 8, 3);
      ctx.fillStyle = env.shirt;
      ctx.fillRect(a.x - 5 + dx, y + 3, 10, 4); // 前倾的肩背
    },
  },
  // A5 cheer：双臂上举（两侧 skin 色块，两帧交替高度）
  cheer: {
    paint(ctx, a, t, env) {
      const up = Math.floor(t * 3) % 2;
      const top = headTopY(a, env) - (up ? 3 : 1);
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x - 8, top, 3, 7);
      ctx.fillRect(a.x + 5, top, 3, 7);
      ctx.fillStyle = SKIN_D;
      ctx.fillRect(a.x - 8, top + 6, 3, 1);
      ctx.fillRect(a.x + 5, top + 6, 3, 1);
    },
  },
  // A6 facepalm：一只 skin 色手臂盖脸
  facepalm: {
    paint(ctx, a, t, env) {
      const y = headTopY(a, env) + 4; // 脸区
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x - 4, y, 8, 5);
      ctx.fillStyle = SKIN_D;
      ctx.fillRect(a.x - 4, y + 4, 8, 1);
    },
  },
  // A7 stretch：双臂上举＋身体 1px 拉长（配合 lift 半格）
  stretch: {
    lift: 0.5,
    paint(ctx, a, t, env) {
      const k = Math.sin((t / DUR.stretch) * Math.PI); // 中段最高
      const top = headTopY(a, env) - Math.round(4 * k);
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x - 8, top, 3, 6);
      ctx.fillRect(a.x + 5, top, 3, 6);
    },
  },
  // A8 point：一臂平伸指向（朝向侧）
  point: {
    paint(ctx, a, t, env) {
      const left = a.dir === DIR.LEFT;
      const x0 = left ? a.x - 12 : a.x + 4;
      const y = headTopY(a, env) + 7; // 肩高
      ctx.fillStyle = SKIN;
      ctx.fillRect(x0, y, 8, 3);
      ctx.fillStyle = env.shirt;
      ctx.fillRect(left ? a.x - 5 : a.x + 2, y - 1, 3, 4); // 袖口
    },
  },
  // A9 lean：上身左倾 1px＋手撑桌（长时 idle 看戏态）
  lean: {
    paint(ctx, a, t, env) {
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x - 9, headTopY(a, env) + 6, 3, 4); // 撑桌的手
      ctx.fillStyle = env.shirt;
      ctx.fillRect(a.x - 5, headTopY(a, env) + 3, 10, 4); // 微倾的肩
    },
  },
  // A10 typing：手部两帧交替（工位屏幕微闪由 working 光环管，这里只管手）
  typing: {
    paint(ctx, a, t, env) {
      const alt = Math.floor(t * 6) % 2;
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x - 4 + alt, headTopY(a, env) + 14, 2, 2);
      ctx.fillRect(a.x + 2 - alt, headTopY(a, env) + 14, 2, 2);
    },
  },
  // A11 lookaround：头部左右慢扫（1px 两帧，3s 周期）
  lookaround: {
    headOnly: true,
    paint(ctx, a, t, env) {
      const phase = Math.floor(t / 1.5) % 2;
      const side = phase ? 1 : -1;
      ctx.fillStyle = env.hair;
      ctx.fillRect(a.x - 4 + side, headTopY(a, env), 8, 2);
    },
  },
  // A13 eat（r_07）：手到嘴两帧＋咀嚼小点
  eat: {
    paint(ctx, a, t, env) {
      const chew = Math.floor(t * 4) % 2;
      const y = headTopY(a, env) + 5; // 嘴部
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x + 2, y + 2 - chew, 3, 3); // 举食的手
      ctx.fillStyle = env.food || '#e8b84c';
      ctx.fillRect(a.x + 2, y - chew, 3, 3); // 食物（t_136 资产到位前色块占位）
      if (chew) {
        ctx.fillStyle = 'rgba(120,100,70,.5)';
        ctx.fillRect(a.x - 1, y + 4, 2, 1); // 掉渣
      }
    },
  },
  // A14 toss（r_07）：抛物弧线一帧＋出手
  toss: {
    paint(ctx, a, t, env) {
      const k = t / DUR.toss;
      const x = a.x + Math.round(k * 10);
      const y = a.y - Math.round((1 - Math.pow(1 - k, 2)) * 14); // 弧线
      ctx.fillStyle = env.food || '#b0a890';
      ctx.fillRect(x - 1, y, 3, 3);
    },
  },
  // A15 bend（r_07）：弯腰拾物——头低＋一只手向下
  bend: {
    paint(ctx, a, t, env) {
      const k = Math.sin(Math.min(1, t / 0.8) * Math.PI / 2);
      const y = headTopY(a, env) + Math.round(6 * k);
      ctx.fillStyle = env.hair;
      ctx.fillRect(a.x - 4, y, 8, 3);
      ctx.fillStyle = SKIN;
      ctx.fillRect(a.x + 3, y + 3, 3, Math.round(6 * k) + 2); // 探下的手
    },
  },
};

// headTopY：头顶 y（native px）——sprite 顶 + 发顶一行。坐姿工位照用
//（坐站同高，分册坐姿由桌面遮挡兜底）。
function headTopY(a, env) {
  return Math.round(a.y - (env.avatarH || 48) + 2);
}

// idle 自发动作池（t_122 描述：空闲牛马随机小动作，间隔呼吸感）——
// 只收不移动的轻动作，重演出（cheer/bow）归事件触发。
const IDLE_POOL = ['nod', 'lookaround', 'stretch', 'typing', 'lean', 'shake'];

export class PoseRitual {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.byName = new Map(); // name → {key, t, dur}
    this.idleT = 0;
  }

  /** Play one pose on a member (unknown name/key is a no-op). dur 可省——
   * 用动作库默认时长；carry 等 0 时长动作由调用方自管生命周期。 */
  pose(name, key, dur) {
    if (!POSES[key]) return;
    const a = this.board.actors.get(name);
    if (!a) return;
    const d = dur ?? DUR[key] ?? 1.6;
    this.byName.set(name, { key, t: 0, dur: d });
  }

  /** 当前某成员的动作 lift（board 绘 sprite 时上抬量，native px）。 */
  liftOf(name) {
    const p = this.byName.get(name);
    return p ? poseLift(p.key) : 0;
  }

  // the tick: age poses, drop expired; idle 随机小动作（8s 节拍，仅真正
  // 空闲者——与 EmoteRitual 的 idle 泳池同资格链）
  step(dt) {
    for (const [name, p] of this.byName) {
      p.t += dt;
      if (p.dur > 0 && p.t >= p.dur) this.byName.delete(name);
    }
    this.idleT -= dt;
    if (this.idleT <= 0) {
      this.idleT = 10; // 比 idle 表情更慢的节拍——动作更稀有才不闹
      const busy = this.board.busy;
      if (!busy) return;
      const idle = [];
      for (const a of this.board.actors.values()) {
        if (!a.isLocal && !a.isNPC && a.meetIdx < 0 && a.breakIdx < 0 && !busy.has(a.name)) idle.push(a);
      }
      if (idle.length && Math.random() < 0.12) {
        const a = idle[(Math.random() * idle.length) | 0];
        this.pose(a.name, IDLE_POOL[(Math.random() * IDLE_POOL.length) | 0]);
      }
    }
  }

  /** Paint every live pose overlay (the board calls per actor, right
   * after the sprite — before the fore layer so desks occlude).
   * entry（r_32.2）：外部自带动作条目（{key, t}——回放影子从帧还原），
   * 缺省查实时队列。同一条像素路径，实时/历史同一张脸。 */
  paint(ctx, a, env, entry) {
    const p = entry || this.byName.get(a.name);
    if (!p) return;
    const def = POSES[p.key];
    if (!def) return;
    ctx.save();
    try {
      def.paint(ctx, a, p.t, env);
    } finally {
      ctx.restore();
    }
  }
}

// 动作时长表（r_32.2 导出：回放影子的起源扫描按它钳生命——carry=0
// 表示「跟着手持物走」的无限期动作，不钳）。
export const POSE_DUR = DUR;

// poseLift：某动作键的 sprite 抬升量（native px）——回放影子直调
// （liftOf 走实时 byName 队列，历史画面不该问现在）。
export function poseLift(key) {
  const def = POSES[key];
  if (!def || !def.lift) return 0;
  return def.lift * 4; // lift 档 → native px
}

export const POSE_KEYS = Object.keys(POSES);

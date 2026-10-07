// interact.js — t_123 交互系统（r_04 表现力分册 §三 I1–I12）：房主点击
// 办公室物件的反馈动画。与 PrinterRitual/EmoteRitual 同哲学——纯前端、
// 每件 ≤1.5s、零业务副作用（失败静默）、命中区一律走 geom 或与绘制
// 同源的常量（无 geom 的旧物件不收，分册纪律）。
//
// 命中区来源：printer 走 geom.printer（I1）；白板/饮水机/绿植/挂钟已
// 有 roomview 命中（I2 之外本册补动画反馈）；茶水间小件（咖啡机/微波
// 炉/垃圾桶/零食架/吧台凳）走本文件常量——与 pixart/room.go drawPantry
// 的 legacy 坐标同源（×2 native），art 与 hit 不漂移。

import { sfx } from './sfxport.js';

const S = 2; // legacy → native

// 茶水间小件的命中区（legacy 坐标，drawPantry 同源）：
//   coffee  咖啡机 (280,77) 11×7    microwave 微波炉 (306,76) 16×8
//   bin     垃圾桶 (244,101) 10×14  snack    零食架 (238,49) 26×36
//   stools  吧台凳×2 (280,108)/(318,108) 8×11
const SPOTS = {
  coffee: { x: 280 * S, y: 77 * S, w: 11 * S, h: 7 * S },
  microwave: { x: 306 * S, y: 76 * S, w: 16 * S, h: 8 * S },
  bin: { x: 244 * S, y: 101 * S, w: 10 * S, h: 14 * S },
  snack: { x: 238 * S, y: 49 * S, w: 26 * S, h: 36 * S },
  // 收藏品货架（t_188 荣誉墙改版）：北墙中柱落地玻璃展示柜——点击开
  // 成就清单页签（legacy 172,30 起 40×34，与 pixart drawShowcase 同源）
  honorwall: { x: 172 * S, y: 30 * S, w: 40 * S, h: 34 * S },
};

// 每件反馈的时长（秒）——分册纪律 ≤1.5s
const T = { printer: 1.2, plant: 1.0, coffee: 1.2, bin: 0.9, snack: 1.3, microwave: 1.5, floor: 0.9, desk: 1.0, member: 1.4 };

export class InteractRitual {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    // 活动画：kind → {t, x, y, i}（x/y 是 native 锚点，i 是复用索引如绿植/凳）
    this.live = new Map();
  }

  // ── 命中测试（canvas 坐标，native px）──────────────────────────

  hitPantry(x, y) {
    for (const k of ['coffee', 'microwave', 'bin', 'snack']) {
      const r = SPOTS[k];
      if (x >= r.x && x < r.x + r.w && y >= r.y && y < r.y + r.h) return k;
    }
    return null;
  }

  // 荣誉墙命中（t_188）：SPOTS.honorwall 区间
  hitHonorWall(x, y) {
    const r = SPOTS.honorwall;
    return x >= r.x && x < r.x + r.w && y >= r.y && y < r.y + r.h;
  }

  hitFloor(x, y) {
    // I12 地板彩蛋：地毯区（墙沿之下、茶水间/会议室 zone 之外）都算
    const g = this.board.art.geom;
    if (!g) return false;
    return y > g.wallH && !this.inPantry(x, y) && !this.inMeeting(x, y);
  }

  inPantry(x, y) {
    const g = this.board.art.geom;
    const pz = g && g.pantry;
    if (!pz) return false;
    const [px, py, pw, ph] = pz.rect;
    return x >= px && x < px + pw && y >= py && y < py + ph;
  }

  inMeeting(x, y) {
    const g = this.board.art.geom;
    const m = g && g.meet;
    if (!m) return false;
    const [rx, ry, rw, rh] = m.room;
    return x >= rx && x < rx + rw && y >= ry && y < ry + rh;
  }

  // ── 触发入口（roomview.onClick 调用；返回 true 表示已消费）────

  onPantry(x, y) {
    const k = this.hitPantry(x, y);
    if (!k) return false;
    const r = SPOTS[k];
    if (k === 'coffee') this.live.set('coffee', { t: 0, x: r.x, y: r.y });
    else if (k === 'microwave') this.live.set('microwave', { t: 0, x: r.x, y: r.y });
    else if (k === 'bin') this.live.set('bin', { t: 0, x: r.x, y: r.y });
    else if (k === 'snack') this.live.set('snack', { t: 0, x: r.x, y: r.y });
    return true;
  }

  onFloor(x, y) {
    if (!this.hitFloor(x, y)) return false;
    this.live.set('floor', { t: 0, x, y });
    return true;
  }

  onDesk(x, y) {
    // I11 摸工位：desk 命中（roomview 已有 deskAt 类逻辑则复用；这里
    // 自查 geom.desks——点中即给 doing 者的屏幕亮一下）
    const g = this.board.art.geom;
    if (!g) return false;
    for (let i = 0; i < g.desks.length; i++) {
      const d = g.desks[i];
      if (x >= d.x && x < d.x + d.w && y >= d.y && y < d.y + d.h) {
        this.live.set('desk', { t: 0, i });
        return true;
      }
    }
    return false;
  }

  // ── 节拍 ──────────────────────────────────────────────────────

  step(dt) {
    for (const [k, a] of this.live) {
      a.t += dt;
      if (a.t >= T[k]) this.live.delete(k);
    }
  }

  // ── 绘制（roomview 在前景层之后调用——反馈要盖在家具上）────────
  // live（r_32.2）：外部自带活动表（Map kind → {t,x,y,i}，回放从帧还原），
  // 缺省用实时队列；silent＝回放态——帧沿音效不响（历史不播音）。
  paint(ctx, live = this.live, silent = false) {
    const g = this.board.art.geom;
    if (!g) return;
    for (const [k, a] of live) {
      ctx.save();
      try {
        this['p_' + k](ctx, a, g, silent);
      } finally {
        ctx.restore();
      }
    }
  }

  // I1 摸打印机：LED 快闪三下＋托盘纸跳 1px（printer geom 全套）
  p_printer(ctx, a, g) {
    const p = g.printer;
    if (!p) return;
    const [lx, ly, lw, lh] = p.led;
    const flash = Math.floor(a.t * 9) % 2;
    ctx.fillStyle = flash ? '#3370ff' : '#7fb0d8';
    ctx.fillRect(lx, ly, lw, lh);
    const [tx, ty, tw, th] = p.tray;
    const hop = Math.floor(a.t * 6) % 2;
    ctx.fillStyle = '#fbfbf8';
    ctx.fillRect(tx, ty - hop, tw, Math.max(2, th / 2));
  }

  // I2 摸绿植（roomview 已有浇水；这里不重复——植物反馈沿用 t_105）
  // I3 摸饮水机（t_105 倒水已有）；I5 白板（可写已有）；I6 挂钟（表盘已有）

  // I4 摸咖啡机：蒸汽缕 3 帧＋「叮」色圈
  p_coffee(ctx, a) {
    const x = a.x + 5 * S, y = a.y; // 出水口上方
    const k = a.t / T.coffee;
    for (let i = 0; i < 3; i++) {
      const sy = y - ((k * 10 + i * 3) % 10) - 2;
      const sway = Math.sin((a.t * 4 + i * 2)) * 1.5;
      ctx.fillStyle = 'rgba(240,243,246,.75)';
      ctx.fillRect(Math.round(x + sway), Math.round(sy), 2, 3);
    }
    if (k > 0.8) { // 「叮」色圈（尾段一环）
      ctx.strokeStyle = 'rgba(52,199,36,.8)';
      ctx.lineWidth = 1;
      const r = 3 + (k - 0.8) * 20;
      ctx.beginPath();
      ctx.arc(x, y + 4, r, 0, Math.PI * 2);
      ctx.stroke();
    }
  }

  // I7 摸成员：名片＋挥手（roomview openPerson 已有通道，这里补挥手
  // 帧——调 board.poser 的 wave 类动作，pose 键用 cheer 轻量版 nod）
  // 实装在 onMember 里（见下）

  /** I7：摸成员的挥手动作（roomview 调 openPerson 前调这个）。 */
  onMember(name) {
    this.board.poser.pose(name, 'nod');
    this.board.emoter.emote(name, 'wave');
  }

  // I8 摸垃圾桶：盖子开合 2 帧＋「啪」灰尘 puff
  p_bin(ctx, a, g, silent) {
    const x = a.x, y = a.y;
    const open = Math.floor(a.t * 5) % 2;
    // r_09：盖子落下帧（open 1→0）响「啪」——帧沿检测（回放静音）
    if (!silent && a.lastBinOpen && !open) sfx('scene-binlap');
    a.lastBinOpen = open;
    // 盖子：翻盖绕后沿翻起（画一条抬起的盖）
    ctx.fillStyle = '#9aa4ad';
    ctx.fillRect(x, y - 2 - open * 2, 10 * S, 2);
    if (a.t > T.bin * 0.5) { // 「啪」puff：两粒灰尘
      ctx.fillStyle = 'rgba(154,164,173,.6)';
      ctx.fillRect(x + 1, y - 5, 2, 2);
      ctx.fillRect(x + 6 * S - 2, y - 6, 2, 2);
    }
  }

  // I9 摸零食架：架上袋子抖 2 帧＋掉一包（台面弹一下停住）
  p_snack(ctx, a) {
    const x = a.x, y = a.y;
    const jig = Math.floor(a.t * 6) % 2;
    // 抖动：上层三包整体 1px 错位（重画简化袋形——原绘制同位）
    ctx.fillStyle = '#ff8800';
    ctx.fillRect(x + 2 * S + jig, y + 1 * S, 5 * S, 4 * S);
    // 掉落：一包从下层落到台面，弹一下
    if (a.t > 0.3) {
      const k = Math.min(1, (a.t - 0.3) / 0.5);
      const bounce = k < 0.8 ? 0 : Math.sin((k - 0.8) * Math.PI * 3) * 3 * (1 - k);
      const dy = k * (30 * S) - bounce;
      ctx.fillStyle = markerRedHex;
      ctx.fillRect(x + 11 * S, y + 15 * S + dy, 5 * S, 4 * S);
    }
  }

  // I10 摸微波炉：门内亮暖光 1.5s＋「叮」
  p_microwave(ctx, a) {
    const x = a.x, y = a.y;
    const k = a.t / T.microwave;
    ctx.fillStyle = `rgba(247,163,49,${0.35 + 0.25 * Math.sin(a.t * 8)})`;
    ctx.fillRect(x + 1 * S, y + 1 * S, 9 * S, 6 * S); // 门视窗暖光
    if (k > 0.85) {
      ctx.strokeStyle = 'rgba(247,163,49,.9)';
      ctx.lineWidth = 1;
      const r = 3 + (k - 0.85) * 30;
      ctx.beginPath();
      ctx.arc(x + 8 * S, y + 4 * S, r, 0, Math.PI * 2);
      ctx.stroke();
    }
  }

  // I11 摸工位：屏幕亮起＋任务缩写（doing 者的——板上的 bright 状态
  // 由 working 光环管，这里只闪一下白）
  p_desk(ctx, a, g) {
    const d = g.desks[a.i];
    if (!d) return;
    const k = a.t / T.desk;
    const [cx, cy, cw, ch] = d.chair[2] ? d.chair : [d.x, d.y, d.w, d.h / 2];
    ctx.fillStyle = `rgba(255,255,255,${0.4 * (1 - k)})`;
    ctx.fillRect(cx, cy, cw, Math.max(2, ch / 2));
  }

  // I12 摸地板：脚印粒子 2 枚渐隐
  p_floor(ctx, a) {
    const k = a.t / T.floor;
    ctx.fillStyle = `rgba(90,100,110,${0.5 * (1 - k)})`;
    ctx.fillRect(a.x - 3, a.y - 1, 2, 3);
    ctx.fillRect(a.x + 2, a.y + 2, 2, 3);
  }
}

const markerRedHex = '#f54a45';

// 导出时长表（roomview 的 hover/点击消费用；r_32.2 回放还原也按它钳生命）
export const INTERACT_SPOTS = SPOTS;
export const INTERACT_T = T;

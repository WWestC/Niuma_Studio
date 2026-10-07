// actor.js — one little person: the sprite's own state machine and
// animation pick. The Actor owns where it is, where it is going, why
// (working desk / idle wander / the owner's key walk) and which atlas
// frame that implies; the board (roomview.js) only feeds it input and
// bounds each tick and paints whatever frame it reports.

import { FRAME } from './art.js';
import { clamp, rand } from './util.js';

// sim constants (world.go's, native px — the ×S rescale is baked into
// the art; speeds stay per-second logical)
export const WALK = 26;         // NPC px/s
export const PLAYER_SPEED = 62; // the owner, px/s
export const ANIM_FPS = 6;
export const SPEAK_BEAT = 0.5, SPEAK_HOP_DUR = 0.3, SPEAK_HOP_PX = 3;

export const DIR = { DOWN: 0, UP: 1, RIGHT: 2, LEFT: 3 };

// crowd constants: AVOID_R is how far ahead a body yields to another,
// YIELD_R where it slows to let them pass, and STUCK_T how long of no
// progress buys a re-route (nav.js's grid knows furniture, not people,
// so people are dodged here and the route refreshed when jammed).
const AVOID_R = 34, YIELD_R = 18, AVOID_K = 1.6, STUCK_T = 0.8; // t_162：对撞快解窗 1.5→1.2→0.8
// t_164：避让常数导出（对撞模拟测试要按半径构造场景）
export const CROWD = { AVOID_R, YIELD_R, AVOID_K, STUCK_T };

// yieldHash：对向让路的确定性优先级（同名恒值——同一对子永远同判）
export function yieldHash(name) {
  let h = 2166136261;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}
// 键盘借路：直行顶住 KEY_STUCK_S 秒没进展才算顶住（被人蹭一下不
// 算），KEY_CD 是两次借路的最小间隔，KEY_LOOK 是目标点取在按键方
// 向前方多远（nav.path 会把落在家具上的目标松弛到最近空格）
const KEY_STUCK_S = 0.12, KEY_CD = 0.35, KEY_LOOK = 56;

/** The dominant cardinal of a motion vector — the facing pick. */
export function dominantDir(dx, dy) {
  if (Math.abs(dx) > Math.abs(dy)) return dx > 0 ? DIR.RIGHT : DIR.LEFT;
  return dy > 0 ? DIR.DOWN : DIR.UP;
}

/** The speaking hop's height at remain seconds into the beat (0 off-beat). */
export function speakHop(remain) {
  const e = SPEAK_BEAT - remain;
  if (e < 0 || e >= SPEAK_HOP_DUR) return 0;
  const t = e / SPEAK_HOP_DUR;
  return SPEAK_HOP_PX * (1 - Math.abs(2 * t - 1));
}

export class Actor {
  /** @param {import('../chat/rooms.js').Member} m the roster member @param {number} x @param {number} y @param {HTMLImageElement} atlas */
  constructor(m, x, y, atlas) {
    this.name = m.name; this.color = m.color || '#7fb0d8'; this.hair = m.hair || '#4a3b2a';
    this.role = m.role || '';
    this.atlas = atlas;
    this.x = x; this.y = y;
    this.dir = DIR.DOWN; this.moving = false; this.animT = 0;
    this.blinkIn = rand(1, 5); this.blinkFor = 0;
    this.idleT = rand(0, 2); this.tx = 0; this.ty = 0; this.hasTarget = false;
    this.working = false; this.deskIdx = -1; this.lastDesk = -1;
    this.workFace = DIR.UP; // the facing a seated state holds (geom-driven)
    this.bubble = null;
    this.isLocal = false;
    this.spawnT = 0.35;
    this.speakT = 0;
    this.rank = null; this.grace = false; this.label = '';
    // 需求评审会议：meetIdx ≥ 0 = 该小牛马属于会议室的某个站位；
    // meetLeg 是进门的两段路（先到门口，再入座），到达即面向桌子站定
    this.meetIdx = -1;
    this.meetLeg = null; // {x, y} the second leg after the door waypoint
    // 茶水间咖啡歇（t_103）：breakIdx ≥ 0 = 正在茶水间某个站位小歇，
    // breakUntil 是散场时刻（roomview 的钟）；到达即面向家具站定
    this.breakIdx = -1;
    this.breakUntil = 0;
    // 打印仪式（r_03）：holding = 手持打印材料的种类（printer.js 的
    // SHEET_KIND，null 无物）；sheetPopT 是送达后头顶小泡的余命
    this.holding = null;
    this.sheetPopT = 0;
    // t_162 对向让路：yieldHold>0 = 低优先级方停走中（秒）
    this.yieldHold = 0;
    // t_163 错峰退避：deferT>0 = 卡死重算发现同路头，退避中（秒）
    this.deferT = 0;
    // t_104：doneT > 0 = 任务刚完成——头顶绿 ✓ 的余命（2s，roomview
    // 每帧递减；任务气泡 label 为空时 ✓ 单独亮）
    this.doneT = 0;
    // 摸鱼团（r_06/t_131）：socialGroup ≠ null = 正在某场摸鱼社交里
    //（社交引擎持有；派活/叫会时引擎负责清场）
    this.socialGroup = null;
    // 觅食链（r_07/t_137）：eating ≠ null = 正在 取食→吃→丢 三段中
    //（{phase: walk|eat|toss, t}；energy.js 持有推进）
    this.eating = null;
    // 外勤占用声明（errand.js 一口径）：fetching = 打印取材在途
    //（printer.js 持有）；drinking ≠ null = 接水在途（water.js 持有）
    this.fetching = false;
    this.drinking = null;
    // 绕障走路：path 是 nav.js 给的路标链，pathGoal 记它是为哪个目标
    // 算的（目标一变就重算），stuckT 累计原地踏步、攒够就重寻路
    this.path = null;
    this.pathGoal = '';
    this.stuckT = 0;
    this.seenDist = Infinity;
    // 键盘借路：直行顶住家具时朝按键方向前方借的 nav.path——和寻
    // 路走位的 this.path 互不相干，keyDir/keyCd 管它的失效与重借
    this.keyPath = null;
    this.keyDir = '';
    this.keyStuck = 0;
    this.keyCd = 0;
  }

  /**
   * One sim tick: timers, then the motion state machine (owner keys →
   * working stillness → target walk → idle wander), then the bounds
   * clamp. mvx/mvy is the owner's key vector (-1/0/1) — only the local
   * actor reads it, and it slides along furniture (nav) instead of
   * crossing it. avoid (optional rect {x,y,w,h}) keeps idle wander out
   * of the meeting room's box; nav is the walk grid (null until the
   * art loads — then walks go straight, the old way); others is the
   * crowd this body yields to.
   * @param {number} dt @param {{mvx:number, mvy:number, bounds:object, avoid?:object, nav?:object, others?:object[]}} input
   */
  step(dt, { mvx, mvy, bounds, avoid, nav, others }) {
    this.spawnT = Math.max(0, this.spawnT - dt);
    this.speakT = Math.max(0, this.speakT - dt);
    if (this.blinkFor > 0) this.blinkFor -= dt;
    else {
      this.blinkIn -= dt;
      if (this.blinkIn <= 0) { this.blinkFor = 0.16; this.blinkIn = rand(2, 6); }
    }

    if (this.isLocal && (mvx || mvy)) {
      // 键盘走位：全向量撞家具就分轴滑（贴着桌子边缘蹭过去）。顶住
      // 与否看位移在按键方向上的分量——斜键贴墙滑还有一半进展，不
      // 算顶住；真顶住了就朝按键方向前方借一条寻路绕过去，走的就
      // 是工位/会议室走位那张网格，直行一恢复顺畅立刻丢掉借路回到
      // 直控。键盘是主人的手：任何坐定态（评审借座）一并打断，松
      // 手后由落座逻辑决定是否请回。
      this.hasTarget = false;
      this.path = null;
      this.working = false;
      const len = Math.hypot(mvx, mvy);
      const ux = mvx / len, uy = mvy / len;
      this.dir = dominantDir(mvx, mvy);
      const stepLen = PLAYER_SPEED * dt;
      const dx = ux * stepLen, dy = uy * stepLen;
      const okX = !nav || nav.free(this.x + dx, this.y);
      const okY = !nav || nav.free(this.x, this.y + dy);
      const prog = (okX ? dx : 0) * ux + (okY ? dy : 0) * uy;
      if (!nav || prog >= stepLen * 0.5) {
        if (okX) this.x += dx;
        if (okY) this.y += dy;
        this.moving = true;
        this.keyStuck = 0; this.keyPath = null; this.keyCd = 0;
      } else {
        const dirKey = (ux > 0.5 ? 'r' : ux < -0.5 ? 'l' : '') +
          (uy > 0.5 ? 'd' : uy < -0.5 ? 'u' : '');
        if (dirKey !== this.keyDir) {
          // 按键方向变了，旧路是为别的方向借的，作废重借
          this.keyDir = dirKey; this.keyPath = null; this.keyCd = 0;
        }
        if (this.keyPath && this.keyPath.length) {
          this.moving = this.followKeyPath(dt, nav);
        } else {
          // 还没借到路：照旧分轴滑（窄缝里蹭），顶住攒够才借路
          if (okX) this.x += dx;
          if (okY) this.y += dy;
          this.moving = okX || okY;
          this.keyStuck += dt;
          if (this.keyStuck > KEY_STUCK_S) {
            this.keyCd -= dt;
            if (this.keyCd <= 0) {
              this.keyCd = KEY_CD;
              const tx = clamp(this.x + ux * KEY_LOOK, bounds.minX, bounds.maxX);
              const ty = clamp(this.y + uy * KEY_LOOK, bounds.minY, bounds.maxY);
              this.keyPath = nav.path(this.x, this.y, tx, ty);
              if (this.keyPath.length) this.moving = this.followKeyPath(dt, nav);
            }
          }
        }
      }
    } else if (this.working) {
      this.moving = false; this.dir = this.workFace;
    } else if (this.yieldHold > 0) {
      // t_162 对向让路停走：yieldHold 窗内原地不动（不触发卡死重算
      //——moving=false 且 stuckT 不积累：这不是卡是礼让）
      this.yieldHold = Math.max(0, this.yieldHold - dt);
      this.moving = false;
      if (this.yieldHold === 0) this._resumedFromYield = true; // 让完恢复：清卡死账
    } else if (this.hasTarget) {
      // 懒寻路：目标变了、或上一条路算完还没走到就卡住了，都重算
      const goalKey = `${Math.round(this.tx)},${Math.round(this.ty)}`;
      if (nav && (!this.path || this.pathGoal !== goalKey)) {
        this.path = nav.path(this.x, this.y, this.tx, this.ty);
        this.pathGoal = goalKey;
        this.stuckT = 0;
        this.seenDist = Infinity;
      }
      // 卡死检测：快一秒半原地没挪窝（多半是被人堵在窄道里），当场
      // 重算路线——不能置空留出无护栏的一帧，那一帧就是爬进桌子的缝
      if (this._resumedFromYield) { this._resumedFromYield = false; this.stuckT = 0; this.seenDist = Infinity; }
      const goalDist = Math.hypot(this.tx - this.x, this.ty - this.y);
      if (goalDist > 12) {
        if (goalDist > this.seenDist - 0.5) this.stuckT += dt;
        else this.stuckT = 0;
        this.seenDist = goalDist;
        if (this.stuckT > STUCK_T && nav) {
          const fresh = nav.path(this.x, this.y, this.tx, this.ty);
          // t_163 错峰：新路径与旧路径前三步重合（还是同一条窄道）——
          // 立刻重走必再撞同一人。随机退避 0.5-1s 再重算（对面那位此刻
          // 已挪开/另一条路已空），退避中 moving=false 不再积累 stuckT
          //（这不是卡是让路错峰）。
          const sameHead = (p, q) => p && q && p.length >= 3 && q.length >= 3 &&
            Math.hypot(p[0].x - q[0].x, p[0].y - q[0].y) < 4 &&
            Math.hypot(p[1].x - q[1].x, p[1].y - q[1].y) < 4;
          if (fresh && fresh.length && sameHead(fresh, this.path)) {
            this.deferT = 0.5 + Math.random() * 0.5;
            this.path = fresh; // 先收着——deferT 归零后接续走
          } else {
            this.path = fresh;
          }
          this.pathGoal = goalKey;
          this.stuckT = 0;
          this.seenDist = Infinity;
        }
      }
      // t_163 错峰退避：到期前不走（连 waypoint 消耗也停——存着的路
      // 一帧都别吃，退避完从原样接续）
      if (this.deferT > 0) {
        this.deferT = Math.max(0, this.deferT - dt);
        this.moving = false;
        this.animT = 0;
      } else {
      // 路标接力：2px 内的路标直接吞掉（切角留太大泳进禁行带），走空
      // 了就直奔终点（最后几 px 的「到桌入座」小跳是合法的，锚点本来
      // 就在桌沿上）
      let wp = null;
      while (this.path && this.path.length) {
        wp = this.path[0];
        if (Math.hypot(wp.x - this.x, wp.y - this.y) < 2) { this.path.shift(); wp = null; continue; }
        break;
      }
      const gx = wp ? wp.x : this.tx, gy = wp ? wp.y : this.ty;
      const dx = gx - this.x, dy = gy - this.y;
      const dist = Math.hypot(dx, dy);
      if (dist < 2 && !wp) {
        this.hasTarget = false; this.moving = false;
        // 会议站位只在走完两段路（门口→站位）后才站定；第一段到达
        // 交给 roomview 的 meetLeg 链接着走。茶水间歇脚位同理：到了
        // 就面向家具站定（working 当「锚」，人群绕行不推挤）。
        // 外勤（errand 根治）同权：吃/取材/接水到位即锚定等链推进
        //——否则闲逛计时器一到期就把等纸的人从机器前拉走
        if (this.deskIdx !== -1 || (this.meetIdx >= 0 && !this.meetLeg) || this.breakIdx >= 0 ||
            this.eating || this.fetching || this.drinking) {
          this.working = true; this.dir = this.workFace;
        } else this.idleT = rand(1.5, 5);
      } else {
        let vx = dx / (dist || 1), vy = dy / (dist || 1);
        const av = this.yieldTo(vx, vy, others, nav);
        let mx = vx + av.x, my = vy + av.y;
        const ml = Math.hypot(mx, my) || 1;
        mx /= ml; my /= ml;
        const sp = WALK * av.slow * dt;
        let nx = this.x + mx * sp, ny = this.y + my * sp;
        // 路标段护栏：避让分量把人抵进家具时退回纯航向，再分轴滑，
        // 绝不踏入桌子。最后一段（!wp，入座小跳/沿桌滑入）豁免——坐
        // 锚本来就在桌沿上。人已被挤进禁行格时朝最近的空格走出去
        // （定向逃离，不穿桌直奔路标）
        if (nav && wp) {
          if (!nav.free(this.x, this.y)) {
            const esc = nav.escape(this.x, this.y);
            const ex = esc.x - this.x, ey = esc.y - this.y;
            const el = Math.hypot(ex, ey) || 1;
            nx = this.x + (ex / el) * sp;
            ny = this.y + (ey / el) * sp;
          } else if (!nav.free(nx, ny)) {
            nx = this.x + vx * sp; ny = this.y + vy * sp;
            if (!nav.free(nx, ny)) {
              const ox = nav.free(this.x + vx * sp, this.y);
              const oy = nav.free(this.x, this.y + vy * sp);
              nx = ox ? this.x + vx * sp : this.x;
              ny = oy ? this.y + vy * sp : this.y;
            }
          }
        }
        this.moving = nx !== this.x || ny !== this.y;
        this.x = nx; this.y = ny;
        this.dir = dominantDir(mx, my);
      }
      } // deferT else 合口（t_163）
    } else {
      this.moving = false;
      this.idleT -= dt;
      // t_139 补钉：NPC 不吃闲逛——她的位移由自己的 Ritual 全权驱动
      //（cleaner.js 的 arrive），这里再派闲逛目标就是两个方向盘打架
      if (this.idleT <= 0 && !this.isLocal && !this.isNPC && this.deskIdx === -1 && this.meetIdx < 0 && !this.socialGroup) {
        let tx = 0, ty = 0, ok = false;
        for (let i = 0; i < 8 && !ok; i++) { // 闲逛别撞进会议室的玻璃盒子，也别把落点押在家具上
          tx = clamp(this.x + rand(-70, 70), bounds.minX, bounds.maxX);
          ty = clamp(this.y + rand(-45, 45), bounds.minY, bounds.maxY);
          ok = !(avoid && tx >= avoid.x && tx < avoid.x + avoid.w &&
            ty >= avoid.y && ty < avoid.y + avoid.h) &&
            (!nav || nav.free(tx, ty));
        }
        // 八次都没抽中空地（被挤在角落时会发生）就再等一轮，绝不把
        // 禁行格当作目的地
        if (ok) { this.tx = tx; this.ty = ty; this.hasTarget = true; }
        else this.idleT = rand(0.5, 1.5);
      }
    }
    if (this.moving) this.animT += dt; else this.animT = 0;
    this.x = clamp(this.x, bounds.minX, bounds.maxX);
    this.y = clamp(this.y, bounds.minY, bounds.maxY);
    if (this.bubble && (this.bubble.ttl -= dt) <= 0) this.bubble = null;
  }

  /**
   * 键盘借路的路标接力：吞掉走近了的路标、朝下一个走一步（键盘速
   * 度）。路标本就铺在自由格上，人被人群挤偏时分轴滑回来，绝不踩
   * 进家具；一条路走完就地清空，下一帧直行还顶住就再借新的。
   * @returns {boolean} did this frame actually move
   */
  followKeyPath(dt, nav) {
    while (this.keyPath.length &&
      Math.hypot(this.keyPath[0].x - this.x, this.keyPath[0].y - this.y) < 3) {
      this.keyPath.shift();
    }
    const wp = this.keyPath[0];
    if (!wp) { this.keyPath = null; return false; }
    const dx = wp.x - this.x, dy = wp.y - this.y;
    const d = Math.hypot(dx, dy) || 1;
    const sp = Math.min(PLAYER_SPEED * dt, d);
    let nx = this.x + (dx / d) * sp, ny = this.y + (dy / d) * sp;
    if (!nav.free(nx, ny)) {
      if (nav.free(nx, this.y)) ny = this.y;
      else if (nav.free(this.x, ny)) nx = this.x;
      else { this.keyPath = null; return false; } // 被挤进死角，弃路重借
    }
    this.x = nx; this.y = ny;
    return true;
  }

  /**
   * 遇人绕行：把落在行进正前方的他人往侧向让开一个分量——越近、越
   * 迎面越强；贴得够近时减速让行。侧向取「自己已经在偏的那一边」，
   * 完全对头时两人天然各让一侧；那一侧若顶着家具（过道贴桌沿的形
   * 态），探一脚步点、被挡就翻到另一侧——翻不动（两侧都堵）才放弃
   * 侧推只减速。坐定的人（working 锚）不会挪窝，绕行分量照常给、
   * 贴得极近时再压一手减速。真正的硬碰撞（重叠）由 roomview 的分离
   * 推挤兜底，这里只负责提前让路。
   */
  yieldTo(vx, vy, others, nav) {
    if (!others || !others.length) return { x: 0, y: 0, slow: 1 };
    let ax = 0, ay = 0, slow = 1;
    for (const o of others) {
      if (o === this) continue;
      const rx = o.x - this.x, ry = o.y - this.y;
      const d = Math.hypot(rx, ry);
      if (d > AVOID_R || d < 0.001) continue;
      const fx = rx / d, fy = ry / d;
      const ahead = fx * vx + fy * vy;
      // t_162 对向解僵：对头相遇（航向点积<-0.5 且已进减速圈）时按
      // 名字 hash 定优先级——低者原地停 0.5s（slow=0、不加侧推——
      // 侧推在对向时互相对消是僵局根因），高者正常走。确定性规则：
      // 同一对子永远同判，杜绝「同时让/同时抢」的振荡。
      if (ahead < -0.5 && d < YIELD_R) {
        if (yieldHash(this.name) < yieldHash(o.name)) {
          this.yieldHold = 0.5; // 我是低者——让路停走
          return { x: 0, y: 0, slow: 0 };
        }
        continue; // 我是高者——对向者交给他的让路，我不加侧推（防对消）
      }
      if (ahead < 0.05) continue; // 在身后或纯横向——交给分离推挤
      const w = (1 - d / AVOID_R) * (0.35 + 0.65 * ahead);
      const px = -fy, py = fx;
      let side = px * vx + py * vy >= 0 ? 1 : -1;
      // 绕行方向探边：侧推把人抵进家具就等于没让（护栏一帧就把它
      // 退回纯航向，直穿对方）。步点取前方 10px 的绕行落点，被挡翻
      // 边；两侧都堵（窄缝）才认命不加侧推。
      if (nav && !nav.free(this.x + px * side * 10, this.y + py * side * 10)) side = -side;
      if (nav && !nav.free(this.x + px * side * 10, this.y + py * side * 10)) continue;
      ax += px * side * w;
      ay += py * side * w;
      if (d < YIELD_R) slow = Math.min(slow, 0.4 + 0.6 * (d / YIELD_R));
    }
    return { x: ax * AVOID_K, y: ay * AVOID_K, slow };
  }

  /** Which atlas frame the current state paints (art.js's FRAME order). */
  frameIndex() {
    const blink = this.blinkFor > 0;
    const second = Math.floor(this.animT * ANIM_FPS) % 2 === 1;
    const d = this.dir;
    if (!this.moving) {
      if (d === DIR.UP) return FRAME.upA;
      if (d === DIR.RIGHT) return blink ? FRAME.downBlink : FRAME.rightA; // right-blink shares the down face's cue
      if (d === DIR.LEFT) return FRAME.leftA;
      return blink ? FRAME.downBlink : FRAME.downA;
    }
    if (d === DIR.UP) return second ? FRAME.upB : FRAME.upA;
    if (d === DIR.RIGHT) return second ? FRAME.rightB : FRAME.rightA;
    if (d === DIR.LEFT) return second ? FRAME.leftB : FRAME.leftA;
    return second ? FRAME.downB : FRAME.downA;
  }
}

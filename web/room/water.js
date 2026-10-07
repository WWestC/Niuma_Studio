// water.js — 接水外勤（t_105 氛围行为的根治版）。
//
// 旧实现（tryPantry 内联）：挑「饮水机 240px 内」的闲人原地说
// 「接杯水～」，机器那头自己放水——240px 半径覆盖近半屋，人不动
// 水自流，读出来就是隔空取水。根治口径：接水是一条真外勤——
// 只挑真的在机器跟前的（PICK_R），走去机前锚点、到位才放水出声
// 说台词，演完散场。占用声明挂 a.drinking（errand.js 一口径），
// 派活/叫会/别的外勤照旧立即散（零残留）。房主点击面（pourWater）
// 不在此列——那是主人的手，不占成员。
//
// 纯氛围：不进协议、不落盘，音效走 sfxport（未绑定即静默）。

import { sfx } from './sfxport.js';
import { t } from '../ui/i18n.js';
import { DIR } from './actor.js';
import { claimed, dispatchable, sendOnErrand, leaveDesk } from './errand.js';

const PICK_R = 90;        // 只挑真在机器跟前的（路过/近旁——240 半屋全含是隔空根因）
const WALK_DEADLINE = 12; // 走位死线：被堵就散（水不占库存，零损散场）
const POUR = 1.2;         // 倒水动画时长（与 roomview.pourT 同长同源）

export class WaterRitual {
  /** @param {import('./roomview.js').RoomView} board */
  constructor(board) {
    this.board = board;
    this.active = new Set(); // 接水在途名单（一人一杯，同时最多一人占机）
  }

  /** 机前站位：机身南侧走道（与 social.js 的 dispenser 锚点同式——两处同源）。 */
  anchor() {
    const it = this.board.art.geom && this.board.art.geom.interact;
    if (!it || !it.dispenser) return null;
    const [dx, dy, , dh] = it.dispenser;
    return { x: dx + 10, y: dy + dh + 6 };
  }

  /** 每拍（tryPantry 节拍捎带）：机器跟前挑一位真闲人发起接水。 */
  beat() {
    // 陈名清场（成员离房/换房后 active 不留残影——残影会永远占住机器）
    for (const n of this.active) if (!this.board.actors.has(n)) this.active.delete(n);
    if (this.active.size) return; // 已有人占机
    if ((this.board.pourT || 0) > 0) return; // 正在出水（含房主点击面）
    const an = this.anchor();
    if (!an) return;
    const busy = this.board.busy;
    if (!busy) return; // 名册未就绪不派（errand.js 口径）
    const cands = [];
    for (const a of this.board.actors.values()) {
      if (!dispatchable(a, busy)) continue;
      if (Math.hypot(a.x - an.x, a.y - an.y) > PICK_R) continue;
      cands.push(a);
    }
    if (!cands.length) return;
    const a = cands[(Math.random() * cands.length) | 0];
    leaveDesk(a, this.board);
    sendOnErrand(a, an.x, an.y, DIR.UP); // 面北朝机器
    a.drinking = { phase: 'walk', t: 0, born: this.board.clock || 0 };
    this.active.add(a.name);
  }

  /** 吃链式推进（board.update 每帧对每位成员调，非成员态是零开销早退）。 */
  step(a, dt) {
    const st = a.drinking;
    if (!st) return;
    const board = this.board;
    // 中断纪律：只认外部占用（own='drinking'）；散场摘锚，路权归接管者
    if (claimed(a, board.busy, 'drinking')) {
      a.drinking = null;
      a.working = false;
      this.active.delete(a.name);
      return;
    }
    st.t += dt;
    if (st.phase === 'walk') {
      if ((board.clock || 0) - st.born > WALK_DEADLINE) { this.finish(a); return; }
      if (!a.hasTarget && !a.moving) { // 到机器前：出水
        st.phase = 'pour';
        st.t = 0;
        a.workFace = DIR.UP; // 到位锚定后朝北对机（working 锚的朝向真源）
        board.pourT = POUR;
        sfx('scene-pour');
        board.say(a.name, t('接杯水～'), Math.floor(Date.now() / 1000), false, false, false);
      }
    } else if (st.phase === 'pour' && st.t >= POUR) {
      this.finish(a); // 接完就走（idle 闲逛/落座自然接管）
    }
  }

  finish(a) {
    a.drinking = null;
    a.working = false;
    a.hasTarget = false;
    a.idleT = 0.5 + Math.random() * 1.5;
    this.active.delete(a.name);
  }

  /** 房切换（setRoom）：演员全换，在途名单清空。 */
  reset() { this.active.clear(); }
}

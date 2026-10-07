// errand.js — 演员占用的一口径真源（氛围外勤根治）。
//
// 病根是同族的三案：办公室每条氛围外勤（吃零食/打印取材/接水）和
// 每个调度器（落座 applyBusy/咖啡歇 tryPantry/觅食 forageBeat/摸鱼
// eligible/取材认领）各自手搓一份「这人可不可派、被没被占」的判定，
// 互相不可见——漏一个标记就是一次互踩，实录三案：
//   · 吃链把自己手持的 'food' 道具读成「别人的取材在途」，开吃下一
//     帧自弃（energy.stepEat 旧守卫）；
//   · 打印仪式等取材人走到机器前，但从没有人给他下发过走位目标
//     （printer.print 只入队，take 阶段等一个永不会来的人）；
//   · 落座轮询把外勤中人的目标改写成回工位/新座位，走位半路被踩。
// 根治口径：占用声明进演员的三个外勤态（eating/fetching/drinking），
// 判定只此一处、处处引用——手持件是道具不是占用（'food' 只属于吃
// 链自己）；名册未就绪一律保守不派。
//
// 纯函数模块：不持有状态、不碰 DOM，测试直调。

import { DIR } from './actor.js';

/** 三条氛围外勤在演员身上的占用态字段（声明面，各自模块推进）。 */
export const ERRANDS = ['eating', 'fetching', 'drinking'];

/** onErrand：演员正身处某条氛围外勤（调度器豁免面）。 */
export function onErrand(a) {
  return !!(a && (a.eating || a.fetching || a.drinking));
}

/**
 * claimed：演员是否被「own 之外」的任何占有者占住。外勤链自己的推进
 * 循环用它做中断判定——own 传自己那态，防止把自己的占用读成别人的
 * 打断（吃链一帧自弃的旧疤）。holding 是道具面：只有吃链自己（own=
 * 'eating'）手里的 'food' 不算占用，任何纸张在手都是「手满不可派」。
 * 名册（busy）未就绪按「被占」处理——保守不派人，宁可无戏不可抢人。
 */
export function claimed(a, busy, own) {
  if (!a) return true;
  if (a.meetIdx >= 0 || a.breakIdx >= 0 || a.socialGroup) return true;
  if (a.holding && !(own === 'eating' && a.holding === 'food')) return true;
  for (const k of ERRANDS) if (k !== own && a[k]) return true;
  if (!busy) return true;
  return busy.has(a.name);
}

/** dispatchable：调度器可否把一条新外勤派给该演员（口径同上）。 */
export function dispatchable(a, busy) {
  return !!(a && !a.isLocal && !a.isNPC && !claimed(a, busy, null));
}

/**
 * sendOnErrand：外勤链下发走位的唯一通道。working 锚必须摘——
 * actor.step 里 working 优先于 hasTarget，坐着的人带着目标也不动
 * （吃链旧疤：派了目标人在工位纹丝不动）；face 是到位后站定的朝向。
 */
export function sendOnErrand(a, x, y, face) {
  if (!a) return;
  a.working = false;
  a.workFace = face === undefined ? DIR.UP : face;
  a.tx = x; a.ty = y; a.hasTarget = true;
}

/**
 * leaveDesk：外勤出发时暂离工位（咖啡歇同款语义）——release 保 30s
 * 回座缓冲，lastDesk 记旧座，散场后 idle 落座多半坐回原位。
 */
export function leaveDesk(a, board) {
  if (!a || a.deskIdx < 0) return;
  if (board && board.desks) board.desks.release(a.deskIdx, board.clock || 0);
  a.lastDesk = a.deskIdx;
  a.deskIdx = -1;
}

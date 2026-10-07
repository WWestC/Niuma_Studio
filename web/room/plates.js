// plates.js — the little people's 标签 (nameplates): one clean line
// floating over every actor's head — the name only (+宽限 ellipsis) —
// with the color bound to rank: the host wears gold, Lv.1..Lv.9 climb
// the game rarity ladder (白→绿→蓝→紫→粉→橙→红), unknown rank sits
// neutral. No backing box — the dark outline carries legibility — and
// the whole line sits clear above the hair. The plate rides the
// speaking hop so it stays glued to the head; bubbles (bubbles.js)
// clear PLATE_BAND above the same anchor. The board passes view width
// for the edge clamp and avatar height for the head anchor.
//
// t_104（r_01 §二·A4）：干活中的成员名字上方再浮一枚任务气泡——
// 「🔨 t_NN」式任务号缩写（锤子走 16 网格描线路径，同摄像机小标的
// 语言，不用 emoji）；任务转 done 后气泡换成绿 ✓，2 秒后消散。气泡
// 骑与名牌同一跳跃锚，与 PLATE_BAND 一起抬高 speech 气泡的底.

import { clamp } from './util.js';
import { speakHop } from './actor.js';
import { rankColor } from '../ui/person.js';

const NAME_LINE = 18; // lift above the head: 12px face + air gap
// air reserved over the head: hop margin + the name line + breathing
// room — bubbles stack from this line up, never on the plate. t_104:
// the task plate (🔨 t_NN / ✓) floats TASK_LIFT above the name line,
// so speech bubbles clear that too.
export const PLATE_BAND = 34;

/**
 * Paint one actor's plate over its head: the name only, rank-colored,
 * no backing — the line floats fully above the hair. 需求评审会议的
 * 参会人名字后缀一枚描线摄像机——会议室里一眼认出谁在开会。
 * @param {CanvasRenderingContext2D} ctx @param {import('./actor.js').Actor} a
 * @param {{hovered: boolean, viewW: number, avatarH?: number}} o
 */
export function drawPlate(ctx, a, { hovered, viewW, avatarH }) {
  ctx.font = '12px system-ui, "PingFang SC", sans-serif';
  const inMeet = a.meetIdx >= 0;
  const name = a.name + (a.grace ? '…' : '');
  if (!name) return;
  const textW = ctx.measureText(name).width;
  // 摄像机小标占位：11px 图标 + 4px 间距，钉在名字右侧
  const bw = textW + (inMeet ? 15 : 0);
  const x = Math.round(clamp(a.x - bw / 2, 2, Math.max(2, viewW - bw - 2)));
  // speakT 缺省必 0（影子演员没有 speakT——speakHop(undefined) 是 NaN，
  // 名牌 y 会被毒化成 NaN 画不出来）
  const y = Math.max(2, Math.round(a.y - (avatarH || 48) - speakHop(a.speakT || 0) - NAME_LINE - 1));
  ctx.textAlign = 'left';
  ctx.textBaseline = 'top';
  drawOutlined(ctx, name, x, y, rankColor(a.rank, a.isLocal));
  if (inMeet) drawMeetBadge(ctx, x + textW + 4, y, rankColor(a.rank, a.isLocal));
}

// TASK_LIFT：任务气泡浮在名牌之上（名牌行 18px 之上再留 12px 行高），
// speech 气泡的 PLATE_BAND 相应抬高由 bubbles.js 感知（见其引用）。
const TASK_LIFT = 30;

// 锤子（16 网格描线，dom.js 图标语言）：柄斜握＋锤头横置——「在干活」
// 的通用符号，随名牌同黑描边。
const HAMMER_D =
  'M2.5 13.5 7 9' +
  'M5.5 3.5h5v3.2c0 .4-.3.8-.8.8H6.3c-.4 0-.8-.4-.8-.8Z';

/**
 * Paint the working actor's task plate over the nameplate (t_104):
 * the hammer glyph + the task id (「t_103」), or a green ✓ when the
 * task just landed done (a.doneT > 0 — the board runs the 2s fade).
 * Skipped for actors without a label or mid-printing rituals.
 * @param {CanvasRenderingContext2D} ctx @param {import('./actor.js').Actor} a
 * @param {{viewW: number, avatarH?: number}} o
 */
export function drawTaskPlate(ctx, a, { viewW, avatarH }) {
  // 🔨 只挂「坐上工位」的人（working 且 deskIdx 有落点——actor.js 到站
  // 置 working 用的同一条判据）：走位途中、会议室/咖啡歇的锚定站定、
  // 房主巡场都不挂，头上没坐进工位就不顶着任务。✓ 例外：刚落定的
  // 2 秒谢幕跟人走，不问在不在工位。
  if (!(a.doneT || (a.label && a.working && a.deskIdx !== -1))) return;
  const y = Math.max(2, Math.round(
    a.y - (avatarH || 48) - speakHop(a.speakT) - NAME_LINE - TASK_LIFT - 1));
  ctx.font = '12px system-ui, "PingFang SC", sans-serif';
  ctx.textAlign = 'left';
  ctx.textBaseline = 'top';
  if (a.doneT) {
    // done ✓：绿色对勾（2px 粗），随 doneT 淡出
    ctx.save();
    ctx.globalAlpha = Math.min(1, a.doneT / 0.5);
    const cx = Math.round(clamp(a.x - 5, 2, Math.max(2, viewW - 12)));
    ctx.lineWidth = 2.4;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.strokeStyle = 'rgba(0,0,0,.7)';
    ctx.beginPath();
    ctx.moveTo(cx - 5, y + 6);
    ctx.lineTo(cx - 1.5, y + 10);
    ctx.lineTo(cx + 6, y + 1);
    ctx.stroke();
    ctx.strokeStyle = '#34c724';
    ctx.lineWidth = 1.5;
    ctx.stroke();
    ctx.restore();
    return;
  }
  const text = a.label;
  const textW = ctx.measureText(text).width;
  const bw = 16 + textW; // 锤子 12px + 4px 间距
  const x = Math.round(clamp(a.x - bw / 2, 2, Math.max(2, viewW - bw - 2)));
  // 锤子小标（名牌同法：黑描边＋内容色——干活用暖金）
  const p = new Path2D(HAMMER_D);
  ctx.save();
  ctx.translate(x, y + 0.5);
  ctx.scale(12 / 16, 12 / 16);
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';
  ctx.strokeStyle = 'rgba(0,0,0,.75)';
  ctx.lineWidth = 3.2;
  ctx.stroke(p);
  ctx.strokeStyle = '#d9b96a'; // 房主金一族——干活的暖色
  ctx.lineWidth = 1.5;
  ctx.stroke();
  ctx.restore();
  drawOutlined(ctx, text, x + 16, y, '#f2e6c8');
}

// dom.js 里 video 图标的同一 16 网格路径（合并成一条 d，走 Path2D）：
// 机身圆角矩形 + 右侧镜头折线，随名字同色、同黑描边。
const MEET_BADGE_D =
  'M3 4h6c.83 0 1.5.67 1.5 1.5v5c0 .83-.67 1.5-1.5 1.5H3c-.83 0-1.5-.67-1.5-1.5v-5C1.5 4.67 2.17 4 3 4Z' +
  'M10.5 7.5 14.5 5v6l-4-2.5';

function drawMeetBadge(ctx, x, y, color) {
  const p = new Path2D(MEET_BADGE_D);
  ctx.save();
  ctx.translate(x, y + 0.5);
  ctx.scale(11 / 16, 11 / 16); // 16 网格 → 11px，与 12px 名字同高
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';
  ctx.strokeStyle = 'rgba(0,0,0,.75)'; // 先描黑边再上色，与名字的描边同法
  ctx.lineWidth = 3.2;
  ctx.stroke(p);
  ctx.strokeStyle = color;
  ctx.lineWidth = 1.5;
  ctx.stroke(p);
  ctx.restore();
}

function drawOutlined(ctx, text, x, y, fill) {
  ctx.lineWidth = 2;
  ctx.strokeStyle = 'rgba(0,0,0,.75)';
  ctx.strokeText(text, x, y);
  ctx.fillStyle = fill;
  ctx.fillText(text, x, y);
}

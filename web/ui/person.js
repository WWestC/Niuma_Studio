// person.js — person presentation rules shared across the boards: the
// one rankLabel/personState truth for the roster rows, the detail
// header and the room's nameplates (a person renders the same 等级/状态
// words on every face). Plain functions over summary-shaped objects —
// {local, rank, online, grace} — no protocol or DOM knowledge.

import { t } from './i18n.js';

/** 房主 for the local human or rank 99, else Lv.N. */
export function rankLabel(p) {
  return p.local || p.rank === 99 ? t('房主') : `Lv.${p.rank}`;
}

// 等级色阶（游戏稀有度惯用序）：Lv.1..Lv.9，房主金。低级冷静、高级
// 暖亮，一眼读出资历而不必写字。这是办公室名牌（room/plates.js）的
// 画布真身；聊天头/名片卡的等级小标走 index.html 的 --rank-1..9 CSS
// 变量（深色照抄这里、浅色同色相压暗）——改动须两边同步。
const RANK_COLORS = [
  '#e8eef2', // Lv.1 白
  '#9ede9e', // Lv.2 绿
  '#52c489', // Lv.3 深绿
  '#6fb8e8', // Lv.4 蓝
  '#5a8fd8', // Lv.5 深蓝
  '#a88fe0', // Lv.6 紫
  '#e08fc0', // Lv.7 粉
  '#f0a860', // Lv.8 橙
  '#e87878', // Lv.9 红
];
const HOST_COLOR = '#ffec78';
const UNKNOWN_COLOR = '#dfe6ea';

/** A person's color by rank: host gold, else the rarity ladder. The
 *  office paints the nameplate with it; the chat/profile rank chips
 *  mirror the same ladder via CSS vars so 等级 reads the same on
 *  every face. */
export function rankColor(rank, isLocal) {
  if (isLocal || rank === 99) return HOST_COLOR;
  if (typeof rank !== 'number' || rank < 1 || rank > 9) return UNKNOWN_COLOR;
  return RANK_COLORS[rank - 1];
}

/** The three-state bucket: online / grace / offline. */
export function personState(p) {
  if (p.online && !p.grace) return 'online';
  if (p.grace) return 'grace';
  return 'offline';
}

export const STATE_LABEL = { online: t('在线'), grace: t('宽限'), offline: t('离线') };

/**
 * 离职标注 chip：label 来自项目板的 departTag(name)——已离职（全局
 * 归档）或已离编（本项目编制行）；'' = 名字裸显。任务台账视角里离
 * 过职的负责人不消失（历史任务还在），但每处人名都带得上说明。
 * @param {string} label '' | '已离职' | '已离编'
 * @returns {string} the tag HTML ('' when label empty)
 */
export function goneTagHTML(label) {
  return label ? `<span class="tag gone">${label}</span>` : '';
}

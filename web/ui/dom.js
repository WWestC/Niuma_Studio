// dom.js — tiny cross-domain DOM helpers (frontend 稿 §2.5 puts shared
// feedback/widget bits under ui/). No framework, no state.

import { t, localeTag } from './i18n.js';

// 统一描线图标（Icon 篇）：16 网格、1.5px 描线、圆角端点、currentColor
// 随文。模板串里直接展开；与 index.html 内联的 .ico SVG 同一语言。
// 全站不再用 emoji/字符图形当图标——一律走这里（rooms.js 的会话摘要
// 除外：那是 esc 过的纯文本行，只留文字标签）。
const ICON_PATHS = {
  // 通用动作
  close: '<path d="M4 4l8 8M12 4l-8 8"/>',
  down: '<path d="M8 2.5V12"/><path d="m4.5 8.5 3.5 3.5 3.5-3.5"/>',
  up: '<path d="M8 13.5V4"/><path d="m4.5 7.5 3.5-3.5 3.5 3.5"/>',
  right: '<path d="M6 3.5 10.5 8 6 12.5"/>',
  reply: '<path d="M6 4 3 7l3 3"/><path d="M3 7h8.5A3.5 3.5 0 0 1 15 10.5V13"/>',
  pin: '<circle cx="10.5" cy="5.5" r="2.75"/><path d="M8.6 7.4 3 13"/>',
  menu: '<path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11"/>',
  check: '<path d="m3.5 8.5 3 3 6-7"/>',
  // 已读回执（飞书式）：「N人 已读」小签的眼睛
  eye: '<path d="M1.8 8s2.4-4.2 6.2-4.2S14.2 8 14.2 8s-2.4 4.2-6.2 4.2S1.8 8 1.8 8Z"/><circle cx="8" cy="8" r="2"/>',
  dash: '<path d="M4 8h8"/>',
  home: '<path d="M2.5 7 8 2.5 13.5 7v6a1 1 0 0 1-1 1h-9a1 1 0 0 1-1-1Z"/><path d="M6.5 14v-4h3v4"/>',
  smile: '<circle cx="8" cy="8" r="6"/><path d="M5.6 9.4c.6 1.1 1.4 1.7 2.4 1.7s1.8-.6 2.4-1.7"/><path d="M5.8 6.2h.01M10.2 6.2h.01"/>',
  // 任务状态（taskcard 的 tk-icon 一族）
  circle: '<circle cx="8" cy="8" r="5.5"/>',
  doing: '<circle cx="8" cy="8" r="5.5"/><path d="M8 2.5a5.5 5.5 0 0 1 0 11Z" fill="currentColor" stroke="none"/>',
  pending: '<circle cx="8" cy="8" r="5.5"/><path d="M8 5.2V8l2 1.4"/>',
  milestone: '<path d="M8 2.5 13.5 8 8 13.5 2.5 8Z"/>',
  // 板块导航（index.html 侧边栏同款内联）
  building: '<rect x="2.5" y="2.5" width="11" height="11" rx="1"/><path d="M5.5 5.5h.01M10.5 5.5h.01M5.5 8h.01M10.5 8h.01M5.5 10.5h.01M10.5 10.5h.01"/><path d="M7 13.5v-2.3h2v2.3"/>',
  users: '<circle cx="6.25" cy="5.25" r="2.75"/><path d="M1.75 13.5c0-2.7 2-4.35 4.5-4.35s4.5 1.65 4.5 4.35"/><path d="M10.75 3a2.75 2.75 0 0 1 0 4.5"/><path d="M11.9 9.5c1.4.7 2.35 1.9 2.35 4"/>',
  clipboard: '<rect x="3.5" y="3.5" width="9" height="10" rx="1.5"/><path d="M6 3.5V3a1.5 1.5 0 0 1 1.5-1.5h1A1.5 1.5 0 0 1 10 3v.5"/><path d="M6 7.5h4M6 10h4"/>',
  // 需求（t_170 事件帧族）：旗标——立在办公室门口的诉求条目
  flag: '<path d="M4 14V2.5h7l-1.9 2.6 1.9 2.6H4"/>',
  chat: '<path d="M13 11.5A1.5 1.5 0 0 1 11.5 13h-6L2 15.5V4a1.5 1.5 0 0 1 1.5-1.5h8A1.5 1.5 0 0 1 13 4Z"/>',
  robot: '<rect x="3" y="5.5" width="10" height="7.5" rx="1.8"/><path d="M8 5.5V3.2"/><circle cx="8" cy="2.2" r=".8"/><path d="M5.8 9h.01M10.2 9h.01"/><path d="M6.6 11.5h2.8"/><path d="M1.5 8.5v2.5M14.5 8.5v2.5"/>',
  book: '<path d="M8 6.2C7 4.9 5.5 4.3 3.5 4.3h-2v8.4h2c2 0 3.5.6 4.5 1.9 1-1.3 2.5-1.9 4.5-1.9h2V4.3h-2c-2 0-3.5.6-4.5 1.9Z"/><path d="M8 6.2v8.4"/>',
  // 事件帧（stream 通知卡 / 会话摘要同义词表）
  person: '<circle cx="8" cy="5.25" r="2.75"/><path d="M2.75 13.5c0-2.7 2.4-4.35 5.25-4.35s5.25 1.65 5.25 4.35"/>',
  notebook: '<rect x="3" y="2" width="10" height="12" rx="1.5"/><path d="M5.5 2v12"/><path d="M7.5 5.5h3M7.5 8h3M7.5 10.5h2"/>',
  bolt: '<path d="M9.5 1.5 3.5 9.5h4L7 14.5l6-8H9z"/>',
  // 房间暂停（侧栏房行，原时间角落）：⏸ 双竖条＝点此暂停；▶ 实心
  // 三角＝已暂停、点此恢复
  pause: '<path d="M5.2 3.2v9.6M10.8 3.2v9.6"/>',
  play: '<path d="M5.2 2.8v10.4L12.6 8Z"/>',
  calendar: '<rect x="2.5" y="3.5" width="11" height="10" rx="1.5"/><path d="M2.5 6.5h11M5.5 3.5v-2M10.5 3.5v-2"/><path d="M5.5 9.5h.01M8 9.5h.01M10.5 9.5h.01M5.5 11.5h.01M8 11.5h.01"/>',
  video: '<rect x="1.5" y="4" width="9" height="8" rx="1.5"/><path d="M10.5 7.5 14.5 5v6l-4-2.5"/>',
  megaphone: '<path d="M2.5 6.5 13.5 3v8L2.5 9Z"/><path d="M5.2 9.3v1.7a1.5 1.5 0 0 0 1.5 1.5h.6"/><path d="M13.5 5.2a2.3 2.3 0 0 1 0 3.6"/>',
  bell: '<path d="M8 1.8a4.2 4.2 0 0 1 4.2 4.2v1.6c0 2.4.8 3.4 1.4 4H2.4c.6-.6 1.4-1.6 1.4-4V6A4.2 4.2 0 0 1 8 1.8Z"/><path d="M6.5 13.8a1.7 1.7 0 0 0 3 0"/>',
  clock: '<circle cx="8" cy="8" r="6"/><path d="M8 4.8V8l2.2 1.5"/>',
  // 侧栏底排（ident-row）：ZCode 式设置入口（图标篇·准确性——齿轮是
  // 「设置」的约定俗成表意，环＋八齿避免多余堆砌）
  gear: '<circle cx="8" cy="8" r="3.8"/><circle cx="8" cy="8" r="1.3"/><path d="M8 1.5v1.7M8 12.8v1.7M1.5 8h1.7M12.8 8h1.7M3.4 3.4l1.2 1.2M11.4 11.4l1.2 1.2M12.6 3.4l-1.2 1.2M4.6 11.4l-1.2 1.2"/>',
  // 设置卡危险区（重置工作室）：垃圾桶——盖＋桶身两道竖线，危险动作
  // 的约定俗成表意
  trash: '<path d="M2.5 4h11M6 4V3a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1v1M4.2 4l.6 8.2a1.5 1.5 0 0 0 1.5 1.4h3.4a1.5 1.5 0 0 0 1.5-1.4L11.8 4M6.6 7v3.6M9.4 7v3.6"/>',
  // 设置中心左导航（v2.9）：分类图标——外观调色盘、连接链环、数据
  // 圆柱库、关于信息泡（通用/通知/智能/维护沿用既有 gear/bell/
  // sparkle/refresh）
  palette: '<path d="M8 1.8c-3.4 0-6.2 2.5-6.2 5.7 0 3.1 2.5 5.7 5.6 5.7.9 0 1.6-.6 1.6-1.4 0-.4-.15-.7-.35-1-.2-.3-.35-.6-.35-1 0-.9.7-1.6 1.6-1.6h1.4c1.6 0 2.9-1.2 2.9-2.8 0-2.8-2.8-4.6-6.2-4.6Z"/><circle cx="5" cy="6.4" r=".9" fill="currentColor" stroke="none"/><circle cx="8.1" cy="4.8" r=".9" fill="currentColor" stroke="none"/><circle cx="11.2" cy="5.9" r=".9" fill="currentColor" stroke="none"/>',
  link: '<path d="M6.5 9.5 9.5 6.5"/><path d="m9 3.6 1.5-1.5a2.83 2.83 0 0 1 4 4L13 7.6"/><path d="M7 12.4 5.5 13.9a2.83 2.83 0 0 1-4-4L3 8.4"/>',
  db: '<ellipse cx="8" cy="4" rx="5" ry="2"/><path d="M3 4v8c0 1.1 2.24 2 5 2s5-.9 5-2V4"/><path d="M3 8c0 1.1 2.24 2 5 2s5-.9 5-2"/>',
  info: '<circle cx="8" cy="8" r="6"/><path d="M8 7.6v4"/><circle cx="8" cy="5.1" r=".9" fill="currentColor" stroke="none"/>',
  // 项目面板
  folder: '<path d="M1.75 4.25a1.5 1.5 0 0 1 1.5-1.5h3.1L8 5h5.75a1.5 1.5 0 0 1 1.5 1.5v5.75a1.5 1.5 0 0 1-1.5 1.5H3.25a1.5 1.5 0 0 1-1.5-1.5Z"/>',
  // 版本管理（v2.7）：分支（两节点一线，git-branch 的约定俗成表意）与
  // 提交（节点＋横线）
  branch: '<circle cx="4" cy="4" r="1.9"/><circle cx="4" cy="12" r="1.9"/><circle cx="12" cy="4" r="1.9"/><path d="M4 5.9v4.2"/><path d="M12 5.9c0 2.4-2 3.2-5.4 3.5"/>',
  sparkle: '<path d="M8 1.8 9.4 6.6 14.2 8l-4.8 1.4L8 14.2 6.6 9.4 1.8 8l4.8-1.4Z"/>',
  refresh: '<path d="M13.5 8a5.5 5.5 0 1 1-1.61-3.89"/><path d="M13.5 2.5v3.5H10"/>',
  fire: '<path d="M5.7 9.7A1.7 1.7 0 0 0 7.3 8c0-.9-.33-1.25-.66-2-1-1.4-.8-2.7 1.36-4 .33 1.67 1.67 3.27 3 4.33 1.3 1.07 2 2.37 2 3.67a4.7 4.7 0 1 1-9.4 0c0-.77.3-1.53.66-2A1.7 1.7 0 0 0 5.7 9.7Z"/>',
  // 编制表管理卡：锁（系统岗置灰锁定）＋加号（加岗位）
  lock: '<rect x="3.5" y="7" width="9" height="6.5" rx="1.2"/><path d="M5.5 7V5.5a2.5 2.5 0 0 1 5 0V7"/>',
  plus: '<path d="M8 3v10M3 8h10"/>',
  // 立项预检（v2.13）：警示三角——惊叹号居中，风险清单行首的约定俗成表意
  warn: '<path d="M8 2.2 14.3 13.3H1.7Z"/><path d="M8 6.4v3"/><circle cx="8" cy="11.4" r=".2" fill="currentColor" stroke="none"/>',
};

/** Inline stroke icon (currentColor follows the surrounding text), size in px. */
export function icon(name, size = 16) {
  return `<svg class="ico" width="${size}" height="${size}" viewBox="0 0 16 16" fill="none" ` +
    `stroke="currentColor" stroke-width="1.5" stroke-linecap="round" ` +
    `stroke-linejoin="round" aria-hidden="true">${ICON_PATHS[name] || ''}</svg>`;
}

/** 弹层退场（动效篇：消失快于出现）——打 .out 播退场动画，180ms 后
 *  摘除节点；内层卡面（.overlay-card/.bo-card/.sheet/.set-win）同步打
 *  .out。各浮层模块的 close() 统一换用它，替代裸 el.remove()。 */
export function dismiss(el, wait = 180) {
  if (!el || !el.isConnected) return;
  el.classList.add('out');
  const card = el.querySelector?.(':scope > .overlay-card, :scope > .bo-card, :scope > .sheet, :scope > .set-win');
  if (card) card.classList.add('out');
  setTimeout(() => el.remove(), wait);
}

/** 持久层的退场（如 #bubble-overlay）：播完把 hidden 归位、摘掉 .out。 */
export function dismissHide(el, wait = 150) {
  if (!el || el.hidden) return;
  el.classList.add('out');
  setTimeout(() => { el.hidden = true; el.classList.remove('out'); }, wait);
}

/** 写系统剪贴板——全站「复制」按钮的唯一入口。Clipboard API 优先，
 *  但它只在安全上下文（https/127.0.0.1）存在且授权可被拒（内嵌
 *  webview、局域网 http 打开都会碰壁），一律落隐藏 textarea +
 *  execCommand 兜底，别再裸调 navigator.clipboard 各写各的失败文案。
 *  @param {string} text @returns {Promise<boolean>} 是否复制成功 */
export async function copyToClipboard(text) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch { /* 授权被拒/非安全上下文 → execCommand 兜底 */ }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', ''); // iOS 兼容：readonly 才能弹出复制
  ta.style.cssText = 'position:fixed;top:-9999px;opacity:0';
  document.body.appendChild(ta);
  ta.select();
  ta.setSelectionRange(0, ta.value.length);
  let ok = false;
  try { ok = document.execCommand('copy'); } catch { ok = false; }
  ta.remove();
  return ok;
}

/** Escape untrusted text for innerHTML/attribute interpolation (quotes included — safe inside data-="" too). */
export function esc(s) {
  return String(s ?? '')
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// md → 净文本投影：引用摘要（snipOf）与头顶小气泡（room/bubbles.js
// makeBubble）共享——一行摘要/几行像素正文画不下排版，但也不能把
// **加粗**/# 标题/` 代码 ` 的原始标记闪在人脸上：块级标记（栅栏行、
// 标题井号、引用箭头、分割线、表格分隔行）按行剥掉，行内标记（强调、
// 行内代码、链接/图片方括号）收壳留馅。语法表与 chat/markdown.js 同源
// （宽松镜像，不走导出：那边是渲染器，这边只是投影）；完整排版是气泡
// 正文（richHTML）与点开浮层（roomview.showBubbleText）的事，raw 原文
// 不动。（原住 room/bubbles.js，搬来与 snipOf 同居——引用条也要这份
// 剥法。只删字符不改写内容，但跨标记的整句不再是原文的逐字子串：
// 引用跳转的摘要兜底因此两侧都试——原文与 plainify 后的原文，见
// chat/stream.js jumpToMessage。）
const FENCE_LINE_RE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^ \t]*)[ \t]*$/;
const HEAD_LINE_RE = /^ {0,3}(#{1,6}) +(.*?)\s*#*\s*$/;
const HR_LINE_RE = /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;
const QUOTE_LINE_RE = /^ {0,3}>[ \t]?(.*)$/;
const TABLE_SPLIT_LINE_RE = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;

export function plainifyMD(src) {
  const out = [];
  let inFence = false;
  for (const line of String(src ?? '').replace(/\r\n?/g, '\n').split('\n')) {
    if (FENCE_LINE_RE.test(line)) { inFence = !inFence; continue; } // 栅栏行本身不上泡
    if (inFence) { out.push(line); continue; } // 代码内容原样（超行由 maxLines 省略号兜底）
    const head = HEAD_LINE_RE.exec(line);
    if (head) { out.push(head[2]); continue; }
    if (HR_LINE_RE.test(line) || TABLE_SPLIT_LINE_RE.test(line)) continue; // 纯噪音行
    const quote = QUOTE_LINE_RE.exec(line);
    out.push(quote ? quote[1] : line);
  }
  return out.join('\n')
    .replace(/!\[([^[\]]*)\]\([^()\s]*\)/g, (_, alt) => alt || t('「图片」'))
    .replace(/\[([^[\]]+)\]\([^()\s]*\)/g, '$1')
    .replace(/(`+)([^]+?)\1/g, '$2')
    .replace(/\*\*\*([^]+?)\*\*\*/g, '$1')
    .replace(/\*\*([^]+?)\*\*/g, '$1')
    .replace(/(?<![\w_])__([^]+?)__(?![\w_])/g, '$1')
    .replace(/(?<![\w*])\*([^*\n]+?)\*(?![\w*])/g, '$1')
    .replace(/(?<![\w_])_([^_\n]+?)_(?![\w_])/g, '$1')
    .replace(/~~([^~\n]+?)~~/g, '$1')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** One-line snippet for quote bars / forward previews — a Feishu-style
 *  preview identifies the message, it doesn't retell it. The source is
 *  md prose more often than not, so plainifyMD strips the markup first
 *  (the bar previews the rendered message, not its source — 「**更大**」
 *  reads as 更大) and the width cap then counts clean text. Whitespace
 *  collapsed, capped by display width (全角/emoji 计 2、半角计 1；cap 是
 *  宽度单位，60＝30 个汉字) so a CJK-heavy say can't drag its whole body
 *  (let alone an embedded 「指令原文」 echo) into the quote line. The
 *  composer's quote embed and the stream's forward line both drink from
 *  here — one cap, one shape. */
export function snipOf(text, cap = 60) {
  const cps = [...plainifyMD(text).replace(/\s+/g, ' ').trim()];
  let w = 0, n = cps.length;
  for (let i = 0; i < cps.length; i++) {
    w += cps[i].codePointAt(0) > 0xff ? 2 : 1;
    if (w > cap) { n = i; break; }
  }
  return n < cps.length ? cps.slice(0, n).join('').trimEnd() + '…' : cps.join('');
}

/** hh:mm for a frame ts (Unix seconds); '' when absent. */
export function fmtTime(ts) {
  if (!ts) return '';
  return new Date(ts * 1000).toLocaleTimeString(localeTag(), { hour12: false, hour: '2-digit', minute: '2-digit' });
}

/** yyyy-MM-dd hh:mm for a Unix-seconds ts; '' when absent. */
export function fmtDateTime(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Relative age for a Unix-seconds ts: 刚刚 / N 分钟前 / N 小时前 / N 天前
 *  （en: just now / Nm ago / Nh ago / Nd ago——走 i18n 词典）。 */
export function fmtAge(ts, nowMs = Date.now()) {
  if (!ts) return '';
  const s = Math.max(0, Math.floor(nowMs / 1000 - ts));
  if (s < 60) return t('刚刚');
  if (s < 3600) return t('{n} 分钟前', { n: Math.floor(s / 60) });
  if (s < 86400) return t('{n} 小时前', { n: Math.floor(s / 3600) });
  return t('{n} 天前', { n: Math.floor(s / 86400) });
}

const CJK = /[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/;

/** Person-avatar text per the Feishu rule: 中文姓名取后两字（林七七→七七、
 *  张三→张三），拉丁名取首词+末词首字母（John Smith→JS），单词取首字母；
 *  空名兜底 '?'。同一人处处同一串字。 */
export function avatarText(name) {
  const cs = [...String(name || '').trim()];
  if (!cs.length) return '?';
  if (CJK.test(cs.join(''))) return cs.length >= 2 ? cs.slice(-2).join('') : cs[0];
  const ws = cs.join('').split(/\s+/);
  if (ws.length >= 2) return (ws[0][0] + ws[ws.length - 1][0]).toUpperCase();
  return cs[0].toUpperCase();
}

/** Group/room-avatar text per the Feishu rule: 取前两个字符（飞书群组/
 *  应用头像从名首取），拉丁取前两字母大写。 */
export function groupAvatarText(name) {
  const cs = [...String(name || '').trim()];
  if (!cs.length) return '?';
  return cs.slice(0, 2).join('').toUpperCase();
}

/** A server-given color, or the neutral fallback — never a free-form string in CSS. */
export function safeColor(hex, fallback = '#8a9186') {
  return /^#[0-9a-fA-F]{3,8}$/.test(hex || '') ? hex : fallback;
}

// chat.MemberColor's palette twin (chat/hub.go shirtPalette): the same
// fnv1a-32 hash over the name, the same hex — a person is one color on
// every face even where a projection carries no color field. Feishu-
// family vivid set: brand blue/green/orange/red plus kin hues.
const SHIRT_PALETTE = [
  '#3370ff', '#00a9ff', '#00b392', '#34c724', '#7ac70c',
  '#ff8800', '#f54a45', '#f75cb4', '#9e6bff', '#5856d6',
];

/** Stable member color for a name — the mirror of chat.MemberColor.
 *  The hash rides the name's UTF-8 bytes (Go's []byte(name)), not
 *  UTF-16 code units: the two diverge on every CJK name, and the old
 *  twin painted the ledger a different shirt than the roster's
 *  server-given color. */
export function memberColor(name) {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(String(name || ''))) {
    h ^= b;
    h = Math.imul(h, 0x01000193); // fnv prime, wrapping like uint32
  }
  h >>>= 0;
  return SHIRT_PALETTE[h % SHIRT_PALETTE.length];
}

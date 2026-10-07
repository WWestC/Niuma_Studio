// kbdiff.js — 黑板写播报卡的红绿对比（git 式前后效果）：kb 广播帧的
// diff 行（'-' 删 '+' 增，服务端 chat.LineDiff 产出）画成通知卡正文——
// 「黑板 写入 · 小马」头下不再只有孤零零一个事件词，而是看得见改了
// 什么、加了什么。独立纯模块：不碰 DOM、不 import dom.js，node:test
// 直测；stream.js 只管在 #noticeRow 里挂进来。
//
// 帧形状（chat/wire.go）：{diff: [{k:'-'|'+', t:'行文本'}…], diff_more:
// 超帽省略的行数, doc: {title, key, rev}}。diff 缺席（旧服务端、denied、
// 内容未变的写）→ 空串，调用方退回原来的事件词渲染。

import { t } from '../ui/i18n.js';

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

// 行宽帽与服务端 DiffMaxRunes 同数（200 字符）：服务端已夹过一次，这里
// 再夹一遍是纵深防御——手造帧/未来改帽都不至于把气泡撑爆。
const clampLine = (s, max = 200) => {
  const t = String(s ?? '');
  const rs = [...t];
  return rs.length > max ? rs.slice(0, max).join('') + '…' : t;
};

/** 黑板写播报卡的 diff 正文。无 diff 行 → ''（调用方退回旧渲染）。
 * @param {import('../wire/wire.js').Frame} frame
 * @returns {string} */
export function kbDiffHTML(frame) {
  const rows = frame.diff || [];
  if (!rows.length) return '';
  const doc = frame.doc || {};
  // 题名与 key 同文（设计稿类文档 key 即题名）只留 key——同一段路径
  // 「」一遍、等宽再来一遍，卡片头自己先啰嗦一遍
  const head = doc.key
    ? '<div class="kb-diff-doc">' +
      (doc.title && doc.title !== doc.key ? `「${esc(doc.title)}」` : '') +
      `<span class="kd-key">${esc(doc.key)}</span>` +
      `<span class="kd-rev">rev ${Number(doc.rev) || 0}</span></div>`
    : '';
  const body = rows.map((r) =>
    `<div class="kb-diff-row ${r.k === '+' ? 'add' : 'del'}">` +
    `<span class="kd-sign">${r.k === '+' ? '+' : '-'}</span>` +
    `<span class="kd-text">${esc(clampLine(r.t))}</span></div>`).join('');
  const more = frame.diff_more > 0
    ? `<div class="kb-diff-more">${t('…另有 {n} 行未展示', { n: frame.diff_more })}</div>` : '';
  return `<div class="kb-diff">${head}${body}${more}</div>`;
}

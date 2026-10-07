// gitcard.js — 版本管理事件流的两张卡（v2.8 gitflow）：
//
//   · gitCommitHTML：post-commit 钩子报来的提交卡——"提交"头下一行
//     摘要（等宽字体，git log 的观感）＋逐文件红绿行（+/− 行数，超
//     帽折叠加计数）＋引用的台账号 chips（t_NN/r_NN——点卡跳任务暂
//     缺，先只读展示）。提交从此不再是房间里 的静默事件。
//   · mergeCardHTML：合并提案卡——"合并"头下分支 → 主线一行、提交
//     数/文件红绿合计、涉及任务号；event=submitted 且查看者是房主
//     时出「验收并入 / 驳回」按钮（data-merge-accept / -reject，
//     stream.js 的 #onClick 经 frameTo 发 WS——与任务卡动词同一条
//     纪律：聊天侧的按钮是房主自己的手，不是越权代审）。
//
// 独立纯模块：不碰 DOM、不 import dom.js，node:test 直测；stream.js
// 只管在 #noticeRow 里挂进来。帧形状（chat/wire.go）：
// {git: {sha, author, subject, body, files:[{path,added,removed,binary}],
//  additions, deletions, refs:[t_NN…]}} / {merge: {id, branch, into,
//  commits, files, additions, deletions, tasks:[t_NN…], project_key}}。

import { t } from '../ui/i18n.js';

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const clamp = (s, max) => {
  const rs = [...String(s ?? '')];
  return rs.length > max ? rs.slice(0, max).join('') + '…' : String(s ?? '');
};

const shortSHA = (sha) => String(sha || '').slice(0, 7);

/** 提交卡正文。无 git 载荷 → ''（调用方退回旧渲染）。
 * @param {import('../wire/wire.js').Frame} frame
 * @returns {string} */
export function gitCommitHTML(frame) {
  const g = frame.git;
  if (!g || !g.sha) return '';
  const files = Array.isArray(g.files) ? g.files : [];
  const shown = files.slice(0, 6);
  const rows = shown.map((f) => {
    const stat = f.binary
      ? t('<span class="gc-bin">二进制</span>')
      : `<span class="gc-add">+${Number(f.added) || 0}</span>` +
        `<span class="gc-del">−${Number(f.removed) || 0}</span>`;
    return `<div class="gc-file"><span class="gc-stat">${stat}</span>` +
      `<span class="gc-path">${esc(clamp(f.path, 72))}</span></div>`;
  }).join('');
  const more = files.length > shown.length
    ? `<div class="gc-more">${t('…另有 {n} 个文件', { n: files.length - shown.length })}</div>` : '';
  const refs = (g.refs || []).length
    ? `<div class="gc-refs">${g.refs.map((r) => `<span class="gc-ref">${esc(r)}</span>`).join('')}</div>` : '';
  return `<div class="git-commit">` +
    `<div class="gc-subject"><code>${esc(shortSHA(g.sha))}</code> ${esc(clamp(g.subject || '', 90))}</div>` +
    (rows || more ? `<div class="gc-files">${rows}${more}</div>` : '') +
    `<div class="gc-tally">${t('共 {n} 文件', { n: files.length })}` +
    `<span class="gc-add"> +${Number(g.additions) || 0}</span>` +
    `<span class="gc-del"> −${Number(g.deletions) || 0}</span></div>` +
    refs +
    `</div>`;
}

/** 合并提案卡正文。无 merge 载荷 → ''。
 * @param {import('../wire/wire.js').Frame} frame
 * @param {{viewer?: string}} [opts] 查看者身份（房主才出按钮）
 * @returns {string} */
export function mergeCardHTML(frame, opts = {}) {
  const m = frame.merge;
  if (!m || !m.branch) return '';
  const refs = (m.tasks || []).length
    ? `<div class="gc-refs">${m.tasks.map((r) => `<span class="gc-ref">${esc(r)}</span>`).join('')}</div>` : '';
  let actions = '';
  // 只有 submitted（待审）卡出动词；merged/rejected 是历史回响，静读。
  if (frame.event === 'submitted' && opts.viewer) {
    actions = '<div class="nc-actions">' +
      `<button type="button" class="btn small gold" data-merge-accept>${t('验收并入 →')}</button>` +
      `<button type="button" class="btn small" data-merge-reject>${t('驳回')}</button>` +
      '</div>';
  }
  return `<div class="git-merge">` +
    `<div class="gm-line"><span class="gm-branch">${esc(m.branch)}</span>` +
    `<span class="gm-arrow">→</span><span class="gm-into">${esc(m.into)}</span>` +
    (m.id ? `<span class="gm-id">${esc(m.id)}</span>` : '') + `</div>` +
    `<div class="gc-tally">${t('{c} 提交 · {f} 文件', { c: Number(m.commits) || 0, f: Number(m.files) || 0 })}` +
    `<span class="gc-add"> +${Number(m.additions) || 0}</span>` +
    `<span class="gc-del"> −${Number(m.deletions) || 0}</span></div>` +
    refs +
    actions +
    `</div>`;
}

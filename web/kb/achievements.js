// achievements.js — 成就页签：像素收藏品货架（三角洲展柜语言）。
// 数据面不变（t_187 引擎的 GET 扩展）：
//   GET /achieve/tests-green → { unlocked: {key|key@成员: ts},
//                                achievements: [{key,name,text,perMember,medal}] }
// 解锁键两形：裸 key（全室集体）与 "key@成员名"（个人奖章）——面板从
// 键反解 holders。展出面把定义按 perMember 分区（个人里程碑/集体记忆）
// 摆上货架：每排 6 展位、不足补空展示底座（房间奖杯柜的空位语言）；
// 已解锁按后端定档（medal 0金1银2铜）点亮，未解锁暗格剪影＋灰字名
// （目标可见）。展位固定按定义序——收藏品货架的「格子逐步点亮」感。
// 点选展位柜底详情条看文案/持有者/达成时间；默认选最近达成一枚。
// 四态降级不白屏，纯读零写。

import { esc, fmtDateTime } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { getAchievements } from '../wire/api.js';

// 奖章三档色（与 pixart/room.go trophyPalettes 孪生——同序 0金1银2铜，
// 驾驶舱最近三奖章同取此表）
export const MEDAL_PAL = [
  ['#f2c14e', '#b8860b'], // 金
  ['#c8cfd6', '#8f959e'], // 银
  ['#e0a03a', '#a05a2a'], // 铜
];

const MEDAL_NAME = ['金', '银', '铜'];
// 未解锁暗格的剪影双色——var() 取主题线色（亮主题浅灰、暗主题深灰，
// 各自读得出杯形又不抢已解锁的戏）
const SIL = ['var(--line-2)', 'var(--line)'];
const PER_ROW = 6; // 每排展位数（个人里程碑 12＝两排、集体记忆 6＝一排）

// trophySvg：7×8 站姿奖杯的内联 SVG 精灵（pixart drawTrophy 孪生——
// 口沿/最宽腹线连耳/收腹/颈柄/两级底座），shape-rendering 钉死棱角，
// 任意缩放不掉像素感。sil=true 画剪影（单系暗色、无高光）。
function trophySvg(main, dark, sil = false) {
  const r = (x, y, w, h, f) =>
    `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="${f}"/>`;
  return '<svg class="tc-svg" viewBox="0 0 7 8" width="56" height="64" aria-hidden="true" shape-rendering="crispEdges">' +
    r(1, 0, 5, 1, main) +   // 口沿
    r(0, 1, 7, 1, main) +   // 杯腹最宽（连耳）
    r(1, 2, 5, 1, main) +   // 杯腹
    r(2, 3, 3, 1, main) +   // 收腹
    r(3, 4, 1, 1, main) +   // 杯颈柄
    r(2, 5, 3, 1, main) +   // 座张口
    r(0, 2, 1, 1, dark) + r(6, 2, 1, 1, dark) + // 杯耳
    r(2, 6, 3, 1, dark) +   // 座踝
    r(1, 7, 5, 1, dark) +   // 底板
    (sil ? '' : r(2, 0, 1, 1, '#ffffff')) +     // 口沿高光
    '</svg>';
}

export class AchievementsPanel {
  constructor() {
    this.state = 'loading';
    this.data = null;
    this.error = null;
    this.token = 0;
    this.root = null;
    this.sel = null;      // 详情条当前展位（成就 key）
    this.clickBound = false;
  }

  async load(root) {
    this.root = root;
    const tok = ++this.token;
    this.state = 'loading';
    this.paint();
    try {
      const body = await getAchievements();
      if (this.token !== tok) return;
      const defs = (body && body.achievements) || [];
      const unlocked = (body && body.unlocked) || {};
      if (!defs.length) {
        this.state = 'empty';
      } else {
        this.state = 'ready';
        // 从解锁键反解每枚定义的持有者（key 或 key@成员）
        this.data = defs.map((d) => {
          const holders = [];
          let best = 0;
          for (const k of Object.keys(unlocked)) {
            if (k === d.key) { holders.push(t('工作室')); best = Math.max(best, unlocked[k]); }
            else if (k.startsWith(d.key + '@')) { holders.push(k.slice(d.key.length + 1)); best = Math.max(best, unlocked[k]); }
          }
          return { ...d, desc: d.text, holders, ts: best };
        });
        // 默认选中最近达成一枚（无达成选首枚——详情条不空着）
        const done = this.data.filter((a) => (a.holders || []).length);
        this.sel = (done.length ? done.reduce((a, b) => (b.ts > a.ts ? b : a)) : this.data[0]).key;
      }
    } catch (err) {
      if (this.token !== tok) return;
      this.state = 'error';
      this.error = err;
    }
    this.paint();
  }

  /** 页签切走即作废在途 load（kb.js switchTab 调）——迟到回包不再
   *  paint 覆盖新页签（chronicle.cancel 同款纪律）。 */
  cancel() { this.token++; }

  paint() {
    const root = this.root;
    if (!root) return;
    if (this.state === 'loading') {
      root.innerHTML = '<div class="chron-hint">' + esc(t('成就加载中…')) + '</div>';
      return;
    }
    if (this.state === 'error') {
      root.innerHTML =
        '<div class="chron-hint chron-err">' +
        esc(t('成就清单读取失败：{msg}', { msg: this.error?.message || String(this.error) })) + '</div>' +
        '<button type="button" class="chron-retry btn">' + esc(t('重试')) + '</button>';
      const btn = root.querySelector('.chron-retry');
      if (btn) btn.onclick = () => this.load(this.root);
      return;
    }
    if (this.state === 'empty') {
      root.innerHTML =
        '<div class="chron-hint">' + esc(t('成就清单还没定稿——奖章在路上。')) + '</div>';
      return;
    }
    // 货架面：两分区（perMember 切分）＋预留展位排（成就以后再加，
    // 空格先占架铺满），每排 6 位不足补空展示底座
    const done = this.data.filter((a) => (a.holders || []).length);
    const sections = [
      { label: t('个人里程碑'), items: this.data.filter((a) => a.perMember) },
      { label: t('集体记忆'), items: this.data.filter((a) => !a.perMember) },
      { label: t('预留展位'), items: [], reserve: true },
    ];
    let html = '<div class="trophy-case">' +
      '<div class="tc-head"><span class="tc-title">' + esc(t('荣誉墙 · 收藏品')) + '</span>' +
      `<span class="tc-count">${esc(t('已达成 {done} / {total} 枚', { done: done.length, total: this.data.length }))}</span></div>`;
    for (const sec of sections) {
      if (!sec.reserve && !sec.items.length) continue;
      if (sec.reserve) {
        html += `<div class="tc-sec"><span>${sec.label}</span></div>`;
        html += this.shelfRow([]);
        continue;
      }
      const got = sec.items.filter((a) => (a.holders || []).length).length;
      html += `<div class="tc-sec"><span>${sec.label}</span>` +
        `<span class="tc-sec-n">${got}/${sec.items.length}</span></div>`;
      for (let i = 0; i < sec.items.length; i += PER_ROW) {
        html += this.shelfRow(sec.items.slice(i, i + PER_ROW));
      }
    }
    const sel = this.data.find((a) => a.key === this.sel) || this.data[0];
    html += `<div class="tc-detail" role="status">${this.detailHtml(sel)}</div></div>`;
    root.innerHTML = html;
    this.bindClick(root);
  }

  // 一排展位：奖杯按钮＋名字铭牌，空位给展示底座（房间柜同款空位语言）
  shelfRow(items) {
    let h = '<div class="tc-shelf">';
    for (let i = 0; i < PER_ROW; i++) {
      const a = items[i];
      if (!a) {
        h += '<span class="tc-slot tc-peg" aria-hidden="true"><i class="tc-pegbar"></i></span>';
        continue;
      }
      const got = (a.holders || []).length > 0;
      const pal = MEDAL_PAL[a.medal ?? 0];
      h += `<button type="button" class="tc-slot${got ? '' : ' tc-todo'}` +
        `${a.key === this.sel ? ' sel' : ''}" data-ach="${esc(a.key)}">` +
        `<span class="tc-trophy">${got ? trophySvg(pal[0], pal[1]) : trophySvg(SIL[0], SIL[1], true)}</span>` +
        `<span class="tc-name">${esc(a.name)}</span>` +
        '</button>';
    }
    return h + '</div>';
  }

  // 详情条内容：档位色点＋名字＋章级（/未解锁）＋达成时间；次行文案＋持有者
  detailHtml(a) {
    const holders = (a.holders || []).map(esc).join(t('、'));
    const got = holders.length > 0;
    const pal = MEDAL_PAL[a.medal ?? 0];
    const line1 = `<span class="tc-dot" style="background:${pal[0]};border-color:${pal[1]}"></span>` +
      `<span class="tc-d-name">${esc(a.name)}</span>` +
      `<span class="tc-d-medal${got ? '' : ' tc-dim'}">${got ? esc(t(MEDAL_NAME[a.medal ?? 0] + '章')) : esc(t('未解锁'))}</span>` +
      (got && a.ts ? `<span class="tc-d-ts">${esc(t('达成于 {ts}', { ts: fmtDateTime(a.ts) }))}</span>` : '');
    return `<div class="tc-d-line">${line1}</div>` +
      `<div class="tc-d-text">${esc(a.desc || '')}${got && holders ? esc(t('——{h}', { h: holders })) : ''}</div>`;
  }

  // 点选展位换详情（事件委托挂一次——面板反复进出 load() 不叠加，
  // cockpit bindRetry 同款卫哨；黑板的 pane onClick 不认识 data-ach，
  // 两边互不打扰）
  bindClick(root) {
    if (this.clickBound) return;
    this.clickBound = true;
    root.addEventListener('click', (ev) => {
      const slot = ev.target.closest('[data-ach]');
      if (slot) this.select(slot.dataset.ach);
    });
  }

  select(key) {
    if (!this.data || !this.data.some((a) => a.key === key)) return;
    this.sel = key;
    const root = this.root;
    if (!root) return;
    root.querySelectorAll('[data-ach]').forEach((b) =>
      b.classList.toggle('sel', b.dataset.ach === key));
    const a = this.data.find((d) => d.key === key);
    const bar = root.querySelector('.tc-detail');
    if (bar && a) bar.innerHTML = this.detailHtml(a);
  }
}

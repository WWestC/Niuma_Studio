// profilecard.js — the person profile card (飞书设计规范·头像篇＋气泡
// 卡片篇): clicking an avatar, a sender name or an @mention token opens
// the SAME card — 头像篇 says avatar clicks may expand the person
// profile per scene; this card is that expansion. Content: letter
// avatar with a presence dot (pavatar/pdot, the people board's visual
// language), name + rank tag (person.js rankLabel — one person renders
// the same 等级/状态 words on every face), role · state line, and the
// action row over a full-width divider with the primary action
// rightmost, max 3 (气泡卡片篇). Positioning follows the popover spec:
// width 280, trigger gap 4, page safe margin 16, the 12×6 arrow tracks
// the anchor's center and clamps near corners, flips above when below
// doesn't fit. Click-triggered semantics: closes on outside click,
// Esc (layered — it eats the Esc before the stream's row selection),
// any scroll (the fixed card must not float off its anchor), or
// completing an action.

import { esc, safeColor, memberColor, avatarText, dismiss } from './dom.js';
import { rankLabel, personState, STATE_LABEL } from './person.js';
import { t } from './i18n.js';

const WIDTH = 280; // 气泡卡片篇：推荐宽 280

export class ProfileCard {
  /**
   * @param {Object} [opts]
   * @param {(name: string) => void} [opts.mention] @ 提及 — aim the composer
   *   (app.js decides the board jump and the owner-face guard)
   * @param {(name: string) => void} [opts.open] 牛马主页 — cross-board people detail
   * @param {(name: string) => void} [opts.trace] 工作过程 — open the trace
   *   drawer for the CURRENT room's member of this name (v2.8)
   * @param {(name: string) => (null|{name: string, role?: string, color?: string,
   *   online?: boolean, grace?: boolean, rank?: number, local?: boolean})} [opts.person]
   *   the person truth resolver (app.js merges /kb/people presence with the roster)
   */
  constructor(opts = {}) {
    this.opts = opts;
    this.el = null;    // the open card (null = closed)
    this.name = null;  // whose card it is
    // Esc 是层级收口：注册一次（先于使用方注册 → 先触发），开着时吃掉
    // 这一下，别让同一颗 Esc 既收卡又清掉别的选中态
    this.onKey = (ev) => {
      if (ev.key !== 'Escape' || !this.el) return;
      ev.stopImmediatePropagation();
      this.close();
    };
    document.addEventListener('keydown', this.onKey);
  }

  /** @param {HTMLElement} anchor the clicked avatar/name/token @param {string} name */
  openFor(anchor, name) {
    if (!name) return;
    this.close();
    const p = this.opts.person?.(name) || null;
    const color = safeColor(p?.color || memberColor(name));
    const state = p && (p.online !== undefined || p.grace !== undefined) ? personState(p) : null;
    const rank = p ? rankLabel(p) : '';
    // 等级小标与办公室名牌/聊天头同一副色阶（index.html 的 --rank-1..9，
    // 深色主题照抄名牌、浅色同色相压暗），房主走 .you 金字槽——三张脸一枚色
    const host = !!p && (p.local || p.rank === 99);
    const rc = p && !host && p.rank >= 1 && p.rank <= 9 ? ` style="color:var(--rank-${p.rank})"` : '';
    const rankTag = rank ? ` <span class="tag${host ? ' you' : ''}"${rc}>${esc(rank)}</span>` : '';
    const role = p?.role || t('不在名册中');
    const el = document.createElement('div');
    el.className = 'profile-card';
    el.innerHTML =
      `<span class="pavatar pf-avatar" style="background:${color}">${esc(avatarText(name))}` +
      (state ? `<i class="pdot ${state}" title="${STATE_LABEL[state]}"></i>` : '') +
      '</span>' +
      `<strong class="pf-name">${esc(name)}${rankTag}</strong>` +
      `<span class="pf-role">${esc(role)}${state ? ` · ${STATE_LABEL[state]}` : ''}</span>` +
      '<div class="pf-ops">' +
        '<button type="button" class="btn small" data-pf-page>' + esc(t('牛马主页')) + '</button>' +
        '<button type="button" class="btn small" data-pf-trace>' + esc(t('工作过程')) + '</button>' +
        '<button type="button" class="btn small gold" data-pf-mention>' + esc(t('@ 提及')) + '</button>' +
      '</div>';
    document.body.appendChild(el);
    this.el = el;
    this.name = name;
    this.#place(anchor);
    el.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-pf-mention]')) {
        this.close();
        this.opts.mention?.(name);
        return;
      }
      if (ev.target.closest('[data-pf-trace]')) {
        this.close();
        this.opts.trace?.(name);
        return;
      }
      if (ev.target.closest('[data-pf-page]')) {
        this.close();
        this.opts.open?.(name);
      }
    });
    this.onDoc = (ev2) => { if (!el.contains(ev2.target)) this.close(); };
    document.addEventListener('mousedown', this.onDoc, true);
    // fixed 卡不跟锚点滚动，收口通路要三保险：window/document 捕获
    // （Chromium 系元素滚动也穿捕获层）＋ 锚点的滚动祖先链逐个直挂
    // （WebKit 系元素 scroll 只达滚动元素自身，不穿 window 捕获——
    // 直挂祖先后「滚到哪个容器」都有一份监听在容器上等）。开卡挂、
    // 收卡全拆；非滚动祖先上的监听永不触发，零成本。
    this.scrollHosts = [document, window];
    for (let n = anchor.parentElement; n && n !== document; n = n.parentElement) {
      this.scrollHosts.push(n);
    }
    this.onScroll = () => this.close();
    for (const h of this.scrollHosts) h.addEventListener('scroll', this.onScroll, true);
  }

  close() {
    if (!this.el) return;
    dismiss(this.el, 150);
    this.el = null;
    this.name = null;
    document.removeEventListener('mousedown', this.onDoc, true);
    if (this.scrollHosts) {
      for (const h of this.scrollHosts) h.removeEventListener('scroll', this.onScroll, true);
      this.scrollHosts = null;
    }
  }

  // 气泡卡片篇定位：与锚点居中优先、页边安全距 16、下方放不下翻上
  // 面；箭头始终对准锚点中心，卡片被钳到边时箭头随中心走并钳回卡内
  #place(anchor) {
    const a = anchor.getBoundingClientRect();
    const margin = 16, gap = 4;
    const h = this.el.offsetHeight || 150;
    let left = a.left + a.width / 2 - WIDTH / 2;
    left = Math.max(margin, Math.min(left, innerWidth - WIDTH - margin));
    const below = a.bottom + gap + h <= innerHeight - margin;
    const top = below ? a.bottom + gap : Math.max(margin, a.top - h - gap);
    this.el.classList.add(below ? 'pc-below' : 'pc-above');
    this.el.style.left = `${left}px`;
    this.el.style.top = `${top}px`;
    const cx = a.left + a.width / 2 - left;
    this.el.style.setProperty('--pf-arrow', `${Math.max(12, Math.min(cx, WIDTH - 12))}px`);
  }
}

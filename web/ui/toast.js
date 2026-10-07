import { icon, esc } from './dom.js';
import { t } from './i18n.js';

// toast.js — 全局临时提示（飞书「设计应用」临时提示规范重设计）。
//
// 规范落点（component----feedback/toast）：
// - 五种语义 info / success / error / warning / loading，各配 16px 图标
//   （不支持自定义颜色）；loading 与 sticky 提示不自动消失。
// - 组成＝图标 + 文本（≤2 行，超出省略）+ 文字按钮（≤2，与文本同行
//   右对齐，放不下时单独一行）+ 关闭按钮（文本超 20 字或常驻时出现）。
// - 时长：无操作 0-40 字 3-5s、40-80 字 6-8s；带操作 ≥6s（4-6s / 6-10s）。
//   悬停暂停计时、离开续计（剩余时长下限 800ms 保证可见）。
// - 多条：默认同时最多 2 条，按触发先后自上而下排（最新在下），超量
//   挤掉最早一条；文本完全相同的重复触发合并为一条并重置计时。
// - 位置：页面顶部居中，PC 距顶 72px；宽度随内容自适应，上限 600px。
//
// API 兼容旧调用：show(text, kind)；扩展 show(text, opts) 与
// show({ text, kind, actions, sticky })，返回 { close() } 句柄。

const KIND_CLASS = { info: 'info', ok: 'success', err: 'error', warn: 'warning', success: 'success', error: 'error', warning: 'warning', loading: 'loading' };
const MAX_TOASTS = 2;

// 规范时长（取区间中值）：无操作 3-5s / 6-8s；有操作 4-6s / 6-10s
const dwellFor = (t, hasActions) => {
  if (hasActions) return t.length <= 40 ? 5000 : 8000;
  return t.length <= 40 ? 4000 : 7000;
};

const ICONS = {
  // 16px 线性图标，currentColor 上色（.toast 类型类定色）
  info: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="M8 7.5v3.4" stroke-linecap="round"/><circle cx="8" cy="5.1" r=".9" fill="currentColor" stroke="none"/></svg>',
  success: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="m5.4 8.2 1.8 1.8 3.4-3.9" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  error: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="m6 6 4 4M10 6l-4 4" stroke-linecap="round"/></svg>',
  warning: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2.6 14 13H2z" stroke-linejoin="round"/><path d="M8 6.4v3" stroke-linecap="round"/><circle cx="8" cy="11.2" r=".9" fill="currentColor" stroke="none"/></svg>',
  loading: '<svg class="t-spin" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 1.75A6.25 6.25 0 1 1 1.75 8" stroke-linecap="round"/></svg>',
};

export class Toast {
  constructor() {
    this.root = null;
    this.live = new Map(); // 合并键（kind+text）→ 在屏节点
  }

  /**
   * @param {string} text
   * @param {('info'|'ok'|'err'|'warn'|'success'|'error'|'warning'|'loading')|Object} [kind]
   * @param {{kind?:string, actions?:Array<{label:string, onClick:Function}>, sticky?:boolean}} [opts]
   * @returns {{close: Function}} 句柄（loading/常驻提示由调用方收尾）
   */
  show(text, kind = 'info', opts) {
    const o = typeof kind === 'object' ? kind : (opts || {});
    const k = KIND_CLASS[typeof kind === 'string' ? kind : o.kind] || KIND_CLASS[o.kind] || 'info';
    const body = String(text ?? o.text ?? '');
    if (!body) return { close() {} };
    const actions = (o.actions || []).filter((a) => a && a.label).slice(0, 2);
    const sticky = !!o.sticky || k === 'loading';

    if (!this.root) {
      this.root = document.createElement('div');
      this.root.id = 'toasts';
      document.body.appendChild(this.root);
    }

    // 相同内容重复触发：合并为一条，重置计时（规范「多条显示」）
    const key = k + '\u0000' + body;
    const dup = this.live.get(key);
    if (dup?.isConnected) {
      this.#arm(dup, sticky ? 0 : dwellFor(body, actions.length > 0), actions.length > 0);
      return dup.__handle;
    }

    const el = document.createElement('div');
    el.className = `toast ${k}`;
    el.innerHTML =
      `<span class="t-ico" aria-hidden="true">${ICONS[k] || ICONS.info}</span>` +
      `<div class="t-main"></div>` +
      (actions.length
        ? `<span class="t-acts">${actions.map((a, i) => `<button type="button" class="t-act" data-i="${i}">${a.label.replace(/</g, '&lt;')}</button>`).join('')}</span>`
        : '') +
      // 关闭按钮：超 20 字或常驻/带操作时提供（规范组成要素）
      (sticky || body.length > 20 || actions.length
        ? '<button type="button" class="t-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>'
        : '');
    el.querySelector('.t-main').textContent = body;
    // 长文本（>40 字）时文字按钮换到第二行（规范：右边空间不够或文本超一行）
    el.classList.toggle('t-wrap', body.length > 40 && actions.length);

    el.addEventListener('click', (ev) => {
      const act = ev.target.closest('.t-act');
      if (act) {
        actions[+act.dataset.i]?.onClick?.();
        this.#dismiss(el); // 点击文字按钮即关（承载快捷回退的语义）
        return;
      }
      if (ev.target.closest('.t-x')) { this.#dismiss(el); return; }
      // 老行为保留：点击提示本体也可收起
      this.#dismiss(el);
    });
    // 悬停暂停计时，离开后续计（剩余时长下限 800ms，保证可见）
    el.addEventListener('mouseenter', () => {
      clearTimeout(el.__t);
      el.__left -= Date.now() - el.__start;
    });
    el.addEventListener('mouseleave', () => {
      if (!el.__sticky) this.#arm(el, Math.max(el.__left, 800), el.__hasActs);
    });

    this.root.appendChild(el);
    while (this.root.children.length > MAX_TOASTS) {
      const dead = this.root.firstElementChild;
      this.live.delete(dead.__key);
      dead.remove();
    }
    this.live.set(key, el);
    el.__key = key;
    el.__sticky = sticky;
    el.__hasActs = actions.length > 0;
    el.__handle = { close: () => this.#dismiss(el) };
    if (!sticky) this.#arm(el, dwellFor(body, el.__hasActs), el.__hasActs);
    return el.__handle;
  }

  /** loading 语义糖：常驻 + 转圈，调用方拿句柄关闭。 */
  loading(text) { return this.show(text, 'loading'); }

  #arm(el, ms) {
    clearTimeout(el.__t);
    el.__left = ms;
    el.__start = Date.now();
    el.__t = setTimeout(() => this.#dismiss(el), ms);
  }

  #dismiss(el) {
    if (!el || !el.isConnected) return;
    clearTimeout(el.__t);
    this.live.delete(el.__key);
    el.classList.add('out');
    setTimeout(() => el.remove(), 200);
  }
}

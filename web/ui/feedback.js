// feedback.js — 飞书「设计应用」反馈组件套件（本文件是临时提示的
// 兄弟件，Toast 在 toast.js）。全部按开放平台设计规范文档落地：
//   对话框 Dialog     component----feedback/dialog
//   抽屉 Drawer       component----feedback/drawer
//   常驻提示 Notice   component----feedback/notice
//   通知提醒框 Notify component----feedback/notification
//   加载中 Spin/Loading overlay  component----feedback/loading
//   进度条 Progress   component----feedback/progress
// 设计令牌沿用 index.html 的 :root（--gold=B500 品牌蓝、--ok/--warn/
// --bad = G500/O500/R500、N 灰阶 = --chalk*）。无框架、无状态层，
// 纯 DOM + 模板串（house style）。

import { esc, icon } from './dom.js';
import { t } from './i18n.js';

// ── 共用：语义色与图标 ────────────────────────────────────────────
// 四态 24px 圆形图标（对话框/通知提醒框用）；16px 线性图标给常驻提示
const SEM = {
  info:    { color: 'var(--gold)', name: t('普通') },
  success: { color: 'var(--ok)',   name: t('成功') },
  warning: { color: 'var(--warn)', name: t('警告') },
  error:   { color: 'var(--bad)',  name: t('错误') },
};

const CIRCLE_ICON = {
  info: '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="9.4"/><path d="M12 11v5.4" stroke-linecap="round"/><circle cx="12" cy="7.7" r="1.2" fill="currentColor" stroke="none"/></svg>',
  success: '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="9.4"/><path d="m8 12.4 2.7 2.7 5.2-6" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  warning: '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="9.4"/><path d="M12 7.6V13" stroke-linecap="round"/><circle cx="12" cy="16.4" r="1.2" fill="currentColor" stroke="none"/></svg>',
  error: '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="9.4"/><path d="M9.2 9.2l5.6 5.6M14.8 9.2l-5.6 5.6" stroke-linecap="round"/></svg>',
};
const LINE_ICON = {
  info: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="M8 7.5v3.4" stroke-linecap="round"/><circle cx="8" cy="5.1" r=".9" fill="currentColor" stroke="none"/></svg>',
  success: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="m5.4 8.2 1.8 1.8 3.4-3.9" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  warning: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2.6 14 13H2z" stroke-linejoin="round"/><path d="M8 6.4v3" stroke-linecap="round"/><circle cx="8" cy="11.2" r=".9" fill="currentColor" stroke="none"/></svg>',
  error: '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="8" cy="8" r="6.25"/><path d="m6 6 4 4M10 6l-4 4" stroke-linecap="round"/></svg>',
};

/** 语义色的 CSS 变量名（模板串里内联用）。 */
const varOf = (type) => (SEM[type] ? SEM[type].color : 'var(--gold)');

// 浮层栈（对话框/抽屉/设置窗可嵌套，规范「同时出现多个对话框」）：
// ESC 只结束最顶层一层，底下的原样留住。栈随模块加载挂一次全局键
// 监听（早于流/输入井等同节点的后来监听注册——stopImmediatePropagation
// 才拦得住它们：栈上有层时这颗 Esc 归层，不再顺带清掉流里的选中态，
// 设置气泡卡时代的老纪律上收到这里）。layerPush 导出给自绘浮层
// （settings.js 的设置窗）入栈同一套层级语义。
const LAYER_STACK = [];
document.addEventListener('keydown', (ev) => {
  if (ev.key !== 'Escape' || !LAYER_STACK.length) return;
  ev.stopImmediatePropagation();
  LAYER_STACK[LAYER_STACK.length - 1]();
});
export const layerPush = (close) => { LAYER_STACK.push(close); return () => { const i = LAYER_STACK.indexOf(close); if (i >= 0) LAYER_STACK.splice(i, 1); }; };

// ── 对话框 Dialog ─────────────────────────────────────────────────
// 规范：标题区（可选四态图标/标题/描述/右上关闭）+ 内容区（超长内滚，
// 溢出时标题/操作区压 1px 分割线）+ 操作区（按钮右对齐，仅一个主操作）；
// 遮罩 rgba(0,0,0,.55)，Z 轴最高；ESC/遮罩/×/按钮都可结束；固定宽
// Small 420 / Medium 600 / Large 840 / Extra-large 1080，页面居中。
export class Dialog {
  /**
   * @param {{
   *   title?:string, desc?:string, text?:string, html?:string,
   *   kind?:'info'|'success'|'warning'|'error'|null,
   *   size?:'s'|'m'|'l'|'xl',
   *   actions?:Array<{label:string, primary?:boolean, danger?:boolean, value?:*, keep?:boolean}>,
   *   maskClosable?:boolean, escClosable?:boolean,
   * }} [opts]
   * @returns {Promise<*>} resolve 为被点按钮的 value（无 value 用 label）；× / ESC / 遮罩 → null
   */
  static show(opts = {}) {
    const {
      title = '', desc = '', text = '', html = '',
      kind = null, size = 'm',
      actions = [{ label: t('确定'), primary: true }],
      maskClosable = true, escClosable = true,
    } = opts;

    return new Promise((resolve) => {
      const wrap = document.createElement('div');
      wrap.className = 'fb-dlg-wrap';
      wrap.innerHTML =
        `<div class="fb-dlg fb-${size}" role="dialog" aria-modal="true"${title ? ` aria-label="${esc(title)}"` : ''}>` +
        `<header class="fb-dlg-head${kind ? ' has-ico' : ''}">` +
        (kind ? `<span class="fb-ico24" style="color:${varOf(kind)}" aria-hidden="true">${CIRCLE_ICON[kind]}</span>` : '') +
        `<div class="fb-dlg-tt">${title ? `<strong>${esc(title)}</strong>` : ''}${desc ? `<span>${esc(desc)}</span>` : ''}</div>` +
        '<button type="button" class="fb-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>' +
        '</header>' +
        `<div class="fb-dlg-body">${html || (text ? esc(text).replace(/\n/g, '<br>') : '')}</div>` +
        '<footer class="fb-dlg-foot">' +
        actions.map((a, i) =>
          `<button type="button" class="btn${a.primary ? (a.danger ? ' danger armed' : ' gold') : (a.danger ? ' danger' : '')}" data-i="${i}">${esc(a.label)}</button>`).join('') +
        '</footer></div>';

      const done = (v) => {
        layerOff?.();
        document.body.classList.remove('fb-lock');
        wrap.classList.add('out');
        setTimeout(() => wrap.remove(), 160);
        resolve(v);
      };
      const layerOff = escClosable ? layerPush(() => done(null)) : null;
      wrap.addEventListener('click', (ev) => {
        if (ev.target === wrap && maskClosable) { done(null); return; }
        if (ev.target.closest('.fb-x')) { done(null); return; }
        const btn = ev.target.closest('.fb-dlg-foot .btn');
        if (btn) {
          const a = actions[+btn.dataset.i];
          if (!a.keep) done(a.value !== undefined ? a.value : a.label);
        }
      });
      document.body.classList.add('fb-lock');
      document.body.appendChild(wrap);
      // 内容超长（内滚出现）时给标题/操作区压 1px 分割线（规范）。首帧
      // 布局可能未定（动画/字体），双 rAF 之外再挂 scroll 兜底：真滚起来
      // 一定补线；晚到的异步内容也覆盖。
      const bodyEl = wrap.querySelector('.fb-dlg-body');
      const markScroll = () => {
        if (bodyEl && bodyEl.scrollHeight > bodyEl.clientHeight + 1) wrap.querySelector('.fb-dlg').classList.add('is-scroll');
      };
      requestAnimationFrame(() => requestAnimationFrame(markScroll));
      setTimeout(markScroll, 120); // 渲染对齐回调在后台 webview 会挂起，定时器兜底
      bodyEl?.addEventListener('scroll', markScroll);
      // 布局驱动的兜底：入场动画收尾/晚到的异步内容改变 body 盒尺寸时
      // 再判一次（不依赖事件时序，关窗即断开）
      let ro;
      if (bodyEl && window.ResizeObserver) {
        ro = new ResizeObserver(markScroll);
        ro.observe(bodyEl);
      }
      const wrapEl = wrap;
      setTimeout(() => ro?.disconnect(), 3000);
      wrap.querySelector('.fb-dlg-foot .gold, .fb-dlg-foot .btn')?.focus();
    });
  }

  /** 二次确认（基础对话框：警示/简单确认，一个主操作）。 */
  static confirm(opts = {}) {
    const {
      title = t('确认操作'), text = '', danger = false,
      okText = danger ? t('删除') : t('确定'), cancelText = t('取消'),
      kind = danger ? 'warning' : null,
    } = opts;
    return Dialog.show({
      title, text, kind, size: 's',
      actions: [
        { label: cancelText },
        { label: okText, primary: true, danger, value: true },
      ],
    }).then((v) => v === true);
  }

  /** 单按钮消息提醒（info/success/warning/error 四态图标）。 */
  static alert(opts = {}) {
    const { kind = 'info', title = '', text = '', okText = t('确定') } = opts;
    return Dialog.show({ kind, title, text, size: 's', actions: [{ label: okText, primary: true }] });
  }
}

// ── 抽屉 Drawer ───────────────────────────────────────────────────
// 规范：右侧滑入，高度与窗口等高；小 350 / 中 480 / 大 680；标题区吸顶
// （16px Medium 标题 + 14px N600 描述 + 右上关闭），内容区滚动，操作区
// 吸底可选（按钮右对齐/主操作居右）；模态蒙层 #000 55%。
export class Drawer {
  /** @param {{title?:string, desc?:string, size?:'s'|'m'|'l', width?:number,
   *  footer?:Array<{label:string, primary?:boolean, danger?:boolean, onClick:Function, keep?:boolean}>,
   *  onClose?:Function}} [opts] */
  constructor(opts = {}) {
    this.o = { title: '', desc: '', size: 'm', footer: [], onClose: null, ...opts };
    this.el = null;   // .fb-drawer-wrap（蒙层）
    this.body = null; // 内容区（调用方填充）
    this.layerOff = null; // 浮层栈注销器（ESC 只关最顶层）
  }

  open() {
    this.close();
    const { title, desc, size, width, footer } = this.o;
    const el = document.createElement('div');
    el.className = 'fb-drawer-wrap';
    el.innerHTML =
      `<aside class="fb-drawer fb-${size}" role="dialog" aria-label="${esc(title || t('抽屉'))}"${width ? ` style="width:${width}px"` : ''}>` +
      `<header class="fb-drw-head">` +
      `<div class="fb-drw-tt">${title ? `<strong>${esc(title)}</strong>` : ''}${desc ? `<span>${esc(desc)}</span>` : ''}</div>` +
      '<button type="button" class="fb-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>' +
      '</header>' +
      '<div class="fb-drw-body"></div>' +
      (footer?.length
        ? `<footer class="fb-drw-foot">${footer.map((a, i) =>
            `<button type="button" class="btn${a.primary ? (a.danger ? ' danger armed' : ' gold') : (a.danger ? ' danger' : '')}" data-i="${i}">${esc(a.label)}</button>`).join('')}</footer>`
        : '') +
      '</aside>';
    document.body.appendChild(el);
    this.el = el;
    this.body = el.querySelector('.fb-drw-body');
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      if (ev.target.closest('.fb-x')) { this.close(); return; }
      const btn = ev.target.closest('.fb-drw-foot .btn');
      if (btn) {
        const a = footer[+btn.dataset.i];
        a?.onClick?.();
        if (!a?.keep) this.close();
      }
    });
    this.layerOff = layerPush(() => this.close());
    return this.body;
  }

  close() {
    if (!this.el) return;
    this.layerOff?.();
    this.layerOff = null;
    const el = this.el;
    this.el = null;
    this.body = null;
    el.classList.add('out');
    setTimeout(() => el.remove(), 200);
    this.o.onClose?.();
  }
}

// ── 常驻提示 Notice ───────────────────────────────────────────────
// 规范：非浮层静态提示，始终展现不自动消失，用户可关闭；四态
// info/success/warning/error；图标+标题（可选）+正文+文字按钮（≤2，
// 单行右对齐）+关闭（始终最右，与第一行文本齐平）；通栏或与内容等宽；
// 文本左对齐、建议 ≤4 行；页面内最多两条。
export const Notice = {
  /**
   * 生成常驻提示节点（调用方决定挂哪：站点级 Header 上方 / 页面级
   * 标题下方 / 模块级就近）。
   * @param {{type?:'info'|'success'|'warning'|'error', title?:string, text?:string,
   *  html?:string, actions?:Array<{label:string, onClick:Function}>, closable?:boolean,
   *  onClose?:Function}} [opts]
   * @returns {HTMLElement} .fb-notice 节点（自带 close() 方法）
   */
  create(opts = {}) {
    const { type = 'info', title = '', text = '', html = '', actions = [], closable = true, onClose } = opts;
    const el = document.createElement('div');
    el.className = `fb-notice fb-${type}`;
    el.innerHTML =
      `<span class="fb-n-ico" aria-hidden="true">${LINE_ICON[type] || LINE_ICON.info}</span>` +
      `<div class="fb-n-main">${title ? `<strong>${esc(title)}</strong>` : ''}` +
      (html || `<span>${esc(text)}</span>`) + '</div>' +
      (actions.length
        ? `<span class="fb-n-acts">${actions.slice(0, 2).map((a, i) => `<button type="button" class="fb-n-act" data-i="${i}">${esc(a.label)}</button>`).join('')}</span>`
        : '') +
      (closable ? '<button type="button" class="fb-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>' : '');
    el.close = () => { el.remove(); onClose?.(); };
    el.addEventListener('click', (ev) => {
      if (ev.target.closest('.fb-x')) { el.close(); return; }
      const act = ev.target.closest('.fb-n-act');
      if (act) actions[+act.dataset.i]?.onClick?.();
    });
    return el;
  },

  /** 页面级：插到某容器顶（默认主内容区），返回节点。 */
  page(opts, container) {
    const host = container || document.querySelector('.board:not([hidden])') || document.body;
    const el = Notice.create(opts);
    // 同位最多两条：挤掉最早一条（规范「多个常驻提示展示」）
    const olds = host.querySelectorAll(':scope > .fb-notice');
    if (olds.length >= 2) olds[0].remove();
    host.prepend(el);
    return el;
  },
};

// ── 通知提醒框 Notify ─────────────────────────────────────────────
// 规范：页面右上角（距顶/右 16px），右侧滑入，默认 4s 自动消失（悬停
// 暂停）；图标 24px + 标题（16/24 Medium，1 行）+ 正文（14/22，1-2 行）
// + 按钮（1-2 个，间距 12px）+ 关闭 16px；四色 B500 系统通知 /
// G500 成功 / O500 重要 / R500 紧急（紧急不自动消失，需手动关）。
let __notifyRoot = null;
export const Notify = {
  /** @param {{kind?:'info'|'success'|'warning'|'error', title?:string, text?:string,
   *  actions?:Array<{label:string, onClick:Function}>, duration?:number, sticky?:boolean}} [opts]
   *  @returns {{close:Function}} */
  show(opts = {}) {
    const { kind = 'info', title = '', text = '', actions = [], duration = 4000 } = opts;
    const sticky = opts.sticky !== undefined ? opts.sticky : kind === 'error';
    if (!__notifyRoot) {
      __notifyRoot = document.createElement('div');
      __notifyRoot.id = 'fb-notifies';
      document.body.appendChild(__notifyRoot);
    }
    const el = document.createElement('div');
    el.className = `fb-notify fb-${kind}`;
    el.innerHTML =
      `<span class="fb-ico24" aria-hidden="true">${CIRCLE_ICON[kind] || CIRCLE_ICON.info}</span>` +
      `<div class="fb-ny-main">${title ? `<strong>${esc(title)}</strong>` : ''}${text ? `<span>${esc(text)}</span>` : ''}` +
      (actions.length
        ? `<div class="fb-ny-acts">${actions.slice(0, 2).map((a, i) =>
            `<button type="button" class="btn small${i === 0 ? ' gold' : ''}" data-i="${i}">${esc(a.label)}</button>`).join('')}</div>`
        : '') + '</div>' +
      '<button type="button" class="fb-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>';
    __notifyRoot.appendChild(el);
    while (__notifyRoot.children.length > 5) __notifyRoot.firstElementChild.remove();

    const handle = {
      close() {
        clearTimeout(el.__t);
        el.classList.add('out');
        setTimeout(() => el.remove(), 200);
      },
    };
    el.addEventListener('click', (ev) => {
      if (ev.target.closest('.fb-x')) { handle.close(); return; }
      const act = ev.target.closest('.fb-ny-acts .btn');
      if (act) { actions[+act.dataset.i]?.onClick?.(); handle.close(); }
    });
    // 悬停暂停 / 离开续计（同 toast 规则）
    el.addEventListener('mouseenter', () => {
      clearTimeout(el.__t);
      el.__left -= Date.now() - el.__start;
    });
    el.addEventListener('mouseleave', () => {
      if (!sticky) {
        clearTimeout(el.__t);
        el.__t = setTimeout(handle.close, Math.max(el.__left, 800));
      }
    });
    if (!sticky) { el.__left = duration; el.__start = Date.now(); el.__t = setTimeout(handle.close, duration); }
    return handle;
  },
};

// ── 加载中 Spin / 蒙层加载 ────────────────────────────────────────
// 规范：Spin 小 24px（局部/横向可带描述）、大 40px（全局/纵向带描述），
// 色 B500，容器内居中；蒙层加载＝白色 N00 60% + Spin 居中；可选延迟
// 显示（建议 ≥1s，内容先到则不闪）；骨架屏已有（skimmer 系），不重复。
export const Spin = {
  /** 转圈标记串。@param {number} [size] 24|40（4N 规则） @param {string} [text] 描述（14px N600） */
  html(size = 24, text = '') {
    const r = (size - 2) / 2; // 留 1px 出血
    return `<span class="fb-spin fb-s${size}" role="status" aria-label="${esc(text ? t('{a}：{b}', { a: t('加载中'), b: text }) : t('加载中'))}">` +
      `<svg viewBox="0 0 ${size} ${size}" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.8">` +
      `<circle cx="${size / 2}" cy="${size / 2}" r="${r}" opacity=".18"/>` +
      `<path d="M ${size / 2} 1 A ${r} ${r} 0 0 1 ${size / 2 + r} ${size / 2}" stroke-linecap="round"/></svg>` +
      (text ? `<span>${esc(text)}</span>` : '') + '</span>';
  },
};

export const Loading = {
  /**
   * 蒙层加载：盖在 target（缺省整页）上，白色 60% + 大 Spin + 描述。
   * 返回 hide()；delay 内 hide 则从未上屏（规范：延迟显示，内容先到不闪）。
   * @param {HTMLElement} [target] @param {{text?:string, delay?:number}} [opts]
   */
  overlay(target, opts = {}) {
    const { text = '', delay = 1000 } = opts;
    let el = null;
    const timer = setTimeout(() => {
      el = document.createElement('div');
      el.className = 'fb-loading';
      el.innerHTML = `<div class="fb-loading-card">${Spin.html(40, text)}</div>`; // xss-ok: Spin.html 内部 esc(text)
      (target || document.body).appendChild(el);
    }, delay);
    return {
      hide() {
        clearTimeout(timer);
        el?.remove();
      },
    };
  },
};

// ── 进度条 Progress ───────────────────────────────────────────────
// 规范：线性＝高 4px、圆角 2px、百分比与条间距 12px；可确定（0→100 填
// 充，常态 B500/成功 G500/失败 R500）与不可确定（指示器沿轨道往复）。
// 圆形＝16px 圈 + 12px 百分比（默认在左，间距 8px）。
export const Progress = {
  /**
   * 线性进度条标记串。
   * @param {number|null} pct 0-100；null/undefined＝不可确定
   * @param {{status?:'active'|'success'|'error', showPct?:boolean, color?:string}} [opts]
   */
  line(pct, opts = {}) {
    const { status = 'active', showPct = true, color } = opts;
    const v = Math.max(0, Math.min(100, Math.round(pct ?? 0)));
    const fill = color || (status === 'success' ? 'var(--ok)' : status === 'error' ? 'var(--bad)' : 'var(--gold)');
    const ind = pct === null || pct === undefined;
    return `<span class="fb-progress${ind ? ' indet' : status !== 'active' ? ` ${status}` : ''}">` +
      `<span class="fb-p-track"><i style="${ind ? '' : `width:${v}%;background:${fill};`}"></i></span>` +
      (showPct && !ind ? `<b>${v}%</b>` : '') + '</span>';
  },

  /** 圆形进度标记串（16px 圈，百分比默认在左侧、间距 8px）。 */
  ring(pct, opts = {}) {
    const { size = 16, showPct = true } = opts;
    const v = Math.max(0, Math.min(100, Math.round(pct ?? 0)));
    const R = 6.5, C = 2 * Math.PI * R;
    return `<span class="fb-ring">` +
      (showPct ? `<b>${v}%</b>` : '') +
      `<svg viewBox="0 0 16 16" width="${size}" height="${size}" fill="none" aria-hidden="true">` +
      `<circle cx="8" cy="8" r="${R}" stroke="rgba(31,35,41,.1)" stroke-width="1.8"/>` +
      `<circle cx="8" cy="8" r="${R}" stroke="var(--gold)" stroke-width="1.8" stroke-linecap="round"` +
      ` stroke-dasharray="${(C * v / 100).toFixed(2)} ${C.toFixed(2)}" transform="rotate(-90 8 8)"/></svg></span>`;
  },
};

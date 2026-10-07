// guide.js — t_159（r_11）：首次观察引导层。三步气泡（点成员看名片/
// 点物件有反馈/滚轮凑近看）＋跳过钮；完成标记走 setPref（localStorage），
// 设置窗·关于页「再看一遍」重置标记。纯 DOM 覆盖层——与设置窗同语言
// （浮层栈/遮罩/Esc 语义），不碰 canvas 渲染链。

import { setPref, prefs } from './settings.js';
import { layerPush } from './feedback.js'; // 返回注销函数（栈是 close-回调制）
import { esc } from './dom.js';
import { t } from './i18n.js';

const KEY = 'guideSeen';

// 三步：文案＋指向画布区域的大致方位（气泡锚在场景外的固定边——
// 办公室布局是固定的，指个方向比算坐标稳）
const STEPS = [
  {
    title: t('点牛马看名片'),
    tip: t('办公室里走动的小牛马都能点——名片里有他的岗位、任务和等级。'),
    anchor: 'left', // 成员大多在场景中部活动，气泡挂左侧指向中央
  },
  {
    title: t('点物件有反馈'),
    tip: t('打印机、咖啡机、垃圾桶、零食架……家具多数能点，试试看。'),
    anchor: 'right', // 茶水间在东侧
  },
  {
    title: t('滚轮凑近看'),
    tip: t('滚轮缩放（1–3 倍，以鼠标为锚）、拖拽平移、双击空白处或点框角「复位」小钮归位——像素办公室值得凑近看。'),
    anchor: 'bottom', // 相机是全局操作，挂底部
  },
];

export class FirstGuide {
  /** @param {HTMLElement} host 引导层挂载的宿主（body 即可） */
  constructor(host) {
    this.host = host || document.body;
    this.el = null;
    this.step = 0;
    this.onDone = null;
  }

  /** 首次进办公室时启动（看过就跳过）。force 供「再看一遍」用。 */
  maybeStart(force = false) {
    if (!force && prefs()[KEY]) return false;
    this.start();
    return true;
  }

  start() {
    this.step = 0;
    this.render();
    return true;
  }

  render() {
    this.teardown(false);
    const s = STEPS[this.step];
    if (!s) { this.finish(); return; }
    const total = STEPS.length;
    const el = document.createElement('div');
    el.className = 'guide-layer';
    el.innerHTML =
      '<div class="guide-dim" data-guide-dim></div>' +
      `<div class="guide-bubble guide-at-${s.anchor}">` +
        `<div class="guide-t"><b>${s.title}</b><span class="guide-n">${this.step + 1}/${total}</span></div>` +
        `<p class="guide-tip">${s.tip}</p>` +
        '<div class="guide-actions">' +
          (this.step > 0 ? '<button type="button" class="btn small" data-guide-prev>' + esc(t('上一步')) + '</button>' : '') +
          (this.step < total - 1
            ? '<button type="button" class="btn small" data-guide-next>' + esc(t('下一步')) + '</button>'
            : '<button type="button" class="btn small gold" data-guide-done>' + esc(t('完成了')) + '</button>') +
          '<button type="button" class="btn small" data-guide-skip>' + esc(t('跳过')) + '</button>' +
        '</div>' +
      '</div>';
    this.host.appendChild(el);
    this.el = el;
    // Esc 走浮层栈（close-回调制）：这层 Esc＝跳过；翻步时旧层注销重入
    this.unlayer = layerPush(() => this.finish());
    el.querySelector('[data-guide-next]')?.addEventListener('click', () => { this.step++; this.render(); });
    el.querySelector('[data-guide-prev]')?.addEventListener('click', () => { this.step--; this.render(); });
    el.querySelector('[data-guide-done]')?.addEventListener('click', () => this.finish());
    el.querySelector('[data-guide-skip]')?.addEventListener('click', () => this.finish());
    el.querySelector('[data-guide-dim]')?.addEventListener('click', () => this.finish());
  }

  /** 收尾：标记已看＋清层＋出栈。teardown(false) 供翻步重渲（不写标记）。 */
  finish() {
    setPref(KEY, true);
    this.teardown();
    this.onDone?.();
  }

  /** 中途离场（人去了别的板块）：只收层出栈，不写「看过」——下次进
   *  办公室接着弹（记账是 finish/跳过的事，别替人做决定）。 */
  dismiss() {
    this.teardown();
  }

  teardown() {
    if (this.unlayer) { this.unlayer(); this.unlayer = null; } // 出浮层栈
    if (this.el) { this.el.remove(); this.el = null; }
  }
}

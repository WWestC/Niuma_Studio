// fold.js — 长内容自动折叠（飞书式）的共用词汇：对话气泡、工作汇报、
// 事件通知卡（黑板 diff、纯文本播报）、任务卡描述，同一套折叠语言——
// 正文量高超过折叠线（约 6 行）才收起为渐隐预览＋「展开」，点开翻
// 「收起」；短内容零铬面（不加类、钮不显形）。独立纯模块：量高只读
// scrollHeight/classList，不碰 DOM 树结构，node:test 直测。
//
// DOM 契约（一处定形，CSS/量高/翻转都只认这一层）：
//   .mc-foldzone > .mc-body（裁剪目标） + .mc-fold[data-mcfold]（触发钮）
// 带尾件（tail）时钮与尾件同住区脚一行 .mc-foot（展开居左、尾件居右，
// 如黑板卡的「看全文」）；无尾件保持裸钮旧形。zone 可以包在气泡、通知
// 卡、任务描述里——宿主自己的排版（.msg-text/.nc-body/.tk-desc 的字号
// 行高）在 .mc-body 内部照常生效。

import { icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

/** 量高线（px）：正文高于此值（约 6 行）才折叠。与预览高 132px（CSS）
 *  留一行余量——量高恰在线上的内容收起后视觉无跳变。 */
export const FOLD_LIMIT = 140;

/** 折叠区的 HTML 形状。触发钮默认文案「展开」——CSS 保证未定折
 *  （无 .mc-foldable）时钮不显形，这里的文案只是定折后的初值。
 *  tail 是与触发钮同排的区脚 HTML（如黑板卡的「看全文」钮）——展开
 *  居左、尾件居右；短文不折（钮不显形）时尾件独自居右。省则保持裸钮
 *  旧形，既有宿主零感知。
 *  @param {string} inner 正文 HTML（已转义/已渲染）
 *  @param {string} [tail] 区脚同行 HTML（可省） */
export function foldZoneHTML(inner, tail = '') {
  const btn = '<button type="button" class="mc-fold" data-mcfold>' + t('展开') + ' ' + icon('down', 10) + '</button>';
  return '<div class="mc-foldzone"><div class="mc-body">' + inner + '</div>' +
    (tail ? '<div class="mc-foot">' + btn + tail + '</div>' : btn) + '</div>';
}

/** 量高定折（一个 zone）：scrollHeight 超线才挂折叠态。幂等——
 *  已定折（.mc-foldable 在场，含用户手动展开过的）不再改判；图片
 *  后到撑高时重跑只补新判，不翻用户的旧账。
 *  @param {Element|null} zone @returns {boolean} 是否新挂了折叠 */
export function wireFoldZone(zone) {
  if (!zone || !zone.classList || zone.classList.contains('mc-foldable')) return false;
  const body = zone.querySelector('.mc-body');
  const btn = zone.querySelector('.mc-fold');
  if (!body || !btn) return false;
  if ((body.scrollHeight || 0) > FOLD_LIMIT) {
    zone.classList.add('mc-foldable', 'folded');
    btn.innerHTML = t('展开') + ' ' + icon('down', 10);
    return true;
  }
  return false;
}

/** 量高定折（一片范围）：行/容器落 DOM 后跑一遍。 */
export function wireFoldZones(root) {
  if (!root || !root.querySelectorAll) return;
  for (const z of root.querySelectorAll('.mc-foldzone')) wireFoldZone(z);
}

/** 触发钮的翻转：所在 zone 翻折叠态并换文案。
 *  @param {Element} btn @returns {boolean} 翻转后的折叠态 */
export function toggleFoldBtn(btn) {
  const zone = btn.closest('.mc-foldzone');
  if (!zone) return false;
  const folded = zone.classList.toggle('folded');
  btn.innerHTML = folded ? t('展开') + ' ' + icon('down', 10) : t('收起') + ' ' + icon('up', 10);
  return folded;
}

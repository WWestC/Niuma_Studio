// breakpoint.js — 飞书「设计应用」响应式断点、XS 导航抽屉与侧栏折叠。
//
// 断点表（响应式设计指南）：XS 0-599 / S 600-1023 / M 1024-1439 /
// L 1440-1919 / XL ≥1920。当前档常驻写在 <html data-bp="xs|s|m|l|xl">，
// CSS 属性选择器与各板块 JS 都读它；换档时在 document 上派发
// 'bpchange' 事件（detail.bp 为新档）。
//
// #nav-toggle（顶栏 ☰）按档分两职（ZCode 式同一颗钮）：
//   · XS：侧栏收进抽屉，☰ 开合 html[data-nav-open]，遮罩
//     #nav-backdrop 惰性创建；切板块、Esc、点遮罩、断点升出 XS
//     都收抽屉（指南·响应式导航：隐藏 → icon 触发侧边抽屉）；
//   · S+：折叠/展开侧栏 html[data-side-collapsed]（桌面折成 56px 图标
//     轨——VSCode 式活动栏，图标常驻可点、hover 浮展开；S 档折叠连图
//     标轨一并让出），状态落 localStorage 跨重启保持。aria/文案随档随
//     态换。

import { t } from './i18n.js';

const QUERIES = [
  ['xs', '(max-width: 599.98px)'],
  ['s', '(min-width: 600px) and (max-width: 1023.98px)'],
  ['m', '(min-width: 1024px) and (max-width: 1439.98px)'],
  ['l', '(min-width: 1440px) and (max-width: 1919.98px)'],
  ['xl', '(min-width: 1920px)'],
];

const root = document.documentElement;
const mqls = QUERIES.map(([bp, q]) => [bp, matchMedia(q)]);

function apply() {
  for (const [bp, mq] of mqls) {
    if (!mq.matches) continue;
    if (root.dataset.bp !== bp) {
      root.dataset.bp = bp;
      root.dispatchEvent(new CustomEvent('bpchange', { detail: { bp } }));
    }
    return;
  }
}
for (const [, mq] of mqls) mq.addEventListener('change', apply);
// 部分内嵌 webview 不派发 matchMedia change（gantt.js 同款怪癖）——
// resize 事件与 ResizeObserver（观察根元素尺寸）双路兜底，多路同到
// 时 apply 幂等
window.addEventListener('resize', apply);
new ResizeObserver(apply).observe(document.documentElement);
apply();

// ---- 顶栏 ☰：XS 导航抽屉 / S+ 侧栏折叠 ----
const toggle = document.getElementById('nav-toggle');
const COLLAPSE_KEY = 'dh.side.collapsed';
const collapsed = () => root.hasAttribute('data-side-collapsed');

/** ☰ 的无障碍文案随档随态换（抽屉的「打开导航」/折叠的「展开/缩起
 *  侧栏」）。同步维护 data-i-title/-aria 为当前中文键——applyStaticI18n
 *  （语言初始化/切换）按这俩属性重翻，不同步就会被静态键盖回旧文案。 */
function syncToggleAria() {
  if (!toggle) return;
  const key = root.dataset.bp === 'xs' ? '打开导航'
    : collapsed() ? '展开侧边栏' : '缩起侧边栏';
  toggle.title = t(key);
  toggle.setAttribute('aria-label', t(key));
  toggle.setAttribute('data-i-title', key);
  toggle.setAttribute('data-i-aria', key);
  toggle.setAttribute('aria-expanded',
    root.dataset.bp === 'xs' ? (root.hasAttribute('data-nav-open') ? 'true' : 'false') : 'true');
}

function closeNav() {
  root.removeAttribute('data-nav-open');
  syncToggleAria();
}

function openNav() {
  let backdrop = document.getElementById('nav-backdrop');
  if (!backdrop) {
    backdrop = document.createElement('div');
    backdrop.id = 'nav-backdrop';
    backdrop.addEventListener('click', closeNav);
    document.body.appendChild(backdrop);
  }
  root.setAttribute('data-nav-open', '');
  syncToggleAria();
}

/** S+ 的侧栏折叠翻转（状态落盘）。 */
function toggleCollapse() {
  root.toggleAttribute('data-side-collapsed');
  try { localStorage.setItem(COLLAPSE_KEY, collapsed() ? '1' : '0'); } catch { /* 私密模式：内存态仍生效 */ }
  syncToggleAria();
}

if (toggle) {
  toggle.addEventListener('click', () => {
    if (root.dataset.bp === 'xs') {
      if (root.hasAttribute('data-nav-open')) closeNav();
      else openNav();
    } else {
      toggleCollapse();
    }
  });
}
root.addEventListener('bpchange', (ev) => {
  if (ev.detail.bp !== 'xs') {
    closeNav(); // 升出 XS 收抽屉
    // 折叠态按落盘值复位（进 xs 期间属性被摘过——抽屉档不吃折叠，
    // 否则 display:none 连抽屉一起吞，☰ 点了没反应）
    try {
      root.toggleAttribute('data-side-collapsed', localStorage.getItem(COLLAPSE_KEY) === '1');
    } catch { /* 读不了就保持现状 */ }
  } else {
    root.removeAttribute('data-side-collapsed');
  }
  syncToggleAria(); // 档换了，☰ 的职责与文案跟着换
});
window.addEventListener('hashchange', closeNav); // 选中板块即收抽屉
window.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeNav(); });

// 折叠态回流（跨重启保持）；xs 不适用（抽屉档自己的开合不持久）
try {
  if (root.dataset.bp !== 'xs' && localStorage.getItem(COLLAPSE_KEY) === '1') {
    root.setAttribute('data-side-collapsed', '');
  }
} catch { /* 读不了就当没折过 */ }
syncToggleAria();

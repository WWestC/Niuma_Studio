// selectmenu.js — 原生 select 弹层接管（Select 篇补章）。桌面内嵌
// WebView 里 <select> 的下拉是系统级菜单（macOS NSMenu）：跟随系统
// 深色模式渲染成深色半透玻璃——页面 color-scheme: light 够不到系
// 统层——还贴着控件盖开遮住自身，与飞书风浅色界面割裂。这里在细
// 指针环境用 document 委托接管「弹开」这一步：mousedown preventDefault
// 掐掉原生弹层（焦点被一并掐掉，自己补），改弹自绘菜单——飞书弹层
// 语言（board-3 白卡＋描边＋S 影＋riseIn），短清单贴锚下方留缝、底部
// 空间不足翻上方；长清单（放不进宽裕侧）升全高面板（原生长菜单形
// 态，盖过锚点换可见行数）。select 本体原样保留：外观
// （.input 系）、焦点环、change 监听、程序化赋值全走原路。粗指针
// （触屏）不接管——手机原生滚轮/选择器本就是对的。
//
// 键盘：打开键（Space/Enter/↑↓）进菜单路由（↑↓ 跳禁用、Enter 确
// 认、Esc 吃键不外漏——浮层「Esc 关页」的手别伸进来、Tab 收场放
// 行）；未打开时放行原生字母快选。optgroup 渲染成分组标题
// （recruit / people detail 的模型选择器）。外点/滚动/缩放/失焦
// 收场（clockpop 同款纪律：固定面板不许悬离锚点）。

import { esc, icon } from './dom.js';

// —— 数据面（纯函数，dom 冒烟可测）———————————————————————

/** fitMenu——place 的几何决策（纯函数化供冒烟）：锚行 viewport 坐标
 *  （top/bottom）、视口高 vh、弹层自然高 mh → 落位 {top, maxH}。三档：
 *   ① 放得下就贴锚（下方优先，翻上为辅——短清单的原生手感）；
 *   ② 两侧都放不下且全高面板明显更宽（full > avail+64）——长清单升
 *     全高面板（M..vh−M，盖过锚点）：原生长菜单形态——模型档这类
 *     20+ 行的清单贴锚开只能露几行，全高把可见行数拉满视口，「看全」
 *     优先于「不遮锚」（Esc/外点收场照旧，盖住的锚可再点行提交）；
 *   ③ 其余——占满宽裕侧（96px 兜底，但不越视口：越界回夹）。
 *  视口边距 M=8、锚缝 GAP=4 与 place() 同源。 */
const EDGE = 8, SEAM = 4;

export function fitMenu(mh, top, bottom, vh) {
  const M = EDGE, GAP = SEAM;
  const below = vh - bottom - GAP - M;
  const above = top - GAP - M;
  if (mh <= below) return { top: bottom + GAP, maxH: mh };   // ① 下方放得下
  if (mh <= above) return { top: top - GAP - mh, maxH: mh }; // ① 翻上方放得下
  const avail = Math.max(below, above, 0);
  const full = Math.max(vh - 2 * M, 0);
  if (full > avail + 64) return { top: M, maxH: Math.min(mh, full) }; // ②
  const maxH = Math.min(mh, Math.max(avail, 96));                  // ③
  let y = above > below ? top - GAP - maxH : bottom + GAP;
  if (y + maxH > vh - M) y = Math.max(M, vh - M - maxH); // 兜底高度不越视口
  if (y < M) y = M;
  return { top: y, maxH };
}

/** options 平铺成菜单项：optgroup 首项前插分组头，选中随
 *  selectedIndex。sel 只需长得像 select（options 数组＋selectedIndex，
 *  选项带 label/value/disabled/parentNode）。 */
export function itemsOf(sel) {
  const items = [];
  let lastPg = null;
  for (let i = 0; i < sel.options.length; i++) {
    const o = sel.options[i];
    const pg = o.parentNode && o.parentNode.tagName === 'OPTGROUP' ? o.parentNode : null;
    if (pg !== lastPg) {
      if (pg) items.push({ type: 'group', label: pg.label });
      lastPg = pg;
    }
    items.push({
      type: 'opt', label: o.label, value: o.value, index: i,
      disabled: !!o.disabled, selected: i === sel.selectedIndex,
    });
  }
  return items;
}

/** 菜单内层 HTML（.sel-pop 的孩子）：分组头 sel-grp、选项行 sel-opt
 *  （选中 is-sel 带 ✓、禁用 is-dis）。esc 是唯一注入面。 */
export function rowsHTML(items) {
  return items.map((it) => {
    if (it.type === 'group') return `<div class="sel-grp">${esc(it.label)}</div>`;
    const cls = `sel-opt${it.selected ? ' is-sel' : ''}${it.disabled ? ' is-dis' : ''}`;
    return `<div class="${cls}" role="option"${it.selected ? ' aria-selected="true"' : ''}` +
      `${it.disabled ? ' aria-disabled="true"' : ''}>` +
      `<span class="sel-chk">${it.selected ? icon('check', 14) : ''}</span>` +
      `<span class="sel-lb">${esc(it.label)}</span></div>`;
  }).join('');
}

// —— 交互壳（真 DOM＋细指针才安装）——————————————————————

const FINE = typeof matchMedia === 'function' && matchMedia('(pointer: fine)').matches;

let menu = null;   // 弹层单例（懒建，常驻 body；display 开合重播 riseIn）
let owner = null;  // 弹层正服务着的 select
let rows = [];     // [{ el, optIndex, disabled }]——与菜单行同序
let index = -1;    // 键盘/悬停活动行（rows 下标）

function ensureMenu() {
  if (menu) return menu;
  menu = document.createElement('div');
  menu.className = 'sel-pop';
  menu.setAttribute('role', 'listbox');
  menu.addEventListener('mousedown', (e) => e.preventDefault()); // 选行不夺焦点
  document.body.appendChild(menu);
  return menu;
}

function setIndex(j, reveal = true) {
  if (rows[index]) rows[index].el.classList.remove('is-act');
  index = j;
  if (!rows[j]) return;
  rows[j].el.classList.add('is-act');
  if (reveal) rows[j].el.scrollIntoView({ block: 'nearest' });
}

function move(step) {
  if (!rows.length) return;
  let j = index;
  for (let n = 0; n < rows.length; n++) {
    j = (j + step + rows.length) % rows.length;
    if (!rows[j].disabled) { setIndex(j); return; }
  }
}

/** 首末可选项（Home/End）；PageUp/PageDown 走 move 十连跳。 */
function jump(dir) {
  const j = dir > 0 ? rows.findIndex((r) => !r.disabled)
    : rows.length - 1 - [...rows].reverse().findIndex((r) => !r.disabled);
  if (j >= 0) setIndex(j);
}

function open(sel) {
  owner = sel;
  menu = ensureMenu();
  const items = itemsOf(sel);
  menu.innerHTML = rowsHTML(items);
  const opts = items.filter((it) => it.type === 'opt');
  rows = [...menu.querySelectorAll('.sel-opt')].map((el, j) => {
    const rec = { el, optIndex: opts[j].index, disabled: opts[j].disabled };
    el.addEventListener('mouseenter', () => { if (!rec.disabled) setIndex(j, false); });
    el.addEventListener('click', () => { if (!rec.disabled) commit(j); });
    return rec;
  });
  place();
  const cur = opts.findIndex((it) => it.selected);
  setIndex(cur >= 0 ? cur : rows.findIndex((r) => !r.disabled));
}

/** 几何（fitMenu 的 DOM 面）：量自然高前先复位 maxHeight——上一次
 *  开层留下的行内值会把这次的量高夹矮。水平照旧：左缘贴锚、视口内
 *  夹持，最小宽＝锚宽。 */
function place() {
  const r = owner.getBoundingClientRect();
  const M = 8;
  const vw = window.innerWidth, vh = window.innerHeight;
  menu.style.visibility = 'hidden';
  menu.style.display = 'block';
  menu.style.maxHeight = 'none';
  const mw = menu.offsetWidth, mh = menu.offsetHeight;
  const { top, maxH } = fitMenu(mh, r.top, r.bottom, vh);
  menu.style.maxHeight = maxH + 'px';
  menu.style.left = Math.min(Math.max(M, r.left), Math.max(M, vw - M - mw)) + 'px';
  menu.style.top = top + 'px';
  menu.style.minWidth = Math.max(r.width, 96) + 'px';
  menu.style.visibility = '';
}

function commit(j) {
  const rec = rows[j];
  if (!owner || !rec || rec.disabled) return;
  if (rec.optIndex !== owner.selectedIndex) {
    owner.selectedIndex = rec.optIndex;
    // 原生同款事件面：先 input 后 change（既有 change 监听全数照收）
    owner.dispatchEvent(new Event('input', { bubbles: true }));
    owner.dispatchEvent(new Event('change', { bubbles: true }));
  }
  close();
  owner.focus({ preventScroll: true });
}

function close() {
  if (!owner) return;
  owner = null; rows = []; index = -1;
  if (menu) menu.style.display = 'none';
}

function onDown(e) {
  const t = e.target;
  const sel = t && t.closest ? t.closest('select') : null;
  if (owner) {
    if (sel === owner) { e.preventDefault(); close(); return; }  // 再点控件＝收
    if (sel && !sel.disabled && !sel.multiple) {                 // 换控件＝直开
      e.preventDefault();
      sel.focus({ preventScroll: true });
      open(sel);
      return;
    }
    if (!menu || !menu.contains(t)) close();                     // 外点收场
    return;
  }
  if (!sel || sel.disabled || sel.multiple) return;
  e.preventDefault();                                            // 掐原生弹层
  sel.focus({ preventScroll: true });                            // ＋补焦点
  open(sel);
}

const OPENERS = ['ArrowDown', 'ArrowUp', ' ', 'Enter'];

function onKey(e) {
  if (owner) {
    switch (e.key) {
      case 'ArrowDown': e.preventDefault(); move(1); return;
      case 'ArrowUp': e.preventDefault(); move(-1); return;
      case 'Home': e.preventDefault(); jump(1); return;
      case 'End': e.preventDefault(); jump(-1); return;
      case 'PageUp': e.preventDefault(); for (let i = 0; i < 10; i++) move(-1); return;
      case 'PageDown': e.preventDefault(); for (let i = 0; i < 10; i++) move(1); return;
      case 'Enter': case ' ': e.preventDefault(); commit(index); return;
      case 'Escape': e.preventDefault(); e.stopImmediatePropagation(); close(); return;
      case 'Tab': close(); return;  // 收场放行——焦点照常走
      default: return;              // 其余不拦（字母快选等）
    }
  }
  const sel = e.target && e.target.tagName === 'SELECT' ? e.target : null;
  if (!sel || sel.disabled || sel.multiple || !OPENERS.includes(e.key)) return;
  e.preventDefault();
  open(sel);
}

function install() {
  document.addEventListener('mousedown', onDown, true);
  document.addEventListener('keydown', onKey, true);
  // 固定面板不许悬离锚点（clockpop 同款）：滚动/缩放/失焦一律收场；
  // 菜单自身的内部滚动不算（capture 先于菜单收到，按落点判）
  document.addEventListener('scroll', (e) => {
    if (owner && menu && !menu.contains(e.target)) close();
  }, true);
  window.addEventListener('resize', close);
  window.addEventListener('blur', close);
}

if (FINE) install();

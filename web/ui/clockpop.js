// clockpop.js — the room-time popover (气泡卡片篇). Born as the sidebar
// clock capsule's panel; the capsule row retired with the ident-row
// rework (§4.1④ now carries the owner card + the settings gear only),
// so the panel has two callers: the settings card's 房间时间 row (an
// element anchor) and the office wall clock (a rect anchor the canvas
// board derives off its scene fit). The panel itself is unchanged
// craft: an analog dial (秒针描金、走秒缓动、单调累加不回卷) + big
// time + full date + the server-alignment verdict, closed by outside
// click / Esc (it eats the key before anything below) / scroll / resize
// — a fixed panel must not float off its anchor.

import { dismiss } from './dom.js';
import { t, localeTag } from './i18n.js';
import { serverSkew } from '../wire/api.js';

const WD = ['日', '一', '二', '三', '四', '五', '六'];

/** The room's now: the local clock plus the skew api.js keeps sampling
 *  off response Date headers (0 until the first sample → 本机时间). */
export function roomNow() {
  return new Date(Date.now() + serverSkew());
}

/** 对齐态：skew 未采样＝警（暂显本机时间）；已采样＝差值句 */
export function clockSyncInfo() {
  const skew = serverSkew();
  if (!skew) return { warn: true, text: t('尚未对齐服务器时钟 · 暂显本机时间') };
  const s = Math.abs(skew) / 1000;
  return s >= 1
    ? { warn: false, text: t('已对齐服务器时钟（本机{dir} {s} 秒）', { dir: skew > 0 ? t('慢') : t('快'), s: s.toFixed(1) }) }
    : { warn: false, text: t('已与服务器时钟对齐') };
}

let pop = null, popT = 0, secBase = null, lastSec = -1, anchor = null;

// the anchor wears two shapes: an HTMLElement (the settings card's
// 房间时间 row — geometry off getBoundingClientRect, hits off DOM
// containment) or { box(), hit(ev) } (the wall clock — a viewport rect
// the canvas board recomputes, hits re-run the scene hit test)
const aBox = () => (anchor.box ? anchor.box() : anchor.getBoundingClientRect());
const aHit = (ev) => (anchor.box ? anchor.hit(ev) : anchor && anchor.contains(ev.target));

const paint = () => {
  const d = roomNow();
  pop.querySelector('.cp-time').textContent = d.toLocaleTimeString(localeTag(), { hour12: false });
  pop.querySelector('.cp-date').textContent =
    t('{y}年{m}月{d}日 · 星期{w}', { y: d.getFullYear(), m: d.getMonth() + 1, d: d.getDate(), w: t(WD[d.getDay()]) });
  const s = d.getSeconds(), m = d.getMinutes(), h = d.getHours() % 12;
  if (lastSec < 0) secBase = s * 6;                       // 首帧定基
  else if (s !== lastSec) secBase += ((s - lastSec + 60) % 60) * 6; // 只进不回卷
  lastSec = s;
  pop.querySelector('.cp-s').style.transform = `rotate(${secBase}deg)`;
  pop.querySelector('.cp-m').style.transform = `rotate(${m * 6 + s * 0.1}deg)`;
  pop.querySelector('.cp-h').style.transform = `rotate(${h * 30 + m * 0.5}deg)`;
  const sync = clockSyncInfo();
  pop.querySelector('.cp-dot').classList.toggle('warn', sync.warn);
  pop.querySelector('.cp-sync span').textContent = sync.text;
};

// 与锚点居中、页边安全距 16；时钟贴屏底，默认翻在上方
const place = () => {
  const a = aBox();
  const W = 248, margin = 16, gap = 8;
  const h = pop.offsetHeight || 150;
  let left = a.left + a.width / 2 - W / 2;
  left = Math.max(margin, Math.min(left, innerWidth - W - margin));
  const above = a.top - h - gap >= margin;
  pop.classList.add(above ? 'cp-above' : 'cp-below');
  pop.style.left = `${left}px`;
  pop.style.top = above ? `${a.top - h - gap}px` : `${Math.min(a.bottom + gap, innerHeight - h - margin)}px`;
  pop.style.setProperty('--cp-arrow',
    `${Math.max(12, Math.min(a.left + a.width / 2 - left, W - 12))}px`);
};

const onDoc = (ev) => { if (!pop.contains(ev.target) && !aHit(ev)) closeClockPop(); };
const onKey = (ev) => {
  if (ev.key !== 'Escape' || !pop) return;
  ev.stopImmediatePropagation();
  closeClockPop();
};
const onMove = () => closeClockPop(); // fixed 面板不跟锚点走，滚动/缩放即收

export function closeClockPop() {
  if (!pop) return;
  clearInterval(popT);
  dismiss(pop, 150);
  pop = null; secBase = null; lastSec = -1; anchor = null;
  document.removeEventListener('mousedown', onDoc, true);
  document.removeEventListener('keydown', onKey, true);
  removeEventListener('resize', onMove);
  window.removeEventListener('scroll', onMove, true);
}

/** Open the time panel at an anchor — the settings card's 房间时间 row
 *  (an element) or the office wall clock ({ box(), hit(ev) }).
 *  @param {HTMLElement|{box:Function,hit:Function}} forAnchor what the arrow points at */
export function openClockPop(forAnchor) {
  closeClockPop();
  anchor = forAnchor;
  // 表盘：12 刻度（四向主刻度加深），时针/分针墨色、秒针描金
  let ticks = '';
  for (let i = 0; i < 12; i++) {
    const major = i % 3 === 0, r1 = major ? 21.5 : 23, r2 = 24.5;
    const a = i * Math.PI / 6;
    ticks += `<line x1="${(28 + Math.sin(a) * r1).toFixed(1)}" y1="${(28 - Math.cos(a) * r1).toFixed(1)}"` +
      ` x2="${(28 + Math.sin(a) * r2).toFixed(1)}" y2="${(28 - Math.cos(a) * r2).toFixed(1)}"` +
      ` stroke="${major ? 'var(--chalk-faint)' : 'var(--line-2)'}" stroke-width="${major ? 1.6 : 1.2}"/>`;
  }
  pop = document.createElement('div');
  pop.className = 'clock-pop';
  pop.setAttribute('role', 'dialog');
  pop.setAttribute('aria-label', t('房间时间'));
  pop.innerHTML =
    '<div class="cp-body">' +
    `<svg class="cp-face" viewBox="0 0 56 56" width="56" height="56" aria-hidden="true">` +
    '<circle cx="28" cy="28" r="26.5" fill="var(--board-2)" stroke="var(--line-2)"/>' + ticks +
    '<line class="cp-hand cp-h" x1="28" y1="30.5" x2="28" y2="18" stroke="var(--chalk)" stroke-width="2.6" stroke-linecap="round"/>' +
    '<line class="cp-hand cp-m" x1="28" y1="31.5" x2="28" y2="12.5" stroke="var(--chalk)" stroke-width="1.8" stroke-linecap="round"/>' +
    '<line class="cp-hand cp-s" x1="28" y1="33" x2="28" y2="10" stroke="var(--gold)" stroke-width="1.2" stroke-linecap="round"/>' +
    '<circle cx="28" cy="28" r="2" fill="var(--chalk)"/>' +
    '<circle cx="28" cy="28" r=".8" fill="#fff"/>' +
    '</svg>' +
    '<div class="cp-main"><div class="cp-time"></div><div class="cp-date"></div></div>' +
    '</div>' +
    '<div class="cp-sync"><i class="cp-dot"></i><span></span></div>';
  document.body.appendChild(pop);
  paint();
  place();
  popT = setInterval(paint, 1000);
  document.addEventListener('mousedown', onDoc, true);
  document.addEventListener('keydown', onKey, true);
  addEventListener('resize', onMove);
  window.addEventListener('scroll', onMove, true);
}

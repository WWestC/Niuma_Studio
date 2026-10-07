// gantt.js — the schedule gantt (v2 P4-d, orchestration §4 / PRD
// §4.2 甘特段): PURE READ-ONLY, zero write interaction — the gantt is
// a mirror, not a joystick (Q11). Everything derives from the
// ScheduleView projection (lanes/bars/range/derived states/today
// anchor — the server computes, this file only draws, A-P1): swimlanes
// per assignee, leaf bars + group aggregates, milestone diamonds,
// dependency elbows, the today line, version-window anchors, three
// zoom steps (周/日/时, keyboard ＋/− and buttons), the default view
// fitting the current version window, and the 待排期 inbox strip
// (display only). Hover shows the mini card; selecting a bar offers
// exactly ONE action — 「向编排者提要求」, which pre-fills an
// @排期编排组 message into that project's chat composer (the
// role-group wake token — 岗位名「编排者」不是成员名，@它唤不醒人).
// Hand-drawn SVG, no library: frappe's value is editing, which Q11 removed.

import { getSchedule, getProjectTasks } from '../wire/api.js';
import { esc, fmtDateTime, safeColor } from '../ui/dom.js';
import { t, isEn } from '../ui/i18n.js';
import { goneTagHTML } from '../ui/person.js';
import { Spin } from '../ui/feedback.js';
import { openArchive } from '../project/taskarchive.js'; // r_26 t_212：查档案浮层

const LANE_W = 140;    // sticky lane-header column
const ROW_H = 30;      // one bar row inside a lane
const LANE_PAD = 8;    // extra air under each lane block
const BAND_H = 18;     // ruler's top band (月份/日期 context row)
const RULER_H = 46;    // full ruler height (top band + the dated row)
const BAR_H = 20;      // Feishu girth: the bar fills ~2/3 of the row
const BAR_RX = BAR_H / 2;  // capsule corners, the Feishu silhouette
const GROUP_H = 8;
const DIA = 10;        // milestone diamond half-size (matches BAR_H)

const ZOOMS = {
  week: { label: '周', pxPerDay: 18, padDays: 3, majorDays: 7, minorDays: 1 },
  day: { label: '日', pxPerDay: 130, padDays: 1, majorDays: 1, minorHrs: 6 },
  hour: { label: '时', pxPerHr: 100, majorHrs: 1, minHrs: 0.25 },
};
const ZOOM_ORDER = ['week', 'day', 'hour'];
const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六'];
const DAY = 86400;
const HOUR = 3600;

const STATUS_LABEL = { todo: '待处理', doing: '进行中', done: '已完成', cancelled: '已取消' };

// 状态中文化（投影里的英文枚举 → 人话标签；t() 查词典译）
function statusLabel(st) { return t(STATUS_LABEL[st] || st); }

export class GanttChart {
  /**
   * @param {HTMLElement} container #ppane-gantt
   * @param {import('../project/project.js').ProjectBoard} board
   */
  constructor(container, board) {
    this.container = container;
    this.board = board;
    this.view = null;        // ScheduleView
    this.taskMap = new Map(); // t_NN -> TaskFull (inbox titles)
    this.zoom = 'day';
    this.userZoom = false;   // the user picked a zoom (sticks across reloads)
    this.selId = null;       // the selected bar (the ONE-action target)
    this.tip = null;
    this.dragging = false;
    this.ro = null;          // viewport refit observer
    this._roT = 0;
    this._glideT = 0;

    container.innerHTML =
      '<div class="gantt">' +
        '<div class="gantt-toolbar">' +
          '<span class="gantt-zoom">' +
            ZOOM_ORDER.map((z) => `<button type="button" class="rfilter" data-zoom="${z}">${esc(z === 'day' && isEn() ? 'Day' : t(ZOOMS[z].label))}</button>`).join('') +
          '</span>' +
          `<button type="button" class="btn small" data-today>${esc(t('回到今天'))}</button>` +
          `<button type="button" class="btn small" data-fit>${esc(t('适应全图'))}</button>` +
          `<span class="gantt-hint dim small">${esc(t('拖拽平移 · ＋/− 缩放 · 点击条目选中 · 只读视图'))}</span>` +
          '<span class="gantt-legend dim small">' +
            `<i class="lg lg-blocked"></i>${esc(t('受阻'))}` +
            `<i class="lg lg-overdue"></i>${esc(t('逾期'))}` +
            `<i class="lg lg-today"></i>${esc(t('今日'))}` +
            `<i class="lg lg-actual"></i>${esc(t('实际'))}` +
          '</span>' +
        '</div>' +
        '<div class="gantt-selbar" data-selbar hidden></div>' +
        '<div class="gantt-scroll" tabindex="0" data-scroll>' +
          '<div class="gantt-inner" data-inner></div>' +
        '</div>' +
        '<div class="gantt-inbox" data-inbox hidden></div>' +
      '</div>';
    this.els = {
      zoom: container.querySelector('.gantt-zoom'),
      scroll: container.querySelector('[data-scroll]'),
      inner: container.querySelector('[data-inner]'),
      selbar: container.querySelector('[data-selbar]'),
      inbox: container.querySelector('[data-inbox]'),
    };
    this.#paintZoomButtons();

    this.els.zoom.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-zoom]');
      if (b) this.#setZoom(b.dataset.zoom);
    });
    container.querySelector('[data-today]').addEventListener('click', () => this.#goToday());
    container.querySelector('[data-fit]').addEventListener('click', () => this.#fitAll());
    this.els.scroll.addEventListener('keydown', (ev) => {
      if (ev.key === '+' || ev.key === '=') { ev.preventDefault(); this.#stepZoom(1); }
      if (ev.key === '-' || ev.key === '_') { ev.preventDefault(); this.#stepZoom(-1); }
      if (ev.key === 'Escape') { ev.preventDefault(); this.#select(null); }
    });
    this.els.selbar.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-ask]')) this.#ask();
      if (ev.target.closest('[data-archive]')) this.#archive(); // r_26 t_212：任务档案浮层（同 TaskDrawer 的浮层族）
      if (ev.target.closest('[data-unsel]')) { this.#select(null); }
    });
    this.els.inner.addEventListener('click', (ev) => {
      if (this.dragMoved) return; // a pan, not a pick
      const g = ev.target.closest('[data-bar-id]');
      if (g) this.#select(g.dataset.barId);
      else this.#select(null); // a blank pick clears (Feishu's click-away)
    });
    this.els.inner.addEventListener('mouseover', (ev) => {
      const g = ev.target.closest('[data-bar-id]');
      if (g) this.#showTip(g.dataset.barId, ev);
    });
    this.els.inner.addEventListener('mousemove', (ev) => {
      if (this.tipEl && !this.dragging) this.#placeTip(ev);
    });
    this.els.inner.addEventListener('mouseout', (ev) => {
      if (ev.target.closest('[data-bar-id]')) this.#hideTip();
    });
    // drag-to-pan over the chart (native scrollbars stay too)
    this.els.scroll.addEventListener('mousedown', (ev) => {
      if (ev.button !== 0 || ev.target.closest('button')) return;
      this.dragging = true;
      this.dragMoved = false;
      this.dragFrom = { x: ev.clientX, y: ev.clientY, l: this.els.scroll.scrollLeft, t: this.els.scroll.scrollTop };
      ev.preventDefault();
    });
    window.addEventListener('mousemove', (ev) => {
      if (!this.dragging) return;
      const dx = ev.clientX - this.dragFrom.x, dy = ev.clientY - this.dragFrom.y;
      if (Math.abs(dx) + Math.abs(dy) > 3) this.dragMoved = true;
      this.els.scroll.scrollLeft = this.dragFrom.l - dx;
      this.els.scroll.scrollTop = this.dragFrom.t - dy;
    });
    window.addEventListener('mouseup', () => {
      this.dragging = false;
      setTimeout(() => { this.dragMoved = false; }, 0);
    });
    // the pane can grow/shrink without a reload (window resize, sidebar
    // toggles): refill the viewport width off the same projection.
    // ResizeObserver first; some embedded webviews never deliver it, so
    // window resize backs it up.
    this.refit = () => {
      if (!this.view || !this.view.bars.length) return;
      clearTimeout(this._roT);
      this._roT = setTimeout(() => this.#render(this.#centerTs()), 100);
    };
    this.ro = new ResizeObserver(this.refit);
    this.ro.observe(this.els.scroll);
    window.addEventListener('resize', this.refit);
  }

  async reload() {
    const key = this.board.key;
    const keep = this.#centerTs();
    // 生命周期前置（v2 P7）：排期是开张房间的脸——draft 未开张、archived
    // 冻结，都不该甩原始 404 给用户，直接说人话。
    const p = this.board.currentProject?.();
    if (p && p.status && p.status !== 'active') {
      this.view = null;
      this.els.inbox.hidden = true;
      this.els.inner.innerHTML =
        `<div class="state-card"><strong>${esc(p.status === 'draft' ? t('项目还没开张——排期等开张后长出来') : t('项目已归档封存——排期冻结'))}</strong>` +
        `<span>${esc(p.status === 'draft' ? t('在概览页开张后，任务排期会出现在这里') : t('复活项目后排期视图原样回来；任务台账仍可在「任务台账」页只读查看'))}</span></div>`;
      return;
    }
    this.els.inner.innerHTML = '<div class="state-card">' + Spin.html(40, t('排期加载中——读取 {key} 的 ScheduleView 投影', { key: esc(key) })) + '</div>';
    this.els.inbox.hidden = true;
    let view;
    try {
      view = await getSchedule(key);
    } catch (err) {
      this.els.inner.innerHTML =
        `<div class="state-card error"><strong>${esc(t('排期读取失败'))}</strong><span>${esc(err.message || String(err))}</span></div>`;
      return;
    }
    this.view = view;
    try {
      this.taskMap = new Map((await getProjectTasks(key)).map((task) => [task.id, task]));
    } catch { this.taskMap = new Map(); }
    if (this.selId && !view.bars.some((b) => b.task_id === this.selId)) this.selId = null;
    if (!this.userZoom) {
      // AI-paced plans (hour-scale bars) open at 时 so bars read on the
      // first paint; the user's own pick below sticks across reloads.
      this.zoom = this.#defaultZoom();
      this.#paintZoomButtons();
    }
    this.#render(keep);
  }

  // --- zoom ---------------------------------------------------------------

  #paintZoomButtons() {
    [...this.els.zoom.children].forEach((b) =>
      b.classList.toggle('active', b.dataset.zoom === this.zoom));
  }

  #setZoom(z) {
    if (!ZOOMS[z] || z === this.zoom) return;
    const keep = this.#centerTs();
    this.zoom = z;
    this.userZoom = true;
    this.#paintZoomButtons();
    this.#render(keep);
    this.els.scroll.focus();
  }

  #stepZoom(dir) {
    const i = ZOOM_ORDER.indexOf(this.zoom) + dir;
    if (i >= 0 && i < ZOOM_ORDER.length) this.#setZoom(ZOOM_ORDER[i]);
  }

  /** The opening zoom by the data span (used until the user picks one):
   * AI-paced schedules live inside a day or two and read at 时; week+
   * programs at 日/周. span 0 (nothing drawable yet) falls to 日. */
  #defaultZoom() {
    const r = this.view?.range || [0, 0];
    const span = Math.max(0, r[1] - r[0]);
    if (span > 0 && span <= 2 * DAY) return 'hour';
    if (span <= 60 * DAY) return 'day';
    return 'week';
  }

  /** The timestamp under the viewport's center — kept stable across renders. */
  #centerTs() {
    if (!this.win) return null;
    const sc = this.els.scroll;
    return this.win[0] + (sc.scrollLeft + sc.clientWidth / 2) / this.pxPerSec;
  }

  // --- layout & render ------------------------------------------------------

  #render(centerTs) {
    const view = this.view;
    if (!view) return; // draft/archived gate clears the projection
    const inbox = view.inbox || [];
    this.els.inbox.hidden = false;
    this.els.inbox.innerHTML =
      `<span class="gantt-inbox-label">${esc(t('待排期 {n}', { n: inbox.length }))}</span>` +
      (inbox.length
        ? inbox.map((id) => {
          const task = this.taskMap.get(id);
          const tip = task
            ? `${id} ${task.title} · ${esc(statusLabel(task.status))} · ${esc(t('负责人 {name}', { name: task.assignee || t('未指派') }))}${task.priority ? esc(t(' · 优先级 {p}', { p: task.priority })) : ''}${esc(t(' · 排期后自动上墙'))}`
            : `${id}${esc(t(' · 排期后自动上墙'))}`;
          return `<span class="gantt-inbox-chip" title="${esc(tip)}">${esc(id)}${task ? ` ${esc(task.title)}` : ''}</span>`;
        }).join('')
        : `<span class="dim small">${esc(t('收件格空——编排者巡检的工作队列'))}</span>`);

    if (!view.bars.length) {
      this.els.inner.innerHTML =
        `<div class="state-card"><strong>${esc(t('还没有可画的排期'))}</strong>` +
        `<span>${esc(inbox.length ? t('{n} 条任务在待排期收件格——由编排者巡检消化', { n: inbox.length }) : t('任务排期后条目长在这里'))}</span></div>`;
      this.win = null;
      this.#select(null);
      return;
    }

    // lanes: the projection's order, the pool lane last when pool bars exist
    const lanes = view.lanes.map((l) => ({ ...l, bars: [] }));
    const laneBy = new Map(lanes.map((l) => [l.assignee, l]));
    if (view.bars.some((b) => !b.assignee)) {
      const pool = { assignee: '', label: t('任务池'), color: '#8a9186', bars: [] };
      lanes.push(pool);
      laneBy.set('', pool);
    }
    for (const b of view.bars) laneBy.get(b.assignee)?.bars.push(b);

    let y = 0;
    for (const lane of lanes) {
      lane.top = y;
      lane.h = Math.max(1, lane.bars.length) * ROW_H + LANE_PAD;
      lane.bars.forEach((b, i) => { b._rowY = y + i * ROW_H + ROW_H / 2; });
      y += lane.h;
    }
    const chartH = y;

    // the view window per zoom: every zoom spans the data range (padded
    // and grid-aligned), so panning always reaches the full picture; the
    // window only clamps when 时 zoom over a huge range would paint an
    // absurd canvas (then it holds a 5-day window around the anchor)
    const z = ZOOMS[this.zoom];
    this.pxPerSec = this.zoom === 'hour' ? z.pxPerHr / HOUR : z.pxPerDay / DAY;
    const range = view.range;
    const rangeEnd = Math.max(range[0], range[1]);
    if (this.zoom === 'hour') {
      const inRange = view.today_ts >= range[0] && view.today_ts <= rangeEnd;
      let a = inRange ? view.today_ts : (centerTs ?? (range[0] + rangeEnd) / 2);
      a = Math.min(Math.max(a, range[0]), rangeEnd);
      const full = rangeEnd - range[0] <= 10 * DAY;
      const s = full ? range[0] : Math.max(range[0], a - 2.5 * DAY);
      const e = full ? rangeEnd : Math.min(rangeEnd, a + 2.5 * DAY);
      this.win = [alignTs(s - 2 * HOUR, HOUR), alignTs(Math.max(e, s + HOUR) + 2 * HOUR, HOUR)];
    } else {
      this.win = [alignTs(range[0] - z.padDays * DAY, DAY), alignTs(rangeEnd + z.padDays * DAY, DAY)];
      if (this.zoom === 'week') this.win[0] = alignMonday(this.win[0]);
    }
    // canvas width: the time span, but never narrower than the viewport —
    // the grid and ruler fill the pane (Feishu's timeline never stops short)
    const minW = Math.max(600, this.els.scroll.clientWidth - LANE_W);
    const W = Math.max(minW, Math.round((this.win[1] - this.win[0]) * this.pxPerSec));
    const X = (ts) => (ts - this.win[0]) * this.pxPerSec;

    // --- ruler (two rows: context band on top, the dated row below) ---
    const ticks = this.#rulerTicks(W);
    let ruler =
      `<svg class="gantt-ruler" width="${W}" height="${RULER_H}" viewBox="0 0 ${W} ${RULER_H}">` +
      `<rect x="0" y="0" width="${W}" height="${BAND_H}" class="ruler-band"/>`;
    // context band: 月份 segments (周/日), 日期 segments (时)
    for (const band of this.#topBands()) {
      const bx1 = X(band.s), bx2 = X(band.e);
      const bw = bx2 - bx1;
      ruler += `<line x1="${bx1}" y1="0" x2="${bx1}" y2="${BAND_H}" class="band-sep"/>`;
      if (bw >= 34) {
        const wide = bw >= 96;
        ruler += `<text x="${wide ? bx1 + bw / 2 : bx1 + 4}" y="13"${wide ? ' text-anchor="middle"' : ''} class="ruler-band-label">${esc(band.label)}</text>`;
      }
    }
    ruler += `<line x1="0" y1="${BAND_H}" x2="${W}" y2="${BAND_H}" class="band-sep"/>`;
    // weekend shading reaches into the ruler's dated row too
    for (const we of this.#weekendRects(X, W)) {
      ruler += `<rect x="${we.x}" y="${BAND_H}" width="${we.w}" height="${RULER_H - BAND_H}" class="weekend"/>`;
    }
    ruler += ticks.map((t) =>
      `<line x1="${t.x}" y1="${BAND_H}" x2="${t.x}" y2="${RULER_H}" class="grid grid-${t.major ? 'major' : 'minor'}"/>` +
      (t.label ? `<text x="${t.x + 3}" y="${BAND_H + 16}" class="ruler-label${t.major ? ' major' : ''}">${esc(t.label)}</text>` : '')).join('');
    // today: the flag lives in the ruler (Feishu-style header anchor),
    // the line alone runs through the chart body
    const xtr = X(view.today_ts);
    if (xtr >= 0 && xtr <= W) {
      ruler += `<line x1="${xtr}" y1="0" x2="${xtr}" y2="${RULER_H}" class="today-line"/>` +
        `<rect x="${xtr - 10}" y="2" width="20" height="15" rx="4" class="today-tag"/>` +
        `<text x="${xtr}" y="13" text-anchor="middle" class="today-tag-text">${esc(t('今'))}</text>`;
    }
    ruler += '</svg>';

    // --- chart ---
    const byID = new Map(view.bars.map((b) => [b.task_id, b]));
    const depEdges = [];
    for (const b of view.bars) {
      for (const d of b.deps || []) {
        if (byID.has(d)) depEdges.push({ from: byID.get(d), to: b });
      }
    }
    const selDeps = new Set(); // the selected bar's direct chain
    if (this.selId && byID.has(this.selId)) {
      for (const d of byID.get(this.selId).deps || []) selDeps.add(d);
      for (const b of view.bars) if ((b.deps || []).includes(this.selId)) selDeps.add(b.task_id);
    }

    let svg = `<svg class="gantt-chart" width="${W}" height="${chartH}" viewBox="0 0 ${W} ${chartH}">` +
      '<defs><pattern id="gantt-hatch" width="6" height="6" patternTransform="rotate(45)" patternUnits="userSpaceOnUse">' +
      '<line x1="0" y1="0" x2="0" y2="6" stroke="rgba(255,255,255,.55)" stroke-width="2"/></pattern></defs>';

    // weekend shading under everything (day zoom)
    for (const we of this.#weekendRects(X, W)) {
      svg += `<rect x="${we.x}" y="0" width="${we.w}" height="${chartH}" class="weekend"/>`;
    }
    // grid
    for (const t of ticks) {
      svg += `<line x1="${t.x}" y1="0" x2="${t.x}" y2="${chartH}" class="grid grid-${t.major ? 'major' : 'minor'}"/>`;
    }
    // lane separators
    for (const lane of lanes) {
      svg += `<line x1="0" y1="${lane.top}" x2="${W}" y2="${lane.top}" class="lane-sep"/>`;
    }

    // version window anchors (Q7)
    if (view.version_window) {
      const [v0, v1] = view.version_window;
      const xv0 = Math.max(0, X(v0)), xv1 = Math.min(W, X(v1));
      svg += `<line x1="${xv0}" y1="0" x2="${xv0}" y2="${chartH}" class="vwin-line"/>` +
        `<line x1="${xv1}" y1="0" x2="${xv1}" y2="${chartH}" class="vwin-line"/>` +
        (view.version ? `<text x="${Math.max(4, Math.min(W - 60, xv0 + 4))}" y="${chartH - 5}" class="vwin-label">▣ ${esc(view.version)}</text>` : '');
    }

    // dependency elbows (under the bars), the Feishu look: the line
    // LEAVES the predecessor's finish (a milestone leaves its diamond —
    // the drawn shape, not the phantom end stamp) and ENTERS the
    // successor's start head-first. Horizontal runs travel the GAP
    // BANDS between rows, never along a row center where labels live;
    // only the vertical drop crosses rows, through a corridor hunted
    // clear of every bar's VISUAL extent (body + trailing label).
    // In-order schedules drop into the gap left of the successor;
    // overlapping or adjacent ones swing right past both bars and come
    // back along the band beside the successor row.
    for (const b of view.bars) {
      b._ext1 = X(b.start) - (b.milestone ? DIA : 0);
      b._ext2 = (b.milestone ? X(b.start) + DIA : X(b.end || b.start));
      const lb = this.#labelBox(b, b._ext1, b._ext2);
      if (!lb.inside) b._ext2 = Math.max(b._ext2, lb.x2);
    }
    for (const e of depEdges) {
      const ex = e.from.milestone ? X(e.from.start) + DIA : X(e.from.end || e.from.start);
      const ey = e.from._rowY;
      const sx = X(e.to.start), sy = e.to._rowY;
      const tip = e.to.milestone ? sx - DIA : sx;
      const hl = this.selId && (e.from.task_id === this.selId || e.to.task_id === this.selId);
      const clearX = (x) => {
        for (let guard = 0; guard < 40; guard++) {
          const hit = view.bars.find((b) => {
            if (b === e.to) return false;
            if (ey === sy) { // same row: obstacles right of the exit (from's own label included)
              if (b === e.from) return b._ext2 > ex + 3 && x >= ex + 3 && x <= b._ext2 + 3;
              if (b._ext1 <= ex + 3) return false;
            } else if (b._rowY <= Math.min(ey, sy) || b._rowY >= Math.max(ey, sy)) {
              return false;
            }
            if (b._rowY === sy && b._ext1 < tip - 2 && b._ext2 > x - 3) return true; // to-row entry
            return x >= b._ext1 - 3 && x <= b._ext2 + 3;                             // drop column
          });
          if (!hit) return x;
          x = hit._ext2 + 9;
        }
        return x; // overlapping ladders defeat the hunt: draw the natural line
      };
      let d;
      if (ey === sy) {
        // same row: dip into this row's own band, never along the center
        if (tip - 9 > ex) {
          let v = clearX(tip - 9);
          if (v > sx - 4) v = Math.max(v, e.to._ext2 + 9); // re-enter from above, clear of the target
          d = v <= sx - 4
            ? `M ${ex} ${ey} H ${ex + 4} V ${ey + 15} H ${v} V ${ey} H ${tip - 5}`
            : `M ${ex} ${ey} H ${ex + 4} V ${ey + 15} H ${v} V ${ey - 13} H ${tip - 6} V ${ey}`;
        } else {
          d = `M ${ex} ${ey} H ${ex + 4} V ${ey - 13} H ${tip - 6} V ${ey}`;
        }
      } else {
        const down = sy > ey;
        const bandOut = ey + (down ? 15 : -15);   // gap on the travel side of the from row
        const bandIn = sy + (down ? -15 : 15);    // gap on the approach side of the to row
        if (tip - 9 > ex) {
          const v = clearX(tip - 9);
          d = v <= sx - 4
            ? `M ${ex} ${ey} H ${ex + 4} V ${bandOut} H ${v} V ${sy} H ${tip - 5}`
            : `M ${ex} ${ey} H ${ex + 4} V ${bandOut} H ${v} V ${bandIn} H ${tip - 6} V ${sy}`;
        } else {
          const v = clearX(Math.max(ex, sx) + 9);
          d = `M ${ex} ${ey} H ${ex + 4} V ${bandOut} H ${v} V ${bandIn} H ${tip - 6} V ${sy}`;
        }
      }
      svg += `<path d="${d}" class="dep${hl ? ' hl' : ''}"/>` +
        `<path d="M ${tip} ${sy} l -5 -3 v 6 z" class="dep-arrow${hl ? ' hl' : ''}"/>`;
    }

    // bars — the Feishu silhouette: a fat opaque capsule (2/3 of the
    // row) with the name centered inside in white; done fades to a
    // light wash with dark ink (Feishu tells status by color, not by
    // translucency stacks); blocked wears a white hatch; no checkmark
    // and no double-layer progress — the % rides in the centered label.
    for (const b of view.bars) {
      const color = safeColor(b.color);
      const x1 = X(b.start);
      const x2 = b.milestone ? x1 : X(b.end || b.start);
      const w = Math.max(6, x2 - x1);
      const sel = b.task_id === this.selId ? ' sel' : '';
      const chain = selDeps.has(b.task_id) ? ' chain' : '';
      const cls = `bar kind-${b.kind} st-${b.status}${b.blocked ? ' blocked' : ''}${b.overdue ? ' overdue' : ''}${sel}${chain}`;
      if (b.kind === 'group') {
        svg += `<g data-bar-id="${esc(b.task_id)}" class="${cls}">` +
          `<rect x="${x1}" y="${b._rowY - GROUP_H / 2}" width="${w}" height="${GROUP_H}" rx="${GROUP_H / 2}" fill="${color}" fill-opacity=".28" stroke="${color}" stroke-opacity=".7"/>` +
          this.#barLabel(b, x1, w, b._rowY) +
          '</g>';
        continue;
      }
      if (b.milestone) {
        svg += `<g data-bar-id="${esc(b.task_id)}" class="${cls}">` +
          `<path d="M ${x1} ${b._rowY - DIA} L ${x1 + DIA} ${b._rowY} L ${x1} ${b._rowY + DIA} L ${x1 - DIA} ${b._rowY} Z" fill="${color}" stroke="${color}"/>` +
          this.#barLabel(b, x1 - DIA, DIA * 2, b._rowY) +
          '</g>';
        continue;
      }
      const done = b.status === 'done';
      svg += `<g data-bar-id="${esc(b.task_id)}" class="${cls}">` +
        `<rect x="${x1}" y="${b._rowY - BAR_H / 2}" width="${w}" height="${BAR_H}" rx="${BAR_RX}" fill="${color}"` +
        (done ? ` fill-opacity=".22" stroke="${color}" stroke-opacity=".45"` : ' stroke="none"') + `/>`;
      if (b.blocked) {
        svg += `<rect x="${x1}" y="${b._rowY - BAR_H / 2}" width="${w}" height="${BAR_H}" rx="${BAR_RX}" fill="url(#gantt-hatch)" stroke="rgba(154,163,152,.55)"/>`;
      }
      svg += this.#barLabel(b, x1, w, b._rowY);
      // the REALITY strip (计划 vs 实际, the AI pace): a thin ink line in
      // the margin under the capsule marking when the work truly ran —
      // the plan says 三天, the strip says 七秒, the gap IS the 提效.
      if (b.actual_start > 0) {
        const ax1 = X(b.actual_start);
        const ax2 = b.actual_end >= b.actual_start ? X(b.actual_end) : ax1;
        svg += `<rect x="${ax1}" y="${b._rowY + BAR_H / 2 + 1}" width="${Math.max(2, ax2 - ax1)}" height="3" rx="1.5" class="bar-actual"/>`;
      }
      svg += '</g>';
    }

    // today line through the body (its 今 flag lives in the ruler)
    const xt = X(view.today_ts);
    if (xt >= 0 && xt <= W) {
      svg += `<line x1="${xt}" y1="0" x2="${xt}" y2="${chartH}" class="today-line"/>`;
    }
    svg += '</svg>';

    // lane header column (HTML, sticky-left; heights derive from the
    // same lane layout the SVG rows use, so alignment is by construction).
    // 泳道头同用人名标注：离过职的负责人置灰带章（已离职/已离编），
    // 泳道不撤——排期事实还在。
    const laneCol = lanes.map((l) => {
      const gone = l.assignee && this.board?.departTag ? this.board.departTag(l.assignee) : '';
      return (
      `<div class="gantt-lane" style="height:${l.h}px">` +
        `<i class="dot" style="background:${safeColor(l.color)}"></i>` +
        `<span class="gantt-lane-name${gone ? ' gone' : ''}">${esc(l.assignee || l.label || t('任务池'))}</span>` +
        goneTagHTML(gone) +
        `<span class="gantt-lane-count">${l.bars.length}</span>` +
      '</div>');
    }).join('');

    const vwin = view.version_window;
    this.els.inner.innerHTML =
      '<div class="gantt-top">' +
        '<div class="gantt-corner"></div>' +
        ruler +
      '</div>' +
      '<div class="gantt-bodyrow">' +
        `<div class="gantt-lanes">${laneCol}</div>` +
        svg +
      '</div>';

    // viewport placement: a carried center wins; else fit the version
    // window's left edge (Q7 default), else center today, else home
    const sc = this.els.scroll;
    if (centerTs != null) {
      sc.scrollLeft = Math.max(0, X(centerTs) - sc.clientWidth / 2);
    } else if (vwin) {
      sc.scrollLeft = Math.max(0, X(vwin[0]) - 24);
    } else if (view.today_ts >= this.win[0] && view.today_ts <= this.win[1]) {
      sc.scrollLeft = Math.max(0, X(view.today_ts) - sc.clientWidth / 2);
    } else {
      sc.scrollLeft = 0;
    }
    this.#paintSelbar();
  }

  /** The label's placement decision, shared by the draw and the dep
   * corridor (Feishu's look): a leaf bar wide enough to swallow the
   * text carries it INSIDE — trailing text is exactly where dep lines
   * and gridlines tear through. Milestones and groups (thin shapes)
   * always trail; so does any bar narrower than its name. */
  #labelBox(b, bx1, bx2) {
    const text = labelOf(b);
    const insideable = !b.milestone && b.kind !== 'group';
    if (insideable && bx2 - bx1 - 12 >= textW(text)) {
      return { inside: true, cx: (bx1 + bx2) / 2 }; // Feishu centers the name in the bar
    }
    const short = text.length > 22 ? text.slice(0, 21) + '…' : text;
    return { inside: false, x1: bx2 + 6, x2: bx2 + 6 + textW(short) };
  }

  #barLabel(b, x1, w, y) {
    const lb = this.#labelBox(b, x1, x1 + w);
    if (lb.inside) {
      // opaque capsule → white ink; the done wash → dark ink (Feishu's contrast rule)
      const light = b.status !== 'done';
      return `<text x="${lb.cx}" y="${y + 4}" text-anchor="middle" class="bar-label ${light ? 'in-light' : 'in-dark'}">${esc(labelOf(b))}</text>`;
    }
    const text = labelOf(b);
    const short = text.length > 22 ? text.slice(0, 21) + '…' : text;
    return `<text x="${lb.x1}" y="${y + 4}" class="bar-label">${esc(short)}</text>`;
  }

  /** The ruler's context band segments: 月份 (周/日 zoom) or 日期 (时 zoom). */
  #topBands() {
    const [w0, w1] = this.win;
    const out = [];
    if (this.zoom === 'hour') {
      const d = new Date(w0 * 1000);
      d.setHours(0, 0, 0, 0);
      for (let s = d.getTime() / 1000; s < w1; s += DAY) {
        const dd = new Date(s * 1000);
        out.push({
          s: Math.max(s, w0), e: Math.min(s + DAY, w1),
          label: t('{md} 周{w}', {
            md: `${String(dd.getMonth() + 1).padStart(2, '0')}/${String(dd.getDate()).padStart(2, '0')}`,
            w: t(WEEKDAYS[dd.getDay()]),
          }),
        });
      }
      return out;
    }
    const d = new Date(w0 * 1000);
    d.setDate(1);
    d.setHours(0, 0, 0, 0);
    for (let s = d.getTime() / 1000; s < w1;) {
      const dd = new Date(s * 1000);
      const nxt = new Date(dd.getFullYear(), dd.getMonth() + 1, 1).getTime() / 1000;
      // clamp to the view window: the label centers on the VISIBLE band
      out.push({ s: Math.max(s, w0), e: Math.min(nxt, w1), label: t('{y}年{m}月', { y: dd.getFullYear(), m: dd.getMonth() + 1 }) });
      s = nxt;
    }
    return out;
  }

  /** Weekend column shading (Saturday+Sunday) — day zoom only. */
  #weekendRects(X, W) {
    if (this.zoom !== 'day') return [];
    const out = [];
    for (let t = alignTs(this.win[0], DAY); t <= this.win[1]; t += DAY) {
      if (new Date(t * 1000).getDay() !== 6) continue;
      const x = Math.max(0, X(t));
      const w = Math.min(W, X(t + 2 * DAY)) - x;
      if (w > 0) out.push({ x, w });
    }
    return out;
  }

  /** 适应全图: the finest zoom that paints the whole range in one pane
   * (时 only when the render's 10-day hour-window clamp wouldn't bite),
   * then home the scroll to the range's left edge — the full picture. */
  #fitAll() {
    if (!this.view || !this.view.bars.length) return;
    const range = this.view.range;
    const span = Math.max(0, Math.max(range[0], range[1]) - range[0]);
    const chartW = Math.max(300, this.els.scroll.clientWidth - LANE_W);
    this.userZoom = true;
    this.zoom =
      span <= 10 * DAY && (span / HOUR) * ZOOMS.hour.pxPerHr <= chartW ? 'hour'
      : (span / DAY) * ZOOMS.day.pxPerDay <= chartW ? 'day'
      : 'week';
    this.#paintZoomButtons();
    this.#render();
    this.#glide(0);
  }

  /**
   * Programmatic horizontal scroll: smooth where the runtime honors it.
   * Webviews that silently drop smooth scrollTo (it neither animates nor
   * jumps there — and a smooth style even voids direct assignments) get
   * an instant jump: if nothing has moved off the start shortly after,
   * just go.
   */
  #glide(x) {
    const sc = this.els.scroll;
    const target = Math.max(0, x);
    const from = sc.scrollLeft;
    sc.scrollTo({ left: target, behavior: 'smooth' });
    clearTimeout(this._glideT);
    this._glideT = setTimeout(() => {
      if (Math.abs(sc.scrollLeft - target) > 2 && Math.abs(sc.scrollLeft - from) <= 2) {
        sc.scrollLeft = target;
      }
    }, 120);
  }

  /** Jump the viewport so the today line sits centered (clamped to the data window). */
  #goToday() {
    if (!this.win || !this.view) return;
    const sc = this.els.scroll;
    const x = (this.view.today_ts - this.win[0]) * this.pxPerSec;
    const winPx = (this.win[1] - this.win[0]) * this.pxPerSec;
    if (this.zoom === 'hour' && (x < 0 || x > winPx)) {
      // a clamped 时 window (huge range) re-anchors itself on today
      this.#render();
      return;
    }
    const target = x < 0 ? 0
      : x > winPx ? sc.scrollWidth - sc.clientWidth
        : LANE_W + x - (sc.clientWidth - LANE_W) / 2;
    this.#glide(target);
  }

  /** Ruler ticks for the current zoom: majors carry labels. */
  #rulerTicks(W) {
    const z = ZOOMS[this.zoom];
    const out = [];
    const [w0, w1] = this.win;
    const X = (ts) => (ts - w0) * this.pxPerSec;
    let step, major, first;
    if (this.zoom === 'hour') {
      step = z.minHrs * HOUR;
      major = z.majorHrs * HOUR;
      first = alignTs(w0, major);
    } else if (this.zoom === 'day') {
      step = z.minorHrs * HOUR;
      major = z.majorDays * DAY;
      first = alignTs(w0, DAY);
    } else {
      step = z.minorDays * DAY;
      major = z.majorDays * DAY;
      first = alignMonday(w0);
    }
    let n = 0;
    for (let t = first; t <= w1; t += step, n++) {
      const x = X(t);
      if (x < -40 || x > W + 40) continue;
      const d = new Date(t * 1000);
      const pad = (v) => String(v).padStart(2, '0');
      const md = `${pad(d.getMonth() + 1)}/${pad(d.getDate())}`;
      let label = '';
      if (this.zoom === 'hour') label = `${pad(d.getHours())}:00`;
      else if (this.zoom === 'day') label = t('{md} 周{w}', { md, w: t(WEEKDAYS[d.getDay()]) });
      else label = `${md}`;
      const isMajor = (t - first) % major === 0;
      out.push({ x: Math.round(x * 100) / 100, major: isMajor, label: isMajor ? label : '' });
      if (n > 2000) break; // runaway guard (misaligned windows)
    }
    return out;
  }

  // --- selection: the ONE action -------------------------------------------

  #select(id) {
    this.selId = id;
    const svg = this.els.inner.querySelector('.gantt-chart');
    if (svg) {
      const strip = (c) => (c || '').replace(/(^|\s)(sel|chain)(?=\s|$)/g, '');
      [...svg.querySelectorAll('[data-bar-id]')].forEach((g) => {
        g.setAttribute('class', strip(g.getAttribute('class')) + (g.dataset.barId === id ? ' sel' : ''));
      });
      // re-mark the chain (cheap enough at these sizes)
      if (id && this.view) {
        const byID = new Map(this.view.bars.map((x) => [x.task_id, x]));
        const chain = new Set();
        for (const d of byID.get(id)?.deps || []) chain.add(d);
        for (const b2 of this.view.bars) if ((b2.deps || []).includes(id)) chain.add(b2.task_id);
        for (const gid of chain) {
          svg.querySelector(`[data-bar-id="${CSS.escape(gid)}"]`)?.classList.add('chain');
        }
      }
    }
    this.#paintSelbar();
  }

  #paintSelbar() {
    const el = this.els.selbar;
    const b = this.selId && this.view ? this.view.bars.find((x) => x.task_id === this.selId) : null;
    if (!b) {
      el.hidden = true;
      el.innerHTML = '';
      return;
    }
    const span = b.milestone ? fmtDateTime(b.start)
      : `${b.start ? fmtDateTime(b.start) : t('未定')} ~ ${b.end ? fmtDateTime(b.end) : t('开放')}`;
    const eff = effLine(b, this.view?.today_ts);
    // the eff line already carries 计划/实际 — a bare 时长 only when it's silent
    const dur = !b.milestone && b.start && b.end && !eff ? t(' · 时长 {d}', { d: fmtDur(b.end - b.start) }) : '';
    el.hidden = false;
    el.innerHTML =
      `<span class="gantt-sel-meta"><b>${esc(b.task_id)}</b> ${esc(b.label)}` +
      `<span class="dim small"> · ${esc(b.assignee || t('任务池'))} · ${esc(span)}${esc(dur)}` +
      `${b.progress_pct ? ` · ${b.progress_pct}%` : ''}${(b.deps || []).length ? esc(t(' · 依赖 {n}', { n: b.deps.length })) : ''}` +
      `${b.blocked ? esc(t(' · 受阻')) : ''}${b.overdue ? esc(t(' · 逾期')) : ''}` +
      `${eff ? ` · ${esc(eff)}` : ''}</span></span>` +
      '<span class="gantt-sel-ops">' +
        `<button type="button" class="btn small gold" data-ask>${esc(t('向编排者提要求'))}</button>` +
        `<button type="button" class="btn small" data-archive>${esc(t('查档案'))}</button>` +
        `<button type="button" class="btn small" data-unsel>${esc(t('取消选中'))}</button>` +
      '</span>';
  }

  /** r_26 t_212：任务档案浮层——读 getTask 全量 log（与 CLI task show 同源）。 */
  #archive() {
    if (!this.selId) return;
    openArchive(this.selId);
  }

  /** Q11's single gesture: pre-aim the room's composer, never a write. */
  #ask() {
    const b = this.view?.bars.find((x) => x.task_id === this.selId);
    if (!b) return;
    const text = `@排期编排组 请调整 ${b.task_id}「${b.label}」的排期：`;
    this.board.hooks.draftTo(this.board.key, text);
  }

  // --- hover mini card -------------------------------------------------------

  #showTip(id, ev) {
    const b = this.view?.bars.find((x) => x.task_id === id);
    if (!b) return;
    this.#hideTip();
    const span = b.milestone ? fmtDateTime(b.start)
      : `${b.start ? fmtDateTime(b.start) : t('未定')} ~ ${b.end ? fmtDateTime(b.end) : t('开放')}`;
    const eff = effLine(b, this.view?.today_ts);
    const dur = !b.milestone && b.start && b.end && !eff ? t(' · 时长 {d}', { d: fmtDur(b.end - b.start) }) : '';
    const tip = document.createElement('div');
    tip.className = 'gantt-tip';
    tip.innerHTML =
      `<div class="gt-title">${esc(b.label)}</div>` +
      `<div class="gt-meta">${esc(b.assignee || t('任务池'))} · ${esc(kindLabel(b))} · ${esc(statusLabel(b.status))}</div>` +
      `<div class="gt-meta">${esc(span)}${esc(dur)}${b.progress_pct ? esc(t(' · 进度 {n}%', { n: b.progress_pct })) : ''}</div>` +
      (eff ? `<div class="gt-meta">${esc(eff)}</div>` : '') +
      `<div class="gt-meta">${esc(t('依赖 {n} 项', { n: (b.deps || []).length }))}${b.blocked ? esc(t(' · 受阻（前置未完）')) : ''}${b.overdue ? esc(t(' · 逾期')) : ''}</div>`;
    this.els.inner.appendChild(tip);
    this.tipEl = tip;
    this.#placeTip(ev);
  }

  #placeTip(ev) {
    if (!this.tipEl) return;
    const box = this.els.scroll.getBoundingClientRect();
    const x = Math.min(ev.clientX - box.left + 14, box.width - 240);
    const y = Math.min(ev.clientY - box.top + 12, box.height - 110);
    this.tipEl.style.left = `${Math.max(4, x)}px`;
    this.tipEl.style.top = `${Math.max(4, y)}px`;
  }

  #hideTip() {
    this.tipEl?.remove();
    this.tipEl = null;
  }
}

/** The bar's full label text (group aggregates carry their 组 marker). */
function labelOf(b) {
  return (b.kind === 'group' ? t('组 ') : '') + b.label + (b.progress_pct > 0 ? ` ${b.progress_pct}%` : '');
}

/** Rough 11px display width: CJK/fullwidth ≈ 11, everything else ≈ 6. */
function textW(s) {
  let w = 0;
  for (const ch of s) w += /[\u2e80-\u9fff\u3000-\u303f\uff00-\uffef]/.test(ch) ? 11 : 6;
  return w;
}

function kindLabel(b) {
  return t(b.kind === 'group' ? '分组聚合' : (b.milestone ? '里程碑' : '叶任务'));
}

/** A bar's span in AI-pace words: 秒/分钟/小时/天, half-step rounded. */
function fmtDur(sec) {
  if (sec < 60) return t('{n} 秒', { n: Math.max(1, Math.round(sec)) });
  if (sec < HOUR) return t('{n} 分钟', { n: Math.max(1, Math.round(sec / 60)) });
  const h = sec / HOUR;
  if (h < 48) return t('{n} 小时', { n: Number.isInteger(h) ? h : h.toFixed(1) });
  return t('{n} 天', { n: (h / 24).toFixed(1) });
}

/** The 计划 vs 实际 line (AI 节奏的提效账): a finished leaf compares the
 * planned window against the log-derived real span; a running one shows
 * the live clock since hands actually went on. Empty when no reality is
 * recorded (legacy ledgers, tasks born as doing). */
function effLine(b, nowTs) {
  if (!b.actual_start || b.milestone) return '';
  if (b.status === 'done' && b.end > b.start && b.actual_end >= b.actual_start) {
    const planned = b.end - b.start;
    const spent = Math.max(1, b.actual_end - b.actual_start);
    return t('计划 {p} · 实际 {a} · 提效 {m}', {
      p: fmtDur(planned), a: fmtDur(Math.max(0, b.actual_end - b.actual_start)), m: fmtMult(planned / spent),
    });
  }
  if (b.status === 'doing' && nowTs) {
    const plan = b.end > b.start ? t('（排期 {d}）', { d: fmtDur(b.end - b.start) }) : '';
    return t('实际已进行 {d}{plan}', { d: fmtDur(Math.max(0, nowTs - b.actual_start)), plan });
  }
  return '';
}

/** The 提效 multiple: whole turns past a hundred, one decimal past ten. */
function fmtMult(m) {
  if (m >= 100) return `${Math.round(m)}×`;
  if (m >= 10) return `${m.toFixed(1)}×`;
  return `${Math.max(1, m).toFixed(2)}×`;
}

/** Floor ts down to a step boundary in LOCAL time. A raw Math.floor(ts/step)
 * is a UTC boundary — in +08 every "day" line lands at 08:00 local, so bars
 * sit a column off their date labels and today hugs the NEXT day's grid
 * (the 2026-10 misalignment report). Day steps floor to local midnight;
 * sub-day steps floor to the step inside the local day (DST-free zones). */
function alignTs(ts, step) {
  const d = new Date(ts * 1000);
  d.setHours(0, 0, 0, 0);
  const midnight = Math.floor(d.getTime() / 1000);
  if (step >= DAY) return midnight;
  return midnight + Math.floor((ts - midnight) / step) * step;
}

/** Floor ts down to the local Monday 00:00 (the week grid's anchor). */
function alignMonday(ts) {
  const d = new Date(ts * 1000);
  d.setHours(0, 0, 0, 0);
  const dow = (d.getDay() + 6) % 7; // Monday = 0
  d.setDate(d.getDate() - dow);
  return Math.floor(d.getTime() / 1000);
}

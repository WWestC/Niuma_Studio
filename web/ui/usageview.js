// usageview.js — 耗粮统计抽屉：把牛马员工的 token 消耗摊开给人看。
// 数据真源是 ZCode CLI 记的台账（turn_usage 每轮一行 / model_usage 每次
// 模型请求一行），server 的 GET /usage（员工表＋时/天/周/月分桶趋势＋
// 全史按模型合计，一趟扫账一包全带）与 /usage/turns（每轮明细）把任务
// 索引里的员工标题 join 上去——本视图不存任何第二份账，开着就拉、点谁
// 翻谁；页面加载时服务端已后台预热台账，抽屉首开多半即渲染。右侧 sheet 抽屉（taskdrawer/traceview 同款骨架）：
//
//   总览页 —— 趋势一槽（消耗趋势：四档页签切折线，悬停纵线＋浮牌；
//             模型对比：全史按模型合计的条形排名），下面才是员工表
//             （表头＋合计行吸顶一组，合计行：服务端 total 真源，总计
//             列品牌蓝只落在这里一处），员工行按总计降序（数字列
//             tabular-nums 右对齐、时间列不折行、零活动行置灰）；
//             点行进明细页；
//   明细页 —— 返回钮 ＋ 该员工最近 N 轮（新→旧），每轮三段：时间
//             ·状态·时长·模型请求数／净输入·输出·缓存读·总计四格
//             指标（总计加重，零耗轮置灰）／按模型分摊的 chip（哪个
//             模型几次共多少）。
//
// 纯逻辑（fmtTokens/fmtDur/turnStatusText/fmtBucketLabel/lineGeometry）
// 导出给 node --test；本模块只经 wire/api.js 取数（「组件不自取」的
// 房规），不碰第二份连接。折线是手绘 SVG——不引外部图表库。

import { getUsage, getUsageTurns } from '../wire/api.js';
import { esc, dismiss, fmtDateTime } from './dom.js';
import { t, isEn } from './i18n.js';

// token 数的人话：亿两位小数、万一位小数、万以下原样（en 走英文数量级
// K/M/B——阈值随语言换，中文阈值直接贴「亿/万」的念法）。
export function fmtTokens(n) {
  if (!Number.isFinite(n)) return '—';
  const a = Math.abs(n);
  if (isEn()) {
    if (a >= 1e9) return (n / 1e9).toFixed(2) + ' B';
    if (a >= 1e6) return (n / 1e6).toFixed(2) + ' M';
    if (a >= 1e3) return (n / 1e3).toFixed(1) + ' K';
    return String(n);
  }
  if (a >= 1e8) return (n / 1e8).toFixed(2) + ' 亿';
  if (a >= 1e4) return (n / 1e4).toFixed(1) + ' 万';
  return String(n);
}

// 时长的人话：秒（<10 一位小数）→ 分秒 → 时分；0/缺 = 「—」。
export function fmtDur(ms) {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  const s = ms / 1000;
  if (s < 60) return t('{s} 秒', { s: s.toFixed(s < 10 ? 1 : 0) });
  const m = Math.floor(s / 60);
  if (m < 60) return t('{m} 分 {s} 秒', { m, s: Math.round(s % 60) });
  return t('{h} 时 {m} 分', { h: Math.floor(m / 60), m: String(m % 60).padStart(2, '0') });
}

// 回合状态的界面面（turn_usage 的四态；生面孔原样透出）。
export function turnStatusText(status) {
  return { completed: t('完成'), running: t('进行中'), error: t('出错'), cancelled: t('已取消') }[status] || (status || '—');
}

// 分桶时刻的人话标签：小时档 MM-DD HH:00、天/周档 MM-DD、月档
// YYYY-MM（本地时区，数字补零；纯数字无文法，中英同形）。
export function fmtBucketLabel(ms, bucket) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  const md = p(d.getMonth() + 1) + '-' + p(d.getDate());
  if (bucket === 'hour') return md + ' ' + p(d.getHours()) + ':00';
  if (bucket === 'month') return d.getFullYear() + '-' + p(d.getMonth() + 1);
  return md;
}

// 峰值向上吸到 1/2/2.5/5×10^k 的人话整桶（fmtTokens 的分档念法整好
// 对齐）；全零也给 1，免得除零。
function niceCeil(v) {
  if (!Number.isFinite(v) || v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) {
    if (v <= m * p) return m * p;
  }
  return 10 * p;
}

// 折线的几何（纯函数，node --test 直钉）：totals（旧→新）映射进画布，
// yMax 取整桶、网格三线（峰/半峰/零线）、坐标列自左向右。单点居中，
// 空列给空坐标（只剩网格的空图）。
export function lineGeometry(totals, w, h, padL, padR, padT, padB) {
  const iw = Math.max(1, w - padL - padR);
  const ih = Math.max(1, h - padT - padB);
  let peak = 0;
  for (const v of totals) if (v > peak) peak = v;
  const yMax = niceCeil(peak);
  const n = totals.length;
  const pts = totals.map((v, i) => [
    padL + (n === 1 ? iw / 2 : (iw * i) / (n - 1)),
    padT + ih - (v / yMax) * ih,
  ]);
  const grid = [
    { v: yMax, y: padT },
    { v: yMax / 2, y: padT + ih / 2 },
    { v: 0, y: padT + ih },
  ];
  return { pts, yMax, grid, base: padT + ih };
}

// 四档页签的文案（data-uv-range 值 → i18n key）。
const RANGE_LABEL = {
  hour: '近 48 小时',
  day: '近 30 天',
  week: '近 12 周',
  month: '近 12 个月',
};

const TURN_PAGE = 200; // 明细页每轮拉多少（服务端钳 1..500）

export class UsagePanel {
  /**
   * @param {Object} [opts]
   * @param {(text: string, kind?: string) => void} [opts.toast]
   */
  constructor(opts = {}) {
    this.opts = opts;
    this.el = null;      // .sheet-wrap
    this.sheet = null;   // .sheet.uv-sheet
    this.title = null;   // 明细页正打开的员工标题（null = 总览页）
    this.range = 'day';  // 趋势图档位（hour|day|week|month；人走档位记着）
    this.series = null;  // GET /usage 回包里的趋势面（null = 还没落地）
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
  }

  /** 开抽屉：直落总览页。 */
  open() {
    this.close();
    const el = document.createElement('div');
    el.className = 'sheet-wrap';
    el.innerHTML = '<div class="sheet uv-sheet" role="dialog" aria-label="' + esc(t('耗粮统计')) + '"></div>';
    document.body.appendChild(el);
    this.el = el;
    this.sheet = el.querySelector('.sheet');
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      const back = ev.target.closest('[data-uv-back]');
      if (back) { this.#loadSummary(); return; }
      const tab = ev.target.closest('[data-uv-range]');
      if (tab) { this.range = tab.dataset.uvRange; this.#renderCharts(); return; }
      const emp = ev.target.closest('[data-uv-emp]');
      if (emp) this.#loadTurns(emp.dataset.uvEmp);
      const close = ev.target.closest('[data-uv-close]');
      if (close) this.close();
    });
    document.addEventListener('keydown', this.onDocKey);
    this.#loadSummary();
  }

  close() {
    if (this.el) dismiss(this.el);
    this.el = null;
    this.sheet = null;
    this.title = null;
    this.series = null;
    document.removeEventListener('keydown', this.onDocKey);
  }

  // 总览页：一趟扫账的回包整面渲染——趋势图槽＋合计一行 ＋ 员工行
  // 按总计降序（点行进明细）。员工表与趋势图同一回包，无第二读。
  async #loadSummary() {
    if (!this.sheet) return;
    this.title = null;
    this.series = null;
    this.sheet.innerHTML =
      '<div class="sheet-head"><div class="sheet-title"><strong>' + esc(t('耗粮统计')) + '</strong></div>' +
      '<button type="button" class="btn small" data-uv-close>' + esc(t('关闭')) + '</button></div>' +
      '<div class="uv-loading"><i class="uv-spin"></i>' + esc(t('正在盘点工作室台账…')) + '</div>';
    let data;
    try {
      data = await getUsage();
    } catch (err) {
      this.#fail(err, t('耗粮台账读取失败'));
      return;
    }
    if (!this.sheet || this.title !== null) return; // 已翻页/已收窗：迟到的回包别覆盖
    const total = data.total || {};
    const hasRows = !!(data.employees && data.employees.length);
    let html =
      '<div class="sheet-head"><div class="sheet-title"><strong>' + esc(t('耗粮统计')) + '</strong>' +
      `<span class="uv-sub">${esc(t('{n} 头牛马', { n: data.count || 0 }))}${hasRows ? esc(t(' · 按总计降序，点行看每轮明细')) : ''}</span></div>` +
      '<button type="button" class="btn small" data-uv-close>' + esc(t('关闭')) + '</button></div>' +
      '<div class="uv-charts" data-uv-charts></div>';
    if (!hasRows) {
      html += '<div class="uv-empty">' + esc(t('还没有耗粮记录——牛马出生干活后，这里按员工记账。')) + '</div>';
    } else {
      // 合计行（服务端 total 为真源；会话/轮次只把返回的行相加做展示）
      let sumSessions = 0, sumTurns = 0;
      for (const e of data.employees) { sumSessions += e.sessions || 0; sumTurns += e.turns || 0; }
      html += '<div class="uv-rows">' +
        '<div class="uv-pin">' +
        '<div class="uv-row uv-row-head"><span>' + esc(t('员工')) + '</span><span>' + esc(t('会话')) + '</span><span>' + esc(t('轮次')) + '</span>' +
        '<span>' + esc(t('净输入')) + '</span><span>' + esc(t('输出')) + '</span><span>' + esc(t('总计')) + '</span><span>' + esc(t('最近活动')) + '</span></div>' +
        '<div class="uv-row uv-sum"><span class="uv-name">' + esc(t('合计')) + '</span>' +
        `<span>${sumSessions}</span><span>${sumTurns}</span>` +
        `<span>${esc(fmtTokens(total.net_input || 0))}</span><span>${esc(fmtTokens(total.output || 0))}</span>` +
        `<span class="uv-hot">${esc(fmtTokens(total.total || 0))}</span><span></span></div>` +
        '</div>';
      for (const e of data.employees) {
        html += `<button type="button" class="uv-row${e.turns ? '' : ' uv-zero'}" data-uv-emp="${esc(e.title)}" title="${esc(t('看每轮耗粮明细'))}">` +
          `<span class="uv-name" title="${esc(e.title)}">${esc(e.title)}</span><span>${e.sessions || 0}</span><span>${e.turns || 0}</span>` +
          `<span>${esc(fmtTokens(e.net_input || 0))}</span><span>${esc(fmtTokens(e.output || 0))}</span>` +
          `<span class="uv-hot">${esc(fmtTokens(e.total || 0))}</span>` +
          `<span class="uv-last">${e.last_active ? esc(fmtDateTime(Math.floor(e.last_active / 1000))) : '—'}</span></button>`;
      }
      html += '</div>';
    }
    this.sheet.innerHTML = html;
    this.series = data.series || null;
    this.#renderCharts(); // 同一回包的趋势面当场入槽（无第二次扫账）
  }

  // 趋势与模型对比（总览页顶部一槽）：折线画 total（金）、面积淡金、
  // 网格三线人话刻度；悬停纵导线＋浮牌给该桶明细（含前三名模型）；
  // 模型条按全史合计铺宽、占比分母是全史总和。数据全零/缺席整槽收起
  // ——空工作室不值得一张空图。
  #renderCharts() {
    const box = this.sheet && this.sheet.querySelector('[data-uv-charts]');
    if (!box) return;
    const rep = this.series || {};
    const levels = Object.keys(RANGE_LABEL);
    const hasSpend = levels.some((k) => ((rep[k] && rep[k].points) || []).some((p) => (p.total || 0) > 0)) ||
      (rep.models || []).some((m) => (m.tokens || 0) > 0);
    if (!hasSpend) { box.innerHTML = ''; return; }
    const range = levels.includes(this.range) ? this.range : 'day';
    const points = (rep[range] && rep[range].points) || [];
    const totals = points.map((p) => p.total || 0);
    const W = 644, H = 208, PADL = 10, PADR = 10, PADT = 20, PADB = 24;
    const g = lineGeometry(totals, W, H, PADL, PADR, PADT, PADB);
    const sum = totals.reduce((a, b) => a + b, 0);
    const peak = totals.reduce((a, b) => (b > a ? b : a), 0);
    let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${esc(t('消耗趋势'))}">`;
    for (const row of g.grid) {
      svg += `<line x1="${PADL}" y1="${row.y.toFixed(1)}" x2="${W - PADR}" y2="${row.y.toFixed(1)}" stroke="var(--line)" stroke-width="1"/>` +
        `<text x="${PADL}" y="${(row.y - 4).toFixed(1)}" font-size="10" fill="var(--chalk-faint)">${esc(fmtTokens(row.v))}</text>`;
    }
    if (g.pts.length > 1) {
      const line = g.pts.map((p) => p[0].toFixed(1) + ',' + p[1].toFixed(1)).join(' L');
      svg += `<path d="M${g.pts[0][0].toFixed(1)},${g.base.toFixed(1)} L${line} L${g.pts[g.pts.length - 1][0].toFixed(1)},${g.base.toFixed(1)} Z" fill="color-mix(in srgb, var(--gold) 10%, transparent)" stroke="none"/>` +
        `<polyline points="${g.pts.map((p) => p[0].toFixed(1) + ',' + p[1].toFixed(1)).join(' ')}" fill="none" stroke="var(--gold)" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round"/>`;
    }
    const n = points.length;
    if (n > 1) { // x 刻度约六档均布，首尾锚边防出界
      const step = Math.max(1, Math.ceil(n / 6));
      const ticks = [];
      for (let i = 0; i < n; i += step) ticks.push(i);
      if (ticks[ticks.length - 1] !== n - 1) ticks.push(n - 1);
      for (const i of ticks) {
        const anchor = i === 0 ? 'start' : (i === n - 1 ? 'end' : 'middle');
        svg += `<text x="${g.pts[i][0].toFixed(1)}" y="${H - 6}" font-size="10" fill="var(--chalk-faint)" text-anchor="${anchor}">${esc(fmtBucketLabel(points[i].t, range))}</text>`;
      }
    }
    svg += `<line class="uv-guide" x1="0" y1="${PADT}" x2="0" y2="${g.base.toFixed(1)}" stroke="var(--line-2)" stroke-width="1" visibility="hidden"/>` +
      `<circle class="uv-dot" r="3.2" fill="var(--gold)" stroke="var(--board)" stroke-width="1.5" visibility="hidden"/>` +
      `<rect class="uv-hit" x="${PADL}" y="0" width="${W - PADL - PADR}" height="${H}" fill="transparent"/></svg>`;
    const models = rep.models || [];
    const allTok = models.reduce((a, m) => a + (m.tokens || 0), 0) || 1;
    const top = models.length ? Math.max(1, models[0].tokens || 1) : 1;
    let html =
      '<div class="uv-chart-head"><strong>' + esc(t('消耗趋势')) + '</strong>' +
      `<span class="uv-sub">${esc(t('窗口合计 {a} · 峰值 {b}', { a: fmtTokens(sum), b: fmtTokens(peak) }))}</span>` +
      '<div class="uv-tabs">' +
      levels.map((k) => `<button type="button" data-uv-range="${k}" class="${k === range ? 'cur' : ''}">${esc(t(RANGE_LABEL[k]))}</button>`).join('') +
      '</div></div>' +
      `<div class="uv-line">${svg}<div class="uv-tip" hidden></div></div>` +
      '<div class="uv-mhead"><strong>' + esc(t('模型对比')) + '</strong>' +
      `<span class="uv-sub">${esc(t('全史合计 · {n} 个模型', { n: models.length }))}</span></div>` +
      '<div class="uv-mrows">';
    for (const m of models.slice(0, 12)) {
      const share = Math.round(((m.tokens || 0) / allTok) * 100);
      html += '<div class="uv-mrow">' +
        `<span class="uv-mname" title="${esc(m.model)}">${esc(m.model)}</span>` +
        `<span class="uv-mbar"><i style="width:${Math.max(2, Math.round(((m.tokens || 0) / top) * 100))}%"></i></span>` +
        `<span class="uv-mtok">${esc(fmtTokens(m.tokens || 0))}</span>` +
        `<span>${esc(t('{a} 次', { a: (m.requests || 0).toLocaleString() }))}</span>` +
        `<span>${share}%</span></div>`;
    }
    html += '</div>';
    box.innerHTML = html;
    this.#bindChartHover(box, points, range, g, W, H);
  }

  // 折线悬停：命中层把指针 x 归到最近桶，纵导线＋点走 viewBox 坐标，
  // 浮牌走视口像素（右缘/顶缘内翻）。
  #bindChartHover(box, points, range, g, W, H) {
    const wrap = box.querySelector('.uv-line');
    const svg = wrap && wrap.querySelector('svg');
    const hit = svg && svg.querySelector('.uv-hit');
    if (!hit) return;
    const guide = svg.querySelector('.uv-guide');
    const dot = svg.querySelector('.uv-dot');
    const tip = wrap.querySelector('.uv-tip');
    const hide = () => {
      guide.setAttribute('visibility', 'hidden');
      dot.setAttribute('visibility', 'hidden');
      tip.hidden = true;
    };
    hit.addEventListener('mousemove', (ev) => {
      if (!points.length) return;
      const rect = svg.getBoundingClientRect();
      const frac = Math.min(1, Math.max(0, (ev.clientX - rect.left) / rect.width));
      const vx = frac * W;
      let best = 0, bd = Infinity;
      for (let i = 0; i < g.pts.length; i++) {
        const d = Math.abs(g.pts[i][0] - vx);
        if (d < bd) { bd = d; best = i; }
      }
      const p = points[best], pt = g.pts[best];
      guide.setAttribute('x1', pt[0].toFixed(1));
      guide.setAttribute('x2', pt[0].toFixed(1));
      guide.setAttribute('visibility', 'visible');
      dot.setAttribute('cx', pt[0].toFixed(1));
      dot.setAttribute('cy', pt[1].toFixed(1));
      dot.setAttribute('visibility', 'visible');
      const chips = (p.models || []).slice(0, 3)
        .map((m) => `<div class="uv-tip-row"><span>${esc(m.model)}</span><span>${esc(fmtTokens(m.tokens || 0))}</span></div>`)
        .join('');
      tip.innerHTML =
        `<div class="uv-tip-t">${esc(fmtBucketLabel(p.t, range))} · ${esc(t('{n} 轮', { n: p.turns || 0 }))}</div>` +
        `<b>${esc(fmtTokens(p.total || 0))}</b>` +
        `<div class="uv-tip-row"><span>${esc(t('净输入'))}</span><span>${esc(fmtTokens(Math.max(0, (p.input || 0) - (p.cache || 0))))}</span></div>` +
        `<div class="uv-tip-row"><span>${esc(t('输出'))}</span><span>${esc(fmtTokens(p.output || 0))}</span></div>` +
        `<div class="uv-tip-row"><span>${esc(t('缓存读'))}</span><span>${esc(fmtTokens(p.cache || 0))}</span></div>` +
        chips;
      tip.hidden = false;
      const tw = tip.offsetWidth, th = tip.offsetHeight;
      const px = (pt[0] / W) * rect.width, py = pt[1] * (rect.height / H);
      let left = px + 10;
      if (left + tw > rect.width - 4) left = px - tw - 10;
      let top = py - th - 8;
      if (top < 0) top = py + 10;
      tip.style.left = left + 'px';
      tip.style.top = top + 'px';
    });
    hit.addEventListener('mouseleave', hide);
  }

  // 明细页：一名员工最近 N 轮（新→旧），每轮附按模型分摊。
  async #loadTurns(title) {
    if (!this.sheet) return;
    this.title = title;
    this.sheet.innerHTML =
      '<div class="sheet-head"><button type="button" class="btn small" data-uv-back>' + esc(t('← 返回')) + '</button>' +
      '<div class="sheet-title"><strong>' + esc(t('加载中…')) + '</strong></div></div>' +
      '<div class="uv-loading"><i class="uv-spin"></i>' + esc(t('正在翻账…')) + '</div>';
    let rep;
    try {
      rep = await getUsageTurns(title, TURN_PAGE);
    } catch (err) {
      this.#fail(err, t('耗粮明细读取失败'));
      return;
    }
    if (!this.sheet || this.title !== title) return; // 已返回/已收窗
    let html =
      '<div class="sheet-head"><button type="button" class="btn small" data-uv-back>' + esc(t('← 返回')) + '</button>' +
      `<div class="sheet-title"><strong>${esc(title)}</strong>` +
      `<span class="uv-sub">${esc(t('{n} 条会话 · 最近 {m} 轮（新→旧）', { n: rep.sessions || 0, m: rep.turns ? rep.turns.length : 0 }))}</span></div></div>`;
    if (!rep.turns || !rep.turns.length) {
      html += '<div class="uv-empty">' + esc(t('该员工还没有回合记录。')) + '</div>';
    } else {
      html += '<div class="uv-turns">';
      for (const turn of rep.turns) {
        const models = (turn.models || [])
          .map((m) => `<span class="uv-chip">${esc(m.model)} ×${m.requests || 0} · ${esc(fmtTokens(m.tokens || 0))}</span>`)
          .join('');
        html += `<div class="uv-turn${turn.total ? '' : ' uv-zero'}">` +
          '<div class="uv-t-top">' +
          `<span class="uv-t-time">${turn.started_at ? esc(fmtDateTime(Math.floor(turn.started_at / 1000))) : '—'}</span>` +
          `<span class="uv-t-status s-${esc(turn.status || '')}">${esc(turnStatusText(turn.status))}</span>` +
          `<span class="uv-dim">${esc(fmtDur(turn.duration_ms))}</span>` +
          `<span class="uv-dim">${esc(t('{a} 次模型 · {b} 次工具', { a: turn.model_requests || 0, b: turn.tool_calls || 0 }))}</span>` +
          '</div>' +
          '<div class="uv-t-stats">' +
          `<span class="uv-stat"><i>${esc(t('净输入'))}</i><b>${esc(fmtTokens((turn.input || 0) - (turn.cache_read || 0)))}</b></span>` +
          `<span class="uv-stat"><i>${esc(t('输出'))}</i><b>${esc(fmtTokens(turn.output || 0))}</b></span>` +
          `<span class="uv-stat"><i>${esc(t('缓存读'))}</i><b>${esc(fmtTokens(turn.cache_read || 0))}</b></span>` +
          `<span class="uv-stat uv-stat-hot"><i>${esc(t('总计'))}</i><b>${esc(fmtTokens(turn.total || 0))}</b></span>` +
          '</div>' +
          (models ? `<div class="uv-t-models">${models}</div>` : '') +
          '</div>';
      }
      html += '</div>';
    }
    this.sheet.innerHTML = html;
  }

  // 读失败：抽屉里给一行可读原因（关不掉的账本不值得红整个面板）。
  #fail(err, what) {
    if (!this.sheet) return;
    this.sheet.innerHTML =
      '<div class="sheet-head"><div class="sheet-title"><strong>' + esc(t('耗粮统计')) + '</strong></div>' +
      '<button type="button" class="btn small" data-uv-close>' + esc(t('关闭')) + '</button></div>' +
      `<div class="uv-empty uv-error">${esc(t('{a}：{b}', { a: what, b: err && err.message ? err.message : String(err) }))}</div>`;
  }
}

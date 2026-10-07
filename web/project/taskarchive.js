// taskarchive.js — t_212（r_26）：任务档案浮层——「task show」的图形
// 兄弟。定稿 §一/§二：
//   两段式（上字段区紧凑两行网格：编号/标题/状态/负责人/起止/挂靠需求/
//   进度；下 log 时间线区占余高可滚动）；
//   三层折叠（骨架常显——状态流转条目永远平铺；同日合并——同日多条备
//   注折成「N 条备注 · MM-DD」点击展开；跨日分组——日期分隔线
//   chron-item 视觉同族）；空 log 降级「（暂无流转记录）」。
// 浮层规格：480px 宽、浮层栈同族（.sheet-wrap 同类）、Esc 同语义。
// 数据：getTask(id)——与 CLI task show 同一 store 同一形状（验收①的
// 同源断言基础）。纯读零写路径。
//
// 入口（定稿 §一入口契约——两处入口同浮层）：甘特 selbar「查档案」钮
// （gantt.js 调 TaskArchive.open）与任务卡/overview（TaskDrawer 的
// 变更日志节升级三层折叠同款渲染，见 foldLogHTML——两处共享本文件的
// 折叠逻辑）。

import { getTask } from '../wire/api.js';
import { esc, fmtDateTime, icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

// ── 三层折叠的纯逻辑（可单测的核）──────────────────────────────────

// isSkeleton：状态流转骨架条目（创建/指派/doing/done/cancelled 关键词）
// ——生命历程的骨架永远平铺（定稿 §二）。
export function isSkeleton(note) {
  return /创建任务|指派给|状态|接单|婉拒/.test(note || '');
}

// foldLog：log[] → 渲染模型 {days: [{date, rows: [{type, entries}]}]}
//   骨架条目逐条平铺（type:'row'）；同日连续的非骨架条目折成
//   type:'fold'（N 条 · MM-DD，点击展开）；跨日切日期分隔线。
// 纯函数——DOM 冒烟直接喂构造 log 断言（破坏点：注掉合并分支断言红）。
export function foldLog(log) {
  const days = [];
  const byDate = new Map();
  const dayOf = (ts) => {
    const d = new Date((ts || 0) * 1000);
    const p = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  };
  for (const l of log || []) {
    const date = dayOf(l.ts);
    if (!byDate.has(date)) {
      const day = { date, rows: [] };
      byDate.set(date, day);
      days.push(day);
    }
    const day = byDate.get(date);
    const last = day.rows[day.rows.length - 1];
    if (isSkeleton(l.note)) {
      day.rows.push({ type: 'row', entry: l });
    } else if (last && last.type === 'fold') {
      last.entries.push(l); // 同日同折：连续备注继续并
    } else {
      day.rows.push({ type: 'fold', entries: [l] });
    }
  }
  return days;
}

// foldLogHTML：折叠模型 → HTML（展开态由 expandedDays 控制——Set of
// `${date}#${idx}`；不传=全收）。骨架行带人带时间戳；折行带条数。
export function foldLogHTML(log, expanded) {
  const days = foldLog(log);
  if (!days.length) return `<div class="dim small">${t('（暂无流转记录）')}</div>`;
  const set = expanded || new Set();
  let html = '';
  let foldIdx = 0;
  for (const day of days) {
    html += `<div class="arch-day"><span class="chron-date">${esc(day.date)}</span></div>`;
    for (const row of day.rows) {
      if (row.type === 'row') {
        html += `<div class="arch-row"><span class="dim small">${fmtDateTime(row.entry.ts)} · ${esc(row.entry.by)}</span>` +
          `<div>${esc(row.entry.note)}</div></div>`;
      } else {
        const key = `${day.date}#${foldIdx++}`;
        const open = set.has(key);
        html += `<button type="button" class="arch-fold${open ? ' open' : ''}" data-fold="${esc(key)}">` +
          `${t('{n} 条备注 · {d}', { n: row.entries.length, d: esc(day.date.slice(5)) })}${open ? ' ▴' : ' ▾'}</button>`;
        if (open) {
          html += row.entries.map((l) =>
            `<div class="arch-row arch-note"><span class="dim small">${fmtDateTime(l.ts)} · ${esc(l.by)}</span>` +
            `<div>${esc(l.note)}</div></div>`).join('');
        }
      }
    }
  }
  return html;
}

// ── 浮层本体 ──────────────────────────────────────────────────────

export class TaskArchive {
  constructor() {
    this.el = null;
    this.task = null;
    this.expanded = new Set();
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
  }

  /** 打开一张单的档案：getTask 全量拉取（含 log），错误降级不白屏。
   * project 把裸号限定到一架解析（v2.10 号段分项目——履历行知道自己
   * 的线在哪个项目；缺省＝宿主面全室唯一解析）。 */
  async open(id, project) {
    this.close();
    this.expanded = new Set();
    const el = document.createElement('div');
    el.className = 'sheet-wrap';
    el.innerHTML = `<div class="sheet arch-sheet" role="dialog" aria-label="${t('任务档案')}"></div>`;
    // testdom 的 El 没有 appendChild 到 body 的通路（冒烟面只读
    // innerHTML）——挂 body 失败不致命，浮层引用在手即可断言
    try {
      document.body.appendChild(el);
    } catch { /* stub 世界：el 已是活引用 */ }
    this.el = el;
    // testdom 的惰性 querySelector 每调一新节点——活引用一次取定
    this.sheet = el.querySelector('.arch-sheet');
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      const fold = ev.target.closest('[data-fold]');
      if (fold) {
        const k = fold.dataset.fold;
        if (this.expanded.has(k)) this.expanded.delete(k); else this.expanded.add(k);
        this.#paintLog();
      }
    });
    document.addEventListener('keydown', this.onDocKey);
    this.sheet.innerHTML =
      '<div class="side-skeleton"><div class="sk-line w60"></div><div class="sk-line"></div><div class="sk-line w40"></div></div>';
    try {
      this.task = await getTask(id, project);
    } catch (err) {
      this.sheet.innerHTML =
        `<div class="state-card error"><strong>${t('档案读取失败')}</strong>` +
        `<span>${esc(err?.message || String(err))}</span>` +
        `<button type="button" class="btn" data-retry>${t('重试')}</button></div>`;
      el.querySelector('[data-retry]').onclick = () => { this.close(); this.open(id, project); };
      return;
    }
    this.#paint();
  }

  close() {
    if (!this.el) return;
    this.el.remove();
    this.el = this.sheet = null;
    this.task = null;
    document.removeEventListener('keydown', this.onDocKey);
  }

  #paint() {
    if (!this.sheet || !this.task) return;
    const task = this.task;
    const kv = (k, v) => v ? `<div class="kv"><span>${k}</span><b>${esc(v)}</b></div>` : '';
    this.sheet.innerHTML =
      '<div class="sheet-head">' +
        `<div class="sheet-title"><strong>${esc(task.title)}</strong>` +
          `<span class="dim small">${esc(task.id)} · ${esc(task.assignee || t('任务池'))} · ${esc(task.status)}` +
          `${task.req ? ` · ${esc(task.req)}` : ''}${task.progress_pct ? ` · ${task.progress_pct}%` : ''}</span></span>` +
        `<button type="button" class="icon-btn" data-close>${icon('close', 14)}</button>` +
      '</div>' +
      '<div class="arch-fields">' +
        kv(t('负责人'), task.assignee) + kv(t('状态'), task.status) +
        kv(t('起'), task.start_ts ? fmtDateTime(task.start_ts) : '') +
        kv(t('止'), task.end_ts ? fmtDateTime(task.end_ts) : '') +
        kv(t('挂靠需求'), task.req) + kv(t('提案'), task.plan_id) +
        kv(t('创建'), task.created_by) + kv(t('进度'), task.progress || (task.progress_pct ? `${task.progress_pct}%` : '')) +
      '</div>' +
      `<div class="arch-log" id="arch-log">${foldLogHTML(task.log, this.expanded)}</div>`;
    this.sheet.querySelector('[data-close]').onclick = () => this.close();
  }

  #paintLog() {
    // 活引用下整面重绘（浮层小，无需局部重绘；重挂 close 钮）
    this.#paint();
  }
}

// 单例：两处入口共享一个浮层（开着 A 再点 B——close+open 换单）
let shared;
export function openArchive(id, project) {
  if (!shared) shared = new TaskArchive();
  shared.open(id, project);
  return shared;
}

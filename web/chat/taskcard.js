// taskcard.js — the Feishu-style task card family: ONE shared renderer
// (taskCardHTML) paints both the inline stream cards (task frames carry
// a fresh snapshot) and the t_NN popover, and the popover controller
// (TaskCard) speaks the task verbs over the room's owner write face —
// 仲裁批准/驳回 for pending proposals seen as the host (接单/婉拒 only
// when the host sits in the Need list), 开始/完成/取消 for open tasks.
// Ops are fired back through onOp (StreamView routes them via book.frameTo);
// denials toast privately (app.js), successes broadcast a fresh task
// frame that re-renders the stream card in place.

import { getTask } from '../wire/api.js';
import { esc, fmtAge, fmtDateTime, memberColor, icon, dismiss } from '../ui/dom.js';
// t 别名 i18n：本文件的 t 一律是任务对象（taskStatus(t)/pctOf(t)/
// taskCardHTML(t)/detailRows(t)），直译函数走别名不与其遮蔽打架。
import { t as i18n } from '../ui/i18n.js';
import { foldZoneHTML, wireFoldZones, toggleFoldBtn } from './fold.js';

const STATUS = {
  todo: { label: i18n('待处理'), icon: 'circle', cls: 'st-todo' },
  doing: { label: i18n('进行中'), icon: 'doing', cls: 'st-doing' },
  done: { label: i18n('已完成'), icon: 'check', cls: 'st-done' },
  cancelled: { label: i18n('已取消'), icon: 'close', cls: 'st-cancelled' },
};
const PENDING = { label: i18n('待确认'), icon: 'pending', cls: 'st-pending' };

/** The card's one status: a pending proposal outranks the ledger status. */
export function taskStatus(t) {
  return t.pending
    ? PENDING
    : (STATUS[t.status] || { label: t.status || '—', icon: 'circle', cls: 'st-todo' });
}

/** progress_pct, or the percent inside the human note ("96% · …") as display fallback. */
export function pctOf(t) {
  if (t.progress_pct > 0) return t.progress_pct;
  const m = /(\d{1,3})\s*%/.exec(t.progress || '');
  return m ? Math.min(100, parseInt(m[1], 10)) : 0;
}

// op -> [button label, button class]
const OPS = {
  confirm: [i18n('接单'), 'gold'],
  decline: [i18n('婉拒'), ''],
  approve: [i18n('批准（仲裁）'), 'gold'],
  reject: [i18n('驳回（仲裁）'), ''],
  start: [i18n('开始任务'), 'gold'],
  finish: [i18n('标记完成'), 'gold'],
  cancel: [i18n('取消任务'), ''],
};

/**
 * The shared card body: status icon + title, assignee avatar, progress
 * bar, and — in the popover's detail mode — the schedule/ledger rows.
 * `ops` renders the action row for the CURRENT state; closed tasks and
 * inline compact cards can opt out.
 *
 * The workbench is the HOST's face, and the protocol grants the host
 * different verbs than the Need list: 接单/婉拒 belong to the people in
 * pending.need (the host lands there only when a member proposed onto
 * the host's own task); otherwise the host's verdict is 仲裁
 * (task_arbitrate — approve applies the patch at once, reject discards
 * it). Drawing 接单 for the host paints a button the engine must deny
 * （「你不是该变更的需确认人」）.
 * @param {import('../wire/wire.js').TaskFull} t
 * @param {{ops?: boolean, detail?: boolean, viewer?: string}} [o]
 */
export function taskCardHTML(t, o = {}) {
  const st = taskStatus(t);
  const pct = Math.min(100, pctOf(t));
  const color = memberColor(t.assignee || '');
  const closed = !t.pending && (t.status === 'done' || t.status === 'cancelled');
  const p = t.pending || null;
  const viewerInNeed = p && o.viewer && (p.need || []).includes(o.viewer);
  const ops = o.ops === false ? [] :
    p ? (viewerInNeed ? ['confirm', 'decline'] : ['approve', 'reject']) :
    closed ? [] :
    (t.status === 'todo' ? ['start', 'finish', 'cancel'] : ['finish', 'cancel']);
  return (
    `<div class="tk-card" data-task="${esc(t.id)}">` +
      '<div class="tk-head">' +
        `<span class="tk-icon ${st.cls}">${icon(st.icon, 12)}</span>` +
        `<span class="tk-title">${esc(t.title)}</span>` +
        `<span class="tk-id">${esc(t.id)}</span>` +
        `<span class="tag ${st.cls}">${st.label}</span>` +
      '</div>' +
      (t.desc
        // 描述是任务卡唯一会长的部位：折叠区接管（fold.js）——超线（约
        // 6 行）自动收起成渐隐预览＋「展开」，短描述零铬面。替代旧的
        // line-clamp:2 硬裁——裁掉的字再也没处看，折叠是完整留这的。
        ? `<div class="tk-desc">${foldZoneHTML(esc(t.desc))}</div>`
        : '') +
      (p
        ? `<div class="tk-wait">${i18n('变更申请（{kind}）· 发起 {by}', { kind: esc(p.kind || 'change'), by: esc(p.by || '—') })}` +
          `${i18n(' · 等待 {who}', { who: esc((p.need || []).join(i18n('、')) || '…') })}</div>`
        : '') +
      '<div class="tk-meta">' +
        (t.assignee
          ? `<span class="tk-owner"><i class="dot" style="background:${color}"></i>${esc(t.assignee)}</span>`
          : `<span class="tk-owner dim">${i18n('任务池（未指派）')}</span>`) +
        (pct > 0
          ? `<span class="tk-pct"><i class="tk-bar"><b style="width:${pct}%;background:${color}"></b></i>${pct}%</span>`
          : '') +
        (t.milestone ? `<span class="tk-ms">${icon('milestone', 10)} ${i18n('里程碑')}</span>` : '') +
      '</div>' +
      (o.detail ? `<div class="tk-rows">${detailRows(t)}</div>` : '') +
      (ops.length
        ? '<div class="tk-ops">' + ops.map((op) =>
            `<button type="button" class="btn small${OPS[op][1] ? ' ' + OPS[op][1] : ''}" data-op="${op}">${OPS[op][0]}</button>`).join('') + '</div>'
        : '') +
    '</div>');
}

function detailRows(t) {
  const rows = [
    [i18n('排期'), t.start_ts
      ? `${fmtDateTime(t.start_ts)} ~ ${t.end_ts ? fmtDateTime(t.end_ts) : i18n('开放')}`
      : i18n('未排期（收件格）')],
  ];
  if (t.req) rows.push([i18n('需求'), esc(t.req)]);
  if (t.version) rows.push([i18n('版本'), esc(t.version)]);
  if ((t.deps || []).length) rows.push([i18n('依赖'), esc(t.deps.join(', '))]);
  rows.push([i18n('更新'), fmtAge(t.updated_ts || t.created_ts)]);
  return rows.map(([k, v]) => `<div class="kv"><span>${k}</span><b>${v}</b></div>`).join('');
}

/**
 * TaskCard — the t_NN popover: opens by id (fetches GET /kb/tasks/{id})
 * or with a snapshot in hand (the inline stream cards), anchors to the
 * clicked element, and routes its op buttons through onOp.
 */
export class TaskCard {
  /** @param {{onOp?: (task: import('../wire/wire.js').TaskFull, op: string) => boolean,
   *             toast?: import('../ui/toast.js').Toast,
   *             viewer?: string | (() => string)}} [opts] — viewer names the
   *    host (book.owner?.name), read lazily so a late identity resolution
   *    repaints right on the next paint. */
  constructor(opts = {}) {
    this.opts = opts;
    this.el = null;
    this.task = null;
    this.cache = new Map(); // id -> Task | null (null = unknown id)
    this.onDocClick = (ev) => { if (this.el && !this.el.contains(ev.target)) this.close(); };
    this.onKey = (ev) => { if (ev.key === 'Escape') this.close(); };
  }

  /** Open by id (the t_NN chips): fetch, then render. project scopes the
   * bare id to one shelf (v2.10 号段分项目——房间语境的 chip 带本房 key；
   * 缺省＝宿主面全室唯一解析). */
  open(id, anchor, project) {
    this.#mount(anchor);
    this.#render(id, project);
  }

  /** Open with a snapshot already carried by the frame — no fetch. */
  openWith(task, anchor) {
    this.cache.set(task.id, task);
    this.#mount(anchor);
    this.#paint(task);
  }

  #mount(anchor) {
    this.close();
    const el = document.createElement('div');
    el.className = 'task-card';
    el.innerHTML = `<div class="tc-bar"><span>${i18n('任务卡片')}</span></div><div class="tc-body dim">${i18n('读取中…')}</div>`;
    document.body.appendChild(el);
    this.el = el;
    this.anchorRect = anchor.getBoundingClientRect();
    this.#clamp();
    el.addEventListener('click', (ev) => this.#onClick(ev));
    document.addEventListener('click', this.onDocClick, true);
    document.addEventListener('keydown', this.onKey);
  }

  // Keep the card inside the viewport: below the anchor by default,
  // flipped above when the fold would cut the actions off (re-run after
  // each paint — the async fetch grows the card).
  #clamp() {
    const el = this.el;
    if (!el) return;
    const a = this.anchorRect;
    const width = 340;
    const margin = 8;
    const h = el.offsetHeight || 300;
    let top = a.bottom + 6;
    if (top + h > innerHeight - margin) top = a.top - h - 6;
    if (top < margin || top + h > innerHeight - margin) {
      top = Math.max(margin, Math.min(a.bottom + 6, innerHeight - h - margin));
    }
    el.style.left = `${Math.max(margin, Math.min(a.left, innerWidth - width - margin))}px`;
    el.style.top = `${Math.max(margin, top)}px`;
    el.style.width = `${width}px`;
  }

  /** Refresh the open popover in place (a private task outcome arrived). */
  repaint(task) {
    if (!this.el || this.task?.id !== task.id) return;
    this.#paint(task);
  }

  close() {
    if (!this.el) return;
    dismiss(this.el, 150);
    this.el = null;
    this.task = null;
    document.removeEventListener('click', this.onDocClick, true);
    document.removeEventListener('keydown', this.onKey);
  }

  #onClick(ev) {
    const foldBtn = ev.target.closest('[data-mcfold]');
    if (foldBtn) { // 描述折叠区在卡内（taskCardHTML 共用渲染）：先于关钮/
      // 操作钮——展开描述不是点卡，也不该关掉刚看的卡
      toggleFoldBtn(foldBtn);
      return;
    }
    if (ev.target.closest('[data-tc-close]')) { this.close(); return; }
    const op = ev.target.closest('[data-op]');
    if (op && this.task) {
      const ok = this.opts.onOp?.(this.task, op.dataset.op);
      if (ok !== false) this.close(); // a refused op (read-only face) keeps the card up
      return;
    }
    if (ev.target.closest('[data-tc-jump]')) {
      this.close();
      location.hash = '#/project';
    }
  }

  async #render(id, project) {
    const el = this.el;
    if (!el) return; // closed while fetching
    const cacheKey = (project || '') + '/' + id;
    let task = this.cache.get(cacheKey);
    if (task === undefined) {
      try {
        task = await getTask(id, project);
      } catch (err) {
        task = err.status === 404 ? null : undefined; // undefined = transient failure, no cache
      }
      if (task !== undefined) this.cache.set(cacheKey, task);
    }
    if (!this.el || this.el !== el) return;
    if (task === null) {
      el.innerHTML = `<div class="tc-bar"><span>${i18n('任务卡片')}</span></div>` +
        `<div class="tc-body dim">${i18n('{id} 不在任务台账里（可能来自旧编号）', { id: esc(id) })}</div>`;
      return;
    }
    if (!task) {
      el.innerHTML = `<div class="tc-bar"><span>${i18n('任务卡片')}</span></div>` +
        `<div class="tc-body dim">${i18n('读取失败，关闭后重试')}</div>`;
      return;
    }
    this.#paint(task);
  }

  #paint(task) {
    const el = this.el;
    if (!el) return;
    this.task = task;
    const viewer = typeof this.opts.viewer === 'function' ? this.opts.viewer() : this.opts.viewer;
    el.innerHTML =
      `<div class="tc-bar"><span>${i18n('任务 · {id}', { id: esc(task.id) })}</span>` +
      `<button type="button" class="icon-btn" data-tc-close title="${i18n('关闭')}">` + icon('close') + '</button></div>' +
      taskCardHTML(task, { detail: true, viewer }) +
      `<button type="button" class="tc-jump" data-tc-jump>${i18n('在项目管理中查看 →')}</button>`;
    wireFoldZones(el); // 描述折叠区量高（先于 clamp——折叠后的高度才是卡高）
    this.#clamp(); // the fetched body is taller than the 读取中 placeholder
  }
}

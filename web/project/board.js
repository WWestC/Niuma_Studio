// board.js — the project board's task ledger, laid out the Feishu
// 任务台 way (前端稿 §4.3 的飞书化重排): one toolbar (看板/列表视图切换、
// 搜索、负责人/优先级筛选、只看我的、看板分组切换), one stats strip (六个快滤
// 胶囊：待确认/待处理/进行中/已完成/已取消/逾期，点击过滤), the kanban
// with drag-to-move cards (状态/负责人/优先级列落下即改——仍是
// 一个 task_update，评级/协商机制全在服务端), and a 多维表格-style list
// view (父子任务树形缩进、行内状态下拉/优先级下拉、表头排序). Cards carry
// the Feishu card facts: owner avatar, due chip (逾期红/今天橙),
// priority tag, milestone, req/version 标签, progress bar. The detail
// drawer (taskdrawer.js) opens on click and live-repaints off owner
// receipts. Ops ride the WS task_* frames over the viewed project's
// owner write face; denials arrive privately and toast in app.js.

import { getProjectTasks } from '../wire/api.js';
import { Msg } from '../wire/wire.js';
import { esc, fmtAge, memberColor, avatarText, icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { goneTagHTML } from '../ui/person.js';
import {
  STATUS_META, statusMeta, isClosed, pctOf, isOverdue, dueChip,
  PRIORITY_META, priorityRank, priorityChipHTML,
} from './taskmeta.js';
import { TaskDrawer } from './taskdrawer.js';

const GROUPS = [
  { id: 'pending', label: t('待确认'), match: (t) => !!t.pending },
  { id: 'doing', label: t('进行中'), match: (t) => !t.pending && t.status === 'doing' },
  { id: 'todo', label: t('待处理'), match: (t) => !t.pending && t.status === 'todo' },
  { id: 'done', label: t('已完成'), match: (t) => !t.pending && t.status === 'done' },
  { id: 'cancelled', label: t('已取消'), match: (t) => !t.pending && t.status === 'cancelled' },
];

// The stats strip's quick filters: one chip per state plus the derived
// 逾期 — clicking toggles the same key as a status filter.
const STATS = [
  { id: 'todo', label: t('待处理'), match: (t) => !t.pending && t.status === 'todo', dot: STATUS_META.todo.dot },
  { id: 'doing', label: t('进行中'), match: (t) => !t.pending && t.status === 'doing', dot: STATUS_META.doing.dot },
  { id: 'done', label: t('已完成'), match: (t) => !t.pending && t.status === 'done', dot: STATUS_META.done.dot },
  { id: 'cancelled', label: t('已取消'), match: (t) => !t.pending && t.status === 'cancelled', dot: STATUS_META.cancelled.dot },
  { id: 'pending', label: t('待确认'), match: (t) => !!t.pending, dot: '#ff8800' },
  { id: 'overdue', label: t('逾期'), match: (t) => isOverdue(t), dot: '#f54a45' },
];

// 分组=优先级 的五列（未设置收尾）：优先级列可拖卡改级，未设置列落下
// 即清除（协议里 priority 支持 "-"，和指派不同——清空是一等操作）。
const PRIORITY_GROUPS = [
  ...Object.entries(PRIORITY_META).map(([id, m]) => ({ id, label: m.label, cls: m.cls, key: id })),
  { id: 'unset', label: t('未设置'), cls: '', key: '-' },
];

export class TaskBoardView {
  /**
   * @param {HTMLElement} container #ppane-board
   * @param {import('./project.js').ProjectBoard} board
   */
  constructor(container, board) {
    this.container = container;
    this.board = board;
    this.drawer = new TaskDrawer(board);
    // view state — kept across reloads so a background event tap never
    // resets the user's lens (Feishu-style: the filter is the user's)
    this.view = 'kanban';     // kanban | list
    this.groupBy = 'status';  // status | assignee | priority
    this.q = '';
    this.assignee = '';       // '' 全部 | '__pool__' 任务池 | name
    this.priority = '';       // '' 全部 | '__unset__' 未设置 | urgent|high|medium|low
    this.mine = false;        // 只看我的（房主）
    this.statusFilter = '';   // '' | STATS id
    this.sortKey = 'updated'; // updated | end | title | pct | priority
    this.sortDir = -1;
    this.tasks = null;
    this.dragId = '';         // the card under drag (DnD dataTransfer twin)

    this.#paintShell();
    this.#bindShell();
  }

  /** Idle paint before the first load (board builds all panes up front). */
  renderIdle() {
    this.paintData('<div class="sk-line"></div><div class="sk-line w60"></div>');
  }

  async reload() {
    const key = this.board.key;
    // skeleton only on a truly fresh paint (first load / project switch):
    // background refreshes repaint in place — a skeleton every event tap
    // reads as flicker, not progress
    const fresh = this._loadedKey !== key || !this.tasks;
    this._loadedKey = key;
    if (fresh) {
      this.paintData('<div class="kanban">' + GROUPS.map(() =>
        '<div class="kcol"><div class="sk-line"></div><div class="kcard skeleton"></div><div class="kcard skeleton"></div></div>').join('') +
        '</div>');
    }
    let tasks;
    try {
      tasks = await getProjectTasks(key);
    } catch (err) {
      // a background refresh failure keeps the stale view on screen —
      // wiping it would punish the user for a transient blip
      if (fresh) {
        this.paintData(
          `<div class="state-card error"><strong>${t('任务台账读取失败')}</strong><span>${esc(err.message || String(err))}</span>` +
          `<button type="button" class="btn" data-retry>${t('重试')}</button></div>`);
      }
      return;
    }
    this.tasks = tasks;
    if (!tasks.length) {
      this.els.stats.innerHTML = '';
      this.paintData(
        `<div class="state-card"><strong>${t('这个项目还没有任务')}</strong>` +
        `<span>${t('录入需求后「让编排者拆解」——任务经提案入库，长在甘特上；或直接「+ 新建任务」')}</span></div>`);
      return;
    }
    this.#paintStats();
    this.#paintAssigneeChoices();
    this.paintData(this.view === 'kanban' ? this.#kanbanHTML() : this.#listHTML());
    this.drawer.refresh(tasks);
  }

  /** The owner-face task receipt (app.js → project.js): live drawer repaint. */
  drawerReceipt(f) { this.drawer.syncFromFrame(f); }

  // --- shell (toolbar + stats + data well, painted once) --------------------

  #paintShell() {
    this.container.innerHTML =
      '<div class="tbar">' +
        '<div class="seg" role="tablist">' +
          `<button type="button" data-view="kanban">${t('看板')}</button>` +
          `<button type="button" data-view="list">${t('列表')}</button>` +
        '</div>' +
        `<input class="input tsearch" data-search placeholder="${t('搜索任务：标题 / 编号 / 说明')}" />` +
        '<select class="input tsel" data-f-assignee></select>' +
        '<select class="input tsel" data-f-priority></select>' +
        `<button type="button" class="rfilter" data-f-mine hidden>${t('只看我的')}</button>` +
        '<span class="tabs-spacer"></span>' +
        `<span class="tgroup" data-kanban-only>${t('分组')}` +
          ' <select class="input tsel" data-group>' +
            `<option value="status">${t('按状态')}</option><option value="assignee">${t('按负责人')}</option>` +
            `<option value="priority">${t('按优先级')}</option>` +
          '</select></span>' +
      '</div>' +
      '<div class="stat-strip" data-stats></div>' +
      '<div data-data></div>';
    this.els = {
      seg: this.container.querySelector('.seg'),
      search: this.container.querySelector('[data-search]'),
      assignee: this.container.querySelector('[data-f-assignee]'),
      priority: this.container.querySelector('[data-f-priority]'),
      mine: this.container.querySelector('[data-f-mine]'),
      groupWrap: this.container.querySelector('[data-kanban-only]'),
      group: this.container.querySelector('[data-group]'),
      stats: this.container.querySelector('[data-stats]'),
      data: this.container.querySelector('[data-data]'),
    };
  }

  #bindShell() {
    const on = (el, ev, fn) => el.addEventListener(ev, fn);
    on(this.els.seg, 'click', (ev) => {
      const b = ev.target.closest('[data-view]');
      if (!b || b.dataset.view === this.view) return;
      this.view = b.dataset.view;
      this.#paintSeg();
      if (this.tasks) this.paintData(this.view === 'kanban' ? this.#kanbanHTML() : this.#listHTML());
    });
    on(this.els.search, 'input', () => { this.q = this.els.search.value.trim(); this.#repaintData(); });
    on(this.els.search, 'keydown', (ev) => {
      // Esc 一击清搜索（再按 Esc 落到抽屉/浮层的关闭）
      if (ev.key === 'Escape' && this.els.search.value) {
        ev.stopPropagation();
        this.els.search.value = '';
        this.q = '';
        this.#repaintData();
      }
    });
    on(this.els.assignee, 'change', () => { this.assignee = this.els.assignee.value; this.#repaintData(); });
    this.els.priority.innerHTML =
      `<option value="">${t('全部优先级')}</option>` +
      Object.entries(PRIORITY_META).map(([k, m]) => `<option value="${k}">${t('{p}优先级', { p: m.label })}</option>`).join('') +
      `<option value="__unset__">${t('未设置')}</option>`;
    this.els.priority.value = this.priority;
    on(this.els.priority, 'change', () => { this.priority = this.els.priority.value; this.#repaintData(); });
    on(this.els.mine, 'click', () => {
      this.mine = !this.mine;
      this.els.mine.classList.toggle('active', this.mine);
      this.#repaintData();
    });
    on(this.els.group, 'change', () => { this.groupBy = this.els.group.value; this.#repaintData(); });
    on(this.els.stats, 'click', (ev) => {
      const chip = ev.target.closest('[data-stat]');
      if (!chip) return;
      this.statusFilter = this.statusFilter === chip.dataset.stat ? '' : chip.dataset.stat;
      this.#paintStats();
      this.#repaintData();
    });
    // data-area events survive every repaint (delegation on the well)
    on(this.els.data, 'click', (ev) => {
      if (this._justDragged) return; // a drag ending on the card is not a click
      const retry = ev.target.closest('[data-retry]');
      if (retry) { this.reload(); return; }
      const clear = ev.target.closest('[data-clear-filters]');
      if (clear) { this.#clearFilters(); return; }
      const th = ev.target.closest('th[data-sort]');
      if (th) {
        const key = th.dataset.sort;
        if (this.sortKey === key) this.sortDir = -this.sortDir;
        else { this.sortKey = key; this.sortDir = key === 'title' ? 1 : -1; }
        this.#repaintData();
        return;
      }
      // form controls inside a row/select own the click — the drawer
      // must not yank the pane open just because the user aimed at one
      if (ev.target.closest('select, input, option')) return;
      const card = ev.target.closest('[data-task]');
      if (card) {
        const task = this.tasks?.find((t) => t.id === card.dataset.task);
        if (task) this.drawer.open(task);
      }
    });
    on(this.els.data, 'change', (ev) => {
      const sel = ev.target.closest('select[data-st]');
      if (sel) {
        const id = sel.dataset.st;
        const task = this.tasks?.find((t) => t.id === id);
        if (!task || sel.value === task.status) return;
        this.board.frameTo(
          { type: Msg.TaskUpdate, task_id: id, patch: { status: sel.value } },
          t('{id} 状态→{v}', { id, v: STATUS_META[sel.value]?.label || sel.value }));
        return;
      }
      // 行内优先级下拉：未设置选项发 "-"（协议的清除键，空串=不改）
      const pri = ev.target.closest('select[data-pri]');
      if (pri) {
        const id = pri.dataset.pri;
        const task = this.tasks?.find((t) => t.id === id);
        if (!task || (pri.value || '') === (task.priority || '')) return;
        const patch = pri.value ? { priority: pri.value } : { priority: '-' };
        this.board.frameTo(
          { type: Msg.TaskUpdate, task_id: id, patch },
          t('{id} 优先级→{v}', { id, v: pri.value ? PRIORITY_META[pri.value]?.label || pri.value : t('未设置') }));
      }
    });
    on(this.els.data, 'keydown', (ev) => {
      if (ev.target.closest('select[data-st]') && ev.key === 'Enter') ev.stopPropagation();
      // kanban cards are div[role=button] — give Enter/Space the click
      if ((ev.key === 'Enter' || ev.key === ' ') && ev.target.closest?.('[role="button"][data-task]')) {
        ev.preventDefault();
        const card = ev.target.closest('[data-task]');
        const task = this.tasks?.find((t) => t.id === card.dataset.task);
        if (task) this.drawer.open(task);
      }
    });
    // HTML5 DnD: the card supplies its id, the column supplies the patch
    on(this.els.data, 'dragstart', (ev) => {
      const card = ev.target.closest?.('[data-task][draggable="true"]');
      if (!card) { ev.preventDefault(); return; }
      this.dragId = card.dataset.task;
      card.classList.add('dragging');
      ev.dataTransfer.effectAllowed = 'move';
      ev.dataTransfer.setData('text/plain', this.dragId);
    });
    on(this.els.data, 'dragend', () => {
      this.dragId = '';
      this._justDragged = true;
      setTimeout(() => { this._justDragged = false; }, 0);
      this.els.data.querySelectorAll('.dragging').forEach((el) => el.classList.remove('dragging'));
      this.els.data.querySelectorAll('.kcol.drag-over').forEach((el) => el.classList.remove('drag-over'));
    });
    on(this.els.data, 'dragover', (ev) => {
      const col = ev.target.closest?.('[data-drop]');
      if (!col || !this.#dropPatch(col)) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = 'move';
      col.classList.add('drag-over');
    });
    on(this.els.data, 'dragleave', (ev) => {
      const col = ev.target.closest?.('[data-drop]');
      if (col && !col.contains(ev.relatedTarget)) col.classList.remove('drag-over');
    });
    on(this.els.data, 'drop', (ev) => {
      const col = ev.target.closest?.('[data-drop]');
      if (!col) return;
      ev.preventDefault();
      col.classList.remove('drag-over');
      const id = ev.dataTransfer.getData('text/plain') || this.dragId;
      const patch = this.#dropPatch(col, id);
      if (!patch) return;
      const what = patch.status ? t('状态→{v}', { v: STATUS_META[patch.status]?.label || patch.status })
        : patch.priority ? t('优先级→{v}', { v: PRIORITY_META[patch.priority]?.label || t('未设置') })
        : t('指派给 {v}', { v: patch.assignee });
      this.board.frameTo({ type: Msg.TaskUpdate, task_id: id, patch }, `${id} ${what}`);
    });
  }

  /** The patch a drop onto col would produce for task id ('' = not droppable). */
  #dropPatch(col, id = this.dragId) {
    const task = this.tasks?.find((t) => t.id === id);
    if (!task || task.pending || isClosed(task)) return null;
    if (col.dataset.drop === 'status') {
      const st = col.dataset.col;
      return st && st !== task.status ? { status: st } : null;
    }
    if (col.dataset.drop === 'assignee') {
      const name = col.dataset.col || '';
      // the wire's patch has no "unassign" (empty = leave unchanged), so
      // the pool column is display-only — a drop there stays a no-op
      if (!name || name === task.assignee) return null;
      return { assignee: name };
    }
    if (col.dataset.drop === 'priority') {
      // priority clears for real ("-" = back to 未设置), so every column
      // here takes drops — including the trailing 未设置 one
      const key = col.dataset.col || '';
      const cur = task.priority || '';
      const next = key === '-' ? '' : key;
      return next !== cur ? { priority: key } : null;
    }
    return null;
  }

  #paintSeg() {
    this.els.seg.querySelectorAll('[data-view]').forEach((b) =>
      b.classList.toggle('active', b.dataset.view === this.view));
    this.els.groupWrap.style.display = this.view === 'kanban' ? '' : 'none';
  }

  #clearFilters() {
    this.q = ''; this.assignee = ''; this.priority = ''; this.mine = false; this.statusFilter = '';
    this.els.search.value = '';
    this.els.assignee.value = '';
    this.els.priority.value = '';
    this.els.mine.classList.remove('active');
    this.#paintStats();
    this.#repaintData();
  }

  #repaintData() {
    if (!this.tasks) return;
    this.paintData(this.view === 'kanban' ? this.#kanbanHTML() : this.#listHTML());
  }

  paintData(html) { this.els.data.innerHTML = html; }

  /** 负责人下拉：房册 ∪ 台账里出现过的负责人，一次 reload 重画一次。
   * 离过职的名字保留可筛（历史任务还在），标签注明去留。 */
  #paintAssigneeChoices() {
    const names = new Set(this.board.roster ? this.board.roster() : []);
    for (const t of this.tasks || []) if (t.assignee) names.add(t.assignee);
    const opts =
      `<option value="">${t('全部负责人')}</option>` +
      `<option value="__pool__">${t('任务池')}</option>` +
      [...names].filter(Boolean).sort((a, b) => a.localeCompare(b, 'zh'))
        .map((n) => {
          const tag = this.board.departTag ? this.board.departTag(n) : '';
          return `<option value="${esc(n)}">${esc(n)}${tag ? t('（{v}）', { v: tag }) : ''}</option>`;
        }).join('');
    this.els.assignee.innerHTML = opts;
    this.els.assignee.value = this.assignee;
    if (this.els.assignee.value !== this.assignee) { this.assignee = ''; this.els.assignee.value = ''; }
    const owner = this.board.ownerName;
    this.els.mine.hidden = !owner;
    this.els.mine.classList.toggle('active', this.mine);
  }

  #paintStats() {
    const tasks = this.tasks || [];
    this.els.stats.innerHTML = STATS.map((s) => {
      const n = tasks.filter(s.match).length;
      return (
        `<button type="button" class="stat-chip${this.statusFilter === s.id ? ' active' : ''}" data-stat="${s.id}">` +
          `<i class="sdot" style="background:${s.dot}"></i>${s.label}<b>${n}</b>` +
        '</button>');
    }).join('') + `<span class="stat-sum">${t('共 {n} 条', { n: tasks.length })}</span>`;
  }

  /** The user's lens: search + assignee + priority + mine + the stats quick filter. */
  #filtered() {
    const q = this.q.toLowerCase();
    const owner = this.board.ownerName;
    return (this.tasks || []).filter((t) => {
      if (q && ![t.title, t.id, t.desc, t.assignee].some((v) => (v || '').toLowerCase().includes(q))) return false;
      if (this.assignee === '__pool__' && t.assignee) return false;
      if (this.assignee && this.assignee !== '__pool__' && t.assignee !== this.assignee) return false;
      if (this.priority === '__unset__' && t.priority) return false;
      if (this.priority && this.priority !== '__unset__' && t.priority !== this.priority) return false;
      if (this.mine && t.assignee !== owner) return false;
      if (this.statusFilter === 'overdue') { if (!isOverdue(t)) return false; }
      else if (this.statusFilter === 'pending') { if (!t.pending) return false; }
      else if (this.statusFilter) { if (t.pending || t.status !== this.statusFilter) return false; }
      return true;
    });
  }

  // --- views -----------------------------------------------------------------

  #kanbanHTML() {
    const rows = this.#filtered();
    if (!rows.length) return this.#emptyFilteredHTML();
    const cols = this.groupBy === 'status'
      ? GROUPS.map((g) => ({
          label: g.label,
          rows: rows.filter(g.match).sort(kanbanOrder),
          drop: 'status', key: g.id === 'pending' ? '' : g.id, // 待确认列不收落卡
        }))
      : this.groupBy === 'priority'
        ? PRIORITY_GROUPS.map((g) => ({
            label: g.label, cls: g.cls,
            rows: rows.filter((t) => (g.key === '-' ? !t.priority : t.priority === g.key)).sort(kanbanOrder),
            drop: 'priority', key: g.key,
          }))
        : assigneeColumns(rows);
    return (
      '<div class="kanban">' + cols.map((c) => {
        const canDrop = !!(c.drop && c.key);
        const drop = canDrop ? ` data-drop="${c.drop}" data-col="${esc(c.key)}"` : '';
        const ownerGone = c.owner && this.board.departTag ? this.board.departTag(c.owner) : '';
        const head = c.owner
          ? `<i class="dot" style="background:${memberColor(c.owner)}"></i>` +
            `<span class="${ownerGone ? 'gone' : ''}">${esc(c.owner)}</span>` + goneTagHTML(ownerGone)
          : c.cls ? `<span class="pri ${c.cls}">${esc(c.label)}</span>` : esc(c.label);
        return (
          `<div class="kcol"${drop}>` +
            `<div class="kcol-head">${head}<span class="kcol-count">${c.rows.length}</span></div>` +
            c.rows.map((t) => cardHTML(t, this.groupBy, this.board)).join('') +
            (!c.rows.length ? `<div class="kcol-empty">${canDrop ? t('拖卡到这里') : '—'}</div>` : '') +
          '</div>');
      }).join('') + '</div>');
  }

  #emptyFilteredHTML() {
    const filtered = this.q || this.assignee || this.priority || this.mine || this.statusFilter;
    return filtered
      ? `<div class="state-card"><strong>${t('没有匹配的任务')}</strong>` +
        `<button type="button" class="btn" data-clear-filters>${t('清除筛选')}</button></div>`
      : `<div class="state-card"><strong>${t('这个项目还没有任务')}</strong>` +
        `<span>${t('录入需求后「让编排者拆解」，或直接「+ 新建任务」')}</span></div>`;
  }

  #listHTML() {
    const rows = this.#filtered();
    if (!rows.length) return this.#emptyFilteredHTML();
    const dir = this.sortDir;
    const cmp = {
      updated: (a, b) => (a.updated_ts || a.created_ts || 0) - (b.updated_ts || b.created_ts || 0),
      // 无截止者恒沉底（升序降序都不冒上来），有日期的按方向排
      end: (a, b) => {
        const ae = a.end_ts || 0, be = b.end_ts || 0;
        if (!ae && !be) return 0;
        if (!ae) return 1;
        if (!be) return -1;
        return ae - be;
      },
      title: (a, b) => a.title.localeCompare(b.title, 'zh'),
      pct: (a, b) => pctOf(a) - pctOf(b),
      priority: (a, b) => priorityRank(a) - priorityRank(b),
    }[this.sortKey] || (() => 0);
    const sorted = [...rows].sort((a, b) => {
      // 无截止恒沉底：哨兵判先于方向乘子，降序也不会把空日期顶上来
      if (this.sortKey === 'end' && !a.end_ts !== !b.end_ts) return a.end_ts ? -1 : 1;
      return cmp(a, b) * dir;
    });
    // 多维表格式父子树：当前排序管根任务，子任务紧跟父行缩进——
    // 父不在筛选结果里的子任务原地当根（"子任务"标签还在，不丢上下文）
    const inSet = new Set(rows.map((t) => t.id));
    const kidsOf = new Map();
    for (const t of rows) {
      if (!t.parent || !inSet.has(t.parent)) continue;
      if (!kidsOf.has(t.parent)) kidsOf.set(t.parent, []);
      kidsOf.get(t.parent).push(t);
    }
    for (const list of kidsOf.values()) list.sort((a, b) => a.id.localeCompare(b.id));
    const placed = new Set();
    const tree = [];
    const visit = (t, depth) => {
      if (placed.has(t.id)) return;
      placed.add(t.id);
      tree.push({ t, depth });
      for (const k of kidsOf.get(t.id) || []) visit(k, depth + 1);
    };
    for (const t of sorted) visit(t, 0);
    const th = (key, label, extra = '') =>
      `<th data-sort="${key}"${extra}>${label}${this.sortKey === key ? (dir < 0 ? ' ↓' : ' ↑') : ''}</th>`;
    return (
      '<table class="tlist"><thead><tr>' +
        th('title', t('任务')) + `<th>${t('负责人')}</th><th>${t('状态')}</th>` + th('priority', t('优先级')) +
        th('pct', t('进度')) + th('end', t('截止')) + th('updated', t('更新')) +
      '</tr></thead><tbody>' +
      tree.map(({ t: task, depth }) => {
        const pct = pctOf(task);
        const due = dueChip(task);
        const closed = isClosed(task);
        const locked = task.pending || closed;
        const sel = task.pending
          ? `<span class="tag st-pending">${t('待确认')}</span>`
          : `<select class="input st-sel" data-st="${esc(task.id)}"${closed ? ' disabled' : ''}>` +
            Object.entries(STATUS_META).map(([k, m]) =>
              `<option value="${k}"${task.status === k ? ' selected' : ''}>${m.label}</option>`).join('') +
            '</select>';
        const priSel = locked
          ? (priorityChipHTML(task) || '<span class="dim">—</span>')
          : `<select class="input st-sel pri-sel" data-pri="${esc(task.id)}">` +
            `<option value=""${task.priority ? '' : ' selected'}>${t('未设置')}</option>` +
            Object.entries(PRIORITY_META).map(([k, m]) =>
              `<option value="${k}"${task.priority === k ? ' selected' : ''}>${m.label}</option>`).join('') +
            '</select>';
        return (
          `<tr data-task="${esc(task.id)}"${isOverdue(task) ? ' class="overdue"' : ''}>` +
            `<td class="tl-main"${depth ? ` style="padding-left:${10 + depth * 18}px"` : ''}><div class="tl-title">` +
              `<span class="kcard-no">${esc(task.id)}</span>` +
              (depth ? '<span class="tl-branch">└ </span>' : '') +
              (task.milestone ? `<span class="ms-mark">${icon('milestone', 10)}</span>` : '') + esc(task.title) + '</div>' +
              (() => {
                const bits = [];
                if (task.parent && !inSet.has(task.parent)) bits.push(`<span class="tag">${t('子任务')}</span>`);
                if (task.req) bits.push(`<span class="kcard-ref" title="${t('挂靠需求')}">${esc(task.req)}</span>`);
                if (task.version) bits.push(`<span class="kcard-ref" title="${t('版本')}">${esc(task.version)}</span>`);
                if (task.desc) bits.push(`<span class="dim">${esc(task.desc)}</span>`);
                return bits.length ? `<div class="tl-sub">${bits.join(' · ')}</div>` : '';
              })() + '</td>' +
            '<td class="tl-owner">' + ownerCell(task.assignee, this.board) + '</td>' +
            `<td>${sel}</td>` +
            `<td>${priSel}</td>` +
            '<td class="tl-pct">' + (pct > 0
              ? `<span class="tk-bar"><b style="width:${Math.min(100, pct)}%;background:${memberColor(task.assignee || '')}"></b></span> ${pct}%`
              : '<span class="dim">—</span>') + '</td>' +
            `<td>${due ? `<span class="due ${due.cls}">${esc(due.text)}</span>` : '<span class="dim">—</span>'}</td>` +
            `<td class="dim small">${fmtAge(task.updated_ts || task.created_ts)}</td>` +
          '</tr>');
      }).join('') + '</tbody></table>');
  }

  // --- overview bridges -------------------------------------------------------

  /** 概览页的跳转：落到台账并预置状态快滤（pending/overdue/四状态）。 */
  focusStatus(id) {
    this.statusFilter = id;
    this.#paintStats();
    this.#repaintData();
  }

  /** 概览页的跳转：落到台账并预置负责人筛。 */
  focusAssignee(name) {
    this.assignee = name;
    if (this.tasks) this.#paintAssigneeChoices();
    this.#repaintData();
  }
}

// --- sorting / grouping helpers ---------------------------------------------

/** Kanban within-column order: priority first (紧急在前、未设置沉底),
 * then soonest due, undated last, fresh above stale. */
function kanbanOrder(a, b) {
  const pr = priorityRank(a) - priorityRank(b);
  if (pr) return pr;
  const ae = a.end_ts || Infinity, be = b.end_ts || Infinity;
  if (ae !== be) return ae - be;
  return (b.updated_ts || 0) - (a.updated_ts || 0);
}

/** 分组=负责人：任务池打头，其余按名字序，只列有活的负责人。 */
function assigneeColumns(rows) {
  const pool = rows.filter((t) => !t.assignee);
  const byName = new Map();
  for (const t of rows) {
    if (!t.assignee) continue;
    if (!byName.has(t.assignee)) byName.set(t.assignee, []);
    byName.get(t.assignee).push(t);
  }
  const cols = [{ label: t('任务池'), owner: '', rows: pool.sort(kanbanOrder), drop: 'assignee', key: '' }];
  for (const name of [...byName.keys()].sort((a, b) => a.localeCompare(b, 'zh'))) {
    cols.push({ label: name, owner: name, rows: byName.get(name).sort(kanbanOrder), drop: 'assignee', key: name });
  }
  return cols;
}

/** The avatar + name cell (kanban meta and list row share it). A
 * departed assignee (board.departTag) keeps the name — the tasks are
 * real — but dims it and carries the 已离职/已离编 chip. */
function ownerCell(assignee, board) {
  if (!assignee) return `<span class="kcard-owner dim">${t('任务池')}</span>`;
  const c = memberColor(assignee);
  const gone = board?.departTag ? board.departTag(assignee) : '';
  return (
    '<span class="kcard-owner">' +
      `<span class="avatar xs" style="background:${c}">${esc(avatarText(assignee))}</span>` +
      `<span class="kcard-oname${gone ? ' gone' : ''}">${esc(assignee)}</span>` +
      goneTagHTML(gone) +
    '</span>');
}

// --- the kanban card ----------------------------------------------------------

function cardHTML(task, groupBy, board) {
  const pct = pctOf(task);
  const st = statusMeta(task);
  const due = dueChip(task);
  const pri = priorityChipHTML(task);
  const draggable = !task.pending && !isClosed(task);
  return (
    `<div class="kcard${task.pending ? ' pending' : ''}" role="button" tabindex="0"` +
      ` data-task="${esc(task.id)}"${draggable ? ' draggable="true"' : ''}` +
      `${draggable ? '' : ` title="${task.pending ? t('待确认中，先处理提案') : t('已结束的任务不可再拖动')}"`}>` +
      `<div class="kcard-head"><span class="kcard-no">${esc(task.id)}</span></div>` +
      `<div class="kcard-title">${task.milestone ? `<span class="ms-mark">${icon('milestone', 10)}</span>` : ''}${esc(task.title)}</div>` +
      ((pri || task.req || task.version || task.parent) ?
        '<div class="kcard-tags">' +
          pri +
          (task.req ? `<span class="kcard-ref" title="${t('挂靠需求')}">${esc(task.req)}</span>` : '') +
          (task.version ? `<span class="kcard-ref" title="${t('版本')}">${esc(task.version)}</span>` : '') +
          (task.parent ? `<span class="tag">${t('子任务')}</span>` : '') +
        '</div>' : '') +
      (pct > 0
        ? `<div class="kcard-bar"><i style="width:${Math.min(100, pct)}%;background:${memberColor(task.assignee || '')}"></i></div>`
        : '') +
      '<div class="kcard-meta">' +
        ownerCell(task.assignee, board) +
        (groupBy === 'assignee' && !task.pending ? `<span class="tag ${st.cls}">${st.label}</span>` : '') +
      '</div>' +
      '<div class="kcard-foot">' +
        (due ? `<span class="due ${due.cls}">${esc(due.text)}</span>` : '') +
        `<span class="kcard-age">${fmtAge(task.updated_ts || task.created_ts)}</span>` +
      '</div>' +
    '</div>');
}

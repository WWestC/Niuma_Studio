// taskdrawer.js — the task detail sheet, the Feishu 任务详情 way: the
// fields are the UI (基本信息 rendered as inline-editable rows —
// 负责人/状态/排期/里程碑/进度/版本/依赖 each committing one task_update
// the moment it changes), plus the 子任务 list (parent tree, read side),
// the 待确认 proposal block (接单/婉拒 for the people in Need; the host
// — never in Need — arbitrates instead: 批准/驳回 take effect at once),
// the quick ops and the change log. The sheet stays open across ops
// (Feishu behavior) and live-repaints twice: off the owner receipt frame
// (syncFromFrame) and off the board's reload (refresh), scroll kept.

import { Msg } from '../wire/wire.js';
import { esc, fmtAge, fmtDateTime, icon, dismiss, copyToClipboard } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import {
  STATUS_META, statusMeta, isClosed, pctOf, isOverdue, hasChildren,
  childrenOf, dueChip, tsToLocal, localToSched, PRIORITY_META, priorityMeta, priorityChipHTML,
} from './taskmeta.js';
import { foldLogHTML } from './taskarchive.js'; // r_26 t_212：变更日志升级三层折叠（同档案浮层共享逻辑）

export class TaskDrawer {
  /** @param {import('./project.js').ProjectBoard} board */
  constructor(board) {
    this.board = board;
    this.el = null;     // .sheet-wrap (backdrop)
    this.sheet = null;  // .sheet (the scrollable card)
    this.task = null;
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
  }

  /** @param {import('../wire/wire.js').TaskFull} task */
  open(task) {
    this.close();
    const el = document.createElement('div');
    el.className = 'sheet-wrap';
    el.innerHTML = `<div class="sheet" role="dialog" aria-label="${t('任务详情')}"></div>`;
    document.body.appendChild(el);
    this.el = el;
    this.sheet = el.querySelector('.sheet');
    this.task = task;
    this.#paint();
    // 待确认的任务开箱即见提案块——上面一排锁定字段不是重点。
    // 滚的是抽屉自身且给吸顶头让位：scrollIntoView 会把「待确认」
    // 标题对进吸顶头背后（block:'start' 对齐滚动视口顶），还可能
    // 连带滚动外层页面
    if (task.pending) {
      const sec = [...this.sheet.querySelectorAll('.side-sec h3')]
        .find((h) => h.textContent.trim().startsWith(t('待确认')));
      if (sec) requestAnimationFrame(() => {
        const head = this.sheet.querySelector('.sheet-head');
        const box = this.sheet.getBoundingClientRect();
        const top = sec.parentElement.getBoundingClientRect().top - box.top
          - (head ? head.offsetHeight : 0) + this.sheet.scrollTop;
        this.sheet.scrollTo({ top: Math.max(0, top) });
      });
    }
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      const fold = ev.target.closest('[data-fold]');
      if (fold) { this.toggleFold(fold.dataset.fold); return; } // r_26：同日折叠展开
      const op = ev.target.closest('[data-op]');
      if (op) this.#op(op.dataset.op);
    });
    el.addEventListener('change', (ev) => this.#fieldChange(ev.target));
    el.addEventListener('keydown', (ev) => {
      // 文本行的 Enter＝点它旁边的保存（进度备注/版本/依赖）
      if (ev.key !== 'Enter' || !ev.target.dataset) return;
      const d = ev.target.dataset;
      const save = d.fProgress !== undefined ? 'save-progress'
        : d.fVersion !== undefined ? 'save-version'
        : d.fDeps !== undefined ? 'save-deps' : null;
      if (save) { ev.preventDefault(); this.#op(save); }
    });
    document.addEventListener('keydown', this.onDocKey);
  }

  close() {
    if (!this.el) return;
    dismiss(this.el);
    this.el = this.sheet = null;
    this.task = null;
    document.removeEventListener('keydown', this.onDocKey);
  }

  /** The board reloaded its ledger: follow the open task (or bail if gone). */
  refresh(tasks) {
    if (!this.el || !this.task) return;
    const t = tasks.find((x) => x.id === this.task.id);
    if (!t) { this.close(); return; }
    if (JSON.stringify(t) !== JSON.stringify(this.task)) this.#repaint(t);
  }

  /** The owner receipt frame (app.js → project.js): the authoritative snapshot. */
  syncFromFrame(f) {
    if (!this.el || f.type !== Msg.Task || f.event === 'denied') return;
    if (!f.task?.id || f.task.id !== this.task?.id) return;
    this.#repaint(f.task);
  }

  #repaint(t) {
    if (!this.el) return;
    const sc = this.sheet.scrollTop;
    this.task = t;
    this.sheet.innerHTML = this.#html(t);
    this.sheet.scrollTop = sc;
  }

  // --- the field commits ------------------------------------------------------

  #fieldChange(el) {
    const task = this.task;
    if (!task || !el.dataset) return;
    if (el.dataset.fAssignee !== undefined) {
      if (el.value !== (task.assignee || '')) this.#send({ assignee: el.value }, t('改指派'));
      return;
    }
    if (el.dataset.fStatus !== undefined) {
      if (el.value !== task.status) this.#send({ status: el.value }, t('状态'));
      return;
    }
    if (el.dataset.fPriority !== undefined) {
      // 未设置选项发 "-"（协议的清除键；空串=保持不变，永不该发出）
      if ((el.value || '') !== (task.priority || '')) {
        this.#send({ priority: el.value || '-' }, t('优先级'));
      }
      return;
    }
    if (el.dataset.fStart !== undefined || el.dataset.fEnd !== undefined) {
      const v = localToSched(el.value);
      if (!v) { this.board.toast.show(t('清排期请用「清除」按钮（协议空值=不改）'), 'info'); this.#repaint(task); return; }
      const key = el.dataset.fStart !== undefined ? 'start' : 'end';
      const cur = tsToLocal(key === 'start' ? task.start_ts : task.end_ts).replace('T', ' ');
      if (v !== cur) this.#send({ [key]: v }, key === 'start' ? t('排期开始') : t('排期截止'));
      return;
    }
    if (el.dataset.fMilestone !== undefined) {
      this.#send({ milestone: el.checked ? 'true' : 'false' }, el.checked ? t('设里程碑') : t('取消里程碑'));
      return;
    }
    if (el.dataset.fPct !== undefined) {
      // an emptied field is a stray blur, not a "reset to zero" — only
      // a real number commits (0 itself is typed explicitly)
      if (!String(el.value).trim()) { this.#repaint(task); return; }
      const n = Math.max(0, Math.min(100, parseInt(el.value, 10) || 0));
      if (n !== pctOf(task)) this.#send({ pct: String(n) }, t('进度'));
      return;
    }
  }

  #op(op) {
    const task = this.task;
    const send = (patch, label) => this.#send(patch, label);
    switch (op) {
      case 'close': this.close(); return;
      case 'open-parent': {
        const parent = (this.board.tasks || []).find((x) => x.id === task.parent);
        if (parent) this.open(parent);
        return;
      }
      case 'copy-id': {
        const id = task.id;
        copyToClipboard(id).then((ok) =>
          this.board.toast.show(ok ? t('已复制 {id}', { id }) : t('编号 {id}（复制失败，手动记一下）', { id }), ok ? 'ok' : 'info'));
        return;
      }
      case 'start': send({ status: 'doing' }, t('开始')); return;
      case 'finish': send({ status: 'done' }, t('完成')); return;
      case 'cancel': send({ status: 'cancelled' }, t('取消')); return;
      case 'confirm': this.#confirmDecline(Msg.TaskConfirm, t('接单')); return;
      case 'decline': this.#confirmDecline(Msg.TaskDecline, t('婉拒')); return;
      case 'approve': this.#arbitrate(true); return;
      case 'reject': this.#arbitrate(false); return;
      case 'clear-sched': send({ start: '-', end: '-' }, t('清除排期')); return;
      case 'pct-10':
      case 'pct+10': {
        const now = (task.progress_pct || 0) + (op === 'pct+10' ? 10 : -10);
        send({ pct: String(Math.max(0, Math.min(100, now))) }, t('进度'));
        return;
      }
      case 'save-progress': {
        const v = this.#input('[data-f-progress]').trim();
        if (!v) { this.board.toast.show(t('备注不能为空（协议空值=不改）'), 'info'); return; }
        send({ progress: v }, t('进度备注'));
        return;
      }
      case 'save-version': {
        const v = this.#input('[data-f-version]').trim();
        send({ version: v || '-' }, v ? t('版本') : t('版本清除'));
        return;
      }
      case 'save-deps': {
        const raw = this.#input('[data-f-deps]');
        const toks = raw.split(',').map((s) => s.trim()).filter(Boolean);
        if (toks.some((s) => !/^t_\d{1,4}$/.test(s))) {
          this.board.toast.show(t('依赖要 t_NN 形式，逗号分隔'), 'err');
          return;
        }
        send({ deps: toks.join(',') || '-' }, toks.length ? t('依赖') : t('依赖清空'));
        return;
      }
      default: return;
    }
  }

  #confirmDecline(msgType, label) {
    const task = this.task;
    if (!task.pending) return;
    this.board.frameTo({ type: msgType, task_id: task.id }, `${task.id} ${label}`);
    // the outcome (applied / still waiting / declined) repaints off the receipt
  }

  // The host's verdict on a pending proposal (task_arbitrate): approve
  // applies the patch at once skipping the remaining confirmations,
  // reject discards it (an assign proposal's rejection cancels the task).
  // The host is never in Need — confirm/decline would only deny.
  #arbitrate(approve) {
    const task = this.task;
    if (!task.pending) return;
    this.board.frameTo(
      { type: Msg.TaskArbitrate, task_id: task.id, approve },
      `${task.id} ${approve ? t('仲裁批准') : t('仲裁驳回')}`);
  }

  #input(sel) { return this.sheet.querySelector(sel)?.value || ''; }

  /**
   * One task_update over the viewed project's owner face. The sheet
   * stays open; it repaints back to the known truth at once (so a
   * denial — private, toast-only — never leaves a control lying) and
   * again off the receipt/refresh with the applied snapshot.
   */
  #send(patch, label) {
    const task = this.task;
    if (!task) return;
    if (task.pending) {
      this.board.toast.show(t('该任务有待确认变更：需确认人接单/婉拒，或房主在「待确认」区仲裁后再编辑'), 'info');
      this.#repaint(task);
      return;
    }
    if (isClosed(task)) {
      this.board.toast.show(t('任务已结束，不可再改'), 'err');
      this.#repaint(task);
      return;
    }
    this.board.frameTo({ type: Msg.TaskUpdate, task_id: task.id, patch }, `${task.id} ${label}`);
    this.#repaint(task);
  }

  // --- rendering ---------------------------------------------------------------

  #html(task) {
    const st = statusMeta(task);
    const due = dueChip(task);
    const tasks = this.board.tasks || [];
    const kids = childrenOf(tasks, task.id);
    return (
      (task.parent && tasks.some((x) => x.id === task.parent)
        ? `<button type="button" class="back-parent" data-op="open-parent">${t('← 父任务 {id}', { id: esc(task.parent) })}</button>`
        : '') +
      '<div class="sheet-head">' +
        `<div class="sheet-title"><strong>${esc(task.title)}</strong>` +
          `<span class="dim small"><button type="button" class="id-copy" data-op="copy-id" title="${t('点击复制编号')}">${esc(task.id)}</button>` +
          ` · <span class="tag ${st.cls}">${st.label}</span>` +
          (priorityMeta(task.priority) ? ` · ${priorityChipHTML(task)}` : '') +
          (isOverdue(task) ? ` · <span class="tag st-overdue">${t('已逾期')}</span>` : '') +
          (task.milestone ? ` · ${icon('milestone', 10)} ${t('里程碑')}` : '') + '</span></div>' +
        '<button type="button" class="icon-btn" data-op="close">' + icon('close', 14) + '</button>' +
      '</div>' +
      (task.desc ? `<div class="sheet-desc">${esc(task.desc)}</div>` : '') +
      this.#baseHTML(task) +
      (kids.length ? this.#kidsHTML(kids) : '') +
      (task.pending ? this.#pendingHTML(task) : '') +
      (!task.pending ? this.#opsHTML(task) : '') +
      `<div class="side-sec"><h3>${t('变更日志')}</h3>` +
        // r_26 t_212：三层折叠（骨架平铺/同日合并/跨日分组）——档案浮层
        // 同款逻辑；抽屉里的展开态不持久（重绘回收纳，与抽屉一次性读法相称）
        foldLogHTML((task.log || []).slice(-40).reverse(), this.foldOpen) +
      '</div>');
  }

  /** The fold toggle: expand one same-day bundle in place (r_26). */
  toggleFold(key) {
    if (!this.foldOpen) this.foldOpen = new Set();
    if (this.foldOpen.has(key)) this.foldOpen.delete(key); else this.foldOpen.add(key);
    this.#repaint(this.task);
  }

  /** 基本信息 — the editable field rows (Feishu 详情页的键值排布). */
  #baseHTML(task) {
    // the assignee universe: the room roster ∪ every assignee the
    // ledger has seen ∪ the current one — a task may name someone who
    // is not seated right now (post-restart ghosts, offline agents)
    const roster = this.board.roster ? this.board.roster() : [];
    const seen = new Set([...roster, ...(this.board.tasks || []).map((x) => x.assignee), task.assignee]);
    const names = [...seen].filter(Boolean);
    const leaf = !hasChildren(this.board.tasks || [], task.id);
    const kv = (k, v) => v ? `<div class="kv"><span>${k}</span><b>${v}</b></div>` : '';
    const lock = task.pending || isClosed(task) ? ' disabled' : '';
    return (
      `<div class="side-sec"><h3>${t('基本信息')}</h3>` +
        `<div class="kv-edit"><label>${t('负责人')}</label>` +
          `<select class="input" data-f-assignee${lock}>` +
            `<option value=""${task.assignee ? '' : ' selected'}>${t('任务池（待认领）')}</option>` +
            names.map((n) => {
              const tag = this.board.departTag ? this.board.departTag(n) : '';
              return `<option value="${esc(n)}"${task.assignee === n ? ' selected' : ''}>${esc(n)}${tag ? t('（{tag}）', { tag }) : ''}</option>`;
            }).join('') +
          '</select></div>' +
        `<div class="kv-edit"><label>${t('状态')}</label>` +
          `<select class="input" data-f-status${lock}>` +
            Object.entries(STATUS_META).map(([k, m]) =>
              `<option value="${k}"${task.status === k ? ' selected' : ''}>${m.label}</option>`).join('') +
          '</select></div>' +
        `<div class="kv-edit"><label>${t('优先级')}</label>` +
          `<select class="input" data-f-priority${lock}>` +
            `<option value=""${task.priority ? '' : ' selected'}>${t('未设置')}</option>` +
            Object.entries(PRIORITY_META).map(([k, m]) =>
              `<option value="${k}"${task.priority === k ? ' selected' : ''}>${m.label}</option>`).join('') +
          '</select></div>' +
        `<div class="kv-edit"><label>${t('开始')}</label>` +
          `<input type="datetime-local" class="input" data-f-start value="${tsToLocal(task.start_ts)}"${lock}></div>` +
        `<div class="kv-edit"><label>${t('截止')}</label>` +
          `<input type="datetime-local" class="input" data-f-end value="${tsToLocal(task.end_ts)}"${lock}>` +
          `<button type="button" class="btn small" data-op="clear-sched"${lock}>${t('清除')}</button></div>` +
        `<div class="kv-edit"><label>${t('里程碑')}</label>` +
          `<span class="chk"><input type="checkbox" data-f-milestone${task.milestone ? ' checked' : ''}${lock}> ${t('里程碑节点（不计时长）')}</span></div>` +
        (leaf
          ? `<div class="kv-edit"><label>${t('进度')}</label>` +
            `<button type="button" class="btn small" data-op="pct-10"${lock}>−10</button>` +
            `<input type="number" class="input pct-in" min="0" max="100" data-f-pct value="${task.progress_pct || 0}"${lock}>` +
            `<button type="button" class="btn small" data-op="pct+10"${lock}>＋10</button></div>` +
            `<div class="kv-edit"><label>${t('进度备注')}</label>` +
            `<input class="input" data-f-progress value="${esc(task.progress || '')}" placeholder="${t('如：96% · 差验收（写 % 会同步数值）')}"${lock}>` +
            `<button type="button" class="btn small" data-op="save-progress"${lock}>${t('保存')}</button></div>`
          : `<div class="kv-edit"><label>${t('进度')}</label><span class="dim small">${t('由子任务聚合派生（非叶任务不可直写）')}</span></div>`) +
        `<div class="kv-edit"><label>${t('版本')}</label>` +
          `<input class="input" data-f-version value="${esc(task.version || '')}" placeholder="${t('版本名（- 清除）')}"${lock}>` +
          `<button type="button" class="btn small" data-op="save-version"${lock}>${t('保存')}</button></div>` +
        `<div class="kv-edit"><label>${t('依赖')}</label>` +
          `<input class="input mono" data-f-deps value="${esc((task.deps || []).join(', '))}" placeholder="${t('t_101, t_102（- 清空）')}"${lock}>` +
          `<button type="button" class="btn small" data-op="save-deps"${lock}>${t('保存')}</button></div>` +
        kv(t('需求'), task.req ? esc(task.req) : '') +
        kv(t('提案'), task.plan_id ? esc(task.plan_id) : '') +
        kv(t('创建'), (task.created_by ? esc(task.created_by) + ' · ' : '') + (task.created_ts ? fmtDateTime(task.created_ts) : '')) +
        kv(t('更新'), fmtAge(task.updated_ts || task.created_ts)) +
      '</div>');
  }

  #kidsHTML(kids) {
    const agg = kids.length
      ? Math.round(kids.reduce((s, k) => s + pctOf(k), 0) / kids.length)
      : 0;
    return (
      `<div class="side-sec"><h3>${t('子任务')} <span class="dim">${t('（{n} · 聚合 {p}%）', { n: kids.length, p: agg })}</span></h3>` +
      kids.map((k) => {
        const st = statusMeta(k);
        return (
          `<button type="button" class="sub-row" data-op="noop" data-child="${esc(k.id)}">` +
            `<i class="dot" style="background:${st.dot}"></i>` +
            `<span class="sub-title">${k.milestone ? icon('milestone', 10) + ' ' : ''}${esc(k.title)}</span>` +
            `<span class="sub-pct">${pctOf(k) || ''}${pctOf(k) ? '%' : ''}</span>` +
            `<span class="tag ${st.cls}">${st.label}</span>` +
          '</button>');
      }).join('') + '</div>');
  }

  #pendingHTML(task) {
    const p = task.pending || {};
    const need = Array.isArray(p.need) ? p.need : [];
    const got = Array.isArray(p.got) ? p.got : [];
    const waiting = need.filter((n) => !got.includes(n));
    const me = this.board.ownerName;
    const canAct = me && need.includes(me);
    // 动词按协议分家：接单/婉拒属于 Need 名单里的人（房主只在别人把
    // 变更提上房主自己的任务时才进名单）；名单外的查看者（本工作台
    // 即房主）拿仲裁——批准直接生效、驳回作废（assign 提案驳回即取消
    // 任务），CLI 同款 `task arbitrate <id> approve|reject`。
    const fate = p.kind === 'assign'
      ? t('接单后任务生效，婉拒则任务取消。')
      : t('确认后变更生效，婉拒即放弃。');
    const arbiFate = p.kind === 'assign'
      ? t('批准则任务生效，驳回则变更作废、任务取消。')
      : t('批准则变更直接生效，驳回则变更作废。');
    return (
      `<div class="side-sec"><h3>${t('待确认')}</h3>` +
        `<div class="dim small">${esc(p.by || '')} ${t('发起的变更（{kind}），等待 {who}。', { kind: esc(p.kind || 'change'), who: esc(waiting.join(t('、')) || '…') })}${canAct ? fate : ''}</div>` +
        (canAct
          ? '<div class="ops-grid">' +
            `<button type="button" class="btn gold" data-op="confirm">${t('接单（确认）')}</button>` +
            `<button type="button" class="btn danger" data-op="decline">${t('婉拒')}</button>` +
            '</div>'
          : me
            ? '<div class="ops-grid">' +
              `<button type="button" class="btn gold" data-op="approve">${t('批准（仲裁）')}</button>` +
              `<button type="button" class="btn danger" data-op="reject">${t('驳回（仲裁）')}</button>` +
              '</div>' +
              `<div class="dim small">${t('房主仲裁跳过剩余确认人直接定夺。')}${arbiFate}</div>`
            : `<div class="dim small">${t('需 {who} 本人在场操作（CLI task confirm/decline），或房主仲裁（task arbitrate）。', { who: esc(waiting.join(t('、')) || '…') })}</div>`) +
      '</div>');
  }

  #opsHTML(task) {
    const closed = isClosed(task);
    return (
      `<div class="side-sec"><h3>${t('操作')}</h3>` +
        '<div class="ops-grid">' +
          (task.status === 'todo' ? `<button type="button" class="btn" data-op="start">${t('开始')}</button>` : '') +
          (!closed ? `<button type="button" class="btn" data-op="finish">${t('完成')}</button>` : '') +
          (!closed ? `<button type="button" class="btn danger" data-op="cancel">${t('取消')}</button>` : '') +
        '</div>' +
        `<div class="dim small">${t('改期/依赖/版本/指派已在上方直接编辑；子任务关系由编排者提案建立。')}</div>` +
      '</div>');
  }

  #paint() {
    this.sheet.innerHTML = this.#html(this.task);
    // a click on a sub-task row opens that task in place (the delegated
    // [data-op] match above returns on noop, so catch children here)
    this.sheet.addEventListener('click', (ev) => {
      const sub = ev.target.closest('[data-child]');
      if (!sub) return;
      const next = (this.board.tasks || []).find((x) => x.id === sub.dataset.child);
      if (next) this.open(next);
    }, true);
  }
}

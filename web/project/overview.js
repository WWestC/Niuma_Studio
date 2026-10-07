// overview.js — the project 概览 tab (Feishu 项目空间的落地仪表盘):
// one stats strip of click-through cards (待处理/进行中/已完成/待确认/
// 逾期 — 点卡即落到台账并预置同款筛), the member workload bars (谁的
// 活最重，一眼可读), the version progress bars and the 需要关注 lists
// (逾期 / 今日截止 / 待确认 — 点行直接开任务抽屉). Pure read: every
// number derives client-side from the same ledger the board drinks
// (getProjectTasks), so the two views never disagree; the only writes
// are the jump-into-board bridges the board itself already owns.

import { getProjectTasks, getSchedule, getStaffing } from '../wire/api.js';
import { esc, memberColor, avatarText, icon } from '../ui/dom.js';
import { goneTagHTML } from '../ui/person.js';
import { Dialog } from '../ui/feedback.js';
import { t, localeTag } from '../ui/i18n.js';
import { statusMeta, pctOf, isOverdue, priorityChipHTML } from './taskmeta.js';

export class OverviewView {
  /**
   * @param {HTMLElement} container #ppane-overview
   * @param {import('./project.js').ProjectBoard} board the ProjectBoard
   *   (tab owner — jumps go through switchTab; the TaskBoardView and
   *   its drawer hang off .board)
   */
  constructor(container, board) {
    this.container = container;
    this.board = board;
    this.tasks = null;
    container.innerHTML = '<div class="sk-line"></div><div class="sk-line w60"></div>';
  }

  renderIdle() {
    this.paint('<div class="sk-line"></div><div class="sk-line w60"></div>');
  }

  async reload() {
    const key = this.board.key;
    const fresh = this._loadedKey !== key || !this.tasks;
    this._loadedKey = key;
    if (fresh) this.renderIdle();
    let tasks;
    try {
      tasks = await getProjectTasks(key);
    } catch (err) {
      if (fresh) {
        this.paint(
          `<div class="state-card error"><strong>${t('概览读取失败')}</strong><span>${esc(err.message || String(err))}</span>` +
          `<button type="button" class="btn" data-retry>${t('重试')}</button></div>`);
      }
      return;
    }
    this.tasks = tasks;
    // 提效账本（best-effort）：排期投影里 Σ计划 vs Σ实际——AI 干活比
    // 排期快几个数量级，这张卡把差值摆上台面；拿不到投影（未开张/
    // 网络抖动）就静默不亮卡，绝不拖垮概览。
    try {
      this.schedule = await getSchedule(key);
    } catch { this.schedule = null; }
    const info = this.#projInfoHTML();
    if (!tasks.length) {
      this.paint(
        info +
        '<div class="state-card"><strong>' + (this.#isDraft() ? t('项目还没开张——开张后成员才能进房干活') : t('这个项目还没有任务')) + '</strong>' +
        `<span>${t('录入需求后「让编排者拆解」——任务经提案入库；或直接「+ 新建任务」，概览随台账自动长出来')}</span></div>`);
      return;
    }
    this.paint(info + this.#html(tasks));
  }

  paint(html) { this.container.innerHTML = html; }

  // --- 项目信息卡（v2 P7）：登记事实 + 生命周期入口 --------------------------

  #isDraft() { return this.board.currentProject()?.status === 'draft'; }

  #projInfoHTML() {
    const p = this.board.currentProject();
    if (!p) return '';
    const isLobby = p.key === 'default';
    const st = p.status === 'draft' ? `<span class="pstatus draft">${t('草稿 · 未开张')}</span>`
      : p.status === 'archived' ? `<span class="pstatus archived">${t('已归档 · 封存只读')}</span>`
      : `<span class="pstatus active">${t('进行中')}</span>`;
    const vers = (p.versions || []).map((v) => {
      const end = v.end_ts ? new Date(v.end_ts * 1000).toLocaleDateString(localeTag()) : t('开放窗口');
      const goal = v.goal ? ` · ${esc(v.goal)}` : '';
      // v2.7 版本联动：绑定到这个时间窗的分支直接标在 chip 上（无绑定
      // 则省略——不撒谎也不占地方）。
      const links = p.branch_links || {};
      const bound = Object.keys(links).filter((b) => links[b].version === v.name);
      const branch = bound.length ? ` · ${t('分支')} ${esc(bound.join(t('、')))}` : '';
      return `<span class="chip" title="${t('版本窗口')}">${esc(v.name)}${t('（{v}）', { v: `${end}${goal}${branch}` })}</span>`;
    }).join('');
    const meta = [
      `<span class="ov-pkey">${esc(p.key)}</span>`,
      p.workspace ? `<span class="ov-pws dim" title="${t('成员会话的出生路径')}">${icon('folder', 12)} ${esc(p.workspace)}</span>` : '',
      p.created_by ? `<span class="dim">${t('{who} 立项', { who: esc(p.created_by) })}</span>` : '',
      vers,
    ].filter(Boolean).join('<span class="ov-psep">·</span>');
    // 生命周期入口：draft 开张/删除、active 归档（两步确认）、archived
    // 复活/删除；大厅进程常驻，无动作。编辑对所有非大厅状态开放（封存
    // 全只读除外）。危险区（清退成员/重置项目）只对开张中的项目亮——
    // 归档封存只读，草稿还没有房与人；删除（除名）只对草稿/归档亮——
    // 开张中的项目先归档（退房离编），删除才只是扫残余。
    const acts = [];
    if (p.status === 'draft') {
      acts.push(`<button type="button" class="btn small gold" data-pact="activate">${icon('sparkle', 14)} ${t('开张')}</button>`);
      acts.push(`<button type="button" class="btn small danger" data-pact="delete" title="${esc(t('从项目册除名：台账与房间档案一并删除，工作区目录与 git 仓库不动'))}">${icon('trash', 14)} ${esc(t('删除项目…'))}</button>`);
    }
    if (p.status === 'active' && !isLobby) {
      acts.push(this._archiveArmed
        ? `<button type="button" class="btn small danger" data-pact="archive">${t('再点一次确认归档')}</button>`
        : `<button type="button" class="btn small" data-pact="archive">${t('归档')}</button>`);
    }
    if (p.status === 'archived') {
      acts.push(`<button type="button" class="btn small gold" data-pact="reactivate">${icon('refresh', 14)} ${t('复活')}</button>`);
      acts.push(`<button type="button" class="btn small danger" data-pact="delete" title="${esc(t('从项目册除名：台账与房间档案一并删除，工作区目录与 git 仓库不动'))}">${icon('trash', 14)} ${esc(t('删除项目…'))}</button>`);
    }
    if (!isLobby && p.status !== 'archived') acts.push(`<button type="button" class="btn small" data-pact="edit">${t('编辑')}</button>`);
    if (p.status === 'active' && !isLobby) {
      acts.push(`<button type="button" class="btn small danger" data-pact="dismiss" title="${esc(t('移出全部成员并删除其编制行、全局档案与 ZCode 会话；聊天记录保留'))}">${icon('users', 14)} ${esc(t('清退成员'))}</button>`);
      acts.push(`<button type="button" class="btn small danger" data-pact="reset" title="${esc(t('成员、对话、任务、需求与文档全部清空，回到第一次开张的状态'))}">${icon('trash', 14)} ${esc(t('重置项目…'))}</button>`);
    }
    return (
      '<div class="ov-pinfo">' +
        '<div class="ov-pinfo-main">' +
          '<span class="ov-pname-row">' +
            `<strong class="ov-pname">${esc(p.name || p.key)}</strong>${st}` +
          '</span>' +
          (p.desc ? `<span class="ov-pdesc">${esc(p.desc)}</span>`
            : isLobby ? `<span class="ov-pdesc">${t('全员公共房间 · 常驻运行，不参与归档')}</span>` : '') +
          `<span class="ov-pmeta">${meta}</span>` +
        '</div>' +
        (acts.length ? `<div class="ov-pinfo-acts">${acts.join('')}</div>` : '') +
      '</div>');
  }

  // --- the jumps (all land on the ledger tab) ---------------------------------

  #jumpStatus(ev) {
    const card = ev.target.closest('[data-jump-status]');
    if (!card) return false;
    this.board.switchTab('board');
    this.board.board.focusStatus(card.dataset.jumpStatus);
    return true;
  }

  #jumpAssignee(ev) {
    const row = ev.target.closest('[data-jump-assignee]');
    if (!row) return false;
    this.board.switchTab('board');
    this.board.board.focusAssignee(row.dataset.jumpAssignee);
    return true;
  }

  #openDrawer(ev) {
    const row = ev.target.closest('[data-task]');
    if (!row) return false;
    const task = this.tasks?.find((t) => t.id === row.dataset.task);
    if (task) this.board.board.drawer.open(task);
    return true;
  }

  /** One delegated click handler covering every interactive row/card. */
  onClick(ev) {
    if (ev.target.closest('[data-retry]')) { this.reload(); return; }
    const pact = ev.target.closest('[data-pact]');
    if (pact) {
      const act = pact.dataset.pact;
      if (act === 'edit') { this.board.editProject(); return; }
      if (act === 'dismiss') { this.#confirmDismiss(); return; }
      if (act === 'reset') { this.#confirmReset(); return; }
      if (act === 'delete') { this.#confirmDelete(); return; }
      if (act === 'archive') {
        // 两步确认（牛马档案归档的同款写法）：第一次武装，第二次执行
        if (!this._archiveArmed) { this._archiveArmed = true; this.reload(); return; }
        this._archiveArmed = false;
      }
      this.board.projectAction(act);
      return;
    }
    if (this.#jumpStatus(ev) || this.#jumpAssignee(ev) || this.#openDrawer(ev)) return;
  }

  // --- 危险区确认框（设置卡「重置工作室」的同族交互） ------------------------

  /** 清退项目组成员：普通危险确认（红色主按钮），口令只有「重置项目」要。 */
  async #confirmDismiss() {
    const p = this.board.currentProject();
    if (!p) return;
    let occupying = 0;
    try { // 数目拿不到就不显数字——确认语义不依赖它
      const st = await getStaffing(p.key);
      occupying = (st.rows || []).filter((r) => r.state !== 'offboard').length;
    } catch { /* 刷新竞争里的 404：项目刚被归档/删除 */ }
    const name = p.name || p.key;
    const dlg = Dialog.show({
      title: t('清退项目组成员'),
      kind: 'warning',
      size: 's',
      html:
        '<p>' + esc(t('将清退「{name}」的全部成员（当前在编 {n} 人）：移出房间、删除编制行与全局档案，并清理他们的 ZCode 会话。', { name, n: occupying })) + '</p>' +
        '<p>' + esc(t('在别的项目仍在编的成员保留档案；聊天记录保留。此操作不可撤销。')) + '</p>',
      actions: [
        { label: t('取消') },
        { label: t('清退全部成员'), primary: true, danger: true, value: true },
      ],
    });
    if (await dlg === true) this.board.dismissMembers();
  }

  /** 重置项目：typed 口令确认（同设置卡「重置工作室」的 disarm 写法）。 */
  #confirmReset() {
    const p = this.board.currentProject();
    if (!p) return;
    const name = p.name || p.key;
    const dlg = Dialog.show({
      title: t('重置项目'),
      kind: 'warning',
      size: 's',
      html:
        '<p>' + esc(t('将把「{name}」完全清空：全部成员与他们的对话、任务、需求、计划、会议、项目文档一并删除——回到第一次开张的状态。', { name })) + '</p>' +
        '<p class="dim">' + esc(t('工作区里的文件不受影响。此操作不可撤销。')) + '</p>' +
        '<p>' + esc(t('输入「{pass}」以确认：', { pass: t('重置') })) + '</p>' +
        '<input class="input" data-ov-reset-input placeholder="' + esc(t('重置')) + '" autocomplete="off">',
      actions: [
        { label: t('取消') },
        { label: t('清空并重置项目'), primary: true, danger: true, value: true },
      ],
    });
    // 确认钮在口令对上之前保持禁用（渲染是同步的，此时已在文档里）
    const arm = document.querySelector('.fb-dlg-foot [data-i="1"]');
    const input = document.querySelector('[data-ov-reset-input]');
    if (arm && input) {
      arm.disabled = true;
      const pass = t('重置'); // 口令随语言（en 下为 reset——提示语与占位同源）
      input.addEventListener('input', () => {
        arm.disabled = input.value.trim() !== pass;
      });
      setTimeout(() => input.focus(), 60); // 等入场动画落定再抢焦点
    }
    dlg.then((ok) => {
      if (ok === true) this.board.resetProject();
    });
  }

  /** 删除项目（草稿/归档）：typed 口令确认（重置面同族的 disarm 写法）
   *  ——除名不可逆，连草稿也要亲手写出「删除」两个字。 */
  #confirmDelete() {
    const p = this.board.currentProject();
    if (!p || p.key === 'default' || p.status === 'active') return;
    const name = p.name || p.key;
    const dlg = Dialog.show({
      title: t('删除项目'),
      kind: 'warning',
      size: 's',
      html:
        '<p>' + esc(t('将把「{name}」（{key}）从项目册除名：成员档案、任务/需求/会议等台账与房间历史一并删除。', { name, key: p.key })) + '</p>' +
        '<p class="dim">' + esc(t('工作区目录与 git 仓库原样保留，不会删你的文件。此操作不可撤销。')) + '</p>' +
        '<p>' + esc(t('输入「{pass}」以确认：', { pass: t('删除') })) + '</p>' +
        '<input class="input" data-ov-del-input placeholder="' + esc(t('删除')) + '" autocomplete="off">',
      actions: [
        { label: t('取消') },
        { label: t('删除项目'), primary: true, danger: true, value: true },
      ],
    });
    // 确认钮在口令对上之前保持禁用（渲染是同步的，此时已在文档里）
    const arm = document.querySelector('.fb-dlg-foot [data-i="1"]');
    const input = document.querySelector('[data-ov-del-input]');
    if (arm && input) {
      arm.disabled = true;
      const pass = t('删除'); // 口令随语言（en 下为 delete——提示语与占位同源）
      input.addEventListener('input', () => {
        arm.disabled = input.value.trim() !== pass;
      });
      setTimeout(() => input.focus(), 60); // 等入场动画落定再抢焦点
    }
    dlg.then((ok) => {
      if (ok === true) this.board.deleteProject();
    });
  }

  // --- rendering ---------------------------------------------------------------

  #html(tasks) {
    const count = (fn) => tasks.filter(fn).length;
    const done = count((t) => !t.pending && t.status === 'done');
    const cancelled = count((t) => !t.pending && t.status === 'cancelled');
    const finished = done + cancelled;
    // 完成率：已结束里 done 的占比；没有可完成的任务时不亮数字
    const rate = finished ? Math.round((done / finished) * 100) : null;
    const now = Date.now() / 1000;
    const overdue = tasks.filter((t) => isOverdue(t, now));
    const dueToday = tasks.filter((t) => !t.pending && t.end_ts > 0 && !isOverdue(t, now) &&
      t.end_ts <= now + 86400 && (t.status === 'todo' || t.status === 'doing'));
    const pending = tasks.filter((t) => t.pending);

    return (
      '<div class="ov">' +
        '<div class="ov-cards">' +
          this.#card(t('待处理'), count((t) => !t.pending && t.status === 'todo'), 'todo') +
          this.#card(t('进行中'), count((t) => !t.pending && t.status === 'doing'), 'doing') +
          this.#card(t('已完成'), done, 'done') +
          this.#card(t('已取消'), cancelled, 'cancelled') +
          this.#card(t('待确认'), pending.length, 'pending') +
          this.#card(t('已逾期'), overdue.length, 'overdue') +
          `<div class="ov-card ov-rate"><span class="num">${rate === null ? '—' : rate + '%'}</span>` +
            `<span class="lbl">${t('完成率（不含取消）')}</span></div>` +
          this.#effCard() +
        '</div>' +
        '<div class="ov-cols">' +
          this.#workloadHTML(tasks) +
          this.#versionsHTML(tasks) +
          this.#watchHTML(overdue, dueToday, pending) +
        '</div>' +
      '</div>');
  }

  #card(label, n, cls) {
    return (
      `<button type="button" class="ov-card ov-click" data-jump-status="${cls}">` +
        `<span class="num ${cls}">${n}</span><span class="lbl">${label}</span>` +
      '</button>');
  }

  /** AI 提效卡：Σ计划工期 vs Σ实际工期（实际出自任务日志的状态流转
   * 时刻，服务端聚合在排期投影里）。没有可比对项（还没活干完）就不
   * 亮这张卡——宁缺勿滥。 */
  #effCard() {
    const s = this.schedule;
    if (!s || !s.actual_done || !s.actual_planned_secs || !s.actual_spent_secs) return '';
    const mult = s.actual_planned_secs / s.actual_spent_secs;
    const m = mult >= 100 ? Math.round(mult) : mult >= 10 ? mult.toFixed(1) : mult.toFixed(2);
    return (
      `<div class="ov-card ov-rate" title="${t('{n} 项已完结任务的计划/实际比对', { n: s.actual_done })}">` +
        `<span class="num">${m}×</span>` +
        `<span class="lbl">${t('AI 提效 · 计划 {p} / 实际 {a}', { p: fmtSpan(s.actual_planned_secs), a: fmtSpan(s.actual_spent_secs) })}</span>` +
      '</div>');
  }

  /** 成员负载：在办（进行+待办）条数为主条，完成数灰字缀尾；逾期火焰警示。 */
  #workloadHTML(tasks) {
    const now = Date.now() / 1000;
    const byName = new Map();
    for (const t of tasks) {
      if (!t.assignee) continue;
      if (!byName.has(t.assignee)) byName.set(t.assignee, { open: 0, done: 0, overdue: 0 });
      const row = byName.get(t.assignee);
      if (t.status === 'todo' || t.status === 'doing') {
        row.open++;
        if (isOverdue(t, now)) row.overdue++;
      } else if (t.status === 'done') row.done++;
    }
    const rows = [...byName.entries()]
      .sort((a, b) => b[1].open - a[1].open || b[1].done - a[1].done || a[0].localeCompare(b[0], 'zh'));
    const max = Math.max(1, ...rows.map(([, r]) => r.open));
    const body = rows.length
      ? rows.map(([name, r]) => {
          const gone = this.board.departTag ? this.board.departTag(name) : '';
          return (
          `<button type="button" class="ov-lrow" data-jump-assignee="${esc(name)}">` +
            `<span class="avatar xs" style="background:${memberColor(name)}">${esc(avatarText(name))}</span>` +
            `<span class="ov-lname${gone ? ' gone' : ''}">${esc(name)}</span>` +
            goneTagHTML(gone) +
            (r.overdue ? `<i class="ov-fire" title="${t('{n} 条逾期', { n: r.overdue })}">${icon('fire', 12)}</i>` : '') +
            '<span class="ov-lbar"><i style="width:' + Math.round((r.open / max) * 100) + '%;background:' + memberColor(name) + '"></i></span>' +
            `<span class="ov-lnum"><b>${r.open}</b> ${t('在办 · {b} 完成', { b: r.done })}</span>` +
          '</button>');
        }).join('')
      : `<div class="ov-empty">${t('还没有人领任务')}</div>`;
    return (
      `<div class="ov-card ov-sec"><h3>${t('成员负载')}</h3>` + body +
        `<div class="ov-hint dim">${t('点一行 → 台账只看 TA')}</div></div>`);
  }

  /** 版本进度：任务自报的 version 聚合（均值为未取消任务的 pct 平均）。 */
  #versionsHTML(tasks) {
    const byVer = new Map();
    for (const t of tasks) {
      if (!t.version || t.status === 'cancelled') continue;
      if (!byVer.has(t.version)) byVer.set(t.version, []);
      byVer.get(t.version).push(t);
    }
    const rows = [...byVer.entries()].sort((a, b) => a[0].localeCompare(b[0], 'zh'));
    const body = rows.length
      ? rows.map(([ver, list]) => {
          const agg = Math.round(list.reduce((s, t) => s + pctOf(t), 0) / list.length);
          const doneN = list.filter((t) => t.status === 'done').length;
          return (
            '<div class="ov-vrow">' +
              `<div class="ov-vtop"><span class="ov-vname">${esc(ver)}</span>` +
                `<span class="ov-vnum">${doneN}/${list.length} · ${agg}%</span></div>` +
              `<span class="ov-vbar"><i style="width:${Math.min(100, agg)}%"></i></span>` +
            '</div>');
        }).join('')
      : `<div class="ov-empty">${t('任务还没挂版本——在任务详情里填版本名即入统计')}</div>`;
    return `<div class="ov-card ov-sec"><h3>${t('版本进度')}</h3>` + body + '</div>';
  }

  /** 需要关注：逾期 / 今日截止 / 待确认，三段一卡，点行开抽屉。 */
  #watchHTML(overdue, dueToday, pending) {
    const row = (t) => {
      const st = statusMeta(t);
      return (
        `<button type="button" class="ov-wrow" data-task="${esc(t.id)}">` +
          priorityChipHTML(t) +
          `<span class="ov-wid">${esc(t.id)}</span>` +
          `<span class="ov-wtitle">${t.milestone ? icon('milestone', 10) + ' ' : ''}${esc(t.title)}</span>` +
          `<span class="tag ${st.cls}">${st.label}</span>` +
        '</button>');
    };
    const sec = (title, list, note) =>
      '<div class="ov-wsec">' +
        `<h4>${title}<b>${list.length}</b></h4>` +
        (list.length
          ? list.slice(0, 8).map(row).join('') + (list.length > 8 ? `<div class="ov-empty">${t('…等 {n} 条，台账里看全', { n: list.length })}</div>` : '')
          : `<div class="ov-empty">${note}</div>`) +
      '</div>';
    return (
      `<div class="ov-card ov-sec"><h3>${t('需要关注')}</h3>` +
        sec(t('已逾期'), overdue, t('没有逾期')) +
        sec(t('24 小时内截止'), dueToday, t('近期没有临期任务')) +
        sec(t('待确认提案'), pending, t('没有待确认的变更')) +
      '</div>');
  }
}

/** Human span for the efficiency card: 秒/分钟/小时/天 (AI pace needs
 * the small units — the real work often fits in seconds). */
export function fmtSpan(sec) {
  if (sec < 60) return t('{n} 秒', { n: Math.max(1, Math.round(sec)) });
  if (sec < 3600) return t('{n} 分钟', { n: Math.max(1, Math.round(sec / 60)) });
  if (sec < 48 * 3600) {
    const h = sec / 3600;
    return t('{n} 小时', { n: Number.isInteger(h) ? h : h.toFixed(1) });
  }
  return t('{n} 天', { n: (sec / 86400).toFixed(1) });
}

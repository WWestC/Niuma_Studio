// planreview.js — the host's proposal gate (v2 P4-d, orchestration
// §4.4/Q2 一步到位): the review overlay over the project's single
// pending plan. Per entry: Del 剔除（deps 序号随之换算） / E 编辑
// （改人/起止/依赖/版本）; footer: A 接受（plan_accept 携 revised
// 全量——identity 字段锁死在待审件上，title/need/version/tasks 带编辑）
// / R 拒绝（两段确认）。Ops ride the WS plan verbs over the viewed
// project's owner face; outcomes toast from app.js and the banner
// clears on the slot-empty refresh.
// 负责人是 @ 式补全（复用 composer 的名单面孔）：输入即筛当前房
// 名册（空值=全名单，裸 @ 同款）、↑↓ 导航 Enter/Tab 选中、Esc 只收
// 名单不关弹层；房主行带着「名下无成员驱动」的警示混在名单里——
// 选得到但看得见代价。名单 fixed 锚输入框下沿（.mention-drop），
// 逃出 pr-list 的滚动裁剪。派谁拿不准？「让 TA 提候选」把题踢回
// 提交人（draftTo 预填 @TA 改派请求）：TA 提问卡给候选、房主对话
// 流一键选、TA 修订重提换代待审槽——回来重开弹层即是新稿。

import { Msg } from '../wire/wire.js';
import { esc, fmtDateTime, icon, dismiss, safeColor, memberColor, avatarText } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

export class PlanReview {
  /** @param {import('./project.js').ProjectBoard} board */
  constructor(board) {
    this.board = board;
    this.el = null;
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
    // 负责人补全的浮动随行：弹层内滚动/窗口缩放时名单贴住输入框；
    // 点名单外收起。挂在 open/close 上成对增删，hidden 时皆空转
    this.onPickFloat = () => this.#placePick();
    this.onPickOutside = (ev) => {
      const el = this.el;
      if (!el) return;
      const drop = el.querySelector('[data-e-assignpick]');
      if (drop && !drop.hidden && !drop.contains(ev.target) &&
          ev.target !== el.querySelector('[data-e-assignee]')) this.#hidePick();
    };
    this.pMatches = []; // the assignee pick's current candidates
    this.pActive = -1;  // keyboard-highlighted row
  }

  /** @param {import('../wire/wire.js').Plan} plan */
  open(plan) {
    this.close();
    // working copy: content fields editable, identity fields locked
    this.work = {
      title: plan.title,
      need: plan.need || '',
      version: plan.version || '',
      tasks: (plan.tasks || []).map((t) => ({
        title: t.title, desc: t.desc || '', assignee: t.assignee || '',
        start: t.start || 0, end: t.end || 0,
        deps: [...(t.deps || [])], milestone: !!t.milestone,
      })),
    };
    this.editing = -1; // the row under E, -1 = none
    this.armed = false; // R's two-step confirm
    this.plan = plan;

    const el = document.createElement('div');
    el.className = 'overlay';
    this.el = el;
    document.body.appendChild(el);
    document.addEventListener('keydown', this.onDocKey);
    el.addEventListener('scroll', this.onPickFloat, true); // capture：pr-list 滚动也跟
    window.addEventListener('resize', this.onPickFloat);
    document.addEventListener('mousedown', this.onPickOutside);
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      const act = ev.target.closest('[data-act]');
      if (act) this.#act(act.dataset.act, parseInt(act.dataset.i ?? '-1', 10));
    });
    this.#render();
  }

  close() {
    if (!this.el) return;
    this.el.removeEventListener('scroll', this.onPickFloat, true);
    window.removeEventListener('resize', this.onPickFloat);
    document.removeEventListener('mousedown', this.onPickOutside);
    this.#hidePick();
    dismiss(this.el);
    this.el = null;
    document.removeEventListener('keydown', this.onDocKey);
  }

  #act(act, i) {
    switch (act) {
      case 'del': this.#removeAt(i); break;
      case 'edit':
        // collapsing an edit row harvests its inputs first — the DOM
        // is the edit's only home until it lands in the working copy
        if (this.editing === i) this.#collectEdit();
        this.editing = this.editing === i ? -1 : i;
        this.#render();
        break;
      case 'reject':
        if (!this.armed) { this.armed = true; this.#render(); return; }
        this.#send({ type: Msg.PlanReject, plan_id: this.plan.id, project: this.board.key }, t('拒绝提案'));
        break;
      case 'accept': {
        const err = this.#validate();
        if (err) { this.#toastErr(err); return; }
        this.#send({ type: Msg.PlanAccept, plan_id: this.plan.id, project: this.board.key, revised: this.#revised() }, t('接受提案'));
        break;
      }
      case 'close': this.close(); return;
      case 'ask': {
        // 「派谁」房主拿不准时把题踢回提交人：预填一条 @TA 的改派
        // 请求进对话流（gantt 提要求同一条 draftTo 桥）。之后的环
        // 全走既有机器：TA 用提问卡给候选（对话流末尾点选即答）→
        // 按选择修订重提（Submit 换代待审槽）→ 横幅刷新，本弹层重
        // 开即见新稿。关门即弃编辑草稿——重提会换代，旧稿留不得
        const t = this.work.tasks[i];
        if (!t) return;
        const by = (this.plan.submitted_by || '').trim();
        const who = by && by !== this.board.ownerName ? by : '';
        const at = who ? `@${who}` : '@排期编排组';
        const text = `${at} 【提案 ${this.plan.id || ''} · #${i + 1}「${t.title}」】负责人请你定：` +
          '从在册成员里挑 2~3 个候选（各带一句理由），用提问卡发来让房主一键选；' +
          '按房主的选择修订提案后重新提交，TA 回来审。';
        this.board.hooks.draftTo(this.board.key, text);
        this.close();
        return;
      }
      default: break;
    }
  }

  // Deleting entry i (1-based index i+1): deps naming it drop, deps
  // above it shift down — the surviving references stay the entries
  // they meant.
  #removeAt(i) {
    const gone = i + 1;
    this.work.tasks.splice(i, 1);
    for (const t of this.work.tasks) {
      t.deps = (t.deps || []).filter((d) => d !== gone).map((d) => (d > gone ? d - 1 : d));
    }
    this.editing = -1;
    this.armed = false;
    this.#render();
  }

  #validate() {
    const w = this.work;
    if (!w.tasks.length) return t('剔除后提案为空——改用拒绝');
    this.#collectEdit(); // pull the open edit row's inputs first
    for (let i = 0; i < w.tasks.length; i++) {
      const task = w.tasks[i];
      if (task.start > 0 && task.end > 0 && task.start > task.end) {
        return t('第 {i} 条「{title}」起点晚于终点', { i: i + 1, title: task.title });
      }
      for (const d of task.deps || []) {
        if (d < 1 || d > i) return t('第 {i} 条的依赖序号 {d} 非法（只能引用更早条目 1–{max}）', { i: i + 1, d, max: i });
      }
    }
    return '';
  }

  // pull the currently open edit row's inputs into the working copy
  #collectEdit() {
    if (this.editing < 0 || !this.el) return;
    const row = this.el.querySelector(`[data-edit-row="${this.editing}"]`);
    if (!row) return;
    const t = this.work.tasks[this.editing];
    if (!t) return;
    t.assignee = row.querySelector('[data-e-assignee]').value.trim();
    t.start = dtToTs(row.querySelector('[data-e-start]').value);
    t.end = dtToTs(row.querySelector('[data-e-end]').value);
    t.deps = row.querySelector('[data-e-deps]').value
      .split(/[,，\s]+/).filter(Boolean).map((n) => parseInt(n, 10))
      .filter((n) => Number.isFinite(n));
  }

  /** revised = the full plan with edits; identity fields stay locked. */
  #revised() {
    const p = this.plan;
    return {
      id: p.id, req: p.req || '', project_key: p.project_key || this.board.key,
      title: this.work.title || p.title, need: this.work.need,
      version: this.work.version, tasks: this.work.tasks,
      submitted_by: p.submitted_by || '', submitted_ts: p.submitted_ts || 0,
    };
  }

  #send(frame, label) {
    this.board.frameTo(frame, label);
    this.close();
  }

  #toastErr(text) {
    const el = this.el?.querySelector('[data-pr-status]');
    if (el) {
      el.className = 'overlay-status err';
      el.textContent = text;
    }
  }

  #render() {
    if (!this.el) return;
    const p = this.plan;
    const w = this.work;
    this.el.innerHTML =
      '<div class="overlay-card pr-card">' +
        `<div class="overlay-head"><strong>${t('审阅提案 {id}', { id: esc(p.id || '') })}</strong>` +
          `<span class="dim small">${t('{who} 提交 · {when}', { who: esc(p.submitted_by || ''), when: fmtDateTime(p.submitted_ts) })}</span></div>` +
        `<div class="pr-title">${esc(w.title)}</div>` +
        (w.need ? `<div class="pr-need">${esc(w.need)}</div>` : '') +
        `<div class="field-inline"><span>${t('挂靠版本')}</span>` +
          `<input class="input" data-e-version value="${esc(w.version)}" placeholder="${t('留空=不挂版本')}">` +
        '</div>' +
        `<div class="pr-req dim small">${p.req ? t('需求回指 {req}（锁定）', { req: esc(p.req) }) : t('无需求回指')} · ${t('身份字段锁定，编辑仅作用于内容')}</div>` +
        '<div class="pr-list">' +
          (w.tasks.map((task, i) => {
            const meta = [
              task.assignee ? esc(task.assignee) : t('任务池'),
              task.start ? fmtDateTime(task.start) : t('未定'),
              task.end ? `~ ${fmtDateTime(task.end)}` : '',
              (task.deps || []).length ? t('依赖 #{list}', { list: task.deps.map((d) => d).join(',#') }) : '',
              task.milestone ? icon('milestone', 10) + ` ${t('里程碑')}` : '',
            ].filter(Boolean).join(' · ');
            // 房主名下任务没有任何成员驱动（不会被注入任何会话，也没有
            // 填写面）——审阅时当面提醒，别让「需求澄清」类工作静默扔回房主
            const ownWarn = task.assignee && task.assignee === this.board.ownerName
              ? `<span class="tag" style="color:var(--warn);background:rgba(255,136,0,.1)">${t('房主名下无成员驱动')}</span>` : '';
            return (
              `<div class="pr-row${this.editing === i ? ' editing' : ''}">` +
                `<div class="pr-row-main"><b>#${i + 1}</b> ${esc(task.title)}${ownWarn}<span class="dim small">${meta}</span></div>` +
                '<div class="pr-row-ops">' +
                  `<button type="button" class="btn small" data-act="edit" data-i="${i}">${this.editing === i ? t('收起') : t('编辑')}</button>` +
                  `<button type="button" class="btn small danger" data-act="del" data-i="${i}">${t('剔除')}</button>` +
                '</div>' +
                (this.editing === i ? this.#editRowHTML(i, task) : '') +
              '</div>');
          }).join('') || `<div class="dim small">${t('（全部剔除——只能拒绝了）')}</div>`) +
        '</div>' +
        '<div class="overlay-status" data-pr-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-act="close">${t('关闭')}</button>` +
          `<button type="button" class="btn danger${this.armed ? ' armed' : ''}" data-act="reject">${this.armed ? t('确认拒绝？再点一次') : t('拒绝')}</button>` +
          `<button type="button" class="btn gold" data-act="accept">${t('接受（含修订）')}</button>` +
        '</div>' +
      '</div>';
    const ver = this.el.querySelector('[data-e-version]');
    ver?.addEventListener('input', () => { w.version = ver.value.trim(); });
    if (this.editing >= 0) {
      const row = this.el.querySelector(`[data-edit-row="${this.editing}"]`);
      const inp = row?.querySelector('[data-e-assignee]');
      const drop = row?.querySelector('[data-e-assignpick]');
      if (inp && drop) {
        this.#wirePick(inp, drop);
        inp.focus();
        // 失焦文档里浏览器的 focus 事件派发会被吞（后台窗/webview）——
        // 开行时直接亮一次名单，不赌事件；聚焦监听照留，回点输入框再亮
        this.#syncPick(inp, drop, true);
      }
    }
  }

  #editRowHTML(i, task) {
    // 提交人＝改派请求的收件人（房主自提的提案没有成员收件人，
    // 落回岗位群呼 @排期编排组——与 gantt 提要求同一个词）
    const by = (this.plan.submitted_by || '').trim();
    const who = by && by !== this.board.ownerName ? by : '';
    // 指派给房主时的当面提示：任务不会驱动任何成员会话，需要房主
    // 澄清/拍板的工作应改派成员（成员用提问卡与房主确认——问答都
    // 发生在本办公室对话流，卡在末尾点选即答；拿不准派谁就把题踢
    // 回给提交人，见「提候选」）
    const ownNote = task.assignee && task.assignee === this.board.ownerName
      ? `<div class="dim small" style="color:var(--warn)">${t('房主名下任务不会被注入任何会话——需要房主澄清/拍板的，改派成员，由成员用提问卡（AskUserQuestion）与房主确认；拿不准派谁就点「提候选」，选择题会出现在本办公室对话流末尾，点选即答')}</div>`
      : '';
    return (
      `<div class="pr-edit" data-edit-row="${i}">` +
        `<div class="field-inline"><span>${t('负责人')}</span>` +
          `<input class="input" data-e-assignee value="${esc(task.assignee)}" placeholder="${t('留空=任务池')}">` +
          `<button type="button" class="btn small" data-act="ask" data-i="${i}">${t('让{who}提候选', { who: esc(who || '编排组') })}</button>` +
          '<div class="mention-drop" data-e-assignpick hidden></div>' +
        '</div>' + ownNote +
        `<div class="field-inline"><span>${t('起止')}</span>` +
          `<input class="input" type="datetime-local" data-e-start value="${tsToDt(task.start)}">` +
          `<input class="input" type="datetime-local" data-e-end value="${tsToDt(task.end)}">` +
        '</div>' +
        `<div class="field-inline"><span>${t('依赖')}</span>` +
          `<input class="input mono" data-e-deps value="${esc((task.deps || []).join(','))}" placeholder="${t('如 1,2（只能引用更早条目）')}">` +
          (i === 0 ? `<span class="dim small">${t('首条无前置')}</span>` : `<span class="dim small">${t('可选 1–{n}', { n: i })}</span>`) +
        '</div>' +
      '</div>');
  }

  // ── 负责人 @ 式补全（composer 名单面孔的指派版）────────────────────
  // 候选宇宙：当前房名册 ∪ 房主（带警示，选得到但看得见代价）∪ 编辑中
  // 已有的名字（名册外的幽灵不留死角）。roster() 只给名字，这里要
  // 头像与角色，直接读房对象的 members（与 mentionCandidates 同源）。
  #assigneeCands() {
    const room = this.board.book?.rooms?.get(this.board.key);
    const out = room
      ? [...room.members.values()].map((m) => ({ name: m.name, role: m.role || '' }))
      : [];
    const owner = this.board.ownerName;
    if (owner && !out.some((c) => c.name === owner)) {
      out.push({ name: owner, role: t('房主 · 名下无成员驱动'), warn: true });
    }
    const cur = (this.work.tasks[this.editing] || {}).assignee || '';
    if (cur && !out.some((c) => c.name === cur)) out.push({ name: cur, role: t('名册外') });
    return out;
  }

  // the edit row's DOM is rebuilt per render — the pick re-wires fresh
  #wirePick(inp, drop) {
    if (!inp || !drop) return;
    inp.addEventListener('input', () => this.#syncPick(inp, drop));
    // 聚焦亮全名单（裸 @ 同款）：指派场景里输入框常已装着完整名字
    // （房主名），按「敲完即收」的律名单就永远不露脸——换人恰恰是
    // 这个框的主职
    inp.addEventListener('focus', () => this.#syncPick(inp, drop, true));
    inp.addEventListener('blur', () => setTimeout(() => this.#hidePick(), 150)); // let a click land first
    inp.addEventListener('keydown', (ev) => this.#pickKey(ev, inp, drop));
    drop.addEventListener('mousedown', (ev) => ev.preventDefault()); // keep focus
    drop.addEventListener('click', (ev) => {
      const item = ev.target.closest('[data-name]');
      if (item) this.#pickDone(item.dataset.name, inp, drop);
    });
  }

  // 前缀优先、包含兜底（与 @ 补全同律）；空 stem=全名单、已敲成完整
  // 名字就收——补全只伺候没打完的时候（聚焦另走 showAll 强制全名单）
  #syncPick(inp, drop, showAll = false) {
    const stemStr = showAll ? '' : inp.value.trim();
    const stem = stemStr.toLowerCase();
    const starts = [], contains = [];
    for (const c of this.#assigneeCands()) {
      const n = c.name.toLowerCase();
      if (n === stem) continue;
      if (n.startsWith(stem)) starts.push(c);
      else if (stem && n.includes(stem)) contains.push(c);
    }
    this.pMatches = [...starts, ...contains];
    if (!this.pMatches.length) { this.#hidePick(); return; }
    this.pActive = 0;
    this.pInp = inp;
    this.pDrop = drop;
    drop.innerHTML = this.pMatches.map((c, i) =>
      `<button type="button" class="pick-item${i === 0 ? ' active' : ''}" data-name="${esc(c.name)}">` +
        `<span class="avatar xs" style="background:${safeColor(memberColor(c.name))}">${esc(avatarText(c.name))}</span>` +
        `<span class="name">${esc(c.name)}</span>` +
        `<span class="role${c.warn ? ' warn' : ''}">${esc(c.role || '')}</span>` +
      '</button>').join('');
    drop.hidden = false;
    this.#placePick();
  }

  // fixed 锚位：贴输入框下沿，底放不下翻上方，左右钳在视口内
  // （气泡卡片篇钳位同款）；滚动/缩放由 onPickFloat 回访
  #placePick() {
    const inp = this.pInp, drop = this.pDrop;
    if (!inp || !drop || drop.hidden || !drop.isConnected) return;
    const r = inp.getBoundingClientRect();
    const w = Math.max(280, Math.min(r.width, 360));
    drop.style.width = `${w}px`;
    drop.style.left = `${Math.max(8, Math.min(r.left, innerWidth - w - 8))}px`;
    const h = drop.offsetHeight || 160;
    let top = r.bottom + 4;
    if (top + h > innerHeight - 8) top = Math.max(8, r.top - h - 4);
    drop.style.top = `${top}px`;
  }

  #pickKey(ev, inp, drop) {
    // IME 组合中的键不是导航键（拼音打一半 ↑↓ 是候选窗的）
    if (ev.isComposing || ev.keyCode === 229) return;
    if (!this.pMatches.length) return;
    const delta = ev.key === 'ArrowDown' ? 1 : ev.key === 'ArrowUp' ? -1 : 0;
    if (delta) {
      ev.preventDefault(); ev.stopPropagation();
      this.pActive = (this.pActive + delta + this.pMatches.length) % this.pMatches.length;
      [...drop.children].forEach((el, i) => el.classList.toggle('active', i === this.pActive));
      drop.children[this.pActive]?.scrollIntoView({ block: 'nearest' });
      return;
    }
    if (ev.key === 'Enter' || ev.key === 'Tab') {
      ev.preventDefault(); ev.stopPropagation();
      this.#pickDone(this.pMatches[this.pActive].name, inp, drop);
      return;
    }
    if (ev.key === 'Escape') {
      // 只收名单不关弹层——document 上的 Escape 关门键别被连坐
      ev.preventDefault(); ev.stopPropagation();
      this.#hidePick();
    }
  }

  #pickDone(name, inp, drop) {
    inp.value = name;
    this.#hidePick();
    inp.focus();
  }

  #hidePick() {
    this.pMatches = [];
    this.pActive = -1;
    if (this.pDrop) {
      this.pDrop.hidden = true;
      this.pDrop.innerHTML = '';
    }
  }
}

/** Unix seconds → datetime-local value (local zone, '' when unset). */
function tsToDt(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** datetime-local value → Unix seconds (0 when unset/invalid). */
function dtToTs(v) {
  if (!v) return 0;
  const ms = Date.parse(v);
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : 0;
}

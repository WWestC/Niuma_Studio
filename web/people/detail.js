// detail.js — the person detail sidebar (frontend 稿 §4.2 侧栏 320px):
// the orchestrator of the sidebar's sections — profile (identity/rank/
// manual link/collapsible prompt with copy), latest report, in-progress
// tasks (/kb/tasks?assignee= linking into the project board), the
// session 三态 crossed with /dispatch snapshots (受管=managed by a
// dispatcher vs 游离=free), and the management row: rank_set / kick
// (two-step confirm) / steer / archive over the owner write face. The
// two heavy sub-views live beside this file: the capability preview
// (fabric.js) and the departure pipeline (offboard.js).

import {
  getPerson, getPersonHistory, getTasksByAssignee, getDispatch, postSteer, postRecall,
  getModels, postSetModel,
} from '../wire/api.js';
import { Msg } from '../wire/wire.js';
import { esc, safeColor, fmtAge, fmtDateTime, avatarText, icon, copyToClipboard } from '../ui/dom.js';
import { t as msgT } from '../ui/i18n.js';
import { fillReasoningOptions, levelLabel, providerGroupLabel } from './modellevels.js';
import { rankLabel, personState, STATE_LABEL } from '../ui/person.js';
import { openArchive } from '../project/taskarchive.js'; // r_26 t_212：履历条目 → 任务档案浮层
import { OffboardPanel } from './offboard.js';
import { FabricPanel } from './fabric.js';
import { Progress } from '../ui/feedback.js';

// The model pick list is machine-global (every personal provider in
// the local ZCode config): fetch once per board lifetime, share across
// every member sidebar.
let modelList;            // undefined = 未拉取；null = 失败；{default, levels?, providers}
let modelListLoading = false;

const TASK_STATUS = {
  todo: [msgT('待处理'), 'st-todo'], doing: [msgT('进行中'), 'st-doing'],
  pending: [msgT('待确认'), 'st-pending'], done: [msgT('已完成'), 'st-done'],
  cancelled: [msgT('已取消'), 'st-cancelled'],
};
const SESSION_STATUS = {
  ready: msgT('就绪'), idle: msgT('待命'), running: msgT('工作中'),
};
const ACTIVE_TASKS = new Set(['todo', 'doing', 'pending']);

export class DetailView {
  /** @param {HTMLElement} side @param {import('./people.js').PeopleBoard} board */
  constructor(side, board) {
    this.side = side;
    this.board = board;
    this.state = 'empty';   // empty | loading | error | ready | missing
    this.name = null;
    this.data = null;       // PersonDetail
    this.tasks = [];        // in-progress slice
    this.dispatch = null;   // {project, status, queued, inject} | null = 游离
    this.steerOpen = false;
    this.steerDraft = '';   // typed steer text survives re-renders
    this.kickArmed = false;
    this.archiveArmed = false;
    this.recallArmed = false;
    this.loadSeq = 0;

    this.fabric = new FabricPanel(() => this.render());
    this.off = new OffboardPanel(
      (text, kind) => this.board.toast.show(text, kind),
      () => this.board.requestRefresh(),
      () => this.render(),
    );

    side.addEventListener('click', (ev) => this.#onClick(ev));
    side.addEventListener('input', (ev) => {
      if (ev.target.matches('[data-steer-text]')) this.steerDraft = ev.target.value;
    });
    side.addEventListener('change', (ev) => this.onChange(ev));
  }

  // onChange 是 change 事件的公共缝（composer 同款）：模型档两槽选择
  // 即提交——「按了就要切换」，没有先挑后确认的两步。模型一变先按新
  // 模型重建强度选项（已选强度能存活就存活，不能则回模型缺省档；回
  // 默认链时按默认模型的档位表——ZCode 同口径），随后整对即交写面；
  // 强度单独挑同样即交，模型槽原样带上（下拉显示的就是座位现档）＝
  // 只动强度不动模型（空模型＋非空强度＝默认链挂档，写面单飞口径）。
  onChange(ev) {
    if (ev.target.matches('[data-fabric-project]')) {
      // 手动挑位：标记 picked 后才落面板状态——刷新周期里这张脸保持
      // 操作员的挑位，换人时 resetPick 弃置、回落新本人所在项目
      this.fabric.picked = true;
      this.fabric.load(ev.target.value, this.name);
      return;
    }
    if (ev.target.matches('[data-model]')) {
      const lvl = this.side.querySelector('[data-reasoning]');
      if (lvl) {
        fillReasoningOptions(lvl, ev.target.value, modelList?.levels, lvl.value,
          modelList?.default || '');
      }
      this.#setModel(ev.target.value, lvl?.value || '');
      return;
    }
    if (ev.target.matches('[data-reasoning]')) {
      const sel = this.side.querySelector('[data-model]');
      this.#setModel(sel?.value ?? '', ev.target.value);
    }
  }

  async reload(name) {
    // Same-person reloads are the refresh cycle's heartbeat (every room
    // event taps it): they must not reset the transient UI state — the
    // two-step confirm arms and the typed steer text belong to the
    // operator's in-flight interaction, not to the data. Only a real
    // person switch starts clean.
    const samePerson = name === this.name;
    if (!samePerson) {
      this.steerOpen = false;
      this.steerDraft = '';
      this.kickArmed = false;
      this.archiveArmed = false;
      this.recallArmed = false;
      this.off.reset();
      this.tasks = null;
      this.dispatch = null; // stale cross from the previous person must not leak
      this.history = null;  // r_26 t_212：交付史也是 bonus 读，换人必须清
      // 换人弃置上一人的席位挑位：能力装配重新默认到新本人所在项目
      //（上一人的手动挑位跟过去，就是「详情项目与本人所在对不上」）
      this.fabric.resetPick();
    }
    this.name = name;
    const seq = ++this.loadSeq;
    if (!samePerson || this.state !== 'ready') {
      this.state = 'loading';
      this.render();
    }
    try {
      const [detail, tasks, history] = await Promise.all([
        getPerson(name),
        getTasksByAssignee(name).catch(() => null), // the ledger read is a bonus, not a gate
        getPersonHistory(name).catch(() => null),   // r_26：交付史同 bonus（挂了不挡详情）
      ]);
      if (seq !== this.loadSeq) return;
      this.data = detail;
      this.tasks = tasks; // null = the ledger read failed; PersonDetail's copy stands in
      this.history = history;
      this.state = 'ready';
      this.#cross().then(() => { if (seq === this.loadSeq) this.render(); });
      // 席位默认跟人走：PersonSummary.room（座位真源，同一轮 fetch 里
      // 已到手）优先，座位暂缺（重接窗口）时回落上一轮 cross 的受管项
      // 目——不能等 #cross：它此刻才起步，而换人分支刚把 dispatch 清
      // 空，旧写法在这里读到的永远是 null，于是每个人物详情的席位都
      // 先落「大厅」再粘死。操作员的手动挑位（picked）只在本人的视图
      // 生命周期内存活，换人即失效。
      const seat = this.fabric.picked
        ? this.fabric.project
        : (this.data?.room || this.dispatch?.project || 'default');
      this.fabric.load(seat, name);
    } catch (err) {
      if (seq !== this.loadSeq) return;
      this.data = null;
      if (err.status === 404) { this.state = 'missing'; }
      else { this.state = 'error'; this.error = err; }
    }
    if (seq === this.loadSeq) this.render();
  }

  // 受管/游离 + the session 三态: the staffing read face is not live
  // yet, so the cross runs over every active room's /dispatch snapshot
  // and takes the first row naming this person (the lobby included).
  async #cross() {
    const choices = this.board.projectChoices(); // lobby first
    const snaps = await Promise.all(
      choices.map((c) => getDispatch(c.key === 'default' ? '' : c.key).catch(() => null)),
    );
    let found = null;
    for (let i = 0; i < choices.length && !found; i++) {
      for (const row of snaps[i] || []) {
        if (row.name === this.name) { found = { project: choices[i].key, ...row }; break; }
      }
    }
    this.dispatch = found;
  }

  render() {
    // Same paint gate as the roster: skip the innerHTML write when the
    // markup is unchanged — the sidebar repaints on every room event,
    // and rewriting identical nodes mid-click swallows the second click
    // of a two-step confirm (and the focus of a half-typed steer).
    if (this.state === 'empty' || !this.name) {
      this.#paint(`<div class="side-empty">${msgT('从花名册选择一位成员查看详情')}</div>`);
      return;
    }
    if (this.state === 'loading') {
      this.#paint('<div class="side-skeleton">' +
        '<div class="sk-line w60"></div><div class="sk-line w40"></div>'.repeat(3) + '</div>');
      return;
    }
    if (this.state === 'missing') {
      this.#paint(
        `<div class="state-card error"><strong>${esc(this.name)}</strong><span>${msgT('名册中无此人（可能已被移出或归档——同名重新出生即复职）')}</span></div>`);
      return;
    }
    if (this.state === 'error') {
      this.#paint(
        `<div class="state-card error"><strong>${msgT('详情读取失败')}</strong>
         <span>${esc(this.error?.message || String(this.error))}</span>
         <button type="button" class="btn" data-retry>${msgT('重试')}</button></div>`);
      return;
    }
    this.#paint(
      this.#headerHTML() +
      this.#reportHTML() +
      this.#tasksHTML() +
      this.#historyHTML() + // r_26 t_212：交付史（履历非考核）
      this.#profileHTML() +
      this.fabric.html(this.board.projectChoices()) +
      this.#opsHTML() +
      this.off.html(this.data));
    // The model pick list arrives async — one repaint when it lands.
    if (modelList === undefined && !modelListLoading) {
      modelListLoading = true;
      getModels()
        .then((l) => { modelList = l; })
        .catch(() => { modelList = null; })
        .finally(() => { modelListLoading = false; this.render(); });
    }
  }

  #paint(html) {
    if (this._lastHTML === html) return;
    this._lastHTML = html;
    this.side.innerHTML = html;
    // 思考强度下拉随 paint 落座：按当前座位档（模型+强度）填选项——
    // 字符串模板里留的是空壳，选项永远由 modellevels 的单一入口生成；
    // 座位模型留空（默认链）时按默认模型的档位表填。
    const lvl = this.side.querySelector('[data-reasoning]');
    if (lvl) {
      fillReasoningOptions(lvl, this.dispatch?.model || '',
        modelList?.levels, this.dispatch?.reasoning || '', modelList?.default || '');
    }
  }

  // A room key's display name — 与花名册项目列同源同口径（大厅/项目
  // 名，而非裸 key）：详情里的项目读出来要和名册列对得上。
  #roomName(key) {
    return this.board.projectChoices().find((c) => c.key === key)?.name || key;
  }

  #headerHTML() {
    const p = this.data;
    const state = personState(p);
    // 与花名册行同款：成员色字母头像＋右下角存在点（飞书组织架构式）
    const avatar =
      `<span class="pavatar" style="background:${safeColor(p.color)}">` +
      `${esc(avatarText(p.name))}<i class="pdot ${state}"></i></span>`;
    const session = this.dispatch
      ? `<span class="tag ok" title="${esc(this.dispatch.project)}">${msgT('受管')} · ${esc(this.#roomName(this.dispatch.project))}</span>
         <span class="tag">${SESSION_STATUS[this.dispatch.status] || this.dispatch.status}` +
        (this.dispatch.queued || this.dispatch.inject
          ? msgT('（队列 {q}·注入 {i}）', { q: this.dispatch.queued, i: this.dispatch.inject }) : '') + '</span>'
      : `<span class="tag dim">${msgT('游离（无调度器接管）')}</span>`;
    // 等级字色与名片卡同一副色阶（index.html 的 --rank-1..9，房主走金字槽）
    const host = p.local || p.rank === 99;
    const rc = !host && p.rank >= 1 && p.rank <= 9 ? ` style="color:var(--rank-${p.rank})"` : '';
    return `<div class="side-head">${avatar}
        <div class="side-title"><strong>${esc(p.name)}</strong>
          <span class="dim">${esc(p.role || msgT('未设身份'))} · <b${rc}>${rankLabel(p)}</b></span></div>
        <button type="button" class="icon-btn side-close" data-close title="${msgT('关闭')}">${icon('close', 14)}</button>
      </div>
      <div class="side-tags">
        <span class="tag st-${state}">${STATE_LABEL[state]}</span>
        ${p.local ? `<span class="tag you">${msgT('本机房主')}</span>` : ''}
        ${session}
      </div>`;
  }

  #reportHTML() {
    const r = this.data.last_report;
    if (!r) return `<div class="side-sec"><h3>${msgT('最近汇报')}</h3><div class="dim small">${msgT('还没有汇报过')}</div></div>`;
    return `<div class="side-sec"><h3>${msgT('最近汇报')} <span class="dim small">${esc(fmtAge(this.data.last_report_ts))}</span></h3>
      <div class="report-text">${esc(r)}</div></div>`;
  }

  #tasksHTML() {
    // /kb/tasks?assignee= newest-first; 在办 = todo|doing|pending
    const all = this.tasks ?? (this.data?.tasks || []);
    const active = all.filter((t) => ACTIVE_TASKS.has(t.status));
    let body;
    if (!active.length) {
      body = `<div class="dim small">${msgT('名下无在办任务')}</div>`;
    } else {
      body = active.slice(0, 6).map((t) => {
        const [label, cls] = TASK_STATUS[t.status] || [t.status, 'st-todo'];
        const pct = t.progress_pct || 0; // >0 时行尾给 16px 圆形进度（飞书进度条规范）
        return `<button type="button" class="task-line" data-goto-project="${esc(t.project_key || '')}" title="${esc(t.title)}">
          <span class="tag ${cls}">${label}</span>
          <span class="task-title">${esc(t.id)} ${esc(t.title)}</span>
          ${pct > 0 ? `<span class="task-prog">${Progress.ring(pct)}</span>` : ''}
        </button>`;
      }).join('') + (active.length > 6 ? `<div class="dim small">${msgT('…另有 {n} 条', { n: active.length - 6 })}</div>` : '');
    }
    return `<div class="side-sec"><h3>${msgT('在办任务 · {n}', { n: active.length })}</h3>${body}</div>`;
  }

  #profileHTML() {
    const p = this.data;
    const manual = p.manual
      ? `<button type="button" class="manual-link" data-open-doc="${esc(p.manual)}">${msgT('手册')} ${esc(p.manual)} ↗</button>`
      : `<span class="dim small">${msgT('未挂岗位手册')}</span>`;
    const prompt = p.prompt
      ? `<details class="fold"><summary>${msgT('接入提示词（{n} 字）· 点击展开', { n: p.prompt.length })}</summary>
           <pre class="pre">${esc(p.prompt)}</pre>
           <button type="button" class="btn small" data-copy-prompt>${msgT('复制提示词')}</button>
         </details>`
      : `<div class="dim small">${msgT('未保存接入提示词')}</div>`;
    return `<div class="side-sec"><h3>${msgT('档案')}</h3>${manual}${prompt}</div>`;
  }

  // r_26 t_212：交付史（履历非考核——定稿 §三）。三件套：入职锚（头像
  // 下小字语进节头）/需求线徽带（色相按需求号哈希，服务端已算好 color）
  // /按线分组列表（组内 done 倒序，条目点击开任务档案浮层——两页互为
  // 入口）。边界条款：不设排序视图、不设跨成员对比——聚合数字只在组头
  // 做静态计数。
  #historyHTML() {
    const h = this.history;
    if (!h) return `<div class="side-sec"><h3>${msgT('交付履历')}</h3><div class="dim small">${msgT('（交付史读取失败或未启用）')}</div></div>`;
    const anchor = h.joined
      ? `<span class="dim small">${msgT('入职于 {age}前', { age: fmtAge(h.joined) })}</span>` : '';
    if (!h.lines || !h.lines.length) {
      return `<div class="side-sec"><h3>${msgT('交付履历')} ${anchor}</h3>` +
        `<div class="dim small">${msgT('还没有交付的单——第一张在途上')}</div></div>`;
    }
    const total = h.lines.reduce((s, l) => s + l.done.length, 0);
    const line = (l) => {
      const label = l.req || msgT('未挂线');
      // v2.10 号段分项目：线按（项目, 需求）分组——非大厅项目的线挂
      // 项目小标，条目点击带架解析（两个项目的 r_01 不再并线）
      const proj = l.project && l.project !== 'default'
        ? `<span class="hist-proj" title="${esc(l.project)}">@${esc(l.project)}</span>` : '';
      return `<div class="hist-line">` +
        `<div class="hist-head">` +
          `<span class="hist-badge" style="background:${safeColor(l.color)}" title="${esc(label)}"></span>` +
          `<span class="hist-req">${esc(label)}</span>${proj}` +
          (l.title ? `<span class="dim small hist-title">${esc(l.title)}</span>` : '') +
          `<span class="dim small">${msgT('· {n} 张', { n: l.done.length })}</span>` +
        `</div>` +
        l.done.map((d) =>
          `<button type="button" class="hist-item" data-task-archive="${esc(d.id)}" data-task-project="${esc(l.project || '')}">` +
            `<span class="dim small">${fmtDateTime(d.ts)}</span>` +
            `<span class="hist-item-title">${esc(d.id)} ${esc(d.title)}</span>` +
          `</button>`).join('') +
      `</div>`;
    };
    return `<div class="side-sec"><h3>${msgT('交付履历 · {n} 张', { n: total })} ${anchor}</h3>` +
      h.lines.map(line).join('') + '</div>';
  }

  #opsHTML() {
    const p = this.data;
    const isOwner = !!p.local || p.rank === 99;
    const modelOps = this.dispatch
      ? `<label class="field-inline"><span>${msgT('模型')}</span>
          <select class="input" data-model>${this.#modelOptions()}</select>
          <button type="button" class="icon-btn" data-models-refresh title="${msgT('刷新模型清单')}">${icon('refresh')}</button>
        </label>
        <label class="field-inline"><span>${msgT('思考强度')}</span>
          <select class="input" data-reasoning aria-label="${msgT('思考强度（选择即提交）')}"></select>
        </label>`
      : `<span class="dim small">${msgT('游离成员无座位档（出生时选模型）')}</span>`;
    const rankOps = isOwner
      ? `<span class="dim small">${msgT('房主不可调级/移出')}</span>`
      : `<label class="field-inline"><span>${msgT('调级')}</span>
          <select class="input" data-rank>${[1, 2, 3, 4, 5, 6, 7, 8, 9]
            .map((n) => `<option value="${n}"${n === p.rank ? ' selected' : ''}>Lv.${n}</option>`).join('')}</select>
          <button type="button" class="btn small" data-rank-set>${msgT('确定')}</button>
        </label>`;
    const kick = isOwner ? '' :
      `<button type="button" class="btn small danger${this.kickArmed ? ' armed' : ''}" data-kick>
        ${this.kickArmed ? msgT('再点一次确认移出') : msgT('移出办公室')}</button>`;
    const archive = isOwner ? '' :
      `<button type="button" class="btn small${this.archiveArmed ? ' armed' : ''}" data-archive>
        ${this.archiveArmed ? msgT('再点一次确认归档') : msgT('归档档案')}</button>`;
    const steer = this.steerOpen
      ? `<div class="steer-box">
          <textarea class="input" rows="2" data-steer-text placeholder="${msgT('注入指令到 {name} 的当前会话', { name: esc(p.name) })}">${esc(this.steerDraft)}</textarea>
          <button type="button" class="btn small gold" data-steer-send>${msgT('发送 steer')}</button>
        </div>`
      : `<button type="button" class="btn small" data-steer-open ${this.dispatch ? '' : `title="${msgT('游离成员无会话可注入，仍可尝试')}"`}>${msgT('steer 注入')}</button>`;
    return `<div class="side-sec"><h3>${msgT('管理操作')}</h3>
      <div class="ops-grid">${rankOps}${modelOps}${kick}${archive}${steer}
        <button type="button" class="btn small${this.recallArmed ? ' armed' : ''}" data-recall>
          ${this.recallArmed ? msgT('再点一次确认召回') : msgT('召回 recall')}</button>
      </div>
      <div class="dim small">${msgT('离职走下方五步管线；已离职成员可召回重新入座')}</div>
    </div>`;
  }

  // #modelOptions builds the 改档 dropdown: 默认 first (the empty pick,
  // labelled with the process default when known), one optgroup per
  // provider (value "providerId/modelId"), the member's current tier
  // selected — a current tier outside the list (a pack-slot model, or
  // the list failed to load) still shows as its own option so the
  // select never lies about what the seat runs. Undrivable account
  // faces stay visible but disabled (reason in the group label, the
  // desktop registry's own口径) — except the tier the seat is
  // currently running: that option must stay re-pickable, it's the
  // truth.
  #modelOptions() {
    const cur = this.dispatch?.model || '';
    const def = modelList?.default || msgT('调度默认');
    let html = `<option value=""${cur === '' ? ' selected' : ''}>${msgT('默认（{v}）', { v: esc(def) })}</option>`;
    const refs = new Set(['']);
    for (const prov of modelList?.providers || []) {
      const off = prov.available === false;
      const group = prov.models?.map((m) => {
        const ref = `${prov.id}/${m}`;
        refs.add(ref);
        const dis = off && ref !== cur ? ' disabled' : '';
        return `<option value="${esc(ref)}"${ref === cur ? ' selected' : ''}${dis}>${esc(m)}</option>`;
      }).join('') || '';
      if (group) html += `<optgroup label="${esc(providerGroupLabel(prov))}">${group}</optgroup>`;
    }
    if (cur && !refs.has(cur)) {
      html = `<option value="${esc(cur)}" selected>${esc(cur)}${msgT('（当前档）')}</option>` + html;
    }
    return html;
  }

  #onClick(ev) {
    const t = ev.target;
    if (t.closest('[data-retry]')) { this.reload(this.name); return; }
    // r_26 t_212：履历条目点击开任务档案浮层（两页互为入口）——带线
    // 所属项目的架解析（v2.10）
    const histItem = t.closest('[data-task-archive]');
    if (histItem) { openArchive(histItem.dataset.taskArchive, histItem.dataset.taskProject || undefined); return; }
    if (t.closest('[data-close]')) {
      this.board.selected = null;
      this.state = 'empty';
      this.name = null;
      this.render();
      this.board.roster.render();
      return;
    }
    if (t.closest('[data-goto-project]')) {
      location.hash = '#/project'; // the P4 board placeholder
      return;
    }
    if (t.closest('[data-copy-prompt]')) { this.#copyPrompt(); return; }
    const od = t.closest('[data-open-doc]');
    if (od) {
      window.dispatchEvent(new CustomEvent('dh:open-doc', { detail: od.dataset.openDoc }));
      return;
    }
    if (t.closest('[data-rank-set]')) {
      const sel = this.side.querySelector('[data-rank]');
      const rank = parseInt(sel?.value || '0', 10);
      if (rank >= 1 && rank <= 9) {
        this.board.ownerSend({ type: Msg.RankSet, name: this.name, rank }, msgT('调级 {name}→Lv.{rank}', { name: this.name, rank }));
        this.board.requestRefresh();
      }
      return;
    }
    // 刷新模型清单：清模块缓存重渲染——render() 里既有的异步拉取块
    // 会重新取一次、落位时再刷一帧（应用内没有整页刷新入口，「重看
    // 一次」就地解决；改档两槽选择即提交，没有暂存挑选可丢——下拉
    // 恒显座位现档）。
    if (t.closest('[data-models-refresh]')) {
      modelList = undefined;
      modelListLoading = false;
      this.render();
      return;
    }
    if (t.closest('[data-kick]')) {
      if (!this.kickArmed) { // 两段确认 (§4.2): arm, then commit on the second click
        this.kickArmed = true;
        this.render();
        setTimeout(() => { if (this.kickArmed) { this.kickArmed = false; this.render(); } }, 4000);
        return;
      }
      this.kickArmed = false;
      this.board.ownerSend({ type: Msg.Kick, name: this.name }, msgT('移出 {name}', { name: this.name }));
      this.board.requestRefresh();
      return;
    }
    if (t.closest('[data-archive]')) {
      if (!this.archiveArmed) {
        this.archiveArmed = true;
        this.render();
        setTimeout(() => { if (this.archiveArmed) { this.archiveArmed = false; this.render(); } }, 4000);
        return;
      }
      this.archiveArmed = false;
      this.board.ownerSend({ type: Msg.AgentArchive, name: this.name }, msgT('归档 {name}', { name: this.name }));
      this.board.requestRefresh();
      return;
    }
    if (t.closest('[data-recall]')) {
      if (!this.recallArmed) { // 两段确认 (§4.2): arm, then commit on the second click
        this.recallArmed = true;
        this.render();
        setTimeout(() => { if (this.recallArmed) { this.recallArmed = false; this.render(); } }, 4000);
        return;
      }
      this.recallArmed = false;
      this.#recall();
      return;
    }
    if (t.closest('[data-steer-open]')) { this.steerOpen = true; this.render(); return; }
    if (t.closest('[data-steer-send]')) { this.#sendSteer(); return; }
    if (this.off.click(t, this.name)) return;
  }

  async #recall() {
    try {
      await postRecall({ name: this.name });
      this.board.toast.show(msgT('已召回 {name}，等待重新入座', { name: this.name }), 'ok');
      this.board.requestRefresh();
      this.reload(this.name);
    } catch (err) {
      this.board.toast.show(String(err.message || err), 'err');
    }
  }

  // 改档 rides the same receipt habit as steer: the 400 body is the
  // refusal reason, the room's system line carries the effect (the
  // switch itself may land at turn end — the dispatcher's matrix).
  // 模型与强度各自可单飞：模型留空＋非空强度＝默认链模型换强度
  //（ZCode 同口径）；两槽皆空＝清档回默认。
  async #setModel(model, reasoning = '') {
    const project = this.dispatch?.project || '';
    try {
      await postSetModel({ name: this.name, model, reasoning, project: project === 'default' ? '' : project });
      const label = (model || msgT('默认')) + (reasoning ? msgT('（思考 {lv}）', { lv: levelLabel(reasoning) }) : '');
      this.board.toast.show(msgT('已为 {name} 改档模型：{label}', { name: this.name, label }), 'ok');
      this.board.requestRefresh();
      this.reload(this.name);
    } catch (err) {
      this.board.toast.show(msgT('改档失败：{msg}', { msg: err.message }), 'err');
    }
  }

  async #sendSteer() {
    const box = this.side.querySelector('[data-steer-text]');
    const text = (box?.value ?? this.steerDraft ?? '').trim();
    if (!text) {
      this.board.toast.show(msgT('请先输入要注入的指令'), 'info');
      return;
    }
    const project = this.dispatch?.project || '';
    if (box) box.disabled = true;
    try {
      await postSteer({ name: this.name, text, project: project === 'default' ? '' : project });
      this.board.toast.show(msgT('已向 {name} 注入 steer 指令', { name: this.name }), 'ok');
      this.steerOpen = false;
      this.steerDraft = '';
      this.render();
    } catch (err) {
      this.board.toast.show(msgT('steer 失败：{msg}', { msg: err.message }), 'err');
      if (box) box.disabled = false;
    }
  }

  async #copyPrompt() {
    const ok = await copyToClipboard(this.data?.prompt || '');
    this.board.toast.show(ok ? msgT('接入提示词已复制') : msgT('复制失败——可展开后手动选择复制'), ok ? 'ok' : 'err');
  }
}

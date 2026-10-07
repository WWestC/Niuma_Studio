// staffing.js — the per-project staffing view (US-H5): consumes GET
// /p/{key}/staffing (v2 P4-c) — every row with the seat's presence and
// its EFFECTIVE pack list (profile ⊕ override, resolved server-side).
// The endpoint is a live dependency this board refuses to wait on: an
// embed without the face (nil stores) answers 404, and until then the
// roster (/kb/people) renders as the placeholder with the reason said
// out loud — the probe reruns on every reload, so the table lights up
// the moment the face exists.
//
// v2.4 adds the 编制表管理卡 above the staffing rows — the
// establishment table became a UI-configurable surface (moved into
// 牛马管理): rows add/edit/delete through POST …/establishment/row
// under the frozen contract, the system posts (编排者 / HR / 小助手,
// 必须=是) render locked and grey — they are code-governed and the
// server refuses their mutation anyway. v2.5 的分项目编制：每个项目
// 的表随开张自动带齐三行系统岗，编排者/HR 由招聘循环自动出生到岗
//（每项目各一套——不同项目的 HR 互相独立，在本项目出生的人才算
// 数），小助手是全工作室唯一的一个（同一行出现在每张表里，背后是
// 同一个小助手会话）。空缺的系统岗留「招牛马」手动备胎（招聘受阻
// 时的出路）；缺行喂给招牛马表单（同 recruit 表单的 身份 预填）。

import {
  getStaffing, getPeople, getEstablishment, getProjectEstablishment,
  setAutoRecall, postEstablishmentRow,
} from '../wire/api.js';
import { LobbyKey } from '../wire/wire.js';
import { esc, icon, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { rankLabel, personState, STATE_LABEL } from '../ui/person.js';

// the staffing table's own seat states (distinct from the roster's
// person online/offline buckets)
const SEAT_LABEL = {
  onboard: [t('入编中'), 'st-pending'], active: [t('在岗'), 'st-doing'], offboard: [t('已离编'), 'st-done'],
};

// the 小助手 row's presence cell — the advisor holds no seat; its life
// rides the dispatch bridge (see kb.AdvisorPost).
const ADVISOR_KEY = 'assistant';

export class StaffingView {
  /** @param {HTMLElement} pane @param {import('./people.js').PeopleBoard} board */
  constructor(pane, board) {
    this.pane = pane;
    this.board = board;
    this.project = 'default';
    this.state = 'loading'; // loading | error | ready | fallback
    this.est = null;        // the establishment face ({rows, rev, parse_error, auto_recall?}) — null = face off
    this.confirmDel = '';   // the two-step delete's armed row key
    this.confirmTimer = 0;
    this.editor = null;     // the row editor overlay (kept for 409 retry)
    pane.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-retry]')) { this.reload(); return; }
      // 分支 chip 只导航（聊天/面板侧只导航、动作落在面板的纪律）：
      // 去项目管理的「版本管理」页签管理（title 里已指路）。
      if (ev.target.closest('[data-branch-jump]')) {
        location.hash = '#/project';
        return;
      }
      if (ev.target.closest('[data-est-add]')) { this.#openEditor('add'); return; }
      if (ev.target.closest('[data-est-edit]')) {
        const key = ev.target.closest('[data-key]')?.dataset.key;
        const row = this.est?.rows?.find((r) => r.key === key);
        if (row) this.#openEditor('update', row);
        return;
      }
      if (ev.target.closest('[data-est-del]')) {
        const key = ev.target.closest('[data-key]')?.dataset.key;
        if (key) this.#deleteRow(key);
        return;
      }
      if (ev.target.closest('[data-est-hire]')) {
        const key = ev.target.closest('[data-key]')?.dataset.key;
        const row = this.est?.rows?.find((r) => r.key === key);
        if (row) this.board.openRecruit({ prefRole: row.role, prefProject: this.project });
        return;
      }
    });
    pane.addEventListener('change', (ev) => {
      if (ev.target.matches('[data-project]')) {
        this.project = ev.target.value;
        this.reload();
        return;
      }
      // 自动补员项目总闸（P5-a）：POST 落盘后看护下一拍即按新值行事，
      // 无需重启。以服务端回执为准渲染；失败回滚开关并把原因说出口。
      if (ev.target.matches('[data-autorecall]')) {
        const on = ev.target.checked;
        setAutoRecall(this.project, on)
          .then((r) => {
            if (this.est) this.est.auto_recall = r.auto_recall;
            this.board.toast.show(r.auto_recall
              ? t('「{p}」自动补员已开启——看护下一拍生效', { p: this.project })
              : t('「{p}」自动补员已关闭——看护下一拍生效', { p: this.project }), 'ok');
          })
          .catch((err) => {
            ev.target.checked = !on;
            this.board.toast.show(t('自动补员切换失败：{msg}', { msg: err?.message || err }), 'err');
          });
      }
    });
  }

  async reload() {
    // 项目落位：选项里没有的项目键（首开的 'default' 残值、已收摊的
    // 项目）回落大厅——编制面永远有一张可读的表，聊天「加编制」的
    // 跳转才有落点
    const keys = this.board.projectChoices().map((c) => c.key);
    if (!keys.includes(this.project)) this.project = LobbyKey;
    this.state = 'loading';
    this.est = null;
    this.#disarmDel();
    this.render();
    try {
      this.report = await getStaffing(this.project);
      this.state = 'ready';
      // 编制表面与 staffing 同源：大厅读 /kb/establishment（无开关），
      // 项目读 /p/{key}/establishment（带 auto_recall）；404（非 active
      // 项目）= 无面，编制表卡随 staffing 一起退场。
      this.est = this.project === LobbyKey
        ? await getEstablishment().catch(() => null)
        : await getProjectEstablishment(this.project).catch(() => null);
    } catch (err) {
      if (err.status === 404) {
        try {
          this.people = await getPeople();
          this.state = 'fallback';
        } catch (e2) {
          this.state = 'error';
          this.error = e2;
        }
      } else {
        this.state = 'error';
        this.error = err;
      }
    }
    this.render();
  }

  render() {
    const choices = this.board.projectChoices();
    // 自动补员总闸（仅编制面可用的项目有它——服务端 404 即无开关）：
    // 两级门的项目级一档（岗级「自动补员」列仍在其上）。
    const estSwitch = this.est && this.est.auto_recall !== undefined
      ? `<label class="field-inline est-switch" title="${t('看护发现缺岗时是否自动召回补位（项目总闸）。岗级「自动补员」列仍在其上——两级门同时打开才补员。改动即刻生效，无需重启。')}">` +
        `<input type="checkbox" data-autorecall${this.est.auto_recall ? ' checked' : ''}><span>${t('自动补员')}</span></label>`
      : '';
    const head =
      `<label class="field-inline"><span>${t('项目')}</span>` +
        `<select class="input" data-project>${choices.map((c) =>
          `<option value="${esc(c.key)}"${c.key === this.project ? ' selected' : ''}>${esc(c.name)}（${esc(c.key)}）</option>`).join('')}</select>` +
      '</label>' + estSwitch;
    if (this.state === 'loading') {
      this.pane.innerHTML = head + '<div class="staffing skeleton"></div>';
      return;
    }
    if (this.state === 'error') {
      this.pane.innerHTML = head +
        `<div class="state-card error"><strong>${t('编制读取失败')}</strong>
         <span>${esc(this.error?.message || String(this.error))}</span>
         <button type="button" class="btn" data-retry>${t('重试')}</button></div>`;
      return;
    }
    if (this.state === 'fallback') {
      this.pane.innerHTML = head +
        `<div class="est-bar broken">${t('GET /p/{key}/staffing 不可用（仅 active 项目有编制面，或该面未启用）——暂以全房名册占位', { key: esc(this.project) })}</div>` +
        this.#rosterHTML();
      return;
    }
    const rows = this.report?.rows || [];
    const estCard = this.est ? this.#estCardHTML() : '';
    if (!rows.length && !(this.est?.rows?.length)) {
      this.pane.innerHTML = head + estCard +
        `<div class="state-card empty"><strong>${t('该项目暂无编制行')}</strong><span>${t('在上方编制表加岗，或出生新成员（birth）后出现在此')}</span></div>`;
      return;
    }
    this.pane.innerHTML = head + estCard + '<div class="staffing">' +
      `<div class="srow shead"><span>${t('成员')}</span><span>${t('岗位')}</span><span>${t('状态')}</span><span>${t('会话')}</span><span>${t('有效装配')}</span></div>` +
      rows.map((r) => {
        // 档案已归档（已离职）的行：服务端已把状态折成 offboard，这里
        // 把标签直接说成「已离职」——比「已离编」更准（全离，非转岗）。
        const [label, cls] = r.archived
          ? [t('已离职'), 'st-done']
          : (SEAT_LABEL[r.state] || [r.state, 'st-todo']);
        const seat = r.seated ? t('在座')
          : r.session_id ? t('离座（会话在档）') : t('未绑定');
        const skills = (r.skills || []).map((p) =>
          `<span class="chip">${esc(p.name || p.key)}</span>`).join('');
        // 座位模型 chip：显示 ref 的模型段（providerId/modelId 取后半）
        const model = r.model
          ? `<span class="chip">${t('模型：{m}', { m: esc(r.model.split('/').pop()) })}</span>` : '';
        // 座位分支 chip（v2.7.1 分支隔离）：专属工作树驻的分支，点击跳
        // 版本管理页签的成员分支小节。
        const branch = r.branch
          ? `<span class="chip" data-branch-jump="1" title="${t('专属工作树分支——项目管理 → 版本管理里管理')}">${icon('branch', 11)} ${esc(r.branch)}</span>` : '';
        return `<div class="srow">
          <span class="sname">${esc(r.person)}</span>
          <span>${esc(r.project_role || '—')}</span>
          <span class="tag ${cls}">${label}</span>
          <span class="dim small">${seat}</span>
          <span class="packs-cell">${skills}${model}${branch || (skills || model ? '' : `<span class="dim small">${t('继承档案（无装配）')}</span>`)}</span>
        </div>`;
      }).join('') + '</div>';
  }

  // --- the 编制表管理卡 ------------------------------------------------------

  #estCardHTML() {
    const est = this.est;
    if (est.parse_error) {
      return `<div class="est-card"><div class="est-card-head"><strong>${t('编制表 · 岗位编制')}</strong>${icon('lock')}</div>` +
        `<div class="est-bar broken">${esc(est.parse_error)}${t('——修复前不可编辑（可 CLI kb write {p}/establishment 重建）', { p: this.project === LobbyKey ? 'ops' : 'p/' + esc(this.project) })}</div></div>`;
    }
    const rows = est.rows || [];
    const body = rows.length
      ? '<div class="est-table">' +
        `<div class="erow ehead"><span>${t('岗位')}</span><span>${t('身份')}</span><span>${t('编制')}</span><span>${t('在岗')}</span><span>${t('自动补员')}</span><span>${t('必须')}</span><span>${t('操作')}</span></div>` +
        rows.map((r) => this.#estRowHTML(r)).join('') + '</div>'
      : `<div class="est-empty dim">${t('该表还没有岗位行——加第一个岗位（招牛马请找 HR 或用「出生新成员」）')}</div>`;
    // 系统岗学说分范围：大厅三岗由启动招聘自动到岗；项目的系统岗随开张
    // 自动登记并自动招聘到岗（编排者/HR 每个项目各一套、互相独立——各
    // 自出生各自的人，大厅的小牛/小马不算任何项目的在岗），小助手全工
    // 作室唯一（同一行在每张表里，背后是同一个小助手）。
    const sysHint = this.project === LobbyKey
      ? t('岗位由房主在此配置；带「必须」的三行系统岗（编排者/HR/小助手）由系统自动招募维护，锁定不可改')
      : t('岗位由房主在此配置；带「必须」的系统岗随开张自动登记并自动招聘到岗、锁定不可改——编排者/HR 每个项目各一套（不同项目的 HR 互相独立），小助手全工作室唯一');
    return `<div class="est-card">` +
      `<div class="est-card-head"><strong>${t('编制表 · 岗位编制')}</strong>${icon('lock')}` +
        `<span class="dim small est-hint">${esc(sysHint)}</span>` +
        `<span class="est-head-actions"><button type="button" class="btn small gold" data-est-add>` + icon('plus', 13) + `${t('加岗位')}</button></span></div>` +
      body + '</div>';
  }

  #estRowHTML(r) {
    const system = r.system || r.must;
    const present = r.key === ADVISOR_KEY
      ? `<span class="dim small">${t('全工作室唯一 · 随调度常驻')}</span>`
      : (r.present?.length
        ? `<span class="dim small">${esc(r.present.join(t('、')))}</span>`
        : `<span class="tag st-todo">${t('缺 {n}', { n: r.missing })}</span>`);
    const must = r.must
      ? `<span class="chip must">${t('是')}</span>`
      : `<span class="dim small">${t('否')}</span>`;
    const fill = r.auto_fill ? t('是') : t('否');
    // 空缺的系统岗也给「招牛马」快捷入口（小助手除外——它无座位，随
    // 调度常驻）：系统岗锁定的是「改/删」，出生由招聘循环自动进行，这
    // 个入口是招聘受阻时的手动备胎；预填该岗身份，出生落在本项目。
    const hireBtn = (title) => r.key !== ADVISOR_KEY && r.missing > 0
      ? `<button type="button" class="btn small" data-est-hire title="${title}">${t('招牛马')}</button>`
      : '';
    if (system) {
      return `<div class="erow locked" data-key="${esc(r.key)}" title="${t('系统岗（必须）：由系统自动招募维护，锁定不可改——Niuma_Studio 随启动招聘，项目随开张自动招聘')}">` +
        `<span class="ename">${esc(r.name)}<span class="dim small ekey">${esc(r.key)}</span></span>` +
        `<span>${esc(r.role)}</span>` +
        `<span>${r.headcount}</span>` +
        `<span>${present}</span>` +
        `<span class="dim small">${fill}</span>` +
        `<span>${must}</span>` +
        `<span class="erow-acts">${hireBtn(t('按此系统岗身份预填出生表单，到岗本项目'))}${icon('lock', 13)}<span class="dim small">${t('系统岗')}</span></span>` +
      '</div>';
    }
    const hire = hireBtn(t('按此岗位身份预填出生表单'));
    const del = this.confirmDel === r.key
      ? `<button type="button" class="btn small danger" data-est-del>${t('确认删除')}</button>`
      : `<button type="button" class="btn small" data-est-del title="${t('删除此岗位行')}">${t('删除')}</button>`;
    return `<div class="erow" data-key="${esc(r.key)}">` +
      `<span class="ename">${esc(r.name)}<span class="dim small ekey">${esc(r.key)}</span></span>` +
      `<span>${esc(r.role)}</span>` +
      `<span>${r.headcount}</span>` +
      `<span>${present}</span>` +
      `<span class="dim small">${fill}</span>` +
      `<span>${must}</span>` +
      `<span class="erow-acts"><button type="button" class="btn small" data-est-edit>${t('编辑')}</button>${del}${hire}</span>` +
    '</div>';
  }

  /** 聊天「加编制」的一键落点：目标项目选对（大厅建议可带项目键），
   *  rev 面后台刷新，加岗浮层按建议预填并聚焦确认钮——房主只确认一次。
   *  @param {string|null} project @param {Object} prefill */
  prefillAddRow(project, prefill) {
    if (project && project !== this.project &&
      this.board.projectChoices().some((c) => c.key === project)) {
      this.project = project;
    }
    this.reload(); // expect_rev 以服务端为准（后台刷新，不挡浮层）
    this.#openEditor('add', null, prefill);
  }

  #openEditor(mode, row = null, prefill = null) {
    this.#closeEditor();
    const isAdd = mode === 'add';
    const src = isAdd ? (prefill || {}) : (row || {}); // 加岗可带聊天建议预填
    const choice = this.board.projectChoices().find((c) => c.key === this.project);
    const target = `${t('写入：')}${choice ? choice.name : this.project}${this.project === LobbyKey ? t('（Niuma_Studio 编制表）') : ''}`;
    const el = document.createElement('div');
    el.className = 'overlay';
    el.innerHTML =
      `<div class="overlay-card" role="dialog" aria-modal="true" aria-label="${t('编制表岗位编辑')}">` +
        `<div class="overlay-head"><strong>${isAdd ? t('加岗位') : t('编辑岗位 · {name}', { name: esc(row?.name || '') })}</strong>` +
          `<span class="dim small" title="${t('编制表行写入的目标项目（面板上方的项目选择器可换）')}">${esc(target)}</span>` +
          `<button type="button" class="icon-btn" data-close title="${t('关闭')}">` + icon('close') + '</button></div>' +
        `<label class="field"><span>${t('岗位 key')}<i class="req">*</i><i>${t('行的身份，建后不可改；小写字母/数字/- 为佳')}</i></span>` +
          `<input class="input" data-f-key maxlength="32" placeholder="${t('如 developer')}" ${isAdd ? '' : 'readonly'} value="${esc(src.key || '')}"></label>` +
        `<label class="field"><span>${t('名称')}<i class="req">*</i><i>${t('岗位显示名，如 开发')}</i></span>` +
          `<input class="input" data-f-name maxlength="32" placeholder="${t('岗位名称')}" value="${esc(src.name || '')}"></label>` +
        `<label class="field"><span>${t('身份')}<i class="req">*</i><i>${t('一句话身份；须与出生 --role 一字不差（看护按精确文本对账）')}</i></span>` +
          `<input class="input" data-f-role maxlength="32" placeholder="${t('如 前端开发')}" value="${esc(src.role || '')}"></label>` +
        '<div class="field-row">' +
          `<label class="field"><span>${t('编制')}<i class="req">*</i><i>1–50</i></span>` +
            `<input class="input" data-f-count type="number" min="1" max="50" value="${src.headcount || 1}"></label>` +
          `<label class="field"><span>${t('自动补员')}</span>` +
            `<select class="input" data-f-fill><option value="no"${!src.auto_fill ? ' selected' : ''}>${t('否（不看护缺岗）')}</option><option value="yes"${src.auto_fill ? ' selected' : ''}>${t('是（缺岗自动召回）')}</option></select></label>` +
        '</div>' +
        `<label class="field"><span>${t('手册')}<i>${t('岗位手册的文档地址，可留空（HR 会起草）')}</i></span>` +
          `<input class="input" data-f-manual maxlength="64" placeholder="roles/developer" value="${esc(src.manual || '')}"></label>` +
        (prefill
          ? `<div class="est-editor-note dim small">${t('已按 {who} 在对话里的建议预填——核对无误点「加入编制表」即可。', { who: esc(prefill.from || t('成员')) })}</div>`
          : '') +
        `<div class="est-editor-note dim small">${t('「必须」列为系统岗专属（编排者/HR/小助手——编排者/HR 每个项目各一套、随开张自动招聘到岗，Niuma_Studio 的随启动招聘，小助手全工作室唯一），自建岗位固定为否。看护提醒只覆盖「必须」岗与「自动补员=是」的岗——普通自建岗缺岗不报警，先定编后招牛马即可。加岗后招牛马：找 HR，或用「出生新成员」（在岗列的「招牛马」快捷入口）。')}</div>` +
        '<div class="overlay-status" data-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-cancel>${t('取消')}</button>` +
          `<button type="button" class="btn gold" data-submit>${isAdd ? t('加入编制表') : t('保存')}</button>` +
        '</div>' +
      '</div>';
    el.addEventListener('click', (ev) => {
      if (ev.target === el) this.#closeEditor();
      if (ev.target.closest('[data-close],[data-cancel]')) this.#closeEditor();
      if (ev.target.closest('[data-submit]')) this.#submitEditor(isAdd);
    });
    document.body.appendChild(el);
    this.editor = el;
    const first = el.querySelector(isAdd ? '[data-f-key]' : '[data-f-name]');
    if (isAdd && prefill?.key) {
      el.querySelector('[data-submit]')?.focus(); // 一键确认流：值已齐，Enter 即入表
    } else {
      first.focus();
      if (!isAdd) first.select();
    }
  }

  #closeEditor() {
    dismiss(this.editor);
    this.editor = null;
  }

  #editorStatus(text, err = false) {
    const el = this.editor?.querySelector('[data-status]');
    if (!el) return;
    el.textContent = text || '';
    el.classList.toggle('err', !!err);
  }

  async #submitEditor(isAdd) {
    const el = this.editor;
    if (!el) return;
    const key = el.querySelector('[data-f-key]').value.trim();
    const name = el.querySelector('[data-f-name]').value.trim();
    const role = el.querySelector('[data-f-role]').value.trim();
    const count = parseInt(el.querySelector('[data-f-count]').value, 10);
    const fill = el.querySelector('[data-f-fill]').value === 'yes';
    const manual = el.querySelector('[data-f-manual]').value.trim();
    if (!key || !name || !role || !Number.isInteger(count)) {
      this.#editorStatus(t('岗位 key / 名称 / 身份 必填，编制须为整数'), true);
      return;
    }
    const btn = el.querySelector('[data-submit]');
    btn.disabled = true;
    btn.classList.add('loading');
    try {
      const report = await postEstablishmentRow(this.project, isAdd
        ? { op: 'add', row: { key, name, role, headcount: count, auto_fill: fill, manual }, expect_rev: this.est?.rev || 0 }
        : { op: 'update', key, row: { key, name, role, headcount: count, auto_fill: fill, manual }, expect_rev: this.est?.rev || 0 });
      // 渲染以服务端回执为准（rows+rev 一起换新），不吃自己的回声
      this.est = this.project === LobbyKey
        ? report
        : { ...report, auto_recall: this.est?.auto_recall };
      this.#closeEditor();
      this.render();
      this.board.toast.show(t('编制表已更新：{name}（{key}）', { name, key }), 'ok');
      this.board.requestRefresh();
    } catch (err) {
      btn.disabled = false;
      btn.classList.remove('loading');
      if (err.status === 409) {
        // 表在眼皮底下被别人改了：重读最新 rev，表单留着原值重试
        this.#editorStatus(t('编制表已被他人修改，已刷新到最新版本——请核对后重试'), true);
        await this.#refreshEstOnly();
        this.render();
      } else {
        this.#editorStatus(t('保存失败：{msg}', { msg: err.message }), true);
      }
    }
  }

  async #deleteRow(key) {
    // 两步确认：第一次点亮「确认删除」，3 秒内再点才真删
    if (this.confirmDel !== key) {
      this.confirmDel = key;
      this.render();
      clearTimeout(this.confirmTimer);
      this.confirmTimer = setTimeout(() => this.#disarmDel(), 3000);
      return;
    }
    this.#disarmDel();
    try {
      const report = await postEstablishmentRow(this.project,
        { op: 'delete', key, expect_rev: this.est?.rev || 0 });
      this.est = this.project === LobbyKey
        ? report
        : { ...report, auto_recall: this.est?.auto_recall };
      this.render();
      this.board.toast.show(t('编制表已删除岗位行：{key}', { key }), 'ok');
      this.board.requestRefresh();
    } catch (err) {
      if (err.status === 409) {
        await this.#refreshEstOnly();
        this.render();
        this.board.toast.show(t('编制表已被他人修改，已刷新——请重试删除'), 'err');
      } else {
        this.board.toast.show(t('删除失败：{msg}', { msg: err.message }), 'err');
      }
    }
  }

  #disarmDel() {
    clearTimeout(this.confirmTimer);
    if (!this.confirmDel) return;
    this.confirmDel = '';
    if (this.state === 'ready') this.render();
  }

  // 只换编制面数据（保持 staffing 报告与当前 tab 不动）——409 重试路径用
  async #refreshEstOnly() {
    try {
      this.est = this.project === LobbyKey
        ? await getEstablishment()
        : await getProjectEstablishment(this.project);
    } catch { /* 保持旧值，重试自然再撞 */ }
  }

  #rosterHTML() {
    if (!this.people?.length) {
      return `<div class="state-card empty"><strong>${t('全房名册为空')}</strong><span>${t('先出生第一位成员')}</span></div>`;
    }
    return '<div class="staffing">' +
      `<div class="srow shead"><span>${t('成员')}</span><span>${t('身份')}</span><span>${t('状态')}</span><span>${t('等级')}</span><span>${t('在办')}</span></div>` +
      this.people.map((p) => `<div class="srow">
        <span class="sname">${esc(p.name)}</span>
        <span>${esc(p.role || '—')}</span>
        <span class="tag ${p.online ? (p.grace ? 'st-pending' : 'st-doing') : 'st-done'}">${STATE_LABEL[personState(p)]}</span>
        <span>${rankLabel(p)}</span>
        <span>${p.task_doing || 0}</span>
      </div>`).join('') + '</div>';
  }
}

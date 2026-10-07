// tasknew.js — the 新建任务 overlay (v3, 飞书建卡式): the dialog opens
// headerless — the hero title well IS the header (borderless input the
// moment the card lands), description below in the same quiet voice,
// then the meta as 飞书字段行 (icon + name + value: 负责人 / 时间 /
// 优先级 / 所属), and 取消·创建 bottom-right where 创建 progressively
// lights up once the title exists. title/desc/assignee plus the
// scheduling pair ride one task_create frame over the viewed project's
// owner face; priority and the schedule ride the create→patch chain
// (project.js's acceptTaskReceipt merges {project,priority,start,end}
// into ONE task_update on the create receipt). The assignee picker is
// the room roster, not free text — same-rank nominations still
// negotiate server-side.

import { esc, icon, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { PRIORITY_META } from './taskmeta.js';

export class TaskNewForm {
  /**
   * @param {Object} opts
   * @param {string} opts.projectName the viewed project's display name
   * @param {string[]} [opts.roster] candidate assignee names
   * @param {(n: string) => string} [opts.tagOf] departure annotator
   *   (board.departTag — 离座 lingering 的已离职成员选项注明去留)
   * @param {(draft: {title: string, desc: string, assignee: string, priority: string, start: string, end: string}) => void} opts.onSubmit
   */
  constructor(opts) {
    this.opts = opts;
    const roster = opts.roster || [];
    const tagOf = opts.tagOf || (() => '');
    const el = document.createElement('div');
    el.className = 'overlay';
    el.innerHTML = `<div class="overlay-card tn-card" role="dialog" aria-label="${t('新建任务')}">
      <div class="overlay-head">
        <button type="button" class="icon-btn" data-close title="${t('关闭')}">${icon('close', 14)}</button></div>
      <input class="tn-title" data-title maxlength="120" placeholder="${t('要做成什么（一句话）')}" aria-label="${t('任务标题')}">
      <textarea class="tn-desc" data-desc rows="2" maxlength="2000"
        placeholder="${t('添加任务描述——背景 / 验收标准（可空）')}" aria-label="${t('任务描述')}"></textarea>
      <div class="tn-meta">
        <label class="tn-row">
          <span class="tn-k">${icon('person', 15)}<b>${t('负责人')}</b></span>
          <select class="tn-ctl" data-assignee aria-label="${t('负责人（可空为任务池）')}">
            <option value="">${t('任务池（待认领）')}</option>
            ${roster.map((n) => {
              const tag = tagOf(n);
              return `<option value="${esc(n)}">${esc(n)}${tag ? t('（{tag}）', { tag }) : ''}</option>`;
            }).join('')}
          </select>
        </label>
        <div class="tn-row">
          <span class="tn-k">${icon('calendar', 15)}<b>${t('时间')}</b></span>
          <div class="tn-when">
            <input class="tn-date" type="datetime-local" data-start aria-label="${t('开始时间（可空）')}">
            <i class="tn-sep">—</i>
            <input class="tn-date" type="datetime-local" data-end aria-label="${t('截止时间（可空）')}">
          </div>
        </div>
        <label class="tn-row">
          <span class="tn-k">${icon('flag', 15)}<b>${t('优先级')}</b></span>
          <select class="tn-ctl" data-priority aria-label="${t('优先级（可空为未设置）')}">
            <option value="">${t('未设置')}</option>
            ${Object.entries(PRIORITY_META).map(([k, m]) => `<option value="${k}">${m.label}</option>`).join('')}
          </select>
        </label>
        <div class="tn-row tn-own">
          <span class="tn-k">${icon('folder', 15)}<b>${t('所属')}</b></span>
          <span class="tn-own-name">${esc(opts.projectName)}</span>
        </div>
      </div>
      <div class="overlay-status" data-status></div>
      <div class="overlay-actions">
        <span class="tn-hint" data-hint></span>
        <button type="button" class="btn" data-cancel>${t('取消')}</button>
        <button type="button" class="btn gold" data-ok disabled>${t('创建')}</button>
      </div>
    </div>`;
    document.body.appendChild(el);
    this.el = el;
    const title = el.querySelector('[data-title]');
    const ok = el.querySelector('[data-ok]');
    // 创建键随标题渐进点亮（飞书同款）：空标题不设防地禁掉，提交口的
    // 校验只兜 Cmd+Enter 与纯空白
    this.#syncOk = () => { ok.disabled = !title.value.trim(); };
    title.addEventListener('input', this.#syncOk);
    this.#syncOk();
    const mac = typeof navigator !== 'undefined' && /Mac/i.test(String(navigator.platform || ''));
    el.querySelector('[data-hint]').textContent = `${mac ? '⌘' : 'Ctrl+'}↵ ${t('直接创建')}`;
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      if (ev.target.closest('[data-close]') || ev.target.closest('[data-cancel]')) this.close();
      if (ev.target.closest('[data-ok]')) this.#submit();
    });
    el.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) this.#submit();
    });
    document.addEventListener('keydown', this.onDocKey);
    title.focus();
  }

  #syncOk;

  #submit() {
    const el = this.el;
    const title = el.querySelector('[data-title]').value.trim();
    const status = el.querySelector('[data-status]');
    if (!title) {
      status.textContent = t('标题必填');
      status.classList.add('err');
      return;
    }
    const draft = {
      title,
      desc: el.querySelector('[data-desc]').value.trim(),
      assignee: el.querySelector('[data-assignee]').value,
      priority: el.querySelector('[data-priority]').value,
      start: el.querySelector('[data-start]').value.replace('T', ' '),
      end: el.querySelector('[data-end]').value.replace('T', ' '),
    };
    if (draft.start && draft.end && draft.start > draft.end) {
      status.textContent = t('开始晚于截止——调一下再创建');
      status.classList.add('err');
      return;
    }
    this.opts.onSubmit(draft);
    this.close();
  }

  close() {
    if (!this.el) return;
    dismiss(this.el);
    this.el = null;
    document.removeEventListener('keydown', this.onDocKey);
  }
}

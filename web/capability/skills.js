// skills.js — the skill-library management page (people board's
// 「技能库」tab): the library list (GET /skills — summaries, bodies
// stay behind the detail read) beside one editor (key/名称/描述/
// 正文/手册引用) saved over POST /skills/{key} — the host's HTTP
// write face, the loopback trust model's local human. A 400's body is
// the refusal (validation lives in Go), shown in place. Deleting
// rides DELETE (two-step confirm, offboard's armed pattern) and the
// receipt names the assemblies that still reference it. A background
// refresh (any room event pokes requestRefresh, and a save's own
// receipt broadcasts back) repaints the list and never the editor:
// rebuilds belong to user actions only, so a broadcast can neither
// lose typed input nor race away a just-loaded detail — and an armed
// delete can't be visually reset while still primed.

import { getSkills, getSkill, postSkill, deleteSkill } from '../wire/api.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

const KEY_RE = /^[a-z0-9_-]{1,32}$/;
const MAX_MANUALS = 4;

export class SkillLibraryView {
  /** @param {HTMLElement} pane @param {import('../people/people.js').PeopleBoard} board */
  constructor(pane, board) {
    this.pane = pane;
    this.board = board;
    this.state = 'loading'; // loading | error | ready
    this.skills = [];       // library rows
    this.editing = null;    // null = blank new-skill form; string = its key
    this.saving = false;
    this.confirmDelete = false; // the delete button's armed state
    pane.addEventListener('click', (ev) => this.#onClick(ev));
  }

  async reload() {
    if (this.state !== 'ready') { this.state = 'loading'; this.render(); }
    try {
      this.skills = await getSkills();
      this.state = 'ready';
      // the edited key may have vanished (rename-by-recreate is
      // possible) — the editor stays as-is regardless
    } catch (err) {
      this.state = 'error';
      this.error = err;
    }
    // With the shell up, a refresh is list-only: the editor's contents
    // are owned by user actions (new/select/reset/save/delete), never
    // by the refresh cycle.
    const shell = this.pane.querySelector('.packs');
    const list = this.pane.querySelector('.pack-list');
    if (this.state === 'ready' && shell && list) {
      list.innerHTML = this.#listHTML();
      return;
    }
    this.render();
  }

  #onClick(ev) {
    const tg = ev.target;
    if (tg.closest('[data-retry]')) { this.reload(); return; }
    if (tg.closest('[data-new]')) { this.editing = null; this.render(); return; }
    if (tg.closest('[data-del]')) { this.#delete(); return; }
    const row = tg.closest('[data-skill]');
    if (row && row.dataset.skill !== this.editing) {
      this.editing = row.dataset.skill;
      this.render();
      this.#loadDetail();
      return;
    }
    if (tg.closest('[data-save]')) this.#save();
    if (tg.closest('[data-reset]')) { this.render(); this.#loadDetail(); }
  }

  async #delete() {
    if (this.editing === null) return;
    const btn = this.pane.querySelector('[data-del]');
    if (!this.confirmDelete) { // two-step confirm: arm, commit on the second click
      this.confirmDelete = true;
      if (btn) {
        btn.classList.add('armed');
        btn.textContent = t('再点一次确认删除（装配引用将失效）');
        setTimeout(() => {
          if (!this.confirmDelete) return;
          this.confirmDelete = false;
          const b = this.pane.querySelector('[data-del]');
          if (b) { b.classList.remove('armed'); b.textContent = t('删除技能'); }
        }, 4000);
      }
      return;
    }
    if (btn) { btn.disabled = true; btn.textContent = t('删除中…'); }
    try {
      const out = await deleteSkill(this.editing);
      const extra = (out.referenced_by || []).length
        ? t('（{who} 的装配已失效）', { who: out.referenced_by.join(t('、')) }) : '';
      this.board.toast.show(t('已删除技能 {key}{extra}', { key: this.editing, extra }), 'ok');
      this.confirmDelete = false;
      this.editing = null;
      await this.reload();
      this.render(); // the shell stays up after reload — rebuild the editor to the blank form
    } catch (err) {
      this.confirmDelete = false;
      if (btn) { btn.disabled = false; btn.classList.remove('armed'); btn.textContent = t('删除技能'); }
      this.#formError(t('删除被拒：{msg}', { msg: err.message }));
    }
  }

  async #loadDetail() {
    const form = this.pane.querySelector('[data-form]');
    if (!form || this.editing === null) return;
    form.classList.add('dim');
    try {
      const skill = await getSkill(this.editing);
      if (!this.pane.querySelector('[data-form]') || this.editing !== skill.key) return;
      this.#fillForm(skill);
    } catch (err) {
      this.#formError(t('读取失败：{msg}', { msg: err.message }));
    } finally {
      form.classList.remove('dim');
    }
  }

  render() {
    this.confirmDelete = false;
    if (this.state === 'loading') {
      this.pane.innerHTML = '<div class="packs skeleton"></div>';
      return;
    }
    if (this.state === 'error') {
      this.pane.innerHTML =
        `<div class="state-card error"><strong>${esc(t('技能库读取失败'))}</strong>
         <span>${esc(this.error?.message || String(this.error))}</span>
         <button type="button" class="btn" data-retry>${esc(t('重试'))}</button></div>`;
      return;
    }
    this.pane.innerHTML =
      `<div class="packs">
        <div class="pack-list">${this.#listHTML()}</div>
        <div class="pack-editor" data-form>${this.#formHTML()}</div>
      </div>`;
  }

  #listHTML() {
    if (!this.skills.length) {
      return `<div class="state-card empty"><strong>${esc(t('技能库还是空的'))}</strong><span>${esc(t('技能是跟着人走的跨项目本事——建第一个（或直接在房里 /skill new）'))}</span></div>` +
        `<button type="button" class="btn gold" data-new>${esc(t('+ 新建技能'))}</button>`;
    }
    const rows = this.skills.map((s) => {
      const marks = [
        s.body_bytes > 0 ? '文' : '', (s.manuals || []).length ? '册' : '',
      ].filter(Boolean).map((g) => `<i title="${esc(t(MARK_TITLE[g]))}">${esc(t(g))}</i>`).join('');
      const tip = s.desc ? ` title="${esc(s.desc)}"` : '';
      return `<button type="button" class="pack-row${s.key === this.editing ? ' active' : ''}" data-skill="${esc(s.key)}"${tip}>
        <span class="pack-key">${esc(s.key)}</span>
        <span class="pack-name">${esc(s.name)}</span>
        <span class="pack-slots">${marks || `<i class="dim">${esc(t('空'))}</i>`}</span>
      </button>`;
    }).join('');
    return `<div class="pack-list-head">${esc(t('技能库 · {n} 个', { n: this.skills.length }))}
      <button type="button" class="btn small gold" data-new>${esc(t('+ 新建'))}</button></div>` + rows;
  }

  #formHTML() {
    const isNew = this.editing === null;
    const title = isNew ? t('新建技能') : t('编辑 {key}', { key: this.editing });
    const del = isNew ? '' :
      `<button type="button" class="btn small danger" data-del>${esc(t('删除技能'))}</button>`;
    return `<div class="form-head"><strong>${esc(title)}</strong>
        <span class="form-head-actions">${del}
        <button type="button" class="btn small" data-reset>${esc(t('还原'))}</button></span></div>
      <div class="prov" data-prov hidden></div>
      <label class="field"><span>key<i class="req">*</i>${isNew ? '' : esc(t('（创建后不可改名）'))}</span>
        <input class="input" data-key maxlength="32" value="${esc(isNew ? '' : this.editing)}" ${isNew ? 'placeholder="[a-z0-9_-]{1,32}"' : 'disabled'}></label>
      <label class="field"><span>${esc(t('名称'))}<i class="req">*</i></span>
        <input class="input" data-name maxlength="48" placeholder="${esc(t('显示名，≤48 字'))}"></label>
      <label class="field"><span>${esc(t('描述'))}</span>
        <input class="input" data-desc maxlength="2000" placeholder="${esc(t('一句话说明（可空）'))}"></label>
      <label class="field"><span>${esc(t('正文（指令面料）'))}</span>
        <textarea class="input mono" data-body rows="8" placeholder="${esc(t('注入给成员的技能正文（≤16KB）——消息里 /<key> 附着或装配后随面料注入'))}"></textarea></label>
      <label class="field"><span>${esc(t('手册引用（每行一个 kb key，≤4）'))}</span>
        <textarea class="input mono" data-manuals rows="3" placeholder="roles/reviewer"></textarea></label>
      <div class="overlay-status" data-status></div>
      <div class="overlay-actions">
        <button type="button" class="btn gold" data-save ${this.saving ? 'disabled' : ''}>${this.saving ? esc(t('保存中…')) : esc(t('保存技能'))}</button>
      </div>`;
  }

  #fillForm(skill) {
    const q = (sel) => this.pane.querySelector(sel);
    q('[data-key]').value = skill.key;
    q('[data-name]').value = skill.name || '';
    q('[data-desc]').value = skill.desc || '';
    q('[data-body]').value = skill.body || '';
    q('[data-manuals]').value = (skill.manuals || []).join('\n');
    const prov = q('[data-prov]');
    if (prov) {
      if (skill.created_by) {
        const ts = skill.created_ts ? new Date(skill.created_ts * 1000).toLocaleString() : '';
        prov.textContent = t('由 {who} 创建{ts}', { who: skill.created_by, ts: ts ? ' · ' + ts : '' });
        prov.hidden = false;
      } else {
        prov.hidden = true;
      }
    }
  }

  #formError(text) {
    const el = this.pane.querySelector('[data-status]');
    if (el) { el.textContent = text; el.classList.add('err'); }
  }

  async #save() {
    if (this.saving) return;
    const q = (sel) => this.pane.querySelector(sel);
    const key = (q('[data-key]')?.value || '').trim();
    const name = (q('[data-name]')?.value || '').trim();
    const status = q('[data-status]');
    status.textContent = '';
    status.classList.remove('err');
    if (!KEY_RE.test(key)) { this.#formError(t('key 非法：[a-z0-9_-]{1,32}（创建后不可改名）')); return; }
    if (!name) { this.#formError(t('名称不能为空')); return; }
    const manuals = (q('[data-manuals]').value || '')
      .split(/[\n,，]/).map((s) => s.trim()).filter(Boolean);
    if (manuals.length > MAX_MANUALS) { this.#formError(t('手册引用超数：{n} 条（上限 {max}）', { n: manuals.length, max: MAX_MANUALS })); return; }
    const skill = { key, name, desc: q('[data-desc]').value.trim(), body: q('[data-body]').value, manuals };
    this.saving = true;
    q('[data-save]').disabled = true;
    q('[data-save]').textContent = t('保存中…');
    try {
      const saved = await postSkill(key, skill);
      this.board.toast.show(t('已保存技能 {key}（{name}）', { key: saved.key, name: saved.name }), 'ok');
      this.editing = saved.key;
      this.saving = false;
      this.render();      // re-shape the editor for the saved key (disabled key, delete face)
      await this.reload(); // list-only: fresh rows + active highlight
      this.#loadDetail(); // refill from the saved skill (prov line included)
    } catch (err) {
      this.saving = false;
      const btn = this.pane.querySelector('[data-save]');
      if (btn) { btn.disabled = false; btn.textContent = t('保存技能'); }
      this.#formError(t('保存被拒：{msg}', { msg: err.message }));
    }
  }
}

// the list marks' one-glyph legend, for the rows' title tooltips
const MARK_TITLE = {
  '文': '正文（指令面料）',
  '册': '手册引用',
};

// mcps.js — the MCP server library page (people board's 「MCP 服务」
// tab): the list (GET /mcps — header values masked) beside one editor
// (key/名称/类型 http|sse/URL/请求头行) saved over POST /mcps/{key}.
// Cleartext header values load only through the loopback detail read
// (getMCP); the list never carries them. Same refresh discipline as
// the skill library: background refreshes repaint the list, never the
// editor.

import { getMCPs, getMCP, postMCP, deleteMCP } from '../wire/api.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

const KEY_RE = /^[a-z0-9_-]{1,32}$/;

export class MCPLibraryView {
  /** @param {HTMLElement} pane @param {import('../people/people.js').PeopleBoard} board */
  constructor(pane, board) {
    this.pane = pane;
    this.board = board;
    this.state = 'loading'; // loading | error | ready
    this.mcps = [];
    this.editing = null;    // null = blank new form; string = its key
    this.saving = false;
    this.confirmDelete = false;
    pane.addEventListener('click', (ev) => this.#onClick(ev));
  }

  async reload() {
    if (this.state !== 'ready') { this.state = 'loading'; this.render(); }
    try {
      this.mcps = await getMCPs();
      this.state = 'ready';
    } catch (err) {
      this.state = 'error';
      this.error = err;
    }
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
    if (tg.closest('[data-header-add]')) { this.#addHeaderRow(); return; }
    const hr = tg.closest('[data-header-rm]');
    if (hr) { hr.closest('.mcp-header-row').remove(); return; }
    const row = tg.closest('[data-mcp]');
    if (row && row.dataset.mcp !== this.editing) {
      this.editing = row.dataset.mcp;
      this.render();
      this.#loadDetail();
      return;
    }
    if (tg.closest('[data-save]')) this.#save();
    if (tg.closest('[data-reset]')) { this.render(); this.#loadDetail(); }
  }

  #addHeaderRow(name = '', value = '') {
    const host = this.pane.querySelector('[data-headers]');
    if (!host) return;
    const div = document.createElement('div');
    div.className = 'mcp-header-row';
    div.innerHTML =
      `<input class="input mono" data-header-name maxlength="64" placeholder="${esc(t('头名（如 Authorization）'))}" value="${esc(name)}">` +
      `<input class="input mono" data-header-value maxlength="512" placeholder="${esc(t('值（可含机密，列表不回显）'))}" value="${esc(value)}">` +
      `<button type="button" class="btn small" data-header-rm>${esc(t('去'))}</button>`;
    host.appendChild(div);
  }

  async #delete() {
    if (this.editing === null) return;
    const btn = this.pane.querySelector('[data-del]');
    if (!this.confirmDelete) {
      this.confirmDelete = true;
      if (btn) {
        btn.classList.add('armed');
        btn.textContent = t('再点一次确认删除（装配引用将失效）');
        setTimeout(() => {
          if (!this.confirmDelete) return;
          this.confirmDelete = false;
          const b = this.pane.querySelector('[data-del]');
          if (b) { b.classList.remove('armed'); b.textContent = t('删除服务'); }
        }, 4000);
      }
      return;
    }
    if (btn) { btn.disabled = true; btn.textContent = t('删除中…'); }
    try {
      const out = await deleteMCP(this.editing);
      const extra = (out.referenced_by || []).length
        ? t('（{who} 的装配已失效，成员出生不再挂载）', { who: out.referenced_by.join(t('、')) }) : '';
      this.board.toast.show(t('已删除 MCP 服务 {key}{extra}', { key: this.editing, extra }), 'ok');
      this.confirmDelete = false;
      this.editing = null;
      await this.reload();
      this.render();
    } catch (err) {
      this.confirmDelete = false;
      if (btn) { btn.disabled = false; btn.classList.remove('armed'); btn.textContent = t('删除服务'); }
      this.#formError(t('删除被拒：{msg}', { msg: err.message }));
    }
  }

  async #loadDetail() {
    const form = this.pane.querySelector('[data-form]');
    if (!form || this.editing === null) return;
    form.classList.add('dim');
    try {
      const m = await getMCP(this.editing);
      if (!this.pane.querySelector('[data-form]') || this.editing !== m.key) return;
      this.#fillForm(m);
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
        `<div class="state-card error"><strong>${esc(t('MCP 库读取失败'))}</strong>
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
    if (!this.mcps.length) {
      return `<div class="state-card empty"><strong>${esc(t('MCP 库还是空的'))}</strong><span>${esc(t('登记第一个 MCP 服务（http/sse），装配给成员后其会话出生即挂载'))}</span></div>` +
        `<button type="button" class="btn gold" data-new>${esc(t('+ 登记 MCP 服务'))}</button>`;
    }
    const rows = this.mcps.map((m) => {
      const tip = m.desc ? ` title="${esc(m.desc)}"` : '';
      const nh = (m.headers || []).length
        ? `<i title="${esc(t('请求头'))}">${esc(t('头×' + (m.headers || []).length))}</i>` : '';
      return `<button type="button" class="pack-row${m.key === this.editing ? ' active' : ''}" data-mcp="${esc(m.key)}"${tip}>
        <span class="pack-key">${esc(m.key)}</span>
        <span class="pack-name">${esc(m.name)}</span>
        <span class="pack-slots"><i>${esc(m.type)}</i>${nh}</span>
      </button>`;
    }).join('');
    return `<div class="pack-list-head">${esc(t('MCP 库 · {n} 个', { n: this.mcps.length }))}
      <button type="button" class="btn small gold" data-new>${esc(t('+ 新建'))}</button></div>` + rows;
  }

  #formHTML() {
    const isNew = this.editing === null;
    const title = isNew ? t('登记 MCP 服务') : t('编辑 {key}', { key: this.editing });
    const del = isNew ? '' :
      `<button type="button" class="btn small danger" data-del>${esc(t('删除服务'))}</button>`;
    return `<div class="form-head"><strong>${esc(title)}</strong>
        <span class="form-head-actions">${del}
        <button type="button" class="btn small" data-reset>${esc(t('还原'))}</button></span></div>
      <div class="prov" data-prov hidden></div>
      <div class="field-note dim">${esc(t('装配给成员后，其会话出生时以这些服务整体替换默认 MCP fleet；仅支持 http/sse。'))}</div>
      <label class="field"><span>key<i class="req">*</i>${isNew ? '' : esc(t('（创建后不可改名）'))}</span>
        <input class="input" data-key maxlength="32" value="${esc(isNew ? '' : this.editing)}" ${isNew ? 'placeholder="[a-z0-9_-]{1,32}"' : 'disabled'}></label>
      <label class="field"><span>${esc(t('名称'))}<i class="req">*</i></span>
        <input class="input" data-name maxlength="48" placeholder="${esc(t('显示名，≤48 字'))}"></label>
      <label class="field"><span>${esc(t('描述'))}</span>
        <input class="input" data-desc maxlength="2000" placeholder="${esc(t('一句话说明（可空）'))}"></label>
      <div class="slot-grid">
        <label class="slot"><span>${esc(t('类型'))}</span></label>
        <select class="input" data-type>
          <option value="http">http</option><option value="sse">sse</option>
        </select>
      </div>
      <label class="field"><span>URL<i class="req">*</i></span>
        <input class="input mono" data-url maxlength="512" placeholder="http://127.0.0.1:4401/mcp"></label>
      <label class="field"><span>${esc(t('请求头'))}</span></label>
      <div data-headers class="mcp-headers"></div>
      <button type="button" class="btn small" data-header-add>${esc(t('+ 加请求头'))}</button>
      <div class="overlay-status" data-status></div>
      <div class="overlay-actions">
        <button type="button" class="btn gold" data-save ${this.saving ? 'disabled' : ''}>${this.saving ? esc(t('保存中…')) : esc(t('保存服务'))}</button>
      </div>`;
  }

  #fillForm(m) {
    const q = (sel) => this.pane.querySelector(sel);
    q('[data-key]').value = m.key;
    q('[data-name]').value = m.name || '';
    q('[data-desc]').value = m.desc || '';
    q('[data-type]').value = m.type || 'http';
    q('[data-url]').value = m.url || '';
    q('[data-headers]').innerHTML = '';
    for (const h of m.headers || []) this.#addHeaderRow(h.name, h.value);
    const prov = q('[data-prov]');
    if (prov) {
      if (m.created_by) {
        const ts = m.created_ts ? new Date(m.created_ts * 1000).toLocaleString() : '';
        prov.textContent = t('由 {who} 创建{ts}', { who: m.created_by, ts: ts ? ' · ' + ts : '' });
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
    const url = (q('[data-url]')?.value || '').trim();
    const status = q('[data-status]');
    status.textContent = '';
    status.classList.remove('err');
    if (!KEY_RE.test(key)) { this.#formError(t('key 非法：[a-z0-9_-]{1,32}（创建后不可改名）')); return; }
    if (!name) { this.#formError(t('名称不能为空')); return; }
    if (!/^https?:\/\/\S+$/.test(url)) { this.#formError(t('URL 须为 http/https 绝对地址')); return; }
    const headers = [];
    for (const row of this.pane.querySelectorAll('.mcp-header-row')) {
      const hn = row.querySelector('[data-header-name]').value.trim();
      const hv = row.querySelector('[data-header-value]').value.trim();
      if (hn) headers.push({ name: hn, value: hv });
    }
    const mcp = { key, name, desc: q('[data-desc]').value.trim(), type: q('[data-type]').value, url, headers };
    this.saving = true;
    q('[data-save]').disabled = true;
    q('[data-save]').textContent = t('保存中…');
    try {
      const saved = await postMCP(key, mcp);
      this.board.toast.show(t('已保存 MCP 服务 {key}（{name}）', { key: saved.key, name: saved.name }), 'ok');
      this.editing = saved.key;
      this.saving = false;
      this.render();
      await this.reload();
      this.#loadDetail();
    } catch (err) {
      this.saving = false;
      const btn = this.pane.querySelector('[data-save]');
      if (btn) { btn.disabled = false; btn.textContent = t('保存服务'); }
      this.#formError(t('保存被拒：{msg}', { msg: err.message }));
    }
  }
}

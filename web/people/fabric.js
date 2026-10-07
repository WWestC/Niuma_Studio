// fabric.js — the capability assembly preview panel: the sidebar's
// 技能装配 zone as a self-contained sub-view. One project selector
// feeding GET /p/{key}/staffing/{person}/fabric — Go composes
// (profile ⊕ override), this only reads: effective skill chips up
// front, the full Compose fabric (model tier / MCP servers / manual
// chain / fabric text) behind a fold. The panel's project DEFAULTS to
// the person's own seat (their room — the roster column's source); a
// manual pick (picked=true) sticks for THAT person's view only and is
// dropped on the next person switch — a sticky pick riding across
// persons shows a project the person isn't in. Loads are tokened so a
// slow answer for the previous person can never overwrite the current
// one's.

import { getFabric } from '../wire/api.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

export class FabricPanel {
  /** @param {() => void} rerender repaint the host sidebar */
  constructor(rerender) {
    this.rerender = rerender;
    this.state = 'idle'; // idle | loading | ready | error
    this.project = '';
    this.picked = false;  // 操作员手动挑过席位（换人即失效，见 resetPick）
    this.data = null;
    this.error = null;
    this.token = 0;
  }

  /** A person switch drops the operator's manual seat pick — the next
   *  load re-defaults to the new person's own room (the caller's
   *  fallback chain), never the previous person's pick. */
  resetPick() {
    this.picked = false;
  }

  /**
   * Load the named person's fabric for a project seat. Keeps the panel
   * on the last picked project when the caller passes the same key.
   * @param {string} project @param {string} person
   */
  async load(project, person) {
    if (!person) return;
    const tok = ++this.token;
    this.state = 'loading';
    this.project = project;
    this.data = null;
    this.error = null;
    this.rerender();
    try {
      const data = await getFabric(project, person);
      if (this.token !== tok) return; // a newer load won
      this.state = 'ready';
      this.data = data;
    } catch (err) {
      if (this.token !== tok) return;
      this.state = 'error';
      this.error = err;
    }
    this.rerender();
  }

  /** The 技能装配 section for the given project choices. */
  html(choices) {
    const cur = this.project || 'default';
    let chips;
    if (this.state === 'ready') {
      const skills = this.data?.skills || [];
      const mcps = this.data?.mcps || [];
      const skillChips = skills.length
        ? skills.map((k) => `<span class="chip">${esc(k)}</span>`).join('') : '';
      const mcpChips = mcps.length
        ? mcps.map((k) => `<span class="chip mcp">${esc(t('MCP'))} ${esc(k)}</span>`).join('') : '';
      chips = (skillChips || mcpChips)
        ? '<div class="chips">' + skillChips + mcpChips + '</div>'
        : `<div class="dim small">${t('该席位未装配技能（基座面料）')}</div>`;
    } else {
      chips = `<div class="dim small">${this.state === 'loading' ? t('面料计算中…') : ''}</div>`;
    }
    let fold = '';
    if (this.state === 'ready') {
      const fab = this.data?.fabric || {};
      const model = fab.model ? `${esc(fab.model.id)}${fab.model.reasoning ? ' / ' + esc(fab.model.reasoning) : ''}` : t('—（进程默认）');
      const mcps = (fab.mcp || []).map((m) => `${esc(m.key)}（${esc(m.type)}）`).join(t('、')) || '—';
      fold = `<details class="fold"><summary>${t('面料预览（Compose 同源输出）')}</summary>
        <div class="kv"><span>${t('模型档位')}</span><b>${model}</b></div>
        <div class="kv"><span>${t('MCP（出生挂载）')}</span><b>${mcps}</b></div>
        <div class="kv"><span>${t('手册链')}</span><b>${(fab.manuals || []).map(esc).join(t('、')) || '—'}</b></div>
        <pre class="pre fabric">${esc(fab.text || '')}</pre>
      </details>`;
    } else if (this.state === 'error') {
      fold = `<div class="side-err">${t('面料预览失败：{msg}', { msg: esc(this.error?.message || String(this.error)) })}</div>`;
    }
    return `<div class="side-sec"><h3>${t('技能装配')}</h3>
      <label class="field-inline"><span>${t('席位')}</span>
        <select class="input" data-fabric-project>
          ${choices.map((c) => `<option value="${esc(c.key)}"${c.key === cur ? ' selected' : ''}>${esc(c.name)}（${esc(c.key)}）</option>`).join('')}
        </select>
      </label>
      ${chips}${fold}
      <div class="dim small">${t('装配管理在「技能库」页、房内斜杠命令与 CLI（assemble）；此处只读预览')}</div>
    </div>`;
  }
}

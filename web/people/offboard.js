// offboard.js — the departure pipeline panel (v2 P5/P6): the sidebar's
// 离职 zone as a self-contained sub-view. Owns the two-step confirm,
// the busy state and the result record; POST /offboard's answers map
// 1:1 onto renders — 200 {ok,steps,note} → the ✓/✗/△ step log exactly
// like the CLI's, 409 {blocked{reason,tasks}} → the reason plus the
// in-progress tasks and the force escape, anything else → the error
// body. The host (local / rank 99) gets no panel at all.

import { postOffboard } from '../wire/api.js';
import { esc, icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

export class OffboardPanel {
  /**
   * @param {(text: string, kind?: string) => void} notify toast face
   * @param {() => void} onDone a completed departure (refresh the boards)
   * @param {() => void} rerender repaint the host sidebar
   */
  constructor(notify, onDone, rerender) {
    this.notify = notify;
    this.onDone = onDone;
    this.rerender = rerender;
    this.reset();
  }

  reset() {
    this.armed = false;
    this.busy = false;
    this.result = null; // {kind:'steps'|'blocked'|'error', ...}
  }

  /** The 离职管线 section; '' for the host (never removable). */
  html(p) {
    if (!p || p.local || p.rank === 99) return '';
    let result = '';
    const r = this.result;
    if (r?.kind === 'blocked') {
      result = `<div class="side-err">${t('被拦截：{reason}', { reason: esc(r.reason) })}</div>` +
        '<div class="off-steps">' +
        (r.tasks || []).map((t2) =>
          `<div class="off-step"><span class="mk skip">${icon('dash', 12)}</span><span>${esc(t2.id)} ${esc(t2.title)}（${esc(t2.status)}）</span></div>`).join('') +
        '</div>' +
        '<div class="ops-grid" style="margin-top:6px">' +
        `<button type="button" class="btn small danger" data-offboard-force>${t('仍要离职（在办任务直接取消）')}</button>` +
        '</div>';
    } else if (r?.kind === 'steps') {
      result = '<div class="off-steps">' +
        (r.steps || []).map((s) => {
          const mk = s.skip ? 'skip' : s.ok ? 'ok' : 'bad';
          const glyph = icon(s.skip ? 'dash' : s.ok ? 'check' : 'close', 12);
          return `<div class="off-step"><span class="mk ${mk}">${glyph}</span><span>${esc(s.step)}${s.note ? ` · ${esc(s.note)}` : ''}</span></div>`;
        }).join('') +
        '</div>' +
        (r.note ? `<div class="dim small" style="margin-top:6px">${esc(r.note)}</div>` : '');
    } else if (r?.kind === 'error') {
      result = `<div class="side-err">${t('离职失败：{msg}', { msg: esc(r.message) })}</div>`;
    }
    const btn = this.busy
      ? `<button type="button" class="btn small" disabled>${t('离职执行中…')}</button>`
      : `<button type="button" class="btn small danger${this.armed ? ' armed' : ''}" data-offboard>
          ${this.armed ? t('再点一次确认离职（五步不可逆）') : t('离职 offboard')}</button>`;
    return `<div class="side-sec"><h3>${t('离职管线')}</h3>
      <div class="ops-grid">${btn}</div>${result}
      <div class="dim small">${t('改派在办任务 → 通告 → 移出 → 编制/档案 → 台账留痕')}</div>
    </div>`;
  }

  /**
   * Consume a click inside the panel. Returns true when handled.
   * @param {Element} t the closest-target being dispatched @param {string} name
   */
  click(t, name) {
    if (t.closest('[data-offboard]')) {
      if (!this.armed) { // 两段确认：arm, then commit on the second click
        this.armed = true;
        this.rerender();
        setTimeout(() => { if (this.armed) { this.armed = false; this.rerender(); } }, 4000);
        return true;
      }
      this.armed = false;
      this.#run(false, name);
      return true;
    }
    if (t.closest('[data-offboard-force]')) { this.#run(true, name); return true; }
    return false;
  }

  // POST /offboard — 200 {ok,steps,note} / 409 {blocked{reason,tasks}}
  // / anything else: the error body text.
  async #run(force, name) {
    this.busy = true;
    this.rerender();
    let res;
    try {
      res = await postOffboard({ name, force });
    } catch (err) {
      res = { ok: false, data: { error: err?.message || String(err) } };
    }
    this.busy = false;
    if (res.ok && res.data?.ok) {
      this.result = { kind: 'steps', steps: res.data.steps || [], note: res.data.note || '' };
      this.notify(t('{name} 离职完成', { name }), 'ok');
      this.onDone();
    } else if (res.status === 409 && res.data?.blocked) {
      this.result = { kind: 'blocked', reason: res.data.blocked.reason || t('在办任务未处理'), tasks: res.data.blocked.tasks || [] };
      this.notify(t('离职被拦截：在办任务未处理'), 'err');
    } else {
      this.result = { kind: 'error', message: res.data?.error || `HTTP ${res.status}` };
      this.notify(t('离职失败'), 'err');
    }
    this.rerender();
  }
}

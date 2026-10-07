// recruit.js — the birth form (US-H2, frontend 稿 §4.2 主操作): an
// overlay card over the roster — 名称/身份/模型/出生项目/提示词 fields →
// POST /dispatch/birth. The 模型 dropdown drinks GET /dispatch/models
// (the desktop picker's full surface: account-plan faces first —
// BigModel 团队 / Start Plan 免费, badge chipped — then every personal
// provider in the local ZCode config, grouped by display name;
// undrivable account faces stay visible but disabled, reason in the
// group label, exactly as the desktop registry keeps them; 留空 =
// the default chain) and degrades to a 默认-only select when the list
// can't load. Birth is an asynchronous session creation, so the button
// rides the 「出生中…」 state until the /dispatch snapshot for the
// chosen project names the newborn (a light poll, capped); a 400's
// body is the refusal reason, shown in place, never swallowed.

import { postBirth, getDispatch, getModels } from '../wire/api.js';
import { esc, icon, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { fillReasoningOptions, providerGroupLabel } from './modellevels.js';

const POLL_EVERY_MS = 1500;
const POLL_MAX_MS = 90_000;

export class RecruitForm {
  /**
   * @param {HTMLElement} _pane kept for signature symmetry with the
   *   sibling views — the card anchors to document.body instead, a
   *   fixed overlay that survives the roster pane's async re-renders
   *   @param {import('./people.js').PeopleBoard} board
   */
  constructor(_pane, board) {
    this.board = board;
    this.el = null;
    this.busy = false;
    this.models = undefined; // undefined = 未拉取；null = 拉取失败；object = {default, levels?, providers}
  }

  /**
   * @param {{prefProject?: string, prefRole?: string, prefName?: string}|string} [pref]
   *   prefill for the quick-hire path (the est-bar's 缺员 chips hand in
   *   role + a suggested name); a bare string is the legacy prefProject
   */
  open(pref = {}) {
    this.close();
    const { prefProject = '', prefRole = '', prefName = '' } =
      typeof pref === 'string' ? { prefProject: pref } : pref;
    const choices = this.board.projectChoices();
    // 缺省链：显式预填（编制页「招牛马」）→ 房主当前正坐的房间 → 大厅。
    // 人在哪个项目，出生就落在哪个项目；当前房不在可选面（未开张/已
    // 归档/清单未及拉回）时照旧回大厅。
    const room = this.board.book?.current;
    const project = [prefProject, room].find((k) => k && choices.some((c) => c.key === k)) || 'default';
    const el = document.createElement('div');
    el.className = 'overlay';
    el.innerHTML =
      `<div class="overlay-card" role="dialog" aria-modal="true" aria-label="${t('出生新成员')}">` +
        `<div class="overlay-head"><strong>${prefRole ? `${t('补员')} · ${esc(prefRole)}` : t('出生新成员')}</strong>` +
          `<button type="button" class="icon-btn" data-close title="${t('关闭')}">` + icon('close') + '</button></div>' +
        `<label class="field"><span>${t('名称')}<i class="req">*</i><i>${t('≤24 字符；同名复用离线档案，已在调度中则拒绝')}</i></span>` +
          `<input class="input" data-name maxlength="24" placeholder="${t('成员名')}"></label>` +
        `<label class="field"><span>${t('身份')}<i>${t('与编制表「身份」列一字不差；旗舰岗（排期编排/人事）各 1 席，缺员补员用同名出生，勿另招新名')}</i></span>` +
          `<input class="input" data-role maxlength="24" placeholder="${t('如：后端 / 测试 / 前端（与编制表精确匹配）')}"></label>` +
        `<label class="field"><span>${t('模型')}</span>` +
          `<div class="model-row"><select class="input" data-model><option value="">${t('默认（跟随调度配置）')}</option></select>` +
          `<button type="button" class="icon-btn" data-models-refresh title="${t('刷新模型清单')}">${icon('refresh')}</button></div></label>` +
        `<label class="field"><span>${t('思考强度')}<i>${t('随所选模型的可选档位（与 ZCode 同源）；默认＝模型缺省档；模型留空时档位作用于默认链模型')}</i></span>` +
          `<select class="input" data-reasoning disabled><option value="">${t('默认（模型缺省档）')}</option></select></label>` +
        `<label class="field"><span>${t('出生项目')}</span>` +
          `<select class="input" data-project>${choices.map((c) =>
            `<option value="${esc(c.key)}"${c.key === project ? ' selected' : ''}>${esc(c.name)}（${esc(c.key)}）</option>`).join('')}</select></label>` + // xss-ok: 三元产物是字面类名，键已 esc
        `<label class="field"><span>${t('接入提示词')}</span>` +
          `<textarea class="input" data-prompt rows="5" placeholder="${t('留空 = 按档案/技能装配组合基座面料出生')}"></textarea></label>` +
        '<div class="overlay-status" data-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-cancel>${t('取消')}</button>` +
          `<button type="button" class="btn gold" data-submit>${t('出生')}</button>` +
        '</div>' +
      '</div>';
    el.addEventListener('click', (ev) => {
      if (ev.target === el) this.close(); // backdrop click
      if (ev.target.closest('[data-close],[data-cancel]')) this.close();
      if (ev.target.closest('[data-submit]')) this.#submit();
      // 应用内没有整页刷新入口——清单的「重看一次」就地解决：清缓存
      // 重拉，选中项能存活就存活（#fillModels 的 prev 语义）。
      if (ev.target.closest('[data-models-refresh]')) {
        this.models = undefined;
        this.#fillModels();
      }
    });
    document.body.appendChild(el);
    this.el = el;
    if (prefRole) el.querySelector('[data-role]').value = prefRole;
    // 每次开卡都重拉清单（本机 localhost JSON，开销可忽略）：拉取失败
    // 不再粘死在「清单不可用」，服务端换血后重开表单即见新面。
    this.models = undefined;
    this.#fillModels();
    const nameInput = el.querySelector('[data-name]');
    nameInput.value = prefName;
    nameInput.focus();
    nameInput.select(); // quick-hire lands one Enter away from 出生
  }

  // #fillModels drinks the pick list into [data-model]: a 默认 option
  // first (labelled with the process default when known), then one
  // optgroup per provider — the option value is "providerId/modelId",
  // the exact ref the seat stamps. Fetched per open (and per
  // [data-models-refresh] click); a fetch failure keeps the 默认-only
  // select and says so in its label.
  async #fillModels() {
    if (this.models === undefined) {
      this.models = await getModels().catch(() => null);
    }
    const sel = this.el?.querySelector('[data-model]');
    if (!sel || !this.models) {
      if (sel && this.models === null) {
        sel.querySelector('option').textContent = t('默认（模型清单不可用）');
      }
      return;
    }
    const prev = sel.value; // keep the pick across a re-open
    const def = this.models.default || 'GLM-5.3';
    let html = `<option value="">${t('默认（调度配置 {v}）', { v: esc(def) })}</option>`;
    for (const p of this.models.providers || []) {
      // 分组标签与 ZCode 拾取器对齐：账号套餐面带计划徽记
      // （BigModel Team Coding Plan · 团队 / Start Plan · 免费），
      // 个人 provider（智谱内部）无徽记；不可用的账号面照桌面注册表
      // 口径保留、选项禁用（组标签括注原因）——ZCode 看得见的面这里
      // 都看得见，点得动的才可选。
      const off = p.available === false;
      html += `<optgroup label="${esc(providerGroupLabel(p))}">` +
        (p.models || []).map((m) =>
          `<option value="${esc(p.id)}/${esc(m)}"${off ? ' disabled' : ''}>${esc(m)}</option>`).join('') +
        '</optgroup>';
    }
    sel.innerHTML = html;
    if ([...sel.options].some((o) => o.value === prev)) sel.value = prev;
    // 思考强度随模型选择联动：换模型时按新模型的档位表重建选项，
    // 已选强度能存活就存活（低档模型间通用），不能则回默认；模型留
    // 空（回默认链）时按默认模型的档位表——ZCode 同口径，不必先换模
    // 型也能挑强度。
    const lvl = this.el?.querySelector('[data-reasoning]');
    if (lvl) {
      const defModel = this.models.default || '';
      fillReasoningOptions(lvl, sel.value, this.models.levels, lvl.value, defModel);
      sel.onchange = () =>
        fillReasoningOptions(lvl, sel.value, this.models.levels, lvl.value, defModel);
    }
  }

  close() {
    this.pollStop = true;
    this.busy = false;
    dismiss(this.el);
    this.el = null;
  }

  #status(text, err = false) {
    const el = this.el?.querySelector('[data-status]');
    if (!el) return;
    el.textContent = text || '';
    el.classList.toggle('err', !!err);
  }

  async #submit() {
    if (!this.el || this.busy) return;
    const name = this.el.querySelector('[data-name]').value.trim();
    const role = this.el.querySelector('[data-role]').value.trim();
    const project = this.el.querySelector('[data-project]').value;
    const prompt = this.el.querySelector('[data-prompt]').value.trim();
    const model = this.el.querySelector('[data-model]')?.value || '';
    const reasoning = this.el.querySelector('[data-reasoning]')?.value || '';
    if (!name) { this.#status(t('名称不能为空'), true); return; }
    this.busy = true;
    const btn = this.el.querySelector('[data-submit]');
    btn.disabled = true;
    btn.classList.add('loading'); // 按钮加载态（数据录入·按钮篇）
    btn.textContent = t('出生中…');
    this.#status(t('正在创建会话并注入面料（最长约 1 分钟）…'));
    try {
      const receipt = await postBirth({
        name, role, prompt, model, reasoning,
        project: project === 'default' ? '' : project,
      });
      await this.#awaitSeat(project, name, receipt);
    } catch (err) {
      this.busy = false;
      btn.disabled = false;
      btn.classList.remove('loading');
      btn.textContent = t('出生');
      this.#status(t('出生失败：{msg}', { msg: err.message }), true);
    }
  }

  // Poll the project's dispatcher snapshot until the newborn shows up
  // (§4.2 加载态: birth 的确认以 /dispatch 快照或后续事件为准).
  #awaitSeat(project, name, receipt) {
    return new Promise((resolve) => {
      const started = Date.now();
      this.pollStop = false;
      const tick = async () => {
        if (!this.el || this.pollStop) return resolve();
        try {
          const rows = await getDispatch(project === 'default' ? '' : project);
          if (rows.some((r) => r.name === name)) {
            this.board.toast.show(t('{name} 已出生入座（{inner}）', {
              name,
              inner: receipt ? t('会话 {id}…', { id: receipt.slice(0, 12) }) : t('回执已到'),
            }), 'ok');
            this.close();
            this.board.requestRefresh();
            this.board.select(name);
            return resolve();
          }
        } catch { /* the snapshot is a poll, transient failures just wait */ }
        if (Date.now() - started > POLL_MAX_MS) {
          this.board.toast.show(t('{name} 的出生回执已到，调度快照暂未见其入座——稍后自行刷新确认', { name }), 'info');
          this.close();
          this.board.requestRefresh();
          return resolve();
        }
        this.#status(t('出生中…（等待调度快照出现该成员）'));
        setTimeout(tick, POLL_EVERY_MS);
      };
      setTimeout(tick, POLL_EVERY_MS);
    });
  }
}

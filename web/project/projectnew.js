// projectnew.js — the 立项 / 项目信息编辑 overlay (v2 P7). Create mode
// lands a draft project (POST /projects); edit mode PATCHes the content
// envelope (key frozen, workspace locked by the store once active). One
// optional version rides along in either mode — the gantt's version
// window and the overview's 版本进度 both drink from it. The submit
// callback owns the API call and may throw: the thrown reason shows in
// the form's status line (server refusals are the actionable why —
// duplicate slug, relative workspace, overlapping windows).
//
// v2.13 立项预检：创建态在表单里常驻「立项须知」（工作区是 AI 成员的
// 会话出生地、key/工作区创建即锁定、开张招系统岗耗额度、上限 32 个），
// 提交前先打 POST /projects/preflight——error（服务端必拒项）内联拦下，
// warn（目录不存在/主目录/嵌套等注意事项）第一次点只亮清单＋改键
// 「已了解风险，继续立项」，再点一次才放行；相关字段一动确认即撤销。

import { esc, icon, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { listDir, preflightProject } from '../wire/api.js';

const KEY_RE = /^[a-z0-9_-]{1,32}$/;

/** name → slug suggestion (ASCII folds; a CJK name yields '' — typed by hand). */
function slugify(name) {
  return name.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32);
}

function dtToUnix(v) {
  if (!v) return 0;
  const t = new Date(v).getTime();
  return Number.isNaN(t) ? 0 : Math.round(t / 1000);
}

function unixToDT(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export class ProjectNewForm {
  /**
   * @param {Object} opts
   * @param {Object} [opts.project] present = edit mode (PATCH); absent = 立项
   * @param {HTMLElement} [opts.mount] 挂载点（默认 body）——项目空间门
   *   （spacegate.js）把自己 z-96 自成栈上下文，表单与目录选择器都得
   *   挂进门内才画得在门上
   * @param {(payload: Object) => Promise<void>} opts.onSubmit the API
   *   call; a rejection's message shows in-form, a resolution closes.
   */
  constructor(opts) {
    this.opts = opts;
    const p = opts.project || null;
    const editing = !!p;
    const wsLocked = editing && p.status === 'active';
    const vers = (p && p.versions) || [];
    const el = document.createElement('div');
    el.className = 'overlay';
    el.innerHTML = `<div class="overlay-card w-md" role="dialog" aria-modal="true" aria-label="${editing ? t('编辑项目信息') : t('立项')}">
      <div class="overlay-head"><strong>${editing ? t('编辑项目信息 · {name}', { name: esc(p.name || p.key) }) : t('立项 · 新项目')}</strong>
        <button type="button" class="icon-btn" data-close title="${t('关闭')}">${icon('close')}</button></div>
      ${editing ? '' : `<div class="overlay-desc">${t('登记一个项目容器：成员会话、任务台账与排期都以它为单位组织。')}</div>`}
      ${editing ? '' : `<div class="pn-notice"><strong>${t('立项须知——先把风险说在前面')}</strong><ul>` +
        `<li>${t('工作区是 AI 成员的会话出生地：他们会在该目录里读写文件、执行命令。请指向本项目的专用目录，不要指向主目录、系统目录或存有敏感数据的地方。')}</li>` +
        `<li>${t('项目 key 与工作区一经创建即锁定：开张后工作区不可改（会话钉在路径上），确需迁移＝归档后另立新项目。')}</li>` +
        `<li>${t('开张会生成项目房并自动招聘系统岗（编排者/HR 各一，小助手全工作室唯一）——招聘与日常运转消耗模型额度。')}</li>` +
        `<li>${t('项目上限 32 个；历史、任务台账与编制都以 key 为外键，创建后不可改名。')}</li>` +
        `<li>${t('分支工作法：成员可各驻一根分支（专属工作树，互不踩脚）；提交引用台账号（t_NN），关单要验提交；并入主线走合并提案——编排者提、房主批。工作室对 git 只做新增性操作：绝不删除分支/工作树，绝不碰工作区所在仓库之外或之上的仓库。')}</li>` +
        `</ul></div>`}
      ${editing ? '' : `<label class="field"><span>${t('项目 key')}<i class="req">*</i><i>${t('小写字母/数字/-/_，创建后不可改')}</i></span>
        <input class="input" data-key maxlength="32" placeholder="${t('如 website-redesign')}" spellcheck="false"></label>`}
      <label class="field"><span>${t('项目名称')}<i class="req">*</i></span>
        <input class="input" data-name maxlength="48" placeholder="${t('一句话叫得出口的名字')}"></label>
      <div class="field"><span>${t('工作区')}<i class="req">*</i><i>${t('成员会话的出生路径')}${wsLocked ? t('，已开张不可改') : ''}</i></span>
        <div class="ws-row">
          <input class="input mono" data-workspace ${wsLocked ? 'disabled' : ''} value="${esc(p?.workspace || '')}"
            placeholder="${t('/绝对/路径——成员的会话将开在这里，或点「浏览…」选一个')}" spellcheck="false">
          ${wsLocked ? '' : `<button type="button" class="btn small" data-browse title="${t('浏览本机目录选一个')}">${t('浏览…')}</button>`}
        </div></div>
      ${editing
        ? `<div class="pn-git" data-git>
            <div class="pn-git-head"><span>${t('Git 版本管理')}</span></div>
            <label class="chk"><input type="checkbox" data-git-on${p.git_disabled ? '' : ' checked'}> ${t('启用 git 版本管理（总开关——基线与自动建枝在设置中心·项目页设置）')}</label>
          </div>`
        : `<div class="pn-git" data-git>
          <div class="pn-git-head"><span>${t('Git 引导')}</span><span class="pn-git-state" data-git-state></span></div>
          <label class="chk"><input type="checkbox" data-git-on checked> ${t('启用 git 版本管理（总开关）')}</label>
          <div data-git-sub>
            <label class="chk"><input type="checkbox" data-git-init> ${t('工作区不是 git 仓库时自动 git init（含一次空首提交，并装好提交规范/播报钩子）')}</label>
            <div class="field"><span>${t('基线分支')}<i>${t('成员分支从这里切出——检测到仓库时自动填当前分支')}</i></span>
              <input class="input mono" data-git-base maxlength="64" placeholder="main" spellcheck="false"></div>
            <label class="chk"><input type="checkbox" data-git-seat checked> ${t('给成员设分支时，分支不存在就从基线自动新建（不必再手工建枝）')}</label>
          </div>
        </div>`}
      <label class="field"><span>${t('描述')}</span>
        <textarea class="input" rows="2" data-desc maxlength="2000" placeholder="${t('这个项目做成什么算成（可空）')}"></textarea></label>
      <div class="pn-vers" data-vers></div>
      <button type="button" class="btn small" data-add-ver>${t('＋ 添加版本')}</button>
      <div class="pn-risks" data-risks hidden></div>
      <div class="overlay-status" data-status></div>
      <div class="overlay-actions">
        <button type="button" class="btn" data-cancel>${t('取消')}</button>
        <button type="button" class="btn gold" data-ok>${editing ? t('保存') : t('立项')}</button>
      </div>
    </div>`;
    this.mount = opts.mount || document.body;
    this.mount.appendChild(el);
    this.el = el;
    this.#verRow(null);
    for (const v of vers) this.#verRow(v);
    if (!editing) {
      // the key follows the name until the key is touched by hand
      const name = el.querySelector('[data-name]');
      const key = el.querySelector('[data-key]');
      let keyTouched = false;
      key.addEventListener('input', () => { keyTouched = true; });
      name.addEventListener('input', () => {
        if (!keyTouched) key.value = slugify(name.value);
      });
      // 风险确认随字段作废：key/名称/工作区一动，亮过的预检清单收回、
      // 按钮复原——下一次提交重新预检（确认只对确认时的那份输入有效）
      for (const field of [key, name, el.querySelector('[data-workspace]')]) {
        field?.addEventListener('input', () => this.#unack());
      }
      // 总开关（两级门顶层）：关掉即收起引导细项——提交载荷带
      // git_disabled，#gitPlan() 返回 null（服务端引导整档跳过）。
      const gitOn = el.querySelector('[data-git-on]');
      const gitSub = el.querySelector('[data-git-sub]');
      if (gitOn && gitSub) {
        const syncGitSub = () => { gitSub.hidden = !gitOn.checked; };
        gitOn.addEventListener('change', syncGitSub);
        syncGitSub();
      }
      // Git 引导的自动填写（v2.14）：工作区一动（防抖 500ms）即探测仓库
      // ——有仓自动填当前分支做基线，无仓亮「可自动 init」，外层仓库亮
      // 红线停用。基线一旦手填，探测不再覆盖（手填优先）。
      this.gitOuter = false;          // 外层仓库形态：自动引导整体停用
      this.baseTouched = false;       // 基线分支被手填过（自动填让位）
      let probeTimer = 0;
      const base = el.querySelector('[data-git-base]');
      base?.addEventListener('input', () => { this.baseTouched = true; });
      el.querySelector('[data-workspace]')?.addEventListener('input', () => {
        clearTimeout(probeTimer);
        probeTimer = setTimeout(() => { this.probeWorkspace(); }, 500);
      });
    }
    this.acked = false;      // 预检 warn 的确认位（true＝已点过「已了解风险」）
    this.picker = null; // the workspace browser while it is up
    this.onDocKey = (ev) => {
      // Escape belongs to the picker while it is open — the form waits its turn
      if (ev.key === 'Escape' && !this.picker) this.close();
    };
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      if (ev.target.closest('[data-close]') || ev.target.closest('[data-cancel]')) this.close();
      if (ev.target.closest('[data-add-ver]')) this.#verRow(null);
      if (ev.target.closest('[data-ver-del]')) ev.target.closest('.pn-ver').remove();
      if (ev.target.closest('[data-browse]')) this.#openPicker();
      if (ev.target.closest('[data-ok]')) this.submit();
    });
    el.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) this.submit();
    });
    document.addEventListener('keydown', this.onDocKey);
    (el.querySelector('[data-key]') || el.querySelector('[data-name]')).focus();
  }

  /** One version editor row (name + goal + end). v=null appends an empty row.
   * The row remembers its original start_ts — a save must not slide an
   * existing version's window start to "now". */
  #verRow(v) {
    const wrap = this.el.querySelector('[data-vers]');
    const row = document.createElement('div');
    row.className = 'pn-ver';
    row._start = v?.start_ts || 0;
    row.innerHTML =
      `<input class="input" data-vname maxlength="48" placeholder="${t('版本名（可空行忽略）')}" value="${esc(v?.name || '')}">` +
      `<input class="input" data-vgoal maxlength="200" placeholder="${t('版本目标（可空）')}" value="${esc(v?.goal || '')}">` +
      `<input class="input" type="datetime-local" data-vend value="${unixToDT(v?.end_ts || 0)}" title="${t('版本截止（可空）')}">` +
      `<button type="button" class="icon-btn" data-ver-del title="${t('移除该版本')}">` + icon('close') + '</button>';
    wrap.appendChild(row);
  }

  #collectVersions() {
    const out = [];
    for (const row of this.el.querySelectorAll('.pn-ver')) {
      const name = row.querySelector('[data-vname]').value.trim();
      if (!name) continue;
      out.push({
        name,
        start_ts: row._start || Math.round(Date.now() / 1000),
        end_ts: dtToUnix(row.querySelector('[data-vend]').value),
        goal: row.querySelector('[data-vgoal]').value.trim(),
      });    }
    return out;
  }

  /** 提交（公开面：按钮/⌘Enter 的落点，dom 冒烟直驱同一条路）。创建态
   *  先过预检门——error 内联拦下、warn 亮清单要一次显式确认；编辑态与
   *  预检不可达时直走 onSubmit（服务端仍把关）。 */
  async submit() {
    const el = this.el;
    const p = this.opts.project;
    const status = el.querySelector('[data-status]');
    status.classList.remove('err');
    const name = el.querySelector('[data-name]').value.trim();
    if (!name) return this.#err(status, t('项目名称必填'));
    const workspace = p?.status === 'active' ? p.workspace : el.querySelector('[data-workspace]').value.trim();
    if (!p && !KEY_RE.test(el.querySelector('[data-key]').value.trim())) {
      return this.#err(status, t('项目 key 需匹配 [a-z0-9_-]{1,32}（小写字母/数字/连字符/下划线）'));
    }
    if (!p || p.status !== 'active') {
      if (!/^\/|^[A-Za-z]:[\\/]/.test(workspace)) return this.#err(status, t('工作区必须是绝对路径（以 / 开头）——成员会话的出生地'));
    }
    const payload = p
      ? {
          name,
          desc: el.querySelector('[data-desc]').value.trim(),
          ...(p.status === 'active' ? {} : { workspace }),
          versions: this.#collectVersions(),
          git_disabled: !this.#gitOn(),
        }
      : {
          key: el.querySelector('[data-key]').value.trim(),
          name,
          desc: el.querySelector('[data-desc]').value.trim(),
          workspace,
          versions: this.#collectVersions(),
          git_plan: this.#gitPlan(),
          git_disabled: !this.#gitOn(),
        };
    if (!p) {
      const gate = await this.#preflightGate(payload, status);
      if (!gate) return; // error 内联已亮／warn 等待确认——都没到放行的时候
    }
    const okBtn = el.querySelector('[data-ok]');
    okBtn.disabled = true;
    okBtn.classList.add('loading'); // 按钮加载态（数据录入·按钮篇）：转圈＋指针没收
    status.textContent = p ? t('保存中…') : t('立项中…');
    try {
      await this.opts.onSubmit(payload);
      this.close();
    } catch (err) {
      okBtn.disabled = false;
      okBtn.classList.remove('loading');
      this.#unack(); // 失败收摊回初始面：确认位清零，重走预检
      this.#err(status, err.message || String(err));
    }
  }

  /** 立项预检门：提交前把服务端风险检查的结果亮在表单里。error（key/
   *  目录冲突等必拒项）拼成一行内联拦下；warn（目录不存在、主目录、
   *  嵌套等注意事项）第一次点只亮清单＋改键「已了解风险，继续立项」，
   *  再点一次才放行。预检面打不通时不拦人——正门 POST 仍由服务端把关，
   *  预检只是提前亮。@returns {Promise<boolean>} 是否放行本次提交 */
  async #preflightGate(payload, status) {
    let pf = null;
    try {
      pf = await preflightProject({ key: payload.key, name: payload.name, workspace: payload.workspace });
    } catch {
      return true; // 预检不可达：照走正门（服务端仍把关）
    }
    const findings = Array.isArray(pf?.findings) ? pf.findings : [];
    const errors = findings.filter((f) => f && f.level === 'error').map((f) => f.text).filter(Boolean);
    if (errors.length) {
      this.#err(status, errors.join('；'));
      return false;
    }
    const warns = findings.filter((f) => f && f.level === 'warn').map((f) => f.text).filter(Boolean);
    if (!warns.length) {
      this.#unack(); // 干净通过：顺手收掉上一轮可能亮过的清单
      return true;
    }
    if (this.acked) return true; // 同一份输入已显式确认过——放行
    this.acked = true;
    const risks = this.el.querySelector('[data-risks]');
    risks.hidden = false;
    risks.innerHTML = `<strong>${t('风险与注意事项（来自预检）')}</strong>` +
      warns.map((x) => `<div class="pn-risk">${icon('warn', 14)}<span>${esc(x)}</span></div>`).join('');
    this.el.querySelector('[data-ok]').textContent = t('已了解风险，继续立项');
    status.classList.remove('err');
    status.textContent = t('确认无误后，再点一次按钮完成立项');
    return false;
  }

  /** 撤销风险确认：相关字段一动（或提交失败收摊），亮过的清单收回、
   *  按钮复原——确认只对确认时的那份输入有效。 */
  #unack() {
    this.acked = false;
    if (!this.el) return;
    const risks = this.el.querySelector('[data-risks]');
    if (risks && !risks.hidden) {
      risks.hidden = true;
      risks.innerHTML = '';
    }
    const okBtn = this.el.querySelector('[data-ok]');
    if (okBtn) okBtn.textContent = this.opts.project ? t('保存') : t('立项');
  }

  /** Git 引导计划（提交载荷的一角）：总开关关着或外层仓库形态下整体
   *  置空——前者是用户明说不引导，后者服务端本就拒绝自动操作（红线
   *  双保险：前端停用＋服务端拒绝）。 */
  #gitPlan() {
    if (!this.el || this.gitOuter) return null;
    if (this.el.querySelector('[data-git-on]') && !this.el.querySelector('[data-git-on]').checked) return null;
    const chk = (sel) => !!(this.el.querySelector(sel)?.checked);
    const base = (this.el.querySelector('[data-git-base]')?.value || '').trim();
    return {
      init_if_missing: chk('[data-git-init]'),
      base_branch: base,
      auto_seat_branch: chk('[data-git-seat]'),
    };
  }

  /** 总开关现值（true＝git 版本管理开启）：编辑态与创建态共用。 */
  #gitOn() {
    const node = this.el?.querySelector('[data-git-on]');
    return !node || node.checked;
  }

  /** 探测工作区的 git 事实并自动填写（公开面：工作区输入的防抖落点，
   *  dom 冒烟直驱同一条路）。有仓且根即工作区 → 基线自动填当前分支；
   *  无仓 → 亮「可自动 init」；外层仓库 → 亮红线并停用整个引导段。
   *  探测失败保持现状（提交前的预检还会再探一遍，服务端才是把关人）。 */
  async probeWorkspace() {
    if (!this.el) return;
    const el = this.el;
    const ws = (el.querySelector('[data-workspace]')?.value || '').trim();
    const state = el.querySelector('[data-git-state]');
    if (!ws || !/^\/|^[A-Za-z]:[\\/]/.test(ws)) {
      if (state) state.textContent = '';
      return;
    }
    let git = null;
    try {
      const pf = await preflightProject({
        key: (el.querySelector('[data-key]')?.value || '').trim(),
        name: (el.querySelector('[data-name]')?.value || '').trim(),
        workspace: ws,
      });
      git = pf?.git || null;
    } catch {
      return; // 探测面打不通：保持现状（预检/提交时还有把关）
    }
    if (!this.el || !state) return; // 探测期间表单已关
    const setGitDisabled = (off) => {
      for (const sel of ['[data-git-init]', '[data-git-base]', '[data-git-seat]']) {
        const node = el.querySelector(sel);
        if (node) node.disabled = off;
      }
    };
    this.gitOuter = !!(git && git.outer_repo);
    if (this.gitOuter) {
      state.textContent = t('位于外层仓库内——自动引导停用（不动别的项目）');
      state.classList.add('warn');
      setGitDisabled(true);
      return;
    }
    state.classList.remove('warn');
    setGitDisabled(false);
    if (git && git.is_repo && git.branch) {
      state.textContent = t('检测到 git 仓库（当前分支 {branch}）', { branch: git.branch });
      if (!this.baseTouched) {
        const base = el.querySelector('[data-git-base]');
        if (base) base.value = git.base_fill || git.branch;
      }
    } else {
      state.textContent = t('不是 git 仓库——可勾选自动 init，或留空稍后手工建仓');
    }
  }

  /**
   * The workspace address chooser: a small overlay stacked above the
   * form, listing the loopback server's directory tree (the browser
   * cannot hand out absolute local paths, so the server does the
   * reading). Clicking a directory enters it; 选这个目录 confirms the
   * currently shown path back into the form's field.
   */
  #openPicker() {
    if (this.picker) return;
    const input = this.el.querySelector('[data-workspace]');
    const overlay = document.createElement('div');
    overlay.className = 'overlay fp';
    // 嵌套对话框（对话框篇）：二级与一级同宽——立项卡是 w-md（600px）
    overlay.innerHTML = `<div class="overlay-card w-md" role="dialog" aria-modal="true" aria-label="${t('选择工作区目录')}">
      <div class="overlay-head"><strong>${t('选择工作区目录')}</strong>
        <button type="button" class="icon-btn" data-close title="${t('关闭')}">${icon('close', 14)}</button></div>
      <div class="fp-bar">
        <button type="button" class="btn small" data-home title="${t('回到主目录')}">${icon('home', 14)}</button>
        <button type="button" class="btn small" data-up title="${t('上一级')}">${icon('up', 14)}</button>
        <code class="fp-path" data-path></code>
      </div>
      <div class="fp-list" data-list><div class="fp-empty">${t('读取中…')}</div></div>
      <div class="overlay-status" data-status></div>
      <div class="overlay-actions">
        <button type="button" class="btn" data-cancel>${t('取消')}</button>
        <button type="button" class="btn gold" data-pick>${t('选这个目录')}</button>
      </div>
    </div>`;
    this.mount.appendChild(overlay);
    const cur = { path: '', parent: '', home: '' };
    const listEl = overlay.querySelector('[data-list]');
    const status = overlay.querySelector('[data-status]');
    const onKey = (ev) => { if (ev.key === 'Escape') this.picker?.close(); };
    this.picker = {
      el: overlay,
      close: () => {
        document.removeEventListener('keydown', onKey);
        dismiss(overlay);
        this.picker = null;
      },
    };
    // base + name, keeping a trailing volume/root separator intact; the
    // server's filepath.Clean normalizes the mixed separators on Windows
    const joinPath = (base, name) =>
      (base.endsWith('/') || base.endsWith('\\') ? base : base + '/') + name;
    const go = async (p) => {
      status.classList.remove('err');
      status.textContent = t('读取中…');
      try {
        const d = await listDir(p);
        cur.path = d.path; cur.parent = d.parent; cur.home = d.home || '';
        overlay.querySelector('[data-path]').textContent = cur.path || '…';
        overlay.querySelector('[data-up]').disabled = !cur.parent;
        overlay.querySelector('[data-home]').disabled = !!cur.home && cur.path === cur.home;
        listEl.innerHTML = d.dirs.length
          ? d.dirs.map((n) => `<button type="button" class="fp-item" data-dir="${esc(n)}">${icon('folder', 14)} ${esc(n)}</button>`).join('')
          : `<div class="fp-empty">${t('此目录下没有子目录——可以直接「选这个目录」')}</div>`;
        status.textContent = '';
      } catch (err) {
        if (!cur.path) return go(''); // the typed starting path was unusable — land home
        status.textContent = err.message || String(err);
        status.classList.add('err');
      }
    };
    overlay.addEventListener('click', (ev) => {
      if (ev.target === overlay) { this.picker?.close(); return; }
      if (ev.target.closest('[data-close]') || ev.target.closest('[data-cancel]')) { this.picker?.close(); return; }
      if (ev.target.closest('[data-home]')) go('');
      if (ev.target.closest('[data-up]')) go(cur.parent);
      const dir = ev.target.closest('[data-dir]');
      if (dir) go(joinPath(cur.path, dir.dataset.dir));
      if (ev.target.closest('[data-pick]')) {
        input.value = cur.path;
        this.picker?.close();
      }
    });
    document.addEventListener('keydown', onKey);
    go(input.value.trim()); // start where the typed path points
  }

  #err(status, text) {
    status.textContent = text;
    status.classList.add('err');
  }

  close() {
    if (!this.el) return;
    if (this.picker) this.picker.close();
    dismiss(this.el);
    this.el = null;
    document.removeEventListener('keydown', this.onDocKey);
  }
}

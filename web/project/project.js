// project.js — the project board's container (v2 P4-d, frontend 稿
// §4.3): one project selector (the same active-room set the people
// board drinks from; the viewed project follows the room you're in —
// 跟房落位 on entry + on live switch while visible, 跨板跳转除外),
// four inner tabs (概览 / 需求 / 任务台账 / 排期甘特
// —— 概览 is the Feishu-style landing dashboard, overview.js), the
// plan-review banner across all tabs, and one debounced refresh cycle
// driven by the room book's event taps (task/plan frames). The board
// owns no protocol knowledge — reads go through wire/api, task and
// plan verbs go through the viewed project's owner write face,
// receipts arrive as toasts from app.js. Three-state contracts
// (empty/loading/error-retry) live in each view, not here.

import { getProjects, getPlan, getAgents, getStaffing, postProject, postProjectAction, patchProject, postProjectDismiss, postProjectReset, postProjectDelete } from '../wire/api.js';
import { LobbyKey, Msg } from '../wire/wire.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { OverviewView } from './overview.js';
import { ReqsView } from './reqs.js';
import { TaskBoardView } from './board.js';
import { TaskNewForm } from './tasknew.js';
import { ProjectNewForm } from './projectnew.js';
import { PlanReview } from './planreview.js';
import { GanttChart } from '../gantt/gantt.js';
import { GitView } from './gitview.js';

const TABS = [
  { id: 'overview', label: t('概览') },
  { id: 'reqs', label: t('需求') },
  { id: 'board', label: t('任务台账') },
  { id: 'gantt', label: t('排期甘特') },
  { id: 'git', label: t('版本管理') },
];

// 选择器与信息卡的状态语言（后端 draft|active|archived 的前端同源）。
const STATUS_LABEL = { draft: t('草稿'), active: '', archived: t('已归档') };

export class ProjectBoard {
  // 跨板跳转（聊天「去审批」/跳转条）指名的落位键：revealed 消费即清
  // ——进门跟房给显式跳转让位，跳完离场再进门才回到跟房口径。
  #jumpIntent = null;

  /**
   * @param {HTMLElement} root #board-project (the .board container)
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {import('../ui/toast.js').Toast} toast
   * @param {{draftTo: (key: string, text: string) => void}} hooks cross-board bridges (the gantt's 提要求 pre-aims the chat composer)
   */
  constructor(root, book, toast, hooks) {
    this.book = book;
    this.toast = toast;
    this.hooks = hooks || { draftTo: () => {} };
    this.projects = [];        // ProjectSummary[] — the selector's source
    this.key = LobbyKey;       // the viewed project (= its room key)
    this.tab = 'overview';
    this.visible = false;      // 板块在屏上（revealed/conceal 维护）——换房跟走只吃可见时
    this.pendingPlan = null;   // Plan|null — the review-slot read
    this.pendingAttaches = []; // create→attach 链队列（tasknew 的补挂排期/项目）
    this.archivedNames = new Set(); // /agents?archived=1 — 牛马管理的全局离职真相
    this.offboardNames = new Set(); // 本项目编制表的 offboard 行（已离编）
    this.refreshTimer = 0;

    root.innerHTML =
      '<div class="project-wrap">' +
        '<div class="project-main">' +
          '<div class="people-tabs" role="tablist">' +
            `<span class="proj-pick">${t('项目')} <select class="input" data-proj-select></select></span>` +
            TABS.map((t) => `<button type="button" class="ptab${t.id === this.tab ? ' active' : ''}" data-tab="${t.id}" role="tab">${t.label}</button>`).join('') +
            '<span class="tabs-spacer"></span>' +
            `<button type="button" class="btn small" data-cta-newproj>${t('+ 立项')}</button>` +
            `<button type="button" class="btn small" data-cta-newtask>${t('+ 新建任务')}</button>` +
            `<button type="button" class="btn small gold" data-cta-newreq>${t('+ 录入需求')}</button>` +
          '</div>' +
          '<div class="plan-banner" data-plan-banner hidden></div>' +
          '<div class="project-pane" id="ppane-overview"></div>' +
          '<div class="project-pane" id="ppane-reqs" hidden></div>' +
          '<div class="project-pane" id="ppane-board" hidden></div>' +
          '<div class="project-pane project-pane-gantt" id="ppane-gantt" hidden></div>' +
          '<div class="project-pane" id="ppane-git" hidden></div>' +
        '</div>' +
      '</div>';
    this.els = {
      select: root.querySelector('[data-proj-select]'),
      banner: root.querySelector('[data-plan-banner]'),
      panes: {
        overview: root.querySelector('#ppane-overview'),
        reqs: root.querySelector('#ppane-reqs'),
        board: root.querySelector('#ppane-board'),
        gantt: root.querySelector('#ppane-gantt'),
        git: root.querySelector('#ppane-git'),
      },
      tabs: root.querySelector('.people-tabs'),
    };
    this.els.tabs.addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-tab]');
      if (btn) this.switchTab(btn.dataset.tab);
      if (ev.target.closest('[data-cta-newreq]')) this.newReq();
      if (ev.target.closest('[data-cta-newtask]')) this.newTask();
      if (ev.target.closest('[data-cta-newproj]')) this.newProject();
    });
    this.els.select.addEventListener('change', () => this.selectKey(this.els.select.value));
    this.els.banner.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-plan-review]')) this.openPlanReview();
    });

    this.overview = new OverviewView(this.els.panes.overview, this);
    this.els.panes.overview.addEventListener('click', (ev) => this.overview.onClick(ev));
    this.reqs = new ReqsView(this.els.panes.reqs, this);
    this.board = new TaskBoardView(this.els.panes.board, this);
    this.gantt = new GanttChart(this.els.panes.gantt, this);
    this.git = new GitView(this.els.panes.git, this);
    this.planReview = new PlanReview(this);
    this.overview.renderIdle();
    this.reqs.renderEmptyIdle();
    this.board.renderIdle();
    this.git.renderIdle();
  }

  /** Board entry (hash route or first paint): load the selector, then refresh.
   *  进门跟房：人在哪间项目房，板块落哪个项目（跨板跳转占位优先）。 */
  async revealed() {
    this.visible = true;
    await this.#loadProjects(); // 跟房落位前选择器先就位——choices 空着对不上房
    if (this.#jumpIntent) {
      const want = this.#jumpIntent;
      this.#jumpIntent = null;
      if (want !== this.key) this.selectKey(want);
    } else {
      this.#followRoom();
    }
    await this.refresh();
  }

  /** 离开项目管理板块即收起任务抽屉——详情不跟着人飘到别的板块。 */
  conceal() {
    this.visible = false;
    this.board?.drawer?.close();
    this.planReview?.close?.();
  }

  /** 项目管理跟办公室走：人在哪间房，查看项目就落哪个——大厅与项目
   *  房一视同仁（回大厅就落大厅；设置中心 project() 的同款优先级）。
   *  @returns {boolean} 落位是否翻面（翻面才需要补一次 refresh） */
  #followRoom() {
    const cur = this.book.current;
    if (!cur || cur === this.key) return false;
    if (!this.projectChoices().some((p) => p.key === cur)) return false;
    this.key = cur;
    this.#paintSelector();
    return true;
  }

  /** 换房（app.js book.onChange 'switch' 转来）：可见才跟（终端板
   *  onSwitch 同款纪律）——隐藏时零动作，进门 revealed() 重新落位。 */
  onRoomSwitch() {
    if (this.visible && this.#followRoom()) this.refresh();
  }

  /** 项目空间面板的指名落位（v2.12）：草稿/归档项目点行进板块——与
   *  聊天跨板跳转（jumpTo/reviewPending）同一优先级：进门跟房给指名
   *  跳转让位，revealed() 消费即清。 */
  seedInspect(key) {
    this.#jumpIntent = key;
  }

  /** The selector's choices: the lobby, then active projects (rooms),
   * then drafts (未开张) and archived (封存可读) — the management face
   * must reach every lifecycle state; the room switcher still lists
   * active rooms only. */
  projectChoices() {
    const rank = { active: 0, draft: 1, archived: 2 };
    const rest = this.projects
      .filter((p) => p.key !== LobbyKey)
      .sort((a, b) => (rank[a.status] ?? 3) - (rank[b.status] ?? 3) || a.key.localeCompare(b.key));
    return [
      { key: LobbyKey, name: t('Niuma_Studio'), status: 'active' },
      ...rest.map((p) => ({ key: p.key, name: p.name, status: p.status })),
    ];
  }

  /** The viewed project's registry row (raw store summary), if loaded. */
  currentProject() {
    return this.projects.find((p) => p.key === this.key) || null;
  }

  roomName(key) {
    return this.projectChoices().find((p) => p.key === key)?.name || key;
  }

  selectKey(key) {
    if (!this.projectChoices().some((p) => p.key === key)) return;
    this.key = key;
    this.#paintSelector();
    this.refresh();
  }

  switchTab(tab) {
    if (!TABS.some((t) => t.id === tab)) return;
    this.tab = tab;
    for (const t of TABS) {
      this.els.panes[t.id].hidden = t.id !== tab;
      this.els.tabs.querySelector(`[data-tab="${t.id}"]`)?.classList.toggle('active', t.id === tab);
    }
    this.#loadTab();
  }

  /** Cross-board refresh tap (event frames, owner receipts, after local writes). */
  requestRefresh() {
    clearTimeout(this.refreshTimer);
    this.refreshTimer = setTimeout(() => { this.refresh(); }, 400);
  }

  /** Reload the review slot + the visible tab's data. */
  async refresh() {
    this.#loadProjects();
    await this.#loadDeparted();
    try {
      const rep = await getPlan(this.key);
      this.pendingPlan = rep.plan || null;
    } catch { this.pendingPlan = null; }
    this.#paintBanner();
    this.#loadTab();
  }

  #loadTab() {
    if (this.tab === 'overview') this.overview.reload();
    if (this.tab === 'reqs') this.reqs.reload();
    if (this.tab === 'board') this.board.reload();
    if (this.tab === 'gantt') this.gantt.reload();
    if (this.tab === 'git') this.git.reload();
  }

  // The departure truth this board consumes (the sync with 牛马管理 the
  // task-derived name displays were missing): /agents?archived=1 names
  // the globally departed (离职归档 — the people board's 归档 tab), and
  // /p/{key}/staffing names this project's offboard rows (已离编 — left
  // this seat, maybe still on staff elsewhere). Both reads degrade to
  // "no annotation": an unreachable face must not blank the board, only
  // lose the tags; offboard resets per key so a project switch never
  // carries the previous project's rows over.
  async #loadDeparted() {
    const [archived, staffing] = await Promise.allSettled([getAgents(true), getStaffing(this.key)]);
    if (archived.status === 'fulfilled') {
      this.archivedNames = new Set(archived.value.map((a) => a.name));
    }
    this.offboardNames = staffing.status === 'fulfilled'
      ? new Set((staffing.value.rows || []).filter((r) => r.state === 'offboard').map((r) => r.person))
      : new Set();
  }

  /**
   * 离职标注：已离职（全局归档）优先于已离编（本项目编制行）；在岗或
   * 未知返回 ''——名字照旧裸显。台账里出现过的负责人不因离职消失
   * （任务还在），但每一处人名都带上说明。
   * @param {string} name @returns {string} '' | '已离职' | '已离编'
   */
  departTag(name) {
    if (!name) return '';
    if (this.archivedNames.has(name)) return t('已离职');
    if (this.offboardNames.has(name)) return t('已离编');
    return '';
  }

  #paintSelector() {
    const choices = this.projectChoices();
    if (!choices.some((p) => p.key === this.key)) this.key = LobbyKey;
    this.els.select.innerHTML = choices
      .map((p) => {
        const st = STATUS_LABEL[p.status] ? ` · ${STATUS_LABEL[p.status]}` : '';
        return `<option value="${esc(p.key)}"${p.key === this.key ? ' selected' : ''}>${esc(p.name)}${t('（{v}）', { v: `${esc(p.key)}${st}` })}</option>`;
      })
      .join('');
  }

  #paintBanner() {
    const el = this.els.banner;
    const p = this.pendingPlan;
    if (!p) {
      el.hidden = true;
      el.innerHTML = '';
      return;
    }
    el.hidden = false;
    el.innerHTML =
      `<span class="plan-banner-dot"></span>` +
      `<strong>${t('1 份提案待审')}</strong>` +
      `<span class="plan-banner-meta">${esc(p.id)}${t('「{v}」', { v: esc(p.title) })}· ${t('{n} 条任务', { n: p.tasks?.length || 0 })}${p.req ? ` · ${t('需求')} ${esc(p.req)}` : ''}</span>` +
      `<button type="button" class="btn small gold" data-plan-review>${t('开始审阅')}</button>`;
  }

  /** The 需求 tab's empty-state CTA and header button both land here. */
  newReq() {
    if (!this.#requireActive()) return;
    this.switchTab('reqs');
    this.reqs.openForm();
  }

  /** 生命周期守卫：非 active 项目没有可写的房间，管理表单干脆不开——
   * 让用户填完才在提交时报错是刁难人。 */
  #requireActive() {
    const cur = this.currentProject();
    if (!cur || cur.status === 'active') return true;
    this.toast.show(cur.status === 'draft'
      ? t('项目尚未开张（草稿）——先在概览页开张，才有可写的房间')
      : t('项目已归档封存——台账只读，写操作不可用'), 'err');
    return false;
  }

  /** 立项：一个 draft 项目（POST /projects）。成功后选择器收下它、
   * 概览的项目信息卡给出「开张」入口。 */
  newProject() {
    new ProjectNewForm({
      onSubmit: async (payload) => {
        const p = await postProject({ ...payload, by: this.ownerName || undefined });
        // git 引导回执（v2.14）：失败如实亮；新建仓库给一条 ok。
        const boot = p.git_boot;
        if (boot && !boot.ok) {
          this.toast.show(t('立项 git 引导未全成：{why}', { why: (boot.notes || []).join('；') }), 'err');
        } else if (boot && boot.initiated) {
          this.toast.show(t('已自动初始化 git 仓库（基线 {base}）', { base: boot.base_branch || 'main' }), 'ok');
        }
        this.toast.show(t('已立项：{name}（{key}，草稿）——开张后生成项目房', { name: p.name, key: p.key }), 'ok');
        await this.#loadProjects(); // 选择器先收下新 key，selectKey 才肯切
        this.selectKey(p.key);
        this.switchTab('overview');
      },
    });
  }

  /** 编辑当前项目的内容 envelope（PATCH /p/{key}）。 */
  editProject() {
    const cur = this.currentProject();
    if (!cur) return;
    new ProjectNewForm({
      project: cur,
      onSubmit: async (patch) => {
        await patchProject(cur.key, patch);
        this.toast.show(t('已保存：{key}', { key: cur.key }), 'ok');
        await this.refresh();
      },
    });
  }

  /** 生命周期一步（开张/归档/复活，POST /p/{key}/lifecycle）。房间集
   * 的后果（开房/撤房/离编）在服务端；前端只管刷新两面（选择器与侧
   * 栏房间集）。 */
  async projectAction(action) {
    const cur = this.currentProject();
    if (!cur || cur.key === LobbyKey) return;
    try {
      const p = await postProjectAction(cur.key, action);
      const what = action === 'activate' ? t('已开张')
        : action === 'archive' ? t('已归档封存（编制离编、房间已撤）') : t('已复活开张');
      this.toast.show(t('项目 {name}（{key}）{what}', { name: p.name, key: p.key, what }), 'ok');
      await this.book.refreshRooms(); // 侧栏房间集跟着生命周期走
      await this.refresh();
    } catch (err) {
      this.toast.show(err.message || String(err), 'err');
    }
  }

  /** 清退项目组成员（POST /p/{key}/dismiss，概览卡危险区）：服务端同步
   * 踢房＋删编制行＋删档案＋ZCode 深清，历史保留——前端只需刷新两拍
   * （成员面板与花名册都按存储重画）。 */
  async dismissMembers() {
    const cur = this.currentProject();
    if (!cur || cur.key === LobbyKey) return;
    let r;
    try {
      r = await postProjectDismiss(cur.key);
    } catch {
      this.toast.show(t('连接失败——未执行任何清退'), 'err');
      return;
    }
    if (!r.ok) {
      this.toast.show(r.data?.error || t('清退被拒（HTTP {status}）', { status: r.status }), 'err');
      return;
    }
    this.toast.show(r.data?.note || t('成员已全部清退'), 'ok');
    await this.refresh();
    setTimeout(() => this.refresh(), 1500); // 踢房帧广播后成员面板的第二拍
  }

  /** 重置项目（POST /p/{key}/reset，概览卡危险区）：成功后整页重载——
   * 聊天流、任务台账、黑板全还持着旧项目的帧，重载是唯一诚实的收尾
   * （工厂重置同款纪律，只是不换进程）。 */
  async resetProject() {
    const cur = this.currentProject();
    if (!cur || cur.key === LobbyKey) return;
    let r;
    try {
      r = await postProjectReset(cur.key);
    } catch {
      this.toast.show(t('连接失败——未执行任何重置'), 'err');
      return;
    }
    if (!r.ok) {
      this.toast.show(r.data?.error || t('重置被拒（HTTP {status}）', { status: r.status }), 'err');
      return;
    }
    this.toast.show(r.data?.note || t('项目已重置'), 'ok');
    setTimeout(() => location.reload(), 800); // 让回执的 toast 落地一眼
  }

  /** 删除项目（POST /p/{key}/delete，概览卡/项目空间的除名入口）：服务
   *  端同步清人＋清台账＋清房间档案＋除名，工作区目录不动。成功后刷
   *  两拍：房册（侧栏/项目空间跟着少一行）与本板——选择器自愈落回大
   *  厅（#paintSelector 的缺省回退），不重载窗口：除名不动任何还在屏
   *  上的活房，只是台账各页回到「大厅无数据」的诚实空态。 */
  async deleteProject() {
    const cur = this.currentProject();
    if (!cur || cur.key === LobbyKey) return;
    let r;
    try {
      r = await postProjectDelete(cur.key);
    } catch {
      this.toast.show(t('连接失败——未执行任何删除'), 'err');
      return;
    }
    if (!r.ok) {
      this.toast.show(r.data?.error || t('删除被拒（HTTP {status}）', { status: r.status }), 'err');
      return;
    }
    this.toast.show(r.data?.note || t('项目已删除'), 'ok');
    await this.book.refreshRooms();
    await this.refresh();
  }

  /**
   * 新建任务 (v2 P6 merge — the pixel board's creation page, Feishu
   * 建卡式补全): one task_create over the viewed project's owner face;
   * the create→attach chain hangs the fresh task on the viewed project
   * and merges the form's schedule pair into the SAME follow-up
   * task_update patch (the wire's create carries no project/schedule).
   */
  newTask() {
    if (!this.#requireActive()) return;
    new TaskNewForm({
      projectName: this.roomName(this.key),
      roster: this.roster(),
      tagOf: (n) => this.departTag(n),
      onSubmit: (draft) => {
        const extra = {};
        if (draft.start) extra.start = draft.start;
        if (draft.end) extra.end = draft.end;
        if (draft.priority) extra.priority = draft.priority;
        const ok = this.frameTo(
          { type: Msg.TaskCreate, task: { title: draft.title, desc: draft.desc, assignee: draft.assignee } },
          t('新建 {title}', { title: draft.title }));
        // 队列而非单槽：连续建两单时，先到的收据不该被第二单顶掉
        if (ok) this.pendingAttaches.push({ title: draft.title, key: this.key, extra });
        this.switchTab('board');
      },
    });
  }

  /**
   * The create receipt (a Task frame straight back over the owner
   * face): attach the fresh task to the project it was created under
   * and apply the form's schedule — one combined task_update. app.js
   * routes every owner Task frame here; non-creates just pass through
   * (they still feed the drawer's live repaint first).
   * @param {import('../wire/wire.js').Frame} f
   */
  acceptTaskReceipt(f) {
    this.board.drawerReceipt(f);
    if (!f.task?.id) return;
    const i = this.pendingAttaches.findIndex((p) => p.title === f.task.title);
    if (i < 0) return;
    const [p] = this.pendingAttaches.splice(i, 1);
    if (f.event === 'denied') return;
    const patch = {};
    if (p.key !== LobbyKey && (f.task.project || '') !== p.key) patch.project = p.key;
    for (const [k, v] of Object.entries(p.extra || {})) if (v) patch[k] = v;
    if (Object.keys(patch).length) {
      const what = [patch.project ? t('挂到 {v}', { v: p.key }) : '', patch.start ? t('始于 {v}', { v: patch.start }) : '',
        patch.end ? t('止于 {v}', { v: patch.end }) : '', patch.priority ? t('优先级 {v}', { v: patch.priority }) : '']
        .filter(Boolean).join(t('、'));
      this.frameTo({ type: Msg.TaskUpdate, task_id: f.task.id, patch }, `${f.task.id} ${what}`);
    }
  }

  /** Banner / plan(submitted) event card entry into the review overlay. */
  openPlanReview() {
    if (!this.pendingPlan) {
      this.toast.show(t('当前没有待审提案（可能已审结或被新提案取代）'), 'info');
      return;
    }
    this.planReview.open(this.pendingPlan);
  }

  /**
   * 聊天「提案 submitted」卡的 去审批 落点：切到提案自己的项目
   * （plan.project_key——聊天里看到的提案不一定挂在当前查看的项目
   * 上），待审槽刷新落地后再开审阅浮层——浮层锁的是待审件，等一拍
   * 才不会拿上一个项目的旧提案充数。
   * @param {string} key the plan's project key ('' = keep the viewed one)
   */
  async reviewPending(key) {
    this.#jumpIntent = key || null; // 同一跳 hash 换来的 revealed 让位给指名项目
    await this.#loadProjects();
    if (key && key !== this.key && this.projectChoices().some((p) => p.key === key)) {
      this.key = key;
      this.#paintSelector();
    }
    await this.refresh();
    this.openPlanReview();
  }

  /**
   * 聊天快捷入口（拆解/排期/需求环节的消息跳转条）的跨板落点：
   * 审提案复用 reviewPending 的全套（项目落位＋待审浮层）；看排期/
   * 看需求只切页签——甘特与需求列表都是只读镜面，照当前项目落位即
   * 可（projectKey 探不到时保持当前查看的项目）。
   * @param {{kind:'plan'|'gantt'|'req', id:string}} offer
   * @param {string|null} projectKey
   */
  async jumpTo(offer, projectKey) {
    this.#jumpIntent = projectKey || null; // 同 reviewPending：进门跟房给指名跳转让位
    if (offer.kind === 'plan') return this.reviewPending(projectKey || '');
    await this.#loadProjects();
    if (projectKey && projectKey !== this.key && this.projectChoices().some((p) => p.key === projectKey)) {
      this.key = projectKey;
      this.#paintSelector();
    }
    await this.refresh();
    this.switchTab(offer.kind === 'gantt' ? 'gantt' : 'reqs');
  }

  /**
   * 需求行的版本管理跳入口（v2.7）：切到版本管理页签并按该需求聚
   * 焦（绑定分支＋提及提交的反查小节）。
   * @param {string} reqKey r_NN
   */
  jumpToGit(reqKey) {
    this.switchTab('git');
    this.git.focusReq(reqKey);
  }

  /** One task/plan verb over the viewed project's owner write face.
   * @param {import('../wire/wire.js').Frame} frame @param {string} label */
  frameTo(frame, label) {
    const cur = this.currentProject();
    if (cur && cur.status !== 'active') {
      this.toast.show(cur.status === 'draft'
        ? t('项目尚未开张（草稿）——先在概览页开张，才有可写的房间')
        : t('项目已归档封存——台账只读，写操作不可用'), 'err');
      return false;
    }
    const ok = this.book.frameTo(this.key, frame, label);
    if (!ok) {
      this.toast.show(t('未识别到房主身份，管理操作不可用（/kb/people 无 local:true）'), 'err');
      return false;
    }
    // the owner's seatless task writes are receipt-only by protocol
    // (t_64: the mirror channel never broadcasts), so the board resyncs
    // itself off its own sends — two beats cover the lazy dial + write
    setTimeout(() => this.requestRefresh(), 500);
    setTimeout(() => this.requestRefresh(), 1500);
    return true;
  }

  /** Owner identity for gates. */
  get ownerName() { return this.book.owner?.name || null; }

  /** The viewed room's roster (∪ the owner) — the assignee pickers' source. */
  roster() {
    const room = this.book.rooms.get(this.key);
    const names = new Set(room ? [...room.members.keys()] : []);
    if (this.book.owner?.name) names.add(this.book.owner.name);
    return [...names];
  }

  /** The viewed project's task ledger (the drawer's 子任务/非叶 lookups). */
  get tasks() { return this.board ? this.board.tasks : null; }

  async #loadProjects() {
    try {
      this.projects = await getProjects();
    } catch { this.projects = []; }
    this.#paintSelector();
  }
}

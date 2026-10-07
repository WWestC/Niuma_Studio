// people.js — the people board's container (CAP-M4, frontend 稿 §4.2):
// three inner tabs (花名册 / 技能库 / MCP 服务 / 项目编制) plus the 320px detail
// sidebar, sharing one project list (every selector drinks from it) and
// one debounced refresh cycle. The board itself owns no protocol
// knowledge — reads go through wire/api, management verbs go through
// the RoomBook's owner write face, receipts come back as toasts from
// app.js. Three-state contract (empty/loading/error-retry) lives in
// each view, not here.

import { getProjects } from '../wire/api.js';
import { LobbyKey } from '../wire/wire.js';
import { t } from '../ui/i18n.js';
import { RosterView } from './list.js';
import { DetailView } from './detail.js';
import { RecruitForm } from './recruit.js';
import { SkillLibraryView } from '../capability/skills.js';
import { MCPLibraryView } from '../capability/mcps.js';
import { StaffingView } from './staffing.js';

const TABS = [
  { id: 'roster', label: t('花名册') },
  { id: 'skills', label: t('技能库') },
  { id: 'mcps', label: t('MCP 服务') },
  { id: 'staffing', label: t('项目编制') },
];

export class PeopleBoard {
  // 跨板跳转（聊天「加编制」）指名的落位键：revealed 消费即清——进门
  // 跟房给显式跳转让位（项目管理板同款纪律），跳转自己落位。
  #jumpIntent = null;

  /**
   * @param {HTMLElement} root #board-people (the .board container)
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {import('../ui/toast.js').Toast} toast
   */
  constructor(root, book, toast) {
    this.book = book;
    this.toast = toast;
    this.projects = [];        // ProjectSummary[] — every selector's source
    this.selected = null;      // the roster row the sidebar details
    this.tab = 'roster';
    this.visible = false;      // 板块在屏上（revealed/conceal 维护）——换房跟走只吃可见时

    root.innerHTML =
      '<div class="people-wrap">' +
        '<div class="people-main">' +
          '<div class="people-tabs" role="tablist">' +
            TABS.map((t) => `<button type="button" class="ptab" data-tab="${t.id}" role="tab">${t.label}</button>`).join('') +
            '<span class="tabs-spacer"></span>' +
            `<button type="button" class="btn small gold" data-cta-recruit>+ ${t('出生新成员')}</button>` +
          '</div>' +
          '<div class="people-pane" id="pane-roster"></div>' +
          '<div class="people-pane" id="pane-skills" hidden></div>' +
          '<div class="people-pane" id="pane-mcps" hidden></div>' +
          '<div class="people-pane" id="pane-staffing" hidden></div>' +
        '</div>' +
        '<aside class="person-side" id="person-side"></aside>' +
      '</div>';
    this.els = {
      tabs: root.querySelector('.people-tabs'),
      panes: {
        roster: root.querySelector('#pane-roster'),
        skills: root.querySelector('#pane-skills'),
        mcps: root.querySelector('#pane-mcps'),
        staffing: root.querySelector('#pane-staffing'),
      },
      side: root.querySelector('#person-side'),
    };
    this.els.tabs.addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-tab]');
      if (btn) this.switchTab(btn.dataset.tab);
      if (ev.target.closest('[data-cta-recruit]')) this.openRecruit();
    });

    this.roster = new RosterView(this.els.panes.roster, this);
    this.detail = new DetailView(this.els.side, this);
    this.recruit = new RecruitForm(this.els.panes.roster, this);
    this.skills = new SkillLibraryView(this.els.panes.skills, this);
    this.mcps = new MCPLibraryView(this.els.panes.mcps, this);
    this.staffing = new StaffingView(this.els.panes.staffing, this);
    this.detail.render(); // the sidebar's empty state paints before any pick

    this.refreshTimer = 0;
    this.pollTimer = 0;
  }

  /** Board entry (hash route or first paint): load, then keep warm while
   *  visible. 进门跟房：人在哪间项目房，花名册筛子与编制面落哪个项目
   *  （跨板跳转占位优先——「加编制」自己落位，跟房让位）。 */
  async revealed() {
    this.visible = true;
    await this.#loadProjects(); // 跟房落位前选择器先就位——编制面的键守卫才不回落大厅
    if (!this.#jumpIntent) this.#followRoom();
    this.#jumpIntent = null;
    await this.roster.reload();
    if (this.selected) this.detail.reload(this.selected);
    this.#armPoll();
  }

  /** 路由离场：记账可见性（换房跟走只吃可见时——与 revealed 成对）。 */
  conceal() {
    this.visible = false;
  }

  /** 牛马管理跟办公室走：人在哪间房，花名册的项目筛子与编制面
   *  的项目就落哪个——大厅与项目房一视同仁（回大厅就落大厅）。 */
  #followRoom() {
    const cur = this.book.current;
    if (!cur) return;
    if (this.roster.projectFilter !== cur) {
      this.roster.projectFilter = cur;
      if (this.tab === 'roster' && this.roster.state === 'ready') this.roster.render();
    }
    if (this.staffing.project !== cur) {
      this.staffing.project = cur;
      if (this.tab === 'staffing') this.staffing.reload();
    }
  }

  /** 换房（app.js book.onChange 'switch' 转来）：可见才跟（终端板/项目
   *  管理板同款纪律）——隐藏时零动作，进门 revealed() 重新落位。 */
  onRoomSwitch() {
    if (this.visible) this.#followRoom();
  }

  /** Active projects for the selectors, lobby first (the room-switcher's order). */
  projectChoices() {
    const active = this.projects.filter((p) => p.status === 'active');
    return [
      { key: LobbyKey, name: t('Niuma_Studio') },
      ...active.filter((p) => p.key !== LobbyKey).map((p) => ({ key: p.key, name: p.name })),
    ];
  }

  switchTab(tab) {
    if (!TABS.some((t) => t.id === tab)) return;
    this.tab = tab;
    for (const t of TABS) {
      this.els.panes[t.id].hidden = t.id !== tab;
      this.els.tabs.querySelector(`[data-tab="${t.id}"]`)?.classList.toggle('active', t.id === tab);
    }
    // the 320px sidebar belongs to the roster face (§4.2 layout)
    this.els.side.hidden = tab !== 'roster';
    if (tab === 'roster') this.roster.reload();
    if (tab === 'skills') this.skills.reload();
    if (tab === 'mcps') this.mcps.reload();
    if (tab === 'staffing') this.staffing.reload();
  }

  select(name) {
    this.selected = name;
    this.switchTab('roster');
    this.detail.reload(name);
  }

  /** Cross-board refresh tap (event frames, owner receipts, after local writes). */
  requestRefresh() {
    clearTimeout(this.refreshTimer);
    this.refreshTimer = setTimeout(() => this.#refreshNow(), 400);
  }

  async #refreshNow() {
    this.#loadProjects(); // fire and forget — selectors read the field
    if (this.tab === 'roster') await this.roster.reload();
    if (this.tab === 'skills') this.skills.reload();
    if (this.tab === 'mcps') this.mcps.reload();
    if (this.tab === 'staffing') this.staffing.reload();
    if (this.selected && this.tab === 'roster') this.detail.reload(this.selected);
  }

  // A quiet poll keeps the roster honest when nothing broadcasts (the
  // establishment doc, archive states and staffing files have no frames
  // of their own yet).
  #armPoll() {
    clearInterval(this.pollTimer);
    this.pollTimer = setInterval(() => {
      if (this.tab === 'roster' && document.visibilityState === 'visible') {
        this.roster.reload();
        if (this.selected) this.detail.reload(this.selected);
      }
    }, 30_000);
  }

  async #loadProjects() {
    try {
      this.projects = await getProjects();
    } catch { this.projects = []; } // selectors degrade to the lobby alone
    // The roster's project column may have painted before this list
    // landed (the load rides fire-and-forget) — repaint so it shows
    // project names, not raw keys (paint-gated: identical markup stays).
    if (this.tab === 'roster' && this.roster.state === 'ready') this.roster.render();
  }

  /** The roster's empty-state CTA target: 出生第一位成员; the est-bar's
   * quick-hire chips pass prefill ({prefRole, prefName}). */
  openRecruit(opts) { this.switchTab('roster'); this.recruit.open(opts); }

  /** 聊天流「加编制」的跨板落点：编制页签 + 按建议预填加岗浮层
   *  （projectKey 可空——面板按当前项目选择落位，浮层里目标可见）。
   *  @param {string|null} projectKey @param {Object} offer */
  prefillEstablishment(projectKey, offer) {
    this.#jumpIntent = projectKey || null; // 同一跳 hash 换来的 revealed 让位（加编制自己落位）
    this.switchTab('staffing');
    this.staffing.prefillAddRow(projectKey || LobbyKey, offer);
  }

  /** One owner management verb over the lobby write face; toast refusals locally too.
   * @param {import('../wire/wire.js').Frame} frame @param {string} label */
  ownerSend(frame, label) {
    const ok = this.book.ownerSend(frame, label);
    if (!ok) this.toast.show(t('未识别到房主身份，管理操作不可用（/kb/people 无 local:true）'), 'err');
    return ok;
  }

  /** Owner identity for guards (房主不可被调级/移出). */
  get ownerName() { return this.book.owner?.name || null; }

  dispose() {
    clearInterval(this.pollTimer);
    clearTimeout(this.refreshTimer);
  }
}

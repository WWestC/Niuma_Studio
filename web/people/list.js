// list.js — the roster face (frontend 稿 §4.2 主列): the establishment
// reconciliation bar on top (缺员标红, each chip a quick-hire tap into
// the birth form prefilled for that post; parse_error → yellow「去修」
// bar —
// a broken table is 200+parse_error, never an HTTP error), the
// [全部|在线|宽限|离线|已归档] filter with three-state grouping (在线 /
// 宽限 grace / 离线), the toolbar's right wing (project dropdown + 搜索
// name/role box), and rows of 色点+名+项目+身份+等级+状态 clicking into
// the detail sidebar. 项目 = the seat the person currently holds
// (PersonSummary.room: "default" = 大厅; no seat = a dim dash — offline
// configs and departed memories hold no project). The 归档 tab reads
// /agents?archived=1 — /kb/people is active-only by design. Three-state
// contract: CTA card when nobody exists, skeleton rows while loading,
// in-place retry on failure.

import { getPeople, getAgents, getEstablishment } from '../wire/api.js';
import { LobbyKey } from '../wire/wire.js';
import { esc, safeColor, avatarText } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { rankLabel, personState, STATE_LABEL } from '../ui/person.js';

const FILTERS = [
  { id: 'all', label: t('全部') },
  { id: 'online', label: t('在线') },
  { id: 'grace', label: t('宽限') },
  { id: 'offline', label: t('离线') },
  { id: 'archived', label: t('已归档') },
];

// The project dropdown's 「未入座」 bucket (offline configs, departed
// memories) — a value that can never collide with a real project key.
const NO_ROOM = 'none';

const SKELETON_ROWS = 6;

export class RosterView {
  /** @param {HTMLElement} pane @param {import('./people.js').PeopleBoard} board */
  constructor(pane, board) {
    this.pane = pane;
    this.board = board;
    this.filter = 'all';
    this.projectFilter = ''; // '' = 全部项目; NO_ROOM = 未入座; else a room key
    this.q = '';             // 搜索 substring over name+role, case-insensitive
    this.state = 'loading'; // loading | error | ready
    this.people = [];       // PersonSummary[] (active roster)
    this.archived = [];     // AgentSummary[] (/agents?archived=1)
    this.establishment = null;
    pane.addEventListener('click', (ev) => {
      const retry = ev.target.closest('[data-retry]');
      if (retry) { this.reload(); return; }
      const cta = ev.target.closest('[data-cta-recruit]');
      if (cta) { this.board.openRecruit(); return; }
      // quick hire: a 缺员 chip opens the birth form prefilled for its
      // post (role from the establishment table, name suggested)
      const quick = ev.target.closest('[data-quick-role]');
      if (quick) {
        this.board.openRecruit({ prefRole: quick.dataset.quickRole, prefName: quick.dataset.quickName });
        return;
      }
      const row = ev.target.closest('[data-person]');
      if (row) this.board.select(row.dataset.person);
      // 空项目的出生 CTA：预填目标项目（新开张的项目按它筛人先见到的
      // 就是这张卡——项目有人是从出生开始的）
      const birth = ev.target.closest('[data-cta-recruit-project]');
      if (birth) { this.board.openRecruit({ prefProject: birth.dataset.ctaRecruitProject }); return; }
      const tab = ev.target.closest('[data-filter]');
      if (tab && this.filter !== tab.dataset.filter) {
        this.filter = tab.dataset.filter;
        this.render();
      }
    });
    // The toolbar's own controls re-filter through #repaintList — only
    // the list zone rewrites, never the toolbar itself: rewriting the
    // search box mid-keystroke eats the caret (and the click racing the
    // repaint), rewriting the select closes an open dropdown.
    pane.addEventListener('input', (ev) => {
      if (ev.target.matches('[data-q]')) {
        this.q = ev.target.value || '';
        this.#repaintList();
      }
    });
    pane.addEventListener('change', (ev) => {
      if (ev.target.matches('[data-projects]')) {
        this.projectFilter = ev.target.value || '';
        this.#repaintList();
      }
    });
  }

  async reload() {
    // Keep the current rows on screen while a refresh fetches (the
    // board re-taps reload on every room event): the skeleton flash
    // swaps the DOM twice per refresh, which is what swallowed clicks
    // racing the repaint. Only a genuinely empty pane shows skeletons.
    const hadData = this.state === 'ready';
    if (!hadData) {
      this.state = 'loading';
      this.render();
    }
    const jobs = [getPeople(), getEstablishment()];
    if (this.filter === 'archived' || this.archived.length === 0) {
      jobs.push(getAgents(true).catch(() => [])); // archive face optional
    }
    const [people, est, archived] = await Promise.allSettled(jobs);
    if (people.status !== 'fulfilled') {
      if (!hadData) {
        this.state = 'error';
        this.error = people.reason;
        this.render();
      } // stale-but-standing rows beat an error card on a transient blip
      return;
    }
    this.state = 'ready';
    this.people = people.value;
    this.establishment = est.status === 'fulfilled' ? est.value : null;
    this.estError = est.status === 'rejected' ? est.reason : null;
    if (archived && archived.status === 'fulfilled') this.archived = archived.value;
    this.render();
  }

  render() {
    if (this.state === 'loading') return this.#renderLoading();
    if (this.state === 'error') return this.#renderError();
    this.#renderReady();
  }

  // One paint gate for every face: skip the innerHTML write when the
  // markup is byte-identical to what is already up. The board refreshes
  // on every room event (a busy room repaints every few hundred ms);
  // rewriting identical nodes mid-click swallows real clicks and
  // resets :focus — a no-op write keeps the DOM (and the user's click)
  // alive while data-driven changes still repaint.
  #paint(html) {
    if (this._lastHTML === html) return;
    this._lastHTML = html;
    // A data refresh can repaint the pane while the user types in the
    // search box — capture the caret before the innerHTML write and
    // put it back after (the markup re-embeds this.q as the value).
    const typing = document.activeElement?.matches?.('[data-q]');
    this.pane.innerHTML = html;
    if (typing) {
      const box = this.pane.querySelector('[data-q]');
      if (box) {
        box.focus();
        const end = (this.q || '').length;
        try { box.setSelectionRange(end, end); } catch { /* not a text control */ }
      }
    }
  }

  // The toolbar's narrow repaint: only the list zone under it rewrites
  // (search keystrokes, project flips) — the search box and the select
  // never leave the tree, so the caret and the open dropdown survive.
  #repaintList() {
    if (this.state !== 'ready') return;
    const body = this.pane.querySelector('.roster-body');
    if (body) body.innerHTML = this.#bodyHTML();
  }

  #renderLoading() {
    this.#paint(
      this.#barHTML('loading') + this.#filtersHTML() +
      `<div class="roster-body"><div class="roster-list">${'<div class="prow skeleton"></div>'.repeat(SKELETON_ROWS)}</div></div>`);
  }

  #renderError() {
    this.#paint(
      `<div class="state-card error">
        <strong>${t('名册读取失败')}</strong>
        <span>${esc(this.error?.message || String(this.error))}</span>
        <button type="button" class="btn" data-retry>${t('重试')}</button>
      </div>`);
  }

  #renderReady() {
    this.#paint(this.#barHTML('ready') + this.#filtersHTML() +
      `<div class="roster-body">${this.#bodyHTML()}</div>`);
  }

  // The toolbar: state chips on the left, the project dropdown and the
  // search box on the right (tabs-spacer pushes them over — the tabs
  // row's own idiom). The dropdown drinks from the same projectChoices
  // source as every selector, plus the 未入座 bucket; the archived face
  // has no seat truth to filter by, so the dropdown stands disabled
  // there rather than lying with a no-op filter.
  #filtersHTML() {
    const chips = FILTERS.map((f) =>
      `<button type="button" class="rfilter${f.id === this.filter ? ' active' : ''}" data-filter="${f.id}">${f.label}</button>`).join('');
    const choices = this.board.projectChoices?.() || [];
    const project = choices.length
      ? `<select class="input roster-project" data-projects aria-label="${t('按项目筛选')}" title="${t('按项目筛选成员')}"${this.filter === 'archived' ? ' disabled' : ''}>` +
        `<option value="">${t('全部项目')}</option>` +
        choices.map((c) => `<option value="${esc(c.key)}"${c.key === this.projectFilter ? ' selected' : ''}>${esc(c.name)}</option>`).join('') +
        `<option value="${NO_ROOM}"${this.projectFilter === NO_ROOM ? ' selected' : ''}>${t('未入座')}</option></select>`
      : '';
    return `<div class="roster-filters">${chips}<span class="tabs-spacer"></span>${project}` +
      `<input class="input roster-search" type="search" data-q value="${esc(this.q)}" placeholder="${t('搜索姓名 / 身份')}" aria-label="${t('搜索成员')}"></div>`;
  }

  // The list zone under the toolbar — everything the search box and
  // the project dropdown re-filter without touching the toolbar.
  #bodyHTML() {
    if (this.filter === 'archived') return this.#archivedHTML();
    if (this.people.length === 0) {
      return `<div class="state-card empty">
        <strong>${t('办公室里还没有人')}</strong>
        <span>${t('出生第一位成员，团队从这里开始')}</span>
        <button type="button" class="btn gold" data-cta-recruit>+ ${t('出生第一位成员')}</button>
      </div>`;
    }
    const rows = this.#visiblePeople();
    if (rows.length === 0) {
      // 按项目筛到空不是死胡同：新开张的项目没人是从「出生第一位
      // 成员」开始的——直接把出生表单按该项目预填好（大厅同理，
      // 空工作室的首卡在上一分支）。
      if (this.projectFilter && this.projectFilter !== NO_ROOM && !this.q.trim()) {
        const name = this.#roomName(this.projectFilter);
        return `<div class="state-card empty">
          <strong>${t('「{name}」还没有成员', { name: esc(name) })}</strong>
          <span>${t('出生第一位成员进该项目，这里就会亮起来')}</span>
          <button type="button" class="btn gold" data-cta-recruit-project="${esc(this.projectFilter)}">+ ${t('出生成员进「{name}」', { name: esc(name) })}</button>
        </div>`;
      }
      return `<div class="roster-list"><div class="group-empty">${t('没有匹配的成员——换个词，或清空筛选再试')}</div></div>`;
    }
    return this.#groupedHTML(rows);
  }

  /** The roster after both toolbar filters: the project (seat truth)
   * narrows first, then the name/role substring. */
  #visiblePeople() {
    let rows = this.people;
    if (this.projectFilter) {
      rows = this.projectFilter === NO_ROOM
        ? rows.filter((p) => !p.room)
        : rows.filter((p) => p.room === this.projectFilter);
    }
    const q = this.q.trim().toLowerCase();
    if (q) {
      rows = rows.filter((p) =>
        p.name.toLowerCase().includes(q) || (p.role || '').toLowerCase().includes(q));
    }
    return rows;
  }

  #groupedHTML(people) {
    const byState = { online: [], grace: [], offline: [] };
    for (const p of people) byState[personState(p)].push(p);
    const groups = this.filter === 'all'
      ? [['online', byState.online], ['grace', byState.grace], ['offline', byState.offline]]
      : [[this.filter, byState[this.filter] || []]];
    let html = '<div class="roster-list">';
    for (const [state, rows] of groups) {
      if (this.filter === 'all') {
        html += `<div class="group-head">${STATE_LABEL[state]} · ${rows.length}</div>`;
      }
      if (!rows.length && this.filter === 'all') continue;
      if (!rows.length && this.filter !== 'all') {
        html += `<div class="group-empty">${t('「{s}」组暂无成员', { s: STATE_LABEL[state] })}</div>`;
      }
      html += rows.map((p) => this.#rowHTML(p, state)).join('');
    }
    return html + '</div>';
  }

  #rowHTML(p, state) {
    // 飞书组织架构式行头：成员色字母头像，存在点亮在头像右下角
    const avatar =
      `<span class="pavatar" style="background:${safeColor(p.color)}">` +
      `${esc(avatarText(p.name))}<i class="pdot ${state}"></i></span>`;
    const tasks = p.task_doing > 0
      ? t(' · 在办 {n}', { n: p.task_doing })
      : (p.task_count > 0 ? t(' · 任务 {n}', { n: p.task_count }) : '');
    // 项目列 = the seat the person holds now. /kb/people is the whole
    // studio's roster, so the column doubles as 「这人坐在哪间办公
    // 室」——online members in another room used to carry an @房间 tag
    // on the name; the column says it for everyone, seat or not.
    const proj = p.room
      ? `<span class="pproj" title="${esc(this.#roomName(p.room))}">${esc(this.#roomName(p.room))}</span>`
      : `<span class="pproj none" title="${t('当前未入座任何项目')}">—</span>`;
    return `<button type="button" class="prow${p.local ? ' local' : ''}" data-person="${esc(p.name)}" ${this.board.selected === p.name ? ' aria-current="true"' : ''}>
      ${avatar}
      <span class="pname">${esc(p.name)}${p.local ? ` <span class="tag you">${t('本机房主')}</span>` : ''}</span>
      ${proj}
      <span class="prole">${esc(p.role || t('未设身份'))}</span>
      <span class="prank">${rankLabel(p)}${tasks}</span>
      <span class="pstate st-${state}">${STATE_LABEL[state]}</span>
    </button>`;
  }

  /** A room key's display name — 大厅 for the lobby, else the projects
   * list's name, else the key. */
  #roomName(key) {
    if (key === LobbyKey) return t('Niuma_Studio');
    return this.board.projects.find((p) => p.key === key)?.name || key;
  }

  #archivedHTML() {
    // 搜索对归档档案同样生效（按姓名/身份）；项目下拉在归档视图
    // 禁用（无座位真源），这里不掺项目过滤。
    const q = this.q.trim().toLowerCase();
    const rows = q
      ? this.archived.filter((a) =>
          a.name.toLowerCase().includes(q) || (a.role || '').toLowerCase().includes(q))
      : this.archived;
    if (!rows.length) {
      const note = q
        ? t('没有匹配「{q}」的归档档案', { q: esc(this.q.trim()) })
        : t('没有已归档档案（v2 空库起步，归档随离职流产生）');
      return `<div class="roster-list"><div class="group-empty">${note}</div></div>`;
    }
    return `<div class="roster-list"><div class="group-head">${t('已归档 · {n}', { n: rows.length })}</div>` +
      rows.map((a) =>
        `<button type="button" class="prow archived" data-person="${esc(a.name)}">
          <span class="pavatar" style="background:var(--chalk-faint)">${esc(avatarText(a.name))}<i class="pdot offline"></i></span>
          <span class="pname">${esc(a.name)}</span>
          <span class="prole">${esc(a.role || t('未设身份'))}</span>
          <span class="prank"></span>
          <span class="pstate st-offline">${t('已归档')}</span>
        </button>`).join('') + '</div>';
  }

  // The establishment bar (§4.2 顶部): one horizontal diff summary,
  // missing posts red. Hidden when there is nothing to reconcile; a
  // parse_error shows the yellow "go fix the table" bar instead (rows
  // are empty by contract — that is a judgeable failure, not a guess).
  // The 缺员 alarm mirrors the keeper's watch gate (server/watch.go v2.4):
  // only 必须 posts and 自动补员=是 rows count as missing — a planned
  // row (先定编后招牛马) is merely unfilled, not a gap to nag about, so it
  // never turns the bar red nor grows a quick-hire chip. The report
  // stamps system rows must=true and zeroes the advisor row, so the
  // client-side gate is exactly must || auto_fill.
  #barHTML(mode) {
    if (mode === 'loading') return '<div class="est-bar skeleton"></div>';
    if (this.estError || !this.establishment) return '';
    const { rows, parse_error } = this.establishment;
    if (parse_error) {
      return `<div class="est-bar broken">${t('编制表坏了，去修')} → <a href="/kb/docs/ops/establishment" target="_blank" rel="noopener">ops/establishment</a><span class="est-why">${esc(parse_error)}</span></div>`;
    }
    if (!rows.length) return ''; // 无编制表时隐藏 (§4.5)
    const watched = (r) => r.must || r.auto_fill;
    const missing = rows.filter((r) => r.missing > 0 && watched(r));
    const planned = rows.filter((r) => r.missing > 0 && !watched(r));
    // Go nil slices marshal as JSON null — empty present lists arrive
    // as null, so every read goes through (r.present || []).
    const onStaff = rows.reduce((n, r) => n + (r.present || []).length, 0);
    const plan = rows.reduce((n, r) => n + r.headcount, 0);
    if (!missing.length) {
      // 规划岗未招只作中性注脚，不出红色、不出补员 chips
      const note = planned.length ? t('规划未招 {n}', { n: planned.length }) : t('无缺员');
      return `<div class="est-bar ok">${t('编制对账：{rows} 岗位 · 计划 {plan} · 在岗 {on} · {note}', { rows: rows.length, plan, on: onStaff, note })}</div>`;
    }
    const chips = missing.map((r) => {
      const label = r.name || r.key;
      const role = r.role || label; // 补员按身份列精确匹配（§12-5）
      return `<button type="button" class="est-miss" data-quick-role="${esc(role)}" data-quick-name="${esc(this.#suggestName(label))}" title="${t('快速补员：出生一名「{name}」（身份 {role}）', { name: esc(label), role: esc(role) })}">${esc(label)} ${t('缺 {n}', { n: r.missing })} <i class="est-plus">＋</i></button>`;
    }).join('');
    const note = planned.length ? `<span class="est-why">${t('另有规划未招 {n}', { n: planned.length })}</span>` : '';
    return `<div class="est-bar bad">${t('编制对账：{rows} 岗位 · 在岗 {on} · ', { rows: rows.length, on: onStaff })}<span class="est-miss-count">${t('缺员 {n} 岗', { n: missing.length })}</span>${chips}${note}</div>`;
  }

  // The quick-hire chip's suggested name: the post label when free,
  // else label+2, +3… — active names are the collision set (birth
  // refuses a name already in dispatch, but reuses an archived
  // namesake by design, so archived names stay fair game).
  #suggestName(base) {
    const taken = new Set(this.people.map((p) => p.name));
    if (!taken.has(base)) return base;
    for (let n = 2; ; n++) if (!taken.has(base + n)) return base + n;
  }
}

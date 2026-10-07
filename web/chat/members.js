// members.js — the sidebar roster (§4.1③): the room's welcome roster
// + join/leave/rename deltas (grace ghosts ride the roster server-side
// — 在线∪宽限 in one list); cold start falls back to /kb/people's
// online slice for the lobby until the welcome lands. It is the single
// member list — the chat board's old aside duplicate is gone. Rows are
// Feishu-style letter avatars over the member color, with a presence
// dot cross-read from /kb/people (roster frames carry no presence, so
// the people truth is polled gently and cached 30s); the section head
// carries the live count. Clicking a row opens the person profile card
// (头像篇: avatar/person taps expand the profile) — the @ 提及 lives
// IN the card now, not on the bare row.

import { esc, safeColor, avatarText } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { getPeople } from '../wire/api.js';

const PEOPLE_TTL = 30_000; // presence truth refresh cadence

export class MembersView {
  /**
   * @param {HTMLElement} list the <div> holding member rows
   * @param {import('./rooms.js').RoomBook} book
   * @param {{onPerson?: (name: string, anchor: HTMLElement) => void, onPeople?: () => void, onTrace?: (name: string) => void, head?: HTMLElement, search?: HTMLInputElement}} [opts]
   *   点一行 = 开该成员的牛马名片卡（app.js 挂共享 ProfileCard；
   *   名片里的 @ 提及/牛马主页 跨板都能走）；onTrace 点「干活中」
   *   chip 直开工作过程抽屉（v2.8）；onPeople 在一轮
   *   /kb/people 采集成功后回灌（对话流借此原地补消息头的岗位/等级
   *   小标）；head 是「在线牛马」小标题，人数跟帧；search 是标题
   *   右侧的名字过滤框（input 即过滤、Esc 清词），过滤中头上的
   *   计数显示 命中/总数。
   */
  constructor(list, book, opts = {}) {
    this.list = list;
    this.book = book;
    this.opts = opts;
    this.people = new Map();  // name -> PersonSummary (presence truth)
    this.fetchedAt = 0;
    this.refreshing = false;
    this.query = ''; // 侧栏搜索词（牛马名字的子串，大小写不敏感）
    if (opts.search) {
      opts.search.addEventListener('input', () => {
        this.query = opts.search.value.trim();
        this.render();
      });
      opts.search.addEventListener('keydown', (ev) => {
        if (ev.key !== 'Escape') return; // Esc＝清词还整册（与对话头栏搜索同款）
        opts.search.value = '';
        this.query = '';
        this.render();
        opts.search.blur();
      });
    }
    list.addEventListener('click', (ev) => {
      // 干活中 chip 是行内按钮：点开工作过程抽屉，不冒泡成名片卡
      const chip = ev.target.closest('[data-trace]');
      if (chip) {
        ev.stopPropagation();
        this.opts.onTrace?.(chip.dataset.trace);
        return;
      }
      const row = ev.target.closest('[data-person]');
      if (!row) return;
      // 锚点取行内头像（名片卡箭头对头像中心，头像篇的卡从人出发）
      this.opts.onPerson?.(row.dataset.person, row.querySelector('.avatar') || row);
    });
  }

  /**
   * /kb/people 里此人的最新真相（presence/rank/local），名册与名片卡
   * 共读；未采到时 null（roster 行仍照画，卡退到纯色名）。
   * @param {string} name
   */
  person(name) {
    return this.people.get(name) || null;
  }

  /** @param {string|null} key @param {string} hint */
  onChange(key, hint) {
    if (hint !== 'members' && hint !== 'switch' && hint !== 'rooms' && hint !== 'status' && hint !== 'queues') return;
    const room = this.book.currentRoom();
    if (!room) return;
    if ((hint === 'members' || hint === 'queues') && key !== room.key) return;
    this.render();
    if (hint !== 'queues') this.#refreshPeople(); // presence truth rides along, cached
  }

  async #refreshPeople() {
    if (this.refreshing || Date.now() - this.fetchedAt < PEOPLE_TTL) return;
    this.refreshing = true;
    try {
      const list = await getPeople();
      this.people = new Map(list.map((p) => [p.name, p]));
      this.fetchedAt = Date.now();
      this.render(); // repaint with the dots
      this.opts.onPeople?.(); // presence/rank/role truth landed — consumers repaint off it
    } catch { /* presence stays blank — roster rows still render */ }
    finally { this.refreshing = false; }
  }

  /** presence state for one row: roster has no state, so cross-read /kb/people;
   *  the cold-start fallback rows carry their own online/grace flags. */
  #stateOf(m) {
    if (m.grace !== undefined || m.online !== undefined) {
      return m.grace ? 'grace' : (m.online === false ? 'offline' : 'online');
    }
    const p = this.people.get(m.name);
    if (!p) return null;
    return p.grace ? 'grace' : (p.online ? 'online' : 'offline');
  }

  render() {
    const room = this.book.currentRoom();
    const all = room?.rosterLoaded
      ? [...room.members.values()]
      : this.book.sidebarRoster(); // fallback slice while unwelcomed
    // 待投计数：排队中投影按人聚合（seq -> who 反转）——谁的调度队列
    // 里还压着没送进会话的消息，行上挂「N 待投」徽标
    const waitBy = new Map();
    if (room?.rosterLoaded) {
      for (const who of room.queues.values()) {
        for (const name of who) waitBy.set(name, (waitBy.get(name) || 0) + 1);
      }
    }
    const q = this.query.toLowerCase();
    const rows = q ? all.filter((m) => (m.name || '').toLowerCase().includes(q)) : all;
    if (this.opts.head) {
      this.opts.head.textContent =
        `${t('在线牛马')} · ${q ? `${rows.length}/${all.length}` : all.length}`;
    }
    if (!rows.length) {
      this.list.innerHTML = q && all.length
        ? `<div class="member empty">${t('没有叫「{name}」的牛马', { name: esc(this.query) })}</div>`
        : `<div class="member empty">${room?.rosterLoaded ? t('办公室空无一人') : t('连接中…')}</div>`;
      return;
    }
    this.list.innerHTML = '';
    for (const m of rows) {
      const state = this.#stateOf(m);
      const dot = state
        ? `<i class="presence st-${state}" title="${state === 'online' ? t('在线') : state === 'grace' ? t('离线宽限中') : t('离线')}"></i>`
        : '';
      // 干活中 chip: the dispatcher's turn tell — live flips ride
      // member_work frames; cold hydration falls back to /kb/people.
      // Gate is offline-only (v2.10): a live roster flag must not be
      // suppressed just because /kb/people hasn't answered yet (state
      // null during the cold window) — working implies a live seat.
      const working = state !== 'offline' &&
        (m.working || this.people?.get(m.name)?.working);
      const chip = working
        ? `<button type="button" class="work-chip" data-trace="${esc(m.name)}" title="${t('回合进行中——点开看 TA 的思考与工具调用（工作过程）')}">${t('干活中')}</button>`
        : '';
      const waiting = waitBy.get(m.name) || 0;
      const qchip = waiting
        ? `<span class="wait-chip" title="${t('该成员的待投队列压着 {n} 条消息（其回合未结束，消息尚未注入会话）', { n: waiting })}">${t('{n} 待投', { n: waiting })}</span>`
        : '';
      const row = document.createElement('div');
      row.className = 'member';
      if (this.opts.onPerson) {
        row.dataset.person = m.name;
        row.title = t('查看 {name} 的名片', { name: m.name });
      }
      row.innerHTML =
        `<span class="avatar sm" style="background:${safeColor(m.color)}">${esc(avatarText(m.name))}</span>` +
        `<span class="m-col"><span class="name">${esc(m.name)}</span>` +
        `<span class="role">${esc(m.role || '')}</span></span>` + chip + qchip + dot;
      this.list.appendChild(row);
    }
  }
}

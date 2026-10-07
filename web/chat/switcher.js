// switcher.js — the room switcher bar (P3-d ①, the sidebar's project
// switcher face): the lobby first, then every active project room
// (GET /projects), the current room highlighted, per-room unread
// badges fed by the rooms the observer connections keep alive — 巡视
// 流's scan surface (PRD §4.3). The current room never badges: its
// unread lives only on the 牛马对话 nav pill (entering the board is
// reading it), the row stays quiet. Rows are the Feishu conversation-list
// face: a letter avatar over the room color, name + roster headcount,
// and the room's 暂停/开始 button (the old last-message time corner —
// a room you can freeze beats a room you can only watch); the unread
// pill; no chat-content preview — the preview line exists only for
// state tags (the blue 公告 unread tag, the red 有人@我 tag). Draft and
// archived projects never list; the lobby is process-constant, and
// while it is the only room a 立项 hint fills the space below.

import { LobbyKey } from '../wire/wire.js';
import { postRoomPause } from '../wire/api.js';
import { esc, safeColor, memberColor, groupAvatarText, icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

/** One-line snippet for the notice preview. */
function snippet(text, n = 40) {
  const one = String(text || '').replace(/\s+/g, ' ').trim();
  return one.length > n ? `${one.slice(0, n)}…` : one;
}

export class SwitcherView {
  /** @param {HTMLElement} nav @param {import('./rooms.js').RoomBook} book */
  constructor(nav, book) {
    this.nav = nav;
    this.book = book;
    nav.addEventListener('click', (ev) => {
      // 暂停/开始按钮先于行落点：按钮在行内但不该换房（stopPropagation
      // 不必——这里先判先归，行落点根本走不到）
      const tog = ev.target.closest('[data-pausetoggle]');
      if (tog) { this.#togglePause(tog.dataset.pausetoggle); return; }
      const row = ev.target.closest('[data-room]');
      if (row) this.book.switchTo(row.dataset.room);
    });
  }

  /**
   * 暂停/开始的写腿：乐观翻面（按钮即刻反馈），POST /p/{key}/pause；
   * 服务端的 "pause" 广播回来把全房窗口对齐（含访客页），失败则回滚
   * ＋提示。暂停期间该房对话、会议、巡逻全停、小人定格。
   * @param {string} key
   */
  async #togglePause(key) {
    const room = this.book.rooms.get(key);
    if (!room || room.pauseBusy) return;
    const was = !!room.paused;
    room.pauseBusy = true;
    room.paused = !was; // 乐观翻面——广播回来自然对齐
    this.render();
    try {
      const r = await postRoomPause(key, !was);
      if (!r.ok) throw new Error(r.data?.error || r.data?.reason || `HTTP ${r.status}`);
    } catch (err) {
      if (this.book.rooms.get(key) === room) {
        room.paused = was; // 写失败回滚——按钮不撒谎
        this.render();
        this.book.ev.onNotice(t('暂停开关写失败：{why}', { why: err?.message || err }));
      }
    } finally {
      room.pauseBusy = false;
    }
  }

  /** @param {string|null} _key @param {string} hint */
  onChange(_key, hint) {
    // 'messages' too: the current room's own time corner stays fresh;
    // 'notice' repaints the 📢 标（公告发布/撤下/未读红标/未读预览行）;
    // 'pause' repaints the ⏯ 按钮（暂停/恢复的帧同步与 welcome 水合）
    if (hint === 'rooms' || hint === 'switch' || hint === 'unread' ||
        hint === 'status' || hint === 'messages' || hint === 'notice' ||
        hint === 'pause') {
      this.render();
    }
  }

  render() {
    const rooms = this.book.roomList();
    this.nav.innerHTML = '';
    for (const r of rooms) {
      const row = document.createElement('button');
      row.type = 'button';
      row.className = 'room-row' + (r.current ? ' active' : '') + (r.status === 'offline' ? ' offline' : '') + (r.paused ? ' paused' : '');
      row.dataset.room = r.key;
      // 预览行只留状态小签：公告未读（蓝签＋首行摘要——飞书「[群公告]」
      // 式，不进门也看得见内容）＞ 有人@我 标。不晒聊天记录也不晒草稿
      // （草稿仍按房记着，只在输入井里现形）——①区是房列表不是会话摘要。
      // 公告签天生只挂别家房（rooms 侧 set 时就豁免当前房）；@我 标同理
      // 在此豁免——当前房的动静不重复报信（见行尾未读签的同一纪律）
      const preview = (r.noticeUnread && r.noticeText)
        ? `<span class="room-prev"><span class="room-notice-tag">${t('公告')}</span>${esc(snippet(r.noticeText, 46))}</span>`
        : (!r.current && r.atMe)
          ? `<span class="room-prev"><span class="room-atme">${t('有人@我')}</span></span>`
          : '';
      row.innerHTML =
        `<span class="room-avatar" style="background:${safeColor(memberColor(r.key))}">${esc(groupAvatarText(r.name))}</span>` +
        '<span class="room-main">' +
          `<span class="room-top"><span class="room-name">${esc(r.name)}</span>` +
          (r.autopilot ? `<span class="room-auto" title="${t('自动功能运行中（补货/推进至少一项，设置卡·智能 可看可关）')}">${icon('bolt', 13)}</span>` : '') +
          (r.notice ? `<span class="room-pin${r.noticeUnread ? ' unread' : ''}" title="${t('本房有群公告——进门看置顶条')}">${icon('megaphone', 13)}</span>` : '') +
          (r.count > 0 ? `<span class="room-n">${t('{n} 人', { n: `<b>${r.count}</b>` })}</span>` : '') +
          '</span>' +
          preview +
        '</span>' +
        // 声色分语言（与未读红签同一排、同一纪律）：工单变动（任务/需
        // 求）＝绿点（这间房的工单有动静，没当面看见），对话＝红签（数
        // 字）。绿点不带数字——「有变动」是质变不是量变，数几声叮没有
        // 意义；当前房同样豁免（正坐在屋里，办公室导航钉替它报信）
        (r.workFresh > 0 && !r.current ? `<span class="room-taskdot" title="${t('任务/需求有变动')}"></span>` : '') +
        // 未读签只挂别家房：当前房（大厅或项目）即使人不在对话板块、
        // 未读照常累计，也只让「牛马对话」导航徽标去报数（进门即读），
        // 房行不再重复亮红点——正坐在屋里，门口不该再挂「有信」
        (r.unread > 0 && !r.current ? `<span class="room-badge">${r.unread > 99 ? '99+' : r.unread}</span>` : '') +
        // 暂停/开始钮收尾贴行右缘（红签绿点在内侧——报信的数字贴近
        // 名字，操作钮在最外，拇指落点固定不随有无未读漂移）：暂停冻
        // 结整间办公室——对话停摆、小人定格、会议/巡逻歇拍；恢复按原
        // 顺序继续。span 而非 button（行本身是 button，嵌套不合法），
        // 点击归构造器的 [data-pausetoggle] 先判先归。
        `<span class="room-pause${r.paused ? ' on' : ''}" data-pausetoggle="${esc(r.key)}" role="button" tabindex="0" title="${r.paused ? t('已暂停——点此恢复：对话与会议按原顺序继续') : t('暂停本办公室：对话、会议、巡逻全部停摆，小人定格')}">${icon(r.paused ? 'play' : 'pause', 13)}</span>`;
      row.title = r.key === LobbyKey ? t('Niuma_Studio（default）') : t('{name}（{key}）', { name: r.name, key: r.key });
      this.nav.appendChild(row);
    }
    // 只有大厅时补一条立项引导（③区空态的同款写法），别让①区空着
    if (rooms.length <= 1) {
      const hint = document.createElement('a');
      hint.className = 'room-hint';
      hint.href = '#/project';
      hint.textContent = t('还没有项目房——去项目管理立项，给项目开一间房');
      this.nav.appendChild(hint);
    }
  }
}

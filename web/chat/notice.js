// notice.js — 群公告面板（飞书式）：查看/发布/更新/撤下当前房的群
// 公告。房主见编辑面（markdown 草稿 + 「通知全员（唤醒牛马）」开关 +
// 撤下），只读窗口只见全文。发布走 OwnerChannel 的 notice_save
// （expect_rev 条件写：首发 0=必须不存在，更新=当前 rev）；denied 回
// 执（rev 冲突附 current_rev）由 app.js 路由回 onDenied——草稿保留、
// 面板重开提示合并重试。成功不猜：房间 observer 的 notice 广播经
// app.js 的 onSync 钩子刷回面板，状态层是唯一真源。

import { getRoomNotice } from '../wire/api.js';
import { Msg } from '../wire/wire.js';
import { esc, fmtDateTime, icon } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from './markdown.js';
import { Dialog } from '../ui/feedback.js';

export class NoticePanel {
  /**
   * @param {import('./rooms.js').RoomBook} book
   * @param {import('../ui/toast.js').Toast} toast
   */
  constructor(book, toast) {
    this.book = book;
    this.toast = toast;
    this.key = null;      // 面板正展示的房
    this.el = null;       // .overlay 根（关闭即弃）
    this.token = 0;       // 开面板序号——过期异步回调作废
    this.renderedRev = -1; // 面板已渲染的公告 rev（onSync 判新旧）
    this.redraft = '';    // denied 后要回填的草稿（一次性）
    this.sending = false; // 发布在途（防连点）
  }

  /** 打开 key 房的公告面板（缺省当前房）。 */
  open(key) {
    this.key = key || this.book.current;
    this.close();
    const room = this.book.rooms.get(this.key);
    if (!room) return;
    const tok = ++this.token;
    this.#render(room.notice, t('读取中…'));
    getRoomNotice(this.key)
      .then((body) => {
        if (tok !== this.token || !this.el) return; // 面板已关/已换房
        this.#render(body?.notice || null, '');
      })
      .catch(() => { if (tok === this.token && this.el) this.#render(room.notice, ''); }); // HTTP 读失败退回状态层数据
  }

  close() {
    this.token++; // 在途回调全部作废
    if (this.el) document.removeEventListener('keydown', this.#escHandler);
    this.el?.remove();
    this.el = null;
    this.renderedRev = -1;
    this.sending = false;
  }

  /** app.js 的 'notice' 提示钩子：广播到了就刷面板（发布成功的唯一确认）。 */
  onSync(key) {
    if (!this.el || key !== this.key) return;
    const room = this.book.rooms.get(this.key);
    if (!room || !room.notice) { this.close(); return; } // 撤下——面板随公告一起收
    if (room.notice.rev === this.renderedRev) return;
    this.redraft = '';
    this.#render(room.notice, t('已生效'));
  }

  /** app.js 的 onOwnerFrame 钩子：denied（含 rev 冲突）重开面板保草稿。 */
  onDenied(frame) {
    if (this.sending) { this.sending = false; }
    const keep = this.redraft;
    const msg = frame.text || t('公告保存被拒');
    this.toast.show(/冲突/.test(msg) ? t('{msg}——草稿已保留，重新打开面板合并', { msg }) : msg, 'err');
    if (/冲突/.test(msg) && keep) {
      // 面板可能已被乐观关闭：重开并回填草稿，等使用者对照新内容重试
      this.redraft = keep;
      this.open(this.key);
    }
  }

  /**
   * @param {import('../wire/wire.js').Notice|null} notice
   * @param {string} status 状态行文案（'' = 无）
   */
  #render(notice, status) {
    this.el?.remove();
    const room = this.book.rooms.get(this.key);
    const isOwner = !!this.book.owner;
    const draft = this.redraft || notice?.content || '';
    this.redraft = '';
    this.renderedRev = notice?.rev ?? -1;

    const ov = document.createElement('div');
    ov.className = 'overlay';
    const card = document.createElement('div');
    card.className = 'overlay-card notice-panel';
    const meta = notice
      ? `${esc(notice.by || t('房主'))} · ${notice.ts ? fmtDateTime(notice.ts) : ''} · v${notice.rev}`
      : t('本房还没有公告');
    card.innerHTML =
      `<div class="overlay-head"><b>${icon('megaphone', 14)} ${t('群公告 · {room}', { room: esc(room?.name || this.key) })}</b>` +
      `<button type="button" class="np-close" title="${t('关闭')}">${icon('close', 14)}</button></div>` +
      `<div class="np-meta">${meta}</div>` +
      (notice
        ? `<div class="np-body">${markdown(notice.content)}</div>` // xss-ok: markdown() 先 esc 后排版（markdown.js:50）
        : `<div class="np-empty">${t('房主可在这里发布一条全房可见的公告——置顶在消息流上方，发布时通知全体牛马。')}</div>`) +
      (isOwner
        ? '<div class="np-edit">' +
          `<textarea class="np-text" rows="8" placeholder="${t('公告内容（markdown）——首行会作为摘要展示在置顶条与留痕行')}"></textarea>` +
          `<label class="np-wake"><input type="checkbox" checked>${t('发布并通知全员（唤醒牛马，回复会回到对话流）')}</label>` +
          '<div class="overlay-actions">' +
          (notice ? `<button type="button" class="np-clear">${t('撤下公告')}</button>` : '') +
          `<button type="button" class="np-save primary">${t('发布')}</button>` +
          '</div></div>'
        : '');
    const statusEl = document.createElement('div');
    statusEl.className = 'overlay-status';
    statusEl.textContent = status || '';
    card.appendChild(statusEl);

    card.querySelector('.np-close').addEventListener('click', () => this.close());
    ov.addEventListener('click', (ev) => { if (ev.target === ov) this.close(); });
    document.removeEventListener('keydown', this.#escHandler); // 重渲染去重，close 统一回收
    document.addEventListener('keydown', this.#escHandler);

    if (isOwner) {
      const ta = card.querySelector('.np-text');
      const wake = card.querySelector('.np-wake input');
      ta.value = draft;
      ta.addEventListener('keydown', (ev) => {
        if (ev.key === 'Escape') { ev.stopPropagation(); this.close(); }
      });
      card.querySelector('.np-save').addEventListener('click', () => {
        const text = ta.value.trim();
        if (!text) { statusEl.textContent = t('公告内容不能为空（撤下请用撤下按钮）'); return; }
        // expect_rev：更新=当前 rev，首发=0（必须不存在）——kb_write 契约
        const expect = notice?.rev ?? 0;
        this.redraft = ta.value;
        this.sending = true;
        statusEl.textContent = t('已提交…');
        this.book.frameTo(this.key, {
          type: Msg.NoticeSave, text, expect_rev: expect, silent: !wake.checked,
        }, t('发布群公告')) || (statusEl.textContent = t('未识别到房主身份，发布不可用'));
      });
      card.querySelector('.np-clear')?.addEventListener('click', async () => {
        // 危险操作走规范对话框（警告图标＋「撤下」主操作），替掉原生 confirm
        const ok = await Dialog.confirm({
          title: t('撤下群公告'), text: t('撤下后留痕行仍在历史里。'), danger: true, okText: t('撤下'),
        });
        if (!ok) return;
        this.sending = true;
        statusEl.textContent = t('已提交…');
        this.book.frameTo(this.key, {
          type: Msg.NoticeSave, text: '', expect_rev: notice?.rev ?? 0,
        }, t('撤下群公告')) || (statusEl.textContent = t('未识别到房主身份，撤下不可用'));
      });
    }

    ov.appendChild(card);
    document.body.appendChild(ov);
    this.el = ov;
    if (isOwner) card.querySelector('.np-text')?.focus();
  }

  #escHandler = (ev) => {
    if (ev.key === 'Escape' && this.el) {
      ev.stopPropagation();
      this.close();
      document.removeEventListener('keydown', this.#escHandler);
    }
  };
}

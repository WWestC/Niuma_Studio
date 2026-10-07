// spacegate.js — 项目空间（v2.12）。
//
// 项目空间是工作室全部项目的一面：两副面孔共用同一张面板——
//
// ① 首启守门（forced）：工作室刚开张时除了常驻大厅（Niuma_Studio）
//    没有任何项目——没有项目就没有项目房，牛马无处开工。启动门落下
//    后这层门上墙：全屏不透明、无关闭钮、Esc 无效，唯一出路是立项
//    （创建即自动开张，落门进新房）。
// ② 侧栏入口（browse）：侧栏「项目空间」按钮（原灰色「办公室」标题
//    的位置）随时可开——列出全部项目（大厅＋各项目，草稿/归档也照
//    列），点开张中的项目进它的房，点草稿/归档去项目管理看它；非大
//    厅行行尾常显删除钮（v2.16 项目除名，开张行同权：typed 口令确认，
//    工作区目录与 git 仓库不动；开张中的行走「先归档再除名」的一体
//    流，不必绕道项目管理）；底部常驻「＋ 立项」。可关闭（✕ / Esc /
//    点遮罩）。
//
// 判据与边界：
// - 「有项目」＝项目册里存在大厅以外的任何项目（draft/active/archived
//   皆算——创建过就算；归档封存的项目仍可复活，把人锁死在门外反而
//   断了复活的路）。
// - 房册还没读到项目清单（book.projects 为 null——读面失败/启动未落）
//   时 fail-open 不设门：服务连不上时启动门本就不会开。
// - 访客页与只读态（房主未识别）由 app.js 不设面板、藏入口：前者看的
//   是别人家的项目，后者无写面、立不了项。
// - 守门不只守首启：app.js 把它接在房册 'rooms' 变动上——项目册真的
//   被清空（手工改库等）时门会重新上墙；browse 开着时同一拍重画列表。
//
// 层级：面板 z-index 96，压过侧栏与启动门（90 档）——守门时无路可
// 绕；面板自己发起的反馈都在面板之上：对话框/抽屉 100、全局 toast
// 105、select/时钟弹层 110（zladder.dom.test.js 钉着这架梯子）。立项
// 表单与目录选择器仍挂进面板内（projectnew 的 mount 参数）——.overlay
// 自己的 60 只在面板的栈上下文里比高低，永远画在面板上。

import { LobbyKey } from '../wire/wire.js';
import { postProject, postProjectAction, postProjectDelete } from '../wire/api.js';
import { esc, icon, memberColor, groupAvatarText, safeColor } from './dom.js';
import { Dialog } from './feedback.js';
import { t } from './i18n.js';
import { ProjectNewForm } from '../project/projectnew.js';

export class SpaceGate {
  /**
   * @param {Object} opts
   * @param {import('../chat/rooms.js').RoomBook} opts.book 房册（projects＝全量项目清单，refreshRooms 落账）
   * @param {{show: (text: string, kind?: string) => void}} opts.toast 结果反馈（开张/立项 toast）
   * @param {string} [opts.ownerName] 房主名（立项 created_by）
   * @param {(key: string) => void} [opts.onInspect] 草稿/归档行的落点（app.js 喂：项目管理指名落位）
   */
  constructor(opts) {
    this.book = opts.book;
    this.toast = opts.toast;
    this.ownerName = opts.ownerName || '';
    this.onInspect = opts.onInspect || null;
    this.up = false;          // 面板在屏上（forced 与 browse 共用）
    this.forced = false;      // 首启守门模式（无关闭路径）
    this.form = null;         // 立项表单（el 空＝已关，可重开）
    this._dropWaiters = [];   // hold() 的等待者（守门落门时一并放行）
    const el = document.createElement('div');
    el.id = 'space-gate';
    el.hidden = true;
    document.body.appendChild(el);
    this.el = el;
    el.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-cta]')) { this.openCreate(); return; }
      if (ev.target === el || ev.target.closest('[data-close]')) { this.close(); return; }
      const del = ev.target.closest('[data-pdel]');
      if (del) { this.#confirmDelete(del.dataset.pdel); return; }
      const item = ev.target.closest('[data-pick]');
      if (item) this.pick(item.dataset.pick);
    });
    // Esc 归 browse 面板（守门模式无 Esc；立项表单开着时先归表单——
    // projectnew 自己的 Esc 只关表单，这里让一拍）
    this.onKey = (ev) => {
      if (ev.key !== 'Escape' || this.forced || !this.up) return;
      if (this.form?.el) return;
      this.close();
    };
    document.addEventListener('keydown', this.onKey);
  }

  // ---------- 首启守门（boot） ----------

  /** 项目册里大厅以外的项目是否存在（任何状态——创建过就算）。 */
  hasProjects() {
    const list = this.book.projects;
    return Array.isArray(list) && list.some((p) => p && p.key !== LobbyKey);
  }

  /** 守门该不该守：清单已落且没有任何非大厅项目；清单未落（null）不守。 */
  shouldHold() {
    return Array.isArray(this.book.projects) && !this.hasProjects();
  }

  /** 随项目册同步：该守就上墙；已在屏上时守门落门、browse 重画列表
   *  （browse 开着删光项目的清空态也照重画——面板保持可关，不中途转
   *  守门：把人关在已打开的面板里反而断了出路）。 */
  sync() {
    if (this.shouldHold()) {
      if (!this.up) { this.rise(); return; }
      if (this.forced) return; // 守门已在墙，无需动
      this.#render();
    } else if (this.up) {
      if (this.forced) this.drop();
      else this.#render(); // browse 开着：清单变了原地重画
    }
  }

  rise() {
    if (this.up) return;
    this.up = true;
    this.forced = true;
    this.#render();
    this.el.hidden = false;
  }

  drop() {
    if (!this.up) return;
    this.up = false;
    this.forced = false;
    this.el.hidden = true;
    const waiters = this._dropWaiters;
    this._dropWaiters = [];
    for (const w of waiters) w();
  }

  /** 等守门落门（启动流程的第二道门：无项目不进办公室）。门没上立即放行。 */
  hold() {
    if (!this.up) return Promise.resolve();
    return new Promise((resolve) => this._dropWaiters.push(resolve));
  }

  // ---------- 侧栏入口（browse） ----------

  /** 侧栏「项目空间」入口：开 browse 面板（守门已在屏上则本就开着）。 */
  open() {
    if (this.up) return;
    this.up = true;
    this.forced = false;
    this.#render();
    this.el.hidden = false;
  }

  /** 收 browse 面板（✕ / Esc / 点遮罩）。守门模式不可关——唯一出路是立项。 */
  close() {
    if (!this.up || this.forced) return;
    this.up = false;
    this.el.hidden = true;
    this.form?.close(); // 表单还开着一起收（理论到不了：表单 Esc 先于面板）
  }

  /** 列表行落位：开张中的项目进它的房；草稿/归档交给 onInspect（项目
   *  管理：开张/复活都在概览卡上）。 */
  pick(key) {
    const p = (this.book.projects || []).find((x) => x && x.key === key);
    if (!p) return;
    this.close();
    if (p.status === 'active' && this.book.rooms?.has(key)) {
      this.book.switchTo(key);
    } else {
      this.onInspect?.(key);
    }
  }

  // ---------- 立项（两副面孔共用的唯一创建链路） ----------

  /** 开立项表单（挂进面板内——层级见类头）。 */
  openCreate() {
    if (this.form?.el) return; // 已开着（el 空＝上次已关，可重开）
    this.form = new ProjectNewForm({
      mount: this.el,
      onSubmit: (payload) => this.create(payload),
    });
  }

  /** 立项＋自动开张。POST /projects 先落 draft，紧接 lifecycle activate
   *  开张（开张才生成项目房、招系统岗）；随后房册刷新收下新房、切进
   *  新房、放行（守门落门／browse 原地重画）。立项失败由表单内联展示
   *  （projectnew 的 catch）；开张失败不拦人——草稿已在册，进项目管理
   *  概览的「开张」补上即可。 */
  async create(payload) {
    const p = await postProject({ ...payload, by: this.ownerName || undefined });
    // git 引导回执（v2.14）：失败如实亮（草稿已在册，notes 指路补法）；
    // 新建了仓库给一条 ok 提示，其余静默。
    const boot = p.git_boot;
    if (boot && !boot.ok) {
      this.toast.show(t('立项 git 引导未全成：{why}', { why: (boot.notes || []).join('；') }), 'err');
    } else if (boot && boot.initiated) {
      this.toast.show(t('已自动初始化 git 仓库（基线 {base}）', { base: boot.base_branch || 'main' }), 'ok');
    }
    let opened = true;
    try {
      await postProjectAction(p.key, 'activate');
    } catch (err) {
      opened = false;
      this.toast.show(t('已立项：{name}（{key}，草稿）——自动开张未成：{why}', { name: p.name, key: p.key, why: err?.message || err }), 'err');
    }
    await this.book.refreshRooms(); // 房册收下新房（onChange 链带各板跟房）
    if (opened) {
      this.toast.show(t('已开张：{name}（{key}）——正在进入项目办公室', { name: p.name, key: p.key }), 'ok');
      this.book.switchTo(p.key); // 落进新项目办公室
    }
    this.sync(); // 项目已在册 → 守门落门放行／browse 重画列表
  }

  // ---------- 删除（browse 面的行尾入口；唯一不可逆的生命周期动词） ----------

  /** 删除项目（非大厅行皆亮，开张中也是）：typed 口令确认（重置面同族
   *  的 disarm 写法）——除名不可逆，连草稿也要亲手写出「删除」两个字。
   *  开张中的行文案换成一体流预告：先归档（收房折编）再除名。 */
  #confirmDelete(key) {
    const p = (this.book.projects || []).find((x) => x && x.key === key);
    if (!p || key === LobbyKey) return;
    const name = p.name || key;
    const lead = p.status === 'active'
      ? t('「{name}」（{key}）开张中——将先归档（房间冻结、成员离编），再从项目册除名：成员档案、任务/需求/会议等台账与房间历史一并删除。', { name, key })
      : t('将把「{name}」（{key}）从项目册除名：成员档案、任务/需求/会议等台账与房间历史一并删除。', { name, key });
    const dlg = Dialog.show({
      title: t('删除项目'),
      kind: 'warning',
      size: 's',
      html:
        '<p>' + esc(lead) + '</p>' +
        '<p class="dim">' + esc(t('工作区目录与 git 仓库原样保留，不会删你的文件。此操作不可撤销。')) + '</p>' +
        '<p>' + esc(t('输入「{pass}」以确认：', { pass: t('删除') })) + '</p>' +
        `<input class="input" data-sg-del-input placeholder="${esc(t('删除'))}" autocomplete="off">`,
      actions: [
        { label: t('取消') },
        { label: t('删除项目'), primary: true, danger: true, value: true },
      ],
    });
    // 确认钮在口令对上之前保持禁用（渲染是同步的，此时已在文档里）
    const arm = document.querySelector('.fb-dlg-foot [data-i="1"]');
    const input = document.querySelector('[data-sg-del-input]');
    if (arm && input) {
      arm.disabled = true;
      const pass = t('删除'); // 口令随语言（en 下为 delete——提示语与占位同源）
      input.addEventListener('input', () => {
        arm.disabled = input.value.trim() !== pass;
      });
      setTimeout(() => input.focus(), 60); // 等入场动画落定再抢焦点
    }
    dlg.then((ok) => {
      if (ok === true) this.deleteProject(key);
    });
  }

  /** 删除执行（POST /p/{key}/delete，口令 DELETE 由封装代填）：服务端
   *  同步清人＋清台账＋清房间档案＋除名；前端只管刷新一拍（房册刷新
   *  的 onChange 链会带 sync() 原地重画列表）。开张中的行先补一记
   *  lifecycle archive——服务端删除面只吃草稿/归档（归档收房折编，删除
   *  只扫残渣），一体流替用户走这步；归档成了删除仍被拒时项目停在归
   *  档态（可重试可复活），不是残局。 */
  async deleteProject(key) {
    const p = (this.book.projects || []).find((x) => x && x.key === key);
    if (p && p.status === 'active') {
      try {
        await postProjectAction(key, 'archive');
      } catch (err) {
        this.toast.show(t('归档未成——未执行删除：{why}', { why: err?.message || err }), 'err');
        return;
      }
    }
    let r;
    try {
      r = await postProjectDelete(key);
    } catch {
      this.toast.show(t('连接失败——未执行任何删除'), 'err');
      return;
    }
    if (!r.ok) {
      const why = r.data?.error || t('删除被拒（HTTP {status}）', { status: r.status });
      this.toast.show(p && p.status === 'active' ? why + t('（项目已归档——可重试删除或去项目管理复活）') : why, 'err');
      return;
    }
    this.toast.show(r.data?.note || t('项目已删除'), 'ok');
    await this.book.refreshRooms(); // onChange 链 → sync()：列表原地少一行
    this.sync();
  }

  // ---------- 渲染 ----------

  #render() {
    const list = Array.isArray(this.book.projects) ? this.book.projects : [];
    const head =
      '<div class="sg-head">' +
        '<div class="sg-brand"><span class="sg-name">' + t('牛马工作室') + '</span><span class="sg-en">NIUMA STUDIO</span></div>' +
        (this.forced ? '' : `<button type="button" class="icon-btn" data-close title="${t('关闭')}">${icon('close')}</button>`) +
      '</div>';
    const body = this.forced
      ? // 守门空态：大厅以外一个项目都没有——大按钮立项，别的不放
        '<div class="sg-empty">' +
          '<strong>' + t('工作室还没有项目') + '</strong>' +
          '<p>' + t('这里空空如也。先立一个项目——成员会话、任务台账与排期都以项目为单位组织，没有项目，牛马们无处开工。') + '</p>' +
        '</div>' +
        `<button type="button" class="btn gold sg-cta" data-cta>${t('立项 · 创建第一个项目')}</button>` +
        `<p class="sg-note">${t('创建后自动开张，牛马即可进房开工')}</p>` +
        `<p class="sg-note sg-caution">${t('工作区是 AI 成员的会话出生地——他们会在该目录读写文件、执行命令，请指向本项目的专用目录')}</p>`
      : // browse：全部项目一列（大厅＋各项目，草稿/归档照列）＋底部立项
        '<div class="sg-list">' + list.map((p) => this.#row(p)).join('') + '</div>' +
        `<button type="button" class="btn small sg-new" data-cta>${t('+ 立项')}</button>`;
    this.el.innerHTML =
      `<div class="sg-card sg-panel" role="dialog" aria-modal="true" aria-label="${t('项目空间')}">` +
        head +
        `<h1 class="sg-title">${t('项目空间')}</h1>` +
        body +
      '</div>';
  }

  /** 一行项目：头像色随 key（与侧栏房行同一语言），名＋key＋状态小签；
   *  开张中的行点进房，草稿/归档的行去项目管理。非大厅行行尾另带常显
   *  删除钮（开张中也亮——删除执行面自会先归档；button 不能嵌套，可删
   *  行拆成行容器＋主按钮＋删除钮三件）。 */
  #row(p) {
    const room = this.book.rooms?.get(p.key);
    const status = p.status === 'draft' ? t('草稿 · 未开张') : p.status === 'archived' ? t('已归档') : '';
    const n = room?.members?.size || 0;
    const cur = p.key === this.book.current && p.status === 'active';
    const inner =
      `<span class="sg-item-avatar" style="background:${safeColor(memberColor(p.key))}">${esc(groupAvatarText(p.name || p.key))}</span>` +
      '<span class="sg-item-main">' +
        `<span class="sg-item-top"><span class="sg-item-name">${esc(p.name || p.key)}</span>` +
        (status ? `<span class="sg-item-status">${status}</span>` : '') +
        (n > 0 ? `<span class="sg-item-n">${t('{n} 人', { n: `<b>${n}</b>` })}</span>` : '') +
        (cur ? `<span class="sg-item-cur">${t('当前')}</span>` : '') +
        '</span>' +
        `<span class="sg-item-key">${esc(p.key)}</span>` +
      '</span>';
    if (p.key === LobbyKey) {
      return `<button type="button" class="sg-item${cur ? ' cur' : ''}" data-pick="${esc(p.key)}">${inner}</button>`;
    }
    return `<div class="sg-item has-del${cur ? ' cur' : ''}">` +
      `<button type="button" class="sg-item-pick" data-pick="${esc(p.key)}">${inner}</button>` +
      `<button type="button" class="sg-item-del" data-pdel="${esc(p.key)}" title="${esc(t('删除项目'))}" aria-label="${esc(t('删除项目'))}">${icon('trash', 14)}</button>` +
    '</div>';
  }
}

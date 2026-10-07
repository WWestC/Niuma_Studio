// kb.js — the blackboard board (v2 P6 merge): the retired Ebiten
// blackboard's pages, landed in the workbench. 说明书 renders as
// Markdown (manual.md 是 md 长文，chat/markdown.js 的同一渲染器，无
// token 装扮)；AI 接入 keeps the verbatim prompt (它是复制源，渲染会失
// 真); 文档 lists and edits the manual-docs store over the same
// kb_write wire the CLI speaks (expect_rev keeps saves conditional) —
// every doc opens straight into the md face (编辑 flips to the source
// editor, 预览 flips back with the draft kept; historical snapshots
// are the same face, read-only). Writes
// ride the lobby owner face — receipts land as Kb frames in app.js and
// walk this board back to freshness. (公告页随黑板编辑器退役——
// 群公告在牛马对话区：置顶条＋头栏 📌 面板。)

import {
  getManual, getPrompt, getDocs, getDoc, getDocHistory,
} from '../wire/api.js';
import { Msg, LobbyKey } from '../wire/wire.js';
import { esc, fmtAge, fmtDateTime, copyToClipboard } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from '../chat/markdown.js';
import { ChroniclePanel, chronicleDocKey } from './chronicle.js';
import { AchievementsPanel } from './achievements.js'; // t_188（r_18）
import { CockpitPanel } from './cockpit.js'; // t_205（r_24）

const TABS = [
  { id: 'manual', label: t('说明书') },
  { id: 'docs', label: t('文档') },
  { id: 'chronicle', label: t('工作室志') },
  { id: 'achievements', label: t('成就') },
  { id: 'cockpit', label: t('驾驶舱') }, // t_205（r_24）：房主今日总览页签
  { id: 'ai', label: t('AI 接入') },
];

// the long-text tabs share one loader+face; md=true renders the body
// through markdown.js, the prompt tab stays verbatim (copy source)
const TEXT_FACE = {
  manual: {
    fetch: getManual,
    title: t('说明书'),
    copy: t('复制全文'),
    empty: t('说明书为空（kb/manual.md 目前没有内容）'),
    hint: t('内嵌手册 · 办公室内人与 AI 看到的是同一份'),
    md: true,
  },
  ai: {
    fetch: getPrompt,
    title: t('接入提示词'),
    copy: t('复制提示词'),
    empty: t('提示词不可用'),
    hint: t('粘贴给任意 AI 应用（如 zcode），它自定名称/身份后经 CLI 入座'),
  },
};

// docTree: 键名按斜杠路径归成文件树——design/r20 落进 design 文件夹，
// a/b/c 嵌套两层，无斜杠的根键留根。同层保持首见次序（列表本身更新
// 时间倒序，最近动过的文件夹在上），count 为文件夹下文档总数（含嵌
// 套）。纯函数，web 测试钉。
export function docTree(docs) {
  const root = { dirs: new Map(), docs: [] };
  for (const d of docs || []) {
    const parts = d.key.split('/');
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const name = parts[i];
      if (!node.dirs.has(name)) node.dirs.set(name, { dirs: new Map(), docs: [] });
      node = node.dirs.get(name);
    }
    node.docs.push(d);
  }
  const fill = (node) => {
    node.count = node.docs.length;
    for (const sub of node.dirs.values()) node.count += fill(sub);
    return node.count;
  };
  fill(root);
  return root;
}

// docsInScope：文档元数据按办公室过滤（v2.9 三层口径）——大厅是独
// 立的一间房（niuma_studio 本体的事务房），看自己的 ops/ 私档＋
// p/default/ 防御位＋公共架（一切非 p/ 键）；项目办公室看「公共文档
// 库＋本房项目文档库」两层（公共面是全室共享资产——岗位手册、说明
// 书——项目成员照读，但 ops/ 是大厅私档、p/<other>/ 是别家私架，永
// 不进）。纯函数，web 测试钉。
export function docsInScope(docs, roomKey) {
  if (!roomKey || roomKey === LobbyKey) {
    return (docs || []).filter((d) =>
      !d.key.startsWith('p/') || d.key.startsWith('p/default/'));
  }
  const pre = `p/${roomKey}/`;
  return (docs || []).filter((d) =>
    (!d.key.startsWith('p/') && !d.key.startsWith('ops/')) || d.key.startsWith(pre));
}

// splitShelves：项目办公室的两层架切分——own＝本房 p/<key>/ 架在前，
// pub＝公共架（不含大厅 ops/ 私档）在后（列表按此序渲染两组）。大厅
// 不切（单架）。纯函数。
export function splitShelves(docs, roomKey) {
  const list = docsInScope(docs, roomKey);
  if (!roomKey || roomKey === LobbyKey) return { pub: [], own: list };
  const pre = `p/${roomKey}/`;
  const own = list.filter((d) => d.key.startsWith(pre));
  const pub = list.filter((d) => !d.key.startsWith(pre));
  return { pub, own };
}

export class KbBoard {
  constructor(root, book, toast) {
    this.root = root;
    this.book = book;
    this.toast = toast;
    this.tab = 'manual';
    this.texts = {};        // text-tab id → {state, body} cache
    this.docs = null;       // KbDocMeta[] | 'error'
    this.docKey = null;     // the selected doc's key
    this.doc = null;        // the loaded KbDoc (rev 0 = live head)
    this.docRev = 0;        // >0 = a read-only historical snapshot
    this.hist = null;       // KbDocRev[] of the selected doc
    this.newOpen = false;
    this.pendingNew = null; // the creation-in-flight key (receipt match)
    this.preview = true;    // docs tab: 文档直开 md 渲染面，「编辑」进源码
    this.draft = null;      // 编辑中途切预览的未保存草稿 {title, body}
    this.folded = new Set(); // 文档树折叠中的文件夹完整路径（design、design/sub）

    const tabs = TABS.map((tb) =>
      `<button type="button" class="ptab" data-tab="${tb.id}" role="tab">${tb.label}</button>`).join('');
    root.innerHTML =
      '<div class="people-wrap">' +
        '<div class="people-main">' +
          '<div class="people-tabs" role="tablist">' +
            tabs +
            '<span class="tabs-spacer"></span>' +
            '<button type="button" class="btn small gold" data-cta-newdoc hidden>+ ' + esc(t('新建文档')) + '</button>' +
          '</div>' +
          '<div class="people-pane" id="kb-pane"></div>' +
        '</div>' +
      '</div>';
    this.pane = root.querySelector('#kb-pane');
    this.newBtn = root.querySelector('[data-cta-newdoc]');
    root.querySelector('.people-tabs').addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-tab]');
      if (btn) this.switchTab(btn.dataset.tab);
      if (ev.target.closest('[data-cta-newdoc]')) this.openNewDoc();
    });
    this.pane.addEventListener('click', (ev) => this.onClick(ev));
    this.switchTab('manual');
  }

  // ── lifecycle ───────────────────────────────────────────────────

  /** Board entry (hash route or first paint). */
  revealed() { this.loadTab(); }

  /** Board exit (app.js route)：驾驶舱这类轮询面板离场即停（不看就不烧）；
   *  志/成就在途加载同拍作废，慢回包不往藏起来的 pane 里白写。 */
  conceal() {
    if (this.cockpit) this.cockpit.stop();
    if (this.chronicle) this.chronicle.cancel();
    if (this.achievements) this.achievements.cancel();
  }

  /** Cross-board refresh tap (Kb receipts/broadcasts from app.js). */
  requestRefresh() {
    if (this.tab === 'docs') this.loadDocs();
  }

  /** Deep link (people detail's manual link): select + open one doc. */
  openDoc(key) {
    this.switchTab('docs');
    this.docKey = key;
    if (Array.isArray(this.docs)) {
      this.renderDocsShell();
      this.renderDocList();
    }
    this.loadDoc(key, 0);
  }

  /** Hash deep link（t_153）：#/kb/chronicle 落到志页签。 */
  openTabDeep(tab) { this.switchTab(tab); }

  switchTab(tab) {
    if (!TABS.some((t) => t.id === tab)) return;
    if (this.tab !== tab) {
      if (tab !== 'cockpit' && this.cockpit) this.cockpit.stop(); // t_205：切走驾驶舱即停轮询（不看就不烧）
      // 离场面板的在途请求当场作废：慢回包的 paint 若落在切走之后，
      // 会把新页签刚画好的内容整面重写回旧页签（快速切换「卡住」的根
      // 因之一）；三个面板各自 token 自增，迟到回包对不上号即弃。
      if (tab !== 'chronicle' && this.chronicle) this.chronicle.cancel();
      if (tab !== 'achievements' && this.achievements) this.achievements.cancel();
    }
    this.tab = tab;
    this.newOpen = false;
    this.newBtn.hidden = tab !== 'docs';
    this.root.querySelectorAll('[data-tab]').forEach((b) =>
      b.classList.toggle('active', b.dataset.tab === tab));
    this.loadTab();
  }

  loadTab() {
    if (this.tab === 'docs') { this.loadDocs(); return; }
    if (this.tab === 'chronicle') { this.loadChronicle(); return; } // t_153：志面板独立加载（非纯文本面）
    if (this.tab === 'achievements') { // t_188：成就页签（同族独立加载）
      if (!this.achievements) this.achievements = new AchievementsPanel();
      this.achievements.load(this.pane);
      return;
    }
    if (this.tab === 'cockpit') { // t_205：驾驶舱（20s 轮询面板——load 起拍、stop 停拍）
      if (!this.cockpit) this.cockpit = new CockpitPanel(() => this.chronicleSources());
      this.cockpit.load(this.pane);
      return;
    }
    this.loadText(this.tab);
  }

  // ── the chronicle tab（t_153，r_10）──────────────────────────────

  loadChronicle() {
    if (!this.chronicle) this.chronicle = new ChroniclePanel();
    // 按办公室隔离：黑板读当前房的志（大厅 ops/chronicle，项目各一本）
    this.chronicle.load(this.pane, chronicleDocKey(this.book?.current));
  }

  /** 驾驶舱④的志源清单：大厅＋各项目房，[{key, room}] 顺序稳定（大厅
   *  在前）——项目志未建档时 getDoc 404，驾驶舱按空处理不当错误。 */
  chronicleSources() {
    const out = [{ key: 'ops/chronicle', room: '' }];
    const rooms = this.book?.rooms;
    if (rooms) {
      for (const [key, r] of rooms) {
        if (key === LobbyKey) continue;
        out.push({ key: chronicleDocKey(key), room: r?.name || key });
      }
    }
    return out;
  }

  /** 换房（app.js book.onChange 'switch' 转来）：黑板跟当前办公室走——
   *  文档与工作室志换 scope 重载，旧房的选中位弃（新房列表落首篇）；
   *  说明书/AI 接入/成就是全室共享面，不动。 */
  onRoomSwitch() {
    this.docKey = null;
    this.doc = null;
    this.hist = null;
    this.docs = null;
    this.archivedDocs = null;
    this.draft = null;
    this.preview = true;
    if (this.tab === 'docs') this.loadDocs();
    if (this.tab === 'chronicle') this.loadChronicle();
  }

  // ── the plain-text tabs ─────────────────────────────────────────

  async loadText(id) {
    if (this.tab !== id) return;
    const face = TEXT_FACE[id];
    if (!this.texts[id] || this.texts[id].state === 'error') {
      this.texts[id] = { state: 'loading' };
      this.renderText(id);
      try {
        this.texts[id] = { state: 'ready', body: await face.fetch() };
      } catch (err) {
        this.texts[id] = { state: 'error', error: err };
      }
    }
    if (this.tab === id) this.renderText(id);
  }

  renderText(id) {
    const face = TEXT_FACE[id];
    const st = this.texts[id] || { state: 'loading' };
    let body;
    if (st.state === 'loading') {
      body = '<div class="sk-line"></div><div class="sk-line w60"></div>'.repeat(3);
    } else if (st.state === 'error') {
      body =
        `<div class="state-card error"><strong>${esc(t('{a}读取失败', { a: face.title }))}</strong>` +
        `<span>${esc(st.error?.message || String(st.error))}</span>` +
        '<button type="button" class="btn" data-retry>' + esc(t('重试')) + '</button></div>';
    } else if (!(st.body || '').trim()) {
      body = `<div class="state-card"><strong>${esc(face.empty)}</strong></div>`;
    } else if (face.md) {
      body = `<div class="kb-md">${markdown(st.body)}</div>`;
    } else {
      body = `<pre class="pre kb-pre">${esc(st.body)}</pre>`;
    }
    const hasBody = st.state === 'ready' && (st.body || '').trim();
    const copyBtn = hasBody
      ? `<button type="button" class="btn small" data-copy-text>${esc(face.copy)}</button>` : '';
    const foot = id === 'ai'
      ? `<div class="kb-pane-foot dim small">curl http://${location.host}/prompt | pbcopy</div>` : '';
    this.pane.innerHTML =
      `<div class="kb-pane-head"><span class="dim small">${esc(face.hint)}</span>${copyBtn}</div>` +
      body + foot;
  }

  async copyText() {
    const st = this.texts[this.tab];
    if (!st || st.state !== 'ready') return;
    const ok = await copyToClipboard(st.body);
    this.toast.show(ok ? t('已复制 · 粘贴给任意 AI 应用') : t('复制失败——可全选手动复制'), ok ? 'ok' : 'err');
  }

  // （t_209 的黑板「评审纪要」页签已撤——纪要即文档：ops/meetings/* 在
  // 文档页签的文件树里天然成 meetings 文件夹，独立列表是同一份索引的
  // 重复视图。按需求看的入口在需求行折叠时间线（reqs.js）。）

  // ── the docs tab ────────────────────────────────────────────────

  async loadDocs() {
    if (this.newOpen) return; // the creation form owns the pane
    let docs, archived;
    try {
      // v3 治理：在编与归档架并行取（归档架取败不致命——保持旧值）
      [docs, archived] = await Promise.all([
        getDocs(),
        getDocs({ archived: true }).catch(() => null),
      ]);
    } catch {
      docs = 'error';
    }
    if (this.tab !== 'docs' || this.newOpen) return;
    // 按办公室隔离：列表装当前房的两层架（大厅＝共享架，项目＝公共＋
    // 本房 p/<key>/）。默认选中本房私架的第一篇，空则落公共架首篇。
    if (Array.isArray(docs)) docs = docsInScope(docs, this.book?.current);
    if (Array.isArray(archived)) archived = docsInScope(archived, this.book?.current);
    this.docs = docs;
    if (Array.isArray(archived)) this.archivedDocs = archived;
    if (!this.docKey && Array.isArray(docs) && docs.length) {
      const { own } = splitShelves(docs, this.book?.current);
      this.docKey = (own[0] || docs[0]).key;
    }
    this.renderDocsShell();
    this.renderDocList();
    if (this.docKey) this.loadDoc(this.docKey, 0);
  }

  async loadDoc(key, rev) {
    if (this.tab !== 'docs') return;
    this.doc = { state: 'loading', key };
    this.docRev = rev;
    this.renderDocMain();
    let doc, hist;
    try {
      const histJob = rev > 0 ? Promise.resolve(null) : getDocHistory(key).catch(() => null);
      [doc, hist] = await Promise.all([getDoc(key, rev), histJob]);
    } catch (err) {
      if (this.docKey !== key) return;
      this.doc = { state: 'error', key, error: err };
      this.renderDocMain();
      return;
    }
    if (this.docKey !== key || this.tab !== 'docs') return;
    this.doc = doc;
    this.hist = hist;
    this.renderDocMain();
  }

  renderDocsShell() {
    this.pane.innerHTML =
      '<div class="people-wrap">' +
        '<div class="doc-editor" id="kb-doc-main"></div>' +
        '<aside class="person-side doc-list" id="kb-doc-list"></aside>' +
      '</div>';
  }

// renderDocList: 在编文档按文件夹归成文件树（文件夹行可点折叠，folded
// 记完整路径；叶子行只显键名末段——路径已在树上，完整键留 title 提示）
// ＋底部「已归档」折叠架（默认收起，可点开浏览与出档——archived 文档
// Get 照读，loadDoc 同路，平铺显完整键）。
  renderDocList() {
    const el = this.pane.querySelector('#kb-doc-list');
    if (!el) return;
    if (this.docs === 'error') {
      el.innerHTML =
        '<div class="state-card error"><strong>' + esc(t('文档库读取失败')) + '</strong>' +
        '<button type="button" class="btn small" data-retry>' + esc(t('重试')) + '</button></div>';
      return;
    }
    const docs = this.docs || [];
    const row = (d, baseOnly) => {
      const cur = d.key === this.docKey ? ' aria-current="true"' : '';
      const age = fmtAge(d.updated_ts) || '';
      const name = baseOnly ? d.key.split('/').pop() : d.key;
      return (
        `<button type="button" class="prow"${cur} data-doc="${esc(d.key)}">` +
          `<span class="pack-key" title="${esc(d.key)}">${esc(name)}</span>` +
          `<span class="pack-name">${esc(d.title || t('（无标题）'))}</span>` +
          `<span class="prank">${esc(age)}</span>` +
        '</button>');
    };
    const treeHTML = (node, prefix) => {
      let html = '';
      for (const [name, sub] of node.dirs) {
        const p = prefix ? `${prefix}/${name}` : name;
        const folded = this.folded.has(p);
        html +=
          `<button type="button" class="kb-tree-dir" data-dir="${esc(p)}"` +
            ` title="${esc(t(folded ? '点击展开 {p}' : '点击折叠 {p}', { p }))}">` +
            `<i class="kb-caret${folded ? '' : ' open'}">▸</i>` +
            `<span class="kb-dirname">${esc(name)}</span>` +
            `<span class="prank">· ${sub.count}</span>` +
          '</button>' +
          (folded ? '' : `<div class="kb-tree-kids">${treeHTML(sub, p)}</div>`);
      }
      return html + node.docs.map((d) => row(d, true)).join('');
    };
    // v2.9 两层架：项目办公室列表分「本项目文档库 / 公共文档库」两组
    //（本房私架在前——那是这间房的主场；公共架全室共享、只读心态浏览）。
    // 大厅仍是单架视图。
    const roomKey = this.book?.current;
    const isProject = roomKey && roomKey !== LobbyKey;
    const { pub, own } = isProject ? splitShelves(docs, roomKey) : { pub: [], own: docs };
    let html = '';
    if (isProject) {
      html += `<div class="group-head">${esc(t('本项目文档库'))} · ${own.length}</div>`;
      html += own.length ? treeHTML(docTree(own), '')
        : '<div class="side-empty">' + esc(t('本房还没有文档——右上「＋ 新建文档」落第一篇；下面是全室共享的公共文档库')) + '</div>';
      html += `<div class="group-head">${esc(t('公共文档库'))} · ${pub.length}</div>`;
      html += treeHTML(docTree(pub), '');
    } else {
      html = treeHTML(docTree(docs), '');
    }
    if (!docs.length) {
      html += '<div class="side-empty">' + esc(t('还没有手册文档 —— 右上「＋ 新建文档」落第一篇')) + '</div>';
    }
    const arc = this.archivedDocs || [];
    if (arc.length) {
      html += `<details class="fold kb-archived-fold"><summary>${esc(t('已归档 · {n}', { n: arc.length }))}</summary>${arc.map((d) => row(d, false)).join('')}</details>`;
    }
    // 列表头带办公室名：一间房一本架，谁家的文档一眼可辨
    el.innerHTML = `<div class="group-head">${esc(t('手册文档 · {n} · {scope}', { n: docs.length, scope: this.scopeLabel() }))}</div>` + html;
  }

  /** 当前黑板 scope 的房名（大厅/项目名）——文档列表头与新建表单共用。 */
  scopeLabel() {
    const k = this.book?.current;
    if (!k || k === LobbyKey) return t('Niuma_Studio');
    return this.book?.rooms?.get(k)?.name || k;
  }

  /** 项目办公室的新建键前缀（p/<key>/）；大厅无前缀（键即全址）。 */
  newDocPrefix() {
    const k = this.book?.current;
    return k && k !== LobbyKey ? `p/${k}/` : '';
  }

  renderDocMain() {
    const el = this.pane.querySelector('#kb-doc-main');
    if (!el) return;
    const d = this.doc;
    if (!d || d.state === 'loading') {
      el.innerHTML =
        '<div class="side-skeleton"><div class="sk-line w60"></div>' +
        '<div class="sk-line"></div><div class="sk-line w40"></div></div>';
      return;
    }
    if (d.state === 'error') {
      el.innerHTML =
        `<div class="state-card error"><strong>${esc(t('文档读取失败'))}</strong>` +
        `<span>${esc(d.error?.message || String(d.error))}</span>` +
        `<button type="button" class="btn" data-retry>${esc(t('重试'))}</button></div>`;
      return;
    }
    const ro = this.docRev > 0; // a historical snapshot: read-only
    const view = this.preview || ro; // 文档默认 md 渲染面；历史快照天然只读
    const meta = `${esc(d.key)} · ${esc(t('更新'))} ${esc(fmtDateTime(d.updated_ts))} · ` +
      `${esc(d.updated_by || '')} · ${esc(t('{n} 字', { n: (d.body || '').length }))}`;
    // v3 治理钮：在编可归档、已归档可出档（权限由服务端门兜底，
    // denied 走 onKbReceipt 的错误 toast）
    const lifeBtn = ro ? '' : (d.archived
      ? `<button type="button" class="btn small" data-doc-unarchive>${esc(t('出档'))}</button>`
      : `<button type="button" class="btn small" data-doc-archive>${esc(t('归档'))}</button>`);
    const archTag = d.archived ? `<span class="tag">${esc(t('已归档'))}</span>` : '';
    const histRows = (this.hist || []).map((h) => {
      const cur = h.rev === (this.docRev || d.rev) ? ' cur' : '';
      return (
        `<button type="button" class="hist-row${cur}" data-rev="${h.rev}">` +
          `<span class="hist-mark">r${h.rev}</span>` +
          `<span class="dim small">${esc(fmtDateTime(h.ts))} · ${esc(h.by || '')}</span>` +
          `<span class="pack-name">${esc(h.title || t('（无标题）'))}</span>` +
        '</button>');
    }).join('');
    const histBlock = this.hist && this.hist.length
      ? `<details class="fold" open><summary>${esc(t('版本历史 · {n}', { n: this.hist.length }))}</summary>${histRows}</details>`
      : `<div class="dim small" style="margin-top:8px">${esc(t('（暂无版本历史）'))}</div>`;
    const backBtn = ro
      ? `<button type="button" class="btn small" data-doc-head>${esc(t('回到最新版'))}</button>`
      : '';
    if (view) {
      // 预览的对象是草稿（若有）——用户改完想看的是自己改动后的样子
      const pvTitle = this.draft?.title ?? d.title;
      const pvBody = this.draft?.body ?? d.body;
      const editBtn = ro ? '' : `<button type="button" class="btn small" data-doc-edit>${esc(t('编辑'))}</button>`;
      el.innerHTML =
        '<div class="form-head">' +
          `<h2 class="doc-view-title">${esc(pvTitle || t('（无标题）'))}</h2>` +
          `<span class="tag">r${d.rev || 0}${ro ? esc(t(' · 只读快照')) : esc(t(' · 预览'))}</span>${archTag}` +
          `<span class="tabs-spacer"></span>${lifeBtn}${editBtn}${backBtn}` +
        '</div>' +
        `<div class="dim small kb-doc-meta">${meta}${this.draft ? esc(t(' · 有未保存修改')) : ''}</div>` +
        `<div class="kb-md doc-view">${markdown(pvBody || '') || `<span class="dim">${esc(t('（空文档）'))}</span>`}</div>` +
        histBlock;
      return;
    }
    const dis = ro ? ' disabled' : '';
    const draft = this.draft || {};
    el.innerHTML =
      '<div class="form-head">' +
        `<input class="input" data-doc-title value="${esc(draft.title ?? d.title ?? '')}" placeholder="${esc(t('标题'))}"${dis}>` +
        `<span class="tag">r${d.rev || 0}</span>${archTag}` +
        `<button type="button" class="btn small" data-doc-view>${esc(t('预览'))}</button>` +
        `${lifeBtn}` +
        `<button type="button" class="btn small gold" data-doc-save${dis}>${esc(t('保存'))}</button>` +
      '</div>' +
      `<div class="dim small kb-doc-meta">${meta}${this.draft ? esc(t(' · 有未保存修改')) : ''}</div>` +
      `<textarea class="input doc-body" data-doc-body${dis}>${esc(draft.body ?? d.body ?? '')}</textarea>` +
      backBtn + histBlock;
  }

  // ── creation ────────────────────────────────────────────────────

  openNewDoc() {
    this.newOpen = true;
    // 项目办公室的文档天然落在本房架下：键名只填末段，前缀 p/<key>/
    // 由表单自动拼（隔离的写侧对齐读侧——键即落架，不会建出别家的文档）
    const pre = this.newDocPrefix();
    const keyHint = pre
      ? t('一段（落在 {pre} 下）', { pre })
      : t('1–3 级斜杠，如 roles/hr');
    this.pane.innerHTML =
      '<div class="state-card" style="max-width:520px;align-items:stretch;text-align:left">' +
        `<strong>${esc(t('新建手册文档'))} · ${esc(this.scopeLabel())}</strong>` +
        `<label class="field"><span>${esc(t('键名'))}<i class="req">*</i><i>${esc(keyHint)}</i></span>` +
          `<input class="input mono" data-new-key placeholder="${pre ? esc(pre) : 'roles/hr'}"></label>` +
        `<label class="field"><span>${esc(t('标题'))}</span>` +
          `<input class="input" data-new-title placeholder="${esc(t('岗位手册：HR'))}"></label>` +
        `<label class="field"><span>${esc(t('正文'))}</span>` +
          `<textarea class="input" rows="10" data-new-body placeholder="${esc(t('手册正文（成员装配后注入提示词）'))}"></textarea></label>` +
        '<div class="overlay-status" data-new-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-new-cancel>${esc(t('取消'))}</button>` +
          `<button type="button" class="btn gold" data-new-save>${esc(t('创建'))}</button>` +
        '</div>' +
      '</div>';
  }

  saveNewDoc() {
    const typed = (this.pane.querySelector('[data-new-key]')?.value || '').trim();
    const title = (this.pane.querySelector('[data-new-title]')?.value || '').trim();
    const body = this.pane.querySelector('[data-new-body]')?.value || '';
    const status = this.pane.querySelector('[data-new-status]');
    const pre = this.newDocPrefix();
    const key = pre + typed.toLowerCase();
    if (pre ? !/^[a-z0-9][a-z0-9-_]*$/.test(typed.toLowerCase())
            : !/^[a-z0-9][a-z0-9-_]*(\/[a-z0-9][a-z0-9-_]*){0,2}$/i.test(typed)) {
      status.textContent = pre
        ? t('键名不合法：一段小写字母/数字/-/_（自动落在 {pre} 下）', { pre })
        : t('键名不合法：1–3 级小写字母/数字/-/_，以 / 分隔');
      status.classList.add('err');
      return;
    }
    const ok = this.book.ownerSend(
      { type: Msg.KbWrite, doc: { key, title, body }, expect_rev: 0 },
      t('新建文档 {key}', { key }));
    if (!ok) return;
    status.classList.remove('err');
    status.textContent = t('创建中…');
    this.pendingNew = key;
  }

  // ── writes ──────────────────────────────────────────────────────

  saveDoc() {
    if (!this.doc || this.docRev > 0) return;
    const title = this.pane.querySelector('[data-doc-title]')?.value ?? this.doc.title;
    const body = this.pane.querySelector('[data-doc-body]')?.value ?? this.doc.body;
    this.book.ownerSend(
      { type: Msg.KbWrite, doc: { key: this.docKey, title, body }, expect_rev: this.doc.rev || 0 },
      t('保存 {key}', { key: this.docKey }));
    // loading 临时提示：绑定在途事件不自动消失（规范），回执落在 onKbReceipt 收口
    this.saveToast?.close();
    this.saveToast = this.toast.loading(t('保存中：{key}（r{rev} 条件写）', { key: this.docKey, rev: this.doc.rev || 0 }));
  }

  /** app.js forwards Kb receipts here: conflicts carry current_rev. */
  onKbReceipt(f) {
    this.saveToast?.close();
    this.saveToast = null;
    if (f.event === 'denied') {
      const rev = f.current_rev ? t('，办公室已是 r{n}', { n: f.current_rev }) : '';
      this.toast.show(t('写入被拒：{why}{rev} —— 已刷新到最新版', { why: f.text || t('冲突或只读'), rev }), 'err');
      this.loadDoc(this.docKey, 0);
      this.loadDocs();
      return;
    }
    const verbs = { appended: '追加', written: '保存', restored: '恢复',
      archived: '归档', unarchived: '出档', deleted: '删除' };
    this.toast.show(f.text || t(`文档已${verbs[f.event] || f.event}`), 'ok');
    this.draft = null; // 写回生效：编辑器回到新真源（appended 的正文本就不该被旧草稿盖住）
    if (this.pendingNew && f.doc?.key === this.pendingNew) {
      this.docKey = this.pendingNew;
      this.pendingNew = null;
      this.newOpen = false;
    }
    if (f.event === 'deleted' && f.key) {
      if (this.docKey === f.key) { // 选中篇被删：清选位，列表重排后落首篇
        this.docKey = null;
        this.doc = null;
        this.hist = null;
      }
    }
    this.loadDocs();
  }

  // ── events ──────────────────────────────────────────────────────

  onClick(ev) {
    const tg = ev.target;
    if (tg.closest('[data-retry]')) { this.loadTab(); return; }
    if (tg.closest('[data-copy-text]')) { this.copyText(); return; }
    const dir = tg.closest('[data-dir]');
    if (dir) { // 文件树文件夹行：折叠/展开（状态记完整路径，跨刷新保留）
      const p = dir.dataset.dir;
      if (this.folded.has(p)) this.folded.delete(p); else this.folded.add(p);
      this.renderDocList();
      return;
    }
    const doc = tg.closest('[data-doc]');
    if (doc) {
      this.docKey = doc.dataset.doc;
      this.preview = true; // 换文档回 md 渲染面（要改再点「编辑」）
      this.draft = null; // 换文档弃草稿；编辑↔预览↔历史版本之间则保着
      this.renderDocList();
      this.loadDoc(this.docKey, 0);
      return;
    }
    const rev = tg.closest('[data-rev]');
    if (rev) { this.loadDoc(this.docKey, parseInt(rev.dataset.rev, 10) || 0); return; }
    if (tg.closest('[data-doc-head]')) { this.loadDoc(this.docKey, 0); return; }
    if (tg.closest('[data-doc-view]')) { // 进预览前把未保存的编辑留在草稿里
      this.draft = {
        title: this.pane.querySelector('[data-doc-title]')?.value ?? this.doc?.title,
        body: this.pane.querySelector('[data-doc-body]')?.value ?? this.doc?.body,
      };
      this.preview = true;
      this.renderDocMain();
      return;
    }
    if (tg.closest('[data-doc-edit]')) { this.preview = false; this.renderDocMain(); return; }
    if (tg.closest('[data-doc-save]')) { this.saveDoc(); return; }
    if (tg.closest('[data-doc-archive]')) {
      // v3 治理：房主面归档（服务端门：房主/owner/rank≥owner）
      this.book.ownerSend({ type: Msg.KbArchive, key: this.docKey }, t('归档 {key}', { key: this.docKey }));
      return;
    }
    if (tg.closest('[data-doc-unarchive]')) {
      this.book.ownerSend({ type: Msg.KbUnarchive, key: this.docKey }, t('出档 {key}', { key: this.docKey }));
      return;
    }
    if (tg.closest('[data-new-cancel]')) { this.newOpen = false; this.loadDocs(); return; }
    if (tg.closest('[data-new-save]')) { this.saveNewDoc(); return; }
  }
}

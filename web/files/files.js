// files.js — 左侧边栏一级板块「文件」（#/files，类 VSCode 资源管理器）：
// 左树右查看器的双栏布局，浏览的项目跟办公室走（人在哪间房就看哪个
// 工作区——大厅即 Niuma_Studio 本仓，其他项目各归各的），侧栏头的项目
// 选择器可显式改看别家（draft/归档也照看——只读浏览无生命周期门，与
// 服务端 /p/{key}/fs 的 ProjectStore 口径一致）。
//
// 树懒加载（点开一层拉一层，.git 服务端已隐去）；查看器三种货——文本
// （filelight.js 逐行高亮＋行号沟，md 默认走 markdown.js 渲染、可切源
// 码）、图片（/p/{key}/trace/file 直出）、二进制（明说不可预览）。检索
// 盒走 /fs/search（.git/node_modules 不进检索），点结果落查看器并顺带
// 把树展开到该文件。三态契约照 gitview.js（骨架/错误重试/空态）；同项
// 目刷新不打扰树（只重拉当前文件），换项目全重置；跟房纪律同项目管理
// 板（进门跟房＋换房只在可见时跟，大厅不抢位）。

import { getFsTree, getFsFile, fsSearch, getProjects } from '../wire/api.js';
import { LobbyKey } from '../wire/wire.js';
import { esc } from '../ui/dom.js';
import { t, isEn } from '../ui/i18n.js';
import { markdown } from '../chat/markdown.js';
import { highlightLines, langOf } from './filelight.js';

// 扩展名 → 徽标色（文件行的 3 字母小徽章；不在表上的走灰）。
const FILE_TONE = {
  js: '#e8be4a', mjs: '#e8be4a', cjs: '#e8be4a', jsx: '#e8be4a',
  ts: '#3178c6', tsx: '#3178c6',
  go: '#00add8', c: '#555f6c', h: '#555f6c', cpp: '#f34b7d', hpp: '#f34b7d', cc: '#f34b7d',
  py: '#3572a5', rb: '#701516', rs: '#dea584', java: '#b07219', kt: '#a97bff',
  md: '#8a7ff0', json: '#cb9a3c', yml: '#cb9a3c', yaml: '#cb9a3c', toml: '#9c8b7d',
  html: '#e34c26', xml: '#e34c26', svg: '#ffb13b', css: '#563d7c', scss: '#c6538c',
  sh: '#89e051', bash: '#89e051', zsh: '#89e051', sql: '#e38c00', lua: '#000080',
  png: '#a074c4', jpg: '#a074c4', jpeg: '#a074c4', gif: '#a074c4', webp: '#a074c4',
  lock: '#6e7480', mod: '#00add8', sum: '#00add8',
};

// 超过这个行数只渲染开头（2MB 文本能到几十万行，全铺 DOM 会卡死浏
// 览器——VSCode 也虚拟滚动，这里诚实截断＋提示）。行帽之外还有字符帽：
// 压成单行的巨型文件（minified/数据文件）一行就是几 MB，整行进 DOM 同
// 样卡死——每行只显前 LINE_CHAR_CAP 字符，截断行数在帽注里说明。
const RENDER_LINE_CAP = 5000;
const LINE_CHAR_CAP = 4000;

const KIND_LABEL = { text: () => t('文本'), image: () => t('图片'), binary: () => t('二进制') };

/** 字节数人话：zh 走 B/KB/MB/GB，en 走 K/M/B 数量级口径。 */
function fmtBytes(n) {
  if (!Number.isFinite(n)) return '—';
  if (n < 1024) return `${n} B`;
  const units = isEn() ? ['K', 'M', 'G'] : ['KB', 'MB', 'GB'];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** querySelector 属性值转义：真浏览器走 CSS.escape；无头测试世界没有
 *  CSS 全局，退化为引号/反斜杠转义（冒烟路径的路径值不含怪字符）。 */
function cssEsc(s) {
  if (typeof CSS !== 'undefined' && CSS.escape) return CSS.escape(s);
  return String(s).replace(/["\\]/g, '\\$&');
}

export class FilesBoard {
  /**
   * @param {HTMLElement} root #board-files（.board 容器）
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {import('../ui/toast.js').Toast} toast
   */
  constructor(root, book, toast) {
    this.root = root;
    this.book = book;
    this.toast = toast;
    this.projects = [];     // ProjectSummary[] — 选择器的数据源
    this.key = LobbyKey;    // 浏览的项目（跟房落位/选择器改看）
    this.visible = false;   // 板块在屏上（revealed/conceal 维护）
    this.workspace = '';    // 当前项目工作区根（树根响应带来，图片腿要拼绝对路径）
    this.nodes = new Map(); // rel path → {open, children}（'' = 根）
    this.file = null;       // 当前打开的文件信封（getFsFile 的落账）
    this.mdRender = false;  // md 文件正处于渲染视图（默认源码——README 首部 HTML 在聊天渲染纪律下会字面化，源码反而可读）
    this._loadedKey = '';
    this.fsDebounce = 800;  // fs_dirty 的合并窗（ms）——实例字段，测试注 1ms 免真等
    this.els = null;
    root.addEventListener('click', (ev) => this.onClick(ev));
  }

  /** 板块进门：选择器就位→跟房落位→刷新（项目管理板同款纪律）。 */
  async revealed() {
    this.visible = true;
    await this.#loadProjects();
    this.#followRoom();
    const staleTree = this._loadedKey === this.key && !!this.els; // 回场：树还是上次进场的快照
    await this.reload();
    if (staleTree) this.#refreshTree(); // 进门即刷新——展开态原地保留（不收走用户手里的树）
  }

  /** 离场只记账（树与查看器的 DOM 留着，回来接着用）。 */
  conceal() { this.visible = false; }

  /** 换房（book.onChange 'switch' 转来）：可见才跟。 */
  onRoomSwitch() {
    if (this.visible && this.#followRoom()) this.reload();
  }

  /** 跟房：人在哪间房就看哪个工作区——大厅与项目房一视同仁（回大厅
   *  就落大厅，与项目管理板同款优先级）。@returns {boolean} 是否翻面 */
  #followRoom() {
    const cur = this.book.current;
    if (!cur || cur === this.key) return false;
    if (!this.#choices().some((p) => p.key === cur)) return false;
    this.key = cur;
    this.#paintSelector();
    return true;
  }

  #choices() {
    const rank = { active: 0, draft: 1, archived: 2 };
    const rest = this.projects
      .filter((p) => p.key !== LobbyKey)
      .sort((a, b) => (rank[a.status] ?? 3) - (rank[b.status] ?? 3) || a.key.localeCompare(b.key));
    return [
      { key: LobbyKey, name: t('Niuma_Studio') },
      ...rest.map((p) => ({ key: p.key, name: p.name, status: p.status })),
    ];
  }

  selectKey(key) {
    if (!this.#choices().some((p) => p.key === key)) return;
    this.key = key;
    this.#paintSelector();
    this.reload();
  }

  async #loadProjects() {
    try {
      this.projects = await getProjects();
    } catch { this.projects = []; }
    this.#paintSelector();
  }

  #paintSelector() {
    if (!this.els?.proj) return;
    const choices = this.#choices();
    if (!choices.some((p) => p.key === this.key)) this.key = LobbyKey;
    this.els.proj.innerHTML = choices
      .map((p) => `<option value="${esc(p.key)}"${p.key === this.key ? ' selected' : ''}>${esc(p.name)}</option>`) // xss-ok: 三元产物是字面类名，键已 esc
      .join('');
  }

  /** 刷新入口：换项目全重置，同项目只重拉当前文件（树的展开态是用户
   *  手里的事，不该被收走——树的新鲜度由进门刷新/fs_dirty/刷新钮承担）。 */
  async reload() {
    if (this._loadedKey === this.key) {
      if (this.file && this.els) this.#openFile(this.file.path, true);
      return;
    }
    this._loadedKey = this.key;
    this._treeGen = (this._treeGen || 0) + 1; // 在途的 refreshTree 是旧项目的货——作废
    clearTimeout(this._fsTimer);
    this._fsTimer = 0;
    this.workspace = '';
    this.nodes = new Map();
    this.file = null;
    this.root.innerHTML = '<div class="sk-line"></div><div class="sk-line w60"></div>';
    this.els = null;
    await this.#loadRoot();
  }

  // --- 树刷新（进门/fs_dirty/刷新钮三条路共用的同一把伞） ---------------------

  /** 重拉已展开的目录（children 已载的节点＝「展开过」的全部，未展开
   *  的目录没有行也没有状态，不值得拉——懒加载纪律反过来用一遍）。原地
   *  合并：node.children 换新后抹掉行的 built 标，#paintChildren 照旧
   *  重建行＋入册新目录（已入册的展开态原样保留）。gen 护栏防在途刷
   *  新把旧项目的货刷进换防后的新树。 */
  async #refreshTree() {
    if (!this.els) return;
    const gen = (this._treeGen = (this._treeGen || 0) + 1);
    const dirs = [...this.nodes.keys()].filter((rel) => this.nodes.get(rel)?.children);
    const fresh = await Promise.all(dirs.map(async (rel) => {
      try { return [rel, (await getFsTree(this.key, rel)).entries]; } catch { return null; }
    }));
    if (gen !== this._treeGen || !this.els) return; // 途中换项目全重置：过期作废
    for (const row of fresh) {
      if (!row) continue; // 目录没了/读失败——行留在原地，下次全重置收拾
      const [rel, entries] = row;
      const node = this.nodes.get(rel);
      if (!node) continue;
      node.children = entries;
      this.#repaintDir(rel);
    }
    if (this.file) this.#markTreeCurrent(this.file.path); // 行重建过，cur 高亮重新落
  }

  /** 一个目录行的原地重建：出册磁盘上已消失的直接子目录（残册会让下
   *  次 refresh 白拉已不存在的层、让 cur 高亮挂在幽灵行上），再抹 built
   *  标重铺行。 */
  #repaintDir(rel) {
    const node = this.#node(rel);
    if (!node || !node.children || !this.els) return;
    const wrap = rel === '' ? this.els.tree : this.els.tree.querySelector(`[data-wrap="${cssEsc(rel)}"]`);
    if (!wrap) return;
    const prefix = rel === '' ? '' : `${rel}/`;
    const present = new Set(node.children.filter((e) => e.dir).map((e) => prefix + e.name));
    for (const cr of [...this.nodes.keys()]) {
      if (cr !== rel && cr.startsWith(prefix) && !cr.slice(prefix.length).includes('/') && !present.has(cr)) {
        this.nodes.delete(cr);
      }
    }
    delete wrap.dataset.built; // #paintChildren 只建一次行——重建先抹标
    this.#paintChildren(rel);
  }

  /** ws fs_dirty 落点（rooms.js 转来）：某项目的工作区在磁盘上动了。
   *  正浏览该项目且板块在屏才理（看不见的树回场有 revealed 刷新兜底）；
   *  合并进防抖——agent 一轮落十几个文件，一拍一刷足矣。查开中的文件
   *  不自动重读：内容在眼下跳变比短暂陈旧更伤，文件的新鲜归查看器头
   *  的刷新钮（用户读到哪行是手里的事）。 */
  onFsDirty(project) {
    if (project !== this.key || !this.visible || !this.els) return;
    clearTimeout(this._fsTimer);
    this._fsTimer = setTimeout(() => {
      this._fsTimer = 0;
      if (this.els && this.visible) this.#refreshTree();
    }, this.fsDebounce);
  }

  async #loadRoot() {
    let root;
    try {
      root = await getFsTree(this.key, '');
    } catch (err) {
      this.#stateCard(t('文件树读取失败'), err);
      return;
    }
    this.workspace = root.workspace || '';
    this.#paintShell();
    this.nodes.set('', { open: true, children: root.entries });
    this.#paintChildren('');
    this.#paintViewerHint();
  }

  #paintShell() {
    this.root.innerHTML =
      '<div class="fs-wrap">' +
        '<div class="fs-side">' +
          `<div class="fs-side-head">` +
            `<select class="input fs-proj" data-fs-proj title="${t('浏览哪个项目的工作区')}"></select>` +
            `<button type="button" class="btn small fs-refresh" data-fs-refresh title="${t('重拉工作区树（已展开的目录原地刷新）')}">⟳</button></div>` +
          `<input class="input fs-search" type="search" placeholder="${t('搜文件名…')}" data-fs-search>` +
          '<div class="fs-tree" data-fs-tree></div>' +
          '<div class="fs-hits" data-fs-hits hidden></div>' +
        '</div>' +
        '<div class="fs-main">' +
          '<div class="fs-head" data-fs-head hidden></div>' +
          '<div class="fs-body" data-fs-body></div>' +
        '</div>' +
      '</div>';
    this.els = {
      proj: this.root.querySelector('[data-fs-proj]'),
      tree: this.root.querySelector('[data-fs-tree]'),
      hits: this.root.querySelector('[data-fs-hits]'),
      search: this.root.querySelector('[data-fs-search]'),
      head: this.root.querySelector('[data-fs-head]'),
      body: this.root.querySelector('[data-fs-body]'),
    };
    this.#paintSelector();
    this.els.proj.addEventListener('change', () => this.selectKey(this.els.proj.value));
    let timer = 0;
    this.els.search.addEventListener('input', () => {
      clearTimeout(timer);
      timer = setTimeout(() => this.runSearch(this.els.search.value.trim()), 300);
    });
  }

  // --- 树 ---------------------------------------------------------------------

  #node(rel) { return this.nodes.get(rel) || null; }

  #rowHTML(rel, e, depth) {
    const pad = 10 + depth * 14;
    if (e.dir) {
      return `<div class="fs-row fs-dir" data-dir="${esc(rel)}" style="padding-left:${pad}px">` +
        '<span class="fs-tw">▸</span>' + `<span class="fs-fname">${esc(e.name)}</span></div>` +
        `<div class="fs-children" data-wrap="${esc(rel)}" hidden></div>`;
    }
    const ext = (e.name.includes('.') ? e.name.split('.').pop() : '').toLowerCase().slice(0, 3);
    const tone = FILE_TONE[ext] || '#6e7480';
    const size = e.size ? `<span class="fs-fsize">${fmtBytes(e.size)}</span>` : '';
    return `<div class="fs-row fs-file" data-file="${esc(rel)}" style="padding-left:${pad}px">` +
      `<span class="fs-fext" style="color:${tone}">${esc(ext || '···')}</span>` +
      `<span class="fs-fname">${esc(e.name)}</span>${size}</div>`;
  }

  /** 展开过的目录的孩子行铺进自己的 wrap（首次展开时建行，之后只开关
   *  hidden——树行只建一次，折叠不丢）。depth＝目录链层数（根的孩子 0）。 */
  #paintChildren(rel) {
    const node = this.#node(rel);
    if (!node || !node.children) return;
    const wrap = rel === ''
      ? this.els.tree
      : this.els.tree.querySelector(`[data-wrap="${cssEsc(rel)}"]`);
    if (!wrap) return;
    if (!wrap.dataset.built) {
      const depth = rel === '' ? 0 : rel.split('/').length;
      const childRel = (name) => (rel === '' ? name : `${rel}/${name}`);
      wrap.innerHTML = node.children.map((e) => this.#rowHTML(childRel(e.name), e, depth)).join('');
      // 子目录随行入册（缺省态：未加载未展开）——不入册的话 #toggleDir
      // 查无此节点直接吞掉点击（根以外的目录永远点不开）。已入册的
      // （检索落位预载的）不覆盖，保留其加载态。
      for (const e of node.children) {
        if (e.dir) {
          const cr = childRel(e.name);
          if (!this.nodes.has(cr)) this.nodes.set(cr, { open: false, children: null });
        }
      }
      wrap.dataset.built = '1';
    }
    wrap.hidden = !node.open;
    if (rel !== '') {
      const row = this.els.tree.querySelector(`[data-dir="${cssEsc(rel)}"]`);
      if (row) row.querySelector('.fs-tw').textContent = node.open ? '▾' : '▸';
    }
  }

  async #toggleDir(rel) {
    const node = this.#node(rel);
    if (!node) return;
    if (!node.children) {
      this.#markLoading(rel);
      try {
        const rep = await getFsTree(this.key, rel);
        node.children = rep.entries;
      } catch (err) {
        this.toast?.show(err.message || String(err), 'err');
        node.children = [];
      }
    }
    node.open = !node.open;
    this.#paintChildren(rel);
  }

  #markLoading(rel) {
    const wrap = this.els.tree.querySelector(`[data-wrap="${cssEsc(rel)}"]`);
    if (!wrap) return;
    if (!wrap.dataset.built) wrap.innerHTML = `<div class="fs-row fs-loading">${t('载入中…')}</div>`;
    wrap.hidden = false;
  }

  // --- 查看器 -------------------------------------------------------------------

  async #openFile(rel, quiet = false) {
    let env;
    try {
      env = await getFsFile(this.key, rel);
    } catch (err) {
      if (!quiet) this.#stateCard(t('文件读取失败'), err);
      return;
    }
    this.file = env;
    this.mdRender = false;
    this.#markTreeCurrent(rel);
    this.#paintViewer(env);
  }

  #markTreeCurrent(rel) {
    this.els?.tree?.querySelectorAll('.fs-row.cur').forEach((r) => r.classList.remove('cur'));
    this.els?.tree?.querySelector(`[data-file="${cssEsc(rel)}"]`)?.classList.add('cur');
  }

  #paintViewerHint() {
    if (!this.els) return;
    this.els.head.hidden = true;
    this.els.body.innerHTML =
      `<div class="state-card"><strong>${t('选一个文件看内容')}</strong>` +
      `<span>${t('左边的树按项目工作区浏览——Niuma_Studio 的工作区即本仓，其他项目各归各的工作区')}</span></div>`;
  }

  #paintViewer(env) {
    const head = this.els.head;
    head.hidden = false;
    const bits = [];
    if (env.kind === 'text' && env.ext === '.md') {
      bits.push(`<button type="button" class="btn small" data-mdmode>${this.mdRender ? t('看源码') : t('看渲染')}</button>`);
    }
    bits.push(`<button type="button" class="btn small" data-retry>${t('刷新')}</button>`);
    head.innerHTML =
      `<div class="fs-head-main">` +
        `<strong class="fs-fname-big">${esc(env.name)}</strong>` +
        `<span class="fs-hpath" title="${esc(env.path)}">${esc(env.path)}</span>` +
        `<span class="chip">${KIND_LABEL[env.kind]?.() || env.kind}</span>` +
        `<span class="dim small">${fmtBytes(env.size)}</span>` +
        (env.truncated ? `<span class="tag st-pending">${t('超过 2MB，只给开头')}</span>` : '') +
      `</div>` +
      `<div class="fs-head-acts">${bits.join('')}</div>`;
    if (env.kind === 'image') {
      const abs = this.workspace ? `${this.workspace}/${env.path}` : '';
      this.els.body.innerHTML =
        `<div class="fs-imgbox"><img src="/p/${encodeURIComponent(this.key)}/trace/file?path=${encodeURIComponent(abs)}" ` +
        `alt="${esc(env.name)}" loading="lazy"></div>` +
        `<div class="fs-img-fallback">${t('图片加载失败（文件可能在磁盘上已被移动或删除）')}</div>`;
      const img = this.els.body.querySelector('img');
      img?.addEventListener('error', () => img.closest('.fs-imgbox')?.classList.add('broken'));
      return;
    }
    if (env.kind === 'binary') {
      this.els.body.innerHTML =
        `<div class="state-card"><strong>${t('二进制文件不预览')}</strong>` +
        `<span>${esc(env.name)}（${fmtBytes(env.size)}）——${t('文本/图片之外的格式只列不读，内容请在相应工具里打开')}</span></div>`;
      return;
    }
    if (!(env.content || '').trim()) {
      // 空文件明示——一行空白看起来像渲染坏了，其实文件就是空的
      this.els.body.innerHTML =
        `<div class="state-card"><strong>${t('空文件')}</strong>` +
        `<span>${esc(env.name)}（${fmtBytes(env.size)}）——${t('没有可显示的内容（0 字节或全是空白字符）')}</span></div>`;
      return;
    }
    // 文本：md 可切渲染视图（markdown.js——HTML 按聊天纪律字面化，带
    // 内嵌 HTML 的 md 渲染出来满是标签，故默认源码、渲染是显式动作）；
    // 其余一律逐行高亮
    if (env.ext === '.md' && this.mdRender) {
      this.els.body.innerHTML = `<div class="fs-md md-body">${markdown(env.content || '')}</div>`; // xss-ok: markdown() 先 esc 后排版（markdown.js:50）
      return;
    }
    const lang = langOf(env.ext, env.name);
    const srcLines = (env.content || '').split('\n');
    let longLines = 0;
    const clipped = srcLines.map((l) => {
      if (l.length <= LINE_CHAR_CAP) return l;
      longLines++;
      return l.slice(0, LINE_CHAR_CAP) + ' …';
    }).join('\n');
    const lines = highlightLines(clipped, lang);
    const rows = [];
    for (let i = 0; i < Math.min(lines.length, RENDER_LINE_CAP); i++) {
      rows.push(`<div class="fs-cline"><span class="fs-ln">${i + 1}</span><span class="fs-lc">${lines[i] || ' '}</span></div>`);
    }
    const notes = [];
    if (lines.length > RENDER_LINE_CAP) notes.push(t('只渲染前 {n} 行（文件共 {total} 行）', { n: RENDER_LINE_CAP, total: lines.length }));
    if (longLines > 0) notes.push(t('{n} 行超长，每行只显示前 {cap} 字符', { n: longLines, cap: LINE_CHAR_CAP }));
    this.els.body.innerHTML =
      (notes.length ? `<div class="fs-capnote">${notes.join(' · ')}</div>` : '') +
      `<div class="fs-code">${rows.join('')}</div>`;
  }

  #stateCard(title, err) {
    this.root.innerHTML =
      `<div class="state-card error"><strong>${esc(title)}</strong><span>${esc(err.message || String(err))}</span>` +
      `<button type="button" class="btn" data-retry>${t('重试')}</button></div>`;
    this.els = null;
  }

  // --- 检索 -------------------------------------------------------------------

  /** 检索盒输入的防抖落点（公开供测试直呼）。 */
  async runSearch(q) {
    if (!this.els) return;
    if (!q) {
      this.els.hits.hidden = true;
      this.els.hits.innerHTML = '';
      return;
    }
    let rep;
    try {
      rep = await fsSearch(this.key, q);
    } catch { return; } // 检索失败静默——盒子里留着旧结果好过闪红
    const items = rep.hits.map((h) =>
      `<button type="button" class="fs-hit" data-file="${esc(h)}" title="${esc(h)}">${esc(h.split('/').pop())}` +
      `<span class="dim small">${esc(h)}</span></button>`).join('');
    this.els.hits.innerHTML =
      `<div class="fs-hits-head">${t('{n} 个匹配', { n: rep.hits.length })}${rep.truncated ? t('（已达上限，只列前 200）') : ''}</div>` + items;
    this.els.hits.hidden = false;
  }

  /** 把树逐级展开到 rel 的所在目录（检索落位用）：沿途每层拉清单、
   *  先铺父级行再展开本级——wrap 是随父级行一起建的，父级不铺本级无
   *  处挂。 */
  async #revealPath(rel) {
    const dirs = rel.split('/');
    dirs.pop(); // 文件名摘掉，剩目录链
    let cur = '';
    for (const part of dirs) {
      const next = cur === '' ? part : `${cur}/${part}`;
      let node = this.#node(next);
      if (!node?.children) {
        try {
          const rep = await getFsTree(this.key, next);
          this.nodes.set(next, { open: false, children: rep.entries });
        } catch { return; }
      }
      const parent = this.#node(cur);
      if (parent) parent.open = true;
      this.#paintChildren(cur);
      node = this.#node(next);
      node.open = true;
      this.#paintChildren(next);
      cur = next;
    }
  }

  // --- 事件 -------------------------------------------------------------------

  onClick(ev) {
    if (ev.target.closest('[data-fs-refresh]')) {
      this.#refreshTree(); // 手动全量刷新：根层与已展开目录原地重拉
      return;
    }
    const fileRow = ev.target.closest('[data-file]');
    if (fileRow) {
      this.#openFile(fileRow.dataset.file);
      if (fileRow.closest('.fs-hits')) this.#revealPath(fileRow.dataset.file);
      return;
    }
    const dir = ev.target.closest('[data-dir]');
    if (dir) {
      this.#toggleDir(dir.dataset.dir);
      return;
    }
    if (ev.target.closest('[data-mdmode]') && this.file) {
      this.mdRender = !this.mdRender;
      this.#paintViewer(this.file);
      return;
    }
    if (ev.target.closest('[data-retry]')) {
      if (!this.els) this.reload();
      else if (this.file) this.#openFile(this.file.path, true);
      else this.#loadRoot();
    }
  }
}

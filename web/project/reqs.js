// reqs.js — the project board's requirement face (v2 P4-d, PRD §4.2
// US-P2/US-P3): the r_NN ledger (all states), the entry form (POST
// /p/{key}/reqs — the loopback host face) and the「让编排者拆解」
// bridge: one pre-wired "@排期编排组 请拆解 r_NN：<title>" say into the
// project's own room over the existing owner channel (§2.3 step ② —
// zero new protocol). The token is the role-group wake (chat 包
// groupWake 的 @<岗位>组 词令，与 composer 置顶候选/后端同源)：岗位名
// 「编排者」不是成员名也不是身份段，@它谁也唤不醒——v2.2 旗舰上岗后
// 排期编排岗的真人是小牛（大厅）/项目房自聘者，按岗寻人必须走组呼。
// 排序条（创建/完成两档×新旧两向，默认创建最新）：完成档只认
// closed_ts——MarkClosed 盖的完成时刻；未完成的（open/split）与迁移
// 前关闭没盖到戳的旧档恒沉底，两个方向都不冒上来（board.js 无截止
// 沉底的同一规矩）。工具行另坐搜索框（命中 id/标题/正文/录入人，
// Esc 一击清空）与状态筛选（全部/待拆解/已拆解/已关闭）——与任务
// 看板 tbar 的搜索/筛子同族同排，纯过滤面 filterReqs 供测试直饮。
// 误录清理：DELETE /p/{key}/reqs/{id}（本机房主面），仅 open 行给
// 删除钮、两步确认（overview 归档同款）——split 的任务树与 closed
// 的终局留档不随删。
// 点行看全文：列表行正文被两行截断（.req-body 的 line-clamp），行本体
// （标题/正文/元信息/状态签/编号）可点，弹只读详情卡一次看全——正文
// 全文、状态、录入人/录入/完成时刻，评审纪要列出且可点（dh:open-doc
// 桥跳黑板文档面，people/detail 手册链接同一座桥）。评审纪要在行内
// 折叠区里也是同款可点条目——点条目直接跳黑板，不必借详情卡中转。
// 按钮与纪要折叠区各自接手，不触发弹卡。

import { getReqs, postReq, deleteReq , getDocs } from '../wire/api.js';
import { esc, fmtAge, fmtDateTime, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';

const STATUS_TAG = {
  open: { label: t('待拆解'), cls: 'ok' },
  split: { label: t('已拆解'), cls: 'blue' },
  closed: { label: t('已关闭'), cls: 'dim' },
};

// 排序档位表：cmp 全序、零依赖，纯函数面（sortReqs）供测试直饮。
const SORTS = {
  created_desc: { label: t('创建最新'), cmp: (a, b) => (b.created_ts || 0) - (a.created_ts || 0) },
  created_asc: { label: t('创建最早'), cmp: (a, b) => (a.created_ts || 0) - (b.created_ts || 0) },
  closed_desc: { label: t('完成最新'), cmp: (a, b) => sinkNoClosed(a, b) || (b.closed_ts || 0) - (a.closed_ts || 0) },
  closed_asc: { label: t('完成最早'), cmp: (a, b) => sinkNoClosed(a, b) || (a.closed_ts || 0) - (b.closed_ts || 0) },
};

// 无完成时刻者恒沉底；沉底者并排时仍按创建最新——列表形状稳定不跳。
function sinkNoClosed(a, b) {
  const ac = a.closed_ts || 0, bc = b.closed_ts || 0;
  if (ac && bc) return 0;
  if (!ac && !bc) return (b.created_ts || 0) - (a.created_ts || 0);
  return ac ? -1 : 1;
}

/** 排序键白名单化：存档里的未知键（老版本/手改）回落默认档。 */
export function normalizeSort(key) {
  return SORTS[key] ? key : 'created_desc';
}

/** 纯过滤面（测试直饮）：关键词命中 id/标题/正文/录入人（大小写不
 *  敏感，两端空白自剥），叠加状态档；返回新数组，绝不改入参——与
 *  sortReqs 同一纪律。status 为空串即全量。 */
export function filterReqs(reqs, q, status) {
  const needle = String(q || '').trim().toLowerCase();
  return reqs.filter((r) => {
    if (status && r.status !== status) return false;
    if (needle && ![r.id, r.title, r.body, r.created_by]
      .some((v) => (v || '').toLowerCase().includes(needle))) return false;
    return true;
  });
}

/** 纯排序面（测试直饮）：新数组，绝不改入参——this.reqs 缓存保持
 * 插入序，splitSent 名册记账不吃重排的亏。 */
export function sortReqs(reqs, key) {
  return [...reqs].sort((SORTS[key] || SORTS.created_desc).cmp);
}

/** 删除钮纯函数面（测试直饮）：仅 open 行有两态钮——常态「删除」，
 *  武装态「再点一次确认删除」（overview 归档的同款两步写法）；
 *  split/closed 行无钮（库也拒删——UI 不给不能走的路）。 */
export function deleteBtnHTML(req, armedId) {
  if (req.status !== 'open') return '';
  if (armedId === req.id) {
    return `<button type="button" class="btn small danger" data-del="${esc(req.id)}">${t('再点一次确认删除')}</button>`;
  }
  return `<button type="button" class="btn small" data-del="${esc(req.id)}" title="${t('仅 open 需求可删——已拆解的任务树与已关闭的留档不随删')}">${t('删除')}</button>`;
}

// 排序偏好住 dh.ui.prefs 一份（同 composer 草稿的存法）：localStorage
// 不可用时静默降级——会话内仍生效，只是不跨重启。
const PREF_KEY = 'dh.ui.prefs';

function loadSortPref() {
  try { return (JSON.parse(localStorage.getItem(PREF_KEY)) || {}).reqSort || ''; } catch { return ''; }
}

function saveSortPref(key) {
  try {
    const all = JSON.parse(localStorage.getItem(PREF_KEY)) || {};
    all.reqSort = key;
    localStorage.setItem(PREF_KEY, JSON.stringify(all));
  } catch { /* 会话内仍生效，只是不持久 */ }
}

export class ReqsView {
  /**
   * @param {HTMLElement} container #ppane-reqs
   * @param {import('./project.js').ProjectBoard} board
   */
  constructor(container, board) {
    this.container = container;
    this.board = board;
    this.form = null; // the open NewReqForm overlay, if any
    this.detail = null; // the open ReqDetail overlay (点行看全文), if any
    // 「让编排者拆解」派出态：r_NN → 发出时刻。在档期间按钮转
    // 「拆解中…」禁用（防重复派活）；编排回合结束仍待拆解时自动
    // 复位可点。真相源是 member_work 帧维护的名册 working 标志。
    this.splitSent = new Map();
    this.splitWatch = null; // the 3s re-arm ticker, alive only while in flight
    this.reqs = [];         // last painted ledger (cache for local repaints)
    this.paintSig = '';     // skip no-op repaints (the ticker must not jitter)
    this.sortKey = normalizeSort(loadSortPref()); // 需求排序：默认创建最新
    this.q = '';            // 搜索关键词（原始串，trim 在 filterReqs 里）
    this.statusFilter = ''; // 状态筛选档：'' 即全量（与 board 的筛子同规）
    // 删除两步确认的武装行（r_NN 或 null）：第一点武装本行，第二点执
    // 行，点别处即解除；delBusy 挡住执行中（reload 回来前）的重复击发。
    this.delArmed = null;
    this.delBusy = false;
    container.addEventListener('click', (ev) => this.onClick(ev));
    container.addEventListener('change', (ev) => {
      const sel = ev.target.closest('[data-req-sort]');
      if (sel) {
        this.sortKey = normalizeSort(sel.value);
        saveSortPref(this.sortKey);
        this.#paint(this.reqs || []); // sig 含 sortKey，换档必重排
      }
      const stat = ev.target.closest('[data-req-status]');
      if (stat) {
        this.statusFilter = stat.value;
        this.#paint(this.reqs || []); // sig 含 statusFilter，换档必重筛
      }
    });
    container.addEventListener('input', (ev) => {
      const search = ev.target.closest('[data-req-search]');
      if (!search) return;
      this.q = search.value; // 原样存：重绘回填不吞用户刚敲的尾随空格
      this.#paint(this.reqs || []);
    });
    container.addEventListener('keydown', (ev) => {
      // Esc 一击清搜索（board.js 同款；再按 Esc 落到浮层/抽屉的关闭）
      const search = ev.target.closest('[data-req-search]');
      if (!search || ev.key !== 'Escape' || !search.value) return;
      ev.stopPropagation();
      search.value = '';
      this.q = '';
      this.#paint(this.reqs || []);
    });
  }

  /** 点击路由（files.js 的 onClick 同款公开面——dom 冒烟直饮）：各行内
   *  按钮各自接手后早退，行本体空白处的点击弹只读详情卡。解除武装的
   *  那一击只解除不开卡（消音一击，别又弹个窗出来）。 */
  onClick(ev) {
    const del = ev.target.closest('[data-del]');
    if (del) { this.#delClick(del.dataset.del); return; }
    let disarmed = false;
    if (this.delArmed) { // 点了别处：解除武装（两步确认的复位腿）
      this.delArmed = null;
      this.#paint(this.reqs || []);
      disarmed = true;
    }
    const split = ev.target.closest('[data-split]');
    if (split) { this.#askSplit(split.dataset.split, split.dataset.title); return; }
    const gitJump = ev.target.closest('[data-req-git]');
    if (gitJump) { this.board.jumpToGit(gitJump.dataset.reqGit); return; }
    const doc = ev.target.closest('[data-open-doc]');
    if (doc) { // 行内纪要条目：直接跳黑板文档面（详情卡同座 dh:open-doc
      // 桥；须在 .req-minutes 的消音早退之前接手——折叠区里的点击不是
      // 开合余量，就是奔着条目来的）
      window.dispatchEvent(new CustomEvent('dh:open-doc', { detail: doc.dataset.openDoc }));
      return;
    }
    if (ev.target.closest('[data-open-form]')) { this.openForm(); return; }
    if (ev.target.closest('[data-retry]')) { this.reload(); return; }
    // 清筛子：搜索词＋状态档一起还零（board.js「清除筛选」同款）
    if (ev.target.closest('[data-req-clear]')) {
      this.q = '';
      this.statusFilter = '';
      this.#paint(this.reqs || []);
      return;
    }
    // 点行看全文：纪要折叠区自己开合不算，行面其余空白处（标题/正文/
    // 元信息/状态签/编号）都弹只读详情——按钮已在上面各自早退。
    if (disarmed || ev.target.closest('.req-minutes')) return;
    const row = ev.target.closest('[data-req-open]');
    if (row) this.#openDetail(row.dataset.reqOpen);
  }

  /** Idle paint before the first load (board builds all panes up front). */
  renderEmptyIdle() {
    this.container.innerHTML = '<div class="sk-line"></div><div class="sk-line w60"></div>';
  }

  async reload() {
    const key = this.board.key;
    this.paintSig = ''; // skeleton/empty states bypass #paint — stale sigs must not skip the next one
    this.#loadMinutesIndex().then(() => { if (this.reqs) this.#paint(this.reqs); }); // t_209：纪要索引并行拉，就绪后补一次渲染（首次进页折叠区晚一拍到）
    this.container.innerHTML =
      '<div class="sk-line"></div><div class="sk-line w60"></div><div class="sk-line w40"></div>';
    let rep;
    try {
      rep = await getReqs(key);
    } catch (err) {
      this.container.innerHTML = stateCard(
        t('需求台账读取失败'), err.message || String(err), true);
      return;
    }
    const reqs = rep.reqs || [];
    this.reqs = reqs;
    for (const id of [...this.splitSent.keys()]) {
      if (!reqs.some((r) => r.id === id && r.status === 'open')) this.splitSent.delete(id);
    }
    if (!reqs.length) {
      this.container.innerHTML = stateCard(
        t('还没有需求'),
        t('需求是任务树之母——录入第一条，让编排者拆成任务树提案'),
        false, { label: t('录入第一条需求'), cta: 'open-form' });
      return;
    }
    this.#paint(reqs);
  }

  /** One roster-truth repaint: the toolbar (search + status filter +
   * sort) + rows + the split button's 派出态. Filter-then-sort is
   * #paint-local: the cached ledger stays in store order (insertion),
   * every paint re-derives the view. The toolbar rides the same rewrite,
   * so a repaint while typing must hand focus/caret back to the search
   * input（board.js 的壳只画一次没这问题，这里整格重绘就补这一手）. */
  // t_209（r_25）：需求行的评审纪要折叠时间线——docs 索引按 key 前缀
  // ops/meetings/<需求号小写>- 过滤（meeting/minutes.go MinutesKey 规范）。
  // <details> 原生折叠零 JS；条目即按钮（minuteItemsHTML 共用形），点条目
  // 直接 dh:open-doc 跳黑板文档面看全文——不必先点行弹详情卡绕一跳。
  #minutesHTML(reqID) {
    const list = (this.minutesByReq && this.minutesByReq.get(reqID)) || [];
    if (!list.length) return '';
    return `<details class="req-minutes"><summary>${t('评审记录（{n} 场）', { n: list.length })}</summary>` +
      `<div class="chron-list">${minuteItemsHTML(list)}</div></details>`;
  }

  async #loadMinutesIndex() {
    try {
      const docs = await getDocs();
      const by = new Map();
      for (const d of (docs || [])) {
        // v2.9 纪要跟房走：大厅架 ops/meetings/ 与项目架 p/<key>/meetings/
        // 两种键都认（leaf 规范同源 meeting/minutes.go MinutesKey）
        const m = /^(?:ops|p\/[^/]+)\/meetings\/(.+?)-\d+$/.exec(d.key || '');
        if (!m) continue;
        // key 里需求号小写，现行 MinutesKey 保留 r_ 前缀（r_25 → r_25-1），
        // 老档无下划线（r25 → r25-1）——两种都剥前缀重铸，别拼出 r_r_25
        const reqID = 'r_' + m[1].replace(/^r_?/, '');
        if (!by.has(reqID)) by.set(reqID, []);
        by.get(reqID).push(d);
      }
      this.minutesByReq = by;
    } catch { this.minutesByReq = new Map(); } // 索引缺席：行不渲染折叠区
  }

  #paint(reqs) {
    const prevSearch = this.container.querySelector('[data-req-search]');
    const caret = prevSearch && document.activeElement === prevSearch
      ? prevSearch.selectionStart : null;
    const visible = filterReqs(reqs, this.q, this.statusFilter);
    const sorted = sortReqs(visible, this.sortKey);
    const sig = this.sortKey + '|' + this.statusFilter + '|' + this.q + '|' + sorted
      .map((r) => `${r.id}:${r.status}:${this.splitSent.has(r.id) ? 1 : 0}:${this.delArmed === r.id ? 1 : 0}`)
      .join('|');
    if (sig === this.paintSig) return; // the ticker's no-op beats skip the DOM
    this.paintSig = sig;
    this.container.innerHTML = this.#toolbarHTML() + (sorted.length ? sorted.map((r) => {
      const st = STATUS_TAG[r.status] || { label: r.status, cls: 'dim' };
      const splitBtn = r.status !== 'open' ? '' : this.splitSent.has(r.id)
        ? `<button type="button" class="btn small" disabled title="${t('已 @排期编排组 派活，编排回合进行中——结束后若仍待拆解会自动恢复可点')}">${t('拆解中…')}</button>`
        : `<button type="button" class="btn small" data-split="${esc(r.id)}" data-title="${esc(r.title)}">${t('让编排者拆解')}</button>`;
      const meta = [
        r.created_by ? t('录入 {who}', { who: esc(r.created_by) }) : '',
        fmtAge(r.created_ts),
        r.closed_ts ? t('完成 {ago}', { ago: fmtAge(r.closed_ts) }) : '', // 关闭才有完成时刻
      ].filter(Boolean).join(' · ');
      return (
        `<div class="req-row" data-req-open="${esc(r.id)}" title="${t('点击看全文')}">` +
          `<div class="req-id">${esc(r.id)}</div>` +
          `<div class="req-main">` +
            `<div class="req-title">${esc(r.title)}</div>` +
            (r.body ? `<div class="req-body">${esc(r.body)}</div>` : '') +
            `<div class="req-meta">${meta}</div>` +
            this.#minutesHTML(r.id) +
          `</div>` +
          `<div class="req-side">` +
            `<span class="tag ${st.cls}">${st.label}</span>` + splitBtn +
            `<button type="button" class="btn small" data-req-git="${esc(r.id)}" title="${t('看这个需求的分支与提交落地')}">${t('分支动向')}</button>` +
            deleteBtnHTML(r, this.delArmed) +
          `</div>` +
        `</div>`);
    }).join('') : (this.q.trim() || this.statusFilter
      // 筛没剩（board.js #emptyFilteredHTML 同款）与真·空台账分开口：
      // 前者给「清除筛选」一键还原，后者仍走「还没有需求」的录入引导。
      ? stateCard(t('没有匹配的需求'), t('换个关键词，或清除筛选后再看全量台账'),
        false, { label: t('清除筛选'), cta: 'req-clear' })
      : stateCard(t('还没有需求'), t('需求是任务树之母——录入第一条，让编排者拆成任务树提案'),
        false, { label: t('录入第一条需求'), cta: 'open-form' })));
    if (caret !== null) { // 正在敲搜索框时被 ticker 重绘：焦点与光标原位奉还
      const search = this.container.querySelector('[data-req-search]');
      search.focus();
      search.setSelectionRange(caret, caret);
    }
  }

  /** The toolbar row: 搜索框 + 状态筛选 + 排序同坐一排（与任务看板
   * tbar 的搜索/筛子布局同族——.tsearch/.tsel 全局样式直用）。搜索
   * input/change 冒泡到容器统一处理；选项序即 STATUS_TAG 表序。 */
  #toolbarHTML() {
    return (
      '<div class="tbar">' +
        `<input class="input tsearch" data-req-search placeholder="${t('搜索需求：标题 / 编号 / 正文')}" value="${esc(this.q)}">` +
        `<select class="input tsel" data-req-status title="${t('按状态筛选')}">` +
          `<option value="">${t('全部状态')}</option>` +
          Object.entries(STATUS_TAG).map(([k, v]) =>
            `<option value="${k}"${k === this.statusFilter ? ' selected' : ''}>${v.label}</option>`).join('') +
        '</select>' +
        `<span class="tgroup">${t('排序')}</span>` +
        `<select class="input tsel" data-req-sort title="${t('需求排序')}">` +
          Object.entries(SORTS).map(([k, s]) =>
            `<option value="${k}"${k === this.sortKey ? ' selected' : ''}>${s.label}</option>`).join('') +
        '</select>' +
      '</div>');
  }

  /** The re-arm ticker: while a split request is in flight, watch the
   * roster's working tell — the orchestrator's turn ending with the
   * requirement still open means the button may be pressed again
   * (plan rejected, group-call missed, turn failed). The 15s floor
   * keeps the button disabled through the wake→running gap. */
  #armSplitWatch() {
    if (this.splitWatch) return;
    this.splitWatch = setInterval(() => {
      if (!this.splitSent.size) {
        clearInterval(this.splitWatch);
        this.splitWatch = null;
        return;
      }
      const room = this.board.book?.rooms?.get(this.board.key);
      const orchestrating = room
        ? [...room.members.values()].some((m) => m.working && m.role === '排期编排')
        : false;
      if (!orchestrating) {
        const now = Date.now();
        for (const [id, ts] of this.splitSent) {
          if (now - ts > 15000) this.splitSent.delete(id);
        }
      }
      if (!this.splitSent.size) {
        clearInterval(this.splitWatch);
        this.splitWatch = null;
      }
      this.#paint(this.reqs || []);
    }, 3000);
  }

  /**
   * 「让编排者拆解」: the fixed §2.3 step-② wording, fired into the
   * requirement's own room (the room the board is viewing). The say
   * rides the owner write face; the room's stream carries it from there.
   * @排期编排组 is the role-group token（与后端 groupWake/composer 置顶
   * 候选同源）：在岗编排者（role=排期编排）被唤醒；项目房没有编排者时
   * 房间会播「岗位群呼未命中」提示，绝不静默。
   */
  #askSplit(id, title) {
    if (this.splitSent.has(id)) return; // 已派出：等编排回合，不重复派活
    const key = this.board.key;
    const text = `@排期编排组 请拆解 ${id}：${title}`;
    const room = this.board.book.rooms?.get(key);
    if (!room?.writer) {
      this.board.toast.show(t('未识别到房主身份，无法派活（/kb/people 无 local:true）'), 'err');
      return;
    }
    this.board.book.sendTo(key, text);
    this.splitSent.set(id, Date.now());
    this.paintSig = ''; // force the disabled-shape repaint
    this.#paint(this.reqs || []);
    this.#armSplitWatch();
    this.board.toast.show(t('已发进「{room}」聊天：{text}', { room: this.board.roomName(key), text }), 'ok');
  }

  /** 删除两步走（overview 归档同款）：第一点武装（本行换「再点一次
   *  确认删除」红钮），第二点执行；点别处即解除武装。执行后无论成败
   *  都 reload——拒绝常见于台账过期（别处刚拆解/已删），重取对齐真
   *  相；req{deleted} 广播只喂别家的绿点，本页自己走这条腿。 */
  #delClick(id) {
    if (this.delBusy) return;
    if (this.delArmed !== id) {
      this.delArmed = id;
      this.#paint(this.reqs || []);
      return;
    }
    this.delArmed = null;
    this.delBusy = true;
    deleteReq(this.board.key, id)
      .then((rep) => this.board.toast.show(t('需求 {id} 已删除', { id: rep.req?.id || id }), 'ok'))
      .catch((err) => this.board.toast.show(err.message || String(err), 'err'))
      .finally(() => {
        this.delBusy = false;
        this.reload();
      });
  }

  /** 只读详情卡（点行看全文）：列表行正文被两行截断，全文在此一次看
   *  全——编号/状态/标题/正文全文/录入人与时刻/评审纪要（可点跳黑板）。
   *  浮层骑 document.body（NewReqForm 同款）：reload 重写格子带不走它。 */
  #openDetail(id) {
    const req = (this.reqs || []).find((r) => r.id === id);
    if (!req) return; // 行是旧画、台账已换：等下一次 reload，不弹空卡
    this.detail?.close();
    this.detail = new ReqDetail(
      this.board, req, (this.minutesByReq?.get(id)) || [],
      () => { this.detail = null; });
  }

  openForm() {
    this.form?.close();
    // the overlay rides document.body, not the pane: reload() rewrites
    // the pane's innerHTML and would take a pane-hosted overlay with it
    this.form = new NewReqForm(this.board, () => {
      this.form = null;
      this.reload();
    });
  }
}

/**
 * The entry overlay: title + body → POST /p/{key}/reqs. Refusals (the
 * store's own reasons) show in place; success closes and reloads.
 */
class NewReqForm {
  constructor(board, onDone) {
    this.board = board;
    this.onDone = onDone;
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card">' +
        `<div class="overlay-head"><strong>${t('录入需求')}</strong>` +
          `<span class="dim small">${esc(board.roomName(board.key))}${t('（{v}）', { v: esc(board.key) })}</span></div>` +
        `<div class="field"><span>${t('标题')}<i class="req">*</i></span>` +
          `<input class="input" data-f-title maxlength="120" placeholder="${t('一句话说要什么（≤120 字）')}"></div>` +
        `<div class="field"><span>${t('正文')}</span>` +
          `<textarea class="input" data-f-body rows="5" maxlength="2000" placeholder="${t('背景、验收口径、约束——编排者拆解时读得到')}"></textarea></div>` +
        '<div class="overlay-status" data-f-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-f-cancel>${t('取消')}</button>` +
          `<button type="button" class="btn gold" data-f-save>${t('录入')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    this.els = {      title: this.el.querySelector('[data-f-title]'),
      body: this.el.querySelector('[data-f-body]'),
      status: this.el.querySelector('[data-f-status]'),
      save: this.el.querySelector('[data-f-save]'),
    };
    this.el.addEventListener('click', (ev) => {
      if (ev.target === this.el || ev.target.closest('[data-f-cancel]')) this.close();
      if (ev.target.closest('[data-f-save]')) this.#save();
    });
    this.el.addEventListener('keydown', (ev) => {
      if (ev.key === 'Escape') this.close();
    });
    this.els.title.focus();
  }

  async #save() {
    const title = this.els.title.value.trim();
    if (!title) {
      this.#err(t('需求标题不能为空'));
      return;
    }
    this.els.save.disabled = true;
    this.els.status.className = 'overlay-status';
    this.els.status.textContent = t('录入中…');
    try {
      const rep = await postReq(this.board.key, { title, body: this.els.body.value.trim() });
      this.board.toast.show(t('需求 {id} 已录入（open）', { id: rep.req.id }), 'ok');
      this.onDone();
      this.close();
    } catch (err) {
      this.#err(err.message || String(err));
      this.els.save.disabled = false;
    }
  }

  #err(text) {
    this.els.status.className = 'overlay-status err';
    this.els.status.textContent = text;
  }

  close() { dismiss(this.el); }
}

function stateCard(title, note, isError, cta) {
  return (
    `<div class="state-card${isError ? ' error' : ''}">` +
      `<strong>${esc(title)}</strong><span>${esc(note)}</span>` +
      (isError
        ? `<button type="button" class="btn" data-retry>${t('重试')}</button>`
        : cta ? `<button type="button" class="btn gold" data-${cta.cta}>${esc(cta.label)}</button>` : '') +
    '</div>');
}

/** 纪要条目共用形（行内折叠区与详情卡同源；测试直饮）：条目即按钮，
 *  data-open-doc 携文档 key，dh:open-doc 桥跳黑板文档面（people/detail
 *  手册链接同一座桥）——点击直接走，不借详情卡中转。 */
export function minuteItemsHTML(list) {
  return list.map((d) =>
    `<div class="chron-item"><span class="chron-tp chron-tp-event">${t('评审')}</span>` +
    `<button type="button" class="req-min-item" data-open-doc="${esc(d.key)}" title="${t('在黑板里打开这份纪要')}">${esc(d.title || d.key)}</button></div>`).join('');
}

/**
 * The read-only detail overlay (点行看全文): full body, status, logger
 * and timestamps, plus the requirement's review minutes — each minute a
 * button that bridges to the Blackboard doc face via the dh:open-doc
 * event (people/detail's manual link rides the same bridge; app.js
 * closes this page by switching the hash). Backdrop / Esc / 关闭 all
 * close; nothing here writes.
 */
class ReqDetail {
  constructor(board, req, minutes, onClose) {
    this.board = board;
    this.onClose = onClose;
    const st = STATUS_TAG[req.status] || { label: req.status, cls: 'dim' };
    const meta = [
      req.created_by ? t('录入 {who}', { who: esc(req.created_by) }) : '',
      req.created_ts ? t('录入于 {time}', { time: fmtDateTime(req.created_ts) }) : '',
      req.closed_ts ? t('完成于 {time}', { time: fmtDateTime(req.closed_ts) }) : '',
    ].filter(Boolean).join(' · ');
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card w-md req-detail">' +
        `<div class="overlay-head"><strong>${esc(req.id)}</strong>` +
          `<span class="req-detail-head"><span class="tag ${st.cls}">${esc(st.label)}</span>` +
          `<span class="dim small">${esc(board.roomName(board.key))}${t('（{v}）', { v: esc(board.key) })}</span></span></div>` +
        `<div class="req-detail-title">${esc(req.title)}</div>` +
        (req.body
          ? `<div class="req-detail-body">${esc(req.body)}</div>`
          : `<div class="req-detail-body req-detail-nobody">${t('（无正文）')}</div>`) +
        `<div class="req-detail-meta">${meta}</div>` +
        this.#minutesHTML(minutes) +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-f-cancel>${t('关闭')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    this.el.addEventListener('click', (ev) => {
      const doc = ev.target.closest('[data-open-doc]');
      if (doc) { // 纪要跳黑板文档面：先收自己再跳（app.js 转换板块）
        this.close();
        window.dispatchEvent(new CustomEvent('dh:open-doc', { detail: doc.dataset.openDoc }));
        return;
      }
      if (ev.target === this.el || ev.target.closest('[data-f-cancel]')) this.close();
    });
    this.el.addEventListener('keydown', (ev) => {
      if (ev.key === 'Escape') this.close();
    });
    this.el.querySelector('[data-f-cancel]')?.focus(); // 焦点进卡：Esc 随时可关
  }

  /** 评审纪要列表（行内折叠区的展开版）：与行内同一条目形（共用
   *  minuteItemsHTML），条目即按钮，跳黑板看全文。 */
  #minutesHTML(minutes) {
    if (!minutes || !minutes.length) return '';
    return `<div class="req-detail-minutes"><div class="req-detail-sub">${t('评审记录（{n} 场）', { n: minutes.length })}</div>` +
      `<div class="chron-list">${minuteItemsHTML(minutes)}</div></div>`;
  }

  close() { dismiss(this.el); this.onClose?.(); }
}

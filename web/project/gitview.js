// gitview.js — 项目面板第五页签「版本管理」（v2.8）：项目按 workspace
// 探测所属 git 仓库（不同项目各归各仓），摘要卡（当前分支/脏净/合规/
// 钩子）＋全分支树（本地/远端分组折叠）＋GitLens 式树形提交图（泳道
// 拓扑：分叉/汇合弧线、分支徽章贴在 HEAD 提交行、聚焦高亮）＋需求反
// 查视图（reqs 行跳入）。写动作全是房主面：拉取、建分支、切分支（两
// 步确认＋干活成员强警告；脏树由服务端 409 拒绝，文案原样 toast）、
// 绑定浮层、规范策略、commit-msg 钩子装卸。三态契约（骨架/错误重试/
// 空态）照 overview.js；聊天侧只导航、动作落在本面板的纪律不变。

import {
  getGitSummary, getGitBranches, getGitReqActivity, getGitGraph,
  postGitFetch, postGitPush, postGitPull, postGitInit, getGitAuth,
  postGitCheckout, postGitBranch, postGitBind,
  postGitUnbind, postGitPolicy, postGitHook,
  getGitSeats, postGitSeat, postGitSeatCleanup,
  getReqs, getProjectTasks,
} from '../wire/api.js';
import { esc, fmtAge, icon, dismiss } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { Dialog } from '../ui/feedback.js';
import { layoutGraph, edgePath, laneColor, refsBySHA, ROW_H, LANE_W, LANE_X0 } from './gitgraph.js';

const REF_LABEL = {
  task: t('须带任务号 t_NN'),
  req: t('须带需求号 r_NN'),
  either: t('t_NN 或 r_NN 任一'),
  off: t('不强制'),
};

/** 长路径保尾截短（仓库根路径是上下文不是主角，尾巴才是仓库名）。 */
function shortPath(p, max = 48) {
  if (!p || p.length <= max) return p || '';
  const parts = p.split('/').filter(Boolean);
  let tail = parts[parts.length - 1] || p;
  for (let i = parts.length - 2; i >= 0; i--) {
    const next = `${parts[i]}/${tail}`;
    if (next.length > max - 2) break;
    tail = next;
  }
  return `…/${tail}`;
}

export class GitView {
  /**
   * @param {HTMLElement} container #ppane-git
   * @param {import('./project.js').ProjectBoard} board
   */
  constructor(container, board) {
    this.container = container;
    this.board = board;
    this.summary = null;     // GET /p/{key}/git 的最后落账
    this.branches = null;    // GET branches 的最后落账
    this.seats = null;       // GET seats 的最后落账（分支隔离矩阵，v2.7.1）
    this.graph = null;       // GET graph 的最后落账（null=未拉/失效）
    this.graphOpen = false;  // 提交图区块展开态
    this.graphFocus = '';    // 聚焦分支名（''=不聚焦，只展开全图）
    this.collapsed = new Set(); // 分支树折叠中的组键（local / remote:名）
    this.reqFocus = '';      // 需求反查的 r_NN（''=不聚焦）
    this.reqActivity = null; // 聚焦时的反查结果
    this.armBranch = '';     // 切分支两步确认的 armed 名
    this.armTimer = 0;
    this.authChecks = null;  // 远端授权探测的落账（null=未查过）
    this._loadedKey = '';
    this._busy = false;      // 写动作进行中（按钮防抖）
    container.addEventListener('click', (ev) => this.onClick(ev));
  }

  renderIdle() {
    this.container.innerHTML = '<div class="sk-line"></div><div class="sk-line w60"></div>';
  }

  /** reqs 行的跳入口：聚焦某需求后重载。 */
  focusReq(reqKey) {
    this.reqFocus = reqKey;
    this.reload();
  }

  async reload() {
    const key = this.board.key;
    const fresh = this._loadedKey !== key;
    this._loadedKey = key;
    this.authChecks = null; // 世界变了，探测行随之作废
    if (fresh) {
      this.graph = null;
      this.graphOpen = false;
      this.graphFocus = '';
      this.renderIdle();
    }
    let sum;
    try {
      sum = await getGitSummary(key);
    } catch (err) {
      if (fresh) this.#stateCard(t('版本管理读取失败'), err, true);
      return;
    }
    this.summary = sum;
    if (sum.enabled === false) {
      // 两级门顶层关着：整面停摆——不看 repo 空态（那是另一回事）。
      // 开关不住本面板（设置中心 → 项目），这里只如实指路。仓库与既有
      // 提交不受影响。
      this.container.innerHTML =
        `<div class="state-card"><strong>${t('git 版本管理已关闭')}</strong>` +
        `<span>${t('总开关关着：分支隔离、合并流、提交播报、自动建枝全停；仓库与既有提交不受影响。')}</span>` +
        `<span class="dim small">${t('到 设置 → 项目 打开总开关（作用于本项目的房）。')}</span>` +
        `<div class="erow-acts"><button type="button" class="btn" data-retry>${t('刷新')}</button></div></div>`;
      return;
    }
    if (!sum.repo) {
      const p = this.board.currentProject();
      if (sum.git_missing) {
        // 本机没有 git 命令：整页 git 都跑不了，这不是「差一个 init」。
        this.container.innerHTML =
          `<div class="state-card"><strong>${t('没找到 git 命令')}</strong>` +
          `<span>${esc(sum.git_missing_reason || t('本机没有安装 git——版本管理整页依赖 git 命令行'))}</span>` +
          `<button type="button" class="btn" data-retry>${t('装好后重试')}</button></div>`;
      } else {
        this.container.innerHTML =
          `<div class="state-card"><strong>${t('这个工作区还不是 git 仓库')}</strong>` +
          `<span>${t('项目按 workspace 路径探测仓库（{ws}）——点下面初始化，或把 workspace 指向一个已有仓库后回来刷新', { ws: esc(p?.workspace || key) })}</span>` +
          `<div class="erow-acts"><button type="button" class="btn gold" data-gitinit>${t('初始化仓库')}</button>` +
          `<button type="button" class="btn" data-retry>${t('刷新')}</button></div></div>`;
      }
      return;
    }
    let bs;
    try {
      bs = await getGitBranches(key);
    } catch (err) {
      this.#stateCard(t('分支列表读取失败'), err, true);
      return;
    }
    this.branches = bs;
    try {
      this.seats = await getGitSeats(key); // 分支隔离矩阵（失败只缺小节）
    } catch {
      this.seats = null;
    }
    if (this.reqFocus) await this.#loadReqActivity();
    this.#paint();
  }

  #stateCard(title, err, retry) {
    this.container.innerHTML =
      `<div class="state-card error"><strong>${esc(title)}</strong><span>${esc(err.message || String(err))}</span>` +
      (retry ? `<button type="button" class="btn" data-retry>${t('重试')}</button>` : '') + '</div>';
  }

  async #loadReqActivity() {
    try {
      this.reqActivity = await getGitReqActivity(this.board.key, this.reqFocus);
    } catch {
      this.reqActivity = null; // 反查失败只缺小节，不拖垮整页
    }
  }

  // --- 摘要卡 ----------------------------------------------------------------

  #headHTML() {
    const s = this.summary;
    const st = s.status || {};
    const repo = s.repo;
    const cur = st.branch || (st.detached ? t('游离 HEAD') : '—');
    const track = [];
    if (st.ahead) track.push(t('领先 {n}', { n: st.ahead }));
    if (st.behind) track.push(t('落后 {n}', { n: st.behind }));
    const linkBits = [];
    if (s.link?.req) linkBits.push(`<span class="chip" title="${t('绑定需求')}">${t('需求 {id}', { id: esc(s.link.req) })}</span>`);
    if (s.link?.version) linkBits.push(`<span class="chip" title="${t('绑定版本')}">${t('版本 {id}', { id: esc(s.link.version) })}</span>`);
    const dirty = (st.staged || 0) + (st.modified || 0);
    const dirtyHTML = dirty > 0
      ? `<span class="tag st-pending">${t('{n} 处未提交改动（切分支会被拒）', { n: dirty })}</span>`
      : `<span class="tag st-done">${t('工作区干净')}</span>`;
    const comp = s.compliance;
    const compHTML = !comp ? '' : comp.bad > 0
      ? `<span class="tag st-pending" title="${esc(comp.bad_shas?.join(' ') || '')}">${t('近 {a} 条 · {b} 条不合规范', { a: comp.checked, b: comp.bad })}</span>`
      : `<span class="tag st-done">${t('近 {n} 条提交全合规', { n: comp.checked })}</span>`;
    const authBy = new Map((this.authChecks || []).map((c) => [c.remote, c]));
    const remotes = (repo.remotes || []).map((r) => {
      const a = authBy.get(r.name);
      const tag = !a ? '' : a.ok
        ? `<span class="tag st-done" title="${t('git ls-remote 探测通过')}">${t('可访问')}</span>`
        : `<span class="tag st-pending" title="${esc((a.reason || '') + (a.hint || ''))}">${a.auth ? t('需凭据') : t('连不上')}</span>`;
      return `<span class="dim small">${esc(r.name)} → ${esc(r.url)}</span>${tag}`;
    }).join('<span class="ov-psep">·</span>');
    const hookBtn = s.hook_foreign
      ? `<span class="tag st-pending" title="${t('钩子位被未知脚本占用——先人工确认')}">${t('钩子位被占用')}</span>`
      : s.hook_installed
        ? `<button type="button" class="btn small" data-hook="uninstall" title="${t('卸载 commit-msg 钩子（提交不再被拦）')}">${t('卸提交钩子')}</button>`
        : `<button type="button" class="btn small" data-hook="install" title="${t('装 commit-msg 钩子：不合规范的提交当场被拦（--no-verify 可绕过，面板统计仍可见）')}">${t('装提交钩子')}</button>`;
    // 动作条按使用频率分组（同步 / 视图 / 分支 / 治理），组间细分隔线；
    // 独立成行铺满卡宽，不再与信息行抢横向空间。
    const sep = '<span class="git-tb-sep" aria-hidden="true"></span>';
    const groups = [
      [
        '<button type="button" class="btn small" data-fetch>' + icon('refresh', 13) + ` ${t('拉取')}</button>`,
        '<button type="button" class="btn small" data-pull title="' + esc(t('把当前分支快进到上游（--ff-only，分叉即拒绝）')) + '">' + icon('down', 13) + ` ${t('拉取更新')}</button>`,
        '<button type="button" class="btn small" data-push title="' + esc(t('把当前分支推去远端（缺省 origin）')) + '">' + icon('up', 13) + ` ${t('推送')}</button>`,
        (repo.remotes || []).length
          ? `<button type="button" class="btn small" data-auth>${this.authChecks ? t('重查远端') : t('检查远端')}</button>`
          : '',
      ],
      [
        `<button type="button" class="btn small" data-retry>${t('刷新')}</button>`,
        '<button type="button" class="btn small" data-graph>' + icon('branch', 13) + ` ${this.graphOpen ? t('收起提交图') : t('提交图')}</button>`,
      ],
      [
        '<button type="button" class="btn small gold" data-newbranch>' + icon('plus', 13) + ` ${t('建分支')}</button>`,
      ],
      [
        `<button type="button" class="btn small" data-policy>${t('规范')}</button>`,
        hookBtn,
      ],
    ];
    const actsHTML = groups.map((g) => g.filter(Boolean).join('')).filter(Boolean).join(sep);
    return (
      '<div class="git-head">' +
        '<div class="git-head-main">' +
          '<div class="git-title-row">' +
            icon('branch', 15) +
            `<span class="git-branch-now">${t('当前分支')} <strong>${esc(cur)}</strong>` +
              (track.length ? `<span class="dim">（${track.join(t('，'))}）</span>` : '') + '</span>' +
            (repo.worktree ? `<span class="chip" title="${t('同一仓库的另一棵工作树')}">worktree</span>` : '') +
            (repo.subdir ? `<span class="chip" title="${t('项目工作区在仓库子目录')}">${t('子目录')}</span>` : '') +
            `<span class="git-root" title="${esc(repo.root)}">${esc(shortPath(repo.root))}</span>` +
          '</div>' +
          `<div class="git-meta">${dirtyHTML}${compHTML}${linkBits.join('')}</div>` +
          (remotes ? `<div class="git-meta">${remotes}</div>` : '') +
          `<div class="git-meta dim small">${t('提交规范：type(scope): 摘要 · {ref}（可用 type：{types}）', { ref: esc(REF_LABEL[s.policy?.require_ref] || s.policy?.require_ref || t('t_NN 或 r_NN 任一')), types: esc((s.policy?.commit_types || []).join('|')) })}</div>` +
        '</div>' +
        `<div class="git-head-acts">${actsHTML}</div>` +
      '</div>');
  }

  // --- 分支表 ----------------------------------------------------------------

  #branchRowHTML(b) {
    const badge = b.remote
      ? `<span class="git-badge remote" title="${t('远端跟踪分支')}">${t('远端')}</span>`
      : `<span class="git-badge local">${t('本地')}</span>`;
    const cur = b.current ? `<span class="git-now" title="${t('HEAD 所在分支')}">●</span>` : '';
    const last = b.subject
      ? `<span class="git-last" title="${esc(b.subject)}">${esc(b.subject)}</span><span class="dim small">${esc(b.author || '')} ${b.date_ts ? esc(fmtAge(b.date_ts)) : ''}</span>`
      : '<span class="dim small">—</span>';
    const chips = [];
    if (b.link?.req) chips.push(`<span class="chip" title="${t('绑定需求')}">${esc(b.link.req)}</span>`);
    if (b.link?.version) chips.push(`<span class="chip" title="${t('绑定版本')}">${esc(b.link.version)}</span>`);
    if (b.link?.note) chips.push(`<span class="dim small" title="${esc(b.link.note)}">${esc(b.link.note.slice(0, 12))}</span>`);
    const acts = [];
    const graphHere = this.graphOpen && this.graphFocus === b.name;
    acts.push(`<button type="button" class="btn small${graphHere ? ' gold' : ''}" data-gfocus="${esc(b.name)}">${graphHere ? t('收起提交图') : t('提交图')}</button>`);
    acts.push(`<button type="button" class="btn small" data-bind="${esc(b.name)}">${t('绑定')}</button>`);
    if (!b.current) {
      // 远端枝走 --track 落同名本地跟踪枝（服务端 Checkout 的前缀分
      // 流），按钮口径换成「拉到本地」；本地枝照旧「切换到此」。
      const armed = this.armBranch === b.name;
      const label = b.remote ? t('拉到本地') : t('切换到此');
      acts.push(armed
        ? `<button type="button" class="btn small danger" data-switch="${esc(b.name)}">${t('再点一次确认')}${b.remote ? t('拉取') : t('切换')}</button>`
        : `<button type="button" class="btn small" data-switch="${esc(b.name)}">${label}</button>`);
    }
    return (
      `<div class="grow git-row${b.current ? ' current' : ''}">` +
        `<div class="git-bname">${badge}<strong>${esc(b.name)}</strong>${cur}</div>` +
        `<div class="git-blast">${last}</div>` +
        `<div class="git-blink">${chips.join('') || `<span class="dim small">${t('未绑定')}</span>`}</div>` +
        `<div class="erow-acts">${acts.join('')}</div>` +
      '</div>');
  }

  // --- 提交图（GitLens 式树形拓扑）------------------------------------------
  //
  // 左列是整块 SVG（泳道连线 + 节点），右侧行 DOM 用 padding-left 让
  // 位——行高 ROW_H 由 gitgraph.js 钉死，两边靠同一组常量对齐。分支
  // 徽章贴在 HEAD 提交行上（refs 列），当前分支徽章描金。

  #graphHTML() {
    if (!this.graphOpen) return '';
    if (!this.graph) {
      return '<div class="git-graph"><div class="sk-line"></div><div class="sk-line w60"></div></div>';
    }
    const commits = this.graph.commits || [];
    if (!commits.length) {
      return `<div class="git-graph"><span class="dim small">${t('仓库里还没有提交')}</span></div>`;
    }
    const lay = layoutGraph(commits);
    const refs = refsBySHA(this.branches?.branches || []);
    const width = LANE_X0 + (lay.maxLane + 1) * LANE_W + 6;
    const height = commits.length * ROW_H;
    const paths = lay.edges.map((e) => {
      const cut = e.toIdx < 0 ? ' stroke-dasharray="2 3" opacity=".35"' : '';
      return `<path d="${edgePath(e)}" stroke="${laneColor(e.colorLane)}" stroke-width="1.8" fill="none"${cut}/>`;
    }).join('');
    const nodes = commits.map((c, i) => {
      const lane = lay.laneOf.get(c.sha) || 0;
      const x = LANE_X0 + lane * LANE_W;
      const y = i * ROW_H + ROW_H / 2;
      const color = laneColor(lane);
      const isHead = (refs.get(c.sha) || []).some((r) => r.current);
      const ring = isHead ? ' stroke="var(--gold)" stroke-width="1.6"' : '';
      return (c.parents >= 2)
        ? `<circle cx="${x}" cy="${y}" r="4.5" fill="${color}"/><circle cx="${x}" cy="${y}" r="2" fill="var(--board-2)"/>`
        : `<circle cx="${x}" cy="${y}" r="4" fill="${color}"${ring}/>`;
    }).join('');
    const rows = commits.map((c, i) => {
      const v = c.verdict || { ok: true };
      const vd = v.ok
        ? `<span class="git-verdict ok" title="${t('合乎提交规范')}">${icon('check', 12)}</span>`
        : `<span class="git-verdict bad" title="${esc((v.issues || []).join('；'))}">${icon('close', 12)}</span>`;
      const refList = refs.get(c.sha) || [];
      const refHTML = refList.map((rf) =>
        `<span class="gref${rf.current ? ' cur' : ''}${rf.remote ? ' rem' : ''}" title="${rf.current ? t('HEAD 所在分支') : rf.remote ? t('远端跟踪分支') : t('本地分支')}">${esc(rf.name)}</span>`).join('');
      const focused = this.graphFocus && refList.some((rf) => rf.name === this.graphFocus)
        ? ' focused' : '';
      return (
        `<div class="git-grow${focused}" data-gsha="${esc(c.sha)}" data-gi="${i}" style="padding-left:${width}px">` +
          refHTML +
          `<span class="git-sha">${esc((c.sha || '').slice(0, 7))}</span>` +
          `<span class="git-csubj">${esc(c.subject)}</span>` +
          `<span class="dim small">${esc(c.author || '')} ${c.date_ts ? esc(fmtAge(c.date_ts)) : ''}</span>` +
          vd +
        '</div>');
    }).join('');
    const focusBit = this.graphFocus
      ? `<span class="chip" title="${t('聚焦分支：徽章所在行已高亮并滚动到位')}">${esc(this.graphFocus)}</span>` : '';
    return (
      '<div class="git-graph">' +
        '<div class="git-graph-head">' +
          `<span class="dim small">${t('提交图 · 全部分支 · 近 {n} 条（{m} 条泳道）', { n: commits.length, m: lay.maxLane + 1 })}</span>` +
          focusBit +
          `<button type="button" class="btn small" data-graph>${t('收起')}</button>` +
        '</div>' +
        `<div class="git-gbody">` +
          `<svg class="git-gsvg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" aria-hidden="true">${paths}${nodes}</svg>` +
          rows +
        '</div>' +
      '</div>');
  }

  /** 展开/收起提交图；给分支名则聚焦该枝（高亮＋滚动到位）。 */
  async #toggleGraph(focus) {
    if (this.graphOpen && (!focus || this.graphFocus === focus)) {
      this.graphOpen = false;
      this.graphFocus = '';
      this.#paint();
      return;
    }
    this.graphOpen = true;
    this.graphFocus = focus || '';
    this.#paint(); // 先亮骨架
    if (!this.graph) {
      try {
        this.graph = await getGitGraph(this.board.key, 60);
      } catch (err) {
        this.graph = { current: '', commits: [] };
        this.board.toast.show(t('读提交图失败：{msg}', { msg: err.message || err }), 'err');
      }
    }
    if (!this.graphOpen) return; // 骨架期间被收起了
    this.#paint();
    if (this.graphFocus) {
      const row = this.container.querySelector('.git-grow.focused');
      if (row) row.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  }

  #reqFocusHTML() {
    if (!this.reqFocus) return '';
    const act = this.reqActivity;
    const body = act
      ? (act.branches.length
          ? `<div class="git-meta"><span class="dim small">${t('绑定分支：')}</span>${act.branches.map((b) => `<span class="chip">${esc(b)}</span>`).join('')}</div>`
          : `<div class="dim small">${t('还没有分支绑定这个需求（分支行的「绑定」可以挂上）')}</div>`) +
        (act.commits.length
          ? act.commits.map((c) =>
            `<div class="git-commit"><span class="git-sha">${esc((c.sha || '').slice(0, 7))}</span>` +
            `<span class="git-csubj">${esc(c.subject)}</span><span class="dim small">${esc(c.author || '')}</span>` +
            (c.verdict?.ok ? '' : `<span class="git-verdict bad" title="${esc((c.verdict.issues || []).join('；'))}">${icon('close', 12)}</span>`) +
            '</div>').join('')
          : `<div class="dim small">${t('提交里还没人提及这个编号（commit 信息带上它，这里自动长出来）')}</div>`)
      : `<div class="dim small">${t('反查读取失败——稍后刷新重试')}</div>`;
    return (
      `<div class="git-reqfocus">` +
        `<div class="git-reqfocus-head"><strong>${t('需求 {id}', { id: esc(this.reqFocus) })}</strong>` +
        `<span class="dim small">${t('分支与提交的落地动静')}</span>` +
        `<button type="button" class="btn small" data-unfocus>${t('清除聚焦')}</button></div>` +
        body +
      '</div>');
  }

  // 孤儿绑定：注册表里有、仓库里已没有的分支（删枝/改名后的残留）。
  // 不清理不影响运行，但每一行都该看得见、清得掉——隐形的账不是账。
  #orphanHTML() {
    const links = this.board.currentProject()?.branch_links || {};
    const names = new Set((this.branches?.branches || []).map((b) => b.name));
    const orphans = Object.keys(links).filter((b) => !names.has(b));
    if (!orphans.length) return '';
    const rows = orphans.map((b) => {
      const link = links[b];
      const chips = [];
      if (link.req) chips.push(`<span class="chip">${esc(link.req)}</span>`);
      if (link.version) chips.push(`<span class="chip">${esc(link.version)}</span>`);
      if (link.note) chips.push(`<span class="dim small">${esc(link.note)}</span>`);
      return (
        `<div class="grow git-row orphan">` +
          `<div class="git-bname"><span class="git-badge remote">${t('残留')}</span><strong>${esc(b)}</strong></div>` +
          `<div class="git-blast"><span class="dim small">${t('已不在仓库（本地与远端都没有）——分支删了/改名了，绑定还挂在注册表')}</span></div>` +
          `<div class="git-blink">${chips.join('') || '<span class="dim small">—</span>'}</div>` +
          `<div class="erow-acts"><button type="button" class="btn small danger" data-orphanclean="${esc(b)}">${t('清理')}</button></div>` +
        '</div>');
    }).join('');
    return (
      '<div class="git-table git-orphans">' +
        `<div class="grow git-row ehead"><span>${t('残留绑定')}</span><span>${t('说明')}</span><span>${t('需求/版本')}</span><span style="text-align:center">${t('操作')}</span></div>` +
        rows +
      '</div>');
  }

  // 分支树：本地一组、每个远端一组（GitLens Branches 视图的树形分组
  // 形态）。组头行可折叠——折叠态只在内存，刷新后归位（不落盘）。
  #branchTreeHTML(bs) {
    if (!bs.branches.length) return `<div class="state-card"><strong>${t('仓库里还没有分支')}</strong></div>`;
    const local = bs.branches.filter((b) => !b.remote);
    const remoteGroups = new Map();
    for (const b of bs.branches) {
      if (!b.remote) continue;
      if (!remoteGroups.has(b.remote)) remoteGroups.set(b.remote, []);
      remoteGroups.get(b.remote).push(b);
    }
    const groups = [{ key: 'local', title: t('本地分支'), list: local }];
    for (const [name, list] of remoteGroups) {
      groups.push({ key: `remote:${name}`, title: t('{name}（远端）', { name }), list });
    }
    return groups.map((g) => {
      const folded = this.collapsed.has(g.key);
      return (
        `<div class="git-row git-group${folded ? ' folded' : ''}" data-group="${esc(g.key)}" title="${folded ? t('点击展开这组分支') : t('点击折叠这组分支')}">` +
          `<span class="git-group-head"><i class="git-caret${folded ? '' : ' open'}">▸</i>${esc(g.title)}<span class="dim small">· ${g.list.length}</span></span>` +
        '</div>' +
        (folded ? '' : g.list.map((b) => this.#branchRowHTML(b)).join('')));
    }).join('');
  }

  #paint() {
    const bs = this.branches || { branches: [] };
    this.container.innerHTML =
      this.#headHTML() +
      this.#reqFocusHTML() +
      this.#seatsHTML() +
      '<div class="git-table">' +
        `<div class="grow git-row ehead"><span>${t('分支')}</span><span>${t('最近提交')}</span><span>${t('需求/版本')}</span><span style="text-align:center">${t('操作')}</span></div>` +
        this.#branchTreeHTML(bs) +
      '</div>' +
      this.#graphHTML() +
      this.#orphanHTML();
  }

  // --- 成员分支（v2.7.1 分支隔离：每人一棵专属工作树各驻各的分支）--------

  #seatsHTML() {
    const seats = this.seats;
    if (!seats) return '';
    const rows = (seats.seats || []).map((seat) => {
      const badge = seat.branch
        ? `<span class="git-badge local" title="${t('专属工作树分支')}">${esc(seat.branch)}</span>`
        : `<span class="git-badge remote">${t('共享主树')}</span>`;
      let st = `<span class="dim small">${t('树未建（下次出生时落位）')}</span>`;
      if (seat.branch && seat.tree_status) {
        const ts = seat.tree_status;
        st = `<span class="dim small">${esc(ts.branch)} · ${ts.dirty ? `<span class="tag st-pending">${t('{n} 处未提交', { n: ts.dirty })}</span>` : `<span class="tag st-done">${t('干净')}</span>`}${ts.ahead ? ` · ${t('领先 {n}', { n: ts.ahead })}` : ''}${ts.behind ? ` · ${t('落后 {n}', { n: ts.behind })}` : ''}</span>`;
      } else if (seat.branch && !seat.tree_status) {
        st = `<span class="dim small">${t('树未建（下次出生/迁移时落位）')}</span>`;
      }
      const acts = `<button type="button" class="btn small" data-seat-set="${esc(seat.person)}" data-seat-branch="${esc(seat.branch || '')}">${seat.branch ? t('改分支') : t('设分支')}</button>` +
        (seat.branch ? `<button type="button" class="btn small" data-seat-main="${esc(seat.person)}">${t('回主树')}</button>` : '');
      return (
        `<div class="grow git-row seat">` +
          `<div class="git-bname"><strong>${esc(seat.person)}</strong>${badge}</div>` +
          `<div class="git-blast">${st}</div>` +
          `<div class="git-blink"><span class="dim small" title="${esc(seat.worktree || '')}">${seat.worktree ? t('专属工作树') : '—'}</span></div>` +
          `<div class="erow-acts">${acts}</div>` +
        '</div>');
    }).join('');
    // 残留树：人已不在编制（离职/迁出），树还在——回收入口。
    const orphans = (seats.orphan_trees || []).map((seat) =>
      `<div class="grow git-row seat orphan">` +
        `<div class="git-bname"><strong>${esc(seat.person)}</strong><span class="git-badge remote">${t('残留树')}</span></div>` +
        `<div class="git-blast"><span class="dim small">${seat.tree_status ? esc(seat.tree_status.branch) : ''}${seat.tree_status?.dirty ? ` · ${t('{n} 处未提交', { n: seat.tree_status.dirty })}` : ''}</span></div>` +
        `<div class="git-blink"><span class="dim small">${t('人已不在编制')}</span></div>` +
        `<div class="erow-acts"><button type="button" class="btn small danger" data-seat-clean="${esc(seat.person)}">${t('回收工作树')}</button></div>` +
      '</div>').join('');
    if (!rows && !orphans) return '';
    return (
      '<div class="git-table git-seats">' +
        `<div class="grow git-row ehead"><span>${t('成员分支')}</span><span>${t('树状态')}</span><span>${t('位置')}</span><span style="text-align:center">${t('操作')}</span></div>` +
        rows + orphans +
      '</div>');
  }

  // --- 交互 ------------------------------------------------------------------

  onClick(ev) {
    if (ev.target.closest('[data-retry]')) { this.reload(); return; }
    if (ev.target.closest('[data-fetch]')) { this.#doFetch(); return; }
    if (ev.target.closest('[data-push]')) { this.#doPush(); return; }
    if (ev.target.closest('[data-pull]')) { this.#doPull(); return; }
    if (ev.target.closest('[data-auth]')) { this.#doAuthProbe(); return; }
    if (ev.target.closest('[data-gitinit]')) { this.#doInit(); return; }
    if (ev.target.closest('[data-graph]')) { this.#toggleGraph(''); return; }
    const gf = ev.target.closest('[data-gfocus]');
    if (gf) { this.#toggleGraph(gf.dataset.gfocus); return; }
    const grp = ev.target.closest('[data-group]');
    if (grp) {
      const k = grp.dataset.group;
      this.collapsed.has(k) ? this.collapsed.delete(k) : this.collapsed.add(k);
      this.#paint();
      return;
    }
    if (ev.target.closest('[data-newbranch]')) { new BranchForm(this.board, () => this.reload()); return; }
    if (ev.target.closest('[data-policy]')) { new PolicyForm(this.board, this.summary, () => this.reload()); return; }
    const hook = ev.target.closest('[data-hook]');
    if (hook) { this.#doHook(hook.dataset.hook); return; }
    const bind = ev.target.closest('[data-bind]');
    if (bind) { new BindForm(this.board, bind.dataset.bind, () => this.reload()); return; }
    const sw = ev.target.closest('[data-switch]');
    if (sw) { this.#armSwitch(sw.dataset.switch); return; }
    const clean = ev.target.closest('[data-orphanclean]');
    if (clean) { this.#cleanOrphan(clean.dataset.orphanclean); return; }
    const seatSet = ev.target.closest('[data-seat-set]');
    if (seatSet) { new SeatForm(this, seatSet.dataset.seatSet, seatSet.dataset.seatBranch, () => this.reload()); return; }
    const seatMain = ev.target.closest('[data-seat-main]');
    if (seatMain) { this.#backToMain(seatMain.dataset.seatMain); return; }
    const seatClean = ev.target.closest('[data-seat-clean]');
    if (seatClean) { this.#cleanTree(seatClean.dataset.seatClean); return; }
    if (ev.target.closest('[data-unfocus]')) { this.reqFocus = ''; this.#paint(); }
  }

  async #doFetch() {
    if (this._busy) return;
    this._busy = true;
    try {
      await postGitFetch(this.board.key);
      this.board.toast.show(t('已拉取（fetch --all --prune）——远端分支进视野'), 'ok');
      this.graph = null; // 远端引用变了，图缓存作废
      await this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
      this.#probeAuthQuiet(); // 凭据类失败顺手把每根远端的体检行带出来
    } finally {
      this._busy = false;
    }
  }

  /** 推送当前分支到远端（发布是房间可见的事实，失败多见凭据）。 */
  async #doPush() {
    if (this._busy) return;
    this._busy = true;
    try {
      const res = await postGitPush(this.board.key);
      this.board.toast.show(t('已推送 {branch} → {remote}——远端从此可见', { branch: res.branch, remote: res.remote }), 'ok');
      this.graph = null; // 远端跟踪引用变了，图缓存作废
      await this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
      this.#probeAuthQuiet();
    } finally {
      this._busy = false;
    }
  }

  /** 快进当前分支到上游（--ff-only；确有新提交时房间会广播）。 */
  async #doPull() {
    if (this._busy) return;
    this._busy = true;
    try {
      const res = await postGitPull(this.board.key);
      if (res.incoming > 0) {
        this.board.toast.show(t('已快进 {n} 个提交（{branch}）', { n: res.incoming, branch: res.branch }), 'ok');
        this.graph = null; // HEAD 变了，图缓存作废
        await this.reload();
      } else {
        this.board.toast.show(t('已是最新——上游没有新提交'), 'ok');
      }
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    } finally {
      this._busy = false;
    }
  }

  /** 逐远端授权探测（检查/重查远端按钮）。 */
  async #doAuthProbe() {
    try {
      const res = await getGitAuth(this.board.key);
      this.authChecks = res.checks || [];
      if (!this.authChecks.length) {
        this.board.toast.show(t('没有配置任何远端——先 git remote add 再查'), 'err');
        return;
      }
      this.#paint();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    }
  }

  /** 静默探测（fetch/push 失败后顺手带上体检行，失败不声张）。 */
  async #probeAuthQuiet() {
    try {
      const res = await getGitAuth(this.board.key);
      this.authChecks = res.checks || [];
      if (this.authChecks.length) this.#paint();
    } catch { /* 体检是锦上添花 */ }
  }

  /** 房主显式初始化空 workspace（init＋main＋空根提交；已有文件不动）。 */
  async #doInit() {
    const p = this.board.currentProject();
    const go = await Dialog.confirm({
      title: t('初始化 git 仓库'),
      text: t('在 {ws} 里执行 git init（初始分支 main，带一笔空根提交）。已有文件不会被动——真实的首个提交由你们自己落。', { ws: p?.workspace || this.board.key }),
      okText: t('初始化'),
    });
    if (!go) return;
    try {
      await postGitInit(this.board.key);
      this.board.toast.show(t('已初始化——提交一次即可建分支'), 'ok');
      await this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    }
  }

  async #doHook(action) {
    const installing = action === 'install';
    const ok = await Dialog.confirm({
      title: installing ? t('安装提交钩子') : t('卸载提交钩子'),
      text: installing
        ? t('不合规范的 git commit 将被当场拦下（成员仍可用 --no-verify 绕过——面板合规统计兜底可见）。')
        : t('提交不再被拦截，规范回到「面板可见、不强制」。'),
      okText: installing ? t('安装') : t('卸载'), danger: !installing,
    });
    if (!ok) return;
    try {
      await postGitHook(this.board.key, action);
      this.board.toast.show(installing ? t('提交钩子已装') : t('提交钩子已卸'), 'ok');
      this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    }
  }

  async #armSwitch(branch) {
    if (this.armBranch !== branch) {
      // 第一步：armed，3 秒内再点才算数（staffing 删除同款）。
      this.armBranch = branch;
      this.#paint();
      clearTimeout(this.armTimer);
      this.armTimer = setTimeout(() => {
        if (this.armBranch === branch) { this.armBranch = ''; this.#paint(); }
      }, 3000);
      return;
    }
    clearTimeout(this.armTimer);
    this.armBranch = '';
    // 干活成员强警告：doing 任务的负责人名单如实列。
    let busy = [];
    try {
      const tasks = await getProjectTasks(this.board.key);
      busy = [...new Set(tasks.filter((t) => t.status === 'doing' && t.assignee).map((t) => t.assignee))];
    } catch { /* 拿不到名单就不吓人，服务端护栏仍在 */
    }
    if (busy.length) {
      const go = await Dialog.confirm({
        title: t('切到 {branch}', { branch }),
        text: t('{who} 正在这个工作区干活——切分支会动 TA 们脚下的文件。确认继续？', { who: busy.join(t('、')) }),
        okText: t('仍要切换'), danger: true,
      });
      if (!go) { this.#paint(); return; }
    }
    if (this._busy) return;
    this._busy = true;
    try {
      await postGitCheckout(this.board.key, branch, '房主');
      this.board.toast.show(t('已切到 {branch}（房间会广播，在岗成员收到告知）', { branch }), 'ok');
      this.graph = null; // HEAD 变了，图缓存作废
      await this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
      this.#paint();
    } finally {
      this._busy = false;
    }
  }

  /** 清理孤儿绑定（幂等解绑 + 重载）。 */
  async #cleanOrphan(branch) {
    try {
      await postGitUnbind(this.board.key, branch);
      this.board.toast.show(t('已清理残留绑定：{branch}', { branch }), 'ok');
      this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    }
  }

  /** 回共享主树：跨树迁移（会话重生成，历史/任务无损续接）。 */
  async #backToMain(person) {
    const go = await Dialog.confirm({
      title: t('{person} 回共享主工作区', { person }),
      text: t('会话将重生成到主工作区（历史、任务、车道无损续接）；原分支的专属工作树原样保留，需要时可再设回。'),
      okText: t('迁回主树'),
    });
    if (!go) return;
    try {
      await postGitSeat(this.board.key, person, '');
      this.board.toast.show(t('{person} 已回共享主工作区', { person }), 'ok');
      this.reload();
    } catch (err) {
      this.board.toast.show(err.message || String(err), 'err');
    }
  }

  /** 回收残留工作树（人已不在编制；脏树由服务端如实拒绝，force 走确认）。 */
  async #cleanTree(person) {
    const go = await Dialog.confirm({
      title: t('回收 {person} 的工作树', { person }),
      text: t('该成员已不在编制。工作树里有未提交改动时会被拒绝（保护数据）；确认回收将移除整棵树（已提交的工作仍在仓库分支上）。'),
      okText: t('回收'), danger: true,
    });
    if (!go) return;
    try {
      await postGitSeatCleanup(this.board.key, person, false);
      this.board.toast.show(t('已回收 {person} 的工作树', { person }), 'ok');
      this.reload();
    } catch (err) {
      // 脏树拒绝：给一次显式推翻的机会。
      if (/未提交|dirty|modified/i.test(err.message || '')) {
        const force = await Dialog.confirm({
          title: t('工作树有未提交改动'),
          text: t('{msg}——强制回收会丢弃这些改动（已提交的不受影响）。仍要强制回收？', { msg: err.message }),
          okText: t('强制回收'), danger: true,
        });
        if (!force) return;
        try {
          await postGitSeatCleanup(this.board.key, person, true);
          this.board.toast.show(t('已强制回收 {person} 的工作树', { person }), 'ok');
          this.reload();
          return;
        } catch (err2) {
          err = err2;
        }
      }
      this.board.toast.show(err.message || String(err), 'err');
    }
  }
}

// --- 浮层三件套（overlay 惯例：document.body 宿主、Esc 关、data-act 委托）---

/** 分支↔需求/版本绑定浮层：整条替换语义（清空选择＝解绑该侧）。 */
class BindForm {
  constructor(board, branch, onDone) {
    this.board = board;
    this.branch = branch;
    this.onDone = onDone;
    const p = board.currentProject();
    this.versions = p?.versions || [];
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card">' +
        `<div class="overlay-head"><strong>${t('绑定分支')}</strong>` +
          `<span class="dim small">${esc(branch)} · ${esc(board.roomName(board.key))}</span></div>` +
        `<div class="field"><span>${t('需求（r_NN）')}</span>` +
          `<select class="input" data-f-req><option value="">${t('（不绑）')}</option></select></div>` +
        `<div class="field"><span>${t('版本（时间窗）')}</span>` +
          `<select class="input" data-f-version><option value="">${t('（不绑）')}</option></select></div>` +
        `<div class="field"><span>${t('备注')}</span>` +
          `<input class="input" data-f-note maxlength="200" placeholder="${t('一句话说明这枝是干嘛的（可空）')}"></div>` +
        '<div class="overlay-status" data-f-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-act="cancel">${t('取消')}</button>` +
          `<button type="button" class="btn danger" data-act="unbind">${t('解绑')}</button>` +
          `<button type="button" class="btn gold" data-act="save">${t('保存')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    this.els = {
      req: this.el.querySelector('[data-f-req]'),
      version: this.el.querySelector('[data-f-version]'),
      note: this.el.querySelector('[data-f-note]'),
      status: this.el.querySelector('[data-f-status]'),
    };
    // 需求下拉异步补（拿不到就只有手填位——不挡绑定动作）。
    getReqs(board.key)
      .then((rep) => {
        const opts = [`<option value="">${t('（不绑）')}</option>`]
          .concat((rep.reqs || []).map((r) => `<option value="${esc(r.id)}">${esc(r.id)} ${esc(r.title.slice(0, 24))}</option>`));
        this.els.req.innerHTML = opts.join('');
        this.#prefill();
      })
      .catch(() => this.#prefill());
    this.els.version.innerHTML = [`<option value="">${t('（不绑）')}</option>`]
      .concat(this.versions.map((v) => `<option value="${esc(v.name)}">${esc(v.name)}</option>`)).join('');
    this.#prefill();
    this.el.addEventListener('click', (ev) => {
      const act = ev.target.closest('[data-act]')?.dataset.act;
      if (act === 'cancel' || ev.target === this.el) this.close();
      if (act === 'save') this.#save(false);
      if (act === 'unbind') this.#save(true);
    });
    this.el.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') this.close(); });
  }

  #prefill() {
    const link = this.board.git?.branches?.branches?.find((b) => b.name === this.branch)?.link;
    if (!link) return;
    if (link.req && [...this.els.req.options].some((o) => o.value === link.req)) this.els.req.value = link.req;
    if (link.version) this.els.version.value = link.version;
    this.els.note.value = link.note || '';
  }

  async #save(unbind) {
    this.els.status.className = 'overlay-status';
    this.els.status.textContent = t('保存中…');
    try {
      if (unbind) {
        await postGitUnbind(this.board.key, this.branch);
        this.board.toast.show(t('已解绑 {branch}', { branch: this.branch }), 'ok');
      } else {
        await postGitBind(this.board.key, {
          branch: this.branch,
          req: this.els.req.value, version: this.els.version.value,
          note: this.els.note.value.trim(), by: '房主',
        });
        this.board.toast.show(t('绑定已更新：{branch}', { branch: this.branch }), 'ok');
      }
      this.onDone();
      this.close();
    } catch (err) {
      this.els.status.className = 'overlay-status err';
      this.els.status.textContent = err.message || String(err);
    }
  }

  close() { dismiss(this.el); }
}

/** 建分支浮层：名字（按需求/版本给建议）＋起点＋顺手绑定。 */
class BranchForm {
  constructor(board, onDone) {
    this.board = board;
    this.onDone = onDone;
    const p = board.currentProject();
    this.versions = p?.versions || [];
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card">' +
        `<div class="overlay-head"><strong>${t('建分支')}</strong>` +
          `<span class="dim small">${esc(board.roomName(board.key))} · ${t('建好即切换过去')}</span></div>` +
        `<div class="field"><span>${t('分支名')}<i class="req">*</i></span>` +
          `<input class="input" data-f-name maxlength="64" placeholder="${t('如 r12-login 或 v1-hotfix')}"></div>` +
        '<div class="est-field-row">' +
          `<div class="field"><span>${t('顺手绑需求')}</span><select class="input" data-f-req><option value="">${t('（不绑）')}</option></select></div>` +
          `<div class="field"><span>${t('顺手绑版本')}</span><select class="input" data-f-version><option value="">${t('（不绑）')}</option></select></div>` +
        '</div>' +
        `<div class="est-editor-note dim small">${t('起点默认当前 HEAD（脏树也能建，文件不动）；命名建议：需求 r_12 → r12-一句话，版本 v1 → v1-一句话。')}</div>` +
        '<div class="overlay-status" data-f-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-act="cancel">${t('取消')}</button>` +
          `<button type="button" class="btn gold" data-act="save">${t('建分支')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    this.els = {
      name: this.el.querySelector('[data-f-name]'),
      req: this.el.querySelector('[data-f-req]'),
      version: this.el.querySelector('[data-f-version]'),
      status: this.el.querySelector('[data-f-status]'),
    };
    getReqs(board.key)
      .then((rep) => {
        this.els.req.innerHTML = [`<option value="">${t('（不绑）')}</option>`]
          .concat((rep.reqs || []).map((r) => `<option value="${esc(r.id)}">${esc(r.id)} ${esc(r.title.slice(0, 24))}</option>`))
          .join('');
      })
      .catch(() => {});
    this.els.version.innerHTML = [`<option value="">${t('（不绑）')}</option>`]
      .concat(this.versions.map((v) => `<option value="${esc(v.name)}">${esc(v.name)}</option>`)).join('');
    this.els.req.addEventListener('change', () => this.#suggest());
    this.els.version.addEventListener('change', () => this.#suggest());
    this.el.addEventListener('click', (ev) => {
      const act = ev.target.closest('[data-act]')?.dataset.act;
      if (act === 'cancel' || ev.target === this.el) this.close();
      if (act === 'save') this.#save();
    });
    this.el.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') this.close(); });
    this.els.name.focus();
  }

  /** 命名建议：r_12 → r12-…；版本 v1 → v1-…。人改过的名字不覆盖。 */
  #suggest() {
    if (this._touched) return;
    const req = this.els.req.value;   // r_12
    const ver = this.els.version.value;
    let prefix = '';
    if (req) prefix = req.replace('_', '');
    else if (ver) prefix = ver;
    this.els.name.value = prefix ? `${prefix}-` : '';
  }

  async #save() {
    const name = this.els.name.value.trim();
    this._touched = true;
    if (!/^[A-Za-z0-9][A-Za-z0-9._\-/]*$/.test(name)) {
      this.#err(t('分支名须字母数字开头，只用字母/数字/._-/'));
      return;
    }
    this.els.status.className = 'overlay-status';
    this.els.status.textContent = t('创建中…');
    try {
      const out = await postGitBranch(this.board.key, {
        name, req: this.els.req.value, version: this.els.version.value, by: '房主',
      });
      let msg = t('分支 {name} 已建好并切过去', { name });
      if (out.bind_error) msg += t('（绑定没落成：{err}）', { err: out.bind_error });
      this.board.toast.show(msg, out.bind_error ? 'warn' : 'ok');
      this.onDone();
      this.close();
    } catch (err) {
      this.#err(err.message || String(err));
    }
  }

  #err(text) {
    this.els.status.className = 'overlay-status err';
    this.els.status.textContent = text;
  }

  close() { dismiss(this.el); }
}

/** 提交规范策略浮层：type 表 + 引用强制档 + 一键回默认。 */
class PolicyForm {
  constructor(board, summary, onDone) {
    this.board = board;
    this.onDone = onDone;
    const pol = summary?.policy || { commit_types: [], require_ref: 'either' };
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card">' +
        `<div class="overlay-head"><strong>${t('提交规范')}</strong><span class="dim small">${t('面板徽章、CLI lint、commit 钩子同一套判定')}</span></div>` +
        '<div class="git-policy-doc dim small">' +
          `${t('格式：<code>type(scope): 一句话摘要</code>，正文附 <code>任务: t_NN</code> 或 <code>需求: r_NN</code>（标题里直接带号也算）。merge 提交豁免。')}` +
        '</div>' +
        `<div class="field"><span>${t('type 允许表（竖线分隔）')}</span>` +
          `<input class="input" data-f-types maxlength="200" value="${esc((pol.commit_types || []).join('|'))}" placeholder="feat|fix|docs|style|refactor|test|chore"></div>` +
        `<div class="field"><span>${t('引用强制')}</span>` +
          '<select class="input" data-f-ref>' +
            `<option value="either"${pol.require_ref === 'either' ? ' selected' : ''}>${REF_LABEL.either}</option>` +
            `<option value="task"${pol.require_ref === 'task' ? ' selected' : ''}>${REF_LABEL.task}</option>` +
            `<option value="req"${pol.require_ref === 'req' ? ' selected' : ''}>${REF_LABEL.req}</option>` +
            `<option value="off"${pol.require_ref === 'off' ? ' selected' : ''}>${REF_LABEL.off}</option>` +
          '</select></div>' +
        '<div class="overlay-status" data-f-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-act="cancel">${t('取消')}</button>` +
          `<button type="button" class="btn" data-act="reset">${t('回默认')}</button>` +
          `<button type="button" class="btn gold" data-act="save">${t('保存')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    this.els = {
      types: this.el.querySelector('[data-f-types]'),
      ref: this.el.querySelector('[data-f-ref]'),
      status: this.el.querySelector('[data-f-status]'),
    };
    this.el.addEventListener('click', (ev) => {
      const act = ev.target.closest('[data-act]')?.dataset.act;
      if (act === 'cancel' || ev.target === this.el) this.close();
      if (act === 'save') this.#save(false);
      if (act === 'reset') this.#save(true);
    });
    this.el.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') this.close(); });
  }

  async #save(reset) {
    this.els.status.className = 'overlay-status';
    this.els.status.textContent = t('保存中…');
    const body = reset
      ? { reset: true }
      : {
          commit_types: this.els.types.value.split('|').map((s) => s.trim()).filter(Boolean),
          require_ref: this.els.ref.value,
        };
    try {
      await postGitPolicy(this.board.key, body);
      this.board.toast.show(reset ? t('提交规范已回默认档') : t('提交规范已更新'), 'ok');
      this.onDone();
      this.close();
    } catch (err) {
      this.els.status.className = 'overlay-status err';
      this.els.status.textContent = err.message || String(err);
    }
  }

  close() { dismiss(this.el); }
}

/** 设分支浮层（v2.7.1 分支隔离）：选本地枝或新建 wt/<成员名>——同一
 * 个端点两种语义（树内换枝不动会话 / 跨树迁移重生会话），浮层如实
 * 说明会发生哪一种。 */
class SeatForm {
  constructor(view, person, current, onDone) {
    this.view = view;
    this.person = person;
    this.onDone = onDone;
    const locals = (view.branches?.branches || []).filter((b) => !b.remote);
    const suggested = `wt/${person}`;
    const options = [`<option value="">${t('（回共享主工作树）')}</option>`]
      .concat([`<option value="${esc(suggested)}" selected>${t('新建 {name}（推荐——成员专属枝）', { name: esc(suggested) })}</option>`])
      .concat(locals
        .filter((b) => b.name !== suggested)
        .map((b) => `<option value="${esc(b.name)}"${b.name === current ? ' selected' : ''}>${esc(b.name)}</option>`));
    this.el = document.createElement('div');
    this.el.className = 'overlay';
    this.el.innerHTML =
      '<div class="overlay-card">' +
        `<div class="overlay-head"><strong>${t('设分支')}</strong>` +
          `<span class="dim small">${esc(person)} · ${esc(view.board.roomName(view.board.key))}</span></div>` +
        `<div class="field"><span>${t('驻到哪个分支')}</span>` +
          `<select class="input" data-f-branch>${options.join('')}</select></div>` +
        '<div class="est-editor-note dim small">' +
          `${t('每位成员一棵专属工作树（~/.niuma/wt 下），各驻各的分支、互不踩脚，提交互相可见。')}` +
          (current
            ? t('换到别的分支：树内切换，会话不动。')
            : t('从共享主树迁入：会话重生成到树上（历史、任务、车道无损续接）。')) +
          `${t('成员自己在树里随时能跑 git；提交请走 type(scope): 摘要 带 t_NN/r_NN 号。')}` +
        '</div>' +
        '<div class="overlay-status" data-f-status></div>' +
        '<div class="overlay-actions">' +
          `<button type="button" class="btn" data-act="cancel">${t('取消')}</button>` +
          `<button type="button" class="btn gold" data-act="save">${t('设定')}</button>` +
        '</div>' +
      '</div>';
    document.body.appendChild(this.el);
    if (current) {
      const sel = this.el.querySelector('[data-f-branch]');
      if ([...sel.options].some((o) => o.value === current)) sel.value = current;
    }
    this.els = {
      branch: this.el.querySelector('[data-f-branch]'),
      status: this.el.querySelector('[data-f-status]'),
    };
    this.el.addEventListener('click', (ev) => {
      const act = ev.target.closest('[data-act]')?.dataset.act;
      if (act === 'cancel' || ev.target === this.el) this.close();
      if (act === 'save') this.#save();
    });
    this.el.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') this.close(); });
  }

  async #save() {
    const branch = this.els.branch.value.trim();
    if (branch && !/^[^\s-][^\s..]*$/.test(branch)) {
      this.#err(t('分支名不合法'));
      return;
    }
    this.els.status.className = 'overlay-status';
    this.els.status.textContent = t('设定中…（迁移可能要建树/重开会话，稍等）');
    try {
      const out = await postGitSeat(this.view.board.key, this.person, branch);
      const msg = branch
        ? `${t('{person} 已驻分支 {branch}', { person: this.person, branch })}${out.moved ? t('（会话已迁移）') : ''}`
        : t('{person} 已回共享主工作区', { person: this.person });
      this.view.board.toast.show(msg, 'ok');
      this.onDone();
      this.close();
    } catch (err) {
      this.#err(err.message || String(err));
    }
  }

  #err(text) {
    this.els.status.className = 'overlay-status err';
    this.els.status.textContent = text;
  }

  close() { dismiss(this.el); }
}

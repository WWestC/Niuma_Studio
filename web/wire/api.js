// api.js — the HTTP read family (frontend 稿 §2.5): thin fetch
// wrappers over the Go projections. Reads over HTTP is the house rule
// (R2); nothing here mutates room or domain state, and no component
// fetches on its own — wire/ is the protocol's only entry.
//
// The read family doubles as the connection truth's source (the chat
// head lamp + the settings popover's 连接 rows): every 2xx samples the
// response Date header (the room time's skew against the local clock —
// §4.1④ 房间时间) and stamps the HTTP-alive mark (§4.1④ 连接态第四档
// 「仅缓存」: WS 断但 HTTP 活).
//
// i18n 循环引用（api.js ⇄ ui/i18n.js）是安全的：两侧都只在函数体内
// 运行时调对方（postLang/setLang、t()），函数声明在 ESM 实例化期即
// 挂出，加载先后无关。

import { t } from '../ui/i18n.js';

// --- the connection truth: server clock & HTTP liveness ---

let skewMs = 0;     // server time − local clock (ms); 0 = never sampled
let lastHttpOk = 0;  // Date.now() of the last 2xx — the 仅缓存 probe truth

// One response's Date header refines both truths. The header is
// second-truncated, so each sample jitters ±1s — adopt only real drift
// (≥2s) to keep the room clock from hopping between samples.
function sampleServerClock(resp) {
  lastHttpOk = Date.now();
  const t = Date.parse(resp.headers.get('date') || '');
  if (Number.isNaN(t)) return;
  const next = t - Date.now();
  if (Math.abs(next - skewMs) > 2000) skewMs = next;
}

/** The room clock's skew to add to Date.now() (0 until first sample). */
export function serverSkew() { return skewMs; }

/**
 * One deliberate HTTP probe while the WS is down — settles the gray
 * (offline: transport gone) vs orange (仅缓存: reads still alive) call
 * and refreshes the clock skew on the way. A failure zeroes the alive
 * mark: a stale success inside the freshness window must not keep
 * swearing HTTP lives after the transport died.
 * @returns {Promise<boolean>} true = HTTP face answered 2xx
 */
export async function probeHTTP() {
  try {
    const resp = await fetch('/projects', { headers: { Accept: 'application/json' }, cache: 'no-store' });
    if (resp.ok) { sampleServerClock(resp); return true; }
  } catch { /* transport gone — offline, not cache */ }
  lastHttpOk = 0;
  return false;
}

/** Whether the last known HTTP success is still within withMs (the probe's freshness window). */
export function httpAlive(withinMs = 15_000) {
  return Date.now() - lastHttpOk <= withinMs;
}

/**
 * GET /projects — the room registry (lobby included, every state).
 * @returns {Promise<import('./wire.js').ProjectSummary[]>}
 */
export function getProjects() {
  return getJSON('/projects');
}

/**
 * GET / — the studio's hello card (name / version / ws address). The
 * settings popover's 连接 rows read it (about the studio this window
 * is bound to); callers cache it, one fetch per open is plenty.
 * @returns {Promise<{name: string, version: string, ws: string, members: unknown[]}>}
 */
export function getInfo() {
  return getJSON('/');
}

/**
 * GET /fs/ls?path= — 目录浏览（立项「工作区」选择器的数据源）：返回该
 * 目录的子目录名、上级路径与用户主目录。path 空串＝主目录。浏览器拿
 * 不到本机绝对路径（file input 出于安全只给假路径），所以由回环服务
 * 端代读。
 * @param {string} [path] absolute directory to list; '' = the home dir
 * @returns {Promise<{path: string, parent: string, dirs: string[], home: string}>}
 */
export function listDir(path) {
  return getJSON(`/fs/ls?path=${encodeURIComponent(path || '')}`);
}

/**
 * GET /p/{key}/history — the room's append-only jsonl. Two batch
 * shapes: since pages the OLDEST matches past the cursor first (the
 * reconnect backfill, chasing next_since until has_more clears);
 * opts.tail flips the read to the NEWEST opts.limit matches (the cold
 * open's hydration — the conversation's end, not its head).
 * @param {string} key
 * @param {number} [since] seq cursor, 0 = from the top (ignored with tail)
 * @param {{tail?: boolean, limit?: number}} [opts]
 * @returns {Promise<import('./wire.js').HistoryPage>}
 */
export function getHistory(key, since = 0, opts = {}) {
  const params = [];
  if (since > 0) params.push(`since=${encodeURIComponent(String(since))}`);
  if (opts.tail) params.push('tail=1');
  if (opts.limit) params.push(`limit=${encodeURIComponent(String(opts.limit))}`);
  const qs = params.length ? `?${params.join('&')}` : '';
  return getJSON(`/p/${encodeURIComponent(key)}/history${qs}`);
}

/**
 * GET /zcode — the ZCode boot gate's answer (the app is bound to
 * ZCode: the splash door waits on this face until the bridge reports
 * ready AND the flagship establishment is hired). state ∈ booting |
 * warming | recruiting | ready | missing | error | off；recruiting 态
 * 附带 recruit 进度（filled/total/current/blocked）。
 * @returns {Promise<{state: string, detail?: string, recruit?: object}>}
 */
export function getZCode() {
  return getJSON('/zcode');
}

/**
 * GET /kb/people — the people roster (online ∪ saved configs ∪ the
 * local human, merged by name). Cold-start fallback for the member
 * roster and the owner probe (the local:true person).
 * @returns {Promise<import('./wire.js').PersonSummary[]>}
 */
export function getPeople() {
  return getJSON('/kb/people');
}

/**
 * GET /kb/people/{name} — one person's detail (summary + prompt body +
 * manual key + task ledger); 404 when neither a member nor a config
 * carries the exact name.
 * @param {string} name
 * @returns {Promise<import('./wire.js').PersonDetail>}
 */
export function getPerson(name) {
  return getJSON(`/kb/people/${encodeURIComponent(name)}`);
}

/**
 * GET /kb/people/{name}/history — the member's delivery dossier (r_26
 * t_212): every done task grouped by requirement line, per-line hash
 * color (stable across sessions), and the joined anchor. Pure read off
 * the task ledger; 履历非考核 — no metrics beyond group counts.
 * @param {string} name
 * @returns {Promise<{name: string, joined?: number, lines: Array<{req: string, title?: string, color: string, done: Array<{id: string, title: string, ts: number}>}>}>}
 */
export function getPersonHistory(name) {
  return getJSON(`/kb/people/${encodeURIComponent(name)}/history`);
}

/**
 * GET /kb/tasks/{id} — one task with its change log; 404 when unknown.
 * project scopes the bare id to one shelf (v2.10：号段分项目，房间语境
 * 的 chip 带本房 key 才能解析别家也用过的号；缺省＝宿主面全室唯一解析).
 * @param {string} id t_NN
 * @param {string} [project]
 * @returns {Promise<import('./wire.js').Task>}
 */
export function getTask(id, project) {
  const q = project ? `?project=${encodeURIComponent(project)}` : '';
  return getJSON(`/kb/tasks/${encodeURIComponent(id)}${q}`);
}

/**
 * GET /agents — the saved-config summaries. The roster (/kb/people) is
 * active-only by design, so the archive toggle reads this face with
 * ?archived=1 instead (frontend 稿 §4.2's 归档 tab).
 * @param {boolean} [archived]
 * @returns {Promise<import('./wire.js').AgentSummary[]>}
 */
export function getAgents(archived = false) {
  return getJSON(`/agents${archived ? '?archived=1' : ''}`);
}

/**
 * GET /kb/tasks?assignee= — the person's task ledger, newest first.
 * @param {string} assignee
 * @returns {Promise<import('./wire.js').Task[]>}
 */
export function getTasksByAssignee(assignee) {
  return getJSON(`/kb/tasks?assignee=${encodeURIComponent(assignee)}`);
}

/**
 * GET /kb/tasks — the whole ledger, newest first (the room board's
 * doing-set source: one request beats one per member).
 * @returns {Promise<import('./wire.js').Task[]>}
 */
export function getTasks() {
  return getJSON('/kb/tasks');
}

/**
 * GET /kb/establishment — the reconciliation diff. A broken table is
 * NOT an HTTP error here: 200 + parse_error + empty rows (§4.5's yellow
 * bar contract); the caller judges.
 * @returns {Promise<import('./wire.js').EstablishmentReport>}
 */
export function getEstablishment() {
  return getJSON('/kb/establishment');
}

/**
 * GET /dispatch?project= — one project dispatcher's member snapshot
 * (session binding 三态). Absent project = the lobby.
 * @param {string} [project]
 * @returns {Promise<import('./wire.js').DispatchStatus[]>}
 */
export function getDispatch(project = '') {
  const qs = project ? `?project=${encodeURIComponent(project)}` : '';
  return getJSON(`/dispatch${qs}`);
}

/**
 * GET /skills — the global skill library (summaries: the body stays
 * behind getSkill).
 * @returns {Promise<import('./wire.js').Skill[]>}
 */
export function getSkills() {
  return getJSON('/skills');
}

/**
 * GET /skills/{key} — one skill in full, body included.
 * @param {string} key
 * @returns {Promise<import('./wire.js').Skill>}
 */
export function getSkill(key) {
  return getJSON(`/skills/${encodeURIComponent(key)}`);
}

/**
 * GET /skills/authoring — the studio-wide 自制技能 switch.
 * @returns {Promise<{on: boolean}>}
 */
export function getSkillAuthoring() {
  return getJSON('/skills/authoring');
}

/**
 * POST /skills/authoring — flip the 自制技能 switch (persists with the
 * skills library).
 * @param {boolean} on
 * @returns {Promise<{on: boolean}>}
 */
export function postSkillAuthoring(on) {
  return postJSON('/skills/authoring', { on });
}

/**
 * GET /mcps — the MCP server library (header values masked; the
 * cleartext stays behind getMCP's loopback detail read).
 * @returns {Promise<import('./wire.js').MCPServer[]>}
 */
export function getMCPs() {
  return getJSON('/mcps');
}

/**
 * GET /mcps/{key} — one MCP server in full, cleartext header values
 * included (loopback read face).
 * @param {string} key
 * @returns {Promise<import('./wire.js').MCPServer>}
 */
export function getMCP(key) {
  return getJSON(`/mcps/${encodeURIComponent(key)}`);
}

/**
 * GET /p/{key}/staffing/{person}/fabric — the seat's effective fabric
 * (Compose over profile ⊕ staffing override): the preview never
 * re-implements assembly, it reads Go's single source (US-H3).
 * @param {string} key     project key ("default" = the lobby seat)
 * @param {string} person
 * @returns {Promise<import('./wire.js').FabricPreview>}
 */
export function getFabric(key, person) {
  return getJSON(`/p/${encodeURIComponent(key)}/staffing/${encodeURIComponent(person)}/fabric`);
}

/**
 * GET /p/{key}/staffing — the project's staffing table (v2 P4-c:
 * every row, all states, with seat presence and effective packs).
 * Active projects only — a 404 keeps the caller on its fallback.
 * @param {string} key
 * @returns {Promise<import('./wire.js').StaffingReport>}
 */
export function getStaffing(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/staffing`);
}

/**
 * GET /p/{key}/establishment — one project's establishment diff with
 * the auto_recall master switch riding along (server/establishment.go).
 * Active projects only — a 404 means no switch face (lobby/non-active).
 * @param {string} key
 * @returns {Promise<{project: string, auto_recall: boolean, rev: number,
 *   rows: import('./wire.js').EstablishmentRow[], parse_error: string}>}
 */
export function getProjectEstablishment(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/establishment`);
}

/**
 * POST /kb/establishment/row（大厅）| /p/{key}/establishment/row（项目）—
 * 编制表行级维护（v2.4 牛马管理面板的写面）：{op:add|update|delete, key,
 * row, expect_rev}。expect_rev 以 GET 拿到的 rev 为准——表被他人改动时
 * 服务端回 409（错误体带 current_rev），调用方重读重试；系统岗（必须，
 * 编排者/HR/小助手）被 403 拒绝。应答即该 scope 的最新报告（rows+rev），
 * 从它重渲染，不吃自己的回声。
 * @param {string} key room key ("default" = the lobby)
 * @param {{op: 'add'|'update'|'delete', key?: string,
 *   row?: {key: string, name: string, role: string, headcount: number,
 *     auto_fill: boolean, manual?: string}, expect_rev?: number}} body
 * @returns {Promise<import('./wire.js').EstablishmentReport>} the fresh report
 */
export function postEstablishmentRow(key, body) {
  const base = key === 'default'
    ? '/kb/establishment/row'
    : `/p/${encodeURIComponent(key)}/establishment/row`;
  return postJSON(base, body);
}

/**
 * POST /p/{key}/establishment {auto_recall} — flip the staffing refill
 * master switch. The keeper reads it live every round, so the flip
 * takes effect on the very next beat — the response echoes the stored
 * truth (render from it, never from the optimistic local value).
 * @param {string} key
 * @param {boolean} on
 * @returns {Promise<{project: string, auto_recall: boolean}>}
 */
export function setAutoRecall(key, on) {
  return postJSON(`/p/${encodeURIComponent(key)}/establishment`, { auto_recall: on });
}

/**
 * GET /p/{key}/reqs — the project's requirement ledger (v2 P4-d), all
 * states in insertion order.
 * @param {string} key
 * @returns {Promise<import('./wire.js').ReqsReport>}
 */
export function getReqs(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/reqs`);
}

/**
 * POST /p/{key}/reqs — file one requirement in open (v2 P4-d). The
 * loopback host face: created_by records provenance (default = the
 * host); the store validates, a refusal is a 400 whose body is the
 * reason.
 * @param {string} key
 * @param {{title: string, body?: string, created_by?: string}} body
 * @returns {Promise<{project: string, req: import('./wire.js').Requirement}>}
 */
export async function postReq(key, body) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/reqs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * DELETE /p/{key}/reqs/{id} — remove one OPEN requirement (误录清理，
 * the store refuses split/closed and the 400 body carries the reason).
 * Same loopback host face as POST; no body — provenance is the host.
 * @param {string} key @param {string} id
 * @returns {Promise<{project: string, req: import('./wire.js').Requirement}>} the removed snapshot
 */
export async function deleteReq(key, id) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/reqs/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json' },
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * GET /p/{key}/plan — the project's single pending-review slot (v2
 * P4-b): plan is null when nothing awaits.
 * @param {string} key
 * @returns {Promise<import('./wire.js').PlanReport>}
 */
export function getPlan(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/plan`);
}

/**
 * GET /p/{key}/schedule — the gantt projection (v2 P4-c): lanes/bars
 * painted with the member palette, derived states computed, range and
 * today anchor included. The gantt never re-derives anything (A-P1).
 * @param {string} key
 * @returns {Promise<import('./wire.js').ScheduleView>}
 */
export function getSchedule(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/schedule`);
}

/**
 * GET /p/{key}/meeting — the meeting room's occupancy truth (需求评审
 * 会议): meeting is null when the room is free (screen dark); the
 * live record names the chair (编排者) and participants the pixel room
 * board seats. The broadcast "meeting" frame is the live cue; this is
 * the resync read.
 * @param {string} key
 * @returns {Promise<import('./wire.js').MeetingReport>}
 */
export function getMeeting(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/meeting`);
}

/**
 * GET /p/{key}/tasks — the project's task ledger (v2 P4-a), newest
 * update first; the lobby read also sees unattached tasks.
 * @param {string} key
 * @returns {Promise<import('./wire.js').TaskFull[]>}
 */
export function getProjectTasks(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/tasks`);
}

// --- 文件浏览器（类 VSCode 资源管理器）——项目工作区的只读浏览 --------
// path 一律是「相对工作区根的斜杠路径」，空串＝根；服务端把任何输入钉
// 死在根下（fsview.go 的围界主张），前端不需要也不携带绝对路径。

/**
 * GET /p/{key}/fs/tree?path= — 一层目录清单（懒加载树的数据源）：
 * 目录在前（组内不区分大小写），.git 隐去，文件行带 size。
 * @param {string} key
 * @param {string} [path] '' = 工作区根
 * @returns {Promise<{project: string, path: string, parent: string, workspace: string, entries: {name: string, dir: boolean, size?: number}[]}>}
 */
export function getFsTree(key, path = '') {
  return getJSON(`/p/${encodeURIComponent(key)}/fs/tree?path=${encodeURIComponent(path)}`);
}

/**
 * GET /p/{key}/fs/file?path= — 一个文件的预览信封：kind=text（content
 * 随行，超 2MB 截断标 truncated）/ image（字节走 /p/{key}/trace/file）/
 * binary（不带货）。
 * @param {string} key
 * @param {string} path 相对工作区的文件路径
 * @returns {Promise<{project: string, path: string, name: string, ext: string, size: number, kind: 'text'|'image'|'binary', truncated?: boolean, content?: string}>}
 */
export function getFsFile(key, path) {
  return getJSON(`/p/${encodeURIComponent(key)}/fs/file?path=${encodeURIComponent(path)}`);
}

/**
 * GET /p/{key}/fs/search?q= — 工作区内文件名/路径子串检索（不区分大
 * 小写；.git 与 node_modules 不进检索；命中帽 200，truncated=true 说明
 * 只是前缀）。
 * @param {string} key
 * @param {string} q
 * @returns {Promise<{project: string, q: string, hits: string[], truncated: boolean}>}
 */
export function fsSearch(key, q) {
  return getJSON(`/p/${encodeURIComponent(key)}/fs/search?q=${encodeURIComponent(q)}`);
}

// --- 版本管理（v2.7）——项目按 workspace 探测 git 仓库，读脸 + 写脸 ----
// 读脸（summary/branches/log/req-activity）随叫随答；写脸（fetch/
// checkout/branch/bind/policy/hook）的服务端按项目互斥，409 是业务
// 拒绝（脏树切分支等），err.message 即原因。

/**
 * GET /p/{key}/git — 仓库摘要：repo（null=工作区不在 git 仓库，空态
 * 卡）+ 状态 + 生效 commit 策略 + 当前分支的绑定 + 近 30 条合规统计 +
 * 钩子占用。
 * @param {string} key
 */
export function getGitSummary(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/git`);
}

/**
 * GET /p/{key}/git/branches — 本地＋远端全分支，行带需求/版本绑定。
 * @param {string} key
 */
export function getGitBranches(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/git/branches`);
}

/**
 * GET /p/{key}/git/log?branch=&n= — 提交历史，逐条带规范 verdict。
 * @param {string} key @param {string} [branch] 空=HEAD @param {number} [n]
 */
export function getGitLog(key, branch = '', n = 30) {
  const q = new URLSearchParams();
  if (branch) q.set('branch', branch);
  q.set('n', String(n));
  return getJSON(`/p/${encodeURIComponent(key)}/git/log?${q}`);
}

/**
 * GET /p/{key}/git/req-activity?req=r_12 — 需求反查：绑定分支＋全分支
 * 范围内提及该号的提交。
 * @param {string} key @param {string} req
 */
export function getGitReqActivity(key, req) {
  return getJSON(`/p/${encodeURIComponent(key)}/git/req-activity?req=${encodeURIComponent(req)}`);
}

/**
 * GET /p/{key}/git/graph?n= — 提交图（全分支拓扑）：commits 逐条带
 * parent_shas 与 verdict，current 是 HEAD 分支名——树形视图数据源。
 * @param {string} key @param {number} [n]
 */
export function getGitGraph(key, n = 60) {
  return getJSON(`/p/${encodeURIComponent(key)}/git/graph?n=${n}`);
}

/** POST /p/{key}/git/fetch — git fetch --all --prune（工作树不动）。 */
export function postGitFetch(key) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/fetch`, {});
}

/**
 * POST /p/{key}/git/push — 推送当前分支（或指名分支）到远端（缺省
 * origin，唯一远端时取它）。远端拒绝分叉/凭据不认（401）文案透传。
 * @param {string} key @param {string} [remote] @param {string} [branch] 空=当前分支
 */
export function postGitPush(key, remote = '', branch = '') {
  return postJSON(`/p/${encodeURIComponent(key)}/git/push`, { remote, branch });
}

/**
 * POST /p/{key}/git/pull — 把工作区当前分支快进到上游（--ff-only，
 * 分叉即拒绝）。脏树 409；incoming 是实际进来的提交数。
 * @param {string} key
 */
export function postGitPull(key) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/pull`, {});
}

/**
 * GET /p/{key}/git/auth — 逐远端授权探测（git ls-remote 体检面）：
 * checks 行带 ok / auth（需凭据）/ reason（git 原文截尾）。
 * @param {string} key
 */
export function getGitAuth(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/git/auth`);
}

/**
 * POST /p/{key}/git/init — 房主显式把空 workspace 初始化成 git 仓库
 * （init＋main＋一笔空根提交；已在仓库内 409）。
 * @param {string} key
 */
export function postGitInit(key) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/init`, {});
}

/**
 * POST /p/{key}/git/checkout — 切工作区分支。脏树 409（面板不做强切）。
 * @param {string} key @param {string} branch @param {string} [by]
 */
export function postGitCheckout(key, branch, by) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/checkout`, { branch, by });
}

/**
 * POST /p/{key}/git/branch — 建分支即切换；req/version 给了顺手落绑定。
 * @param {string} key @param {{name: string, from?: string, req?: string, version?: string, by?: string}} body
 */
export function postGitBranch(key, body) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/branch`, body);
}

/**
 * POST /p/{key}/git/bind — 分支↔需求/版本绑定（整条替换语义）。
 * @param {string} key @param {{branch: string, req?: string, version?: string, note?: string, by?: string}} body
 */
export function postGitBind(key, body) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/bind`, body);
}

/** POST /p/{key}/git/unbind — 解绑（幂等）。 */
export function postGitUnbind(key, branch) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/unbind`, { branch });
}

/**
 * POST /p/{key}/git/policy — commit 规范策略；reset=true 回默认档。
 * @param {string} key @param {{commit_types?: string[], require_ref?: string, reset?: boolean}} body
 */
export function postGitPolicy(key, body) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/policy`, body);
}

/**
 * POST /p/{key}/git/settings — git 版本管理两级门写面：总开关（disabled）
 * ＋基线分支＋成员分支自动新建。回执带生效值（基线空＝main）。
 * @param {string} key @param {{disabled: boolean, base_branch: string, auto_seat_branch: boolean}} body
 */
export function postGitSettings(key, body) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/settings`, body);
}

/** POST /p/{key}/git/hook — commit-msg 钩子装/卸（action: install|uninstall）。 */
export function postGitHook(key, action) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/hook`, { action });
}

/**
 * GET /p/{key}/git/seats — 分支隔离（v2.7.1）的成员×分支矩阵：在编制
 * 行（branch 槽、树状态）＋不在编制的残留树（回收入口的数据源）。
 * @param {string} key
 */
export function getGitSeats(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/git/seats`);
}

/**
 * POST /p/{key}/git/seat — 设分支/回主树（branch 空=回共享主工作树）。
 * 有调度器时迁移全套经办（跨树重生会话、树内换枝不动会话）。
 * @param {string} key @param {string} person @param {string} branch
 */
export function postGitSeat(key, person, branch) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/seat`, { person, branch });
}

/**
 * POST /p/{key}/git/seat-cleanup — 回收残留工作树（{person, force?}）。
 * @param {string} key @param {string} person @param {boolean} [force]
 */
export function postGitSeatCleanup(key, person, force = false) {
  return postJSON(`/p/${encodeURIComponent(key)}/git/seat-cleanup`, { person, force });
}

/**
 * GET /assistant — the 小助手 panel's state: availability (it rides
 * the member dispatch's zcode bridge — dispatch off means unavailable
 * with the reason), the turn status (idle | running), the streamed
 * partial of the running turn and the transcript.
 * @returns {Promise<{available: boolean, reason?: string, status: string, partial?: string, messages: {role: string, text: string, ts: number}[]}>}
 */
export function getAssistant() {
  return getJSON('/assistant');
}

/**
 * POST /assistant/ask — start one assistant turn. 409 = a turn is
 * still running; 503 = dispatch off (no bridge); the error body is
 * JSON {"error": "..."}.
 * @param {string} text
 * @returns {Promise<ReturnType<typeof getAssistant>>} the fresh state
 */
export async function askAssistant(text) {
  const resp = await fetch('/assistant/ask', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ text }),
  });
  if (!resp.ok) {
    let reason = '';
    try {
      const body = await resp.json();
      if (body && typeof body.error === 'string') reason = body.error;
    } catch { /* not JSON */ }
    throw new Error(reason || `/assistant/ask: HTTP ${resp.status}`);
  }
  return resp.json();
}

/**
 * GET /dispatch/models — 模型选择面（招牛马/改档下拉的数据源），与
 * ZCode 桌面端拾取器对齐：账号套餐面在前（builtin 配置 ∩ 本机计划
 * 钥匙，badge=团队/免费/个人），个人供应商（~/.zcode/v2/
 * provider_config.json：显示名 + 模型列表，按操作者排序）在后，
 * ＋ 空选择的回落默认（调度配置）＋ levels 侧表（每模型的思考强度
 * 档位，与 ZCode 桌面拾取器同读 CLI 内置 modelRules）。
 * @returns {Promise<{default: string, reasoning?: string, levels?: Record<string, string[]>, providers: Array<{id: string, name?: string, badge?: string, models: string[]}>}>}
 */
export function getModels() {
  return getJSON('/dispatch/models');
}

/**
 * POST /dispatch/birth — {name, role, prompt, model?, reasoning?, project?} → the session
 * id as plain text, or a 400 whose body is the refusal reason. model is the
 * birth-time seat pick ("providerId/modelId" or a bare id; empty = the
 * default chain); reasoning is the thinking intensity (the CLI's
 * reasoningLevel, e.g. low/high/max; empty = the model's default tier,
 * and it rides the default-chain model too when the pick is empty —
 * the ZCode picker's shape).
 * @param {{name: string, role?: string, prompt?: string, model?: string, reasoning?: string, project?: string}} body
 * @returns {Promise<string>} the receipt text
 */
export function postBirth(body) {
  return postText('/dispatch/birth', body);
}

/**
 * POST /dispatch/model — 改在座成员的座位模型（{name, model, reasoning?,
 * project?}）。两槽各自可单飞：模型留空＋非空 reasoning＝默认链模型
 * 换思考强度（ZCode 拾取器口径）；两槽皆空＝清档回「调度
 * 配置」默认链。reasoning 是思考强度（CLI 的 reasoningLevel；空＝
 * 模型缺省档）。房内成员立即（或本轮结束后）切换，不在座成员落账
 * 待出生/召回生效。
 * @param {{name: string, model?: string, reasoning?: string, project?: string}} body
 * @returns {Promise<string>}
 */
export function postSetModel(body) {
  return postText('/dispatch/model', body);
}

/**
 * POST /dispatch/steer — interrupt-inject one member's live session.
 * @param {{name: string, text: string, project?: string}} body
 * @returns {Promise<string>}
 */
export function postSteer(body) {
  return postText('/dispatch/steer', body);
}

/**
 * POST /dispatch/recall — rebirth a departed member's session (v2
 * P5-a): the staffing occupancy route, same path the thin CLI uses.
 * @param {{name: string, project?: string}} body
 * @returns {Promise<string>} the receipt text
 */
export function postRecall(body) {
  return postText('/dispatch/recall', body);
}

/**
 * POST /skills/{key} — skill upsert over the host's HTTP face (the
 * loopback trust model makes this writer the local human). The path
 * is the identity: a body key mismatching it never wins.
 * @param {string} key
 * @param {import('./wire.js').Skill} skill
 * @returns {Promise<import('./wire.js').Skill>} the saved skill
 */
export async function postSkill(key, skill) {
  const resp = await fetch(`/skills/${encodeURIComponent(key)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(skill),
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * DELETE /skills/{key} — skill removal over the same loopback face.
 * Assemblies still naming the key degrade to nothing at compose time;
 * the answer carries them so the caller can say so.
 * @param {string} key
 * @returns {Promise<{ok: boolean, key: string, referenced_by: string[]}>}
 */
export async function deleteSkill(key) {
  const resp = await fetch(`/skills/${encodeURIComponent(key)}`, {
    method: 'DELETE', headers: { Accept: 'application/json' },
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * POST /mcps/{key} — MCP server upsert, same loopback discipline.
 * @param {string} key
 * @param {import('./wire.js').MCPServer} mcp
 * @returns {Promise<import('./wire.js').MCPServer>} the saved server
 */
export async function postMCP(key, mcp) {
  const resp = await fetch(`/mcps/${encodeURIComponent(key)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(mcp),
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * DELETE /mcps/{key} — MCP server removal, same loopback face.
 * @param {string} key
 * @returns {Promise<{ok: boolean, key: string, referenced_by: string[]}>}
 */
export async function deleteMCP(key) {
  const resp = await fetch(`/mcps/${encodeURIComponent(key)}`, {
    method: 'DELETE', headers: { Accept: 'application/json' },
  });
  if (!resp.ok) throw new Error(await errReason(resp));
  return resp.json();
}

/**
 * POST /projects — 立项（v2 P7）：永远落成 draft，开张走
 * postProjectAction。store 校验（slug/名称/绝对 workspace/版本表），
 * 拒绝理由是 400 的文本体。
 * @param {{key: string, name: string, desc?: string, workspace: string, by?: string, versions?: Array<{name: string, start_ts?: number, end_ts?: number, goal?: string}>}} body
 * @returns {Promise<Object>} the created project
 */
export async function postProject(body) {
  return postJSON('/projects', body);
}

/**
 * POST /projects/preflight — 立项预检（v2.13）：提交前把「服务端会不会
 * 拒」与「目录上有什么风险」提前亮给用户。error＝随后 POST /projects
 * 必被拒的理由（key 冲突、根目录、与在册项目共用目录…）；warn＝允许
 * 但该知晓的注意事项（目录不存在、主目录、与别的项目嵌套、非 git
 * 仓库…）。只读幂等，可反复调；预检只是预告，正门 POST 仍由 store 把关。
 * @param {{key: string, name: string, workspace: string}} body
 * @returns {Promise<{findings: Array<{level: 'error'|'warn', code: string, text: string}>, blocking: boolean}>}
 */
export async function preflightProject(body) {
  return postJSON('/projects/preflight', body);
}

/**
 * POST /p/{key}/lifecycle — 项目生命周期一步（v2 P7）：activate |
 * archive | reactivate。房间集后果（开房/撤房/离编）在服务端发生。
 * @param {string} key
 * @param {'activate'|'archive'|'reactivate'} action
 * @returns {Promise<Object>} the transitioned project
 */
export async function postProjectAction(key, action) {
  return postJSON(`/p/${encodeURIComponent(key)}/lifecycle`, { action });
}

/**
 * PATCH /p/{key} — 项目内容编辑（v2 P7）：name/desc/versions，零值保
 * 持原值；workspace 的按状态边界由 store 把关（active 不可改）。
 * @param {string} key
 * @param {{name?: string, desc?: string, workspace?: string, versions?: Array<{name: string, start_ts?: number, end_ts?: number, goal?: string}>}} patch
 * @returns {Promise<Object>} the patched project
 */
export async function patchProject(key, patch) {
  return sendJSON('PATCH', `/p/${encodeURIComponent(key)}`, patch);
}

// errReason 从一个 !ok 的 Response 里取出人读的拒绝原因：服务端
// {"error"} 信封优先（writeJSONErr 的形状），纯文本体原样回落（
// /dispatch 家族是明文面），最后才是状态行——调用方 throw 的
// Error.message 就是 toast 直接可亮的句子。
async function errReason(resp) {
  let reason = '';
  try {
    const j = await resp.json();
    if (j && typeof j.error === 'string') reason = j.error;
  } catch { /* plain-text body — use it raw below */ }
  return reason || (await resp.text().catch(() => '')) || `HTTP ${resp.status}`;
}

// The JSON write family (POST/PATCH faces): a 4xx body IS the refusal
// reason — thrown as the Error message so toasts show the why.
async function postJSON(url, body) {
  return sendJSON('POST', url, body);
}

async function sendJSON(method, url, body) {
  const resp = await fetch(url, {
    method,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    let payload = null;
    let reason = '';
    try {
      payload = await resp.json();
      if (payload && typeof payload.error === 'string') reason = payload.error;
    } catch { /* plain-text body — use it raw below */ }
    if (!reason) reason = (await resp.text().catch(() => '')) || `${url}: HTTP ${resp.status}`;
    const err = new Error(reason);
    err.status = resp.status;      // 写面调用方按状态分诊（409 冲突等）
    err.data = payload;            // 结构化回执随行（409 的 current_rev 等）
    throw err;
  }
  return resp.json();
}

// --- the retired blackboard's read family (/kb/*, /prompt — v2 P6) ---

/** GET /prompt — the universal AI-onboarding prompt (plain text). */
export function getPrompt() {
  return getText('/prompt');
}

/** GET /kb/manual — the embedded manual (plain text). */
export function getManual() {
  return getText('/kb/manual');
}

/**
 * GET /p/{key}/notice — one room's current group announcement (飞书式
 * 群公告; {project, notice}); notice is null when the room holds none.
 * @param {string} key room key ("default" = the lobby)
 * @returns {Promise<{project: string, notice: import('./wire.js').Notice|null}>}
 */
export function getRoomNotice(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/notice`);
}

/**
 * GET /p/{key}/acks — one room's 收到回执 snapshot (飞书式收到):
 * {project, since, acks} — since is the tracking floor (older frames
 * predate the ledger and render no chips), one row per acked say line,
 * oldest first. The chips' hydration source: the ledger never enters
 * the chat history, so a cold window fetches it. 404 when the /p face
 * is off (no registry).
 * @param {string} key room key ("default" = the lobby)
 * @returns {Promise<{project: string, since: number, acks: Array<{seq: number, ackers: string[]}>}>}
 */
export function getRoomAcks(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/acks`);
}

/**
 * GET /p/{key}/reacts — one room's 表情回应 snapshot (飞书式表情回
 * 应): {project, since, reacts} — since is the tracking floor (older
 * frames predate the ledger and render no chips), one row per reacted
 * say line, oldest first, each row's reactions sorted by emoji with
 * the reactor names inside. The chips' hydration source: the ledger
 * never enters the chat history, so a cold window fetches it. 404 when
 * the /p face is off (no registry).
 * @param {string} key room key ("default" = the lobby)
 * @returns {Promise<{project: string, since: number, reacts: Array<{seq: number, reactions: Array<{emoji: string, names: string[]}>}>}>}
 */
export function getRoomReacts(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/reacts`);
}

/**
 * GET /p/{key}/reads — one room's 已读回执 snapshot (飞书式已读三态):
 * {project, since, reads, queues} — since is the tracking floor (older
 * frames predate the ledger and render no chips), one row per read say
 * line, oldest first; a reader is a member whose turn CONSUMED the line
 * (the dispatcher flushes the read cargo at the turn's terminal —
 * accepted ≠ read). queues rows carry the delivery-queue projection
 * (排队中: members whose next-turn lane still holds the line — not yet
 * injected, not lost). The chips' hydration source: both ledgers never
 * enter the chat history, so a cold window fetches them here. 404 when
 * the /p face is off (no registry).
 * @param {string} key room key ("default" = the lobby)
 * @returns {Promise<{project: string, since: number, reads: Array<{seq: number, readers: string[]}>, queues: Array<{seq: number, who: string[]}>}>}
 */
export function getRoomReads(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/reads`);
}

/**
 * GET /p/{key}/questions — one room's OPEN interactive asks (向房主
 * 提问): {project, questions} — the clickable option cards' hydration
 * source. Question frames never enter the chat history, so a cold
 * window fetches the open set here; closed cards (answered|expired)
 * never come back — the ask's durable transcript copy is the member's
 * own say line. 404 when the /p face is off (no registry).
 * @param {string} key room key ("default" = the lobby)
 * @returns {Promise<{project: string, questions: import('./wire.js').Question[]}>}
 */
export function getRoomQuestions(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/questions`);
}

/**
 * GET /p/{key}/trace[?name=] — one room's 工作过程 snapshot (v2.8):
 * per-member bounded trails of displayable session moments (think/
 * draft/tool/model/turn/error), oldest first, plus the roster's
 * working stamp. Trace frames never enter the chat history, so a cold
 * drawer fetches the ring here and then lives off the live frames.
 * 404 when the /p face is off (no registry); read failure degrades to
 * an empty panel (live frames still flow).
 * @param {string} key room key ("default" = the lobby)
 * @param {string} [name] narrow to one member (the open panel refresh)
 * @returns {Promise<import('./wire.js').TraceReport>}
 */
export function getRoomTrace(key, name) {
  const q = name ? `?name=${encodeURIComponent(name)}` : '';
  return getJSON(`/p/${encodeURIComponent(key)}/trace${q}`);
}

/**
 * 工作过程图片的窄端点 URL（GET /p/{key}/trace/file?path=）：工具入参
 * 里引用的工作区本地图片由此直出 <img>。服务端只放行该房工作区内、
 * 图片扩展名、8MB 帽内的文件——这里只拼 URL，不预取。
 * @param {string} key room key
 * @param {string} path absolute local file path
 * @returns {string}
 */
export function traceFileURL(key, path) {
  return `/p/${encodeURIComponent(key)}/trace/file?path=${encodeURIComponent(path)}`;
}

/**
 * GET /p/{key}/term[?name=&tail=] — one room's 终端转录 snapshot (r_17):
 * per-member journals of the COMPLETE session I/O (input 注入行 / think /
 * draft / tool / model / turn / error / ask / answer / sys), oldest first,
 * folded to final state, plus the roster's working stamp. The tail rides
 * the query (default 500, server ceiling 2000); term frames never enter
 * the chat history, so a cold board fetches here then lives off frames.
 * 404 when the /p face is off (no registry); read failure degrades to an
 * empty stream (live frames still flow).
 * @param {string} key room key ("default" = the lobby)
 * @param {string} [name] narrow to one member
 * @param {number} [tail] per-member row count asked of the server
 * @returns {Promise<import('./wire.js').TraceReport>}
 */
export function getRoomTerm(key, name, tail) {
  const parts = [];
  if (name) parts.push(`name=${encodeURIComponent(name)}`);
  if (tail) parts.push(`tail=${encodeURIComponent(tail)}`);
  const q = parts.length ? `?${parts.join('&')}` : '';
  return getJSON(`/p/${encodeURIComponent(key)}/term${q}`);
}

/**
 * 终端转录整册下载 URL（GET /p/{key}/term/file?name=）：一名成员的
 * 完整 jsonl 史册。这里只拼 URL——下载是 <a download> 的事。
 * @param {string} key room key
 * @param {string} name member
 * @returns {string}
 */
export function termFileURL(key, name) {
  return `/p/${encodeURIComponent(key)}/term/file?name=${encodeURIComponent(name)}`;
}

/**
 * POST /p/{key}/replay — the 一日回放 recorder's leg (r_32): one batch
 * of sampled frames (t = wall-clock ms) folded into the room's day
 * file. The server's watermark makes re-sent batches idempotent, so a
 * failed post can simply retry the whole batch next beat.
 * @param {string} key room key
 * @param {{t: number, a: Array<{n: string, x: number, y: number, d: number,
 *   b?: string, w?: boolean, hold?: string, c?: string, hair?: string}>}[]} frames
 * @param {{keepalive?: boolean}} [opts] pagehide flush rides keepalive
 * @returns {Promise<{stored: number, skipped: number}>}
 */
export function postReplay(key, frames, opts = {}) {
  return fetch(`/p/${encodeURIComponent(key)}/replay`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ frames }),
    keepalive: !!opts.keepalive,
  }).then((resp) => {
    if (!resp.ok) return resp.json().catch(() => ({})).then(() => null); // 丢批不致命——水位线兜底
    return resp.json();
  });
}

/**
 * GET /p/{key}/replay?date= — the day player's paged read (r_32):
 * oldest-first frames past the since cursor (ms), history-face paging
 * (has_more + next_since). The token rides the query on the shared
 * 分享链接 path (the gate only unlocks its granted day).
 * @param {string} key room key
 * @param {string} date YYYY-MM-DD
 * @param {{since?: number, limit?: number, token?: string}} [opts]
 * @returns {Promise<{project: string, date: string, count: number,
 *   frames: Array<{t: number, a: Array<Object>}>, has_more: boolean, next_since: number}>}
 */
export function getReplayFrames(key, date, opts = {}) {
  const parts = [`date=${encodeURIComponent(date)}`];
  if (opts.since) parts.push(`since=${opts.since}`);
  if (opts.limit) parts.push(`limit=${opts.limit}`);
  if (opts.token) parts.push(`t=${encodeURIComponent(opts.token)}`);
  return getJSON(`/p/${encodeURIComponent(key)}/replay?${parts.join('&')}`);
}

/**
 * GET /p/{key}/replay/days — the date picker's data face (r_32): the
 * room's recorded days, newest first.
 * @param {string} key room key
 * @returns {Promise<{project: string, days: string[]}>}
 */
export function getReplayDays(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/replay/days`);
}

/**
 * POST /p/{key}/replay/share — mint the 一日回放分享链接 (r_32):
 * {date, confirm:"REPLAY"} grants (room, day) on the live token epoch
 * and answers the path; {off:true} revokes the epoch (every link dies).
 * @param {string} key room key
 * @param {{date?: string, off?: boolean, confirm?: string}} body
 * @returns {Promise<{on: boolean, date?: string, token?: string, path?: string}>}
 */
export function postReplayShare(key, body) {
  return postJSON(`/p/${encodeURIComponent(key)}/replay/share`, body);
}

/**
 * GET /p/{key}/replay/share — the share face's read: {on, days} for
 * this room (the export card's state).
 * @param {string} key room key
 * @returns {Promise<{on: boolean, days: string[]}>}
 */
export function getReplayShare(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/replay/share`);
}


/**
 * GET /visitor — the visitor gate's read face (r_19): {on, count} —
 * count is the 👁 N data (per-project observer snapshot aggregated on
 * the lobby key). Owner-only concern: the badge and the cockpit's
 * ① quadrant both read it; visitors themselves may see the count too
 * (透明无妨——r_24 定稿 §五).
 * @returns {Promise<{on: boolean, count: number}>}
 */
export function getVisitorOn() {
  return getJSON('/visitor');
}

/**
 * GET /kb/capacity[?all=1] — the saturation read face (r_24 t_205):
 * {rooms: [{room, doing, seated, sat, open, water, patrol_ts, updated_ts}],
 *  now} — one source feeding three consumers (patrol line / 选题
 * injection / owner cockpit), so the numbers only ever differ by time,
 * never by formula. ?all=1 spans the lobby + every active project;
 * absent = the lobby alone. Pure read — no clock it touches fires.
 * @param {boolean} [all]
 * @returns {Promise<{rooms: Array<{room: string, doing: number, seated: number, sat: number, open: number, water: number, patrol_ts?: number, updated_ts: number}>, now: number}>}
 */
export function getCapacity(all = false) {
  return getJSON(`/kb/capacity${all ? '?all=1' : ''}`);
}

/**
 * GET /kb/docs — the manual-docs index, most recently updated first.
 * 404 when the docs store is disabled. v3 治理：{archived:true} 翻到
 * 归档架；{q} 是 key＋标题的子串过滤（大小写不敏感）。
 * @param {{archived?: boolean, q?: string}} [opts]
 * @returns {Promise<import('./wire.js').KbDocMeta[]>}
 */
export function getDocs(opts = {}) {
  const qs = new URLSearchParams();
  if (opts.archived) qs.set('archived', '1');
  if (opts.q) qs.set('q', opts.q);
  const tail = qs.toString() ? `?${qs}` : '';
  return getJSON(`/kb/docs${tail}`);
}

/**
 * GET /kb/docs/{key}?rev= — one doc in full (rev 0/absent = the live
 * head; a positive rev is that version's read-only snapshot).
 * @param {string} key one-to-three slash levels (roles/hr)
 * @param {number} [rev]
 * @returns {Promise<import('./wire.js').KbDoc>}
 */
export function getDoc(key, rev = 0) {
  const qs = rev > 0 ? `?rev=${rev}` : '';
  return getJSON(`/kb/docs/${key.split('/').map(encodeURIComponent).join('/')}${qs}`);
}

/**
 * GET /kb/docs/{key}/history — the doc's version chain.
 * @param {string} key
 * @returns {Promise<import('./wire.js').KbDocRev[]>}
 */
export function getDocHistory(key) {
  return getJSON(`/kb/docs/${key.split('/').map(encodeURIComponent).join('/')}/history`);
}

/**
 * POST /offboard — the five-step departure pipeline (v2 P5-a). Never
 * throws on the domain answers: the 409 preflight block and the 400
 * validation body come back as data for the caller to render.
 * @param {{name: string, project?: string, reassign_to?: string, force?: boolean, dry_run?: boolean}} body
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postOffboard(body) {
  const resp = await fetch('/offboard', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * POST /reset — 工厂重置（设置卡「重置工作室」的最终一步）：后端校验
 * "confirm":"RESET" 后投一枚信号，进程随即收摊→擦除 ~/.niuma*→拉起
 * 全新替身。本请求的应答只负责把确认对话框收尾——窗口本身会在替身
 * 就位前后随旧进程一起退场，新窗口即首装状态。域名拒绝（口令不符
 * 400、已在重置 409）以 data 带回，不抛异常。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postReset() {
  const resp = await fetch('/reset', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ confirm: 'RESET' }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * POST /p/{key}/dismiss — 清退项目组成员：后端校验 "confirm":"DISMISS"
 * 后同步执行（踢房→删编制行→删全局档案→ZCode 会话深清），聊天历史
 * 保留。应答是 /offboard 同形状的步骤回执 {ok, steps[], note}；拒绝
 * （口令不符 400、大厅 400、项目不存在 404）以 data.error 带回，不抛
 * 异常。
 * @param {string} key 项目 key
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postProjectDismiss(key) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/dismiss`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ confirm: 'DISMISS' }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * POST /p/{key}/reset — 重置项目：后端校验 "confirm":"RESET" 后同步执行
 * （先清退全部成员，再清需求/任务/提案/会议/文档/开关与房间持久记忆，
 * 编制表按开张态重建）。应答同 dismiss 的步骤回执；大厅（400）与已归
 * 档项目（409）拒绝。成功后调用方应整页重载——聊天流各面板还持着旧
 * 项目的帧，重载是唯一诚实的收尾。
 * @param {string} key 项目 key
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postProjectReset(key) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/reset`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ confirm: 'RESET' }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * POST /p/{key}/delete — 删除项目（项目册除名，唯一不可逆的生命周期
 * 动词）：后端校验 "confirm":"DELETE" 后同步执行（清退成员＋深清
 * ZCode 会话 → 清全部台账架与项目文档（不重建编制表）→ 删房间持久
 * 档案与回放日 → 除名）。仅草稿/归档项目可删，开张中的项目 409 拒
 * 绝（先归档）；工作区目录与 git 仓库永远不动。应答同 dismiss 的步
 * 骤回执；拒绝（口令不符 400、大厅 400、未知 404、开张中 409）以
 * data.error 带回，不抛异常。
 * @param {string} key 项目 key
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postProjectDelete(key) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ confirm: 'DELETE' }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * POST /rebuild — 重新编译并重启（设置卡「重新编译并重启」）：后端在
 * 前台跑 go build 并把新二进制原子换位，成功才投重启令牌——进程随即
 * 收摊→拉起新二进制，本窗口随旧进程退场、新窗口自动顶上；失败（编译
 * 错误/无源码/无 Go 工具链）以 400 带原因返回，应用原地不动。应答可
 * 能要等一轮编译（热缓存几秒、冷缓存一两分钟），也可能被收摊掐断
 * （编译已成功的情形）——后者按「连接已断」中性提示收尾，不装作知道。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postRebuild() {
  const resp = await fetch('/rebuild', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /retention — 聊天记录保留期（v2.8.2）读面：{days, default_days}，
 * days 0=永久。设置卡「数据」分节的真源。
 * @returns {Promise<{days: number, default_days: number}>}
 */
export function getRetention() {
  return getJSON('/retention');
}

/**
 * POST /retention — 聊天记录保留期写面：{days: 0（永久）|1..3650}。服务端
 * 落盘即按新策略当场清扫（回执带 {days, pruned}——pruned 是本次清掉的
 * 旧消息条数）。不抛异常风格（设置卡维护动作专用），400 的原因在 data。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setRetention(days) {
  const resp = await fetch('/retention', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ days }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /dispatch-wait — 消息注入等待读面：{seconds, default_seconds,
 * min_seconds, max_seconds}（缺省 300 秒 = 5 分钟）。设置卡「智能」
 * 分节的真源——调度器给成员注入消息后等 ack 的时长。
 * @returns {Promise<{seconds: number, default_seconds: number, min_seconds: number, max_seconds: number}>}
 */
export function getSendWait() {
  return getJSON('/dispatch-wait');
}

/**
 * POST /dispatch-wait — 消息注入等待写面：{seconds: 10..3600}。服务端
 * 落盘并热生效到调度器（下一次投递即按新值等待，无需重启）。不抛异常
 * 风格（设置卡专用），400 的原因在 data。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setSendWait(seconds) {
  const resp = await fetch('/dispatch-wait', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ seconds }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /budget — token 预算读面（r_26）：{day_tokens, week_tokens（0=不限）,
 * day_used, week_used, day_start, week_start（窗口锚点 unix 秒）, tripped_day,
 * tripped_week, breaker_live, usage_error}——设置卡·智能 预算旋钮的回显面与
 * 驾驶舱成本象限的共同真源（只有一个口径面）。台账读挂时 usage_error=true、
 * 旋钮照回（耗粮标不可用，不是 5xx）。
 */
export function getBudget() {
  return getJSON('/budget');
}

/**
 * GET /achieve/tests-green — 成就读面：{unlocked: {key[@who]: ts},
 * achievements: [{key, name, text, perMember, medal}]}——成就页签与
 * 驾驶舱④象限「最近三奖章」的共同真源。
 * @returns {Promise<{unlocked: Object<string, number>, achievements: Array<{key: string, name: string, text: string, perMember: boolean, medal: number}>}>}
 */
export function getAchievements() {
  return getJSON('/achieve/tests-green');
}

/**
 * POST /budget — token 预算写面：成对覆写 {day_tokens, week_tokens}（预算是
 * 一份策略，0=不限）。落盘 budget.json 并热生效——自动驾驶引擎每拍直读，
 * 下一拍即按新值熔断/放开。不抛异常风格（设置卡专用），400 的原因在 data。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setBudget(dayTokens, weekTokens) {
  const resp = await fetch('/budget', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ day_tokens: dayTokens, week_tokens: weekTokens }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /reveal — ZCode 侧栏即时刷新（设置窗·连接面板）读面：
 * {mode, on, darwin}——on 是本平台的投递判定（服务端 RevealDelivers
 * 的裁决，开关画的就是进程真正会做的事）；darwin 供行文案按平台说
 * 人话（信任框代价只在 macOS 存在）。
 * @returns {Promise<{mode: string, on: boolean, darwin: boolean}>}
 */
export function getReveal() {
  return getJSON('/reveal');
}

/**
 * POST /reveal — ZCode 侧栏即时刷新写面：{on: boolean}。服务端落盘
 * reveal.json 并当场换进程内镜像（下一次投递立刻按新模式走）；开启
 * 的那一笔还会顺手强投一次揭示——把已在索引库里置顶的牛马行当场拉
 * 回 ZCode 侧栏（重启收摊后被 boot 回填重新置顶、只差桌面端重读的
 * 那些）。不抛异常风格（设置卡维护动作专用），400 的原因在 data。
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setReveal(on) {
  const resp = await fetch('/reveal', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ on }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /lang — 界面语言读面：{lang: 'zh'|'en'}。后端文案（错误串/系统
 * 广播/成就名）按这个值返回；真源 ~/.niuma/lang.json，前端切换时推送。
 * @returns {Promise<{lang: string}>}
 */
export function getLang() {
  return getJSON('/lang');
}

/**
 * POST /lang — 界面语言写面：{lang: 'zh'|'en'}。前端 setLang() 落偏好
 * 后顺手推给后端（fire-and-forget，失败静默）；启动时也会对齐一次，
 * 让后端文案与前端语言不脱节。不抛异常风格。
 * @param {string} lang
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postLang(lang) {
  const resp = await fetch('/lang', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ lang }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /p/{key}/autopilot — 全智能模式（v2.8）的状态读面：设置窗「智能」
 * 分节的真源。r_12（t_171）扩为 {autopilot:{on,poke_every,accept_delay,
 * stall_delay,max_plans}}；max_plans 落盘 0 在 API 面回 -1（「不限」语义）。
 * 兼容期旧载荷 {autopilot:boolean, max_plans} 由调用侧归一。
 * @returns {Promise<object>}
 */
export function getAutopilot(key) {
  return getJSON(`/p/${key}/autopilot`);
}

/**
 * POST /p/{key}/autopilot — 自动补货/自动推进双开关写面（r_19）＋五旋钮
 * ＋本次目标（目标制补货）。开任一开关须带协议级确认令牌
 * confirm:"AUTOPILOT"（无人值守消耗 token——前端从警告对话框发起，
 * 服务端缺令牌即 400）；只改旋钮值也带令牌（契约 §三：一处确认对话框
 * 管整卡）；关闭免确认。patch 字段全可选：stock/advance（布尔，未传
 * 不动该开关——服务端区分未传与 false）、旋钮五字段（未传字段服务端
 * 不动）、goal（开补货必填——服务端 off→on 缺 goal 即 400；补货已开
 * 时随带即热更目标）。不抛异常风格（设置窗维护动作专用）。
 * @param {string} key @param {{stock?:boolean,advance?:boolean,poke_every?:number,accept_delay?:number,stall_delay?:number,max_plans?:number,water_target?:number,goal?:string}} [patch]
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setAutopilot(key, patch) {
  const p = patch || {};
  const body = {};
  if ('stock' in p) body.stock = !!p.stock;
  if ('advance' in p) body.advance = !!p.advance;
  if ('goal' in p) body.goal = p.goal;
  if (body.stock || body.advance) body.confirm = 'AUTOPILOT'; // 开任一开关都要令牌
  for (const k of ['poke_every', 'accept_delay', 'stall_delay', 'max_plans', 'water_target']) {
    if (k in p) body[k] = p[k];
  }
  if (body.confirm === undefined && Object.keys(body).length > 0) {
    body.confirm = 'AUTOPILOT'; // 契约：改值不开关也带令牌（旋钮写免确认门）
  }
  const resp = await fetch(`/p/${key}/autopilot`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /p/{key}/pause — 房间暂停的读面：{project, paused}。侧栏房行
 * 暂停/开始按钮的状态源之一（welcome 帧与 "pause" 广播是实时同步面，
 * 这里是冷读兜底）。
 * @param {string} key
 * @returns {Promise<{project: string, paused: boolean}>}
 */
export function getRoomPause(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/pause`);
}

/**
 * POST /p/{key}/pause — 房间暂停的写面（暂停/恢复同一条）：暂停期间
 * 该房对话（调度回合）、会议、巡逻/日报、全智能引擎、看护补员全部
 * 停摆，办公室小人定格；恢复即按原顺序续投。免确认令牌（两个方向都
 * 只省不费）。不抛异常风格（维护动作专用）。
 * @param {string} key @param {boolean} paused
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function postRoomPause(key, paused) {
  const resp = await fetch(`/p/${encodeURIComponent(key)}/pause`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paused: !!paused }),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

/**
 * GET /p/{key}/rebuild-vote — AI 重编译投票开关的读面（按项目）：
 * {project, rebuild_vote}。设置窗「智能」分节第三位开关的真源。
 * @param {string} key
 * @returns {Promise<{project: string, rebuild_vote: boolean}>}
 */
export function getRebuildVote(key) {
  return getJSON(`/p/${encodeURIComponent(key)}/rebuild-vote`);
}

/**
 * POST /p/{key}/rebuild-vote — AI 重编译投票开关写面。开启须带协议级
 * 确认令牌 confirm:"REBUILD"（成员投票超半数将直接重启整个工作室——
 * 前端从警告对话框发起，服务端缺令牌即 400）；关闭免确认（服务端还会
 * 作废该房在投的票）。不抛异常风格（设置窗维护动作专用）。
 * @param {string} key @param {boolean} on
 * @returns {Promise<{status: number, ok: boolean, data: any}>}
 */
export async function setRebuildVote(key, on) {
  const body = { on: !!on };
  if (on) body.confirm = 'REBUILD';
  const resp = await fetch(`/p/${encodeURIComponent(key)}/rebuild-vote`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await resp.json().catch(() => ({}));
  return { status: resp.status, ok: resp.ok, data };
}

async function getJSON(url) {
  const resp = await fetch(url, { headers: { Accept: 'application/json' } });
  if (resp.ok) sampleServerClock(resp); // room-time skew + HTTP-alive ride along
  if (!resp.ok) {
    // prefer the server's JSON {"error": "..."} reason when it sent one
    // (writeJSONErr faces) — a bare status line hides the actionable why
    let reason = '';
    try {
      const body = await resp.json();
      if (body && typeof body.error === 'string') reason = body.error;
    } catch { /* not JSON — fall back to the status line */ }
    const err = new Error(reason || `${url}: HTTP ${resp.status}`);
    err.status = resp.status;
    throw err;
  }
  return resp.json();
}

// The plain-text read family (/prompt, /kb/manual, /kb/notice).
async function getText(url) {
  const resp = await fetch(url);
  if (!resp.ok) {
    const err = new Error(`${url}: HTTP ${resp.status}`);
    err.status = resp.status;
    throw err;
  }
  return resp.text();
}

// The /dispatch/ family answers in plain text: the receipt body IS the
// result (a session id, or the refusal reason on a 400) — shown in
// place, never swallowed (§4.5).
async function postText(url, body) {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const text = (await resp.text()).trim();
  if (!resp.ok) throw new Error(text || `${url}: HTTP ${resp.status}`);
  return text;
}

/**
 * POST /media — 上传一张图片进媒体仓库（输入图片的第一步：拿到引用，
 * say 帧的 images 字段只带引用，字节不进 WS）。body 即原始字节，
 * ?name= 带原文件名（Content-Type 缺失/通用时服务端按后缀兜底）。
 * 上传失败抛带原因的 Error——composer 的缩略图转红展示，不打断打字。
 * @param {File|Blob} file @param {string} [name] 覆盖文件名（粘贴无名时）
 * @returns {Promise<{image: import('./wire.js').MsgImage, url: string}>}
 */
export async function postMedia(file, name) {
  const nm = name || file.name || '';
  const resp = await fetch(`/media?name=${encodeURIComponent(nm)}`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    body: file,
  });
  if (resp.ok) {
    sampleServerClock(resp);
    return resp.json();
  }
  let reason = '';
  try {
    const body = await resp.json();
    if (body && typeof body.error === 'string') reason = body.error;
  } catch { /* not JSON */ }
  throw new Error(reason || t('上传失败：HTTP {code}', { code: resp.status }));
}

/**
 * GET /usage — 耗粮统计总览（r_xx）：全工作室牛马员工的 token 台账
 * （按员工聚合、最大耗子在前）＋工作室合计行＋趋势面（series：时/天/
 * 周/月四档日历分桶＋全史按模型合计——员工表与趋势图一趟扫账，一次
 * 取数抽屉整面渲染）。数据真源是 ZCode CLI 的 turn_usage/model_usage
 * （server/usage.go 讲 join）；缺库 = 空表。
 * @returns {Promise<{employees: import('../ui/usageview.js').UsageEmployeeRow[],
 *   count: number, total: import('../ui/usageview.js').UsageEmployeeRow,
 *   series: import('../ui/usageview.js').UsageSeriesReport}>}
 */
export function getUsage() {
  return getJSON('/usage');
}

/**
 * GET /usage/turns — 一名员工的每轮耗粮明细（新→旧，每轮附按模型
 * 分摊）；limit 缺省 100、服务端钳 1..500。
 * @param {string} employee 员工标题（【项目】名-岗-Lv.N）
 * @param {number} [limit]
 */
export function getUsageTurns(employee, limit = 100) {
  const q = new URLSearchParams({ employee, limit: String(limit) });
  return getJSON(`/usage/turns?${q}`);
}

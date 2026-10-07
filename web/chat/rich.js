// rich.js — 聊天流的纯函数前导（自 stream.js 抽出）：装扮语言（dress：
// t_NN 任务 chip 与 @提及令牌，小助手面板与流渲染共用同一份）、旧引用
// 前缀/编制建议行/快捷入口的文本解析、提案 submitted 卡的等待行钟面、
// 窗帽裁头方案与流渲染的常量词表。全部纯函数/常量——不触 DOM、不持状
// 态；stream.js 经 re-export 保持既有导入面（只缩不涨，同 Go 侧
// chat/wire.go 兼容 shim 的纪律），新代码直接 import rich.js。
import { Msg } from '../wire/wire.js';
import { esc, fmtTime } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from './markdown.js';

export const TASK_ID_RE = /\bt_\d{1,6}\b/g;
const MENTION_RE = /@[\w\u4e00-\u9fff][\w\u4e00-\u9fff_-]*/g;
// 全员性令牌（@所有人 点名 / @全员 唤醒）与岗位群呼令牌（@<岗位>组）
// 的分级高亮：词表与后端 chat 包的 allMentionKind/groupWakeLocked 同源。
const ALL_TOKEN_RE = /^@(所有人|全员)$/;
const GROUP_TOKEN_RE = /^@[\w\u4e00-\u9fff_-]+组$/;
// 有人@我 的全员令牌兜底：服务端 mentions 已按逐人展开但豁免房主，
// 徽标语义要求房主也看见「全房被点了名」——只认裸令牌，宁多勿漏。
export const ALL_AT_RE = /@(所有人|全员)/;
export const GROUP_WINDOW = 5 * 60; // seconds: same-sender header collapse
export const GAP_DIVIDER = 10 * 60; // seconds: a quiet gap earns a time divider
// 回原位浮标的驻留门（px）：来路与视口差不到这一截就不指路——人在
// 原地附近的「回原位」是噪音，跳出了屏才算真的走远。
export const BACK_NEAR = 240;
// 跳到未读的停线高度（px）：落位后「以下是新消息」界线要停在视口上
// 部一眼可见——锚行顶到视口顶留这一截（界线高＋呼吸口），往下补读
// 的起点不顶死在边框上。
export const UNREAD_PAD = 48;
// 长内容折叠线住在 fold.js（对话/汇报/通知卡/任务描述共用的词汇）

// 问题卡答案的补充段分隔符：`<选项部分>；补充：<文本>`——选项与补充
// 可并存（单选也常要补一句话），收口读档按它对称还原；老答案没有
// 尾巴，indexOf 一次原样通过。
export const Q_SUPP = '；补充：';

// 表情回应的快捷盘（飞书式常用表情）：emoji 本身即线上载荷——台账按
// 字符串存（后端 validReactEmoji 校验），盘外的表情进不了 chips。
export const REACT_SET = ['👍', '👌', '✅', '❌', '⚠️', '🔥', '🎉', '🙏', '💡', '👀', '🚀', '😂'];

// 成员提问行（dispatch 侧 askLineText 的两种Inline 形——真 @ 与无房主
// 兜底）的指纹：开放问题卡的行内锚点。指纹只认自己生成的行首形态，
// 成员自然发言碰不出这个开头。
export const ASK_LINE_RE = /^（(?:@[^\s]+ )?需要你确认：|^（向房主提问：/;

// ---- 提案 submitted 卡的等待行（时间显示＋代收预计） ----------------------
// 房主在场让位（r_19 修订）让「自动推进开着却不动」成了一口看不见的
// 钟——提案在槽、宽限早满、每一拍都因房主 10 分钟内说过话而让位，房主
// 眼里就是「设置开了没推进」。等待行把这口钟亮在卡上：已候多久、开着
// 自动推进时预计何时代收（在场让位中就明说）。在场是估算面：服务端
// chat.OwnerPresenceWindow 的前端镜像＋本房房主最近一条 say（messages
// 倒扫）与点卡应答（#sendAnswer 落戳）——ETA 差几秒无妨，看得见才是
// 目的。
export const OWNER_PRESENCE_S = 10 * 60; // chat.OwnerPresenceWindow 的镜像（秒）

/** 已候时长的口语化：与 fmtAge 同词汇但去掉「前」——「已候 3 分钟」。 */
function waitDur(s) {
  if (s < 60) return t('刚刚');
  if (s < 3600) return t('{n} 分钟', { n: Math.floor(s / 60) });
  if (s < 86400) return t('{n} 小时', { n: Math.floor(s / 3600) });
  return t('{n} 天', { n: Math.floor(s / 86400) });
}

/** 提案 submitted 卡等待行文案（纯函数，测试直测）。advance/paused 是引
 *  擎态（GET /p/{key}/autopilot 的 auto_advance＋房记录 paused），pending
 *  =false（槽里已有更新的 plan 帧——本卡已被代收/驳回/换代）时整行撤
 *  下：历史卡不再假装钟还在走。
 *  @param {import('../wire/wire.js').Frame} frame
 *  @param {{now?:number, advance?:boolean, acceptDelayS?:number, ownerLastActiveS?:number, paused?:boolean, pending?:boolean}} [st]
 *  @returns {string} 空串＝不出行 */
export function planWaitText(frame, st = {}) {
  const p = frame?.plan || {};
  const submitted = p.submitted_ts || frame?.ts || 0;
  if (!submitted || st.pending === false) return '';
  const now = st.now || 0;
  const base = `⏱ ${t('已候 {d}', { d: waitDur(Math.max(0, now - submitted)) })}`;
  if (!st.advance || st.paused) return base; // 推进没开/房暂停：只报候时，不编钟
  const delay = st.acceptDelayS > 0 ? st.acceptDelayS : 120; // staffing.DefaultAcceptDelayS 缺省
  const graceEnd = submitted + delay;
  const holdEnd = st.ownerLastActiveS > 0 ? st.ownerLastActiveS + OWNER_PRESENCE_S : 0;
  const eta = Math.max(graceEnd, holdEnd);
  if (now >= eta) return `${base} · ${t('代收条件已满，引擎下一拍放行')}`;
  if (holdEnd > graceEnd) {
    return `${base} · ${t('你在场，代收让位中——{time} 前无动作即自动放行', { time: fmtTime(eta) })}`;
  }
  return `${base} · ${t('预计 {time} 自动代收', { time: fmtTime(eta) })}`;
}

// ---- 编制建议行（聊天快捷入口） ------------------------------------------
// HR/编排者请房主加编的标准建议形态（hrSeed 招聘流程第 1 步的「岗位行」）：
// 正文里一条竖线行 `| key | 名称 | 身份 | 编制 | 自动补员 | 手册 |`
// （冻结六列；第七列「必须」可带，系统岗建议跳过）。识别从严——key 须为
// 短 slug、编制须为纯整数、自动补员须是 是/否——普通表格对不上这三关，
// 误配不出按钮；引了整张表的段落（带冻结表头行）视为转述不是建议。
const EST_SYSTEM_KEYS = new Set(['orchestrator', 'hr', 'assistant']);
const EST_KEY_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$/;
const EST_FILL = { '是': true, yes: true, true: true, '否': false, no: false, false: false };

/** 编制建议行解析：每条命中一行建议（同 key 去重，至多 3 条）。
 *  @param {string} text @returns {{key:string,name:string,role:string,headcount:number,auto_fill:boolean,manual:string,from:string}[]} */
export function parseEstOffers(text) {
  const offers = [];
  const seen = new Set();
  for (const raw of String(text || '').split('\n')) {
    if (!/^\s*\|/.test(raw)) continue;
    const cells = raw.trim().replace(/^\|/, '').replace(/\|[ \t]*$/, '')
      .split('|').map((c) => c.trim());
    if (cells[0] === '岗位 key' || cells.includes('自动补员')) return []; // 整表转述
    if (cells.length < 6) continue;
    const [key, name, role, count, fill, manual, tail] = cells;
    if (!EST_KEY_RE.test(key) || EST_SYSTEM_KEYS.has(key)) continue;
    if (!/^\d{1,3}$/.test(count) || !(fill in EST_FILL)) continue;
    if (tail === '是') continue; // 「必须」是代码的列，不是房主能加的岗
    if (seen.has(key)) continue;
    seen.add(key);
    offers.push({
      key,
      name: name.slice(0, 32),
      role: role.slice(0, 32),
      headcount: Math.min(50, Math.max(1, parseInt(count, 10))),
      auto_fill: EST_FILL[fill],
      manual: (manual || '').slice(0, 64),
      from: '',
    });
    if (offers.length >= 3) break;
  }
  return offers;
}

// ---- 消息快捷入口（跳转条，复用编制建议条 UI） ---------------------------
// 拆解/排期/需求环节的消息在气泡下挂跳转钮，把房主一步送到对应板块——
// 聊天侧只负责送人，动作都留在落地面里（审提案开浮层、看排期/看需求
// 只切页签）。识别与编制建议同一分从严：光有 id 不够，还须带环节语境
// （提案 p_NN × 提案/待审词、t_NN × 排期动词、r_NN × 需求/拆解/评审
// 词），普通闲谈对不上就不出钮——p_/r_ 编号本身只存在于提案与需求域，
// 误配面天然小。
const PLAN_ID_RE = /\bp_\d{1,6}\b/g;
const REQ_ID_RE = /\br_\d{1,6}\b/g;
export const JUMP_META = {
  plan: { tag: t('快捷入口'), title: t('跳到 项目管理 → 该项目，打开待审提案的审阅浮层——接受/修订/拒绝都在浮层里') },
  gantt: { tag: t('快捷入口'), title: t('跳到 项目管理 → 排期甘特，看这批任务排上之后的整体盘面') },
  req: { tag: t('快捷入口'), title: t('跳到 项目管理 → 需求页签，看这条需求的状态与拆解去向') },
};

/** 快捷入口解析：每类至多一枚（消息通常只围绕一件正事），无命中返回
 *  空数组——条本身不出。
 *  @param {string} text @returns {{kind:'plan'|'gantt'|'req', id:string, label:string}[]} */
export function parseJumpOffers(text) {
  const s = String(text || '');
  const offers = [];
  const planId = s.match(PLAN_ID_RE)?.[0];
  if (planId && /提案|待审|请审|审批|过审/.test(s)) {
    offers.push({ kind: 'plan', id: planId, label: t('审提案 {id}', { id: planId }) });
  }
  if (/\bt_\d{1,6}\b/.test(s) && /改期|顺延|重排|排期|延后|提前|开工|挪/.test(s)) {
    offers.push({ kind: 'gantt', id: '', label: t('看排期') });
  }
  const reqId = s.match(REQ_ID_RE)?.[0];
  if (reqId && /需求|拆解|评审/.test(s)) {
    offers.push({ kind: 'req', id: reqId, label: t('看需求 {id}', { id: reqId }) });
  }
  return offers;
}

// （转发/引用的单行摘要 snipOf 从 ui/dom.js 引入——与 composer 引用条
// 同一个函数、同一份宽度帽，两处永不各截各的。）

// className 串里有无某类名（真 DOM 与 testdom 假节点同一口径：行与
// 分隔线的类别都经 className 属性落笔，classList.contains 在假节点上
// 恒 false，串匹配两边都真）。
export const hasCls = (el, name) => !!el && (' ' + String(el.className || '') + ' ').includes(' ' + name + ' ');

// 裁头方案（纯函数，streamnav.test 直测）：消息窗到帽后要从头上匀走
// 的行——行数超帽的超出量从前头数，新首行若是无主的连发续行（grouped
// 没有自己的头像与名字）顺带多裁到非续行为止。调用方按裁走行合计高
// 补还 scrollTop（#trimCapped），读位不被匀走的行拽动。
// @param {Element[]} kids @param {number} cap @returns {Element[]}
export function trimPlan(kids, cap) {
  const isRow = (el) => hasCls(el, 'line') || hasCls(el, 'time-divider');
  let count = 0;
  for (const el of kids) if (isRow(el)) count++;
  const cut = [];
  let i = 0;
  while (i < kids.length && count > cap) {
    if (isRow(kids[i])) { cut.push(kids[i]); count -= 1; }
    i += 1;
  }
  while (i < kids.length && hasCls(kids[i], 'grouped')) { cut.push(kids[i]); i += 1; }
  return cut;
}

// 引用回复的旧线上形态是 composer 前置的「引用 #N @X：…」纯文本前缀
// ——结构化引用（frame.quote，chat.Quote 字段）上线后新消息正文不再
// 带；这道解析只服务历史存量（重启前的旧 jsonl、转发带来的旧串），
// 渲染时同样还原成灰底引用条（@X 亮成可点名牌：飞书式「回复 @某
// 人」，发送即点名通知 TA），回文本体照常走 rich 表达。#N 是原消息
// seq：跳转先按 seq 精确命中；旧格式（「引用 X：…」，无 @ 无 seq）
// 照旧按发送者＋摘要匹配。
export const QUOTE_RE = /^「引用 (?:#(\d+) )?(@?)([^「」：]{1,60})：([\s\S]{0,200}?)」\s*\n?/;

/** @param {string} text @returns {{seq: number, at: boolean, from: string, snip: string, rest: string}|null} */
export function parseQuote(text) {
  const m = QUOTE_RE.exec(String(text || ''));
  if (!m) return null;
  return { seq: m[1] ? +m[1] : 0, at: m[2] === '@', from: m[3], snip: m[4], rest: text.slice(m[0].length) };
}

/** Whitespace-collapsed form — quote snippets match source through it. */
export const normText = (s) => String(s || '').replace(/\s+/g, ' ').trim();

/** chips + mentions over an already-escaped segment (URLs stay bare).
 *  导出供小助手面板共用——两处的正文走同一装扮语言（t_NN chip、@提及）。
 *  单人 @令牌带 data-pfat：点了开牛马名片（ui/profilecard.js 挂在
 *  app.js，小助手面板暂不接线、属性无害）；@所有人/@全员/@岗位组 是
 *  群呼令牌不是人，不挂。 */
export function dress(s) {
  return s
    .replace(TASK_ID_RE, (id) =>
      `<button type="button" class="task-chip" data-task="${id}">${id}</button>`)
    .replace(MENTION_RE, (m) => {
      const cls = mentionTokenClass(m);
      const pf = cls === 'mention-token' ? ` data-pfat="${esc(m.slice(1))}"` : '';
      return `<span class="${cls}"${pf}>${m}</span>`;
    });
}

/** Mention tokens tier up: all-address and role-group calls read
 *  stronger than a single-person @ (飞书式 @所有人 醒目同款意图). */
function mentionTokenClass(m) {
  if (ALL_TOKEN_RE.test(m)) return 'mention-token at-all';
  if (GROUP_TOKEN_RE.test(m)) return 'mention-token at-group';
  return 'mention-token';
}

/** 正文：Markdown 渲染（markdown.js 负责转义/链接/强调），dress 把
 *  t_NN 任务 chip 与 @提及装扮进纯文本段。 */
export function richHTML(text) {
  return markdown(text, { dress });
}

export const KIND_META = {
  [Msg.Task]: { label: t('任务'), icon: 'clipboard' },
  [Msg.Req]: { label: t('需求'), icon: 'flag' },
  [Msg.Agent]: { label: t('牛马'), icon: 'person' },
  [Msg.Kb]: { label: t('黑板'), icon: 'notebook' },
  [Msg.SkillEvt]: { label: t('技能'), icon: 'bolt' },
  [Msg.McpEvt]: { label: t('MCP'), icon: 'bolt' },
  [Msg.Plan]: { label: t('提案'), icon: 'calendar' },
  [Msg.Meeting]: { label: t('会议'), icon: 'video' },
  // v2.8 gitflow：提交卡（post-commit 钩子播报）与合并提案卡——与
  // kb 的红绿对比同一「看得见改了什么」的纪律。
  [Msg.Git]: { label: t('提交'), icon: 'branch' },
  [Msg.Merge]: { label: t('合并'), icon: 'branch' },
};
export const EVENT_LABEL = {
  created: t('新建'), updated: t('更新'), proposal: t('变更申请'), confirmed: t('变更生效'),
  declined: t('已婉拒'), denied: t('被拒'), written: t('写入'), appended: t('追加'), restored: t('回滚'),
  started: t('开始'), ended: t('结束'), advanced: t('进总结'), extended: t('延时'),
  commit: t('新提交'), submitted: t('待审'), merged: t('已并入'), superseded: t('被取代'), rejected: t('已驳回'),
};

// 问题卡的窗口小签：due（unix 秒）距现在还剩多久——「30 分钟内有效」。
export function dueLabel(due) {
  if (!due) return '';
  const mins = Math.round((due - Date.now() / 1000) / 60);
  if (mins <= 0) return t('即将过期');
  if (mins < 60) return t('{n} 分钟内有效', { n: mins });
  return t('{n} 小时内有效', { n: Math.round(mins / 60) });
}

// cockpit.js — t_205（r_24）：房主驾驶舱——「今日工作室」单屏总览。
// 黑板板块新页签（与工作室志/成就同族），纯前端聚合接口的 DOM 面板
// （定稿 §〇：零新服务端服务面——/kb/capacity 是既有饱和度的只读抽取
// 面，巡报/注入/驾驶舱三消费者同源）。
//
// 四象限（定稿 §一）：
//   ① 人在哪——/kb/people 四态计数＋👁 访客数（/visitor）
//   ② 活在干——/kb/tasks doing 卡（谁/标题/已进行时长）＋今日 done 计数
//   ③ 池有多深——/kb/capacity?all=1（水位三档色签＋饱和度＋三档计数）
//   ④ 今天值得看——各房工作室志今日条目聚合（志分本后驾驶舱仍是全室
//      总览：大厅＋各项目各拉一本；项目志未建档读作空）＋/achieve 最近三枚
//   ⑤ 耗粮多少（r_26 成本象限，通栏）——/budget：今日/本周已耗 vs 日/周
//     上限（三档色签同水位口径）、熔断态。成本是钱面：访客档整段不
//     出数字（「成本面仅房主可见」占位，DOM 断言无耗粮数字）。
//
// 纪律（定稿 §三＋小狐评审③）：
//   - 20s 轮询与 META_EVERY 同族；首拍不等（打开即拉）；
//   - paint 门槛：每象限上次 HTML 相同则跳过 innerHTML 写入（防闪屏
//     兼防点击被重写吞掉——ChroniclePanel 同款）；
//   - document.hidden 跳拍，visibilitychange 回来立即补一拍；
//   - 切走页签（switchTab）或离开黑板板块（kb.conceal←app.js route）
//     即 clearInterval——不看就不烧轮询；
//   - 「今日」＝自然日本地零点起（ts >= 当日 00:00）——与台账
//     UpdatedTS/CreatedTS 同钟；「已进行时长」从 working_since 起算
//     不截断到零点（跨夜干活从开干时刻算，展示语义是「这件事干了
//     多久」不是「今天干了多久」）；
//   - 零副作用：全 GET，不出现在房间流（边界条款 §四）；
//   - 头部纪律文案「只读总览 · 管理请走各入口」（评审补充④）。
//
// 访客公开档（定稿 §五）：visitorMode() 时只渲染 ③ 的水位数字＋色签、
// ④ 的成就三枚、① 的访客计数——不出成员四态明细（working-detail 类
// 名整段不渲染，DOM 断言：访客帧无该类名）；今日 done 计数匿名聚合
// （「今日交付 N 单」，不带谁几单——评审补充③：个人工作量出门即绩效
// 公示）；⑤ 成本面不出（成本是钱面，出门即账本）。降级：四象限各自
// 空态/错误态独立降级，绝不白屏。

import { getPeople, getTasks, getCapacity, getDoc, getVisitorOn, getBudget, getAchievements } from '../wire/api.js';
import { visitorMode } from '../ui/visitor.js';
import { t } from '../ui/i18n.js';
import { parseChronicle } from './chronicle.js';
import { MEDAL_PAL } from './achievements.js';
import { fmtTokens } from '../ui/usageview.js';
import { esc } from '../ui/dom.js';

// 20s —— 与房间 meta 刷新同拍（定稿 §三：不添新节拍器）
const POLL_EVERY = 20_000;

// 本地自然日零点（unix 秒）——「今日」口径（定稿 §二）
export function dayStart(now = Date.now()) {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

// 水位三档色签（r_13 同口径：open ≥ target 健康绿 / target-1 偏低黄 /
// 更少见底红——water 是目标线）
export function waterLevel(open, water) {
  if (open >= water) return { key: 'ok', label: t('健康') };
  if (open >= water - 1) return { key: 'mid', label: t('偏低') };
  return { key: 'low', label: t('见底') };
}

// 预算三档色签（r_26 成本象限，水位同形）：limit ≤ 0 = 不限（灰签）；
// 到额 ≥100% 已到额红 / ≥80% 接近黄 / 否则预算内绿
export function budgetLevel(used, limit) {
  if (!(limit > 0)) return { key: 'off', label: t('不限'), pct: 0 };
  const pct = Math.floor((used / limit) * 100);
  if (used >= limit) return { key: 'low', label: t('已到额'), pct };
  if (pct >= 80) return { key: 'mid', label: t('接近'), pct };
  return { key: 'ok', label: t('预算内'), pct };
}

// fmtDur：已进行时长的紧凑语（时/分）
function fmtDur(sinceSec, nowSec) {
  if (!sinceSec) return '';
  const m = Math.max(0, Math.floor((nowSec - sinceSec) / 60));
  if (m < 60) return t('{m} 分钟', { m });
  const h = Math.floor(m / 60);
  return h < 24 ? t('{h} 时 {m} 分', { h, m: m % 60 })
    : t('{d} 天 {h} 时', { d: Math.floor(h / 24), h: h % 24 });
}

// fmtHM：巡报采集时刻「HH:MM」（评审补充②——一小时前的快照不当现在时）
function fmtHM(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

// fmtDate：编年史条目的日期段（YYYY-MM-DD）
function fmtDate(ts) {
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export class CockpitPanel {
  /** @param {() => Array<{key: string, room: string}>} [chronSources]
   *  各房工作室志源清单（kb.js 喂：大厅＋各项目房）。志按办公室分本后
   *  驾驶舱仍是全室总览——④的今日条目聚合各房；项目志未建档（该房还没
   *  有事件）读作空，不当错误。缺省回落只读大厅的 ops/chronicle。 */
  constructor(chronSources) {
    this.chronSources = chronSources;
    this.root = null;
    this.timer = 0;
    this.token = 0;
    this.lastPaint = {}; // pane key → 上次 HTML（paint 门槛）
    this.data = {};      // pane key → 象限数据（空态判断用）
    this.onStop = null;  // kb.js 的 stop 钩子挂这里
    this.retryBound = false; // 重试委托只挂一次（load 反复进出不叠加监听）
  }

  /** 打开即拉＋起拍；重复进入幂等（定时器不叠）。壳被别家页签的迟到
   *  paint 整面重写过时（els 脱树），重进立即重建——不该干等下一拍
   *  才恢复（快速切换「点了没反应」的另一半根因）。 */
  load(root) {
    this.root = root;
    this.bindRetry(root); // t_206 移交：错误象限重试钮（事件委托一次挂）
    if (!this.timer) {
      this.refresh();
      this.timer = setInterval(() => this.tick(), POLL_EVERY);
      document.addEventListener('visibilitychange', this);
    } else if (this.els && this.els.p1 && this.els.p1.isConnected === false) {
      this.refresh(); // 定时器照旧（不叠拍），refresh 里的 paintShell 会重建壳
    }
  }

  /** 离开页签即停（定稿 §三：不看就不烧轮询）；token 同拍作废——
   *  在途 refresh 的迟到回包不再 paint（切走后写进脱树节点是白烧，
   *  更糟的是 lastPaint 记了旧值，等下一拍才重画）。 */
  stop() {
    this.token++;
    if (this.timer) clearInterval(this.timer);
    this.timer = 0;
    document.removeEventListener('visibilitychange', this);
  }

  handleEvent(ev) {
    if (ev.type !== 'visibilitychange') return;
    // 切走跳拍（礼貌底线）；回来立即补一拍
    if (!document.hidden) this.tick();
  }

  tick() {
    if (document.hidden) return;
    this.refresh();
  }

  async refresh() {
    const tok = ++this.token;
    this.paintShell();
    const visitor = visitorMode();
    // 接口并行；各象限独立降级（一个面挂了不拖邻家）。④的志源是清单：
    // 大厅＋各项目房各拉一本（sources[0] 恒大厅——它挂了才算编年史错误，
    // 项目志未建档 404 读作空，只是没有条目）
    const sources = (typeof this.chronSources === 'function' && this.chronSources())
      || [{ key: 'ops/chronicle', room: '' }];
    const jobs = [
      getPeople().then((p) => ({ k: 'people', v: p })).catch(() => ({ k: 'people', e: true })),
      getTasks().then((t) => ({ k: 'tasks', v: t })).catch(() => ({ k: 'tasks', e: true })),
      getCapacity(true).then((c) => ({ k: 'cap', v: c })).catch(() => ({ k: 'cap', e: true })),
      ...sources.map((s, i) =>
        getDoc(s.key).then((d) => ({ k: 'chron', i, v: d, room: s.room }))
          .catch(() => ({ k: 'chron', i, e: true, lobby: i === 0 }))),
      getAchievements().then((a) => ({ k: 'achieve', v: a })).catch(() => ({ k: 'achieve', e: true })),
      getVisitorOn().then((v) => ({ k: 'visit', v: v })).catch(() => ({ k: 'visit', e: true })),
      getBudget().then((b) => ({ k: 'budget', v: b })).catch(() => ({ k: 'budget', e: true })),
    ];
    const settled = await Promise.all(jobs);
    if (tok !== this.token) return; // 离开页签后的迟到回包丢弃
    const src = {};
    src.chron = [];
    for (const s of settled) {
      if (s.k === 'chron') { src.chron.push(s); continue; }
      src[s.k] = s.e ? { error: true } : { v: s.v };
    }
    const nowSec = Math.floor(Date.now() / 1000);
    const day = dayStart();

    // ① 人在哪（访客档：只有访客计数，无四态明细——DOM 断言类名 absent）
    if (visitor) {
      this.data.p1 = null;
      this.paint('p1', this.q1Visitor(src.visit));
    } else {
      const p = this.q1Data(src.people, src.visit);
      this.data.p1 = p;
      this.paint('p1', this.q1Html(p));
    }

    // ② 活在干（访客档：不出任务流水）
    if (visitor) {
      this.data.p2 = null;
      this.paint('p2', this.q2Visitor(src.tasks, day));
    } else {
      const p = this.q2Data(src.tasks, day, nowSec);
      this.data.p2 = p;
      this.paint('p2', this.q2Html(p));
    }

    // ③ 池有多深（访客档同出：水位数字＋色签）
    const p3 = this.q3Data(src.cap);
    this.data.p3 = p3;
    this.paint('p3', this.q3Html(p3, visitor));

    // ④ 今天值得看（访客档：成就三枚可出，编年史不出）
    const p4 = this.q4Data(src.chron, src.achieve, day);
    this.data.p4 = p4;
    this.paint('p4', this.q4Html(p4, visitor));

    // ⑤ 耗粮多少（r_26 成本象限；访客档整段不出数字——成本是钱面）
    const p5 = this.q5Data(src.budget);
    this.data.p5 = p5;
    this.paint('p5', visitor ? this.q5Visitor() : this.q5Html(p5));
  }

  // ── 壳与 paint 门槛 ──────────────────────────────────────────

  paintShell() {
    if (!this.root) return; // load() 首拍先于 root 存在——壳留给下一拍
    // 壳已立「且还挂在活树上」才跳过：黑板各页签共用同一 pane，切走
    // 页签后 innerHTML 被别的面板整面重写，缓存的象限元素全数脱树——
    // 此时必须重建壳重取引用，否则数据照拉照写全落进脱树旧节点，重进
    // 驾驶舱就是「点了没反应」（面板停在上一页签的内容）。isConnected
    // !== false 兼容 testdom（El 假节点无此位，照旧靠缓存判不重建）。
    if (this.els && this.els.p1 && this.els.p1.isConnected !== false) return;
    this.els = {};
    this.lastPaint = {};
    this.root.innerHTML =
      '<div class="cockpit">' +
        '<div class="cockpit-head"><span class="dim small">' + esc(t('只读总览 · 管理请走各入口')) + '</span>' +
          '<span class="dim small" data-cockpit-ts></span></div>' +
        '<div class="cockpit-grid">' +
          '<section class="cockpit-q" id="ck-p1"><div class="sk-line"></div></section>' +
          '<section class="cockpit-q" id="ck-p2"><div class="sk-line"></div></section>' +
          '<section class="cockpit-q" id="ck-p3"><div class="sk-line"></div></section>' +
          '<section class="cockpit-q" id="ck-p4"><div class="sk-line"></div></section>' +
          '<section class="cockpit-q cockpit-q-wide" id="ck-p5"><div class="sk-line"></div></section>' +
        '</div>' +
      '</div>';
    for (const k of ['p1', 'p2', 'p3', 'p4', 'p5']) {
      this.els[k] = this.root.querySelector(`#ck-${k}`);
    }
  }

  /** paint 门槛：HTML 没变不写 innerHTML（防闪屏／防点击被吞）。
   *  象限元素在 paintShell 时取一次活面板引用（testdom 的惰性
   *  querySelector 每调一新节点——按次取会写丢）。 */
  paint(key, html) {
    if (this.lastPaint[key] === html) return;
    this.lastPaint[key] = html;
    const el = this.els && this.els[key];
    if (el) el.innerHTML = html;
  }

  // ── ① 人在哪 ────────────────────────────────────────────────

  q1Data(people, visit) {
    if (people.error) return { error: true };
    const rows = Array.isArray(people.v) ? people.v : [];
    let working = 0, meeting = 0, idleSeats = 0, online = 0;
    for (const p of rows) {
      if (!p.online) continue;
      online++;
      if (p.working) working++;
      else if (p.room && p.room !== 'default') meeting++;
      else idleSeats++;
    }
    return { online, working, meeting, idleSeats,
      visitors: (!visit.error && visit.v && visit.v.on) ? (visit.v.count || 0) : 0 };
  }

  q1Html(p) {
    if (p.error) return this.errHtml(t('名册读取失败'));
    const stat = (n, label, cls) =>
      `<div class="ck-stat"><span class="ck-num${cls ? ` ${cls}` : ''}">${n}</span><span class="ck-lb">${label}</span></div>`;
    return '<h3 class="ck-h">' + esc(t('人在哪')) + '</h3>' +
      `<div class="ck-stats working-detail">` +
        stat(p.online, esc(t('在线')), '') +
        stat(p.working, esc(t('干活中')), 'ck-good') +
        stat(p.meeting, esc(t('在项目房')), '') +
        stat(p.idleSeats, esc(t('Niuma_Studio 歇脚')), '') +
      `</div>` +
      (p.visitors ? `<div class="ck-visitors">👁 ${esc(t('{n} 位访客正在看', { n: p.visitors }))}</div>` : '');
  }

  q1Visitor(visit) {
    const n = (!visit.error && visit.v && visit.v.on) ? (visit.v.count || 0) : 0;
    return '<h3 class="ck-h">' + esc(t('人在哪')) + '</h3>' +
      `<div class="ck-visitors">👁 ${esc(t('{n} 位访客正在看', { n }))}</div>`;
  }

  // ── ② 活在干 ────────────────────────────────────────────────

  q2Data(tasks, day, nowSec) {
    if (tasks.error) return { error: true };
    const rows = Array.isArray(tasks.v) ? tasks.v : [];
    const doing = rows.filter((t) => t.status === 'doing');
    const doneToday = rows.filter((t) =>
      t.status === 'done' && (t.updated_ts || 0) >= day);
    return { doing, doneToday };
  }

  q2Html(p) {
    if (p.error) return this.errHtml(t('台账读取失败'));
    const cards = p.doing.slice(0, 8).map((task) =>
      `<div class="ck-task working-detail">` +
        `<span class="ck-who">${esc(task.assignee || t('未指派'))}</span>` +
        `<span class="ck-title">${esc(task.title)}</span>` +
        `<span class="ck-dur dim small">${esc(task.id)}</span>` +
      `</div>`).join('');
    return '<h3 class="ck-h">' + esc(t('活在干')) + '</h3>' +
      `<div class="ck-done-today">${t('今日已闭环 <strong>{n}</strong> 单', { n: p.doneToday.length })}</div>` +
      (cards || '<div class="ck-empty">' + esc(t('当前没有进行中的任务')) + '</div>');
  }

  q2Visitor(tasks, day) {
    if (tasks.error) return this.errHtml(t('台账读取失败'));
    const rows = Array.isArray(tasks.v) ? tasks.v : [];
    const n = rows.filter((task) => task.status === 'done' && (task.updated_ts || 0) >= day).length;
    return '<h3 class="ck-h">' + esc(t('活在干')) + '</h3>' +
      `<div class="ck-done-today">${t('今日已交付 <strong>{n}</strong> 单', { n })}</div>`;
  }

  // ── ③ 池有多深 ──────────────────────────────────────────────

  q3Data(cap) {
    if (cap.error) return { error: true };
    const rows = (cap.v && cap.v.rooms) || [];
    return { rooms: rows, now: (cap.v && cap.v.now) || 0 };
  }

  q3Html(p, visitor) {
    if (p.error) return this.errHtml(t('水位读取失败'));
    const lobby = p.rooms.find((r) => r.room === 'default') ||
      (p.rooms.length ? p.rooms[0] : null);
    if (!lobby) return '<h3 class="ck-h">' + esc(t('池有多深')) + '</h3><div class="ck-empty">' + esc(t('暂无数据')) + '</div>';
    const lv = waterLevel(lobby.open, lobby.water || 3);
    const row = (r) => {
      const l = waterLevel(r.open, r.water || 3);
      const patrol = r.patrol_ts ? ` <span class="dim small">${esc(t('（采集于 {ts}）', { ts: fmtHM(r.patrol_ts) }))}</span>` : '';
      // r_31 灰字行：另有 N 条暂缓（计数不列清单——「需要关注」区不进）
      const parked = r.parked > 0 ? ` <span class="dim small">${esc(t('另有 {n} 条暂缓', { n: r.parked }))}</span>` : '';
      return `<div class="ck-pool-row">` +
        `<span class="ck-pool-room">${esc(r.room === 'default' ? t('Niuma_Studio') : r.room)}</span>` +
        `<span class="ck-water ck-water-${l.key}">${esc(t('{n} 条 · {label}', { n: r.open, label: l.label }))}</span>` +
        `<span class="ck-sat">${esc(t('饱和 {n}%', { n: r.sat }))}</span>` +
        (visitor ? '' : parked) +
        (visitor ? '' : `<span class="dim small">${esc(t('{a}活/{b}座', { a: r.doing, b: r.seated }))}</span>`) +
        (visitor ? '' : patrol) +
      `</div>`;
    };
    return '<h3 class="ck-h">' + esc(t('池有多深')) + '</h3>' +
      p.rooms.map(row).join('') +
      (visitor ? '' : '<div class="dim small ck-note">' + esc(t('巡报表是全室通报；这里是房主自己的水位面')) + '</div>');
  }

  // ── ④ 今天值得看 ────────────────────────────────────────────

  // q4Data：志源是各房一本的数组（大厅恒在前）。今日条目按源序聚合
  // （大厅先、项目按房序）；非大厅的条目带 room 名供行内标注。错误只
  // 认大厅那本（e && lobby）——项目志未建档是常态不是故障。
  q4Data(chrons, achieve, day) {
    const out = { chronError: false, today: [], medals: [], achieveError: false };
    for (const chron of chrons || []) {
      if (chron.e) {
        if (chron.lobby) out.chronError = true;
        continue;
      }
      const md = (chron.v && chron.v.body) || '';
      const today = fmtDate(day);
      for (const d of parseChronicle(md).dates) {
        if (d.date !== today) continue;
        for (const it of d.items) out.today.push(chron.room ? { ...it, room: chron.room } : it);
      }
    }
    if (achieve.error) out.achieveError = true;
    else {
      const defs = (achieve.v && achieve.v.achievements) || [];
      const unlocked = (achieve.v && achieve.v.unlocked) || {};
      const won = [];
      for (const d of defs) {
        let best = 0;
        for (const k of Object.keys(unlocked)) {
          if (k === d.key || k.startsWith(d.key + '@')) best = Math.max(best, unlocked[k]);
        }
        if (best) won.push({ ...d, ts: best });
      }
      won.sort((a, b) => b.ts - a.ts);
      out.medals = won.slice(0, 3);
    }
    return out;
  }

  q4Html(p, visitor) {
    let html = '<h3 class="ck-h">' + esc(t('今天值得看')) + '</h3>';
    if (!visitor) {
      if (p.chronError) html += '<div class="ck-empty">' + esc(t('编年史读取失败')) + '</div>';
      else if (!p.today.length) html += '<div class="ck-empty">' + esc(t('今天还没有编年史条目')) + '</div>';
      else {
        html += p.today.slice(0, 5).map((it) =>
          `<div class="ck-chron"><span class="ck-who">${esc(it.who)}</span>` +
          (it.room ? `<span class="dim small">${esc(it.room)} ·</span>` : '') +
          `<span>${esc(it.text)}</span></div>`).join('');
      }
    }
    if (p.achieveError) html += '<div class="ck-empty">' + esc(t('成就读取失败')) + '</div>';
    else if (!p.medals.length) html += '<div class="ck-empty">' + esc(t('还没有解锁的奖章')) + '</div>';
    else {
      html += '<div class="ck-medals">' + p.medals.map((m) =>
        `<div class="ck-medal">` +
          `<span class="ach-medal" style="background:${MEDAL_PAL[m.medal ?? 0][0]};border-color:${MEDAL_PAL[m.medal ?? 0][1]}"></span>` +
          `<span class="ck-who">${esc(m.name)}</span>` +
          `<span class="dim small">${fmtDate(m.ts)}</span>` +
        `</div>`).join('') + '</div>';
    }
    return html;
  }

  // ── ⑤ 耗粮多少（r_26 成本象限）──────────────────────────────

  // q5Data 容错读数：缺字段/怪形状一律折成 0/不限（/budget 的新形状
  // 还没上服务端时，象限显示「未设预算」而不是 NaN）。
  q5Data(budget) {
    if (budget.error) return { error: true };
    const b = budget.v || {};
    const num = (v) => (Number.isFinite(Number(v)) ? Number(v) : 0);
    return {
      day: num(b.day_used), dayLimit: num(b.day_tokens),
      week: num(b.week_used), weekLimit: num(b.week_tokens),
      trippedDay: !!b.tripped_day, trippedWeek: !!b.tripped_week,
      usageError: !!b.usage_error, breakerLive: !!b.breaker_live,
    };
  }

  q5Html(p) {
    if (p.error) return this.errHtml(t('预算读取失败'));
    // 一行一窗：今日/本周，人话数字＋三档色签＋百分比（水位同形）
    const row = (label, used, limit) => {
      const lv = budgetLevel(used, limit);
      const spent = limit > 0
        ? `${esc(fmtTokens(used))} / ${esc(fmtTokens(limit))}`
        : esc(fmtTokens(used));
      return `<div class="ck-pool-row">` +
        `<span class="ck-pool-room">${esc(label)}</span>` +
        `<span class="ck-water ck-water-${lv.key}">${spent} · ${esc(lv.label)}</span>` +
        (limit > 0 ? `<span class="ck-sat">${lv.pct}%</span>` : '<span class="ck-sat">' + esc(t('未设上限')) + '</span>') +
      `</div>`;
    };
    let html = '<h3 class="ck-h">' + esc(t('耗粮多少')) + '</h3>';
    if (p.usageError) html += '<div class="ck-empty">' + esc(t('耗粮台账暂不可读')) + '</div>';
    html += row(t('今日'), p.day, p.dayLimit) + row(t('本周'), p.week, p.weekLimit);
    // 熔断横幅只在台账读数可信时出：usage_error 时 tripped 位是服务端的
    // 「旋钮已设」种子值（budget.go），当真渲染就是误报熔断——只剩降级行
    if (!p.usageError && (p.trippedDay || p.trippedWeek)) {
      html += '<div class="ck-budget-trip">⚠ ' + esc(t('预算熔断：自动推进已关（补货不受影响；窗口重置或调预算后可重开）')) + '</div>';
    } else if (p.dayLimit <= 0 && p.weekLimit <= 0) {
      html += '<div class="dim small ck-note">' + esc(t('未设预算——到额不熔断；设置卡·智能 可设日/周上限（熔断只关推进）')) + '</div>';
    } else if (!p.breakerLive) {
      html += '<div class="dim small ck-note">' + esc(t('自动驾驶引擎未在跑——预算只记账不熔断')) + '</div>';
    }
    return html;
  }

  // 访客档占位：成本是钱面，出门即账本——不出现任何耗粮数字。
  q5Visitor() {
    return '<h3 class="ck-h">' + esc(t('耗粮多少')) + '</h3><div class="ck-empty">' + esc(t('成本面仅房主可见')) + '</div>';
  }

  errHtml(msg) {
    // t_206 验收偏差裁定移交：重试钮接线（click 委托挂 paint 后——
    // paint 门槛重写 innerHTML 会吞 onclick 属性绑法，故走事件委托）
    return `<div class="ck-empty">${esc(msg)}` +
      `<button type="button" class="btn small ck-retry" data-ck-retry>${esc(t('重试'))}</button></div>`;
  }

  /** 错误象限的重试：整面重拉（t_206 移交一行）。委托挂一次——面板
   *  反复进出 load() 时监听不叠加（pane 是 KbBoard 的同一元素）。 */
  bindRetry(root) {
    if (this.retryBound) return;
    this.retryBound = true;
    root.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-ck-retry]')) this.refresh();
    });
  }
}

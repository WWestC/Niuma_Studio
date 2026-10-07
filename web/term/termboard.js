// termboard.js — 终端总览（r_17）：把每头牛马的 ZCode 会话当一台终端看——
// 注入进去的每一行（input）、吐出来的每一次思考/草稿/工具/回合（与
// 工作过程同源，但不裁不折帽）、反问与应答（ask/answer）、系统事件
// （sys），按时间排成一条等宽滚动流。左栏成员列表（含干活中戳），
// 主区两种形态：「总览」＝全房按 seq 归并的合流大屏（term 的 seq 是
// 房间级单调序，跨成员归并即真实时间序）、「聚焦」＝单人一册。
//
// 数据真源是 RoomBook 的 room.term（rooms.js 的 foldTerm 按 seq
// upsert，与工作过程同一套语义）；本视图不存连接、不存第二份条目——
// 开着就 book.termOf/termAllOf 读、term hint 到就（rAF 合帧）重画，
// 冷窗水合走 GET /p/{key}/term，整册下载走 /p/{key}/term/file。
// 渲染纪律：注入与思考按终端原样 esc 直出（会话看到什么就是什么）；
// 面向人的产出（回合回复、工具产物）走 markdown 渲染、工具入参里的
// 本地图片经窄端点直出（traceview 同款）；长文默认折叠成摘要行，
// <details> 点开；贴底自动跟随、上翻脱钉、「回到最新」浮标。
//
// 重画是增量账（性能的关键）：直播帧可每秒数十发，全量 innerHTML 重建
// 上千条（每条可能跑 markdown）就是卡顿的根源——常态帧只做尾部追加＋
// 同 seq 回填的单卡置换（<details> 展开态保留）；窗口滑动（超帽后头部
// 整段后移）认得出就按差数从头上修（修剪补偿 scrollTop，上翻的人原地
// 不动）；换房/换聚焦/水合插史/空态才全量重建。

import { getRoomTerm, termFileURL, traceFileURL } from '../wire/api.js';
import { esc, fmtTime, avatarText, memberColor, safeColor } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from '../chat/markdown.js';

// 工具名 → 摘要字段优先级（一张卡的「一行话说清在干什么」，traceview
// 同款词汇）。
const TOOL_SUMMARY_KEYS = [
  ['command', 'cmd'], ['file_path', 'path'], ['path', 'path'],
  ['pattern', 'pattern'], ['query', 'query'], ['url', 'url'],
  ['prompt', 'prompt'], ['description', 'desc'], ['name', 'name'],
];

// 本地图片扩展（工具入参里的 file_path 命中即画 <img>，traceview 同款）。
const IMG_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;

// 渲染帽：单窗最多画这么多条（更早的换下载），与摘要折叠的行长。
const RENDER_CAP = 1500;
const PREVIEW_LEN = 120;

/**
 * 一条转录条目的渲染入口（纯函数，测试面）。
 * @param {import('../wire/wire.js').TraceEntry} e
 * @param {Object} o
 * @param {boolean} [o.who] 总览态：条目前带成员名签
 * @param {string} [o.color] 成员名签颜色（总览态）
 * @param {string} [o.key] 房间 key（工具入参图片的窄端点）
 * @returns {string} HTML
 */
export function entryHTML(e, o = {}) {
  if (!e || !e.kind) return '';
  const who = o.who && e.from
    ? `<span class="tl-who" style="color:${safeColor(o.color || memberColor(e.from))}">${esc(e.from)}</span>`
    : '';
  const ag = e.agent ? `<span class="tl-ag">${esc(t('子'))}</span>` : '';
  switch (e.kind) {
    case 'input': {
      const src = e.src ? t('来自 {src}', { src: e.src }) : t('注入');
      const text = e.text || '';
      if (text.length > 800) {
        return `<div class="tl-in">${who}<details><summary>▸ ${esc(src)} · ${t('{n} 字', { n: text.length })}</summary>` +
          `<pre>${esc(text)}</pre></details></div>`;
      }
      return `<div class="tl-in">${who}<div class="tl-in-line"><span class="tl-k">▸ ${esc(src)}</span></div><pre>${esc(text)}</pre></div>`;
    }
    case 'sys':
      return `<div class="tl-sys">${who}<span class="tl-k">·</span> ${esc(e.text || '')}</div>`;
    case 'ask':
      return `<div class="tl-ask">${who}<span class="tl-k">?</span> ${esc(e.tool ? t('权限：{tool}', { tool: e.tool }) : t('提问'))}` +
        `<pre class="tl-clip">${esc(preview(e.text || ''))}</pre></div>`;
    case 'answer':
      return `<div class="tl-anw${e.ok === false ? ' bad' : ''}">${who}` +
        `<span class="tl-k">${e.ok === false ? '✕' : '✓'}</span> ${esc(e.text || '')}</div>`;
    case 'think':
      return `<div class="tl-think">${who}<details><summary><span class="tl-k">${esc(t('思'))}</span>` +
        `${esc(preview(e.text || ''))}</summary><pre>${esc(e.text || '')}</pre></details></div>`;
    case 'draft':
      return `<div class="tl-draft">${who}<span class="tl-k">${esc(t('稿'))}</span> ${esc(preview(e.text || ''))}</div>`;
    case 'model':
      return `<div class="tl-model">${who}<span class="tl-k">⟳</span> ${esc(e.model || t('模型'))} · ${t('第 {n} 轮', { n: e.iter || 1 })}</div>`;
    case 'error':
      return `<div class="tl-err">${who}<span class="tl-k">!</span> ${esc(e.text || t('出错了'))}</div>`;
    case 'tool': {
      const sum = toolSummary(e);
      const state = e.state === 'done' && e.ok === false ? 'err'
        : e.state === 'done' ? 'ok' : e.state === 'error' ? 'err' : e.state || '';
      const ms = e.ms ? ` · ${(e.ms / 1000).toFixed(1)}s` : '';
      const input = (e.input && typeof e.input === 'object' && !Array.isArray(e.input)) ? e.input : null;
      const img = o.key && input ? imageInput(o.key, input) : '';
      const body = (input != null || e.output)
        ? `<details><summary>${esc(sum)}<span class="tl-st ${state}">${state}</span>${ms}</summary>` +
          (img ? `<img class="tl-img" src="${esc(img)}" loading="lazy" alt="${esc(t('工具读取的图片'))}">` : '') +
          `${input != null ? `<div class="tl-io-t">${esc(t('入参'))}</div><pre>${esc(fmtInput(input))}</pre>` : ''}` +
          `${e.output ? `<div class="tl-io-t">${esc(t('产物'))}</div><div class="tl-md">${markdown(e.output)}</div>` : ''}</details>`
        : `<div class="tl-line">${esc(sum)}<span class="tl-st ${state}">${state}</span>${ms}</div>`;
      return `<div class="tl-tool">${who}${ag}${body}</div>`;
    }
    case 'turn':
      if (e.state === 'start') {
        return `<div class="tl-turn">${who}<span class="tl-k">—</span> ${esc(t('回合开始'))} <span class="tl-t">${esc(fmtTime(e.ts))}</span></div>`;
      }
      return `<div class="tl-turn">${who}<span class="tl-k">—</span> ${esc(t('回合结束'))}` +
        `${e.stop ? ` · <span class="tl-st ${/^(stop|success|success_with|)$/.test(e.stop) ? 'ok' : 'err'}">${esc(e.stop)}</span>` : ''}` +
        `${e.ms ? ` · ${(e.ms / 1000).toFixed(1)}s` : ''} <span class="tl-t">${esc(fmtTime(e.ts))}</span></div>` +
        (e.text ? `<div class="tl-reply">${who}<span class="tl-k">${esc(t('答'))}</span><div class="tl-md">${markdown(e.text)}</div></div>` : '');
    default:
      return '';
  }
}

/** 长文的摘要行（渲染帽内直出，超帽保头留尾）。 */
function preview(s) {
  if (!s) return '';
  if (s.length <= PREVIEW_LEN) return s;
  return t('{head} …（共 {n} 字，展开见全文）', { head: s.slice(0, PREVIEW_LEN), n: s.length });
}

/** 工具卡的摘要字段（traceview 同款优先级）。 */
function toolSummary(e) {
  const name = e.tool || 'tool';
  if (e.input && typeof e.input === 'object') {
    for (const [k] of TOOL_SUMMARY_KEYS) {
      const v = e.input[k];
      if (typeof v === 'string' && v) return `${name} ${v.slice(0, 80)}`;
    }
  }
  return name;
}

/** 工具入参里的本地图片：file_path/path/image_path 命中图片扩展名 → 窄端点 URL（traceview 同款）。 */
function imageInput(key, input) {
  for (const k of ['file_path', 'path', 'image_path']) {
    const v = input[k];
    if (typeof v === 'string' && IMG_RE.test(v)) return traceFileURL(key, v);
  }
  return null;
}

/** 入参渲染：对象走 JSON，字符串直出。 */
function fmtInput(v) {
  if (typeof v === 'string') return v;
  try { return JSON.stringify(v, null, 2); } catch { return String(v); }
}

/**
 * 终端总览板（#/term）。构造后由 app.js 路由驱动 revealed/conceal，
 * term hint 进 onSync。
 */
export class TermBoard {
  /**
   * @param {HTMLElement} el #board-term 容器
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {Object} [opts]
   * @param {(text: string, kind?: string) => void} [opts.toast]
   */
  constructor(el, book, opts = {}) {
    this.el = el;
    this.book = book;
    this.opts = opts;
    this.key = null;      // 打开中的房间 key（revealed 时钉住）
    this.focus = '';      // '' = 总览；否则成员名
    this.rafPending = false;
    this.el.innerHTML =
      '<div class="tl-rail" role="navigation" aria-label="' + esc(t('成员列表')) + '"></div>' +
      '<div class="tl-main">' +
        '<div class="tl-head"></div>' +
        '<div class="tl-stream" role="log" aria-label="' + esc(t('终端流')) + '"></div>' +
        '<div class="tl-follow" hidden><button type="button" class="btn small">' + esc(t('回到最新 ↓')) + '</button></div>' +
      '</div>';
    this.rail = this.el.querySelector('.tl-rail');
    this.head = this.el.querySelector('.tl-head');
    this.stream = this.el.querySelector('.tl-stream');
    // 增量渲染账：keys[] 是已画条目的 seq 序（与 DOM 同序），nodes/objs
    // 按 seq 取回节点组与条目对象——回填比对靠对象身份，置换靠节点组。
    this.rs = null;
    // 渲染帽提示条：持久节点改字即可，不参与每次重建。
    this.capNote = document.createElement('div');
    this.capNote.className = 'tl-cap';
    this.capNote.hidden = true;
    this.stream.appendChild(this.capNote);
    this.followBtn = this.el.querySelector('.tl-follow button');
    this.stream.addEventListener('scroll', () => this.#tickFollow());
    this.followBtn.addEventListener('click', () => this.#scrollBottom(true));
    this.rail.addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-tl-name]');
      if (!btn) return;
      this.focus = btn.dataset.tlName || '';
      this.hydrate();
    });
    this.head.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-tl-refresh]')) this.hydrate();
    });
  }

  /** 路由进场：钉住当前房、水合、首画。 */
  revealed() {
    this.visible = true;
    this.key = this.book.current;
    this.hydrate();
  }

  /** 路由离场：只解钉（数据照收，回来不重建）。 */
  conceal() {
    this.visible = false;
    this.key = null;
  }

  /** 换房（人在本板上切房）：可见才跟着走。 */
  onSwitch() {
    if (this.visible) this.revealed();
  }

  /** app.js 的 hint 落点：当前房有 term 帧就（合帧）重画。 */
  onSync(key, hint) {
    if (!this.key || key !== this.key) return;
    if (hint === 'term') this.#repaint();
    // members 帧（member_work 的 working 翻转随行）也要刷左栏的干活
    // 圆点——只刷头会让点子亮/灭拖到下一张 term 卡（v2.10 同步收口）。
    if (hint === 'members') this.#repaint();
  }

  /** 冷窗水合（也覆盖聚焦切换）：拉快照 fold 进 book 再画。 */
  async hydrate() {
    if (!this.key) this.key = this.book.current;
    const key = this.key;
    try {
      const body = await getRoomTerm(key, this.focus || undefined, 600);
      for (const row of body?.members || []) {
        this.book.foldTerm(key, row?.entries || []);
      }
    } catch { /* 快照不可用：直播帧照流，头上一句如实话 */ }
    if (this.key === key) this.#paint();
  }

  // --- 渲染 -----------------------------------------------------------

  #repaint() {
    if (this.rafPending) return;
    this.rafPending = true;
    requestAnimationFrame(() => {
      this.rafPending = false;
      if (this.key) this.#paint();
    });
  }

  #pinned() {
    const sh = this.stream.scrollHeight, st = this.stream.scrollTop, ch = this.stream.clientHeight;
    return sh - st - ch < 48;
  }
  #scrollBottom(smooth) {
    if (smooth) this.stream.scrollTo({ top: this.stream.scrollHeight, behavior: 'smooth' });
    else this.stream.scrollTop = this.stream.scrollHeight;
  }
  #tickFollow() {
    const f = this.followBtn.parentElement;
    if (f) f.hidden = this.#pinned();
  }

  /**
   * 画一帧：优先增量（尾部追加＋同 seq 回填只换那张卡），上下文换了
   * （换房/换聚焦/水合插史/空态翻转）才全量重建。
   */
  #paint() {
    this.#paintRail();
    this.#paintHead();
    const entries = this.focus ? this.book.termOf(this.key, this.focus)
      : this.book.termAllOf(this.key);
    const rs = this.rs;
    const sameCtx = !!(rs && rs.key === this.key && rs.focus === this.focus);
    const pinned = this.#pinned();
    if (sameCtx && this.#syncEntries(entries, pinned)) {
      if (pinned) this.#scrollBottom(false);
    } else {
      this.#paintAll(entries, pinned);
    }
    this.#tickFollow();
  }

  /** 全量重建（换房/换聚焦/水合插史/空态）：一次付清窗口内全部渲染。 */
  #paintAll(entries, pinned) {
    const sc = this.stream.scrollTop;
    this.stream.innerHTML = '';
    this.stream.appendChild(this.capNote);
    const shown = entries.length > RENDER_CAP ? entries.slice(-RENDER_CAP) : entries;
    const rs = { key: this.key, focus: this.focus, keys: [], nodes: new Map(), objs: new Map() };
    this.rs = rs;
    if (!shown.length) {
      this.stream.insertAdjacentHTML('beforeend',
        '<div class="tl-empty">' + t('还没有终端记录——这间房的牛马被点名干活后，<br>注入的指令与全部产出都会逐行落在这里。') + '</div>');
    } else {
      const frag = document.createDocumentFragment();
      for (const e of shown) this.#grow(frag, e, rs);
      this.stream.appendChild(frag);
    }
    this.#paintNote(entries);
    if (pinned) this.#scrollBottom(false);
    else this.stream.scrollTop = sc;
  }

  /**
   * 增量同步：返回 false = 对不上账（水合把历史插进了头部、册被裁剪、
   * 清空），调用方退全量。对齐在全量 entries 上做（先切窗会让大批量帧
   * 把旧窗头切出窗外、退化成帧帧全量）。步骤：
   *   ① 对齐——旧窗首 seq 在 entries 里找位（base）：找不到或对不上＝
   *      断裂；base>0 且无尾部新增＝纯头部插史（水合），也交全量；
   *   ② 回填——对齐区内同 seq 换了对象（服务端冲水带携原 seq 全量字段：
   *      工具卡落终态、回合落全文）按对象身份判定，只换那一张卡，展开的
   *      <details> 原样保留；
   *   ③ 追加——尾部新增整段进 DOM；
   *   ④ 修剪——DOM 超帽从头上修差数（窗口滑动的常态步），补偿 scrollTop
   *      （.tl-stream 已关滚动锚定，补偿即精确）。
   */
  #syncEntries(entries, pinned) {
    const rs = this.rs;
    let base = 0;
    if (rs.keys.length) {
      base = entries.findIndex((e) => e.seq === rs.keys[0]);
      if (base < 0) return false;
      for (let i = 0; i < rs.keys.length; i++) {
        if (entries[base + i]?.seq !== rs.keys[i]) return false;
      }
      // 头部插史（水合）：插进的史只要有任何一段还落在渲染窗内（窗＝
      // entries 的最后 CAP 条），增量账就会漏画那一段——交全量一次付清；
      // 史完全出窗（窗已满且被推得更远）才走增量滑窗。
      const tailCount = entries.length - base - rs.keys.length;
      if (base > 0 && rs.keys.length + tailCount < RENDER_CAP) return false;
    }
    if (!rs.keys.length) {
      // 空账起笔：清掉全量态留下的空态占位行，条目从帽提示条后排。
      this.stream.innerHTML = '';
      this.stream.appendChild(this.capNote);
    }
    for (let i = 0; i < rs.keys.length; i++) {
      const e = entries[base + i];
      if (rs.objs.get(e.seq) === e) continue;
      const old = rs.nodes.get(e.seq);
      const wasOpen = old.map((n) => [...n.querySelectorAll('details')].map((d) => d.open));
      const nodes = this.#nodes(e);
      old[0].before(...nodes);
      old.forEach((n) => n.remove());
      const ds = nodes.flatMap((n) => [...n.querySelectorAll('details')]);
      wasOpen.flat().forEach((o, i) => { if (o && ds[i]) ds[i].open = true; });
      rs.nodes.set(e.seq, nodes);
      rs.objs.set(e.seq, e);
    }
    if (entries.length > base + rs.keys.length) {
      const frag = document.createDocumentFragment();
      for (let i = base + rs.keys.length; i < entries.length; i++) this.#grow(frag, entries[i], rs);
      this.stream.appendChild(frag);
    }
    if (rs.keys.length > RENDER_CAP) {
      const drop = rs.keys.length - RENDER_CAP;
      let h = 0;
      const dropping = [];
      for (let i = 0; i < drop; i++) {
        const nodes = rs.nodes.get(rs.keys[i]);
        dropping.push([rs.keys[i], nodes]);
        for (const n of nodes) h += n.offsetHeight;
      }
      for (const [, nodes] of dropping) nodes.forEach((n) => n.remove());
      for (const [k] of dropping) { rs.nodes.delete(k); rs.objs.delete(k); }
      rs.keys.splice(0, drop);
      if (!pinned && h) this.stream.scrollTop -= h;
    }
    this.#paintNote(entries);
    return true;
  }

  /** 一条条目建节点（entryHTML 可能多根——回合分隔线＋回复卡）。 */
  #nodes(e) {
    const tpl = document.createElement('template');
    tpl.innerHTML = entryHTML(e, { who: !this.focus, key: this.key });
    return [...tpl.content.children];
  }

  #grow(frag, e, rs) {
    const nodes = this.#nodes(e);
    nodes.forEach((n) => frag.appendChild(n));
    rs.keys.push(e.seq);
    rs.nodes.set(e.seq, nodes);
    rs.objs.set(e.seq, e);
  }

  /** 渲染帽提示条（持久节点改字，不重建）。 */
  #paintNote(entries) {
    const over = entries.length > RENDER_CAP;
    this.capNote.hidden = !over;
    if (over) this.capNote.textContent =
      t('仅显示最近 {shown} 条（共 {total}）——更早的记录用右上角「下载」取整册', { shown: this.rs.keys.length, total: entries.length });
  }

  #paintRail() {
    const room = this.book.rooms.get(this.key);
    if (!room) return;
    const names = new Set([...room.term.keys(), ...(room.members ? room.members.keys() : [])]);
    const rows = ['<button type="button" class="tl-row all' + (this.focus === '' ? ' cur' : '') + '" data-tl-name="">' + esc(t('总览')) + '</button>'];
    for (const name of names) {
      if (!name) continue;
      const m = room.members?.get(name);
      const color = safeColor((m && m.color) || memberColor(name));
      const dot = m?.working ? '<i class="tl-dot on"></i>' : '';
      const cur = this.focus === name ? ' cur' : '';
      rows.push(`<button type="button" class="tl-row${cur}" data-tl-name="${esc(name)}">` +
        `<span class="pavatar sm" style="background:${color}">${esc(avatarText(name))}</span>` +
        `<span class="tl-name">${esc(name)}</span>${dot}</button>`);
    }
    this.rail.innerHTML = rows.join('');
  }

  #paintHead() {
    const room = this.book.rooms.get(this.key);
    const title = this.focus ? t('{name} 的终端', { name: this.focus }) : t('全房终端 · 总览');
    const dl = this.focus
      ? `<a class="btn small" href="${termFileURL(this.key, this.focus)}" download>${esc(t('下载整册'))}</a>`
      : '';
    this.head.innerHTML =
      `<strong>${esc(title)}</strong>` +
      `<span class="tl-head-sub">${esc((room && room.name) || this.key || '')}</span>` +
      `<button type="button" class="btn small" data-tl-refresh title="${esc(t('重拉快照'))}">${esc(t('刷新'))}</button>${dl}`;
  }
}

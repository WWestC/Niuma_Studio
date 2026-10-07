// traceview.js — 工作过程抽屉（v2.8）：把牛马 ZCode 会话里原先藏在后台
// 的思考流、回复草稿、工具调用与结果、模型轮次、子智能体流，按 ZCode
// 的语言摊开成一条时间线——思考块可折叠（干活中的最新块自动展开）、
// 工具卡一条摘要线＋可展开的入参/产物（markdown 渲染、本地图片直出）、
// 子智能体缩进成块、回合分隔线带最终回复。右侧 sheet 抽屉（taskdrawer
// 同款骨架），直播跟随：trace 帧到即增量重画（rAF 合帧），贴底时自动
// 跟随、上翻脱钉后不拽人（「回到最新」浮标）。
//
// v2.9 统一：回合的输入侧（注入行）与生命周期注记（sys/ask/answer）与
// 输出流同环——「干活中」亮起时打开抽屉就有本回合的注入；悬置（超时
// 弃单、等权限、回收、长回合）在时间线上自己会说话。空态因此只属于
// 真没干过活的成员。
//
// 数据真源是 RoomBook 的 room.trace（rooms.js 的 foldTrace 按 seq
// upsert）；本视图不存连接、不存第二份条目——开着就 book.traceOf 读、
// trace hint 到就重画。

import { getRoomTrace, traceFileURL } from '../wire/api.js';
import { esc, icon, dismiss, fmtTime, avatarText, memberColor, safeColor } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from './markdown.js';

// 工具名 → 摘要字段优先级（一张卡的「一行话说清在干什么」）。
const TOOL_SUMMARY_KEYS = [
  ['command', 'cmd'], ['file_path', 'path'], ['path', 'path'],
  ['pattern', 'pattern'], ['query', 'query'], ['url', 'url'],
  ['prompt', 'prompt'], ['description', 'desc'], ['name', 'name'],
];

// 本地图片扩展（工具入参里的 file_path 命中即画 <img>）。
const IMG_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;

export class TracePanel {
  /**
   * @param {import('../chat/rooms.js').RoomBook} book
   * @param {Object} opts
   * @param {(name: string) => (null|{name: string, role?: string, color?: string})} [opts.person]
   *   名册真相（头像色/岗位）——app.js 的 personOf
   * @param {(text: string, kind?: string) => void} [opts.toast]
   */
  constructor(book, opts = {}) {
    this.book = book;
    this.opts = opts;
    this.el = null;      // .sheet-wrap
    this.sheet = null;   // .sheet.tv-sheet
    this.key = null;     // 打开中的房间 key
    this.name = null;    // 打开中的成员名
    this.userOpen = new Map(); // seq -> boolean：用户手动的折叠态（覆盖自动规则）
    this.rafPending = false;
    this.tickTimer = null;
    this.onDocKey = (ev) => { if (ev.key === 'Escape') this.close(); };
  }

  /** 开抽屉：先折快照（冷窗水合），再首画。 */
  open(key, name) {
    if (!key || !name) return;
    this.close();
    this.key = key;
    this.name = name;
    this.userOpen.clear();
    const el = document.createElement('div');
    el.className = 'sheet-wrap';
    el.innerHTML = `<div class="sheet tv-sheet" role="dialog" aria-label="${t('工作过程')}"></div>`;
    document.body.appendChild(el);
    this.el = el;
    this.sheet = el.querySelector('.sheet');
    this.#paint(t('加载中…'));
    el.addEventListener('click', (ev) => {
      if (ev.target === el) { this.close(); return; }
      const close = ev.target.closest('[data-tv-close]');
      if (close) { this.close(); return; }
      const head = ev.target.closest('[data-tv-toggle]');
      if (head) {
        this.#toggle(+head.dataset.tvToggle, head.closest('.tv-think, .tv-tool'));
        return;
      }
      const more = ev.target.closest('[data-tv-out]');
      if (more) {
        more.closest('.tv-tool-body')?.querySelector('.tv-out')?.classList.toggle('full');
        more.remove();
        return;
      }
      const jump = ev.target.closest('[data-tv-follow]');
      if (jump) { this.#scrollBottom(true); return; }
      const refresh = ev.target.closest('[data-tv-refresh]');
      if (refresh) { this.#hydrate(); return; }
    });
    document.addEventListener('keydown', this.onDocKey);
    this.sheet.addEventListener('scroll', () => this.#tickFollow());
    this.tickTimer = setInterval(() => this.#tickHead(), 1000);
    this.#hydrate();
  }

  close() {
    if (!this.el) return;
    dismiss(this.el);
    this.el = this.sheet = null;
    this.key = this.name = null;
    this.userOpen.clear();
    if (this.tickTimer) { clearInterval(this.tickTimer); this.tickTimer = null; }
    document.removeEventListener('keydown', this.onDocKey);
  }

  /** app.js 的 hint 落点：开着的那间房有新帧就（合帧）重画。 */
  onSync(key, hint) {
    if (!this.el || hint !== 'trace' || key !== this.key) return;
    if (this.rafPending) return;
    this.rafPending = true;
    requestAnimationFrame(() => {
      this.rafPending = false;
      if (this.el) this.#paint();
    });
  }

  /** 名册工作戳变了（member_work）也该刷头上的状态条。 */
  onMembers(key) {
    if (this.el && key === this.key) this.#tickHead();
  }

  // --- 数据 -----------------------------------------------------------

  async #hydrate() {
    if (!this.el) return;
    try {
      const body = await getRoomTrace(this.key, this.name);
      for (const row of body?.members || []) {
        this.book.foldTrace(this.key, row?.entries || []);
      }
    } catch { /* 快照不可用：直播帧照流，面板顶上一句如实话 */ }
    if (this.el) this.#paint();
  }

  #working() {
    const room = this.book.rooms.get(this.key);
    const m = room?.members.get(this.name);
    return { on: !!(m && m.working), since: (m && m.working_since) || 0 };
  }

  #entries() { return this.book.traceOf(this.key, this.name); }

  // --- 渲染 -----------------------------------------------------------

  #paint(loadingNote) {
    const sc = this.sheet.scrollTop;
    const pinned = this.#pinned();
    const entries = this.#entries();
    const w = this.#working();
    const p = this.opts.person?.(this.name) || {};
    const color = safeColor(p.color || memberColor(this.name));
    const head =
      `<div class="sheet-head tv-head">` +
        `<span class="pavatar tv-avatar" style="background:${color}">${esc(avatarText(this.name))}</span>` +
        `<div class="sheet-title">` +
          `<strong>${esc(this.name)}<span class="tv-sub">${t('的工作过程')}</span></strong>` +
          `<span class="tv-role">${esc(p.role || '')}${p.role ? ' · ' : ''}<span data-tv-state>${this.#stateHTML(w)}</span></span>` +
        `</div>` +
        `<button type="button" class="btn small" data-tv-refresh title="${t('重拉快照')}">${icon('refresh', 16)}</button>` +
        `<button type="button" class="btn small" data-tv-close title="${t('收起')}">${icon('dash', 16)}</button>` +
      `</div>`;
    let body;
    if (!entries.length) {
      // 空态篇：插画（大号弱化图标）＋一句描述，居中。统一后空态只属于
      // 真没干过活的成员——干过活，注入行至少在环里。chip 亮着的空窗
      // （快照竞态/首回合开场行在路上）按实话另说一句（v2.10 收口）。
      const note = loadingNote ||
        (w.on ? t('回合进行中——工作过程正在路上，注入与首段思考马上就到。')
              : t('TA 还没干过活——下一轮点名后，注入、思考、工具调用和子智能体都会出现在这里。'));
      body = `<div class="tv-empty">${icon('robot', 44)}<div>${note}</div></div>`;
    } else {
      body = this.#timelineHTML(entries, w.on);
    }
    this.sheet.innerHTML = head + body;
    // 回到最新浮标挂 wrap（不随内容滚动）
    let follow = this.el.querySelector('.tv-follow');
    if (!follow) {
      follow = document.createElement('div');
      follow.className = 'tv-follow';
      follow.dataset.tvFollow = '1';
      follow.innerHTML = `${icon('down', 14)} ${t('回到最新')}`;
      this.el.appendChild(follow);
    }
    if (pinned) this.#scrollBottom();
    else this.sheet.scrollTop = sc;
    this.#tickFollow();
  }

  #stateHTML(w) {
    if (!w.on) return `<i class="tv-dot off"></i>${t('空闲')}`;
    const el = Math.max(0, Math.round(Date.now() / 1000 - (w.since || Date.now() / 1000)));
    return `<i class="tv-dot on"></i>${t('干活中')} · ${t('已 {dur}', { dur: esc(fmtDur(el)) })}`;
  }

  #tickHead() {
    if (!this.el) return;
    const st = this.sheet.querySelector('[data-tv-state]');
    if (st) st.innerHTML = this.#stateHTML(this.#working());
    this.#tickElapsed();
    this.#tickFollow();
  }

  // 运行态工具卡的已耗时（复用头部那路 1s 滴答）：TaskOutput block 等
  // 待、长 Bash 之类零流式输出的卡不再静默——卡自己报「已跑多久」。
  #tickElapsed() {
    const now = Math.floor(Date.now() / 1000);
    for (const el of this.sheet.querySelectorAll('[data-tv-elapsed]')) {
      const v = elapsedSuffix(+el.dataset.tvElapsed, now);
      if (el.textContent !== v) el.textContent = v;
    }
  }

  #pinned() {
    if (!this.sheet) return true;
    const sh = this.sheet.scrollHeight, st = this.sheet.scrollTop, ch = this.sheet.clientHeight;
    return sh - st - ch < 64; // 阈值盖过抽屉底的留白（pill 的落位区）
  }
  #scrollBottom(force) {
    if (!this.sheet) return;
    if (force) this.sheet.scrollTo({ top: this.sheet.scrollHeight, behavior: 'smooth' });
    else this.sheet.scrollTop = this.sheet.scrollHeight;
  }
  #tickFollow() {
    const f = this.el?.querySelector('.tv-follow');
    if (f) f.classList.toggle('show', !this.#pinned());
  }

  #toggle(seq, container) {
    // 折叠态以「此刻画面的实际态」为准翻转后钉住（自动规则让位给用户手势）
    const wasOpen = !!container?.classList.contains('open');
    this.userOpen.set(seq, !wasOpen);
    const sc = this.sheet.scrollTop;
    this.#paint();
    this.sheet.scrollTop = sc;
  }

  /** 时间线：回合分组（turn:start 开组、turn:done 收口带最终回复）。 */
  #timelineHTML(entries, working) {
    const groups = [];
    let cur = null;
    for (const e of entries) {
      if (e.kind === 'turn' && e.state === 'start') {
        cur = { turn: true, start: e, done: null, items: [] };
        groups.push(cur);
        continue;
      }
      if (e.kind === 'turn' && e.state === 'done') {
        if (cur) cur.done = e;
        else groups.push({ turn: false, start: null, done: e, items: [] });
        cur = null;
        continue;
      }
      if (!cur) { cur = { turn: false, start: null, done: null, items: [] }; groups.push(cur); }
      cur.items.push(e);
    }
    let out = '<div class="tv-turns">';
    let n = 0;
    for (const g of groups) {
      if (g.turn) n++;
      out += this.#groupHTML(g, n, working && g === groups[groups.length - 1]);
    }
    return out + '</div>';
  }

  #groupHTML(g, n, live) {
    let head = '';
    if (g.turn || g.done) {
      const ts = (g.done || g.start)?.ts || 0;
      const dur = g.done?.ms ? ` · ${(g.done.ms / 1000).toFixed(1)}s` : '';
      const bad = g.done && g.done.stop && !/^(stop|success|success_with|)$/.test(g.done.stop)
        ? ` · <span class="tv-bad">${esc(g.done.stop)}</span>` : '';
      head = `<div class="tv-turn-head">${icon('right', 12)} ${t('回合 {n}', { n })}<span class="tv-time">${esc(fmtTime(ts))}${dur}${bad}</span></div>`;
    }
    // 组内条目：done 带全文时隐掉同组草稿（同一句话的流式预览，别念两遍）
    const hasReply = !!(g.done && g.done.text);
    // 干活中的组只自动展开「最后一块思考」（ZCode 式：追最新的，旧的收起）
    let lastThink = 0;
    for (const e of g.items) if (e.kind === 'think') lastThink = e.seq;
    let items = '';
    let agentOpen = false, agentID = '';
    for (const e of g.items) {
      if (e.agent && e.agent_id !== agentID) {
        if (agentOpen) items += '</div>';
        agentID = e.agent_id || '';
        items += `<div class="tv-agent"><div class="tv-agent-tag">${icon('robot', 12)} ${t('子智能体 · {id}', { id: esc(String(agentID).slice(0, 10)) })}</div>`;
        agentOpen = true;
      }
      if (!e.agent && agentOpen) { items += '</div>'; agentOpen = false; agentID = ''; }
      if (e.kind === 'draft' && hasReply) continue;
      items += this.#entryHTML(e, live, lastThink);
    }
    if (agentOpen) items += '</div>';
    let reply = '';
    if (hasReply) {
      reply = `<div class="tv-reply"><div class="tv-reply-tag">${icon('chat', 12)} ${t('回复')}</div>${markdown(g.done.text)}</div>`;
    }
    return `<div class="tv-group${live ? ' live' : ''}">${head}${items}${reply}</div>`;
  }

  #entryHTML(e, live, lastThink) {
    switch (e.kind) {
      case 'think': return this.#thinkHTML(e, live && e.seq === lastThink);
      case 'draft': return `<div class="tv-draft"><span class="tv-k">${t('草稿')}</span>${esc(e.text || '')}</div>`;
      case 'model': return `<div class="tv-model">${icon('bolt', 12)} ${esc(e.model || t('模型'))} · ${t('第 {n} 轮', { n: e.iter || 1 })}</div>`;
      case 'error': return `<div class="tv-error">${icon('bell', 12)} ${esc(e.text || t('出错了'))}</div>`;
      case 'tool': return this.#toolHTML(e, live);
      // 回合生命周期行（v2.9 双门）：「干活中」亮起的那一刻起，抽屉里
      // 就有这轮注入了什么、悬置在哪（超时/等权限/回收/长回合注记）。
      case 'input': return this.#inputHTML(e);
      case 'sys': return `<div class="tv-sys">${esc(e.text || '')}</div>`;
      case 'ask': return `<div class="tv-ask"><span class="tv-k">${t('反问')}</span><span>${esc(e.text || '')}</span></div>`;
      case 'answer': return `<div class="tv-answer${e.ok === false ? ' bad' : ''}"><span class="tv-k">${e.ok === false ? t('应答 ✗') : t('应答 ✓')}</span><span>${esc(e.text || '')}</span></div>`;
      default: return '';
    }
  }

  // 注入行（回合输入侧）：think 同款折叠骨架（toggle 的 closest 选择器
  // 认 .tv-think，复用即得交互）——默认收起，预览 88 字；src 是来处。
  #inputHTML(e) {
    const text = e.text || '';
    const open = this.userOpen.has(e.seq) ? this.userOpen.get(e.seq) : false;
    return `<div class="tv-think tv-input${open ? ' open' : ''}">` +
      `<button type="button" class="tv-think-head" data-tv-toggle="${e.seq}">` +
        `<span class="tv-k">${t('注入')}${e.src ? ' · ' + esc(e.src) : ''}</span>` +
        `<span class="tv-think-prev">${esc(head88(text))}</span>` +
        `<span class="tv-think-caret">${icon('down', 16)}</span>` +
      `</button>` +
      `<div class="tv-think-body" ${open ? '' : 'hidden'}>${esc(text)}</div>` +
      `</div>`;
  }

  #thinkHTML(e, autoOpen) {
    const text = e.text || '';
    const open = this.userOpen.has(e.seq) ? this.userOpen.get(e.seq) : !!autoOpen;
    return `<div class="tv-think${open ? ' open' : ''}">` +
      `<button type="button" class="tv-think-head" data-tv-toggle="${e.seq}">` +
        `<span class="tv-k">${t('思考')}</span>` +
        `<span class="tv-think-prev">${esc(head88(text))}</span>` +
        `<span class="tv-think-caret">${icon('down', 16)}</span>` +
      `</button>` +
      `<div class="tv-think-body" ${open ? '' : 'hidden'}>${esc(text)}</div>` +
      `</div>`;
  }

  #toolHTML(e, live) {
    const input = (e.input && typeof e.input === 'object' && !Array.isArray(e.input)) ? e.input : null;
    const summary = toolSummary(e.tool, input);
    const running = e.state === 'run' || e.state === 'call';
    let state;
    if (running) {
      state = `<span class="tv-chip run"><i class="tv-spin"></i>${e.state === 'call' ? t('准备') : t('运行中')}` +
        `<span class="tv-elapsed" data-tv-elapsed="${e.ts || 0}">${esc(elapsedSuffix(e.ts, Math.floor(Date.now() / 1000)))}</span></span>`;
    } else if (e.state === 'error') {
      state = `<span class="tv-chip bad">${t('失败')}</span>`;
    } else {
      state = `<span class="tv-chip ok">${icon('check', 11)}${e.ms ? ` ${(e.ms / 1000).toFixed(1)}s` : ''}</span>`;
    }
    const open = this.userOpen.has(e.seq) ? this.userOpen.get(e.seq) : !!(live && running);
    let body = '';
    if (open) {
      body = `<div class="tv-tool-body">`;
      if (input) {
        const img = imageInput(this.key, input);
        if (img) body += `<img class="tv-img" src="${esc(img)}" loading="lazy" alt="${t('工具读取的图片')}">`;
        body += `<pre class="tv-pre">${esc(prettyJSON(input))}</pre>`;
      }
      if (e.output) {
        const tall = e.output.length > 1200;
        body += `<div class="tv-out${tall ? ' tall' : ''}">${markdown(e.output)}</div>` +
          (tall ? `<button type="button" class="tv-out-more" data-tv-out>${t('展开全部输出')}</button>` : '');
      }
      body += `</div>`;
    }
    return `<div class="tv-tool${open ? ' open' : ''}${running ? ' live' : ''}">` +
      `<button type="button" class="tv-tool-head" data-tv-toggle="${e.seq}">` +
        `<span class="tv-tool-name">${esc(e.tool || t('工具'))}</span>` +
        `<span class="tv-tool-sum">${esc(summary)}</span>` +
        state +
        `<span class="tv-think-caret">${icon('down', 16)}</span>` +
      `</button>${body}</div>`;
  }
}

// --- 纯渲染帮手 ---------------------------------------------------------

// 时长文案（头部工作戳与工具卡计时共用；导出供冒烟断言——testdom 的
// querySelectorAll 恒空，滴答的 DOM 面在桩世界不可测，纯函数直接验）。
export function fmtDur(s) {
  return s >= 60 ? t('{m} 分 {s} 秒', { m: Math.floor(s / 60), s: s % 60 })
                 : t('{s} 秒', { s });
}

// 运行态卡的已耗时尾注（ts 是该卡启动的秒；空 ts 或时钟未到不显示）。
export function elapsedSuffix(ts, now) {
  if (!ts || !(now > ts)) return '';
  return ' · ' + t('已 {dur}', { dur: fmtDur(now - ts) });
}

// 思考块头的 88 字预览（单行化）。
function head88(text) {
  const one = String(text || '').split('\n').map((l) => l.trim()).filter(Boolean)[0] || '';
  return one.length > 88 ? one.slice(0, 88) + '…' : one || '…';
}

// 工具卡的摘要行：常见字段优先，退回 JSON 首键。
function toolSummary(tool, input) {
  // 等待类工具说人话：TaskOutput 的入参只有 block/task_id，走默认摘
  // 要会顶出一句 "true"——直接报在等哪个后台任务。
  if (tool === 'TaskOutput' && input && typeof input === 'object' && !Array.isArray(input)) {
    const id = String(input.task_id || '');
    return id ? t('后台任务 {id}', { id: clip(id, 18) }) : t('后台任务');
  }
  if (input) {
    for (const [k] of TOOL_SUMMARY_KEYS) {
      const v = input[k];
      if (typeof v === 'string' && v.trim()) return clip(v, 96);
    }
    const keys = Object.keys(input);
    if (keys.length) {
      const v = input[keys[0]];
      const s = typeof v === 'string' ? v : JSON.stringify(v);
      return clip(String(s), 96);
    }
  }
  return tool || '';
}

function clip(s, n) {
  s = String(s || '');
  return s.length > n ? s.slice(0, n) + '…' : s;
}

function prettyJSON(v) {
  try { return JSON.stringify(v, null, 2); } catch { return String(v); }
}

// 工具入参里的本地图片：file_path/path 命中图片扩展名 → 窄端点 URL。
function imageInput(key, input) {
  for (const k of ['file_path', 'path', 'image_path']) {
    const v = input[k];
    if (typeof v === 'string' && IMG_RE.test(v)) return traceFileURL(key, v);
  }
  return null;
}

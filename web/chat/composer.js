// composer.js — the room's input face (P3-d ②, 飞书式重排): speaking
// identity is the owner (读走 observer 连接、写走房主面 — frontend 稿
// §4.6-1), the @-completion list is the current room's roster (在线∪宽限
// — the same set the server resolves @ against) with the Feishu-pinned
// all-address entries riding on top — @所有人（点名回应）、@全员（只
// 唤醒）、@<岗位>组（岗位群呼，词表与后端 RoleStem/groupWake 同源）—
// and write-face refusals surface in place via the status line, never
// swallowed. 引用回复 rides a quote bar above the input: the quoted
// line's identity is sent as STRUCTURED FIELDS on the say (chat.Quote —
// seq/from/at/snip), never as text — the body stays clean, so 复制/转发
// 不再把引用头带下来 (the old wire prefixed 「引用 #N @名字：摘要」 into
// the text; the root fix moved it to fields). at=true is Feishu's
// reply-mention (服务端按字段点名：回复即通知 TA；#N 是原消息 seq，流内
// 引用块按它精确跳回), the reply itself stays untouched, and the bar
// itself clicks back to the source message.
// 飞书-parity extras: a bare @ pops the full roster (letter
// avatars, not dots), a small emoji palette inserts at the caret
// (outside clicks close it), the input grows with its content up to
// the cap, drafts are remembered per room (localStorage-backed, the
// switcher's 草稿 marker follows live), the length counter wakes near
// the cap, the send button mirrors the input state, and ⌘/Ctrl+Enter
// sends. ZCode-parity: Tab/Shift+Tab indent/outdent the caret line or
// the selected lines (indent.js — the ←→ toolbar buttons ride the
// same leg, dim when there is nothing to outdent).

import { esc, safeColor, memberColor, avatarText, snipOf } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { postMedia, getSkills } from '../wire/api.js';
import { indentLines } from './indent.js';

// the /fragment being completed: '/' + key stem up to the caret. The
// slash must sit at the start of the text or after whitespace/CJK
// punctuation (URLs like "http://x" never match — their '/'s ride
// word text), and the stem is strictly [a-z0-9_-] — the asset-key
// alphabet, same source as the server's slash-attach parser in
// dispatch (the 词表与服务端解析同源 discipline the @ list follows).
const SLASH_RE = /(?:^|[\s，。、；：！？（）【】「」])\/([a-z0-9_-]{0,32})$/;

// the fixed management verbs (the server's chatCommand intercepts
// them on the owner channel — the composer's menu only teaches the
// grammar; display rides the same pick-item face as skills).
const SLASH_VERBS = [
  { cmd: 'skill new', role: () => t('新建技能：<key> <名称>＋换行正文') },
  { cmd: 'skill rm', role: () => t('删除技能 <key>') },
  { cmd: 'skill list', role: () => t('列出技能库') },
  { cmd: 'mcp add', role: () => t('登记 MCP 服务：<key> <名称> <http|sse> <url>') },
  { cmd: 'mcp rm', role: () => t('删除 MCP 服务 <key>') },
  { cmd: 'mcp list', role: () => t('列出 MCP 库') },
];

// the skill library cache feeding the menu: fetched lazily on first
// slash, refreshed at most once a minute (a slow fetch never blocks —
// the menu paints verbs immediately and skills join on the next
// keystroke once they land).
const skillMenu = { at: 0, list: null, loading: false };
function refreshSkillMenu() {
  const now = Date.now();
  if (skillMenu.loading || (skillMenu.list && now - skillMenu.at < 60_000)) return;
  skillMenu.loading = true;
  getSkills().then((list) => { skillMenu.list = list || []; skillMenu.at = now; })
    .catch(() => { /* 库不可达：动词照常，技能缺席 */ })
    .finally(() => { skillMenu.loading = false; });
}

// the @fragment being completed: @ + name stem up to the caret. CJK
// and full/half-width punctuation before '@' all trigger（中文没有空格
// 分词，"帮我@小策" 是主流打法）, and '@' right after ASCII word text
// triggers too while the stem is empty or CJK-headed — "bug@小策"
// completes, "a@b" (an email in progress) stays quiet（与后端 chat 包
// atMentionStart 同源）。The stem is OPTIONAL: a bare '@' opens the
// whole roster (Feishu behavior). Two alternation arms instead of a
// lookbehind: the app webview must run on older engines too.
const AT_RE = /(?:^|[^\w@._+-])@([\w\u4e00-\u9fff][\w\u4e00-\u9fff_-]*)?$|[\w._+-]@([\u4e00-\u9fff][\w\u4e00-\u9fff_-]*)?$/;

// 被引消息行首的引用头（历史存量格式）：「引用 #N X：摘要」（带「」）
// 与「引用 #N @X：摘要」（无括号，v2.8 存量）。结构化引用上线后新消息
// 的正文不再带头（引用走 chat.Quote 字段），这道剥离只服务旧消息——
// 引用一条存量带头的回复时剥掉它再取摘要：机械引用头里的 @ 不是被引
// 人的话，搬进你的消息就成了你的点名（与 chat 包 maskQuoteSnip 的服
// 务端遮罩同一条纪律的两端）。
const QUOTE_HEAD_RE = /^(?:「引用 [^「」\n]*」|引用 (?:#\d+ )?@?[^：\n]{1,60}：[^\n]*)\s*\n?/;

// 表情面板：一小套高频表情，点选即插进光标处（纯文本，线上无差异）。
// 小助手顾问室的输入井共用同一份（以牛马对话为准），故导出。
export const EMOJI = [
  '👍', '👌', '✅', '❌', '⚠️', '🔥', '💡', '🎉',
  '🙏', '😂', '😊', '😅', '😮', '🥲', '😢', '😡',
  '🤔', '🫡', '👀', '💪', '🚀', '📌', '📝', '⏰',
  '☕', '🎯', '🐛', '🧪', '📊', '🧵', '🫧', '🌙',
];

// 会话草稿（飞书式按房记忆）：切走存、切回取，localStorage 兜底跨重启。
// 条目是 {t: 文本, i: [已上传图片引用]}；旧版的纯字符串条目读回时归一
// 成 {t: 原文, i: []}（图片位空——老草稿没图）。
const DRAFT_KEY = 'dh.chat.drafts';
const DRAFT_CAP = 5000;

// 输入图片（飞书式附件）：一条 say 至多 9 张、单张 8MB（与服务端
// media.Store 同帽——客户端先拒并说明，服务端再兜底）。
const IMG_MAX = 9;
const IMG_MAX_BYTES = 8 << 20;

function loadDrafts() {
  let raw = {};
  try { raw = JSON.parse(localStorage.getItem(DRAFT_KEY)) || {}; } catch { raw = {}; }
  const out = {};
  for (const [k, v] of Object.entries(raw)) {
    out[k] = typeof v === 'string' ? { t: v, i: [] } : (v || { t: '', i: [] });
  }
  return out;
}

function saveDrafts(drafts) {
  try { localStorage.setItem(DRAFT_KEY, JSON.stringify(drafts)); } catch { /* 私密模式等：内存里仍生效 */ }
}

export class Composer {
  /**
   * @param {Object} els {root, input, send, pick, status, quote, quoteText,
   *   [count] 字数角标, [at] @小钮, [emoji] 表情小钮, [emojiPick] 表情面板,
   *   [outdent] ← 小钮, [indent] → 小钮（可选——测试脸不传则缩进只走键盘腿）}
   * @param {import('./rooms.js').RoomBook} book
   * @param {Object} [opts]
   * @param {(from: string, snip: string, seq?: number) => void} [opts.jump] 点引用条跳回原消息（seq 在场时精确命中）
   * @param {() => void} [opts.onSent] 发出一条消息后的回调（流借机回钉贴底——读历史时回话也跟到底）
   */
  constructor(els, book, opts = {}) {
    this.els = els;
    this.book = book;
    this.opts = opts;
    this.ownerName = null;
    this.quote = null; // {from, text} — the bar's payload, prepended on send
    this.matches = [];
    this.active = -1;
    this.atStart = -1; // caret-relative start of the @fragment being completed
    this.pickMode = null; // 'at' | 'slash' — which fragment the menu completes
    this.slashStart = -1; // caret-relative start of the /fragment
    this.drafts = loadDrafts();
    this.roomKey = book.current; // the room whose draft the input currently holds
    // 输入图片的待发件（{state:'up'|'ok'|'fail', img?, url?}）：选图/
    // 贴图/拖图即上传（POST /media），say 帧只带回来的引用。
    this.images = [];
    // 关窗/刷新时当前房草稿也落一份——切房才存会漏掉「打完就走」
    window.addEventListener('pagehide', () => this.#stashDraft(this.roomKey));

    els.send.addEventListener('click', () => this.#send());
    els.input.addEventListener('keydown', (ev) => this.#keydown(ev));
    els.input.addEventListener('input', () => this.onInput());
    // ← 钮的可用态跟着选区走（盖行里得有前导空白可剥）：光标移动不走
    // input 事件，select/keyup 两路兜住方向键与点击挪光标。
    els.input.addEventListener('select', () => this.#syncIndent());
    els.input.addEventListener('keyup', (ev) => { if (ev.key.startsWith('Arrow')) this.#syncIndent(); });
    // 贴图：截图进剪贴板直接贴进输入井（纯文本粘贴不受影响——只认
    // clipboardData.files 里的图片文件）。
    els.input.addEventListener('paste', (ev) => {
      const files = [...(ev.clipboardData?.files || [])].filter((f) => f.type.startsWith('image/'));
      if (files.length) {
        ev.preventDefault(); // 图片不进正文（正文是纯文本井）
        this.#attachFiles(files);
      }
    });
    els.input.addEventListener('blur', () => {
      setTimeout(() => this.#hidePick(), 150); // let a click land first
    });
    els.pick.addEventListener('mousedown', (ev) => ev.preventDefault()); // keep focus
    els.pick.addEventListener('click', (ev) => {
      const at = ev.target.closest('[data-name]');
      if (at) this.#complete(at.dataset.name);
      const sl = ev.target.closest('[data-slash]');
      if (sl) this.insertSlash(sl.dataset.slash);
    });
    // @/表情/图片/缩进小钮同样按住焦点：mousedown 一落输入井就 blur，blur
    // 定时器会把刚弹出的名单/面板又秒收回去
    for (const b of [els.at, els.emoji, els.image, els.outdent, els.indent]) {
      b?.addEventListener('mousedown', (ev) => ev.preventDefault());
    }
    // 缩进小钮 ←→：与 Tab/Shift+Tab 同一条腿（indent()），点完焦点留在
    // 输入井，连点连缩。
    els.outdent?.addEventListener('click', () => this.indent(-1));
    els.indent?.addEventListener('click', () => this.indent(1));
    // 图片小钮 → 隐藏文件井（accept 白名单与服务端同源）；选完即传。
    els.image?.addEventListener('click', () => {
      if (!els.input.disabled) els.file?.click();
    });
    els.file?.addEventListener('change', () => {
      this.#attachFiles([...els.file.files]);
      els.file.value = ''; // 同一张图连选两次也要再触发 change
    });
    // 拖图进输入框：整个 shell 是落点，dragover 亮金框、松手接文件。
    const shell = els.input.closest('#composer-shell');
    if (shell) {
      shell.addEventListener('dragover', (ev) => {
        if ([...(ev.dataTransfer?.types || [])].includes('Files')) {
          ev.preventDefault();
          shell.classList.add('drag');
        }
      });
      shell.addEventListener('dragleave', (ev) => {
        if (!shell.contains(ev.relatedTarget)) shell.classList.remove('drag');
      });
      shell.addEventListener('drop', (ev) => {
        const files = [...(ev.dataTransfer?.files || [])].filter((f) => f.type.startsWith('image/'));
        if (files.length) {
          ev.preventDefault();
          this.#attachFiles(files);
        }
        shell.classList.remove('drag');
      });
    }
    // 待发缩略图的移除钮（事件委托——条目重画不重挂）
    els.thumbs?.addEventListener('click', (ev) => {
      const btn = ev.target.closest('[data-img-x]');
      if (btn) this.#removeImage(Number(btn.dataset.imgX));
    });
    els.quote?.addEventListener('click', (ev) => {
      if (ev.target.closest('[data-quote-x]')) { this.#clearQuote(); return; }
      // 点引用条本体 → 跳回原消息（与流内引用块同一跳法；seq 精确命中）
      if (this.quote && !ev.target.closest('button')) {
        this.opts.jump?.(this.quote.from, this.quote.text, this.quote.seq || 0);
      }
    });
    els.at?.addEventListener('click', () => this.#insert('@'));
    if (els.emoji && els.emojiPick) {
      els.emojiPick.innerHTML = EMOJI.map((e) =>
        `<button type="button" data-emoji="${e}">${e}</button>`).join('');
      els.emoji.addEventListener('click', () => {
        els.emojiPick.hidden = !els.emojiPick.hidden;
        els.emoji.classList.toggle('on', !els.emojiPick.hidden);
        if (!els.emojiPick.hidden) this.#hidePick(); // 两弹层互斥
      });
      els.emojiPick.addEventListener('mousedown', (ev) => ev.preventDefault()); // keep focus
      els.emojiPick.addEventListener('click', (ev) => {
        const b = ev.target.closest('[data-emoji]');
        if (b) {
          this.#insert(b.dataset.emoji);
          els.emojiPick.hidden = true;
          els.emoji.classList.remove('on');
        }
      });
      // 点面板外收起（选中/ Esc 之外的第二条收口）
      document.addEventListener('mousedown', (ev) => {
        if (els.emojiPick.hidden) return;
        if (ev.target.closest('#emoji-pick, #composer-emoji')) return;
        els.emojiPick.hidden = true;
        els.emoji.classList.remove('on');
      });
    }
    this.#syncIndent(); // ←→ 初始亮灭（只读脸/空井先对上账）
  }

  /** input 事件的公共缝（构造器接线；测试直驱）：补全探测＋发送钮对
   *  账＋量高＋草稿＋缩进钮亮灭——与真输入事件同一腿。 */
  onInput() {
    this.#sync();
    this.#syncSend();
    this.#grow();
    this.#queueDraft();
    this.#syncIndent();
  }

  /**
   * 办公室切换：草稿按办公室记忆——旧办公室存、新办公室取；引用不跨办公室携带。
   * @param {string|null} _key @param {string} hint
   */
  onChange(_key, hint) {
    if (hint !== 'switch') return;
    this.#stashDraft(this.roomKey);
    this.roomKey = this.book.current;
    const d = this.drafts[this.roomKey] || { t: '', i: [] };
    this.els.input.value = (d.t || '').slice(0, DRAFT_CAP);
    // 草稿里的图片引用原样还回（字节在服务端仓库，缩略图走 /media/{id}）
    this.#setImages((d.i || []).map((im) => ({ state: 'ok', img: im, url: '/media/' + im.id })));
    this.#clearQuote();
    this.#hidePick();
    if (this.els.emojiPick) this.els.emojiPick.hidden = true;
    this.els.emoji?.classList.remove('on');
    this.setIdentity(this.ownerName); // status line back to the resting label
    this.#grow();
    this.#syncSend();
    this.#syncIndent(); // ←→ 钮跟着换房后的文本/选区重新亮灭
  }

  setIdentity(name) {
    this.ownerName = name;
    if (name) {
      this.els.status.textContent = t('以 {name}（房主）身份发言', { name });
      this.els.input.disabled = false;
      this.els.input.placeholder = t('发言，@成员 / @所有人 / @全员 / @岗位组 派活；Enter 发送，Shift+Enter 换行');
    } else {
      this.els.status.textContent = t('未识别到房主身份（/kb/people 无 local:true）——只读模式');
      this.els.input.disabled = true;
      this.els.input.placeholder = t('只读模式');
    }
    if (this.els.image) this.els.image.disabled = !name;
    this.#syncSend();
    this.#syncIndent(); // 只读翻转连 ←→ 一起灰
  }

  /** 发送键随输入亮灭：空串或只读时灰着，别让点击落空。有已就绪的图
   * 片时空文本也可发（飞书式纯图消息）；仍有上传在途时灰着——半截消
   * 息不如等一秒。逼近上限亮字数。 */
  #syncSend() {
    const ready = this.images.filter((a) => a.state === 'ok');
    const uploading = this.images.some((a) => a.state === 'up');
    this.els.send.disabled = !this.ownerName || uploading ||
      (!this.els.input.value.trim() && ready.length === 0);
    const count = this.els.count;
    if (!count) return;
    const len = this.els.input.value.length;
    const hot = len > DRAFT_CAP - 500;
    count.hidden = !hot;
    count.classList.toggle('hot', len > DRAFT_CAP - 100);
    if (hot) count.textContent = `${len}/${DRAFT_CAP}`;
  }

  /** 输入井随内容自增高（封顶 140px，CSS max-height 同源）。板块藏着
   * 量不出（display:none 下 scrollHeight＝0）——不把 0 写成高度（那会
   * 把输入井压成一条缝），保持 'auto'（自然行高兜底），露面后
   * revealed() 补量。 */
  #grow() {
    const input = this.els.input;
    input.style.height = 'auto';
    if (input.scrollHeight > 0) {
      input.style.height = `${Math.min(140, input.scrollHeight)}px`;
    }
    this.#resized();
  }

  /** 对话板块露面（app.js route() 喂）：侧栏办公室列表在任何板块都能切
   * 房，藏着切过来的那一刻输入井量不出高度——露面重量一次，顺带把
   * 窗口变宽变窄后的换行变化也对上账。 */
  revealed() { this.#grow(); }

  // 输入区的任何高度变化（输入井自增/缩、引用条挂/收、图片条涨/落）
  // 都在挤压上方的消息流：流钉着贴底时要跟着到新底（onRelayout →
  // stream.relayout）——高度变化不触发 scroll 事件，流自己察觉不到。
  #resized() { this.opts.onRelayout?.(); }

  #stashDraft(key) {
    const text = this.els.input.value.slice(0, DRAFT_CAP);
    // 草稿只带已就绪的图片引用（上传中/失败的留在界面上不进草稿——
    // 它们没有可引用的 id）。
    const imgs = this.images.filter((a) => a.state === 'ok').map((a) => a.img);
    if (text.trim() || imgs.length) this.drafts[key] = { t: text, i: imgs };
    else delete this.drafts[key];
    saveDrafts(this.drafts);
  }

  /** 边打边存（300ms 去抖）：草稿跟手落库——硬杀无 pagehide 也丢不了。 */
  #queueDraft() {
    clearTimeout(this._draftSyncTimer);
    this._draftSyncTimer = setTimeout(() => {
      this.#stashDraft(this.roomKey);
    }, 300);
  }

  note(text) { // write-face receipts: failures and seat downgrades, in place
    if (!text) return;
    this.els.status.textContent = text;
    this.els.status.classList.add('err');
    setTimeout(() => {
      this.els.status.classList.remove('err');
      this.setIdentity(this.ownerName); // back to the resting label
    }, 6000);
  }

  /**
   * 引用回复: aim the composer at one message — the stream's hover
   * action parks a quote bar above the input; sending prepends the
   * quoted line (「引用 #N @名字：摘要」 — the @ makes it a Feishu
   * reply-mention: 被引用人随 mentions 解析被点名唤醒) and clears
   * the bar.
   * @param {import('../wire/wire.js').Frame} frame
   */
  quoteFrom(frame) {
    if (this.els.input.disabled || !frame) return;
    // 被引消息自带的引用头不搬进摘要：剥掉行首的「引用 …」（新格式带
    // 「」/#N）与旧自动头（「引用 #N X：…」无括号的历史存量）——你引用
    // 的是这条回复本身，它肚子里机械的引用头（连同其中的 @名字，迟到
    // 回答的头常是 @你自己）进了你的消息就成了字面点名：引用别人却
    // 「艾特自己」、误唤醒被引消息里 @ 到的第三人，都是这条搬运引起的。
    let body = String(frame.text || ''), stripped = body.replace(QUOTE_HEAD_RE, '');
    while (stripped !== body) { body = stripped; stripped = body.replace(QUOTE_HEAD_RE, ''); }
    this.quote = { from: frame.from || '', text: snipOf(stripped.trim() || frame.text), seq: frame.seq || 0 };
    if (this.els.quoteText) {
      // 回复 @名字（飞书式回复即点名）：@段亮成名牌色，提示发送即 @ 到 TA
      this.els.quoteText.innerHTML =
        t('回复 {at}：{text}', { at: `<span class="q-at">@${esc(this.quote.from)}</span>`, text: esc(this.quote.text) }) +
        '<span class="q-wake">' + esc(t('发送将 @ 并通知 TA')) + '</span>';
    }
    if (this.els.quote) this.els.quote.hidden = false;
    this.#resized();
    this.focus();
  }

  #clearQuote() {
    this.quote = null;
    if (this.els.quote) this.els.quote.hidden = true;
    this.#resized();
  }

  /**
   * Prefill the input without sending — the gantt's 「向编排者提要求」
   * lands here (Q11: the mirror pre-aims the composer, the host pulls
   * the trigger). Caret parks at the end; a hint says what happened.
   * @param {string} text
   */
  draft(text) {
    if (!text || this.els.input.disabled) return;
    this.#clearQuote(); // a gantt draft and a chat quote don't compose
    this.els.input.value = text;
    this.els.input.focus();
    this.els.input.setSelectionRange(text.length, text.length);
    this.#grow();
    this.#syncSend();
    this.els.status.textContent = t('已预填一条 @排期编排组 消息——检查/补全后 Enter 发送');
    this.els.status.classList.remove('err');
    clearTimeout(this._draftTimer);
    this._draftTimer = setTimeout(() => this.setIdentity(this.ownerName), 10_000);
  }

  focus() { if (!this.els.input.disabled) this.els.input.focus(); }

  #send() {
    const text = this.els.input.value.trim();
    const images = this.images.filter((a) => a.state === 'ok').map((a) => a.img);
    if (!text && !images.length) return;
    // 结构化引用（飞书式回复即点名，根治版）：引用以字段随帧——正文
    // 保持干净，复制/转发/检索不再把引用头带下来。#N 是原消息 seq（跳
    // 转精确命中）；at=true 让服务端按字段点名（引用即点名）——被引用
    // 人收到点名注入，回复即通知，正文无需再写 @。
    const q = this.quote;
    const quote = q ? { seq: q.seq || 0, from: q.from, at: true, snip: q.text } : undefined;
    this.book.send(text, images.length ? images : undefined, quote);
    this.els.input.value = '';
    this.#clearQuote();
    this.#setImages([]);
    this.#hidePick();
    delete this.drafts[this.roomKey]; // 已说出口的话不再占草稿位
    saveDrafts(this.drafts);
    this.#grow();
    this.#syncSend();
    this.#syncIndent(); // 井已清空：← 没什么可剥，跟着灭
    // 发送即拉底：流回钉贴底（读历史时回话跟着自己的话走）。放在输入
    // 井/引用条/图片条收缩之后——送底量的才是落定后的布局（收缩会改
    // 流高，ResizeObserver 那条腿也兜得住，但直接量准就少一跳）。
    this.opts.onSent?.();
    this.els.input.focus();
  }

  // --- 输入图片（飞书式附件） ---------------------------------------
  //
  // 选图/贴图/拖图都落 #attachFiles：先过张数与单张尺寸帽（超帽当场
  // 说明，不打扰其余张），逐张 POST /media 换引用；缩略图本地预览
  //（objectURL），上传完成的条目随时可随 say 发出，失败的转红可移除。

  /**
   * 接住一批图片文件：逐张登记待发条目并开始上传。
   * @param {File[]} files 只剩图片文件的列表（调用方已滤）
   */
  #attachFiles(files) {
    if (this.els.input.disabled || !files?.length) return;
    let rejected = 0, oversize = 0;
    for (const f of files) {
      if (this.images.length >= IMG_MAX) { rejected++; continue; }
      if (f.size > IMG_MAX_BYTES) { oversize++; continue; }
      const entry = { state: 'up', img: null, url: null, name: f.name || '' };
      entry.url = URL.createObjectURL(f); // 本地预览先上，不等网络
      this.images.push(entry);
      this.#measureDims(entry, f);
      this.#upload(entry, f);
    }
    if (rejected) this.note(t('一条消息至多 {max} 张图，多出的 {n} 张没有附上', { max: IMG_MAX, n: rejected }));
    if (oversize) this.note(t('{n} 张超过 8MB 尺寸帽，没有附上', { n: oversize }));
    this.#paintThumbs();
    this.#syncSend();
  }

  /** 上传一张（结果回写条目：ok 换引用；失败转红等用户处置）。 */
  async #upload(entry, file) {
    try {
      const { image } = await postMedia(file, entry.name);
      if (this.images.includes(entry)) { // 移除竞态：上传归来时条目可能已被 ✕
        entry.img = image;
        entry.state = 'ok';
      }
    } catch (err) {
      if (this.images.includes(entry)) {
        entry.state = 'fail';
        entry.err = (err && err.message) || t('上传失败');
      }
    }
    this.#paintThumbs();
    this.#syncSend();
  }

  /** 量像素（展示提示用，尽力而为——量不出不影响发送）。 */
  #measureDims(entry) {
    const img = new Image();
    img.onload = () => {
      if (entry.img) { entry.img.w = img.naturalWidth; entry.img.h = img.naturalHeight; }
      else { entry.w = img.naturalWidth; entry.h = img.naturalHeight; }
    };
    img.src = entry.url;
  }

  /** 移除一枚待发件（上传中也可移除——归来时按竞态守卫丢弃）。 */
  #removeImage(i) {
    const [entry] = this.images.splice(i, 1);
    if (entry?.url?.startsWith('blob:')) URL.revokeObjectURL(entry.url);
    this.#paintThumbs();
    this.#syncSend();
  }

  /** 整批替换（切房还草稿/发送清空）。 */
  #setImages(list) {
    for (const e of this.images) {
      if (e.url && e.url.startsWith('blob:')) URL.revokeObjectURL(e.url);
    }
    this.images = list;
    this.#paintThumbs();
    this.#syncSend();
    this.#resized();
  }

  /** 待发缩略图条：本地 objectURL 预览，上传中蒙纱、失败红框，✕ 移除。 */
  #paintThumbs() {
    const strip = this.els.thumbs;
    if (!strip) return;
    if (!this.images.length) {
      strip.hidden = true;
      strip.innerHTML = '';
      return;
    }
    strip.hidden = false;
    strip.innerHTML = this.images.map((a, i) => {
      const cls = a.state === 'up' ? ' pending' : a.state === 'fail' ? ' failed' : '';
      const title = a.state === 'fail'
        ? t('{err}——点 ✕ 移除', { err: a.err || t('上传失败') })
        : (a.img?.name || a.name || t('图片'));
      const inner = a.url ? `<img src="${a.url}" alt="">` : (a.state === 'fail' ? '!' : '');
      return `<div class="cthumb${cls}" title="${esc(title)}">${inner}` +
        `<button type="button" class="ct-x" data-img-x="${i}" title="${t('移除')}">✕</button></div>`;
    }).join('');
  }

  #keydown(ev) {
    // IME 组合中：回车是确认候选、Esc 是取消组合，都不是聊天键——
    // 不挡的话拼音打一半一回车，半截话就直接发出去了（飞书同款守卫）。
    if (ev.isComposing || ev.keyCode === 229) return;
    if (this.matches.length) {
      if (ev.key === 'ArrowDown') { ev.preventDefault(); this.#move(1); return; }
      if (ev.key === 'ArrowUp') { ev.preventDefault(); this.#move(-1); return; }
      if (ev.key === 'Enter' || ev.key === 'Tab') {
        ev.preventDefault();
        const hit = this.matches[this.active];
        if (hit.slash !== undefined) this.insertSlash(hit.slash);
        else this.#complete(hit.name);
        return;
      }
      if (ev.key === 'Escape') { ev.preventDefault(); this.#hidePick(); return; }
    }
    if (ev.key === 'Escape' && this.els.emojiPick && !this.els.emojiPick.hidden) {
      ev.preventDefault();
      this.els.emojiPick.hidden = true;
      this.els.emoji?.classList.remove('on');
      return;
    }
    // Tab 的本职是缩进（ZCode 式）：@ 补全弹层开着时在上面被摘走当
    // 「选中候选」，其余场合不再把焦点拱手让出输入井——多行排列表
    // 格/嵌套列表的输入手感与编辑器对齐。Shift+Tab 反向剥一层。
    if (ev.key === 'Tab') {
      ev.preventDefault();
      this.indent(ev.shiftKey ? -1 : 1);
      return;
    }
    if (ev.key === 'Enter' && (!ev.shiftKey || ev.metaKey || ev.ctrlKey)) {
      ev.preventDefault();
      this.#send();
    }
  }

  /** ZCode 式行缩进（←→ 小钮与 Tab/Shift+Tab 同一条腿，indent.js）：
   * 无选区＝光标处垫两空格，有选区＝盖行整块垫/剥一层；剥无可剥
   * （indentLines 返 null）原样不动。完事跑一遍「值变了」的例行公事
   * （@ 探测/发送键/自增高/草稿——与 input 事件同一队列），← 钮可用
   * 态对上账。@param {1|-1} dir */
  indent(dir) {
    const input = this.els.input;
    if (input.disabled) return;
    const r = indentLines(input.value,
      input.selectionStart ?? input.value.length,
      input.selectionEnd ?? input.value.length, dir);
    if (!r) return;
    input.value = r.value;
    input.setSelectionRange(r.start, r.end);
    this.#sync();
    this.#syncSend();
    this.#grow();
    this.#queueDraft();
    this.#syncIndent();
    input.focus();
  }

  /** ←→ 小钮的亮灭（照 ZCode 工具栏箭头：有事可做才亮）：→ 跟输入井
   *  走（能写就能缩）；← 还得盖行里真有前导空白可剥——indentLines
   *  返 null 就是「无可剥」的唯一判据，别另写一份正则。 */
  #syncIndent() {
    const input = this.els.input;
    if (this.els.indent) this.els.indent.disabled = input.disabled;
    if (this.els.outdent) {
      this.els.outdent.disabled = input.disabled || indentLines(input.value,
        input.selectionStart ?? input.value.length,
        input.selectionEnd ?? input.value.length, -1) === null;
    }
  }

  /**
   * 在光标处插入一段文本（@ 小钮、表情面板、侧栏点名共用），插入后
   * 重跑 @ 补全探测——裸 '@' 会当场弹出整份名单。
   * @param {string} text
   */
  #insert(text) {
    const input = this.els.input;
    if (input.disabled) return;
    const s = input.selectionStart ?? input.value.length;
    const e = input.selectionEnd ?? s;
    input.value = input.value.slice(0, s) + text + input.value.slice(e);
    const pos = s + text.length;
    input.setSelectionRange(pos, pos);
    input.focus();
    this.#sync();
    this.#grow();
    this.#syncSend();
    this.#syncIndent(); // 值变了就对账（← 的事可做随行首空白走）
  }

  /** 侧栏在线牛马点名的落点：把 `@名字 ` 插进光标处。 @param {string} name */
  insertMention(name) {
    this.#insert(`@${name} `);
  }

  #move(delta) {
    this.active = (this.active + delta + this.matches.length) % this.matches.length;
    this.#paintActive();
  }

  #sync() {
    const input = this.els.input;
    const before = input.value.slice(0, input.selectionStart);
    const m = AT_RE.exec(before);
    if (m) { this.#syncAt(m, before); return; }
    const sm = SLASH_RE.exec(before);
    if (sm) { this.#syncSlash(sm, before); return; }
    this.#hidePick();
  }

  // the @ completion (roster + pinned broadcast entries) — the
  // original pick face, unchanged.
  #syncAt(m, before) {
    const stemStr = m[1] ?? m[2] ?? ''; // 裸 '@' 时为空串 → 全名单兜底
    const stem = stemStr.toLowerCase();
    // 置顶群发项（飞书式「所有人」置顶的同族扩展）：点名、唤醒、岗位
    // 群呼——补全词与后端 chat 包的解析令牌严格同源（@所有人/@全员/
    // @<岗位>组），敲出去即生效，不需要服务端再猜。
    const pins = [];
    if ('所有人'.startsWith(stem) && stemStr !== '所有人') {
      // name 是敲进正文的 @令牌（服务端 allMentionKind 只认中文裸令牌），
      // 不进词典；role 是名单行的描述文案，随界面语言走。
      pins.push({ name: '所有人', role: t('点名全办公室（各成员需回应）'), pin: 'all' });
    }
    if ('全员'.startsWith(stem) && stemStr !== '全员') {
      pins.push({ name: '全员', role: t('唤醒所有人（无需逐人回应）'), pin: 'all' });
    }
    for (const g of this.book.groupCandidates()) {
      if (g.stem.toLowerCase().startsWith(stem) && g.name !== stemStr) {
        pins.push({ name: g.name, role: t('岗位群呼 · {n} 人', { n: g.count }), pin: 'group', stem: g.stem });
      }
    }
    const starts = [], contains = [];
    for (const mem of this.book.mentionCandidates()) {
      const n = mem.name.toLowerCase();
      if (n.startsWith(stem) && mem.name !== stemStr) starts.push(mem);
      else if (stem.length > 0 && n.includes(stem)) contains.push(mem);
    }
    // 全量进榜不截断：名单锚在输入井上沿、CSS max-height 内滚动
    // （键盘导航 scrollIntoView 跟随）——截 8 条时置顶群发项先吃掉
    // 几格，房里人一多真名册就「显示不全」。
    this.matches = [...pins, ...starts, ...contains];
    this.atStart = before.length - stemStr.length - 1; // index of the '@'
    this.pickMode = 'at';
    if (!this.matches.length) { this.#hidePick(); return; }
    this.active = 0;
    // 飞书式名单：字母头像＋名字＋角色（点彩色小点难认人）；群发项
    // 用专属头像——全房项蓝底 '@'，岗位组项按组名稳定色取首字。
    this.els.pick.innerHTML = this.matches.map((mem, i) => {
      const ava = mem.pin === 'all'
        ? `<span class="avatar xs pin-all">@</span>`
        : `<span class="avatar xs" style="background:${safeColor(memberColor(mem.stem || mem.name))}">${esc(avatarText(mem.stem || mem.name))}</span>`;
      return `<button type="button" class="pick-item${i === 0 ? ' active' : ''}" data-name="${esc(mem.name)}">` +
        ava +
        `<span class="name">${esc(mem.name)}</span>` +
        `<span class="role">${esc(mem.role || '')}</span></button>`;
    }).join('');
    this.els.pick.hidden = false;
    if (this.els.emojiPick && !this.els.emojiPick.hidden) { // 两弹层互斥
      this.els.emojiPick.hidden = true;
      this.els.emoji?.classList.remove('on');
    }
  }

  // the / completion: management verbs pinned on top, then the skill
  // library (selecting one attaches it to the outgoing message — the
  // dispatcher resolves /key tokens against the library and prepends
  // the bodies; the chat text keeps the tokens, readable as sent).
  #syncSlash(sm, before) {
    refreshSkillMenu(); // lazy, rate-limited; this keystroke paints verbs
    const stem = (sm[1] || '').toLowerCase();
    const items = [];
    for (const v of SLASH_VERBS) {
      if (v.cmd.startsWith(stem) && v.cmd !== stem) {
        items.push({ slash: v.cmd, role: v.role(), verb: true });
      }
    }
    for (const sk of skillMenu.list || []) {
      const k = (sk.key || '').toLowerCase();
      if (k.startsWith(stem) && k !== stem) {
        items.push({ slash: sk.key, role: sk.name || '', skill: true });
      }
    }
    this.slashStart = before.length - (sm[1] || '').length - 1; // index of the '/'
    this.pickMode = 'slash';
    this.matches = items;
    if (!items.length) { this.#hidePick(); return; }
    this.active = 0;
    this.els.pick.innerHTML = items.map((it, i) =>
      `<button type="button" class="pick-item${i === 0 ? ' active' : ''}" data-slash="${esc(it.slash)}">` +
      `<span class="avatar xs slash">${it.verb ? esc(t('令')) : esc(t('技'))}</span>` +
      `<span class="name">/${esc(it.slash)}</span>` +
      `<span class="role">${esc(it.role)}</span></button>`).join('');
    this.els.pick.hidden = false;
    if (this.els.emojiPick && !this.els.emojiPick.hidden) { // 两弹层互斥
      this.els.emojiPick.hidden = true;
      this.els.emoji?.classList.remove('on');
    }
  }

  #paintActive() {
    [...this.els.pick.children].forEach((el, i) =>
      el.classList.toggle('active', i === this.active));
    this.els.pick.children[this.active]?.scrollIntoView({ block: 'nearest' });
  }

  #complete(name) {
    const input = this.els.input;
    const caret = input.selectionStart;
    const after = input.value.slice(caret);
    input.value = input.value.slice(0, this.atStart) + '@' + name + ' ' + after;
    const pos = this.atStart + name.length + 2;
    input.setSelectionRange(pos, pos);
    this.#hidePick();
    input.focus();
  }

  // the / completion: replace the /fragment with '/' + the picked
  // token + a trailing space. Verbs land as '/skill new ' (the user
  // keeps typing the args — or the body on the lines after); skills
  // land as '/key ' and ride the message to the dispatcher's
  // slash-attach. Public for the same reason insertMention is: side
  // panels (and tests) drive the composer's insert face.
  insertSlash(token) {
    const input = this.els.input;
    const caret = input.selectionStart;
    const after = input.value.slice(caret);
    input.value = input.value.slice(0, this.slashStart) + '/' + token + ' ' + after;
    const pos = this.slashStart + token.length + 2;
    input.setSelectionRange(pos, pos);
    this.#hidePick();
    this.#syncSend();
    this.#grow();
    this.#queueDraft();
    input.focus();
  }

  #hidePick() {
    this.matches = [];
    this.active = -1;
    this.pickMode = null;
    this.els.pick.hidden = true;
    this.els.pick.innerHTML = '';
  }
}

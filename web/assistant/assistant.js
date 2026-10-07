// assistant.js — the 小助手 board (the host's usage advisor).
//
// 一个板块一间顾问室：房主问、小助手答。助手本体在 Go 侧（assistant
// 包）——它和成员调度共用同一个 ZCode app-server 子进程与操作者的
// provider 配置，零额外 AI key 接入。本模块只做面板：GET /assistant
// 轮询（运行中 800ms 一拍，text_delta 的 partial 逐拍上屏＝伪流式；
// idle 后降到 15s 一拍的值班拍——超时挂起的 turn 迟到回复要能自己冒
// 出来），POST /assistant/ask 发问；不可用（调度关闭）时整间降级成说
// 明牌。回答正文与牛马对话同一渲染语言：markdown.js + stream.js 的
// dress（t_NN 任务 chip、@提及令牌），chip 点击开 TaskCard 弹层。
//
// 输入井也与牛马对话同一张脸（以对话的 composer 为准）：shell 外框＋
// 工具条（←→ 缩进钮/表情钮/状态行/字数角标/发送键），行为全套对齐——
// 随内容自增高（封顶 140px）、IME 组合中回车不发送、Enter 发送
// Shift+Enter 换行 ⌘/Ctrl+Enter 也发送、Tab/Shift+Tab 缩进/退缩
// （indent.js 的 ZCode 式行缩进，←→ 小钮同腿、← 无可剥时灰着）、
// 发送键随输入亮灭、逼近上限亮字数、问到一半的草稿落 localStorage
// （跨重启留存）。@ 提及与图片是办公室对话的专属能力（顾问室没有名
// 册可 @，ask 也只收文本），不设钮。

import { getAssistant, askAssistant } from '../wire/api.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { markdown } from '../chat/markdown.js';
import { dress } from '../chat/stream.js';
import { TaskCard } from '../chat/taskcard.js';
import { EMOJI } from '../chat/composer.js';
import { indentLines } from '../chat/indent.js';

// 空白时的引路问题：从题库随机抽一把，点了直接发。题库覆盖说明书
// 各章（台账/排期/点名/等级/技能/自动驾驶……都备着问法），进一次
// 顾问室换一把——老四条天天见，房主很快就不看了。
const SUGGESTIONS = [
  t('这间工作室是干什么的？我该从哪儿开始？'),
  t('怎么给牛马派活？@成员 派活有什么讲究？'),
  t('怎么开一个新项目（立项）？'),
  t('任务台账和甘特图怎么配合着用？'),
  t('需求是怎么变成任务的？提案和评审会是怎么走的？'),
  t('全智能模式（自动驾驶）是什么？开着有什么代价？'),
  t('任务树怎么用？父任务、子任务、依赖各是什么讲究？'),
  t('待确认变更（pending）是什么？我该去哪确认？'),
  t('牛马等级是干嘛的？等级不同权限差在哪？'),
  t('技能是什么？怎么给牛马装配技能和 MCP？'),
  t('@所有人 和 @岗位组 分别什么时候用？'),
  t('收到、已读回执都是什么意思？'),
  t('怎么引用回复某条消息？引用会点名对方吗？'),
  t('牛马给我弹了张提问卡，怎么处理？超时会怎样？'),
  t('聊天记录默认保留多久？在哪改保留期？'),
  t('Niuma_Studio 里 [proj-x] 开头的摘要行是什么？要回应吗？'),
  t('怎么招新牛马？开位是什么意思？'),
  t('哪些活推荐用命令行（niuma …）干？'),
];
const CHIP_COUNT = 4;

// 抽一把（Fisher–Yates 洗全库再切前 N 条）：停留期间的轮询重渲不换
// 签（按钮不能在指头底下跳），与上一把撞满同集就再抽一次
function rollChips(prev) {
  const roll = () => {
    const pool = SUGGESTIONS.slice();
    for (let i = pool.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [pool[i], pool[j]] = [pool[j], pool[i]];
    }
    return pool.slice(0, CHIP_COUNT);
  };
  const first = roll();
  if (prev && first.every((q) => prev.includes(q))) return roll();
  return first;
}

const POLL_RUNNING = 800; // the pseudo-stream beat while a turn runs
const POLL_IDLE = 15000;  // the standby beat: a watchdog-expired turn can
                          // still deliver its late answer minutes later —
                          // the slow poll lets it surface by itself

// 问句草稿（与牛马对话 composer 同一纪律，顾问室只有一间房＝一格）：
// 边打边存、发出去即清，问到一半关应用/刷新回来还在。
const DRAFT_KEY = 'dh.assistant.draft';
const DRAFT_CAP = 5000;

function loadDraft() {
  try { return localStorage.getItem(DRAFT_KEY) || ''; } catch { return ''; }
}

function saveDraft(text) {
  try {
    if (text.trim()) localStorage.setItem(DRAFT_KEY, text.slice(0, DRAFT_CAP));
    else localStorage.removeItem(DRAFT_KEY);
  } catch { /* 私密模式等：内存里仍生效 */ }
}

export class AssistantBoard {
  /**
   * @param {HTMLElement} root the #board-assistant container
   * @param {import('../ui/toast.js').Toast} toast the global toast
   */
  constructor(root, toast) {
    this.root = root;
    this.toast = toast;
    this.state = null;
    this.timer = null;
    this.renderedSig = ''; // 状态签：跳过无变化的重渲
    this.chips = rollChips(); // 空态引路问题：每次进屋换一把

    root.innerHTML = `
      <div class="as-wrap">
        <div class="as-list" id="as-list"></div>
        <div class="as-composer">
          <div class="as-shell">
            <div class="as-emoji-pick" id="as-emoji-pick" hidden></div>
            <textarea id="as-input" rows="2" placeholder="${esc(t('问点什么——功能怎么用、命令怎么写、概念什么意思，随便聊也行；Enter 发送，Shift+Enter 换行'))}"></textarea>
            <div class="as-bar">
              <button type="button" class="cbar-btn" id="as-outdent" title="${esc(t('减少缩进（Shift+Tab）'))}"><svg class="ico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13 8H3"/><path d="M6.5 4.5 3 8l3.5 3.5"/></svg></button>
              <button type="button" class="cbar-btn" id="as-indent" title="${esc(t('增加缩进（Tab）'))}"><svg class="ico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 8h10"/><path d="M9.5 4.5 13 8l-3.5 3.5"/></svg></button>
              <button type="button" class="cbar-btn" id="as-emoji" title="${esc(t('表情'))}"><svg class="ico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="8" cy="8" r="6"/><path d="M5.6 9.4c.6 1.1 1.4 1.7 2.4 1.7s1.8-.6 2.4-1.7"/><path d="M5.8 6.2h.01M10.2 6.2h.01"/></svg></button>
              <span id="as-note"></span>
              <span id="as-count" hidden></span>
              <button id="as-send" type="button">${esc(t('发送'))}</button>
            </div>
          </div>
        </div>
      </div>`;
    this.list = root.querySelector('#as-list');
    this.input = root.querySelector('#as-input');
    this.sendBtn = root.querySelector('#as-send');
    this.noteEl = root.querySelector('#as-note');
    this.countEl = root.querySelector('#as-count');
    this.emojiBtn = root.querySelector('#as-emoji');
    this.emojiPick = root.querySelector('#as-emoji-pick');
    this.outdentBtn = root.querySelector('#as-outdent');
    this.indentBtn = root.querySelector('#as-indent');
    this._draftTimer = null;

    this.sendBtn.addEventListener('click', () => this.send());
    this.input.addEventListener('keydown', (ev) => this.#keydown(ev));
    this.input.addEventListener('input', () => {
      this.#grow();
      this.#syncSend();
      this.#queueDraft();
      this.#syncIndent();
    });
    // ← 钮可用态跟选区走（方向键/点击挪光标不走 input）——与 composer
    // 同一对兜底
    this.input.addEventListener('select', () => this.#syncIndent());
    this.input.addEventListener('keyup', (ev) => { if (ev.key.startsWith('Arrow')) this.#syncIndent(); });

    // 缩进小钮 ←→（与 composer 同腿）：按住焦点、点完连缩
    for (const b of [this.outdentBtn, this.indentBtn]) {
      b?.addEventListener('mousedown', (ev) => ev.preventDefault());
    }
    this.outdentBtn?.addEventListener('click', () => this.#indent(-1));
    this.indentBtn?.addEventListener('click', () => this.#indent(1));

    // 表情面板（与牛马对话同一套）：小钮按住焦点、点选插进光标处、
    // 点面板外/Esc 收起
    this.emojiPick.innerHTML = EMOJI.map((e) =>
      `<button type="button" data-emoji="${e}">${e}</button>`).join('');
    this.emojiBtn.addEventListener('mousedown', (ev) => ev.preventDefault()); // keep focus
    this.emojiPick.addEventListener('mousedown', (ev) => ev.preventDefault()); // keep focus
    this.emojiBtn.addEventListener('click', () => {
      this.emojiPick.hidden = !this.emojiPick.hidden;
      this.emojiBtn.classList.toggle('on', !this.emojiPick.hidden);
    });
    this.emojiPick.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-emoji]');
      if (b) {
        this.#insert(b.dataset.emoji);
        this.#closeEmoji();
      }
    });
    document.addEventListener('mousedown', (ev) => {
      if (this.emojiPick.hidden) return;
      if (ev.target.closest('#as-emoji-pick, #as-emoji')) return;
      this.#closeEmoji();
    });

    // 草稿回流：上次问到一半的话还进输入井（面板常驻 DOM，切板本来
    // 就不丢——这里兜的是关应用/刷新）；关窗前再落一稿。
    this.input.value = loadDraft();
    window.addEventListener('pagehide', () => {
      clearTimeout(this._draftTimer);
      saveDraft(this.input.value);
    });
    this.#grow();
    this.#syncSend();
    this.#syncIndent();

    // t_NN chip → TaskCard 弹层（open 自拉 GET /kb/tasks/{id}）；顾问
    // 房是只读脸——操作钮指路项目看板，弹层不收（onOp 返 false）
    this.card = new TaskCard({
      onOp: () => { this.toast.show(t('任务操作请到「项目」看板进行'), 'info'); return false; },
      toast: this.toast,
    });
    this.list.addEventListener('click', (ev) => {
      const chip = ev.target.closest('.task-chip');
      if (chip) this.card.open(chip.dataset.task, chip);
    });

    this.refresh();
  }

  /** 板块切入视野：换一把引路问题（只在空态看得见）、立即同步一次并
   *  给输入井焦点。状态签不因换签而失效的话，轮询重渲会被 sig 挡掉、
   *  新题上不了屏——空态下顺手作废它。输入井补量一次：板块藏着量不
   *  出（与 composer.#grow 同一条纪律）。 */
  revealed() {
    this.chips = rollChips(this.chips);
    if (!this.state?.messages?.length) this.renderedSig = '';
    this.refresh();
    this.#grow();
    this.input.focus();
  }

  /** 拉一次面板状态并落屏。 */
  async refresh() {
    try {
      this.apply(await getAssistant());
    } catch { /* 瞬时失败：面板停在上一帧，下一拍自愈 */ }
  }

  /**
   * 状态落屏 + 轮询节拍：运行中按 POLL_RUNNING 续拍（partial 逐拍
   * 上屏），回到 idle 即停拍——本机回环，只在有戏时花钱。
   */
  apply(st) {
    this.state = st;
    if (this.timer) { clearTimeout(this.timer); this.timer = null; }

    const running = st.status === 'running';
    const warming = st.link === 'warming';
    this.input.disabled = !st.available;
    this.#syncSend(); // 发送键三态合一：空问题 / 运行中（预热不算）/ 不可用
    this.#syncIndent(); // 不可用翻转连 ←→ 一起灰（与 composer.setIdentity 同账）
    // 久等无字：模型通道多半在重连重试（企业网/自建中继最慢要等几分
    // 钟），把这件事说破，免得像“没有回复”；预热失败的原因也落这里
    this.noteEl.textContent =
      running && !st.partial && st.since > 90 && !warming
        ? t('模型通道可能在重连重试（最长约 12 分钟）——迟到的回复会自动补上')
        : st.link === 'fail'
          ? t('通道预热失败：{why}——首个问题可能较慢，回答不会丢', { why: st.linkNote || t('原因未知') })
          : warming
            ? t('通道预热中（企业网冷启动常见）——提问无需等待，预热不打架')
            : '';

    this.render(st);

    if (running) this.timer = setTimeout(() => this.refresh(), POLL_RUNNING);
    else this.timer = setTimeout(() => this.refresh(), POLL_IDLE);
  }

  /** 发一问：409/503 等以吐司交代，成功即以返回的新状态落屏。预热
   *  期间不拦（探针跑在独立的一次性会话上，这一问立即送出，互不挡
   *  道）。 */
  async send() {
    const text = this.input.value.trim();
    if (!text) return;
    if (this.state?.status === 'running' && this.state?.link !== 'warming') {
      this.toast.show(t('小助手还在回答上一个问题，稍候'), 'err');
      return;
    }
    if (this.state && this.state.available === false) {
      this.toast.show(this.state.reason || t('小助手当前不可用'), 'err');
      return;
    }
    this.input.value = '';
    saveDraft(''); // 已问出口的话不再占草稿位
    this.#grow();
    try {
      this.apply(await askAssistant(text));
    } catch (err) {
      this.toast.show(String(err.message || err), 'err');
      this.input.value = text; // 问句还给输入井，改两个字再发
      saveDraft(text);
      this.#grow();
      this.#syncSend();
      this.refresh();
      return;
    }
    this.input.focus();
  }

  // ---------- 输入井（与牛马对话 composer.js 同一张脸） ----------

  /** 输入井随内容自增高（封顶 140px，CSS max-height 同源）。板块藏着
   *  量不出（display:none 下 scrollHeight＝0，构造期草稿回流就会撞
   *  上）——不把 0 写成高度，露面后 revealed() 补量（composer.#grow
   *  同一条纪律）。 */
  #grow() {
    this.input.style.height = 'auto';
    if (this.input.scrollHeight > 0) {
      this.input.style.height = `${Math.min(140, this.input.scrollHeight)}px`;
    }
  }

  /** 发送键随输入亮灭：空问题灰着（点击/回车都不落空），运行中与不可
   *  用另算（态来自 apply 落的 state）。逼近上限亮字数角标（阈值与牛
   *  马对话同一对）。 */
  #syncSend() {
    const len = this.input.value.length;
    this.sendBtn.disabled = !this.input.value.trim() ||
      (this.state?.status === 'running' && this.state?.link !== 'warming') ||
      this.state?.available === false;
    const hot = len > DRAFT_CAP - 500;
    this.countEl.hidden = !hot;
    this.countEl.classList.toggle('hot', len > DRAFT_CAP - 100);
    if (hot) this.countEl.textContent = `${len}/${DRAFT_CAP}`;
  }

  #keydown(ev) {
    // IME 组合中：回车是确认候选，不是发送（与牛马对话同一守卫——拼
    // 音打一半一回车，半截话就发出去了）。
    if (ev.isComposing || ev.keyCode === 229) return;
    if (ev.key === 'Escape' && !this.emojiPick.hidden) {
      ev.preventDefault();
      this.#closeEmoji();
      return;
    }
    // Tab＝缩进（ZCode 式，与牛马对话同腿）：不把焦点拱手让出输入井
    if (ev.key === 'Tab') {
      ev.preventDefault();
      this.#indent(ev.shiftKey ? -1 : 1);
      return;
    }
    if (ev.key === 'Enter' && (!ev.shiftKey || ev.metaKey || ev.ctrlKey)) {
      ev.preventDefault();
      this.send();
    }
  }

  /** ZCode 式行缩进（indent.js，与 composer.indent 同一条腿）：无选区
   *  ＝光标处垫两空格、有选区＝盖行整块垫/剥一层；无可剥原样不动。 */
  #indent(dir) {
    if (this.input.disabled) return;
    const r = indentLines(this.input.value,
      this.input.selectionStart ?? this.input.value.length,
      this.input.selectionEnd ?? this.input.value.length, dir);
    if (!r) return;
    this.input.value = r.value;
    this.input.setSelectionRange(r.start, r.end);
    this.#grow();
    this.#syncSend();
    this.#queueDraft();
    this.#syncIndent();
    this.input.focus();
  }

  /** ←→ 小钮亮灭（与 composer.#syncIndent 同一判据）：能写就能 →；
   *  ← 得盖行里真有前导空白可剥。 */
  #syncIndent() {
    if (this.indentBtn) this.indentBtn.disabled = this.input.disabled;
    if (this.outdentBtn) {
      this.outdentBtn.disabled = this.input.disabled || indentLines(this.input.value,
        this.input.selectionStart ?? this.input.value.length,
        this.input.selectionEnd ?? this.input.value.length, -1) === null;
    }
  }

  /** 在光标处插入一段文本（表情面板用），插完自增高＋亮发送＋记草稿。 */
  #insert(text) {
    if (this.input.disabled) return;
    const s = this.input.selectionStart ?? this.input.value.length;
    const e = this.input.selectionEnd ?? s;
    this.input.value = this.input.value.slice(0, s) + text + this.input.value.slice(e);
    const pos = s + text.length;
    this.input.setSelectionRange(pos, pos);
    this.input.focus();
    this.#grow();
    this.#syncSend();
    this.#queueDraft();
  }

  #closeEmoji() {
    this.emojiPick.hidden = true;
    this.emojiBtn.classList.remove('on');
  }

  /** 边打边存（300ms 去抖）：草稿跟手落库——硬杀无 pagehide 也丢不了。 */
  #queueDraft() {
    clearTimeout(this._draftTimer);
    this._draftTimer = setTimeout(() => saveDraft(this.input.value), 300);
  }

  // ---------- 渲染 ----------

  render(st) {
    // 状态签：消息数 + 最后一条长度 + partial 长度 + 可用性——都没动
    // 就跳过（轮询很密，重渲要省）
    const last = st.messages?.[st.messages.length - 1];
    const sig = [
      st.available ? 1 : 0, st.reason || '',
      st.messages?.length || 0, last ? last.role + last.text.length : '',
      running0(st), st.partial?.length || 0,
    ].join('|');
    if (sig === this.renderedSig) { this.scrollBottom(false); return; }
    this.renderedSig = sig;
    // 全量重画前先记读位：流式回答每次长大都会重画，读历史的人不得
    // 借机被拽回底——贴底的照常跟随（scrollBottom），脱底的还塬位置
    const keepTop = (this.list.scrollHeight - this.list.scrollTop - this.list.clientHeight < 60)
      ? null : this.list.scrollTop;
    this.list.innerHTML = '';

    if (st.available === false) {
      this.list.appendChild(this.unavailableCard(st.reason || ''));
      return;
    }

    if (!st.messages?.length) {
      const chips = document.createElement('div');
      chips.className = 'as-chips';
      for (const q of this.chips) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'as-chip';
        b.textContent = q;
        b.addEventListener('click', () => { this.input.value = q; this.send(); });
        chips.appendChild(b);
      }
      const intro = document.createElement('div');
      intro.className = 'as-intro';
      intro.innerHTML =
        `<span class="as-avatar lg" aria-hidden="true">牛</span>` +
        `<p>${esc(t('我是小助手，工作室的使用顾问——怎么用、命令怎么写、概念什么意思，问我就行。'))}</p>`;
      this.list.appendChild(intro);
      this.list.appendChild(chips);
      return;
    }

    for (const m of st.messages) this.list.appendChild(this.bubble(m));
    if (st.status === 'running') this.list.appendChild(this.liveBubble(st.partial || ''));
    if (keepTop === null) this.scrollBottom(true);
    else this.list.scrollTop = Math.min(keepTop, this.list.scrollHeight);
  }

  /** 一条消息气泡（user 右浅蓝 / assistant 左灰 / error 左红），正文
   *  与牛马对话同一 Markdown 渲染（代码块/列表/表格/引用/chip/提及）。 */
  bubble(m) {
    const row = document.createElement('div');
    row.className = `as-row ${m.role === 'user' ? 'me' : m.role}`;
    if (m.role !== 'user') {
      const av = document.createElement('span');
      av.className = 'as-avatar sm';
      av.textContent = m.role === 'error' ? '!' : '牛';
      row.appendChild(av);
    }
    const b = document.createElement('div');
    b.className = 'as-bubble';
    b.innerHTML = md(m.text);
    row.appendChild(b);
    return row;
  }

  /** 运行中的活气泡：partial 有字逐字上屏（每拍整段重渲 md），没字打三点。 */
  liveBubble(partial) {
    const row = document.createElement('div');
    row.className = 'as-row assistant';
    const av = document.createElement('span');
    av.className = 'as-avatar sm';
    av.textContent = '牛';
    row.appendChild(av);
    const b = document.createElement('div');
    b.className = 'as-bubble live';
    if (!partial) {
      b.innerHTML = '<span class="as-dots"><i>·</i><i>·</i><i>·</i></span>';
      row.appendChild(b);
      return row;
    }
    // md 输出以块元素收尾，光标并进最后一个块（与末行文字同行），
    // 没有可并的块（表格/分割线收尾）才坠在泡尾
    const html = md(partial);
    b.innerHTML = /<\/(?:p|li|h[1-6]|pre)>\s*$/.test(html)
      ? html.replace(/(<\/(?:p|li|h[1-6]|pre)>)\s*$/, '<span class="as-caret"></span>$1')
      : html + '<span class="as-caret"></span>';
    row.appendChild(b);
    return row;
  }

  /** 不可用说明牌：原因 + 去黑板的指路。 */
  unavailableCard(reason) {
    const card = document.createElement('div');
    card.className = 'as-off';
    card.innerHTML =
      `<span class="as-avatar lg" aria-hidden="true">牛</span>` +
      `<p>${esc(reason || t('小助手当前不可用。'))}</p>` +
      `<p class="dim">${esc(t('恢复成员调度（去掉 --no-dispatch，并确认 ZCode 可用）后，这里即可提问；期间可去「办公室黑板」翻说明书。'))}</p>`;
    return card;
  }

  scrollBottom(force) {
    const atBottom = this.list.scrollHeight - this.list.scrollTop - this.list.clientHeight < 60;
    if (force || atBottom) this.list.scrollTop = this.list.scrollHeight;
  }
}

// running 标记进状态签（apply 里拼串用）
function running0(st) { return st.status === 'running' ? 1 : 0; }

// 正文落屏：与牛马对话同一 Markdown 渲染器 + dress（markdown.js 负责
// 转义/链接/强调；dress 装扮 t_NN chip 与 @提及——chip 点击在构造器接线）
function md(text) {
  return markdown(text, { dress });
}

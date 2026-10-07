// chronicle.js — t_153（r_10）：「工作室志」面板——编年史的纯读渲染
// 面。黑板板块第四页签（与说明书/文档/AI 接入同族），读走 getDoc 既
// 有通道，把 markdown 正文按「日期｜类型｜人｜一句话」逐行解析成日期
// 分组的时间线：类型做小标签（里程碑/交付/上岗/事件各一色），人名高
// 亮，划线修订（~~原句~~）原样保留。
//
// 按办公室隔离：每间办公室读自己的志——大厅 ops/chronicle，项目
// p/<key>/chronicle（服务端 kb.ChronicleDocKey 同一规则，钩子按事件
// 所属房落行）。load(root, key) 由 kb.js 喂当前房的键。
//
// v3 分卷：AppendRollover 把溢出的活卷整卷落成归档文档
// <key>-vol-NNN——面板顶部列出卷切换条（活卷＋各历史卷，新卷在
// 前），选卷只读渲染同款时间线。卷前缀跟当前志键走。
//
// 降级纪律（任务描述原文）：空数据/读取失败给提示不白屏——体例节以
// 外的正文为空时提示「暂无条目」，fetch 失败提示重试按钮；解析不了
// 的行（格式漂移）原样淡显不丢内容。纯展示零写路径（append 是服务端
// 钩子与 CLI 的领地）。

import { getDoc, getDocs } from '../wire/api.js';
import { esc } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { LobbyKey } from '../wire/wire.js';

// chronicleDocKey：房键 → 该房工作室志的文档键（大厅保留 v1 的
// ops/chronicle，项目各一本 p/<key>/chronicle）。纯函数，web 测试钉。
export function chronicleDocKey(roomKey) {
  if (!roomKey || roomKey === LobbyKey) return 'ops/chronicle';
  return `p/${roomKey}/chronicle`;
}

// isUnfiled：读志的错误是不是「未建档」——志文档是懒建档（这间房第
// 一条编年史事件落下时才创建），新开张的项目没有志文档是常态，
// 404 按「暂无条目」处理不当错误（驾驶舱 chronicleSources 同一条
// 纪律）。纯函数，web 测试钉。
export function isUnfiled(err) {
  return !!err && err.status === 404;
}

// 条目四型（体例 §一）——标签色沿用办公室语义色板
const TYPE_CLS = {
  '里程碑': 'chron-tp-milestone',
  '交付': 'chron-tp-delivery',
  '上岗': 'chron-tp-join',
  '事件': 'chron-tp-event',
};

// 前置段（体例说明）到正文条目的分界：首个 YYYY-MM-DD｜ 行之前都是
// 体例节（含 §四 细则的折行），渲染时跳过——面板只关心条目流。
const ENTRY_RE = /^(\d{4}-\d{2}-\d{2})｜([^｜]*)｜([^｜]*)｜(.*)$/;

// parseChronicle: markdown 正文 → {dates:[{date, items:[{type,who,text,raw}]}]}
// 折行条目（体例节里的续行）不进时间线；解析失败的行返回原样进
// raw 行，不静默丢弃。
export function parseChronicle(md) {
  const dates = [];
  const byDate = new Map();
  const stray = [];
  for (const line of String(md || '').split('\n')) {
    const m = ENTRY_RE.exec(line.trim());
    if (!m) {
      // 非条目行：可能是体例节/回溯批次说明——有实质内容且形似漏格
      // 式的（含 ｜ 但不匹配四段），收进 stray 淡显
      if (line.includes('｜') && line.trim() && !line.startsWith('#')) {
        stray.push(line.trim());
      }
      continue;
    }
    const [, date, type, who, text] = m;
    if (!byDate.has(date)) {
      const d = { date, items: [] };
      byDate.set(date, d);
      dates.push(d);
    }
    byDate.get(date).items.push({
      date, type: type.trim(), who: who.trim(), text: text.trim(), raw: line.trim(),
    });
  }
  return { dates, stray };
}

// volumeList: 归档架元数据 → 编年史历史卷清单（前缀 ops/chronicle-
// vol-，按卷号降序＝新卷在前）。纯函数，web 测试钉。
export function volumeList(metas, prefix) {
  const out = [];
  for (const m of metas || []) {
    if (!m.key || !m.key.startsWith(prefix)) continue;
    const n = parseInt(m.key.slice(prefix.length), 10);
    if (!Number.isFinite(n)) continue;
    out.push({ key: m.key, n });
  }
  out.sort((a, b) => b.n - a.n);
  return out;
}

// 渲染一日的头部（周一式紧凑：日期原样＋条数）
function dayHeader(date, n) {
  return `<div class="chron-day"><span class="chron-date">${esc(date)}</span>` +
    `<span class="chron-count">${esc(t('{n} 条', { n }))}</span></div>`;
}

// 渲染一条：类型标签＋人（高亮）＋一句话（划线修订原样）。
// it.type 是解析出的条目字面量（里程碑/交付/上岗/事件——TYPE_CLS 的键），
// 渲染时经 t() 查词典译标签；未知类型回落原文。
function itemHtml(it) {
  const cls = TYPE_CLS[it.type] || 'chron-tp-event';
  return `<div class="chron-item">` +
    `<span class="chron-tp ${cls}">${esc(t(it.type))}</span>` +
    `<span class="chron-who">${esc(it.who)}</span>` +
    `<span class="chron-text">${esc(it.text)}</span>` +
    `</div>`;
}

export class ChroniclePanel {
  /** @param {object} hooks {onRetry?} 读取失败时的重试回调（面板内自带按钮，钩子留扩展位） */
  constructor() {
    this.state = 'loading'; // loading | ready | empty | error
    this.error = null;
    this.token = 0;
    this.root = null;
    this.key = 'ops/chronicle'; // 当前读的志键（kb.js 按当前房喂入）
    this.volumes = []; // v3 分卷：[{key, n}] 新卷在前
    this.volKey = null; // null = 活卷；否则当前只读展示的历史卷 key
  }

  /** Load and render into the given root element (the tab pane host).
   *  key = 该办公室的志文档键（缺省回大厅的 ops/chronicle）。 */
  async load(root, key) {
    this.root = root;
    if (key) this.key = key;
    const tok = ++this.token;
    this.state = 'loading';
    this.volKey = null;
    this.paint();
    try {
      const [doc, arc] = await Promise.all([
        getDoc(this.key),
        getDocs({ archived: true }).catch(() => null), // 归档架取败只少卷条，不当错误
      ]);
      if (this.token !== tok) return; // a newer load won
      this.volumes = volumeList(Array.isArray(arc) ? arc : [], this.key + '-vol-');
      const md = doc && doc.body || '';
      const { dates, stray } = parseChronicle(md);
      if (!dates.length) {
        this.state = 'empty';
      } else {
        this.state = 'ready';
        this.data = { dates, stray };
      }
    } catch (err) {
      if (this.token !== tok) return;
      // 活卷未建档（404）＝这间房还没有编年史事件——空态不是错误；
      // 其余失败照旧走错误态给重试。
      if (isUnfiled(err)) {
        this.state = 'empty';
        this.volumes = [];
      } else {
        this.state = 'error';
        this.error = err;
      }
    }
    this.paint();
  }

  /** 页签切走即作废在途 load（kb.js switchTab 调）——慢回包晚到时
   *  token 对不上号，paint 不再执行，不会把别的页签刚画好的内容整面
   *  重写回工作室志（快速切换「卡住」的根因）。 */
  cancel() { this.token++; }

  /** 切卷：null 回活卷（重新走 load），历史卷就地取文渲染。 */
  async openVolume(key) {
    if (!key) { this.load(this.root); return; }
    const tok = ++this.token;
    this.state = 'loading';
    this.paint();
    try {
      const doc = await getDoc(key);
      if (this.token !== tok) return;
      const md = doc && doc.body || '';
      const { dates, stray } = parseChronicle(md);
      this.volKey = key;
      this.state = dates.length ? 'ready' : 'empty';
      this.data = { dates, stray };
    } catch (err) {
      if (this.token !== tok) return;
      this.state = 'error';
      this.error = err;
    }
    this.paint();
  }

  /** 卷切换条：活卷＋历史卷（新卷在前），当前卷高亮。 */
  volumeBar() {
    if (!this.volumes.length && !this.volKey) return '';
    const cur = this.volKey || '';
    const liveCls = !cur ? ' active' : '';
    let html = `<div class="chron-vols">` +
      `<button type="button" class="btn small${liveCls}" data-vol="">${esc(t('活卷'))}</button>`;
    for (const v of this.volumes) {
      const cls = v.key === cur ? ' active' : '';
      html += `<button type="button" class="btn small${cls}" data-vol="${esc(v.key)}">${esc(t('第{n}卷', { n: v.n }))}</button>`;
    }
    return html + '</div>';
  }

  wireVolumes() {
    const root = this.root;
    if (!root) return;
    root.querySelectorAll('[data-vol]').forEach((btn) => {
      btn.onclick = () => this.openVolume(btn.dataset.vol || null);
    });
  }

  /** Paint the current state into root (never throws, never blanks). */
  paint() {
    const root = this.root;
    if (!root) return;
    if (this.state === 'loading') {
      root.innerHTML = '<div class="chron-hint">' + esc(t('志加载中…')) + '</div>';
      return;
    }
    if (this.state === 'error') {
      root.innerHTML =
        '<div class="chron-hint chron-err">' +
        esc(t('工作室志读取失败：{msg}', { msg: this.error?.message || String(this.error) })) + '</div>' +
        '<button type="button" class="chron-retry btn">' + esc(t('重试')) + '</button>';
      const btn = root.querySelector('.chron-retry');
      if (btn) btn.onclick = () => this.load(this.root);
      return;
    }
    if (this.state === 'empty') {
      root.innerHTML = this.volumeBar() +
        '<div class="chron-hint">' + esc(t('编年史还没有条目——任务完结、需求立顶与提交落库时会自动记上一笔。')) + '</div>';
      this.wireVolumes();
      return;
    }
    const { dates, stray } = this.data;
    let html = this.volumeBar() + '<div class="chron-list">';
    for (const d of dates) {
      html += dayHeader(d.date, d.items.length);
      for (const it of d.items) html += itemHtml(it);
    }
    if (stray.length) {
      html += '<div class="chron-day"><span class="chron-date">' + esc(t('未归类行')) + '</span>' +
        `<span class="chron-count">${esc(t('{n} 条', { n: stray.length }))}</span></div>`;
      for (const s of stray) html += `<div class="chron-item chron-stray">${esc(s)}</div>`;
    }
    html += '</div>';
    root.innerHTML = html;
    this.wireVolumes();
  }
}

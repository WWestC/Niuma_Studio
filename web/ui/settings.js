// settings.js — the studio settings window (设置中心, v2.9). The gear at
// the ident-row's right opens a centered modal — the 280px popover grew
// through eight sections (连接/外观/通知/通用/智能/数据/维护/重置) until
// the list stopped being a list, so the settings moved into a proper
// Feishu-style window: a left category nav (通用/外观/通知/智能/数据/
// 连接/维护/关于) and a scrolling pane on the right, one category at a
// time (last pane remembered in prefs). The window plays by the same
// layer contract as Dialog/Drawer: mask click / × / Esc close it, and it
// pushes onto feedback.js's LAYER_STACK so a nested confirm dialog owns
// the next Esc (turning autopilot on, resetting, rebuilding all ask).
//
// 分类落位：
//   通用 — 进入对话自动聚焦（开关篇：立即生效，文案静态、肯定句）与
//          快捷键说明（对话框列出真实存在的快捷键，不编造）
//   外观 — 主题三选（浅色/深色/跟随系统），分段器点一下整窗换季（深色块
//          在 index.html 同一套槽位换值；<head> 内联脚本在首帧前先落
//          data-theme 防白闪，运行时换季由本模块的 applyTheme 接管并同
//          步原生标题栏与 PWA theme-color）
//   通知 — 新消息系统通知（微信/飞书式 OS 弹窗：原生壳经 UNUser-
//          NotificationCenter 代发、浏览器走 Notification API，点击
//          跳房；不在眼前的房才弹，与未读徽标同一道门）、提示音（三
//          枚音色住 sound.js：消息叮咚/点名叮铃/横幅叮，单一开关管
//          全部）＋音量滑杆（0–100 直写 notifyVolume，松手按新音量
//          试听一声）、横幅通知（右上角通知提醒框的总闸——他房公告
//          更新等事件）
//   智能 — 全智能模式（按项目的服务端开关，作用域跟 app.js 的
//          project() 回调＝当前房，大厅与项目房一视同仁；仅启动早期
//          还没有当前房时整行置灰）＋四旋钮（按项目的全智能节流参
//          数）＋消息注入等待（工作室级：调度器给成员注入消息后等
//          ack 的时长，真源 ~/.niuma/sendwait.json，写后热生效到全
//          部调度器）
//   数据 — 聊天记录保留期（真源在服务端 ~/.niuma/retention.json）
//   连接 — the connection verdict, 房间时间（点行弹表盘面板——面板
//          浮在设置窗之上，窗不收）、服务器地址（tap to copy）、
//          ZCode 侧栏即时刷新（服务端 reveal.json 真源——牛马行落
//          ZCode 索引库后要不要主动投递让侧栏立刻重读；开启的那一
//          笔当场强投一次，把已在库里置顶的行拉回屏幕）
//   维护 — 重新编译并重启 / 重新载入 / 重置工作室（危险红）
//   关于 — 版本（GET /）、查看说明书，与卡底落款两行（st-credit）：
//          作者署名 3StripFish（三文鱼），并特别鸣谢智谱（GLM 驱动
//          全体牛马）与飞书（界面设计语言所本）。
// 偏好键族 dh.*（同 composer 草稿的存法）：localStorage 不可用时静默降级
// ——内存默认值仍让开关可用，只是不跨重启。

import { esc, icon, dismiss, copyToClipboard } from './dom.js';
import { t, isEn, localeTag, langPref, setLang } from './i18n.js';
import { getInfo, postReset, postRebuild, getAutopilot, setAutopilot, getRebuildVote, setRebuildVote, getRetention, setRetention, getReveal, setReveal, getSendWait, setSendWait, getBudget, setBudget, getSkillAuthoring, postSkillAuthoring, getGitSummary, postGitSettings } from '../wire/api.js';
import { fmtTokens } from './usageview.js';
import { roomNow, clockSyncInfo, closeClockPop } from './clockpop.js';
import { Dialog, Notify, layerPush } from './feedback.js';

// 左导航分类：顺序即导航顺序；icon 住 dom.js（palette/link/db/info 为
// v2.9 新入列，其余沿用既有图标）
const PANES = [
  { key: 'gen', icon: 'gear', label: t('通用') },
  { key: 'proj', icon: 'branch', label: t('项目') },
  { key: 'look', icon: 'palette', label: t('外观') },
  { key: 'notify', icon: 'bell', label: t('通知') },
  { key: 'auto', icon: 'sparkle', label: t('智能') },
  { key: 'data', icon: 'db', label: t('数据') },
  { key: 'conn', icon: 'link', label: t('连接') },
  { key: 'maint', icon: 'refresh', label: t('维护') },
  { key: 'about', icon: 'info', label: t('关于') },
];

// 偏好（dh.* 键族，同 composer 草稿的存法）：私密模式等 localStorage
// 不可用时静默降级——内存默认值仍让开关可用，只是不跨重启。
const PREF_KEY = 'dh.ui.prefs';

// token 预算输入框的预设单位：真源永远是服务端的 token 数，框只是镜头
// ——中文按「万」（×1e4，贴 fmtTokens 的念法）、英文按 M（×1e6，贴 K/M/B
// 数量级）。同一预算两种语言互读：中文 500 万 ⇄ 英文 5 M。
const budgetMul = () => (isEn() ? 1e6 : 1e4);
// token → 单位面的回显：整除给整数（500万 就是 500）；带零头四舍五入
// 到两位小数（1234567 → 123.46万）——回显有损不伤真值，成对落库带的
// 是 #budget 缓存里的服务端真值
const budgetUnitStr = (tokens) => {
  const uv = tokens / budgetMul();
  return Number.isInteger(uv) ? String(uv) : String(Number(uv.toFixed(2)));
};

export function prefs() {
  try { return JSON.parse(localStorage.getItem(PREF_KEY)) || {}; } catch { return {}; }
}

export function setPref(key, value) {
  try {
    const all = prefs();
    all[key] = value;
    localStorage.setItem(PREF_KEY, JSON.stringify(all));
  } catch { /* 内存态仍生效，只是不持久 */ }
}

// 消息注入等待的人话念法：60 秒以下念秒，以上一律念分钟（一位小数，
// 整分省略）——拖滑杆时单位只在 1 分钟边界换一次，不会一会儿秒一会儿
// 分钟地跳（3030 →「50.5 分钟」，而不是「3030 秒」）
function fmtWait(seconds) {
  if (seconds < 60) return t('{n} 秒', { n: seconds });
  const m = seconds / 60;
  return t('{n} 分钟', { n: Number.isInteger(m) ? m : m.toFixed(1) });
}

// ── 主题（外观篇）─────────────────────────────────────────────────
// 偏好三值 light/dark/system（未设置＝light，与既有外观一致）；system
// 解析进 matchMedia。data-theme 落在 <html> 上，index.html 的深色块同
// 一套槽位换值——这里只管落键＋把原生标题栏（webview setChromeColor）
// 和 PWA 的 <meta theme-color> 拉齐，换季即时无重载。

const DARK_MQ = typeof matchMedia === 'function'
  ? matchMedia('(prefers-color-scheme: dark)') : null;

export function themePref() { return prefs().theme || 'light'; }

function resolvedTheme(pref) {
  if (pref === 'dark' || pref === 'light') return pref;
  if (pref === 'system') return DARK_MQ?.matches ? 'dark' : 'light';
  return 'light';
}

export function applyTheme() {
  const dark = resolvedTheme(themePref()) === 'dark';
  if (dark) document.documentElement.dataset.theme = 'dark';
  else delete document.documentElement.dataset.theme;
  applyPluginThemeOverlay();
  // 原生壳标题栏与 PWA 地址条跟 --titlebar 走（浅灰/夜灰各归其位；
  // 插件主题铺过 --titlebar 时计算值自然取到插件的那一档）
  const tb = getComputedStyle(document.documentElement).getPropertyValue('--titlebar').trim();
  if (tb) {
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', tb);
    if (typeof window.setChromeColor === 'function') window.setChromeColor(tb);
  }
}

// 跟随系统的用户：系统换季我们跟着换（偏好锁定 light/dark 时不听）
DARK_MQ?.addEventListener?.('change', () => { if (themePref() === 'system') applyTheme(); });

// ── 插件主题（v3 形态 A）─────────────────────────────────────────
// 插件贡献的调色板叠加在内置浅/深之上：选中槽位铺到 <html> 行内样式
// （行内 > :root / 深色块，故叠加必然赢），换回原生时把上一轮铺的
// 逐一撤下——绝不停在半套主题上。清单由 app.js 在插件装载完后喂进
// 来（setPluginThemes）；槽位白名单与颜色合法性在宿主装载面已把关。

let pluginThemeList = [];
let themedSlots = [];

export function setPluginThemes(list) {
  pluginThemeList = Array.isArray(list) ? list : [];
}

export function pluginThemePref() { return prefs().themePlugin || ''; }

function applyPluginThemeOverlay() {
  for (const s of themedSlots) document.documentElement.style.removeProperty(s);
  themedSlots = [];
  const want = pluginThemeList.find((t) => t.key === pluginThemePref());
  if (!want) return;
  for (const [slot, val] of Object.entries(want.vars || {})) {
    document.documentElement.style.setProperty(slot, val);
    themedSlots.push(slot);
  }
}

// ── 通知与提示音 ──────────────────────────────────────────────────
// announce 是右上角通知提醒框（Notify）的总闸：横幅关了就不打扰（提示音
// 也一并静默——没有横幅就别响）；开着则横幅＋（偏好允许时的）一声轻叮。
// toast 是操作回执（用户自己刚做的动作的答复），不走这道闸。
//
// 音色与节流住在 sound.js（声音交互篇）：新消息微信式叮咚、有人@我
// 叮铃、横幅轻叮三枚，各带冷却窗——HTMLAudio＋内联 WAV 的选型理由
// （无音频栈环境不冻屏、自动播放策略下静默让行）见那边头注。氛围音（scene- 前缀九枚）另有
// 独立闸 sceneSound——下面的「办公室氛围音」行只管它，总闸仍管全部。

import { sfx, volumePref } from './sound.js';

/** 门控后的通知提醒框：偏好关横幅 → 什么都不出。返回 Notify 的句柄
 *  （被闸下时 null），调用方无需自检偏好。 */
export function announce(opts) {
  if (prefs().notifyBanner === false) return null;
  const handle = Notify.show(opts);
  sfx('banner');
  return handle;
}

// ── 设置窗 ────────────────────────────────────────────────────────

export class SettingsWin {
  /** 聊天记录保留期真源缓存（null = 未读到）；#retBusy 一次设置在途防重入 */
  #retDays = null;
  #retBusy = false;
  /** 侧栏即时刷新的平台位（null = 未读到；GET /reveal 的 darwin）——
   *  行标题与 toast 按平台说人话：信任框代价只在 macOS 存在 */
  #revealDarwin = null;
  /** token 预算已加载真值（r_26；输入框成对覆写时另一位带这个，不带
   *  框里未落库的草稿值；读挂前双零） */
  #budget = { day: 0, week: 0 };
  /** 自动补货的「本次目标」缓存（目标制补货；'' = 未设/未读到）——
   *  行内回显与重开确认框的预填共用一个真源 */
  #apGoal = '';
  /** 当前房 git 设置的基线分支缓存（开关翻转时原样回送，不覆盖） */
  #gitBase = '';

  /**
   * @param {Object} [opts]
   * @param {() => {mode: string, label: string, roomName: string, note?: string}} [opts.conn]
   *   the connection verdict snapshot (app.js's connState keeps it fresh)
   * @param {(text: string, kind?: string) => void} [opts.toast] copy feedback
   * @param {() => void} [opts.onManual] 「查看说明书」— app.js jumps the kb board
   * @param {(anchor: HTMLElement) => void} [opts.onClock] 「房间时间」行 —
   *   app.js hands the row to openClockPop（表盘面板浮在设置窗之上，
   *   窗保持开着，锚点行在文档里稳稳不动）
   * @param {() => {key: string, name: string}|null} [opts.project] 当前房
   *   （app.js 从 RoomBook 取，大厅一视同仁）——「智能」面板的全智能模
   *   式开关作用于它；null（没有当前房，仅启动早期）时整行置灰。
   */
  constructor(opts = {}) {
    this.opts = opts;
    this.el = null;   // the open wrap（null = closed）
    this.layerOff = null; // 浮层栈注销器（Esc 只关最顶层——嵌套对话框先于本窗）
  }

  /** @param {HTMLElement} [anchor] the clicked gear（居中模态不锚定，
   *  形参只为调用方签名稳定） */
  openFor(anchor) {
    void anchor;
    if (this.el) return;
    const c = this.opts.conn?.() || { mode: 'offline', label: t('离线'), roomName: '' };
    const p = prefs();
    const theme = themePref();
    const pTheme = pluginThemePref();
    const lang = langPref();
    const el = document.createElement('div');
    el.className = 'set-wrap';
    el.innerHTML =
      '<div class="set-win" role="dialog" aria-modal="true" aria-label="' + esc(t('设置')) + '">' +
      '<header class="sw-head"><strong>' + esc(t('设置')) + '</strong>' +
      '<button type="button" class="fb-x" aria-label="' + esc(t('关闭')) + '">' + icon('close', 14) + '</button>' +
      '</header>' +
      '<div class="sw-body">' +
      '<nav class="sw-nav" aria-label="' + esc(t('设置分类')) + '">' +
      PANES.map((pn) =>
        // 图标 20（导航菜单篇·Desktop 侧边导航：菜单项图标 20×20）
        `<button type="button" data-sw-nav="${esc(pn.key)}">${icon(pn.icon, 20)}<span>${esc(pn.label)}</span></button>`).join('') +
      '</nav>' +
      '<div class="sw-main">' +

      // ── 通用 ──
      '<section class="sw-pane" data-sw-pane="gen" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('通用')) + '</h3>' +
      // 语言行（中/英分段器，主题行同款面孔）：t()/S() 都在启动时定语
      // 言，运行时不热换——点选即落偏好＋通知后端＋整页重载
      '<div class="st-row" title="' + esc(t('界面与后台文案的语言（切换后整页重载；未选择时跟随系统）')) + '">' +
        '<span class="st-k">' + esc(t('语言')) + '</span>' +
        '<span class="st-seg" role="group" aria-label="' + esc(t('语言')) + '">' +
          `<button type="button" data-st-lang="zh" aria-pressed="${lang === 'zh'}">中文</button>` +
          `<button type="button" data-st-lang="en" aria-pressed="${lang === 'en'}">English</button>` +
        '</span>' +
      '</div>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('进入对话时聚焦输入框')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="true" ' +
          'title="' + esc(t('进入对话板块时自动聚焦发言输入井')) + '" data-st-focus></button>' +
      '</div>' +
      '<button type="button" class="st-row st-copy" data-st-keys title="' + esc(t('查看键盘操作一览')) + '">' +
        `<span class="st-k">${esc(t('快捷键'))}</span><span class="st-v">${icon('right', 14)}</span>` +
      '</button>' +
      '</section>' +

      // ── 项目 ──（按项目的服务端开关，作用域＝当前房，智能面板同款）
      '<section class="sw-pane" data-sw-pane="proj" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('项目')) + '</h3>' +
      '<div class="st-row" data-st-gitrow title="' + esc(t('作用于当前所在的项目房：关掉后版本管理面板只读，分支隔离、合并流、提交播报、自动建枝全停（仓库与既有提交不受影响）；挂着分支座位的成员在重新打开前无法重生会话')) + '">' +
        '<span class="st-k">' + esc(t('git 版本管理')) + '</span>' +
        '<span class="dim small" data-st-gitname></span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-git></button>' +
      '</div>' +
      '<div class="st-row" data-st-gitautorow title="' + esc(t('作用于当前所在的项目房：给成员设分支遇到不存在的分支时不再拒绝——自动从基线切出（只建不删）。总开关关着时这项不生效')) + '">' +
        '<span class="st-k">' + esc(t('成员分支自动新建')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-gitauto></button>' +
      '</div>' +
      '<div class="st-row" data-st-gitseatrow title="' + esc(t('作用于当前所在的项目房：成员出生即自动各驻专属枝 wt/<人名>（自基线切出）；打开时会立即给现有无分支成员补驻并迁移其会话（历史/任务无损续接）。总开关关着时这项不生效')) + '">' +
        '<span class="st-k">' + esc(t('成员自动驻分支')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-gitseat></button>' +
      '</div>' +
      '<div class="st-row" data-st-gitbaserow title="' + esc(t('作用于当前所在的项目房：分支隔离与自动建枝的出发点，留空＝main')) + '">' +
        '<span class="st-k">' + esc(t('基线分支')) + '</span>' +
        '<input type="text" maxlength="60" placeholder="main" data-st-gitbase aria-label="' + esc(t('基线分支')) + '" spellcheck="false">' +
        '<button type="button" class="st-ret-set" data-st-gitbaseset>' + esc(t('设')) + '</button>' +
      '</div>' +
      '<p class="st-note">' + esc(t('四行都作用于当前所在的项目房（行内小签即名字）；还没有所在房时置灰——进任意一间房后再来。')) + '</p>' +
      '</section>' +

      // ── 外观 ──
      '<section class="sw-pane" data-sw-pane="look" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('外观')) + '</h3>' +
      '<div class="st-row" title="' + esc(t('整窗配色：深色换夜间灰阶，跟随系统听 OS 换季')) + '">' +
        '<span class="st-k">' + esc(t('主题')) + '</span>' +
        '<span class="st-seg" role="group" aria-label="' + esc(t('主题')) + '">' +
          `<button type="button" data-st-theme="light" aria-pressed="${theme === 'light'}">` + esc(t('浅色')) + '</button>' +
          `<button type="button" data-st-theme="dark" aria-pressed="${theme === 'dark'}">` + esc(t('深色')) + '</button>' +
          `<button type="button" data-st-theme="system" aria-pressed="${theme === 'system'}">` + esc(t('跟随系统')) + '</button>' +
        '</span>' +
      '</div>' +
      (pluginThemeList.length ?
        '<div class="st-row" title="' + esc(t('插件贡献的调色板，叠加在浅/深底色之上（v3 插件·形态 A）')) + '">' +
          '<span class="st-k">' + esc(t('插件主题')) + '</span>' +
          '<span class="st-seg" role="group" aria-label="' + esc(t('插件主题')) + '">' +
            `<button type="button" data-st-ptheme="" aria-pressed="${!pTheme}">` + esc(t('原生')) + '</button>' +
            pluginThemeList.map((tp) =>
              `<button type="button" data-st-ptheme="${esc(tp.key)}" aria-pressed="${pTheme === tp.key}">${esc(tp.name)}</button>`).join('') +
          '</span>' +
        '</div>' : '') +
      '</section>' +

      // ── 通知 ──
      '<section class="sw-pane" data-sw-pane="notify" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('通知')) + '</h3>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('新消息系统通知')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="true" ' +
          'title="' + esc(t('不在眼前的房来了新对话时弹系统级通知横幅（微信/飞书式，点击跳去该房）；开启当场发一条测试通知。浏览器需允许本站通知，应用窗口需在 系统设置→通知 允许')) + '" data-st-msgpop></button>' +
      '</div>' +
      '<div class="st-row" data-st-authrow title="' + esc(t('macOS／浏览器对本应用通知的现行授权——每次打开设置现查，系统设置里改的主意立刻可见。被拒时系统不再弹问询，只有这里说实话')) + '">' +
        '<span class="st-k">' + esc(t('系统授权')) + '</span>' +
        '<span class="st-v" data-st-auth>…</span>' +
      '</div>' +
      '<div class="st-hr" role="separator"></div>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('提示音')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="true" ' +
          'title="' + esc(t('新消息微信式叮咚、有人@我叮铃、事件横幅轻叮（各有冷却窗防连响）')) + '" data-st-sound></button>' +
      '</div>' +
      '<div class="st-row st-vol" title="' + esc(t('全部提示音的音量（消息叮咚、点名叮铃、横幅叮）——松手按新音量试听一声')) + '">' +
        '<span class="st-k">' + esc(t('音量')) + '</span>' +
        '<input type="range" min="0" max="100" step="5" data-st-volume aria-label="' + esc(t('提示音音量')) + '">' +
        '<span class="st-volv" data-st-volv>50%</span>' +
      '</div>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('办公室氛围音')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="true" ' +
          'title="' + esc(t('像素办公室的生活动静（冒泡啵、成团私语、打印机吱、零食咔嚓、保洁刷刷、接水汩汩、任务落地叮、垃圾桶盖叭）——世界常转，人在别的板块也听得见动静却看不见画面；关闭只静这些，消息叮咚/点名叮铃不受影响')) + '" data-st-scene></button>' +
      '</div>' +
      '<div class="st-hr" role="separator"></div>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('横幅通知')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="true" ' +
          'title="' + esc(t('他房公告更新等事件的右上角提醒横幅')) + '" data-st-banner></button>' +
      '</div>' +
      '</section>' +

      // ── 智能 ──（r_19：单一「全智能模式」拆成双开关——补货/推进
      // 各自独立开启，双开＝旧口径）
      '<section class="sw-pane" data-sw-pane="auto" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('智能')) + '</h3>' +
      '<div class="st-row" data-st-stockrow>' +
        '<span class="st-k">' + esc(t('自动补货')) + '</span>' +
        '<span class="st-v" data-st-apname>…</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-autostock ' +
          'title="' + esc(t('需求池 open 低于蓄水目标线（默认 3 条）时自动注入【自动驾驶·选题】给编排者补货——先蓄需求不急开工，立到目标线即停（忙闲皆然）。开启须填本次目标：目标未达成前编排者不得以「本期无新需求」应付；判定达成后自动关闭补货与推进。耗 token，开启需确认。作用于哪间项目房看行内小签')) + '">' +
        '</button>' +
      '</div>' +
      '<p class="st-note" data-st-goalline hidden>' + esc(t('本次目标')) + '：<span data-st-goaltxt></span></p>' +
      '<div class="st-row" data-st-advrow>' +
        '<span class="st-k">' + esc(t('自动推进')) + '</span>' +
        '<span class="st-v" data-st-advname>…</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-autoadvance ' +
          'title="' + esc(t('代行不失聪的推进半边：提案满宽限自动接受、闲置+池有货自动拆解、停滞任务自动点名续做、HR 定时产能巡报；你在场（本房近 10 分钟内发言/点卡）时代收与提问放行让位等你点选，不在场时提问卡先给约 2 分钟点选窗、满窗按合理假设放行——很耗 token，开启需确认。作用于哪间项目房看行内小签')) + '">' +
        '</button>' +
      '</div>' +
      '<p class="st-note">' + esc(t('双开＝旧「全智能模式」（完全不介入）；只开补货＝池子自动蓄水、提案仍由你审；只开推进＝池内存货自动往前推、不进新货。都很耗 token，按项目开启，作用域见行内小签。')) + '</p>' +
      '<div class="st-hr" role="separator"></div>' +
      // 五旋钮（r_12/t_172 ＋ r_19 water_target）：滑杆＋域值说明＋当前
      // 生效值回显（env 兜底在起作用时带角标）；改值即热生效（toast 反馈）
      '<div class="st-knob" data-st-knob="water_target" data-st-unit="' + esc(t('条')) + '" data-st-min="1" data-st-max="9" ' +
        'title="' + esc(t('蓄水目标线：需求池常备几条 open 需求（补货侧）——低于此线即触发选题补货，巡报的「⚠ 水位偏低」也按它判（1–9，默认 3）')) + '">' +
        '<span class="st-k">' + esc(t('蓄水目标线')) + '</span><span class="st-knob-v" data-st-knobv>…</span>' +
        '<input type="range" min="1" max="9" step="1" data-st-knobin aria-label="' + esc(t('蓄水目标线（条）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('补货侧')) + '</span>' +
      '</div>' +
      '<div class="st-knob" data-st-knob="poke_every" data-st-unit="' + esc(t('分钟')) + '" data-st-min="5" data-st-max="120" ' +
        'title="' + esc(t('多久补货选题一次（需求池低于蓄水目标线时注入——补货侧）——同时是拆解注入与被拒退避的间隔基准（调大=更保守）')) + '">' +
        '<span class="st-k">' + esc(t('选题间隔')) + '</span><span class="st-knob-v" data-st-knobv>…</span>' +
        '<input type="range" min="5" max="120" step="5" data-st-knobin aria-label="' + esc(t('选题间隔（分钟）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('调大=更保守')) + '</span>' +
      '</div>' +
      '<div class="st-knob" data-st-knob="accept_delay" data-st-unit="' + esc(t('秒')) + '" data-st-min="30" data-st-max="300" ' +
        'title="' + esc(t('提案满宽限自动代收前的否决窗口（推进侧）——留给你喊停的时间（调大=更保守）')) + '">' +
        '<span class="st-k">' + esc(t('否决窗口')) + '</span><span class="st-knob-v" data-st-knobv>…</span>' +
        '<input type="range" min="30" max="300" step="10" data-st-knobin aria-label="' + esc(t('否决窗口（秒）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('调大=更保守')) + '</span>' +
      '</div>' +
      '<div class="st-knob" data-st-knob="stall_delay" data-st-unit="' + esc(t('分钟')) + '" data-st-min="10" data-st-max="60" ' +
        'title="' + esc(t('进行中任务多久没动静就自动点名续做（推进侧；调大=更宽容）')) + '">' +
        '<span class="st-k">' + esc(t('停滞唤醒阈')) + '</span><span class="st-knob-v" data-st-knobv>…</span>' +
        '<input type="range" min="10" max="60" step="5" data-st-knobin aria-label="' + esc(t('停滞唤醒阈（分钟）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('调大=更宽容')) + '</span>' +
      '</div>' +
      '<div class="st-knob" data-st-knob="max_plans" data-st-unit="' + esc(t('份')) + '" data-st-min="0" data-st-max="99" ' +
        'title="' + esc(t('每天最多自动接受多少份提案（推进侧）——熔断保护，防无人值守跑飞（0=不限；熔断只关推进、补货不受影响）')) + '">' +
        '<span class="st-k">' + esc(t('每日上限')) + '</span><span class="st-knob-v" data-st-knobv>…</span>' +
        '<input type="range" min="0" max="99" step="1" data-st-knobin aria-label="' + esc(t('每日自动接受上限（份，0=不限）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('0=不限')) + '</span>' +
      '</div>' +
      // token 预算双输入框（r_26，工作室级钱面——与五旋钮同面孔不同真
      // 源：五旋钮按项目走 setAutopilot，这两位走 /budget 整对覆写）：到
      // 额熔断只关推进（与每日代收上限同语义），窗口＝本地自然日/自然
      // 周。金额面手敲比滑杆准，且带预设单位（中文万/英文 M，budgetMul
      // 换算）——敲 500 就是 500 万 token；取值口径＝服务端：0=不限，
      // 1–1e12 token（上限随单位换算进 max）。
      `<div class="st-knob" data-st-budget="day" ` +
        'title="' + esc(t('工作室每天 token 耗粮上限（今日零点起算，全室合计）——到额即熔断自动推进（只关推进，补货与人力操作不受影响），明日零点窗口重置。0=不限')) + '">' +
        '<span class="st-k">' + esc(t('每日 token 预算')) + '</span>' +
        '<input type="number" class="st-budget-in" min="0" max="' + (1e12 / budgetMul()) + '" step="1" placeholder="0" data-st-budgetin="day" aria-label="' + esc(t('每日 token 预算（万，0=不限）')) + '">' +
        '<span class="st-knob-unit">' + esc(t('万')) + '</span>' +
        '<span class="st-knob-v" data-st-budgetv>…</span>' +
        '<span class="st-knob-hint">' + esc(t('0=不限')) + '</span>' +
      '</div>' +
      `<div class="st-knob" data-st-budget="week" ` +
        'title="' + esc(t('工作室每周 token 耗粮上限（本周一零点起算，全室合计）——到额即熔断自动推进（只关推进），下周一零点窗口重置。0=不限')) + '">' +
        '<span class="st-k">' + esc(t('每周 token 预算')) + '</span>' +
        '<input type="number" class="st-budget-in" min="0" max="' + (1e12 / budgetMul()) + '" step="1" placeholder="0" data-st-budgetin="week" aria-label="' + esc(t('每周 token 预算（万，0=不限）')) + '">' +
        '<span class="st-knob-unit">' + esc(t('万')) + '</span>' +
        '<span class="st-knob-v" data-st-budgetv>…</span>' +
        '<span class="st-knob-hint">' + esc(t('0=不限')) + '</span>' +
      '</div>' +
      // 消息注入等待（工作室级，与五旋钮同面孔不同真源）：五旋钮是按项
      // 目的自动补货/推进参数（setAutopilot＋AUTOPILOT 令牌），这一位管的是调
      // 度器投递层——session/send 等 ack 多久才判「结果未知」弃单播报。
      // 一个 app-server 驱动全部房间，所以工作室级；大厅也能调；写后
      // 热生效（下一次投递即按新值等）。缺省 300 秒＝5 分钟。
      '<div class="st-knob" data-st-swwait data-st-unit="" data-st-min="10" data-st-max="3600" ' +
        'title="' + esc(t('给成员注入消息后等回应（ack）的时长——超时按「结果未知」放弃该条并提示重发（不重试，防同一条指令在下一轮重放）。会话繁忙/冷启动 ack 偏慢时调长；代价只是真死会话的播报晚一点')) + '">' +
        '<span class="st-k">' + esc(t('消息注入等待')) + '</span><span class="st-knob-v" data-st-swv>…</span>' +
        '<input type="range" min="10" max="3600" step="10" data-st-swin aria-label="' + esc(t('消息注入等待（秒）')) + '">' +
        '<span class="st-knob-hint">' + esc(t('调大=更耐等')) + '</span>' +
      '</div>' +
      '<div class="st-hr" role="separator"></div>' +
      // AI 重编译投票（按项目的服务端开关，与双开关同族但管的是重启权
      // 面）：成员提议→全房投票→超半数即自动 rebuild。默认关，开启走
      // REBUILD 令牌的警告对话框。
      '<div class="st-row" data-st-rvoterow>' +
        '<span class="st-k">' + esc(t('AI 重编译投票')) + '</span>' +
        '<span class="st-v" data-st-rvotename>…</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-rvote ' +
          'title="' + esc(t('允许本房 AI 成员发起重编译投票：成员回复「提议重编译」开场，全房超半数同意即自动重新编译并重启工作室。高权面（AI 可重启全室），开启需确认。作用于哪间项目房看行内小签')) + '">' +
        '</button>' +
      '</div>' +
      '<p class="st-note">' + esc(t('AI 重编译投票按项目开启：开着时本房成员可提议 rebuild，全房投票超半数同意即立即自动执行（编译期间照常运行、完成后自动重启）；关闭即作废在投的票。')) + '</p>' +
      '<div class="st-hr" role="separator"></div>' +
      // 允许成员自制技能（工作室级服务端开关，随技能库落盘）：开着时
      // 成员的回复里出现 niuma-skill 围栏块即入库（协议说明随下次注入
      // 教给成员）；关着则忽略并回一行说明。
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('允许成员自制技能')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-modelskill ' +
          'title="' + esc(t('工作室级开关：开启后成员可在回复中写 niuma-skill 围栏块把工作套路固化成技能（key 不可改名、重名整体替换、创建人可追溯）；拦截即时生效，协议说明随成员下次注入到达。关闭后此类块被忽略并回一行说明')) + '">' +
        '</button>' +
      '</div>' +
      '<p class="st-note">' + esc(t('技能库是全工作室共享的——任何房的成员自制技能都会进同一座库。收录不设房主审批（开关即门槛），创建人记录可追溯，删除走技能库页。')) + '</p>' +
      '</section>' +

      // ── 数据 ──
      '<section class="sw-pane" data-sw-pane="data" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('数据')) + '</h3>' +
      '<div class="st-row" title="' + esc(t('聊天记录在磁盘上的保留时长——超期的历史消息被定期清理（聊天窗口本身只显示最近几百条，这里是落盘文件的寿命）')) + '">' +
        '<span class="st-k">' + esc(t('聊天记录保留')) + '</span>' +
        '<span class="st-v" data-st-retv>…</span>' +
      '</div>' +
      '<div class="st-ret-opts" data-st-retopts>' +
        '<span class="st-ret-seg" role="group" aria-label="' + esc(t('聊天记录保留期')) + '">' +
          '<button type="button" data-st-ret="1">' + esc(t('1天')) + '</button>' +
          '<button type="button" data-st-ret="3">' + esc(t('3天')) + '</button>' +
          '<button type="button" data-st-ret="7">' + esc(t('7天')) + '</button>' +
          '<button type="button" data-st-ret="15">' + esc(t('15天')) + '</button>' +
          '<button type="button" data-st-ret="30">' + esc(t('30天')) + '</button>' +
          '<button type="button" data-st-ret="0">' + esc(t('永久')) + '</button>' +
          '<span class="st-ret-custom">' +
            '<input type="number" min="1" max="3650" placeholder="' + esc(t('自定义')) + '" data-st-retin aria-label="' + esc(t('自定义保留天数')) + '">' +
            '<span class="st-ret-unit">' + esc(t('天')) + '</span>' +
            '<button type="button" class="st-ret-set" data-st-retset>' + esc(t('设')) + '</button>' +
          '</span>' +
        '</span>' +
      '</div>' +
      '<p class="st-note">' + esc(t('缩短保留期会立即删除超期消息（需确认）；延长与转永久不删任何东西。')) + '</p>' +
      '</section>' +

      // ── 连接 ──
      '<section class="sw-pane" data-sw-pane="conn" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('连接')) + '</h3>' +
      `<div class="st-row st-conn" data-mode="${esc(c.mode)}">` +
        '<i class="st-dot" aria-hidden="true"></i>' +
        `<span class="st-label">${esc(c.label)}</span>` +
        (c.roomName ? `<span class="st-room">${esc(c.roomName)}</span>` : '') +
      '</div>' +
      '<button type="button" class="st-row st-copy" data-st-clock title="' + esc(t('点开时间面板（表盘＋服务器对齐态）')) + '">' +
        '<span class="st-k">' + esc(t('房间时间')) + '</span><span class="st-v" data-st-clockv></span>' +
      '</button>' +
      '<button type="button" class="st-row st-copy" data-st-copy title="' + esc(t('点击复制工作室地址')) + '">' +
        '<span class="st-k">' + esc(t('服务器')) + '</span><span class="st-v" data-st-origin></span>' +
      '</button>' +
      '<div class="st-hr" role="separator"></div>' +
      '<div class="st-row">' +
        '<span class="st-k">' + esc(t('ZCode 侧栏即时刷新')) + '</span>' +
        '<button type="button" class="sw" role="switch" aria-checked="false" disabled data-st-reveal></button>' +
      '</div>' +
      '<p class="st-note">' + esc(t('牛马/小助手的侧栏行写在 ZCode 的索引库里，桌面端不会自己重读。开着：每有注册/置顶就递一条「打开工作区」投递，行即时浮现（macOS 走深链，ZCode ≥3.14 每次投递弹一次信任框——Dock 不留图标的唯一通道；Windows/Linux 零弹窗）；关着：行照常落库，等侧栏下次被碰到（搜索/点开任务/重启 ZCode）才显现。开启的这一次会马上强投，把已在库里置顶的行当场拉回屏幕。')) + '</p>' +
      '</section>' +

      // ── 维护 ──
      '<section class="sw-pane" data-sw-pane="maint" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('维护')) + '</h3>' +
      '<button type="button" class="st-row" data-st-rebuild ' +
        'title="' + esc(t('从源码重新编译本应用并重启（需要本机有源码仓库与 Go 工具链；编译失败应用原地不动；前端改动无需编译，重新载入即得）')) + '">' +
        `${icon('refresh', 14)}<span class="st-k">` + esc(t('重新编译并重启…')) + '</span>' +
      '</button>' +
      '<button type="button" class="st-row" data-st-reload title="' + esc(t('重载前端页面（前端改动无需编译）')) + '">' +
        `${icon('down', 14)}<span class="st-k">` + esc(t('重新载入')) + '</span>' +
      '</button>' +
      '<button type="button" class="st-row st-danger" data-st-reset ' +
        'title="' + esc(t('删除这台机器上工作室的全部数据，恢复到刚安装时的状态（需输入确认）')) + '">' +
        `${icon('trash', 14)}<span class="st-k">` + esc(t('重置工作室…')) + '</span>' +
      '</button>' +
      '</section>' +

      // ── 关于 ──
      '<section class="sw-pane" data-sw-pane="about" hidden>' +
      '<h3 class="sw-pane-t">' + esc(t('关于')) + '</h3>' +
      '<div class="st-row"><span class="st-k">' + esc(t('版本')) + '</span><span class="st-v" data-st-version>…</span></div>' +
      '<button type="button" class="st-row st-copy" data-st-manual title="' + esc(t('打开内嵌的使用说明书（黑板板块）')) + '">' +
        `<span class="st-k">` + esc(t('查看说明书')) + `</span><span class="st-v">${icon('right', 14)}</span>` +
      '</button>' +
      '<button type="button" class="st-row st-copy" data-st-chronicle title="' + esc(t('打开工作室志（黑板板块）——需求线、里程碑与大事的编年史')) + '">' +
        `<span class="st-k">` + esc(t('工作室志')) + `</span><span class="st-v">${icon('right', 14)}</span>` +
      '</button>' +
      '<button type="button" class="st-row st-copy" data-st-guide title="' + esc(t('重看办公室的三步观察引导：点牛马看名片、点物件有反馈、滚轮凑近看')) + '">' +
        `<span class="st-k">` + esc(t('再看一遍引导')) + `</span><span class="st-v">${icon('right', 14)}</span>` +
      '</button>' +
      // 落款（作者署名＋特别鸣谢）：不分组不抢戏，两行小字居中收尾
      // ——智谱供模型的智力，飞书供界面的语言，各有其主
      '<div class="st-credit">' +
        '<div>' + esc(t('Niuma Studio · 作者 3StripFish（三文鱼）')) + '</div>' +
        '<div>' + esc(t('特别鸣谢 智谱 · 飞书')) + '</div>' +
      '</div>' +
      '</section>' +

      '</div></div></div>';
    document.body.appendChild(el);
    this.el = el;
    el.querySelector('[data-st-origin]').textContent = location.origin;
    el.querySelector('[data-st-focus]').setAttribute('aria-checked', String(p.focusComposer !== false));
    el.querySelector('[data-st-msgpop]').setAttribute('aria-checked', String(p.msgPopup !== false));
    this.#refreshAuth(el.querySelector('[data-st-auth]'), el.querySelector('[data-st-authrow]'));
    el.querySelector('[data-st-banner]').setAttribute('aria-checked', String(p.notifyBanner !== false));
    el.querySelector('[data-st-sound]').setAttribute('aria-checked', String(p.notifySound !== false));
    el.querySelector('[data-st-scene]').setAttribute('aria-checked', String(p.sceneSound !== false));
    // 音量滑杆（开关篇同款立即生效）：初值＝偏好（缺省 50）；input 直写
    // 偏好并画填充轨，change（松手）按新音量试听一声——耳朵校准，不猜
    const volIn = el.querySelector('[data-st-volume]');
    const volLabel = el.querySelector('[data-st-volv]');
    const volPaint = (v) => {
      volLabel.textContent = `${v}%`;
      volIn.style.setProperty('--fill', `${v}%`); // WebKit 填充轨（见 index.html 滑杆样式）
    };
    volIn.value = String(Math.round(volumePref() * 100));
    volPaint(Number(volIn.value));
    volIn.addEventListener('input', () => {
      const v = Number(volIn.value);
      setPref('notifyVolume', v);
      volPaint(v);
    });
    volIn.addEventListener('change', () => sfx('msg', { force: true }));
    // 四旋钮（r_12/t_172）：input 画实时值（不动库），change（松手）才
    // 带令牌 POST——与音量滑杆同款「先看效果再落」节奏
    for (const rowEl of el.querySelectorAll('[data-st-knob]')) {
      const inEl = rowEl.querySelector('[data-st-knobin]');
      const valEl = rowEl.querySelector('[data-st-knobv]');
      if (!inEl) continue;
      const unit = rowEl.dataset.stUnit || '';
      const knobFill = (v) => {
        const min = Number(rowEl.dataset.stMin || 0), max = Number(rowEl.dataset.stMax || 100);
        inEl.style.setProperty('--fill', `${max > min ? Math.round(((v - min) / (max - min)) * 100) : 50}%`);
      };
      knobFill(Number(inEl.value));
      inEl.addEventListener('input', () => {
        const v = Number(inEl.value);
        if (valEl) valEl.textContent = v === 0 && rowEl.dataset.stKnob === 'max_plans' ? t('不限') : `${v} ${unit}`.trim();
        knobFill(v);
      });
      inEl.addEventListener('change', () => this.#knobChanged(inEl, rowEl));
    }
    // 消息注入等待（工作室级第五位，独立真源）：同款「input 画实时值、
    // change 落库」节奏，但 POST /dispatch-wait（无令牌——不是 autopilot
    // 域的值），秒数按 60 整除进位成分钟念给人听
    const swRow = el.querySelector('[data-st-swwait]');
    const swIn = swRow?.querySelector('[data-st-swin]');
    if (swRow && swIn) {
      const swFill = (v) => {
        const min = Number(swRow.dataset.stMin || 0), max = Number(swRow.dataset.stMax || 100);
        swIn.style.setProperty('--fill', `${max > min ? Math.round(((v - min) / (max - min)) * 100) : 50}%`);
      };
      swIn.addEventListener('input', () => {
        const v = Number(swIn.value);
        const valEl = swRow.querySelector('[data-st-swv]');
        if (valEl) valEl.textContent = fmtWait(v);
        swFill(v);
      });
      swIn.addEventListener('change', () => this.#sendWaitChanged(swIn, swRow));
    }
    // token 预算双输入框（工作室级钱面，预设单位万/M）：输入时右侧读数
    // 实时折回 token 念人话（亿/B 级读数靠它确认量级），change（失焦/
    // 回车）整对落库——POST /budget 成对覆写（一份预算一个策略），改的
    // 是哪个框换哪位，另一位带上已加载真值。
    for (const inEl of el.querySelectorAll('[data-st-budgetin]')) {
      const rowEl = inEl.closest('[data-st-budget]');
      inEl.addEventListener('input', () => {
        if (inEl.value === '') return; // 清空到一半不闪读数；失焦由校验驳回
        const tok = Number(inEl.value) * budgetMul();
        const valEl = rowEl.querySelector('[data-st-budgetv]');
        if (valEl && Number.isFinite(tok)) valEl.textContent = tok === 0 ? t('不限') : fmtTokens(tok);
      });
      inEl.addEventListener('change', () => this.#budgetChanged(inEl, rowEl));
    }
    el.addEventListener('click', (ev) => {
      const navBtn = ev.target.closest('[data-sw-nav]');
      if (navBtn) { this.#showPane(navBtn.dataset.swNav); return; }
      if (ev.target === el) { this.close(); return; } // 遮罩点击收窗
      if (ev.target.closest('.fb-x')) { this.close(); return; }
      if (ev.target.closest('[data-st-copy]')) { this.#copy(); return; }
      if (ev.target.closest('[data-st-clock]')) {
        // 表盘面板浮在窗上（clock-pop 的 z 高于遮罩），窗不收——行在
        // 文档里稳稳不动，锚点比气泡卡时代更可靠
        this.opts.onClock?.(el.querySelector('[data-st-clock]'));
        return;
      }
      const themeBtn = ev.target.closest('[data-st-theme]');
      if (themeBtn) {
        // 立即换季（窗不收——背后整窗当场变色，所见即所得）
        setPref('theme', themeBtn.dataset.stTheme);
        for (const b of el.querySelectorAll('[data-st-theme]')) {
          b.setAttribute('aria-pressed', String(b === themeBtn));
        }
        applyTheme();
        return;
      }
      // 语言行：与主题行同一副面孔、不同的生效方式——t()/S() 都在启动
      // 时定语言，运行时不热换。点选即落偏好＋通知后端＋整页重载（同
      // 语点击只补一发后端对齐推送、不重载——setLang 自判）。
      const langBtn = ev.target.closest('[data-st-lang]');
      if (langBtn) {
        setLang(langBtn.dataset.stLang);
        return;
      }
      const pThemeBtn = ev.target.closest('[data-st-ptheme]');
      if (pThemeBtn) {
        // 插件主题叠加（窗不收，整窗当场换装；「原生」＝撤下叠加回到内置浅/深）
        setPref('themePlugin', pThemeBtn.dataset.stPtheme || '');
        for (const b of el.querySelectorAll('[data-st-ptheme]')) {
          b.setAttribute('aria-pressed', String(b === pThemeBtn));
        }
        applyTheme();
        return;
      }
      // 自动补货/自动推进双开关（r_19）：真源在服务端（警告对话框→带
      // 确认令牌的 POST→回执渲染），不能走下面的乐观翻转分支——先于
      // .sw 拦截
      const apStock = ev.target.closest('[data-st-autostock]');
      if (apStock) { this.#toggleAutoFeature(apStock, 'stock'); return; }
      const apAdv = ev.target.closest('[data-st-autoadvance]');
      if (apAdv) { this.#toggleAutoFeature(apAdv, 'advance'); return; }
      // git 版本管理双开关（「项目」面板）：真源在服务端（关闭方向带确
      // 认→POST /p/{key}/git/settings→回读渲染），不走乐观翻转
      const gitSw = ev.target.closest('[data-st-git]');
      if (gitSw) { this.#toggleGitSwitch(gitSw, 'master'); return; }
      const gitAuto = ev.target.closest('[data-st-gitauto]');
      if (gitAuto) { this.#toggleGitSwitch(gitAuto, 'autoseat'); return; }
      const gitSeat = ev.target.closest('[data-st-gitseat]');
      if (gitSeat) { this.#toggleGitSwitch(gitSeat, 'seat'); return; }
      if (ev.target.closest('[data-st-gitbaseset]')) { this.#applyGitBase(); return; }
      // AI 重编译投票同族：真源在服务端（REBUILD 令牌的警告对话框→
      // POST→回执渲染），也不走乐观翻转
      const rvVote = ev.target.closest('[data-st-rvote]');
      if (rvVote) { this.#toggleRebuildVote(rvVote); return; }
      // 允许成员自制技能同族：真源在服务端（skills.json 的开关位→POST
      // 回执渲染），不走乐观翻转
      const mkSw = ev.target.closest('[data-st-modelskill]');
      if (mkSw) { this.#toggleModelSkills(mkSw); return; }
      // 侧栏即时刷新同族：真源在服务端（reveal.json→POST 回执渲染），
      // 也不走乐观翻转
      const rvSw = ev.target.closest('[data-st-reveal]');
      if (rvSw) { this.#toggleReveal(rvSw); return; }
      const sw = ev.target.closest('.sw');
      if (sw) {
        const on = sw.getAttribute('aria-checked') !== 'true';
        sw.setAttribute('aria-checked', String(on));
        if (sw.hasAttribute('data-st-focus')) setPref('focusComposer', on); // 开关篇：立即生效，无确定钮
        if (sw.hasAttribute('data-st-msgpop')) {
          setPref('msgPopup', on);
          if (on) this.#testNotify(); // 开启当场发一条测试通知——看得到才叫开了
        }
        if (sw.hasAttribute('data-st-banner')) setPref('notifyBanner', on);
        if (sw.hasAttribute('data-st-sound')) {
          setPref('notifySound', on);
          if (on) sfx('msg', { force: true }); // 开提示音当场试听一枚新消息叮咚
        }
        if (sw.hasAttribute('data-st-scene')) {
          setPref('sceneSound', on);
          if (on) sfx('scene-pop', { force: true }); // 开氛围音当场试听一枚冒泡「啵」
        }
        return;
      }
      if (ev.target.closest('[data-st-keys]')) { this.#shortcuts(); return; }
      const retChip = ev.target.closest('[data-st-ret]');
      if (retChip) { this.#applyRetention(Number(retChip.dataset.stRet)); return; }
      if (ev.target.closest('[data-st-retset]')) { this.#applyRetentionCustom(); return; }
      if (ev.target.closest('[data-st-rebuild]')) { this.#confirmRebuild(); return; }
      if (ev.target.closest('[data-st-reset]')) { this.#confirmReset(); return; }
      if (ev.target.closest('[data-st-manual]')) { this.close(); this.opts.onManual?.(); return; }
      if (ev.target.closest('[data-st-chronicle]')) { this.close(); this.opts.onChronicle?.(); return; } // t_153
      if (ev.target.closest('[data-st-guide]')) { this.close(); this.opts.onGuide?.(); return; } // t_159
      // t_172：旋钮行点击不吃（滑杆自己的 input/change 管交互）
      if (ev.target.closest('[data-st-reload]')) { location.reload(); return; }
    });
    // Esc 走浮层栈（feedback.js）：嵌套对话框开着时先关对话框，本窗
    // 的下一次 Esc 才收窗；栈上收口顺带吃掉这颗键（stopImmediate）
    this.layerOff = layerPush(() => this.close());
    document.body.classList.add('fb-lock');
    this.#showPane(PANES.some((pn) => pn.key === prefs().settingsPane) ? prefs().settingsPane : 'gen');
    // 键盘入口：焦点落在当前分类的导航钮上（Tab 序从这里走进窗内）
    el.querySelector('[data-sw-nav][aria-current="true"]')?.focus();
    this.#loadInfo();
    // 房间时间行：开着才走秒（与表盘面板同一 serverSkew 真源），收窗
    // 即停——侧栏的常驻时钟已退役，秒针只为打开设置的人跳
    const clockVal = el.querySelector('[data-st-clockv]');
    clockVal.textContent = roomNow().toLocaleTimeString(localeTag(), { hour12: false });
    el.querySelector('[data-st-clock]').title = t('房间时间 · {text}', { text: clockSyncInfo().text });
    this.clockT = setInterval(() => {
      clockVal.textContent = roomNow().toLocaleTimeString(localeTag(), { hour12: false });
    }, 1000);
    // 自定义保留天数输入：回车即设（与「设」钮同一入口）
    el.querySelector('[data-st-retin]')?.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') { ev.preventDefault(); this.#applyRetentionCustom(); }
    });
    // 基线分支输入：回车即设（与「设」钮同一入口，保留天数同款）
    el.querySelector('[data-st-gitbase]')?.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') { ev.preventDefault(); this.#applyGitBase(); }
    });
    this.#loadAutopilot();
    this.#loadGitSwitches();
    this.#loadRebuildVote();
    this.#loadModelSkills();
    this.#loadRetention();
    this.#loadSendWait();
    this.#loadBudget();
    this.#loadReveal();
  }

  close() {
    if (!this.el) return;
    this.layerOff?.();
    this.layerOff = null;
    dismiss(this.el, 160);
    this.el = null;
    clearInterval(this.clockT);
    closeClockPop(); // 表盘面板锚在本窗的行上，窗收了面板别孤悬
    document.body.classList.remove('fb-lock');
  }

  /** 切换分类面板：一次只亮一片（hidden 挂在 section 上），当前项
   *  aria-current；记住上次停在哪个分类（下次开窗回到原地） */
  #showPane(key) {
    if (!this.el) return;
    const win = this.el.querySelector('.set-win');
    if (win.dataset.pane === key) return;
    win.dataset.pane = key;
    for (const pn of this.el.querySelectorAll('[data-sw-nav]')) {
      pn.setAttribute('aria-current', String(pn.dataset.swNav === key));
    }
    for (const pane of this.el.querySelectorAll('.sw-pane')) {
      pane.hidden = pane.dataset.swPane !== key;
    }
    this.el.querySelector('.sw-main').scrollTop = 0;
    setPref('settingsPane', key);
    closeClockPop(); // 表盘面板锚在连接面板的行上，切走了别孤悬
  }

  /** connState 的每帧回灌：窗开着时连接行跟着真相同步（含「仅缓存」落定） */
  sync() {
    if (!this.el) return;
    const c = this.opts.conn?.();
    if (!c) return;
    const row = this.el.querySelector('.st-conn');
    row.dataset.mode = c.mode;
    row.querySelector('.st-label').textContent = c.label;
    row.title = c.note || '';
    let room = row.querySelector('.st-room');
    if (c.roomName) {
      if (!room) {
        room = document.createElement('span');
        room.className = 'st-room';
        row.appendChild(room);
      }
      room.textContent = c.roomName;
    } else {
      room?.remove();
    }
  }

  // 授权状态行（通知面板）：壳面走 osNotifyState 只读探针（绝不弹问
  // 询——getNotificationSettings 读现行账），浏览器面读
  // Notification.permission。每次开窗现查：系统设置里改的主意立刻可
  // 见。被拒时状态行直接长出「去系统设置」按钮（壳桥一键直达通知面
  // 板——比一句指路文本少翻四层设置树）；无桥又无 API（局域网 http 的
  // 浏览器面）时整行撤下——没有可说的真相就不占地方。
  async #refreshAuth(cell, row) {
    if (!cell || !row) return;
    const done = (text, fix) => {
      cell.innerHTML = esc(text) +
        (fix ? ' <button type="button" class="btn small gold" data-st-authfix>' + esc(t('去系统设置')) + '</button>' : '');
      cell.querySelector('[data-st-authfix]')?.addEventListener('click', () => {
        try { window.openNotifySettings?.(); } catch { /* 桥面故障：状态文本已指路 */ }
      });
    };
    if (typeof window.osNotifyState === 'function') { // 原生壳：只读探针
      let s = 'unknown';
      try { s = await window.osNotifyState(); } catch { /* 桥面故障按未决 */ }
      if (s === 'granted') { done(t('已允许——系统横幅可用')); return; }
      if (s === 'denied') { done(t('被 macOS 拒绝（新消息只响声不出横幅）'), true); return; }
      done(t('未决定——开关「新消息系统通知」会弹一次系统问询')); return;
    }
    if (typeof Notification !== 'undefined') { // 浏览器面
      const p = Notification.permission;
      if (p === 'granted') { done(t('已允许——浏览器横幅可用')); return; }
      if (p === 'denied') { done(t('被浏览器拒绝（站点权限里可改）')); return; }
      done(t('未决定——首条新消息会问一次')); return;
    }
    row.remove(); // 无桥无 API：没有可说的真相
  }

  // 开「新消息系统通知」当场发一条测试通知：与正式投递同一副面孔
  // （壳的 osNotify 桥优先，浏览器走 Notification）——看到横幅即知通
  // 路已通。浏览器未授权时借这次点开关的手势请求（问权限的礼貌时机）；
  // 拒过就不再纠缠，提示语指路浏览器站点权限/系统设置——壳面被拒时
  // osNotifyAuth 会说实话（webview 桥的返回是 Promise，须 await），当
  // 场把路标给出来，而不是静默装作通了。
  async #testNotify() {
    const title = t('牛马工作室');
    const body = t('系统通知已开启——新消息会像这样弹出，点击可跳去对应办公室');
    if (typeof window.osNotify === 'function') { // 原生壳：UN 代发
      let auth = 'granted';
      if (typeof window.osNotifyAuth === 'function') {
        try { auth = await window.osNotifyAuth(); } catch { /* 桥面故障按已许处理（照发，响不响系统说了算） */ }
      }
      if (auth === 'denied') {
        this.opts.toast?.(t('macOS 已拒绝本应用的通知——去 系统设置 → 通知 → 牛马工作室 打开「允许通知」（本面板的「系统授权」行有一键直达），再来试'), 'err');
        return;
      }
      try { window.osNotify(title, body, 'default'); } catch { /* 桥面故障静默 */ }
      return;
    }
    if (typeof Notification === 'undefined') return; // 无桥又无 API（局域网 http 等）——无面可试
    const send = () => { try { new Notification(title, { body }); } catch { /* 静默 */ } };
    if (Notification.permission === 'granted') { send(); return; }
    if (Notification.permission !== 'default') return;
    try { Notification.requestPermission?.().then((p) => { if (p === 'granted') send(); }).catch(() => {}); } catch { /* 静默 */ }
  }

  // 快捷键一览（对话框）：只列真实存在的行为——每一条都能在代码里
  // 指到对应的 keydown 守卫，不编造未来的快捷键
  #shortcuts() {
    const row = (keys, what) =>
      `<div class="sc-row"><span class="sc-keys">${keys.map((k) => `<kbd>${esc(k)}</kbd>`).join('')}</span>` +
      `<span class="sc-what">${esc(what)}</span></div>`;
    Dialog.show({
      title: t('快捷键'), desc: t('对话与办公室的键盘操作'),
      size: 's',
      html:
        row(['Enter'], t('发送消息')) +
        row(['Shift', 'Enter'], t('输入井内换行')) +
        row(['@'], t('提及成员（↑ ↓ 选择，Enter / Tab 补全）')) +
        row(['Esc'], t('关闭浮层 / 清空并退出消息搜索')) +
        row(['↑ ↓ W A S D'], t('像素办公室里走动')),
      actions: [{ label: t('知道了'), primary: true }],
    });
  }

  // 重置工作室（危险对话框）：单向门配双保险——说明清单把「删什么/
  // 留什么」摆全，确认钮在输入「重置」之前保持禁用（Dialog 的按钮
  // 是静态模板，这里在渲染后抓 data-i 钮挂 disabled；原生 disabled
  // 让点击根本不达 Dialog 的委派）。确认后 POST /reset：应答只需要
  // 撑到把对话框收尾——后端随即收摊→擦除→拉起替身，本窗口随旧进程
  // 一起退场，新窗口就是首装状态。应答没到（连接被收摊掐断）给一句
  // 中性提示：信号可能已投出，也可能服务不可达——不装作知道是哪种。
  #confirmReset() {
    const dlg = Dialog.show({
      title: t('重置工作室'), kind: 'warning',
      desc: t('删除全部数据，恢复到刚安装时的状态——不可撤销'),
      size: 's',
      html:
        '<div class="sc-danger">' +
        '<p>' + t('将删除这台机器上<b>工作室的全部数据</b>：') + '</p>' +
        '<ul>' +
          '<li>' + esc(t('所有房间与聊天历史、成员档案与编制表')) + '</li>' +
          '<li>' + esc(t('任务 / 需求 / 排期 / 会议台账、项目登记、群公告')) + '</li>' +
          '<li>' + esc(t('技能/MCP 库与说明书文档（下次启动重新播种）')) + '</li>' +
          '<li>' + esc(t('ZCode 侧：牛马 / 小助手的侧栏行与它们的会话数据一并删除')) + '</li>' +
        '</ul>' +
        '<p class="sc-keep">' + esc(t('保留：各项目工作区里的文件、你在 ZCode 里自己的会话。')) + '</p>' +
        '<p class="sc-confirm">' + esc(t('输入「重置」以确认：')) + '</p>' +
        '<input class="input" data-st-reset-input placeholder="' + esc(t('重置')) + '" autocomplete="off">' +
        '</div>',
      actions: [
        { label: t('取消') },
        { label: t('删除全部数据并重置'), primary: true, danger: true, value: true },
      ],
    });
    // 确认钮在口令对上之前保持禁用（渲染是同步的，此时已在文档里）
    const arm = document.querySelector('.fb-dlg-foot [data-i="1"]');
    const input = document.querySelector('[data-st-reset-input]');
    if (arm && input) {
      arm.disabled = true;
      const pass = t('重置'); // 口令随语言（en 下为 reset——提示语与占位同源）
      input.addEventListener('input', () => {
        arm.disabled = input.value.trim() !== pass;
      });
      setTimeout(() => input.focus(), 60); // 等入场动画落定再抢焦点
    }
    dlg.then(async (ok) => {
      if (ok !== true) return;
      this.close();
      let r;
      try {
        r = await postReset();
      } catch {
        // 应答没到：多半是信号已投出、连接被收摊掐断（窗口马上随旧进程
        // 退场，无需多言）；但也可能是服务真不可达——给一句中性提示，
        // 两种情形都适用，不装作知道是哪一种
        this.opts.toast?.(t('连接已断——若重置已开始，本窗口将随旧进程关闭；若久未重启，请手动打开应用'), 'warn');
        return;
      }
      if (r?.ok) {
        this.opts.toast?.(t('重置已开始——归档侧栏、擦除数据，应用即将以全新状态重启'), 'ok');
      } else if (r && !r.ok) {
        this.opts.toast?.(r.data?.error || t('重置被拒（HTTP {status}）', { status: r.status }), 'err');
      }
    });
  }

  // 重新编译并重启（维护面板，非危险动作不设口令）：确认后 POST
  // /rebuild——后端在前台跑 go build 并把新二进制原子换位，成功才投
  // 重启令牌。编译可能要等一会儿（热缓存几秒、冷缓存一两分钟），
  // 确认一按窗即收、sticky 通知报「正在编译」顶着，应答到达再换回
  // 执；编译失败的原因（编译器输出原样带回）用对话框整段亮出——
  // toast 装不下那么多行。应答没到（连接被收摊掐断）与重置同一句
  // 中性提示：编译可能已成功、窗口马上退场，也可能服务不可达——
  // 不装作知道是哪种。
  #confirmRebuild() {
    const dlg = Dialog.show({
      title: t('重新编译并重启'), kind: 'warning',
      desc: t('从源码重新编译本应用，成功后自动重启工作室'),
      size: 's',
      html:
        '<div class="sc-rebuild">' +
        '<p>' + esc(t('编译期间工作室照常运行——热缓存几秒，冷缓存（清过缓存/换机器）可能一两分钟。')) + '</p>' +
        '<p>' + esc(t('正在干活的成员本轮随重启中断，重启后点名即可接续——与手动重启同一语义。')) + '</p>' +
        '<p class="sc-keep">' + esc(t('编译失败则应用原地不动，原因整段亮出；前端改动无需编译，「重新载入」即得。')) + '</p>' +
        '</div>',
      actions: [
        { label: t('取消') },
        { label: t('编译并重启'), primary: true, value: true },
      ],
    });
    dlg.then(async (ok) => {
      if (ok !== true) return;
      this.close();
      const note = Notify.show({
        kind: 'info', title: t('正在编译'), sticky: true,
        text: t('go build 进行中——热缓存几秒、冷缓存一两分钟，请稍候'),
      });
      let r;
      try {
        r = await postRebuild();
      } catch {
        note.close();
        this.opts.toast?.(t('连接已断——若编译已成功，本窗口将随旧进程关闭；若久未重启，请手动打开应用'), 'warn');
        return;
      }
      note.close();
      if (r?.ok) {
        this.opts.toast?.(r.data?.text || t('编译成功——正在收摊重启，新窗口即将自动顶上'), 'ok');
        return;
      }
      if (r && !r.ok) {
        const why = String(r.data?.error || t('编译被拒（HTTP {status}）', { status: r.status }));
        Dialog.show({
          title: t('重新编译失败'), kind: 'error', size: 's',
          html: `<pre class="st-build-log">${esc(why)}</pre>`,
          actions: [{ label: t('知道了'), primary: true }],
        });
      }
    });
  }

  // ── 聊天记录保留期（「数据」面板）────────────────────────────────
  // 真源在服务端（~/.niuma/retention.json，days 0=永久，缺省 7 天）：
  // 开窗即读；预设 1/3/7/15/30/永久 一点即设，自定义天数回车或「设」。
  // 缩短保留期是删数据——先过确认对话框再 POST；服务端落盘即按新
  // 策略当场清扫，回执的 pruned（本次清掉的条数）进 toast。

  async #loadRetention() {
    const val = this.el?.querySelector('[data-st-retv]');
    if (!val) return;
    let days = null;
    try { days = (await getRetention()).days; } catch { days = null; }
    if (!this.el) return; // 等待期间窗已收起
    if (days === null) {
      val.textContent = t('暂不可用');
      val.title = t('读取保留期失败（服务不可达或内嵌形态）');
      return;
    }
    this.#retDays = days;
    this.#paintRetention(days);
  }

  // 画当前值：行内小签＋chips 选中态＋自定义输入框（恰为预设值时留空）
  #paintRetention(days) {
    const val = this.el?.querySelector('[data-st-retv]');
    if (val) val.textContent = days === 0 ? t('永久') : t('{n} 天', { n: days });
    let preset = false;
    for (const b of this.el.querySelectorAll('[data-st-ret]')) {
      const hit = Number(b.dataset.stRet) === days;
      b.setAttribute('aria-pressed', String(hit));
      if (hit) preset = true;
    }
    const input = this.el.querySelector('[data-st-retin]');
    if (input) input.value = preset ? '' : String(days);
  }

  // 自定义输入的入口：读输入框，1–3650 的整数才放行
  #applyRetentionCustom() {
    const input = this.el?.querySelector('[data-st-retin]');
    if (!input) return;
    const n = Number(input.value);
    if (!Number.isInteger(n) || n < 1 || n > 3650) {
      this.opts.toast?.(t('自定义保留天数需为 1–3650 的整数（更久请选永久）'), 'err');
      return;
    }
    this.#applyRetention(n);
  }

  async #applyRetention(days) {
    if (!this.el || this.#retBusy) return;
    if (!(days === 0 || (Number.isInteger(days) && days >= 1 && days <= 3650))) return;
    // 缩短＝删数据：确认对话框把后果说全（延长/转永久免确认）
    const cur = this.#retDays ?? 7;
    const rank = (d) => (d === 0 ? Infinity : d);
    if (rank(days) < rank(cur)) {
      const ok = await Dialog.show({
        title: t('缩短聊天记录保留期'), kind: 'warning', size: 's',
        desc: t('保留期从 {a} 缩短到 {b}', { a: cur === 0 ? t('永久') : t('{n} 天', { n: cur }), b: days === 0 ? t('永久') : t('{n} 天', { n: days }) }),
        html: '<div class="sc-rebuild"><p>' + t('超出新保留期的历史消息将<b>立即删除</b>（不可恢复）——所有房间的聊天记录、以及它们的图片引用所指消息，都会按新期限截断。') + '</p></div>',
        actions: [{ label: t('再想想') }, { label: t('确认并清理'), primary: true, danger: true, value: true }],
      });
      if (ok !== true) return;
      if (!this.el) return; // 等待确认期间窗已收起
    }
    this.#retBusy = true;
    const r = await setRetention(days);
    this.#retBusy = false;
    if (r.ok) {
      this.#retDays = days;
      this.#paintRetention(days);
      const pruned = Number(r.data?.pruned ?? 0);
      this.opts.toast?.(
        days === 0 ? t('聊天记录改为永久保留')
                   : t('聊天记录保留 {n} 天{pruned}', { n: days, pruned: pruned ? t('，已清理 {n} 条超期消息', { n: pruned }) : '' }),
        'ok');
    } else {
      this.opts.toast?.(r.data?.error || t('设置失败（HTTP {status}）', { status: r.status }), 'err');
    }
  }

  // 自动补货/自动推进双开关（「智能」面板，r_19）：按项目的服务端开
  // 关——开窗即读当前房的真源（大厅与项目房一视同仁；没有当前房时
  // 两行置灰，仅启动早期），翻转走「警告对话框→带确认令牌的 POST→
  // 回执渲染」（耗 token，开启必须显式知情）。一次 GET 同时回显两钮
  // ＋五旋钮。
  async #loadAutopilot() {
    const stockRow = this.el?.querySelector('[data-st-stockrow]');
    const advRow = this.el?.querySelector('[data-st-advrow]');
    const stockBtn = this.el?.querySelector('[data-st-autostock]');
    const advBtn = this.el?.querySelector('[data-st-autoadvance]');
    const nameEl = this.el?.querySelector('[data-st-apname]');
    const advNameEl = this.el?.querySelector('[data-st-advname]');
    if (!stockRow || !stockBtn || !advRow || !advBtn) return;
    const proj = this.opts.project?.() || null;
    if (!proj) {
      stockRow.classList.add('st-none');
      advRow.classList.add('st-none');
      stockBtn.disabled = true;
      advBtn.disabled = true;
      if (nameEl) nameEl.textContent = t('暂无所在房');
      if (advNameEl) advNameEl.textContent = t('暂无所在房');
      stockBtn.title = t('自动补货按项目开启——还没有所在房（启动早期房册未落），进任意一间房（Niuma_Studio 也算）后再来这里打开');
      advBtn.title = t('自动推进按项目开启——还没有所在房（启动早期房册未落），进任意一间房（Niuma_Studio 也算）后再来这里打开');
      return;
    }
    if (nameEl) nameEl.textContent = proj.name;
    if (advNameEl) advNameEl.textContent = proj.name;
    let body = null;
    try { body = await getAutopilot(proj.key); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    const max = Number(body?.max_plans ?? 0);
    const cap = max > 0 ? t('每日自动接受上限 {n} 份后熔断（只关推进，补货不受影响）', { n: max }) : t('每日接受上限：无限制');
    stockBtn.disabled = false;
    advBtn.disabled = false;
    stockBtn.setAttribute('aria-checked', String(!!body?.auto_stock));
    advBtn.setAttribute('aria-checked', String(!!body?.auto_advance));
    // 目标制补货：目标随 GET 面回显（开着才亮行；关着不亮，避免「上次
    // 目标」冒充在跑的目标——真值仍存于服务端，重开框里作预填）
    this.#apGoal = typeof body?.stock_goal === 'string' ? body.stock_goal : '';
    const goalLine = this.el.querySelector('[data-st-goalline]');
    const goalTxt = this.el.querySelector('[data-st-goaltxt]');
    if (goalLine && goalTxt) {
      goalLine.hidden = !(body?.auto_stock && this.#apGoal);
      goalTxt.textContent = this.#apGoal;
    }
    stockBtn.title = t('作用于项目「{name}」（{key}）：需求池 open 低于蓄水目标线（默认 3 条）时注入【自动驾驶·选题】补货——先蓄需求不急开工。开启须填本次目标，达成后自动收摊。耗 token。', { name: proj.name, key: proj.key });
    advBtn.title = t('作用于项目「{name}」（{key}）：提案满宽限自动接受、闲置+池有货自动拆解、停滞任务点名续做、HR 定时巡报，提问卡与权限按合理假设放行。{cap}，很耗 token。', { name: proj.name, key: proj.key, cap });
    this.#paintKnobs(proj, body);
  }

  // 五旋钮回显：GET /p/{key}/autopilot 回的是 {auto_stock,auto_advance,
  // autopilot,knobs:{poke_every_min,accept_delay_s,stall_delay_min,max_plans,
  // water_target}}（r_14 GET 面的键名带单位后缀；r_19 增双开关与
  // water_target）——滑杆键名走 staffing 写侧标签，这里做一次读侧映射
  //（复查#5：旧代码等的是从未上线过的 autopilot:{...} 对象，滑杆被永
  // 久禁用）；兼容期旧载荷 {autopilot:boolean, max_plans} 只有上限可回
  // 显。max_plans -1（API 面「不限」）映射滑杆 0；缺值滑杆空置（值走
  // 服务端缺省链，UI 不猜）。
  #paintKnobs(proj, body) {
    const raw = body?.knobs;
    const knobs = raw && typeof raw === 'object' ? {
      poke_every: raw.poke_every_min,
      accept_delay: raw.accept_delay_s,
      stall_delay: raw.stall_delay_min,
      max_plans: raw.max_plans,
      water_target: raw.water_target,
    } : null;
    for (const el of this.el.querySelectorAll('[data-st-knob]')) {
      const key = el.dataset.stKnob;
      const valEl = el.querySelector('[data-st-knobv]');
      const inEl = el.querySelector('[data-st-knobin]');
      const unit = el.dataset.stUnit || '';
      let v = null;
      if (knobs) {
        v = knobs[key];
        if (key === 'max_plans' && v === -1) v = 0; // API「不限」→滑杆 0
      } else if (key === 'max_plans' && body && 'max_plans' in body) {
        v = body.max_plans === -1 ? 0 : body.max_plans; // 旧载荷只有 max_plans
      }
      // r_14/t_180：stall_min 非 autopilot 独占——载荷顶层也可带（实现
      // 侧存 settings 顶层时 GET 面直接回顶层字段）
      if (v == null && key === 'stall_min' && body && 'stall_min' in body) {
        v = body.stall_min;
      }
      if (!inEl) continue;
      inEl.disabled = !knobs && key !== 'max_plans'; // 旧载荷：只有上限可调
      if (v == null) {
        if (valEl) valEl.textContent = t('默认');
        continue;
      }
      inEl.value = String(v);
      const min = Number(el.dataset.stMin || 0), max = Number(el.dataset.stMax || 100);
      inEl.style.setProperty('--fill', `${max > min ? Math.round(((v - min) / (max - min)) * 100) : 50}%`);
      if (valEl) valEl.textContent = v === 0 && key === 'max_plans' ? t('不限') : `${v} ${unit}`.trim();
    }
  }

  // 滑杆改值：带令牌 POST 只改该旋钮（r_19：knobs 路径与双开关完全解
  // 耦——服务端只写旋钮不动开关）——写盘即热更（引擎 60s 心跳收敛），
  // toast 反馈生效值。
  async #knobChanged(inputEl, rowEl) {
    const proj = this.opts.project?.() || null;
    if (!proj) return;
    const key = rowEl.dataset.stKnob;
    const v = Number(inputEl.value);
    const r = await setAutopilot(proj.key, { [key]: v });
    const valEl = rowEl.querySelector('[data-st-knobv]');
    const unit = rowEl.dataset.stUnit || '';
    if (r.ok) {
      const shown = key === 'max_plans' && v === 0 ? t('不限') : `${v} ${unit}`.trim();
      if (valEl) valEl.textContent = shown;
      this.opts.toast?.(t('已生效：{k} = {v}', { k: rowEl.querySelector('.st-k').textContent, v: shown }), 'ok');
    } else {
      this.opts.toast?.(r.data?.error || t('保存失败，请重试'), 'err');
      this.#loadAutopilot(); // 回读真源，滚回失败前的值
    }
  }

  // ── 消息注入等待（「智能」面板第五位，工作室级）──────────────────
  // 真源在服务端（~/.niuma/sendwait.json，秒，缺省 300＝5 分钟）：开窗
  // 即读；滑杆松手即写（无确认——不是删数据，调错方向的代价只是多等
  // 或早播报）。写后热生效：服务端推给 Fleet，下一次投递即按新值等。
  async #loadSendWait() {
    const row = this.el?.querySelector('[data-st-swwait]');
    const inEl = row?.querySelector('[data-st-swin]');
    if (!row || !inEl) return;
    let body = null;
    try { body = await getSendWait(); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    const seconds = Number(body?.seconds);
    if (!body || !Number.isFinite(seconds)) {
      inEl.disabled = true;
      const valEl = row.querySelector('[data-st-swv]');
      if (valEl) valEl.textContent = t('暂不可用');
      row.title = t('读取注入等待失败（服务不可达或内嵌形态）');
      return;
    }
    inEl.disabled = false;
    inEl.value = String(seconds);
    const min = Number(row.dataset.stMin || 0), max = Number(row.dataset.stMax || 100);
    inEl.style.setProperty('--fill', `${max > min ? Math.round(((seconds - min) / (max - min)) * 100) : 50}%`);
    const valEl = row.querySelector('[data-st-swv]');
    if (valEl) valEl.textContent = fmtWait(seconds);
  }

  async #sendWaitChanged(inEl, rowEl) {
    const v = Number(inEl.value);
    const r = await setSendWait(v);
    const valEl = rowEl.querySelector('[data-st-swv]');
    if (r.ok) {
      if (valEl) valEl.textContent = fmtWait(v);
      this.opts.toast?.(t('已生效：消息注入等待 = {v}（工作室级，下一次投递即按新值等）', { v: fmtWait(v) }), 'ok');
    } else {
      this.opts.toast?.(r.data?.error || t('保存失败，请重试'), 'err');
      this.#loadSendWait(); // 回读真源，滚回失败前的值
    }
  }

  // ── token 预算（「智能」面板，工作室级钱面，r_26）─────────────────
  // 真源在服务端（~/.niuma/budget.json，日/周两输入框，0=不限）：开窗
  // 即读；失焦/回车整对落库（改谁换谁、另一位带已加载真值）。
  // 写后热生效：引擎每拍直读，下一拍即
  // 按新值熔断/放开；到额熔断只关推进（与每日代收上限同语义）。
  async #loadBudget() {
    const rows = this.el ? [...this.el.querySelectorAll('[data-st-budget]')] : [];
    if (!rows.length) return;
    let body = null;
    try { body = await getBudget(); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    const day = Number(body?.day_tokens), week = Number(body?.week_tokens);
    if (!body || !Number.isFinite(day) || !Number.isFinite(week)) {
      for (const row of rows) {
        const inEl = row.querySelector('[data-st-budgetin]');
        if (inEl) inEl.disabled = true;
        const valEl = row.querySelector('[data-st-budgetv]');
        if (valEl) valEl.textContent = t('暂不可用');
      }
      return;
    }
    this.#budget = { day, week };
    for (const row of rows) {
      const which = row.dataset.stBudget; // 'day' | 'week'
      const inEl = row.querySelector('[data-st-budgetin]');
      if (!inEl) continue;
      inEl.disabled = false;
      const v = which === 'day' ? day : week;
      inEl.value = budgetUnitStr(v); // token → 单位面（万/M）
      const valEl = row.querySelector('[data-st-budgetv]');
      if (valEl) valEl.textContent = v === 0 ? t('不限') : fmtTokens(v);
    }
  }

  async #budgetChanged(inEl, rowEl) {
    const which = rowEl.dataset.stBudget;
    const raw = inEl.value.trim();
    const uv = Number(raw); // 单位面（万/M）
    // 单位面折 token：正输入至少折 1 token（0.00001 万取整不成 0——
    // 0 是「不限」的保留字，不能被取整悄悄沾上）
    const v = uv > 0 ? Math.max(1, Math.floor(uv * budgetMul())) : 0;
    // 校验＝服务端口径（token 面）：0（不限）或 1–1e12。空串先拦
    // （Number('') 是 0，会悄悄写成不限）；非数/负数/超上限同驳——
    // 回显滚回已加载真值，不动库、不烧请求
    if (raw === '' || !Number.isFinite(uv) || uv < 0 || v > 1e12) {
      this.opts.toast?.(t('预算需为 0（不限）或 1 万亿 token 以内的数'), 'err');
      this.#loadBudget();
      return;
    }
    // 整对覆写：改的是这框，另一位带已加载真值
    const next = { ...this.#budget, [which]: v };
    const r = await setBudget(next.day, next.week);
    const valEl = rowEl.querySelector('[data-st-budgetv]');
    if (r.ok) {
      this.#budget = next;
      inEl.value = budgetUnitStr(v); // 归一回单位面（123.456789万 → 123.46万）
      if (valEl) valEl.textContent = v === 0 ? t('不限') : fmtTokens(v);
      const label = which === 'day' ? t('每日') : t('每周');
      this.opts.toast?.(t('已生效：{label} token 预算 = {v}（工作室级，到额熔断只关推进）', { label, v: v === 0 ? t('不限') : fmtTokens(v) }), 'ok');
    } else {
      this.opts.toast?.(r.data?.error || t('保存失败，请重试'), 'err');
      this.#loadBudget(); // 回读真源，滚回失败前的值
    }
  }

  // 双开关翻转（r_19）：kind＝'stock'（自动补货）| 'advance'（自动推
  // 进）。关闭免确认（停下来永远是无代价的安全方向）；开启走各自的警
  // 告对话框（两开关烧 token 的面不同，知情清单分开列）→带令牌 POST→
  // 回执渲染。
  async #toggleAutoFeature(btn, kind) {
    if (btn.disabled) return;
    const proj = this.opts.project?.() || null;
    if (!proj) return;
    const stock = kind === 'stock';
    const on = btn.getAttribute('aria-checked') === 'true';
    if (on) {
      const r = await setAutopilot(proj.key, stock ? { stock: false } : { advance: false });
      if (r.ok) {
        btn.setAttribute('aria-checked', 'false');
        if (stock) {
          const goalLine = this.el?.querySelector('[data-st-goalline]');
          if (goalLine) goalLine.hidden = true;
        }
        this.opts.toast?.(stock
          ? t('已关闭「{name}」的自动补货——需求池不再自动蓄水，池内既有需求不受影响', { name: proj.name })
          : t('已关闭「{name}」的自动推进——待审提案回到房主关口', { name: proj.name }), 'ok');
      } else {
        this.opts.toast?.(r.data?.error || t('关闭失败，请重试'), 'err');
      }
      return;
    }
    const warn = stock ? {
      title: t('开启自动补货'),
      desc: t('作用于项目「{name}」——开启后：', { name: proj.name }),
      html: '<div class="sc-ap-warn"><ul>' +
        '<li>' + t('需求池 open 低于蓄水目标线（默认 3 条）时，自动注入【自动驾驶·选题】让编排者<b>补货蓄水</b>——先蓄需求不急开工，立到目标线即停（忙闲皆然）') + '</li>' +
        '<li>' + t('<b>目标制</b>：选题围绕你填的本次目标展开——目标未达成前，编排者不得以「本期无新需求」应付；判定达成（或确认无法达成）后会<b>自动关闭</b>本房的自动补货与自动推进') + '</li>' +
        '<li>' + t('只开补货时提案仍由你审、成员权限/提问照常找你——这是「帮你补弹药、打不打你来定」的档位') + '</li>' +
        '<li>' + t('<b>消耗 token</b>：编排者的补货回合会自动发生') + '</li>' +
      '</ul>' +
      '<p class="sc-confirm">' + esc(t('本次目标（必填，达成后自动收摊）：')) + '</p>' +
      // 目标框给足版面：3 行 textarea（可竖向拖高），中号对话框承托——
      // 目标常是一整句话，单行小框看不全也难编辑
      '<textarea class="input" data-st-goalinput rows="3" placeholder="' + esc(t('例：写完第二卷前三章并过一审')) + '" autocomplete="off">' + esc(this.#apGoal) + '</textarea>' +
      '</div>',
      okToast: t('「{name}」已开启自动补货——围绕本次目标蓄水，达成后自动收摊（耗 token，随时可关）', { name: proj.name }),
    } : {
      title: t('开启自动推进'),
      desc: t('作用于项目「{name}」——开启后房主不介入推进：', { name: proj.name }),
      html: '<div class="sc-ap-warn"><ul>' +
        '<li>' + t('提案满宽限（默认 2 分钟）<b>自动接受</b>、任务自动执行与汇报') + '</li>' +
        '<li>' + t('池里有货没人拆时自动<b>拆解排期</b>——小需求直提提案、大需求开评审会') + '</li>' +
        '<li>' + t('成员提问卡<b>照常出卡</b>：你在场时按正常窗口等你点选；不在场先给约 2 分钟代行宽限（随 accept_delay 旋钮），满窗按合理假设放行、权限请求直接放行') + '</li>' +
        '<li>' + t('进行中任务<b>停滞超阈（默认 30 分钟）自动点名续做</b>；HR 定时产能巡报') + '</li>' +
        '<li>' + t('<b>大量消耗 token</b>：牛马回合将持续自发产生') + '</li>' +
        '<li>' + t('每日自动接受达上限后熔断（只关推进，补货不受影响）；随时可回到这里关闭') + '</li>' +
      '</ul></div>',
      okToast: t('「{name}」已开启自动推进——排期/接受/催办自动运转（很耗 token，随时可关）', { name: proj.name }),
    };
    const dlg = Dialog.show({
      title: warn.title, kind: 'warning', size: stock ? 'm' : 's',
      desc: warn.desc,
      html: warn.html,
      actions: [{ label: t('再想想') }, { label: t('我已知晓，开启'), primary: true, value: true }],
    });
    if (stock) {
      // 目标必填（目标制补货）：确认钮在目标非空前保持禁用——重置口令
      // 框的模式（渲染同步，此时已在文档里）；服务端还有一道 off→on
      // 缺 goal 即 400 的硬门，前端这只是第一道顺手门
      const arm = document.querySelector('.fb-dlg-foot [data-i="1"]');
      const goalIn = document.querySelector('[data-st-goalinput]');
      if (arm && goalIn) {
        arm.disabled = goalIn.value.trim() === '';
        goalIn.addEventListener('input', () => {
          arm.disabled = goalIn.value.trim() === '';
        });
        setTimeout(() => goalIn.focus(), 60); // 等入场动画落定再抢焦点
      }
    }
    dlg.then(async (ok) => {
      if (ok !== true) return;
      // wrap 在 resolve 后 160ms 才摘除，then 微任务先跑——输入仍可读
      const goal = (document.querySelector('[data-st-goalinput]')?.value || '').trim();
      const r = await setAutopilot(proj.key, stock ? { stock: true, goal } : { advance: true });
      if (r.ok) {
        btn.setAttribute('aria-checked', String(stock
          ? r.data?.auto_stock !== false
          : r.data?.auto_advance !== false));
        if (stock) {
          this.#apGoal = goal;
          const goalLine = this.el?.querySelector('[data-st-goalline]');
          const goalTxt = this.el?.querySelector('[data-st-goaltxt]');
          if (goalLine && goalTxt) {
            goalLine.hidden = !goal;
            goalTxt.textContent = goal;
          }
        }
        this.opts.toast?.(warn.okToast, 'ok');
        return;
      }
      Dialog.show({
        title: t('开启失败'), kind: 'error', size: 's',
        html: `<pre class="st-build-log">${esc(r.data?.error || t('HTTP {status}', { status: r.status }))}</pre>`,
        actions: [{ label: t('知道了'), primary: true }],
      });
    });
  }

  // ── AI 重编译投票（「智能」面板第三位开关，按项目）────────────────
  // 真源在 staffing 设置（rebuild_vote，服务端 GET/POST /p/{key}/
  // rebuild-vote）：开着时本房成员可「提议重编译」开场全房表决，超半
  // 数同意即自动 rebuild。开窗即读；关闭免确认（作废在投的票）；开启
  // 走 REBUILD 令牌的警告对话框（AI 能重启整个工作室——知情清单）。
  async #loadRebuildVote() {
    const row = this.el?.querySelector('[data-st-rvoterow]');
    const btn = this.el?.querySelector('[data-st-rvote]');
    const nameEl = this.el?.querySelector('[data-st-rvotename]');
    if (!row || !btn) return;
    const proj = this.opts.project?.() || null;
    if (!proj) {
      row.classList.add('st-none');
      btn.disabled = true;
      if (nameEl) nameEl.textContent = t('暂无所在房');
      btn.title = t('AI 重编译投票按项目开启——还没有所在房（启动早期房册未落），进任意一间房（Niuma_Studio 也算）后再来这里打开');
      return;
    }
    if (nameEl) nameEl.textContent = proj.name;
    let body = null;
    try { body = await getRebuildVote(proj.key); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    if (!body) {
      btn.disabled = true;
      btn.title = t('暂不可用（服务不可达或内嵌形态）');
      return;
    }
    btn.disabled = false;
    btn.setAttribute('aria-checked', String(!!body.rebuild_vote));
    btn.title = t('作用于项目「{name}」（{key}）：本房成员可回复「提议重编译」发起投票，全房超半数同意即自动重新编译并重启工作室。', { name: proj.name, key: proj.key });
  }

  // git 版本管理四行设置的读面（「项目」面板，两级门顶层）：开窗即
  // 读当前房 summary 真源（enabled/base_branch/auto_seat_branch/
  // auto_seat 恒给）——没有当前房时四行置灰（autopilot 同款）；读挂
  // 了如实置灰，不猜。
  async #loadGitSwitches() {
    const row = this.el?.querySelector('[data-st-gitrow]');
    const autoRow = this.el?.querySelector('[data-st-gitautorow]');
    const seatRow = this.el?.querySelector('[data-st-gitseatrow]');
    const baseRow = this.el?.querySelector('[data-st-gitbaserow]');
    const btn = this.el?.querySelector('[data-st-git]');
    const autoBtn = this.el?.querySelector('[data-st-gitauto]');
    const seatBtn = this.el?.querySelector('[data-st-gitseat]');
    const baseIn = this.el?.querySelector('[data-st-gitbase]');
    if (!row || !btn || !autoRow || !autoBtn || !seatRow || !seatBtn || !baseRow || !baseIn) return;
    const nameEl = this.el.querySelector('[data-st-gitname]');
    const proj = this.opts.project?.() || null;
    const greyAll = (why) => {
      row.classList.add('st-none');
      autoRow.classList.add('st-none');
      seatRow.classList.add('st-none');
      btn.disabled = true;
      autoBtn.disabled = true;
      seatBtn.disabled = true;
      baseIn.disabled = true;
      if (why) btn.title = why;
    };
    if (!proj) {
      greyAll();
      if (nameEl) nameEl.textContent = t('暂无所在房');
      return;
    }
    row.classList.remove('st-none');
    autoRow.classList.remove('st-none');
    seatRow.classList.remove('st-none');
    if (nameEl) nameEl.textContent = proj.name;
    let body = null;
    try { body = await getGitSummary(proj.key); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    if (!body) {
      greyAll(t('暂不可用（版本管理摘要读取失败）'));
      return;
    }
    const enabled = body.enabled !== false; // 恒给字段；缺席按开（与服务端缺省同向）
    this.#gitBase = typeof body.base_branch === 'string' ? body.base_branch : '';
    const auto = !!body.auto_seat_branch;
    const seat = !!body.auto_seat;
    btn.disabled = false;
    btn.setAttribute('aria-checked', String(enabled));
    autoBtn.disabled = !enabled; // 总开关关着＝自动建枝不生效，置灰等总开关先开
    autoBtn.setAttribute('aria-checked', String(auto));
    seatBtn.disabled = !enabled;
    seatBtn.setAttribute('aria-checked', String(seat));
    baseIn.disabled = false;
    baseIn.value = this.#gitBase; // 回显生效值（空＝main）
    btn.title = t('作用于项目「{name}」（{key}）：关掉后版本管理面板只读，分支隔离、合并流、提交播报、自动建枝全停（仓库与既有提交不受影响）', { name: proj.name, key: proj.key });
  }

  // 翻转：POST /p/{key}/git/settings 一次写全（基线用读面缓存、各行现
  // 势原样回送，不覆盖）；总开关的关闭方向与自动驻枝的开启方向带确认
  //（后者要立即补驻存量成员并迁移会话）——与后端口径一致；成功失败都
  // 回读真源渲染（autopilot 同款，不乐观翻转）。
  async #toggleGitSwitch(btn, kind) {
    const proj = this.opts.project?.() || null;
    if (!proj || btn.disabled) return;
    const on = btn.getAttribute('aria-checked') !== 'true'; // 目标态
    if (kind === 'master' && !on) {
      const go = await Dialog.confirm({
        title: t('关闭 git 版本管理'),
        text: t('分支隔离、合并流、提交播报、自动建枝全停；挂着分支座位的成员在重新打开前无法重生会话。仓库与既有提交不受影响。'),
        okText: t('仍要关闭'), danger: true,
      });
      if (!go) return;
    }
    if (kind === 'seat' && on) {
      const go = await Dialog.confirm({
        title: t('开启成员自动驻分支'),
        text: t('现有无分支的在编成员会立即各驻专属枝 wt/<人名>（自基线切出），在岗者的会话迁至专属工作树（历史/任务/车道无损续接）；此后新成员出生即自动驻枝。'),
        okText: t('开启并补驻现有成员'), danger: true,
      });
      if (!go) return;
    }
    const masterBtn = this.el.querySelector('[data-st-git]');
    const autoBtn = this.el.querySelector('[data-st-gitauto]');
    const seatBtn = this.el.querySelector('[data-st-gitseat]');
    const masterOn = kind === 'master' ? on : masterBtn.getAttribute('aria-checked') === 'true';
    const autoOn = kind === 'autoseat' ? on : autoBtn.getAttribute('aria-checked') === 'true';
    const seatOn = kind === 'seat' ? on : seatBtn.getAttribute('aria-checked') === 'true';
    try {
      const out = await postGitSettings(proj.key, {
        disabled: !masterOn,
        base_branch: this.#gitBase || '',
        auto_seat_branch: autoOn,
        auto_seat: seatOn,
      });
      if (seatOn && Array.isArray(out?.backfilled) && out.backfilled.length) {
        this.opts.toast?.(t('已补驻 {who}（wt/ 前缀专属枝，在岗者会话已迁移）', { who: out.backfilled.join(t('、')) }), 'ok');
      }
    } catch {
      /* 失败静默——回读真源即回滚渲染 */
    }
    this.#loadGitSwitches(); // 回读真源，滚回失败前的值
  }

  // 基线分支的「设」入口（保留天数自定义输入同款）：读输入框，空＝main；
  // POST 一次写全（两开关的现势从行上读，原样保留），回读渲染。服务端
  // 校验分支名，拒绝原文 toast。
  async #applyGitBase() {
    const proj = this.opts.project?.() || null;
    const baseIn = this.el?.querySelector('[data-st-gitbase]');
    if (!proj || !baseIn || baseIn.disabled) return;
    const base = baseIn.value.trim();
    const masterBtn = this.el.querySelector('[data-st-git]');
    const autoBtn = this.el.querySelector('[data-st-gitauto]');
    try {
      await postGitSettings(proj.key, {
        disabled: masterBtn.getAttribute('aria-checked') !== 'true',
        base_branch: base,
        auto_seat_branch: autoBtn.getAttribute('aria-checked') === 'true',
      });
      this.opts.toast?.(t('基线分支已设为 {base}', { base: base || 'main' }), 'ok');
    } catch (err) {
      this.opts.toast?.(err.message || String(err), 'err');
    }
    this.#loadGitSwitches(); // 回读真源（拒绝时输入框滚回生效值）
  }

  // 翻转：关闭免确认（顺手作废在投的票）；开启走警告对话框（REBUILD
  // 令牌）→带令牌 POST→回执渲染，失败弹错误详情（autopilot 同款）。
  async #toggleRebuildVote(btn) {
    if (btn.disabled) return;
    const proj = this.opts.project?.() || null;
    if (!proj) return;
    const on = btn.getAttribute('aria-checked') === 'true';
    if (on) {
      const r = await setRebuildVote(proj.key, false);
      if (r.ok) {
        btn.setAttribute('aria-checked', 'false');
        this.opts.toast?.(t('已关闭「{name}」的 AI 重编译投票——成员提议不再受理，在投的票已作废', { name: proj.name }), 'ok');
      } else {
        this.opts.toast?.(r.data?.error || t('关闭失败，请重试'), 'err');
      }
      return;
    }
    const dlg = Dialog.show({
      title: t('开启 AI 重编译投票'), kind: 'warning', size: 's',
      desc: t('作用于项目「{name}」——开启后：', { name: proj.name }),
      html: '<div class="sc-ap-warn"><ul>' +
        '<li>' + t('本房 AI 成员可回复<b>「提议重编译」</b>发起重编译投票，其余成员以「同意重编译」/「反对重编译」表决') + '</li>' +
        '<li>' + t('<b>全房超过半数同意即立即自动重新编译并重启整个工作室</b>（不只是本项目房）——编译期间照常运行，完成后自动重启、成员自动归位') + '</li>' +
        '<li>' + t('投票 10 分钟内未过半自动作废；你随时可回到这里关闭（关闭即作废在投的票）') + '</li>' +
      '</ul></div>',
      actions: [{ label: t('再想想') }, { label: t('我已知晓，开启'), primary: true, value: true }],
    });
    dlg.then(async (ok) => {
      if (ok !== true) return;
      const r = await setRebuildVote(proj.key, true);
      if (r.ok) {
        btn.setAttribute('aria-checked', String(r.data?.rebuild_vote !== false));
        this.opts.toast?.(t('「{name}」已开启 AI 重编译投票——成员表决超半数即自动重编译重启（高权面，随时可关）', { name: proj.name }), 'ok');
        return;
      }
      Dialog.show({
        title: t('开启失败'), kind: 'error', size: 's',
        html: `<pre class="st-build-log">${esc(r.data?.error || t('HTTP {status}', { status: r.status }))}</pre>`,
        actions: [{ label: t('知道了'), primary: true }],
      });
    });
  }

  // ── 允许成员自制技能（「智能」面板，工作室级）──────────────────────
  // 真源在服务端（skills.json 的 model_skills 位，随库落盘）：开窗即
  // 读；翻转走 POST 回执渲染，不乐观翻转。开启走一个轻量知情对话框
  // （技能库全室共享、无逐条审批——开关即门槛）；关闭免确认。
  async #loadModelSkills() {
    const btn = this.el?.querySelector('[data-st-modelskill]');
    if (!btn) return;
    let body = null;
    try { body = await getSkillAuthoring(); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    if (!body) {
      btn.disabled = true;
      btn.title = t('暂不可用（服务不可达或技能库未接线）');
      return;
    }
    btn.disabled = false;
    btn.setAttribute('aria-checked', String(!!body.on));
  }

  async #toggleModelSkills(btn) {
    if (btn.disabled) return;
    const on = btn.getAttribute('aria-checked') === 'true';
    if (on) {
      try {
        const r = await postSkillAuthoring(false);
        btn.setAttribute('aria-checked', String(!!r.on));
        this.opts.toast?.(t('已关闭「允许成员自制技能」——成员回复里的技能块将被忽略'), 'ok');
      } catch (err) {
        this.opts.toast?.(err.message || t('关闭失败，请重试'), 'err');
      }
      return;
    }
    const dlg = Dialog.show({
      title: t('开启「允许成员自制技能」'), kind: 'warning', size: 's',
      desc: t('开启后：'),
      html: '<div class="sc-ap-warn"><ul>' +
        '<li>' + t('成员可在回复中写 <b>niuma-skill 围栏块</b>（key＋名称＋正文），调度器拦截后直接收入<b>全工作室共享的技能库</b>，可装配给任何成员') + '</li>' +
        '<li>' + t('收录<b>不设逐条审批</b>——开关即门槛；创建人随技能记录（可追溯），删除随时走「技能库」页') + '</li>' +
        '<li>' + t('拦截即时生效；围栏块格式的说明会随成员下次注入教给他们') + '</li>' +
      '</ul></div>',
      actions: [{ label: t('再想想') }, { label: t('我已知晓，开启'), primary: true, value: true }],
    });
    dlg.then(async (ok) => {
      if (ok !== true) return;
      try {
        const r = await postSkillAuthoring(true);
        btn.setAttribute('aria-checked', String(!!r.on));
        this.opts.toast?.(t('已开启「允许成员自制技能」——成员沉淀的套路可自行固化成技能'), 'ok');
      } catch (err) {
        Dialog.show({
          title: t('开启失败'), kind: 'error', size: 's',
          html: `<pre class="st-build-log">${esc(err.message || String(err))}</pre>`,
          actions: [{ label: t('知道了'), primary: true }],
        });
      }
    });
  }

  // ── ZCode 侧栏即时刷新（「连接」面板）────────────────────────────
  // 真源在服务端（~/.niuma/reveal.json，词表 off|link|auto 同 env）：
  // 开窗即读（on 是服务端对本平台的投递判定，开关画的就是进程真正
  // 会做的事）；翻转走 POST 回执渲染，不乐观翻转。开启的那一笔服务
  // 端会当场强投一次——把已在索引库里置顶、只差桌面端重读的牛马行
  // 拉回屏幕（编译重启后「员工都不见了」的自愈按钮就是它）。

  async #loadReveal() {
    const btn = this.el?.querySelector('[data-st-reveal]');
    if (!btn) return;
    let body = null;
    try { body = await getReveal(); } catch { body = null; }
    if (!this.el) return; // 等待期间窗已收起
    if (!body) {
      btn.disabled = true; // 读取失败：置灰不动猜（连接面板其余行照常可用）
      btn.title = t('暂不可用（服务不可达或内嵌形态）');
      return;
    }
    this.#revealDarwin = !!body.darwin;
    btn.disabled = false;
    btn.setAttribute('aria-checked', String(!!body.on));
    btn.title = this.#revealTitle(!!body.on);
  }

  // 行标题按开态＋平台说人话：macOS 的信任框代价值得摆进标题，
  // 其余平台只说零弹窗的通道
  #revealTitle(on) {
    const channel = this.#revealDarwin
      ? t('macOS 走 zcode:// 深链（ZCode ≥3.14 每次投递弹一次「只打开你信任来源的文件夹」确认框——Dock 不留图标的唯一通道）')
      : t('Windows/Linux 走零弹窗的第二实例投递');
    return on
      ? t('开：牛马/小助手的侧栏行一注册就投递让 ZCode 立刻重读，即时浮现。{channel}。', { channel })
      : t('关：行照常落库，ZCode 侧栏等下次被碰到（搜索/点开任务/重启 ZCode）才显现。开启后{channel}。', { channel });
  }

  async #toggleReveal(btn) {
    if (btn.disabled) return;
    const on = btn.getAttribute('aria-checked') === 'true';
    btn.disabled = true; // 请求在途防双击（双击会让两次翻转互相抵消）
    const r = await setReveal(!on);
    if (!this.el) return; // 等待期间窗已收起
    btn.disabled = false;
    if (r.ok) {
      const now = !!r.data?.on;
      btn.setAttribute('aria-checked', String(now));
      btn.title = this.#revealTitle(now);
      this.opts.toast?.(now
        ? t('已开启{extra}，置顶的牛马行即将回屏；之后的注册即时浮现', { extra: this.#revealDarwin ? t('——马上强投一次（ZCode 若弹信任框请放行）') : t('——马上强投一次') })
        : t('已关闭——行照常落库，ZCode 侧栏等下次自然刷新后显现'), 'ok');
      return;
    }
    this.opts.toast?.(r.data?.error || t('设置失败（HTTP {status}）', { status: r.status }), 'err');
  }

  // GET / 的名字与版本（懒取缓存）：失败就把版本行降级为「暂不可知」，
  // 不让一张设置窗因为一个读挂掉而整窗难看
  async #loadInfo() {
    if (!this.info) {
      try { this.info = await getInfo(); } catch { this.info = null; }
    }
    if (!this.el) return; // 等待期间窗已收起
    this.el.querySelector('[data-st-version]').textContent =
      this.info?.version ? `${this.info.version}` : '—';
  }

  async #copy() {
    // 内嵌窗口可能没给 Clipboard API 权限——兜底逻辑统一住在 dom.js
    const ok = await copyToClipboard(location.origin);
    this.opts.toast?.(ok ? t('工作室地址已复制') : t('复制失败——请手动选择复制'), ok ? 'ok' : 'err');
  }
}

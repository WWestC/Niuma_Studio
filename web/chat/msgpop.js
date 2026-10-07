// msgpop.js — 新消息提醒调度（声音＋系统通知，微信/飞书式）。
//
// 弹窗走**系统级**通知（OS 右上角横幅、通知中心留存、点击跳房）——
// 不在页面里画卡片：
// - 原生壳（WKWebView 没有 Web Notification API）：壳里经
//   UNUserNotificationCenter 代发——window.osNotify(title, body, thread)
//   绑定（shell/notify_darwin.m）；thread＝房键，通知中心按房折叠、
//   同房新弹窗顶掉旧的（微信同款）；点弹窗壳回调
//   window.__osNotifyClick(房键) → 切房＋跳对话板。
// - 浏览器打开的（http://127.0.0.1:PORT/app）：Web Notification API
//   （tag＝房键合并）；首次到消息时请求一次授权，拒过就静默。
//   应用前台聚焦时浏览器可能不弹横幅（平台策略）——壳与后台页不受影响。
//
// 声音独立于弹窗：sfx 先行（有人@我叮铃、其余叮咚，冷却窗防连珠炮），
// 弹窗另有偏好开关（dh.ui.prefs 的 msgPopup——环境不可用/未授权时
// 静默让行，声音与未读红标仍在）。
//
// 与未读徽标同一道门（RoomBook 的 onChatLine 落点）：自己发的话不提
// 醒、当前正看着的房不提醒、断线补窗与开页载历史不算新动静——系统
// 横幅在前台也弹（原生壳 willPresent 放行），但「正看着的房」根本
// 不会走到这里，不打扰自己。
//
// 通道被判死时的兜底（弹窗失灵之夜的教训：授权被拒是静默的，用户只
// 听见叮咚、永远等不来横幅，还以为弹窗坏了）：壳面经 osNotifyState
// 只读探针（getNotificationSettings——绝不弹问询）跟踪授权；被判死
// 则不再往死通道里投，改由页内 Notify 提醒框顶岗（同房顶掉旧的——
// 与 UN 的 identifier 语义同款），并每会话一次指路「去系统设置→通
// 知」。授权态随时会变（悬着的问询被答、系统设置被拨、rebuild 重签
// 后身份变化），非判死状态在节流窗外带复探；窗口回焦时被判死的通道
// 复探一次——用户改了主意，回到应用通道当场复活。

import { Msg } from '../wire/wire.js';
import { snipOf } from '../ui/dom.js';
import { t } from '../ui/i18n.js';
import { sfx } from '../ui/sound.js';
import { prefs } from '../ui/settings.js';
import { Notify } from '../ui/feedback.js';

/** 浏览器授权问询只发一次（会话内），不逐条消息纠缠用户。 */
let browserAsked = false;
/** 壳面授权的会话缓存：null＝未探 | 'granted' | 'denied' | 'unknown'。 */
let shellState = null;
/** 在飞的只读探针（防同窗重复发）。 */
let stateProbe = null;
/** 上次发起探针的时刻（消息路径 5s 节流——授权态要跟，但不逐条跟）。 */
let lastProbeAt = 0;
/** 「被拒→去系统设置」指路横幅的句柄（每会话一次，复活时收回）。 */
let deniedHintHandle = null;
/** 被拒后窗口回焦复探的节流（30s——改主意不需要秒级响应）。 */
let lastDeniedReprobe = 0;
/** 房键 → 页内兜底提醒框句柄（同房顶掉旧的，同 UN identifier 语义）。 */
const roomFallbacks = new Map();

/** 真读一次壳面授权（只读探针，绝不弹问询）并落会话账。osNotifyState
 *  桥缺席按未决——照投不装知道，桥面故障不该降级成「没有系统横幅」。 */
function readShellState() {
  return (async () => {
    let s = 'unknown';
    if (typeof window.osNotifyState === 'function') {
      try { s = await window.osNotifyState(); } catch { /* 桥面故障按未决 */ }
    }
    shellState = s;
    return s;
  })();
}

export class MsgPop {
  /**
   * @param {Object} opts
   * @param {import('./rooms.js').RoomBook} opts.book 换房/房名/房主真相
   */
  constructor(opts = {}) {
    this.book = opts.book;
    // 壳的点弹窗回调（notify_darwin.m didReceive → Eval 唤这里）
    window.__osNotifyClick = (key) => this.#jump(key);
    // 用户可能随时在系统设置里改主意（答了悬着的问询/拨了开关）：窗口
    // 回焦时若通道曾被判死就复探一次（30s 节流）——复活则收回兜底横幅
    // 与指路横幅，别让页内外的两套横幅双班并岗。
    window.addEventListener?.('focus', () => {
      if (shellState !== 'denied') return;
      const now = Date.now();
      if (now - lastDeniedReprobe < 30000) return;
      lastDeniedReprobe = now;
      readShellState().then((s) => {
        if (s === 'denied') return;
        roomFallbacks.forEach((h) => h.close());
        roomFallbacks.clear();
        deniedHintHandle?.close();
        deniedHintHandle = null;
      });
    });
  }

  /**
   * RoomBook 的 onChatLine 落点：一条会徽标未读的对话行到了。本方法
   * 自带全部策略（自己的话不提醒、只认 say/report、出声、投系统通知），
   * 调用方（app.js）无需自检任何条件。
   * @param {import('../wire/wire.js').Frame} frame
   * @param {{key: string, name: string}} room
   * @param {boolean} atMe 这条是否点名了房主（含全员令牌）
   */
  feed(frame, room, atMe) {
    if (frame.type !== Msg.Say && frame.type !== Msg.Report) return;
    if (frame.from && frame.from === this.book.owner?.name) return; // 自己的话不提醒（微信同款）
    const from = frame.from || room.name || t('成员');
    const snip = snipOf(frame.text || '');
    const body = frame.type === Msg.Report
      ? t('汇报 · {who}：{text}', { who: from, text: snip })
      : t('{who}：{text}', { who: from, text: snip });
    const title = atMe ? t('【有人@我】{room}', { room: room.name }) : room.name;

    // 声音先行（sfx 内部有提示音总闸＋冷却窗）：点名叮铃，其余叮咚
    sfx(atMe ? 'at' : 'msg');

    if (prefs().msgPopup === false) return; // 弹窗总闸（声音不受影响）
    this.#deliver(title, body, room.key);
  }

  /** 投递一条系统通知：壳桥优先（UN 代发），浏览器走 Notification API；
   *  壳面通道被判死（授权被拒）时落页内兜底提醒框。 */
  #deliver(title, body, key) {
    // 原生壳：osNotify 绑定（app_darwin.go）→ UNUserNotificationCenter
    if (typeof window.osNotify === 'function') {
      if (shellState !== 'denied') {
        // 未探/未决/已许都照投——投递不该被探针拖慢，未决通道投了也不
        // 亏（系统只丢被拒的）。节流窗外顺带一次只读复探：授权态随时
        // 会变（问询被答、设置被拨、rebuild 重签变了身份），通道死了
        // 要当场知道——探针归来若判死，这条已被系统丢弃，由兜底横幅
        // 补位（同一条消息最终只见面一次）。
        const now = Date.now();
        if (!stateProbe && now - lastProbeAt >= 5000) {
          lastProbeAt = now;
          stateProbe = readShellState().finally(() => { stateProbe = null; });
          stateProbe.then((s) => { if (s === 'denied') this.#denied(title, body, key); });
        }
        try { window.osNotify(title, body, key); } catch { /* 桥面故障静默 */ }
        return;
      }
      this.#denied(title, body, key); // 判死的通道：投了也是黑洞，页内顶岗
      return;
    }
    // 浏览器面：Notification API（非安全上下文——局域网 http——没有
    // 这一面，typeof 探不到直接静默）
    if (typeof Notification === 'undefined') return;
    const send = () => {
      try {
        const n = new Notification(title, { body, tag: `niuma:${key}` }); // tag＝同房合并
        n.onclick = () => { window.focus(); this.#jump(key); n.close(); };
      } catch { /* 环境不让发就静默 */ }
    };
    if (Notification.permission === 'granted') { send(); return; }
    if (Notification.permission === 'denied') { this.#denied(title, body, key, true); return; }
    // 首次：请求一次授权（多数浏览器会弹权限问询——微信首条消息问系统
    // 授权的同款时刻）；拒过（denied）或问过没允，不再纠缠
    if (Notification.permission !== 'default' || browserAsked) return;
    browserAsked = true;
    try {
      Notification.requestPermission?.().then((p) => { if (p === 'granted') send(); }).catch(() => {});
    } catch { /* webview 无此面 */ }
  }

  /** 系统横幅通道被判死（授权被拒）的兜底：页内 Notify 提醒框顶岗。
   *  指路横幅每会话一次（教一次路就够，之后只安静顶岗；通道复活时收
   *  回）；顶岗横幅同房顶掉旧的——OS 横幅的 identifier 语义原样搬进
   *  页内。 */
  #denied(title, body, key, browser = false) {
    if (!deniedHintHandle) {
      const hint = browser
        ? { title: t('浏览器拦下了本站通知'), text: t('新消息只响声、弹不出横幅——在浏览器的站点权限里允许通知，或改用应用窗口') }
        : { title: t('macOS 拒绝了本应用的通知'), text: t('新消息只响声、弹不出横幅——去 系统设置→通知→牛马工作室 打开「允许通知」') };
      deniedHintHandle = Notify.show({
        kind: 'warning', sticky: true, ...hint,
        ...(browser ? {} : {
          actions: [{ label: t('去系统设置'), onClick: () => { try { window.openNotifySettings?.(); } catch { /* 桥面故障：横幅文本已指路 */ } } }],
        }),
      });
    }
    roomFallbacks.get(key)?.close();
    roomFallbacks.set(key, Notify.show({
      kind: 'info', title, text: body,
      actions: [{ label: t('查看'), onClick: () => this.#jump(key) }],
    }));
  }

  // 切房＋跳对话板（与项目板 draftTo 同一条送人路径；也承接壳的
  // __osNotifyClick——点系统弹窗＝送人）
  #jump(key) {
    this.book.switchTo(key);
    location.hash = '#/chat';
  }
}

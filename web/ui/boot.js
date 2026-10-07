// boot.js — the ZCode boot gate (the app's front door).
//
// 本应用与 ZCode 完全绑定：点击应用后不直接进办公室，先落在这个
// 启动门上等 ZCode 识别完成——GET /zcode 轮询桥接状态（booting →
// warming → recruiting → ready：桥认出后小助手还要把模型通道预热烧
// 完、启动编制（编排者小牛/HR 小马）要招聘满员，门多关一会儿，进门
// 即满员热通道；服务端有封顶，预热卡住不会焊死门），配像素小牛动
// 画交待进度；识别成功才开门。失败绝不放行：missing/error/连不上服
// 务都停在失败面板，把原因亮出来（无「仍要进入」逃生口）——ZCode
// 装好后服务端 10 秒一探会自动识别，门自己开；off（--no-dispatch）
// 是运维显式关闭，提示后直接开门。标记位 #boot 与小牛动画在
// index.html 里内联，模块加载前就已上屏——这层门不依赖任何 JS 模
// 块先行。

import { getZCode } from '../wire/api.js';
import { t } from './i18n.js';

const $ = (sel) => document.querySelector(sel);

const POLL_EVERY = 400;        // the /zcode poll beat (ms)
const MISS_GRACE = 12;         // consecutive fetch misses before the failure panel (~5s: server gone / endpoint absent)
const RETRY_HOLD = 1500;       // 重新检测后失败面板的静默窗（别让面板一拍脸就弹回来）

const t0 = performance.now();  // splash 上屏时刻（模块加载 ≈ 页面起点），min-beat 的锚
const COPY_LABEL = t('复制错误');  // 复制按钮的常态文案（点击后短暂换成结果反馈）

let entered = false;           // 门已开（再调 dismissBoot 是幂等空转）

/**
 * 复制失败面板上的错误文本。Clipboard API 在非安全上下文（如局域网
 * http 地址打开）下不存在，退回隐藏 textarea + execCommand 兜底。
 * @returns {Promise<boolean>} 是否复制成功
 */
async function copyFailText() {
  const text = $('#boot-fail-text').textContent.trim();
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch { /* 授权被拒等 → execCommand 兜底 */ }
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.cssText = 'position:fixed;top:-9999px;opacity:0';
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch { ok = false; }
  ta.remove();
  return ok;
}

/** 换一行门面状态（门已开后由 app.js 报「正在进入办公室」等进度）。 */
export function bootNote(line) {
  const dots = $('#boot-dots');
  if (dots) dots.hidden = true;
  $('#boot-status-text').textContent = line;
}

/**
 * A fatal boot error OUTSIDE the gate (e.g. the rooms failed to
 * connect): same rule as a failed gate — the reason goes on the door
 * and the door STAYS SHUT. app.js calls this from its boot catch.
 */
export function bootFail(title, detail) {
  const dots = $('#boot-dots');
  if (dots) dots.hidden = true;
  $('#boot-status-text').textContent = t('启动失败');
  $('#boot-fail-text').textContent = detail ? t('{a}：{b}', { a: title, b: detail }) : title;
  $('#boot-fail').hidden = false;
}

/**
 * The gate promise: resolves ONLY when the door may open — ZCode
 * ready with the assistant's channel warm-up landed, or dispatch off
 * (--no-dispatch). Every failure keeps the door
 * shut with the panel showing; polling never stops behind it, so a
 * late recovery (ZCode installed mid-wait, the server coming back)
 * still opens the door on its own.
 * @returns {Promise<void>}
 */
export function bootGate() {
  if (!document.getElementById('boot')) return Promise.resolve(); // 无门可守（自定义嵌入页）
  window.__bootGateLive = true; // 接管完成：index.html 的 20s 兜底不再介入
  return new Promise((resolve) => {
    const text = $('#boot-status-text');
    const dots = $('#boot-dots');
    const fail = $('#boot-fail');
    const failText = $('#boot-fail-text');
    let misses = 0;
    let failHoldUntil = 0;     // 重新检测按下后的面板静默窗

    const say = (line, { waiting = true } = {}) => {
      text.textContent = line;
      if (dots) dots.hidden = !waiting;
    };
    const open = () => {
      if (entered) return;
      entered = true;
      resolve();
    };
    const showFail = (title, detail) => {
      failText.textContent = detail ? t('{a}：{b}', { a: title, b: detail }) : title;
      if (Date.now() >= failHoldUntil) fail.hidden = false;
    };

    $('#boot-retry').addEventListener('click', () => {
      fail.hidden = true;
      failHoldUntil = Date.now() + RETRY_HOLD;
      misses = 0;
      say(t('正在重新检测'));
    });

    // 复制错误：把面板上的整行失败原因交到剪贴板（拿去问人/贴给
    // AI/搜报错），反馈就用按钮自己的文案，不给启动门引 toast 依赖
    const copyBtn = $('#boot-copy');
    copyBtn.addEventListener('click', async () => {
      const ok = await copyFailText();
      copyBtn.textContent = ok ? t('已复制') : t('复制失败');
      clearTimeout(copyBtn.__t);
      copyBtn.__t = setTimeout(() => { copyBtn.textContent = COPY_LABEL; }, 1200);
    });

    const tick = async () => {
      let st = null;
      try {
        st = await getZCode();
        misses = 0;
      } catch {
        misses++;
      }
      switch (st?.state) {
        case 'ready':
          say(t('ZCode 已就绪'), { waiting: false });
          open();
          return;
        case 'off':
          say(t('成员调度已停用（--no-dispatch）'), { waiting: false });
          open();
          return;
        case 'booting':
          fail.hidden = true;
          // Detail 由服务端给（如「正在全盘搜索 ZCode…」）：没有就
          // 退回静态文案
          say(st.detail || t('正在识别 ZCode'));
          break;
        case 'warming':
          // 桥已认出，小助手的通道预热还在烧冷启动窗口——门再关片
          // 刻，进门即热通道；服务端封顶兜底，预热卡住不会焊死门。
          // 服务端 Detail 带已等待秒数，等待在走而不是卡死，看得见
          //（旧构建没有 Detail 就退回静态文案）
          fail.hidden = true;
          say(st.detail || t('正在预热模型通道'));
          break;
        case 'recruiting': {
          // 桥已就绪，启动编制（编排者小牛/HR 小马，Lv.8）还没招满——
          // 应用开张前必须满编：进度上墙（已到岗 n/N ＋ 当前正在招
          // 的岗位），满员后门自己开。招聘受阻（表损坏/出生被拒）不
          // 开逃生口：原因亮在状态行，服务端持续重试，修复即自动放行
          fail.hidden = true;
          const r = st.recruit || {};
          const cnt = r.total > 0 ? ` ${r.filled}/${r.total}` : '';
          let line = t('正在招聘启动编制{n}', { n: cnt });
          if (r.current) line += t(' · {cur}', { cur: r.current });
          if (r.blocked) line += t('（受阻：{why}，持续重试中）', { why: r.blocked });
          say(line);
          break;
        }
        case 'missing':
          // 服务端 10 秒一探：装好 ZCode 后无需任何操作，门自己开
          showFail(t('未识别到 ZCode'), t('未找到 {what}——安装 ZCode 后将自动识别并进入', { what: st.detail || 'ZCode CLI' }));
          say(t('正在等待 ZCode'));
          break;
        case 'error':
          // 桥的错误族：没起来 / 中途退出 / 哑了。标题不写「启动失败」
          // ——ZCode 桌面端可能开得好好的，断的是本应用自己拉起的
          // app-server 子进程；指引语（要不要重启应用）由服务端按
          // 具体原因给，中退场景是看门狗自动重启、无需重启。
          showFail(t('ZCode 通道异常'), st.detail || t('详见终端日志'));
          say(t('正在等待 ZCode'));
          break;
        default:
          // fetch miss / 404：服务还没起来，或这个构建没有 /zcode。
          // 宽限之后亮出失败面板——门不开，等服务回来
          if (misses >= MISS_GRACE) {
            showFail(t('无法连接办公室服务'), t('应用服务没有应答——请重启应用'));
            say(t('等待办公室服务'));
          } else {
            say(t('正在连接办公室'));
          }
      }
      setTimeout(tick, POLL_EVERY);
    };
    tick();
  });
}

/**
 * Close the splash: keep the door on screen a minimum beat (a flash
 * reads as a glitch, not a gate), then fade and drop the node.
 * Idempotent.
 */
export async function dismissBoot() {
  const MIN_BEAT = 600;
  const remain = MIN_BEAT - (performance.now() - t0);
  if (remain > 0) await new Promise((r) => setTimeout(r, remain));
  const el = $('#boot');
  if (!el) return;
  el.classList.add('gone');
  el.addEventListener('transitionend', () => el.remove(), { once: true });
  setTimeout(() => el.remove(), 800); // transitionend 的兜底（动画被关掉时也能摘掉节点）
}

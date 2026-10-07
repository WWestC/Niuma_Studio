// visitor.js — t_192（r_19）：访客态初始化分支。定稿 §四/§六：访客页
// 与主应用同壳同缓存（不另出 bundle），前端只做三件事——
//   ① 判别：location.pathname 以 /view/ 开头 → 访客态；
//   ② 裁剪：藏发送面/管理面/设置入口（UI 藏 + 服务端写拒双保险）；
//   ③ token：query ?t= 首见换 cookie（刷新免带 query——链接可分享
//      短形），cookie 在（服务端已验）即放行。
// 房主侧的访客计数角标（👁 N）也在这里：仅主应用（非访客态）挂载，
// 轮询 GET /visitor。

// 访客态判别（boot 最早调——后续所有分支读这个标记）

// i18n 的 t 在 initVisitor 里被局部 token 变量 t 遮蔽，按 people/detail.js
// 先例以别名引入。
import { t as i18nT } from './i18n.js';

export function visitorMode() {
  return globalThis.__NIUMA_VISITOR__ === true;
}

// visitorToken：本页的访客 token（query 优先，回落 cookie）。观察者
// hello 带上它，服务端只认带 token 的拨号为访客（限流/TTL 只落在
// 访客头上）——房主自己的窗口不带，永不进访客计数（仅缓存事故的
// 根治面）。非访客页返回 ''。
let vt = '';
export function visitorToken() { return vt; }

// initVisitor：/view/{project}?t=xxx 进入时设标记＋裁剪 UI＋存 cookie。
// 返回 true = 本页是访客态（app.js 据此跳过 owner 探测等房主专属 boot 段）。
export function initVisitor() {
  if (!location.pathname.startsWith('/view/')) return false;
  globalThis.__NIUMA_VISITOR__ = true;
  // token：query 首见 → cookie（path=/view 同域同源；服务端 t_191 的
  // cookie 换发契约）；已有 cookie 的直接链接刷新不再需要 query
  const t = new URLSearchParams(location.search).get('t');
  if (t) document.cookie = `niuma_vt=${encodeURIComponent(t)}; path=/view; max-age=14400`; // 4h 与 TTL 同拍
  vt = t || (document.cookie.split('; ')
    .find((row) => row.startsWith('niuma_vt='))?.slice('niuma_vt='.length) || '');
  try { vt = decodeURIComponent(vt); } catch { /* 原样保留——服务端逐字比对 */ }
  // UI 裁剪（§四清单）：管理/发送/设置入口整段隐藏——CSS 一层盖住比
  // 逐个 remove 温和（DOM 仍在但不可达；服务端写拒是第二道闸，UI 藏
  // 不是安全边界是体验边界——定稿双保险口径）
  const style = document.createElement('style');
  style.textContent = `
    #nav-people, #nav-project, #nav-files, #nav-term, #side-settings, #side-usage,
    #titlebar-tools,
    #composer, #owner-card, #owner-miss { display: none !important; }
    body.visitor #side { visibility: hidden; } /* 整侧栏收纳（仅留顶栏导航） */
  `;
  document.head.appendChild(style);
  document.body.classList.add('visitor');
  // 访客顶栏小条：明示只读态（「访客模式 · 只读浏览」——定稿 §四的
  // 可供性：访客自己也不该困惑为什么不能发言）
  const bar = document.createElement('div');
  bar.id = 'visitor-bar';
  bar.textContent = '👁 ' + i18nT('访客模式 · 只读浏览');
  document.body.appendChild(bar);
  return true;
}

// initVisitorBadge：房主侧的访客计数角标（👁 N，hover 明细）——仅非
// 访客态挂载（定稿：成员与访客不可见，前端在房主应用里渲染；数据源
// GET /visitor {on, count}，60s 慢轮询）。
export async function initVisitorBadge() {
  if (visitorMode()) return;
  const anchor = document.getElementById('side-usage')?.parentElement;
  if (!anchor) return;
  const el = document.createElement('button');
  el.type = 'button';
  el.id = 'visitor-badge';
  el.hidden = true;
  el.title = i18nT('访客通道：关闭（设置窗·关于页可开启）');
  anchor.insertBefore(el, anchor.firstChild);
  const tick = async () => {
    try {
      const r = await fetch('/visitor');
      if (!r.ok) { el.hidden = true; return; }
      const b = await r.json();
      if (b && b.on && b.count > 0) {
        el.textContent = `👁 ${b.count}`;
        el.title = i18nT('访客通道开启中：{n} 人正在驻足观看', { n: b.count });
        el.hidden = false;
      } else {
        el.hidden = true;
      }
    } catch { el.hidden = true; }
  };
  await tick();
  setInterval(tick, 60_000);
}

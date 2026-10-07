// extlink.js — 外链统一跳系统默认浏览器（对话/公告/文档正文共面）。
// 三处正文都出自 markdown.js 渲染的 .mlink，加上 /media 附件、/kb 文
// 档等站内绝对路径链接。原生壳（WKWebView/WebView2/WebKitGTK）没有
// 新窗口面：target=_blank 点击无处可去，无 target 的页内跳转会顶掉
// 整个工作台——壳装了 openExternal 桥（shell/open_external.go，scheme
// 白名单在壳侧），这里在 document 捕获阶段把可外开的链接拦下转交。
// 桥不存在（PWA/普通浏览器）则一根手指都不伸：原生 target=_blank 本
// 来就对，拦了反而多此一举。

// externalHref：一个 href 属性值 → 应交默认浏览器打开的绝对 URL，或
// null（不归本模块管）。与 markdown.js 的 safeHref 同一放行口径：
// http(s)/mailto 的绝对链接、站内绝对路径（/media、/kb/docs…，补全
// origin——同一台机的 127.0.0.1 服务浏览器可达）；#锚点、空值、相对
// 引用与其他 scheme（javascript: 等）一律不归管。
export function externalHref(raw, loc = globalThis.location) {
  if (!raw || raw.startsWith('#')) return null;
  if (!/^(https?:\/\/|mailto:)/.test(raw) && !raw.startsWith('/')) return null;
  let abs;
  try {
    abs = new URL(raw, loc && loc.href);
  } catch {
    return null;
  }
  if (abs.protocol === 'mailto:') return abs.href;
  if (abs.protocol !== 'http:' && abs.protocol !== 'https:') return null;
  return abs.href;
}

// extLinkHandler：捕获阶段的点击处理器（抽出来供冒烟直测——testdom
// 不走事件路由）。preventDefault 必须先于放行判断的失败面：桥在而
// 壳侧拒绝（理论上只有 scheme 闯关，JS 侧已同口径滤过）时静默吞掉，
// 不让异常冒进页面的任何处理器。
export function extLinkHandler() {
  return (ev) => {
    const open = globalThis.openExternal;
    if (typeof open !== 'function') return; // 无桥＝浏览器：原生行为自管
    const a = ev.target && ev.target.closest && ev.target.closest('a[href]');
    if (!a || (a.hasAttribute && a.hasAttribute('download'))) return; // 下载钮走原生语义
    const target = externalHref(a.getAttribute('href') || '');
    if (!target) return;
    ev.preventDefault();
    try {
      Promise.resolve(open(target)).catch(() => { /* 桥面故障：尽力即止 */ });
    } catch { /* 同步炸同理 */ }
  };
}

// installExtLinks：app.js 启动时装一次。捕获在 document 上——赶在任
// 何正文面的冒泡/stopPropagation 之前拿到点击。
export function installExtLinks(doc = document) {
  doc.addEventListener('click', extLinkHandler(), true);
}

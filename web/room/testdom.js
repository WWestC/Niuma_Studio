// testdom.js — t_182（r_17）：第一层 DOM 冒烟测试的极简基座。
// 评审定案（小狐实证＋小牛采纳）：手写 El stub 覆盖「innerHTML 型」渲染
// 面（构造即渲染的模块），零第三方依赖、零安装——r_08 的零依赖纪律不破。
// happy-dom 留作 settings/stream 两个重交互模块的可选项（第二层，另单）。
//
// 用法（*.dom.test.js）：
//   import { dom, fetchStub } from './testdom.js';
//   const { window, El } = dom();          // 每测试一个干净世界
//   fetchStub([{ body: '...' }]);           // 按序回 JSON（或传单值）
//   await import('./chronicle.js');        // 在 stub 世界里加载被测模块
//
// 关键设计：globalThis 的 document/fetch 在 dom() 时整体替换——被测模块
// 在 import 时可能已捕 document 引用（模块顶层），所以世界要在 import
// 之前立好；fetchStub 的响应契约按 wire/api.js getJSON 的真实面补齐
// （ok/status/headers.get/json——评审实测缺一就 HTTP undefined）。

// El：测试世界里的万能节点。innerHTML 是字符串槽（冒烟面只断言结构
// 字符串，不需要真 DOM 树）；querySelector 链返回惰性新节点（冒烟不
// 追交互，按钮 onclick 挂上就行）。
export class El {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.dataset = {};
    this.value = ''; // 表单件（input/select/textarea）的值槽
    // 选区槽（textarea/input 的 selectionStart/End + setSelectionRange）：
    // 真浏览器里设 value 会把光标归到末尾，stub 不模拟——用例自设选区，
    // 未设时按 0（与空 value 的真初值一致）
    this.selectionStart = 0;
    this.selectionEnd = 0;
    this.style = { setProperty() {}, removeProperty() {} };
    this.classList = {
      add() {}, remove() {}, toggle() {}, contains: () => false,
    };
    this._html = '';
  }
  set innerHTML(v) { this._html = String(v); this.children.length = 0; } // 真语义：整写 innerHTML 即换子
  get innerHTML() { return this._html; }
  set textContent(v) { this._html = String(v); this.children.length = 0; }
  get textContent() { return this._html; }
  set href(v) { this._href = v; }
  get href() { return this._href; }
  set onclick(f) { this._onclick = f; }
  get onclick() { return this._onclick; }
  // 吸收一个子节点（#fragment 摊平、真子记账 parentNode）——真 DOM 的
  // appendChild/insertBefore 对 DocumentFragment 是拼入其子再清空，stub
  // 同口径，流的 frag 拼装才不把行藏在假果里。
  #take(c) {
    const kids = c && c.tag === '#fragment' ? [...c.children] : [c];
    if (c && c.tag === '#fragment') c.children.length = 0;
    for (const k of kids) k.parentNode = this;
    return kids;
  }
  appendChild(c) { this.children.push(...this.#take(c)); return c; }
  append(...cs) { for (const c of cs) this.children.push(...this.#take(c)); }
  get childNodes() { return this.children; } // DocumentFragment 的拼装口（#appendRow 读它判空）
  insertBefore(el, ref) {
    const i = ref ? this.children.indexOf(ref) : -1;
    this.children.splice(i < 0 ? this.children.length : i, 0, ...this.#take(el));
    return el;
  }
  remove() { // 从真父上摘自己（parentNode 由 #take 记账；没挂过树＝no-op）
    if (!this.parentNode) return;
    const i = this.parentNode.children.indexOf(this);
    if (i >= 0) this.parentNode.children.splice(i, 1);
    this.parentNode = null;
  }
  setAttribute(k, v) { this.dataset[k] = v; }
  getAttribute(k) { return this.dataset[k] ?? null; }
  removeAttribute(k) { delete this.dataset[k]; }
  addEventListener() {} // 冒烟不测事件路由
  contains() { return false; }
  focus() {}
  select() {} // 表单件的全选（input.select()）——冒烟不测交互，no-op 同 focus
  setSelectionRange(s, e) { this.selectionStart = s; this.selectionEnd = e; }
  scrollTo(o) { if (o && typeof o.top === 'number') this.scrollTop = o.top; } // 流导航的平滑送位（stub 直落，无动画）
  scrollIntoView() {} // 跳转定位（stream 的引用跳转）——stub 无布局，no-op
  querySelector() { return new El('div'); }
  querySelectorAll() { return []; }
  closest() { return null; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 0, height: 0, right: 0, bottom: 0 }; }
}

// dom()：立一个测试世界（document/fetch/getComputedStyle 全套）。
// 返回 { window, El }——window 供需要 window.* 的模块（roomview 类不
// 进第一层，此面留最小占位）。
export function dom() {
  const doc = {
    createElement: (t) => new El(t),
    createDocumentFragment: () => new El('#fragment'),
    createTextNode: (t) => { const e = new El('#text'); e._html = String(t); return e; },
    body: new El('body'),
    documentElement: new El('html'),
    head: new El('head'),
    querySelector: () => new El('div'),
    querySelectorAll: () => [],
    getElementById: () => new El('div'),
    addEventListener() {},
    removeEventListener() {},
  };
  globalThis.document = doc;
  globalThis.window = {
    document: doc,
    addEventListener() {}, removeEventListener() {},
    location: { hash: '', host: 'localhost:7777', origin: 'http://localhost:7777', href: 'http://localhost:7777/app' },
    innerWidth: 1200, innerHeight: 800,
    getComputedStyle: () => ({ getPropertyValue: () => '' }),
  };
  globalThis.getComputedStyle = globalThis.window.getComputedStyle;
  // node ≥21 的 globalThis.navigator 是 getter-only——defineProperty 覆盖
  //（language 补 zh-CN：i18n 的跟随系统判定在测试世界里落在中文，
  // 与既有中文断言的冒烟口径一致）
  setGlobal('navigator', { userAgent: 'testdom', language: 'zh-CN', clipboard: { writeText: async () => true } });
  // ResizeObserver：重交互模块的常需件（stream 的贴底跟随等）——
  // 空实现（observe 即丢，不回调——冒烟不测布局响应）
  setGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  // matchMedia：流的平滑送位/回原位会问减动效偏好——stub 恒「不减」，
  // 走 scrollTo 分支（stub 里直落 scrollTop，时序确定）
  setGlobal('matchMedia', () => ({ matches: false }));
  setGlobal('localStorage', {
    _m: new Map(),
    getItem(k) { return this._m.has(k) ? this._m.get(k) : null; },
    setItem(k, v) { this._m.set(k, String(v)); },
    removeItem(k) { this._m.delete(k); },
  });
  return { window: globalThis.window, El };
}

// setGlobal：跨 node 版本的 globalThis 赋值（navigator 这类 getter-only
// 属性需要 defineProperty 强设；普通属性走直赋值）。
function setGlobal(key, value) {
  try { globalThis[key] = value; } catch {
    Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
  }
}

// fetchStub：按序回放 JSON 响应（getJSON 契约五面）。传数组＝按序取，
// 耗尽后重复末项；传单值＝恒回。响应对象带 ok/status/headers.get/json。
export function fetchStub(responses) {
  const list = Array.isArray(responses) ? responses : [responses];
  let i = 0;
  globalThis.fetch = async () => {
    const r = list[Math.min(i, list.length - 1)];
    i++;
    // 载荷形态：字符串/裸对象＝直当响应体（json 的返回值）；
    // {json:...|ok|status}＝响应配置；{body:...}＝body 包装（getJSON 直回）
    let cfg;
    if (typeof r === 'object' && r !== null && ('json' in r || 'ok' in r || 'status' in r)) cfg = r;
    else if (typeof r === 'object' && r !== null && 'body' in r) cfg = { json: r };
    else cfg = { json: r };
    return {
      ok: cfg.ok !== false,
      status: cfg.status ?? 200,
      headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
      json: async () => (cfg.json ?? cfg),
    };
  };
  return globalThis.fetch;
}

// resetDom：测试收尾清场（node --test 同进程跑多文件时防世界互渗）。
export function resetDom() {
  for (const k of ['document', 'window', 'getComputedStyle', 'navigator', 'localStorage', 'fetch', 'ResizeObserver', 'matchMedia', 'location']) {
    try { delete globalThis[k]; } catch { /* getter-only 的留给 setGlobal 语义 */ }
  }
}

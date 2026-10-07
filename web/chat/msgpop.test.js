// msgpop.test.js — 新消息提醒的兜底契约（弹窗失灵之夜的钉）：系统横幅
// 通道被判死（授权被拒）时反馈不许跟着死——页内兜底提醒框顶岗（同房
// 顶掉旧的，UN identifier 同语义），「去系统设置」指路每会话一次；通
// 道活着（granted）照投 osNotify、不出页内横幅；弹窗总闸（msgPopup）
// 关着则除了声音全静；窗口回焦复探——系统设置里改了主意，通道当场复
// 活并收回兜底。
//
// 驱动方式：DOM/浏览器假面只盖导入期与投递期的触点（feedback.js 的
// document.keydown、Notify 的 createElement/appendChild、prefs 的
// localStorage、sfx 的 Audio）；模块级会话账（探针缓存/指路一次）用
// import 查询串逐用例取新模块实例，互不渗账；横幅断言按 created 数组
// 的用例起点切片，跨用例不串账。
//
// 跑法：按仓库惯例的目录切片 `node --test web/chat/*.test.js`（单文件
// 亦净）。全仓 24 个测试文件一把并行汇总会卡在聚合上（node:test 多子
// 进程的调度怪癖，与用例正确性无关——`--test-concurrency=1` 串行全量
// 162/162 全绿即证）。

import test from 'node:test';
import assert from 'node:assert/strict';

// —— 假面（导入前就位：模块顶层就有 document.addEventListener）——
const created = []; // createElement 产出的全部假节点（fb-notify 断言用）
const fakeEl = (tag) => {
  const el = {
    tagName: tag, className: '', id: '', innerHTML: '', textContent: '',
    dataset: {}, style: { setProperty() {} }, children: [], __added: [],
    classList: {
      add: (...c) => el.__added.push(...c),
      remove: () => {}, contains: () => false,
    },
    addEventListener() {},
    appendChild(c) { c.__parent = el; el.children.push(c); return c; },
    prepend(c) { c.__parent = el; el.children.unshift(c); },
    // remove 必须真正脱离父容器：Notify 的「同屏最多 5 条」是
    // while (children.length > 5) firstElementChild.remove() ——空操作
    // 的 remove 会让它原地自旋，第 6 只横幅一到就冻死整个事件循环
    remove() {
      const p = el.__parent;
      if (p) {
        const i = p.children.indexOf(el);
        if (i >= 0) p.children.splice(i, 1);
      }
    },
    setAttribute() {}, focus() {},
    querySelector: () => null, querySelectorAll: () => [],
    closest: () => null,
    get firstElementChild() { return el.children[0] || null; },
  };
  created.push(el);
  return el;
};
globalThis.document = {
  hidden: false, addEventListener() {},
  createElement: fakeEl, body: fakeEl('body'),
  documentElement: { dataset: {}, style: { setProperty() {} } },
  querySelector: () => null,
};
globalThis.window = {
  handlers: {},
  addEventListener(type, fn) { this.handlers[type] = fn; },
};
globalThis.location = { hash: '', origin: 'http://127.0.0.1:7777' };
let prefStore = null; // 每用例可换的偏好真源（null＝localStorage 缺省）
globalThis.localStorage = {
  // lang:zh 恒并——裸 Node 的 navigator 是 en-US，而本面文案走 t()（i18n）
  getItem: () => (prefStore === null ? JSON.stringify({ lang: 'zh' }) : JSON.stringify({ lang: 'zh', ...prefStore })),
  setItem() {},
};
globalThis.Audio = class { constructor() {} play() { return Promise.resolve(); } };
globalThis.getComputedStyle = () => ({ getPropertyValue: () => '' });

const { Msg } = await import('../wire/wire.js');

/** 逐用例的新模块实例（查询串隔离会话账）＋按需装拆桥面。
 *  @param {{verdict?: string|(()=>string), osNotify?: Function, browser?: boolean}} [opts] */
async function freshPop(opts = {}) {
  if (opts.browser) {
    delete globalThis.window.osNotify;
    delete globalThis.window.osNotifyState;
  } else {
    globalThis.window.osNotify = opts.osNotify ?? (() => {});
    if (opts.verdict !== undefined) {
      globalThis.window.osNotifyState = () =>
        Promise.resolve(typeof opts.verdict === 'function' ? opts.verdict() : opts.verdict);
    } else {
      delete globalThis.window.osNotifyState;
    }
  }
  const mod = await import(`./msgpop.js?case=${Math.random().toString(36).slice(2)}`);
  return new mod.MsgPop({ book: { owner: { name: '房主' }, switchTo() {} } });
}

/** 纯微任务冲刷：探针→投递→兜底的 promise 链走完（不碰任何全局
 *  定时器——node:test 的簿记与它们共享全局，换桩等于埋雷）。Notify
 *  的 4s 自动消失定时器照常存在：断言在毫秒级完成，不受影响；套件
 *  收尾随最后一只定时器自然退出（≈4s）。 */
async function drain() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

/** 喂一条 alpha 房的消息并冲完微任务链。 */
async function feed(pop, from = '小鹿', text = '在吗') {
  pop.feed({ type: Msg.Say, from, text, ts: 1 }, { key: 'alpha', name: '项目甲' }, false);
  await drain();
}

/** 本用例内新建的横幅（按 created 起点切片，跨用例不串账）。 */
const bannersSince = (n) => created.slice(n).filter((el) => String(el.className).includes('fb-notify'));
const liveSince = (n) => bannersSince(n).filter((el) => !el.__added.includes('out'));

test('通道活着（granted）：照投 osNotify，页内不出横幅', async () => {
  const n0 = created.length;
  const posts = [];
  const pop = await freshPop({ verdict: 'granted', osNotify: (...a) => posts.push(a) });
  await feed(pop);
  assert.equal(posts.length, 1, '已许通道逐条照投');
  assert.equal(posts[0][0], '项目甲', '标题带房名');
  assert.equal(posts[0][2], 'alpha', 'thread 用房键（同房顶掉语义）');
  assert.equal(bannersSince(n0).length, 0, '页内不重复出横幅——系统横幅已在岗');
});

test('通道被判死（denied）：判死之后不投黑洞，页内兜底顶岗＋指路一次', async () => {
  const n0 = created.length;
  const posts = [];
  const pop = await freshPop({ verdict: 'denied', osNotify: (...a) => posts.push(a) });
  await feed(pop);
  // 首条在探针归来前照投（未知不装死，投了被系统丢不亏）——探针回来
  // 当场判死并兜底补位，这条消息只见面一次
  assert.equal(posts.length, 1, '判死之前的那条照投（探针是异步的）');
  assert.ok(liveSince(n0).some((el) => el.innerHTML.includes('macOS 拒绝了本应用的通知')), '指路横幅把话挑明（带去系统设置）');
  assert.ok(liveSince(n0).some((el) => el.innerHTML.includes('项目甲')), '兜底横幅顶岗（带房名与查看动作）');

  await feed(pop, '小马', '第二条');
  assert.equal(posts.length, 1, '判死之后不再投死通道');
  assert.equal(bannersSince(n0).filter((el) => el.innerHTML.includes('macOS 拒绝')).length, 1, '指路横幅每会话只出一次');
  assert.ok(liveSince(n0).some((el) => el.innerHTML.includes('小马：')), '同房新消息顶上');
  assert.ok(!liveSince(n0).some((el) => el.innerHTML.includes('小鹿：')), '同房旧兜底已收（UN identifier 同语义）');
});

test('弹窗总闸（msgPopup）关：声音之外全静——兜底横幅也不出', async () => {
  const n0 = created.length;
  prefStore = { msgPopup: false };
  try {
    const posts = [];
    const pop = await freshPop({ verdict: 'denied', osNotify: (...a) => posts.push(a) });
    await feed(pop);
    assert.equal(posts.length, 0);
    assert.equal(bannersSince(n0).length, 0, '总闸关＝弹窗一族全静（声音照响，那是 sfx 的事）');
  } finally {
    prefStore = null;
  }
});

test('浏览器面被拒：兜底横幅按浏览器口径指路，不再发 Notification', async () => {
  const n0 = created.length;
  const ctor = [];
  globalThis.Notification = Object.assign(function N(...a) { ctor.push(a); }, { permission: 'denied' });
  try {
    const pop = await freshPop({ browser: true });
    await feed(pop);
    assert.equal(ctor.length, 0, 'denied 之下不再构造 Notification（纠缠无益）');
    assert.ok(liveSince(n0).some((el) => el.innerHTML.includes('浏览器拦下了本站通知')), '指路横幅按浏览器口径说话');
    assert.ok(liveSince(n0).some((el) => el.innerHTML.includes('项目甲')), '兜底横幅照常顶岗');
  } finally {
    delete globalThis.Notification;
  }
});

test('自己的话不提醒（老契约零回退）', async () => {
  const n0 = created.length;
  const posts = [];
  const pop = await freshPop({ verdict: 'granted', osNotify: (...a) => posts.push(a) });
  await feed(pop, '房主', '我自己说的');
  assert.equal(posts.length, 0, '房主自己的话不投');
  assert.equal(bannersSince(n0).length, 0, '页内同样安静');
});

test('窗口回焦复探：系统设置里改了主意，通道当场复活并收回兜底', async () => {
  const n0 = created.length;
  const posts = [];
  let verdict = 'denied';
  const pop = await freshPop({ verdict: () => verdict, osNotify: (...a) => posts.push(a) });
  await feed(pop); // 判死＋兜底顶岗＋指路一次
  assert.ok(liveSince(n0).length >= 2, '判死时兜底在岗（指路＋顶岗）');

  verdict = 'granted'; // 用户去 系统设置→通知 拨开了
  globalThis.window.handlers.focus();
  await drain(); // 复探的微任务链走完

  await feed(pop, '小马', '复活之后');
  assert.equal(posts.length, 2, '复活后照投 osNotify（首条＋复活后一条）');
  assert.equal(liveSince(n0).length, 0, '兜底与指路横幅全部收回——页内外不双班并岗');
});

// 诊断尾巴（临时）：6s 后（越过全部 4s 横幅定时器）转储活跃句柄并强退
import fs from 'node:fs';
setTimeout(() => {
  fs.writeSync(2, '\n[handles] ' + process._getActiveHandles().map((h) =>
    h.constructor?.name + (h._onTimeout !== undefined ? '(timer)' : '')).join(', ') + '\n');
  process.exit(0);
}, 6000);

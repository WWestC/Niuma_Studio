// i18n.test.js — 语言框架（t() 中文键查找）的单测。覆盖：zh 原样返回
// （行为零变化）、en 查词典、缺键回落原文、{x} 占位代换、跟随系统启发
// 式（读不出系统语言回落中文）、setLang 落偏好＋推后端＋重载、静态
// DOM 标记（data-i 族）翻译。世界自立（localStorage/navigator/fetch/
// location），先立世界再动态 import——i18n 顶层只 import，无全局捕获，
// 但 postLang 走 globalThis.fetch，须在调用前替换。

import test from 'node:test';
import assert from 'node:assert/strict';

function world({ saved, navLang } = {}) {
  const store = new Map();
  if (saved) store.set('dh.ui.prefs', JSON.stringify(saved));
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
  };
  Object.defineProperty(globalThis, 'navigator', {
    value: { language: navLang }, configurable: true, writable: true,
  });
  const calls = [];
  globalThis.fetch = async (url, opts) => {
    calls.push({ url, opts });
    return {
      ok: true, status: 200,
      headers: { get: () => 'application/json' },
      json: async () => ({}),
    };
  };
  let reloads = 0;
  globalThis.location = { reload: () => { reloads++; } };
  return {
    calls,
    reloads: () => reloads,
    prefs: () => JSON.parse(store.get('dh.ui.prefs') || '{}'),
  };
}

// 极简节点（applyStaticI18n 只用 getAttribute/setAttribute/textContent）
function fakeEl(attrs) {
  return {
    textContent: '',
    _a: { ...attrs },
    getAttribute(k) { return k in this._a ? this._a[k] : null; },
    setAttribute(k, v) { this._a[k] = v; },
  };
}

test('zh：t() 原样返回键（含占位代换）——行为零变化', async () => {
  world({ navLang: 'zh-CN' });
  const { t } = await import('./i18n.js');
  assert.equal(t('发送'), '发送');
  assert.equal(t('另有 {n} 行未展示', { n: 3 }), '另有 3 行未展示');
});

test('en：手动偏好优先，t() 查词典；带占位键英文语序成立', async () => {
  world({ saved: { lang: 'en' }, navLang: 'zh-CN' });
  const { t } = await import('./i18n.js');
  assert.equal(t('发送'), 'Send');
  assert.equal(t('{n} 分钟前', { n: 5 }), '5m ago');
  assert.equal(t('另有 {n} 行未展示', { n: 3 }), '3 more rows not shown');
});

test('en：缺键回落中文原文（未迁移文案不破相）', async () => {
  world({ saved: { lang: 'en' } });
  const { t } = await import('./i18n.js');
  assert.equal(t('这条键没收录过'), '这条键没收录过');
});

test('跟随系统：zh* → zh，其余 → en，读不出系统语言回落 zh', async () => {
  const { langPref } = await import('./i18n.js');
  world({ navLang: 'zh-CN' });
  assert.equal(langPref(), 'zh');
  world({ navLang: 'zh-TW' });
  assert.equal(langPref(), 'zh');
  world({ navLang: 'en-US' });
  assert.equal(langPref(), 'en');
  world({ navLang: undefined }); // 无头环境兜底：默认中文
  assert.equal(langPref(), 'zh');
  world({ saved: { lang: 'en' }, navLang: 'zh-CN' }); // 手动值盖过系统
  assert.equal(langPref(), 'en');
});

test('setLang：落偏好＋POST /lang＋整页重载；同语点击只补对齐推送', async () => {
  const w = world({ navLang: 'zh-CN' });
  const { setLang } = await import('./i18n.js');
  setLang('en');
  assert.equal(w.prefs().lang, 'en', '偏好已落');
  assert.equal(w.reloads(), 1, '整页重载一次');
  assert.equal(w.calls.length, 1, '推了一次后端');
  assert.equal(w.calls[0].url, '/lang');
  assert.equal(w.calls[0].opts.body, JSON.stringify({ lang: 'en' }));
  const reloadsBefore = w.reloads();
  setLang('en'); // 同语：不重载不落盘，但补一发对齐推送（后端可能被外来写入捋偏）
  assert.equal(w.reloads(), reloadsBefore, '同语不重载');
  assert.equal(w.prefs().lang, 'en', '偏好未被重写');
  assert.equal(w.calls.length, 2, '同语也推一次（幂等对齐）');
  assert.equal(w.calls[1].opts.body, JSON.stringify({ lang: 'en' }));
  setLang('fr'); // 非法值：拒绝
  assert.equal(w.reloads(), reloadsBefore, '非法值不折腾');
  assert.equal(w.calls.length, 2, '非法值不推送');
});

test('langPicked：手动选过才算数——跟随系统的自动判定不算（写后端的资格线）', async () => {
  const { langPicked } = await import('./i18n.js');
  world({ navLang: 'en-US' });
  assert.equal(langPicked(), false, '跟随系统（哪怕是 en）＝没选过');
  world({ saved: { theme: 'light' }, navLang: 'zh-CN' });
  assert.equal(langPicked(), false, '有别的偏好但没有 lang＝没选过');
  world({ saved: { lang: 'zh' }, navLang: 'en-US' });
  assert.equal(langPicked(), true, '手动 zh 算（系统相反也认手动）');
  world({ saved: { lang: 'fr' }, navLang: 'zh-CN' });
  assert.equal(langPicked(), false, '非法存量不算');
});

test('applyStaticI18n：data-i（文本）/data-i-ph（placeholder）/data-i-title（title）都翻', async () => {
  world({ saved: { lang: 'en' } });
  const { applyStaticI18n } = await import('./i18n.js');
  const text = fakeEl({ 'data-i': '发送' });
  const ph = fakeEl({ 'data-i-ph': '搜索' });
  const title = fakeEl({ 'data-i-title': '设置' });
  const root = { querySelectorAll: () => [text, ph, title] };
  applyStaticI18n(root);
  assert.equal(text.textContent, 'Send');
  assert.equal(ph.getAttribute('placeholder'), 'Search');
  assert.equal(title.getAttribute('title'), 'Settings');
});

test('en 渲染冒烟：格式化器与共享组件默认文案出英文', async () => {
  world({ saved: { lang: 'en' } });
  // dom.js 的 fmtAge 与 usageview 的数量级念法（zh 路径另有既有测试钉）
  const { fmtAge } = await import('./dom.js');
  const now = Date.now();
  assert.equal(fmtAge(Math.floor(now / 1000) - 30, now), 'just now');
  assert.equal(fmtAge(Math.floor(now / 1000) - 5 * 60, now), '5m ago');
  const { fmtTokens, fmtDur } = await import('./usageview.js');
  assert.equal(fmtTokens(474790418), '474.79 M', '英文数量级走 K/M/B');
  assert.equal(fmtDur(65000), '1m 5s');
  const { turnStatusText } = await import('./usageview.js');
  assert.equal(turnStatusText('completed'), 'done');
});

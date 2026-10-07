// i18n.js — 界面语言（中文/英文）。设计口径见 CONTRIBUTING 与提交说明：
// 以中文原文为字典键的 t() 查找——不为几百条文案发明键名，迁移就是把
// 内联中文包一层函数，可逐文件推进、缺失翻译优雅回落中文（zh 模式
// t() 原样返回键，行为零变化；en 模式查 i18n_en.js 词典，查不到回落
// 原文）。带参数的文案用 {x} 占位：t('另有 {n} 行未展示', {n: 3})——
// 英文语序不再受中文拼接顺序拖累。
//
// 偏好沿用 dh.ui.prefs 键族（同 composer 草稿/主题的存法），但本模块
// 自读自写、不 import settings.js——settings 要用 t()，反着引会成环
//（reqs.js 直读同一键blob 是既有先例）。langPref() 未手动选过时不落
// 盘、按 navigator.language 现判（zh* → zh，否则 en）：跟随系统但把
// 选择权留给用户第一次点分段器。
//
// 切换走整页重载：全部板块 DOM 都在启动时由 JS 构建，逐板块实时重渲染
// 是雷区；本地应用重载很快（启动门本来就每次都过）。setLang() 落偏好
// → 顺手把语言推给后端（后端文案跟着换，fire-and-forget）→ reload。
//
// 谁能写后端：只有手动选择。启动推送（app.js）以 langPicked() 门住——
// 跟随系统的自动判定只管本窗显示、永不推送：后端语言是全工作室单一
// 真源，任何英文环境的页面（自动化浏览器、别的机器代开）一加载就把它
// 连同落盘捋走的事故在这堵上。同语点击仍补一发对齐推送（幂等）：后端
// 被外来写入捋偏时点一下当前语言即复位，不必等下一次整页重载。
//
// 静态 HTML 文本（侧栏导航、聊天壳等 index.html 里写死的中文）无法调
// t()：给节点标 data-i（文本）/ data-i-ph（placeholder）/ data-i-title
//（title）/ data-i-aria（aria-label），属性值即中文原文（HTML 保真源），
// applyStaticI18n() 启动时翻译。启动门更早——它的门面文案由 index.html
// 的门闩内联脚本在模块加载前现翻（同一套启发式）。

import { postLang } from '../wire/api.js';
import EN from './i18n_en.js';

const PREF_KEY = 'dh.ui.prefs';

function rawPrefs() {
  try { return JSON.parse(localStorage.getItem(PREF_KEY)) || {}; } catch { return {}; }
}

/** 当前生效语言：'zh' | 'en'。手动选过（dh.ui.prefs.lang）尊重手动值；
 *  未选过跟随系统语言现判——不落盘，用户第一次点分段器才持久化。
 *  读不出系统语言（无头测试等）回落中文：真实浏览器总有该字段，
 *  这里只是兜底，不影响产品语义。 */
export function langPref() {
  const v = rawPrefs().lang;
  if (v === 'zh' || v === 'en') return v;
  const nav = typeof navigator !== 'undefined' && navigator.language ? navigator.language : '';
  if (!nav) return 'zh';
  return /^zh/i.test(nav) ? 'zh' : 'en';
}

/** 手动选过语言吗（dh.ui.prefs.lang 落过 zh|en）？写后端的资格线：
 *  跟随系统的自动判定只管本窗显示，永不推送（见文件头注）。 */
export function langPicked() {
  const v = rawPrefs().lang;
  return v === 'zh' || v === 'en';
}

/** BCP47 语区标签（Intl/locales 用）：en → 'en'，zh → 'zh-CN'。 */
export function localeTag() { return langPref() === 'en' ? 'en' : 'zh-CN'; }

/** 当前是否英文（个别有英文专属口径的分支用，如数量级单位 K/M/B）。 */
export function isEn() { return langPref() === 'en'; }

function subst(str, params) {
  if (!params) return str;
  return String(str).replace(/\{(\w+)\}/g, (m, k) => (k in params ? String(params[k]) : m));
}

/**
 * 查一条文案。src 即中文原文（键）；params 填 {x} 占位。
 * zh 模式原样返回（占位照替换），en 模式查词典、缺失回落原文。 */
export function t(src, params) {
  const out = langPref() === 'en' && Object.prototype.hasOwnProperty.call(EN, src) ? EN[src] : src;
  return subst(out, params);
}

/**
 * 切语言：落偏好 → 通知后端（失败静默——服务端回落默认中文）→ 整页
 * 重载。同语点击不重载不落盘，但补一发对齐推送（幂等）——后端可能被
 * 外来写入捋偏，点一下当前语言即复位。 */
export function setLang(lang) {
  if (lang !== 'zh' && lang !== 'en') return;
  if (lang === langPref()) {
    postLang(lang).catch(() => {});
    return;
  }
  try {
    const all = rawPrefs();
    all.lang = lang;
    localStorage.setItem(PREF_KEY, JSON.stringify(all));
  } catch { /* 私密模式：内存态仍生效（本页重载前） */ }
  postLang(lang).catch(() => {});
  location.reload();
}

/** 启动时把 <html lang> 与文档标题拉齐当前语言（head 内联脚本已先落
 *  过一遭，这里兜底＋负责切语言后的新文档）。 */
export function applyLangMeta() {
  const en = langPref() === 'en';
  document.documentElement.lang = en ? 'en' : 'zh-CN';
  document.title = en ? 'Niuma Studio' : '牛马工作室';
}

const STATIC_ATTRS = [
  ['data-i', null],        // 纯文本节点（节点内容必须是纯文本）
  ['data-i-ph', 'placeholder'],
  ['data-i-title', 'title'],
  ['data-i-aria', 'aria-label'],
];

/** 翻译静态 DOM 里带 data-i 系列标记的节点（见文件头注）。幂等：再次
 *  调用按属性里的中文原文重查（不叠加翻译）。 */
export function applyStaticI18n(root) {
  const scope = root || document;
  if (typeof scope.querySelectorAll !== 'function') return;
  for (const [attr, prop] of STATIC_ATTRS) {
    for (const el of scope.querySelectorAll(`[${attr}]`)) {
      const src = el.getAttribute(attr);
      if (!src) continue;
      const val = t(src);
      if (prop) el.setAttribute(prop, val);
      else el.textContent = val;
    }
  }
}

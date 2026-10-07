// markdown.js — 会话正文的轻量 Markdown 渲染器（牛马多为 AI，正文按
// md 读）：零依赖、先全量转义再组装 HTML（AI 输出是不可信文本，源里
// 的原始 HTML 一律按字面显示），链接与图片只放行 http(s)/mailto/站内
// 绝对路径。块级支持围栏代码块、标题、引用、有序/无序列表（缩进
// 嵌套）、GFM 表格、分割线；段内单换行折 <br>（聊天排版，空行分段）。
// 行内支持代码、粗斜体、删除线、[文本](链接)、![图](链接)与裸 URL。
// 平台 token 装扮（t_NN 任务 chip、@提及）经 opts.dress 注入 —— 与
// stream.js 的 dress 同一函数，在哨兵保护的纯文本段上执行。

import { esc } from '../ui/dom.js';

// bare links: from scheme to whitespace; CJK/ASCII trailing punctuation
// is trimmed in the callback so a sentence-final URL doesn't eat the 。
const URL_RE = /https?:\/\/[^\s，。；：！？、）】」》〉"']+/g;

const FENCE_RE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^ \t]*)[ \t]*$/;
const HEAD_RE = /^ {0,3}(#{1,6}) +(.*?)\s*#*\s*$/;
const HR_RE = /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;
const QUOTE_RE = /^ {0,3}>[ \t]?(.*)$/;
const LIST_RE = /^(\s*)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/;
const TABLE_SPLIT_RE = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;

// 强调规则按特异性降序逐条替换；每条产物立刻寄存为哨兵，后续规则
// 与 dress 只见纯文本，永不互相改写对方的标签。
const EMPH_RULES = [
  [/\*\*\*([^]+?)\*\*\*/g, (t) => `<strong><em>${t}</em></strong>`],
  [/___([^]+?)___/g, (t) => `<strong><em>${t}</em></strong>`],
  [/\*\*([^]+?)\*\*/g, (t) => `<strong>${t}</strong>`],
  [/(?<![\w_])__([^]+?)__(?![\w_])/g, (t) => `<strong>${t}</strong>`],
  [/(?<![\w*])\*([^*\n]+?)\*(?![\w*])/g, (t) => `<em>${t}</em>`],
  [/(?<![\w_])_([^_\n]+?)_(?![\w_])/g, (t) => `<em>${t}</em>`],
  [/~~([^~\n]+?)~~/g, (t) => `<del>${t}</del>`],
];

/** 链接目标放行：http(s)/mailto/站内绝对路径，其余（javascript: 等）按字面显示。 */
function safeHref(url) {
  return /^(https?:\/\/|mailto:)/.test(url) || url.startsWith('/');
}

/**
 * 一段行内文本 → HTML：转义后依次收行内代码、图、链、裸 URL（寄存
 * 哨兵），跑 dress（任务 chip / @提及），再跑强调规则，最后还原哨兵、
 * 换行折 <br>。dress 与强调只见「纯转义文本 + 哨兵」，chip 文本
 * （t_NN）不构成任何裸标记，互不误伤。
 * @param {string} text @param {((s: string) => string)|null} dress
 */
function inlineHTML(text, dress) {
  const slots = [];
  const park = (html) => (slots.push(html), `\x00${slots.length - 1}\x00`);
  let s = esc(text).replace(/\x00/g, ''); // 哨兵字符不入正文

  // 行内代码：内容原样（已转义），不做链接/装扮/强调
  s = s.replace(/(`+)([^]+?)\1/g, (m, ticks, code) =>
    park(`<code class="md-code">${code.trim()}</code>`));

  // 图片与链接：文本/alt 保持已转义形态；title（"…"）仅支持转义引号形态
  s = s.replace(/!\[([^[\]]*)\]\(([^()\s]+)(?:[ \t]+&quot;[^()]*&quot;)?\)/g, (m, alt, url) =>
    safeHref(url) ? park(`<img class="md-img" src="${url}" alt="${alt}" loading="lazy">`) : m);
  s = s.replace(/\[([^[\]]+)\]\(([^()\s]+)(?:[ \t]+&quot;[^()]*&quot;)?\)/g, (m, txt, url) =>
    safeHref(url)
      ? park(`<a class="mlink" href="${url}" target="_blank" rel="noopener noreferrer">${txt}</a>`)
      : m);

  // 裸链接：句尾的中英文标点修剪掉（承接 stream.js 的原规则）
  s = s.replace(URL_RE, (m0) => {
    let href = m0;
    while (/[.,;:!?)）】」》〉、。，；：！？]$/.test(href)) href = href.slice(0, -1);
    return park(`<a class="mlink" href="${href}" target="_blank" rel="noopener noreferrer">${href}</a>`) +
      m0.slice(href.length);
  });

  if (dress) s = dress(s);
  for (const [re, wrap] of EMPH_RULES) s = s.replace(re, (...a) => park(wrap(a[1])));

  return s
    .replace(/\n/g, '<br>')
    .replace(/\x00(\d+)\x00/g, (_, n) => slots[+n]);
}

/** @param {string} line 展开制表后的行首缩进宽度 */
function indentOf(line) {
  return line.replace(/\t/g, '  ').match(/^\s*/)[0].length;
}

/** 拆一行表格为单元格：剥首尾竖线后按 | 切，保端对齐靠调用方补空串。 */
function tableCells(line) {
  return line.trim().replace(/^\|/, '').replace(/\|[ \t]*$/, '').split('|').map((c) => c.trim());
}

/**
 * 一个列表区（同基准缩进）→ <ul>/<ol> + 已消费行数。项内容剥一层
 * 缩进后整体递归 markdown()，嵌套列表/引用/代码块自然成立。
 */
function buildList(lines, start, opts) {
  const base = indentOf(lines[start]);
  const kind = (l) => (/^\d/.test(LIST_RE.exec(l)[2]) ? 'ol' : 'ul');
  const want = kind(lines[start]);
  const items = [];
  let i = start;

  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { // 空行收尾：后随同类型同级项才续（松散列表）
      let j = i;
      while (j < lines.length && !lines[j].trim()) j++;
      if (j < lines.length && indentOf(lines[j]) === base && LIST_RE.test(lines[j]) && kind(lines[j]) === want) { i = j; continue; }
      break;
    }
    const m = LIST_RE.exec(line);
    if (m && indentOf(line) === base && kind(line) === want) {
      const itemLines = [m[3]];
      i++;
      while (i < lines.length) {
        const l2 = lines[i];
        if (!l2.trim()) { // 项内空行：后随更深缩进则并入（松散续段）
          let j = i;
          while (j < lines.length && !lines[j].trim()) j++;
          if (j < lines.length && lines[j].trim() && indentOf(lines[j]) > base) {
            for (let k = i; k < j; k++) itemLines.push('');
            i = j;
            continue;
          }
          break;
        }
        if (indentOf(l2) > base) {
          const n = indentOf(l2);
          const cut = Math.min(n, base + 2);
          itemLines.push(' '.repeat(n - cut) + l2.trimStart());
          i++;
          continue;
        }
        break;
      }
      items.push(itemLines);
      continue;
    }
    break; // 缩进回退或非列表行：本层结束
  }

  const html = items.map((ls) => `<li>${markdown(ls.join('\n'), opts)}</li>`).join('');
  return { html: `<${want} class="md-list">${html}</${want}>`, next: i };
}

/**
 * 正文 → 安全 HTML。空行分段、段内单换行折 <br>；围栏代码块/标题/
 * 引用/列表/表格/分割线走块级分支。opts.dress 见 inlineHTML。
 * @param {string} src @param {{dress?: (s: string) => string}} [opts]
 * @returns {string}
 */
export function markdown(src, opts = {}) {
  const dress = typeof opts.dress === 'function' ? opts.dress : null;
  const o = dress ? opts : { ...opts, dress: null };
  const lines = String(src ?? '').replace(/\r\n?/g, '\n').replace(/\t/g, '  ').split('\n');
  const out = [];
  let para = [];
  let i = 0;

  const flushPara = () => {
    if (para.length) out.push(`<p class="md-p">${inlineHTML(para.join('\n'), dress)}</p>`);
    para = [];
  };

  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { flushPara(); i++; continue; }

    const fence = FENCE_RE.exec(line);
    if (fence) {
      flushPara();
      const ch = fence[1][0];
      const isClose = (l) => {
        const m = /^ {0,3}(`+|~+)/.exec(l);
        return !!m && m[1][0] === ch && !l.slice(m[0].length).trim();
      };
      const body = [];
      i++;
      while (i < lines.length && !isClose(lines[i])) { body.push(lines[i]); i++; }
      if (i < lines.length) i++; // 无闭合栏则一路吃到结尾
      out.push(`<pre class="md-pre"${fence[2] ? ` data-lang="${esc(fence[2])}"` : ''}><code>${esc(body.join('\n'))}</code></pre>`);
      continue;
    }

    const head = HEAD_RE.exec(line);
    if (head) {
      flushPara();
      out.push(`<h${head[1].length} class="md-h md-h${head[1].length}">${inlineHTML(head[2], dress)}</h${head[1].length}>`);
      i++;
      continue;
    }

    if (HR_RE.test(line)) { flushPara(); out.push('<hr class="md-hr">'); i++; continue; }

    if (QUOTE_RE.test(line)) {
      flushPara();
      const inner = [];
      while (i < lines.length) {
        const m = QUOTE_RE.exec(lines[i]);
        if (!m) {
          // 引用的 lazy 延续：空行终止，其后缩进行的并入
          if (!lines[i].trim() || !inner.length || !/^ {2,}/.test(lines[i])) break;
          inner.push(lines[i].replace(/^ {2}/, ''));
          i++;
          continue;
        }
        inner.push(m[1]);
        i++;
      }
      out.push(`<blockquote class="md-quote">${markdown(inner.join('\n'), o)}</blockquote>`);
      continue;
    }

    if (/^ {0,3}\|/.test(line) && i + 1 < lines.length && TABLE_SPLIT_RE.test(lines[i + 1])) {
      flushPara();
      const aligns = tableCells(lines[i + 1]).map((c) =>
        c.startsWith(':') && c.endsWith(':') ? 'center' : c.endsWith(':') ? 'right' : '');
      const cols = tableCells(line).length;
      const cell = (c, x, tag) =>
        `<${tag}${aligns[x] ? ` style="text-align:${aligns[x]}"` : ''}>${inlineHTML(c, dress)}</${tag}>`;
      const heads = tableCells(line);
      i += 2;
      const rows = [];
      while (i < lines.length && /^ {0,3}\|/.test(lines[i])) { rows.push(tableCells(lines[i])); i++; }
      const th = heads.map((c, x) => cell(c, x, 'th')).join('');
      const tb = rows.map((r) => `<tr>${heads.map((_, x) => cell(r[x] ?? '', x, 'td')).join('')}</tr>`).join('');
      out.push(`<div class="md-table-wrap"><table class="md-table"><thead><tr>${th}</tr></thead><tbody>${tb}</tbody></table></div>`);
      continue;
    }

    if (LIST_RE.test(line)) {
      flushPara();
      const built = buildList(lines, i, o);
      out.push(built.html);
      i = built.next;
      continue;
    }

    para.push(line.trim());
    i++;
  }
  flushPara();
  return out.join('');
}

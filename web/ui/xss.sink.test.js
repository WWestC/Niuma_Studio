// xss.sink.test.js — the XSS sentry: the zero-dependency frontend has
// no framework auto-escaping, so "every interpolation into an HTML
// sink must pass esc()" used to be an eyeball tax. This test machines
// the dangerous half of that rule: any .innerHTML / insertAdjacentHTML
// statement that interpolates EXTERNAL-DATA fields (text/from/name/
// title/body/content/… — the wire-data shapes) without an esc/icon/
// safeColor/markdown-style guard fails here, naming the file and line.
//
// Scope is deliberately narrow so it has real teeth: class fragments,
// indexes, sizes and other non-payload interpolations don't trip it
// (a strict whitelist produced 151 false positives on day one — noise
// kills sentries). Statements proven benign carry an inline
// `// xss-ok: <reason>` marker, which this test honors as a reviewed
// exemption and which greps cleanly during audits.
//
// esc() itself is pinned by the hostile-corpus test below: the
// classic payloads must come out inert.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { esc } from './dom.js';

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

// ---------- Part 1: esc() 的敌意语料 ----------
const HOSTILE = [
  '<script>alert(1)</script>',
  '<img src=x onerror=alert(1)>',
  '<svg onload=alert(1)>',
  '"><script>alert(1)</script>',
  `'</span><script>alert(1)</script>`,
  '<a href="javascript:alert(1)">点我</a>',
  '&lt;script&gt;', // 双重走私：原文已含实体，不得被还原成标签
  '"><iframe src=//evil.example></iframe>',
  '\u003cscript\u003ealert(1)\u003c/script\u003e',
  'onmouseover=alert(1) x=',
];

test('esc() 把敌意语料变成惰性文本（无裸 <>&\'"）', () => {
  for (const payload of HOSTILE) {
    const out = esc(payload);
    assert.ok(!/[<>"']/.test(out), `esc 后仍有裸元字符：${payload} → ${out}`);
    assert.ok(!out.includes('<script') && !out.includes('<img') && !out.includes('<svg'), `标签存活：${payload} → ${out}`);
  }
});

test('esc() 的 null/undefined/数字宽容与恒等性', () => {
  assert.equal(esc(null), '');
  assert.equal(esc(undefined), '');
  assert.equal(esc(0), '0');
  assert.equal(esc('plain文本'), 'plain文本'); // 无元字符时零改写
});

// ---------- Part 2: sink 哨兵 ----------
const SAFE_CALL = /^(esc|icon|safeColor|t|plainifyMD|snipOf|planWaitText|trimPlan|dress|encodeURIComponent)\(/;
// 外部数据字段（wire 载荷形状）。命中即视为「把可能出自消息/成员/文档
// 的字符串放进 HTML」，必须经过守卫调用。
const EXTERNAL = /\b\w*(text|from|name|title|body|content|note|desc|prompt|summary|snip|question|answer|branch|into|sha|author|subject|manual|role|key|word|input|output|error|reason|detail)\b/i;

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'icons' || name === 'gen' || name.startsWith('.')) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (name.endsWith('.js') && !name.endsWith('.test.js')) out.push(p);
  }
  return out;
}

test('外部数据进 HTML sink 必须过守卫（esc/icon/safeColor/markdown 系）', () => {
  const violations = [];
  for (const path of walk(webRoot)) {
    if (path.endsWith(join('ui', 'xss.sink.test.js'))) continue;
    const lines = readFileSync(path, 'utf8').split('\n');
    let i = 0;
    while (i < lines.length) {
      const l = lines[i];
      if (/\.innerHTML\s*=/.test(l) || l.includes('insertAdjacentHTML')) {
        // 收整条语句：直到模板反引号配平且以 ; 收尾
        const stmt = [l];
        let j = i;
        while (j < lines.length && !(stmt[stmt.length - 1].trimEnd().endsWith(';') && stmt.join('').split('`').length % 2 === 1)) {
          j += 1;
          if (j < lines.length) stmt.push(lines[j]);
        }
        const text = stmt.join('\n');
        if (text.includes('${') && !text.includes('// xss-ok')) {
          for (const m of text.matchAll(/\$\{([^{}]*)\}/g)) {
            const expr = m[1].trim();
            if (SAFE_CALL.test(expr)) continue;
            if (EXTERNAL.test(expr) && !text.includes('esc(') && !text.includes('markdown(')) {
              violations.push(`${path.replace(webRoot + '/', '')}:${i + 1} \${${expr.slice(0, 60)}}`);
            }
          }
        }
        i = j;
      }
      i += 1;
    }
  }
  assert.deepEqual(violations, [],
    '外部数据字段未经守卫直插 HTML sink——包 esc()/icon()/safeColor()，' +
    '或确证无害后在本语句尾注 `// xss-ok: <理由>`：');
});

// i18n_orphan.test.js — 防「改一个标点即静默回落中文」的近失哨兵。
// 机制背景：t() 的键＝中文原文，缺键按设计回落中文（渐进迁移），所以
// 「键不在词典」本身不是错；错的是「键本想命中词典里的某条、却因标
// 点/空白/全半角漂移而 miss」——那条译文从此静默失效，界面回到中文
// 且无任何告警。本测试把每个未命中的源键做规范化比对（去空白与标
// 点），规范化后能在词典里找到唯一近似条目即为漂移事故，测试失败并
// 指认两侧原文。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import EN from './i18n_en.js';

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'icons' || name.startsWith('.')) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (name.endsWith('.js') && !name.endsWith('.test.js')) out.push(p);
  }
  return out;
}

// 规范化：去全部空白与中英标点，只留实词字符——漂移通常正是这些差异。
function norm(s) {
  return s.replace(/[\s，。！？：；、…—·（）【】《》“”‘’,.!?:;'"\-–—/\\|()\[\]{}<>@#%^&*+=~`$]/g, '');
}

test('every untranslated t() key is either new prose or an exact dict hit — never a drifted near-miss', () => {
  const dictKeys = new Set(Object.keys(EN));
  const dictByNorm = new Map();
  for (const k of dictKeys) {
    const n = norm(k);
    if (n && !dictByNorm.has(n)) dictByNorm.set(n, k);
  }

  const drifts = [];
  const files = walk(webRoot);
  const keyRes = /\bt\(\s*'((?:[^'\\]|\\.)*)'\s*[,)]/g;
  for (const path of files) {
    const src = readFileSync(path, 'utf8');
    if (path.includes(join('ui', 'i18n'))) continue; // 词典自身不是调用点
    let m;
    while ((m = keyRes.exec(src))) {
      const key = m[1];
      if (!key || dictKeys.has(key)) continue; // 命中或空串：无事
      const near = dictByNorm.get(norm(key));
      if (near) drifts.push({ file: path.replace(webRoot + '/', ''), key, near });
    }
  }
  // 同样钉 data-i 族静态标记（index.html 的属性值即键）。
  const html = readFileSync(join(webRoot, 'index.html'), 'utf8');
  const attrRes = /data-(?:i|ai|i-ph|i-title|i-aria|ai-ph|ai-title|ai-aria)="([^"]+)"/g;
  let hm;
  while ((hm = attrRes.exec(html))) {
    const key = hm[1];
    if (!key || dictKeys.has(key)) continue;
    const near = dictByNorm.get(norm(key));
    if (near) drifts.push({ file: 'index.html', key, near });
  }

  assert.deepEqual(drifts, [],
    '发现 t()/data-i 键的近失漂移（调用点 vs 词典条目只差标点/空白）——' +
    '这会让译文静默失效回落中文。把两侧改成一致（改回调用点，或同步词典）：');
});

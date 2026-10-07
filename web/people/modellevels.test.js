// modellevels.test.js — 思考强度下拉的选项生成契约（modellevels.js
// 是出生表单与改档共用的单一入口）：默认项恒在、按模型档位表出档、
// 当前档存活或追加「当前」自选项（下拉不说谎）、模型留空时挂默认模型
// 的档位表（ZCode 同口径——默认模型也是真模型），连默认模型都未知
// 才禁用。

import test from 'node:test';
import assert from 'node:assert/strict';

import { fillReasoningOptions, levelLabel } from './modellevels.js';
// zh 钉子：裸 Node 的 navigator.language 是 en-US，本文件断言的中文文案
// 已全线走 t()（i18n.js）——先钉 zh 偏好再断言（usage.test.js 同款；
// en 面归 ui/i18n.test.js）。t() 调用时才读偏好，import 后立桩即可。
try {
  globalThis.localStorage = {
    _m: new Map([['dh.ui.prefs', JSON.stringify({ lang: 'zh' })]]),
    getItem(k) { return this._m.has(k) ? this._m.get(k) : null; },
    setItem(k, v) { this._m.set(k, String(v)); },
  };
} catch { /* 已有可写 localStorage 的环境照用 */ }


// 极小 select 替身：innerolfHTML/insertAdjacentHTML 落串并同步 options
// （value 匹配），够 fillReasoningOptions 走完整路径。
function fakeSelect() {
  const sel = {
    disabled: false,
    value: '',
    options: [],
    _html: '',
    set innerHTML(v) {
      this._html = v;
      this.options = [...v.matchAll(/value="([^"]*)"/g)].map((m) => ({ value: m[1] }));
    },
    get innerHTML() { return this._html; },
    insertAdjacentHTML(_pos, frag) {
      this._html += frag;
      this.options.push(...[...frag.matchAll(/value="([^"]*)"/g)].map((m) => ({ value: m[1] })));
    },
  };
  return sel;
}

const LEVELS = { 'GLM-5.3': ['low', 'high', 'max'] };

test('按模型档位表出档：默认项恒在 + 三档，无当前档时选默认', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, 'prov/GLM-5.3', LEVELS);
  assert.equal(sel.disabled, false);
  assert.deepEqual(sel.options.map((o) => o.value), ['', 'low', 'high', 'max']);
  assert.equal(sel.value, '');
  assert.ok(sel.innerHTML.includes('低 low') && sel.innerHTML.includes('最大 max'), '中文标签在');
});

test('当前档在列表内：预选存活', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, 'prov/GLM-5.3', LEVELS, 'high');
  assert.equal(sel.value, 'high');
});

test('当前档在列表外（自由文本档案值）：追加「当前」自选项', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, 'prov/GLM-5.3', LEVELS, 'medium');
  assert.equal(sel.value, 'medium');
  assert.ok(sel.innerHTML.includes('medium（当前）'), '防谎报的自选项在');
});

test('模型留空＋默认模型已知（回默认链）：按默认模型的档位表出档——ZCode 同口径，不必先换模型', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, '', LEVELS, 'low', 'GLM-5.3');
  assert.equal(sel.disabled, false);
  assert.deepEqual(sel.options.map((o) => o.value), ['', 'low', 'high', 'max']);
  assert.equal(sel.value, 'low', '默认链上的当前档预选');
});

test('模型留空＋默认模型未知（清单没拉到）：才禁用', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, '', LEVELS, 'low', '');
  assert.equal(sel.disabled, true);
  assert.deepEqual(sel.options.map((o) => o.value), ['']);
});

test('模型留空＋默认模型已知＋当前档在表外：追加「当前」自选项（强度真的随默认链生效了）', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, '', LEVELS, 'medium', 'GLM-5.3');
  assert.equal(sel.value, 'medium');
  assert.ok(sel.innerHTML.includes('medium（当前）'), '防谎报的自选项在');
});

test('档位表没提的模型：不瞎猜，只给默认项', () => {
  const sel = fakeSelect();
  fillReasoningOptions(sel, 'prov/kimi-k3', LEVELS);
  assert.equal(sel.disabled, false);
  assert.deepEqual(sel.options.map((o) => o.value), ['']);
  const gone = fakeSelect();
  fillReasoningOptions(gone, 'prov/GLM-5.3', undefined);
  assert.deepEqual(gone.options.map((o) => o.value), [''], '清单拉取失败同款降级');
});

test('levelLabel：已知 token 中文映射、未知原样', () => {
  assert.equal(levelLabel('low'), '低 low');
  assert.equal(levelLabel('max'), '最大 max');
  assert.equal(levelLabel('turbo'), 'turbo');
});

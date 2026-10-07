// modellevels.js — 思考强度（ZCode 的 reasoningLevel）在人员面板的
// 展示层小件，出生表单（recruit.js）与在座改档（detail.js）共用；
// 顺带承载模型面的分组标签（providerGroupLabel：徽记＋不可用原因）。
// 档位表来自 GET /dispatch/models 的 levels 侧表（后端解析 CLI 内置
// zcode-builtin.json 的 modelRules——与桌面拾取器同源同表），所以这里
// 只做展示映射：已知 token 配中文标签，未知 token 原样显示（自定义
// 中继的模型可能带生僻档位名）。

import { esc } from '../ui/dom.js';
import { t, isEn } from '../ui/i18n.js';

// 已知档位的中文标签（词表与内置 modelRules 的 values 并集）。「关闭」
// 走 isEn 分支：词典里 '关闭' 是按钮语义（Close），档位此处应为 Off，
// 单键双义不合典，en 专属口径就地分叉（usageview 同款先例）。
// 标签惰性求值：import 期定语言会把它冻在载入时刻（测试世界里桩立得
// 比 import 晚就再也翻不回），levelLabel 每次现查。
const LEVEL_LABELS = () => ({
  none: t('无'),
  disabled: isEn() ? 'Off' : '关闭',
  enabled: t('开启'),
  low: t('低'),
  medium: t('中'),
  high: t('高'),
  xhigh: t('超高'),
  max: t('最大'),
});

// 不可用账号面的原因标签（后端只发稳定 token，措辞归展示层）。
// 口径与桌面注册表一致：面保留、置灰、括注原因，从不悄悄消失。
const REASON_LABELS = {
  'not-connected': '未连接',
  'connection-unselected': '未选择该连接',
  'no-entitled-models': '无已授权模型',
};

/**
 * 一个模型面（/dispatch/models 的 provider 行）的分组标签：显示名 +
 * 计划徽记（· 团队/免费），不可用面追加原因括注（（未连接）等）——
 * 出生表单与在座改档的 optgroup 共用，保证两处下拉永远同词。
 * @param {{id?: string, name?: string, badge?: string, available?: boolean, reason?: string}} p
 */
export function providerGroupLabel(p) {
  const base = (p.name || p.id) + (p.badge ? ` · ${p.badge}` : '');
  const reason = p.available === false && p.reason && REASON_LABELS[p.reason];
  return reason ? `${base}（${t(REASON_LABELS[p.reason])}）` : base;
}

/** 一个档位 token 的展示标签（「低 low」；未知 token 原样）。 */
export function levelLabel(lv) {
  const L = LEVEL_LABELS();
  return L[lv] ? `${L[lv]} ${lv}` : lv;
}

// modelIdOf breaks a "providerId/modelId" pick down to its bare model
// id — the levels table's key（与后端 ParseModelRef 同切法：首个「/」
// 之后全归模型）；裸 id 原样过。
function modelIdOf(ref) {
  const i = ref.indexOf('/');
  return i >= 0 ? ref.slice(i + 1) : ref;
}

/**
 * 重建一个思考强度 <select>（[data-reasoning] 的唯一入口）：
 * - 首项恒为「默认（模型缺省档）」（空值＝不带 reasoningLevel，模型
 *   自己的缺省思考档）；
 * - 档位挂在具体模型上（与 ZCode 同口径——默认模型也是真模型，档照
 *   挑）：选中模型用它的档位表；模型留空（回默认链）用 def（默认链
 *   的模型 id）的表；表没提（或清单拉取失败）→ 只剩默认项，不瞎猜；
 * - cur 是当前生效档：在列表内则预选；不在（自由文本/历史档案带来
 *   的生僻值）→ 追加「（当前）」自选项——下拉永远不说谎；
 * - 连 def 都未知（模型清单没拉到）才禁用——有默认模型在，强度永远
 *   可选，不必先换模型。
 * @param {HTMLSelectElement} sel 目标 select
 * @param {string} ref 当前模型选择（"providerId/modelId" 或空）
 * @param {Record<string, string[]>|null|undefined} levels levels 侧表
 * @param {string} [cur] 当前生效的思考档
 * @param {string} [def] 默认链的模型 id（模型留空时的档位表键）
 */
export function fillReasoningOptions(sel, ref, levels, cur = '', def = '') {
  if (!sel) return;
  const model = ref ? modelIdOf(ref) : def;
  const list = model ? levels?.[model] || null : null;
  let html = `<option value="">${t('默认（模型缺省档）')}</option>`;
  const values = [''];
  if (list?.length) {
    for (const lv of list) {
      if (values.includes(lv)) continue;
      values.push(lv);
      html += `<option value="${esc(lv)}">${esc(levelLabel(lv))}</option>`;
    }
  }
  sel.disabled = !model;
  sel.innerHTML = html;
  // 列表外的当前档只在有模型挂着时自荐（连模型面都没有时强度无从
  // 生效——追加「当前」只会误导）；防下拉谎报现状的自选项。
  if (model && cur && !values.includes(cur)) {
    sel.insertAdjacentHTML(
      'beforeend', `<option value="${esc(cur)}">${esc(levelLabel(cur))}${t('（当前）')}</option>`);
    values.push(cur);
  }
  sel.value = values.includes(cur) ? cur : '';
}

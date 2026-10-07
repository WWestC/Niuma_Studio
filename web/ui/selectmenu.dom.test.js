// selectmenu.dom.test.js — select 弹层接管（Select 篇补章）的数据面
// 冒烟：itemsOf 把 options 平铺成菜单项（optgroup 首项前插分组头、
// 选中随 selectedIndex、禁用随行）、rowsHTML 注入面 esc（<b>/<img>
// 不穿透）＋状态类（is-sel/is-dis）落点、fitMenu 落位几何三档（贴锚
// /全高面板/占满宽裕侧）。交互面（开合/键盘/提交）要真 DOM 事件路由，
// 测试基座（testdom 零事件路由）够不到，不在此冒。

import test from 'node:test';
import assert from 'node:assert/strict';
import { dom } from '../room/testdom.js';

test('selectmenu：itemsOf——optgroup 分组头插在组首项前、选中/禁用随行', async () => {
  dom();
  const { itemsOf } = await import('./selectmenu.js');
  const zai = { tagName: 'OPTGROUP', label: '在编' };
  const sel = {
    selectedIndex: 2,
    options: [
      { label: '默认（跟随调度配置）', value: '', index: 0 },
      { label: 'kimi-k2', value: 'k2', index: 1, parentNode: zai },
      { label: 'glm-5', value: 'glm', index: 2, parentNode: zai },
      { label: '老王（已离职）', value: 'wang', index: 3, disabled: true },
    ],
  };
  const items = itemsOf(sel);
  assert.equal(items.length, 5, '三选项＋一组头');
  assert.equal(items[0].type, 'opt', '无组选项不插头');
  assert.equal(items[0].selected, false, 'selectedIndex 之前不误标');
  assert.equal(items[1].type, 'group', '组首项前插分组头');
  assert.equal(items[1].label, '在编');
  assert.equal(items[3].selected, true, 'selectedIndex 落在组内第二项');
  assert.equal(items[2].selected, false, '组内首项不误标选中');
  assert.equal(items[4].disabled, true, '禁用随行');
  // 无 optgroup 的普通 select：零组头
  const flat = itemsOf({ selectedIndex: 0, options: [{ label: 'a', value: 'a', index: 0 }] });
  assert.equal(flat.length, 1);
});

test('selectmenu：rowsHTML——esc 注入面＋状态类＋✓ 列占位', async () => {
  dom();
  const { rowsHTML } = await import('./selectmenu.js');
  const html = rowsHTML([
    { type: 'group', label: '<b>在编' },
    { type: 'opt', label: '<img src=x onerror=1>', value: 'v', index: 0, selected: true, disabled: false },
    { type: 'opt', label: '老王（已离职）', value: 'w', index: 1, selected: false, disabled: true },
  ]);
  assert.ok(!html.includes('<b>'), '分组头裸 HTML 不穿透');
  assert.ok(html.includes('&lt;b&gt;'), '分组头已 esc');
  assert.ok(!html.includes('<img'), '选项裸 HTML 不穿透');
  assert.ok(html.includes('&lt;img'), '选项已 esc');
  assert.ok(html.includes('sel-grp') && html.includes('sel-opt'), '结构类在');
  const selRow = html.split('\n').find((l) => l.includes('is-sel'));
  assert.ok(selRow.includes('is-sel') && selRow.includes('sel-chk'), '选中行带 ✓ 列');
  assert.ok(html.includes('is-dis'), '禁用类在');
  assert.ok(html.includes('aria-selected="true"') && html.includes('aria-disabled="true"'), 'aria 随行');
});

// fitMenu 落位三档（M=8、GAP=4；行高 32 是 CSS 口径，这里只谈几何）
test('selectmenu：fitMenu——短清单贴锚、长清单升全高面板、夹持不越视口', async () => {
  dom();
  const { fitMenu } = await import('./selectmenu.js');

  // ① 短清单（mh=100）锚在视口中部（720 高）：下方放得下 → 贴锚下方留缝
  let p = fitMenu(100, 360, 392, 720);
  assert.equal(p.top, 396, '贴锚下方留缝（GAP=4）');
  assert.equal(p.maxH, 100, '放得下就全高，不裁');

  // 短清单锚在底部：下方只剩 20 → 翻上方贴锚
  p = fitMenu(100, 640, 672, 720);
  assert.equal(p.top, 640 - 4 - 100, '翻上方贴锚底');
  assert.equal(p.maxH, 100);

  // ② 长清单（mh=943，模型档 22 项＋7 组头的真实量级）锚在视口中部：
  // 宽裕侧（上方 348）放不下 → 全高面板（M..vh−M，盖过锚点换行数）
  p = fitMenu(943, 360, 392, 720);
  assert.equal(p.top, 8, '全高面板从视口边距起');
  assert.equal(p.maxH, 720 - 16, '全高＝视口减双边距');
  // 锚在底部（管理操作是侧栏末节，常态位）：同样升全高
  p = fitMenu(943, 640, 672, 720);
  assert.equal(p.maxH, 720 - 16, '底部锚同样全高——不再只露一两行');

  // ③ 宽裕侧几乎已是全高（差 ≤64 不值得盖锚）：占满宽裕侧
  // 锚在视口顶（top=8）：上方只有 0，下方 700≈全高 704−64 → 占满下方
  p = fitMenu(943, 8, 40, 720);
  assert.equal(p.top, 44, '贴锚下方');
  assert.equal(p.maxH, 720 - 40 - 4 - 8, '占满下方到视口底边距');

  // 矮视口夹持：96 兜底不许把弹层推出视口
  p = fitMenu(943, 100, 132, 160);   // 160 高的视口：上 88、下 16
  // 两侧都放不下；full(144) ≤ avail(88)+64 → 占满宽裕侧（上方），96
  // 兜底超出上方空间 → 越界回夹到视口内（top 贴上边距）
  assert.equal(p.maxH, 96, '96 兜底');
  assert.equal(p.top, 8, '回夹进视口（贴上边距），不越视口');
});

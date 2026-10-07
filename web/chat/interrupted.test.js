// interrupted.test.js — 中断接续卡的「一键全部继续」钉行为面：告别快照
// 抓到多人干活被中断时，房主不该挨个点五次「让 TA 继续」。按
// guide.test.js 的接线扫描先例钉契约——stream.js 无 DOM 测试基建
// （构造即挂 ResizeObserver/window），模板与点击分支只能源扫描：
//   ① 卡模板：席位 ≥2 才挂「全部继续」（单人卡本就一键，不摆冗余钮）；
//   ② 点击分支：全部钮只续卡内未点过的席位（:not([disabled])——已点过
//      的不重复喊人），自己也翻待回话态防连点；
//   ③ 共通落点：单点与全部继续走同一道 #continueSeat——点名文案、
//      去向房间的兜底一字不差，两条路不容许分叉。
// 任一处被删，这里必红（全部钮消失、或点了不发货、或单点/全部两条
// 路喊法漂移）。

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('./stream.js', import.meta.url), 'utf8');

test('卡模板：席位 ≥2 才挂「全部继续」，单人卡不摆冗余钮', () => {
  const guard = src.match(/const all = list\.length > 1\s*\?[^:]*:\s*'';/s);
  assert.ok(guard, '全部继续钮须由 list.length > 1 把门——单人卡不出钮');
  assert.match(guard[0], /data-continue-all/, '守卫的正是全部继续钮');
  assert.match(guard[0], /全部继续 →/, '钮面文字在');
  // 席位钮与全部钮同卡同款（gold），名单行仍逐席带 data-continue
  assert.match(src, /data-continue="\$\{esc\(s\.name\)\}" data-continue-room="\$\{esc\(s\.project \|\| LobbyKey\)\}"/,
    '席位钮照旧带名字与去向房间');
  // 卡身文案钉自动接续口径：服务端重启已自动点名，卡是兜底手动通道
  assert.match(src, /已自动点名接续；若有人迟迟没接上，可再点名催一次/,
    '卡身必须说清「已自动接续，按钮是兜底」——否则房主以为还得挨个点');
});

test('点击分支：全部钮只续卡内未点过的席位，自己也翻待回话态', () => {
  const branch = src.match(/const contAll = t\.closest\('\[data-continue-all\]'\);[\s\S]*?\n    \}/);
  assert.ok(branch, '须有 [data-continue-all] 的点击分支');
  assert.match(branch[0], /closest\('\.iw-card'\)/, '范围圈死本卡——隔壁卡不连坐');
  assert.match(branch[0], /button\[data-continue\]:not\(\[disabled\]\)/, '只挑未点过的席位，已点过的不重复喊');
  assert.match(branch[0], /#continueSeat\(btn\)/, '逐席位走共通落点');
  assert.match(branch[0], /contAll\.disabled = true/, '全部钮翻完成态防连点');
  assert.match(branch[0], /已全部点名/, '完成态文案在');
});

test('共通落点：单点与全部继续同一道 #continueSeat，喊法不漂移', () => {
  const fn = src.match(/#continueSeat\(btn\) \{[\s\S]*?\n  \}/);
  assert.ok(fn, '#continueSeat 须存在');
  assert.match(fn[0], /btn\.disabled = true/, '点过的钮翻待回话态');
  assert.match(fn[0], /已点名，等 TA 回话…/, '席位钮待回话文案在');
  assert.match(fn[0], /sendTo\(btn\.dataset\.continueRoom \|\| LobbyKey/,
    '点名发往席位自己的去向房间，缺省 Niuma_Studio');
  assert.match(fn[0], /请继续刚才中断的工作/, '点名文案与单点时代一字不差');
  // 两条点击路都必须走它——不允许谁自带另一套发送
  const single = src.match(/const cont = t\.closest\('\[data-continue\]'\);[\s\S]*?\n    \}/);
  assert.ok(single, '单席位点击分支在');
  assert.match(single[0], /this\.#continueSeat\(cont\)/, '单点走共通落点');
  const calls = (src.match(/#continueSeat\(/g) || []).length;
  assert.equal(calls, 3, '一定义两调用，多一处就是旁路');
});

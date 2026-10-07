// boot.test.js — 启动门 warming 文案的静态断言（xs.dom.test.js 同款
// 降级口径：断言关键规则存在而非渲染实测）。预热等待必须带服务端的
// 已等待秒数（Detail），旧构建没有 Detail 时退回静态文案——等待在走
// 而不是卡死，看得见。真机走查留验收段。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('./boot.js', import.meta.url), 'utf8');

test('warming 分支渲染服务端 Detail（已等待秒数），并兜底静态文案', () => {
  assert.ok(
    src.includes("say(st.detail || t('正在预热模型通道'))"),
    'warming 分支应优先渲染 st.detail，缺失时退回静态文案',
  );
  // 不得退回成无兜底的纯 Detail 渲染（旧服务端字段缺席会显示空行）
  assert.ok(!src.includes('say(st.detail);'), '不允许无兜底的纯 st.detail 渲染');
});

test('booting 分支渲染服务端 Detail（全盘搜索文案），并兜底静态文案', () => {
  assert.ok(
    src.includes("say(st.detail || t('正在识别 ZCode'))"),
    'booting 分支应优先渲染 st.detail（如「正在全盘搜索 ZCode…」），缺失时退回静态文案',
  );
});

// plugins/loader.js — 形态 B 插件运行时（v3 preview）：拉取宿主插件索引，
// 逐个动态 import 入口模块（/plugins/<id>/<entry>，资产面直读磁盘——
// 插件载荷不进 go:embed 的发布纪律），入口约定 VSCode 同款：
//
//   export function activate(niuma) { … }
//
// niuma.* 是宿主给插件的全部面孔，按 manifest 声明的 permissions 裁剪
// （没声明的域调用即抛错）；插件不得触碰调度核心（dispatch/autopilot/
// fleet）——这张 API 表里根本没有那些门。帧订阅（chat.watch）经
// pluginBus：app.js 的 RoomBook 回调把帧送进来，这里分发给订阅者，
// 插件回调的异常只落 console，绝不反噬房间流。
//
// 装载失败的纪律：单个插件/单个板块失败只 toast + console，其余插件
// 照常装载；/plugins.json 整个不可达视为「没有插件」（开发早期与纯净
// 安装的正常形态），完全静默。

import { getPluginIndex } from '../wire/plugins.js';
import { getProjects, getProjectTasks, getTasks } from '../wire/api.js';
import { Msg } from '../wire/wire.js';
import { t } from '../ui/i18n.js';

// pluginBus — app.js 送帧、loader 发糖的唯一管道。
const chatSubs = new Set();
const eventSubs = new Set();
export const pluginBus = {
  // 一条活的对话帧（say/report 等经 onLiveFrame 的同源帧）＋所属房间
  // key。backfill＝载史/断线补窗的重放帧，不分发（t_197）：订阅面吃的
  // 是直播动静，重放是历史不是新消息——计数类订阅（工作室脉搏）不因
  // 重连/刷新虚胖。
  emitChat(f, key, backfill) {
    if (backfill) return;
    for (const cb of chatSubs) {
      try { cb(f, key); } catch (err) { console.error('[plugin] chat.watch 回调抛错', err); }
    }
  },
  onChat(cb) {
    chatSubs.add(cb);
    return () => chatSubs.delete(cb); // 退订
  },
  // 一条台账/名册提醒帧（RoomBook onEvent 的同源帧：task/agent/plan/kb 变更）
  emitEvent(f) {
    for (const cb of eventSubs) {
      try { cb(f); } catch (err) { console.error('[plugin] events.watch 回调抛错', err); }
    }
  },
  onEvent(cb) {
    eventSubs.add(cb);
    return () => eventSubs.delete(cb);
  },
};

// pluginThemes — 装载时从索引收集的插件主题清单（形态 A 的主题通道）：
// [{key:'<pluginId>:<themeId>', name, vars, plugin}]。settings.js 的外观
// 面板拿它渲染「插件主题」分段器，app.js 装载完喂给 setPluginThemes。
export const pluginThemes = [];

// bootPlugins 装载全部已启用插件：先收集主题（纯数据通道，无入口模块
// 的主题插件也要进清单），再逐板块动态 import 激活。ctx 由 app.js 提供：
//   registerBoard(plugin, boardMeta, view) — 板块注册面（app.js 建锚点/容器）
//   registerWidget(plugin, view) — HUD 挂件注册面（房间画板浮层位）
//   book — RoomBook（只读快照：当前房）
//   toast(text, kind) — 全局提示
// 永不 reject：装载失败是插件的事，不是工作室的事。
export async function bootPlugins(ctx) {
  let index;
  try {
    index = await getPluginIndex();
  } catch {
    return; // 没有插件面：静默（见文件头）
  }
  pluginThemes.length = 0;
  for (const p of index.plugins || []) {
    if (!p.enabled || !p.manifest) continue;
    for (const t of p.manifest.contributes?.themes || []) {
      pluginThemes.push({
        key: `${p.id}:${t.id}`,
        name: t.name || t.id,
        vars: t.vars || {},
        plugin: { id: p.id, name: p.manifest.name || p.id },
      });
    }
  }
  for (const p of index.plugins || []) {
    if (!p.enabled || !p.manifest) continue;
    const boards = p.manifest.contributes?.boards || [];
    if (!boards.length) continue;
    for (const b of boards) {
      const niuma = makeApi(p, b, ctx);
      try {
        const mod = await import(`/plugins/${p.id}/${b.entry}`);
        if (typeof mod.activate !== 'function') {
          throw new Error(t('入口 {entry} 未导出 activate(niuma)', { entry: b.entry }));
        }
        mod.activate(niuma);
      } catch (err) {
        console.error(`[plugin ${p.id}]`, err);
        ctx.toast?.(t('插件「{name}」装载失败：{err}', { name: p.manifest.name || p.id, err: err?.message || err }), 'err');
      }
    }
  }
}

// makeApi 为「一个插件的一个板块」裁出它的 niuma.*——权限门是装饰器：
// 未声明即抛错，错误文案告诉作者去 plugin.json 的 permissions 里补哪项。
function makeApi(p, boardMeta, ctx) {
  const perms = new Set(p.manifest.permissions || []);
  const need = (perm) => {
    if (!perms.has(perm)) {
      throw new Error(t('插件 {id} 未声明权限 "{perm}"（在 plugin.json 的 permissions 里补上）', { id: p.id, perm }));
    }
  };
  return {
    // 宿主插件 API 版本（与 /plugins.json 的 api 同源，server/plugins.go）
    version: '1.0',
    // 本插件的自述（id/名字/版本）——一个插件可能注册多个板块，共享同一张脸
    plugin: { id: p.id, name: p.manifest.name, version: p.manifest.version },
    // 帧类型常量表（wire.js 的 Msg 原样共享——判断 f.type 用）
    wire: Msg,
    // 注册工作台板块：view = { id?, title?, icon?, mount(el), reveal?(), conceal?() }
    // id/title/icon 缺省用 manifest 里声明的板块元数据（boardMeta）。
    registerBoard(view = {}) {
      need('boards');
      return ctx.registerBoard(p, boardMeta, view);
    },
    // 注册 HUD 挂件：view = { spot:'room-hud', mount(el) }——挂在房间画板
    // 左上角的浮层位（随办公室板块显隐）。挂件是表现层，与板块同一张
    // 权限表。
    registerWidget(view = {}) {
      need('widgets');
      if (!ctx.registerWidget) throw new Error(t('宿主不支持挂件位'));
      return ctx.registerWidget(p, view);
    },
    chat: {
      // 订阅活的对话帧（含所属房间 key）；返回退订函数。只读——发言行
      // 为不属于插件面（那是房主与成员的对话，不是插件的）。
      watch(cb) {
        need('chat.read');
        return pluginBus.onChat(cb);
      },
    },
    events: {
      // 订阅台账/名册提醒帧（task/agent/plan/kb 变更广播；同样只读）
      watch(cb) {
        need('chat.read');
        return pluginBus.onEvent(cb);
      },
    },
    tasks: {
      // 任务台账读取：projectKey 给定读该房任务，缺省读全量
      async list(projectKey) {
        need('tasks.read');
        return projectKey ? getProjectTasks(projectKey) : getTasks();
      },
    },
    projects: {
      async list() {
        need('projects.read');
        return getProjects();
      },
    },
    rooms: {
      // 当前正看的房间（快照，不订阅）——给「跟随房主视线」类板块用
      current() {
        const room = ctx.book.currentRoom?.();
        return { key: ctx.book.current, name: room?.name || '' };
      },
    },
    ui: {
      toast(text, kind = 'ok') {
        need('ui.toast');
        ctx.toast?.(text, kind);
      },
    },
    market: {
      // 市场管理面（一键安装/启停/卸载）——supply-chain 级权力，独立
      // 权限 market.admin，官方市场板块专用；启停卸载即时生效（服务
      // 端当场重放衣橱贡献）。
      async index(sourceUrl) {
        need('market.admin');
        const r = await fetch(`/market/index?url=${encodeURIComponent(sourceUrl)}`);
        if (!r.ok) {
          const data = await r.json().catch(() => ({}));
          throw new Error(data.error || `HTTP ${r.status}`);
        }
        return r.json();
      },
      async install(body) {
        need('market.admin');
        const r = await fetch('/market/install', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        });
        const data = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
        return data;
      },
      async setEnabled(id, on) {
        need('market.admin');
        const r = await fetch('/market/enable', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id, on }),
        });
        const data = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
        return data;
      },
      async remove(id) {
        need('market.admin');
        const r = await fetch('/market/remove', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id }),
        });
        const data = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
        return data;
      },
    },
  };
}

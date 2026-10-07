// dayreplay.js — r_32 分享页 boot：/dayreplay/{key}?t=&date= 的轻量入
// 口（index.html 的入口选择器分流进来）。同壳不同命：主应用的 boot
// （ZCode 门/房间流/各板块/插件）一概不起，只做四件事——
//   ① 判别+解析 {key, date, token}；
//   ② 裁剪：只留办公室一块屏（侧栏/顶栏/其他板块全藏——这是当日回
//      放的分享页，不是访客通道，没有实时面）；
//   ③ 起 RoomView（stub book——无实时名册；影子演员凭帧内外观键
//      c/hair 自取 atlas，sprite 不缺）＋拉当日帧进 dayBuf 开演；
//   ④ 播放器 UI 原生（选日/播放暂停/倍速；导出钮与录制腿在
//      roomview 里按 __NIUMA_DAYREPLAY__ 标记摘除）。
// index.html 与主应用共用（handleDayReplayPage 回同一份壳），这里的
// 分支纪律照抄 visitor.js：CSS 一层裁剪 + 服务端 token 门双保险。

import { RoomView } from '../room/roomview.js';
import { dismissBoot } from './boot.js';
import { Toast } from './toast.js';
import { t } from './i18n.js';

// 分享态判别（roomview 的录制腿/导出面读这个标记）
export function dayReplayMode() {
  return !!globalThis.__NIUMA_DAYREPLAY__;
}

// initDayReplay：/dayreplay/{key} 进入时设标记＋裁剪 UI＋顶条。返回
// true = 本页是分享页（入口选择器据此不走主应用）。
export function initDayReplay() {
  if (!location.pathname.startsWith('/dayreplay/')) return false;
  const key = decodeURIComponent(location.pathname.split('/')[2] || '');
  const q = new URLSearchParams(location.search);
  globalThis.__NIUMA_DAYREPLAY__ = {
    key,
    token: q.get('t') || '',
    date: (q.get('date') || '').trim(),
  };
  // 裁剪（§四清单同款纪律）：分享页只有办公室一块屏
  const style = document.createElement('style');
  style.textContent = `
    #side, #titlebar-tools, #drawer-btn, #visitor-bar,
    #composer, #owner-card, #owner-miss { display: none !important; }
    .board:not(#board-room) { display: none !important; }
  `;
  document.head.appendChild(style);
  // 分享顶条：明示这是哪一天的回放
  const bar = document.createElement('div');
  bar.id = 'dayreplay-bar';
  bar.textContent = `⟲ ${globalThis.__NIUMA_DAYREPLAY__.date} · ${t('公司一日回放 · 只读分享')}`;
  document.body.appendChild(bar);
  return true;
}

// bootDayReplay：门后只有办公室——拉帧、开演、落门。
export async function bootDayReplay() {
  const { key, date } = globalThis.__NIUMA_DAYREPLAY__;
  const toast = new Toast();
  const room = document.getElementById('board-room');
  if (room) room.hidden = false;
  const canvas = document.getElementById('room-canvas');
  if (!canvas || !key || !date) {
    await dismissBoot();
    return;
  }
  // stub book：无实时流（rooms 空表——syncRoster 空转，办公室的「实时」
  // 是一层静止的家具，画面主体是日缓冲的影子演员）
  const book = { rooms: new Map(), current: key, frameTo: () => false };
  const rv = new RoomView(canvas, book, {
    toast: (text, kind) => toast.show(text, kind),
    openPerson: () => {},
    openBoard: () => {},
  });
  rv.roomKey = key;
  globalThis.roomView = rv; // 调试/测试句柄（主应用同款口径）
  await rv.selectReplayDay(date);
  if (rv.replaySrc === 'day') rv.revealed();
  await dismissBoot();
}

// dev 热重载（NIUMA_DEV=1 才有这个端点；生产 404 即刻关闭）。EventSource
// 有无守卫——node --test 的裸环境导入本模块只测分支，不建 SSE。
if (typeof EventSource === 'function') {
  const devReload = new EventSource('/app/__reload');
  devReload.onmessage = () => { devReload.close(); location.reload(); };
  devReload.onerror = () => devReload.close();
}

// main：入口选择器（index.html）调——分享页路径才起 boot，主应用路径
// 本模块零副作用。
export function main() {
  return initDayReplay() ? bootDayReplay() : Promise.resolve();
}

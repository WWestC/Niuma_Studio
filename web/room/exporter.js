// exporter.js — the 公司一日回放's video leg (r_32): the day buffer
// rendered onto an offscreen canvas and recorded frame-by-frame into a
// downloadable WebM. 设计三件事：
//
//   编码 — canvas.captureStream(0) + MediaRecorder 逐帧 requestFrame：
//          帧的时刻跟着 requestFrame 的真实时刻走，导出一段 60s 片
//          就花 60s（进度条照实走）。mimeType 探测 vp9→vp8→webm→mp4，
//          全灭（老 WebKitGTK）时明确报「此环境不支持」——回放/播放
//          器不受影响，视频只是产物之一。
//   渲染 — roomView.draw(target) 参数化腿：离屏画布吃同一 draw 链
//          （影子演员/名牌/气泡全套），去回放灰层，烧入导出 HUD
//          （右下日期时间码、左上工作室名）。
//   跳空白 — segmentsOf 把一天剪成段：帧间断裂 >30s 或全帧无人
//          >10s 的空档硬切掉（app 关着的深夜不该进视频）。
//
// 纯函数（segmentsOf/pickMime/nextT）与浏览器腿（exportDayReplay）
// 同文件分居——node --test 直接测前者。

import { SCENE_W, SCENE_H } from './art.js';

export const GAP_MS = 30_000;   // 相邻帧断裂阈值：跨过即硬切
export const EMPTY_MS = 10_000; // 全帧无人的空场阈值：超时即剪
export const EXPORT_FPS = 12;   // 采样帧率（像素风 12fps 足够顺）

// segmentsOf folds a day's frames into playback segments: [{s, e}] in
// wall-clock ms, cut at stream breaks (>gapMs) and dead air (no actors
// for >emptyMs — the segment's tail trims back to where it emptied).
export function segmentsOf(frames, opts = {}) {
  const { gapMs = GAP_MS, emptyMs = EMPTY_MS } = opts;
  const segs = [];
  let start = null, last = null, emptySince = null;
  const push = (e) => { if (start !== null && e > start) segs.push({ s: start, e }); };
  for (const f of frames) {
    if (!f || typeof f.t !== 'number') continue;
    const empty = !f.a || f.a.length === 0;
    if (start === null) {
      start = f.t; last = f.t; emptySince = empty ? f.t : null;
      continue;
    }
    if (f.t - last > gapMs) { // 断档（app 关过/换过房）：前段收，新段起
      push(last);
      start = f.t; emptySince = empty ? f.t : null; last = f.t;
      continue;
    }
    if (!empty) {
      if (emptySince !== null && f.t - emptySince > emptyMs) {
        push(emptySince); // 空场剪掉：前段收到空场起点，新段从这帧起
        start = f.t;
      }
      emptySince = null;
    } else if (emptySince === null) {
      emptySince = f.t;
    }
    last = f.t;
  }
  push(last);
  return segs;
}

// nextT advances the playhead across segment gaps: within a segment it
// is the raw advance; crossing a gap snaps to the next segment's start
// (the hard cut). Returns null past the end.
export function nextT(segs, T, advance) {
  let t = T + advance;
  for (const g of segs) {
    if (t < g.s) return g.s;               // gap 前的空档：直接落到下一段
    if (t <= g.e) return t;                // 段内：正常前进
  }
  return segs.length ? null : t;           // 过尾：null（调用方收工）
}

// EXPORT_MIMES is the probe order: vp9 (crisper per byte) → vp8 → plain
// webm → mp4 (WebView2's h264 — a .mp4 plays everywhere the webm won't).
export const EXPORT_MIMES = [
  'video/webm;codecs=vp9',
  'video/webm;codecs=vp8',
  'video/webm',
  'video/mp4',
];

// pickMime answers the first supported recording type (null = no
// recorder in this environment — caller reports, nothing else breaks).
export function pickMime() {
  if (typeof MediaRecorder === 'undefined') return null;
  for (const m of EXPORT_MIMES) {
    try { if (MediaRecorder.isTypeSupported(m)) return m; } catch { /* keep probing */ }
  }
  return null;
}

// exportDayReplay renders [t0, t1] of the loaded day buffer at
// (t1-t0)/targetSec speed onto a scale× offscreen canvas and returns
// the recorded Blob. onProgress(frac 0..1) rides every painted frame;
// opts.cancelled() true stops early (the partial recording still
// resolves — 半截视频好过没有).
export async function exportDayReplay(opts) {
  const {
    rv, t0, t1, targetSec = 60, scale = 2,
    onProgress = null, cancelled = null, hudLabel = '',
  } = opts;
  const mime = pickMime();
  if (!mime) throw new Error('NORECORDER');
  if (!rv || !rv.dayBuf || rv.dayBuf.frames.length < 2) throw new Error('NODAY');
  const segs = segmentsOf(rv.dayBuf.frames).filter((g) => g.e >= t0 && g.s <= t1);
  if (!segs.length) throw new Error('EMPTY');

  const W = SCENE_W * scale, H = SCENE_H * scale;
  const canvas = document.createElement('canvas');
  canvas.width = W; canvas.height = H;
  const ctx = canvas.getContext('2d');
  const stream = canvas.captureStream(0);
  const track = stream.getVideoTracks()[0];
  const rec = new MediaRecorder(stream, { mimeType: mime, videoBitsPerSecond: 8_000_000 });
  const chunks = [];
  rec.ondataavailable = (e) => { if (e.data && e.data.size) chunks.push(e.data); };

  // the playhead state we borrow (restored on the way out — the office
  // must find its replay where the viewer left it)
  const wasT = rv.replayT, wasPlaying = rv.replayPlaying;
  rv.setReplayPlaying(false);

  const speed = (t1 - t0) / (targetSec * 1000); // day-ms per real second
  const frameEvery = 1000 / EXPORT_FPS;
  let T = nextT(segs, t0, 0) ?? t0;
  let lastPaint = 0, done = false;

  const paint = () => {
    rv.replayT = T;
    rv.draw({ ctx, W, H, scale, hud: 'export', hudLabel });
    track.requestFrame();
    if (onProgress) onProgress(Math.min(1, (T - t0) / (t1 - t0)));
  };

  return await new Promise((resolve, reject) => {
    rec.onerror = () => { done = true; reject(new Error('RECERROR')); };
    rec.onstop = () => resolve(new Blob(chunks, { type: mime }));
    const tick = (now) => {
      if (done) return;
      if (now - lastPaint >= frameEvery) {
        lastPaint = now;
        const nt = nextT(segs, T, frameEvery * speed);
        if (nt === null || nt >= t1) {
          paint(); T = t1;
          done = true;
          rec.stop();
          track.stop();
          rv.replayT = wasT; rv.replayPlaying = wasPlaying;
          return;
        }
        T = nt;
        paint();
      }
      if (cancelled && cancelled()) {
        done = true;
        rec.stop();
        track.stop();
        rv.replayT = wasT; rv.replayPlaying = wasPlaying;
        return;
      }
      requestAnimationFrame(tick);
    };
    paint(); // 首帧先落定，再开录
    rec.start();
    lastPaint = performance.now();
    requestAnimationFrame(tick);
  });
}

// downloadBlob hands the recorded file to the OS (the term 下载整册
// precedent: an <a download> click is the whole ceremony).
export function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

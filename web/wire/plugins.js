// wire/plugins.js — /plugins.json 的取数面（v3 形态 B）：宿主插件索引，
// 一次拉全（装了谁、谁启用、各贡献什么板块）。404/网络失败由调用方
// （plugins/loader.js）静默降级——没有插件面的工作室是正常形态。

export async function getPluginIndex() {
  const r = await fetch('/plugins.json', { headers: { Accept: 'application/json' } });
  if (!r.ok) throw new Error(`/plugins.json ${r.status}`);
  return r.json();
}

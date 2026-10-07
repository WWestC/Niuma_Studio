// sfxport.js — the room side's sound port (r_09, t_148): room modules
// never import web/ui/sound.js directly — that chain pulls settings.js →
// feedback.js → document, which breaks the zero-DOM test discipline
// (r_08). Instead the app composition root injects the real sfx here at
// boot; before that (or in tests) the port is a silent no-op — sounds are
// garnish, their absence is never an error.

let impl = null;

/** The app root calls this once with the real sfx (web/ui/sound.js). */
export function bindSfx(fn) { impl = fn; }

/** Play one sound by kind ('scene-print' etc). Silent no-op until bound. */
export function sfx(kind, opts) {
  try { impl?.(kind, opts); } catch { /* 静默让行 */ }
}

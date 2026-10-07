// bus.js — the room-store fan-out bus. RoomBook speaks ONE change
// channel (key, hint); before this bus existed app.js hand-wired every
// board × hint line in one growing switch — each new board or hint
// touched the whole chain. Boards now SUBSCRIBE to the hints they
// consume (registration lives where the consumer is constructed); the
// bus preserves emission order (subscription order), and a null hint
// list means "everything" (the always-on views: switcher/stream/
// members/composer).
//
// Zero dependencies by design (web/ has no build step); tested in
// bus.test.js.

/**
 * @returns {{
 *   on: (hints: string|string[]|null, fn: (key: string|null, hint: string) => void) => void,
 *   emit: (key: string|null, hint: string) => void,
 *   size: () => number,
 * }}
 */
export function createBus() {
  /** @type {{hints: Set<string>|null, fn: (key: string|null, hint: string) => void}[]} */
  const subs = [];
  return {
    on(hints, fn) {
      subs.push({ hints: hints ? new Set([].concat(hints)) : null, fn });
    },
    emit(key, hint) {
      for (const s of subs) {
        if (!s.hints || s.hints.has(hint)) s.fn(key, hint);
      }
    },
    size() {
      return subs.length;
    },
  };
}

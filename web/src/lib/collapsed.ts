// Which room list groups are folded. The only thing kept in the browser.

export type Collapsed = { dms: boolean; rooms: boolean };

const key = 'hermes-chat.collapsed';

// loadCollapsed never throws: absent, unreadable or malformed reads as both expanded.
export function loadCollapsed(): Collapsed {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? 'null');
    if (v && typeof v.dms === 'boolean' && typeof v.rooms === 'boolean') {
      return { dms: v.dms, rooms: v.rooms };
    }
  } catch {
    // Storage may be blocked or hold garbage.
  }
  return { dms: false, rooms: false };
}

// saveCollapsed swallows a storage error: the groups still fold, just not for next time.
export function saveCollapsed(c: Collapsed): void {
  try {
    localStorage.setItem(key, JSON.stringify(c));
  } catch {
    // Nothing to do.
  }
}

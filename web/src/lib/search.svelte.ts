// The room list's search: whether the field is open, what was typed, and which
// rooms match. Held here so the field and the list read one query; Home empties
// it when it unmounts. Nothing is stored in the browser.
import { byRoomName, listOrder, roomName } from './rooms';
import type { Room } from './types';

export const search = $state({ open: false, query: '' });

export function openSearch(): void {
  search.open = true;
}

export function closeSearch(): void {
  search.open = false;
  search.query = '';
}

// One leading @ or # is dropped from each term, so "@scout" and "#trip" work.
function terms(query: string): string[] {
  return query
    .toLocaleLowerCase()
    .split(/\s+/)
    .map((t) => t.replace(/^[@#]/, ''))
    .filter((t) => t !== '');
}

function haystack(room: Room): string[] {
  const parts = [roomName(room), room.topic];
  for (const a of room.agents) parts.push(a.display_name, a.handle);
  return parts.map((p) => p.toLocaleLowerCase());
}

// Rooms whose name, topic or agents hold every term, as plain substrings: live
// ones in the list's order, then archived ones, direct messages first in each.
export function matchRooms(rooms: Room[], query: string): Room[] {
  const wanted = terms(query);
  if (wanted.length === 0) return [];
  const found = rooms.filter((r) => {
    const parts = haystack(r);
    return wanted.every((t) => parts.some((p) => p.includes(t)));
  });
  const archived = found
    .filter((r) => r.archived)
    .sort((a, b) => Number(b.flavor === 'dm') - Number(a.flavor === 'dm') || byRoomName(a, b));
  return [...listOrder(found), ...archived];
}

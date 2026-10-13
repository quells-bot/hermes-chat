// How a room's messages are kept in order and brought up to date. Message ids
// are ULIDs, so string order is time order.
import type { Message } from './types';

// The server's page size for the newest messages of a room.
export const pageSize = 50;

// Puts m in its place by id, replacing a message with the same id. In place.
export function upsert(messages: Message[], m: Message): void {
  let i = messages.length;
  while (i > 0 && messages[i - 1].id > m.id) i--;
  if (i > 0 && messages[i - 1].id === m.id) messages[i - 1] = m;
  else messages.splice(i, 0, m);
}

// The held messages brought up to date with the newest page. When the page is
// full and its oldest message is newer than the newest held one, there may be
// a gap between them, so the page replaces them; otherwise each of its
// messages is upserted (a short page is the whole room).
export function mergeNewest(
  held: Message[],
  page: Message[],
): { messages: Message[]; replaced: boolean } {
  const newestHeld = held.at(-1);
  if (page.length >= pageSize && (!newestHeld || page[0].id > newestHeld.id)) {
    return { messages: page, replaced: true };
  }
  const messages = [...held];
  for (const m of page) upsert(messages, m);
  return { messages, replaced: false };
}

// The id of the first message newer than the marker; the first message when
// the marker is null; null when there is none.
export function firstUnread(messages: Message[], marker: string | null): string | null {
  const next = marker === null ? messages[0] : messages.find((m) => m.id > marker);
  return next?.id ?? null;
}

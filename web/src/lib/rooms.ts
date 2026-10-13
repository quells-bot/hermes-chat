// How the room list is grouped and ordered, and which rooms are unread.
// A room's place depends on its name alone, so a new message never moves it.
import type { Room } from './types';

export type Groups = { dms: Room[]; rooms: Room[] };

// Built once: case is ignored, accents are not, and "Run 2" sorts before "Run 10".
const collator = new Intl.Collator(undefined, { sensitivity: 'accent', numeric: true });

export function byName(a: string, b: string): number {
  return collator.compare(a, b);
}

// A DM is named for its agent; any other room for its topic.
export function roomName(room: Room): string {
  if (room.flavor === 'dm') return room.agents[0]?.display_name ?? '';
  return room.topic;
}

export function isUnread(room: Room): boolean {
  return !room.archived && room.last_message_id !== room.last_read_message_id;
}

export function isWorking(room: Room): boolean {
  return room.agents.some((a) => a.working);
}

export function byRoomName(a: Room, b: Room): number {
  return byName(roomName(a), roomName(b)) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

export function groupRooms(rooms: Room[]): Groups {
  const live = rooms.filter((r) => !r.archived);
  return {
    dms: live.filter((r) => r.flavor === 'dm').sort(byRoomName),
    rooms: live.filter((r) => r.flavor !== 'dm').sort(byRoomName),
  };
}

// The list top to bottom: direct messages, then rooms, archived ones left out.
export function listOrder(rooms: Room[]): Room[] {
  const g = groupRooms(rooms);
  return [...g.dms, ...g.rooms];
}

// The Unread page's order: rooms waiting on a human first, then the newest
// activity first. It is separate from groupRooms, which never follows traffic.
export function unreadRooms(rooms: Room[]): Room[] {
  const newest = (r: Room) => r.last_message_id ?? '';
  return rooms.filter(isUnread).sort((a, b) => {
    const prompts = Number(b.open_prompts > 0) - Number(a.open_prompts > 0);
    if (prompts) return prompts;
    return newest(a) < newest(b) ? 1 : newest(a) > newest(b) ? -1 : 0;
  });
}

import { expect, test } from 'vitest';
import { agent, room } from './fixtures';
import { groupRooms, isUnread, isWorking, roomName, unreadRooms } from './rooms';

test('rooms sort by topic ignoring case, conversations and channels together', () => {
  const g = groupRooms([
    room({ id: '3', topic: 'trip planning', flavor: 'channel' }),
    room({ id: '1', topic: 'Garden' }),
    room({ id: '2', topic: 'heat pump' }),
  ]);
  expect(g.rooms.map((r) => r.topic)).toEqual(['Garden', 'heat pump', 'trip planning']);
});

test('a newer message does not move a room', () => {
  const a = room({ id: '1', topic: 'A', last_message_id: '01A' });
  const b = room({ id: '2', topic: 'B', last_message_id: '01Z' });
  expect(groupRooms([b, a]).rooms.map((r) => r.id)).toEqual(['1', '2']);
});

test('unread is the two markers differing', () => {
  expect(isUnread(room({ last_message_id: null, last_read_message_id: null }))).toBe(false);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: null }))).toBe(true);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: '01A' }))).toBe(true);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: '01B' }))).toBe(false);
  expect(isUnread(room({ archived: true, last_message_id: '01B', last_read_message_id: null }))).toBe(false);
});

test("DMs sort by the agent's display name, not the handle or topic", () => {
  const dm = (id: string, handle: string, display_name: string) =>
    room({ id, flavor: 'dm', topic: 'z', agents: [agent({ id: 'a' + id, handle, display_name })] });
  const g = groupRooms([dm('1', 'aaa', 'Zed'), dm('2', 'zzz', 'amy')]);
  expect(g.dms.map((r) => roomName(r))).toEqual(['amy', 'Zed']);
  expect(g.rooms).toEqual([]);
});

test('archived rooms are in neither group', () => {
  const g = groupRooms([
    room({ id: '1', archived: true }),
    room({ id: '2', flavor: 'dm', archived: true }),
  ]);
  expect(g).toEqual({ dms: [], rooms: [] });
});

test('names that differ only in case keep one order whatever the input order', () => {
  const x = room({ id: '1', topic: 'garden' });
  const y = room({ id: '2', topic: 'Garden' });
  const ids = (rs: typeof x[]) => groupRooms(rs).rooms.map((r) => r.id);
  expect(ids([x, y])).toEqual(['1', '2']);
  expect(ids([y, x])).toEqual(['1', '2']);
});

test('numbers in names sort by value', () => {
  const g = groupRooms([room({ id: '1', topic: 'Run 10' }), room({ id: '2', topic: 'Run 2' })]);
  expect(g.rooms.map((r) => r.topic)).toEqual(['Run 2', 'Run 10']);
});

test("a DM with no agents is named '' and does not throw", () => {
  const dm = room({ flavor: 'dm', topic: 'x' });
  expect(roomName(dm)).toBe('');
  expect(() => groupRooms([dm])).not.toThrow();
});

test('isWorking is true when any agent is working', () => {
  expect(isWorking(room())).toBe(false);
  expect(isWorking(room({ agents: [agent({ id: '1' }), agent({ id: '2' })] }))).toBe(false);
  expect(isWorking(room({ agents: [agent({ id: '1' }), agent({ id: '2', working: true })] }))).toBe(true);
});

test('unreadRooms puts open prompts first, then newest first, and leaves out read and archived rooms', () => {
  const rs = [
    room({ id: 'old', last_message_id: '01A' }),
    room({ id: 'read', last_message_id: '01Z', last_read_message_id: '01Z' }),
    room({ id: 'arch', last_message_id: '01Y', archived: true }),
    room({ id: 'new', last_message_id: '01C' }),
    room({ id: 'prompt', last_message_id: '01B', open_prompts: 1 }),
    room({ id: 'nullid', last_message_id: null, last_read_message_id: '01A' }),
  ];
  expect(unreadRooms(rs).map((r) => r.id)).toEqual(['prompt', 'new', 'old', 'nullid']);
});

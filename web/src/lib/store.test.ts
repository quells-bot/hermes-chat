import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { agent, message, room } from './fixtures';
import {
  apply,
  closeRoom,
  connect,
  disconnect,
  load,
  loadOlder,
  openRoom,
  putRoom,
  resumeAll,
  stopAll,
  store,
} from './store.svelte';
import { openStream, type StreamHandlers } from './stream';
import type { Agent, Message, Room } from './types';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  listRooms: vi.fn(),
  getRoom: vi.fn(),
  listAgents: vi.fn(),
  getHalt: vi.fn(),
  listMessages: vi.fn(),
  stopEverything: vi.fn(),
  resumeEverything: vi.fn(),
}));

vi.mock('./stream', () => ({ openStream: vi.fn() }));

// What the store passed to the stream, and the stream it was given back.
let handlers: StreamHandlers;
const stream = { close: vi.fn(), kick: vi.fn() };

const unreachable = () => new api.ApiError(0, 'network', 'cannot reach the server');
const noSession = () => new api.ApiError(401, 'unauthenticated', 'authentication required');

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

// delivered applies a delivery.changed for one message in room 01R: every
// agent's copy of it, as [agent id, state].
function delivered(message: string, ...copies: [agent: string, state: string][]): void {
  apply('delivery.changed', {
    message: {
      id: message,
      room_id: '01R',
      deliveries: copies.map(([id, state]) => ({ agent: { id }, state })),
    },
  });
}

// workingIn says which of room 01R's agents the store holds as working.
function workingIn(): Record<string, boolean> {
  return Object.fromEntries(store.rooms['01R'].agents.map((a) => [a.id, a.working]));
}

// run makes count messages in a room, oldest first, with the ids 01M<first>
// and up, padded so that they sort as their numbers do.
function run(first: number, count: number, roomId = '01R'): Message[] {
  return Array.from({ length: count }, (_, i) =>
    message({ id: `01M${String(first + i).padStart(3, '0')}`, room_id: roomId }),
  );
}

function ids(roomId = '01R'): string[] {
  return store.feeds[roomId].messages.map((m) => m.id);
}

// loaded connects, lets ready load these rooms, and waits for the load.
async function loaded(rooms: Room[] = []): Promise<void> {
  vi.mocked(api.listRooms).mockResolvedValue(rooms);
  connect();
  handlers.ready();
  await vi.waitFor(() => expect(store.loaded).toBe(true));
}

// opened is loaded, with the room among the rooms unless they are given, and
// then openRoom answered with this page.
async function opened(id: string, page: Message[], rooms: Room[] = [room({ id })]): Promise<void> {
  await loaded(rooms);
  vi.mocked(api.listMessages).mockResolvedValueOnce(page);
  await openRoom(id);
}

function becomeVisible(): void {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
  disconnect();
  stream.close.mockReset();
  stream.kick.mockReset();
  vi.mocked(openStream)
    .mockReset()
    .mockImplementation((h) => {
      handlers = h;
      return stream;
    });
  vi.mocked(api.listRooms).mockReset().mockResolvedValue([]);
  vi.mocked(api.listAgents).mockReset().mockResolvedValue([]);
  vi.mocked(api.getRoom).mockReset();
  vi.mocked(api.getHalt).mockReset().mockResolvedValue(false);
  vi.mocked(api.listMessages).mockReset().mockResolvedValue([]);
  vi.mocked(api.stopEverything).mockReset();
  vi.mocked(api.resumeEverything).mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

test('ready loads rooms, agents and halt', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  vi.mocked(api.listAgents).mockResolvedValue([agent({ id: '01A' })]);
  vi.mocked(api.getHalt).mockResolvedValue(true);
  connect();
  handlers.ready();
  await vi.waitFor(() => expect(store.loaded).toBe(true));
  expect(Object.keys(store.rooms)).toEqual(['01R']);
  expect(store.agents['01A']).toBeDefined();
  expect(store.halted).toBe(true);
});

test('the stream reports its state and its events to the store', () => {
  connect();
  expect(store.link).toBe('connecting');

  handlers.state('waiting');
  expect(store.link).toBe('waiting');

  handlers.event('room.changed', { room: room({ id: '01R' }) });
  expect(store.rooms['01R']).toBeDefined();
});

test('a replayed older message does not move last_message_id back', () => {
  putRoom(room({ id: '01R', last_message_id: '01M5' }));
  apply('message.posted', { message: { id: '01M3', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M5');
  apply('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  apply('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M7');
});

test('the first message in a room sets last_message_id', () => {
  putRoom(room({ id: '01R' }));
  apply('message.posted', { message: { id: '01M1', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M1');
});

test('room.changed replaces the room and carries the halt', () => {
  apply('room.changed', { room: room({ id: '01R', topic: 'Before', open_prompts: 2, halted: true }) });
  expect(store.rooms['01R'].topic).toBe('Before');
  expect(store.halted).toBe(true);

  const after = room({ id: '01R', topic: 'After', halted: false });
  apply('room.changed', { room: after });
  expect(store.rooms['01R']).toEqual(after);
  expect(Object.keys(store.rooms)).toEqual(['01R']);
  expect(store.halted).toBe(false);
});

test('delivery.changed sets and clears working', () => {
  const helper = agent({ id: '01A' });
  const other = agent({ id: '01B', handle: 'other' });
  putRoom(room({ id: '01R', agents: [helper, other] }));
  const working = (id: string) => store.rooms['01R'].agents.find((a) => a.id === id)?.working;
  const report = (state: string) =>
    apply('delivery.changed', {
      message: { id: '01M1', room_id: '01R', deliveries: [{ agent: { id: '01A' }, state }] },
    });

  for (const state of ['waiting', 'delivered', 'cancelled']) {
    report(state);
    expect(working('01A')).toBe(false);
  }
  report('working');
  expect(working('01A')).toBe(true);
  expect(working('01B')).toBe(false);
  for (const state of ['waiting', 'delivered', 'cancelled']) {
    report(state);
    expect(working('01A')).toBe(true);
  }
  report('done');
  expect(working('01A')).toBe(false);
  report('working');
  report('failed');
  expect(working('01A')).toBe(false);
});

test('a finished copy of an older message does not clear an agent working on a later one', () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A' }), agent({ id: '01B' })] }));

  // M1 wakes both agents, M2 only A. A finishes M1 and starts M2.
  delivered('01M1', ['01A', 'working'], ['01B', 'working']);
  delivered('01M1', ['01A', 'done'], ['01B', 'working']);
  delivered('01M2', ['01A', 'working']);
  expect(workingIn()).toEqual({ '01A': true, '01B': true });

  // B finishes M1; the event carries A's finished copy of M1 too.
  delivered('01M1', ['01A', 'done'], ['01B', 'done']);
  expect(workingIn()).toEqual({ '01A': true, '01B': false });

  delivered('01M2', ['01A', 'failed']);
  expect(workingIn()).toEqual({ '01A': false, '01B': false });
  expect(api.getRoom).not.toHaveBeenCalled();
});

test('done for the message that set working clears it', () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A' })] }));

  delivered('01M1', ['01A', 'working']);
  delivered('01M1', ['01A', 'done']);
  expect(workingIn()).toEqual({ '01A': false });

  // Nothing is recorded and nothing is set, so there is nothing to ask.
  delivered('01M1', ['01A', 'done']);
  expect(workingIn()).toEqual({ '01A': false });
  expect(api.getRoom).not.toHaveBeenCalled();
});

test('done for a message after a snapshot said working refetches the room', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([
    room({ id: '01R', agents: [agent({ id: '01A', working: true })] }),
  ]);
  await load();
  // The agent is on M2; this is its finished copy of M1.
  const now = room({ id: '01R', topic: 'Now', agents: [agent({ id: '01A', working: true })] });
  vi.mocked(api.getRoom).mockResolvedValue(now);

  delivered('01M1', ['01A', 'done']);

  expect(workingIn()).toEqual({ '01A': true }); // not guessed
  expect(api.getRoom).toHaveBeenCalledTimes(1);
  expect(api.getRoom).toHaveBeenCalledWith('01R');
  await vi.waitFor(() => expect(store.rooms['01R']).toEqual(now));
});

test('two such events for one room make one refetch while it is in flight', async () => {
  const working = agent({ id: '01A', working: true });
  putRoom(room({ id: '01R', agents: [working] }));
  putRoom(room({ id: '01S', agents: [working] }));
  const first = deferred<Room>();
  vi.mocked(api.getRoom).mockReturnValueOnce(first.promise);

  delivered('01M1', ['01A', 'done']);
  delivered('01M2', ['01A', 'failed']);
  expect(api.getRoom).toHaveBeenCalledTimes(1);

  first.resolve(room({ id: '01R', agents: [working] }));
  await vi.waitFor(() => expect(store.rooms['01R'].topic).toBe('Topic'));
  await Promise.resolve();

  // The first has settled, so the next one asks again; another room never waited.
  vi.mocked(api.getRoom).mockResolvedValue(room({ id: '01R', agents: [agent({ id: '01A' })] }));
  delivered('01M3', ['01A', 'done']);
  expect(api.getRoom).toHaveBeenCalledTimes(2);
  await vi.waitFor(() => expect(workingIn()).toEqual({ '01A': false }));
});

test('a refetch that settles after disconnect writes nothing', async () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A', working: true })] }));
  const slow = deferred<Room>();
  vi.mocked(api.getRoom).mockReturnValueOnce(slow.promise);
  delivered('01M1', ['01A', 'done']);

  disconnect();
  slow.resolve(room({ id: '01R' }));
  await slow.promise;
  await Promise.resolve();

  expect(store.rooms).toEqual({});
});

test('a refetch that settles after a newer load started writes nothing', async () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A', working: true })] }));
  const slow = deferred<Room>();
  vi.mocked(api.getRoom).mockReturnValueOnce(slow.promise);
  delivered('01M1', ['01A', 'done']);

  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R', topic: 'Loaded' })]);
  await load();
  slow.resolve(room({ id: '01R', topic: 'Stale' }));
  await slow.promise;
  await Promise.resolve();

  expect(store.rooms['01R'].topic).toBe('Loaded');
});

test('a failed refetch changes nothing and sets no error', async () => {
  const held = room({ id: '01R', agents: [agent({ id: '01A', working: true })] });
  putRoom(held);
  let reject!: (err: Error) => void;
  const failing = new Promise<Room>((_, r) => (reject = r));
  vi.mocked(api.getRoom).mockReturnValueOnce(failing);

  delivered('01M1', ['01A', 'done']);
  reject(unreachable());
  await failing.catch(() => {});
  await Promise.resolve();

  expect(store.rooms).toEqual({ '01R': held });
  expect(store.error).toBe('');
});

test('a room.changed forgets which message set working in that room', () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A' })] }));
  delivered('01M2', ['01A', 'working']);

  // The room's agents are replaced, working as the server has it now.
  apply('room.changed', { room: room({ id: '01R', agents: [agent({ id: '01A', working: true })] }) });
  vi.mocked(api.getRoom).mockReturnValue(new Promise(() => {}));
  delivered('01M3', ['01A', 'done']);

  expect(workingIn()).toEqual({ '01A': true });
  expect(api.getRoom).toHaveBeenCalledTimes(1);
});

test('agent.presence reaches the agent in every room it is in, and the directory', () => {
  putRoom(room({ id: '01R', agents: [agent({ id: '01A' }), agent({ id: '01B' })] }));
  putRoom(room({ id: '01S', agents: [agent({ id: '01A' })] }));
  putRoom(room({ id: '01T', agents: [agent({ id: '01B' })] }));
  const renamed: Agent = { id: '01A', handle: 'helper', display_name: 'Renamed', revoked: false };

  apply('agent.presence', { agent: renamed, connected: true });

  expect(store.agents['01A']).toEqual(renamed);
  expect(store.rooms['01R'].agents.map((a) => a.connected)).toEqual([true, false]);
  expect(store.rooms['01S'].agents[0].connected).toBe(true);
  expect(store.rooms['01T'].agents[0].connected).toBe(false);

  apply('agent.presence', { agent: renamed, connected: false });
  expect(store.rooms['01R'].agents[0].connected).toBe(false);
  expect(store.rooms['01S'].agents[0].connected).toBe(false);
});

test('an event for a room that is not held does nothing', () => {
  const held = room({ id: '01R', last_message_id: '01M1', agents: [agent({ id: '01A' })] });
  putRoom(held);

  apply('message.posted', { message: { id: '01M9', room_id: '01X', deliveries: [] } });
  apply('delivery.changed', {
    message: { id: '01M9', room_id: '01X', deliveries: [{ agent: { id: '01A' }, state: 'working' }] },
  });
  // A name every object has is still not a room.
  apply('message.posted', { message: { id: '01M9', room_id: 'constructor', deliveries: [] } });
  apply('delivery.changed', {
    message: { id: '01M9', room_id: 'constructor', deliveries: [{ agent: { id: '01A' }, state: 'working' }] },
  });

  expect(store.rooms).toEqual({ '01R': held });
});

test('malformed data does nothing', () => {
  const held = room({ id: '01R', last_message_id: '01M1', agents: [agent({ id: '01A' })] });
  putRoom(held);
  const helper: Agent = { id: '01A', handle: 'helper', display_name: 'Helper', revoked: false };
  apply('agent.presence', { agent: helper, connected: false });

  apply('room.changed', null);
  apply('room.changed', {});
  apply('room.changed', { room: 'x' });
  apply('room.changed', { room: { topic: 'no id' } });
  apply('room.changed', { room: { id: '01Z' } }); // no agents to keep current
  apply('message.posted', {});
  apply('message.posted', { message: null });
  apply('message.posted', { message: { room_id: '01R' } });
  apply('message.posted', { message: { id: 9, room_id: '01R' } });
  apply('delivery.changed', undefined);
  apply('delivery.changed', { message: { id: '01M2', room_id: '01R' } });
  apply('delivery.changed', { message: { id: '01M2', room_id: '01R', deliveries: 'x' } });
  apply('delivery.changed', {
    message: { id: '01M2', room_id: '01R', deliveries: [null, {}, { agent: null, state: 'working' }, { state: 'working' }] },
  });
  apply('agent.presence', null);
  apply('agent.presence', 'x');
  apply('agent.presence', { connected: true });
  apply('agent.presence', { agent: {}, connected: true });
  apply('agent.presence', { agent: helper });
  apply('nope', 1);

  expect(store.rooms).toEqual({ '01R': held });
  expect(store.agents).toEqual({ '01A': helper });
  expect(store.halted).toBe(false);
});

test('a load replaces the rooms, dropping one that is gone', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' }), room({ id: '01S' })]);
  vi.mocked(api.listAgents).mockResolvedValue([agent({ id: '01A' }), agent({ id: '01B' })]);
  await load();
  expect(Object.keys(store.rooms)).toEqual(['01R', '01S']);

  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01S', topic: 'Renamed' })]);
  vi.mocked(api.listAgents).mockResolvedValue([agent({ id: '01B' })]);
  await load();

  expect(Object.keys(store.rooms)).toEqual(['01S']);
  expect(store.rooms['01S'].topic).toBe('Renamed');
  expect(Object.keys(store.agents)).toEqual(['01B']);
});

test('a slow load that finishes after a newer one is discarded', async () => {
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  vi.mocked(api.getHalt).mockResolvedValueOnce(true);
  const first = load();

  vi.mocked(api.listRooms).mockResolvedValueOnce([room({ id: '01NEW' })]);
  await load();
  slow.resolve([room({ id: '01OLD' })]);
  await first;

  expect(Object.keys(store.rooms)).toEqual(['01NEW']);
  expect(store.halted).toBe(false);
});

test('a slow load that fails after a newer one succeeded sets no error', async () => {
  let reject!: (err: Error) => void;
  vi.mocked(api.listRooms).mockReturnValueOnce(new Promise((_, r) => (reject = r)));
  const first = load();

  await load();
  reject(unreachable());
  await first;

  expect(store.error).toBe('');
});

test('a failed load sets error and leaves the rooms; the next success clears it', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  await load();

  vi.mocked(api.getHalt).mockRejectedValueOnce(unreachable());
  await expect(load()).resolves.toBeUndefined();
  expect(store.error).toBe('cannot reach the server');
  expect(Object.keys(store.rooms)).toEqual(['01R']);
  expect(store.loaded).toBe(true);

  await load();
  expect(store.error).toBe('');
});

test('a failed first load leaves the store not loaded', async () => {
  vi.mocked(api.listAgents).mockRejectedValue(unreachable());
  await load();
  expect(store.loaded).toBe(false);
  expect(store.error).toBe('cannot reach the server');
});

test('a 401 during load sets no error', async () => {
  vi.mocked(api.listRooms).mockRejectedValue(noSession());
  await expect(load()).resolves.toBeUndefined();
  expect(store.error).toBe('');
  expect(store.loaded).toBe(false);
});

test('becoming visible kicks the stream and reloads when live', async () => {
  connect();
  handlers.state('live');

  becomeVisible();

  expect(stream.kick).toHaveBeenCalledTimes(1);
  expect(api.listRooms).toHaveBeenCalledTimes(1);
  await vi.waitFor(() => expect(store.loaded).toBe(true));
});

test('becoming visible while not live kicks and does not load', () => {
  connect();
  handlers.state('waiting');

  becomeVisible();

  expect(stream.kick).toHaveBeenCalledTimes(1);
  expect(api.listRooms).not.toHaveBeenCalled();
});

test('becoming hidden neither kicks nor loads', () => {
  connect();
  handlers.state('live');
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');

  document.dispatchEvent(new Event('visibilitychange'));

  expect(stream.kick).not.toHaveBeenCalled();
  expect(api.listRooms).not.toHaveBeenCalled();
});

test('stopAll sets halted from the response; a failure sets error and leaves halted', async () => {
  vi.mocked(api.stopEverything).mockResolvedValue(true);
  await stopAll();
  expect(store.halted).toBe(true);
  expect(store.error).toBe('');

  vi.mocked(api.stopEverything).mockRejectedValue(unreachable());
  await expect(stopAll()).resolves.toBeUndefined();
  expect(store.error).toBe('cannot reach the server');
  expect(store.halted).toBe(true);
});

test('resumeAll sets halted from the response; a failure sets error and leaves halted', async () => {
  vi.mocked(api.stopEverything).mockResolvedValue(true);
  await stopAll();

  vi.mocked(api.resumeEverything).mockRejectedValue(unreachable());
  await expect(resumeAll()).resolves.toBeUndefined();
  expect(store.error).toBe('cannot reach the server');
  expect(store.halted).toBe(true);

  vi.mocked(api.resumeEverything).mockResolvedValue(false);
  await resumeAll();
  expect(store.halted).toBe(false);
});

test('disconnect closes the stream, resets the store, and stops listening', async () => {
  const before = store;
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  vi.mocked(api.listAgents).mockResolvedValue([agent({ id: '01A' })]);
  vi.mocked(api.getHalt).mockResolvedValue(true);
  vi.mocked(api.stopEverything).mockRejectedValue(unreachable());
  connect();
  handlers.state('live');
  await load();
  await stopAll();
  vi.mocked(api.listRooms).mockClear();

  disconnect();

  expect(stream.close).toHaveBeenCalledTimes(1);
  expect(store).toBe(before);
  expect(store).toEqual({
    link: 'connecting',
    loaded: false,
    error: '',
    rooms: {},
    agents: {},
    halted: false,
    open: null,
    feeds: {},
  });

  becomeVisible();
  expect(stream.kick).not.toHaveBeenCalled();
  expect(api.listRooms).not.toHaveBeenCalled();
});

test('a load still in flight at disconnect writes nothing', async () => {
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  connect();
  const pending = load();

  disconnect();
  slow.resolve([room({ id: '01R' })]);
  await pending;

  expect(store.rooms).toEqual({});
  expect(store.loaded).toBe(false);
});

test('connect twice opens one stream', () => {
  connect();
  connect();
  expect(openStream).toHaveBeenCalledTimes(1);
});

test('connect after disconnect opens a new stream', () => {
  connect();
  disconnect();
  connect();
  expect(openStream).toHaveBeenCalledTimes(2);
});

test('an event that arrives during a load is applied after the snapshot', async () => {
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  connect();
  handlers.ready();

  handlers.event('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  slow.resolve([room({ id: '01R', last_message_id: '01M5' })]);

  await vi.waitFor(() => expect(store.loaded).toBe(true));
  expect(store.rooms['01R'].last_message_id).toBe('01M7');
});

test('a room.changed for a new room during a load survives the snapshot', async () => {
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  connect();
  handlers.ready();

  handlers.event('room.changed', { room: room({ id: '01NEW' }) });
  expect(store.rooms).toEqual({}); // held, not applied
  slow.resolve([room({ id: '01R' })]);

  await vi.waitFor(() => expect(store.loaded).toBe(true));
  expect(Object.keys(store.rooms)).toEqual(['01R', '01NEW']);
});

test('events held during a failed load are still applied', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R', last_message_id: '01M5' })]);
  await load();
  let reject!: (err: Error) => void;
  vi.mocked(api.listRooms).mockReturnValueOnce(new Promise((_, r) => (reject = r)));
  connect();
  handlers.ready();

  handlers.event('message.posted', { message: { id: '01M6', room_id: '01R', deliveries: [] } });
  handlers.event('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M5');
  reject(unreachable());

  await vi.waitFor(() => expect(store.error).toBe('cannot reach the server'));
  expect(store.rooms['01R'].last_message_id).toBe('01M7');
});

test('events held behind a superseded load wait for the newest one', async () => {
  const first = deferred<Room[]>();
  const second = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  connect();
  const older = load();
  handlers.event('room.changed', { room: room({ id: '01A' }) });
  const newer = load();
  handlers.event('room.changed', { room: room({ id: '01B' }) });

  first.resolve([room({ id: '01OLD' })]);
  await older;
  expect(store.rooms).toEqual({});

  second.resolve([room({ id: '01R' })]);
  await newer;
  expect(Object.keys(store.rooms)).toEqual(['01R', '01A', '01B']);

  // Nothing is in flight now, so an event is applied as it arrives.
  handlers.event('room.changed', { room: room({ id: '01C' }) });
  expect(store.rooms['01C']).toBeDefined();
});

test('a held event that throws does not drop the ones behind it', async () => {
  const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  connect();
  const pending = load();
  const boom = new Error('boom');

  handlers.event('room.changed', { room: room({ id: '01R', topic: 'Before' }) });
  handlers.event('room.changed', {
    get room(): never {
      throw boom;
    },
  });
  handlers.event('room.changed', { room: room({ id: '01R', topic: 'After' }) });
  handlers.event('room.changed', { room: room({ id: '01S' }) });
  slow.resolve([]);

  await expect(pending).resolves.toBeUndefined();
  expect(store.rooms['01R'].topic).toBe('After');
  expect(store.rooms['01S']).toBeDefined();
  expect(logged).toHaveBeenCalledTimes(1);
  expect(logged.mock.calls[0]).toContain(boom);

  // The queue is empty again: nothing is applied twice by the next load.
  await load();
  expect(logged).toHaveBeenCalledTimes(1);
});

test('an entry among a room\'s agents that is not an object is skipped', () => {
  const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
  connect();
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  const pending = load();

  handlers.event('room.changed', { room: { ...room({ id: '01R' }), agents: [null, agent({ id: '01A' })] } });
  handlers.event('delivery.changed', {
    message: { id: '01M1', room_id: '01R', deliveries: [{ agent: { id: '01A' }, state: 'working' }] },
  });
  handlers.event('agent.presence', { agent: agent({ id: '01A' }), connected: true });
  slow.resolve([]);
  return pending.then(() => {
    expect(store.rooms['01R'].agents[1]).toMatchObject({ working: true, connected: true });
    expect(logged).not.toHaveBeenCalled();
  });
});

test('disconnect drops held events', async () => {
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  connect();
  const pending = load();
  handlers.event('room.changed', { room: room({ id: '01GONE' }) });

  disconnect();
  slow.resolve([]);
  await pending;
  expect(store.rooms).toEqual({});

  connect();
  await load();
  expect(store.rooms).toEqual({});
  handlers.event('room.changed', { room: room({ id: '01R' }) });
  expect(Object.keys(store.rooms)).toEqual(['01R']);
});

test('a stop that settles after disconnect writes nothing', async () => {
  const stop = deferred<boolean>();
  vi.mocked(api.stopEverything).mockReturnValueOnce(stop.promise);
  let reject!: (err: Error) => void;
  vi.mocked(api.resumeEverything).mockReturnValueOnce(new Promise((_, r) => (reject = r)));
  connect();
  const stopping = stopAll();
  const resuming = resumeAll();

  disconnect();
  stop.resolve(true);
  reject(unreachable());
  await stopping;
  await resuming;

  expect(store.halted).toBe(false);
  expect(store.error).toBe('');
});

test('a message posted while the page is loading is in the feed once', async () => {
  await loaded([room({ id: '01R' })]);
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  const opening = openRoom('01R');
  handlers.event('message.posted', { message: message({ id: '01M2', room_id: '01R' }) });
  expect(store.feeds['01R'].messages).toEqual([]);
  page.resolve([message({ id: '01M1', room_id: '01R' }), message({ id: '01M2', room_id: '01R' })]);
  await opening;
  expect(store.feeds['01R'].messages.map((m) => m.id)).toEqual(['01M1', '01M2']);
});

test('a live edit sets the text, the logged edit replaces it, and a late live edit is ignored', async () => {
  await opened('01R', [message({ id: '01M1', room_id: '01R', text: '' })]);
  apply('message.edited', { message_id: '01M1', text: 'Bottom li', final: false });
  expect(store.feeds['01R'].messages[0]).toMatchObject({ text: 'Bottom li', streaming: true });
  apply('message.edited', { message: message({ id: '01M1', room_id: '01R', text: 'Bottom line.' }) });
  apply('message.edited', { message_id: '01M1', text: 'Bottom li', final: false });
  expect(store.feeds['01R'].messages[0]).toMatchObject({ text: 'Bottom line.', streaming: false });
});

test('openRoom loads the newest page and says whether older ones exist', async () => {
  await loaded([room({ id: '01R' }), room({ id: '01S' })]);
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);

  const opening = openRoom('01R');
  expect(store.open).toBe('01R');
  expect(store.feeds['01R']).toEqual({ messages: [], more: false, loading: true, error: '' });
  page.resolve(run(1, 50));
  await opening;

  expect(api.listMessages).toHaveBeenCalledWith('01R');
  expect(store.feeds['01R']).toEqual({ messages: run(1, 50), more: true, loading: false, error: '' });

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 3, '01S'));
  await openRoom('01S');
  expect(store.open).toBe('01S');
  expect(store.feeds['01S']).toEqual({ messages: run(1, 3, '01S'), more: false, loading: false, error: '' });
  expect(api.listMessages).toHaveBeenCalledTimes(2);
});

test('openRoom before the first load fetches nothing, and the load brings the page', async () => {
  connect();
  await openRoom('01R');
  expect(store.open).toBe('01R');
  expect(store.feeds).toEqual({});
  expect(api.listMessages).not.toHaveBeenCalled();

  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 50));
  await load();

  expect(api.listMessages).toHaveBeenCalledTimes(1);
  expect(api.listMessages).toHaveBeenCalledWith('01R');
  expect(store.feeds).toEqual({ '01R': { messages: run(1, 50), more: true, loading: false, error: '' } });
});

test('openRoom on a held feed fetches nothing', async () => {
  await opened('01R', run(1, 3));
  const opening = openRoom('01R');
  const again = openRoom('01R'); // nor while the first is in flight
  await opening;
  await again;
  expect(api.listMessages).toHaveBeenCalledTimes(1);
  expect(ids()).toEqual(['01M001', '01M002', '01M003']);
});

test('two openRoom calls for one room make one request while it is in flight', async () => {
  await loaded([room({ id: '01R' })]);
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  const opening = openRoom('01R');
  await openRoom('01R');
  expect(api.listMessages).toHaveBeenCalledTimes(1);
  page.resolve(run(1, 3));
  await opening;
  expect(ids()).toEqual(['01M001', '01M002', '01M003']);
});

test('closeRoom clears open only when it is that room, and the feed stays', async () => {
  await opened('01R', run(1, 3));
  closeRoom('01S');
  expect(store.open).toBe('01R');
  closeRoom('01R');
  expect(store.open).toBeNull();
  expect(ids()).toEqual(['01M001', '01M002', '01M003']);
});

test('a failed page is recorded on the feed, and openRoom tries again', async () => {
  await loaded([room({ id: '01R' }), room({ id: '01S' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());
  await expect(openRoom('01R')).resolves.toBeUndefined();
  expect(store.feeds['01R']).toEqual({ messages: [], more: false, loading: false, error: 'cannot reach the server' });
  expect(store.error).toBe('');

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 2));
  await openRoom('01R');
  expect(api.listMessages).toHaveBeenCalledTimes(2);
  expect(store.feeds['01R']).toEqual({ messages: run(1, 2), more: false, loading: false, error: '' });

  // The API layer has already signed the person out; there is nothing to say.
  vi.mocked(api.listMessages).mockRejectedValueOnce(noSession());
  await openRoom('01S');
  expect(store.feeds['01S']).toEqual({ messages: [], more: false, loading: false, error: '' });
});

test('message.posted appends to a held feed, and applying it twice changes nothing', async () => {
  await opened('01R', run(1, 2));
  const posted = message({ id: '01M003', room_id: '01R', text: 'third' });
  apply('message.posted', { message: posted });
  apply('message.posted', { message: posted });
  expect(store.feeds['01R'].messages).toEqual([...run(1, 2), posted]);
  expect(store.rooms['01R'].last_message_id).toBe('01M003');
});

test('a message for a room whose feed is not held changes no feed', async () => {
  await opened('01R', run(1, 2), [room({ id: '01R' }), room({ id: '01S' })]);
  apply('message.posted', { message: message({ id: '01M009', room_id: '01S' }) });
  apply('message.edited', { message: message({ id: '01M009', room_id: '01S' }) });
  expect(store.feeds).toEqual({ '01R': { messages: run(1, 2), more: false, loading: false, error: '' } });
  expect(store.rooms['01S'].last_message_id).toBe('01M009');
});

test('a message older than the oldest held is ignored while older pages remain, and inserted when none do', async () => {
  await opened('01R', run(11, 50), [room({ id: '01R' }), room({ id: '01S' })]);
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(11, 3, '01S'));
  await openRoom('01S');

  // It belongs to a page not yet loaded.
  apply('delivery.changed', { message: message({ id: '01M005', room_id: '01R' }) });
  expect(ids()).toEqual(run(11, 50).map((m) => m.id));

  // The whole room is held, so this is a message the page missed.
  apply('delivery.changed', { message: message({ id: '01M005', room_id: '01S' }) });
  expect(ids('01S')).toEqual(['01M005', '01M011', '01M012', '01M013']);
});

test('delivery.changed and prompt.changed replace the held message', async () => {
  await opened('01R', run(1, 2), [room({ id: '01R', agents: [agent({ id: '01A' })] })]);
  const helper: Agent = { id: '01A', handle: 'helper', display_name: 'Helper', revoked: false };

  const working = message({ id: '01M001', room_id: '01R', deliveries: [{ agent: helper, state: 'working' }] });
  apply('delivery.changed', { message: working });
  expect(store.feeds['01R'].messages).toEqual([working, ...run(2, 1)]);
  expect(workingIn()).toEqual({ '01A': true }); // slice 2's handling still runs

  const answered = message({
    id: '01M002',
    room_id: '01R',
    prompt: { kind: 'approval', choices: ['yes', 'no'], state: 'answered', answer: 'yes', answered_by: null, closed_at: null },
  });
  apply('prompt.changed', { message: answered });
  expect(store.feeds['01R'].messages).toEqual([working, answered]);
});

test('a message event without text or attachments still moves last_message_id and changes no feed', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', agents: [agent({ id: '01A' })] })]);
  const { text: _text, ...noText } = message({ id: '01M002', room_id: '01R' });
  const { attachments: _attachments, ...noAttachments } = message({
    id: '01M003',
    room_id: '01R',
    deliveries: [{ agent: agent({ id: '01A' }), state: 'working' }],
  });

  apply('message.posted', { message: noText });
  expect(store.rooms['01R'].last_message_id).toBe('01M002');
  apply('delivery.changed', { message: noAttachments });
  expect(workingIn()).toEqual({ '01A': true });
  apply('message.edited', { message: noText });
  apply('prompt.changed', { message: { ...noAttachments, deliveries: 'x' } });

  expect(ids()).toEqual(['01M001']);
});

test('a live edit for a message that is not held changes nothing', async () => {
  await opened('01R', run(1, 2));
  apply('message.edited', { message_id: '01M009', text: 'typing', final: false });
  apply('message.edited', { message_id: '01M001', final: false }); // no text
  apply('message.edited', { message_id: 1, text: 'typing' });
  apply('message.edited', null);
  expect(store.feeds).toEqual({ '01R': { messages: run(1, 2), more: false, loading: false, error: '' } });
});

test('a logged edit for a message not yet held still makes a late live edit ignored', async () => {
  await opened('01R', run(51, 50));
  // 01M010 is on an older page: the logged edit is not inserted, but it is final.
  apply('message.edited', { message: message({ id: '01M010', room_id: '01R', text: 'Done.' }) });
  expect(ids()).toEqual(run(51, 50).map((m) => m.id));

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 50));
  await loadOlder('01R');
  apply('message.edited', { message_id: '01M010', text: 'Do', final: false });
  expect(store.feeds['01R'].messages[9]).toMatchObject({ id: '01M010', streaming: false });
  expect(store.feeds['01R'].messages[9].text).not.toBe('Do');
});

test('a marker older than the first page brings one request for the 150 before it', async () => {
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockResolvedValueOnce(run(51, 150));
  await loaded([room({ id: '01R', last_read_message_id: '01M040' })]);
  await openRoom('01R');

  expect(api.listMessages).toHaveBeenCalledTimes(2);
  expect(api.listMessages).toHaveBeenNthCalledWith(2, '01R', '01M201', 150);
  expect(ids()).toEqual(run(51, 200).map((m) => m.id));
  expect(store.feeds['01R']).toMatchObject({ more: true, loading: false, error: '' });
});

test('a backlog shorter than 150 says there are no older ones, and one that fails is ignored', async () => {
  await loaded([
    room({ id: '01R', last_read_message_id: '01M040' }),
    room({ id: '01S', last_read_message_id: '01M040' }),
  ]);
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockResolvedValueOnce(run(191, 10));
  await openRoom('01R');
  expect(ids()).toEqual(run(191, 60).map((m) => m.id));
  expect(store.feeds['01R'].more).toBe(false);

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50, '01S')).mockRejectedValueOnce(unreachable());
  await expect(openRoom('01S')).resolves.toBeUndefined();
  expect(store.feeds['01S']).toEqual({ messages: run(201, 50, '01S'), more: true, loading: false, error: '' });
  expect(store.error).toBe('');
});

test('a null marker, and a marker inside the first page, bring no second request', async () => {
  await loaded([
    room({ id: '01R', last_read_message_id: null }),
    room({ id: '01S', last_read_message_id: '01M220' }),
    room({ id: '01T', last_read_message_id: '01M201' }), // the oldest held is read: nothing unread lies before it
    room({ id: '01U', last_read_message_id: '01M001' }), // older, but the page is the whole room
  ]);
  for (const id of ['01R', '01S', '01T']) {
    vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50, id));
    await openRoom(id);
  }
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 3, '01U'));
  await openRoom('01U');

  expect(api.listMessages).toHaveBeenCalledTimes(4);
  expect(ids('01S')).toEqual(run(201, 50).map((m) => m.id));
});

test('events stay held from the newest page through the backlog', async () => {
  await loaded([room({ id: '01R', last_read_message_id: '01M040' })]);
  const page = deferred<Message[]>();
  const backlog = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise).mockReturnValueOnce(backlog.promise);
  const opening = openRoom('01R');

  // A change to a message the backlog will bring. Applied between the two
  // fetches it would be dropped, as older than the oldest held.
  handlers.event('delivery.changed', { message: message({ id: '01M060', room_id: '01R', text: 'edited' }) });
  page.resolve(run(201, 50));
  await vi.waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(2));
  handlers.event('message.posted', { message: message({ id: '01M251', room_id: '01R' }) });
  expect(ids()).toEqual(run(201, 50).map((m) => m.id));

  backlog.resolve(run(51, 150));
  await opening;
  expect(ids()).toEqual(run(51, 201).map((m) => m.id));
  expect(store.feeds['01R'].messages[9]).toMatchObject({ id: '01M060', text: 'edited' });
});

test('loadOlder prepends the page before the oldest held', async () => {
  await opened('01R', run(101, 50));

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(51, 50));
  await loadOlder('01R');
  expect(api.listMessages).toHaveBeenLastCalledWith('01R', '01M101', 50);
  expect(ids()).toEqual(run(51, 100).map((m) => m.id));
  expect(store.feeds['01R']).toMatchObject({ more: true, loading: false, error: '' });

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(48, 3));
  await loadOlder('01R');
  expect(api.listMessages).toHaveBeenLastCalledWith('01R', '01M051', 50);
  expect(ids()).toEqual(run(48, 103).map((m) => m.id));
  expect(store.feeds['01R'].more).toBe(false);

  // Nothing older, and a room whose feed is not held: nothing to ask.
  await loadOlder('01R');
  await loadOlder('01S');
  expect(api.listMessages).toHaveBeenCalledTimes(3);
});

test('a second loadOlder while one is in flight does nothing', async () => {
  await opened('01R', run(101, 50));
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);

  const first = loadOlder('01R');
  expect(store.feeds['01R'].loading).toBe(true);
  await loadOlder('01R');
  expect(api.listMessages).toHaveBeenCalledTimes(2); // the open, and one older page

  page.resolve(run(51, 50));
  await first;
  expect(store.feeds['01R'].loading).toBe(false);
  expect(ids()).toEqual(run(51, 100).map((m) => m.id));
});

test('a loadOlder that fails leaves more true and records nothing', async () => {
  await opened('01R', run(101, 50));
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());

  await expect(loadOlder('01R')).resolves.toBeUndefined();

  expect(store.feeds['01R']).toEqual({ messages: run(101, 50), more: true, loading: false, error: '' });
  expect(store.error).toBe('');
});

test('events during loadOlder are held and applied after it', async () => {
  await opened('01R', run(51, 50));
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  const loading = loadOlder('01R');

  // Applied now, the first would be dropped as older than the oldest held,
  // and the page, read before it, would bring the old text.
  handlers.event('delivery.changed', { message: message({ id: '01M010', room_id: '01R', text: 'edited' }) });
  handlers.event('message.posted', { message: message({ id: '01M101', room_id: '01R' }) });
  expect(ids()).toEqual(run(51, 50).map((m) => m.id));

  page.resolve(run(1, 50));
  await loading;
  expect(ids()).toEqual(run(1, 101).map((m) => m.id));
  expect(store.feeds['01R'].messages[9]).toMatchObject({ id: '01M010', text: 'edited' });

  // Nothing is in flight now, so an event is applied as it arrives.
  handlers.event('message.posted', { message: message({ id: '01M102', room_id: '01R' }) });
  expect(ids().at(-1)).toBe('01M102');
});

test('an older page that arrives after the feed was replaced is dropped', async () => {
  await opened('01R', run(101, 50));
  const older = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(older.promise).mockResolvedValueOnce(run(201, 50));
  const loading = loadOlder('01R');

  await load(); // its page does not reach the held messages, so it replaces them
  expect(ids()).toEqual(run(201, 50).map((m) => m.id));
  older.resolve(run(51, 50));
  await loading;

  // Prepended, it would sit across a gap from the messages now held.
  expect(ids()).toEqual(run(201, 50).map((m) => m.id));
  expect(store.feeds['01R']).toMatchObject({ more: true, loading: false });
});

test('load keeps only the open feed and merges its newest page', async () => {
  await opened('01S', run(1, 3, '01S'), [room({ id: '01R' }), room({ id: '01S' })]);
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 50));
  await openRoom('01R');
  expect(Object.keys(store.feeds)).toEqual(['01S', '01R']);

  const newer = run(41, 50).map((m) => ({ ...m, text: 'newer' }));
  vi.mocked(api.listMessages).mockResolvedValueOnce(newer);
  await load();

  expect(api.listMessages).toHaveBeenLastCalledWith('01R');
  expect(Object.keys(store.feeds)).toEqual(['01R']);
  expect(store.feeds['01R']).toEqual({ messages: [...run(1, 40), ...newer], more: true, loading: false, error: '' });
});

test('a load with no room open keeps no feed and asks for no page', async () => {
  await opened('01R', run(1, 3));
  closeRoom('01R');
  await load();
  expect(store.feeds).toEqual({});
  expect(api.listMessages).toHaveBeenCalledTimes(1);
});

test('load replaces the open feed when the page does not reach it', async () => {
  // The marker is inside the first page, so opening brings no backlog.
  await opened('01R', run(1, 50), [room({ id: '01R', last_read_message_id: '01M040' })]);
  expect(api.listMessages).toHaveBeenCalledTimes(1);

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(101, 50)).mockResolvedValueOnce(run(51, 50));
  await load();

  // The new page starts past the marker, so the backlog rule runs again.
  expect(api.listMessages).toHaveBeenCalledTimes(3);
  expect(api.listMessages).toHaveBeenNthCalledWith(3, '01R', '01M101', 150);
  expect(ids()).toEqual(run(51, 100).map((m) => m.id));
  expect(store.feeds['01R'].more).toBe(false);
});

test('a second load whose page merely merges brings no backlog request', async () => {
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(301, 50)).mockResolvedValueOnce(run(151, 150));
  await loaded([room({ id: '01R', last_read_message_id: '01M100' })]);
  await openRoom('01R');
  // The marker is still older than the oldest held, and older pages remain.
  expect(ids()[0]).toBe('01M151');
  expect(store.feeds['01R'].more).toBe(true);

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(341, 50));
  await load();

  expect(api.listMessages).toHaveBeenCalledTimes(3);
  expect(ids()).toEqual(run(151, 240).map((m) => m.id));
  expect(store.feeds['01R'].more).toBe(true);
});

test('a page that fails inside load does not fail the load', async () => {
  connect();
  await openRoom('01R');
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());

  await load();

  expect(Object.keys(store.rooms)).toEqual(['01R']);
  expect(store.loaded).toBe(true);
  expect(store.error).toBe('');
  // No feed was held, so the failure is the feed's, as after a failed openRoom.
  expect(store.feeds['01R']).toEqual({ messages: [], more: false, loading: false, error: 'cannot reach the server' });

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 3));
  await openRoom('01R');
  expect(ids()).toEqual(['01M001', '01M002', '01M003']);
});

test('a 401 on the page inside load records no message on the feed', async () => {
  connect();
  await openRoom('01R');
  vi.mocked(api.listMessages).mockRejectedValueOnce(noSession());
  await load();
  expect(store.feeds['01R']).toEqual({ messages: [], more: false, loading: false, error: '' });
  expect(store.error).toBe('');
});

test('a page that fails inside load leaves a held feed as it was', async () => {
  await opened('01R', run(1, 50));
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R', topic: 'Renamed' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());

  await load();

  expect(store.rooms['01R'].topic).toBe('Renamed');
  expect(store.error).toBe('');
  expect(store.feeds['01R']).toEqual({ messages: run(1, 50), more: true, loading: false, error: '' });
});

test('a page that arrives through load clears a held feed\'s error', async () => {
  await loaded([room({ id: '01R' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());
  await openRoom('01R');
  expect(store.feeds['01R'].error).toBe('cannot reach the server');

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 3));
  await load();

  expect(store.feeds['01R']).toEqual({ messages: run(1, 3), more: false, loading: false, error: '' });
});

test('a room opened while a load is in flight gets its page when the load is written', async () => {
  connect();
  await openRoom('01R');
  const rooms = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(rooms.promise);
  const other = deferred<Message[]>();
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 3)).mockReturnValueOnce(other.promise);
  const loading = load();

  // Not loaded yet, so this fetches nothing, and the load asked for 01R.
  await openRoom('01S');
  expect(api.listMessages).toHaveBeenCalledTimes(1);
  rooms.resolve([room({ id: '01R' }), room({ id: '01S' })]);
  await vi.waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(2));
  expect(api.listMessages).toHaveBeenLastCalledWith('01S');
  expect(store.feeds).toEqual({ '01S': { messages: [], more: false, loading: true, error: '' } });

  // Still one snapshot in flight: events wait for it.
  handlers.event('message.posted', { message: message({ id: '01M004', room_id: '01S' }) });
  expect(ids('01S')).toEqual([]);
  other.resolve(run(1, 3, '01S'));
  await loading;

  expect(Object.keys(store.feeds)).toEqual(['01S']);
  expect(ids('01S')).toEqual(['01M001', '01M002', '01M003', '01M004']);
});

test('disconnect empties open and feeds, and a page that arrives afterwards writes nothing', async () => {
  await opened('01R', run(101, 50), [room({ id: '01R' }), room({ id: '01S' })]);
  const page = deferred<Message[]>();
  const older = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise).mockReturnValueOnce(older.promise);
  const opening = openRoom('01S');
  const loading = loadOlder('01R');
  handlers.event('message.posted', { message: message({ id: '01M151', room_id: '01R' }) });

  disconnect();
  expect(store.open).toBeNull();
  expect(store.feeds).toEqual({});
  page.resolve(run(1, 3, '01S'));
  older.resolve(run(51, 50));
  await opening;
  await loading;
  expect(store.feeds).toEqual({});

  // The count went back to zero with the disconnect, and stays there when
  // the old answers land: an event is applied as it arrives.
  connect();
  handlers.event('room.changed', { room: room({ id: '01R' }) });
  expect(Object.keys(store.rooms)).toEqual(['01R']);

  // And the next fetch is counted from zero, so events are held during it.
  const rooms = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(rooms.promise);
  const reloading = load();
  handlers.event('room.changed', { room: room({ id: '01NEW' }) });
  expect(Object.keys(store.rooms)).toEqual(['01R']);
  rooms.resolve([]);
  await reloading;
  expect(Object.keys(store.rooms)).toEqual(['01NEW']);
});

test('disconnect forgets which messages were finalized', async () => {
  await opened('01R', [message({ id: '01M1', room_id: '01R', text: '' })]);
  apply('message.edited', { message: message({ id: '01M1', room_id: '01R', text: 'Done.' }) });

  disconnect();
  await opened('01R', [message({ id: '01M1', room_id: '01R', text: '' })]);
  apply('message.edited', { message_id: '01M1', text: 'Do', final: false });

  expect(store.feeds['01R'].messages[0]).toMatchObject({ text: 'Do', streaming: true });
});

test('a message event for a feed whose first page failed changes no feed', async () => {
  await loaded([room({ id: '01R' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());
  await openRoom('01R');
  const failedFeed = { messages: [], more: false, loading: false, error: 'cannot reach the server' };

  apply('message.posted', { message: message({ id: '01M050', room_id: '01R' }) });
  apply('delivery.changed', { message: message({ id: '01M050', room_id: '01R' }) });
  apply('message.edited', { message_id: '01M050', text: 'typing', final: false });

  expect(store.feeds).toEqual({ '01R': failedFeed });
  expect(store.rooms['01R'].last_message_id).toBe('01M050');
});

test('a feed whose first page failed is written fresh by the load that brings a page', async () => {
  await loaded([room({ id: '01R', last_read_message_id: '01M040' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());
  await openRoom('01R');
  // Put into the failed feed, this would make the full page below look like
  // one that reaches what is held, and more would stay false.
  apply('message.posted', { message: message({ id: '01M250', room_id: '01R' }) });

  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockResolvedValueOnce(run(51, 150));
  await load();

  expect(api.listMessages).toHaveBeenCalledTimes(3);
  expect(api.listMessages).toHaveBeenNthCalledWith(3, '01R', '01M201', 150);
  expect(ids()).toEqual(run(51, 200).map((m) => m.id));
  expect(store.feeds['01R']).toMatchObject({ more: true, loading: false, error: '' });
});

test('a page that fails inside load again replaces the reason on a failed feed', async () => {
  await loaded([room({ id: '01R' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());
  await openRoom('01R');

  vi.mocked(api.listMessages).mockRejectedValueOnce(new api.ApiError(500, 'internal', 'internal error'));
  await load();

  expect(store.feeds['01R']).toEqual({ messages: [], more: false, loading: false, error: 'internal error' });
  expect(store.error).toBe('');
});

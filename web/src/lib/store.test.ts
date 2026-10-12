import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { agent, room } from './fixtures';
import { apply, connect, disconnect, load, putRoom, resumeAll, stopAll, store } from './store.svelte';
import { openStream, type StreamHandlers } from './stream';
import type { Agent, Room } from './types';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  listRooms: vi.fn(),
  getRoom: vi.fn(),
  listAgents: vi.fn(),
  getHalt: vi.fn(),
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

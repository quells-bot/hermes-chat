import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { agent, room } from './fixtures';
import { apply, connect, disconnect, load, putRoom, resumeAll, stopAll, store } from './store.svelte';
import { openStream, type StreamHandlers } from './stream';
import type { Agent, Room } from './types';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  listRooms: vi.fn(),
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

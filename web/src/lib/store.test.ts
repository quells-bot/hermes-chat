import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { activity, agent, message, note as fixture, room } from './fixtures';
import { note } from './note.svelte';
import {
  addAgent,
  answer,
  apply,
  cancel,
  changeRoom,
  closeRoom,
  connect,
  disconnect,
  invite,
  load,
  loadActivity,
  loadLatest,
  loadOlder,
  loadQuoted,
  markAllRead,
  markRead,
  openRoom,
  pause,
  putRoom,
  removeAgent,
  resume,
  resumeAll,
  send,
  stopAll,
  stopTurns,
  store,
} from './store.svelte';
import { openStream, type StreamHandlers } from './stream';
import type { Activity, Agent, Message, Prompt, Room } from './types';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  listRooms: vi.fn(),
  getRoom: vi.fn(),
  listAgents: vi.fn(),
  getHalt: vi.fn(),
  getNote: vi.fn(),
  listMessages: vi.fn(),
  listActivity: vi.fn(),
  getMessage: vi.fn(),
  markRoomRead: vi.fn(),
  postMessage: vi.fn(),
  steer: vi.fn(),
  stopEverything: vi.fn(),
  resumeEverything: vi.fn(),
  answerPrompt: vi.fn(),
  cancelMessage: vi.fn(),
  stopTurn: vi.fn(),
  pauseRoom: vi.fn(),
  resumeRoom: vi.fn(),
  updateRoom: vi.fn(),
  inviteHuman: vi.fn(),
  addRoomAgent: vi.fn(),
  removeRoomAgent: vi.fn(),
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
  vi.mocked(api.getNote).mockReset().mockResolvedValue(fixture());
  vi.mocked(api.getRoom).mockReset();
  vi.mocked(api.getHalt).mockReset().mockResolvedValue(false);
  vi.mocked(api.listMessages).mockReset().mockResolvedValue([]);
  vi.mocked(api.listActivity).mockReset().mockResolvedValue([]);
  vi.mocked(api.getMessage).mockReset();
  vi.mocked(api.markRoomRead).mockReset();
  vi.mocked(api.postMessage).mockReset();
  vi.mocked(api.steer).mockReset();
  vi.mocked(api.stopEverything).mockReset();
  vi.mocked(api.resumeEverything).mockReset();
  vi.mocked(api.answerPrompt).mockReset();
  vi.mocked(api.cancelMessage).mockReset();
  vi.mocked(api.stopTurn).mockReset();
  vi.mocked(api.pauseRoom).mockReset();
  vi.mocked(api.resumeRoom).mockReset();
  vi.mocked(api.updateRoom).mockReset();
  vi.mocked(api.inviteHuman).mockReset();
  vi.mocked(api.addRoomAgent).mockReset();
  vi.mocked(api.removeRoomAgent).mockReset();
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

test('a load fetches the note', async () => {
  const held = fixture({ text: 'likes tea' });
  vi.mocked(api.getNote).mockResolvedValue(held);
  await loaded();
  await vi.waitFor(() => expect(note.value).toEqual(held));
});

test('a note that cannot be fetched does not fail the load', async () => {
  vi.mocked(api.getNote).mockRejectedValue(unreachable());
  await loaded();
  expect(store.loaded).toBe(true);
  expect(store.error).toBe('');
});

test('disconnect empties the note', () => {
  note.value = fixture({ text: 'mine' });
  disconnect();
  expect(note.value).toBeNull();
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
    latest: {},
    quoted: {},
    activity: {},
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
  await loaded([room({ id: '01R', last_read_message_id: '01M050' }), room({ id: '01S', last_read_message_id: '01M003' })]);
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

  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R', last_read_message_id: '01M050' })]);
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
  await opened('01R', run(11, 50), [room({ id: '01R', last_read_message_id: '01M060' }), room({ id: '01S' })]);
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
  await opened('01R', run(51, 50), [room({ id: '01R', last_read_message_id: '01M100' })]);
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

test('a marker inside the first page, and a page that is the whole room, bring no second request', async () => {
  await loaded([
    room({ id: '01S', last_read_message_id: '01M220' }),
    room({ id: '01T', last_read_message_id: '01M201' }), // the oldest held is read: nothing unread lies before it
    room({ id: '01U', last_read_message_id: '01M001' }), // older, but the page is the whole room
    room({ id: '01V', last_read_message_id: null }), // never read, but the page is the whole room
  ]);
  for (const id of ['01S', '01T']) {
    vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50, id));
    await openRoom(id);
  }
  for (const id of ['01U', '01V']) {
    vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 3, id));
    await openRoom(id);
  }

  expect(api.listMessages).toHaveBeenCalledTimes(4);
  expect(ids('01S')).toEqual(run(201, 50).map((m) => m.id));
});

test('a room that has never been read brings one request for the 150 before its newest page', async () => {
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockResolvedValueOnce(run(171, 30));
  await loaded([room({ id: '01R', last_read_message_id: null })]);
  await openRoom('01R');

  expect(api.listMessages).toHaveBeenCalledTimes(2);
  expect(api.listMessages).toHaveBeenNthCalledWith(2, '01R', '01M201', 150);
  expect(ids()).toEqual(run(171, 80).map((m) => m.id));
  expect(store.feeds['01R']).toMatchObject({ more: false, loading: false, error: '' });
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

test('loading stays true until the backlog settles', async () => {
  await loaded([
    room({ id: '01R', last_read_message_id: '01M040' }),
    room({ id: '01S', last_read_message_id: '01M040' }),
  ]);
  const backlog = deferred<Message[]>();
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockReturnValueOnce(backlog.promise);
  const opening = openRoom('01R');
  await vi.waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(2));
  expect(store.feeds['01R']).toEqual({ messages: run(201, 50), more: true, loading: true, error: '' });

  backlog.resolve(run(51, 150));
  await opening;
  expect(ids()).toEqual(run(51, 200).map((m) => m.id));
  expect(store.feeds['01R'].loading).toBe(false);

  // One that fails settles too.
  const failing = deferred<Message[]>();
  vi.mocked(api.listMessages)
    .mockResolvedValueOnce(run(201, 50, '01S'))
    .mockReturnValueOnce(
      failing.promise.then(() => {
        throw unreachable();
      }),
    );
  const second = openRoom('01S');
  await vi.waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(4));
  expect(store.feeds['01S']).toEqual({ messages: run(201, 50, '01S'), more: true, loading: true, error: '' });

  failing.resolve([]);
  await second;
  expect(store.feeds['01S']).toEqual({ messages: run(201, 50, '01S'), more: true, loading: false, error: '' });
});

test('loadOlder does nothing while the backlog is in flight', async () => {
  await loaded([room({ id: '01R', last_read_message_id: '01M040' })]);
  const backlog = deferred<Message[]>();
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50)).mockReturnValueOnce(backlog.promise);
  const opening = openRoom('01R');
  await vi.waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(2));

  await loadOlder('01R');
  expect(api.listMessages).toHaveBeenCalledTimes(2); // the page, and its backlog
  expect(store.feeds['01R'].loading).toBe(true);

  backlog.resolve(run(51, 150));
  await opening;
  expect(ids()).toEqual(run(51, 200).map((m) => m.id));

  // Settled, the page before it can be asked for again.
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(1, 50));
  await loadOlder('01R');
  expect(api.listMessages).toHaveBeenLastCalledWith('01R', '01M051', 50);
  expect(ids()).toEqual(run(1, 250).map((m) => m.id));
});

test('loadOlder prepends the page before the oldest held', async () => {
  await opened('01R', run(101, 50), [room({ id: '01R', last_read_message_id: '01M150' })]);

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
  await opened('01R', run(101, 50), [room({ id: '01R', last_read_message_id: '01M150' })]);
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
  await opened('01R', run(101, 50), [room({ id: '01R', last_read_message_id: '01M150' })]);
  vi.mocked(api.listMessages).mockRejectedValueOnce(unreachable());

  await expect(loadOlder('01R')).resolves.toBeUndefined();

  expect(store.feeds['01R']).toEqual({ messages: run(101, 50), more: true, loading: false, error: '' });
  expect(store.error).toBe('');
});

test('events during loadOlder are held and applied after it', async () => {
  await opened('01R', run(51, 50), [room({ id: '01R', last_read_message_id: '01M100' })]);
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
  await opened('01R', run(101, 50), [room({ id: '01R', last_read_message_id: '01M250' })]);
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
  await opened('01S', run(1, 3, '01S'), [room({ id: '01R', last_read_message_id: '01M050' }), room({ id: '01S' })]);
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
  await opened('01R', run(1, 50), [room({ id: '01R', last_read_message_id: '01M050' })]);
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

test('markRead puts the marker and then sets it on the room', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  vi.mocked(api.markRoomRead).mockResolvedValue();
  await markRead('01R', '01M2');
  expect(api.markRoomRead).toHaveBeenCalledWith('01R', '01M2');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('markRead sets the first marker of a room that had none', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' })]);
  vi.mocked(api.markRoomRead).mockResolvedValue();
  await markRead('01R', '01M2');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('markRead does nothing when the marker is already there or newer', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M3', last_read_message_id: '01M2' })]);
  await markRead('01R', '01M2');
  await markRead('01R', '01M1');
  await markRead('01X', '01M9'); // a room that is not held
  await markRead('constructor', '01M9');
  expect(api.markRoomRead).not.toHaveBeenCalled();
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('the same marker asked twice while in flight is one request', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  const put = deferred<void>();
  vi.mocked(api.markRoomRead).mockReturnValueOnce(put.promise);

  const first = markRead('01R', '01M2');
  await markRead('01R', '01M2');
  expect(api.markRoomRead).toHaveBeenCalledTimes(1);
  expect(store.rooms['01R'].last_read_message_id).toBe('01M1');

  put.resolve();
  await first;
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('a failed markRead leaves the room unread and store.error empty', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  vi.mocked(api.markRoomRead).mockRejectedValueOnce(unreachable());

  await expect(markRead('01R', '01M2')).resolves.toBeUndefined();
  expect(store.rooms['01R'].last_read_message_id).toBe('01M1');
  expect(store.error).toBe('');

  // It is no longer in flight, so the next time it is asked for it is tried again.
  vi.mocked(api.markRoomRead).mockResolvedValueOnce();
  await markRead('01R', '01M2');
  expect(api.markRoomRead).toHaveBeenCalledTimes(2);
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('markRead lets through an error that is not a failed request', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  vi.mocked(api.markRoomRead).mockRejectedValueOnce(new TypeError('a mistake'));

  await expect(markRead('01R', '01M2')).rejects.toThrow('a mistake');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M1');
  expect(store.error).toBe('');
});

test('a marker that settles after a newer one was set does not move it back', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M3', last_read_message_id: '01M1' })]);
  const slow = deferred<void>();
  vi.mocked(api.markRoomRead).mockReturnValueOnce(slow.promise).mockResolvedValueOnce();

  const older = markRead('01R', '01M2');
  await markRead('01R', '01M3');
  expect(api.markRoomRead).toHaveBeenCalledTimes(2);
  expect(store.rooms['01R'].last_read_message_id).toBe('01M3');

  slow.resolve();
  await older;
  expect(store.rooms['01R'].last_read_message_id).toBe('01M3');
});

test('a marker that settles after disconnect writes nothing, and one the room has left is dropped', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' }), room({ id: '01S', last_message_id: '01M2' })]);
  const gone = deferred<void>();
  const late = deferred<void>();
  vi.mocked(api.markRoomRead).mockReturnValueOnce(gone.promise).mockReturnValueOnce(late.promise);

  const leaving = markRead('01S', '01M2');
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R', last_message_id: '01M2' })]);
  await load(); // 01S is no longer among the rooms
  gone.resolve();
  // Without the guard this would read a room that is not there, and markRead
  // lets such an error through.
  await expect(leaving).resolves.toBeUndefined();
  expect(Object.keys(store.rooms)).toEqual(['01R']);

  const marking = markRead('01R', '01M2');
  disconnect();
  // The same room, held again by the next connection.
  await loaded([room({ id: '01R', last_message_id: '01M2' })]);
  late.resolve();
  await marking;
  expect(store.rooms['01R'].last_read_message_id).toBeNull();

  // What was in flight before the disconnect does not count as in flight now.
  vi.mocked(api.markRoomRead).mockResolvedValueOnce();
  await markRead('01R', '01M2');
  expect(api.markRoomRead).toHaveBeenCalledTimes(3);
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});

test('markAllRead marks every unread room to its newest message and skips read and archived rooms', async () => {
  await loaded([
    room({ id: '01A', last_message_id: '01M2', last_read_message_id: '01M1' }),
    room({ id: '01B', last_message_id: '01M5' }),
    room({ id: '01C', last_message_id: '01M3', last_read_message_id: '01M3' }),
    room({ id: '01D', last_message_id: '01M4', archived: true }),
    room({ id: '01E' }), // nothing was ever posted in it
  ]);
  const first = deferred<void>();
  vi.mocked(api.markRoomRead).mockReturnValueOnce(first.promise).mockResolvedValueOnce();

  const marking = markAllRead();
  // Together: the second is asked for while the first is still in flight.
  expect(vi.mocked(api.markRoomRead).mock.calls).toEqual([
    ['01A', '01M2'],
    ['01B', '01M5'],
  ]);
  first.resolve();
  await marking;

  expect(Object.values(store.rooms).map((r) => r.last_read_message_id)).toEqual(['01M2', '01M5', '01M3', null, null]);
  expect(store.error).toBe('');
});

test('markAllRead records a failure', async () => {
  await loaded([
    room({ id: '01A', last_message_id: '01M2' }),
    room({ id: '01B', last_message_id: '01M5' }),
    room({ id: '01C', last_message_id: '01M7' }),
  ]);
  vi.mocked(api.markRoomRead)
    .mockResolvedValueOnce()
    .mockRejectedValueOnce(unreachable())
    .mockRejectedValueOnce(new api.ApiError(500, 'internal', 'internal error'));

  await expect(markAllRead()).resolves.toBeUndefined();

  expect(store.error).toBe('cannot reach the server'); // the first of the two
  expect(Object.values(store.rooms).map((r) => r.last_read_message_id)).toEqual(['01M2', null, null]);
});

test('a markAllRead that fails with a 401, or after disconnect, records nothing', async () => {
  await loaded([room({ id: '01A', last_message_id: '01M2' })]);
  vi.mocked(api.markRoomRead).mockRejectedValueOnce(noSession());
  await markAllRead();
  expect(store.error).toBe('');

  let reject!: (err: Error) => void;
  vi.mocked(api.markRoomRead).mockReturnValueOnce(new Promise((_, r) => (reject = r)));
  const marking = markAllRead();
  disconnect();
  reject(unreachable());
  await marking;
  expect(store.error).toBe('');
});

test('send posts with the client id and reply, writes the response and moves the marker', async () => {
  await opened('01R', run(1, 2), [room({ id: '01R', last_message_id: '01M002', last_read_message_id: '01M001' })]);
  const mine = message({ id: '01M003', room_id: '01R', text: 'hello' });
  vi.mocked(api.postMessage).mockResolvedValue(mine);
  await send('01R', 'hello', 'queue', 'k-1', '01M002');
  expect(api.postMessage).toHaveBeenCalledWith('01R', 'hello', 'k-1', '01M002');
  expect(store.feeds['01R'].messages.at(-1)).toMatchObject({ id: '01M003' });
  expect(api.markRoomRead).toHaveBeenCalledWith('01R', '01M003');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M003');
});

test('send without a reply omits it', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', last_message_id: '01M001', last_read_message_id: '01M001' })]);
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M002', room_id: '01R', text: 'hello' }));
  await send('01R', 'hello', 'queue', 'k-1');
  expect(api.postMessage).toHaveBeenCalledWith('01R', 'hello', 'k-1', undefined);
});

test('a queued post in a room without a held feed still moves the marker', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', last_message_id: '01M001', last_read_message_id: '01M001' })]);
  delete store.feeds['01R'];
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M002', room_id: '01R', text: 'hello' }));
  await send('01R', 'hello', 'queue', 'k-1');
  expect(store.feeds['01R']).toBeUndefined();
  expect(store.latest['01R']).toMatchObject({ id: '01M002' });
  expect(api.markRoomRead).toHaveBeenCalledWith('01R', '01M002');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M002');
});

test('send steers every working agent, one message each, and marks the newest read', async () => {
  await opened('01R', run(1, 2), [
    room({
      id: '01R',
      last_read_message_id: '01M001',
      agents: [agent({ id: '01A', working: true }), agent({ id: '01B', working: true }), agent({ id: '01C' })],
    }),
  ]);
  vi.mocked(api.steer)
    .mockResolvedValueOnce(message({ id: '01M003', room_id: '01R', text: 'go' }))
    .mockResolvedValueOnce(message({ id: '01M004', room_id: '01R', text: 'go' }));
  await send('01R', 'go', 'steer', 'k-1');
  expect(vi.mocked(api.steer).mock.calls).toEqual([
    ['01R', '01A', 'go', 'k-1-01A'],
    ['01R', '01B', 'go', 'k-1-01B'],
  ]);
  expect(ids('01R')).toEqual(['01M001', '01M002', '01M003', '01M004']);
  expect(api.markRoomRead).toHaveBeenCalledWith('01R', '01M004');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M004');
});

test('send with no working agent rejects no_turn and posts nothing', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', agents: [agent({ id: '01A' })] })]);
  await expect(send('01R', 'go', 'steer', 'k-1')).rejects.toMatchObject({ status: 409, code: 'no_turn' });
  expect(api.steer).not.toHaveBeenCalled();
  expect(api.postMessage).not.toHaveBeenCalled();
});

test('a steer that fails midway keeps what landed', async () => {
  await opened('01R', run(1, 1), [
    room({ id: '01R', agents: [agent({ id: '01A', working: true }), agent({ id: '01B', working: true })] }),
  ]);
  vi.mocked(api.steer)
    .mockResolvedValueOnce(message({ id: '01M002', room_id: '01R', text: 'go' }))
    .mockRejectedValueOnce(new api.ApiError(409, 'paused', 'the room is paused'));
  await expect(send('01R', 'go', 'steer', 'k-1')).rejects.toMatchObject({ status: 409, code: 'paused' });
  expect(ids('01R')).toEqual(['01M001', '01M002']);
  expect(api.markRoomRead).not.toHaveBeenCalled();
});

test('a failed send changes nothing', async () => {
  await opened('01R', run(1, 2), [room({ id: '01R', last_message_id: '01M002', last_read_message_id: '01M001' })]);
  const failure = new api.ApiError(400, 'invalid_text', 'text must not be empty');
  vi.mocked(api.postMessage).mockRejectedValue(failure);
  await expect(send('01R', '', 'queue', 'k-1')).rejects.toBe(failure);
  expect(ids('01R')).toEqual(['01M001', '01M002']);
  expect(store.latest['01R']).toBeUndefined();
  expect(store.rooms['01R'].last_read_message_id).toBe('01M001');
  expect(api.markRoomRead).not.toHaveBeenCalled();
});

test('the response waits for a snapshot fetch in flight', async () => {
  await opened('01R', run(1, 2), [room({ id: '01R', last_read_message_id: '01M002' })]);
  const rooms = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(rooms.promise);
  const loading = load();
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M003', room_id: '01R', text: 'hello' }));
  await send('01R', 'hello', 'queue', 'k-1');
  expect(ids('01R')).toEqual(['01M001', '01M002']); // held: the snapshot is still in flight
  rooms.resolve([room({ id: '01R' })]);
  await loading;
  expect(ids('01R')).toEqual(['01M001', '01M002', '01M003']);
});

// prompt builds a Prompt whose state and answer are the interesting parts.
function prompt(overrides: Partial<Prompt> = {}): Prompt {
  return {
    kind: 'approval',
    choices: ['yes', 'no'],
    state: 'open',
    answer: null,
    answered_by: null,
    closed_at: null,
    ...overrides,
  };
}

test('answer posts the answer and writes the answered prompt into the feed', async () => {
  const asked = message({ id: '01M001', room_id: '01R', prompt: prompt({ state: 'open' }) });
  await opened('01R', [asked]);
  const answered = message({ id: '01M001', room_id: '01R', prompt: prompt({ state: 'answered', answer: 'Once' }) });
  vi.mocked(api.answerPrompt).mockResolvedValue(answered);
  await answer('01R', '01M001', 'Once');
  expect(api.answerPrompt).toHaveBeenCalledWith('01R', '01M001', 'Once');
  expect(store.feeds['01R'].messages[0].prompt?.state).toBe('answered');
});

test('answer rejects with the server\'s error and writes nothing', async () => {
  const asked = message({ id: '01M001', room_id: '01R', prompt: prompt({ state: 'open' }) });
  await opened('01R', [asked]);
  const failure = new api.ApiError(409, 'prompt_closed', 'the prompt has closed');
  vi.mocked(api.answerPrompt).mockRejectedValue(failure);
  await expect(answer('01R', '01M001', 'Once')).rejects.toBe(failure);
  expect(store.feeds['01R'].messages[0].prompt).toMatchObject({ state: 'open' });
  expect(store.error).toBe('');
});

test('cancel posts the cancel and nothing_waiting is quiet', async () => {
  await opened('01R', [message({ id: '01M001', room_id: '01R' })]);
  vi.mocked(api.cancelMessage).mockResolvedValue();
  await cancel('01R', '01M001');
  expect(api.cancelMessage).toHaveBeenCalledWith('01R', '01M001');
  expect(store.error).toBe('');

  vi.mocked(api.cancelMessage).mockRejectedValue(new api.ApiError(409, 'nothing_waiting', 'nothing is waiting'));
  await expect(cancel('01R', '01M001')).resolves.toBeUndefined();
  expect(store.error).toBe('');
});

test('a failed cancel rejects, and is not the store\'s error', async () => {
  await opened('01R', [message({ id: '01M001', room_id: '01R' })]);
  const failure = unreachable();
  vi.mocked(api.cancelMessage).mockRejectedValue(failure);
  await expect(cancel('01R', '01M001')).rejects.toBe(failure);
  expect(store.error).toBe('');
});

test('stopTurns asks each working agent once', async () => {
  await opened('01R', run(1, 1), [
    room({ id: '01R', agents: [agent({ id: '01A', working: true }), agent({ id: '01B', working: true }), agent({ id: '01C' })] }),
  ]);
  vi.mocked(api.stopTurn).mockResolvedValue();
  await stopTurns('01R');
  expect(vi.mocked(api.stopTurn).mock.calls).toEqual([
    ['01R', '01A'],
    ['01R', '01B'],
  ]);
  expect(store.error).toBe('');
});

test('stopTurns asks every agent, then rejects with the first failure', async () => {
  await opened('01R', run(1, 1), [
    room({ id: '01R', agents: [agent({ id: '01A', working: true }), agent({ id: '01B', working: true })] }),
  ]);
  const failure = unreachable();
  vi.mocked(api.stopTurn).mockRejectedValueOnce(failure).mockResolvedValueOnce();
  await expect(stopTurns('01R')).rejects.toBe(failure);
  expect(api.stopTurn).toHaveBeenCalledTimes(2);
  expect(store.error).toBe('');
});

test('a failed pause or resume rejects, and is not the store\'s error', async () => {
  await loaded([room({ id: '01R', paused: false })]);
  const failure = unreachable();
  vi.mocked(api.pauseRoom).mockRejectedValue(failure);
  vi.mocked(api.resumeRoom).mockRejectedValue(failure);
  await expect(pause('01R')).rejects.toBe(failure);
  await expect(resume('01R')).rejects.toBe(failure);
  expect(store.error).toBe('');
  expect(store.rooms['01R'].paused).toBe(false);
});

test('pause and resume write the answer\'s room as room.changed', async () => {
  // The answer comes from the server, which may not hold the newest marker.
  await loaded([room({ id: '01R', paused: false, last_message_id: '01M5', last_read_message_id: '01M5' })]);
  vi.mocked(api.pauseRoom).mockResolvedValue(room({ id: '01R', paused: true, last_message_id: '01M5' }));
  await pause('01R');
  expect(api.pauseRoom).toHaveBeenCalledWith('01R');
  expect(store.rooms['01R']).toMatchObject({ paused: true, last_read_message_id: '01M5' });

  vi.mocked(api.resumeRoom).mockResolvedValue(room({ id: '01R', paused: false, last_message_id: '01M5' }));
  await resume('01R');
  expect(api.resumeRoom).toHaveBeenCalledWith('01R');
  expect(store.rooms['01R']).toMatchObject({ paused: false, last_read_message_id: '01M5' });
});

test('a pause answer that lands while a load is in flight waits for it', async () => {
  await loaded([room({ id: '01R', paused: false })]);
  const rooms = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(rooms.promise);
  const loading = load();
  const answered = deferred<Room>();
  vi.mocked(api.pauseRoom).mockReturnValueOnce(answered.promise);
  const pausing = pause('01R');
  expect(store.rooms['01R'].paused).toBe(false);
  answered.resolve(room({ id: '01R', paused: true }));
  await pausing;
  expect(store.rooms['01R'].paused).toBe(false); // held: the snapshot is still in flight
  rooms.resolve([room({ id: '01R', paused: false })]);
  await loading;
  expect(store.rooms['01R'].paused).toBe(true);
});

test('changeRoom writes the answer as the room\u2019s event', async () => {
  await loaded([room({ id: '01R', topic: 'Old' })]);
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'New' }));
  await changeRoom('01R', { topic: 'New' });
  expect(api.updateRoom).toHaveBeenCalledWith('01R', { topic: 'New' });
  expect(store.rooms['01R'].topic).toBe('New');
});

test('addAgent writes the answer\u2019s room', async () => {
  await loaded([room({ id: '01R', agents: [] })]);
  vi.mocked(api.addRoomAgent).mockResolvedValue(room({ id: '01R', agents: [agent({ id: '01A' })] }));
  await addAgent('01R', '01A');
  expect(api.addRoomAgent).toHaveBeenCalledWith('01R', '01A');
  expect(store.rooms['01R'].agents.map((a) => a.id)).toEqual(['01A']);
});

test('removeAgent drops the agent from the room', async () => {
  await loaded([room({ id: '01R', agents: [agent({ id: '01A' }), agent({ id: '01B' })] })]);
  vi.mocked(api.removeRoomAgent).mockResolvedValue(room({ id: '01R', agents: [agent({ id: '01B' })] }));
  await removeAgent('01R', '01A');
  expect(api.removeRoomAgent).toHaveBeenCalledWith('01R', '01A');
  expect(store.rooms['01R'].agents.map((a) => a.id)).toEqual(['01B']);
});

test('invite writes the answer\u2019s room', async () => {
  await loaded([room({ id: '01R', topic: 'Old' })]);
  vi.mocked(api.inviteHuman).mockResolvedValue(room({ id: '01R', topic: 'Invited' }));
  await invite('01R', '01H');
  expect(api.inviteHuman).toHaveBeenCalledWith('01R', '01H');
  expect(store.rooms['01R'].topic).toBe('Invited');
});

test('a refused change rejects and leaves the room alone', async () => {
  await loaded([room({ id: '01R', topic: 'Old' })]);
  const failure = new api.ApiError(409, 'room_archived', 'the room is archived');
  vi.mocked(api.updateRoom).mockRejectedValue(failure);
  await expect(changeRoom('01R', { topic: 'New' })).rejects.toBe(failure);
  expect(store.rooms['01R'].topic).toBe('Old');
  expect(store.error).toBe('');
});

test('a change answered after disconnect writes nothing', async () => {
  await loaded([room({ id: '01R', topic: 'Old' })]);
  const answered = deferred<Room>();
  vi.mocked(api.updateRoom).mockReturnValueOnce(answered.promise);
  const changing = changeRoom('01R', { topic: 'New' });
  disconnect();
  answered.resolve(room({ id: '01R', topic: 'New' }));
  await changing;
  expect(store.rooms).toEqual({});
});

test('a change answered while a load is in flight is not undone by the load', async () => {
  await loaded([room({ id: '01R', topic: 'Old' })]);
  const rooms = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(rooms.promise);
  const loading = load();
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'New' }));
  await changeRoom('01R', { topic: 'New' });
  rooms.resolve([room({ id: '01R', topic: 'Old' })]);
  await loading;
  expect(store.rooms['01R'].topic).toBe('New');
});

test('a change keeps the read marker the page has put', async () => {
  await loaded([room({ id: '01R', topic: 'Old', last_read_message_id: '01M5' })]);
  vi.mocked(api.updateRoom).mockResolvedValue(
    room({ id: '01R', topic: 'New', last_read_message_id: '01M3' }),
  );
  await changeRoom('01R', { topic: 'New' });
  expect(store.rooms['01R']).toMatchObject({ topic: 'New', last_read_message_id: '01M5' });
});

test('a posted message becomes its room\'s latest, and an older one does not replace it', async () => {
  await loaded([room({ id: '01R' }), room({ id: '01S' })]);
  const second = message({ id: '01M2', room_id: '01R', text: 'second' });
  apply('message.posted', { message: second });
  expect(store.latest).toEqual({ '01R': second });
  expect(store.feeds).toEqual({}); // no feed is needed for it

  apply('message.posted', { message: message({ id: '01M1', room_id: '01R', text: 'first' }) });
  expect(store.latest['01R']).toEqual(second);

  // The same message, as it is now, replaces the one held.
  const changed = message({ id: '01M2', room_id: '01R', text: 'second, edited' });
  apply('message.edited', { message: changed });
  expect(store.latest['01R']).toEqual(changed);

  const other = message({ id: '01M1', room_id: '01S' });
  apply('delivery.changed', { message: other });
  expect(store.latest).toEqual({ '01R': changed, '01S': other });

  // One the shape check refuses is not put.
  const { text: _text, ...noText } = message({ id: '01M9', room_id: '01R' });
  apply('message.posted', { message: noText });
  expect(store.latest['01R']).toEqual(changed);
});

test('a live edit reaches latest', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R' }), room({ id: '01S' })]);
  // One in a room whose feed is held, and one in a room whose feed is not.
  apply('message.posted', { message: message({ id: '01M002', room_id: '01R', text: '' }) });
  apply('message.posted', { message: message({ id: '01M003', room_id: '01S', text: '' }) });

  apply('message.edited', { message_id: '01M002', text: 'Bottom li', final: false });
  apply('message.edited', { message_id: '01M003', text: 'Else', final: false });
  expect(store.feeds['01R'].messages[1]).toMatchObject({ text: 'Bottom li', streaming: true });
  expect(store.latest['01R'].text).toBe('Bottom li');
  expect(store.latest['01S'].text).toBe('Else');

  apply('message.edited', { message: message({ id: '01M003', room_id: '01S', text: 'Elsewhere.' }) });
  apply('message.edited', { message_id: '01M003', text: 'Else', final: false });
  expect(store.latest['01S'].text).toBe('Elsewhere.');

  // One for a message that is not the latest of any room changes none.
  apply('message.edited', { message_id: '01M001', text: 'older', final: false });
  expect(store.latest['01R'].text).toBe('Bottom li');
});

test('loadLatest fetches last_message_id once, and not at all when latest already has it', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' }), room({ id: '01S' })]);
  const newest = message({ id: '01M2', room_id: '01R', text: 'newest' });
  const answer = deferred<Message>();
  vi.mocked(api.getMessage).mockReturnValueOnce(answer.promise);

  const loading = loadLatest('01R');
  await loadLatest('01R'); // in flight
  expect(api.getMessage).toHaveBeenCalledTimes(1);
  expect(api.getMessage).toHaveBeenCalledWith('01R', '01M2');
  answer.resolve(newest);
  await loading;
  expect(store.latest).toEqual({ '01R': newest });

  await loadLatest('01R'); // latest has it
  await loadLatest('01S'); // nothing was posted in it
  await loadLatest('01X'); // not held
  expect(api.getMessage).toHaveBeenCalledTimes(1);

  // The room has a newer one now, which latest does not hold.
  store.rooms['01R'].last_message_id = '01M3';
  vi.mocked(api.getMessage).mockResolvedValueOnce(message({ id: '01M3', room_id: '01R' }));
  await loadLatest('01R');
  expect(api.getMessage).toHaveBeenLastCalledWith('01R', '01M3');
  expect(store.latest['01R'].id).toBe('01M3');
});

test('a loadLatest answer does not replace a newer message, and a failure is ignored', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' })]);
  const answer = deferred<Message>();
  vi.mocked(api.getMessage).mockReturnValueOnce(answer.promise);
  const loading = loadLatest('01R');
  const posted = message({ id: '01M3', room_id: '01R' });
  handlers.event('message.posted', { message: posted }); // not held: this is no snapshot fetch
  expect(store.latest['01R']).toEqual(posted);
  answer.resolve(message({ id: '01M2', room_id: '01R' }));
  await loading;
  expect(store.latest['01R']).toEqual(posted);

  store.rooms['01R'].last_message_id = '01M4';
  vi.mocked(api.getMessage).mockRejectedValueOnce(unreachable());
  await expect(loadLatest('01R')).resolves.toBeUndefined();
  expect(store.latest['01R']).toEqual(posted);
  expect(store.error).toBe('');

  // It is no longer in flight, so it can be asked for again.
  vi.mocked(api.getMessage).mockResolvedValueOnce(message({ id: '01M4', room_id: '01R' }));
  await loadLatest('01R');
  expect(api.getMessage).toHaveBeenCalledTimes(3);
  expect(store.latest['01R'].id).toBe('01M4');
});

test('loadQuoted fetches once, and a 404 is not asked again', async () => {
  await loaded([room({ id: '01R' })]);
  const original = message({ id: '01M1', room_id: '01R', text: 'original' });
  const answer = deferred<Message>();
  vi.mocked(api.getMessage).mockReturnValueOnce(answer.promise);

  const loading = loadQuoted('01R', '01M1');
  await loadQuoted('01R', '01M1'); // in flight
  expect(api.getMessage).toHaveBeenCalledTimes(1);
  expect(api.getMessage).toHaveBeenCalledWith('01R', '01M1');
  answer.resolve(original);
  await loading;
  expect(store.quoted).toEqual({ '01M1': original });
  await loadQuoted('01R', '01M1');
  expect(api.getMessage).toHaveBeenCalledTimes(1);

  vi.mocked(api.getMessage).mockRejectedValueOnce(new api.ApiError(404, 'not_found', 'not found'));
  await expect(loadQuoted('01R', '01M9')).resolves.toBeUndefined();
  await loadQuoted('01R', '01M9');
  expect(api.getMessage).toHaveBeenCalledTimes(2);
  expect(store.quoted).toEqual({ '01M1': original });
  expect(store.error).toBe('');
});

test('a load leaves latest and quoted alone', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' })]);
  const posted = message({ id: '01M2', room_id: '01R' });
  const original = message({ id: '01M1', room_id: '01R' });
  apply('message.posted', { message: posted });
  vi.mocked(api.getMessage).mockResolvedValueOnce(original);
  await loadQuoted('01R', '01M1');

  await load();

  expect(store.latest).toEqual({ '01R': posted });
  expect(store.quoted).toEqual({ '01M1': original });
  await loadQuoted('01R', '01M1');
  expect(api.getMessage).toHaveBeenCalledTimes(1);
});

test('disconnect empties latest and quoted', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M3' }), room({ id: '01S', last_message_id: '01M5' })]);
  apply('message.posted', { message: message({ id: '01M3', room_id: '01R' }) });
  vi.mocked(api.getMessage).mockResolvedValueOnce(message({ id: '01M1', room_id: '01R' }));
  await loadQuoted('01R', '01M1');
  expect(Object.keys(store.latest)).toEqual(['01R']);
  expect(Object.keys(store.quoted)).toEqual(['01M1']);
  const newest = deferred<Message>();
  const original = deferred<Message>();
  vi.mocked(api.getMessage).mockReturnValueOnce(newest.promise).mockReturnValueOnce(original.promise);
  const latest = loadLatest('01S');
  const quoting = loadQuoted('01R', '01M2');

  disconnect();
  expect(store.latest).toEqual({});
  expect(store.quoted).toEqual({});

  // Answers that arrive afterwards write nothing.
  newest.resolve(message({ id: '01M5', room_id: '01S' }));
  original.resolve(message({ id: '01M2', room_id: '01R' }));
  await latest;
  await quoting;
  expect(store.latest).toEqual({});
  expect(store.quoted).toEqual({});

  // The next connection asks again for what the last one had, or was fetching.
  await loaded([room({ id: '01S', last_message_id: '01M5' })]);
  vi.mocked(api.getMessage).mockResolvedValue(message({ id: '01M5', room_id: '01S' }));
  await loadLatest('01S');
  await loadQuoted('01R', '01M1');
  await loadQuoted('01R', '01M2');
  expect(api.getMessage).toHaveBeenCalledTimes(6);
});

test('a load that settles after a successful markRead does not move the marker back', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  const slow = deferred<Room[]>();
  vi.mocked(api.listRooms).mockReturnValueOnce(slow.promise);
  const loading = load();
  vi.mocked(api.markRoomRead).mockResolvedValue();
  await markRead('01R', '01M2');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');

  // The server read the room before the marker was put.
  slow.resolve([room({ id: '01R', topic: 'Renamed', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  await loading;

  expect(store.rooms['01R']).toMatchObject({ topic: 'Renamed', last_read_message_id: '01M2' });
});

test('a room.changed carrying an older marker after a successful markRead does not move it back', async () => {
  await loaded([
    room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' }),
    room({ id: '01S', last_message_id: '01M2' }),
  ]);
  vi.mocked(api.markRoomRead).mockResolvedValue();
  await markRead('01R', '01M2');
  await markRead('01S', '01M2');

  apply('room.changed', {
    room: room({ id: '01R', topic: 'Renamed', last_message_id: '01M2', last_read_message_id: '01M1' }),
  });
  // No marker at all is older than any.
  apply('room.changed', { room: room({ id: '01S', topic: 'Renamed', last_message_id: '01M2' }) });

  expect(store.rooms['01R']).toMatchObject({ topic: 'Renamed', last_read_message_id: '01M2' });
  expect(store.rooms['01S']).toMatchObject({ topic: 'Renamed', last_read_message_id: '01M2' });
});

test('an incoming marker that is newer than the held one is taken', async () => {
  await loaded([
    room({ id: '01R', last_message_id: '01M3', last_read_message_id: '01M1' }),
    room({ id: '01S', last_message_id: '01M3' }),
  ]);

  // Read on another device: by an event, and by a load.
  apply('room.changed', { room: room({ id: '01R', last_message_id: '01M3', last_read_message_id: '01M2' }) });
  apply('room.changed', { room: room({ id: '01S', last_message_id: '01M3', last_read_message_id: '01M2' }) });
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
  expect(store.rooms['01S'].last_read_message_id).toBe('01M2');

  vi.mocked(api.listRooms).mockResolvedValue([
    room({ id: '01R', last_message_id: '01M3', last_read_message_id: '01M3' }),
    room({ id: '01S', last_message_id: '01M3', last_read_message_id: '01M2' }),
  ]);
  await load();
  expect(store.rooms['01R'].last_read_message_id).toBe('01M3');
  expect(store.rooms['01S'].last_read_message_id).toBe('01M2');
});

test('a loadLatest answer read before an edit to the same message does not put the old text back', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2' })]);
  const answer = deferred<Message>();
  vi.mocked(api.getMessage).mockReturnValueOnce(answer.promise);
  const loading = loadLatest('01R');

  const edited = message({ id: '01M2', room_id: '01R', text: 'Bottom line.' });
  handlers.event('message.edited', { message: edited });
  expect(store.latest['01R']).toEqual(edited);
  answer.resolve(message({ id: '01M2', room_id: '01R', text: 'Bottom li', streaming: true }));
  await loading;

  expect(store.latest['01R']).toEqual(edited);
});

// turn makes a message in room 01R with one copy, agent 01A's, in this state.
function turn(id: string, state: 'working' | 'done'): Message {
  return message({ id, room_id: '01R', deliveries: [{ agent: agent({ id: '01A' }), state }] });
}

// running makes a tool step of a message that has not finished.
function running(id: string, messageId: string): Activity {
  return activity({ id, message_id: messageId, duration_ms: null, succeeded: null });
}

test('a tool that finishes replaces its running entry', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([running('01S1', '01M1')]);
  await opened('01R', [message({ id: '01M1', room_id: '01R', deliveries: [{ agent: agent({ id: '01A' }), state: 'working' }] })]);
  // the working copy loaded the list: one running step
  expect(api.listActivity).toHaveBeenCalledWith('01R', '01M1');
  apply('activity.posted', { message_id: '01M1', activity: activity({ id: '01S1', message_id: '01M1', duration_ms: 2100, succeeded: true }) });
  expect(store.activity['01M1']).toHaveLength(1);
  expect(store.activity['01M1'][0]).toMatchObject({ duration_ms: 2100, succeeded: true });
});

test('a replayed start does not put a finished tool back to unfinished', async () => {
  const finished = activity({ id: '01S1', message_id: '01M1', duration_ms: 2100, succeeded: false });
  vi.mocked(api.listActivity).mockResolvedValue([finished]);
  await loaded();
  await loadActivity('01R', '01M1');

  apply('activity.posted', { message_id: '01M1', activity: running('01S1', '01M1') });
  expect(store.activity['01M1']).toEqual([finished]);

  // A finished copy still replaces a finished one.
  const again = { ...finished, duration_ms: 2200, succeeded: true };
  apply('activity.posted', { message_id: '01M1', activity: again });
  expect(store.activity['01M1']).toEqual([again]);
});

test('a message that starts working loads its activity', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([running('01S1', '01M1')]);
  await opened('01R', [message({ id: '01M1', room_id: '01R' })]);
  expect(api.listActivity).not.toHaveBeenCalled();

  apply('delivery.changed', { message: turn('01M1', 'working') });
  expect(api.listActivity).toHaveBeenCalledTimes(1);
  expect(api.listActivity).toHaveBeenCalledWith('01R', '01M1');
  await vi.waitFor(() => expect(store.activity['01M1']).toEqual([running('01S1', '01M1')]));

  // The list is held now, and kept current by its own events.
  apply('message.edited', { message: turn('01M1', 'working') });
  expect(api.listActivity).toHaveBeenCalledTimes(1);
});

test('a finished message loads none until asked', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([activity({ id: '01S1', message_id: '01M1' })]);
  await opened('01R', [turn('01M1', 'done')]);
  apply('delivery.changed', { message: turn('01M1', 'done') });
  expect(api.listActivity).not.toHaveBeenCalled();
  expect(store.activity).toEqual({});

  await loadActivity('01R', '01M1');
  expect(store.activity).toEqual({ '01M1': [activity({ id: '01S1', message_id: '01M1' })] });
});

test('a working turn on an older page loads its activity', async () => {
  await opened('01R', run(51, 50), [room({ id: '01R', last_read_message_id: '01M100' })]);
  vi.mocked(api.listMessages).mockResolvedValueOnce([turn('01M050', 'working')]);
  await loadOlder('01R');
  expect(api.listActivity).toHaveBeenCalledTimes(1);
  expect(api.listActivity).toHaveBeenCalledWith('01R', '01M050');
});

test('a working turn in a room whose feed is not held loads nothing', async () => {
  await loaded([room({ id: '01R' })]);
  apply('delivery.changed', { message: turn('01M1', 'working') });
  expect(api.listActivity).not.toHaveBeenCalled();
});

test('activity.posted appends a new step', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([activity({ id: '01S1', message_id: '01M1' })]);
  await loaded();
  await loadActivity('01R', '01M1');
  const remark = activity({ id: '01S2', message_id: '01M1', kind: 'remark', text: 'two more', tool: '' });
  apply('activity.posted', { message_id: '01M1', activity: remark });
  apply('activity.posted', { message_id: '01M1', activity: remark }); // replayed
  expect(store.activity['01M1']).toEqual([activity({ id: '01S1', message_id: '01M1' }), remark]);
});

test('activity.posted for a message with no entry changes nothing', async () => {
  await opened('01R', [turn('01M1', 'done')]);
  apply('activity.posted', { message_id: '01M1', activity: activity({ id: '01S1', message_id: '01M1' }) });
  expect(store.activity).toEqual({});
  expect(api.listActivity).not.toHaveBeenCalled();
});

test('a malformed activity.posted changes nothing', async () => {
  await loaded();
  await loadActivity('01R', '01M1');
  for (const data of [
    null,
    {},
    { message_id: '01M1' },
    { message_id: '01M1', activity: 'x' },
    { message_id: '01M1', activity: {} },
    { message_id: '01M1', activity: { ...activity(), id: 7 } },
    { message_id: '01M1', activity: { ...activity(), agent: null } },
    { message_id: '01M1', activity: { ...activity(), kind: 'other' } },
    { message_id: '01M1', activity: { ...activity(), preview: null } },
    { message_id: 7, activity: activity() },
  ]) {
    apply('activity.posted', data);
  }
  expect(store.activity).toEqual({ '01M1': [] });
});

test('a step posted while the list is loading is not lost', async () => {
  await loaded();
  const list = deferred<Activity[]>();
  vi.mocked(api.listActivity).mockReturnValueOnce(list.promise);
  const loading = loadActivity('01R', '01M1');

  // Applied now it would find no entry, and the list was read before it.
  handlers.event('activity.posted', { message_id: '01M1', activity: running('01S2', '01M1') });
  expect(store.activity).toEqual({});
  list.resolve([activity({ id: '01S1', message_id: '01M1' })]);
  await loading;

  expect(store.activity['01M1'].map((a) => a.id)).toEqual(['01S1', '01S2']);
});

test('two loadActivity calls for one message are one request', async () => {
  await loaded();
  const list = deferred<Activity[]>();
  vi.mocked(api.listActivity).mockReturnValueOnce(list.promise);
  const first = loadActivity('01R', '01M1');
  await loadActivity('01R', '01M1');
  void loadActivity('01R', '01M2'); // another message is another request
  expect(api.listActivity).toHaveBeenCalledTimes(2);
  list.resolve([]);
  await first;

  // Once it has settled, the list can be asked for again, and is replaced.
  vi.mocked(api.listActivity).mockResolvedValueOnce([activity({ id: '01S1', message_id: '01M1' })]);
  await loadActivity('01R', '01M1');
  expect(api.listActivity).toHaveBeenCalledTimes(3);
  expect(store.activity['01M1']).toHaveLength(1);
});

test('a loadActivity that fails leaves the entry absent and records nothing', async () => {
  await loaded();
  vi.mocked(api.listActivity).mockRejectedValueOnce(unreachable());
  await expect(loadActivity('01R', '01M1')).resolves.toBeUndefined();
  expect(store.activity).toEqual({});
  expect(store.error).toBe('');

  // Neither the request nor the events held behind it are stuck.
  handlers.event('room.changed', { room: room({ id: '01NEW' }) });
  expect(Object.keys(store.rooms)).toEqual(['01NEW']);
  await loadActivity('01R', '01M1');
  expect(store.activity).toEqual({ '01M1': [] });
});

test('load keeps the activity of the open room and reloads it for a turn still working there', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([running('01S1', '01M001')]);
  // Older pages remain, and the load's page will not reach back to 01M1.
  const page = [turn('01M001', 'working'), turn('01M002', 'done'), ...run(3, 48)];
  await opened('01R', page);
  await loadActivity('01R', '01M002');
  await vi.waitFor(() => expect(Object.keys(store.activity)).toEqual(['01M001', '01M002']));
  const working = store.activity['01M001'];
  const done = store.activity['01M002'];

  const again = deferred<Activity[]>();
  vi.mocked(api.listActivity).mockReturnValueOnce(again.promise);
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(3, 50));
  const loading = load();
  await vi.waitFor(() => expect(api.listActivity).toHaveBeenCalledTimes(3));
  // Only the working turn is asked for again, and both lists stay meanwhile.
  expect(api.listActivity).toHaveBeenLastCalledWith('01R', '01M001');
  expect(store.activity).toEqual({ '01M001': working, '01M002': done });

  again.resolve([running('01S1', '01M001'), running('01S2', '01M001')]);
  await loading;
  await vi.waitFor(() => expect(store.activity['01M001']).toHaveLength(2));
  expect(store.activity['01M002']).toEqual(done);
  expect(api.listActivity).toHaveBeenCalledTimes(3);
});

test('load drops the activity of messages that are not in the open room\'s feed', async () => {
  await opened('01R', run(1, 50), [room({ id: '01R', last_read_message_id: '01M050' })]);
  await loadActivity('01R', '01M001');
  await loadActivity('01R', '01M050');
  await loadActivity('01S', '01X1'); // another room's
  expect(Object.keys(store.activity)).toEqual(['01M001', '01M050', '01X1']);

  // A page that merges keeps both of the room's messages, and drops the other room's.
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(40, 50));
  await load();
  expect(Object.keys(store.activity)).toEqual(['01M001', '01M050']);

  // A page that is wholly newer replaces the feed, and takes the rest with it.
  vi.mocked(api.listMessages).mockResolvedValueOnce(run(201, 50));
  await load();
  expect(store.activity).toEqual({});

  closeRoom('01R');
  await load();
  expect(store.activity).toEqual({});
});

test('disconnect empties activity', async () => {
  await loaded();
  await loadActivity('01R', '01M1');
  const list = deferred<Activity[]>();
  vi.mocked(api.listActivity).mockReturnValueOnce(list.promise);
  const loading = loadActivity('01R', '01M2');

  disconnect();
  expect(store.activity).toEqual({});
  list.resolve([activity({ id: '01S1', message_id: '01M2' })]);
  await loading;
  expect(store.activity).toEqual({});

  // The request in flight belonged to the old connection: the new one asks.
  connect();
  await loadActivity('01R', '01M2');
  expect(api.listActivity).toHaveBeenCalledTimes(3);
  expect(store.activity).toEqual({ '01M2': [] });
});

test('a post answer that lands after its message started work does not put it back to waiting', async () => {
  const bot = agent({ id: '01A' });
  await opened('01R', run(1, 1), [room({ id: '01R', agents: [bot] })]);
  const answered = deferred<Message>();
  vi.mocked(api.postMessage).mockReturnValueOnce(answered.promise);
  const sending = send('01R', 'hello', 'queue', 'k-1');
  // The stream is ahead of the answer: posted, then picked up.
  const working = message({ id: '01M002', room_id: '01R', text: 'hello', deliveries: [{ agent: bot, state: 'working' }] });
  apply('message.posted', { message: { ...working, deliveries: [{ agent: bot, state: 'waiting' }] } });
  apply('delivery.changed', { message: working });
  answered.resolve(message({ id: '01M002', room_id: '01R', text: 'hello', deliveries: [{ agent: bot, state: 'waiting' }] }));
  await sending;
  expect(store.feeds['01R'].messages.at(-1)?.deliveries[0].state).toBe('working');
  expect(store.latest['01R'].deliveries[0].state).toBe('working');
  expect(ids('01R')).toEqual(['01M001', '01M002']);
});

test('a write that answers after disconnect() writes nothing', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', agents: [agent({ id: '01A', working: true })] })]);
  const posted = deferred<Message>();
  const paused = deferred<Room>();
  const prompted = deferred<Message>();
  vi.mocked(api.postMessage).mockReturnValueOnce(posted.promise);
  vi.mocked(api.pauseRoom).mockReturnValueOnce(paused.promise);
  vi.mocked(api.answerPrompt).mockReturnValueOnce(prompted.promise);
  const writes = [send('01R', 'hello', 'queue', 'k-1'), pause('01R'), answer('01R', '01M001', 'yes')];
  disconnect();
  posted.resolve(message({ id: '01M002', room_id: '01R', text: 'hello' }));
  paused.resolve(room({ id: '01R', paused: true }));
  prompted.resolve(message({ id: '01M001', room_id: '01R' }));
  await Promise.all(writes);
  expect(store.rooms).toEqual({});
  expect(store.feeds).toEqual({});
  expect(store.latest).toEqual({});
  expect(api.markRoomRead).not.toHaveBeenCalled();
});

test('a steer that fails midway says who was steered and who was not', async () => {
  await opened('01R', run(1, 1), [
    room({
      id: '01R',
      agents: [
        agent({ id: '01A', display_name: 'Atlas', working: true }),
        agent({ id: '01B', display_name: 'Bolt', working: true }),
      ],
    }),
  ]);
  vi.mocked(api.steer)
    .mockResolvedValueOnce(message({ id: '01M002', room_id: '01R', text: 'go' }))
    .mockRejectedValueOnce(new api.ApiError(409, 'no_turn', 'no turn is running'));
  await expect(send('01R', 'go', 'steer', 'k-1')).rejects.toMatchObject({
    status: 409,
    code: 'no_turn',
    message: 'Atlas was steered, Bolt was not: no turn is running',
  });
});

test('a steer whose first agent refuses says only why', async () => {
  await opened('01R', run(1, 1), [room({ id: '01R', agents: [agent({ id: '01A', working: true })] })]);
  vi.mocked(api.steer).mockRejectedValueOnce(new api.ApiError(409, 'no_turn', 'no turn is running'));
  await expect(send('01R', 'go', 'steer', 'k-1')).rejects.toMatchObject({ message: 'no turn is running' });
});

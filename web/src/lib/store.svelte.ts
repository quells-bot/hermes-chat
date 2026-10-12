// What the server holds, as far as the screens show it. Screens read `store`;
// it is loaded each time the event stream reaches `ready` and kept current by
// the events after it. Nothing else holds this state.

import { ApiError, getHalt, listAgents, listRooms, resumeEverything, stopEverything } from './api';
import { openStream, type Stream, type StreamState } from './stream';
import type { Agent, Room } from './types';

type Store = {
  link: StreamState; // the stream's state
  loaded: boolean; // a load has succeeded since connect()
  error: string; // the last failed load, stop or resume; '' when none
  rooms: Record<string, Room>; // by id, archived ones included
  agents: Record<string, Agent>; // the directory, by id
  halted: boolean;
};

function initial(): Store {
  return { link: 'connecting', loaded: false, error: '', rooms: {}, agents: {}, halted: false };
}

export const store: Store = $state(initial());

let stream: Stream | null = null;

// loads numbers the calls to load(), so that only the newest one writes.
let loads = 0;

// loading is true from the start of a load until the newest one settles.
// Events that arrive meanwhile are held, in order, and applied after it: the
// snapshot replaces everything, so one applied before it would be lost.
let loading = false;
let held: [name: string, data: unknown][] = [];

// resets counts the calls to disconnect(), so that a stop or resume that
// settles after one does not write into the emptied store.
let resets = 0;

// A hidden page may have missed a read marker moving, which no event
// announces, and its stream may be waiting out a long reconnect delay.
function visibilityChanged(): void {
  if (document.visibilityState !== 'visible') {
    return;
  }
  stream?.kick();
  // Otherwise the coming ready loads.
  if (store.link === 'live') {
    void load();
  }
}

// connect opens the stream. A second call does nothing.
export function connect(): void {
  if (stream !== null) {
    return;
  }
  stream = openStream({
    ready: () => void load(),
    event: receive,
    state: (s) => {
      store.link = s;
    },
  });
  document.addEventListener('visibilitychange', visibilityChanged);
}

// disconnect closes the stream and empties the store. The store object stays
// the same one, since components hold it.
export function disconnect(): void {
  document.removeEventListener('visibilitychange', visibilityChanged);
  stream?.close();
  stream = null;
  loads += 1; // a load still in flight must not write into the empty store
  loading = false;
  held = [];
  resets += 1;
  Object.assign(store, initial());
}

function byId<T extends { id: string }>(items: T[]): Record<string, T> {
  const out: Record<string, T> = {};
  for (const item of items) {
    out[item.id] = item;
  }
  return out;
}

// failed records why a request failed. A 401 is not recorded: the API layer
// has already signed the person out.
function failed(err: unknown): void {
  if (err instanceof ApiError && err.status === 401) {
    return;
  }
  store.error = err instanceof Error ? err.message : String(err);
}

// load replaces the rooms, the agents and the halt with the server's, so a
// room the person has left disappears. A load that settles after a later one
// started is discarded. It never rejects.
export async function load(): Promise<void> {
  loads += 1;
  const seq = loads;
  loading = true;
  try {
    const [rooms, agents, halted] = await Promise.all([listRooms(), listAgents(), getHalt()]);
    if (seq !== loads) {
      return;
    }
    store.rooms = byId(rooms);
    store.agents = byId(agents);
    store.halted = halted;
    store.error = '';
    store.loaded = true;
  } catch (err) {
    if (seq !== loads) {
      return;
    }
    failed(err);
  }
  // The newest load has settled, well or badly. The held events carry the
  // state as it was when each was sent, so applying them over a snapshot that
  // already includes them changes nothing.
  loading = false;
  const events = held;
  held = [];
  for (const [name, data] of events) {
    apply(name, data);
  }
}

// receive takes an event from the stream: held while a load is in flight,
// applied at once otherwise.
function receive(name: string, data: unknown): void {
  if (loading) {
    held.push([name, data]);
    return;
  }
  apply(name, data);
}

// putRoom inserts the room, or replaces the one held under its id.
export function putRoom(room: Room): void {
  store.rooms[room.id] = room;
}

// Event data comes off the wire as unknown. The checks below cover what apply
// reads and what the store later iterates, not every field.

type Fields = Record<string, unknown>;

function isObject(v: unknown): v is Fields {
  return typeof v === 'object' && v !== null;
}

// field returns data[name] when it is an object, and null otherwise.
function field(data: unknown, name: string): Fields | null {
  if (!isObject(data)) {
    return null;
  }
  const v = data[name];
  return isObject(v) ? v : null;
}

// heldRoom returns the room an event's message is in, or null when the
// message names none or the store does not hold it.
function heldRoom(message: Fields | null): Room | null {
  const id = message?.room_id;
  if (typeof id !== 'string' || !Object.hasOwn(store.rooms, id)) {
    return null;
  }
  return store.rooms[id];
}

// apply patches the store with one event from the stream. An event it does
// not know, one for a room it does not hold, and data of the wrong shape all
// change nothing.
export function apply(name: string, data: unknown): void {
  switch (name) {
    case 'room.changed': {
      const room = field(data, 'room');
      if (room === null || typeof room.id !== 'string' || !Array.isArray(room.agents)) {
        return;
      }
      putRoom(room as Room);
      if (typeof room.halted === 'boolean') {
        store.halted = room.halted;
      }
      return;
    }
    case 'message.posted': {
      const message = field(data, 'message');
      const room = heldRoom(message);
      const id = message?.id;
      if (room === null || typeof id !== 'string') {
        return;
      }
      // ULIDs sort by time, so a replayed older message does not move it back.
      if (room.last_message_id === null || room.last_message_id < id) {
        room.last_message_id = id;
      }
      return;
    }
    case 'delivery.changed': {
      const message = field(data, 'message');
      const room = heldRoom(message);
      if (room === null || !Array.isArray(message?.deliveries)) {
        return;
      }
      for (const delivery of message.deliveries as unknown[]) {
        const id = field(delivery, 'agent')?.id;
        const state = isObject(delivery) ? delivery.state : undefined;
        if (state !== 'working' && state !== 'done' && state !== 'failed') {
          continue; // waiting, delivered and cancelled say nothing about working
        }
        for (const a of room.agents) {
          if (a.id === id) {
            a.working = state === 'working';
          }
        }
      }
      return;
    }
    case 'agent.presence': {
      const agent = field(data, 'agent');
      const connected = isObject(data) ? data.connected : undefined;
      if (agent === null || typeof agent.id !== 'string' || typeof connected !== 'boolean') {
        return;
      }
      store.agents[agent.id] = agent as Agent;
      for (const room of Object.values(store.rooms)) {
        for (const a of room.agents) {
          if (a.id === agent.id) {
            a.connected = connected;
          }
        }
      }
      return;
    }
  }
}

// stopAll stops every room. It never rejects; a failure leaves halted as it was.
export async function stopAll(): Promise<void> {
  await setHalt(stopEverything);
}

// resumeAll undoes stopAll. It never rejects; a failure leaves halted as it was.
export async function resumeAll(): Promise<void> {
  await setHalt(resumeEverything);
}

// setHalt makes the call and sets halted from its answer. An answer or a
// failure that arrives after disconnect() is dropped.
async function setHalt(call: () => Promise<boolean>): Promise<void> {
  const epoch = resets;
  try {
    const halted = await call();
    if (epoch === resets) {
      store.halted = halted;
    }
  } catch (err) {
    if (epoch === resets) {
      failed(err);
    }
  }
}

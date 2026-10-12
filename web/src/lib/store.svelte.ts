// What the server holds, as far as the screens show it. Screens read `store`;
// it is loaded each time the event stream reaches `ready` and kept current by
// the events after it. Nothing else holds this state.

import { ApiError, getHalt, getRoom, listAgents, listRooms, resumeEverything, stopEverything } from './api';
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

// workingOn holds, by room id and then agent id, the message whose
// delivery.changed last set working for that agent in that room. A room's
// queue is per agent, so a finished copy of an earlier message says nothing
// about an agent that has since started a later one.
let workingOn = new Map<string, Map<string, string>>();

// refetching holds the rooms being fetched again, each with the value of
// loads it started under. Once loads has moved on, that fetch will write
// nothing and no longer counts as in flight.
const refetching = new Map<string, number>();

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
  loads += 1; // a load or refetch still in flight must not write into the empty store
  loading = false;
  held = [];
  workingOn = new Map();
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
    workingOn = new Map();
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
  // The newest load has settled, well or badly. The held events are applied
  // in order and each carries the state as it was when it was sent, so the
  // last one for a room wins. A held one can be older than the snapshot and
  // briefly put back that older state, until the next event or load.
  loading = false;
  const events = held;
  held = [];
  for (const [name, data] of events) {
    // One that throws must not take the ones behind it, or the load, with it.
    try {
      apply(name, data);
    } catch (err) {
      console.error('a held event could not be applied', name, err);
    }
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

// putRoom inserts the room, or replaces the one held under its id. Its agents
// come with it, so what set working for the old ones is forgotten.
export function putRoom(room: Room): void {
  store.rooms[room.id] = room;
  workingOn.delete(room.id);
}

// refetch replaces one room with the server's, for when an event cannot say
// whether an agent is still working. One fetch per room at a time; a second
// request meanwhile is dropped. A failure is ignored, since the next load
// heals it, and an answer that arrives after disconnect() or after a newer
// load() started is dropped.
async function refetch(id: string): Promise<void> {
  const seq = loads;
  if (refetching.get(id) === seq) {
    return;
  }
  refetching.set(id, seq);
  try {
    const room = await getRoom(id);
    if (seq === loads) {
      putRoom(room);
    }
  } catch {
    // ignored
  } finally {
    if (refetching.get(id) === seq) {
      refetching.delete(id);
    }
  }
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
      const mid = message?.id;
      if (room === null || typeof mid !== 'string' || !Array.isArray(message?.deliveries)) {
        return;
      }
      // The event carries every agent's copy of this one message, as it is
      // now. An agent may be on a later message, which this event does not show.
      for (const delivery of message.deliveries as unknown[]) {
        const id = field(delivery, 'agent')?.id;
        const state = isObject(delivery) ? delivery.state : undefined;
        if (state !== 'working' && state !== 'done' && state !== 'failed') {
          continue; // waiting, delivered and cancelled say nothing about working
        }
        for (const a of room.agents) {
          if (!isObject(a) || a.id !== id) {
            continue; // a room off the wire may hold anything in agents
          }
          const set = workingOn.get(room.id)?.get(a.id);
          if (state === 'working') {
            a.working = true;
            if (!workingOn.has(room.id)) {
              workingOn.set(room.id, new Map());
            }
            workingOn.get(room.id)?.set(a.id, mid);
          } else if (set !== undefined) {
            // ULIDs sort by time: a copy of an earlier message changes nothing.
            if (mid >= set) {
              a.working = false;
              workingOn.get(room.id)?.delete(a.id);
            }
          } else if (a.working) {
            // The flag came from a snapshot or a room.changed, which do not
            // say for which message. Ask rather than guess.
            void refetch(room.id);
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
          if (isObject(a) && a.id === agent.id) {
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

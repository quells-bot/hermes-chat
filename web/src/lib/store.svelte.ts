// What the server holds, as far as the screens show it. Screens read `store`;
// it is loaded each time the event stream reaches `ready` and kept current by
// the events after it. Nothing else holds this state.

import {
  ApiError,
  getHalt,
  getRoom,
  listAgents,
  listMessages,
  listRooms,
  resumeEverything,
  stopEverything,
} from './api';
import { mergeNewest, pageSize, upsert } from './feed';
import { openStream, type Stream, type StreamState } from './stream';
import type { Agent, Message, Room } from './types';

// The messages held for one room: its newest ones, and the pages before them
// that have been asked for.
export type Feed = {
  messages: Message[]; // ascending by id, each once
  more: boolean; // the server may hold older ones
  loading: boolean; // a page is being fetched
  error: string; // why the first page failed; '' when none
};

type Store = {
  link: StreamState; // the stream's state
  loaded: boolean; // a load has succeeded since connect()
  error: string; // the last failed load, stop or resume; '' when none
  rooms: Record<string, Room>; // by id, archived ones included
  agents: Record<string, Agent>; // the directory, by id
  halted: boolean;
  open: string | null; // the room on screen
  feeds: Record<string, Feed>; // by room id
};

function initial(): Store {
  return {
    link: 'connecting',
    loaded: false,
    error: '',
    rooms: {},
    agents: {},
    halted: false,
    open: null,
    feeds: {},
  };
}

// How many messages are asked for when a room's read marker lies before its
// newest page, so that the first unread one is usually among those held.
const backlogSize = 150;

export const store: Store = $state(initial());

let stream: Stream | null = null;

// loads numbers the calls to load(), so that only the newest one writes.
let loads = 0;

// fetching counts the snapshot fetches in flight: loads, and pages of
// messages. Events that arrive while it is above zero are held, in order, and
// applied when it returns to zero. A snapshot replaces what it covers, so an
// event applied before it would be lost; and an event for a message on a page
// still being fetched has nowhere to go until the page is written.
let fetching = 0;
let held: [name: string, data: unknown][] = [];

// resets counts the calls to disconnect(), so that a stop or resume that
// settles after one does not write into the emptied store.
let resets = 0;

// workingOn holds, by room id and then agent id, the message whose
// delivery.changed last set working for that agent in that room. A room's
// queue is per agent, so a finished copy of an earlier message says nothing
// about an agent that has since started a later one.
let workingOn = new Map<string, Map<string, string>>();

// finalized holds the ids of the messages whose logged message.edited has
// been seen. A live edit can arrive after it, and must not put back the
// unfinished text.
let finalized = new Set<string>();

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
  fetching = 0;
  held = [];
  workingOn = new Map();
  finalized = new Set();
  resets += 1; // a fetch still in flight must neither write nor be counted when it settles
  Object.assign(store, initial());
}

function byId<T extends { id: string }>(items: T[]): Record<string, T> {
  const out: Record<string, T> = {};
  for (const item of items) {
    out[item.id] = item;
  }
  return out;
}

// reason says why a request failed, or '' for a 401, which is not recorded:
// the API layer has already signed the person out.
function reason(err: unknown): string {
  if (err instanceof ApiError && err.status === 401) {
    return '';
  }
  return err instanceof Error ? err.message : String(err);
}

// failed records why a load, a stop or a resume failed.
function failed(err: unknown): void {
  const why = reason(err);
  if (why !== '') {
    store.error = why;
  }
}

// heldFeed returns a room's feed, or null when there is none or its first
// page failed. A failed feed is only the reason: it is not held. Events are
// not put into it, and the page that arrives for it is written as a first one.
// Messages put into it meanwhile would make that page look like one that
// joins what was already there, and more would never be set from it.
function heldFeed(id: string): Feed | null {
  if (!Object.hasOwn(store.feeds, id) || store.feeds[id].error !== '') {
    return null;
  }
  return store.feeds[id];
}

// fetchStarted counts one more snapshot fetch in flight. What it returns is
// handed back to fetchSettled.
function fetchStarted(): number {
  fetching += 1;
  return resets;
}

// fetchSettled is called when a snapshot fetch has settled, well or badly,
// after its answer is written. A fetch that needs another one to finish its
// work (a page, then the backlog before it) starts that one first, so the
// count does not pass through zero between them.
function fetchSettled(epoch: number): void {
  if (epoch !== resets) {
    return; // disconnect() zeroed the count this fetch was in
  }
  fetching -= 1;
  if (fetching > 0) {
    return;
  }
  // Nothing is in flight. The held events are applied in order and each
  // carries the state as it was when it was sent, so the last one for a room
  // or a message wins. A held one can be older than the snapshot and briefly
  // put back that older state, until the next event or load.
  const events = held;
  held = [];
  for (const [name, data] of events) {
    // One that throws must not take the ones behind it, or the fetch, with it.
    try {
      apply(name, data);
    } catch (err) {
      console.error('a held event could not be applied', name, err);
    }
  }
}

// load replaces the rooms, the agents and the halt with the server's, so a
// room the person has left disappears, and brings the open room's newest
// messages. A load that settles after a later one started is discarded. It
// never rejects.
export async function load(): Promise<void> {
  loads += 1;
  const seq = loads;
  const epoch = fetchStarted();
  const asked = store.open;
  let rest: Promise<void> | null = null;
  try {
    const [rooms, agents, halted, page] = await Promise.all([
      listRooms(),
      listAgents(),
      getHalt(),
      // A page that fails must not fail the load: it becomes the reason.
      asked === null ? null : listMessages(asked).catch(reason),
    ]);
    if (seq === loads) {
      store.rooms = byId(rooms);
      workingOn = new Map();
      store.agents = byId(agents);
      store.halted = halted;
      store.error = '';
      store.loaded = true;
      rest = loadedPage(asked, page);
    }
  } catch (err) {
    if (seq === loads) {
      failed(err);
    }
  }
  // A superseded load is counted too, so held events wait for the newest.
  fetchSettled(epoch);
  await rest;
}

// loadedPage is the feeds' part of a load: only the open room's is kept, and
// the page load() asked for, or the reason there is none, is written to it.
// What it returns settles when the fetch it started, if any, has.
function loadedPage(asked: string | null, page: Message[] | string | null): Promise<void> | null {
  const id = store.open;
  for (const key of Object.keys(store.feeds)) {
    if (key !== id) {
      delete store.feeds[key];
    }
  }
  if (id === null) {
    return null;
  }
  const feed = heldFeed(id);
  if (id !== asked || page === null) {
    // The person moved on while the load was in flight, so the page is
    // another room's. If openRoom did not fetch this one's, because nothing
    // was loaded yet, nothing else will; and one whose page failed is due
    // another try.
    return feed === null ? newest(id) : null;
  }
  if (typeof page === 'string') {
    // A held feed is still good; without one, the room has only the failure,
    // and the newest reason for it.
    if (feed === null) {
      store.feeds[id] = { messages: [], more: false, loading: false, error: page };
    }
    return null;
  }
  if (feed === null) {
    store.feeds[id] = { messages: page, more: page.length === pageSize, loading: false, error: '' };
    return backlog(id);
  }
  const merged = mergeNewest(feed.messages, page);
  feed.messages = merged.messages;
  if (!merged.replaced) {
    return null; // what was held reaches the page: more, and the backlog, are as they were
  }
  feed.more = page.length === pageSize;
  return backlog(id);
}

// newest replaces a room's feed with its newest page, then applies the
// backlog rule. A failure is recorded on the feed. It never rejects.
async function newest(id: string): Promise<void> {
  const epoch = fetchStarted();
  store.feeds[id] = { messages: [], more: false, loading: true, error: '' };
  let rest: Promise<void> | null = null;
  try {
    const page = await listMessages(id);
    if (epoch === resets) {
      store.feeds[id] = { messages: page, more: page.length === pageSize, loading: false, error: '' };
      rest = backlog(id);
    }
  } catch (err) {
    if (epoch === resets) {
      store.feeds[id] = { messages: [], more: false, loading: false, error: reason(err) };
    }
  }
  fetchSettled(epoch);
  await rest;
}

// backlog runs when a newest page has just become a room's feed. If the read
// marker is older than everything on it and older messages exist, it starts
// one fetch for those before it, so that opening a room with more unread than
// one page rarely leaves the first unread message out of reach.
function backlog(id: string): Promise<void> | null {
  const feed = store.feeds[id];
  const marker = Object.hasOwn(store.rooms, id) ? store.rooms[id].last_read_message_id : null;
  if (!feed.more || feed.messages.length === 0 || marker === null || marker >= feed.messages[0].id) {
    return null;
  }
  return older(id, backlogSize);
}

// older fetches the messages before the oldest held and prepends them. The
// answer is dropped unless the feed still starts where it did: one that was
// replaced meanwhile would get a page that does not join it. A failure is
// ignored and leaves more true, so it can be asked for again. It never rejects.
async function older(id: string, limit: number): Promise<void> {
  const before = store.feeds[id].messages[0].id;
  const epoch = fetchStarted();
  try {
    const page = await listMessages(id, before, limit);
    const feed = epoch === resets ? heldFeed(id) : null;
    if (feed !== null && feed.messages.length > 0 && feed.messages[0].id === before) {
      feed.messages.unshift(...page);
      feed.more = page.length === limit;
    }
  } catch {
    // ignored
  }
  fetchSettled(epoch);
}

// openRoom says which room is on screen and makes sure its feed is held, or
// being fetched. It never rejects.
export async function openRoom(id: string): Promise<void> {
  store.open = id;
  if (!store.loaded) {
    return; // the coming load fetches the page
  }
  if (heldFeed(id) !== null) {
    return;
  }
  await newest(id);
}

// closeRoom says the room is no longer on screen. Its feed stays until the
// next load, so going straight back to it fetches nothing.
export function closeRoom(id: string): void {
  if (store.open === id) {
    store.open = null;
  }
}

// loadOlder brings the page before the oldest held message of a room. One at
// a time per room. It never rejects.
export async function loadOlder(id: string): Promise<void> {
  const feed = heldFeed(id);
  if (feed === null || !feed.more || feed.loading || feed.messages.length === 0) {
    return;
  }
  feed.loading = true;
  await older(id, pageSize);
  feed.loading = false;
}

// receive takes an event from the stream: held while a snapshot fetch is in
// flight, applied at once otherwise.
function receive(name: string, data: unknown): void {
  if (fetching > 0) {
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
// change nothing. The rooms come first: they need less of a message than the
// feeds do, and take what they can from one the feeds will not hold.
export function apply(name: string, data: unknown): void {
  applyToRooms(name, data);
  applyToFeeds(name, data);
}

// isMessage checks what the feeds and the screens that draw them rely on.
function isMessage(v: Fields): v is Fields & Message {
  return (
    typeof v.id === 'string' &&
    typeof v.room_id === 'string' &&
    typeof v.text === 'string' &&
    typeof v.created_at === 'string' &&
    Array.isArray(v.deliveries) &&
    Array.isArray(v.attachments)
  );
}

// applyToFeeds keeps the held messages current.
function applyToFeeds(name: string, data: unknown): void {
  switch (name) {
    case 'message.posted':
    case 'delivery.changed':
    case 'prompt.changed':
    case 'message.edited': {
      const message = field(data, 'message');
      if (message === null) {
        if (name === 'message.edited') {
          liveEdit(data);
        }
        return;
      }
      if (!isMessage(message)) {
        return;
      }
      if (name === 'message.edited') {
        // Recorded whether or not the message is held: an older page may
        // bring it later, and a late live edit must still be ignored.
        finalized.add(message.id);
      }
      const feed = heldFeed(message.room_id);
      if (feed === null) {
        return;
      }
      // One older than everything held, while older pages remain, belongs to
      // a page not yet loaded. Inserted, it would become the oldest held, and
      // loadOlder would skip what lies between.
      if (feed.more && feed.messages.length > 0 && message.id < feed.messages[0].id) {
        return;
      }
      upsert(feed.messages, message);
      return;
    }
  }
}

// liveEdit shows the text an agent is still writing. These frames are not
// logged, so one can arrive after the logged edit that finalized the message.
function liveEdit(data: unknown): void {
  if (!isObject(data) || typeof data.message_id !== 'string' || typeof data.text !== 'string') {
    return;
  }
  if (finalized.has(data.message_id)) {
    return;
  }
  for (const feed of Object.values(store.feeds)) {
    const message = feed.messages.find((m) => m.id === data.message_id);
    if (message !== undefined) {
      message.text = data.text;
      message.streaming = true;
      return;
    }
  }
}

// applyToRooms keeps the rooms, the directory and the halt current.
function applyToRooms(name: string, data: unknown): void {
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

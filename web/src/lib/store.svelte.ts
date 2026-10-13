// What the server holds, as far as the screens show it. Screens read `store`;
// it is loaded each time the event stream reaches `ready` and kept current by
// the events after it. Nothing else holds this state.

import {
  ApiError,
  answerPrompt,
  cancelMessage,
  getHalt,
  getMessage,
  getRoom,
  listActivity,
  listAgents,
  listMessages,
  listRooms,
  markRoomRead,
  pauseRoom,
  postMessage,
  resumeEverything,
  resumeRoom,
  steer,
  stopEverything,
  stopTurn,
} from './api';
import { mergeNewest, pageSize, upsert } from './feed';
import { isUnread } from './rooms';
import { openStream, type Stream, type StreamState } from './stream';
import type { Activity, Agent, Message, Room } from './types';

// The messages held for one room: its newest ones, and the pages before them
// that have been asked for.
export type Feed = {
  messages: Message[]; // ascending by id, each once
  more: boolean; // the server may hold older ones
  loading: boolean; // a page is being fetched: the first, its backlog, or an older one
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
  latest: Record<string, Message>; // by room id: the newest message known
  quoted: Record<string, Message>; // by message id: fetched because a reply names it
  activity: Record<string, Activity[]>; // by message id, oldest first; absent until loaded
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
    latest: {},
    quoted: {},
    activity: {},
  };
}

// How many messages are asked for when a room's read marker lies before its
// newest page, so that the first unread one is usually among those held.
const backlogSize = 150;

export const store: Store = $state(initial());

let stream: Stream | null = null;

// loads numbers the calls to load(), so that only the newest one writes.
let loads = 0;

// fetching counts the snapshot fetches in flight: loads, pages of messages,
// and lists of activity. Events that arrive while it is above zero are held, in order, and
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

// marking holds the read markers being put, and fetchingLatest the newest
// messages being fetched, each as a room id and a message id. The same one is
// not asked for again while it is in flight.
let marking = new Set<string>();
let fetchingLatest = new Set<string>();

// askedQuoted holds the ids of the quoted messages that have been asked for,
// whatever the answer was. One that failed is not asked for again: a reply
// can name a message that no longer exists, and every drawing of it would ask.
let askedQuoted = new Set<string>();

// loadingActivity holds the ids of the messages whose activity is being
// fetched.
let loadingActivity = new Set<string>();

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
  marking = new Set();
  fetchingLatest = new Set();
  askedQuoted = new Set();
  loadingActivity = new Set();
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
// leaves latest and quoted alone: the events replayed after it keep them
// current, and an entry is only shown while its id still matches. The lists
// of activity are kept for the messages still in the open room's feed, and
// asked for again for its working turns (keepActivity). It never rejects.
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
      rooms.forEach(keepMarker);
      store.rooms = byId(rooms);
      workingOn = new Map();
      store.agents = byId(agents);
      store.halted = halted;
      store.error = '';
      store.loaded = true;
      rest = loadedPage(asked, page);
      keepActivity();
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

// keepActivity is the activity lists' part of a load. The lists of messages
// that are no longer in the open room's feed are dropped. The others stay, so
// a block that is open keeps its list on screen; but the list of a turn still
// working may have missed steps while the stream was down, so it is fetched
// again, and the answer replaces it when it lands. A feed that newest() is
// fetching is empty here, and is looked at when its page is written.
function keepActivity(): void {
  const feed = store.open === null ? null : heldFeed(store.open);
  const messages = feed === null ? [] : feed.messages;
  const inFeed = new Set(messages.map((m) => m.id));
  for (const id of Object.keys(store.activity)) {
    if (!inFeed.has(id)) {
      delete store.activity[id];
    }
  }
  for (const message of messages) {
    if (isWorking(message)) {
      void loadActivity(message.room_id, message.id);
    }
  }
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
      followWorking(page);
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
// marker is older than everything on it, or the room has never been read, and
// older messages exist, it starts one fetch for those before it, so that
// opening a room with more unread than one page rarely leaves the first
// unread message out of reach. The feed stays loading until that fetch
// settles, well or badly: the screen waits for it before it says where the
// unread messages start, and loadOlder asks for nothing meanwhile.
function backlog(id: string): Promise<void> | null {
  const feed = store.feeds[id];
  const marker = Object.hasOwn(store.rooms, id) ? store.rooms[id].last_read_message_id : null;
  if (!feed.more || feed.messages.length === 0 || (marker !== null && marker >= feed.messages[0].id)) {
    return null;
  }
  feed.loading = true;
  return older(id, backlogSize).then(() => {
    feed.loading = false;
  });
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
      followWorking(page);
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

// markRead tells the server that the person has read a room up to a message.
// A request that fails changes and records nothing: the marker is asked for
// again the next time the end of the feed is seen. It rejects only with an
// error that is not a failed request, which is a mistake in the code.
export async function markRead(roomId: string, messageId: string): Promise<void> {
  try {
    await putMarker(roomId, messageId);
  } catch (err) {
    if (!(err instanceof ApiError)) {
      throw err;
    }
  }
}

// markAllRead marks every unread room read up to its newest message, all at
// once. The first failure among them is recorded. It never rejects.
export async function markAllRead(): Promise<void> {
  const epoch = resets;
  const marks: Promise<void>[] = [];
  for (const room of Object.values(store.rooms)) {
    if (isUnread(room) && room.last_message_id !== null) {
      marks.push(putMarker(room.id, room.last_message_id));
    }
  }
  const failure = (await Promise.allSettled(marks)).find((r) => r.status === 'rejected');
  if (failure !== undefined && epoch === resets) {
    failed(failure.reason);
  }
}

// How send acts in a room: whether it posts a message of its own, or steers
// the agents already working in it.
export type SendMode = 'queue' | 'steer';

// send posts text to the room ('queue') or steers every working agent in it
// ('steer'). Each response is written as its message.posted event would be,
// then the room's marker moves to the newest of them. It rejects with the
// ApiError, and never throws anything else.
export async function send(
  roomId: string,
  text: string,
  mode: SendMode,
  clientId: string,
  replyTo?: string | null,
): Promise<void> {
  let newest: string | null = null;
  // Each response is written as it lands, so a failure behind it keeps what
  // came before. The marker waits for the whole send to succeed.
  const written = (message: Message): void => {
    receive('message.posted', { message });
    if (newest === null || message.id > newest) {
      newest = message.id;
    }
  };
  if (mode === 'queue') {
    written(await postMessage(roomId, text, clientId, replyTo ?? undefined));
  } else {
    const working = store.rooms[roomId]?.agents.filter((a) => a.working) ?? [];
    if (working.length === 0) {
      throw new ApiError(409, 'no_turn', 'no agent has a running turn');
    }
    for (const [i, agent] of working.entries()) {
      written(await steer(roomId, agent.id, text, `${clientId}-${i}`));
    }
  }
  if (newest !== null) {
    await markRead(roomId, newest);
  }
}

// answer posts the answer to a message's open prompt. The response is written
// as its prompt.changed event would be, so the prompt shown becomes the
// answered one. It rejects with the ApiError, and never throws anything else.
export async function answer(roomId: string, messageId: string, text: string): Promise<void> {
  const message = await answerPrompt(roomId, messageId, text);
  receive('prompt.changed', { message });
}

// cancel cancels a message's waiting deliveries. A 409 nothing_waiting is
// quiet: there was nothing to cancel. Any other failure is recorded. It
// never rejects.
export async function cancel(roomId: string, messageId: string): Promise<void> {
  try {
    await cancelMessage(roomId, messageId);
  } catch (err) {
    if (!(err instanceof ApiError && err.code === 'nothing_waiting')) {
      failed(err);
    }
  }
}

// stopTurns asks every agent working in the room to stop its running turn. A
// failure is recorded. It never rejects.
export async function stopTurns(roomId: string): Promise<void> {
  const working = store.rooms[roomId]?.agents.filter((a) => a.working) ?? [];
  for (const agent of working) {
    try {
      await stopTurn(roomId, agent.id);
    } catch (err) {
      failed(err);
    }
  }
}

// pause and resume toggle a room's paused state. The answer's room is written
// as its room.changed event would be. A failure is recorded. They never
// reject.
export async function pause(roomId: string): Promise<void> {
  await setPaused(roomId, pauseRoom);
}
export async function resume(roomId: string): Promise<void> {
  await setPaused(roomId, resumeRoom);
}

// setPaused makes the call and writes the answer's room after it.
async function setPaused(roomId: string, call: (id: string) => Promise<Room>): Promise<void> {
  try {
    const room = await call(roomId);
    receive('room.changed', { room });
  } catch (err) {
    failed(err);
  }
}

// putMarker is markRead, except that it rejects when the request fails. It
// does nothing when the room is not held, when its marker is already there or
// newer, or when the same marker is in flight.
async function putMarker(roomId: string, messageId: string): Promise<void> {
  if (!Object.hasOwn(store.rooms, roomId)) {
    return;
  }
  const read = store.rooms[roomId].last_read_message_id;
  // ULIDs sort by time, and the server only moves a marker forward.
  if (read !== null && read >= messageId) {
    return;
  }
  const key = `${roomId} ${messageId}`;
  if (marking.has(key)) {
    return;
  }
  const epoch = resets;
  marking.add(key);
  try {
    await markRoomRead(roomId, messageId);
  } finally {
    if (epoch === resets) {
      marking.delete(key); // after a disconnect() the set is another connection's
    }
  }
  if (epoch !== resets || !Object.hasOwn(store.rooms, roomId)) {
    return;
  }
  // No event announces a marker, so it is set here. The room is read again:
  // it may have been replaced meanwhile, and a later marker may have settled
  // first, which this one must not move back.
  const room = store.rooms[roomId];
  if (room.last_read_message_id === null || room.last_read_message_id < messageId) {
    room.last_read_message_id = messageId;
  }
}

// putLatest makes a message the newest known of its room, unless a newer one
// is held there. The same message replaces itself, so a change to it shows.
function putLatest(message: Message): void {
  const id = message.room_id;
  if (Object.hasOwn(store.latest, id) && store.latest[id].id > message.id) {
    return;
  }
  store.latest[id] = message;
}

// loadLatest fetches a room's newest message when it is not the one held in
// latest, for a room that is listed without its feed being held. The answer
// is written only when nothing is held there or what is held is older. A
// failure is ignored. It never rejects.
export async function loadLatest(roomId: string): Promise<void> {
  if (!Object.hasOwn(store.rooms, roomId)) {
    return;
  }
  const messageId = store.rooms[roomId].last_message_id;
  if (messageId === null) {
    return;
  }
  if (Object.hasOwn(store.latest, roomId) && store.latest[roomId].id === messageId) {
    return;
  }
  const key = `${roomId} ${messageId}`;
  if (fetchingLatest.has(key)) {
    return;
  }
  const epoch = resets;
  fetchingLatest.add(key);
  try {
    const message = await getMessage(roomId, messageId);
    // An event may have brought a newer one while this was in flight, or this
    // one as it is now: the answer was read before either, so unlike an event
    // it does not replace the message it names.
    if (epoch === resets && (!Object.hasOwn(store.latest, roomId) || store.latest[roomId].id < message.id)) {
      store.latest[roomId] = message;
    }
  } catch {
    // ignored
  } finally {
    if (epoch === resets) {
      fetchingLatest.delete(key);
    }
  }
}

// loadQuoted fetches a message that a reply names, once per id for the life
// of the connection, whether or not the fetch succeeds. It never rejects.
export async function loadQuoted(roomId: string, messageId: string): Promise<void> {
  if (askedQuoted.has(messageId)) {
    return;
  }
  const epoch = resets;
  askedQuoted.add(messageId);
  try {
    const message = await getMessage(roomId, messageId);
    if (epoch === resets) {
      store.quoted[messageId] = message;
    }
  } catch {
    // ignored: it stays absent
  }
}

// loadActivity replaces the list of what the agents did while working on a
// message with the server's. It is a snapshot fetch, so the steps posted
// while it is in flight are held and applied to its answer. One request per
// message at a time. A failure is ignored and leaves the entry absent. It
// never rejects.
export async function loadActivity(roomId: string, messageId: string): Promise<void> {
  if (loadingActivity.has(messageId)) {
    return;
  }
  loadingActivity.add(messageId);
  const epoch = fetchStarted();
  try {
    const list = await listActivity(roomId, messageId);
    if (epoch === resets) {
      store.activity[messageId] = list;
    }
  } catch {
    // ignored: it stays absent
  }
  if (epoch === resets) {
    loadingActivity.delete(messageId); // after a disconnect() the set is another connection's
  }
  fetchSettled(epoch);
}

// followWorking is called with the messages just written into a held feed. A
// turn that is working shows its steps without being asked, so its list is
// fetched unless it is held already; a finished one waits to be opened.
function followWorking(messages: Message[]): void {
  for (const message of messages) {
    if (!Object.hasOwn(store.activity, message.id) && isWorking(message)) {
      void loadActivity(message.room_id, message.id);
    }
  }
}

// isWorking says whether any agent's copy of a message is being worked on. A
// message off the wire may hold anything in deliveries.
function isWorking(message: Message): boolean {
  return message.deliveries.some((d: unknown) => isObject(d) && d.state === 'working');
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

// keepMarker gives a room that is about to replace the held one the held read
// marker, when that is the newer of the two. A snapshot or an event carries
// the marker as it was when the server read the room, which can be before a
// marker this page has since put; and no event announces a marker, so nothing
// would correct it afterwards. The server only moves a marker forward, so the
// newer one held is the truth, and the older one incoming is late.
function keepMarker(room: Room): void {
  if (!Object.hasOwn(store.rooms, room.id)) {
    return;
  }
  const held = store.rooms[room.id].last_read_message_id;
  if (held !== null && (room.last_read_message_id === null || room.last_read_message_id < held)) {
    room.last_read_message_id = held;
  }
}

// putRoom inserts the room, or replaces the one held under its id. Its agents
// come with it, so what set working for the old ones is forgotten.
export function putRoom(room: Room): void {
  keepMarker(room);
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
  applyToActivity(name, data);
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

// applyToFeeds keeps the held messages, and the newest of each room, current.
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
      // Whether or not the room's feed is held: the Unread page shows rooms
      // that have never been opened.
      putLatest(message);
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
      followWorking([message]);
      return;
    }
  }
}

// isActivity checks what the activity block reads.
function isActivity(v: Fields): v is Fields & Activity {
  return (
    typeof v.id === 'string' &&
    typeof field(v, 'agent')?.id === 'string' &&
    (v.kind === 'tool' || v.kind === 'remark') &&
    typeof v.text === 'string' &&
    typeof v.tool === 'string' &&
    typeof v.preview === 'string' &&
    typeof v.arguments === 'string'
  );
}

// applyToActivity keeps the held lists of activity current. A step of a
// message whose list is not held is dropped: the list is fetched whole when
// it is wanted. A tool is posted when it starts and again, under the same id,
// when it finishes; and a replayed event repeats one already held, so a
// replayed start must not put a finished tool back to unfinished.
function applyToActivity(name: string, data: unknown): void {
  if (name !== 'activity.posted' || !isObject(data)) {
    return;
  }
  const id = data.message_id;
  const posted = field(data, 'activity');
  if (typeof id !== 'string' || posted === null || !isActivity(posted) || !Object.hasOwn(store.activity, id)) {
    return;
  }
  const list = store.activity[id];
  const at = list.findIndex((a) => a.id === posted.id);
  if (at === -1) {
    list.push(posted);
  } else if (list[at].succeeded === null || posted.succeeded !== null) {
    list[at] = posted;
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
      break;
    }
  }
  // The newest message of a room is held on its own, feed or no feed.
  for (const message of Object.values(store.latest)) {
    if (message.id === data.message_id) {
      message.text = data.text;
      break;
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

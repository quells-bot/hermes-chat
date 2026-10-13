// The room screen on the real store. The network, the stream and the
// sentinels' observer are mocked; the test says when an edge is seen.
import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { tick } from 'svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { agent, message, room } from './lib/fixtures';
import { navigate, route } from './lib/route.svelte';
import { apply, disconnect, load, putRoom, store, type Feed } from './lib/store.svelte';
import type { Message, Room as RoomData } from './lib/types';
import Room from './Room.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  listRooms: vi.fn(),
  listAgents: vi.fn(),
  getHalt: vi.fn(),
  listMessages: vi.fn(),
  getMessage: vi.fn(),
  markRoomRead: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));
vi.mock('./lib/route.svelte', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/route.svelte')>()),
  navigate: vi.fn(),
}));

// The sentinels' reports, by edge. With topSeenAtOnce the top one reports that
// it is seen as soon as it is drawn, which is before the feed has been placed.
const edges = new Map<string, (seen: boolean) => void>();
let topSeenAtOnce = false;
vi.mock('./lib/seen', () => ({
  whenSeen: (node: HTMLElement, report: (seen: boolean) => void) => {
    const edge = node.dataset.edge ?? '';
    edges.set(edge, report);
    if (topSeenAtOnce && edge === 'top') {
      report(true);
    }
    return { destroy: () => edges.delete(edge) };
  },
}));

// jsdom lays nothing out. Here a feed is as tall as its messages, at 300px
// each, plus 10px for every x in it, so that a longer text is a taller feed.
function height(this: Element): number {
  const text = this.textContent ?? '';
  return this.querySelectorAll('article').length * 300 + (text.split('x').length - 1) * 10;
}

const scrollIntoView = vi.fn();

beforeEach(() => {
  disconnect();
  store.loaded = true;
  route.prev = '/';
  edges.clear();
  topSeenAtOnce = false;
  scrollIntoView.mockReset();
  Element.prototype.scrollIntoView = scrollIntoView;
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(height);
  vi.mocked(navigate).mockReset();
  vi.mocked(api.listRooms).mockReset();
  vi.mocked(api.listAgents).mockReset();
  vi.mocked(api.getHalt).mockReset();
  vi.mocked(api.listMessages).mockReset().mockResolvedValue([]);
  vi.mocked(api.getMessage).mockReset();
  vi.mocked(api.markRoomRead).mockReset().mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
});

function messages(ids: string[]) {
  return ids.map((id) => message({ id, text: id }));
}

// showRoom puts the room and its feed in the store, then draws the screen.
async function showRoom(overrides: Partial<RoomData>, ids: string[], feed: Partial<Feed> = {}) {
  putRoom(room({ last_message_id: ids.at(-1) ?? null, ...overrides }));
  store.feeds['room-1'] = { messages: messages(ids), more: false, loading: false, error: '', ...feed };
  const view = render(Room, { id: 'room-1' });
  await tick();
  return view;
}

function post(id: string, text = id): void {
  apply('message.posted', { message: message({ id, text }) });
}

function feedOf(container: HTMLElement): HTMLElement {
  const feed = container.querySelector<HTMLElement>('.feed');
  expect(feed).toBeTruthy();
  return feed as HTMLElement;
}

// The texts of the messages on screen, in order, with NEW where the divider is.
function drawn(container: HTMLElement): string[] {
  return [...feedOf(container).querySelectorAll('article, [role="separator"]')].map((el) =>
    el.tagName === 'ARTICLE' ? (el.querySelector('.body')?.textContent?.trim() ?? '') : 'NEW',
  );
}

test('the header names the room and says what each agent is doing', async () => {
  await showRoom(
    {
      topic: 'Garden',
      agents: [
        agent({ id: 'a1', display_name: 'Forge', connected: true, working: true }),
        agent({ id: 'a2', display_name: 'Scout', connected: true }),
        agent({ id: 'a3', display_name: 'Atlas' }),
      ],
    },
    [],
  );
  const header = screen.getByRole('banner');
  expect(within(header).getByRole('heading', { name: 'Garden' })).toBeInTheDocument();
  expect(within(header).getByText('Forge working · Scout idle · Atlas offline')).toBeInTheDocument();
  expect(within(header).queryByRole('button')).toBeNull();
});

test('back leads to the list, or to Unread when the person came from there', async () => {
  const { unmount } = await showRoom({}, []);
  expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '#/');
  unmount();

  route.prev = '/unread';
  await showRoom({}, []);
  expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '#/unread');
});

test('a room the store does not hold sends the person to the list', async () => {
  store.loaded = false;
  render(Room, { id: 'room-9' });
  await tick();
  expect(navigate).not.toHaveBeenCalled();

  store.loaded = true;

  await waitFor(() => expect(navigate).toHaveBeenCalledWith('/'));
});

test('a room the store holds stays on screen', async () => {
  await showRoom({}, ['01M1']);
  expect(navigate).not.toHaveBeenCalled();
});

test('the divider sits before the first unread message, and the feed is placed on it', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2', '01M3']);
  expect(drawn(container)).toEqual(['01M1', 'NEW', '01M2', '01M3']);
  const divider = screen.getByRole('separator');
  expect(divider).toHaveTextContent('New');
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
  expect(scrollIntoView).toHaveBeenCalledWith({ block: 'start' });
  expect(scrollIntoView.mock.contexts[0]).toBe(divider);
  expect(feedOf(container).scrollTop).toBe(0);
});

test('with nothing unread there is no divider and the feed is placed at its end', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M3' }, ['01M1', '01M2', '01M3']);
  expect(screen.queryByRole('separator')).toBeNull();
  expect(scrollIntoView).not.toHaveBeenCalled();
  expect(feedOf(container).scrollHeight).toBe(900);
  expect(feedOf(container).scrollTop).toBe(900);
});

test('the feed is placed when its messages arrive after the screen is drawn', async () => {
  putRoom(room({ last_read_message_id: '01M1', last_message_id: '01M2' }));
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  expect(scrollIntoView).not.toHaveBeenCalled();

  store.feeds['room-1'] = { messages: messages(['01M1', '01M2']), more: false, loading: false, error: '' };

  await waitFor(() => expect(drawn(container)).toEqual(['01M1', 'NEW', '01M2']));
  expect(scrollIntoView.mock.contexts).toEqual([screen.getByRole('separator')]);
});

test('the divider and the placement wait for the backlog, and take it into account', async () => {
  // The newest page is held and the messages before it are still being
  // fetched: the first unread one is among those.
  const { container } = await showRoom({ last_read_message_id: '01K1' }, ['01M1', '01M2'], {
    more: true,
    loading: true,
  });
  const feed = feedOf(container);
  expect(drawn(container)).toEqual(['01M1', '01M2']);
  expect(scrollIntoView).not.toHaveBeenCalled();
  expect(feed.scrollTop).toBe(0);

  store.feeds['room-1'].messages.unshift(...messages(['01K1', '01K2', '01K3']));
  await waitFor(() => expect(drawn(container)).toEqual(['01K1', '01K2', '01K3', '01M1', '01M2']));
  expect(scrollIntoView).not.toHaveBeenCalled();
  expect(feed.scrollTop).toBe(0);

  store.feeds['room-1'].loading = false;

  await waitFor(() => expect(drawn(container)).toEqual(['01K1', 'NEW', '01K2', '01K3', '01M1', '01M2']));
  expect(scrollIntoView.mock.contexts).toEqual([screen.getByRole('separator')]);
  expect(feed.scrollTop).toBe(0);
});

test('a backlog that fails still lets the feed be placed', async () => {
  const { container } = await showRoom({ last_read_message_id: '01K1' }, ['01M1', '01M2'], {
    more: true,
    loading: true,
  });
  const feed = feedOf(container);

  store.feeds['room-1'].loading = false;

  await waitFor(() => expect(drawn(container)).toEqual(['01M1', '01M2']));
  // The first unread may lie on the page the backlog failed to bring: there
  // is no New, and the feed opens at its oldest held message.
  expect(screen.queryByRole('separator')).toBeNull();
  expect(scrollIntoView).not.toHaveBeenCalled();
  expect(feed.scrollTop).toBe(0);
});

test('the divider stays where it was after the marker moves', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2', '01M3']);

  edges.get('end')!(true);
  await waitFor(() => expect(store.rooms['room-1'].last_read_message_id).toBe('01M3'));
  post('01M4');
  await waitFor(() => expect(store.rooms['room-1'].last_read_message_id).toBe('01M4'));
  await tick();

  expect(drawn(container)).toEqual(['01M1', 'NEW', '01M2', '01M3', '01M4']);
  // Placed once: the feed does not jump back to the divider.
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
});

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function numbered(prefix: string, count: number): string[] {
  return Array.from({ length: count }, (_, i) => `${prefix}${String(i).padStart(2, '0')}`);
}

test('the end is not watched until the feed has been placed', async () => {
  const page = deferred<ReturnType<typeof messages>>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  putRoom(room({ last_read_message_id: '01M1', last_message_id: '01M2' }));
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  expect(api.listMessages).toHaveBeenCalledTimes(1);
  // An empty feed's end would be in view, and say nothing about reading.
  expect(container.querySelector('[data-edge="end"]')).toBeNull();
  expect(edges.has('end')).toBe(false);

  page.resolve(messages(['01M1', '01M2']));

  await waitFor(() => expect(edges.has('end')).toBe(true));
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
  expect(drawn(container)).toEqual(['01M1', 'NEW', '01M2']);
});

test('a page that lands does not mark the room read until the end is seen', async () => {
  const page = deferred<ReturnType<typeof messages>>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  putRoom(room({ last_read_message_id: '01M10', last_message_id: '01M49' }));
  render(Room, { id: 'room-1' });
  await tick();
  // Were the end of the empty feed watched, this is what it would report.
  edges.get('end')?.(true);
  await tick();

  page.resolve(messages(numbered('01M', 50)));

  await waitFor(() => expect(edges.has('end')).toBe(true));
  await tick();
  expect(screen.getByRole('separator').nextElementSibling).toHaveTextContent('01M11');
  expect(api.markRoomRead).not.toHaveBeenCalled();
  expect(store.rooms['room-1'].last_read_message_id).toBe('01M10');

  edges.get('end')!(true);

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M49'));
  expect(api.markRoomRead).toHaveBeenCalledTimes(1);
});

test('a page and its backlog do not mark the room read until the end is seen', async () => {
  const backlog = deferred<ReturnType<typeof messages>>();
  vi.mocked(api.listMessages)
    .mockResolvedValueOnce(messages(numbered('01M', 50)))
    .mockReturnValueOnce(backlog.promise);
  putRoom(room({ last_read_message_id: '01K10', last_message_id: '01M49' }));
  const { container } = render(Room, { id: 'room-1' });
  edges.get('end')?.(true); // as above: the empty feed's end, if it were watched

  // The newest page is drawn while the messages before it are fetched.
  await waitFor(() => expect(screen.getByText('01M49')).toBeInTheDocument());
  expect(api.listMessages).toHaveBeenCalledTimes(2);
  expect(store.feeds['room-1'].loading).toBe(true);
  await tick();
  expect(edges.has('end')).toBe(false);
  expect(screen.queryByRole('separator')).toBeNull();
  expect(scrollIntoView).not.toHaveBeenCalled();
  expect(api.markRoomRead).not.toHaveBeenCalled();

  backlog.resolve(messages(numbered('01K', 21)));

  await waitFor(() => expect(edges.has('end')).toBe(true));
  const divider = screen.getByRole('separator');
  expect(divider.previousElementSibling).toHaveTextContent('01K10');
  expect(divider.nextElementSibling).toHaveTextContent('01K11');
  expect(scrollIntoView.mock.contexts).toEqual([divider]);
  expect(drawn(container)).toHaveLength(72);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled();
  expect(store.rooms['room-1'].last_read_message_id).toBe('01K10');

  edges.get('end')!(true);

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M49'));
  expect(api.markRoomRead).toHaveBeenCalledTimes(1);
});

test('a short feed whose end is in view is marked once the sentinel reports', async () => {
  await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2']);
  await tick();
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
  expect(api.markRoomRead).not.toHaveBeenCalled();

  edges.get('end')!(true);

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M2'));
  await waitFor(() => expect(store.rooms['room-1'].last_read_message_id).toBe('01M2'));
});

test('the marker does not move until the end has been seen', async () => {
  const ids = Array.from({ length: 60 }, (_, i) => `01M${String(i).padStart(2, '0')}`);
  await showRoom({ last_read_message_id: null }, ids);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled();

  edges.get('end')!(true);

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M59'));
  expect(api.markRoomRead).toHaveBeenCalledTimes(1);
});

test('the marker does not move while the page is hidden, and moves when it is shown', async () => {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2']);
  edges.get('end')!(true);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M2'));
});

test('a page that is hidden after the end was seen does not move the marker', async () => {
  await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2']);
  edges.get('end')!(true);
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledTimes(1));

  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  document.dispatchEvent(new Event('visibilitychange'));
  post('01M3');
  await tick();
  await tick();

  expect(screen.getByText('01M3')).toBeInTheDocument();
  expect(api.markRoomRead).toHaveBeenCalledTimes(1);
});

test('a message that arrives while the end is in view moves the marker to it', async () => {
  // The view is 300px tall, so the new message fits in it.
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(300);
  const { container } = await showRoom({ last_read_message_id: '01M2' }, ['01M1', '01M2']);
  const feed = feedOf(container);
  edges.get('end')!(true);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled(); // already read

  post('01M3');

  await waitFor(() => expect(feed.scrollTop).toBe(600)); // the end keeps it there
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M3'));
  await waitFor(() => expect(store.rooms['room-1'].last_read_message_id).toBe('01M3'));
  await tick();
  expect(screen.queryByRole('separator')).toBeNull(); // it fits: no New
});

test('a message that arrives after the end has left view does not move the marker', async () => {
  await showRoom({ last_read_message_id: '01M2' }, ['01M1', '01M2']);
  edges.get('end')!(true);
  await tick();
  edges.get('end')!(false);
  await tick();

  post('01M3');
  await tick();
  await tick();

  expect(screen.getByText('01M3')).toBeInTheDocument();
  expect(api.markRoomRead).not.toHaveBeenCalled();
});

test('the top is watched only after first placement, and then a report loads the page before it', async () => {
  topSeenAtOnce = true;
  putRoom(room({ id: 'room-1', last_read_message_id: '01M3' }));
  store.feeds['room-1'] = { messages: messages(['01M2', '01M3']), more: true, loading: true, error: '' };
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  expect(api.listMessages).not.toHaveBeenCalled();
  // Not placed yet: the sentinel is not drawn, so its report cannot be taken
  // before the feed has been put where it opens (A3).
  expect(container.querySelector('[data-edge="top"]')).toBeNull();
  expect(edges.has('top')).toBe(false);

  store.feeds['room-1'].loading = false;

  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(1));
  expect(api.listMessages).toHaveBeenCalledWith('room-1', '01M2', 50);
  expect(feedOf(container).scrollTop).toBe(600); // placed at its end
});

test('the top is only watched while older messages remain', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M2' }, ['01M1', '01M2'], { more: true });
  const feed = feedOf(container);
  expect(feed.firstElementChild).toHaveAttribute('data-edge', 'top');
  expect(feed.querySelector('[data-edge="end"]')?.previousElementSibling).toHaveClass('messages');

  store.feeds['room-1'].more = false;

  await waitFor(() => expect(feed.querySelector('[data-edge="top"]')).toBeNull());
  expect(edges.has('top')).toBe(false);
});

test('older messages arriving keep the same message in view', async () => {
  vi.mocked(api.listMessages).mockResolvedValue(messages(['01K1', '01K2']));
  const { container } = await showRoom({ last_read_message_id: '01M3' }, ['01M1', '01M2', '01M3'], {
    more: true,
  });
  const feed = feedOf(container);
  expect(feed.scrollHeight).toBe(900);
  feed.scrollTop = 0;

  edges.get('top')!(true);

  await waitFor(() => expect(drawn(container)).toEqual(['01K1', '01K2', '01M1', '01M2', '01M3']));
  expect(feed.scrollHeight).toBe(1500);
  expect(feed.scrollTop).toBe(600);
});

test('a message that arrives while the feed is at its end keeps it there when it fits', async () => {
  // The view is 300px tall, so the new message fits in it.
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(300);
  const { container } = await showRoom({ last_read_message_id: '01M3' }, ['01M1', '01M2', '01M3']);
  const feed = feedOf(container);
  expect(feed.scrollTop).toBe(900);
  edges.get('end')!(true);
  await tick();

  post('01M4');

  await waitFor(() => expect(feed.scrollTop).toBe(900)); // the end stays in view
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M4'));
  await waitFor(() => expect(store.rooms['room-1'].last_read_message_id).toBe('01M4'));
  await tick();
  expect(screen.queryByRole('separator')).toBeNull(); // it fits: no New
});

test('a message that arrives while the person reads further up does not move the feed', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M3' }, ['01M1', '01M2', '01M3']);
  const feed = feedOf(container);
  feed.scrollTop = 100;

  post('01M4');

  await waitFor(() => expect(screen.getByText('01M4')).toBeInTheDocument());
  await tick();
  expect(feed.scrollTop).toBe(100);
});

test('a reply being written keeps the feed at its end as it grows', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M3' }, ['01M1', '01M2', '01M3']);
  const feed = feedOf(container);
  expect(feed.scrollTop).toBe(900);

  apply('message.edited', { message_id: '01M3', text: 'xxxx' });

  await waitFor(() => expect(feed.scrollTop).toBe(940));
});

test('a failed page shows the error and Retry asks again', async () => {
  vi.mocked(api.listMessages).mockRejectedValueOnce(new api.ApiError(0, 'network', 'cannot reach the server'));
  putRoom(room());
  render(Room, { id: 'room-1' });
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('cannot reach the server'));
  expect(api.listMessages).toHaveBeenCalledTimes(1);
  vi.mocked(api.listMessages).mockResolvedValue(messages(['01M1']));

  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(api.listMessages).toHaveBeenCalledTimes(2);
  expect(api.listMessages).toHaveBeenLastCalledWith('room-1');
  await waitFor(() => expect(screen.getByText('01M1')).toBeInTheDocument());
  expect(screen.queryByRole('alert')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
});

test('a failed load shows on the room screen and Retry loads again', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room()]);
  vi.mocked(api.listAgents).mockResolvedValue([]);
  vi.mocked(api.getHalt).mockResolvedValue(false);
  store.loaded = false;
  store.error = 'cannot reach the server';
  render(Room, { id: 'room-1' });
  await tick();
  expect(screen.getByRole('alert')).toHaveTextContent('cannot reach the server');
  expect(api.listRooms).not.toHaveBeenCalled();
  vi.mocked(api.listMessages).mockResolvedValue(messages(['01M1']));

  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(api.listRooms).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(screen.getByText('01M1')).toBeInTheDocument());
  expect(screen.queryByRole('alert')).toBeNull();
  expect(screen.getByRole('heading', { name: 'Topic' })).toBeInTheDocument();
});

test('a failed load and a failed page both show, each with its Retry', async () => {
  await showRoom({}, []);
  store.error = 'internal error';
  store.feeds['room-1'].error = 'cannot reach the server';

  await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2));
  expect(screen.getAllByRole('alert').map((a) => a.textContent)).toEqual([
    'internal error',
    'cannot reach the server',
  ]);
  expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(2);
});

test('the foot of the feed states Paused, the turn cap, Archived, and Everything is stopped', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M1' }, ['01M1']);
  const foot = () => feedOf(container).querySelector('[data-edge="end"]')?.nextElementSibling?.textContent ?? null;
  expect(foot()).toBeNull();

  store.rooms['room-1'].paused = true;
  store.rooms['room-1'].pause_reason = 'human';
  await waitFor(() => expect(foot()).toBe('Paused'));

  store.rooms['room-1'].pause_reason = 'turn_cap';
  await waitFor(() => expect(foot()).toBe('Paused: turn cap of 6 reached'));

  store.rooms['room-1'].archived = true;
  await waitFor(() => expect(foot()).toBe('Archived'));

  store.halted = true;
  await waitFor(() => expect(foot()).toBe('Everything is stopped'));
});

test('leaving the room closes it', async () => {
  const { unmount } = await showRoom({}, ['01M1']);
  expect(store.open).toBe('room-1');

  unmount();

  expect(store.open).toBeNull();
});

test('returning to a room does not read messages posted while hidden', async () => {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  const { container } = await showRoom({ last_read_message_id: '01M2' }, ['01M1', '01M2']);
  const feed = feedOf(container);
  edges.get('end')!(true);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled(); // hidden: nothing is read

  post('01M3');
  post('01M4');
  await tick();
  await tick();
  expect(feed.scrollTop).toBe(600); // the feed did not move while hidden
  expect(api.markRoomRead).not.toHaveBeenCalled();

  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));

  await waitFor(() => expect(screen.getByRole('separator')).toBeInTheDocument());
  expect(drawn(container)).toEqual(['01M1', '01M2', 'NEW', '01M3', '01M4']);
  expect(feed.scrollTop).toBe(600); // the first of them stands at the top of the view
  expect(api.markRoomRead).not.toHaveBeenCalled();
  expect(store.rooms['room-1'].last_read_message_id).toBe('01M2');

  edges.get('end')!(true); // scrolling to the end

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M4'));
});

test('a load that merges unread messages the view cannot hold stops at the first of them', async () => {
  const { container } = await showRoom({ last_read_message_id: '01M002' }, ['01M001', '01M002']);
  const feed = feedOf(container);
  expect(feed.scrollTop).toBe(600); // placed at the end
  edges.get('end')!(true);
  await tick();

  vi.mocked(api.listRooms).mockResolvedValue([room({ id: 'room-1', last_read_message_id: '01M002' })]);
  vi.mocked(api.listAgents).mockResolvedValue([]);
  vi.mocked(api.getHalt).mockResolvedValue(false);
  vi.mocked(api.listMessages).mockResolvedValueOnce(
    messages(['01M003', '01M004', '01M005', '01M006', '01M007', '01M008', '01M009', '01M010']),
  );
  await load();

  // The first unread message's top is now the top of the view; the end is
  // not, so the marker does not move on the report taken before the merge.
  await waitFor(() => expect(feed.scrollTop).toBe(600));
  expect(drawn(container)).toEqual([
    '01M001',
    '01M002',
    'NEW',
    '01M003',
    '01M004',
    '01M005',
    '01M006',
    '01M007',
    '01M008',
    '01M009',
    '01M010',
  ]);
  expect(api.markRoomRead).not.toHaveBeenCalled();
  expect(store.rooms['room-1'].last_read_message_id).toBe('01M002');

  edges.get('end')!(true);

  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M010'));
});

test('a room opened empty draws no New, and its first message is read on a visible page', async () => {
  putRoom(room({ id: 'room-1' })); // never read
  store.feeds['room-1'] = { messages: [], more: false, loading: false, error: '' };
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  expect(edges.has('end')).toBe(false); // an empty feed is not placed
  expect(screen.queryByRole('separator')).toBeNull();
  expect(api.markRoomRead).not.toHaveBeenCalled();
  void container;

  post('01M1');

  await waitFor(() => expect(edges.has('end')).toBe(true)); // the first message places the feed
  expect(screen.queryByRole('separator')).toBeNull(); // and is in view: no New
  edges.get('end')!(true);
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M1'));
});

test('a never-read room asks for the 150 before its newest page, and opens at its oldest held message', async () => {
  const page = deferred<Message[]>();
  const backlog = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise).mockReturnValueOnce(backlog.promise);
  putRoom(room({ id: 'room-1', last_message_id: '01M199' })); // never read
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  expect(container.querySelector('[data-edge="top"]')).toBeNull(); // not placed yet

  const newest = Array.from({ length: 50 }, (_, i) => `01M${String(150 + i).padStart(3, '0')}`);
  const oldest = Array.from({ length: 150 }, (_, i) => `01M${String(i).padStart(3, '0')}`);
  page.resolve(messages(newest));
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(2));
  expect(api.listMessages).toHaveBeenLastCalledWith('room-1', '01M150', 150);
  await tick();
  expect(container.querySelector('[data-edge="top"]')).toBeNull(); // waiting for the backlog

  backlog.resolve(messages(oldest));
  const feed = feedOf(container);
  await waitFor(() => expect(feed.scrollTop).toBe(0)); // opens at its oldest held message
  expect(screen.queryByRole('separator')).toBeNull(); // where New would be is unknown
  expect(container.querySelector('[data-edge="top"]')).not.toBeNull(); // mounted after placement
  expect(api.markRoomRead).not.toHaveBeenCalled();
});

test('messages are hidden until the feed has been placed', async () => {
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  putRoom(room({ id: 'room-1', last_read_message_id: '01M1' }));
  const { container } = render(Room, { id: 'room-1' });
  await tick();
  const wrapper = container.querySelector('.messages');
  expect(wrapper).not.toBeNull();
  expect(wrapper).not.toHaveClass('placed');

  page.resolve(messages(['01M1', '01M2']));

  await waitFor(() => expect(wrapper).toHaveClass('placed'));
});

test('the two error blocks sit between the header and the scrolling feed', async () => {
  const { container } = await showRoom({}, []);
  store.error = 'internal error';
  store.feeds['room-1'].error = 'cannot reach the server';
  await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2));

  const room_ = container.querySelector('.room') as HTMLElement;
  const order = Array.from(room_.children).map((el) =>
    el.tagName === 'HEADER' ? 'header' : (el as HTMLElement).classList.contains('problem') ? 'problem' : 'feed',
  );
  expect(order).toEqual(['header', 'problem', 'problem', 'feed']);
  expect(container.querySelector('.feed')?.querySelector('.problem')).toBeNull();
});

test('the header says when the stream is not live', async () => {
  store.link = 'live';
  const { unmount } = await showRoom({}, []);
  expect(screen.queryByText('reconnecting')).toBeNull();
  unmount();

  store.link = 'waiting';
  await showRoom({}, []);
  expect(screen.getByText('reconnecting')).toBeInTheDocument();
});

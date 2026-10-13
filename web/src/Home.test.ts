import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { layout } from './lib/layout.svelte';
import { signOut } from './lib/session.svelte';
import { search } from './lib/search.svelte';
import { closeRoom, connect, disconnect, load, openRoom, store } from './lib/store.svelte';
import { room } from './lib/fixtures';
import { route, startRouting } from './lib/route.svelte';
import { note } from './lib/note.svelte';
import { note as fixture, noteEntry } from './lib/fixtures';
import Home from './Home.svelte';

vi.mock('./lib/session.svelte', () => ({
  session: { status: 'in', me: { id: '01J', username: 'kai' }, error: '' },
  start: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
}));
vi.mock('./lib/store.svelte', () => ({
  store: { link: 'live', loaded: false, error: '', rooms: {}, agents: {}, halted: false, feeds: {}, latest: {} },
  connect: vi.fn(),
  openRoom: vi.fn(),
  closeRoom: vi.fn(),
  loadOlder: vi.fn(),
  markRead: vi.fn(),
  disconnect: vi.fn(),
  load: vi.fn(),
  loadLatest: vi.fn(),
  markAllRead: vi.fn(),
  putRoom: vi.fn(),
  changeRoom: vi.fn(),
  addAgent: vi.fn(),
  removeAgent: vi.fn(),
  invite: vi.fn(),
  reason: (err: unknown) => (err instanceof Error ? err.message : String(err)),
  stopAll: vi.fn(),
  resumeAll: vi.fn(),
}));
// The real route, so that a test can move between paths under a mounted Home.
vi.mock('./lib/route.svelte', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/route.svelte')>()),
  startRouting: vi.fn(),
  navigate: vi.fn(),
}));
vi.mock('./lib/seen', () => ({ whenSeen: () => ({ destroy: () => {} }) }));
vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  createRoom: vi.fn(),
  openDM: vi.fn(),
}));
vi.mock('./lib/collapsed', () => ({
  loadCollapsed: () => ({ dms: false, rooms: false }),
  saveCollapsed: vi.fn(),
}));

const stopRouting = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(startRouting).mockReturnValue(stopRouting);
  store.link = 'live';
  store.loaded = true;
  store.error = '';
  store.rooms = {};
  route.path = '/';
  layout.wide = false;
  note.value = null;
});

test('connects on mount and disconnects on unmount', () => {
  const { unmount } = render(Home);
  expect(connect).toHaveBeenCalledTimes(1);
  expect(startRouting).toHaveBeenCalledTimes(1);

  unmount();

  expect(disconnect).toHaveBeenCalledTimes(1);
  expect(stopRouting).toHaveBeenCalledTimes(1);
});

test('shows nothing in place of the list until loaded', () => {
  store.loaded = false;
  render(Home);
  expect(screen.queryByRole('navigation')).toBeNull();
});

test('shows the list and the username once loaded', () => {
  render(Home);
  expect(screen.getByRole('navigation')).toBeInTheDocument();
  expect(screen.getByText('kai')).toBeInTheDocument();
});

test('an error shows with Retry, which loads', async () => {
  store.error = 'could not load';
  render(Home);
  expect(screen.getByRole('alert')).toHaveTextContent('could not load');

  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(load).toHaveBeenCalledTimes(1);
});

test('an error shows on #/new too', async () => {
  store.error = 'could not load';
  route.path = '/new';
  render(Home);
  expect(screen.getByRole('alert')).toHaveTextContent('could not load');
  expect(screen.getByRole('heading', { name: 'New room' })).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(load).toHaveBeenCalledTimes(1);
});

test('reconnecting shows when loaded and not live, and not before the first load', () => {
  store.link = 'waiting';
  store.loaded = false;
  const { unmount } = render(Home);
  expect(screen.queryByText('reconnecting')).toBeNull();
  unmount();

  store.loaded = true;
  render(Home);
  expect(screen.getByText('reconnecting')).toBeInTheDocument();
});

test('Log out calls signOut', async () => {
  render(Home);

  await userEvent.click(screen.getByRole('button', { name: 'Log out' }));

  expect(signOut).toHaveBeenCalledTimes(1);
});

test('the + link goes to #/new', () => {
  render(Home);
  expect(screen.getByRole('link', { name: 'New room' })).toHaveAttribute('href', '#/new');
});

test('the list’s header has the search button', () => {
  render(Home);
  expect(screen.getByRole('button', { name: 'Search' })).toHaveAttribute('aria-expanded', 'false');
});

test('the new-room route shows its own header and no list', () => {
  route.path = '/new';
  render(Home);
  expect(screen.getByRole('heading', { name: 'New room' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '#/');
  expect(screen.queryByRole('navigation')).toBeNull();
});

test('the halt control is in the header on the list and on #/new', () => {
  const { unmount } = render(Home);
  expect(screen.getByRole('button', { name: 'Stop all' })).toBeInTheDocument();
  unmount();

  route.path = '/new';
  render(Home);
  expect(screen.getByRole('button', { name: 'Stop all' })).toBeInTheDocument();
});

test('#/new shows the form under a header with a back link to #/', () => {
  route.path = '/new';
  render(Home);
  expect(screen.getByRole('heading', { name: 'New room' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '#/');
  expect(screen.getByLabelText('Topic')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
});

test('#/rooms/<id> shows the room, and another id shows another room', async () => {
  store.rooms = { '01R': room({ id: '01R', topic: 'Garden' }), '01S': room({ id: '01S', topic: 'Trip' }) };
  route.path = '/rooms/01R';
  render(Home);
  expect(screen.getByRole('heading', { name: 'Garden' })).toBeInTheDocument();
  expect(screen.getAllByRole('banner')).toHaveLength(1);
  expect(screen.queryByRole('navigation')).toBeNull();
  expect(openRoom).toHaveBeenCalledWith('01R');

  route.path = '/rooms/01S';

  await waitFor(() => expect(screen.getByRole('heading', { name: 'Trip' })).toBeInTheDocument());
  expect(screen.queryByRole('heading', { name: 'Garden' })).toBeNull();
  expect(closeRoom).toHaveBeenCalledWith('01R');
  expect(openRoom).toHaveBeenLastCalledWith('01S');
});

test('#/unread shows the Unread page', () => {
  route.path = '/unread';
  render(Home);
  expect(screen.getByRole('heading', { name: 'Unread' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Mark all read' })).toBeInTheDocument();
  expect(screen.queryByRole('navigation')).toBeNull();
});

test('the footer links to the note', () => {
  render(Home);
  expect(screen.getByRole('link', { name: /Note/ })).toHaveAttribute('href', '#/note');
});

test('the note link counts what waits for review', () => {
  note.value = fixture({
    entries: [noteEntry({ id: 'a' }), noteEntry({ id: 'b' }), noteEntry({ id: 'c', approved: true })],
  });
  const { unmount } = render(Home);
  expect(screen.getByRole('link', { name: /Note/ })).toHaveTextContent('Note 2');
  unmount();

  note.value = fixture({ entries: [noteEntry({ approved: true })] });
  render(Home);
  expect(screen.getByRole('link', { name: /Note/ })).toHaveTextContent(/^Note$/);
});

test('#/note shows the note screen', () => {
  note.value = fixture();
  route.path = '/note';
  render(Home);
  expect(screen.getByRole('heading', { name: 'Note' })).toBeInTheDocument();
  expect(screen.getByRole('textbox', { name: 'Note' })).toBeInTheDocument();
});

test('#/rooms/<id>/settings shows the settings screen', () => {
  store.rooms['01R'] = room({ id: '01R' });
  route.path = '/rooms/01R/settings';
  render(Home);
  expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '#/rooms/01R');
});

test('a room keeps its typing when the layout changes', async () => {
  store.rooms = { '01R': room({ id: '01R', topic: 'Trip' }) };
  route.path = '/rooms/01R';
  render(Home);
  expect(screen.queryByRole('navigation')).toBeNull();
  const box = screen.getByRole('textbox', { name: 'Message' });
  await userEvent.type(box, 'half a sentence');
  layout.wide = true;
  await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
  expect(screen.getByRole('textbox', { name: 'Message' })).toBe(box);
  expect(box).toHaveValue('half a sentence');
  layout.wide = false;
  await waitFor(() => expect(screen.queryByRole('navigation')).toBeNull());
  expect(box).toHaveValue('half a sentence');
  expect(openRoom).toHaveBeenCalledTimes(1);
});

test.each([
  ['/new', 'New room'],
  ['/unread', 'Unread'],
  ['/note', 'Note'],
  ['/rooms/01R/settings', 'Settings'],
])('wide shows the list beside %s', (path, heading) => {
  store.rooms['01R'] = room({ id: '01R' });
  note.value = fixture();
  layout.wide = true;
  route.path = path;
  render(Home);
  expect(screen.getByRole('navigation')).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
});

test('wide at #/ shows the list and an empty pane', () => {
  layout.wide = true;
  const { container } = render(Home);
  expect(screen.getByRole('navigation')).toBeInTheDocument();
  const pane = container.querySelector('.pane');
  expect(pane).not.toBeNull();
  expect(pane!.children).toHaveLength(0);
});

test('narrow at #/ has no pane', () => {
  const { container } = render(Home);
  expect(container.querySelector('.pane')).toBeNull();
});

test('wide draws Stop all once', () => {
  store.rooms['01R'] = room({ id: '01R' });
  note.value = fixture();
  layout.wide = true;
  for (const path of ['/new', '/note', '/rooms/01R/settings']) {
    route.path = path;
    const { unmount } = render(Home);
    expect(screen.getAllByRole('button', { name: 'Stop all' })).toHaveLength(1);
    unmount();
  }
  layout.wide = false;
  route.path = '/note';
  render(Home);
  expect(screen.getAllByRole('button', { name: 'Stop all' })).toHaveLength(1);
});

test('Home stops the layout and empties search on unmount', () => {
  layout.wide = true;
  search.query = 'trip';
  const { unmount } = render(Home);
  expect(layout.wide).toBe(true);
  unmount();
  expect(layout.wide).toBe(false);
  expect(search.query).toBe('');
});

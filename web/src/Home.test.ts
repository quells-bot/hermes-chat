import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { signOut } from './lib/session.svelte';
import { connect, disconnect, load, store } from './lib/store.svelte';
import { route, startRouting } from './lib/route.svelte';
import Home from './Home.svelte';

vi.mock('./lib/session.svelte', () => ({
  session: { status: 'in', me: { id: '01J', username: 'kai' }, error: '' },
  start: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
}));
vi.mock('./lib/store.svelte', () => ({
  store: { link: 'live', loaded: false, error: '', rooms: {}, agents: {}, halted: false },
  connect: vi.fn(),
  disconnect: vi.fn(),
  load: vi.fn(),
  putRoom: vi.fn(),
  stopAll: vi.fn(),
  resumeAll: vi.fn(),
}));
vi.mock('./lib/route.svelte', () => ({
  route: { path: '/' },
  startRouting: vi.fn(),
  navigate: vi.fn(),
}));
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

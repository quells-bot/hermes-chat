import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { room } from './lib/fixtures';
import { navigate } from './lib/route.svelte';
import { closeSearch, search } from './lib/search.svelte';
import { store } from './lib/store.svelte';
import ListScreen from './ListScreen.svelte';

vi.mock('./lib/session.svelte', () => ({
  session: { status: 'in', me: { id: '01J', username: 'kai' }, error: '' },
  signOut: vi.fn(),
}));
vi.mock('./lib/store.svelte', () => ({
  store: { link: 'live', loaded: true, error: '', rooms: {}, agents: {}, halted: false },
  load: vi.fn(),
  stopAll: vi.fn(),
  resumeAll: vi.fn(),
}));
vi.mock('./lib/route.svelte', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/route.svelte')>()),
  navigate: vi.fn(),
}));
vi.mock('./lib/collapsed', () => ({
  loadCollapsed: () => ({ dms: false, rooms: false }),
  saveCollapsed: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  closeSearch();
  store.loaded = true;
  store.rooms = {};
});

test('the search button opens a field that has the focus, and closes it', async () => {
  render(ListScreen);
  expect(screen.queryByRole('searchbox')).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  expect(screen.getByRole('searchbox', { name: 'Search' })).toHaveFocus();
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  expect(screen.queryByRole('searchbox')).toBeNull();
});

test('typing narrows the list to matches, in a collapsed group too, and without the Unread row', async () => {
  store.rooms = {
    r1: room({ id: 'r1', topic: 'Trip planning', last_message_id: 'm2', last_read_message_id: 'm1' }),
    r2: room({ id: 'r2', topic: 'Tax paperwork' }),
    r3: room({ id: 'r3', topic: 'Old trip', archived: true }),
  };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Rooms' }));
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'trip');
  const rows = screen.getAllByRole('listitem');
  expect(rows.map((r) => r.textContent)).toEqual([
    expect.stringContaining('Trip planning'),
    expect.stringContaining('Old trip'),
  ]);
  expect(rows[1]).toHaveTextContent('archived');
  expect(screen.queryByRole('link', { name: /Unread/ })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Rooms' })).toBeNull();
});

test('Enter opens the first match and closes search', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'trip{Enter}');
  expect(navigate).toHaveBeenCalledWith('/rooms/r1');
  expect(search).toEqual({ open: false, query: '' });
});

test('Enter with no match goes nowhere and keeps the field', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'zzz{Enter}');
  expect(navigate).not.toHaveBeenCalled();
  expect(screen.getByRole('searchbox')).toHaveValue('zzz');
});

test('Escape closes and empties the field', async () => {
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'tr{Escape}');
  expect(screen.queryByRole('searchbox')).toBeNull();
  expect(search).toEqual({ open: false, query: '' });
});

test('× closes and empties the field', async () => {
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'tr');
  await userEvent.click(screen.getByRole('button', { name: 'Close search' }));
  expect(screen.queryByRole('searchbox')).toBeNull();
  expect(search).toEqual({ open: false, query: '' });
});

test('an open field with nothing typed shows the normal list', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning', last_message_id: 'm2', last_read_message_id: 'm1' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  expect(screen.getByRole('link', { name: /Unread/ })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Rooms' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Direct messages' })).toBeInTheDocument();
});

test('tapping a result closes search', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'trip');
  await userEvent.click(screen.getByRole('link', { name: /Trip planning/ }));
  expect(search.open).toBe(false);
});

test('a query of regex characters draws no rows and does not throw', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), '[[a-');
  expect(screen.queryAllByRole('listitem')).toHaveLength(0);
});

import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { room } from './lib/fixtures';
import { disconnect, store } from './lib/store.svelte';
import type { Room as RoomData } from './lib/types';
import RoomMenu from './RoomMenu.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  pauseRoom: vi.fn(),
  resumeRoom: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  vi.mocked(api.pauseRoom).mockReset();
  vi.mocked(api.resumeRoom).mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
});

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

test('the menu opens and offers Pause or Resume by the room’s state', async () => {
  store.rooms['01R'] = room({ id: '01R' });
  render(RoomMenu, { id: '01R' });
  expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();

  // a held-out room offers Pause
  await userEvent.click(screen.getByRole('button', { name: 'Menu' }));
  expect(screen.getByRole('button', { name: 'Pause' })).toBeInTheDocument();

  // the ⋯ toggles the menu closed again
  await userEvent.click(screen.getByRole('button', { name: 'Menu' }));
  expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();
});

test('a paused room offers Resume', async () => {
  store.rooms['01R'] = room({ id: '01R', paused: true });
  render(RoomMenu, { id: '01R' });
  await userEvent.click(screen.getByRole('button', { name: 'Menu' }));
  expect(screen.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
});

test('a pending item disables it', async () => {
  store.rooms['01R'] = room({ id: '01R' });
  const page = deferred<RoomData>();
  vi.mocked(api.pauseRoom).mockReturnValue(page.promise);
  render(RoomMenu, { id: '01R' });
  await userEvent.click(screen.getByRole('button', { name: 'Menu' }));
  await userEvent.click(screen.getByRole('button', { name: 'Pause' }));

  expect(screen.getByRole('button', { name: 'Pause' })).toBeDisabled();

  page.resolve(room({ id: '01R', paused: true }));
  // the tap closes the menu once it has settled
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull());
});

test('pause writes the room through the store', async () => {
  store.rooms['01R'] = room({ id: '01R' });
  vi.mocked(api.pauseRoom).mockResolvedValue(room({ id: '01R', paused: true }));
  render(RoomMenu, { id: '01R' });
  await userEvent.click(screen.getByRole('button', { name: 'Menu' }));
  await userEvent.click(screen.getByRole('button', { name: 'Pause' }));

  await waitFor(() => expect(api.pauseRoom).toHaveBeenCalledWith('01R'));
  await waitFor(() => expect(store.rooms['01R'].paused).toBe(true));
});
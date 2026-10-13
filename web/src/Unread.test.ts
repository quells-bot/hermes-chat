// The Unread page on the real store. The network and the stream are mocked.
import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { tick } from 'svelte';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { message, room } from './lib/fixtures';
import { apply, disconnect, putRoom, store } from './lib/store.svelte';
import Unread from './Unread.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  getMessage: vi.fn(),
  markRoomRead: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  store.loaded = true;
  location.hash = '#/unread';
  vi.mocked(api.getMessage).mockReset().mockRejectedValue(new Error('none'));
  vi.mocked(api.markRoomRead).mockReset().mockResolvedValue(undefined);
});

function links(): string[] {
  return screen
    .getAllByRole('link')
    .map((a) => a.getAttribute('href') ?? '')
    .filter((h) => h.startsWith('#/rooms/'));
}

function rowOf(name: string): HTMLElement {
  return screen.getByRole('link', { name: new RegExp(name) });
}

test('rooms with an open prompt come first, then the newest', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  putRoom(room({ id: 'b', topic: 'Heat pump', last_message_id: '01M5' }));
  putRoom(room({ id: 'c', topic: 'Web UI', last_message_id: '01M1', open_prompts: 1 }));
  putRoom(room({ id: 'd', topic: 'Read', last_message_id: '01M2', last_read_message_id: '01M2' }));
  render(Unread);
  expect(links()).toEqual(['#/rooms/c', '#/rooms/a', '#/rooms/b']);
});

test('with nothing unread the page shows only its header', () => {
  putRoom(room({ id: 'd', last_message_id: '01M2', last_read_message_id: '01M2' }));
  render(Unread);
  expect(screen.getByRole('heading', { name: 'Unread' })).toBeInTheDocument();
  expect(links()).toEqual([]);
});

test('a row shows the author and the start of the newest message', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  store.latest.a = message({
    id: '01M9',
    room_id: 'a',
    author: { kind: 'agent', id: 'x', name: 'Helper' },
    text: 'Found   two\nflights',
    created_at: new Date().toISOString(),
  });
  render(Unread);
  const row = rowOf('Trip');
  expect(row).toHaveTextContent('Helper: Found two flights');
  expect(row).toHaveTextContent('now');
  expect(row.querySelector('svg, img, span.icon')).not.toBeNull();
});

test('a prompt reads asks', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9', open_prompts: 1 }));
  store.latest.a = message({
    id: '01M9',
    room_id: 'a',
    author: { kind: 'agent', id: 'x', name: 'Helper' },
    text: 'Book it?',
    prompt: { kind: 'approval', choices: ['Yes', 'No'], state: 'open', answer: null, answered_by: null, closed_at: null },
  });
  render(Unread);
  expect(rowOf('Trip')).toHaveTextContent('Helper asks: Book it?');
});

test('a message with no text names its attachments', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  store.latest.a = message({
    id: '01M9',
    room_id: 'a',
    text: '  ',
    attachments: [
      { id: '1', name: 'a.png', content_type: 'image/png', size: 1, image: true },
      { id: '2', name: 'b.pdf', content_type: 'application/pdf', size: 1, image: false },
    ],
  });
  render(Unread);
  expect(rowOf('Trip')).toHaveTextContent('kai: a.png, b.pdf');
});

test('a removed author shows the text alone', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  store.latest.a = message({ id: '01M9', room_id: 'a', author: null, text: 'left behind' });
  render(Unread);
  const row = rowOf('Trip');
  expect(row).toHaveTextContent(/Trip.*left behind/);
  expect(row).not.toHaveTextContent(':');
  expect(row.querySelector('.icon')).toBeNull();
});

test('the newest message is fetched when the store does not hold it', async () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  vi.mocked(api.getMessage).mockResolvedValue(message({ id: '01M9', room_id: 'a', text: 'fetched text' }));
  render(Unread);
  expect(rowOf('Trip')).not.toHaveTextContent('fetched text');
  await waitFor(() => expect(rowOf('Trip')).toHaveTextContent('fetched text'));
  expect(api.getMessage).toHaveBeenCalledWith('a', '01M9');
  expect(api.getMessage).toHaveBeenCalledTimes(1);
});

test('a latest that is not the room\'s newest is not shown', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  store.latest.a = message({ id: '01M3', room_id: 'a', text: 'older one' });
  render(Unread);
  expect(rowOf('Trip')).not.toHaveTextContent('older one');
});

test('a message that arrives adds its room to the page', async () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M1', last_read_message_id: '01M1' }));
  render(Unread);
  expect(links()).toEqual([]);
  apply('message.posted', { message: message({ id: '01M2', room_id: 'a', text: 'new one' }) });
  await tick();
  expect(links()).toEqual(['#/rooms/a']);
  expect(rowOf('Trip')).toHaveTextContent('new one');
});

test('Mark all read marks every unread room and returns to the list', async () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  putRoom(room({ id: 'b', topic: 'Heat', last_message_id: '01M5' }));
  putRoom(room({ id: 'd', topic: 'Read', last_message_id: '01M2', last_read_message_id: '01M2' }));
  render(Unread);
  await userEvent.click(screen.getByRole('button', { name: 'Mark all read' }));
  await waitFor(() => expect(location.hash).toBe('#/'));
  expect(vi.mocked(api.markRoomRead).mock.calls.map((c) => c.slice(0, 2)).sort()).toEqual([
    ['a', '01M9'],
    ['b', '01M5'],
  ]);
});

test('Mark all read is disabled while it is pending', async () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  let release: () => void = () => {};
  vi.mocked(api.markRoomRead).mockReturnValue(new Promise<void>((r) => (release = r)));
  render(Unread);
  const button = screen.getByRole('button', { name: 'Mark all read' });
  await userEvent.click(button);
  expect(button).toBeDisabled();
  release();
  await waitFor(() => expect(location.hash).toBe('#/'));
});

test('an error shows with Retry', () => {
  store.error = 'could not load';
  render(Unread);
  const alert = screen.getByRole('alert');
  expect(alert).toHaveTextContent('could not load');
  expect(within(alert.parentElement as HTMLElement).getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  store.error = '';
});

test('the header says when the stream is not live', async () => {
  store.link = 'live';
  const { unmount } = render(Unread);
  expect(screen.queryByText('reconnecting')).toBeNull();
  unmount();

  store.link = 'waiting';
  render(Unread);
  expect(screen.getByText('reconnecting')).toBeInTheDocument();
});

test('Everything is stopped is stated under the header when the store is halted', () => {
  store.halted = true;
  render(Unread);
  expect(screen.getByText('Everything is stopped')).toBeInTheDocument();
  const header = screen.getByRole('banner');
  expect(header.nextElementSibling).toHaveTextContent('Everything is stopped');
});


// RoomList on the real store, so that what an event changes is seen to reach
// the page. Only the network is mocked.
import { render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, expect, test, vi } from 'vitest';
import { agent, room } from './lib/fixtures';
import { apply, disconnect, putRoom } from './lib/store.svelte';
import RoomList from './RoomList.svelte';

vi.mock('./lib/api');
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

function names(): (string | undefined)[] {
  return screen.getAllByRole('listitem').map((li) => li.querySelector('.name')?.textContent);
}

function row(name: string): HTMLElement {
  const li = screen.getByText(name).closest('li');
  expect(li).not.toBeNull();
  return li as HTMLElement;
}

beforeEach(() => {
  localStorage.clear();
  disconnect();
  putRoom(room({ id: '01R', topic: 'Garden', agents: [agent({ id: '01A' })] }));
  putRoom(room({ id: '01S', topic: 'Trip' }));
});

test('a message.posted shows the unread dot on that row', async () => {
  render(RoomList);
  expect(screen.queryByRole('img', { name: 'unread' })).toBeNull();

  apply('message.posted', { message: { id: '01M1', room_id: '01S', deliveries: [] } });

  await waitFor(() => expect(within(row('Trip')).getByRole('img', { name: 'unread' })).toBeInTheDocument());
  expect(within(row('Garden')).queryByRole('img', { name: 'unread' })).toBeNull();
});

test('a delivery.changed with state working shows the working badge', async () => {
  render(RoomList);
  expect(screen.queryByText('working')).toBeNull();

  apply('delivery.changed', {
    message: { id: '01M1', room_id: '01R', deliveries: [{ agent: { id: '01A' }, state: 'working' }] },
  });

  await waitFor(() => expect(within(row('Garden')).getByText('working')).toBeInTheDocument());
  expect(within(row('Trip')).queryByText('working')).toBeNull();
});

test('a room.changed with a new topic renames the row and moves it', async () => {
  render(RoomList);
  expect(names()).toEqual(['Garden', 'Trip']);

  apply('room.changed', { room: room({ id: '01R', topic: 'Zoo', agents: [agent({ id: '01A' })] }) });

  await waitFor(() => expect(names()).toEqual(['Trip', 'Zoo']));
  expect(screen.queryByText('Garden')).toBeNull();
});

import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { agent, room } from './lib/fixtures';
import { loadCollapsed, saveCollapsed } from './lib/collapsed';
import { store } from './lib/store.svelte';
import RoomList from './RoomList.svelte';

vi.mock('./lib/store.svelte', () => ({ store: { rooms: {} } }));
vi.mock('./lib/collapsed', () => ({ loadCollapsed: vi.fn(), saveCollapsed: vi.fn() }));

beforeEach(() => {
  vi.mocked(saveCollapsed).mockClear();
  vi.mocked(loadCollapsed).mockReturnValue({ dms: false, rooms: false });
  store.rooms = {};
});

test('a collapsed group with an unread room shows the dot on its header', async () => {
  store.rooms = { '1': room({ id: '1', topic: 'Trip', last_message_id: '01B' }) };
  render(RoomList);
  const header = screen.getByRole('button', { name: /Rooms/ });
  expect(within(header).queryByLabelText('unread')).toBeNull();
  await userEvent.click(header);
  expect(header).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('Trip')).toBeNull();
  expect(within(header).getByLabelText('unread')).toBeInTheDocument();
  expect(saveCollapsed).toHaveBeenCalledWith({ dms: false, rooms: true });
});

test('Direct messages comes before Rooms', () => {
  render(RoomList);
  const names = screen.getAllByRole('button').map((b) => b.textContent?.replace('▾', '').trim());
  expect(names).toEqual(['Direct messages', 'Rooms']);
});

test('rows are in groupRooms order', () => {
  store.rooms = {
    '1': room({ id: '1', topic: 'Beta' }),
    '2': room({ id: '2', topic: 'alpha' }),
    '3': room({
      id: '3',
      flavor: 'dm',
      topic: '',
      agents: [agent({ handle: 'zed', display_name: 'Zed' })],
    }),
  };
  render(RoomList);
  const rows = screen.getAllByRole('listitem').map((li) => li.textContent);
  expect(rows[0]).toContain('Zed');
  expect(rows[1]).toContain('alpha');
  expect(rows[2]).toContain('Beta');
});

test('an archived room is not listed', () => {
  store.rooms = { '1': room({ id: '1', topic: 'Old', archived: true }) };
  render(RoomList);
  expect(screen.queryByText('Old')).toBeNull();
});

test('starts collapsed as loadCollapsed says', () => {
  vi.mocked(loadCollapsed).mockReturnValue({ dms: true, rooms: false });
  store.rooms = { '1': room({ id: '1', topic: 'Trip' }) };
  render(RoomList);
  expect(screen.getByRole('button', { name: /Direct messages/ })).toHaveAttribute('aria-expanded', 'false');
  expect(screen.getByRole('button', { name: /Rooms/ })).toHaveAttribute('aria-expanded', 'true');
  expect(screen.getByText('Trip')).toBeInTheDocument();
});

test('an empty group shows its header and no rows', () => {
  render(RoomList);
  expect(screen.getByRole('button', { name: /Rooms/ })).toHaveAttribute('aria-expanded', 'true');
  expect(screen.queryAllByRole('listitem')).toHaveLength(0);
});

import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { agent, human, room } from './lib/fixtures';
import { route } from './lib/route.svelte';
import { disconnect, store } from './lib/store.svelte';
import type { Room as RoomData } from './lib/types';
import RoomSettings from './RoomSettings.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  updateRoom: vi.fn(),
  addRoomAgent: vi.fn(),
  removeRoomAgent: vi.fn(),
  inviteHuman: vi.fn(),
  listHumans: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  store.loaded = true;
  location.hash = '';
  route.path = '/rooms/01R/settings';
  vi.mocked(api.updateRoom).mockReset();
  vi.mocked(api.addRoomAgent).mockReset();
  vi.mocked(api.removeRoomAgent).mockReset();
  vi.mocked(api.inviteHuman).mockReset();
  vi.mocked(api.listHumans).mockReset().mockResolvedValue([human()]);
});

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const saveButton = () => screen.getByRole('button', { name: 'Save' });

test('Save sends only what changed', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old', turn_cap: 6 });
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'Old', turn_cap: 12 }));
  render(RoomSettings, { id: '01R' });
  const cap = screen.getByRole('spinbutton', { name: 'Turn cap' });
  await userEvent.clear(cap);
  await userEvent.type(cap, '12');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(api.updateRoom).toHaveBeenCalledWith('01R', { turn_cap: 12 });
});

test('a topic being typed survives the room changing', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old', turn_cap: 6 });
  render(RoomSettings, { id: '01R' });
  const topic = screen.getByRole('textbox', { name: 'Topic' });
  await userEvent.clear(topic);
  await userEvent.type(topic, 'Mine');
  store.rooms['01R'] = room({ id: '01R', topic: 'Theirs', turn_cap: 9 });
  await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Turn cap' })).toHaveValue(9));
  expect(topic).toHaveValue('Mine');
});

test('a saved change clears both drafts', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old' });
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'New' }));
  render(RoomSettings, { id: '01R' });
  const topic = screen.getByRole('textbox', { name: 'Topic' });
  await userEvent.clear(topic);
  await userEvent.type(topic, 'New');
  await userEvent.click(saveButton());
  await waitFor(() => expect(saveButton()).toBeDisabled());
  store.rooms['01R'] = room({ id: '01R', topic: 'Later' });
  await waitFor(() => expect(topic).toHaveValue('Later'));
});

test('Save is disabled for a blank topic, a cap of 0, 101 or 2.5, and when nothing differs', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old', turn_cap: 6 });
  render(RoomSettings, { id: '01R' });
  const topic = screen.getByRole('textbox', { name: 'Topic' });
  const cap = screen.getByRole('spinbutton', { name: 'Turn cap' });
  expect(saveButton()).toBeDisabled();

  await userEvent.clear(topic);
  expect(saveButton()).toBeDisabled();
  await userEvent.type(topic, 'New');
  expect(saveButton()).toBeEnabled();

  for (const bad of ['0', '101', '2.5']) {
    await userEvent.clear(cap);
    await userEvent.type(cap, bad);
    expect(saveButton()).toBeDisabled();
  }
  await userEvent.clear(cap);
  await userEvent.type(cap, '100');
  expect(saveButton()).toBeEnabled();
});

test('a topic that differs only by surrounding spaces is not a change', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old' });
  render(RoomSettings, { id: '01R' });
  await userEvent.type(screen.getByRole('textbox', { name: 'Topic' }), '  ');
  expect(saveButton()).toBeDisabled();
});

test('inviting takes a second tap', async () => {
  store.rooms['01R'] = room({ id: '01R', humans: [human()] });
  vi.mocked(api.listHumans).mockResolvedValue([human(), human({ id: '01H2', username: 'sam' })]);
  vi.mocked(api.inviteHuman).mockResolvedValue(room({ id: '01R', humans: [human(), human({ id: '01H2', username: 'sam' })] }));
  render(RoomSettings, { id: '01R' });
  await userEvent.click(await screen.findByRole('button', { name: 'Invite' }));
  expect(api.inviteHuman).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
  expect(api.inviteHuman).toHaveBeenCalledWith('01R', '01H2');
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Invite' })).toBeNull());
});

test('Cancel disarms an invite', async () => {
  store.rooms['01R'] = room({ id: '01R', humans: [human()] });
  vi.mocked(api.listHumans).mockResolvedValue([human(), human({ id: '01H2', username: 'sam' })]);
  render(RoomSettings, { id: '01R' });
  await userEvent.click(await screen.findByRole('button', { name: 'Invite' }));
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(screen.getByRole('button', { name: 'Invite' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Confirm' })).toBeNull();
  expect(api.inviteHuman).not.toHaveBeenCalled();
});

test('arming another row disarms the first', async () => {
  store.rooms['01R'] = room({ id: '01R', humans: [human()] });
  vi.mocked(api.listHumans).mockResolvedValue([
    human(),
    human({ id: '01H2', username: 'sam' }),
    human({ id: '01H3', username: 'tia' }),
  ]);
  render(RoomSettings, { id: '01R' });
  const [first, second] = await screen.findAllByRole('button', { name: 'Invite' });
  await userEvent.click(first);
  await userEvent.click(second);
  expect(screen.getAllByRole('button', { name: 'Confirm' })).toHaveLength(1);
  expect(screen.getAllByRole('button', { name: 'Invite' })).toHaveLength(1);
});

test('people who cannot be listed leave nobody to invite and say why', async () => {
  store.rooms['01R'] = room({ id: '01R', humans: [human()] });
  vi.mocked(api.listHumans).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(RoomSettings, { id: '01R' });
  expect(await screen.findByRole('alert')).toHaveTextContent('cannot reach the server');
  expect(screen.queryByRole('button', { name: 'Invite' })).toBeNull();
});

test('the only agent has no Remove', () => {
  store.rooms['01R'] = room({ id: '01R', agents: [agent()] });
  render(RoomSettings, { id: '01R' });
  expect(screen.getByText('Helper')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
});

test('Remove asks once however often it is tapped', async () => {
  store.rooms['01R'] = room({ id: '01R', agents: [agent(), agent({ id: 'a2', handle: 'forge', display_name: 'Forge' })] });
  const answer = deferred<RoomData>();
  vi.mocked(api.removeRoomAgent).mockReturnValue(answer.promise);
  render(RoomSettings, { id: '01R' });
  const [remove] = screen.getAllByRole('button', { name: 'Remove' });
  await userEvent.click(remove);
  await userEvent.click(remove);
  expect(api.removeRoomAgent).toHaveBeenCalledTimes(1);
  answer.resolve(room({ id: '01R', agents: [agent({ id: 'a2', handle: 'forge', display_name: 'Forge' })] }));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull());
});

test('Add lists agents not in the room and leaves out revoked ones', async () => {
  store.rooms['01R'] = room({ id: '01R', agents: [agent()] });
  store.agents = {
    'agent-1': agent(),
    a2: agent({ id: 'a2', handle: 'forge', display_name: 'Forge' }),
    a3: agent({ id: 'a3', handle: 'old', display_name: 'Old', revoked: true }),
  };
  vi.mocked(api.addRoomAgent).mockResolvedValue(room({ id: '01R' }));
  render(RoomSettings, { id: '01R' });
  expect(screen.getAllByRole('button', { name: 'Add' })).toHaveLength(1);
  expect(screen.queryByText('Old')).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Add' }));
  expect(api.addRoomAgent).toHaveBeenCalledWith('01R', 'a2');
});

test('a revoked agent still in the room is listed with Remove', () => {
  store.rooms['01R'] = room({ id: '01R', agents: [agent(), agent({ id: 'a2', display_name: 'Gone', revoked: true })] });
  render(RoomSettings, { id: '01R' });
  expect(screen.getByText('Gone')).toBeInTheDocument();
  expect(screen.getAllByRole('button', { name: 'Remove' })).toHaveLength(2);
});

test('Archive becomes Unarchive and the screen stays', async () => {
  store.rooms['01R'] = room({ id: '01R' });
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', archived: true }));
  render(RoomSettings, { id: '01R' });
  await userEvent.click(screen.getByRole('button', { name: 'Archive' }));
  expect(api.updateRoom).toHaveBeenCalledWith('01R', { archived: true });
  expect(await screen.findByRole('button', { name: 'Unarchive' })).toBeInTheDocument();
  expect(route.path).toBe('/rooms/01R/settings');
});

test('an archived room disables everything but Unarchive', async () => {
  store.rooms['01R'] = room({ id: '01R', archived: true, agents: [agent(), agent({ id: 'a2' })], humans: [human()] });
  store.agents = { a3: agent({ id: 'a3', display_name: 'Forge' }) };
  vi.mocked(api.listHumans).mockResolvedValue([human(), human({ id: '01H2', username: 'sam' })]);
  render(RoomSettings, { id: '01R' });
  await screen.findByRole('button', { name: 'Invite' });
  for (const b of screen.getAllByRole('button')) {
    if (b.textContent === 'Unarchive') {
      expect(b).toBeEnabled();
    } else if (b.closest('header') === null) {
      expect(b).toBeDisabled();
    }
  }
  expect(screen.getByRole('textbox', { name: 'Topic' })).toBeDisabled();
  expect(screen.getByRole('spinbutton', { name: 'Turn cap' })).toBeDisabled();
});

test('a room archived by someone else freezes the screen', async () => {
  store.rooms['01R'] = room({ id: '01R' });
  render(RoomSettings, { id: '01R' });
  expect(screen.getByRole('textbox', { name: 'Topic' })).toBeEnabled();
  store.rooms['01R'] = room({ id: '01R', archived: true });
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Topic' })).toBeDisabled());
  expect(screen.getByRole('button', { name: 'Unarchive' })).toBeEnabled();
});

test('a refusal is shown and Dismiss clears it', async () => {
  store.rooms['01R'] = room({ id: '01R', agents: [agent(), agent({ id: 'a2' })] });
  vi.mocked(api.removeRoomAgent).mockRejectedValue(new api.ApiError(409, 'room_archived', 'the room is archived'));
  render(RoomSettings, { id: '01R' });
  await userEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]);
  expect(await screen.findByRole('alert')).toHaveTextContent('the room is archived');
  await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.queryByRole('alert')).toBeNull();
});

test('a DM’s settings address goes to the DM', async () => {
  store.rooms['01R'] = room({ id: '01R', flavor: 'dm' });
  render(RoomSettings, { id: '01R' });
  await waitFor(() => expect(location.hash).toBe('#/rooms/01R'));
});

test('an unknown room’s goes to the list', async () => {
  location.hash = '#/rooms/nope/settings';
  render(RoomSettings, { id: 'nope' });
  await waitFor(() => expect(location.hash).toBe('#/'));
});

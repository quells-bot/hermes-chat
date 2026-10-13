import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { ApiError, createRoom, openDM } from './lib/api';
import { agent, room } from './lib/fixtures';
import { replace } from './lib/route.svelte';
import { putRoom, store } from './lib/store.svelte';
import NewRoom from './NewRoom.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  createRoom: vi.fn(),
  openDM: vi.fn(),
}));
vi.mock('./lib/store.svelte', () => ({ store: { agents: {} }, putRoom: vi.fn() }));
vi.mock('./lib/route.svelte', () => ({ route: { path: '/new' }, replace: vi.fn() }));

beforeEach(() => {
  vi.clearAllMocks();
  store.agents = {
    '01A': agent({ id: '01A', handle: 'atlas', display_name: 'Atlas' }),
    '01F': agent({ id: '01F', handle: 'forge', display_name: 'Forge' }),
  };
});

test('Create posts the trimmed topic and the checked agents, then opens the room', async () => {
  const made = room({ id: '01R', topic: 'Trip' });
  vi.mocked(createRoom).mockResolvedValue(made);
  render(NewRoom);
  await userEvent.type(screen.getByLabelText('Topic'), '  Trip ');
  await userEvent.click(screen.getByRole('checkbox', { name: 'Forge' }));
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));
  expect(createRoom).toHaveBeenCalledWith('Trip', ['01F']);
  expect(putRoom).toHaveBeenCalledWith(made);
  expect(replace).toHaveBeenCalledWith('/rooms/01R');
});

test('Create is disabled without a topic, with a blank topic, and with no agent checked', async () => {
  render(NewRoom);
  const create = screen.getByRole('button', { name: 'Create' });
  await userEvent.click(screen.getByRole('checkbox', { name: 'Atlas' }));
  expect(create).toBeDisabled();
  await userEvent.type(screen.getByLabelText('Topic'), '   ');
  expect(create).toBeDisabled();
  await userEvent.type(screen.getByLabelText('Topic'), 'Trip');
  expect(create).toBeEnabled();
  await userEvent.click(screen.getByRole('checkbox', { name: 'Atlas' }));
  expect(create).toBeDisabled();
});

test("an agent's DM button opens its DM and lands in it", async () => {
  const made = room({ id: '01D', flavor: 'dm' });
  vi.mocked(openDM).mockResolvedValue(made);
  render(NewRoom);
  await userEvent.click(screen.getByRole('button', { name: 'Forge' }));
  expect(openDM).toHaveBeenCalledWith('01F');
  expect(putRoom).toHaveBeenCalledWith(made);
  expect(replace).toHaveBeenCalledWith('/rooms/01D');
});

test('a revoked agent is offered in neither section', () => {
  store.agents['01F'] = agent({ id: '01F', display_name: 'Forge', revoked: true });
  render(NewRoom);
  expect(screen.queryByRole('button', { name: 'Forge' })).toBeNull();
  expect(screen.queryByRole('checkbox', { name: 'Forge' })).toBeNull();
  expect(screen.getByRole('button', { name: 'Atlas' })).toBeInTheDocument();
});

test('agents are listed by display name', () => {
  store.agents = {
    '01A': agent({ id: '01A', display_name: 'zed' }),
    '01B': agent({ id: '01B', display_name: 'Beta' }),
    '01C': agent({ id: '01C', display_name: 'alpha' }),
  };
  render(NewRoom);
  const names = ['alpha', 'Beta', 'zed'];
  expect(screen.getAllByRole('checkbox').map((c) => c.closest('label')?.textContent?.trim())).toEqual(names);
  const buttons = screen.getAllByRole('button').map((b) => b.textContent?.trim());
  expect(buttons.slice(0, 3)).toEqual(names);
});

test("the server's validation message is shown and the form keeps its values", async () => {
  vi.mocked(createRoom).mockRejectedValue(
    new ApiError(400, 'validation', 'topic must be 1-200 characters with no control characters'),
  );
  render(NewRoom);
  await userEvent.type(screen.getByLabelText('Topic'), 'Trip');
  await userEvent.click(screen.getByRole('checkbox', { name: 'Atlas' }));
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'topic must be 1-200 characters with no control characters',
  );
  expect(screen.getByLabelText('Topic')).toHaveValue('Trip');
  expect(screen.getByRole('checkbox', { name: 'Atlas' })).toBeChecked();
  expect(replace).not.toHaveBeenCalled();
});

test('an error that is not an ApiError still shows its message', async () => {
  vi.mocked(openDM).mockRejectedValue(new Error('boom'));
  render(NewRoom);
  await userEvent.click(screen.getByRole('button', { name: 'Atlas' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('boom');
});

test('every button is disabled while a request is pending, and a second tap sends nothing', async () => {
  let resolve!: (r: ReturnType<typeof room>) => void;
  vi.mocked(openDM).mockReturnValue(new Promise((r) => (resolve = r)));
  render(NewRoom);
  await userEvent.type(screen.getByLabelText('Topic'), 'Trip{Enter}');
  await userEvent.click(screen.getByRole('checkbox', { name: 'Atlas' }));
  const atlas = screen.getByRole('button', { name: 'Atlas' });
  await userEvent.click(atlas);
  await userEvent.click(atlas);
  for (const b of screen.getAllByRole('button')) expect(b).toBeDisabled();
  await userEvent.type(screen.getByLabelText('Topic'), '{Enter}');
  expect(openDM).toHaveBeenCalledTimes(1);
  expect(createRoom).not.toHaveBeenCalled();
  resolve(room({ id: '01D' }));
  await vi.waitFor(() => expect(replace).toHaveBeenCalledWith('/rooms/01D'));
});

test('with no agents there are no buttons but Create, which is disabled', () => {
  store.agents = {};
  render(NewRoom);
  const buttons = screen.getAllByRole('button');
  expect(buttons).toHaveLength(1);
  expect(buttons[0]).toHaveTextContent('Create');
  expect(buttons[0]).toBeDisabled();
});

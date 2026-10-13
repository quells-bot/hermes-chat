import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { agent, message, room } from './lib/fixtures';
import { disconnect, store } from './lib/store.svelte';
import type { Message } from './lib/types';
import Composer from './Composer.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  postMessage: vi.fn(),
  steer: vi.fn(),
  markRoomRead: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  vi.mocked(api.postMessage).mockReset();
  vi.mocked(api.steer).mockReset();
  vi.mocked(api.markRoomRead).mockReset();
});

test('a failed send keeps the text, shows the error, and Retry reuses the client id', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  vi.mocked(api.postMessage).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'hello');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('cannot reach the server');
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'hello' }));
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  const ids = vi.mocked(api.postMessage).mock.calls.map((c) => c[2]);
  expect(ids[0]).toBe(ids[1]);
});

test('a room with nothing working offers no steer choice', () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  render(Composer, { id: 'room-1', replyTo: null });
  expect(screen.queryByRole('button', { name: 'Queue' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Steer' })).toBeNull();
});

test('while an agent works the composer offers Queue and Steer and defaults to Queue', () => {
  store.rooms['room-1'] = room({ id: 'room-1', agents: [agent({ working: true })] });
  render(Composer, { id: 'room-1', replyTo: null });
  expect(screen.getByRole('button', { name: 'Queue' })).toHaveAttribute('aria-pressed', 'true');
  expect(screen.getByRole('button', { name: 'Steer' })).toHaveAttribute('aria-pressed', 'false');
});

test('steering is not offered while the room is paused or everything is stopped', () => {
  store.rooms['room-1'] = room({ id: 'room-1', paused: true, agents: [agent({ working: true })] });
  const { unmount } = render(Composer, { id: 'room-1', replyTo: null });
  expect(screen.queryByRole('button', { name: 'Steer' })).toBeNull();
  unmount();
  store.rooms['room-1'] = room({ id: 'room-1', paused: false, agents: [agent({ working: true })] });
  store.halted = true;
  render(Composer, { id: 'room-1', replyTo: null });
  expect(screen.queryByRole('button', { name: 'Steer' })).toBeNull();
});

test('a steer sends one request per working agent', async () => {
  store.rooms['room-1'] = room({
    id: 'room-1',
    agents: [
      agent({ id: '01A', working: true }),
      agent({ id: '01B', working: true }),
    ],
  });
  vi.mocked(api.steer).mockResolvedValue(message({ id: '01M2', room_id: 'room-1', text: 'focus' }));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'focus');
  await userEvent.click(screen.getByRole('button', { name: 'Steer' }));
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  expect(api.steer).toHaveBeenCalledTimes(2);
  expect(api.steer).toHaveBeenCalledWith('room-1', '01A', 'focus', expect.stringMatching(/-0$/));
  expect(api.steer).toHaveBeenCalledWith('room-1', '01B', 'focus', expect.stringMatching(/-1$/));
  const ids = vi.mocked(api.steer).mock.calls.map((c) => c[3]);
  expect(ids[1]).toBe(ids[0].replace(/-0$/, '-1'));
});

test('send is disabled while blank and while pending', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  render(Composer, { id: 'room-1', replyTo: null });
  const input = screen.getByLabelText('Message');
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  await userEvent.type(input, 'hello');
  expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  let resolve!: (m: Message) => void;
  vi.mocked(api.postMessage).mockReturnValue(new Promise((r) => (resolve = r)));
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  resolve(message({ id: '01M', room_id: 'room-1', text: 'hello' }));
  await waitFor(() => expect(input).toHaveValue(''));
});

test('editing after a failure makes a new client id', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  vi.mocked(api.postMessage).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(Composer, { id: 'room-1', replyTo: null });
  const input = screen.getByLabelText('Message');
  await userEvent.type(input, 'hello');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await screen.findByRole('alert');
  await userEvent.type(input, ' world');
  expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled();
  await userEvent.type(input, '{Backspace}{Backspace}{Backspace}{Backspace}{Backspace}{Backspace}');
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'hello' }));
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(input).toHaveValue(''));
  const ids = vi.mocked(api.postMessage).mock.calls.map((c) => c[2]);
  expect(ids[1]).not.toBe(ids[0]);
});

test('a reply is quoted and can be cleared', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  const onclearReply = vi.fn();
  const replying = message({
    id: '01Q',
    text: 'a very long original line that starts like this',
    author: { kind: 'human', id: 'h', name: 'ann' },
  });
  render(Composer, { id: 'room-1', replyTo: replying, onclearReply });
  expect(screen.getByText('ann')).toBeInTheDocument();
  expect(screen.getByText(/a very long original/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Clear reply' }));
  expect(onclearReply).toHaveBeenCalledTimes(1);
});

test('a successful send clears the reply', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  const onclearReply = vi.fn();
  const replying = message({ id: '01Q', text: 'original', author: { kind: 'human', id: 'h', name: 'ann' } });
  render(Composer, { id: 'room-1', replyTo: replying, onclearReply });
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'ok' }));
  await userEvent.type(screen.getByLabelText('Message'), 'ok');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  expect(onclearReply).toHaveBeenCalledTimes(1);
});
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
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
  expect(api.steer).toHaveBeenCalledWith('room-1', '01A', 'focus', expect.stringMatching(/-01A$/));
  expect(api.steer).toHaveBeenCalledWith('room-1', '01B', 'focus', expect.stringMatching(/-01B$/));
  const ids = vi.mocked(api.steer).mock.calls.map((c) => c[3]);
  expect(ids[1]).toBe(ids[0].replace(/-01A$/, '-01B'));
});

test('a composer left on Steer posts once the turn has ended', async () => {
  store.rooms['room-1'] = room({ id: 'room-1', agents: [agent({ working: true })] });
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.click(screen.getByRole('button', { name: 'Steer' }));
  store.rooms['room-1'] = room({ id: 'room-1', agents: [agent({ working: false })] });
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'next' }));
  await userEvent.type(screen.getByLabelText('Message'), 'next');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  expect(api.postMessage).toHaveBeenCalledTimes(1);
  expect(api.steer).not.toHaveBeenCalled();
});

test('the text cannot be edited while a send is pending', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  render(Composer, { id: 'room-1', replyTo: null });
  const input = screen.getByLabelText('Message');
  await userEvent.type(input, 'hello');
  let resolve!: (m: Message) => void;
  vi.mocked(api.postMessage).mockReturnValue(new Promise((r) => (resolve = r)));
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(input).toHaveAttribute('readonly');
  await userEvent.type(input, ' more');
  expect(input).toHaveValue('hello');
  resolve(message({ id: '01M', room_id: 'room-1', text: 'hello' }));
  await waitFor(() => expect(input).toHaveValue(''));
  expect(input).not.toHaveAttribute('readonly');
});

test('a steer carries no reply: the quote is hidden and kept for the next post', async () => {
  store.rooms['room-1'] = room({ id: 'room-1', agents: [agent({ id: '01A', working: true })] });
  const onclearReply = vi.fn();
  const replying = message({ id: '01Q', text: 'original', author: { kind: 'human', id: 'h', name: 'ann' } });
  render(Composer, { id: 'room-1', replyTo: replying, onclearReply });
  expect(screen.getByText('ann')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Steer' }));
  expect(screen.queryByText('ann')).toBeNull();
  vi.mocked(api.steer).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'focus' }));
  await userEvent.type(screen.getByLabelText('Message'), 'focus');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  expect(onclearReply).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: 'Queue' }));
  expect(screen.getByText('ann')).toBeInTheDocument();
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
test('a failed steer retries with the same key for each agent', async () => {
  store.rooms['room-1'] = room({ id: 'room-1', agents: [agent({ id: '01A', working: true })] });
  vi.mocked(api.steer).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'focus');
  await userEvent.click(screen.getByRole('button', { name: 'Steer' }));
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await screen.findByRole('alert');
  vi.mocked(api.steer).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'focus' }));
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''));
  const keys = vi.mocked(api.steer).mock.calls.map((c) => c[3]);
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
});

test('a 401 keeps the text and shows no error: the person is being signed out', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  vi.mocked(api.postMessage).mockRejectedValue(new api.ApiError(401, 'unauthenticated', 'sign in'));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'hello');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled());
  expect(screen.queryByRole('alert')).toBeNull();
  expect(screen.getByLabelText('Message')).toHaveValue('hello');
});

test('choosing a message to reply to puts the focus in the composer', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  const view = render(Composer, { id: 'room-1', replyTo: null });
  expect(screen.getByLabelText('Message')).not.toHaveFocus();
  await view.rerender({ replyTo: message({ id: '01Q', text: 'original' }) });
  await waitFor(() => expect(screen.getByLabelText('Message')).toHaveFocus());
});

test('a send that lands says so, and a failed one does not', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  const onsent = vi.fn();
  vi.mocked(api.postMessage).mockRejectedValueOnce(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(Composer, { id: 'room-1', replyTo: null, onsent });
  await userEvent.type(screen.getByLabelText('Message'), 'hello');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await screen.findByRole('alert');
  expect(onsent).not.toHaveBeenCalled();
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'hello' }));
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(onsent).toHaveBeenCalledTimes(1));
});

test.each([
  ['Ctrl+Enter', '{Control>}{Enter}{/Control}'],
  ['⌘Enter', '{Meta>}{Enter}{/Meta}'],
])('%s sends', async (_name, keys) => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: 'hi' }));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'hi');
  await userEvent.keyboard(keys);
  await waitFor(() => expect(api.postMessage).toHaveBeenCalledTimes(1));
  expect(vi.mocked(api.postMessage).mock.calls[0][1]).toBe('hi');
});

test('Enter alone is a newline and sends nothing', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'a{Enter}b');
  expect(screen.getByLabelText('Message')).toHaveValue('a\nb');
  expect(api.postMessage).not.toHaveBeenCalled();
});

test('Ctrl+Enter with nothing typed sends nothing', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  render(Composer, { id: 'room-1', replyTo: null });
  screen.getByLabelText('Message').focus();
  await userEvent.keyboard('{Control>}{Enter}{/Control}');
  expect(api.postMessage).not.toHaveBeenCalled();
});

test('Ctrl+Enter while a send is in flight sends nothing more', async () => {
  store.rooms['room-1'] = room({ id: 'room-1' });
  vi.mocked(api.postMessage).mockReturnValue(new Promise(() => {}));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'hi');
  await userEvent.keyboard('{Control>}{Enter}{Enter}{/Control}');
  expect(api.postMessage).toHaveBeenCalledTimes(1);
});

function channel() {
  store.rooms['room-1'] = room({
    id: 'room-1',
    flavor: 'channel',
    agents: [
      agent({ id: '01S', handle: 'scout', display_name: 'Scout' }),
      agent({ id: '01A', handle: 'atlas', display_name: 'Atlas' }),
    ],
  });
}

test('typing @ in a channel lists the room’s agents', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@');
  const options = screen.getAllByRole('option');
  expect(options).toHaveLength(2);
  expect(options[0]).toHaveAttribute('aria-selected', 'true');
  expect(options[1]).toHaveAttribute('aria-selected', 'false');
  expect(screen.getByRole('listbox', { name: 'Mention' })).toBeInTheDocument();
});

test('the list narrows as the handle is typed', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@sc');
  expect(screen.getAllByRole('option')).toHaveLength(1);
});

test('Enter inserts the chosen handle and sends nothing', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@sc{Enter}');
  expect(screen.getByLabelText('Message')).toHaveValue('@scout ');
  expect(api.postMessage).not.toHaveBeenCalled();
  expect(screen.queryByRole('listbox')).toBeNull();
});

test('Tab inserts too', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@sc{Tab}');
  expect(screen.getByLabelText('Message')).toHaveValue('@scout ');
});

test('↓ then Enter inserts the second', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@{ArrowDown}');
  expect(screen.getAllByRole('option')[1]).toHaveAttribute('aria-selected', 'true');
  await userEvent.keyboard('{Enter}');
  expect(screen.getByLabelText('Message')).toHaveValue('@scout ');
});

test('↑ wraps to the last', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@{ArrowUp}');
  expect(screen.getAllByRole('option')[1]).toHaveAttribute('aria-selected', 'true');
});

test('a tap inserts and the box has the focus again', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@');
  await userEvent.click(screen.getAllByRole('option')[1]);
  const box = screen.getByLabelText('Message') as HTMLTextAreaElement;
  expect(box).toHaveValue('@scout ');
  await waitFor(() => expect(box).toHaveFocus());
  expect(box.selectionStart).toBe(7);
});

test('Escape puts the list away and Enter is a newline again', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  const box = screen.getByLabelText('Message');
  await userEvent.type(box, '@sc{Escape}');
  expect(screen.queryByRole('listbox')).toBeNull();
  await userEvent.keyboard('{Enter}');
  expect(box).toHaveValue('@sc\n');
  await userEvent.type(box, ' @');
  expect(screen.getByRole('listbox')).toBeInTheDocument();
});

test('Enter with no candidate is a newline', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@zz{Enter}');
  expect(screen.getByLabelText('Message')).toHaveValue('@zz\n');
});

test('text after the caret survives', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  const box = screen.getByLabelText('Message') as HTMLTextAreaElement;
  await userEvent.type(box, 'x');
  await userEvent.clear(box);
  box.value = '@sc now';
  await fireEvent.input(box);
  box.setSelectionRange(3, 3);
  await fireEvent.keyUp(box);
  await userEvent.keyboard('{Enter}');
  expect(box).toHaveValue('@scout now');
});

test('Ctrl+Enter sends while the list is open', async () => {
  channel();
  vi.mocked(api.postMessage).mockResolvedValue(message({ id: '01M', room_id: 'room-1', text: '@sc' }));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@sc');
  await userEvent.keyboard('{Control>}{Enter}{/Control}');
  await waitFor(() => expect(api.postMessage).toHaveBeenCalledTimes(1));
  expect(vi.mocked(api.postMessage).mock.calls[0][1]).toBe('@sc');
});

test.each(['dm', 'conversation'] as const)('no list in a %s', async (flavor) => {
  store.rooms['room-1'] = room({ id: 'room-1', flavor, agents: [agent({ handle: 'scout' })] });
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@');
  expect(screen.queryByRole('listbox')).toBeNull();
});

test('an agent removed from the room leaves the list', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@');
  expect(screen.getAllByRole('option')).toHaveLength(2);
  store.rooms['room-1'] = room({
    id: 'room-1',
    flavor: 'channel',
    agents: [agent({ id: '01A', handle: 'atlas', display_name: 'Atlas' })],
  });
  await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(1));
});

test('an address is not a mention', async () => {
  channel();
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), 'kai@example.com');
  expect(screen.queryByRole('listbox')).toBeNull();
});

test('completing drops a failed attempt’s id', async () => {
  channel();
  vi.mocked(api.postMessage).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(Composer, { id: 'room-1', replyTo: null });
  await userEvent.type(screen.getByLabelText('Message'), '@sc');
  await userEvent.click(screen.getByRole('button', { name: 'Send' }));
  await screen.findByRole('alert');
  expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  await userEvent.click(screen.getByRole('option'));
  expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument();
});

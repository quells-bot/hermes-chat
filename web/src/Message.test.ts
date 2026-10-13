import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { ApiError, cancelMessage, getMessage } from './lib/api';
import { agent, message } from './lib/fixtures';
import { session } from './lib/session.svelte';
import { disconnect, store } from './lib/store.svelte';
import type { Delivery, Prompt } from './lib/types';
import Icon from './Icon.svelte';
import Message from './Message.svelte';

vi.mock('./lib/api', async (orig) => ({
  ...(await orig<typeof import('./lib/api')>()),
  getMessage: vi.fn(),
  cancelMessage: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  vi.mocked(getMessage).mockReset();
  vi.mocked(cancelMessage).mockReset();
  // The fixture author signs the person in as kai.
  session.me = { id: 'human-1', username: 'kai' };
});

test('shows the author, the time and the rendered text', () => {
  const { container } = render(Message, {
    message: message({ text: 'a **bold** word', created_at: '2020-03-04T05:06:00.000Z' }),
  });
  expect(screen.getByText('kai')).toBeInTheDocument();
  const time = container.querySelector('time');
  expect(time).toHaveAttribute('datetime', '2020-03-04T05:06:00.000Z');
  expect(time?.textContent).toMatch(/\d\d:\d\d$/);
  expect(screen.getByText('bold').tagName).toBe('STRONG');
});

test('a removed author reads removed and still gets an icon', () => {
  const { container } = render(Message, { message: message({ author: null }) });
  expect(screen.getByText('removed')).toBeInTheDocument();
  expect(container.querySelector('svg')).not.toBeNull();
});

test("an agent's icon comes from its handle in the directory", () => {
  store.agents['01A'] = agent({ id: '01A', handle: 'atlas', display_name: 'Display' });
  const { container } = render(Message, {
    message: message({ author: { kind: 'agent', id: '01A', name: 'Display' } }),
  });
  const expected = render(Icon, { name: 'atlas' }).container.querySelector('svg');
  expect(container.querySelector('svg')?.outerHTML).toBe(expected?.outerHTML);
});

test('a streaming message is marked as being written, and a finished one is not', () => {
  const { unmount } = render(Message, { message: message({ streaming: true }) });
  expect(screen.getByLabelText('writing')).toHaveTextContent('…');
  unmount();
  render(Message, { message: message({ streaming: false }) });
  expect(screen.queryByLabelText('writing')).toBeNull();
});

test('a reply quotes a message in the feed', () => {
  store.feeds['room-1'] = {
    messages: [message({ id: 'q1', text: 'the  original\nline', author: { kind: 'human', id: 'h2', name: 'ann' } })],
    more: false,
    loading: false,
    error: '',
  };
  render(Message, { message: message({ id: 'm2', reply_to_id: 'q1', text: 'answer' }) });
  expect(screen.getByText('ann')).toBeInTheDocument();
  expect(screen.getByText('the original line')).toBeInTheDocument();
  expect(getMessage).not.toHaveBeenCalled();
});

test('a reply to a message outside the feed fetches it once and then quotes it', async () => {
  vi.mocked(getMessage).mockResolvedValue(
    message({ id: 'q1', text: 'old words', author: { kind: 'human', id: 'h2', name: 'ann' } }),
  );
  render(Message, { message: message({ id: 'm2', reply_to_id: 'q1', text: 'answer' }) });
  expect(screen.getByText('reply')).toBeInTheDocument();
  await waitFor(() => expect(screen.getByText('old words')).toBeInTheDocument());
  expect(screen.getByText('ann')).toBeInTheDocument();
  expect(getMessage).toHaveBeenCalledTimes(1);
  expect(getMessage).toHaveBeenCalledWith('room-1', 'q1');
});

test('a reply whose message cannot be fetched reads reply', async () => {
  vi.mocked(getMessage).mockRejectedValue(new Error('gone'));
  render(Message, { message: message({ id: 'm2', reply_to_id: 'q9', text: 'answer' }) });
  await waitFor(() => expect(getMessage).toHaveBeenCalled());
  expect(screen.getByText('reply')).toBeInTheDocument();
});

test('waiting, delivered and cancelled copies are stated; working, done and failed are not', () => {
  const states = ['waiting', 'delivered', 'cancelled', 'working', 'done', 'failed'] as const;
  const deliveries: Delivery[] = states.map((state, i) => ({
    agent: { id: `a${i}`, handle: `h${i}`, display_name: `Agent${i}`, revoked: false },
    state,
  }));
  const { container } = render(Message, { message: message({ deliveries }) });
  expect(screen.getByText('Agent0 · waiting')).toBeInTheDocument();
  expect(screen.getByText('Agent1 · delivered')).toBeInTheDocument();
  expect(screen.getByText('Agent2 · cancelled')).toBeInTheDocument();
  const lines = [...container.querySelectorAll('.state')].map((el) => el.textContent ?? '');
  expect(lines).toHaveLength(3);
  for (const line of lines) expect(line).not.toMatch(/Agent[345]/);
});

test('a prompt renders as a card, not as a plain message', () => {
  const prompt: Prompt = {
    kind: 'approval',
    choices: ['Yes', 'No'],
    state: 'open',
    answer: null,
    answered_by: null,
    closed_at: null,
  };
  render(Message, {
    message: message({ text: 'Deploy now?', prompt, author: { kind: 'agent', id: 'a', name: 'Atlas' } }),
  });
  expect(screen.getByText('Atlas asks')).toBeInTheDocument();
  expect(screen.getByText('Deploy now?')).toBeInTheDocument();
  expect(screen.getAllByText('Deploy now?')).toHaveLength(1);
});

test('an image attachment shows inline from the server, and anything else is a download', () => {
  render(Message, {
    message: message({
      room_id: '01R',
      text: '',
      attachments: [
        { id: '01A', name: 'chart.png', content_type: 'image/png', size: 2048, image: true },
        { id: '01B', name: '"><img src=x>.png', content_type: 'application/octet-stream', size: 1_500_000, image: false },
      ],
    }),
  });
  expect(screen.getByRole('img', { name: 'chart.png' })).toHaveAttribute('src', '/api/rooms/01R/attachments/01A');
  const file = screen.getByRole('link', { name: '"><img src=x>.png' });
  expect(file).toHaveAttribute('download', '"><img src=x>.png');
  expect(file).toHaveAttribute('href', '/api/rooms/01R/attachments/01B');
  expect(screen.getByText('1.4 MB')).toBeInTheDocument();
  expect(screen.getAllByRole('img')).toHaveLength(1);
});

test('working, done and failed copies are activity blocks', () => {
  const states = ['waiting', 'working', 'done', 'failed'] as const;
  const deliveries: Delivery[] = states.map((state, i) => ({
    agent: { id: `a${i}`, handle: `h${i}`, display_name: `Agent${i}`, revoked: false },
    state,
  }));
  // Not kai, so no Cancel button is offered on the waiting copy.
  render(Message, {
    message: message({ author: { kind: 'human', id: 'h2', name: 'ann' }, deliveries }),
  });
  for (const name of ['Agent1 · working', 'Agent2 · done', 'Agent3 · failed']) {
    expect(screen.getByRole('button', { name })).toHaveAttribute('aria-expanded', 'false');
  }
  expect(screen.getAllByRole('button')).toHaveLength(3);
});

test('Cancel appears on my own waiting message and cancels it', async () => {
  const waiting: Delivery[] = [{ agent: agent(), state: 'waiting' }];
  render(Message, { message: message({ deliveries: waiting }) });
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(cancelMessage).toHaveBeenCalledWith('room-1', 'msg-1');
});

test('a reply button appears when asked and calls back', async () => {
  const onreply = vi.fn();
  render(Message, { message: message(), onreply });
  await userEvent.click(screen.getByRole('button', { name: 'Reply' }));
  expect(onreply).toHaveBeenCalledTimes(1);
});

test("Cancel is not offered on someone else's message", () => {
  render(Message, {
    message: message({
      author: { kind: 'human', id: 'h2', name: 'ann' },
      deliveries: [{ agent: agent(), state: 'waiting' }],
    }),
  });
  expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
});

test('Cancel is not offered without a waiting copy', () => {
  render(Message, { message: message({ deliveries: [{ agent: agent(), state: 'working' }] }) });
  expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
});

test('Cancel is disabled while pending', async () => {
  let release!: () => void;
  vi.mocked(cancelMessage).mockReturnValue(new Promise((r) => (release = r)));
  render(Message, { message: message({ deliveries: [{ agent: agent(), state: 'waiting' }] }) });
  const button = screen.getByRole('button', { name: 'Cancel' });
  await userEvent.click(button);
  expect(button).toBeDisabled();
  release();
  await waitFor(() => expect(button).toBeEnabled());
});

test('a failed cancel lands in store.error and a nothing_waiting one does not', async () => {
  vi.mocked(cancelMessage).mockRejectedValue(new ApiError(500, 'boom', 'server gone'));
  render(Message, { message: message({ deliveries: [{ agent: agent(), state: 'waiting' }] }) });
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(store.error).toBe('server gone'));

  vi.mocked(cancelMessage).mockRejectedValue(new ApiError(409, 'nothing_waiting', 'nothing waiting'));
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(store.error).toBe('server gone'));
});

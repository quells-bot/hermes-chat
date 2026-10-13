import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { ApiError, answerPrompt } from './lib/api';
import { message } from './lib/fixtures';
import type { Prompt } from './lib/types';
import PromptCard from './PromptCard.svelte';

vi.mock('./lib/api', async (orig) => ({
  ...(await orig<typeof import('./lib/api')>()),
  answerPrompt: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  vi.mocked(answerPrompt).mockReset();
});

function card(p: Partial<Prompt>) {
  const prompt: Prompt = {
    kind: 'clarify',
    choices: [],
    state: 'open',
    answer: null,
    answered_by: null,
    closed_at: null,
    ...p,
  };
  return render(PromptCard, {
    message: message({ text: 'Which one?', prompt, author: { kind: 'agent', id: 'a', name: 'Atlas' } }),
  });
}

test('an open approval offers its choices as buttons and answers the tapped one', async () => {
  vi.mocked(answerPrompt).mockResolvedValue(message({}));
  card({ kind: 'approval', choices: ['Once', 'Always'] });
  await userEvent.click(screen.getByRole('button', { name: 'Once' }));
  expect(answerPrompt).toHaveBeenCalledWith('room-1', 'msg-1', 'Once');
});

test('an open clarify takes typed words', async () => {
  vi.mocked(answerPrompt).mockResolvedValue(message({}));
  card({});
  await userEvent.type(screen.getByPlaceholderText('Your answer'), '  go now  ');
  await userEvent.click(screen.getByRole('button', { name: 'Answer' }));
  expect(answerPrompt).toHaveBeenCalledWith('room-1', 'msg-1', 'go now');
});

test('a choice button answers too, for a clarify', async () => {
  vi.mocked(answerPrompt).mockResolvedValue(message({}));
  card({ choices: ['Maybe', 'Skip'] });
  await userEvent.click(screen.getByRole('button', { name: 'Skip' }));
  expect(answerPrompt).toHaveBeenCalledWith('room-1', 'msg-1', 'Skip');
});

test('Answer is disabled while blank or pending', async () => {
  let release!: () => void;
  vi.mocked(answerPrompt).mockReturnValue(new Promise((res) => (release = () => res(message({})))));
  card({});
  const answer = screen.getByRole('button', { name: 'Answer' });
  expect(answer).toBeDisabled();
  await userEvent.type(screen.getByPlaceholderText('Your answer'), 'ok');
  expect(answer).toBeEnabled();
  await userEvent.click(answer);
  expect(answer).toBeDisabled();
  release();
  await waitFor(() => expect(answer).toBeEnabled());
});

test('a double tap sends one request', async () => {
  let release!: () => void;
  vi.mocked(answerPrompt).mockReturnValue(new Promise((res) => (release = () => res(message({})))));
  card({ kind: 'approval', choices: ['Once', 'Always'] });
  const once = screen.getByRole('button', { name: 'Once' });
  await userEvent.click(once);
  await userEvent.click(once);
  expect(answerPrompt).toHaveBeenCalledTimes(1);
  release();
});

test('a 409 shows the error and re-enables', async () => {
  vi.mocked(answerPrompt).mockRejectedValue(new ApiError(409, 'prompt_closed', 'This prompt has closed.'));
  card({ kind: 'approval', choices: ['Once', 'Always'] });
  const once = screen.getByRole('button', { name: 'Once' });
  await userEvent.click(once);
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('This prompt has closed.'));
  expect(once).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Always' })).toBeEnabled();
});

test('answered keeps the buttons, disabled, and names who answered', () => {
  card({ state: 'answered', answer: 'Red', answered_by: { id: 'h', username: 'kai' }, choices: ['Red', 'Blue'] });
  expect(screen.getByText('Answered: Red by kai')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Red' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Blue' })).toBeDisabled();
});

test('expired keeps the buttons disabled', () => {
  card({ state: 'expired', choices: ['Red', 'Blue'] });
  expect(screen.getByText('Expired')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Red' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Blue' })).toBeDisabled();
});

test('an open prompt with no choices shows no buttons, and a clarify shows the input', () => {
  const first = card({ kind: 'approval' });
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByPlaceholderText('Your answer')).toBeNull();
  first.unmount();
  card({});
  expect(screen.getByPlaceholderText('Your answer')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Answer' })).toBeInTheDocument();
  expect(screen.getAllByRole('button')).toHaveLength(1);
});

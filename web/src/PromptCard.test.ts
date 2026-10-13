import { render, screen } from '@testing-library/svelte';
import { expect, test } from 'vitest';
import { message } from './lib/fixtures';
import type { Prompt } from './lib/types';
import PromptCard from './PromptCard.svelte';

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

test('an open prompt lists its choices as text and has no button', () => {
  card({ choices: ['Red', 'Blue'] });
  expect(screen.getByText('Atlas asks')).toBeInTheDocument();
  expect(screen.getByText('Which one?')).toBeInTheDocument();
  expect(screen.getByText('Red · Blue')).toBeInTheDocument();
  expect(screen.queryByRole('button')).toBeNull();
});

test('an open prompt with no choices shows none', () => {
  const { container } = card({});
  expect(container.querySelector('.choices')).toBeNull();
});

test('an answered prompt says what was answered and by whom', () => {
  card({ state: 'answered', answer: 'Red', answered_by: { id: 'h', username: 'kai' } });
  expect(screen.getByText('Answered: Red by kai')).toBeInTheDocument();
});

test('an answered prompt whose human was removed names nobody', () => {
  card({ state: 'answered', answer: 'Red', answered_by: null });
  expect(screen.getByText('Answered: Red')).toBeInTheDocument();
});

test('an expired prompt says so', () => {
  card({ state: 'expired', choices: ['Red'] });
  expect(screen.getByText('Expired')).toBeInTheDocument();
  expect(screen.queryByText('Red')).toBeNull();
});

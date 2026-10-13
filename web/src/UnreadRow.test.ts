import { render, screen } from '@testing-library/svelte';
import { beforeEach, expect, test, vi } from 'vitest';
import { room } from './lib/fixtures';
import { store } from './lib/store.svelte';
import UnreadRow from './UnreadRow.svelte';

vi.mock('./lib/store.svelte', () => ({ store: { rooms: {} } }));

beforeEach(() => {
  store.rooms = {};
});

test('nothing unread renders nothing', () => {
  store.rooms = { a: room({ id: 'a', last_message_id: '01M1', last_read_message_id: '01M1' }) };
  const { container } = render(UnreadRow);
  expect(container.textContent?.trim()).toBe('');
  expect(screen.queryByRole('link')).toBeNull();
});

test('shows the count and the names in Unread-page order', () => {
  store.rooms = {
    a: room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }),
    b: room({ id: 'b', topic: 'Heat pump', last_message_id: '01M5' }),
    c: room({ id: 'c', topic: 'Web UI', last_message_id: '01M1', open_prompts: 1 }),
    d: room({ id: 'd', topic: 'Read', last_message_id: '01M2', last_read_message_id: '01M2' }),
  };
  render(UnreadRow);
  const link = screen.getByRole('link');
  expect(link).toHaveTextContent('Web UI, Trip, Heat pump');
  expect(link).toHaveTextContent('3');
  expect(link).not.toHaveTextContent('Read');
});

test.each([
  [[0, 0], null],
  [[1, 0], '1 question'],
  [[1, 2], '3 questions'],
])('shows 1 question, 2 questions, and no tag for none: %j', (prompts, tag) => {
  store.rooms = {
    a: room({ id: 'a', last_message_id: '01M1', open_prompts: prompts[0] }),
    b: room({ id: 'b', last_message_id: '01M2', open_prompts: prompts[1] }),
  };
  render(UnreadRow);
  const link = screen.getByRole('link');
  if (tag === null) {
    expect(link.textContent).not.toMatch(/question/);
  } else {
    expect(link).toHaveTextContent(tag);
  }
});

test('links to #/unread', () => {
  store.rooms = { a: room({ id: 'a', last_message_id: '01M1' }) };
  render(UnreadRow);
  expect(screen.getByRole('link')).toHaveAttribute('href', '#/unread');
});

import { render, screen, within } from '@testing-library/svelte';
import { expect, test } from 'vitest';
import { agent, room } from './lib/fixtures';
import RoomRow from './RoomRow.svelte';

test('an unread room has a dot and a prompt count', () => {
  render(RoomRow, { room: room({ topic: 'Trip', last_message_id: '01B', open_prompts: 2 }) });
  expect(screen.getByRole('listitem')).toHaveClass('unread');
  expect(screen.getByLabelText('unread')).toBeInTheDocument();
  expect(screen.getByLabelText('2 open prompts')).toHaveTextContent('2');
});

test('a read room has no dot and no badges', () => {
  render(RoomRow, { room: room({ topic: 'Trip' }) });
  expect(screen.getByRole('listitem')).not.toHaveClass('unread');
  expect(screen.queryByLabelText('unread')).toBeNull();
  expect(screen.queryByText('working')).toBeNull();
  expect(screen.queryByText('paused')).toBeNull();
  expect(screen.getByText('#')).toBeInTheDocument();
});

test("a DM shows the agent's name, an icon, and connected or offline", () => {
  const dm = room({
    flavor: 'dm',
    topic: '',
    agents: [agent({ handle: 'atlas', display_name: 'Atlas', connected: true })],
  });
  const { unmount } = render(RoomRow, { room: dm });
  const item = screen.getByRole('listitem');
  expect(within(item).getByText('Atlas')).toBeInTheDocument();
  expect(item.querySelector('svg')).not.toBeNull();
  expect(within(item).getByLabelText('connected')).toBeInTheDocument();
  expect(within(item).queryByText('#')).toBeNull();
  unmount();

  render(RoomRow, { room: { ...dm, agents: [agent({ handle: 'atlas', display_name: 'Atlas' })] } });
  expect(screen.getByLabelText('offline')).toBeInTheDocument();
});

test('working and paused badges', () => {
  render(RoomRow, {
    room: room({ paused: true, agents: [agent({ working: true })] }),
  });
  expect(screen.getByText('working')).toBeInTheDocument();
  expect(screen.getByText('paused')).toBeInTheDocument();
});

test('a room with no open prompts has no count', () => {
  render(RoomRow, { room: room({ open_prompts: 0 }) });
  expect(screen.queryByLabelText(/open prompts/)).toBeNull();
});

test('a 200-character topic renders in one element with the badges after it', () => {
  const topic = 'x'.repeat(200);
  render(RoomRow, { room: room({ topic, open_prompts: 1, paused: true }) });
  const name = screen.getByText(topic);
  expect(name.tagName).toBe('SPAN');
  const badge = screen.getByLabelText('1 open prompts');
  expect(name.compareDocumentPosition(badge) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  const paused = screen.getByText('paused');
  expect(badge.compareDocumentPosition(paused) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { note as fixture, noteEntry } from './lib/fixtures';
import { disconnect } from './lib/store.svelte';
import NoteEntry from './NoteEntry.svelte';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  getNote: vi.fn(),
  setNoteText: vi.fn(),
  updateNoteEntry: vi.fn(),
  deleteNoteEntry: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

beforeEach(() => {
  disconnect();
  vi.mocked(api.updateNoteEntry).mockReset().mockResolvedValue(fixture());
  vi.mocked(api.deleteNoteEntry).mockReset().mockResolvedValue(fixture());
});

const names = () => screen.getAllByRole('button').map((b) => b.textContent);

test('a pending entry offers Approve, Edit and Delete', () => {
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror: vi.fn() });
  expect(names()).toEqual(['Approve', 'Edit', 'Delete']);
  expect(screen.getByText('Helper')).toBeInTheDocument();
  expect(screen.getByText('likes tea')).toBeInTheDocument();
});

test('an approved entry offers Unapprove, Edit and Delete', () => {
  render(NoteEntry, { entry: noteEntry({ approved: true }), disabled: false, onerror: vi.fn() });
  expect(names()).toEqual(['Unapprove', 'Edit', 'Delete']);
});

test('Approve, Unapprove and Delete each make their call with the entry’s id', async () => {
  const onerror = vi.fn();
  const { unmount } = render(NoteEntry, { entry: noteEntry({ id: 'e9' }), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Approve' }));
  expect(api.updateNoteEntry).toHaveBeenLastCalledWith('e9', { approved: true });
  await waitFor(() => expect(onerror).toHaveBeenLastCalledWith(''));
  unmount();

  const second = render(NoteEntry, { entry: noteEntry({ id: 'e9', approved: true }), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Unapprove' }));
  expect(api.updateNoteEntry).toHaveBeenLastCalledWith('e9', { approved: false });
  second.unmount();

  render(NoteEntry, { entry: noteEntry({ id: 'e9' }), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
  expect(api.deleteNoteEntry).toHaveBeenCalledWith('e9');
});

test('every button is disabled when the entry is', () => {
  render(NoteEntry, { entry: noteEntry(), disabled: true, onerror: vi.fn() });
  for (const b of screen.getAllByRole('button')) {
    expect(b).toBeDisabled();
  }
});

test('a refused call reports why', async () => {
  vi.mocked(api.deleteNoteEntry).mockRejectedValue(new api.ApiError(404, 'not_found', 'gone'));
  const onerror = vi.fn();
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(onerror).toHaveBeenCalledWith('gone'));
});

test('Edit saves the trimmed text', async () => {
  const onerror = vi.fn();
  render(NoteEntry, { entry: noteEntry({ id: 'e1' }), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
  const box = screen.getByRole('textbox', { name: 'Entry' });
  expect(box).toHaveValue('likes tea');
  await userEvent.clear(box);
  await userEvent.type(box, '  likes coffee  ');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));

  expect(api.updateNoteEntry).toHaveBeenCalledWith('e1', { text: 'likes coffee' });
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Entry' })).toBeNull());
  expect(onerror).toHaveBeenLastCalledWith('');
});

test('Cancel leaves the entry as it was', async () => {
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror: vi.fn() });
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
  await userEvent.type(screen.getByRole('textbox', { name: 'Entry' }), ' more');
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

  expect(api.updateNoteEntry).not.toHaveBeenCalled();
  expect(screen.queryByRole('textbox', { name: 'Entry' })).toBeNull();
  expect(screen.getByText('likes tea')).toBeInTheDocument();
});

test('Save is disabled when the edit is empty or unchanged', async () => {
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror: vi.fn() });
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  await userEvent.clear(screen.getByRole('textbox', { name: 'Entry' }));
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});

test('a refused edit stays open with the typing and reports why', async () => {
  vi.mocked(api.updateNoteEntry).mockRejectedValue(new api.ApiError(400, 'validation', 'would exceed 4000 characters'));
  const onerror = vi.fn();
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror });
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
  await userEvent.type(screen.getByRole('textbox', { name: 'Entry' }), '!');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() => expect(onerror).toHaveBeenCalledWith('would exceed 4000 characters'));
  expect(screen.getByRole('textbox', { name: 'Entry' })).toHaveValue('likes tea!');
});

test('an entry of 301 characters cannot be saved', async () => {
  render(NoteEntry, { entry: noteEntry(), disabled: false, onerror: vi.fn() });
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
  const box = screen.getByRole('textbox', { name: 'Entry' });
  await userEvent.clear(box);
  await userEvent.click(box);
  await userEvent.paste('x'.repeat(300));
  expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  expect(screen.getByText('300 / 300')).toBeInTheDocument();
  await userEvent.paste('x');
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  expect(screen.getByText('301 / 300')).toHaveClass('over');
});

test('a hostile entry is text', () => {
  const texts = [
    '<img src=x onerror=alert(1)>',
    '[x](javascript:alert(1))',
    'x'.repeat(300),
    'one\n\ntwo\n<script>alert(1)</script>',
  ];
  const { container } = render(NoteEntry, {
    entry: noteEntry({ text: texts.join('\n') }),
    disabled: false,
    onerror: vi.fn(),
  });
  expect(container.querySelector('img, a, script, [onerror]')).toBeNull();
  expect(container.textContent).toContain('<img src=x onerror=alert(1)>');
  expect(container.textContent).toContain('[x](javascript:alert(1))');
});

import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import { note as fixture, noteEntry } from './lib/fixtures';
import { loadNote, maxApproved, maxText, note } from './lib/note.svelte';
import { disconnect, store } from './lib/store.svelte';
import type { Note as NoteData } from './lib/types';
import Note from './Note.svelte';

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
  // The screen fetches the note when it opens; unless a test says otherwise
  // the server answers with the note the test holds.
  vi.mocked(api.getNote)
    .mockReset()
    .mockImplementation(async () => JSON.parse(JSON.stringify(note.value)) as NoteData);
  vi.mocked(api.setNoteText).mockReset();
  vi.mocked(api.updateNoteEntry).mockReset();
  vi.mocked(api.deleteNoteEntry).mockReset();
});

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const box = () => screen.getByRole('textbox', { name: 'Note' });
const save = () => screen.getAllByRole('button', { name: 'Save' })[0];

test('typing survives a reload of the note', async () => {
  note.value = fixture({ text: 'old' });
  render(Note);
  const box = screen.getByRole('textbox', { name: 'Note' });
  await userEvent.clear(box);
  await userEvent.type(box, 'half a sentence');
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'changed elsewhere' }));
  await loadNote();
  expect(box).toHaveValue('half a sentence');
});

test('an untouched box follows a reload', async () => {
  note.value = fixture({ text: 'old' });
  render(Note);
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'changed elsewhere' }));
  await loadNote();
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Note' })).toHaveValue('changed elsewhere'));
});

test('a refused save shows the server’s words and keeps the typing', async () => {
  note.value = fixture();
  vi.mocked(api.setNoteText).mockRejectedValue(
    new api.ApiError(400, 'validation', 'the approved note would exceed 4000 characters; shorten or delete something first'),
  );
  render(Note);
  await userEvent.type(screen.getByRole('textbox', { name: 'Note' }), 'mine');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('would exceed 4000 characters');
  expect(screen.getByRole('textbox', { name: 'Note' })).toHaveValue('mine');
});

test('a refusal can be dismissed', async () => {
  note.value = fixture();
  vi.mocked(api.setNoteText).mockRejectedValue(new api.ApiError(400, 'validation', 'nope'));
  render(Note);
  await userEvent.type(box(), 'x');
  await userEvent.click(save());
  await screen.findByRole('alert');
  await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.queryByRole('alert')).toBeNull();
});

test('Save is disabled until the text differs', async () => {
  note.value = fixture({ text: 'old' });
  render(Note);
  expect(save()).toBeDisabled();

  await userEvent.type(box(), '   ');
  expect(save()).toBeDisabled();

  await userEvent.type(box(), 'er');
  expect(save()).toBeEnabled();
});

test('a text of 2,000 characters can be saved and one of 2,001 cannot', async () => {
  note.value = fixture();
  render(Note);

  await fireEvent.input(box(), { target: { value: '👍'.repeat(maxText) } });
  expect(save()).toBeEnabled();
  expect(screen.getByText(`${maxText} / ${maxText}`)).not.toHaveClass('over');

  await fireEvent.input(box(), { target: { value: '👍'.repeat(maxText + 1) } });
  expect(save()).toBeDisabled();
  expect(screen.getByText(`${maxText + 1} / ${maxText}`)).toHaveClass('over');
});

test('a saved text clears the draft', async () => {
  note.value = fixture({ text: 'old' });
  vi.mocked(api.setNoteText).mockResolvedValue(fixture({ text: 'new text' }));
  render(Note);
  await userEvent.clear(box());
  await userEvent.type(box(), 'new text');
  await userEvent.click(save());

  expect(api.setNoteText).toHaveBeenCalledWith('new text');
  await waitFor(() => expect(save()).toBeDisabled());
  expect(box()).toHaveValue('new text');

  // a box still holding a draft would ignore this reload; a cleared one follows it
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'changed elsewhere' }));
  await loadNote();
  await waitFor(() => expect(box()).toHaveValue('changed elsewhere'));
});

test('pending entries sit under To review and approved ones under Approved', () => {
  note.value = fixture();
  const { unmount } = render(Note);
  expect(screen.queryByRole('heading', { name: 'To review' })).toBeNull();
  expect(screen.queryByRole('heading', { name: 'Approved' })).toBeNull();
  unmount();

  note.value = fixture({
    entries: [
      noteEntry({ id: 'e1', text: 'waiting' }),
      noteEntry({ id: 'e2', text: 'kept', approved: true }),
    ],
  });
  render(Note);
  expect(screen.getByRole('heading', { name: 'To review' })).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: 'Approved' })).toBeInTheDocument();
  expect(screen.getByText('waiting')).toBeInTheDocument();
  expect(screen.getByText('kept')).toBeInTheDocument();
});

test('only one list is drawn when the other is empty', () => {
  note.value = fixture({ entries: [noteEntry({ text: 'kept', approved: true })] });
  render(Note);
  expect(screen.queryByRole('heading', { name: 'To review' })).toBeNull();
  expect(screen.getByRole('heading', { name: 'Approved' })).toBeInTheDocument();
});

test('the total counts the draft and the approved entries', async () => {
  note.value = fixture({
    text: 'abc',
    entries: [
      noteEntry({ id: 'e1', text: 'twelve chars', approved: true }),
      noteEntry({ id: 'e2', text: 'not counted' }),
    ],
  });
  render(Note);
  expect(screen.getByText(`15 / ${maxApproved}`)).toBeInTheDocument();

  await userEvent.type(box(), 'de');
  expect(screen.getByText(`17 / ${maxApproved}`)).toBeInTheDocument();
});

test('the total is marked over past the limit', async () => {
  note.value = fixture({ entries: [noteEntry({ text: 'x'.repeat(300), approved: true })] });
  render(Note);
  await fireEvent.input(box(), { target: { value: 'y'.repeat(maxText) } });
  expect(screen.getByText(`${maxText + 300} / ${maxApproved}`)).not.toHaveClass('over');
  note.value = fixture({
    entries: Array.from({ length: 14 }, (_, i) => noteEntry({ id: `e${i}`, text: 'x'.repeat(300), approved: true })),
  });
  await waitFor(() => expect(screen.getByText(`${maxText + 4200} / ${maxApproved}`)).toHaveClass('over'));
});

test('a load that failed shows why and Retry loads again', async () => {
  vi.mocked(api.getNote).mockRejectedValueOnce(new Error('could not load the note'));
  render(Note);
  expect(await screen.findByRole('alert')).toHaveTextContent('could not load the note');
  expect(screen.queryByRole('textbox', { name: 'Note' })).toBeNull();

  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'here' }));
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(api.getNote).toHaveBeenCalledTimes(2);
  expect(await screen.findByRole('textbox', { name: 'Note' })).toHaveValue('here');
});

test('a store error is shown and Retry loads again', async () => {
  note.value = fixture();
  store.error = 'could not stop everything';
  render(Note);
  expect(screen.getByRole('alert')).toHaveTextContent('could not stop everything');

  vi.mocked(api.getNote).mockClear();
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  await waitFor(() => expect(api.getNote).toHaveBeenCalled());
});

test('opening the screen fetches the note', async () => {
  note.value = fixture();
  vi.mocked(api.getNote).mockResolvedValue(fixture({ entries: [noteEntry({ text: 'just suggested' })] }));
  render(Note);
  expect(await screen.findByText('just suggested')).toBeInTheDocument();
  expect(api.getNote).toHaveBeenCalledTimes(1);
});

test('typing during a save is kept', async () => {
  note.value = fixture({ text: 'old' });
  const page = deferred<NoteData>();
  vi.mocked(api.setNoteText).mockReturnValue(page.promise);
  render(Note);
  await userEvent.type(box(), '1');
  await userEvent.click(save());
  await userEvent.type(box(), '2');

  page.resolve(fixture({ text: 'old1' }));

  await waitFor(() => expect(save()).toBeEnabled());
  expect(box()).toHaveValue('old12');
});

test('every control is disabled while a write is in flight', async () => {
  note.value = fixture({ entries: [noteEntry({ id: 'e1' }), noteEntry({ id: 'e2', approved: true })] });
  const page = deferred<NoteData>();
  vi.mocked(api.updateNoteEntry).mockReturnValue(page.promise);
  render(Note);
  await userEvent.type(box(), 'x');
  expect(save()).toBeEnabled();

  await userEvent.click(screen.getByRole('button', { name: 'Approve' }));

  expect(save()).toBeDisabled();
  for (const b of screen.getAllByRole('button', { name: /Approve|Unapprove|Edit|Delete/ })) {
    expect(b).toBeDisabled();
  }

  page.resolve(fixture());
  await waitFor(() => expect(save()).toBeEnabled());
});

import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { note as fixture, noteEntry } from './fixtures';
import {
  approve,
  approvedChars,
  chars,
  editEntry,
  loadNote,
  note,
  pending,
  removeEntry,
  resetNote,
  saveText,
  unapprove,
} from './note.svelte';
import type { Note } from './types';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  getNote: vi.fn(),
  setNoteText: vi.fn(),
  updateNoteEntry: vi.fn(),
  deleteNoteEntry: vi.fn(),
}));

// deferred is a promise the test settles when it chooses.
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

beforeEach(() => {
  resetNote();
  vi.mocked(api.getNote).mockReset();
  vi.mocked(api.setNoteText).mockReset();
  vi.mocked(api.updateNoteEntry).mockReset();
  vi.mocked(api.deleteNoteEntry).mockReset();
});

test('chars counts code points after trimming', () => {
  expect(chars('  héllo 👍\n\n')).toBe(7);
});

test('approvedChars adds the text to the approved entries only', () => {
  const n = fixture({
    text: 'abc',
    entries: [noteEntry({ id: 'a', text: 'four', approved: true }), noteEntry({ id: 'b', text: 'ignored' })],
  });
  expect(approvedChars(n)).toBe(7);
  expect(approvedChars(n, 'a longer draft')).toBe(18);
});

test('pending gives the entries not yet approved, in order', () => {
  const n = fixture({
    entries: [
      noteEntry({ id: 'a' }),
      noteEntry({ id: 'b', approved: true }),
      noteEntry({ id: 'c' }),
    ],
  });
  expect(pending(n).map((e) => e.id)).toEqual(['a', 'c']);
  expect(pending(null)).toEqual([]);
});

test('loadNote holds the note and clears the error', async () => {
  note.error = 'earlier';
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'mine' }));
  await loadNote();
  expect(note.value?.text).toBe('mine');
  expect(note.error).toBe('');
});

test('a failed load records why and keeps the note held', async () => {
  note.value = fixture({ text: 'kept' });
  vi.mocked(api.getNote).mockRejectedValue(new api.ApiError(0, 'network', 'cannot reach the server'));
  await loadNote();
  expect(note.error).toBe('cannot reach the server');
  expect(note.value?.text).toBe('kept');
});

test('a 401 records nothing', async () => {
  vi.mocked(api.getNote).mockRejectedValue(new api.ApiError(401, 'unauthenticated', 'authentication required'));
  await loadNote();
  expect(note.error).toBe('');
});

test('the newer of two loads writes', async () => {
  const first = deferred<Note>();
  const second = deferred<Note>();
  vi.mocked(api.getNote).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  const a = loadNote();
  const b = loadNote();
  second.resolve(fixture({ text: 'newer' }));
  await b;
  first.resolve(fixture({ text: 'older' }));
  await a;
  expect(note.value?.text).toBe('newer');
});

test('a note answered after a reset is not held', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.getNote).mockReturnValueOnce(slow.promise);
  const loading = loadNote();
  resetNote();
  slow.resolve(fixture({ text: 'the first person’s' }));
  await loading;
  expect(note.value).toBeNull();
});

test('a write answered after a reset is not held and leaves nothing busy', async () => {
  note.value = fixture();
  const slow = deferred<Note>();
  vi.mocked(api.setNoteText).mockReturnValueOnce(slow.promise);
  const saving = saveText('mine');
  resetNote();
  slow.resolve(fixture({ text: 'mine' }));
  await saving;
  expect(note.value).toBeNull();
  expect(note.busy).toBe(false);
});

test('a load that started before a write does not undo it', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.getNote).mockReturnValueOnce(slow.promise);
  const loading = loadNote();
  vi.mocked(api.setNoteText).mockResolvedValue(fixture({ text: 'new' }));
  await saveText('new');
  slow.resolve(fixture({ text: 'old' }));
  await loading;
  expect(note.value?.text).toBe('new');
});

test('saveText sends the text and holds the answer', async () => {
  vi.mocked(api.setNoteText).mockResolvedValue(fixture({ text: 'mine' }));
  await saveText('mine');
  expect(api.setNoteText).toHaveBeenCalledWith('mine');
  expect(note.value?.text).toBe('mine');
  expect(note.busy).toBe(false);
});

test('approve approves the entry', async () => {
  vi.mocked(api.updateNoteEntry).mockResolvedValue(fixture());
  await approve('a');
  expect(api.updateNoteEntry).toHaveBeenCalledWith('a', { approved: true });
});

test('unapprove withdraws the approval', async () => {
  vi.mocked(api.updateNoteEntry).mockResolvedValue(fixture());
  await unapprove('a');
  expect(api.updateNoteEntry).toHaveBeenCalledWith('a', { approved: false });
});

test('editEntry changes the entry’s text', async () => {
  vi.mocked(api.updateNoteEntry).mockResolvedValue(fixture());
  await editEntry('a', 'new words');
  expect(api.updateNoteEntry).toHaveBeenCalledWith('a', { text: 'new words' });
});

test('removeEntry deletes the entry', async () => {
  vi.mocked(api.deleteNoteEntry).mockResolvedValue(fixture());
  await removeEntry('a');
  expect(api.deleteNoteEntry).toHaveBeenCalledWith('a');
});

test('a second write while one is in flight asks for nothing', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.updateNoteEntry).mockReturnValueOnce(slow.promise);
  const first = approve('a');
  await removeEntry('b');
  expect(api.deleteNoteEntry).not.toHaveBeenCalled();
  slow.resolve(fixture());
  await first;
});

test('a refused approval rejects and changes nothing', async () => {
  const before = fixture({ entries: [noteEntry({ id: 'a' })] });
  note.value = before;
  const refusal = new api.ApiError(
    400,
    'validation',
    'the approved note would exceed 4000 characters; shorten or delete something first',
  );
  vi.mocked(api.updateNoteEntry).mockRejectedValue(refusal);
  await expect(approve('a')).rejects.toBe(refusal);
  expect(note.value).toEqual(before);
  expect(note.busy).toBe(false);
});

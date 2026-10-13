// The person's note, held outside the main store. It is loaded with every
// load() of the store and emptied when the stream is torn down, so that one
// person's note is never shown to the next. Nothing of it is kept in the browser.

import { ApiError, deleteNoteEntry, getNote, setNoteText, updateNoteEntry } from './api';
import type { Note, NoteEntry } from './types';

// The server's limits, in characters (code points, after trimming).
export const maxText = 2000; // the person's own text
export const maxEntry = 300; // one entry
export const maxApproved = 4000; // the text plus every approved entry

type Held = {
  value: Note | null; // null until the first load succeeds
  error: string; // why the last load failed; '' when none
  busy: boolean; // a write is in flight
};

export const note: Held = $state({ value: null, error: '', busy: false });

// loads numbers the calls to loadNote(), so that only the newest one writes.
let loads = 0;

// writes counts the writes started and the writes settled, well or badly. A
// load that saw it move had a write in flight at some moment between its start
// and its answer; the server may have answered it from before that write, so
// it writes nothing.
let writes = 0;

// resets counts the calls to resetNote(), so that nothing in flight across
// one writes into the emptied note.
let resets = 0;

// reason is the store's rule: '' for a 401, which is not recorded because the
// API layer has already signed the person out.
function reason(err: unknown): string {
  if (err instanceof ApiError && err.status === 401) {
    return '';
  }
  return err instanceof Error ? err.message : String(err);
}

// chars counts the code points of text once trimmed, as the server does.
export function chars(text: string): number {
  return [...text.trim()].length;
}

// approvedChars is what the server holds against maxApproved: the text, or
// the draft given in its place, plus each approved entry.
export function approvedChars(n: Note, text?: string): number {
  return n.entries.reduce((sum, e) => (e.approved ? sum + chars(e.text) : sum), chars(text ?? n.text));
}

// pending gives the entries not yet approved, in order.
export function pending(n: Note | null): NoteEntry[] {
  return n === null ? [] : n.entries.filter((e) => !e.approved);
}

// loadNote fetches the note. It never rejects: a failure becomes the reason,
// and the note already held stays.
export async function loadNote(): Promise<void> {
  loads += 1;
  const seq = loads;
  const started = writes;
  const epoch = resets;
  try {
    const fetched = await getNote();
    if (seq === loads && started === writes && epoch === resets) {
      note.value = fetched;
      note.error = '';
    }
  } catch (err) {
    if (seq === loads && epoch === resets) {
      const why = reason(err);
      if (why !== '') {
        note.error = why;
      }
    }
  }
}

// resetNote empties the note. A load or write still in flight writes nothing.
export function resetNote(): void {
  loads += 1;
  resets += 1;
  note.value = null;
  note.error = '';
  note.busy = false;
}

// write runs one call and holds its answer. A call made while another is in
// flight does nothing. It rejects with the ApiError, leaving the note as it was.
async function write(call: () => Promise<Note>): Promise<void> {
  if (note.busy) {
    return;
  }
  writes += 1;
  const epoch = resets;
  note.busy = true;
  try {
    const answer = await call();
    if (epoch === resets) {
      note.value = answer;
    }
  } finally {
    writes += 1;
    if (epoch === resets) {
      note.busy = false;
    }
  }
}

export function saveText(text: string): Promise<void> {
  return write(() => setNoteText(text));
}

export function approve(entryId: string): Promise<void> {
  return write(() => updateNoteEntry(entryId, { approved: true }));
}

export function unapprove(entryId: string): Promise<void> {
  return write(() => updateNoteEntry(entryId, { approved: false }));
}

export function editEntry(entryId: string, text: string): Promise<void> {
  return write(() => updateNoteEntry(entryId, { text }));
}

export function removeEntry(entryId: string): Promise<void> {
  return write(() => deleteNoteEntry(entryId));
}

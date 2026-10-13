import { expect, test } from 'vitest';
import { agent, room } from './fixtures';
import { closeSearch, matchRooms, openSearch, search } from './search.svelte';

const scout = agent({ id: 'a1', handle: 'scout', display_name: 'Scout' });
const atlas = agent({ id: 'a2', handle: 'atlas-2', display_name: 'Atlas' });
const rooms = [
  room({ id: 'r1', topic: 'Trip planning', agents: [scout] }),
  room({ id: 'r2', topic: 'Heat pump vs. gas furnace', agents: [atlas] }),
  room({ id: 'r3', topic: 'Old trip', agents: [atlas], archived: true }),
  room({ id: 'd1', flavor: 'dm', topic: '', agents: [scout] }),
];
const ids = (q: string) => matchRooms(rooms, q).map((r) => r.id);

test('a term matches a topic, an agent name or a handle, whatever its case', () => {
  expect(ids('HEAT')).toEqual(['r2']);
  expect(ids('atlas-2')).toEqual(['r2', 'r3']);
});

test('an agent’s name finds its DM and every room it is in, DM first', () => {
  expect(ids('scout')).toEqual(['d1', 'r1']);
  expect(ids('@scout')).toEqual(['d1', 'r1']);
});

test('every term must match, and archived rooms come last', () => {
  expect(ids('trip')).toEqual(['r1', 'r3']);
  expect(ids('trip atlas')).toEqual(['r3']);
  expect(ids('#trip')).toEqual(['r1', 'r3']);
});

test('a blank or unmatched query finds nothing and odd characters do not throw', () => {
  for (const q of ['', '   ', '@', '#', '(', '[a-', '\\', 'zzz']) {
    expect(ids(q)).toEqual([]);
  }
  expect(ids('vs.')).toEqual(['r2']);
});

test('closing search empties the query and opening keeps it', () => {
  search.query = 'tr';
  openSearch();
  expect(search).toEqual({ open: true, query: 'tr' });
  closeSearch();
  expect(search).toEqual({ open: false, query: '' });
});

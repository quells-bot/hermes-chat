import { expect, test } from 'vitest';
import { agent } from './fixtures';
import { candidates, complete, mentionAt } from './mention';

test.each([
  ['hi @sc', 6, { start: 3, query: 'sc' }],
  ['@', 1, { start: 0, query: '' }],
  ['(@sc', 4, { start: 1, query: 'sc' }],
  ['a\n@s', 4, { start: 2, query: 's' }],
  ['@sc now', 3, { start: 0, query: 'sc' }],
  ['hi@sc', 5, null],
  ['kai@example.com', 15, null],
  ['@scout', 3, null], // the caret is inside the token
  ['((@sc', 5, null], // only one bracket is ignored, as on the server
  ['hi ', 3, null],
])('mentionAt(%j, %i)', (text, caret, want) => {
  expect(mentionAt(text, caret)).toEqual(want);
});

test('complete replaces what was typed and keeps what follows', () => {
  expect(complete('hi @sc', { start: 3, query: 'sc' }, 6, 'scout')).toEqual({ text: 'hi @scout ', caret: 10 });
  expect(complete('@sc now', { start: 0, query: 'sc' }, 3, 'scout')).toEqual({ text: '@scout now', caret: 7 });
  expect(complete('(@', { start: 1, query: '' }, 2, 'scout')).toEqual({ text: '(@scout ', caret: 8 });
});

test('candidates match the start of a handle or a name, skip revoked agents, and come by handle', () => {
  const agents = [
    agent({ id: '1', handle: 'scout', display_name: 'Scout' }),
    agent({ id: '2', handle: 'atlas', display_name: 'Sky Atlas' }),
    agent({ id: '3', handle: 'sage', display_name: 'Sage', revoked: true }),
  ];
  expect(candidates(agents, 'S').map((a) => a.handle)).toEqual(['atlas', 'scout']);
  expect(candidates(agents, '').map((a) => a.handle)).toEqual(['atlas', 'scout']);
  expect(candidates(agents, 'cout')).toEqual([]);
});

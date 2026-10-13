import { expect, test } from 'vitest';
import { message } from './fixtures';
import { firstUnread, mergeNewest, upsert } from './feed';

const m = (id: string, text = '') => message({ id, text });
const full = (prefix: string) =>
  Array.from({ length: 50 }, (_, i) => m(`${prefix}${String(i).padStart(2, '0')}`));

test('upsert keeps id order and replaces by id', () => {
  const list = [m('01A'), m('01C')];
  upsert(list, m('01B'));
  upsert(list, m('01C', 'edited'));
  expect(list.map((x) => [x.id, x.text])).toEqual([
    ['01A', ''],
    ['01B', ''],
    ['01C', 'edited'],
  ]);
});

test('a full page that does not reach the held messages replaces them', () => {
  const page = Array.from({ length: 50 }, (_, i) => m(`02${String(i).padStart(2, '0')}`));
  expect(mergeNewest([m('01A'), m('01B')], page)).toEqual({ messages: page, replaced: true });
});

test('a page that overlaps is merged and its copies win', () => {
  const page = full('02');
  const held = [m('01A'), m('0210', 'old'), m('0250', 'newer than the page')];
  const r = mergeNewest(held, page);
  expect(r.replaced).toBe(false);
  expect(r.messages).toHaveLength(52);
  expect(r.messages[0].id).toBe('01A');
  expect(r.messages.find((x) => x.id === '0210')?.text).toBe('');
  expect(r.messages.at(-1)?.id).toBe('0250');
});

test('a short page is merged even when it does not overlap', () => {
  const r = mergeNewest([m('01A'), m('01B')], [m('02A')]);
  expect(r.replaced).toBe(false);
  expect(r.messages.map((x) => x.id)).toEqual(['01A', '01B', '02A']);
});

test('firstUnread', () => {
  const list = [m('01A'), m('01B'), m('01C')];
  expect(firstUnread(list, '01A')).toBe('01B');
  expect(firstUnread(list, '01C')).toBeNull();
  expect(firstUnread(list, null)).toBe('01A');
  expect(firstUnread([], null)).toBeNull();
  expect(firstUnread([], '01A')).toBeNull();
});

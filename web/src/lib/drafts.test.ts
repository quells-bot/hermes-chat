import { beforeEach, expect, test } from 'vitest';
import { clearDrafts, getDraft, setDraft } from './drafts';

beforeEach(() => clearDrafts());

test('a room with no draft gives the empty string', () => {
  expect(getDraft('r1')).toBe('');
});

test('a draft is kept per room', () => {
  setDraft('r1', 'one');
  setDraft('r2', 'two');
  expect(getDraft('r1')).toBe('one');
  expect(getDraft('r2')).toBe('two');
});

test('setting the empty string forgets the draft', () => {
  setDraft('r1', 'one');
  setDraft('r1', '');
  expect(getDraft('r1')).toBe('');
});

test('clearDrafts forgets every room', () => {
  setDraft('r1', 'one');
  setDraft('r2', 'two');
  clearDrafts();
  expect(getDraft('r1')).toBe('');
  expect(getDraft('r2')).toBe('');
});

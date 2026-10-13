import { expect, test } from 'vitest';
import { agent } from './fixtures';
import { ago, clock, duration, iconName, preview, size } from './format';

test('clock is HH:MM today and carries the date otherwise', () => {
  const now = new Date(2026, 9, 12, 16, 30);
  const at = (d: Date) => clock(d.toISOString(), now);
  expect(at(new Date(2026, 9, 12, 14, 2))).toBe('14:02');
  expect(at(new Date(2026, 9, 12, 0, 5))).toBe('00:05');
  const other = at(new Date(2026, 9, 10, 14, 2));
  expect(other.endsWith(' 14:02')).toBe(true);
  expect(other.length).toBeGreaterThan(6);
});

test('ago', () => {
  const now = new Date('2026-10-12T12:00:00Z');
  const at = (ms: number) => new Date(now.getTime() - ms).toISOString();
  expect(ago(at(30_000), now)).toBe('now');
  expect(ago(at(4 * 60_000), now)).toBe('4 min');
  expect(ago(at(3 * 3_600_000), now)).toBe('3 h');
  expect(ago(at(30 * 3_600_000), now)).toBe('yesterday');
  expect(ago(at(72 * 3_600_000), now)).not.toMatch(/now|min|h$|yesterday/);
});

test('a time in the future reads now', () => {
  const now = new Date('2026-10-12T12:00:00Z');
  expect(ago(new Date(now.getTime() + 5000).toISOString(), now)).toBe('now');
});

test('preview collapses whitespace and cuts at 280 code points without splitting a pair', () => {
  expect(preview('  a \n\t b  c ')).toBe('a b c');
  const cut = preview('😀'.repeat(300));
  expect(Array.from(cut)).toHaveLength(280);
  expect(cut).toBe('😀'.repeat(280));
  expect(preview('x'.repeat(280))).toBe('x'.repeat(280));
});

test('size', () => {
  expect(size(812)).toBe('812 B');
  expect(size(14 * 1024)).toBe('14 KB');
  expect(size(Math.round(1.4 * 1024 * 1024))).toBe('1.4 MB');
});

test('duration', () => {
  expect(duration(400)).toBe('0.4s');
  expect(duration(30_000)).toBe('30.0s');
  expect(duration(65_000)).toBe('1m 05s');
});

test('iconName', () => {
  const dir = { a1: agent({ id: 'a1', handle: 'helper', display_name: 'Helper' }) };
  expect(iconName({ kind: 'agent', id: 'a1', name: 'Helper' }, dir)).toBe('helper');
  expect(iconName({ kind: 'agent', id: 'zz', name: 'Gone' }, dir)).toBe('Gone');
  expect(iconName({ kind: 'human', id: 'h1', name: 'kai' }, dir)).toBe('kai');
  expect(iconName(null, dir)).toBe('');
});

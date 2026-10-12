import { afterEach, expect, test, vi } from 'vitest';
import { loadCollapsed, saveCollapsed } from './collapsed';

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

test('round trip through localStorage', () => {
  expect(loadCollapsed()).toEqual({ dms: false, rooms: false });

  saveCollapsed({ dms: true, rooms: false });

  expect(loadCollapsed()).toEqual({ dms: true, rooms: false });
});

test('garbage in storage reads as both expanded', () => {
  for (const raw of ['not json', 'null', '7', '{"dms":"yes"}']) {
    localStorage.setItem('hermes-chat.collapsed', raw);
    expect(loadCollapsed()).toEqual({ dms: false, rooms: false });
  }
});

test('getItem throwing reads as both expanded', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('denied');
  });

  expect(loadCollapsed()).toEqual({ dms: false, rooms: false });
});

test('setItem throwing does not throw', () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('full');
  });

  expect(() => saveCollapsed({ dms: true, rooms: true })).not.toThrow();
});

import { afterEach, expect, test, vi } from 'vitest';
import { layout, startLayout } from './layout.svelte';

afterEach(() => {
  vi.unstubAllGlobals();
  layout.wide = false;
});

// fakeMedia is a MediaQueryList that keeps its listener, so a test can fire a change.
function fakeMedia(matches: boolean) {
  let listener: ((event: { matches: boolean }) => void) | null = null;
  return {
    matches,
    addEventListener: vi.fn((_: string, fn: (event: { matches: boolean }) => void) => {
      listener = fn;
    }),
    removeEventListener: vi.fn(),
    fire(next: boolean) {
      listener?.({ matches: next });
    },
  };
}

test('startLayout follows the media query and stop puts it back', () => {
  const mq = fakeMedia(true);
  vi.stubGlobal('matchMedia', vi.fn(() => mq));
  const stop = startLayout();
  expect(matchMedia).toHaveBeenCalledWith('(min-width: 56rem)');
  expect(layout.wide).toBe(true);
  mq.fire(false);
  expect(layout.wide).toBe(false);
  mq.fire(true);
  stop();
  expect(layout.wide).toBe(false);
  expect(mq.removeEventListener).toHaveBeenCalled();
});

test('without matchMedia the layout is narrow', () => {
  vi.stubGlobal('matchMedia', undefined);
  startLayout()();
  expect(layout.wide).toBe(false);
});

test('without matchMedia start leaves the flag alone', () => {
  vi.stubGlobal('matchMedia', undefined);
  layout.wide = true;
  const stop = startLayout();
  expect(layout.wide).toBe(true);
  stop();
  expect(layout.wide).toBe(false);
});

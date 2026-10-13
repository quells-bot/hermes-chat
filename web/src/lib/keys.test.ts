import { beforeEach, expect, test, vi } from 'vitest';
import { room } from './fixtures';
import { shortcut, startKeys } from './keys';
import { layout } from './layout.svelte';
import { navigate, route } from './route.svelte';
import { closeSearch, search } from './search.svelte';
import { store } from './store.svelte';

vi.mock('./route.svelte', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./route.svelte')>()),
  navigate: vi.fn(),
}));

// Dispatches on an element of the named kind so that target is set.
function press(key: string, init: KeyboardEventInit, on: string): KeyboardEvent {
  const el = on === 'body' ? document.body : document.body.appendChild(document.createElement(on));
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
  el.dispatchEvent(e);
  if (el !== document.body) el.remove();
  return e;
}

test.each([
  ['/', {}, 'body', 'search'],
  ['/', {}, 'textarea', null],
  ['/', {}, 'input', null],
  ['/', { shiftKey: true }, 'body', null],
  ['k', { ctrlKey: true }, 'textarea', 'search'],
  ['k', { metaKey: true }, 'body', 'search'],
  ['k', { ctrlKey: true, metaKey: true }, 'body', null],
  ['k', {}, 'body', null],
  ['ArrowDown', { altKey: true }, 'textarea', 'next'],
  ['ArrowUp', { altKey: true }, 'body', 'prev'],
  ['ArrowDown', { altKey: true, shiftKey: true }, 'body', 'next-unread'],
  ['ArrowUp', { altKey: true, shiftKey: true }, 'body', 'prev-unread'],
  ['ArrowDown', {}, 'body', null],
  ['ArrowDown', { altKey: true, ctrlKey: true }, 'body', null],
  ['/', { isComposing: true }, 'body', null],
])('%s %o on %s is %s', (key, init, on, want) => {
  let got: ReturnType<typeof shortcut> = null;
  const el = on === 'body' ? document.body : document.body.appendChild(document.createElement(on));
  el.addEventListener('keydown', (e) => (got = shortcut(e)), { once: true });
  el.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, ...init }));
  if (el !== document.body) el.remove();
  expect(got).toBe(want);
});

let stop: () => void;

beforeEach(() => {
  vi.mocked(navigate).mockClear();
  closeSearch();
  layout.wide = true;
  route.path = '/';
  const dm = room({ id: 'd1', flavor: 'dm', topic: 'z', agents: [{ id: 'a', handle: 'a', display_name: 'Ann' } as never] });
  store.rooms = {
    d1: dm,
    r1: room({ id: 'r1', topic: 'Alpha', last_message_id: '01A', last_read_message_id: '01A' }),
    r2: room({ id: 'r2', topic: 'Beta', last_message_id: '01B', last_read_message_id: null }),
  };
  stop = startKeys();
});

test('Alt+↓ from the DM opens the first room', () => {
  route.path = '/rooms/d1';
  press('ArrowDown', { altKey: true }, 'body');
  expect(navigate).toHaveBeenCalledWith('/rooms/r1');
});

test('Alt+↓ on the last room goes nowhere', () => {
  route.path = '/rooms/r2';
  const e = press('ArrowDown', { altKey: true }, 'body');
  expect(navigate).not.toHaveBeenCalled();
  expect(e.defaultPrevented).toBe(true);
});

test('Alt+↑ from the list opens the last room', () => {
  press('ArrowUp', { altKey: true }, 'body');
  expect(navigate).toHaveBeenCalledWith('/rooms/r2');
});

test('Alt+Shift+↓ skips read rooms', () => {
  route.path = '/rooms/d1';
  press('ArrowDown', { altKey: true, shiftKey: true }, 'body');
  expect(navigate).toHaveBeenCalledWith('/rooms/r2');
});

test('a room’s settings count as that room', () => {
  route.path = '/rooms/r1/settings';
  press('ArrowDown', { altKey: true }, 'body');
  expect(navigate).toHaveBeenCalledWith('/rooms/r2');
});

test('/ opens search, and goes to the list first on the phone layout', () => {
  route.path = '/rooms/r1';
  layout.wide = false;
  press('/', {}, 'body');
  expect(search.open).toBe(true);
  expect(navigate).toHaveBeenCalledWith('/');

  closeSearch();
  vi.mocked(navigate).mockClear();
  layout.wide = true;
  press('/', {}, 'body');
  expect(search.open).toBe(true);
  expect(navigate).not.toHaveBeenCalled();
});

test('/ in a textarea is left alone', () => {
  const e = press('/', {}, 'textarea');
  expect(e.defaultPrevented).toBe(false);
  expect(search.open).toBe(false);
});

test('a key after stop does nothing', () => {
  stop();
  const e = press('k', { ctrlKey: true }, 'body');
  expect(e.defaultPrevented).toBe(false);
  expect(search.open).toBe(false);
});

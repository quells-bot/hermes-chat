import { afterEach, expect, test, vi } from 'vitest';
import { navigate, roomIdOf, route, startRouting } from './route.svelte';

afterEach(() => {
  location.hash = '';
});

function changeHash(hash: string): void {
  location.hash = hash;
  window.dispatchEvent(new HashChangeEvent('hashchange'));
}

test("an empty hash is '/'", () => {
  location.hash = '';
  const stop = startRouting();

  expect(route.path).toBe('/');

  stop();
});

test('a hashchange updates the path', () => {
  const stop = startRouting();

  changeHash('#/new');
  expect(route.path).toBe('/new');

  navigate('/');
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  expect(route.path).toBe('/');

  stop();
});

test('the stop function stops listening', () => {
  const stop = startRouting();
  stop();

  changeHash('#/new');

  expect(route.path).toBe('/');
});

test("prev is '/' at the start", async () => {
  vi.resetModules();
  const fresh = await import('./route.svelte');
  expect(fresh.route).toEqual({ path: '/', prev: '/' });
});

test('prev is the path before the current one', () => {
  const stop = startRouting();

  changeHash('#/unread');
  changeHash('#/rooms/01R');
  expect(route.path).toBe('/rooms/01R');
  expect(route.prev).toBe('/unread');

  // A hashchange that changes nothing does not forget where the person was.
  window.dispatchEvent(new HashChangeEvent('hashchange'));
  expect(route.prev).toBe('/unread');

  changeHash('#/');
  expect(route.prev).toBe('/rooms/01R');

  stop();
});

test('roomIdOf', () => {
  expect(roomIdOf('/rooms/01R')).toBe('01R');
  expect(roomIdOf('/rooms/')).toBeNull();
  expect(roomIdOf('/rooms/a/b')).toBeNull();
  expect(roomIdOf('/new')).toBeNull();
  expect(roomIdOf('/')).toBeNull();
});

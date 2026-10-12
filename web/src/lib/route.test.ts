import { afterEach, expect, test } from 'vitest';
import { navigate, route, startRouting } from './route.svelte';

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

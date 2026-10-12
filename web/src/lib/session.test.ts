import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import * as api from './api';
import { session, signIn, signOut, start } from './session.svelte';

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
}));

const kai = { id: '01J', username: 'kai' };
const unreachable = () => new api.ApiError(0, 'network', 'cannot reach the server');
const noSession = () => new api.ApiError(401, 'unauthenticated', 'authentication required');

beforeEach(() => {
  vi.mocked(api.login).mockReset();
  vi.mocked(api.logout).mockReset();
  vi.mocked(api.me).mockReset();
  session.status = 'loading';
  session.me = null;
  session.error = '';
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('start with a live session', async () => {
  vi.mocked(api.me).mockResolvedValue(kai);

  await start();

  expect(session.status).toBe('in');
  expect(session.me?.username).toBe('kai');
});

test('start with no session', async () => {
  vi.mocked(api.me).mockRejectedValue(noSession());

  await start();

  expect(session.status).toBe('out');
  expect(session.me).toBeNull();
});

test('start when the server is unreachable', async () => {
  vi.mocked(api.me).mockRejectedValue(unreachable());

  await expect(start()).resolves.toBeUndefined();

  expect(session.status).toBe('error');
  expect(session.error).toBe('cannot reach the server');
});

test('start again after an error goes back through loading', async () => {
  session.status = 'error';
  session.error = 'cannot reach the server';
  let resolve!: (me: api.Me) => void;
  vi.mocked(api.me).mockReturnValue(new Promise((r) => (resolve = r)));

  const done = start();
  expect(session.status).toBe('loading');
  resolve(kai);
  await done;

  expect(session.status).toBe('in');
  expect(session.error).toBe('');
});

test('signIn success', async () => {
  session.status = 'out';
  vi.mocked(api.login).mockResolvedValue(kai);

  await signIn('kai', 'pw');

  expect(api.login).toHaveBeenCalledWith('kai', 'pw');
  expect(session.status).toBe('in');
  expect(session.me).toEqual(kai);
});

test('signIn failure rethrows and leaves the person signed out', async () => {
  session.status = 'out';
  const wrong = new api.ApiError(401, 'invalid_credentials', 'wrong username or password');
  vi.mocked(api.login).mockRejectedValue(wrong);

  await expect(signIn('kai', 'nope')).rejects.toBe(wrong);

  expect(session.status).toBe('out');
  expect(session.me).toBeNull();
});

test('signOut ends the session even when logout fails', async () => {
  session.status = 'in';
  session.me = kai;
  vi.mocked(api.logout).mockRejectedValue(unreachable());

  await expect(signOut()).resolves.toBeUndefined();

  expect(session.status).toBe('out');
  expect(session.me).toBeNull();
});

test('a 401 on any later request signs the person out', async () => {
  session.status = 'in';
  session.me = kai;
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response('{"error":{"code":"unauthenticated","message":"authentication required"}}', { status: 401 }),
    ),
  );

  await expect(api.request('GET', '/api/rooms')).rejects.toBeInstanceOf(api.ApiError);

  expect(session.status).toBe('out');
  expect(session.me).toBeNull();
});

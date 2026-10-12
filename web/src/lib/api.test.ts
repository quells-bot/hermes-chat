import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { ApiError, login, logout, me, onUnauthenticated, request } from './api';

const fetchMock = vi.fn();

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

function apiError(code: string, message: string) {
  return { error: { code, message } };
}

async function failure(p: Promise<unknown>): Promise<ApiError> {
  const err = await p.then(
    () => null,
    (e: unknown) => e,
  );
  expect(err).toBeInstanceOf(ApiError);
  return err as ApiError;
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  onUnauthenticated(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('login posts JSON to /api/login', async () => {
  fetchMock.mockResolvedValue(json(200, { id: '01J', username: 'kai' }));

  await expect(login('kai', 'pw')).resolves.toEqual({ id: '01J', username: 'kai' });

  expect(fetchMock).toHaveBeenCalledTimes(1);
  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/login');
  expect(init.method).toBe('POST');
  expect(init.body).toBe('{"username":"kai","password":"pw"}');
  expect(init.headers['Content-Type']).toBe('application/json');
});

test('me sends no body and no content type', async () => {
  fetchMock.mockResolvedValue(json(200, { id: '01J', username: 'kai' }));

  await me();

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/me');
  expect(init.method).toBe('GET');
  expect(init.body).toBeUndefined();
  expect(init.headers['Content-Type']).toBeUndefined();
  expect(init.headers['Accept']).toBe('application/json');
});

test('logout resolves undefined on 204', async () => {
  fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

  await expect(logout()).resolves.toBeUndefined();

  expect(fetchMock.mock.calls[0][0]).toBe('/api/logout');
  expect(fetchMock.mock.calls[0][1].method).toBe('POST');
});

test('a server error becomes an ApiError', async () => {
  fetchMock.mockResolvedValue(json(401, apiError('invalid_credentials', 'wrong username or password')));

  const err = await failure(login('kai', 'nope'));

  expect(err.status).toBe(401);
  expect(err.code).toBe('invalid_credentials');
  expect(err.message).toBe('wrong username or password');
  expect(err.retryAfter).toBeNull();
});

test('Retry-After is parsed', async () => {
  fetchMock.mockResolvedValue(
    json(429, apiError('throttled', 'too many attempts; try again later'), { 'Retry-After': '42' }),
  );

  const err = await failure(login('kai', 'pw'));

  expect(err.code).toBe('throttled');
  expect(err.retryAfter).toBe(42);
});

test.each([
  [502, '<html>Bad Gateway</html>'],
  [500, ''],
  [500, '{"unexpected":true}'],
])('a %i body that is not the error shape gets a fallback', async (status, body) => {
  fetchMock.mockResolvedValue(new Response(body, { status }));

  const err = await failure(me());

  expect(err.status).toBe(status);
  expect(err.code).toBe('http');
  expect(err.message).toBe(`server error (${status})`);
});

test('a failed fetch is a network error', async () => {
  fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));

  const err = await failure(me());

  expect(err.status).toBe(0);
  expect(err.code).toBe('network');
  expect(err.message).toBe('cannot reach the server');
});

test('an expired session calls the handler', async () => {
  const handler = vi.fn();
  onUnauthenticated(handler);
  fetchMock.mockResolvedValue(json(401, apiError('unauthenticated', 'authentication required')));

  const err = await failure(request('GET', '/api/me'));

  expect(err.code).toBe('unauthenticated');
  expect(handler).toHaveBeenCalledTimes(1);
});

test('a wrong password does not call the handler', async () => {
  const handler = vi.fn();
  onUnauthenticated(handler);
  fetchMock.mockResolvedValue(json(401, apiError('invalid_credentials', 'wrong username or password')));

  await failure(login('kai', 'nope'));

  expect(handler).not.toHaveBeenCalled();
});

// A phone leaving wifi stalls a request instead of refusing it.
test('a request that never answers becomes a network error after 15 seconds', async () => {
  vi.useFakeTimers();
  try {
    fetchMock.mockImplementation(
      (_path: string, init: RequestInit) =>
        new Promise((_, reject) => {
          init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
    );

    const pending = failure(me());
    await vi.advanceTimersByTimeAsync(14_999);
    let settled = false;
    void pending.then(() => (settled = true));
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toBe(false);

    await vi.advanceTimersByTimeAsync(1);
    const err = await pending;

    expect(err.status).toBe(0);
    expect(err.code).toBe('network');
    expect(err.message).toBe('cannot reach the server');
  } finally {
    vi.useRealTimers();
  }
});

test('an answered request leaves no timer behind', async () => {
  vi.useFakeTimers();
  try {
    fetchMock.mockResolvedValue(json(200, { id: '01J', username: 'kai' }));

    await me();

    expect(vi.getTimerCount()).toBe(0);
  } finally {
    vi.useRealTimers();
  }
});

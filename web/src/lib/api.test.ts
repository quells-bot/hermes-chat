import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import {
  ApiError,
  addRoomAgent,
  answerPrompt,
  attachmentUrl,
  cancelMessage,
  createRoom,
  deleteNoteEntry,
  getHalt,
  getMessage,
  getNote,
  getRoom,
  inviteHuman,
  listActivity,
  listAgents,
  listHumans,
  listMessages,
  listRooms,
  login,
  logout,
  markRoomRead,
  me,
  onUnauthenticated,
  openDM,
  pauseRoom,
  postMessage,
  removeRoomAgent,
  request,
  resumeEverything,
  resumeRoom,
  sessionLost,
  setNoteText,
  steer,
  stopEverything,
  stopTurn,
  updateNoteEntry,
  updateRoom,
} from './api';

const fetchMock = vi.fn();

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

// stubFetch makes the next fetch answer with the status and body, and returns
// the mock so a test can read the call.
function stubFetch(status: number, body: unknown) {
  fetchMock.mockResolvedValue(json(status, body));
  return fetchMock;
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

test('listRooms unwraps the rooms', async () => {
  fetchMock.mockResolvedValue(json(200, { rooms: [{ id: '01R' }] }));

  await expect(listRooms()).resolves.toEqual([{ id: '01R' }]);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms');
  expect(init.method).toBe('GET');
});

test('getRoom gets one room, which is the body itself', async () => {
  fetchMock.mockResolvedValue(json(200, { id: '01R', topic: 'Trip' }));

  await expect(getRoom('01R')).resolves.toEqual({ id: '01R', topic: 'Trip' });

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R');
  expect(init.method).toBe('GET');
  expect(init.body).toBeUndefined();
});

test('listAgents unwraps the agents', async () => {
  fetchMock.mockResolvedValue(json(200, { agents: [{ id: '01A' }] }));

  await expect(listAgents()).resolves.toEqual([{ id: '01A' }]);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/agents');
  expect(init.method).toBe('GET');
});

test.each([
  ['getHalt', getHalt, 'GET', true],
  ['stopEverything', stopEverything, 'POST', true],
  ['resumeEverything', resumeEverything, 'DELETE', false],
])('%s calls /api/halt and returns halted', async (_name, call, method, halted) => {
  fetchMock.mockResolvedValue(json(200, { halted }));

  await expect(call()).resolves.toBe(halted);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/halt');
  expect(init.method).toBe(method);
  expect(init.body).toBeUndefined();
});

test('createRoom posts the topic and agent ids', async () => {
  fetchMock.mockResolvedValue(json(201, { id: '01R', topic: 'Trip' }));

  await expect(createRoom('Trip', ['01A', '01B'])).resolves.toMatchObject({ id: '01R' });

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms');
  expect(init.method).toBe('POST');
  expect(init.body).toBe('{"topic":"Trip","agent_ids":["01A","01B"]}');
});

test.each([200, 201])('openDM posts the agent id and accepts %i', async (status) => {
  fetchMock.mockResolvedValue(json(status, { id: '01D' }));

  await expect(openDM('01A')).resolves.toMatchObject({ id: '01D' });

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/dms');
  expect(init.method).toBe('POST');
  expect(init.body).toBe('{"agent_id":"01A"}');
});

test('listMessages with no arguments asks for limit=50 and no before', async () => {
  fetchMock.mockResolvedValue(json(200, { messages: [] }));

  await expect(listMessages('01R')).resolves.toEqual([]);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/messages?limit=50');
  expect(init.method).toBe('GET');
});

test('listMessages asks for a page before a message', async () => {
  fetchMock.mockResolvedValue(json(200, { messages: [{ id: '01M' }] }));

  await expect(listMessages('01R', '01N', 150)).resolves.toEqual([{ id: '01M' }]);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/messages?limit=150&before=01N');
  expect(init.method).toBe('GET');
});

test('getMessage gets one message, which is the body itself', async () => {
  fetchMock.mockResolvedValue(json(200, { id: '01M', text: 'hi' }));

  await expect(getMessage('01R', '01M')).resolves.toEqual({ id: '01M', text: 'hi' });

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/messages/01M');
  expect(init.method).toBe('GET');
});

test('listActivity unwraps the activities', async () => {
  fetchMock.mockResolvedValue(json(200, { activities: [{ id: '01X' }] }));

  await expect(listActivity('01R', '01M')).resolves.toEqual([{ id: '01X' }]);

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/messages/01M/activity');
  expect(init.method).toBe('GET');
});

test('markRoomRead puts the marker', async () => {
  fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

  await expect(markRoomRead('01R', '01M')).resolves.toBeUndefined();

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/read');
  expect(init.method).toBe('PUT');
  expect(init.body).toBe('{"message_id":"01M"}');
});

test('postMessage posts text, client id and reply', async () => {
  const msg = { id: '01M', text: 'hello', room_id: '01R' };
  fetchMock.mockResolvedValue(json(201, msg));
  await expect(postMessage('01R', 'hello', 'k-1', '01Q')).resolves.toEqual(msg);
  const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
  expect(path).toBe('/api/rooms/01R/messages');
  expect(init.method).toBe('POST');
  expect(JSON.parse(String(init.body))).toEqual({ text: 'hello', client_id: 'k-1', reply_to_id: '01Q' });
});

test('postMessage omits reply_to_id when there is none', async () => {
  fetchMock.mockResolvedValue(json(201, { id: '01M', text: 'hello', room_id: '01R' }));

  await postMessage('01R', 'hello', 'k-1');

  const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
  expect(path).toBe('/api/rooms/01R/messages');
  const body = JSON.parse(String(init.body));
  expect(body).toEqual({ text: 'hello', client_id: 'k-1' });
  expect(body).not.toHaveProperty('reply_to_id');
});

test("steer posts to the agent's turn", async () => {
  const msg = { id: '01M', text: 'go on', room_id: '01R' };
  fetchMock.mockResolvedValue(json(201, msg));
  await expect(steer('01R', '01A', 'go on', 'k-1')).resolves.toEqual(msg);
  const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
  expect(path).toBe('/api/rooms/01R/agents/01A/steer');
  expect(init.method).toBe('POST');
  expect(JSON.parse(String(init.body))).toEqual({ text: 'go on', client_id: 'k-1' });
});

test('cancelMessage posts and expects no body', async () => {
  fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

  await expect(cancelMessage('01R', '01M')).resolves.toBeUndefined();

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/messages/01M/cancel');
  expect(init.method).toBe('POST');
  expect(init.body).toBeUndefined();
});

test('answerPrompt posts the answer', async () => {
  const msg = { id: '01M', text: 'ok', room_id: '01R' };
  fetchMock.mockResolvedValue(json(200, msg));
  await expect(answerPrompt('01R', '01M', 'Once')).resolves.toEqual(msg);
  const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
  expect(path).toBe('/api/rooms/01R/messages/01M/answer');
  expect(init.method).toBe('POST');
  expect(JSON.parse(String(init.body))).toEqual({ answer: 'Once' });
});

test('stopTurn posts to the agent', async () => {
  fetchMock.mockResolvedValue(new Response(null, { status: 204 }));

  await expect(stopTurn('01R', '01A')).resolves.toBeUndefined();

  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/agents/01A/stop');
  expect(init.method).toBe('POST');
  expect(init.body).toBeUndefined();
});

test('pauseRoom posts and unwraps the room', async () => {
  const room = { id: '01R', paused: true };
  fetchMock.mockResolvedValue(json(200, room));
  await expect(pauseRoom('01R')).resolves.toEqual(room);
  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/pause');
  expect(init.method).toBe('POST');
  expect(init.body).toBeUndefined();
});

test('resumeRoom posts and unwraps the room', async () => {
  const room = { id: '01R', paused: false };
  fetchMock.mockResolvedValue(json(200, room));
  await expect(resumeRoom('01R')).resolves.toEqual(room);
  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/rooms/01R/resume');
  expect(init.method).toBe('POST');
  expect(init.body).toBeUndefined();
});

test('attachmentUrl encodes both ids', () => {
  expect(attachmentUrl('a/b', 'c d')).toBe('/api/rooms/a%2Fb/attachments/c%20d');
});

test('getNote reads the note', async () => {
  const fetch = stubFetch(200, { text: 'hi', entries: [] });
  await expect(getNote()).resolves.toEqual({ text: 'hi', entries: [] });
  expect(fetch).toHaveBeenCalledWith('/api/note', expect.objectContaining({ method: 'GET' }));
});

test('setNoteText puts the text', async () => {
  const fetch = stubFetch(200, { text: '', entries: [] });
  await expect(setNoteText('')).resolves.toEqual({ text: '', entries: [] });
  expect(fetch).toHaveBeenCalledWith('/api/note', expect.objectContaining({ method: 'PUT', body: '{"text":""}' }));
});

test('updateNoteEntry patches only what it is given', async () => {
  const fetch = stubFetch(200, { text: '', entries: [] });
  await expect(updateNoteEntry('01E', { approved: true })).resolves.toEqual({ text: '', entries: [] });
  expect(fetch).toHaveBeenCalledWith('/api/note/entries/01E', expect.objectContaining({ method: 'PATCH', body: '{"approved":true}' }));
});

test('deleteNoteEntry deletes the entry', async () => {
  const fetch = stubFetch(200, { text: '', entries: [] });
  await expect(deleteNoteEntry('01E')).resolves.toEqual({ text: '', entries: [] });
  expect(fetch).toHaveBeenCalledWith('/api/note/entries/01E', expect.objectContaining({ method: 'DELETE' }));
});

test('listHumans unwraps humans', async () => {
  const fetch = stubFetch(200, { humans: [{ id: '01H', username: 'kai' }] });
  await expect(listHumans()).resolves.toEqual([{ id: '01H', username: 'kai' }]);
  expect(fetch).toHaveBeenCalledWith('/api/humans', expect.objectContaining({ method: 'GET' }));
});

test('updateRoom patches the room', async () => {
  const fetch = stubFetch(200, { id: '01R' });
  await expect(updateRoom('01R', { turn_cap: 12 })).resolves.toEqual({ id: '01R' });
  expect(fetch).toHaveBeenCalledWith('/api/rooms/01R', expect.objectContaining({ method: 'PATCH', body: '{"turn_cap":12}' }));
});

test('inviteHuman posts the human', async () => {
  const fetch = stubFetch(200, { id: '01R' });
  await expect(inviteHuman('01R', '01H')).resolves.toEqual({ id: '01R' });
  expect(fetch).toHaveBeenCalledWith(
    '/api/rooms/01R/humans',
    expect.objectContaining({ method: 'POST', body: '{"human_id":"01H"}' }),
  );
});

test('addRoomAgent posts the agent', async () => {
  const fetch = stubFetch(200, { id: '01R' });
  await expect(addRoomAgent('01R', '01A')).resolves.toEqual({ id: '01R' });
  expect(fetch).toHaveBeenCalledWith(
    '/api/rooms/01R/agents',
    expect.objectContaining({ method: 'POST', body: '{"agent_id":"01A"}' }),
  );
});

test('removeRoomAgent deletes the agent and encodes both ids', async () => {
  const fetch = stubFetch(200, { id: 'a/b' });
  await expect(removeRoomAgent('a/b', 'c d')).resolves.toEqual({ id: 'a/b' });
  expect(fetch).toHaveBeenCalledWith('/api/rooms/a%2Fb/agents/c%20d', expect.objectContaining({ method: 'DELETE' }));
});

test('sessionLost runs the unauthenticated handler', () => {
  const handler = vi.fn();
  onUnauthenticated(handler);

  sessionLost();

  expect(handler).toHaveBeenCalledTimes(1);
});

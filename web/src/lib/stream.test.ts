import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { onUnauthenticated } from './api';
import { frameParser, openStream, type Frame, type Stream, type StreamState } from './stream';

// --- frameParser ---

test('a frame cut across chunks arrives once, whole', () => {
  const frames: Frame[] = [];
  const feed = frameParser((f) => frames.push(f));
  feed('id: 7\nevent: room.cha');
  feed('nged\ndata: {"room":\ndata: 1}\n');
  expect(frames).toEqual([]);
  feed('\n');
  expect(frames).toEqual([{ id: '7', event: 'room.changed', data: '{"room":\n1}' }]);
});

test('a heartbeat comment emits nothing', () => {
  const frames: Frame[] = [];
  frameParser((f) => frames.push(f))(': hb\n\n');
  expect(frames).toEqual([]);
});

test('CRLF line endings parse the same', () => {
  const frames: Frame[] = [];
  const feed = frameParser((f) => frames.push(f));
  // the second chunk starts between a CR and its LF
  feed('id: 7\r\nevent: room.changed\r\ndata: {"room":\r');
  feed('\ndata: 1}\r\n\r\n: hb\r\n\r\n');
  expect(frames).toEqual([{ id: '7', event: 'room.changed', data: '{"room":\n1}' }]);
});

test('only one space after the colon is dropped, and a colon in the value is kept', () => {
  const frames: Frame[] = [];
  frameParser((f) => frames.push(f))('event:a\ndata:  b: c\n\n');
  expect(frames).toEqual([{ id: '', event: 'a', data: ' b: c' }]);
});

test('each frame starts empty, and an unknown field is ignored', () => {
  const frames: Frame[] = [];
  frameParser((f) => frames.push(f))('id: 1\nevent: a\ndata: x\n\nretry: 5\nevent: b\ndata: y\n\n');
  expect(frames).toEqual([
    { id: '1', event: 'a', data: 'x' },
    { id: '', event: 'b', data: 'y' },
  ]);
});

// --- openStream ---

type Call = { ready: true } | { event: string; data: unknown };

const encoder = new TextEncoder();
const fetchMock = vi.fn<(path: string, init: RequestInit) => Promise<Response>>();
const lost = vi.fn();

let bodies: ReadableStreamDefaultController<Uint8Array>[];
let calls: Call[];
let states: StreamState[];
let stream: Stream | undefined;

// connect answers 200 with a body the test writes to. The body takes no notice
// of the request's signal, so nothing here passes only because an abort
// happened to end a read.
function connect(): Promise<Response> {
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
    },
  });
  bodies.push(controller);
  return Promise.resolve(
    new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
  );
}

// hang never answers, and rejects when the request is aborted, as fetch does.
function hang(_path: string, init: RequestInit): Promise<Response> {
  return new Promise((_resolve, reject) => {
    const signal = init.signal as AbortSignal;
    signal.addEventListener('abort', () => reject(signal.reason));
  });
}

function status(code: number): Response {
  return new Response(null, { status: code });
}

function open(): Stream {
  stream = openStream({
    ready: () => calls.push({ ready: true }),
    event: (event, data) => calls.push({ event, data }),
    state: (s) => states.push(s),
  });
  return stream;
}

// flush lets pending reads and their handlers run without moving the clock.
function flush(): Promise<unknown> {
  return vi.advanceTimersByTimeAsync(0);
}

function advance(ms: number): Promise<unknown> {
  return vi.advanceTimersByTimeAsync(ms);
}

// push writes to the nth connection's body.
async function push(n: number, chunk: string | Uint8Array): Promise<void> {
  bodies[n].enqueue(typeof chunk === 'string' ? encoder.encode(chunk) : chunk);
  await flush();
}

async function end(n: number): Promise<void> {
  bodies[n].close();
  await flush();
}

function url(n: number): string {
  return fetchMock.mock.calls[n][0];
}

function signal(n: number): AbortSignal {
  return fetchMock.mock.calls[n][1].signal as AbortSignal;
}

beforeEach(() => {
  vi.useFakeTimers();
  bodies = [];
  calls = [];
  states = [];
  stream = undefined;
  fetchMock.mockReset();
  fetchMock.mockImplementation(connect);
  vi.stubGlobal('fetch', fetchMock);
  lost.mockReset();
  onUnauthenticated(lost);
});

afterEach(async () => {
  stream?.close();
  for (const body of bodies) {
    try {
      body.close();
    } catch {
      // already closed
    }
  }
  await flush();
  expect(vi.getTimerCount()).toBe(0);
  onUnauthenticated(() => {});
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test('the request is for /api/events as an event stream', async () => {
  open();
  await flush();

  expect(fetchMock).toHaveBeenCalledTimes(1);
  const [path, init] = fetchMock.mock.calls[0];
  expect(path).toBe('/api/events');
  expect((init.headers as Record<string, string>)['Accept']).toBe('text/event-stream');
  expect(init.signal).toBeInstanceOf(AbortSignal);
});

test('ready calls the handler and the state goes connecting, live', async () => {
  open();
  expect(states).toEqual(['connecting']);
  await flush();
  expect(states).toEqual(['connecting']);
  expect(calls).toEqual([]);

  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  expect(calls).toEqual([{ ready: true }]);
  expect(states).toEqual(['connecting', 'live']);
});

test("an event's data arrives parsed", async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  await push(0, 'id: 13\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n\n');

  expect(calls[1]).toEqual({ event: 'room.changed', data: { room: { id: '01R' } } });
  expect(calls).toHaveLength(2);
});

test('a frame with bad JSON is dropped and the next one still arrives', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  await push(0, 'id: 13\nevent: room.changed\ndata: {"room":\n\n');
  await push(0, 'id: 14\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n\n');

  expect(calls).toEqual([
    { ready: true },
    { event: 'room.changed', data: { room: { id: '01R' } } },
  ]);
  expect(states).toEqual(['connecting', 'live']);
  expect(fetchMock).toHaveBeenCalledTimes(1);

  // the dropped frame's id still counted
  await push(0, 'id: 15\nevent: room.changed\ndata: nope\n\n');
  await end(0);
  await advance(1000);
  expect(url(1)).toBe('/api/events?cursor=15');
});

test('a character cut across chunks arrives whole', async () => {
  open();
  const bytes = encoder.encode('event: room.changed\ndata: {"name":"café ☕"}\n\n');
  const cut = bytes.indexOf(0xe2) + 1; // inside the three bytes of ☕

  await push(0, bytes.slice(0, cut));
  await push(0, bytes.slice(cut));

  expect(calls).toEqual([{ event: 'room.changed', data: { name: 'café ☕' } }]);
});

test('a body that ends reconnects after 1s with the last id as cursor', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  await end(0);
  expect(states).toEqual(['connecting', 'live', 'waiting']);

  await advance(999);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  await advance(1);
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(url(1)).toBe('/api/events?cursor=12');
  expect(states).toEqual(['connecting', 'live', 'waiting', 'connecting']);

  await push(1, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  expect(calls).toEqual([{ ready: true }, { ready: true }]);
  expect(states).toEqual(['connecting', 'live', 'waiting', 'connecting', 'live']);
});

test('the cursor is the latest id, not the highest', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  await push(0, 'id: 14\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n\n');
  await end(0);
  await advance(1000);
  expect(url(1)).toBe('/api/events?cursor=14');

  // the database was rolled back: ready names a lower cursor
  await push(1, 'id: 9\nevent: ready\ndata: {"cursor":9}\n\n');
  await end(1);
  await advance(1000);
  expect(url(2)).toBe('/api/events?cursor=9');
});

test('half a frame from a connection that ended is not carried into the next', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  await push(0, 'id: 13\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n');
  await end(0);
  await advance(1000);
  expect(url(1)).toBe('/api/events?cursor=12');

  await push(1, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  expect(calls).toEqual([{ ready: true }, { ready: true }]);
});

test('the delays grow to 30s and reset after ready', async () => {
  fetchMock.mockImplementation(() => Promise.reject(new TypeError('Failed to fetch')));
  open();
  await flush();
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting', 'waiting']);

  let n = 1;
  for (const delay of [1000, 2000, 4000, 8000, 16000, 30000, 30000]) {
    await advance(delay - 1);
    expect(fetchMock).toHaveBeenCalledTimes(n);
    await advance(1);
    n += 1;
    expect(fetchMock).toHaveBeenCalledTimes(n);
  }

  // the next one connects, and a drop after its ready starts over at 1s
  fetchMock.mockImplementation(connect);
  await advance(30000);
  n += 1;
  expect(fetchMock).toHaveBeenCalledTimes(n);
  await push(0, 'id: 3\nevent: ready\ndata: {"cursor":3}\n\n');
  await end(0);

  await advance(999);
  expect(fetchMock).toHaveBeenCalledTimes(n);
  await advance(1);
  expect(fetchMock).toHaveBeenCalledTimes(n + 1);
});

test('a connection that drops before ready does not reset the delays', async () => {
  open();
  await end(0);
  await advance(1000);
  expect(fetchMock).toHaveBeenCalledTimes(2);

  await end(1);
  await advance(1999);
  expect(fetchMock).toHaveBeenCalledTimes(2);
  await advance(1);
  expect(fetchMock).toHaveBeenCalledTimes(3);
});

test('60s of silence reconnects; a heartbeat every 25s does not', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  for (let i = 0; i < 8; i++) {
    await advance(25000);
    await push(0, ': hb\n\n');
  }
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting', 'live']);
  expect(signal(0).aborted).toBe(false);

  await advance(59999);
  expect(states).toEqual(['connecting', 'live']);
  await advance(1);
  expect(states).toEqual(['connecting', 'live', 'waiting']);
  expect(signal(0).aborted).toBe(true);

  await advance(1000);
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(url(1)).toBe('/api/events?cursor=12');

  // what the abandoned connection still delivers is not heard
  await push(0, 'id: 13\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n\n');
  expect(calls).toEqual([{ ready: true }]);
});

test('a request that never answers is given up on after 60s', async () => {
  fetchMock.mockImplementation(hang);
  open();

  await advance(59999);
  expect(states).toEqual(['connecting']);
  await advance(1);
  expect(states).toEqual(['connecting', 'waiting']);
  expect(signal(0).aborted).toBe(true);

  await advance(999);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  await advance(1);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test('a connection that sends ready and then nothing is given up on after 60s', async () => {
  open();
  await advance(40000);
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  await advance(59999);
  expect(states).toEqual(['connecting', 'live']);
  await advance(1);
  expect(states).toEqual(['connecting', 'live', 'waiting']);
});

test('a 401 calls sessionLost and never retries', async () => {
  fetchMock.mockImplementation(() => Promise.resolve(status(401)));
  const s = open();
  await flush();

  expect(lost).toHaveBeenCalledTimes(1);

  await advance(5 * 60 * 1000);
  s.kick();
  await flush();
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(lost).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting']);
});

test('a 400 retries without the cursor', async () => {
  open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  await end(0);

  fetchMock.mockImplementationOnce(() => Promise.resolve(status(400)));
  await advance(1000);
  expect(url(1)).toBe('/api/events?cursor=12');
  expect(states).toEqual(['connecting', 'live', 'waiting', 'connecting', 'waiting']);
  expect(lost).not.toHaveBeenCalled();

  await advance(2000);
  expect(fetchMock).toHaveBeenCalledTimes(3);
  expect(url(2)).toBe('/api/events');
});

test('a 502 retries', async () => {
  fetchMock.mockImplementationOnce(() => Promise.resolve(status(502)));
  open();
  await flush();
  expect(states).toEqual(['connecting', 'waiting']);

  await advance(1000);
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(url(1)).toBe('/api/events');
  expect(lost).not.toHaveBeenCalled();
});

test('kick reconnects at once while waiting, and does nothing while live', async () => {
  const s = open();
  s.kick(); // connecting
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  s.kick(); // live
  await flush();
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(signal(0).aborted).toBe(false);

  await end(0);
  expect(states.at(-1)).toBe('waiting');
  s.kick();
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect(url(1)).toBe('/api/events?cursor=12');
  expect(states.at(-1)).toBe('connecting');

  // the delay that was running does not connect a second time
  await advance(5000);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test('close aborts, and a frame pushed afterwards reaches no handler', async () => {
  const s = open();
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  s.close();
  expect(signal(0).aborted).toBe(true);
  expect(vi.getTimerCount()).toBe(0);

  await push(0, 'id: 13\nevent: room.changed\ndata: {"room":{"id":"01R"}}\n\n');
  await end(0);
  await advance(5 * 60 * 1000);
  s.kick();
  await flush();

  expect(calls).toEqual([{ ready: true }]);
  expect(states).toEqual(['connecting', 'live']);
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test('close while waiting cancels the reconnect', async () => {
  const s = open();
  await end(0);
  expect(states).toEqual(['connecting', 'waiting']);

  s.close();
  expect(vi.getTimerCount()).toBe(0);
  await advance(5 * 60 * 1000);

  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting', 'waiting']);
});

test('close before the request answers is quiet', async () => {
  fetchMock.mockImplementation(hang);
  const s = open();

  s.close();
  await advance(5 * 60 * 1000);

  expect(signal(0).aborted).toBe(true);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting']);
});

test('close from a handler stops the frames behind it in the same chunk', async () => {
  const seen: string[] = [];
  const s = openStream({
    ready: () => {},
    event: (name) => {
      seen.push(name);
      s.close();
    },
    state: () => {},
  });
  stream = s;

  await push(0, 'event: a\ndata: 1\n\nevent: b\ndata: 2\n\n');

  expect(seen).toEqual(['a']);
});

// --- handlers that throw ---

test('an event handler that throws is reported, and the next frame still arrives', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  const boom = new Error('boom');
  const seen: unknown[] = [];
  stream = openStream({
    ready: () => {},
    event: (_name, data) => {
      seen.push(data);
      if (seen.length === 1) {
        throw boom;
      }
    },
    state: (s) => states.push(s),
  });
  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');

  // the frame behind it in the same chunk, and one in a later chunk
  await push(0, 'id: 13\nevent: a\ndata: 1\n\nid: 14\nevent: a\ndata: 2\n\n');
  await push(0, 'id: 15\nevent: a\ndata: 3\n\n');
  await advance(5000);

  expect(seen).toEqual([1, 2, 3]);
  expect(errors).toHaveBeenCalledTimes(1);
  expect(errors.mock.calls[0]).toContain(boom);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting', 'live']);
  expect(signal(0).aborted).toBe(false);
  errors.mockRestore();
});

test('a ready handler that throws is reported and does not reconnect', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  const boom = new Error('boom');
  const seen: unknown[] = [];
  stream = openStream({
    ready: () => {
      throw boom;
    },
    event: (_name, data) => seen.push(data),
    state: (s) => states.push(s),
  });

  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  await advance(5000);
  await push(0, 'id: 13\nevent: a\ndata: 1\n\n');

  expect(errors).toHaveBeenCalledTimes(1);
  expect(errors.mock.calls[0]).toContain(boom);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting', 'live']);
  expect(seen).toEqual([1]);
  errors.mockRestore();
});

test('a state handler that throws is reported and the stream still reconnects', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  const boom = new Error('boom');
  stream = openStream({
    ready: () => calls.push({ ready: true }),
    event: () => {},
    state: (s) => {
      states.push(s);
      throw boom;
    },
  });
  expect(fetchMock).toHaveBeenCalledTimes(1);

  await push(0, 'id: 12\nevent: ready\ndata: {"cursor":12}\n\n');
  expect(calls).toEqual([{ ready: true }]);
  await end(0);
  await advance(1000);

  expect(url(1)).toBe('/api/events?cursor=12');
  expect(states).toEqual(['connecting', 'live', 'waiting', 'connecting']);
  expect(errors).toHaveBeenCalledTimes(4);
  expect(errors.mock.calls[0]).toContain(boom);
  errors.mockRestore();
});

test('a 401 stops for good even when the session handler throws', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  const boom = new Error('boom');
  lost.mockImplementation(() => {
    throw boom;
  });
  fetchMock.mockImplementation(() => Promise.resolve(status(401)));
  const s = open();
  await flush();

  await advance(5 * 60 * 1000);
  s.kick();
  await flush();

  expect(lost).toHaveBeenCalledTimes(1);
  expect(errors).toHaveBeenCalledTimes(1);
  expect(errors.mock.calls[0]).toContain(boom);
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(states).toEqual(['connecting']);
  errors.mockRestore();
});

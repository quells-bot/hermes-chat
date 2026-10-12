// The event stream, read with fetch instead of EventSource: fetch shows the
// status code (a 401 signs the person out, a 502 during an upgrade is retried)
// and the heartbeat comments, which are the only way to notice a connection
// that died without saying so.

import { sessionLost } from './api';

export type Frame = { id: string; event: string; data: string };

// frameParser returns a function to feed decoded text to, in chunks cut
// anywhere. It reads the format as the server writes it: `field: value` lines,
// several data lines joined with a newline, a blank line ending the frame. A
// line starting with a colon is a comment, and a frame with neither an event
// nor data (a heartbeat) is not emitted.
export function frameParser(onFrame: (f: Frame) => void): (text: string) => void {
  let rest = ''; // the line still being received
  let id = '';
  let event = '';
  let data: string[] = [];

  function line(text: string): void {
    if (text === '') {
      const frame = { id, event, data: data.join('\n') };
      const empty = event === '' && data.length === 0;
      id = '';
      event = '';
      data = [];
      if (!empty) {
        onFrame(frame);
      }
      return;
    }
    const colon = text.indexOf(':');
    if (colon === 0) {
      return; // a comment
    }
    const name = colon < 0 ? text : text.slice(0, colon);
    let value = colon < 0 ? '' : text.slice(colon + 1);
    if (value.startsWith(' ')) {
      value = value.slice(1);
    }
    if (name === 'id') {
      id = value;
    } else if (name === 'event') {
      event = value;
    } else if (name === 'data') {
      data.push(value);
    }
  }

  return (text) => {
    // A line is only read once its \n has arrived, so a chunk cut between a
    // \r and its \n is still one line ending.
    const lines = (rest + text).split('\n');
    rest = lines.pop() ?? '';
    for (const l of lines) {
      line(l.endsWith('\r') ? l.slice(0, -1) : l);
    }
  };
}

export type StreamState = 'connecting' | 'live' | 'waiting';

export type StreamHandlers = {
  ready: () => void; // each time a connection receives `ready`
  event: (name: string, data: unknown) => void; // every other frame, data parsed as JSON
  state: (s: StreamState) => void;
};

export type Stream = { close(): void; kick(): void };

// silenceMs is how long a connection may send nothing, headers included, before
// it is taken for dead. The server sends a heartbeat every 25 seconds.
const silenceMs = 60_000;

// retryDelaysMs are the waits before each reconnect in a row; the last repeats.
const retryDelaysMs = [1000, 2000, 4000, 8000, 16000, 30000];

// openStream connects to /api/events and keeps a connection until close().
// After a failure it reconnects from the id of the last frame it received.
export function openStream(handlers: StreamHandlers): Stream {
  let closed = false;
  let state: StreamState | null = null;
  let cursor: string | null = null; // the latest id received, on any connection
  let failures = 0; // connections lost in a row since the last ready
  // The connection in use, or null while waiting and after close. A connection
  // that is no longer this one has been given up on: whatever it still
  // delivers is ignored.
  let current: AbortController | null = null;
  let watchdog: ReturnType<typeof setTimeout> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;

  // call runs a handler. One that throws is reported and the stream carries
  // on as if it had returned, so the read loop's catch only ever sees the
  // transport fail: a bug in a handler must not look like a lost connection.
  function call(handler: () => void): void {
    try {
      handler();
    } catch (err) {
      console.error('event stream: a handler threw', err);
    }
  }

  function setState(s: StreamState): void {
    if (closed || s === state) {
      return;
    }
    state = s;
    call(() => handlers.state(s));
  }

  // lose gives up on conn and schedules the next one. It is the one way a
  // connection fails, whoever notices first: the read loop or the watchdog.
  function lose(conn: AbortController): void {
    if (conn !== current) {
      return;
    }
    current = null;
    clearTimeout(watchdog);
    conn.abort();
    const delay = retryDelaysMs[Math.min(failures, retryDelaysMs.length - 1)];
    failures += 1;
    retry = setTimeout(connect, delay);
    setState('waiting');
  }

  // The watchdog does not wait for the abort to end the pending read: it
  // declares the connection lost itself.
  function heard(conn: AbortController): void {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => lose(conn), silenceMs);
  }

  function frame(conn: AbortController, f: Frame): void {
    // A handler may have closed the stream earlier in the same chunk.
    if (conn !== current) {
      return;
    }
    if (f.id !== '') {
      cursor = f.id; // assigned, not maximised: ready may name a lower one
    }
    if (f.event === 'ready') {
      failures = 0;
      setState('live');
      if (conn === current) {
        call(() => handlers.ready());
      }
      return;
    }
    let data: unknown;
    try {
      data = JSON.parse(f.data);
    } catch {
      return; // not JSON: dropped, and its id still counts
    }
    call(() => handlers.event(f.event, data));
  }

  async function read(conn: AbortController): Promise<void> {
    try {
      const path =
        cursor === null ? '/api/events' : `/api/events?cursor=${encodeURIComponent(cursor)}`;
      const res = await fetch(path, {
        headers: { Accept: 'text/event-stream' },
        signal: conn.signal,
      });
      if (conn !== current) {
        return;
      }
      if (res.status === 401) {
        close();
        call(sessionLost);
        return;
      }
      if (res.status === 400) {
        cursor = null; // the server refused the cursor
      }
      if (res.status !== 200 || res.body === null) {
        lose(conn);
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      // One parser per connection: half a frame from a connection that ended
      // is not continued by the next.
      const feed = frameParser((f) => frame(conn, f));
      for (;;) {
        const { done, value } = await reader.read();
        if (conn !== current) {
          return;
        }
        if (done) {
          break;
        }
        heard(conn);
        feed(decoder.decode(value, { stream: true }));
      }
      lose(conn);
    } catch {
      // The request failed, the body broke, or it was aborted. After close()
      // or the watchdog this connection is no longer current and lose does
      // nothing.
      lose(conn);
    }
  }

  function connect(): void {
    retry = undefined;
    const conn = new AbortController();
    current = conn;
    heard(conn); // waiting for the headers is silence too
    setState('connecting');
    if (conn === current) {
      void read(conn);
    }
  }

  function close(): void {
    closed = true;
    clearTimeout(watchdog);
    clearTimeout(retry);
    retry = undefined;
    current?.abort();
    current = null;
  }

  function kick(): void {
    if (closed || retry === undefined) {
      return;
    }
    clearTimeout(retry);
    connect();
  }

  connect();
  return { close, kick };
}

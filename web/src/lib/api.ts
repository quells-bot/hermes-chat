// The human API, as the browser calls it. Paths are relative, so the session
// cookie and the Origin header the server checks go with every request.

export type Me = { id: string; username: string };

export class ApiError extends Error {
  readonly status: number; // 0 when no response arrived
  readonly code: string; // the server's code, or 'network', or 'http'
  readonly retryAfter: number | null; // seconds, from Retry-After

  constructor(status: number, code: string, message: string, retryAfter: number | null = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

// requestTimeoutMs is how long one request may take, headers and body.
const requestTimeoutMs = 15_000;

let unauthenticated: () => void = () => {};

// onUnauthenticated sets what runs when the server says the session is gone.
// There is one handler; a later call replaces it.
export function onUnauthenticated(handler: () => void): void {
  unauthenticated = handler;
}

function retryAfter(res: Response): number | null {
  const secs = Number.parseInt(res.headers.get('Retry-After') ?? '', 10);
  return Number.isFinite(secs) ? secs : null;
}

// errorFrom reads the server's error shape, and falls back to a plain message
// for anything else: a proxy's HTML page, an empty or truncated body.
function errorFrom(res: Response, text: string): ApiError {
  try {
    const body: unknown = JSON.parse(text);
    const error = (body as { error?: { code?: unknown; message?: unknown } } | null)?.error;
    if (typeof error?.code === 'string' && typeof error.message === 'string') {
      return new ApiError(res.status, error.code, error.message, retryAfter(res));
    }
  } catch {
    // not JSON
  }
  return new ApiError(res.status, 'http', `server error (${res.status})`, retryAfter(res));
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  const init: RequestInit = { method, headers };
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }

  // A stalled connection (a phone leaving wifi) neither answers nor fails, so
  // a request that takes too long is given up on.
  const abort = new AbortController();
  const timer = setTimeout(() => abort.abort(), requestTimeoutMs);
  init.signal = abort.signal;

  let res: Response;
  let text: string;
  try {
    res = await fetch(path, init);
    text = await res.text();
  } catch {
    throw new ApiError(0, 'network', 'cannot reach the server');
  } finally {
    clearTimeout(timer);
  }

  if (!res.ok) {
    const err = errorFrom(res, text);
    // A wrong password is also a 401, and is not a lost session.
    if (err.status === 401 && err.code !== 'invalid_credentials') {
      unauthenticated();
    }
    throw err;
  }
  if (text === '') {
    return undefined as T;
  }
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(res.status, 'http', `server error (${res.status})`);
  }
}

export function login(username: string, password: string): Promise<Me> {
  return request<Me>('POST', '/api/login', { username, password });
}

export function logout(): Promise<void> {
  return request<void>('POST', '/api/logout');
}

export function me(): Promise<Me> {
  return request<Me>('GET', '/api/me');
}

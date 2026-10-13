// The human API, as the browser calls it. Paths are relative, so the session
// cookie and the Origin header the server checks go with every request.

import type { Activity, Agent, Human, Message, Note, Room } from './types';

export type Me = Human;

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

// sessionLost runs that handler for callers that do not go through request(),
// such as the event stream.
export function sessionLost(): void {
  unauthenticated();
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

export function listRooms(): Promise<Room[]> {
  return request<{ rooms: Room[] }>('GET', '/api/rooms').then((r) => r.rooms);
}

export function getRoom(id: string): Promise<Room> {
  return request<Room>('GET', `/api/rooms/${encodeURIComponent(id)}`);
}

export function listAgents(): Promise<Agent[]> {
  return request<{ agents: Agent[] }>('GET', '/api/agents').then((r) => r.agents);
}

export function getHalt(): Promise<boolean> {
  return request<{ halted: boolean }>('GET', '/api/halt').then((r) => r.halted);
}

export function stopEverything(): Promise<boolean> {
  return request<{ halted: boolean }>('POST', '/api/halt').then((r) => r.halted);
}

export function resumeEverything(): Promise<boolean> {
  return request<{ halted: boolean }>('DELETE', '/api/halt').then((r) => r.halted);
}

export function createRoom(topic: string, agentIds: string[]): Promise<Room> {
  return request<Room>('POST', '/api/rooms', { topic, agent_ids: agentIds });
}

// openDM answers 201 when it created the room and 200 when it was there.
export function openDM(agentId: string): Promise<Room> {
  return request<Room>('POST', '/api/dms', { agent_id: agentId });
}

// listMessages reads one page of a room, oldest first. With before, the page
// ends just short of that message.
export function listMessages(roomId: string, before?: string, limit = 50): Promise<Message[]> {
  let query = `limit=${limit}`;
  if (before !== undefined) {
    query += `&before=${encodeURIComponent(before)}`;
  }
  return request<{ messages: Message[] }>('GET', `/api/rooms/${encodeURIComponent(roomId)}/messages?${query}`).then(
    (r) => r.messages,
  );
}

export function getMessage(roomId: string, messageId: string): Promise<Message> {
  return request<Message>('GET', `/api/rooms/${encodeURIComponent(roomId)}/messages/${encodeURIComponent(messageId)}`);
}

export function listActivity(roomId: string, messageId: string): Promise<Activity[]> {
  return request<{ activities: Activity[] }>(
    'GET',
    `/api/rooms/${encodeURIComponent(roomId)}/messages/${encodeURIComponent(messageId)}/activity`,
  ).then((r) => r.activities);
}

export function markRoomRead(roomId: string, messageId: string): Promise<void> {
  return request<void>('PUT', `/api/rooms/${encodeURIComponent(roomId)}/read`, { message_id: messageId });
}

// postMessage posts to the room. A retried post with the same client_id is
// answered with the first message, not a second. replyTo, when given, is the
// message this one replies to.
export function postMessage(roomId: string, text: string, clientId: string, replyTo?: string): Promise<Message> {
  const body: { text: string; client_id: string; reply_to_id?: string } = { text, client_id: clientId };
  if (replyTo !== undefined) {
    body.reply_to_id = replyTo;
  }
  return request<Message>('POST', `/api/rooms/${encodeURIComponent(roomId)}/messages`, body);
}

// steer redirects the agent's running turn. It too is safe to retry with the
// same client_id.
export function steer(roomId: string, agentId: string, text: string, clientId: string): Promise<Message> {
  return request<Message>(
    'POST',
    `/api/rooms/${encodeURIComponent(roomId)}/agents/${encodeURIComponent(agentId)}/steer`,
    { text, client_id: clientId },
  );
}

// cancelMessage cancels every waiting copy of a message at once; the route
// answers 204 with no body.
export function cancelMessage(roomId: string, messageId: string): Promise<void> {
  return request<void>(
    'POST',
    `/api/rooms/${encodeURIComponent(roomId)}/messages/${encodeURIComponent(messageId)}/cancel`,
  );
}

// answerPrompt answers the prompt riding a message; the reply is the message
// with its prompt.
export function answerPrompt(roomId: string, messageId: string, answer: string): Promise<Message> {
  return request<Message>(
    'POST',
    `/api/rooms/${encodeURIComponent(roomId)}/messages/${encodeURIComponent(messageId)}/answer`,
    { answer },
  );
}

// stopTurn asks the agent to stop its running turn; the route answers 204.
export function stopTurn(roomId: string, agentId: string): Promise<void> {
  return request<void>('POST', `/api/rooms/${encodeURIComponent(roomId)}/agents/${encodeURIComponent(agentId)}/stop`);
}

// pauseRoom and resumeRoom answer with the room in its new state.
export function pauseRoom(roomId: string): Promise<Room> {
  return request<Room>('POST', `/api/rooms/${encodeURIComponent(roomId)}/pause`);
}

export function resumeRoom(roomId: string): Promise<Room> {
  return request<Room>('POST', `/api/rooms/${encodeURIComponent(roomId)}/resume`);
}

// attachmentUrl is where the browser fetches an upload; no request is made here.
export function attachmentUrl(roomId: string, attachmentId: string): string {
  return `/api/rooms/${encodeURIComponent(roomId)}/attachments/${encodeURIComponent(attachmentId)}`;
}

// The note is the signed-in human's own; each call answers with the whole note.
export function getNote(): Promise<Note> {
  return request<Note>('GET', '/api/note');
}

export function setNoteText(text: string): Promise<Note> {
  return request<Note>('PUT', '/api/note', { text });
}

// updateNoteEntry changes only the fields it is given.
export function updateNoteEntry(entryId: string, changes: { text?: string; approved?: boolean }): Promise<Note> {
  return request<Note>('PATCH', `/api/note/entries/${encodeURIComponent(entryId)}`, changes);
}

export function deleteNoteEntry(entryId: string): Promise<Note> {
  return request<Note>('DELETE', `/api/note/entries/${encodeURIComponent(entryId)}`);
}

export function listHumans(): Promise<Human[]> {
  return request<{ humans: Human[] }>('GET', '/api/humans').then((r) => r.humans);
}

export type RoomChanges = { topic?: string; archived?: boolean; turn_cap?: number };

// updateRoom changes only the fields it is given; each room call below answers
// with the room as it now is.
export function updateRoom(roomId: string, changes: RoomChanges): Promise<Room> {
  return request<Room>('PATCH', `/api/rooms/${encodeURIComponent(roomId)}`, changes);
}

export function inviteHuman(roomId: string, humanId: string): Promise<Room> {
  return request<Room>('POST', `/api/rooms/${encodeURIComponent(roomId)}/humans`, { human_id: humanId });
}

export function addRoomAgent(roomId: string, agentId: string): Promise<Room> {
  return request<Room>('POST', `/api/rooms/${encodeURIComponent(roomId)}/agents`, { agent_id: agentId });
}

export function removeRoomAgent(roomId: string, agentId: string): Promise<Room> {
  return request<Room>('DELETE', `/api/rooms/${encodeURIComponent(roomId)}/agents/${encodeURIComponent(agentId)}`);
}

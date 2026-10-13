// Test-only builders for the server's shapes. No production file imports this.
import type { Activity, Human, Message, Note, NoteEntry, Room, RoomAgent } from './types';

export function agent(overrides: Partial<RoomAgent> = {}): RoomAgent {
  return {
    id: 'agent-1',
    handle: 'helper',
    display_name: 'Helper',
    revoked: false,
    connected: false,
    working: false,
    ...overrides,
  };
}

export function room(overrides: Partial<Room> = {}): Room {
  return {
    id: 'room-1',
    flavor: 'conversation',
    topic: 'Topic',
    archived: false,
    created_at: '2026-01-01T00:00:00.000Z',
    humans: [],
    agents: [],
    last_message_id: null,
    last_read_message_id: null,
    paused: false,
    pause_reason: null,
    turn_cap: 6,
    halted: false,
    open_prompts: 0,
    ...overrides,
  };
}

export function message(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg-1',
    room_id: 'room-1',
    author: { kind: 'human', id: 'human-1', name: 'kai' },
    text: 'hello',
    reply_to_id: null,
    deliveries: [],
    created_at: '2026-01-01T00:00:00.000Z',
    prompt: null,
    attachments: [],
    client_id: null,
    streaming: false,
    ...overrides,
  };
}

export function activity(overrides: Partial<Activity> = {}): Activity {
  return {
    id: 'act-1',
    message_id: 'msg-1',
    agent: agent(),
    kind: 'tool',
    text: '',
    tool: 'search',
    preview: '',
    arguments: '',
    duration_ms: 10,
    succeeded: true,
    created_at: '2026-01-01T00:00:00.000Z',
    ...overrides,
  };
}

export function human(overrides: Partial<Human> = {}): Human {
  return { id: 'human-1', username: 'kai', ...overrides };
}

export function noteEntry(overrides: Partial<NoteEntry> = {}): NoteEntry {
  const { id, handle, display_name, revoked } = agent();
  return {
    id: 'entry-1',
    agent: { id, handle, display_name, revoked },
    text: 'likes tea',
    approved: false,
    created_at: '2026-01-01T00:00:00.000Z',
    ...overrides,
  };
}

export function note(overrides: Partial<Note> = {}): Note {
  return { text: '', entries: [], ...overrides };
}

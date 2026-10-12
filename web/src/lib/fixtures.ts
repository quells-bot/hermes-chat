// Test-only builders for the server's shapes. No production file imports this.
import type { Room, RoomAgent } from './types';

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

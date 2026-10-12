// The server's JSON, as the browser reads it. Field names are the wire's
// (docs/openapi.yaml).

export type Human = { id: string; username: string };

export type Agent = { id: string; handle: string; display_name: string; revoked: boolean };

export type RoomAgent = Agent & { connected: boolean; working: boolean };

export type Flavor = 'dm' | 'channel' | 'conversation';

export type Room = {
  id: string;
  flavor: Flavor;
  topic: string;
  archived: boolean;
  created_at: string;
  humans: Human[];
  agents: RoomAgent[];
  last_message_id: string | null;
  last_read_message_id: string | null;
  paused: boolean;
  pause_reason: 'human' | 'turn_cap' | null;
  turn_cap: number;
  halted: boolean;
  open_prompts: number;
};

export type DeliveryState = 'waiting' | 'delivered' | 'working' | 'done' | 'failed' | 'cancelled';

export type Delivery = { agent: Agent; state: DeliveryState };

// Only what slice 2 reads; slice 3 completes it.
export type Message = { id: string; room_id: string; deliveries: Delivery[] };

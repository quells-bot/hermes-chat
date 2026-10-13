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

export type Author = { kind: 'human' | 'agent'; id: string; name: string };

export type Prompt = {
  kind: 'approval' | 'clarify';
  choices: string[];
  state: 'open' | 'answered' | 'expired';
  answer: string | null;
  answered_by: Human | null;
  closed_at: string | null;
};

export type Attachment = { id: string; name: string; content_type: string; size: number; image: boolean };

export type Message = {
  id: string;
  room_id: string;
  author: Author | null;
  text: string;
  reply_to_id: string | null;
  deliveries: Delivery[];
  created_at: string;
  prompt: Prompt | null;
  attachments: Attachment[];
  client_id: string | null;
  streaming: boolean;
};

export type Activity = {
  id: string;
  message_id: string;
  agent: Agent;
  kind: 'remark' | 'tool';
  text: string;
  tool: string;
  preview: string;
  arguments: string;
  duration_ms: number | null;
  succeeded: boolean | null;
  created_at: string;
};

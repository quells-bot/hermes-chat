// @mention completion in the composer. The rule mirrors the server's
// (mentionedAgents in bus/delivery.go): a mention is a whitespace-delimited
// token starting with '@', after at most one '('.
import { byName } from './rooms';
import type { RoomAgent } from './types';

export type MentionAt = { start: number; query: string }; // start is the index of '@'

// The mention being typed at caret, or null. The token is what lies between
// the last whitespace before caret (or the start) and caret, less one leading
// '('. It is a mention when it starts with '@' and the character at caret is
// whitespace or the end of the text.
export function mentionAt(text: string, caret: number): MentionAt | null {
  if (caret < 0 || caret > text.length) return null;
  if (caret < text.length && !/\s/.test(text[caret])) return null;
  let begin = caret;
  while (begin > 0 && !/\s/.test(text[begin - 1])) begin--;
  if (text[begin] === '(') begin++;
  if (begin >= caret || text[begin] !== '@') return null;
  return { start: begin, query: text.slice(begin + 1, caret) };
}

// Agents not revoked whose handle or display_name starts with query, case
// ignored, by handle.
export function candidates(agents: RoomAgent[], query: string): RoomAgent[] {
  const q = query.toLowerCase();
  return agents
    .filter((a) => !a.revoked && (a.handle.toLowerCase().startsWith(q) || a.display_name.toLowerCase().startsWith(q)))
    .sort((a, b) => byName(a.handle, b.handle));
}

// The text with '@handle' in place of what was typed from at.start to caret,
// followed by one space unless whitespace already follows; and where the
// caret goes: after that space.
export function complete(
  text: string,
  at: MentionAt,
  caret: number,
  handle: string,
): { text: string; caret: number } {
  const after = text.slice(caret);
  const gap = after === '' || !/^\s/.test(after) ? ' ' : '';
  const head = `${text.slice(0, at.start)}@${handle}`;
  return { text: `${head}${gap}${after}`, caret: head.length + 1 };
}

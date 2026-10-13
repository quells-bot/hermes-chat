// Each room's unsent composer text, kept in memory so that moving to another
// room and back does not lose it. Not reactive and never written to browser
// storage: it goes with the page, and Home empties it when it unmounts.
const drafts = new Map<string, string>();

export function getDraft(roomId: string): string {
  return drafts.get(roomId) ?? '';
}

export function setDraft(roomId: string, text: string): void {
  if (text === '') {
    drafts.delete(roomId);
  } else {
    drafts.set(roomId, text);
  }
}

export function clearDrafts(): void {
  drafts.clear();
}

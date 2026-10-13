// How times, sizes and names are written on screen.
import type { Agent, Author } from './types';

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  );
}

function dayMonth(d: Date): string {
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}

function hhmm(d: Date): string {
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

// '14:02' on now's local day, the day and month before it otherwise.
export function clock(iso: string, now: Date): string {
  const d = new Date(iso);
  return sameDay(d, now) ? hhmm(d) : `${dayMonth(d)} ${hhmm(d)}`;
}

// How long ago, coarsely. A time ahead of now (clock skew) reads as now.
export function ago(iso: string, now: Date): string {
  const d = new Date(iso);
  const ms = now.getTime() - d.getTime();
  if (ms < 60_000) return 'now';
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)} min`;
  if (ms < 86_400_000) return `${Math.floor(ms / 3_600_000)} h`;
  if (ms < 172_800_000) return 'yesterday';
  return dayMonth(d);
}

// One line of a longer text: whitespace runs become one space, and it is cut
// to 280 code points (never inside a surrogate pair).
export function preview(text: string): string {
  return Array.from(text.replace(/\s+/g, ' ').trim()).slice(0, 280).join('');
}

export function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function duration(ms: number): string {
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const s = Math.floor(ms / 1000);
  return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

// The name an icon is generated from: an agent's handle from the directory
// (its name when the directory lacks it), a human's name, '' for no author.
export function iconName(author: Author | null, agents: Record<string, Agent>): string {
  if (!author) return '';
  if (author.kind === 'agent') return agents[author.id]?.handle ?? author.name;
  return author.name;
}

// The document's keyboard shortcuts: search, and moving between rooms.
import { layout } from './layout.svelte';
import { neighbour } from './rooms';
import { navigate, roomIdOf, route, settingsIdOf } from './route.svelte';
import { openSearch } from './search.svelte';
import { store } from './store.svelte';

export type Shortcut = 'search' | 'next' | 'prev' | 'next-unread' | 'prev-unread';

function inField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName) || target.isContentEditable;
}

export function shortcut(e: KeyboardEvent): Shortcut | null {
  if (e.isComposing) return null;
  if ((e.key === 'k' || e.key === 'K') && e.ctrlKey !== e.metaKey && !e.altKey && !e.shiftKey) {
    return 'search';
  }
  if (e.key === '/' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && !inField(e.target)) {
    return 'search';
  }
  if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && e.altKey && !e.ctrlKey && !e.metaKey) {
    const dir = e.key === 'ArrowDown' ? 'next' : 'prev';
    return e.shiftKey ? `${dir}-unread` : dir;
  }
  return null;
}

function onKeydown(e: KeyboardEvent): void {
  const which = shortcut(e);
  if (which === null) return;
  e.preventDefault();
  if (which === 'search') {
    openSearch();
    if (!layout.wide && route.path !== '/') navigate('/');
    return;
  }
  const current = roomIdOf(route.path) ?? settingsIdOf(route.path);
  const id = neighbour(Object.values(store.rooms), current, which.startsWith('next') ? 1 : -1, which.endsWith('-unread'));
  if (id !== null) navigate('/rooms/' + id);
}

// startKeys listens for keydown on the document and returns the stop function.
export function startKeys(): () => void {
  document.addEventListener('keydown', onKeydown);
  return () => document.removeEventListener('keydown', onKeydown);
}

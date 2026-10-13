// Where the person is, from the hash: #/ is the room list, #/new the form,
// #/unread the Unread page, #/rooms/<id> a room.

// prev is the path before this one, '/' at the start: a room leads back to
// the page the person came from.
export const route: { path: string; prev: string } = $state({ path: '/', prev: '/' });

function read(): void {
  const path = location.hash.replace(/^#/, '') || '/';
  if (path === route.path) {
    return;
  }
  route.prev = route.path;
  route.path = path;
}

// startRouting reads the hash now and on every change. It returns the stop function.
export function startRouting(): () => void {
  read();
  window.addEventListener('hashchange', read);
  return () => window.removeEventListener('hashchange', read);
}

export function navigate(path: string): void {
  location.hash = path;
}

// replace goes to path in place of the current page, so the browser's back
// skips the page being left.
export function replace(path: string): void {
  location.replace(`#${path}`);
}

// roomIdOf gives the id in '/rooms/<id>', and null for any other path.
export function roomIdOf(path: string): string | null {
  return /^\/rooms\/([^/]+)$/.exec(path)?.[1] ?? null;
}

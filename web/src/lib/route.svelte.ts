// Where the person is, from the hash: #/ is the room list, #/new the form.

export const route: { path: string } = $state({ path: '/' });

function read(): void {
  route.path = location.hash.replace(/^#/, '') || '/';
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

// Who is signed in. Screens read `session`; nothing else holds this state.

import { ApiError, login, logout, me, onUnauthenticated, type Me } from './api';

type Session = {
  status: 'loading' | 'in' | 'out' | 'error';
  me: Me | null;
  error: string; // why status is 'error'
};

export const session: Session = $state({ status: 'loading', me: null, error: '' });

function signedOut(): void {
  session.status = 'out';
  session.me = null;
}

// A 401 from any request returns the person to the login screen.
onUnauthenticated(signedOut);

// start asks the server who this is. It never rejects.
export async function start(): Promise<void> {
  session.status = 'loading';
  session.error = '';
  try {
    session.me = await me();
    session.status = 'in';
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      signedOut();
      return;
    }
    session.me = null;
    session.error = err instanceof Error ? err.message : String(err);
    session.status = 'error';
  }
}

// signIn rejects with the ApiError and leaves the state as it was.
export async function signIn(username: string, password: string): Promise<void> {
  session.me = await login(username, password);
  session.status = 'in';
}

// signOut ends the session here whatever the server answers. It never rejects.
export async function signOut(): Promise<void> {
  try {
    await logout();
  } catch {
    // The cookie may outlive this, but the person asked to leave.
  }
  signedOut();
}

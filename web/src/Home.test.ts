import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { expect, test, vi } from 'vitest';
import { signOut } from './lib/session.svelte';
import Home from './Home.svelte';

vi.mock('./lib/session.svelte', () => ({
  session: { status: 'in', me: { id: '01J', username: 'kai' }, error: '' },
  start: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
}));

test('shows the username', () => {
  render(Home);

  expect(screen.getByText('kai')).toBeInTheDocument();
});

test('Log out calls signOut', async () => {
  render(Home);

  await userEvent.click(screen.getByRole('button', { name: 'Log out' }));

  expect(signOut).toHaveBeenCalledTimes(1);
});

import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import * as api from './lib/api';
import App from './App.svelte';

// The real session module runs; only the requests are replaced.
vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  login: vi.fn(),
  logout: vi.fn(),
  me: vi.fn(),
}));

const kai = { id: '01J', username: 'kai' };
const noSession = () => new api.ApiError(401, 'unauthenticated', 'authentication required');

beforeEach(() => {
  vi.mocked(api.login).mockReset();
  vi.mocked(api.logout).mockReset();
  vi.mocked(api.me).mockReset();
});

test('shows only the header while it asks who this is', () => {
  vi.mocked(api.me).mockReturnValue(new Promise(() => {}));

  render(App);

  expect(screen.getByRole('heading', { name: 'Hermes Chat' })).toBeInTheDocument();
  expect(screen.queryByLabelText('Username')).toBeNull();
  expect(screen.queryByRole('button')).toBeNull();
});

test('no session shows the login form', async () => {
  vi.mocked(api.me).mockRejectedValue(noSession());

  render(App);

  expect(await screen.findByLabelText('Username')).toBeInTheDocument();
});

test('a live session shows the username', async () => {
  vi.mocked(api.me).mockResolvedValue(kai);

  render(App);

  expect(await screen.findByText('kai')).toBeInTheDocument();
  expect(screen.queryByLabelText('Password')).toBeNull();
});

test('logging in moves to the signed-in screen', async () => {
  vi.mocked(api.me).mockRejectedValue(noSession());
  vi.mocked(api.login).mockResolvedValue(kai);
  render(App);

  await userEvent.type(await screen.findByLabelText('Username'), 'kai');
  await userEvent.type(screen.getByLabelText('Password'), 'pw{Enter}');

  expect(await screen.findByRole('button', { name: 'Log out' })).toBeInTheDocument();
  expect(screen.getByText('kai')).toBeInTheDocument();
});

test('logging out returns to the login form', async () => {
  vi.mocked(api.me).mockResolvedValue(kai);
  vi.mocked(api.logout).mockResolvedValue();
  render(App);

  await userEvent.click(await screen.findByRole('button', { name: 'Log out' }));

  expect(await screen.findByLabelText('Username')).toBeInTheDocument();
  expect(screen.queryByText('kai')).toBeNull();
});

test('an unreachable server shows the error and Retry', async () => {
  vi.mocked(api.me).mockRejectedValueOnce(new api.ApiError(0, 'network', 'cannot reach the server'));
  render(App);

  expect(await screen.findByRole('alert')).toHaveTextContent('cannot reach the server');

  vi.mocked(api.me).mockResolvedValue(kai);
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));

  expect(await screen.findByText('kai')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).toBeNull();
});

import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import { ApiError } from './lib/api';
import { signIn } from './lib/session.svelte';
import Login from './Login.svelte';

vi.mock('./lib/session.svelte', () => ({
  session: { status: 'out', me: null, error: '' },
  start: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(signIn).mockReset();
});

async function fill(username: string, password: string) {
  await userEvent.type(screen.getByLabelText('Username'), username);
  await userEvent.type(screen.getByLabelText('Password'), password);
}

test('submits the username and password', async () => {
  vi.mocked(signIn).mockResolvedValue();
  render(Login);

  await fill('kai', 'pw');
  await userEvent.click(screen.getByRole('button', { name: 'Log in' }));

  expect(signIn).toHaveBeenCalledTimes(1);
  expect(signIn).toHaveBeenCalledWith('kai', 'pw');
});

test('Enter in the password field submits', async () => {
  vi.mocked(signIn).mockResolvedValue();
  render(Login);

  await fill('kai', 'pw{Enter}');

  expect(signIn).toHaveBeenCalledWith('kai', 'pw');
});

test("shows the server's message on a wrong password", async () => {
  vi.mocked(signIn).mockRejectedValue(new ApiError(401, 'invalid_credentials', 'wrong username or password'));
  render(Login);

  await fill('kai', 'nope{Enter}');

  expect(await screen.findByRole('alert')).toHaveTextContent('wrong username or password');
  expect(screen.getByLabelText('Password')).toHaveValue('');
  expect(screen.getByLabelText('Username')).toHaveValue('kai');
});

test('shows the throttling message with the wait', async () => {
  vi.mocked(signIn).mockRejectedValue(new ApiError(429, 'throttled', 'too many attempts; try again later', 42));
  render(Login);

  await fill('kai', 'pw{Enter}');

  expect(await screen.findByRole('alert')).toHaveTextContent(/^too many attempts; try again later Retry in 42 s\.$/);
});

test('shows a network failure', async () => {
  vi.mocked(signIn).mockRejectedValue(new ApiError(0, 'network', 'cannot reach the server'));
  render(Login);

  await fill('kai', 'pw{Enter}');

  expect(await screen.findByRole('alert')).toHaveTextContent(/^cannot reach the server$/);
});

test('the alert clears on the next submit', async () => {
  vi.mocked(signIn).mockRejectedValueOnce(new ApiError(401, 'invalid_credentials', 'wrong username or password'));
  vi.mocked(signIn).mockReturnValueOnce(new Promise(() => {}));
  render(Login);
  await fill('kai', 'nope{Enter}');
  await screen.findByRole('alert');

  await userEvent.type(screen.getByLabelText('Password'), 'pw{Enter}');

  expect(screen.queryByRole('alert')).toBeNull();
});

test('does not submit twice', async () => {
  let reject!: (err: ApiError) => void;
  vi.mocked(signIn).mockReturnValue(new Promise((_, r) => (reject = r)));
  render(Login);
  await fill('kai', 'pw');
  const button = screen.getByRole('button', { name: 'Log in' });

  await userEvent.click(button);
  await userEvent.click(button);
  await userEvent.type(screen.getByLabelText('Password'), '{Enter}');

  expect(signIn).toHaveBeenCalledTimes(1);
  expect(button).toBeDisabled();

  reject(new ApiError(0, 'network', 'cannot reach the server'));
  await screen.findByRole('alert');
  expect(button).toBeEnabled();
});

test('fields suit password managers and phones', () => {
  render(Login);

  const username = screen.getByLabelText('Username');
  expect(username).toHaveAttribute('type', 'text');
  expect(username).toHaveAttribute('name', 'username');
  expect(username).toHaveAttribute('autocomplete', 'username');
  expect(username).toHaveAttribute('autocapitalize', 'none');
  expect(username).toHaveAttribute('autocorrect', 'off');
  expect(username).toHaveAttribute('spellcheck', 'false');
  expect(username).toBeRequired();

  const password = screen.getByLabelText('Password');
  expect(password).toHaveAttribute('type', 'password');
  expect(password).toHaveAttribute('name', 'password');
  expect(password).toHaveAttribute('autocomplete', 'current-password');
  expect(password).toBeRequired();
});

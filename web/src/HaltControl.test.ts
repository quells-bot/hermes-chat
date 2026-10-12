import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { resumeAll, stopAll, store } from './lib/store.svelte';
import HaltControl from './HaltControl.svelte';

vi.mock('./lib/store.svelte', () => ({
  store: { halted: false },
  stopAll: vi.fn(),
  resumeAll: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(stopAll).mockResolvedValue();
  vi.mocked(resumeAll).mockResolvedValue();
  store.halted = false;
});

afterEach(() => {
  vi.restoreAllMocks();
});

test('a second tap while stopping sends nothing', async () => {
  let settle!: () => void;
  vi.mocked(stopAll).mockReturnValue(new Promise((r) => (settle = r)));
  render(HaltControl);
  const button = screen.getByRole('button', { name: 'Stop all' });
  await userEvent.click(button);
  expect(button).toBeDisabled();
  await userEvent.click(button);
  expect(stopAll).toHaveBeenCalledTimes(1);
  settle();
  await vi.waitFor(() => expect(button).toBeEnabled());
});

test('while stopped it says Stopped and offers Resume all, which calls resumeAll', async () => {
  store.halted = true;
  render(HaltControl);
  expect(screen.getByText('Stopped')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Stop all' })).toBeNull();

  await userEvent.click(screen.getByRole('button', { name: 'Resume all' }));

  expect(resumeAll).toHaveBeenCalledTimes(1);
  expect(stopAll).not.toHaveBeenCalled();
});

test('no dialog is opened', async () => {
  const confirm = vi.spyOn(window, 'confirm');
  render(HaltControl);
  await userEvent.click(screen.getByRole('button', { name: 'Stop all' }));
  expect(stopAll).toHaveBeenCalledTimes(1);
  expect(confirm).not.toHaveBeenCalled();
});

// The activity block on the real store. The network and the stream are mocked.
import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, test, vi } from 'vitest';
import ActivityBlock from './ActivityBlock.svelte';
import * as api from './lib/api';
import { activity, agent, message } from './lib/fixtures';
import { disconnect, load, store } from './lib/store.svelte';
import type { Activity, DeliveryState } from './lib/types';

vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  listRooms: vi.fn(),
  listAgents: vi.fn(),
  getHalt: vi.fn(),
  listActivity: vi.fn(),
}));
vi.mock('./lib/stream', () => ({ openStream: vi.fn() }));

const forge = agent({ id: 'a1', handle: 'forge', display_name: 'Forge' });
const scout = agent({ id: 'a2', handle: 'scout', display_name: 'Scout' });

// tool makes one of Forge's tool steps on message m1; finished unless told.
function tool(id: string, overrides: Partial<Activity> = {}): Activity {
  return activity({ id, message_id: 'm1', agent: forge, tool: 'web_search', preview: id, ...overrides });
}

const unfinished = { duration_ms: null, succeeded: null };

function remark(id: string, text: string): Activity {
  return activity({ id, message_id: 'm1', agent: forge, kind: 'remark', text, tool: '', ...unfinished });
}

// block draws Forge's turn on message m1 of room r1, in this state.
function block(state: DeliveryState, who = forge) {
  return render(ActivityBlock, {
    message: message({ id: 'm1', room_id: 'r1' }),
    delivery: { agent: who, state },
  });
}

// rows are the lines of an open block, as their text.
function rows(): string[] {
  return screen.getAllByRole('listitem').map((li) => li.textContent?.replace(/\s+/g, ' ').trim() ?? '');
}

beforeEach(() => {
  disconnect();
  vi.mocked(api.listRooms).mockReset().mockResolvedValue([]);
  vi.mocked(api.listAgents).mockReset().mockResolvedValue([]);
  vi.mocked(api.getHalt).mockReset().mockResolvedValue(false);
  vi.mocked(api.listActivity).mockReset().mockResolvedValue([]);
});

test('closed, a working turn says how many steps and what it is doing now', () => {
  store.activity.m1 = [
    tool('s1', { duration_ms: 2100 }),
    tool('s2', { tool: 'web_extract', preview: 'xcelenergy.com/rates', ...unfinished }),
  ];
  const { container } = block('working');
  const summary = screen.getByRole('button', { name: 'Forge · working · 2 steps' });
  expect(summary).toHaveAttribute('aria-expanded', 'false');
  expect(summary.querySelector('.dot')).not.toBeNull();
  expect(screen.getByText('Now: web_extract · xcelenergy.com/rates')).toBeInTheDocument();
  expect(summary).not.toContainElement(screen.getByText('Now: web_extract · xcelenergy.com/rates'));
  expect(container.querySelector('li')).toBeNull();
  expect(api.listActivity).not.toHaveBeenCalled();
});

test('a tool with no preview is named alone, and a turn with no steps yet says nothing about now', () => {
  store.activity.m1 = [];
  block('working');
  expect(screen.getByRole('button', { name: 'Forge · working · 0 steps' })).toBeInTheDocument();
  expect(screen.queryByText(/^Now:/)).toBeNull();

  store.activity.m1.push(tool('s1', { tool: 'terminal', preview: '', ...unfinished }));
  return waitFor(() => expect(screen.getByText('Now: terminal')).toBeInTheDocument());
});

test('a remark as the newest activity is what it is doing now', () => {
  store.activity.m1 = [tool('s1'), remark('s2', 'Checking two more **sources**.')];
  block('working');
  expect(screen.getByText('Now: Checking two more **sources**.')).toBeInTheDocument();
});

test('a finished turn shows its state and no count until opened; opening fetches and shows the count', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([tool('s1'), tool('s2'), tool('s3')]);
  block('done');
  const summary = screen.getByRole('button', { name: 'Forge · done' });
  expect(summary.querySelector('.dot')).toBeNull();
  expect(api.listActivity).not.toHaveBeenCalled();

  await userEvent.click(summary);
  expect(api.listActivity).toHaveBeenCalledTimes(1);
  expect(api.listActivity).toHaveBeenCalledWith('r1', 'm1');
  expect(await screen.findByRole('button', { name: 'Forge · done · 3 steps' })).toHaveAttribute('aria-expanded', 'true');
  expect(rows()).toHaveLength(3);
  // What a finished turn did last is not what it is doing now.
  expect(screen.queryByText(/^Now:/)).toBeNull();

  await userEvent.click(summary);
  expect(summary).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryAllByRole('listitem')).toEqual([]);
  expect(api.listActivity).toHaveBeenCalledTimes(1);
});

test('one step is 1 step, and remarks are not counted', () => {
  store.activity.m1 = [remark('s1', 'thinking'), tool('s2'), remark('s3', 'done thinking')];
  block('failed');
  expect(screen.getByRole('button', { name: 'Forge · failed · 1 step' })).toBeInTheDocument();
});

test('open, each tool shows its mark, preview and time', async () => {
  store.activity.m1 = [
    tool('s1', { preview: 'heat pump install cost', duration_ms: 2100 }),
    tool('s2', { tool: 'web_extract', preview: 'hvacquotes.example', duration_ms: 30_000, succeeded: false }),
    tool('s3', { tool: 'web_extract', preview: 'xcelenergy.com/rates', ...unfinished }),
  ];
  block('working');
  await userEvent.click(screen.getByRole('button', { name: /^Forge/ }));
  expect(rows()).toEqual([
    '✓ web_search · heat pump install cost 2.1s',
    '✕ web_extract · hvacquotes.example 30.0s',
    '● web_extract · xcelenergy.com/rates running',
  ]);
  const items = screen.getAllByRole('listitem');
  expect(within(items[0]).getByLabelText('succeeded')).toHaveTextContent('✓');
  expect(within(items[1]).getByLabelText('failed')).toHaveTextContent('✕');
  expect(within(items[2]).getByLabelText('running')).toHaveTextContent('●');
});

test('an unfinished tool on a finished turn is unfinished, not running', async () => {
  store.activity.m1 = [tool('s1', { preview: 'rates', ...unfinished })];
  block('failed');
  await userEvent.click(screen.getByRole('button', { name: /^Forge/ }));
  expect(rows()).toEqual(['– web_search · rates']);
  expect(screen.getByLabelText('unfinished')).toHaveTextContent('–');
  expect(screen.queryByText('running')).toBeNull();
});

test('tapping a tool shows its arguments, and a tool with none is not a button', async () => {
  const args = '{"query": "<b>cold</b> climate"}';
  store.activity.m1 = [tool('s1', { preview: 'with', arguments: args }), tool('s2', { preview: 'without' })];
  const { container } = block('done');
  await userEvent.click(screen.getByRole('button', { name: /^Forge/ }));
  expect(screen.getAllByRole('button')).toHaveLength(2);
  expect(screen.getByText('web_search · without').closest('button')).toBeNull();

  const step = screen.getByRole('button', { name: /with/ });
  expect(step).toHaveAttribute('aria-expanded', 'false');
  expect(container.querySelector('pre')).toBeNull();
  await userEvent.click(step);
  expect(step).toHaveAttribute('aria-expanded', 'true');
  expect(container.querySelector('pre')?.textContent).toBe(args);
  expect(container.querySelector('pre b')).toBeNull();
  await userEvent.click(step);
  expect(container.querySelector('pre')).toBeNull();
});

test('a remark is italic text and is not parsed as markdown', async () => {
  store.activity.m1 = [remark('s1', 'Costs vary **x** <b>more</b> than expected.')];
  const { container } = block('done');
  await userEvent.click(screen.getByRole('button', { name: /^Forge/ }));
  expect(screen.getByText('Costs vary **x** <b>more</b> than expected.').tagName).toBe('I');
  expect(container.querySelector('strong, b')).toBeNull();
});

test('two agents on one message get a block each, with their own steps', async () => {
  store.activity.m1 = [
    tool('s1', { preview: 'mine' }),
    tool('s2', { agent: scout, preview: 'theirs', ...unfinished }),
    tool('s3', { preview: 'mine too' }),
  ];
  const first = block('done');
  const second = block('working', scout);
  const mine = within(first.container);
  const theirs = within(second.container);
  expect(mine.getByRole('button', { name: 'Forge · done · 2 steps' })).toBeInTheDocument();
  expect(theirs.getByRole('button', { name: 'Scout · working · 1 step' })).toBeInTheDocument();
  expect(theirs.getByText('Now: web_search · theirs')).toBeInTheDocument();

  await userEvent.click(mine.getByRole('button'));
  expect(mine.getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    expect.stringContaining('mine'),
    expect.stringContaining('mine too'),
  ]);
  expect(mine.queryByText(/theirs/)).toBeNull();
});

test('an open block whose list was emptied by a reload asks for it again', async () => {
  vi.mocked(api.listActivity).mockResolvedValue([tool('s1')]);
  block('done');
  await userEvent.click(screen.getByRole('button', { name: 'Forge · done' }));
  expect(await screen.findByRole('button', { name: 'Forge · done · 1 step' })).toBeInTheDocument();
  expect(api.listActivity).toHaveBeenCalledTimes(1);

  vi.mocked(api.listActivity).mockResolvedValue([tool('s1'), tool('s2')]);
  await load();
  expect(await screen.findByRole('button', { name: 'Forge · done · 2 steps' })).toBeInTheDocument();
  expect(api.listActivity).toHaveBeenCalledTimes(2);
  expect(rows()).toHaveLength(2);
});

test('a closed block whose list was emptied asks for nothing', async () => {
  store.activity.m1 = [tool('s1')];
  block('done');
  await load();
  expect(await screen.findByRole('button', { name: 'Forge · done' })).toBeInTheDocument();
  expect(api.listActivity).not.toHaveBeenCalled();
});

test('a list that cannot be fetched is asked for once', async () => {
  const failing = Promise.reject(new api.ApiError(0, 'network', 'cannot reach the server'));
  failing.catch(() => {});
  vi.mocked(api.listActivity).mockReturnValue(failing);
  block('done');
  const summary = screen.getByRole('button', { name: 'Forge · done' });
  await userEvent.click(summary);
  await waitFor(() => expect(summary).toHaveAttribute('aria-expanded', 'true'));
  await new Promise((r) => setTimeout(r, 20));
  expect(api.listActivity).toHaveBeenCalledTimes(1);
  expect(screen.getByRole('button', { name: 'Forge · done' })).toBeInTheDocument();
});

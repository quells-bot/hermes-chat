import { afterEach, expect, test, vi } from 'vitest';
import { fileName, saveText } from './download';

const at = new Date(2026, 9, 13, 14, 2); // local, so the test does not depend on the zone

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test.each([
  ['Heat pump vs. gas furnace', 'Heat-pump-vs-gas-furnace-20261013-1402.md'],
  ['../../etc/passwd', 'etc-passwd-20261013-1402.md'],
  ['a/b\\c:d', 'a-b-c-d-20261013-1402.md'],
  ['Küche 🍳 Umbau', 'Küche-Umbau-20261013-1402.md'],
  ['...', 'message-20261013-1402.md'],
  ['', 'message-20261013-1402.md'],
])('fileName(%j)', (room, want) => {
  expect(fileName(room, at)).toBe(want);
});

test('a long name is cut to 60 characters and does not end in a dash', () => {
  const name = fileName(`${'a'.repeat(59)} ${'b'.repeat(240)}`, at);
  expect(name).toBe(`${'a'.repeat(59)}-20261013-1402.md`);
});

function stubUrl() {
  const create = vi.fn((_: Blob) => 'blob:x');
  const revoke = vi.fn();
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke }));
  return { create, revoke };
}

test('saveText saves the text with a final newline', async () => {
  const { create } = stubUrl();
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    expect(this.download).toBe('n.md');
    expect(this.getAttribute('href')).toBe('blob:x');
  });
  saveText('n.md', '# Report\r\nbody 👍');
  const blob = create.mock.calls[0][0];
  expect(blob.type).toBe('text/markdown;charset=utf-8');
  expect(await blob.text()).toBe('# Report\r\nbody 👍\n');
  expect(click).toHaveBeenCalledTimes(1);
});

test('the URL is revoked 10 seconds after the click, not with it', () => {
  vi.useFakeTimers();
  const { revoke } = stubUrl();
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  saveText('n.md', 'x');
  expect(revoke).not.toHaveBeenCalled();
  vi.advanceTimersByTime(9_999);
  expect(revoke).not.toHaveBeenCalled();
  vi.advanceTimersByTime(1);
  expect(revoke).toHaveBeenCalledWith('blob:x');
});

test('text that ends in a newline gets no second one', async () => {
  const { create } = stubUrl();
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  saveText('n.md', 'one\n');
  expect(await create.mock.calls[0][0].text()).toBe('one\n');
});

test('the URL is revoked when the click throws', () => {
  const { revoke } = stubUrl();
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {
    throw new Error('blocked');
  });
  expect(() => saveText('n.md', 'x')).toThrow('blocked');
  expect(revoke).toHaveBeenCalledWith('blob:x');
});

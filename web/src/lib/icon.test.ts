import { expect, test } from 'vitest';
import { iconFor } from './icon';

test('forge', () => {
  const icon = iconFor('forge');
  expect(icon.color).toBe('#c93560');
  expect(icon.cells).toEqual([[1, 0], [3, 0], [2, 0], [0, 2], [4, 2], [2, 2], [0, 4], [4, 4], [1, 4], [3, 4]]);
});

test('case does not change the icon', () => {
  expect(iconFor('Forge')).toEqual(iconFor('forge'));
});

test('kai, a non-ASCII name and an empty name each give an icon', () => {
  expect(iconFor('kai').color).toBe('#c2338b');
  expect(iconFor('é').color).toBe('#3d3d46');
  expect(iconFor('').color).toBe('#c93560');
});

test('every icon is mirrored left to right', () => {
  for (const name of ['atlas', 'scout', 'quill', 'ledger']) {
    const cells = iconFor(name).cells.map(([x, y]) => `${x},${y}`);
    for (const [x, y] of iconFor(name).cells) expect(cells).toContain(`${4 - x},${y}`);
  }
});

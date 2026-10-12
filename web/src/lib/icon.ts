// A small mirrored pattern and a colour, both derived from a name.
export const palette: readonly string[] = [
  '#c0392b', '#d35400', '#b9770e', '#7d8c0a', '#2e8b3d', '#0e8f6e', '#0b8a9a', '#1f78c1',
  '#3555c9', '#6a45c9', '#9b3fb5', '#c2338b', '#c93560', '#8a5a3c', '#5c6b7a', '#3d3d46',
];

export type IconSpec = { color: string; cells: [x: number, y: number][] };

// FNV-1a over code points, finished with a murmur3 finalizer.
function hash(s: string): number {
  let x = 2166136261;
  for (const c of s) {
    x ^= c.codePointAt(0)!;
    x = Math.imul(x, 16777619);
  }
  x ^= x >>> 16;
  x = Math.imul(x, 0x85ebca6b);
  x ^= x >>> 13;
  x = Math.imul(x, 0xc2b2ae35);
  x ^= x >>> 16;
  return x >>> 0;
}

export function iconFor(name: string): IconSpec {
  const h = hash(name.toLowerCase());
  const cells: [number, number][] = [];
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      if ((h >>> (y * 3 + x)) & 1) {
        cells.push([x, y]);
        if (x < 2) cells.push([4 - x, y]);
      }
    }
  }
  return { color: palette[(h >>> 15) % 16], cells };
}

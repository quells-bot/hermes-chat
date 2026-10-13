import { expect, test } from 'vitest';
import { MAX_INLINE_RUN, parse, safeHref, type Block } from './markdown';

const text = (s: string) => ({ type: 'text', text: s });

test('raw HTML is text', () => {
  expect(parse('<script>alert(1)</script>')).toEqual([
    { type: 'paragraph', children: [text('<script>alert(1)</script>')] },
  ]);
});

test('inline HTML inside a paragraph is text', () => {
  const blocks = parse('a <img src=x onerror=alert(1)> b');
  expect(blocks).toHaveLength(1);
  const kids = (blocks[0] as { children: { type: string; text?: string }[] }).children;
  expect(kids.every((k) => k.type === 'text')).toBe(true);
  expect(kids.map((k) => k.text).join('')).toBe('a <img src=x onerror=alert(1)> b');
});

test('a javascript link keeps its words and loses its target', () => {
  expect(parse('[click](javascript:alert(1))')).toEqual([{ type: 'paragraph', children: [text('click')] }]);
});

test('an image is a link, never an image', () => {
  expect(parse('![chart](https://example.com/c.png)')).toEqual([
    { type: 'paragraph', children: [{ type: 'link', href: 'https://example.com/c.png', children: [text('chart')] }] },
  ]);
});

test('an image with no alt shows its href; an unsafe one becomes text', () => {
  expect(parse('![](https://example.com/c.png)')).toEqual([
    {
      type: 'paragraph',
      children: [{ type: 'link', href: 'https://example.com/c.png', children: [text('https://example.com/c.png')] }],
    },
  ]);
  expect(parse('![x](javascript:alert(1))')).toEqual([{ type: 'paragraph', children: [text('x')] }]);
});

test.each([
  ['https://example.com/a?b=1', 'https://example.com/a?b=1'],
  ['mailto:kai@example.com', 'mailto:kai@example.com'],
  ['  http://example.com  ', 'http://example.com'],
  ['javascript:alert(1)', null], ['JaVaScRiPt:alert(1)', null], ['data:text/html,<b>', null],
  ['vbscript:x', null], ['/api/logout', null], ['#top', null], ['', null],
])('safeHref(%j) is %j', (href, want) => expect(safeHref(href)).toBe(want));

test('a fenced block keeps its text exactly', () => {
  const body = 'line one\n\n    indented\n  two';
  expect(parse('```\n' + body + '\n```')).toEqual([{ type: 'code', text: body }]);
});

test('a single newline is a break', () => {
  expect(parse('one\ntwo')).toEqual([
    { type: 'paragraph', children: [text('one'), { type: 'br' }, text('two')] },
  ]);
});

test('strong, em, del and code nest', () => {
  expect(parse('**a *b* ~~c~~ `d`**')).toEqual([
    {
      type: 'paragraph',
      children: [
        {
          type: 'strong',
          children: [
            text('a '),
            { type: 'em', children: [text('b')] },
            text(' '),
            { type: 'del', children: [text('c')] },
            text(' '),
            { type: 'code', text: 'd' },
          ],
        },
      ],
    },
  ]);
});

test('an ordered list keeps its start', () => {
  expect(parse('3. a\n4. b')).toEqual([
    {
      type: 'list',
      ordered: true,
      start: 3,
      items: [[{ type: 'paragraph', children: [text('a')] }], [{ type: 'paragraph', children: [text('b')] }]],
    },
  ]);
});

test('a table has a header row and body rows', () => {
  expect(parse('| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |')).toEqual([
    {
      type: 'table',
      header: [[text('a')], [text('b')]],
      rows: [
        [[text('1')], [text('2')]],
        [[text('3')], [text('4')]],
      ],
    },
  ]);
});

test('a task item starts with its box', () => {
  const [list] = parse('- [ ] todo\n- [x] done') as Extract<Block, { type: 'list' }>[];
  const first = (i: number) => JSON.stringify(list.items[i]);
  expect(first(0)).toContain('☐ ');
  expect(first(1)).toContain('☑ ');
  const p = list.items[0][0] as Extract<Block, { type: 'paragraph' }>;
  expect(p.children[0]).toEqual(text('☐ '));
});

test('a task item in a loose list has one box and no bracket text', () => {
  const [list] = parse('- [ ] todo\n\n- [x] done\n\n  more') as Extract<Block, { type: 'list' }>[];
  expect(list.items[0]).toEqual([{ type: 'paragraph', children: [text('☐ '), text('todo')] }]);
  const p = list.items[1][0] as Extract<Block, { type: 'paragraph' }>;
  expect(p.children).toEqual([text('☑ '), text('done')]);
  expect(JSON.stringify(list)).not.toContain('[ ]');
  expect(JSON.stringify(list)).not.toContain('[x]');
});

function depth(v: unknown): number {
  if (Array.isArray(v)) return Math.max(0, ...v.map(depth));
  if (v && typeof v === 'object') {
    const o = v as { type?: string; children?: unknown; items?: unknown };
    const inner = Math.max(depth(o.children ?? []), depth(o.items ?? []));
    return (o.type === 'blockquote' ? 1 : 0) + inner;
  }
  return 0;
}

test('a thousand nested quotes neither throws nor nests past 20', () => {
  const blocks = parse('> '.repeat(1000) + 'deep');
  expect(blocks.length).toBeGreaterThan(0);
  expect(depth(blocks)).toBeLessThanOrEqual(20);
  expect(JSON.stringify(blocks)).toContain('deep');
});

test('a 262,144-character text parses', () => {
  const big = ('word '.repeat(60) + '\n\n').repeat(Math.ceil(262144 / 302)).slice(0, 262144);
  expect(parse(big).length).toBeGreaterThan(0);
  expect(parse('x'.repeat(262144))).toHaveLength(1);
});

test.each([
  ['*a ', '*a '.repeat(87382)],
  ['[a](', '[a]('.repeat(65536)],
  ['unbalanced stars', '*'.repeat(131072) + 'a' + '*'.repeat(131071)],
])('hostile inline pattern %s at 262,144 characters parses quickly', (_name, src) => {
  const start = performance.now();
  const blocks = parse(src.slice(0, 262144));
  expect(performance.now() - start).toBeLessThan(1500);
  expect(blocks.length).toBeGreaterThan(0);
});

test('a paragraph over the inline cap is one plain text node', () => {
  const src = 'x'.repeat(2500) + ' **bold** ' + 'y'.repeat(2500);
  expect(src.length).toBeGreaterThan(MAX_INLINE_RUN);
  expect(parse(src)).toEqual([{ type: 'paragraph', children: [text(src)] }]);
});

test('a paragraph just under the cap is still formatted', () => {
  const src = '**bold** ' + 'y'.repeat(MAX_INLINE_RUN - 20);
  const [p] = parse(src) as Extract<Block, { type: 'paragraph' }>[];
  expect(p.children[0]).toEqual({ type: 'strong', children: [text('bold')] });
});

test('blank input is no blocks', () => {
  expect(parse('')).toEqual([]);
  expect(parse('  \n\n ')).toEqual([]);
});

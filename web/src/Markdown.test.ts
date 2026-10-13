import { render, screen } from '@testing-library/svelte';
import { expect, test } from 'vitest';
import Markdown from './Markdown.svelte';

test('a link opens in a new tab with noopener noreferrer', () => {
  render(Markdown, { text: '[site](https://example.com/x)' });
  const a = screen.getByRole('link');
  expect(a).toHaveAttribute('href', 'https://example.com/x');
  expect(a).toHaveAttribute('target', '_blank');
  expect(a).toHaveAttribute('rel', 'noopener noreferrer');
});

test('hostile input renders no element outside the allowlist', () => {
  const inputs = [
    '<script>alert(1)</script>',
    'a <img src=x onerror=alert(1)> b',
    '![x](https://example.com/p.png)',
    '[x](javascript:alert(1))',
    '<iframe src="https://evil.example"></iframe>',
    '<object data=x></object><embed src=x><style>*{}</style>',
    '<a href="x" onclick="alert(1)">x</a>',
    '> <script>alert(2)</script>',
    '- <img src=y onerror=alert(3)>',
    '| a |\n|---|\n| <script>alert(4)</script> |',
  ];
  const { container } = render(Markdown, { text: inputs.join('\n\n') });
  expect(container.querySelector('script, img, iframe, object, embed, style, [onerror], [onclick]')).toBeNull();
  expect(container.textContent).toContain('<script>');
});

test('the review inputs render no element but a safe link', () => {
  const inputs = [
    '<script>alert(1)</script>',
    '<img src=x onerror=alert(1)>',
    '[x](javascript:alert(1))',
    '![](https://tracker/p.gif)',
    '[d](data:text/html,<script>alert(1)</script>)',
    '> '.repeat(1000) + 'deep',
  ];
  const { container } = render(Markdown, { text: inputs.join('\n\n') });
  expect(container.querySelector('script, img, iframe, object, embed, style, [onerror], [onclick]')).toBeNull();
  expect(container.textContent).toContain('<script>');
  const links = container.querySelectorAll('a');
  expect(links).toHaveLength(1);
  expect(links[0]).toHaveAttribute('href', 'https://tracker/p.gif');
});

test('a code block is a pre', () => {
  const src = 'one\n  two\n\nthree';
  const { container } = render(Markdown, { text: '```\n' + src + '\n```' });
  const pre = container.querySelector('pre');
  expect(pre).not.toBeNull();
  expect(pre?.textContent).toBe(src);
});

test('lists, tables and headings render as plain elements', () => {
  const { container } = render(Markdown, {
    text: '# T\n\n3. a\n4. b\n\n| h |\n|---|\n| c |\n\n---',
  });
  expect(container.querySelector('h1')).not.toBeNull();
  expect(container.querySelector('ol')).toHaveAttribute('start', '3');
  expect(container.querySelector('.table-wrap table th')).not.toBeNull();
  expect(container.querySelector('hr')).not.toBeNull();
});

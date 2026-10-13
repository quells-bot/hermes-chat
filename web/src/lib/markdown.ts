// Markdown as a small allowlisted tree. Message text comes from agents that
// read the web, so it is untrusted: the lexer's tokens are copied into the
// types below and anything this file does not name becomes text. No HTML
// string is ever built; Markdown.svelte renders the tree as elements.
import { Lexer, type Token, type Tokens } from 'marked';

export type Inline =
  | { type: 'text'; text: string }
  | { type: 'code'; text: string }
  | { type: 'strong' | 'em' | 'del'; children: Inline[] }
  | { type: 'link'; href: string; children: Inline[] }
  | { type: 'br' };
export type Block =
  | { type: 'paragraph'; children: Inline[] }
  | { type: 'heading'; depth: number; children: Inline[] }
  | { type: 'code'; text: string }
  | { type: 'blockquote'; children: Block[] }
  | { type: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { type: 'table'; header: Inline[][]; rows: Inline[][][] }
  | { type: 'hr' };

const MAX_DEPTH = 20;

// The href, or null unless it is http:, https: or mailto:. A string URL
// cannot parse on its own (a relative path, #anchor) is null.
export function safeHref(href: string): string | null {
  const h = href.trim();
  try {
    const p = new URL(h).protocol;
    return p === 'http:' || p === 'https:' || p === 'mailto:' ? h : null;
  } catch {
    return null;
  }
}

const text = (s: string): Inline => ({ type: 'text', text: s });

function inlines(tokens: Token[] | undefined, depth: number): Inline[] {
  const out: Inline[] = [];
  for (const t of tokens ?? []) out.push(...inline(t, depth));
  return out;
}

function inline(t: Token, depth: number): Inline[] {
  if (depth >= MAX_DEPTH) return [text(t.raw)];
  switch (t.type) {
    case 'text': {
      const x = t as Tokens.Text;
      return x.tokens ? inlines(x.tokens, depth + 1) : [text(x.text)];
    }
    case 'escape':
      return [text((t as Tokens.Escape).text)];
    case 'codespan':
      return [{ type: 'code', text: (t as Tokens.Codespan).text }];
    case 'br':
      return [{ type: 'br' }];
    case 'strong':
    case 'em':
    case 'del':
      return [{ type: t.type, children: inlines((t as Tokens.Strong).tokens, depth + 1) }];
    case 'link': {
      const l = t as Tokens.Link;
      const children = inlines(l.tokens, depth + 1);
      const href = safeHref(l.href);
      return href === null ? children : [{ type: 'link', href, children }];
    }
    case 'image': {
      const i = t as Tokens.Image;
      const href = safeHref(i.href);
      if (href === null) return [text(i.text)];
      return [{ type: 'link', href, children: [text(i.text || href)] }];
    }
    default:
      // html, and any token type not named above
      return [text(t.raw)];
  }
}

function blocks(tokens: Token[] | undefined, depth: number): Block[] {
  const out: Block[] = [];
  for (const t of tokens ?? []) {
    const b = block(t, depth);
    if (b) out.push(b);
  }
  return out;
}

function block(t: Token, depth: number): Block | null {
  if (t.type === 'space') return null;
  if (depth >= MAX_DEPTH) return { type: 'paragraph', children: [text(t.raw.trimEnd())] };
  switch (t.type) {
    case 'paragraph':
    case 'text': {
      const x = t as Tokens.Paragraph;
      return { type: 'paragraph', children: x.tokens ? inlines(x.tokens, depth + 1) : [text(x.text)] };
    }
    case 'heading': {
      const h = t as Tokens.Heading;
      return { type: 'heading', depth: Math.min(6, Math.max(1, h.depth)), children: inlines(h.tokens, depth + 1) };
    }
    case 'code':
      return { type: 'code', text: (t as Tokens.Code).text };
    case 'blockquote':
      return { type: 'blockquote', children: blocks((t as Tokens.Blockquote).tokens, depth + 1) };
    case 'list': {
      const l = t as Tokens.List;
      return {
        type: 'list',
        ordered: l.ordered,
        start: typeof l.start === 'number' ? l.start : 1,
        items: l.items.map((item) => {
          const kids = blocks(item.tokens.filter((k) => k.type !== 'checkbox'), depth + 1);
          if (item.task) {
            const mark = text(item.checked ? '☑ ' : '☐ ');
            const first = kids[0];
            if (first && first.type === 'paragraph') first.children.unshift(mark);
            else kids.unshift({ type: 'paragraph', children: [mark] });
          }
          return kids;
        }),
      };
    }
    case 'table': {
      const tb = t as Tokens.Table;
      return {
        type: 'table',
        header: tb.header.map((c) => inlines(c.tokens, depth + 1)),
        rows: tb.rows.map((r) => r.map((c) => inlines(c.tokens, depth + 1))),
      };
    }
    case 'hr':
      return { type: 'hr' };
    default:
      // html, and any token type not named above
      return { type: 'paragraph', children: [text(t.raw.trimEnd())] };
  }
}

export function parse(src: string): Block[] {
  try {
    return blocks(Lexer.lex(src, { gfm: true, breaks: true }), 0);
  } catch {
    return [{ type: 'paragraph', children: [text(src)] }];
  }
}

<script lang="ts">
  import { parse, type Block, type Inline } from './lib/markdown';

  let { text }: { text: string } = $props();

  const blocks = $derived(parse(text));
</script>

{#snippet inl(nodes: Inline[])}
  {#each nodes as n, i (i)}
    {#if n.type === 'text'}{n.text}
    {:else if n.type === 'code'}<code>{n.text}</code>
    {:else if n.type === 'br'}<br />
    {:else if n.type === 'strong'}<strong>{@render inl(n.children)}</strong>
    {:else if n.type === 'em'}<em>{@render inl(n.children)}</em>
    {:else if n.type === 'del'}<del>{@render inl(n.children)}</del>
    {:else if n.type === 'link'}<a href={n.href} target="_blank" rel="noopener noreferrer">{@render inl(n.children)}</a>
    {/if}
  {/each}
{/snippet}

{#snippet blk(nodes: Block[])}
  {#each nodes as b, i (i)}
    {#if b.type === 'paragraph'}<p>{@render inl(b.children)}</p>
    {:else if b.type === 'heading'}
      {#if b.depth === 1}<h1>{@render inl(b.children)}</h1>
      {:else if b.depth === 2}<h2>{@render inl(b.children)}</h2>
      {:else if b.depth === 3}<h3>{@render inl(b.children)}</h3>
      {:else if b.depth === 4}<h4>{@render inl(b.children)}</h4>
      {:else if b.depth === 5}<h5>{@render inl(b.children)}</h5>
      {:else}<h6>{@render inl(b.children)}</h6>
      {/if}
    {:else if b.type === 'code'}<pre><code>{b.text}</code></pre>
    {:else if b.type === 'blockquote'}<blockquote>{@render blk(b.children)}</blockquote>
    {:else if b.type === 'list'}
      {#if b.ordered}
        <ol start={b.start}>
          {#each b.items as item, j (j)}<li>{@render blk(item)}</li>{/each}
        </ol>
      {:else}
        <ul>
          {#each b.items as item, j (j)}<li>{@render blk(item)}</li>{/each}
        </ul>
      {/if}
    {:else if b.type === 'table'}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>{#each b.header as cell, j (j)}<th>{@render inl(cell)}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each b.rows as row, r (r)}
              <tr>{#each row as cell, j (j)}<td>{@render inl(cell)}</td>{/each}</tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if b.type === 'hr'}<hr />
    {/if}
  {/each}
{/snippet}

<div class="md">{@render blk(blocks)}</div>

<style>
  .md {
    overflow-wrap: anywhere;
  }
  .md :global(p),
  .md :global(ul),
  .md :global(ol),
  .md :global(blockquote),
  .md :global(pre),
  .md :global(.table-wrap) {
    margin: 0 0 0.5em;
  }
  .md :global(> :last-child) {
    margin-bottom: 0;
  }
  .md :global(h1),
  .md :global(h2),
  .md :global(h3),
  .md :global(h4),
  .md :global(h5),
  .md :global(h6) {
    margin: 0.6em 0 0.3em;
    font-size: 1rem;
    line-height: 1.3;
  }
  .md :global(h1) {
    font-size: 1.25rem;
  }
  .md :global(h2) {
    font-size: 1.125rem;
  }
  .md :global(ul),
  .md :global(ol) {
    padding-left: 1.4em;
  }
  .md :global(blockquote) {
    padding-left: 0.75em;
    border-left: 3px solid var(--line);
    color: var(--ink2);
  }
  .md :global(code) {
    font-family: ui-monospace, monospace;
    font-size: 0.9em;
    background: var(--paper2);
    padding: 0.1em 0.3em;
  }
  .md :global(pre) {
    font-family: ui-monospace, monospace;
    overflow-x: auto;
    white-space: pre;
    background: var(--paper2);
    border: 1px solid var(--line);
    padding: 0.5em 0.75em;
  }
  .md :global(pre code) {
    background: none;
    padding: 0;
  }
  .md :global(.table-wrap) {
    overflow-x: auto;
  }
  .md :global(table) {
    border-collapse: collapse;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--line);
    padding: 0.25em 0.5em;
    text-align: left;
    /* A word in a table cell is not split; the wide table scrolls instead. */
    overflow-wrap: normal;
  }
  .md :global(th) {
    background: var(--paper2);
  }
  .md :global(a) {
    color: var(--accent);
  }
  .md :global(hr) {
    border: 0;
    border-top: 1px solid var(--line);
  }
</style>

<script lang="ts">
  import type { Snippet } from 'svelte';

  // The one fixed strip on a screen. Children sit at its right edge.
  let {
    title = 'Hermes Chat',
    back,
    children,
  }: { title?: string; back?: string; children?: Snippet } = $props();
</script>

<header>
  {#if back}
    <a class="back" href={back} aria-label="Back">‹</a>
  {/if}
  <h1>{title}</h1>
  <div class="end">{@render children?.()}</div>
</header>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    min-height: 2.75rem;
    padding: 0.25rem 0.75rem;
    background: var(--aub);
    color: var(--on-aub);
  }

  h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 1rem;
    font-weight: 600;
  }

  .back {
    flex: none;
    color: var(--on-aub);
    font-size: 1.25rem;
    line-height: 1;
    text-decoration: none;
  }

  .end {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex: none;
  }

  header :global(button) {
    padding: 0.25rem 0.6rem;
    border: 1px solid var(--aub-line);
    border-radius: 0.25rem;
    background: var(--aub-hi);
    color: var(--on-aub);
    font-size: 0.875rem;
  }
</style>

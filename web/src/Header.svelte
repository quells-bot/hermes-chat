<script lang="ts">
  import type { Snippet } from 'svelte';

  // The one fixed strip on a screen. Children sit at its right edge.
  let { children }: { children?: Snippet } = $props();
</script>

<header>
  <h1>Hermes Chat</h1>
  {@render children?.()}
</header>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    min-height: 2.75rem;
    padding: 0.25rem 0.75rem;
    background: var(--aub);
    color: var(--on-aub);
  }

  h1 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
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

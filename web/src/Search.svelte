<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from './lib/route.svelte';
  import { closeSearch, matchRooms, search } from './lib/search.svelte';
  import { store } from './lib/store.svelte';

  let field: HTMLInputElement;

  onMount(() => field.focus());

  function keydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      closeSearch();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const first = matchRooms(Object.values(store.rooms), search.query)[0];
      if (!first) return;
      closeSearch();
      navigate('/rooms/' + first.id);
    }
  }
</script>

<div class="bar">
  <input
    bind:this={field}
    bind:value={search.query}
    type="search"
    aria-label="Search"
    autocomplete="off"
    autocapitalize="none"
    spellcheck="false"
    onkeydown={keydown}
  />
  <button type="button" aria-label="Close search" onclick={closeSearch}>×</button>
</div>

<style>
  .bar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.25rem 0.75rem 0.5rem;
    background: var(--aub);
    color: var(--on-aub);
  }

  input {
    flex: 1;
    min-width: 0;
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--aub-line);
    border-radius: 0.25rem;
    background: transparent;
    color: var(--on-aub);
    font: inherit;
  }

  button {
    padding: 0 0.25rem;
    border: 0;
    background: none;
    color: var(--on-aub);
    font-size: 1.25rem;
    line-height: 1;
    cursor: pointer;
  }
</style>

<script lang="ts">
  import HaltControl from './HaltControl.svelte';
  import Header from './Header.svelte';
  import RoomList from './RoomList.svelte';
  import Search from './Search.svelte';
  import { note, pending } from './lib/note.svelte';
  import { closeSearch, openSearch, search } from './lib/search.svelte';
  import { session, signOut } from './lib/session.svelte';
  import { load, store } from './lib/store.svelte';
</script>

<Header>
  {#if store.loaded && store.link !== 'live'}
    <span class="link">reconnecting</span>
  {/if}
  <button
    type="button"
    class="find"
    aria-label="Search"
    aria-expanded={search.open}
    onclick={search.open ? closeSearch : openSearch}>⌕</button
  >
  <a class="new" href="#/new" aria-label="New room">+</a>
  <HaltControl />
</Header>

{#if search.open}
  <Search />
{/if}

<main>
  {#if store.error}
    <div class="problem">
      <p role="alert">{store.error}</p>
      <button type="button" onclick={load}>Retry</button>
    </div>
  {/if}

  {#if store.loaded}
    <RoomList />
  {/if}

  <footer>
    <a class="note" href="#/note">
      Note
      {#if pending(note.value).length > 0}
        <span class="badge">{pending(note.value).length}</span>
      {/if}
    </a>
    <span>{session.me?.username}</span>
    <button type="button" onclick={signOut}>Log out</button>
  </footer>
</main>

<style>
  .link {
    color: var(--on-aub2);
    font-size: 0.8125rem;
  }

  .new {
    color: var(--on-aub);
    font-size: 1.25rem;
    line-height: 1;
    text-decoration: none;
  }

  /* Outranks Header's `header :global(button)` rule. */
  button.find {
    padding: 0;
    border: 0;
    background: none;
    color: var(--on-aub);
    font-size: 1.25rem;
    line-height: 1;
  }

  .problem {
    display: grid;
    gap: 0.5rem;
    justify-items: start;
    padding: 0.75rem;
  }

  .problem p {
    margin: 0;
    color: var(--err);
  }

  footer {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem;
    color: var(--ink2);
    font-size: 0.875rem;
  }

  .note {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    color: var(--ink2);
  }

  .badge {
    padding: 1px 6px;
    border-radius: 5px;
    background: var(--ask);
    color: var(--on-ask);
    font-size: 11px;
    font-weight: 650;
  }

  button {
    padding: 0.25rem 0.6rem;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
  }
</style>

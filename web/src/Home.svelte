<script lang="ts">
  import { onMount } from 'svelte';
  import Header from './Header.svelte';
  import RoomList from './RoomList.svelte';
  import { route, startRouting } from './lib/route.svelte';
  import { session, signOut } from './lib/session.svelte';
  import { connect, disconnect, load, store } from './lib/store.svelte';

  // Home only exists while signed in, so unmounting is the one place the
  // stream is torn down, whether the person logged out or a 401 sent them away.
  onMount(() => {
    connect();
    const stop = startRouting();
    return () => {
      disconnect();
      stop();
    };
  });
</script>

{#if route.path === '/new'}
  <Header title="New room" back="#/" />
  <main></main>
{:else}
  <Header>
    {#if store.loaded && store.link !== 'live'}
      <span class="link">reconnecting</span>
    {/if}
    <a class="new" href="#/new" aria-label="New room">+</a>
  </Header>

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
      <span>{session.me?.username}</span>
      <button type="button" onclick={signOut}>Log out</button>
    </footer>
  </main>
{/if}

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

  button {
    padding: 0.25rem 0.6rem;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
  }
</style>

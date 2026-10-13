<script lang="ts">
  import { onMount } from 'svelte';
  import HaltControl from './HaltControl.svelte';
  import Header from './Header.svelte';
  import ListScreen from './ListScreen.svelte';
  import NewRoom from './NewRoom.svelte';
  import Note from './Note.svelte';
  import Room from './Room.svelte';
  import RoomSettings from './RoomSettings.svelte';
  import Unread from './Unread.svelte';
  import { startKeys } from './lib/keys';
  import { layout, startLayout } from './lib/layout.svelte';
  import { closeSearch } from './lib/search.svelte';
  import { roomIdOf, route, settingsIdOf, startRouting } from './lib/route.svelte';
  import { connect, disconnect, load, store } from './lib/store.svelte';

  // Home only exists while signed in, so unmounting is the one place the
  // stream is torn down, whether the person logged out or a 401 sent them away.
  onMount(() => {
    connect();
    const stop = startRouting();
    const stopLayout = startLayout();
    const stopKeys = startKeys();
    return () => {
      disconnect();
      closeSearch();
      stopKeys();
      stopLayout();
      stop();
    };
  });

  const roomId = $derived(roomIdOf(route.path));
  const settingsId = $derived(settingsIdOf(route.path));
  // atList is true on every path that is not a screen of its own, #/ among them.
  const atList = $derived(
    roomId === null &&
      settingsId === null &&
      route.path !== '/unread' &&
      route.path !== '/note' &&
      route.path !== '/new',
  );
</script>

{#snippet problem()}
  {#if store.error}
    <div class="problem">
      <p role="alert">{store.error}</p>
      <button type="button" onclick={load}>Retry</button>
    </div>
  {/if}
{/snippet}

<div class="shell" class:wide={layout.wide}>
  {#if layout.wide || atList}
    <div class="side"><ListScreen /></div>
  {/if}
  {#if layout.wide || !atList}
    <div class="pane">
      {#if roomId !== null}
        {#key roomId}
          <Room id={roomId} />
        {/key}
      {:else if settingsId !== null}
        {#key settingsId}
          <RoomSettings id={settingsId} />
        {/key}
      {:else if route.path === '/unread'}
        <Unread />
      {:else if route.path === '/note'}
        <Note />
      {:else if route.path === '/new'}
        <Header title="New room" back="#/">
          {#if !layout.wide}
          <HaltControl />
        {/if}
        </Header>
        {@render problem()}
        <NewRoom />
      {/if}
    </div>
  {/if}
</div>

<style>
  .wide {
    display: grid;
    grid-template-columns: 20rem minmax(0, 1fr);
    height: 100dvh;
  }

  .wide .side,
  .wide .pane {
    overflow-y: auto;
    min-height: 0;
  }

  .wide .side {
    background: var(--aub);
  }

  .wide .pane {
    background: var(--paper);
    border-left: 1px solid var(--line);
  }

  .wide .pane :global(a.back[href='#/']) {
    display: none;
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
</style>

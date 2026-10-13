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
  import { closeSearch } from './lib/search.svelte';
  import { roomIdOf, route, settingsIdOf, startRouting } from './lib/route.svelte';
  import { connect, disconnect, load, store } from './lib/store.svelte';

  // Home only exists while signed in, so unmounting is the one place the
  // stream is torn down, whether the person logged out or a 401 sent them away.
  onMount(() => {
    connect();
    const stop = startRouting();
    return () => {
      disconnect();
      closeSearch();
      stop();
    };
  });

  const roomId = $derived(roomIdOf(route.path));
  const settingsId = $derived(settingsIdOf(route.path));
</script>

{#snippet problem()}
  {#if store.error}
    <div class="problem">
      <p role="alert">{store.error}</p>
      <button type="button" onclick={load}>Retry</button>
    </div>
  {/if}
{/snippet}

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
    <HaltControl />
  </Header>
  {@render problem()}
  <NewRoom />
{:else}
  <ListScreen />
{/if}

<style>
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

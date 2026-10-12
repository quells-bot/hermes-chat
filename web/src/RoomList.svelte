<script lang="ts">
  import RoomRow from './RoomRow.svelte';
  import { loadCollapsed, saveCollapsed, type Collapsed } from './lib/collapsed';
  import { groupRooms, isUnread } from './lib/rooms';
  import { store } from './lib/store.svelte';

  let collapsed: Collapsed = $state(loadCollapsed());

  const groups = $derived(groupRooms(Object.values(store.rooms)));

  function toggle(key: keyof Collapsed): void {
    collapsed[key] = !collapsed[key];
    saveCollapsed({ dms: collapsed.dms, rooms: collapsed.rooms });
  }
</script>

{#snippet section(key: keyof Collapsed, label: string, list: typeof groups.dms)}
  <section>
    <button type="button" aria-expanded={!collapsed[key]} onclick={() => toggle(key)}>
      <span class="caret" aria-hidden="true">{collapsed[key] ? '▸' : '▾'}</span>
      {label}
      {#if collapsed[key] && list.some(isUnread)}
        <i class="dot" aria-label="unread"></i>
      {/if}
    </button>
    {#if !collapsed[key]}
      <ul>
        {#each list as r (r.id)}
          <RoomRow room={r} />
        {/each}
      </ul>
    {/if}
  </section>
{/snippet}

<nav>
  {@render section('dms', 'Direct messages', groups.dms)}
  {@render section('rooms', 'Rooms', groups.rooms)}
</nav>

<style>
  nav {
    padding-bottom: 6px;
    background: var(--aub);
    color: var(--on-aub);
  }

  button {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 10px 14px 4px;
    border: 0;
    background: none;
    color: var(--on-aub2);
    font-size: 12px;
    letter-spacing: 0.05em;
    text-align: left;
    text-transform: uppercase;
    cursor: pointer;
  }

  .caret {
    width: 10px;
    font-size: 10px;
  }

  .dot {
    margin-left: 4px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--unread);
  }

  ul {
    margin: 0;
    padding: 0;
  }
</style>

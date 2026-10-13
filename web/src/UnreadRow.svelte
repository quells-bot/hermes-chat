<script lang="ts">
  import { roomName, unreadRooms } from './lib/rooms';
  import { store } from './lib/store.svelte';

  const unread = $derived(unreadRooms(Object.values(store.rooms)));
  const questions = $derived(unread.reduce((n, r) => n + r.open_prompts, 0));
</script>

{#if unread.length > 0}
  <a href="#/unread">
    <span class="g">
      <span class="n">Unread</span>
      <span class="s">{unread.map(roomName).join(', ')}</span>
    </span>
    {#if questions > 0}
      <span class="tag">{questions === 1 ? '1 question' : `${questions} questions`}</span>
    {/if}
    <span class="count">{unread.length}</span>
  </a>
{/if}

<style>
  a {
    display: flex;
    align-items: center;
    gap: 9px;
    margin: 8px 8px 4px;
    padding: 9px 10px;
    border-radius: 10px;
    background: var(--aub-hi);
    color: var(--on-aub);
    text-decoration: none;
  }

  .g {
    flex: 1;
    min-width: 0;
  }

  .n,
  .s {
    display: block;
  }

  .n {
    font-size: 14.5px;
    font-weight: 700;
  }

  .s {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    color: var(--on-aub2);
  }

  .tag {
    flex: none;
    padding: 1px 6px;
    border-radius: 5px;
    background: var(--ask);
    color: var(--on-ask);
    font-size: 11px;
    font-weight: 650;
  }

  .count {
    flex: none;
    min-width: 22px;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--unread);
    color: var(--aub);
    text-align: center;
    font-size: 12px;
    font-weight: 800;
  }
</style>

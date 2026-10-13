<script lang="ts">
  import { untrack } from 'svelte';
  import Header from './Header.svelte';
  import Icon from './Icon.svelte';
  import { ago, iconName, preview } from './lib/format';
  import { roomName, unreadRooms } from './lib/rooms';
  import { navigate } from './lib/route.svelte';
  import { load, loadLatest, markAllRead, store } from './lib/store.svelte';
  import type { Message } from './lib/types';

  const rooms = $derived(unreadRooms(Object.values(store.rooms)));

  // The set of unread rooms and the newest message of each: the effect below
  // runs again when either changes, and for nothing else.
  const wanted = $derived(rooms.map((r) => `${r.id} ${r.last_message_id}`).join('\n'));

  $effect(() => {
    void wanted;
    untrack(() => {
      for (const r of rooms) {
        void loadLatest(r.id);
      }
    });
  });

  // The newest message known for a room, when it is the room's newest.
  function newest(roomId: string, lastId: string | null): Message | undefined {
    if (!Object.hasOwn(store.latest, roomId)) return undefined;
    const m = store.latest[roomId];
    return m.id === lastId ? m : undefined;
  }

  function body(m: Message): string {
    const text = preview(m.text);
    return text !== '' ? text : m.attachments.map((a) => a.name).join(', ');
  }

  let pending = $state(false);

  async function markAll(): Promise<void> {
    pending = true;
    try {
      await markAllRead();
    } finally {
      pending = false;
    }
    navigate('/');
  }

  const now = new Date();
</script>

<Header title="Unread" back="#/">
  {#if store.loaded && store.link !== 'live'}
    <span class="link">reconnecting</span>
  {/if}
  <button type="button" disabled={pending} onclick={markAll}>Mark all read</button>
</Header>

{#if store.halted}
  <div class="state">Everything is stopped</div>
{/if}

{#if store.error}
  <div class="problem">
    <p role="alert">{store.error}</p>
    <button type="button" onclick={load}>Retry</button>
  </div>
{/if}

<main>
  {#each rooms as r (r.id)}
    {@const m = newest(r.id, r.last_message_id)}
    <a class="u" class:ask={r.open_prompts > 0} href="#/rooms/{r.id}">
      <span class="h">
        <b>{roomName(r)}</b>
        {#if m}
          <time datetime={m.created_at}>{ago(m.created_at, now)}</time>
        {/if}
      </span>
      {#if m}
        <span class="t">
          {#if m.author}
            <span class="icon"><Icon name={iconName(m.author, store.agents)} /></span>
            <i>{m.author.name}{m.prompt ? ' asks' : ''}:</i>
          {/if}
          {body(m)}
        </span>
      {/if}
    </a>
  {/each}
</main>

<style>
  .link {
    color: var(--on-aub2);
    font-size: 0.8125rem;
  }

  .state {
    padding: 8px 14px;
    color: var(--ink2);
    font-size: 13px;
    text-align: center;
  }

  .u {
    display: block;
    padding: 11px 14px;
    border-bottom: 1px solid var(--line);
    color: inherit;
    text-decoration: none;
  }

  .h {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }

  b {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 14.5px;
  }

  time {
    flex: none;
    font-size: 12px;
    color: var(--ink2);
  }

  .t {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    margin-top: 2px;
    font-size: 13.5px;
    color: var(--ink2);
  }

  .t i {
    font-style: normal;
    font-weight: 600;
    color: var(--ink);
  }

  .icon {
    display: inline-block;
    width: 15px;
    height: 15px;
    margin-right: 4px;
    vertical-align: -3px;
    border-radius: 3px;
    overflow: hidden;
  }

  .ask {
    background: var(--ask-bg);
    box-shadow: inset 3px 0 0 var(--ask);
  }

  .ask .t i {
    color: var(--ask-ink);
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

  .problem button {
    padding: 0.25rem 0.6rem;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
  }
</style>

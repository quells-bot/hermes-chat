<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import Header from './Header.svelte';
  import Message from './Message.svelte';
  import { firstUnread } from './lib/feed';
  import { roomName } from './lib/rooms';
  import { navigate, route } from './lib/route.svelte';
  import { whenSeen } from './lib/seen';
  import { closeRoom, loadOlder, markRead, openRoom, store } from './lib/store.svelte';
  import type { RoomAgent } from './lib/types';

  // One room. Home draws a new one for each id, so id never changes here.
  let { id }: { id: string } = $props();

  // The id comes from the address bar, so only what the store itself holds
  // under it counts.
  function held<T>(all: Record<string, T>, key: string): T | undefined {
    const value = all[key];
    return Object.hasOwn(all, key) ? value : undefined;
  }

  const room = $derived(held(store.rooms, id));
  const feed = $derived(held(store.feeds, id));
  const messages = $derived(feed?.messages ?? []);

  onMount(() => {
    void openRoom(id);
    return () => closeRoom(id);
  });

  // A room the person has left, or an address that names none.
  $effect(() => {
    if (store.loaded && room === undefined) {
      navigate('/');
    }
  });

  function doing(agent: RoomAgent): string {
    if (agent.working) return 'working';
    return agent.connected ? 'idle' : 'offline';
  }

  const sub = $derived((room?.agents ?? []).map((a) => `${a.display_name} ${doing(a)}`).join(' · '));

  const standing = $derived.by(() => {
    if (store.halted) return 'Everything is stopped';
    if (room?.archived) return 'Archived';
    if (!room?.paused) return '';
    return room.pause_reason === 'turn_cap' ? `Paused: turn cap of ${room.turn_cap} reached` : 'Paused';
  });

  let feedEl: HTMLElement | undefined = $state();

  // The first unread message, fixed when the messages first arrive: the
  // divider stays where it is while the marker moves on.
  let divider: string | null = $state(null);
  let dividerFixed = false;

  $effect.pre(() => {
    if (dividerFixed || room === undefined || messages.length === 0) {
      return;
    }
    dividerFixed = true;
    divider = firstUnread(messages, room.last_read_message_id);
  });

  // Reading every message, and its text, makes an effect run again when one
  // arrives, is replaced, or grows while it is being written.
  function watchMessages(): void {
    for (const m of messages) {
      void m.text;
    }
  }

  // placed: the feed has been put where it opens. drawnFirst: the first
  // message as last drawn. before: the feed as it was before the change now
  // being drawn.
  let placed = false;
  let drawnFirst = '';
  let before: { height: number; atEnd: boolean } | null = null;

  $effect.pre(() => {
    watchMessages();
    if (!placed || feedEl === undefined) {
      return;
    }
    const height = feedEl.scrollHeight;
    before = { height, atEnd: height - feedEl.scrollTop - feedEl.clientHeight <= 40 };
  });

  $effect(() => {
    watchMessages();
    if (feedEl === undefined || messages.length === 0) {
      return;
    }
    const first = messages[0].id;
    if (!placed) {
      placed = true;
      const mark = feedEl.querySelector('[role="separator"]');
      if (mark !== null) {
        mark.scrollIntoView({ block: 'start' });
      } else {
        feedEl.scrollTop = feedEl.scrollHeight;
      }
    } else if (before !== null) {
      if (first !== drawnFirst) {
        // Older messages went in above: what was in view stays in view.
        feedEl.scrollTop += feedEl.scrollHeight - before.height;
      } else if (before.atEnd) {
        feedEl.scrollTop = feedEl.scrollHeight;
      }
    }
    drawnFirst = first;
    before = null;
  });

  function topSeen(seen: boolean): void {
    if (seen && placed) {
      void loadOlder(id);
    }
  }

  // The marker moves when the end of the feed has been seen on a page that
  // is itself seen, and again for each message that arrives while both hold.
  let endSeen = $state(false);
  let visible = $state(document.visibilityState === 'visible');

  $effect(() => {
    const last = messages.at(-1)?.id;
    if (endSeen && visible && last !== undefined) {
      untrack(() => void markRead(id, last));
    }
  });
</script>

<svelte:document onvisibilitychange={() => (visible = document.visibilityState === 'visible')} />

<div class="room">
  <Header title={room ? roomName(room) : undefined} back={route.prev === '/unread' ? '#/unread' : '#/'} {sub} />

  <div class="feed" bind:this={feedEl}>
    {#if feed?.error}
      <div class="problem">
        <p role="alert">{feed.error}</p>
        <button type="button" onclick={() => openRoom(id)}>Retry</button>
      </div>
    {/if}
    {#if feed?.more}
      <div class="edge" data-edge="top" use:whenSeen={topSeen}></div>
    {/if}
    {#each messages as message (message.id)}
      {#if message.id === divider}
        <div class="divider" role="separator">New</div>
      {/if}
      <Message {message} />
    {/each}
    <div class="edge" data-edge="end" use:whenSeen={(seen) => (endSeen = seen)}></div>
    {#if standing}
      <div class="state">{standing}</div>
    {/if}
  </div>
</div>

<style>
  .room {
    display: flex;
    flex-direction: column;
    height: 100dvh;
  }

  /* The script keeps the feed's place when messages go in above; the
     browser's own scroll anchoring would move it a second time. */
  .feed {
    flex: 1;
    overflow-y: auto;
    overflow-anchor: none;
    padding: 6px 0;
    background: var(--paper);
  }

  /* A sentinel has a height so that it is in view or out of it, never on the line. */
  .edge {
    height: 1px;
  }

  .divider {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 14px;
    color: var(--accent);
    font-size: 12px;
    font-weight: 650;
  }

  .divider::before,
  .divider::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--accent);
  }

  .state {
    padding: 8px 14px;
    color: var(--ink2);
    font-size: 13px;
    text-align: center;
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

  button {
    padding: 0.25rem 0.6rem;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
  }
</style>

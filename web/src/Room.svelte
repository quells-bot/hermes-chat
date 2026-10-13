<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import Composer from './Composer.svelte';
  import Header from './Header.svelte';
  import Message from './Message.svelte';
  import RoomMenu from './RoomMenu.svelte';
  import { firstUnread } from './lib/feed';
  import { roomName } from './lib/rooms';
  import { navigate, route } from './lib/route.svelte';
  import { whenSeen } from './lib/seen';
  import { closeRoom, load, loadOlder, markRead, openRoom, stopTurns, store } from './lib/store.svelte';
  import type { Message as MessageData, RoomAgent } from './lib/types';

  // One room. Home draws a new one for each id, so id never changes here.
  let { id }: { id: string } = $props();

  // What the composer replies to, set by a message's Reply and dropped again
  // by sending or by the chip's ×.
  let replyTo = $state<MessageData | null>(null);

  // One tap, no confirmation. The header Stop stays disabled until the
  // request settles, so a second tap cannot ask again; it mirrors
  // HaltControl's local busy. A failure shows as store.error.
  let stopBusy = $state(false);
  async function stopWorking(): Promise<void> {
    if (stopBusy) return;
    stopBusy = true;
    try {
      await stopTurns(id);
    } finally {
      stopBusy = false;
    }
  }

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
    // svelte:document's onvisibilitychange does not survive in the browser,
    // so the listener is registered directly, as the store does.
    document.addEventListener('visibilitychange', becameVisible);
    return () => {
      closeRoom(id);
      document.removeEventListener('visibilitychange', becameVisible);
    };
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

  // The feed has what it opens with: held, no error and nothing still on its
  // way. The messages before the newest page (the store's backlog) arrive
  // after it, and the first unread one is usually among them. A feed without
  // messages has still arrived: the divider is decided then, empty or not.
  const arrived = $derived(feed !== undefined && feed.loading === false && feed.error === '');

  // The first unread message the page can name: null when there is none, or
  // when the oldest held message is itself unread and older ones remain on
  // the server (the backlog did not reach them), so where they start is
  // unknown.
  function knownUnread(): string | null {
    const first = firstUnread(messages, room?.last_read_message_id ?? null);
    if (first === null) return null;
    if (feed?.more === true && messages[0]?.id === first) return null;
    return first;
  }

  // The divider, decided when the feed has arrived: it stays where it was
  // while the room stays open, except that new unread messages the view
  // cannot hold move it to above the first of them (follow). It is a plain
  // variable; revision tells the derived below to read it again after follow
  // has moved it.
  let fixed: { id: string | null } | null = null;
  let revision = $state(0);

  const divider = $derived.by(() => {
    void revision;
    if (room === undefined || !arrived) {
      return null;
    }
    if (fixed === null) {
      fixed = { id: knownUnread() };
    }
    return fixed.id;
  });

  // Reading every message, and its text, makes an effect run again when one
  // arrives, is replaced, or grows while it is being written.
  function watchMessages(): void {
    for (const m of messages) {
      void m.text;
    }
  }

  // placed: the feed has been put where it opens. It is state because the
  // sentinels are drawn only once it is true; the effects below read it
  // untracked, so that setting it does not run them again. drawnFirst: the
  // first message as last drawn. before: the feed as it was before the
  // change now being drawn. pending: the feed was at its end when the page
  // was hidden, and has not been put back where it stands.
  let placed = $state(false);
  let drawnFirst = '';
  let before: { height: number; atEnd: boolean } | null = null;
  let pending = false;

  $effect.pre(() => {
    watchMessages();
    if (!untrack(() => placed) || feedEl === undefined) {
      return;
    }
    const height = feedEl.scrollHeight;
    before = { height, atEnd: height - feedEl.scrollTop - feedEl.clientHeight <= 40 };
  });

  // place puts the feed where it opens: on the divider, or at its oldest
  // held message when the first unread one may lie on a page not yet loaded,
  // or at its end when nothing is unread.
  function place(): void {
    if (feedEl === undefined) {
      return;
    }
    const mark = feedEl.querySelector('[role="separator"]');
    if (mark !== null) {
      mark.scrollIntoView({ block: 'start' });
    } else if (firstUnread(messages, room?.last_read_message_id ?? null) !== null) {
      feedEl.scrollTop = 0;
    } else {
      feedEl.scrollTop = feedEl.scrollHeight;
    }
  }

  // Whether the end of the feed is in view, from its scroll position.
  function endInView(): boolean {
    if (feedEl === undefined) {
      return false;
    }
    return feedEl.scrollHeight - feedEl.scrollTop - feedEl.clientHeight <= 40;
  }

  // follow puts the feed back at its end after new messages were drawn, but
  // never past the top of the first unread message: when the new messages do
  // not all fit in the view, the feed stops with the first of them at the
  // top and New above it, and the end is no longer in view, so the marker
  // does not move on a report taken before they arrived. A message that is
  // not newer than the marker, like a reply that grows while it is being
  // written, is followed to its end as before.
  function follow(): void {
    if (feedEl === undefined) {
      return;
    }
    const target = feedEl.scrollHeight - feedEl.clientHeight;
    const unread = knownUnread();
    let top: number | null = null;
    if (unread !== null) {
      // Stand on New when it is drawn above the first unread message; while
      // it is still being drawn for it, the message itself is the anchor.
      const el = feedEl.querySelector<HTMLElement>(`article[data-mid="${unread}"]`);
      const mark = el?.previousElementSibling;
      const anchor = mark !== undefined && mark !== null && mark.getAttribute('role') === 'separator' ? mark : el;
      if (anchor !== null) {
        top = anchor.getBoundingClientRect().top - feedEl.getBoundingClientRect().top + feedEl.scrollTop;
      }
    }
    const clamped = top !== null && top < target;
    if (clamped && unread !== null && fixed?.id !== unread) {
      // New is drawn first, so that it stands above the first unread message
      // in the same view that scrolls to it.
      fixed = { id: unread };
      revision += 1;
    }
    feedEl.scrollTop = top !== null && top < target ? top : target;
    if (endSeen && !endInView()) {
      endSeen = false;
    }
  }

  $effect(() => {
    watchMessages();
    if (feedEl === undefined || messages.length === 0) {
      return;
    }
    const first = messages[0].id;
    if (!untrack(() => placed)) {
      if (!arrived) {
        return;
      }
      placed = true;
      place();
      drawnFirst = first;
      before = null;
      return;
    }
    if (before === null) {
      drawnFirst = first;
      return;
    }
    if (first !== drawnFirst) {
      if (before.atEnd) {
        // The page was replaced by a wholly newer one: the same rule as any
        // other arrival at the end.
        follow();
      } else {
        // Older messages went in above: what was in view stays in view.
        feedEl.scrollTop += feedEl.scrollHeight - before.height;
      }
    } else if (before.atEnd) {
      if (untrack(() => visible)) {
        follow();
      } else {
        // A hidden page does not move the feed; New marks where the unread
        // it could not hold starts, so the person is not carried past it.
        pending = true;
        const unread = knownUnread();
        if (unread !== null && fixed?.id !== unread) {
          fixed = { id: unread };
          revision += 1;
        }
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
  // The end is watched only once the feed has been placed: the end of an
  // empty feed, or of one not yet put where it opens, is in view without
  // anything having been read. Its sentinel's first report then says whether
  // the end really is in view.
  let endSeen = $state(false);
  let visible = $state(document.visibilityState === 'visible');

  $effect(() => {
    const last = messages.at(-1)?.id;
    if (endSeen && visible && last !== undefined) {
      untrack(() => void markRead(id, last));
    }
  });

  // What was not shown is not read: when the page becomes visible again and
  // the feed was at its end while hidden, it is put back where it stands.
  function becameVisible(): void {
    visible = document.visibilityState === 'visible';
    if (visible && pending) {
      pending = false;
      follow();
    }
  }
</script>

<div class="room">
  <Header title={room ? roomName(room) : undefined} back={route.prev === '/unread' ? '#/unread' : '#/'} {sub}>
    {#if store.loaded && store.link !== 'live'}
      <span class="link">reconnecting</span>
    {/if}
    {#if room?.agents.some((a) => a.working)}
      <button class="stop" type="button" disabled={stopBusy} onclick={stopWorking}>Stop</button>
    {/if}
    <RoomMenu {id} />
  </Header>

  {#if store.error}
    <div class="problem">
      <p role="alert">{store.error}</p>
      <button type="button" onclick={load}>Retry</button>
    </div>
  {/if}
  {#if feed?.error}
    <div class="problem">
      <p role="alert">{feed.error}</p>
      <button type="button" onclick={() => openRoom(id)}>Retry</button>
    </div>
  {/if}

  <div class="feed" bind:this={feedEl}>
    {#if placed && feed?.more}
      <div class="edge" data-edge="top" use:whenSeen={topSeen}></div>
    {/if}
    <!-- The messages stay in the document before the feed has been placed, so
       that placing it can measure them; they are invisible until then (A4). -->
    <div class="messages" class:placed>
      <!-- Nothing draws the divider when there are no messages; it is read
         here so that it is decided when the feed arrives, empty or not (A2). -->
      {#if divider !== null}{/if}
      {#each messages as message (message.id)}
        {#if message.id === divider}
          <div class="divider" role="separator">New</div>
        {/if}
        <Message {message} onreply={() => (replyTo = message)} />
      {/each}
    </div>
    {#if placed}
      <div class="edge" data-edge="end" use:whenSeen={(seen) => (endSeen = seen)}></div>
    {/if}
    {#if standing}
      <div class="state">{standing}</div>
    {/if}
  </div>

  {#if room !== undefined && !room.archived}
    <Composer {id} {replyTo} onclearReply={() => (replyTo = null)} />
  {/if}
</div>

<style>
  .room {
    display: flex;
    flex-direction: column;
    height: 100dvh;
  }

  .link {
    color: var(--on-aub2);
    font-size: 0.8125rem;
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

  /* The messages are hidden until the feed has been placed: until then they
     are still in the document, so that placing can measure them. */
  .messages {
    visibility: hidden;
  }

  .messages.placed {
    visibility: visible;
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

  /* Outranks Header's `header :global(button)` rule, as HaltControl's does. */
  button.stop {
    border-color: var(--err);
    background: transparent;
    color: var(--err);
  }
</style>
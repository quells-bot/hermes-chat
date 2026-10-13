<script lang="ts">
  import { onMount } from 'svelte';
  import HaltControl from './HaltControl.svelte';
  import Header from './Header.svelte';
  import Icon from './Icon.svelte';
  import { listHumans } from './lib/api';
  import { byName } from './lib/rooms';
  import { navigate, replace } from './lib/route.svelte';
  import { addAgent, changeRoom, invite, load, reason, removeAgent, store } from './lib/store.svelte';
  import type { Human } from './lib/types';

  // One room's settings. Home draws a new one for each id, so id never
  // changes here.
  let { id }: { id: string } = $props();

  // topicDraft and capDraft are what the person has typed and not yet saved;
  // null while untouched, so that each input follows the room until then.
  let topicDraft = $state<string | null>(null);
  let capDraft = $state<string | null>(null);

  // problem is the last refused write, until the next write that works or the
  // person dismisses it.
  let problem = $state('');

  // busy covers every write on the screen: while one is in flight, every
  // control is disabled. It is set before the first await, so a second tap
  // that lands before the screen redraws still sends nothing.
  let busy = $state(false);

  // armed is the person being invited, once Invite has been tapped.
  let armed = $state<string | null>(null);

  // everyone is the people the server lists; empty until it answers, and when
  // it cannot.
  let everyone = $state<Human[]>([]);

  // The id comes from the address bar, so only what the store itself holds
  // under it counts.
  const room = $derived(Object.hasOwn(store.rooms, id) ? store.rooms[id] : undefined);
  const frozen = $derived(room?.archived ?? false);

  // A room the person has left, an address that names none, or a DM, which
  // has no settings.
  $effect(() => {
    if (!store.loaded) return;
    if (room === undefined) {
      navigate('/');
    } else if (room.flavor === 'dm') {
      replace('/rooms/' + id);
    }
  });

  onMount(() => {
    listHumans().then(
      (humans) => (everyone = humans),
      (err) => (problem = reason(err)),
    );
  });

  const topic = $derived(topicDraft ?? room?.topic ?? '');
  const cap = $derived(capDraft ?? String(room?.turn_cap ?? ''));
  const capValid = $derived(/^\d+$/.test(cap.trim()) && Number(cap) >= 1 && Number(cap) <= 100);
  const topicChanged = $derived(room !== undefined && topic.trim() !== room.topic);
  const capChanged = $derived(room !== undefined && capValid && Number(cap) !== room.turn_cap);
  const canSave = $derived(!busy && !frozen && topic.trim() !== '' && capValid && (topicChanged || capChanged));

  const inRoom = $derived(new Set(room?.agents.map((a) => a.id)));
  const members = $derived([...(room?.agents ?? [])].sort((a, b) => byName(a.display_name, b.display_name)));
  const addable = $derived(
    Object.values(store.agents)
      .filter((a) => !a.revoked && !inRoom.has(a.id))
      .sort((a, b) => byName(a.display_name, b.display_name)),
  );
  const people = $derived([...(room?.humans ?? [])].sort((a, b) => byName(a.username, b.username)));
  const invitable = $derived(
    everyone
      .filter((h) => !room?.humans.some((m) => m.id === h.id))
      .sort((a, b) => byName(a.username, b.username)),
  );

  // act runs one write; a success clears the problem, a failure becomes it.
  async function act(write: () => Promise<void>): Promise<boolean> {
    if (busy) return false;
    busy = true;
    try {
      await write();
      problem = '';
      return true;
    } catch (err) {
      problem = reason(err);
      return false;
    } finally {
      busy = false;
    }
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!canSave) return;
    const changes: { topic?: string; turn_cap?: number } = {};
    if (topicChanged) changes.topic = topic.trim();
    if (capChanged) changes.turn_cap = Number(cap);
    if (await act(() => changeRoom(id, changes))) {
      topicDraft = null;
      capDraft = null;
    }
  }

  // focusing puts the focus on an element as it appears, for a control that
  // has just replaced the one the person tapped.
  function focusing(node: HTMLElement) {
    node.focus();
  }

  async function confirm(humanId: string): Promise<void> {
    if (await act(() => invite(id, humanId))) {
      armed = null;
    }
  }
</script>

<Header title="Settings" sub={room?.topic ?? ''} back="#/rooms/{id}">
  <HaltControl />
</Header>

{#if store.error}
  <div class="problem">
    <p role="alert">{store.error}</p>
    <button type="button" onclick={load}>Retry</button>
  </div>
{/if}
{#if problem}
  <div class="problem">
    <p role="alert">{problem}</p>
    <button type="button" onclick={() => (problem = '')}>Dismiss</button>
  </div>
{/if}

{#if room !== undefined && room.flavor !== 'dm'}
  <main>
    <section>
      <form onsubmit={save}>
        <label>
          Topic
          <input
            type="text"
            maxlength="200"
            value={topic}
            disabled={busy || frozen}
            oninput={(e) => (topicDraft = e.currentTarget.value)}
          />
        </label>
        <label>
          Turn cap
          <input
            type="number"
            min="1"
            max="100"
            inputmode="numeric"
            value={cap}
            disabled={busy || frozen}
            oninput={(e) => (capDraft = e.currentTarget.value)}
          />
        </label>
        <button type="submit" disabled={!canSave}>Save</button>
      </form>
    </section>

    <section>
      <h2>Agents</h2>
      <ul>
        {#each members as a (a.id)}
          <li>
            <span class="icon"><Icon name={a.handle} /></span>
            <span class="name">{a.display_name}</span>
            {#if members.length > 1}
              <button type="button" disabled={busy || frozen} onclick={() => act(() => removeAgent(id, a.id))}>
                Remove
              </button>
            {/if}
          </li>
        {/each}
        {#each addable as a (a.id)}
          <li>
            <span class="icon"><Icon name={a.handle} /></span>
            <span class="name">{a.display_name}</span>
            <button type="button" disabled={busy || frozen} onclick={() => act(() => addAgent(id, a.id))}>Add</button>
          </li>
        {/each}
      </ul>
    </section>

    <section>
      <h2>People</h2>
      <ul>
        {#each people as h (h.id)}
          <li><span class="name">{h.username}</span></li>
        {/each}
        {#each invitable as h (h.id)}
          <li>
            <span class="name">{h.username}</span>
            {#if armed === h.id}
              <button type="button" disabled={busy || frozen} onclick={() => confirm(h.id)} use:focusing>Confirm</button>
              <button type="button" disabled={busy || frozen} onclick={() => (armed = null)}>Cancel</button>
            {:else}
              <button type="button" disabled={busy || frozen} onclick={() => (armed = h.id)}>Invite</button>
            {/if}
          </li>
        {/each}
      </ul>
    </section>

    <section>
      <button type="button" disabled={busy} onclick={() => act(() => changeRoom(id, { archived: !room.archived }))}>
        {room.archived ? 'Unarchive' : 'Archive'}
      </button>
    </section>
  </main>
{/if}

<style>
  main {
    max-width: 26rem;
    margin: 0 auto;
    padding: 1rem 0.75rem;
    display: grid;
    gap: 1rem;
  }

  section {
    display: grid;
    gap: 0.5rem;
    padding: 1rem;
    border: 1px solid var(--line);
    border-radius: 0.375rem;
    background: var(--paper);
  }

  h2 {
    margin: 0;
    font-size: 0.875rem;
    color: var(--ink2);
  }

  form {
    display: grid;
    gap: 0.75rem;
  }

  label {
    display: grid;
    gap: 0.25rem;
    font-size: 0.875rem;
    font-weight: 600;
    color: var(--ink);
  }

  input {
    padding: 0.5rem;
    border: 1px solid var(--edge);
    border-radius: 0.25rem;
    background: var(--paper);
    color: var(--ink);
    font-weight: 400;
  }

  ul {
    display: grid;
    gap: 0.5rem;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .icon {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 5px;
    overflow: hidden;
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
    color: var(--ink);
  }

  button:disabled {
    opacity: 0.5;
  }
</style>

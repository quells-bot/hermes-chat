<script lang="ts">
  import Icon from './Icon.svelte';
  import { ApiError, createRoom, openDM } from './lib/api';
  import { byName } from './lib/rooms';
  import { navigate } from './lib/route.svelte';
  import { putRoom, store } from './lib/store.svelte';
  import type { Room } from './lib/types';

  let topic = $state('');
  let checked = $state<Record<string, boolean>>({});
  let pending = $state(false);
  let error = $state('');

  const agents = $derived(
    Object.values(store.agents)
      .filter((a) => !a.revoked)
      .sort((a, b) => byName(a.display_name, b.display_name)),
  );
  const ids = $derived(agents.filter((a) => checked[a.id]).map((a) => a.id));
  const ready = $derived(topic.trim() !== '' && ids.length > 0);

  // One pending flag covers both kinds of request.
  async function send(make: () => Promise<Room>) {
    if (pending) return;
    pending = true;
    error = '';
    try {
      putRoom(await make());
      navigate('/');
    } catch (err) {
      error = err instanceof ApiError || err instanceof Error ? err.message : String(err);
    } finally {
      pending = false;
    }
  }

  function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!ready) return;
    const t = topic.trim();
    const picked = ids;
    send(() => createRoom(t, picked));
  }
</script>

<main>
  <section>
    <h2>Direct message</h2>
    {#each agents as a (a.id)}
      <button type="button" class="agent" disabled={pending} onclick={() => send(() => openDM(a.id))}>
        <span class="icon"><Icon name={a.handle} /></span>
        {a.display_name}
      </button>
    {/each}
  </section>

  <section>
    <h2>Room</h2>
    <form onsubmit={submit}>
      <label>
        Topic
        <input type="text" name="topic" maxlength="200" bind:value={topic} />
      </label>
      {#each agents as a (a.id)}
        <label class="check">
          <input type="checkbox" bind:checked={checked[a.id]} />
          {a.display_name}
        </label>
      {/each}
      {#if error}
        <p role="alert">{error}</p>
      {/if}
      <button type="submit" class="create" disabled={pending || !ready}>Create</button>
    </form>
  </section>
</main>

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

  .check {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 400;
  }

  input[type='checkbox'] {
    width: 1.125rem;
    height: 1.125rem;
    margin: 0;
    accent-color: var(--accent);
  }

  input[type='text'] {
    padding: 0.5rem;
    border: 1px solid var(--edge);
    border-radius: 0.25rem;
    background: var(--paper);
    color: var(--ink);
    font-weight: 400;
  }

  .agent {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem;
    border: 1px solid var(--edge);
    border-radius: 0.25rem;
    background: var(--paper);
    color: var(--ink);
    font-weight: 600;
    text-align: left;
  }

  .icon {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 5px;
    overflow: hidden;
  }

  p {
    margin: 0;
    color: var(--err);
    font-size: 0.875rem;
  }

  .create {
    padding: 0.5rem;
    border: 0;
    border-radius: 0.25rem;
    background: var(--btn);
    color: var(--on-btn);
    font-weight: 600;
  }

  button:disabled {
    opacity: 0.5;
  }
</style>

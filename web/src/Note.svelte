<script lang="ts">
  import { onMount } from 'svelte';
  import HaltControl from './HaltControl.svelte';
  import Header from './Header.svelte';
  import NoteEntry from './NoteEntry.svelte';
  import {
    approvedChars,
    chars,
    loadNote,
    maxApproved,
    maxText,
    note,
    pending,
    saveText,
  } from './lib/note.svelte';
  import { load, reason, store } from './lib/store.svelte';

  // draft is what the person has typed and not yet saved; null while the box
  // is untouched, so that it follows each load of the note.
  let draft = $state<string | null>(null);

  // problem is the last refused write, until the next write that works or the
  // person dismisses it.
  let problem = $state('');

  const shown = $derived(draft ?? note.value?.text ?? '');
  const approved = $derived(note.value?.entries.filter((e) => e.approved) ?? []);
  const canSave = $derived(
    draft !== null && note.value !== null && draft.trim() !== note.value.text && chars(draft) <= maxText && !note.busy,
  );

  // The stream does not carry the note, so a suggestion made while the page
  // was open is fetched here.
  onMount(() => void loadNote());

  async function save() {
    if (draft === null) return;
    // Typing that lands while the write is in flight stays in the box.
    const sent = draft;
    try {
      await saveText(sent);
      if (draft === sent) draft = null;
      problem = '';
    } catch (err) {
      problem = reason(err);
    }
  }
</script>

<Header title="Note" back="#/">
  <HaltControl />
</Header>

{#if store.error}
  <div class="problem">
    <p role="alert">{store.error}</p>
    <button type="button" onclick={load}>Retry</button>
  </div>
{/if}
{#if note.error}
  <div class="problem">
    <p role="alert">{note.error}</p>
    <button type="button" onclick={loadNote}>Retry</button>
  </div>
{/if}
{#if problem}
  <div class="problem">
    <p role="alert">{problem}</p>
    <button type="button" onclick={() => (problem = '')}>Dismiss</button>
  </div>
{/if}

{#if note.value !== null}
  <main>
    <section>
      <textarea aria-label="Note" rows="8" value={shown} oninput={(e) => (draft = e.currentTarget.value)}></textarea>
      <div class="row">
        <span class="count" class:over={chars(shown) > maxText}>{chars(shown)} / {maxText}</span>
        <button type="button" disabled={!canSave} onclick={save}>Save</button>
      </div>
    </section>

    {#if pending(note.value).length > 0}
      <section>
        <h2>To review</h2>
        <ul>
          {#each pending(note.value) as entry (entry.id)}
            <NoteEntry {entry} disabled={note.busy} onerror={(why) => (problem = why)} />
          {/each}
        </ul>
      </section>
    {/if}

    {#if approved.length > 0}
      <section>
        <h2>Approved</h2>
        <ul>
          {#each approved as entry (entry.id)}
            <NoteEntry {entry} disabled={note.busy} onerror={(why) => (problem = why)} />
          {/each}
        </ul>
      </section>
    {/if}

    <span class="count" class:over={approvedChars(note.value, shown) > maxApproved}>
      {approvedChars(note.value, shown)} / {maxApproved}
    </span>
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
  }

  h2 {
    margin: 0;
    font-size: 0.875rem;
    color: var(--ink2);
  }

  ul {
    display: grid;
    gap: 0.5rem;
    margin: 0;
    padding: 0;
  }

  textarea {
    padding: 0.5rem;
    border: 1px solid var(--edge);
    border-radius: 0.25rem;
    background: var(--paper);
    color: var(--ink);
    font: inherit;
    resize: vertical;
  }

  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .count {
    color: var(--ink2);
    font-size: 0.8125rem;
  }

  .over {
    color: var(--err);
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

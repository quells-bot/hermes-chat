<script lang="ts">
  import Icon from './Icon.svelte';
  import { clock } from './lib/format';
  import { approve, chars, editEntry, maxEntry, removeEntry, unapprove } from './lib/note.svelte';
  import { reason } from './lib/store.svelte';
  import type { NoteEntry as Entry } from './lib/types';

  let { entry, disabled, onerror }: { entry: Entry; disabled: boolean; onerror: (why: string) => void } = $props();

  // draft is the text being edited; null when the entry is not in edit mode.
  let draft = $state<string | null>(null);

  const count = $derived(draft === null ? 0 : chars(draft));
  const canSave = $derived(
    draft !== null && draft.trim() !== '' && draft.trim() !== entry.text && count <= maxEntry,
  );

  // run makes one call and reports its outcome: the reason on a refusal, ''
  // on success. It returns whether the call went through.
  async function run(call: () => Promise<void>): Promise<boolean> {
    try {
      await call();
      onerror('');
      return true;
    } catch (err) {
      onerror(reason(err));
      return false;
    }
  }

  async function saveEdit() {
    if (draft === null || !canSave) return;
    // Typing that lands while the write is in flight keeps the box open.
    const sent = draft;
    if (await run(() => editEntry(entry.id, sent.trim())) && draft === sent) {
      draft = null;
    }
  }

  // focusing puts the focus on an element as it appears, for a control that
  // has just replaced the one the person tapped.
  function focusing(node: HTMLElement) {
    node.focus();
  }
</script>

<li>
  <div class="who">
    <span class="icon"><Icon name={entry.agent.handle} /></span>
    <span class="name">{entry.agent.display_name}</span>
    <span class="when">{clock(entry.created_at, new Date())}</span>
  </div>

  {#if draft === null}
    <p>{entry.text}</p>
    <div class="row">
      {#if entry.approved}
        <button type="button" {disabled} onclick={() => run(() => unapprove(entry.id))}>Unapprove</button>
      {:else}
        <button type="button" {disabled} onclick={() => run(() => approve(entry.id))}>Approve</button>
      {/if}
      <button type="button" {disabled} onclick={() => (draft = entry.text)}>Edit</button>
      <button type="button" {disabled} onclick={() => run(() => removeEntry(entry.id))}>Delete</button>
    </div>
  {:else}
    <textarea aria-label="Entry" rows="3" bind:value={draft} use:focusing></textarea>
    <div class="row">
      <span class="count" class:over={count > maxEntry}>{count} / {maxEntry}</span>
      <button type="button" disabled={disabled || !canSave} onclick={saveEdit}>Save</button>
      <button type="button" {disabled} onclick={() => (draft = null)}>Cancel</button>
    </div>
  {/if}
</li>

<style>
  li {
    display: grid;
    gap: 0.5rem;
    padding: 0.75rem;
    border: 1px solid var(--line);
    border-radius: 0.375rem;
    background: var(--paper);
    list-style: none;
  }

  .who {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.875rem;
  }

  .icon {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 5px;
    overflow: hidden;
  }

  .name {
    font-weight: 600;
    color: var(--ink);
  }

  .when {
    color: var(--ink2);
    font-size: 0.8125rem;
  }

  p {
    margin: 0;
    color: var(--ink);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
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
    gap: 0.5rem;
  }

  .count {
    margin-right: auto;
    color: var(--ink2);
    font-size: 0.8125rem;
  }

  .over {
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

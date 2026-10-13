<script lang="ts">
  import { ApiError } from './lib/api';
  import { preview } from './lib/format';
  import { send, store, type SendMode } from './lib/store.svelte';
  import type { Message } from './lib/types';

  let {
    id,
    replyTo = null,
    onclearReply,
  }: { id: string; replyTo?: Message | null; onclearReply?: () => void } = $props();

  let text = $state('');
  let mode = $state<SendMode>('queue');
  let busy = $state(false);
  let error = $state('');
  // The failed attempt's client id; '' while none, or after an edit, which
  // starts a new attempt.
  let attempt = $state('');

  const room = $derived(store.rooms[id]);
  const working = $derived(room?.agents.filter((a) => a.working) ?? []);
  // The server refuses a steer when no turn runs, so the choice only appears
  // while an agent is working and the room is neither paused nor halted.
  const steerable = $derived(!!room && !room.paused && !store.halted && working.length > 0);
  const placeholder = $derived(
    working.length === 0
      ? 'Message…'
      : working.length === 1
        ? `Message, or steer ${working[0].display_name}…`
        : 'Message, or steer…',
  );

  function onInput() {
    // An edit is a new message: the failed attempt's id no longer applies.
    attempt = '';
  }

  async function submit() {
    if (busy) return;
    const client = attempt || crypto.randomUUID();
    busy = true;
    error = '';
    try {
      await send(id, text.trim(), mode, client, replyTo?.id ?? null);
      text = '';
      attempt = '';
      error = '';
      onclearReply?.();
    } catch (err) {
      if (err instanceof ApiError) {
        attempt = client;
        error = err.message;
      }
    } finally {
      busy = false;
    }
  }
</script>

<div class="composer">
  {#if replyTo}
    <div class="reply">
      <span class="glyph">↩</span>
      <div class="reply-text">
        <b>{replyTo.author?.name ?? 'removed'}</b> {preview(replyTo.text)}
      </div>
      <button type="button" aria-label="Clear reply" onclick={() => onclearReply?.()}>×</button>
    </div>
  {/if}
  {#if steerable}
    <div class="mode">
      <button type="button" aria-pressed={mode === 'queue'} onclick={() => (mode = 'queue')}>Queue</button>
      <button type="button" aria-pressed={mode === 'steer'} onclick={() => (mode = 'steer')}>Steer</button>
    </div>
  {/if}
  <div class="bar">
    <textarea
      aria-label="Message"
      {placeholder}
      bind:value={text}
      oninput={onInput}
    ></textarea>
    <button type="button" class="send" disabled={text.trim() === '' || busy} onclick={submit}>
      {attempt === '' ? 'Send' : 'Retry'}
    </button>
  </div>
  {#if error !== ''}
    <p class="error" role="alert">{error}</p>
  {/if}
</div>

<style>
  .composer {
    border-top: 1px solid var(--line);
    background: var(--paper);
  }

  .reply {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px 0;
    font-size: 12.5px;
    color: var(--ink2);
  }

  .reply .glyph {
    flex: none;
  }

  .reply-text {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .reply b {
    font-weight: 700;
    color: var(--ink);
  }

  .reply button {
    flex: none;
    border: none;
    background: none;
    color: var(--ink2);
    font-size: 16px;
    line-height: 1;
    cursor: pointer;
  }

  .mode {
    display: flex;
    gap: 6px;
    padding: 8px 12px 0;
  }

  .mode button {
    padding: 3px 12px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--paper);
    color: var(--ink2);
    font-size: 12.5px;
    font-weight: 600;
  }

  .mode button[aria-pressed='true'] {
    background: var(--aub);
    border-color: var(--aub);
    color: var(--on-aub);
  }

  .bar {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 9px 12px;
  }

  textarea {
    flex: 1;
    resize: vertical;
    min-height: 2.5rem;
    max-height: 12rem;
    border: 1px solid var(--line);
    border-radius: 9px;
    padding: 8px 11px;
    font-size: 14.5px;
    background: var(--paper);
    color: var(--ink);
  }

  textarea::placeholder {
    color: var(--ink2);
  }

  .send {
    flex: none;
    border: 1px solid var(--aub);
    border-radius: 9px;
    padding: 8px 12px;
    background: var(--aub);
    color: var(--on-aub);
    font-size: 13.5px;
    font-weight: 650;
  }

  .send:disabled {
    border-color: var(--aub-lo);
    background: var(--aub-lo);
    color: var(--on-aub2);
  }

  .error {
    margin: 0;
    padding: 0 12px 8px;
    color: var(--err);
    font-size: 12.5px;
  }
</style>

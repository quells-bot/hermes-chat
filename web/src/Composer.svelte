<script lang="ts">
  import { tick } from 'svelte';
  import { ApiError } from './lib/api';
  import { preview } from './lib/format';
  import { getDraft, setDraft } from './lib/drafts';
  import { candidates, complete, mentionAt } from './lib/mention';
  import { reason, send, store, type SendMode } from './lib/store.svelte';
  import type { Message } from './lib/types';
  import Icon from './Icon.svelte';

  let {
    id,
    replyTo = null,
    onclearReply,
    onsent,
  }: { id: string; replyTo?: Message | null; onclearReply?: () => void; onsent?: () => void } = $props();

  let input: HTMLTextAreaElement | undefined = $state();

  // svelte-ignore state_referenced_locally
  let text = $state(getDraft(id));
  let mode = $state<SendMode>('queue');
  let busy = $state(false);
  let error = $state('');
  // The failed attempt's client id; '' while none, or after an edit, which
  // starts a new attempt.
  let attempt = $state('');

  // The textarea's caret, kept from its events; completion reads it.
  let caret = $state(0);
  // The start of a mention whose list was put away with Escape, until that
  // token stops being a mention.
  let away = $state(-1);
  let chosen = $state(0);

  const room = $derived(store.rooms[id]);
  const at = $derived(room?.flavor === 'channel' ? mentionAt(text, caret) : null);
  // A string, so that the choice starts over on a new query and not whenever
  // the room is replaced by an equal one.
  const query = $derived(at?.query ?? null);
  const options = $derived(at && at.start !== away ? candidates(room.agents, at.query) : []);
  // The options can shrink under the choice; it never reads past the end.
  const pick = $derived(Math.min(chosen, options.length - 1));
  const working = $derived(room?.agents.filter((a) => a.working) ?? []);
  // The server refuses a steer when no turn runs, so the choice only appears
  // while an agent is working and the room is neither paused nor halted.
  const steerable = $derived(!!room && !room.paused && !store.halted && working.length > 0);
  // A choice of Steer lasts only as long as there is a turn to steer: once
  // the choice is gone, the composer posts.
  const sending = $derived<SendMode>(steerable ? mode : 'queue');
  // The steer route takes no reply, so the quote is shown, sent and cleared
  // only with a post; it is kept through a steer for the post after it.
  const quoted = $derived(sending === 'queue' ? replyTo : null);
  const placeholder = $derived(
    working.length === 0
      ? 'Message…'
      : working.length === 1
        ? `Message, or steer ${working[0].display_name}…`
        : 'Message, or steer…',
  );

  // Choosing a message to reply to is the start of writing the reply.
  $effect(() => {
    if (replyTo) {
      input?.focus();
    }
  });

  $effect(() => {
    if (!at) away = -1;
  });

  // A new query starts the choice over.
  $effect(() => {
    void query;
    chosen = 0;
  });

  // Whatever is typed is the room's draft until it is sent.
  $effect(() => {
    setDraft(id, text);
  });

  function track() {
    caret = input?.selectionStart ?? 0;
  }

  function onInput() {
    // An edit is a new message: the failed attempt's id no longer applies.
    attempt = '';
    track();
  }

  async function insert(handle: string) {
    if (!at) return;
    const done = complete(text, at, caret, handle);
    text = done.text;
    // Completing is an edit, like typing.
    attempt = '';
    input?.focus();
    await tick();
    input?.setSelectionRange(done.caret, done.caret);
    // Only now does the box hold the new text; focus tracked the old caret.
    caret = done.caret;
  }

  // While the mention list is open it takes the arrows, Enter, Tab and Escape.
  // Then Ctrl or ⌘ with Enter sends, when the Send button would be enabled;
  // plain Enter stays a newline.
  function onKeydown(e: KeyboardEvent) {
    if (options.length > 0 && at && !e.isComposing) {
      // Alt with an arrow is the room key, which wins.
      if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !e.altKey) {
        e.preventDefault();
        const step = e.key === 'ArrowDown' ? 1 : -1;
        chosen = (pick + step + options.length) % options.length;
        return;
      }
      if ((e.key === 'Enter' && !e.ctrlKey && !e.metaKey) || e.key === 'Tab') {
        e.preventDefault();
        void insert(options[pick].handle);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        away = at.start;
        return;
      }
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.isComposing) {
      e.preventDefault();
      if (text.trim() !== '' && !busy) void submit();
    }
  }

  async function submit() {
    if (busy) return;
    const client = attempt || crypto.randomUUID();
    const reply = quoted;
    busy = true;
    error = '';
    try {
      await send(id, text.trim(), sending, client, reply?.id ?? null);
      text = '';
      attempt = '';
      error = '';
      if (reply) {
        onclearReply?.();
      }
      onsent?.();
    } catch (err) {
      if (err instanceof ApiError) {
        attempt = client;
        error = reason(err);
      }
    } finally {
      busy = false;
    }
  }
</script>

<div class="composer">
  {#if quoted}
    <div class="reply">
      <span class="glyph">↩</span>
      <div class="reply-text">
        <b>{quoted.author?.name ?? 'removed'}</b> {preview(quoted.text)}
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
  {#if options.length > 0}
    <div class="mention" role="listbox" aria-label="Mention">
      {#each options as a, i (a.id)}
        <button
          type="button"
          role="option"
          aria-selected={i === pick}
          onclick={() => void insert(a.handle)}
        >
          <span class="icon"><Icon name={a.handle} /></span>
          <span class="name">{a.display_name}</span>
          <span class="handle">@{a.handle}</span>
        </button>
      {/each}
    </div>
  {/if}
  <div class="bar">
    <textarea
      aria-label="Message"
      {placeholder}
      bind:value={text}
      bind:this={input}
      readonly={busy}
      oninput={onInput}
      onkeyup={track}
      onclick={track}
      onfocus={track}
      onkeydown={onKeydown}
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

  /* The padding is the tap target; the margins take it back out of the row. */
  .reply button {
    flex: none;
    margin: -6px -10px -6px 0;
    padding: 8px 12px;
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

  .mention {
    display: flex;
    flex-direction: column;
    max-height: 12rem;
    overflow-y: auto;
    border-bottom: 1px solid var(--line);
    background: var(--paper);
  }

  .mention button {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 7px 12px;
    border: none;
    background: none;
    color: var(--ink);
    font-size: 14px;
    text-align: left;
    cursor: pointer;
  }

  .mention button[aria-selected='true'] {
    background: var(--paper2);
  }

  .mention .icon {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 5px;
    overflow: hidden;
  }

  .mention .name {
    font-weight: 650;
  }

  .mention .handle {
    color: var(--ink2);
    font-size: 12.5px;
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

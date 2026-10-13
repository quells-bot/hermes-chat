<script lang="ts">
  import { ApiError } from './lib/api';
  import { answer } from './lib/store.svelte';
  import type { Message } from './lib/types';
  import Markdown from './Markdown.svelte';

  let { message }: { message: Message } = $props();

  const prompt = $derived(message.prompt);

  let text = $state('');
  let pending = $state(false);
  let error = $state('');

  const trimmed = $derived(text.trim());

  async function send(value: string) {
    if (pending || value.trim() === '') return;
    pending = true;
    error = '';
    try {
      await answer(message.room_id, message.id, value.trim());
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Answer failed.';
    } finally {
      pending = false;
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      void send(trimmed);
    }
  }
</script>

{#if prompt}
  <div class="card">
    <div class="from">{message.author?.name ?? 'removed'} asks</div>
    <div class="q"><Markdown text={message.text} /></div>
    {#if prompt.state === 'answered'}
      <div class="result">
        Answered: {prompt.answer}{prompt.answered_by ? ` by ${prompt.answered_by.username}` : ''}
      </div>
    {:else if prompt.state === 'expired'}
      <div class="result">Expired</div>
    {/if}
    {#if prompt.choices.length > 0}
      <div class="choices">
        {#each prompt.choices as choice}
          <button
            type="button"
            class="choice"
            disabled={pending || prompt.state !== 'open'}
            onclick={() => void send(choice)}
          >
            {choice}
          </button>
        {/each}
      </div>
    {/if}
    {#if prompt.state === 'open' && prompt.kind === 'clarify'}
      <div class="input">
        <textarea rows={1} placeholder="Your answer" bind:value={text} onkeydown={onkeydown}></textarea>
        <button type="button" class="answer" disabled={pending || trimmed === ''} onclick={() => void send(trimmed)}>
          Answer
        </button>
      </div>
    {/if}
    {#if error}
      <div class="err" role="alert">{error}</div>
    {/if}
  </div>
{/if}

<style>
  .card {
    border: 1px solid var(--ask);
    background: var(--ask-bg);
    border-radius: 10px;
    padding: 10px 11px;
    font-size: 14px;
  }

  .from {
    font-size: 12px;
    color: var(--ask-ink);
  }

  .q {
    font-weight: 650;
    color: var(--ask-ink);
  }

  .result,
  .choices,
  .input,
  .err {
    margin-top: 6px;
  }

  .result,
  .err {
    font-size: 13px;
    color: var(--ask-ink);
  }

  .choice,
  .answer {
    border: 1px solid var(--edge);
    border-radius: 999px;
    padding: 3px 14px;
    background: var(--paper);
    color: var(--ink2);
    font-size: 12.5px;
    font-weight: 600;
    cursor: pointer;
  }

  .choice + .choice {
    margin-left: 6px;
  }

  .choice:disabled,
  .answer:disabled {
    opacity: 0.55;
    cursor: default;
  }

  .input {
    display: flex;
    gap: 6px;
    align-items: flex-start;
  }

  .input textarea {
    flex: 1;
    resize: none;
    border: 1px solid var(--edge);
    border-radius: 8px;
    padding: 5px 8px;
    background: var(--paper);
    color: var(--ink);
    font-size: 13px;
    font-family: inherit;
  }

  .answer {
    flex: none;
    background: var(--btn);
    color: var(--on-btn);
  }

  .err {
    color: var(--err);
  }
</style>

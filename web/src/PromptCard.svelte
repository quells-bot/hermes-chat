<script lang="ts">
  import type { Message } from './lib/types';
  import Markdown from './Markdown.svelte';

  let { message }: { message: Message } = $props();

  const prompt = $derived(message.prompt);
</script>

{#if prompt}
  <div class="card">
    <div class="from">{message.author?.name ?? 'removed'} asks</div>
    <div class="q"><Markdown text={message.text} /></div>
    {#if prompt.state === 'open'}
      {#if prompt.choices.length > 0}
        <div class="choices">{prompt.choices.join(' · ')}</div>
      {/if}
    {:else if prompt.state === 'answered'}
      <div class="result">
        Answered: {prompt.answer}{prompt.answered_by ? ` by ${prompt.answered_by.username}` : ''}
      </div>
    {:else}
      <div class="result">Expired</div>
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

  .choices,
  .result {
    margin-top: 6px;
    font-size: 13px;
    color: var(--ask-ink);
  }
</style>

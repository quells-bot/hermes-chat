<script lang="ts">
  import ActivityBlock from './ActivityBlock.svelte';
  import Icon from './Icon.svelte';
  import { attachmentUrl } from './lib/api';
  import { clock, iconName, preview, size } from './lib/format';
  import { session } from './lib/session.svelte';
  import { cancel, loadQuoted, store } from './lib/store.svelte';
  import type { Message } from './lib/types';
  import Markdown from './Markdown.svelte';
  import PromptCard from './PromptCard.svelte';

  let { message, onreply }: { message: Message; onreply?: () => void } = $props();

  const quoted = $derived.by(() => {
    const id = message.reply_to_id;
    if (id === null) return undefined;
    return store.feeds[message.room_id]?.messages.find((m) => m.id === id) ?? store.quoted[id];
  });

  $effect(() => {
    if (message.reply_to_id !== null && quoted === undefined) {
      void loadQuoted(message.room_id, message.reply_to_id);
    }
  });

  const stated = $derived(
    message.deliveries.filter((d) => d.state === 'waiting' || d.state === 'delivered' || d.state === 'cancelled'),
  );

  // A copy that was worked on shows what the agent did, not a line.
  const turns = $derived(
    message.deliveries.filter((d) => d.state === 'working' || d.state === 'done' || d.state === 'failed'),
  );

  // A Cancel is offered on the person's own message while any copy is waiting.
  const cancellable = $derived(
    message.author?.kind === 'human' &&
      message.author.id === session.me?.id &&
      message.deliveries.some((d) => d.state === 'waiting'),
  );

  let cancelling = $state(false);

  async function cancelDelivery() {
    if (cancelling) return;
    cancelling = true;
    try {
      await cancel(message.room_id, message.id);
    } finally {
      cancelling = false;
    }
  }
</script>

<article data-mid={message.id}>
  <span class="icon"><Icon name={iconName(message.author, store.agents)} /></span>
  <div class="main">
    <div class="who">
      {message.author?.name ?? 'removed'}
      <time datetime={message.created_at}>{clock(message.created_at, new Date())}</time>
      {#if onreply}
        <button type="button" class="reply" aria-label="Reply" onclick={onreply}>↩</button>
      {/if}
    </div>
    {#if message.reply_to_id !== null}
      <div class="quote">
        {#if quoted}
          <b>{quoted.author?.name ?? 'removed'}</b> {preview(quoted.text)}
        {:else}
          reply
        {/if}
      </div>
    {/if}
    <div class="body">
      {#if message.prompt}
        <PromptCard {message} />
      {:else if message.text.trim() !== ''}
        <Markdown text={message.text} />
      {/if}
      {#if message.streaming}
        <span class="writing" aria-label="writing">…</span>
      {/if}
    </div>
    {#each message.attachments as att (att.id)}
      <div class="attachment">
        {#if att.image}
          <a href={attachmentUrl(message.room_id, att.id)} target="_blank" rel="noopener noreferrer">
            <img src={attachmentUrl(message.room_id, att.id)} alt={att.name} loading="lazy" />
          </a>
        {:else}
          <a href={attachmentUrl(message.room_id, att.id)} download={att.name}>{att.name}</a>
          <span class="size">{size(att.size)}</span>
        {/if}
      </div>
    {/each}
    {#each stated as d (d.agent.id)}
      <div class="state">{d.agent.display_name} · {d.state}</div>
    {/each}
    {#if cancellable}
      <button type="button" class="cancel" disabled={cancelling} onclick={cancelDelivery}>Cancel</button>
    {/if}
    {#each turns as d (d.agent.id)}
      <ActivityBlock {message} delivery={d} />
    {/each}
  </div>
</article>

<style>
  article {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    padding: 6px 14px;
  }

  .icon {
    flex: none;
    width: 34px;
    height: 34px;
    border-radius: 7px;
    overflow: hidden;
  }

  .main {
    flex: 1;
    min-width: 0;
  }

  .who {
    font-size: 13px;
    font-weight: 700;
  }

  time {
    margin-left: 6px;
    font-size: 12px;
    font-weight: 400;
    color: var(--ink2);
  }

  .quote {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border-left: 2px solid var(--line);
    padding-left: 8px;
    font-size: 12.5px;
    color: var(--ink2);
  }

  .quote b {
    font-weight: 700;
  }

  .body {
    font-size: 14.5px;
    overflow-wrap: anywhere;
  }

  .writing {
    color: var(--ink2);
  }

  .attachment {
    margin-top: 4px;
    font-size: 13.5px;
  }

  .attachment img {
    display: block;
    max-width: 100%;
    max-height: 20rem;
  }

  .size {
    margin-left: 6px;
    font-size: 12px;
    color: var(--ink2);
  }

  .state {
    font-size: 12px;
    color: var(--ink2);
  }

  .reply {
    margin-left: 6px;
    border: none;
    background: none;
    color: var(--ink2);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
  }

  .cancel {
    margin-top: 4px;
    border: 1px solid var(--line);
    border-radius: 999px;
    padding: 2px 12px;
    background: var(--paper);
    color: var(--ink2);
    font-size: 12px;
    font-weight: 600;
  }

  .cancel:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>

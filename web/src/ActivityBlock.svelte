<script lang="ts">
  import { duration } from './lib/format';
  import { loadActivity, store } from './lib/store.svelte';
  import type { Activity, Delivery, Message } from './lib/types';

  // One agent's turn on one message: what it did while working on it.
  let { message, delivery }: { message: Message; delivery: Delivery } = $props();

  let open = $state(false);
  // The tools whose arguments are shown, by activity id.
  let shown: Record<string, boolean> = $state({});

  // A message's list holds every agent's activity. Undefined until loaded.
  const steps = $derived(store.activity[message.id]?.filter((a) => a.agent.id === delivery.agent.id));
  const working = $derived(delivery.state === 'working');
  const tools = $derived(steps?.filter((a) => a.kind === 'tool').length);
  const now = $derived(working ? steps?.at(-1) : undefined);

  // An open block wants its list: when it is opened without one, and when a
  // load empties the lists under it. A list that is held is kept current by
  // the stream and is not fetched again. This runs again only when the entry
  // or open changes, so a list that cannot be fetched is not asked for in a
  // loop.
  $effect(() => {
    if (open && steps === undefined) {
      void loadActivity(message.room_id, message.id);
    }
  });

  // Everything below is written by an agent, and is shown as plain text.
  function title(a: Activity): string {
    return a.preview === '' ? a.tool : `${a.tool} · ${a.preview}`;
  }
</script>

{#snippet row(a: Activity)}
  {#if a.succeeded === true}
    <span class="mark ok" aria-label="succeeded">✓</span>
  {:else if a.succeeded === false}
    <span class="mark bad" aria-label="failed">✕</span>
  {:else if working}
    <span class="mark ok" aria-label="running">●</span>
  {:else}
    <span class="mark" aria-label="unfinished">–</span>
  {/if}
  <span class="title">{title(a)}</span>
  {#if a.succeeded !== null && a.duration_ms !== null}
    <span class="time">{duration(a.duration_ms)}</span>
  {:else if a.succeeded === null && working}
    <span class="time">running</span>
  {/if}
{/snippet}

<div class="block">
  <button class="summary" aria-expanded={open} onclick={() => (open = !open)}>
    {#if working}<span class="dot"></span>{/if}
    <span class="title">
      {delivery.agent.display_name} · {delivery.state}{#if tools !== undefined}
        {' '}· {tools} {tools === 1 ? 'step' : 'steps'}{/if}
    </span>
    <span class="chevron" aria-hidden="true">{open ? '▾' : '▸'}</span>
  </button>
  {#if now}
    <div class="now">Now: {now.kind === 'tool' ? title(now) : now.text}</div>
  {/if}
  {#if open && steps !== undefined && steps.length > 0}
    <ol>
      {#each steps as a (a.id)}
        <li>
          {#if a.kind === 'remark'}
            <i>{a.text}</i>
          {:else if a.arguments !== ''}
            <button class="step" aria-expanded={shown[a.id] === true} onclick={() => (shown[a.id] = !shown[a.id])}>
              {@render row(a)}
            </button>
            {#if shown[a.id]}
              <pre>{a.arguments}</pre>
            {/if}
          {:else}
            <div class="step">{@render row(a)}</div>
          {/if}
        </li>
      {/each}
    </ol>
  {/if}
</div>

<style>
  .block {
    margin-top: 6px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--paper2);
    font-size: 13px;
  }

  button {
    width: 100%;
    border: 0;
    border-radius: 10px;
    background: none;
    text-align: left;
    cursor: pointer;
  }

  .summary,
  .step {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }

  .summary {
    padding: 9px 11px;
  }

  .dot {
    flex: none;
    align-self: center;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--ok);
  }

  .title {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .summary .title {
    font-weight: 700;
  }

  .chevron {
    flex: none;
    color: var(--ink2);
  }

  .now {
    padding: 0 11px 9px;
    font-size: 12.5px;
    color: var(--ink2);
    overflow-wrap: anywhere;
  }

  ol {
    margin: 0;
    padding: 0;
    list-style: none;
    border-top: 1px solid var(--line);
    font-size: 12.5px;
  }

  li + li {
    border-top: 1px solid var(--line);
  }

  .step,
  i {
    padding: 7px 11px;
  }

  i {
    display: block;
    overflow-wrap: anywhere;
  }

  .mark {
    flex: none;
    color: var(--ink2);
  }

  .ok {
    color: var(--ok);
  }

  .bad {
    color: var(--err);
  }

  .time {
    flex: none;
    color: var(--ink2);
    font-variant-numeric: tabular-nums;
  }

  pre {
    margin: 0 11px 9px;
    border-radius: 8px;
    padding: 7px 9px;
    background: var(--paper);
    font-family: ui-monospace, Menlo, monospace;
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>

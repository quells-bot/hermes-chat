<script lang="ts">
  import { resumeAll, stopAll, store } from './lib/store.svelte';

  // One tap, no confirmation. The button stays disabled until the request
  // settles, so a second tap cannot send a second one. A failure shows as
  // store.error, which Home displays.
  let busy = $state(false);

  async function run(action: () => Promise<void>) {
    if (busy) return;
    busy = true;
    try {
      await action();
    } finally {
      busy = false;
    }
  }
</script>

{#if store.halted}
  <span class="stopped">Stopped</span>
  <button type="button" disabled={busy} onclick={() => run(resumeAll)}>Resume all</button>
{:else}
  <button type="button" class="stop" disabled={busy} onclick={() => run(stopAll)}>Stop all</button>
{/if}

<style>
  .stopped {
    color: var(--err);
    font-size: 0.8125rem;
    font-weight: 600;
  }

  /* Outranks Header's `header :global(button)` rule. */
  button.stop {
    border-color: var(--err);
    background: transparent;
    color: var(--err);
  }
</style>

<script lang="ts">
  import { pause, resume, store } from './lib/store.svelte';

  // The room's one item of menu in the header. The ⋯ opens a dropdown with a
  // single Pause or Resume, whichever the room wants. One tap, no
  // confirmation: the item stays disabled until the request settles, then
  // the menu closes. A failure shows as store.error, which Room displays.
  let { id }: { id: string } = $props();

  let open = $state(false);
  let busy = $state(false);

  const paused = $derived(store.rooms[id]?.paused ?? false);

  async function run(): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      if (paused) {
        await resume(id);
      } else {
        await pause(id);
      }
      open = false;
    } finally {
      busy = false;
    }
  }
</script>

<div class="menu">
  <button type="button" aria-label="Menu" aria-expanded={open} onclick={() => (open = !open)}>⋯</button>
  {#if open}
    <div class="dropdown">
      <button type="button" disabled={busy} onclick={run}>{paused ? 'Resume' : 'Pause'}</button>
    </div>
  {/if}
</div>

<style>
  .menu {
    position: relative;
  }

  /* The dropdown sits over the paper, on the line, under the ⋯. */
  .dropdown {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 10;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
    padding: 2px;
  }

  .dropdown button {
    display: block;
    width: 100%;
    white-space: nowrap;
    text-align: left;
  }
</style>
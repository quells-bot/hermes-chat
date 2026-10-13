<script lang="ts">
  import { pause, reason, resume, store } from './lib/store.svelte';

  // The room's one item of menu in the header. The ⋯ opens a dropdown with a
  // single Pause or Resume, whichever the room wants. One tap, no
  // confirmation: the item stays disabled until the request settles, then
  // the menu closes. A failure is handed to onerror, for Room to show.
  let { id, onerror }: { id: string; onerror?: (why: string) => void } = $props();

  let open = $state(false);
  let busy = $state(false);
  let root: HTMLElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();
  let item: HTMLButtonElement | undefined = $state();

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
      onerror?.('');
    } catch (err) {
      onerror?.(reason(err));
    } finally {
      busy = false;
      close();
    }
  }

  // close shuts the menu and hands the focus back to the ⋯ that opened it.
  function close(): void {
    open = false;
    trigger?.focus();
  }

  function onkeydown(e: KeyboardEvent): void {
    if (open && e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }

  // An open menu takes the focus, and a tap anywhere else shuts it.
  $effect(() => {
    if (!open) {
      return;
    }
    item?.focus();
    const outside = (e: MouseEvent): void => {
      if (root !== undefined && e.target instanceof Node && !root.contains(e.target)) {
        open = false;
      }
    };
    document.addEventListener('click', outside);
    return () => document.removeEventListener('click', outside);
  });
</script>

<div class="menu" bind:this={root}>
  <button
    type="button"
    aria-label="Menu"
    aria-haspopup="menu"
    aria-expanded={open}
    bind:this={trigger}
    onclick={() => (open = !open)}
    {onkeydown}
  >⋯</button>
  {#if open}
    <div class="dropdown" role="menu">
      <button type="button" role="menuitem" disabled={busy} bind:this={item} onclick={run} {onkeydown}>
        {paused ? 'Resume' : 'Pause'}
      </button>
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
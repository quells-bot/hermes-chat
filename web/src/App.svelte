<script lang="ts">
  import { onMount } from 'svelte';
  import Header from './Header.svelte';
  import Home from './Home.svelte';
  import Login from './Login.svelte';
  import { session, start } from './lib/session.svelte';

  onMount(() => {
    start();
  });
</script>

{#if session.status === 'in'}
  <Home />
{:else if session.status === 'out'}
  <Login />
{:else}
  <Header />
  {#if session.status === 'error'}
    <main>
      <p role="alert">{session.error}</p>
      <button type="button" onclick={start}>Retry</button>
    </main>
  {/if}
{/if}

<style>
  main {
    display: grid;
    gap: 0.75rem;
    justify-items: start;
    padding: 1rem 0.75rem;
  }

  p {
    margin: 0;
    color: var(--err);
  }

  button {
    padding: 0.4rem 0.75rem;
    border: 1px solid var(--line);
    border-radius: 0.25rem;
    background: var(--paper);
  }
</style>

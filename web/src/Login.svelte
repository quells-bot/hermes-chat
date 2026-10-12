<script lang="ts">
  import Header from './Header.svelte';
  import { ApiError } from './lib/api';
  import { signIn } from './lib/session.svelte';

  let username = $state('');
  let password = $state('');
  let pending = $state(false);
  let error = $state('');

  function describe(err: unknown): string {
    if (err instanceof ApiError && err.retryAfter !== null) {
      return `${err.message} Retry in ${err.retryAfter} s.`;
    }
    return err instanceof Error ? err.message : String(err);
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (pending) return;
    pending = true;
    error = '';
    try {
      await signIn(username, password);
    } catch (err) {
      error = describe(err);
      password = '';
    } finally {
      pending = false;
    }
  }
</script>

<Header />

<main>
  <form onsubmit={submit}>
    <label>
      Username
      <input
        type="text"
        name="username"
        autocomplete="username"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        required
        bind:value={username}
      />
    </label>
    <label>
      Password
      <input type="password" name="password" autocomplete="current-password" required bind:value={password} />
    </label>
    {#if error}
      <p role="alert">{error}</p>
    {/if}
    <button type="submit" disabled={pending}>Log in</button>
  </form>
</main>

<style>
  main {
    max-width: 26rem;
    margin: 0 auto;
    padding: 1rem 0.75rem;
  }

  form {
    display: grid;
    gap: 0.75rem;
    padding: 1rem;
    border: 1px solid var(--line);
    border-radius: 0.375rem;
    background: var(--paper);
  }

  label {
    display: grid;
    gap: 0.25rem;
    font-size: 0.875rem;
    font-weight: 600;
    color: var(--ink);
  }

  input {
    padding: 0.5rem;
    border: 1px solid var(--edge);
    border-radius: 0.25rem;
    background: var(--paper);
    color: var(--ink);
    font-weight: 400;
  }

  p {
    margin: 0;
    color: var(--err);
    font-size: 0.875rem;
  }

  button {
    padding: 0.5rem;
    border: 0;
    border-radius: 0.25rem;
    background: var(--btn);
    color: var(--on-btn);
    font-weight: 600;
  }

  button:disabled {
    opacity: 0.5;
  }
</style>

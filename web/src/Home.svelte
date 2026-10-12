<script lang="ts">
  import Header from './Header.svelte';
  import { session, signOut } from './lib/session.svelte';
</script>

<Header>
  <button type="button" onclick={signOut}>Log out</button>
</Header>

<main>
  <p>{session.me?.username}</p>
</main>

<style>
  main {
    padding: 1rem 0.75rem;
  }

  p {
    margin: 0;
  }
</style>

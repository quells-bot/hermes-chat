<script lang="ts">
  import { iconFor } from './lib/icon';

  let { name }: { name: string } = $props();

  const icon = $derived(iconFor(name));
</script>

<svg viewBox="0 0 7 7" shape-rendering="crispEdges" aria-hidden="true">
  <rect width="7" height="7" fill="#fff" />
  <rect width="7" height="7" fill={icon.color} opacity="0.15" />
  {#each icon.cells as [x, y] (`${x},${y}`)}
    <rect x={x + 1} y={y + 1} width="1" height="1" fill={icon.color} />
  {/each}
</svg>

<style>
  svg {
    display: block;
    width: 100%;
    height: 100%;
  }
</style>

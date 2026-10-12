<script lang="ts">
  import Icon from './Icon.svelte';
  import { isUnread, isWorking, roomName } from './lib/rooms';
  import type { Room } from './lib/types';

  let { room }: { room: Room } = $props();

  const unread = $derived(isUnread(room));
  const agent = $derived(room.flavor === 'dm' ? room.agents[0] : undefined);
</script>

<li class:unread>
  {#if room.flavor === 'dm'}
    <span class="avatar">
      <span class="icon"><Icon name={agent?.handle ?? ''} /></span>
      <i
        class="presence"
        class:on={agent?.connected}
        aria-label={agent?.connected ? 'connected' : 'offline'}
      ></i>
    </span>
  {:else}
    <span class="hash">#</span>
  {/if}
  <span class="name">{roomName(room)}</span>
  {#if room.open_prompts > 0}
    <span class="tag ask" aria-label="{room.open_prompts} open prompts">{room.open_prompts}</span>
  {/if}
  {#if isWorking(room)}
    <span class="tag work">working</span>
  {/if}
  {#if room.paused}
    <span class="tag pause">paused</span>
  {/if}
  {#if unread}
    <i class="dot" aria-label="unread"></i>
  {/if}
</li>

<style>
  li {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 14px;
    list-style: none;
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 14.5px;
    color: var(--on-aub2);
  }

  .unread .name {
    color: var(--on-aub);
    font-weight: 700;
  }

  .hash {
    flex: none;
    width: 22px;
    text-align: center;
    font-size: 13px;
    color: var(--on-aub2);
  }

  .avatar {
    position: relative;
    flex: none;
    line-height: 0;
  }

  .icon {
    display: inline-block;
    width: 22px;
    height: 22px;
    border-radius: 5px;
    overflow: hidden;
  }

  .presence {
    position: absolute;
    right: -4px;
    bottom: -3px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    border: 1.5px solid var(--on-aub2);
    background: var(--aub);
    box-shadow: 0 0 0 2px var(--aub);
  }

  .presence.on {
    background: var(--ok);
    border-color: var(--ok);
  }

  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--unread);
  }

  .tag {
    flex: none;
    padding: 1px 6px;
    border-radius: 5px;
    font-size: 11px;
    font-weight: 650;
  }

  .ask {
    background: var(--ask);
    color: var(--on-ask);
  }

  .work {
    color: var(--ok);
    border: 1px solid var(--ok);
  }

  .pause {
    color: var(--on-aub2);
    border: 1px solid var(--aub-line);
  }
</style>

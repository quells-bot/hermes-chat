// A Svelte action that says when a node enters and leaves the viewport: the
// sentinels at the edges of a feed. The only use of IntersectionObserver.

export function whenSeen(node: Element, report: (seen: boolean) => void): { destroy(): void } {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      report(entry.isIntersecting);
    }
  });
  observer.observe(node);
  return { destroy: () => observer.disconnect() };
}

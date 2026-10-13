// Whether the screen is wide enough for the room list to sit beside the open
// screen; the one threshold is a 20rem list and at least 36rem of pane.
export const wideQuery = '(min-width: 56rem)';

export const layout: { wide: boolean } = $state({ wide: false });

// startLayout sets layout.wide from matchMedia(wideQuery) now and on every
// change. It returns the stop function, which also sets wide back to false.
// Where matchMedia does not exist (jsdom) it leaves wide as it is.
export function startLayout(): () => void {
  if (typeof matchMedia !== 'function') {
    return () => {
      layout.wide = false;
    };
  }
  const mq = matchMedia(wideQuery);
  const follow = (event: { matches: boolean }) => {
    layout.wide = event.matches;
  };
  follow(mq);
  mq.addEventListener('change', follow);
  return () => {
    mq.removeEventListener('change', follow);
    layout.wide = false;
  };
}

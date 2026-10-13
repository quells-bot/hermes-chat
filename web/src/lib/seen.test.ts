import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { whenSeen } from './seen';

// What the stubbed IntersectionObserver was built with and asked to do.
type Stub = {
  callback: (entries: { isIntersecting: boolean }[]) => void;
  observed: Element[];
  disconnected: number;
};

let observers: Stub[];

beforeEach(() => {
  observers = [];
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      stub: Stub;
      constructor(callback: Stub['callback']) {
        this.stub = { callback, observed: [], disconnected: 0 };
        observers.push(this.stub);
      }
      observe(node: Element): void {
        this.stub.observed.push(node);
      }
      disconnect(): void {
        this.stub.disconnected += 1;
      }
    },
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

test('reports entering and leaving', () => {
  const node = document.createElement('div');
  const report = vi.fn();

  whenSeen(node, report);

  expect(observers).toHaveLength(1);
  expect(observers[0].observed).toEqual([node]);
  expect(report).not.toHaveBeenCalled();

  observers[0].callback([{ isIntersecting: true }]);
  expect(report).toHaveBeenLastCalledWith(true);

  observers[0].callback([{ isIntersecting: false }]);
  expect(report).toHaveBeenLastCalledWith(false);
  expect(report).toHaveBeenCalledTimes(2);
});

test('destroy disconnects the observer', () => {
  const action = whenSeen(document.createElement('div'), vi.fn());
  expect(observers[0].disconnected).toBe(0);

  action.destroy();

  expect(observers[0].disconnected).toBe(1);
});

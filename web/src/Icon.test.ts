import { render } from '@testing-library/svelte';
import { expect, test } from 'vitest';
import Icon from './Icon.svelte';

test('renders one rect per cell plus two backgrounds', () => {
  const { container } = render(Icon, { name: 'forge' });

  expect(container.querySelectorAll('rect')).toHaveLength(12);
});

test('has no style attribute anywhere', () => {
  const { container } = render(Icon, { name: 'forge' });

  expect(container.querySelector('[style]')).toBeNull();
});

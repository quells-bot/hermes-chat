import { render, screen } from '@testing-library/svelte';
import { beforeEach, expect, test } from 'vitest';
import { closeSearch, openSearch, search } from './lib/search.svelte';
import Search from './Search.svelte';

beforeEach(() => closeSearch());

test('the field holds the query and has the focus on mount', () => {
  openSearch();
  search.query = 'trip';
  render(Search);
  expect(screen.getByRole('searchbox', { name: 'Search' })).toHaveValue('trip');
  expect(screen.getByRole('searchbox')).toHaveFocus();
});

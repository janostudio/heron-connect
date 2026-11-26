import { describe, expect, it } from 'vitest';
import { newClientID } from './webClientId';

describe('newClientID', () => {
  // A duplicated tab copies sessionStorage, so a stored id would make two tabs
  // share one connection slot. Minting per page load is what keeps them
  // independent; this asserts the generator never returns the same value.
  it('mints a distinct id on every call', () => {
    const ids = new Set(Array.from({ length: 50 }, () => newClientID()));
    expect(ids.size).toBe(50);
  });

  it('returns a non-empty string', () => {
    const id = newClientID();
    expect(typeof id).toBe('string');
    expect(id.length).toBeGreaterThan(8);
  });
});

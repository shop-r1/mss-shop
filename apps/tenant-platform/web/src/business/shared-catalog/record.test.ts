import { describe, expect, it } from 'vitest';
import {
  isValidJSONText,
  recordID,
  toFormValues,
  toMutationPayload,
  visibleColumns,
  writableColumns,
} from './record';
import type { SharedCatalogColumn } from './types';

const columns: SharedCatalogColumn[] = [
  {
    name: 'id',
    label: 'legacy.fields.id',
    type: 'string',
    writable: false,
    secret: false,
    required: true,
  },
  {
    name: 'name',
    label: 'legacy.fields.name',
    type: 'string',
    writable: true,
    secret: false,
    required: true,
  },
  {
    name: 'token',
    label: 'legacy.fields.token',
    type: 'secret',
    writable: true,
    secret: true,
    required: true,
  },
  {
    name: 'attributes',
    label: 'legacy.fields.attributes',
    type: 'json',
    writable: true,
    secret: false,
    required: false,
  },
];

describe('shared catalog record transforms', () => {
  it('hides secrets from list/detail projections but keeps them writable', () => {
    expect(visibleColumns(columns).map((column) => column.name)).toEqual([
      'id',
      'name',
      'attributes',
    ]);
    expect(writableColumns(columns).map((column) => column.name)).toEqual([
      'name',
      'token',
      'attributes',
    ]);
  });

  it('never prefills secrets and serializes JSON for a text area', () => {
    expect(
      toFormValues(columns, {
        id: 'one',
        name: 'Tea',
        token: 'must-not-render',
        attributes: { organic: true },
      }),
    ).toEqual({
      name: 'Tea',
      token: undefined,
      attributes: '{\n  "organic": true\n}',
    });
  });

  it('omits blank secrets/read-only fields and parses JSON writes', () => {
    expect(
      toMutationPayload(columns, {
        id: 'ignored',
        name: 'Tea',
        token: '   ',
        attributes: '{"organic":true}',
      }),
    ).toEqual({ name: 'Tea', attributes: { organic: true } });
  });

  it('validates JSON and resolves record IDs', () => {
    expect(isValidJSONText('{"ok":true}')).toBe(true);
    expect(isValidJSONText('invalid')).toBe(false);
    expect(() => toMutationPayload(columns, { attributes: 'invalid' })).toThrow();
    expect(recordID({ id: 7 })).toBe('7');
    expect(recordID({ name: 'no id' })).toBeUndefined();
  });
});

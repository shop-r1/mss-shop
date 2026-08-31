import { describe, expect, it } from 'vitest';
import {
  isValidJSONText,
  legacyRecordID,
  toLegacyFormValues,
  toLegacyMutationPayload,
  visibleLegacyColumns,
  writableLegacyColumns,
} from './record';
import type { LegacyResourceColumn } from './types';

const columns: LegacyResourceColumn[] = [
  {
    name: 'id',
    label: 'ID',
    type: 'integer',
    writable: false,
    secret: false,
    required: true,
  },
  {
    name: 'name',
    label: 'Name',
    type: 'string',
    writable: true,
    secret: false,
    required: true,
  },
  {
    name: 'credentials',
    label: 'Credentials',
    type: 'string',
    writable: true,
    secret: true,
    required: true,
  },
  {
    name: 'metadata',
    label: 'Metadata',
    type: 'jsonb',
    writable: true,
    secret: false,
    required: false,
  },
];

describe('legacy dynamic record transforms', () => {
  it('never exposes a secret column in lists or details', () => {
    expect(visibleLegacyColumns(columns).map((column) => column.name)).toEqual([
      'id',
      'name',
      'metadata',
    ]);
    expect(writableLegacyColumns(columns).map((column) => column.name)).toContain('credentials');
  });

  it('does not prefill secrets and formats JSON for editing', () => {
    expect(
      toLegacyFormValues(columns, {
        id: 1,
        name: 'Tea',
        credentials: 'must-not-render',
        metadata: { organic: true },
      }),
    ).toEqual({
      name: 'Tea',
      credentials: undefined,
      metadata: '{\n  "organic": true\n}',
    });
  });

  it('omits blank secrets and read-only fields while parsing JSON mutations', () => {
    expect(
      toLegacyMutationPayload(columns, {
        id: 99,
        name: 'Coffee',
        credentials: '',
        metadata: '{"roast":"dark"}',
      }),
    ).toEqual({ name: 'Coffee', metadata: { roast: 'dark' } });

    expect(
      toLegacyMutationPayload(columns, {
        name: 'Coffee',
        credentials: 'replacement-secret',
      }),
    ).toEqual({ name: 'Coffee', credentials: 'replacement-secret' });
  });

  it('validates JSON text and resolves supported record identifiers', () => {
    expect(isValidJSONText('{"valid":true}')).toBe(true);
    expect(isValidJSONText('not-json')).toBe(false);
    expect(() => toLegacyMutationPayload(columns, { metadata: 'not-json' })).toThrow();
    expect(legacyRecordID({ id: 7 })).toBe('7');
    expect(legacyRecordID({ ID: 'legacy-id' })).toBe('legacy-id');
    expect(legacyRecordID({ name: 'no-id' })).toBeUndefined();
  });
});

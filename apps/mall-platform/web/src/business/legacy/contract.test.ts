import { describe, expect, it } from 'vitest';
import { findLegacyResourceByName } from './catalog';
import { parseLegacyRecordResponse, parseLegacyResourceListResponse } from './contract';

function goodsEntry() {
  const entry = findLegacyResourceByName('goods');
  if (!entry) throw new Error('goods must be present in the mall legacy catalog');
  return entry;
}

function listFixture() {
  return {
    data: [{ id: 1, name: 'Tea' }],
    total: 1,
    page: 1,
    pageSize: 20,
    resource: {
      name: 'goods',
      domain: 'catalog',
      titleKey: 'legacy.resource.goods.title',
      columns: [
        {
          name: 'id',
          label: 'ID',
          type: 'INTEGER',
          writable: false,
          secret: false,
          required: true,
        },
        {
          name: 'metadata',
          label: 'Metadata',
          type: 'JSONB',
          writable: true,
          secret: false,
          required: false,
        },
      ],
      capabilities: { detail: true, create: true, update: true, delete: false },
    },
  };
}

describe('legacy resource response contract', () => {
  it('accepts the documented list shape and normalizes column types', () => {
    const parsed = parseLegacyResourceListResponse(listFixture(), goodsEntry());
    expect(parsed.data).toEqual([{ id: 1, name: 'Tea' }]);
    expect(parsed.resource.columns[0]?.type).toBe('integer');
    expect(parsed.resource.columns[1]?.type).toBe('jsonb');
    expect(parsed.resource.capabilities).toEqual({
      detail: true,
      create: true,
      update: true,
      delete: false,
    });
  });

  it('rejects a descriptor for a different resource or domain', () => {
    const fixture = listFixture();
    fixture.resource.name = 'orders';
    expect(() => parseLegacyResourceListResponse(fixture, goodsEntry())).toThrow(
      'resource identity mismatch',
    );
  });

  it('rejects duplicate and unsafe dynamic columns', () => {
    const duplicate = listFixture();
    const firstDuplicateColumn = duplicate.resource.columns[0];
    if (!firstDuplicateColumn) throw new Error('fixture must contain a column');
    duplicate.resource.columns.push({ ...firstDuplicateColumn });
    expect(() => parseLegacyResourceListResponse(duplicate, goodsEntry())).toThrow(
      'duplicate field id',
    );

    const unsafe = listFixture();
    const firstUnsafeColumn = unsafe.resource.columns[0];
    if (!firstUnsafeColumn) throw new Error('fixture must contain a column');
    firstUnsafeColumn.name = 'id;drop table goods';
    expect(() => parseLegacyResourceListResponse(unsafe, goodsEntry())).toThrow(
      'not a safe field name',
    );
  });

  it('rejects malformed paging and row data', () => {
    expect(() =>
      parseLegacyResourceListResponse({ ...listFixture(), page: 0 }, goodsEntry()),
    ).toThrow('page must be an integer greater than or equal to 1');
    expect(() =>
      parseLegacyResourceListResponse({ ...listFixture(), data: ['not-an-object'] }, goodsEntry()),
    ).toThrow('data must be an array of objects');
  });

  it('accepts raw and data-wrapped detail records', () => {
    expect(parseLegacyRecordResponse({ id: 7 })).toEqual({ id: 7 });
    expect(parseLegacyRecordResponse({ data: { id: 8 } })).toEqual({ id: 8 });
    expect(() => parseLegacyRecordResponse(null)).toThrow(
      'legacy record response must be an object',
    );
  });

  it('requires explicit detail capability and forbids update without detail', () => {
    const missing = listFixture();
    const capabilities = missing.resource.capabilities as Record<string, unknown>;
    delete capabilities.detail;
    expect(() => parseLegacyResourceListResponse(missing, goodsEntry())).toThrow(
      'resource.capabilities.detail must be a boolean',
    );

    const inconsistent = listFixture();
    inconsistent.resource.capabilities.detail = false;
    expect(() => parseLegacyResourceListResponse(inconsistent, goodsEntry())).toThrow(
      'resource.capabilities.update requires detail capability',
    );
  });
});

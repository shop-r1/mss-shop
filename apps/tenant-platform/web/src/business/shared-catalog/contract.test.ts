import { describe, expect, it } from 'vitest';
import { findSharedCatalogResourceByName } from './catalog';
import { parseSharedCatalogListResponse, parseSharedCatalogRecordResponse } from './contract';

function resource(name: 'brands' | 'categories') {
  const entry = findSharedCatalogResourceByName(name);
  if (!entry) throw new Error(`${name} must be in the shared catalog`);
  return entry;
}

function descriptor(name: 'brands' | 'categories', writable = false) {
  return {
    name,
    domain: 'shared-catalog',
    titleKey: `legacy.resources.${name}`,
    columns: [
      {
        name: 'id',
        label: 'legacy.fields.id',
        type: 'STRING',
        writable: false,
        secret: false,
        required: true,
      },
      {
        name: 'name',
        label: 'legacy.fields.name',
        type: 'STRING',
        writable,
        secret: false,
        required: true,
      },
    ],
    capabilities: {
      detail: true,
      create: writable,
      update: writable,
      delete: writable,
    },
  };
}

describe('shared catalog transport contract', () => {
  it('accepts the exact list response and normalizes column types', () => {
    const parsed = parseSharedCatalogListResponse(
      {
        data: [{ id: 'brand-1', name: 'R1' }],
        total: 1,
        page: 1,
        pageSize: 20,
        resource: descriptor('brands'),
      },
      resource('brands'),
    );
    expect(parsed.resource.columns[0]?.type).toBe('string');
    expect(parsed.data).toEqual([{ id: 'brand-1', name: 'R1' }]);
  });

  it('rejects resource, domain, title, unsafe field, and paging drift', () => {
    const base = {
      data: [],
      total: 0,
      page: 1,
      pageSize: 20,
      resource: descriptor('brands'),
    };
    expect(() =>
      parseSharedCatalogListResponse(
        { ...base, resource: { ...base.resource, domain: 'catalog' } },
        resource('brands'),
      ),
    ).toThrow('resource identity mismatch');
    expect(() =>
      parseSharedCatalogListResponse(
        { ...base, resource: { ...base.resource, titleKey: 'wrong.key' } },
        resource('brands'),
      ),
    ).toThrow('resource identity mismatch');

    const unsafe = descriptor('brands');
    const firstColumn = unsafe.columns[0];
    if (!firstColumn) throw new Error('fixture must contain a column');
    firstColumn.name = 'id;drop table brands';
    expect(() =>
      parseSharedCatalogListResponse({ ...base, resource: unsafe }, resource('brands')),
    ).toThrow('not a safe field name');
    expect(() => parseSharedCatalogListResponse({ ...base, page: 0 }, resource('brands'))).toThrow(
      'page must be an integer greater than or equal to 1',
    );
  });

  it('rejects write capabilities advertised for any shared resource', () => {
    const categories = descriptor('categories', false);
    categories.capabilities.update = true;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: categories },
        resource('categories'),
      ),
    ).toThrow('read-only resource categories advertised write capabilities');

    const brands = descriptor('brands', false);
    brands.capabilities.create = true;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: brands },
        resource('brands'),
      ),
    ).toThrow('read-only resource brands advertised write capabilities');
  });

  it('requires explicit detail capability for every single-ID shared resource', () => {
    const brands = descriptor('brands');
    brands.capabilities.detail = false;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: brands },
        resource('brands'),
      ),
    ).toThrow('shared catalog resource brands must support detail');
  });

  it('accepts data-wrapped detail and verifies its resource descriptor', () => {
    expect(
      parseSharedCatalogRecordResponse(
        { data: { id: 'brand-1' }, resource: descriptor('brands') },
        resource('brands'),
      ),
    ).toEqual({ id: 'brand-1' });
    expect(() =>
      parseSharedCatalogRecordResponse(
        { data: { id: 'brand-1' }, resource: descriptor('categories') },
        resource('brands'),
      ),
    ).toThrow('resource identity mismatch');
  });
});

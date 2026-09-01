import { describe, expect, it } from 'vitest';
import { findSharedCatalogResourceByName } from './catalog';
import { parseSharedCatalogListResponse, parseSharedCatalogRecordResponse } from './contract';

function resource(name: 'payments') {
  const entry = findSharedCatalogResourceByName(name);
  if (!entry) throw new Error(`${name} must be in the shared catalog`);
  return entry;
}

function descriptor(name: string, writable = false) {
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
        data: [{ id: 'payment-1', name: 'WeChat' }],
        total: 1,
        page: 1,
        pageSize: 20,
        resource: descriptor('payments'),
      },
      resource('payments'),
    );
    expect(parsed.resource.columns[0]?.type).toBe('string');
    expect(parsed.data).toEqual([{ id: 'payment-1', name: 'WeChat' }]);
  });

  it('rejects resource, domain, title, unsafe field, and paging drift', () => {
    const base = {
      data: [],
      total: 0,
      page: 1,
      pageSize: 20,
      resource: descriptor('payments'),
    };
    expect(() =>
      parseSharedCatalogListResponse(
        { ...base, resource: { ...base.resource, domain: 'catalog' } },
        resource('payments'),
      ),
    ).toThrow('resource identity mismatch');
    expect(() =>
      parseSharedCatalogListResponse(
        { ...base, resource: { ...base.resource, titleKey: 'wrong.key' } },
        resource('payments'),
      ),
    ).toThrow('resource identity mismatch');

    const unsafe = descriptor('payments');
    const firstColumn = unsafe.columns[0];
    if (!firstColumn) throw new Error('fixture must contain a column');
    firstColumn.name = 'id;drop table payments';
    expect(() =>
      parseSharedCatalogListResponse({ ...base, resource: unsafe }, resource('payments')),
    ).toThrow('not a safe field name');
    expect(() => parseSharedCatalogListResponse({ ...base, page: 0 }, resource('payments'))).toThrow(
      'page must be an integer greater than or equal to 1',
    );
  });

  it('rejects write capabilities advertised for any shared resource', () => {
    const payments = descriptor('payments', false);
    payments.capabilities.update = true;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: payments },
        resource('payments'),
      ),
    ).toThrow('read-only resource payments advertised write capabilities');

    payments.capabilities.update = false;
    payments.capabilities.create = true;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: payments },
        resource('payments'),
      ),
    ).toThrow('read-only resource payments advertised write capabilities');
  });

  it('requires explicit detail capability for every single-ID shared resource', () => {
    const payments = descriptor('payments');
    payments.capabilities.detail = false;
    expect(() =>
      parseSharedCatalogListResponse(
        { data: [], total: 0, page: 1, pageSize: 20, resource: payments },
        resource('payments'),
      ),
    ).toThrow('shared catalog resource payments must support detail');
  });

  it('accepts data-wrapped detail and verifies its resource descriptor', () => {
    expect(
      parseSharedCatalogRecordResponse(
        { data: { id: 'payment-1' }, resource: descriptor('payments') },
        resource('payments'),
      ),
    ).toEqual({ id: 'payment-1' });
    expect(() =>
      parseSharedCatalogRecordResponse(
        { data: { id: 'payment-1' }, resource: descriptor('brands') },
        resource('payments'),
      ),
    ).toThrow('resource identity mismatch');
  });
});

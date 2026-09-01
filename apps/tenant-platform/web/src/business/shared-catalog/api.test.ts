import { describe, expect, it, vi } from 'vitest';
import { createSharedCatalogClient, type SharedCatalogRequester } from './api';
import { findSharedCatalogResourceByName, SHARED_CATALOG_RESOURCES } from './catalog';

function payments() {
  const entry = findSharedCatalogResourceByName('payments');
  if (!entry) throw new Error('payments must be in the platform catalog');
  return entry;
}

function descriptor() {
  return {
    name: 'payments',
    domain: 'shared-catalog',
    titleKey: 'legacy.resources.payments',
    columns: [],
    capabilities: { detail: true, create: false, update: false, delete: false },
  };
}

describe('shared catalog API client', () => {
  it('uses the relative Admin API path with page, pageSize, and optional q', async () => {
    const response = {
      data: [],
      total: 0,
      page: 2,
      pageSize: 50,
      resource: descriptor(),
    };
    const requester = vi.fn<SharedCatalogRequester>().mockResolvedValue(response);
    const client = createSharedCatalogClient(requester);
    await expect(client.list(payments(), { page: 2, pageSize: 50, q: 'online' })).resolves.toEqual(
      response,
    );
    expect(requester).toHaveBeenCalledWith('/legacy/resources/payments', {
      method: 'GET',
      params: { page: 2, pageSize: 50, q: 'online' },
      skipErrorHandler: true,
    });
  });

  it('uses encoded detail IDs for read-only record access', async () => {
    const wrapped = { data: { id: 'A/B 1' }, resource: descriptor() };
    const requester = vi.fn<SharedCatalogRequester>().mockResolvedValue(wrapped);
    const client = createSharedCatalogClient(requester);
    const entry = payments();

    await expect(client.detail(entry, 'A/B 1')).resolves.toEqual({
      id: 'A/B 1',
    });
    expect(requester).toHaveBeenCalledExactlyOnceWith('/legacy/resources/payments/A%2FB%201', {
      method: 'GET',
      skipErrorHandler: true,
    });
  });

  it('refuses all mutation requests for every shared resource', async () => {
    const requester = vi.fn<SharedCatalogRequester>();
    const client = createSharedCatalogClient(requester);

    for (const entry of SHARED_CATALOG_RESOURCES) {
      await expect(client.create(entry, {})).rejects.toThrow(
        `shared catalog resource ${entry.resource} is read-only`,
      );
      await expect(client.update(entry, 'one', {})).rejects.toThrow(
        `shared catalog resource ${entry.resource} is read-only`,
      );
      await expect(client.remove(entry, 'one')).rejects.toThrow(
        `shared catalog resource ${entry.resource} is read-only`,
      );
    }
    expect(requester).not.toHaveBeenCalled();
  });
});

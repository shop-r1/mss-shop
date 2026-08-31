import { describe, expect, it, vi } from 'vitest';
import { createLegacyResourceClient, type LegacyRequester } from './api';
import { findLegacyResourceByName } from './catalog';

function goodsEntry() {
  const entry = findLegacyResourceByName('goods');
  if (!entry) throw new Error('goods must be present in the mall legacy catalog');
  return entry;
}

function listResponse() {
  return {
    data: [],
    total: 0,
    page: 2,
    pageSize: 50,
    resource: {
      name: 'goods',
      domain: 'catalog',
      titleKey: 'legacy.resource.goods.title',
      columns: [],
      capabilities: { detail: true, create: true, update: true, delete: true },
    },
  };
}

describe('legacy resource API client', () => {
  it('sends documented list paging and search parameters', async () => {
    const requester = vi.fn<LegacyRequester>().mockResolvedValue(listResponse());
    const client = createLegacyResourceClient(requester);

    await expect(client.list(goodsEntry(), { page: 2, pageSize: 50, q: 'tea' })).resolves.toEqual(
      listResponse(),
    );
    expect(requester).toHaveBeenCalledWith('/legacy/resources/goods', {
      method: 'GET',
      params: { page: 2, pageSize: 50, q: 'tea' },
    });
  });

  it('omits an empty search query', async () => {
    const requester = vi.fn<LegacyRequester>().mockResolvedValue({
      ...listResponse(),
      page: 1,
      pageSize: 20,
    });
    const client = createLegacyResourceClient(requester);
    await client.list(goodsEntry(), { page: 1, pageSize: 20 });
    expect(requester).toHaveBeenCalledWith('/legacy/resources/goods', {
      method: 'GET',
      params: { page: 1, pageSize: 20 },
    });
  });

  it('uses encoded detail IDs and the expected CRUD methods', async () => {
    const requester = vi.fn<LegacyRequester>().mockResolvedValue({ id: 'A/B 1' });
    const client = createLegacyResourceClient(requester);
    const entry = goodsEntry();

    await expect(client.detail(entry, 'A/B 1')).resolves.toEqual({
      id: 'A/B 1',
    });
    await client.create(entry, { name: 'Tea' });
    await client.update(entry, 'A/B 1', { name: 'Coffee' });
    await client.remove(entry, 'A/B 1');

    expect(requester.mock.calls).toEqual([
      ['/legacy/resources/goods/A%2FB%201', { method: 'GET' }],
      ['/legacy/resources/goods', { method: 'POST', data: { name: 'Tea' } }],
      ['/legacy/resources/goods/A%2FB%201', { method: 'PUT', data: { name: 'Coffee' } }],
      ['/legacy/resources/goods/A%2FB%201', { method: 'DELETE' }],
    ]);
  });
});

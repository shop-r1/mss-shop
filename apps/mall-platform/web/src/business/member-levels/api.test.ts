import { describe, expect, it, vi } from 'vitest';
import { createMemberLevelsAPI, type MemberLevelsRequestOptions } from './api';
import { MemberLevelContractError } from './contract';
import type { CreateMemberLevelInput, MemberLevel, MemberLevelPage } from './types';

const revision = 'a'.repeat(64);
const record: MemberLevel = {
  id: '100000000000000001',
  name: 'Standard',
  discountPercent: '10',
  status: 'enabled',
  isDefault: true,
  createdAt: '2026-09-01T00:00:00Z',
  updatedAt: '2026-09-01T00:00:00Z',
  revision,
};
const page: MemberLevelPage = {
  data: [record],
  total: 1,
  current: 1,
  pageSize: 20,
  integrity: {
    flaggedDefaultCount: 1,
    enabledDefaultCount: 1,
    invalidDefaultCount: 0,
  },
  operations: {
    create: false,
    update: false,
    setDefault: false,
    delete: false,
  },
};

describe('member-level API client', () => {
  it('uses only the selected relative resource and encodes identifiers', async () => {
    const client = vi.fn(async (path: string, options: MemberLevelsRequestOptions) => {
      if (path === '/member-levels' && options.method === 'GET') return page;
      if (options.method === 'DELETE') return undefined;
      return record;
    });
    const api = createMemberLevelsAPI(client);

    await expect(api.loadPage({ current: 1, pageSize: 20 })).resolves.toEqual(page);
    expect(client).toHaveBeenLastCalledWith('/member-levels', {
      method: 'GET',
      params: { current: 1, pageSize: 20 },
      skipErrorHandler: true,
    });
    await api.loadOne('level/one');
    expect(client).toHaveBeenLastCalledWith('/member-levels/level%2Fone', {
      method: 'GET',
      skipErrorHandler: true,
    });

    const createInput: CreateMemberLevelInput = {
      name: 'Wholesale',
      discountPercent: '20',
      status: 'enabled',
      paymentPolicySourceLevelId: record.id,
    };
    await api.create(createInput);
    expect(client).toHaveBeenLastCalledWith('/member-levels', {
      method: 'POST',
      data: createInput,
      skipErrorHandler: true,
    });
    await api.update('level/one', {
      name: 'Wholesale',
      discountPercent: '20',
      status: 'disabled',
      revision,
    });
    expect(client).toHaveBeenLastCalledWith('/member-levels/level%2Fone', {
      method: 'PUT',
      data: {
        name: 'Wholesale',
        discountPercent: '20',
        status: 'disabled',
        revision,
      },
      skipErrorHandler: true,
    });
    await api.setDefault('level/one', revision);
    expect(client).toHaveBeenLastCalledWith('/member-levels/level%2Fone/default', {
      method: 'PUT',
      data: { revision },
      skipErrorHandler: true,
    });
    await api.remove('level/one', revision);
    expect(client).toHaveBeenLastCalledWith('/member-levels/level%2Fone', {
      method: 'DELETE',
      data: { revision },
      skipErrorHandler: true,
    });
  });

  it('rejects malformed records, pages, and delete responses at the transport boundary', async () => {
    const malformedRecord = { ...record, payment_ids: ['hidden'] };
    const client = vi.fn(async (path: string, options: MemberLevelsRequestOptions) => {
      if (options.method === 'DELETE') return { deleted: true };
      if (path === '/member-levels') return { ...page, operations: { create: false } };
      return malformedRecord;
    });
    const api = createMemberLevelsAPI(client);

    await expect(api.loadPage({ current: 1, pageSize: 20 })).rejects.toBeInstanceOf(
      MemberLevelContractError,
    );
    await expect(api.loadOne(record.id)).rejects.toBeInstanceOf(MemberLevelContractError);
    await expect(api.remove(record.id, revision)).rejects.toBeInstanceOf(MemberLevelContractError);
  });
});
